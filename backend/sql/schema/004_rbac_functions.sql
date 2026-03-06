-- +goose Up
-- +goose StatementBegin

-- 004_rbac_functions.sql — trigger functions and triggers for RBAC integrity and audit.
-- Depends on: 002_core_functions.sql (fn_set_updated_at), 003_rbac.sql


-- ------------------------------------------------------------
-- updated_at — RBAC TABLES
-- ------------------------------------------------------------

CREATE TRIGGER trg_roles_updated_at
    BEFORE UPDATE ON roles
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

CREATE TRIGGER trg_permissions_updated_at
    BEFORE UPDATE ON permissions
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

CREATE TRIGGER trg_permission_groups_updated_at
    BEFORE UPDATE ON permission_groups
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

CREATE TRIGGER trg_pct_updated_at
    BEFORE UPDATE ON permission_condition_templates
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

CREATE TRIGGER trg_role_permissions_updated_at
    BEFORE UPDATE ON role_permissions
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

CREATE TRIGGER trg_user_roles_updated_at
    BEFORE UPDATE ON user_roles
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

CREATE TRIGGER trg_user_permissions_updated_at
    BEFORE UPDATE ON user_permissions
    FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();


-- ------------------------------------------------------------
-- AUDIT LOG POPULATION
-- ------------------------------------------------------------
-- Triggers rather than application code: app-layer audit can be bypassed by
-- a direct DB write, a race condition, or a missed code path. These fire
-- unconditionally on every mutation.
--
-- changed_by is sourced from the row's own granted_by — the audit record is
-- self-contained and correct even if the app omits a session variable.
--
-- change_reason is read from SET LOCAL rbac.change_reason when the app sets it;
-- NULL otherwise. Optional context, not required for correctness.

CREATE OR REPLACE FUNCTION fn_audit_role_permissions()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    v_change_reason TEXT;
BEGIN
    BEGIN
        v_change_reason := current_setting('rbac.change_reason', TRUE);
    EXCEPTION WHEN OTHERS THEN
        v_change_reason := NULL;
    END;

    IF TG_OP = 'DELETE' THEN
        INSERT INTO role_permissions_audit (
            role_id, permission_id,
            conditions, previous_conditions,
            change_type, changed_by, change_reason
        ) VALUES (
            OLD.role_id, OLD.permission_id,
            NULL, OLD.conditions,
            'deleted', OLD.granted_by, v_change_reason
        );
        RETURN OLD;
    ELSIF TG_OP = 'UPDATE' THEN
        INSERT INTO role_permissions_audit (
            role_id, permission_id,
            conditions, previous_conditions,
            change_type, changed_by, change_reason
        ) VALUES (
            NEW.role_id, NEW.permission_id,
            NEW.conditions, OLD.conditions,
            'updated', NEW.granted_by, v_change_reason
        );
        RETURN NEW;
    ELSE -- INSERT
        INSERT INTO role_permissions_audit (
            role_id, permission_id,
            conditions, previous_conditions,
            change_type, changed_by, change_reason
        ) VALUES (
            NEW.role_id, NEW.permission_id,
            NEW.conditions, NULL,
            'created', NEW.granted_by, v_change_reason
        );
        RETURN NEW;
    END IF;
END;
$$;

COMMENT ON FUNCTION fn_audit_role_permissions() IS
    'Writes an immutable record to role_permissions_audit on every INSERT/UPDATE/DELETE. changed_by sourced from granted_by on the row.';

CREATE TRIGGER trg_audit_role_permissions
    AFTER INSERT OR UPDATE OR DELETE ON role_permissions
    FOR EACH ROW EXECUTE FUNCTION fn_audit_role_permissions();


