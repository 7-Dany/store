-- +goose Up
-- +goose StatementBegin

/*
 * 002_core_functions.sql — Trigger functions and triggers for core auth tables.
 *
 * Covers:
 * fn_set_updated_at — shared utility: keeps updated_at current on every mutation
 * fn_revoke_token_family — RFC 6819 §5.2.2.3 cascade revocation on token reuse
 * fn_end_session_on_token_purge — closes a session when its last token is cleaned up
 * fn_require_auth_method — enforces that every user has at least one login path
 * fn_check_identity_not_last — prevents removing the last auth identity from a password-less user
 * fn_deny_audit_update — makes auth_audit_log immutable
 * fn_deny_created_at_change — makes created_at write-once on key tables
 * fn_deny_purge_log_update — makes account_purge_log append-only
 * fn_check_revoked_after_created — temporal sanity guard for refresh_tokens.revoked_at
 * fn_check_token_family_consistency — validates family membership on insert/update
 *
 * Depends on: 001_core.sql
 */


/* ─────────────────────────────────────────────────────────────
 SHARED UTILITY — updated_at
 ───────────────────────────────────────────────────────────── */

-- Generic BEFORE UPDATE trigger function used by every table that has an updated_at column.
-- Fires before the row is written; sets updated_at to the current transaction timestamp.
-- Shared across all tables to avoid duplicating identical logic.
CREATE OR REPLACE FUNCTION fn_set_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
 NEW.updated_at = NOW();
 RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_set_updated_at() IS
 'Sets updated_at = NOW() on every row update. Shared by all tables with that column.';

-- Keeps users.updated_at current whenever any column is modified.
CREATE TRIGGER trg_users_updated_at
 BEFORE UPDATE ON users
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps user_identities.updated_at current (e.g. when display_name or avatar is refreshed from OAuth).
CREATE TRIGGER trg_user_identities_updated_at
 BEFORE UPDATE ON user_identities
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps refresh_tokens.updated_at current when a token is revoked or its session_id is nulled.
CREATE TRIGGER trg_refresh_tokens_updated_at
 BEFORE UPDATE ON refresh_tokens
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- user_sessions is mutable (ended_at, last_active_at change over time); updated_at must track this.
CREATE TRIGGER trg_user_sessions_updated_at
 BEFORE UPDATE ON user_sessions
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- one_time_tokens is mutable (attempts and used_at are written after creation); updated_at must track this.
CREATE TRIGGER trg_one_time_tokens_updated_at
 BEFORE UPDATE ON one_time_tokens
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- user_secrets is mutable (lock state, attempt counters change); updated_at must track mutations.
CREATE TRIGGER trg_user_secrets_updated_at
 BEFORE UPDATE ON user_secrets
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- user_identity_tokens is mutable (tokens are refreshed on re-login); updated_at must track this.
CREATE TRIGGER trg_user_identity_tokens_updated_at
 BEFORE UPDATE ON user_identity_tokens
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();


/* ─────────────────────────────────────────────────────────────
 REFRESH TOKEN FAMILY REVOCATION (RFC 6819 §5.2.2.3)
 ───────────────────────────────────────────────────────────── */

