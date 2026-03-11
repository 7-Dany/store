-- +goose Up
-- +goose StatementBegin

/*
 * 004_rbac_functions.sql — Trigger functions and triggers for RBAC integrity and audit.
 *
 * Covers:
 * updated_at triggers — keeps updated_at current on all RBAC tables
 * fn_audit_role_permissions — immutable audit trail for role_permissions mutations
 * fn_audit_user_roles — immutable audit trail for user_roles mutations
 * fn_audit_user_permissions — immutable audit trail for user_permissions mutations (highest-risk)
 * fn_prevent_privilege_escalation — prevents granting permissions the granter doesn't hold
 * fn_validate_user_permission_expiry — enforces expires_at bounds on direct grants
 * fn_prevent_orphaned_owner — ensures at least one active owner always exists
 * fn_prevent_owner_role_escalation — only owners can grant the owner role
 * fn_validate_user_role_expiry — enforces minimum lead time on temporary role grants
 * fn_audit_permission_request_approvers — audit trail for approval chain changes
 * RBAC audit table immutability triggers
 *
 * Depends on: 002_core_functions.sql (fn_set_updated_at), 003_rbac.sql
 */


/* ─────────────────────────────────────────────────────────────
 updated_at — RBAC TABLES
 ───────────────────────────────────────────────────────────── */

-- Keeps roles.updated_at current when name, description, or is_active changes.
CREATE TRIGGER trg_roles_updated_at
 BEFORE UPDATE ON roles
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps permissions.updated_at current when description or is_active changes.
CREATE TRIGGER trg_permissions_updated_at
 BEFORE UPDATE ON permissions
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps permission_groups.updated_at current when display metadata changes.
CREATE TRIGGER trg_permission_groups_updated_at
 BEFORE UPDATE ON permission_groups
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps permission_condition_templates.updated_at current when validation rules change.
CREATE TRIGGER trg_pct_updated_at
 BEFORE UPDATE ON permission_condition_templates
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps role_permissions.updated_at current when access_type, scope, or conditions change.
CREATE TRIGGER trg_role_permissions_updated_at
 BEFORE UPDATE ON role_permissions
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps user_roles.updated_at current when role_id or expires_at changes.
CREATE TRIGGER trg_user_roles_updated_at
 BEFORE UPDATE ON user_roles
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps user_permissions.updated_at current when conditions or expires_at changes.
CREATE TRIGGER trg_user_permissions_updated_at
 BEFORE UPDATE ON user_permissions
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();


/* ─────────────────────────────────────────────────────────────
 AUDIT LOG POPULATION
 ───────────────────────────────────────────────────────────── */

/*
 * Why triggers rather than application code:
 * Application-layer audit can be silently bypassed by a direct DB write, a race
 * condition, or a missed code path. These trigger functions fire unconditionally
 * on every mutation regardless of which code path triggered the change.
 *
 * changed_by strategy:
 * INSERT/UPDATE: use the row's own granted_by field — self-contained.
 * DELETE: the deleting actor is not visible from the row alone (granted_by
 * is the original granter, not the person doing the deletion).
 * The app must SET LOCAL rbac.acting_user = '<uuid>' before the DELETE.
 * Falls back to granted_by when unset so seeds and admin scripts work.
 *
 * change_reason is read from SET LOCAL rbac.change_reason when set by the app; NULL otherwise.
 */
CREATE OR REPLACE FUNCTION fn_audit_role_permissions()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
 v_change_reason TEXT;
 v_changed_by UUID;
