-- +goose Up
-- +goose StatementBegin

-- 002_core_functions.sql — trigger functions and triggers for the core auth tables.
-- Depends on: 001_core.sql


-- ------------------------------------------------------------
-- SHARED UTILITY
-- ------------------------------------------------------------

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

CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

CREATE TRIGGER trg_user_identities_updated_at
    BEFORE UPDATE ON user_identities
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

CREATE TRIGGER trg_refresh_tokens_updated_at
    BEFORE UPDATE ON refresh_tokens
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();


-- ------------------------------------------------------------
-- REFRESH TOKEN FAMILY REVOCATION (RFC 6819 §5.2.2.3)
-- ------------------------------------------------------------

CREATE OR REPLACE FUNCTION fn_revoke_token_family()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
    -- Only cascade on theft-detection revocations. Voluntary signals (logout,
    -- session_expired, pre_verification) must not cascade to the whole family.
    IF OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL
       AND (NEW.revoke_reason IS NULL
            OR NEW.revoke_reason NOT IN ('logout', 'session_expired', 'pre_verification'))
    THEN
        -- Re-entry guard: prevents a trigger storm when the UPDATE below fires this
        -- same trigger for each sibling. is_local = true resets at transaction boundary.
        IF current_setting('app.revoking_family', true) = '1' THEN
            RETURN NEW;
        END IF;
        PERFORM set_config('app.revoking_family', '1', true);

        UPDATE refresh_tokens
           SET revoked_at    = NOW(),
               revoke_reason = 'family_revoked:' || COALESCE(NEW.revoke_reason, '')
         WHERE family_id  = NEW.family_id
           AND jti        != NEW.jti
           AND revoked_at IS NULL;
    END IF;
    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_revoke_token_family() IS
    'On theft-detection revocation, revokes all tokens sharing the same family_id. Skips voluntary revocations (logout, session_expired, pre_verification).';

CREATE TRIGGER trg_revoke_token_family
    AFTER UPDATE OF revoked_at ON refresh_tokens
    FOR EACH ROW EXECUTE FUNCTION fn_revoke_token_family();


-- ------------------------------------------------------------
-- SESSION AUTO-END ON TOKEN PURGE
-- ------------------------------------------------------------

CREATE OR REPLACE FUNCTION fn_end_session_on_token_purge()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
    -- session_id is NULLed by ON DELETE SET NULL when the user_sessions row is deleted.
    -- If the session is already gone this UPDATE is a no-op.
    IF OLD.session_id IS NOT NULL AND NEW.session_id IS NULL THEN
        IF NOT EXISTS (SELECT 1 FROM user_sessions WHERE id = OLD.session_id) THEN
            RETURN NEW;
        END IF;
        UPDATE user_sessions
           SET ended_at = NOW()
         WHERE id       = OLD.session_id
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


-- ------------------------------------------------------------
-- ACCOUNT INTEGRITY — AUTH METHOD REQUIREMENT
-- ------------------------------------------------------------

CREATE OR REPLACE FUNCTION fn_require_auth_method()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
    -- OAuth-only users have no password_hash; they must have at least one identity row.
    -- Without this a user could be created with no way to authenticate.
    IF NEW.password_hash IS NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM user_identities WHERE user_id = NEW.id LIMIT 1
        ) THEN
            RAISE EXCEPTION
                'users.% has no authentication method: password_hash is NULL and no user_identities row exists.',
                NEW.id
                USING ERRCODE = 'P0001';
        END IF;
    END IF;
    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_require_auth_method() IS
    'Rejects any users row with no password_hash and no user_identities row. DEFERRABLE so the OAuth identity can be inserted in the same transaction as the user.';

-- DEFERRABLE INITIALLY DEFERRED so the identity INSERT can follow the users INSERT
-- within a single transaction without triggering a false violation.
CREATE CONSTRAINT TRIGGER trg_require_auth_method
    AFTER INSERT OR UPDATE OF password_hash ON users
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION fn_require_auth_method();


-- ------------------------------------------------------------
-- IDENTITY DELETE GUARD
-- ------------------------------------------------------------