CREATE OR REPLACE FUNCTION fn_audit_user_roles()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    v_change_reason TEXT;
BEGIN
    BEGIN
        v_change_reason := current_setting('rbac.change_reason', TRUE);
    EXCEPTION WHEN OTHERS THEN
        v_change_reason := NULL;
    END;

    IF TG_OP = 'DELETE' THEN
        INSERT INTO user_roles_audit (
            user_id, role_id, previous_role_id,
            change_type, changed_by, change_reason
        ) VALUES (
            OLD.user_id, OLD.role_id, NULL,
            'deleted', OLD.granted_by, v_change_reason
        );
        RETURN OLD;
    ELSIF TG_OP = 'UPDATE' THEN
        INSERT INTO user_roles_audit (
            user_id, role_id, previous_role_id,
            change_type, changed_by, change_reason
        ) VALUES (
            NEW.user_id, NEW.role_id, OLD.role_id,
            'updated', NEW.granted_by, v_change_reason
        );
        RETURN NEW;
    ELSE -- INSERT
        INSERT INTO user_roles_audit (
            user_id, role_id, previous_role_id,
            change_type, changed_by, change_reason
        ) VALUES (
            NEW.user_id, NEW.role_id, NULL,
            'created', NEW.granted_by, v_change_reason
        );
        RETURN NEW;
    END IF;
END;
$$;

COMMENT ON FUNCTION fn_audit_user_roles() IS
    'Writes an immutable record to user_roles_audit on every INSERT/UPDATE/DELETE. previous_role_id captures the role before a change; NULL on first INSERT.';

CREATE TRIGGER trg_audit_user_roles
    AFTER INSERT OR UPDATE OR DELETE ON user_roles
    FOR EACH ROW EXECUTE FUNCTION fn_audit_user_roles();


CREATE OR REPLACE FUNCTION fn_audit_user_permissions()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    v_change_reason TEXT;
BEGIN
    BEGIN
        v_change_reason := current_setting('rbac.change_reason', TRUE);
    EXCEPTION WHEN OTHERS THEN
        v_change_reason := NULL;
    END;

    IF TG_OP = 'DELETE' THEN
        INSERT INTO user_permissions_audit (
            user_id, permission_id,
            conditions, previous_conditions,
            change_type, changed_by, change_reason
        ) VALUES (
            OLD.user_id, OLD.permission_id,
            NULL, OLD.conditions,
            'deleted', OLD.granted_by, v_change_reason
        );
        RETURN OLD;
    ELSIF TG_OP = 'UPDATE' THEN
        INSERT INTO user_permissions_audit (
            user_id, permission_id,
            conditions, previous_conditions,
            change_type, changed_by, change_reason
        ) VALUES (
            NEW.user_id, NEW.permission_id,
            NEW.conditions, OLD.conditions,
            'updated', NEW.granted_by, v_change_reason
        );
        RETURN NEW;
    ELSE -- INSERT
        INSERT INTO user_permissions_audit (
            user_id, permission_id,
            conditions, previous_conditions,
            change_type, changed_by, change_reason
        ) VALUES (
            NEW.user_id, NEW.permission_id,
            NEW.conditions, NULL,
            'created', NEW.granted_by, v_change_reason
        );
        RETURN NEW;
    END IF;
END;
$$;

COMMENT ON FUNCTION fn_audit_user_permissions() IS
    'Writes an immutable record to user_permissions_audit on every INSERT/UPDATE/DELETE. user_permissions is the highest-risk RBAC table — every mutation is tracked unconditionally.';

CREATE TRIGGER trg_audit_user_permissions
    AFTER INSERT OR UPDATE OR DELETE ON user_permissions
    FOR EACH ROW EXECUTE FUNCTION fn_audit_user_permissions();


-- ------------------------------------------------------------
-- PRIVILEGE ESCALATION PREVENTION
-- ------------------------------------------------------------
-- A granter cannot assign a user_permission for a permission they do not
-- themselves hold via their active role. Exception: owner-role users are exempt.
--
-- Middleware should perform the same check first for a better error UX. This
-- trigger is the DB backstop — it fires regardless of code path.