BEGIN
 v_change_reason := current_setting('rbac.change_reason', TRUE);

 IF TG_OP = 'DELETE' THEN
 -- On DELETE the actor is whoever ran the DELETE, not the original granter.
 -- Application must SET LOCAL rbac.acting_user = '<uuid>' before deleting.
 -- Falls back to OLD.granted_by for seeds/admin scripts that omit the variable.
 v_changed_by := COALESCE(
 NULLIF(current_setting('rbac.acting_user', TRUE), '')::UUID,
 OLD.granted_by
 );
 INSERT INTO role_permissions_audit (
 role_id, permission_id,
 conditions, access_type, scope,
 previous_conditions, previous_access_type, previous_scope,
 change_type, changed_by, change_reason
 ) VALUES (
 OLD.role_id, OLD.permission_id,
 NULL, NULL, NULL,
 OLD.conditions, OLD.access_type, OLD.scope,
 'deleted', v_changed_by, v_change_reason
 );
 RETURN OLD;

 ELSIF TG_OP = 'UPDATE' THEN
 -- UPDATE actor may differ from the original granter; read rbac.acting_user first.
 v_changed_by := COALESCE(
 NULLIF(current_setting('rbac.acting_user', TRUE), '')::UUID,
 NEW.granted_by
 );
 INSERT INTO role_permissions_audit (
 role_id, permission_id,
 conditions, access_type, scope,
 previous_conditions, previous_access_type, previous_scope,
 change_type, changed_by, change_reason
 ) VALUES (
 NEW.role_id, NEW.permission_id,
 NEW.conditions, NEW.access_type, NEW.scope,
 OLD.conditions, OLD.access_type, OLD.scope,
 'updated', v_changed_by, v_change_reason
 );
 RETURN NEW;

 ELSE -- INSERT
 INSERT INTO role_permissions_audit (
 role_id, permission_id,
 conditions, access_type, scope,
 previous_conditions, previous_access_type, previous_scope,
 change_type, changed_by, change_reason
 ) VALUES (
 NEW.role_id, NEW.permission_id,
 NEW.conditions, NEW.access_type, NEW.scope,
 NULL, NULL, NULL,
 'created', NEW.granted_by, v_change_reason
 );
 RETURN NEW;
 END IF;
END;
$$;

COMMENT ON FUNCTION fn_audit_role_permissions() IS
 'Writes an immutable record to role_permissions_audit on every INSERT/UPDATE/DELETE. '
 'UPDATE and DELETE changed_by reads rbac.acting_user session variable '
 '(falls back to granted_by) so the actor is the person making the change, not the original granter. '
 'Snapshots access_type and scope before/after each mutation.';

CREATE TRIGGER trg_audit_role_permissions
 AFTER INSERT OR UPDATE OR DELETE ON role_permissions
 FOR EACH ROW EXECUTE FUNCTION fn_audit_role_permissions();


CREATE OR REPLACE FUNCTION fn_audit_user_roles()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
 v_change_reason TEXT;
 v_changed_by UUID;
BEGIN
 v_change_reason := current_setting('rbac.change_reason', TRUE);

 IF TG_OP = 'DELETE' THEN
 v_changed_by := COALESCE(
 NULLIF(current_setting('rbac.acting_user', TRUE), '')::UUID,
 OLD.granted_by
 );

 -- Orphan guard: skip the audit INSERT when the parent users row no longer
 -- exists. Two scenarios trigger this:
 -- a) Sequential cleanup in one transaction: user_roles is deleted explicitly
 -- first, then users is deleted; the ON DELETE CASCADE would re-fire this
 -- trigger after the users row is already gone.
 -- b) Orphaned user_roles rows left by a previous aborted cleanup that
 -- deleted users but not user_roles.
 -- Without this guard the INSERT violates fk_ur_audit_user (RESTRICT).
 -- The deletion still proceeds; only this audit record is omitted.
 IF NOT EXISTS (SELECT 1 FROM users WHERE id = OLD.user_id) THEN
 RETURN OLD;
 END IF;

 INSERT INTO user_roles_audit (
 user_id, role_id, previous_role_id,
 expires_at, previous_expires_at,
 change_type, changed_by, change_reason
 ) VALUES (
 OLD.user_id, OLD.role_id,
 OLD.role_id, -- preserve which role was revoked, not NULL
 NULL, OLD.expires_at,
 'deleted', v_changed_by, v_change_reason
 );
 RETURN OLD;

 ELSIF TG_OP = 'UPDATE' THEN
 -- UPDATE actor may differ from the original granter.
 v_changed_by := COALESCE(
 NULLIF(current_setting('rbac.acting_user', TRUE), '')::UUID,
 NEW.granted_by
 );
 INSERT INTO user_roles_audit (
 user_id, role_id, previous_role_id,
 expires_at, previous_expires_at,
 change_type, changed_by, change_reason
 ) VALUES (
 NEW.user_id, NEW.role_id, OLD.role_id,
 NEW.expires_at, OLD.expires_at,
 'updated', v_changed_by, v_change_reason
 );
 RETURN NEW;

 ELSE -- INSERT
 INSERT INTO user_roles_audit (
 user_id, role_id, previous_role_id,
 expires_at, previous_expires_at,
 change_type, changed_by, change_reason
 ) VALUES (
 NEW.user_id, NEW.role_id, NULL,
 NEW.expires_at, NULL,
 'created', NEW.granted_by, v_change_reason
 );
 RETURN NEW;
 END IF;