/*
 * When a refresh token is revoked with a theft-detection reason, this trigger
 * cascades the revocation to all other non-revoked tokens in the same family_id group.
 *
 * Voluntary revocation reasons (logout, session_expired, pre_verification) do NOT
 * trigger the cascade — only reuse-detection revocations do.
 *
 * ══════════════════════════════════════════════════════════════════════════
 * ⚠️  HIGH-3 — DEADLOCK RISK ON CONCURRENT REUSE DETECTION
 * ══════════════════════════════════════════════════════════════════════════
 * This trigger issues an UPDATE on refresh_tokens from within an AFTER trigger
 * on the same table. Two concurrent sessions detecting reuse on the same
 * family_id can deadlock:
 *
 *   Session A: holds lock on token X → trigger fires → UPDATE tries to lock token Y
 *   Session B: holds lock on token Y → trigger fires → UPDATE tries to lock token X
 *   Result: PostgreSQL raises SQLSTATE 40P01 (deadlock_detected)
 *
 * APPLICATION LAYER REQUIREMENTS:
 *   1. Catch SQLSTATE 40P01 as a distinct, named error — not as a generic DB error.
 *   2. Abort the ENTIRE enclosing transaction (not just the failed statement).
 *      Retrying a single statement inside a deadlocked transaction is incorrect.
 *   3. Retry the complete token rotation/revocation operation in a NEW transaction.
 *   4. Emit a warning metric on each deadlock — frequent deadlocks indicate a
 *      problem with concurrent session management.
 *
 * PREFERRED LONG-TERM REMEDIATION:
 *   Move family revocation out of this trigger into an async operation:
 *     1. Commit the single-token revocation with no cascade in the trigger.
 *     2. After commit, enqueue a job (family_id, revoke_reason).
 *     3. The job acquires pg_advisory_xact_lock(hashtext(family_id::text))
 *        before running the batch UPDATE — eliminates the concurrent-trigger
 *        deadlock entirely and makes the revocation observable in the audit log
 *        as a distinct event.
 *   Until that refactor is in place, 40P01 retry at the call site is mandatory.
 * ══════════════════════════════════════════════════════════════════════════
 *
 * Re-entry guard: app.revoking_family session local prevents the cascade UPDATE from
 * re-triggering this function for each sibling row (trigger storm prevention).
 */
CREATE OR REPLACE FUNCTION fn_revoke_token_family()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
 -- Only cascade on theft-detection revocations. Voluntary signals must not cascade.
 -- ⚠️ BRITTLENESS: this whitelist is hardcoded. If new voluntary revocation reasons
 -- are added (e.g. 'device_removed'), this list MUST be updated to include them,
 -- otherwise the cascade will incorrectly fire on voluntary revocations.
 IF OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL
 AND (NEW.revoke_reason IS NULL
 OR NEW.revoke_reason NOT IN ('logout', 'session_expired', 'pre_verification'))
 THEN
 -- Re-entry guard: skip if we are already inside a family cascade for this transaction.
 -- is_local = true means the flag resets automatically at transaction boundary.
 IF current_setting('app.revoking_family', true) = '1' THEN
 RETURN NEW;
 END IF;
 PERFORM set_config('app.revoking_family', '1', true);

 -- Revoke all other live tokens in the same rotation family.
 UPDATE refresh_tokens
 SET revoked_at = NOW(),
 revoke_reason = 'family_revoked:' || COALESCE(NEW.revoke_reason, '')
 WHERE family_id = NEW.family_id
 AND jti != NEW.jti
 AND revoked_at IS NULL;
 END IF;
 RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_revoke_token_family() IS
 'On theft-detection revocation, revokes all tokens sharing the same family_id. '
 'Skips voluntary revocations (logout, session_expired, pre_verification). '
 'HIGH-3: issues UPDATE on refresh_tokens from within an AFTER trigger on the same table. '
 'Concurrent reuse-detection on the same family_id WILL deadlock (SQLSTATE 40P01). '
 'Application MUST catch 40P01 and retry the ENTIRE transaction in a new connection. '
 'Preferred fix: move family revocation to an async job with pg_advisory_xact_lock on family_id.';

-- Fires after revoked_at is set so the cascade UPDATE sees the triggering row already committed.
CREATE TRIGGER trg_revoke_token_family
 AFTER UPDATE OF revoked_at ON refresh_tokens
 FOR EACH ROW EXECUTE FUNCTION fn_revoke_token_family();


/* ─────────────────────────────────────────────────────────────
 SESSION AUTO-END ON TOKEN PURGE
 ───────────────────────────────────────────────────────────── */

/*
 * When a refresh token's session_id is NULLed (because the session row was deleted
 * via ON DELETE SET NULL), this trigger closes the session by setting ended_at.
 *
 * This prevents dangling "open" sessions after a cleanup run or admin purge that
 * deleted the session row but left the token record intact for the audit trail.
 */