CREATE OR REPLACE FUNCTION fn_prevent_privilege_escalation()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    v_granter_is_owner BOOLEAN;
BEGIN
    SELECT r.is_owner_role
    INTO   v_granter_is_owner
    FROM   user_roles ur
    JOIN   roles r ON r.id = ur.role_id
    WHERE  ur.user_id = NEW.granted_by
      AND  (ur.expires_at IS NULL OR ur.expires_at > NOW());

    -- Owner-role users are unrestricted.
    IF FOUND AND v_granter_is_owner = TRUE THEN
        RETURN NEW;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM   user_roles ur
        JOIN   role_permissions rp ON rp.role_id = ur.role_id
        WHERE  ur.user_id       = NEW.granted_by
          AND  rp.permission_id = NEW.permission_id
          AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
    ) THEN
        RAISE EXCEPTION
            'Privilege escalation denied: granter (user_id=%) does not hold permission_id=% on their own role.',
            NEW.granted_by, NEW.permission_id
            USING ERRCODE = 'insufficient_privilege';
    END IF;

    RETURN NEW;
END;
$$;

COMMENT ON FUNCTION fn_prevent_privilege_escalation() IS
    'Prevents a granter from assigning a permission they do not hold themselves. Owner-role users are exempt. Middleware should check first; this is the unconditional DB backstop.';

CREATE TRIGGER trg_prevent_privilege_escalation
    BEFORE INSERT OR UPDATE ON user_permissions
    FOR EACH ROW EXECUTE FUNCTION fn_prevent_privilege_escalation();


-- ------------------------------------------------------------
-- USER PERMISSION EXPIRY POLICY
-- ------------------------------------------------------------
-- Enforces expires_at bounds: min NOW()+5min, max NOW()+90days.
--
-- A CHECK constraint cannot be used here because CHECK evaluates NOW() at
-- constraint-definition time, not at row-insert time. This trigger fires on
-- every INSERT/UPDATE regardless of code path.
--
-- Policy constants (set in 003_rbac.sql, readable via current_setting):
--   rbac.min_temp_grant_lead     — min distance into future (default: 5 min)
--   rbac.max_temp_grant_interval — max TTL from now         (default: 90 days)
-- Both have hard-coded fallbacks.
-- Override in tests: SET LOCAL rbac.min_temp_grant_lead = '1 second'.

CREATE OR REPLACE FUNCTION fn_validate_user_permission_expiry()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    v_max_interval INTERVAL;
    v_min_lead     INTERVAL;
BEGIN
    BEGIN
        v_max_interval := current_setting('rbac.max_temp_grant_interval', TRUE)::INTERVAL;
    EXCEPTION WHEN OTHERS THEN
        v_max_interval := INTERVAL '90 days';
    END;

    BEGIN
        v_min_lead := current_setting('rbac.min_temp_grant_lead', TRUE)::INTERVAL;
    EXCEPTION WHEN OTHERS THEN
        v_min_lead := INTERVAL '5 minutes';
    END;

    IF NEW.expires_at <= (NOW() + v_min_lead) THEN
        RAISE EXCEPTION
            'user_permissions.expires_at must be at least % from now (got %).',
            v_min_lead, NEW.expires_at
            USING ERRCODE = 'check_violation';
    END IF;

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
    'Enforces expires_at bounds: min NOW()+5min, max NOW()+90days. A CHECK constraint cannot be used because it evaluates NOW() at definition time. Policy constants read from rbac.min_temp_grant_lead / rbac.max_temp_grant_interval with hard-coded fallbacks.';

CREATE TRIGGER trg_validate_user_permission_expiry
    BEFORE INSERT OR UPDATE ON user_permissions
    FOR EACH ROW EXECUTE FUNCTION fn_validate_user_permission_expiry();


-- ------------------------------------------------------------
-- ORPHANED OWNER PREVENTION
-- ------------------------------------------------------------
-- Prevents removal of the last active owner-role assignment.
--
-- Application-level checks cannot protect against concurrent transactions that
-- both pass the "is there another owner?" check simultaneously. Only a
-- serialised trigger with an implicit row-level lock is safe here.
--
-- PG forbids subqueries in trigger WHEN clauses (SQLSTATE 0A000), so the
-- is_owner_role check lives inside the function body with an early return for
-- non-owner rows. The UPDATE trigger WHEN clause retains the scalar
-- OLD.role_id != NEW.role_id guard to skip no-op updates cheaply.