END;
$$;

COMMENT ON FUNCTION fn_audit_user_roles() IS
 'Writes an immutable record to user_roles_audit on every INSERT/UPDATE/DELETE. '
 'UPDATE and DELETE changed_by reads rbac.acting_user (falls back to granted_by). '
 'On DELETE, previous_role_id captures OLD.role_id so the revoked role is preserved. '
 'Orphan guard: skips the audit INSERT when the parent users row no longer exists, '
 'preventing fk_ur_audit_user (RESTRICT) violations during e2e cleanup.';

CREATE TRIGGER trg_audit_user_roles
 AFTER INSERT OR UPDATE OR DELETE ON user_roles
 FOR EACH ROW EXECUTE FUNCTION fn_audit_user_roles();


CREATE OR REPLACE FUNCTION fn_audit_user_permissions()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
 v_change_reason TEXT;
 v_changed_by UUID;
BEGIN
 v_change_reason := current_setting('rbac.change_reason', TRUE);

 IF TG_OP = 'DELETE' THEN
 v_changed_by := COALESCE(
 NULLIF(current_setting('rbac.acting_user', TRUE), '')::UUID,
 OLD.granted_by
 );

 -- Orphan guard: same rationale as fn_audit_user_roles.
 -- Skip the audit INSERT when the parent users row no longer exists.
 IF NOT EXISTS (SELECT 1 FROM users WHERE id = OLD.user_id) THEN
 RETURN OLD;
 END IF;

 INSERT INTO user_permissions_audit (
 user_id, permission_id,
 conditions, scope,
 previous_conditions, previous_scope,
 expires_at, previous_expires_at,
 change_type, changed_by, change_reason
 ) VALUES (
 OLD.user_id, OLD.permission_id,
 NULL, NULL,
 OLD.conditions, OLD.scope,
 NULL, OLD.expires_at,
 'deleted', v_changed_by, v_change_reason
 );
 RETURN OLD;

 ELSIF TG_OP = 'UPDATE' THEN
 -- UPDATE actor may differ from the original granter.
 v_changed_by := COALESCE(
 NULLIF(current_setting('rbac.acting_user', TRUE), '')::UUID,
 NEW.granted_by
 );
 INSERT INTO user_permissions_audit (
 user_id, permission_id,
 conditions, scope,
 previous_conditions, previous_scope,
 expires_at, previous_expires_at,
 change_type, changed_by, change_reason
 ) VALUES (
 NEW.user_id, NEW.permission_id,
 NEW.conditions, NEW.scope,
 OLD.conditions, OLD.scope,
 NEW.expires_at, OLD.expires_at,
 'updated', v_changed_by, v_change_reason
 );
 RETURN NEW;

 ELSE -- INSERT
 INSERT INTO user_permissions_audit (
 user_id, permission_id,
 conditions, scope,
 previous_conditions, previous_scope,
 expires_at, previous_expires_at,
 change_type, changed_by, change_reason
 ) VALUES (
 NEW.user_id, NEW.permission_id,
 NEW.conditions, NEW.scope,
 NULL, NULL,
 NEW.expires_at, NULL,
 'created', NEW.granted_by, v_change_reason
 );
 RETURN NEW;
 END IF;
END;
$$;

COMMENT ON FUNCTION fn_audit_user_permissions() IS
 'Writes an immutable record to user_permissions_audit on every INSERT/UPDATE/DELETE. '
 'UPDATE and DELETE changed_by reads rbac.acting_user (falls back to granted_by). '
 'user_permissions is the highest-risk RBAC table — every mutation is tracked unconditionally. '
 'Snapshots scope before/after each mutation. '
 'Orphan guard: skips the audit INSERT when the parent users row no longer exists, '
 'preventing fk_up_audit_user (RESTRICT) violations during e2e cleanup.';

CREATE TRIGGER trg_audit_user_permissions
 AFTER INSERT OR UPDATE OR DELETE ON user_permissions
 FOR EACH ROW EXECUTE FUNCTION fn_audit_user_permissions();


/* ─────────────────────────────────────────────────────────────
 PRIVILEGE ESCALATION PREVENTION
 ───────────────────────────────────────────────────────────── */