CREATE OR REPLACE FUNCTION fn_end_session_on_token_purge()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
 -- session_id is NULLed by ON DELETE SET NULL when the user_sessions row is deleted.
 -- Only act on the NULL transition; skip if both old and new are NULL.
 IF OLD.session_id IS NOT NULL AND NEW.session_id IS NULL THEN
 -- If the session row is truly gone, there is nothing to update.
 IF NOT EXISTS (SELECT 1 FROM user_sessions WHERE id = OLD.session_id) THEN
 RETURN NEW;
 END IF;
 -- Mark the session as ended to avoid leaving it in an apparent "active" state.
 UPDATE user_sessions
 SET ended_at = NOW()
 WHERE id = OLD.session_id
 AND ended_at IS NULL;
 END IF;
 RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_end_session_on_token_purge() IS
 'Closes the linked session when a refresh token loses its session_id, preventing dangling open sessions after a cleanup or admin purge.';

CREATE TRIGGER trg_end_session_on_token_purge
 AFTER UPDATE OF session_id ON refresh_tokens
 FOR EACH ROW EXECUTE FUNCTION fn_end_session_on_token_purge();


/* ─────────────────────────────────────────────────────────────
 ACCOUNT INTEGRITY — AUTH METHOD REQUIREMENT
 ───────────────────────────────────────────────────────────── */

/*
 * Every users row must have at least one way to authenticate: either a password_hash
 * in user_secrets, or at least one row in user_identities (OAuth).
 *
 * Without this guard a user could be created with no authentication path at all,
 * which would make the account permanently inaccessible.
 *
 * DEFERRABLE INITIALLY DEFERRED: the OAuth identity INSERT (and user_secrets INSERT)
 * can follow the users INSERT within the same transaction without triggering a false
 * violation, because the trigger fires only at COMMIT time.
 */
CREATE OR REPLACE FUNCTION fn_require_auth_method()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
 -- Check whether the user has a password stored in user_secrets.
 IF NOT EXISTS (
 SELECT 1 FROM user_secrets WHERE user_id = NEW.id AND password_hash IS NOT NULL LIMIT 1
 ) THEN
 -- No password — require at least one OAuth identity row instead.
 IF NOT EXISTS (
 SELECT 1 FROM user_identities WHERE user_id = NEW.id LIMIT 1
 ) THEN
 RAISE EXCEPTION
 'users.% has no authentication method: password_hash is NULL in user_secrets and no user_identities row exists.',
 NEW.id
 USING ERRCODE = 'P0001';
 END IF;
 END IF;
 RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_require_auth_method() IS
 'Rejects any users row with no password_hash (in user_secrets) and no user_identities row. DEFERRABLE so the OAuth identity can be inserted in the same transaction as the user.';

-- DEFERRABLE INITIALLY DEFERRED: identity and secrets rows can be inserted after the users row
-- within the same transaction without triggering this check prematurely.
CREATE CONSTRAINT TRIGGER trg_require_auth_method
 AFTER INSERT ON users
 DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION fn_require_auth_method();


/* ─────────────────────────────────────────────────────────────
 IDENTITY DELETE GUARD
 ───────────────────────────────────────────────────────────── */

/*
 * Prevents deletion of the last user_identities row for a user who has no password,
 * which would leave them with no authentication path.
 *
 * trg_require_auth_method only fires on users INSERT/UPDATE, so this guard is needed
 * to catch the DELETE path on user_identities.
 *
 * DEFERRABLE INITIALLY DEFERRED: supports removing one identity and adding another
 * within the same transaction without tripping this check in between.
 */
CREATE OR REPLACE FUNCTION fn_check_identity_not_last()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
 -- If the parent user is being cascade-deleted, allow the identity deletion unconditionally.
 IF NOT EXISTS (SELECT 1 FROM users WHERE id = OLD.user_id) THEN
 RETURN OLD;
 END IF;

 -- User has a password — they retain an auth path after losing this identity.
 IF EXISTS (SELECT 1 FROM user_secrets WHERE user_id = OLD.user_id AND password_hash IS NOT NULL) THEN
 RETURN OLD;
 END IF;

 -- No password and this is the last identity — deletion would strand the account.
 IF NOT EXISTS (
 SELECT 1 FROM user_identities WHERE user_id = OLD.user_id AND id != OLD.id
 ) THEN
 RAISE EXCEPTION
 'Cannot delete the last authentication identity for user % who has no password_hash.',
 OLD.user_id
 USING ERRCODE = 'P0001';
 END IF;

 RETURN OLD;