CREATE OR REPLACE FUNCTION fn_check_identity_not_last()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
    -- If the parent user is already being deleted (CASCADE), allow the identity deletion.
    IF NOT EXISTS (SELECT 1 FROM users WHERE id = OLD.user_id) THEN
        RETURN OLD;
    END IF;

    -- User has a password — they retain an auth path without this identity.
    IF EXISTS (SELECT 1 FROM users WHERE id = OLD.user_id AND password_hash IS NOT NULL) THEN
        RETURN OLD;
    END IF;

    -- No password and last identity — deletion would leave the user with no auth path.
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


-- ------------------------------------------------------------
-- AUDIT LOG — IMMUTABILITY ENFORCEMENT
-- ------------------------------------------------------------
-- Rows may be DELETED (by CASCADE on user deletion or by retention sweeps).
-- Rows may NOT be UPDATED — event history must never be rewritten.

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

REVOKE UPDATE ON auth_audit_log FROM PUBLIC;


-- ------------------------------------------------------------
-- REVOKED_AT TEMPORAL GUARD
-- ------------------------------------------------------------

CREATE OR REPLACE FUNCTION fn_check_revoked_after_created()
RETURNS TRIGGER
LANGUAGE plpgsql AS $fn$
BEGIN
    -- Only enforce on the NULL → non-NULL transition (first revocation).
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


-- ------------------------------------------------------------
-- TOKEN FAMILY CONSISTENCY
-- ------------------------------------------------------------

CREATE OR REPLACE FUNCTION fn_check_token_family_consistency()
RETURNS TRIGGER LANGUAGE plpgsql AS $fn$
BEGIN
    -- A child token must share its parent's family_id; a mismatch would silently
    -- break reuse-detection cascades in fn_revoke_token_family.
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
    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION fn_check_token_family_consistency() IS
    'Ensures a child refresh token shares its parent''s family_id. A mismatch would silently break reuse-detection cascades.';

-- Validates family membership on new child token creation.
CREATE TRIGGER trg_check_token_family_consistency
    BEFORE INSERT ON refresh_tokens
    FOR EACH ROW EXECUTE FUNCTION fn_check_token_family_consistency();

-- Prevents family_id mutation on existing tokens via direct DB writes.
CREATE TRIGGER trg_prevent_family_id_change
    BEFORE UPDATE OF family_id ON refresh_tokens
    FOR EACH ROW EXECUTE FUNCTION fn_check_token_family_consistency();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_prevent_family_id_change             ON refresh_tokens;
DROP TRIGGER IF EXISTS trg_check_token_family_consistency       ON refresh_tokens;
DROP TRIGGER IF EXISTS trg_check_revoked_after_created          ON refresh_tokens;
DROP TRIGGER IF EXISTS trg_deny_audit_update                    ON auth_audit_log;
DROP TRIGGER IF EXISTS trg_prevent_orphan_on_identity_delete    ON user_identities;
DROP TRIGGER IF EXISTS trg_require_auth_method                  ON users;
DROP TRIGGER IF EXISTS trg_refresh_tokens_updated_at            ON refresh_tokens;
DROP TRIGGER IF EXISTS trg_user_identities_updated_at           ON user_identities;
DROP TRIGGER IF EXISTS trg_users_updated_at                     ON users;
DROP TRIGGER IF EXISTS trg_end_session_on_token_purge           ON refresh_tokens;
DROP TRIGGER IF EXISTS trg_revoke_token_family                  ON refresh_tokens;

DROP FUNCTION IF EXISTS fn_check_token_family_consistency();
DROP FUNCTION IF EXISTS fn_check_revoked_after_created();
DROP FUNCTION IF EXISTS fn_deny_audit_update();
DROP FUNCTION IF EXISTS fn_check_identity_not_last();
DROP FUNCTION IF EXISTS fn_require_auth_method();
DROP FUNCTION IF EXISTS fn_end_session_on_token_purge();
DROP FUNCTION IF EXISTS fn_revoke_token_family();
DROP FUNCTION IF EXISTS fn_set_updated_at();

-- +goose StatementEnd