/*
 * A granter cannot assign a user_permission for a permission they do not
 * themselves hold via an active, non-deactivated role.
 *
 * Owner-role users are exempt from this check (unrestricted access).
 *
 * Important: receiving a direct user_permission grant does NOT confer the right to
 * re-grant that permission to others. Only role-based grants count for escalation
 * checks.
 *
 * Middleware should perform this check first for a better error UX. This trigger
 * is the DB backstop — it fires regardless of which code path triggered the write.
 *
 * The escape hatch (rbac.skip_escalation_check = '1') is reserved for seeding
 * scripts and test fixtures. It must never be exposed to end users.
 */
CREATE OR REPLACE FUNCTION fn_prevent_privilege_escalation()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
 v_granter_is_owner BOOLEAN;
 v_granter_has_perm  BOOLEAN;
BEGIN
 -- Escape hatch for test fixtures and seeding scripts only.
 IF current_setting('rbac.skip_escalation_check', TRUE) = '1' THEN
 RETURN NEW;
 END IF;

 -- Single pass: fetch is_owner_role and check permission existence in one query.
 -- Previously two separate queries traversed user_roles for the same granted_by user;
 -- this collapses them to one index seek on the user_id PK.
 SELECT
 r.is_owner_role,
 EXISTS (
 SELECT 1
 FROM role_permissions rp
 WHERE rp.role_id       = ur.role_id
 AND rp.permission_id = NEW.permission_id
 )
 INTO v_granter_is_owner, v_granter_has_perm
 FROM user_roles ur
 JOIN roles r ON r.id = ur.role_id
 WHERE ur.user_id   = NEW.granted_by
 AND r.is_active  = TRUE
 AND (ur.expires_at IS NULL OR ur.expires_at > NOW());

 -- No row found → granter has no active role → escalation denied.
 IF NOT FOUND THEN
 RAISE EXCEPTION
 'Privilege escalation denied: granter (user_id=%) has no active role.',
 NEW.granted_by
 USING ERRCODE = 'insufficient_privilege';
 END IF;

 -- Owner-role granters are unrestricted; skip the permission check.
 IF v_granter_is_owner THEN
 RETURN NEW;
 END IF;

 -- Verify the granter holds the target permission via their active role.
 IF NOT v_granter_has_perm THEN
 RAISE EXCEPTION
 'Privilege escalation denied: granter (user_id=%) does not hold permission_id=% on an active role.',
 NEW.granted_by, NEW.permission_id
 USING ERRCODE = 'insufficient_privilege';
 END IF;

 RETURN NEW;
END;
$$;

COMMENT ON FUNCTION fn_prevent_privilege_escalation() IS
 'Prevents a granter from assigning a permission they do not hold via an active role. '
 'Rewritten to a single query (was two sequential user_roles traversals): resolves '
 'is_owner_role and permission existence in one index seek on the user_id PK. '
 'Owner-role users are exempt. Middleware should check first; this is the DB backstop. '
 'Receiving a direct user_permissions grant does NOT confer re-grant rights; '
 'only role-based grants count for escalation checks.';

CREATE TRIGGER trg_prevent_privilege_escalation
 BEFORE INSERT OR UPDATE ON user_permissions
 FOR EACH ROW EXECUTE FUNCTION fn_prevent_privilege_escalation();


/* ─────────────────────────────────────────────────────────────
 USER PERMISSION EXPIRY POLICY
 ───────────────────────────────────────────────────────────── */

/*
 * Enforces expires_at bounds on user_permissions:
 * min: NOW() + rbac.min_temp_grant_lead (default 5 minutes)
 * max: NOW() + rbac.max_temp_grant_interval (default 90 days)
 *
 * A CHECK constraint cannot be used here because CHECK evaluates NOW() at
 * constraint-definition time, not at row-insert time. This trigger fires on
 * every INSERT/UPDATE regardless of code path.
 *
 * Policy constants are read from session variables (set in 003_rbac.sql) with
 * hard-coded fallbacks so the trigger works even if the session variables are unset.
 * Override in tests: SET LOCAL rbac.min_temp_grant_lead = '1 second'.
 */
CREATE OR REPLACE FUNCTION fn_validate_user_permission_expiry()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
 v_max_interval INTERVAL;
 v_min_lead INTERVAL;
 v_raw TEXT;