END;
$fn$;

COMMENT ON FUNCTION fn_check_identity_not_last() IS
 'Prevents deletion of the last user_identities row for a password-less user, closing the gap left by trg_require_auth_method which only fires on users INSERT/UPDATE.';

CREATE CONSTRAINT TRIGGER trg_prevent_orphan_on_identity_delete
 AFTER DELETE ON user_identities
 DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION fn_check_identity_not_last();


/* ─────────────────────────────────────────────────────────────
 AUDIT LOG — IMMUTABILITY
 ───────────────────────────────────────────────────────────── */

/*
 * auth_audit_log rows must never be modified after they are written.
 * This trigger rejects any UPDATE, regardless of which columns are targeted.
 *
 * DELETEs are permitted: rows are removed by CASCADE when a user is hard-purged
 * (but user_id is SET NULL first, so the event is actually retained) and by the
 * periodic retention sweep.
 */
CREATE OR REPLACE FUNCTION fn_deny_audit_update()
RETURNS TRIGGER LANGUAGE plpgsql AS $fn$
BEGIN
 RAISE EXCEPTION 'auth_audit_log is immutable; UPDATE is not permitted'
 USING ERRCODE = 'P0001';
 RETURN NULL;
END;
$fn$;

COMMENT ON FUNCTION fn_deny_audit_update() IS
 'Raises an exception on any UPDATE against auth_audit_log, preventing rewriting of event history. DELETEs are permitted for user account deletion (ON DELETE CASCADE) and periodic retention sweeps.';

CREATE TRIGGER trg_deny_audit_update
 BEFORE UPDATE ON auth_audit_log
 FOR EACH ROW EXECUTE FUNCTION fn_deny_audit_update();

-- Belt-and-suspenders: revoke the UPDATE privilege at the role level as well.
REVOKE UPDATE ON auth_audit_log FROM PUBLIC;


/* ─────────────────────────────────────────────────────────────
 CREATED_AT IMMUTABILITY
 ───────────────────────────────────────────────────────────── */

/*
 * created_at is a write-once timestamp on several tables. This function rejects
 * any UPDATE that modifies it, preventing accidental or malicious backdating.
 *
 * account_purge_log is fully append-only — all UPDATEs are blocked by a separate
 * function (fn_deny_purge_log_update) to give a clearer error message.
 */
CREATE OR REPLACE FUNCTION fn_deny_created_at_change()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.created_at IS DISTINCT FROM NEW.created_at THEN
 RAISE EXCEPTION '%.created_at is immutable and cannot be changed.', TG_TABLE_NAME
 USING ERRCODE = 'P0001';
 END IF;
 RETURN NEW;
END;
$$;

COMMENT ON FUNCTION fn_deny_created_at_change() IS
 'Rejects any UPDATE that modifies created_at. Wired to tables where created_at is semantically write-once.';

-- Prevents backdating user account creation timestamps.
CREATE TRIGGER trg_users_deny_created_at_change
 BEFORE UPDATE OF created_at ON users
 FOR EACH ROW EXECUTE FUNCTION fn_deny_created_at_change();

-- user_secrets.created_at is semantically write-once; rejects any attempt to change it.
CREATE TRIGGER trg_user_secrets_deny_created_at_change
 BEFORE UPDATE OF created_at ON user_secrets
 FOR EACH ROW EXECUTE FUNCTION fn_deny_created_at_change();

-- one_time_tokens.created_at is used in TTL cap constraints; mutation would bypass them.
CREATE TRIGGER trg_one_time_tokens_deny_created_at_change
 BEFORE UPDATE OF created_at ON one_time_tokens
 FOR EACH ROW EXECUTE FUNCTION fn_deny_created_at_change();