CREATE OR REPLACE FUNCTION fn_prevent_orphaned_owner()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    v_remaining_owners INTEGER;
BEGIN
    -- PG forbids subqueries in WHEN clauses; early-exit here for non-owner rows.
    IF NOT EXISTS (
        SELECT 1 FROM roles WHERE id = OLD.role_id AND is_owner_role = TRUE
    ) THEN
        RETURN OLD;
    END IF;

    SELECT COUNT(*)
    INTO   v_remaining_owners
    FROM   user_roles ur
    JOIN   roles r ON r.id = ur.role_id
    JOIN   users u ON u.id = ur.user_id
    WHERE  r.is_owner_role = TRUE
      AND  u.is_active     = TRUE
      AND  ur.user_id     != OLD.user_id
      AND  (ur.expires_at IS NULL OR ur.expires_at > NOW());

    IF v_remaining_owners = 0 THEN
        RAISE EXCEPTION
            'Cannot remove last active owner (user_id=%). At least one active owner must remain.',
            OLD.user_id
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    RETURN OLD;
END;
$$;

COMMENT ON FUNCTION fn_prevent_orphaned_owner() IS
    'Prevents deletion or reassignment of the last active owner-role assignment. Concurrent transactions can race past application-level checks; only a serialised trigger with row-level locking is safe.';

CREATE TRIGGER trg_prevent_orphaned_owner_on_delete
    BEFORE DELETE ON user_roles
    FOR EACH ROW
    EXECUTE FUNCTION fn_prevent_orphaned_owner();

-- WHEN guard skips the function call entirely on no-op role updates.
CREATE TRIGGER trg_prevent_orphaned_owner_on_update
    BEFORE UPDATE OF role_id ON user_roles
    FOR EACH ROW
    WHEN (OLD.role_id != NEW.role_id)
    EXECUTE FUNCTION fn_prevent_orphaned_owner();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_prevent_orphaned_owner_on_update   ON user_roles;
DROP TRIGGER IF EXISTS trg_prevent_orphaned_owner_on_delete   ON user_roles;
DROP TRIGGER IF EXISTS trg_validate_user_permission_expiry    ON user_permissions;
DROP TRIGGER IF EXISTS trg_prevent_privilege_escalation       ON user_permissions;
DROP TRIGGER IF EXISTS trg_audit_user_permissions             ON user_permissions;
DROP TRIGGER IF EXISTS trg_audit_user_roles                   ON user_roles;
DROP TRIGGER IF EXISTS trg_audit_role_permissions             ON role_permissions;
DROP TRIGGER IF EXISTS trg_user_permissions_updated_at        ON user_permissions;
DROP TRIGGER IF EXISTS trg_user_roles_updated_at              ON user_roles;
DROP TRIGGER IF EXISTS trg_role_permissions_updated_at        ON role_permissions;
DROP TRIGGER IF EXISTS trg_pct_updated_at                     ON permission_condition_templates;
DROP TRIGGER IF EXISTS trg_permission_groups_updated_at       ON permission_groups;
DROP TRIGGER IF EXISTS trg_permissions_updated_at             ON permissions;
DROP TRIGGER IF EXISTS trg_roles_updated_at                   ON roles;

DROP FUNCTION IF EXISTS fn_prevent_orphaned_owner();
DROP FUNCTION IF EXISTS fn_validate_user_permission_expiry();
DROP FUNCTION IF EXISTS fn_prevent_privilege_escalation();
DROP FUNCTION IF EXISTS fn_audit_user_permissions();
DROP FUNCTION IF EXISTS fn_audit_user_roles();
DROP FUNCTION IF EXISTS fn_audit_role_permissions();

-- +goose StatementEnd