BEGIN
 v_raw := current_setting('rbac.max_temp_grant_interval', TRUE);
 v_max_interval := CASE WHEN v_raw IS NOT NULL AND v_raw != ''
 THEN v_raw::INTERVAL
 ELSE INTERVAL '90 days' END;

 v_raw := current_setting('rbac.min_temp_grant_lead', TRUE);
 v_min_lead := CASE WHEN v_raw IS NOT NULL AND v_raw != ''
 THEN v_raw::INTERVAL
 ELSE INTERVAL '5 minutes' END;

 -- Reject grants that expire too soon (minimum lead time not met).
 IF NEW.expires_at <= (NOW() + v_min_lead) THEN
 RAISE EXCEPTION
 'user_permissions.expires_at must be at least % from now (got %).',
 v_min_lead, NEW.expires_at
 USING ERRCODE = 'check_violation';
 END IF;

 -- Reject grants that expire too far in the future (maximum interval exceeded).
 IF NEW.expires_at > (NOW() + v_max_interval) THEN
 RAISE EXCEPTION
 'user_permissions.expires_at exceeds maximum grant duration of % (got %).',
 v_max_interval, NEW.expires_at
 USING ERRCODE = 'check_violation';
 END IF;

 RETURN NEW;
END;
$$;

COMMENT ON FUNCTION fn_validate_user_permission_expiry() IS
 'Enforces expires_at bounds: min NOW()+5min, max NOW()+90days. '
 'A CHECK constraint cannot be used because it evaluates NOW() at definition time. '
 'Policy constants read from rbac.min_temp_grant_lead / rbac.max_temp_grant_interval '
 'with hard-coded fallbacks. EXCEPTION blocks removed in favour of CASE expressions '
 'to avoid swallowing unexpected errors.';

CREATE TRIGGER trg_validate_user_permission_expiry
 BEFORE INSERT OR UPDATE ON user_permissions
 FOR EACH ROW EXECUTE FUNCTION fn_validate_user_permission_expiry();


/* ─────────────────────────────────────────────────────────────
 ORPHANED OWNER PREVENTION
 ───────────────────────────────────────────────────────────── */

/*
 * Prevents removal of the last active owner-role assignment. At least one active
 * owner must always exist so the system can always be administered.
 *
 * Race condition fix: SELECT … FOR UPDATE OF ur locks the relevant user_roles rows,
 * serialising concurrent DELETE/UPDATE transactions that would otherwise both pass
 * the COUNT(*) check independently and both proceed to remove their respective
 * owner rows, leaving zero owners.
 *
 * PostgreSQL forbids subqueries in trigger WHEN clauses (SQLSTATE 0A000), so the
 * is_owner_role check lives inside the function body with an early return for
 * non-owner rows. The UPDATE trigger WHEN clause retains a scalar guard to skip
 * no-op role updates cheaply.
 */
CREATE OR REPLACE FUNCTION fn_prevent_orphaned_owner()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
 -- Escape hatch for test fixtures and seeding scripts only.
 -- Mirrors the same bypass used in fn_prevent_privilege_escalation and
 -- fn_prevent_owner_role_escalation. Must never be set to '1' in production.
 IF current_setting('rbac.skip_orphan_check', TRUE) = '1' THEN
 RETURN OLD;
 END IF;

 -- PG forbids subqueries in WHEN clauses; early-exit for non-owner rows.
 IF NOT EXISTS (
 SELECT 1 FROM roles WHERE id = OLD.role_id AND is_owner_role = TRUE AND is_active = TRUE) THEN
 RETURN OLD;
 END IF;

 -- Lock ALL remaining owner rows to serialise concurrent removal attempts.
 -- PERFORM acquires the FOR UPDATE locks without COUNT aggregation overhead,
 -- and stops only after visiting every qualifying row (no LIMIT) so that all
 -- concurrent owner-removal transactions are fully serialised.
 -- NOT FOUND → no remaining owners → reject the removal.
 PERFORM 1
 FROM user_roles ur
 JOIN roles r ON r.id = ur.role_id
 WHERE r.is_owner_role = TRUE
 AND r.is_active     = TRUE
 AND ur.user_id     != OLD.user_id
 AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
 FOR UPDATE OF ur;

 IF NOT FOUND THEN
 RAISE EXCEPTION
 'Cannot remove last active owner (user_id=%). At least one active owner must remain.',
 OLD.user_id
 USING ERRCODE = 'integrity_constraint_violation';
 END IF;

 RETURN OLD;
END;
$$;