-- refresh_tokens.created_at is used in the revoked_at temporal guard; mutation would bypass it.
CREATE TRIGGER trg_refresh_tokens_deny_created_at_change
 BEFORE UPDATE OF created_at ON refresh_tokens
 FOR EACH ROW EXECUTE FUNCTION fn_deny_created_at_change();

-- account_purge_log is fully append-only; block all UPDATEs entirely.
CREATE OR REPLACE FUNCTION fn_deny_purge_log_update()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'account_purge_log is append-only; UPDATE is not permitted.'
 USING ERRCODE = 'P0001';
 RETURN NULL;
END;
$$;

COMMENT ON FUNCTION fn_deny_purge_log_update() IS
 'Blocks all UPDATEs on account_purge_log. Rows are append-only compliance records.';

CREATE TRIGGER trg_deny_purge_log_update
 BEFORE UPDATE ON account_purge_log
 FOR EACH ROW EXECUTE FUNCTION fn_deny_purge_log_update();


/* ─────────────────────────────────────────────────────────────
 REVOKED_AT TEMPORAL GUARD
 ───────────────────────────────────────────────────────────── */

/*
 * Ensures refresh_tokens.revoked_at cannot be set to a timestamp earlier than
 * the token's own created_at, which would produce nonsensical audit timelines.
 *
 * Implemented as a trigger (not a CHECK constraint) because a CHECK would evaluate
 * NOW() at constraint-definition time and could reject valid pre-revoked sibling
 * tokens inserted in the same transaction where created_at defaults to NOW().
 *
 * Only fires on the NULL → non-NULL transition (first revocation) to avoid
 * re-validating already-revoked tokens during unrelated updates.
 */
CREATE OR REPLACE FUNCTION fn_check_revoked_after_created()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
 IF OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL THEN
 IF NEW.revoked_at < NEW.created_at THEN
 RAISE EXCEPTION
 'refresh_tokens.revoked_at (%) must not precede created_at (%) for jti %',
 NEW.revoked_at, NEW.created_at, NEW.jti
 USING ERRCODE = 'P0001';
 END IF;
 END IF;
 RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_check_revoked_after_created() IS
 'Rejects any UPDATE that sets revoked_at earlier than created_at. Fires only on the NULL → non-NULL transition.';

CREATE TRIGGER trg_check_revoked_after_created
 BEFORE UPDATE OF revoked_at ON refresh_tokens
 FOR EACH ROW EXECUTE FUNCTION fn_check_revoked_after_created();


/* ─────────────────────────────────────────────────────────────
 TOKEN FAMILY CONSISTENCY
 ───────────────────────────────────────────────────────────── */

/*
 * Enforces two invariants:
 *
 * INSERT: a child token (parent_jti IS NOT NULL) must share its parent's family_id.
 * A mismatch would silently break fn_revoke_token_family cascade logic.
 *
 * UPDATE OF family_id on root tokens: blocked entirely. Changing a root token's
 * family_id would silently remap the entire rotation chain, breaking reuse detection.
 * On child tokens, the new family_id must still match the parent.
 */
CREATE OR REPLACE FUNCTION fn_check_token_family_consistency()
RETURNS TRIGGER LANGUAGE plpgsql AS $fn$
BEGIN
 IF TG_OP = 'INSERT' THEN
 -- A child token must share its parent's family_id; a mismatch silently breaks cascades.
 IF NEW.parent_jti IS NOT NULL
 AND NOT EXISTS (
 SELECT 1 FROM refresh_tokens
 WHERE jti = NEW.parent_jti AND family_id = NEW.family_id
 )
 THEN
 RAISE EXCEPTION
 'refresh_tokens: family_id % does not match parent_jti % family',
 NEW.family_id, NEW.parent_jti
 USING ERRCODE = 'P0001';
 END IF;

 ELSIF TG_OP = 'UPDATE' THEN
 -- Root-token family_id is immutable. Mutation would silently remap the entire chain.
 IF OLD.parent_jti IS NULL THEN
 RAISE EXCEPTION
 'refresh_tokens: family_id on root token (jti=%) is immutable.',
 OLD.jti
 USING ERRCODE = 'P0001';
 END IF;

 -- For child tokens, the new family_id must still agree with the parent's family_id.
 IF NOT EXISTS (
 SELECT 1 FROM refresh_tokens
 WHERE jti = OLD.parent_jti AND family_id = NEW.family_id
 ) THEN
 RAISE EXCEPTION
 'refresh_tokens: family_id % does not match parent_jti % family',
 NEW.family_id, OLD.parent_jti
 USING ERRCODE = 'P0001';
 END IF;
 END IF;

 RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_check_token_family_consistency() IS
 'INSERT: ensures a child token shares its parent''s family_id. '
 'UPDATE: blocks family_id mutation on root tokens (immutable) and re-validates child tokens. '
 'A mismatch would silently break reuse-detection cascades in fn_revoke_token_family.';