COMMENT ON FUNCTION fn_prevent_orphaned_owner() IS
 'Prevents deletion or reassignment of the last active owner-role assignment. '
 'Uses PERFORM ... FOR UPDATE OF ur to lock all remaining owner rows, serialising '
 'concurrent removal attempts without COUNT aggregation overhead. '
 'NOT FOUND after PERFORM means no remaining owners → raise exception. '
 'Escape hatch: SET rbac.skip_orphan_check = ''1'' (session-scoped, '
 'for test fixtures and seeding scripts only — never use in production).';

-- Fires before a user_roles row is deleted.
CREATE TRIGGER trg_prevent_orphaned_owner_on_delete
 BEFORE DELETE ON user_roles
 FOR EACH ROW
 EXECUTE FUNCTION fn_prevent_orphaned_owner();

-- WHEN guard skips the function call entirely on no-op role updates (same role_id).
CREATE TRIGGER trg_prevent_orphaned_owner_on_update
 BEFORE UPDATE OF role_id ON user_roles
 FOR EACH ROW
 WHEN (OLD.role_id != NEW.role_id)
 EXECUTE FUNCTION fn_prevent_orphaned_owner();


/* ─────────────────────────────────────────────────────────────
 OWNER ROLE ESCALATION PREVENTION
 ───────────────────────────────────────────────────────────── */

/*
 * Prevents any user from assigning the owner role unless they are already
 * an active owner themselves. This is a DB backstop; middleware should enforce
 * the same check first for a better UX error message.
 */
CREATE OR REPLACE FUNCTION fn_prevent_owner_role_escalation()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
 v_role_is_owner    BOOLEAN;
 v_granter_is_owner BOOLEAN;
BEGIN
 -- Escape hatch for test fixtures and seeding scripts only.
 -- Mirrors the same bypass already present in fn_prevent_privilege_escalation.
 -- Must never be set to '1' in production code paths.
 IF current_setting('rbac.skip_escalation_check', TRUE) = '1' THEN
  RETURN NEW;
 END IF;

 -- Single pass: resolve whether the target role is the owner role AND whether
 -- the granter holds an active owner role in one index seek.
 -- idx_roles_owner_active (partial: is_owner_role=TRUE AND is_active=TRUE) makes
 -- the inner correlated subquery an O(1) seek for the common non-owner case.
 SELECT
  r_target.is_owner_role,
  COALESCE((
   SELECT r_granter.is_owner_role
   FROM   user_roles ur
   JOIN   roles r_granter ON r_granter.id = ur.role_id
   WHERE  ur.user_id          = NEW.granted_by
     AND  r_granter.is_active = TRUE
     AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
  ), FALSE)
 INTO v_role_is_owner, v_granter_is_owner
 FROM roles r_target
 WHERE r_target.id = NEW.role_id;

 -- Not the owner role (or role not found) — nothing to enforce.
 IF NOT FOUND OR NOT v_role_is_owner THEN
  RETURN NEW;
 END IF;

 -- Bootstrap exception: if no active owner exists yet, allow the first assignment.
 -- This is the chicken-and-egg path — the /owner/bootstrap endpoint is the only
 -- caller; the service layer must gate on CountActiveOwners = 0 before calling this.
 IF NOT EXISTS (
  SELECT 1
  FROM   user_roles ur
  JOIN   roles r ON r.id = ur.role_id
  WHERE  r.is_owner_role = TRUE
    AND  r.is_active     = TRUE
    AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
 ) THEN
  RETURN NEW;
 END IF;

 -- Granting the owner role requires the granter to hold an active owner role themselves.
 IF NOT v_granter_is_owner THEN
  RAISE EXCEPTION
   'Owner-role escalation denied: granter (user_id=%) does not hold an active owner role.',
   NEW.granted_by
  USING ERRCODE = 'insufficient_privilege';
 END IF;

 RETURN NEW;
END;
$$;

COMMENT ON FUNCTION fn_prevent_owner_role_escalation() IS
 'Blocks assignment of the owner role unless the granter holds an active owner role. '
 'Bootstrap exception: when no active owner exists, the first assignment is always allowed '
 '(chicken-and-egg path for /owner/bootstrap). Service layer must gate on CountActiveOwners = 0. '
 'Fires on INSERT and on UPDATE OF role_id. Middleware should enforce this first; '
 'this trigger is the DB backstop. '
 'Escape hatch: SET LOCAL rbac.skip_escalation_check = ''1'' (transaction-scoped, '
 'for test fixtures and seeding scripts only — never expose to end users).';