-- Validates family membership on every new child token INSERT.
CREATE TRIGGER trg_check_token_family_consistency
 BEFORE INSERT ON refresh_tokens
 FOR EACH ROW EXECUTE FUNCTION fn_check_token_family_consistency();

-- Prevents family_id mutation on existing tokens via direct DB writes or admin tools.
CREATE TRIGGER trg_prevent_family_id_change
 BEFORE UPDATE OF family_id ON refresh_tokens
 FOR EACH ROW EXECUTE FUNCTION fn_check_token_family_consistency();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_prevent_family_id_change ON refresh_tokens;
DROP TRIGGER IF EXISTS trg_check_token_family_consistency ON refresh_tokens;
DROP TRIGGER IF EXISTS trg_check_revoked_after_created ON refresh_tokens;
DROP TRIGGER IF EXISTS trg_deny_purge_log_update ON account_purge_log;
DROP TRIGGER IF EXISTS trg_refresh_tokens_deny_created_at_change ON refresh_tokens;
DROP TRIGGER IF EXISTS trg_one_time_tokens_deny_created_at_change ON one_time_tokens;
DROP TRIGGER IF EXISTS trg_users_deny_created_at_change ON users;
DROP FUNCTION IF EXISTS fn_deny_purge_log_update() CASCADE;
DROP FUNCTION IF EXISTS fn_deny_created_at_change() CASCADE;
DROP TRIGGER IF EXISTS trg_deny_audit_update ON auth_audit_log;
DROP TRIGGER IF EXISTS trg_prevent_orphan_on_identity_delete ON user_identities;
DROP TRIGGER IF EXISTS trg_require_auth_method ON users;
DROP TRIGGER IF EXISTS trg_user_identity_tokens_updated_at ON user_identity_tokens;
DROP TRIGGER IF EXISTS trg_user_secrets_deny_created_at_change ON user_secrets;
DROP TRIGGER IF EXISTS trg_user_secrets_updated_at ON user_secrets;
DROP TRIGGER IF EXISTS trg_one_time_tokens_updated_at ON one_time_tokens;
DROP TRIGGER IF EXISTS trg_user_sessions_updated_at ON user_sessions;
DROP TRIGGER IF EXISTS trg_refresh_tokens_updated_at ON refresh_tokens;
DROP TRIGGER IF EXISTS trg_user_identities_updated_at ON user_identities;
DROP TRIGGER IF EXISTS trg_users_updated_at ON users;
DROP TRIGGER IF EXISTS trg_end_session_on_token_purge ON refresh_tokens;
DROP TRIGGER IF EXISTS trg_revoke_token_family ON refresh_tokens;

DROP FUNCTION IF EXISTS fn_check_token_family_consistency();
DROP FUNCTION IF EXISTS fn_check_revoked_after_created();
DROP FUNCTION IF EXISTS fn_deny_audit_update();
DROP FUNCTION IF EXISTS fn_check_identity_not_last();
DROP FUNCTION IF EXISTS fn_require_auth_method();
DROP FUNCTION IF EXISTS fn_end_session_on_token_purge();
DROP FUNCTION IF EXISTS fn_revoke_token_family();
DROP FUNCTION IF EXISTS fn_set_updated_at() CASCADE;

-- +goose StatementEnd