CREATE TRIGGER trg_prevent_owner_role_escalation
 BEFORE INSERT OR UPDATE OF role_id ON user_roles
 FOR EACH ROW EXECUTE FUNCTION fn_prevent_owner_role_escalation();


/* ─────────────────────────────────────────────────────────────
 USER ROLE EXPIRY POLICY
 ───────────────────────────────────────────────────────────── */

/*
 * When expires_at is set on a user_roles row, it must be at least
 * rbac.min_temp_grant_lead (default 5 minutes) in the future.
 * Permanent grants (expires_at IS NULL) are not restricted.
 *
 * Uses the same policy constant as fn_validate_user_permission_expiry.
 */
CREATE OR REPLACE FUNCTION fn_validate_user_role_expiry()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
 v_min_lead INTERVAL;
 v_raw TEXT;
BEGIN
 -- Permanent grants are always valid; skip the check.
 IF NEW.expires_at IS NULL THEN
 RETURN NEW;
 END IF;

 v_raw := current_setting('rbac.min_temp_grant_lead', TRUE);
 v_min_lead := CASE WHEN v_raw IS NOT NULL AND v_raw != ''
 THEN v_raw::INTERVAL
 ELSE INTERVAL '5 minutes' END;

 -- Reject temporary role grants that expire sooner than the minimum lead time.
 IF NEW.expires_at <= (NOW() + v_min_lead) THEN
 RAISE EXCEPTION
 'user_roles.expires_at must be at least % from now (got %).',
 v_min_lead, NEW.expires_at
 USING ERRCODE = 'check_violation';
 END IF;

 RETURN NEW;
END;
$$;

COMMENT ON FUNCTION fn_validate_user_role_expiry() IS
 'Enforces expires_at >= NOW() + rbac.min_temp_grant_lead for temporary role grants. '
 'Permanent grants (expires_at IS NULL) are not restricted. '
 'Uses the same policy constant as fn_validate_user_permission_expiry.';

CREATE TRIGGER trg_validate_user_role_expiry
 BEFORE INSERT OR UPDATE OF expires_at ON user_roles
 FOR EACH ROW EXECUTE FUNCTION fn_validate_user_role_expiry();


/* ─────────────────────────────────────────────────────────────
 PERMISSION REQUEST APPROVERS AUDIT
 ───────────────────────────────────────────────────────────── */

-- Writes an audit record for every change to the approval chain configuration.
-- changed_by is read from the rbac.acting_user session variable; NULL when unset.
CREATE OR REPLACE FUNCTION fn_audit_permission_request_approvers()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
 v_change_reason TEXT;
 v_changed_by UUID;
BEGIN
 v_change_reason := current_setting('rbac.change_reason', TRUE);
 v_changed_by := NULLIF(current_setting('rbac.acting_user', TRUE), '')::UUID;

 IF TG_OP = 'DELETE' THEN
 INSERT INTO permission_request_approvers_audit (
 permission_id, role_id,
 approval_level, min_required,
 previous_approval_level, previous_min_required,
 change_type, changed_by, change_reason
 ) VALUES (
 OLD.permission_id, OLD.role_id,
 NULL, NULL,
 OLD.approval_level, OLD.min_required,
 'deleted', v_changed_by, v_change_reason
 );
 RETURN OLD;

 ELSIF TG_OP = 'UPDATE' THEN
 INSERT INTO permission_request_approvers_audit (
 permission_id, role_id,
 approval_level, min_required,
 previous_approval_level, previous_min_required,
 change_type, changed_by, change_reason
 ) VALUES (
 NEW.permission_id, NEW.role_id,
 NEW.approval_level, NEW.min_required,
 OLD.approval_level, OLD.min_required,
 'updated', v_changed_by, v_change_reason
 );
 RETURN NEW;

 ELSE -- INSERT
 INSERT INTO permission_request_approvers_audit (
 permission_id, role_id,
 approval_level, min_required,
 previous_approval_level, previous_min_required,
 change_type, changed_by, change_reason
 ) VALUES (
 NEW.permission_id, NEW.role_id,
 NEW.approval_level, NEW.min_required,
 NULL, NULL,
 'created', v_changed_by, v_change_reason
 );
 RETURN NEW;
 END IF;
END;
$$;

COMMENT ON FUNCTION fn_audit_permission_request_approvers() IS
 'Writes an immutable record to permission_request_approvers_audit on every INSERT/UPDATE/DELETE. '
 'changed_by reads rbac.acting_user session variable. NULL when unset (e.g. migrations).';

CREATE TRIGGER trg_audit_pra
 AFTER INSERT OR UPDATE OR DELETE ON permission_request_approvers
 FOR EACH ROW EXECUTE FUNCTION fn_audit_permission_request_approvers();

-- Keeps permission_request_approvers.updated_at current when levels or counts change.
CREATE TRIGGER trg_pra_updated_at
 BEFORE UPDATE ON permission_request_approvers
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();


/* ─────────────────────────────────────────────────────────────
 RBAC AUDIT TABLE IMMUTABILITY
 ───────────────────────────────────────────────────────────── */

-- fn_deny_audit_update() is defined in 002_core_functions.sql and is reused here.
-- Each trigger blocks UPDATE on its respective audit table to prevent rewriting history.

CREATE TRIGGER trg_role_permissions_audit_deny_update
 BEFORE UPDATE ON role_permissions_audit
 FOR EACH ROW EXECUTE FUNCTION fn_deny_audit_update();

CREATE TRIGGER trg_user_roles_audit_deny_update
 BEFORE UPDATE ON user_roles_audit
 FOR EACH ROW EXECUTE FUNCTION fn_deny_audit_update();

CREATE TRIGGER trg_user_permissions_audit_deny_update
 BEFORE UPDATE ON user_permissions_audit
 FOR EACH ROW EXECUTE FUNCTION fn_deny_audit_update();

-- Belt-and-suspenders: revoke UPDATE privilege at the role level on all RBAC audit tables.
REVOKE UPDATE ON role_permissions_audit FROM PUBLIC;
REVOKE UPDATE ON user_roles_audit FROM PUBLIC;
REVOKE UPDATE ON user_permissions_audit FROM PUBLIC;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_user_permissions_audit_deny_update ON user_permissions_audit;
DROP TRIGGER IF EXISTS trg_user_roles_audit_deny_update ON user_roles_audit;
DROP TRIGGER IF EXISTS trg_role_permissions_audit_deny_update ON role_permissions_audit;
DROP TRIGGER IF EXISTS trg_pra_updated_at ON permission_request_approvers;
DROP TRIGGER IF EXISTS trg_audit_pra ON permission_request_approvers;
DROP TRIGGER IF EXISTS trg_validate_user_role_expiry ON user_roles;
DROP TRIGGER IF EXISTS trg_prevent_owner_role_escalation ON user_roles;
DROP TRIGGER IF EXISTS trg_prevent_orphaned_owner_on_update ON user_roles;
DROP TRIGGER IF EXISTS trg_prevent_orphaned_owner_on_delete ON user_roles;
DROP TRIGGER IF EXISTS trg_validate_user_permission_expiry ON user_permissions;
DROP TRIGGER IF EXISTS trg_prevent_privilege_escalation ON user_permissions;
DROP TRIGGER IF EXISTS trg_audit_user_permissions ON user_permissions;
DROP TRIGGER IF EXISTS trg_audit_user_roles ON user_roles;
DROP TRIGGER IF EXISTS trg_audit_role_permissions ON role_permissions;
DROP TRIGGER IF EXISTS trg_user_permissions_updated_at ON user_permissions;
DROP TRIGGER IF EXISTS trg_user_roles_updated_at ON user_roles;
DROP TRIGGER IF EXISTS trg_role_permissions_updated_at ON role_permissions;
DROP TRIGGER IF EXISTS trg_pct_updated_at ON permission_condition_templates;
DROP TRIGGER IF EXISTS trg_permission_groups_updated_at ON permission_groups;
DROP TRIGGER IF EXISTS trg_permissions_updated_at ON permissions;
DROP TRIGGER IF EXISTS trg_roles_updated_at ON roles;

DROP FUNCTION IF EXISTS fn_audit_permission_request_approvers();
DROP FUNCTION IF EXISTS fn_validate_user_role_expiry();
DROP FUNCTION IF EXISTS fn_prevent_owner_role_escalation();
DROP FUNCTION IF EXISTS fn_prevent_orphaned_owner();
DROP FUNCTION IF EXISTS fn_validate_user_permission_expiry();
DROP FUNCTION IF EXISTS fn_prevent_privilege_escalation();
DROP FUNCTION IF EXISTS fn_audit_user_permissions();
DROP FUNCTION IF EXISTS fn_audit_user_roles();
DROP FUNCTION IF EXISTS fn_audit_role_permissions();

-- +goose StatementEnd
