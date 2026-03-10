-- +goose Up
-- +goose StatementBegin

/*
 * 006_request_functions.sql — Trigger functions and triggers for the request workflow.
 *
 * Covers:
 * fn_prevent_terminal_status_change — blocks mutations on requests in terminal states
 * fn_record_request_status_history — auto-populates request_status_history on every status change
 * updated_at triggers — keeps updated_at current on all request tables
 * fn_deny_request_immutable_update — makes request_status_history and request_approvals immutable
 * fn_prevent_self_approval — prevents a requester from approving their own request
 * fn_enforce_request_status_transition — enforces legal state-machine transitions
 * fn_sync_approvals_required — keeps requests.approvals_required in sync
 *
 * Depends on: 005_requests.sql (requests, request_status_history)
 */


/* ─────────────────────────────────────────────────────────────
 TERMINAL STATE IMMUTABILITY
 ───────────────────────────────────────────────────────────── */

/*
 * Once a request reaches a terminal state (rejected, cancelled, completed, failed)
 * it must not be modified in any way. Terminal states are irreversible by definition.
 *
 * This trigger fires BEFORE UPDATE on requests rows that are already in a terminal
 * state — the WHEN clause in the trigger definition filters out non-terminal rows,
 * so the function body only needs to raise the exception.
 */
CREATE OR REPLACE FUNCTION fn_prevent_terminal_status_change()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION
 'Request % is in terminal state % and cannot be updated.',
 OLD.id, OLD.status
 USING ERRCODE = 'check_violation';
 RETURN NEW;
END;
$$;

COMMENT ON FUNCTION fn_prevent_terminal_status_change() IS
 'Blocks any UPDATE on requests rows that are already in a terminal state '
 '(rejected, cancelled, completed, failed). Terminal states are irreversible.';

-- WHEN clause pre-filters: only fires for rows already in a terminal state.
CREATE TRIGGER trg_prevent_terminal_status_change
 BEFORE UPDATE ON requests
 FOR EACH ROW
 WHEN (OLD.status IN ('rejected', 'cancelled', 'completed', 'failed'))
 EXECUTE FUNCTION fn_prevent_terminal_status_change();


/* ─────────────────────────────────────────────────────────────
 REQUEST STATUS HISTORY — AUTO-POPULATION
 ───────────────────────────────────────────────────────────── */

/*
 * Automatically appends a row to request_status_history on every status change.
 *
 * On INSERT: records the initial status with old_status = NULL (no prior state).
 * On UPDATE: records the transition only when status actually changed — skips
 * no-op updates where status is unchanged.
 *
 * The actor is read from SET LOCAL rbac.acting_user; falls back to NULL for
 * automated scheduler transitions that don't set this variable.
 */
CREATE OR REPLACE FUNCTION fn_record_request_status_history()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
 v_actor UUID;
BEGIN
 -- INSERT: record initial status (old_status = NULL).
 IF TG_OP = 'INSERT' THEN
 v_actor := NULLIF(current_setting('rbac.acting_user', TRUE), '')::UUID;
 INSERT INTO request_status_history (request_id, old_status, new_status, changed_by)
 VALUES (NEW.id, NULL, NEW.status, v_actor);
 RETURN NEW;
 END IF;

 -- UPDATE: only record when the status column actually changed.
 IF OLD.status IS DISTINCT FROM NEW.status THEN
 v_actor := NULLIF(current_setting('rbac.acting_user', TRUE), '')::UUID;
 INSERT INTO request_status_history (request_id, old_status, new_status, changed_by)
 VALUES (NEW.id, OLD.status, NEW.status, v_actor);
 END IF;

 RETURN NEW;
END;
$$;

COMMENT ON FUNCTION fn_record_request_status_history() IS
 'Appends a row to request_status_history on every status change (INSERT or UPDATE). '
 'Actor read from rbac.acting_user session variable; NULL for automated transitions.';

-- Fires AFTER so that the triggering row is fully committed before the history row is inserted.
CREATE TRIGGER trg_record_request_status_history
 AFTER INSERT OR UPDATE OF status ON requests
 FOR EACH ROW EXECUTE FUNCTION fn_record_request_status_history();


/* ─────────────────────────────────────────────────────────────
 UPDATED_AT TRIGGERS
 ───────────────────────────────────────────────────────────── */

-- Keeps requests.updated_at current on every mutation (status change, data update, etc.).
CREATE TRIGGER trg_requests_updated_at
 BEFORE UPDATE ON requests
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps request_type_schemas.updated_at current when JSON Schema is updated.
CREATE TRIGGER trg_request_type_schemas_updated_at
 BEFORE UPDATE ON request_type_schemas
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps request_sla_config.updated_at current when thresholds change.
CREATE TRIGGER trg_request_sla_config_updated_at
 BEFORE UPDATE ON request_sla_config
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps request_required_approvers.updated_at current when min_required or conditions change.
CREATE TRIGGER trg_request_required_approvers_updated_at
 BEFORE UPDATE ON request_required_approvers
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps request_sla_violations.updated_at current when resolved_at or notified changes.
CREATE TRIGGER trg_request_sla_violations_updated_at
 BEFORE UPDATE ON request_sla_violations
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();


/* ─────────────────────────────────────────────────────────────
 IMMUTABILITY — request_status_history & request_approvals
 ───────────────────────────────────────────────────────────── */

/*
 * Both request_status_history and request_approvals are forensic audit tables.
 * Their rows must never be modified after they are written.
 * DELETEs are permitted (via ON DELETE CASCADE from requests when a request is hard-deleted).
 */
CREATE OR REPLACE FUNCTION fn_deny_request_immutable_update()
RETURNS TRIGGER LANGUAGE plpgsql AS $body$
BEGIN
 RAISE EXCEPTION '% is immutable; UPDATE is not permitted', TG_TABLE_NAME
 USING ERRCODE = 'P0001';
 RETURN NULL;
END;
$body$;

COMMENT ON FUNCTION fn_deny_request_immutable_update() IS
 'Raises an exception on any UPDATE against request_status_history or request_approvals.
 DELETEs are permitted (ON DELETE CASCADE from requests).';

CREATE TRIGGER trg_request_status_history_deny_update
 BEFORE UPDATE ON request_status_history
 FOR EACH ROW EXECUTE FUNCTION fn_deny_request_immutable_update();

CREATE TRIGGER trg_request_approvals_deny_update
 BEFORE UPDATE ON request_approvals
 FOR EACH ROW EXECUTE FUNCTION fn_deny_request_immutable_update();

-- Belt-and-suspenders: revoke UPDATE privilege at the role level.
REVOKE UPDATE ON request_status_history FROM PUBLIC;
REVOKE UPDATE ON request_approvals FROM PUBLIC;


/* ─────────────────────────────────────────────────────────────
 SELF-APPROVAL PREVENTION
 ───────────────────────────────────────────────────────────── */

/*
 * Prevents the user who submitted a request from approving it themselves.
 * This is a DB backstop; the application middleware should enforce this check first
 * for a better UX error message. The trigger fires regardless of code path.
 */
CREATE OR REPLACE FUNCTION fn_prevent_self_approval()
RETURNS TRIGGER LANGUAGE plpgsql AS $body$
BEGIN
 -- Only enforce when approver_id is known (not NULL due to account deletion).
 IF NEW.approver_id IS NOT NULL AND EXISTS (
 SELECT 1 FROM requests
 WHERE id = NEW.request_id
 AND requester_id = NEW.approver_id
 ) THEN
 RAISE EXCEPTION
 'Self-approval denied: approver_id % is the requester of request %.',
 NEW.approver_id, NEW.request_id
 USING ERRCODE = 'integrity_constraint_violation';
 END IF;
 RETURN NEW;
END;
$body$;

COMMENT ON FUNCTION fn_prevent_self_approval() IS
 'Prevents a requester from approving their own request at the DB layer.
 App middleware should enforce this first; this trigger is the backstop.';

CREATE TRIGGER trg_request_approvals_prevent_self_approval
 BEFORE INSERT ON request_approvals
 FOR EACH ROW EXECUTE FUNCTION fn_prevent_self_approval();


/* ─────────────────────────────────────────────────────────────
 REQUEST STATUS TRANSITION VALIDATION
 ───────────────────────────────────────────────────────────── */

/*
 * Enforces the legal state-machine transition graph:
 * pending → approved | rejected | cancelled
 * approved → executing | rejected | cancelled
 * executing → completed | failed | cancelled
 *
 * All other transitions are illegal and will raise an exception.
 *
 * Terminal state guard (trg_prevent_terminal_status_change) handles the case where
 * the row is already in a terminal state; this trigger's WHEN clause skips those rows.
 */
CREATE OR REPLACE FUNCTION fn_enforce_request_status_transition()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
 -- Skip when status is not changing (no-op update on other columns).
 IF OLD.status IS NOT DISTINCT FROM NEW.status THEN
 RETURN NEW;
 END IF;

 -- Enforce legal transitions only.
 IF NOT (
 (OLD.status = 'pending' AND NEW.status IN ('approved', 'rejected', 'cancelled')) OR
 (OLD.status = 'approved' AND NEW.status IN ('executing', 'rejected', 'cancelled')) OR
 (OLD.status = 'executing' AND NEW.status IN ('completed', 'failed', 'cancelled'))
 ) THEN
 RAISE EXCEPTION
 'Illegal request status transition: % → % (request_id=%).',
 OLD.status, NEW.status, OLD.id
 USING ERRCODE = 'check_violation';
 END IF;

 RETURN NEW;
END;
$$;

COMMENT ON FUNCTION fn_enforce_request_status_transition() IS
 'Enforces legal request state-machine transitions. '
 'Legal: pending→approved|rejected|cancelled, approved→executing|rejected|cancelled, '
 'executing→completed|failed|cancelled. All other transitions raise an exception. '
 'Does not fire when transitioning OUT of terminal states — that is handled by '
 'trg_prevent_terminal_status_change.';

-- WHEN clause: only fire for non-terminal current states (terminal → anything is handled by the other trigger).
CREATE TRIGGER trg_enforce_request_status_transition
 BEFORE UPDATE OF status ON requests
 FOR EACH ROW
 WHEN (OLD.status NOT IN ('rejected', 'cancelled', 'completed', 'failed'))
 EXECUTE FUNCTION fn_enforce_request_status_transition();


/* ─────────────────────────────────────────────────────────────
 APPROVALS_REQUIRED SYNC
 ───────────────────────────────────────────────────────────── */

/*
 * Keeps requests.approvals_required equal to the sum of min_required across all
 * request_required_approvers rows for a given request.
 *
 * Fires after INSERT, UPDATE OF min_required, or DELETE on request_required_approvers.
 * Only updates requests that are still in 'pending' status to avoid disturbing
 * approved or executing rows mid-flight.
 *
 * Defaults to 1 when there are no approver rows (COALESCE(SUM, 1)) so the
 * constraint chk_requests_approvals_positive is never violated.
 */
CREATE OR REPLACE FUNCTION fn_sync_approvals_required()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
 v_request_id UUID;
 v_total INTEGER;
BEGIN
 -- COALESCE handles INSERT (NEW is set) and DELETE (OLD is set) uniformly.
 v_request_id := COALESCE(NEW.request_id, OLD.request_id);

 -- Sum all min_required values for this request; fall back to 1 if no rows exist.
 SELECT COALESCE(SUM(min_required), 1)
 INTO v_total
 FROM request_required_approvers
 WHERE request_id = v_request_id;

 -- Only update requests still in pending status; approved/executing rows are not disturbed.
 UPDATE requests
 SET approvals_required = v_total
 WHERE id = v_request_id
 AND status = 'pending';

 RETURN NEW;
END;
$$;

COMMENT ON FUNCTION fn_sync_approvals_required() IS
 'Keeps requests.approvals_required = SUM(min_required) across all required-approver rows '
 'for the request. Fires after INSERT/UPDATE/DELETE on request_required_approvers. '
 'Only updates requests still in pending status; approved/executing rows are not disturbed. '
 'Defaults to 1 when no approver rows exist (SUM = NULL).';

CREATE TRIGGER trg_sync_approvals_required
 AFTER INSERT OR UPDATE OF min_required OR DELETE ON request_required_approvers
 FOR EACH ROW EXECUTE FUNCTION fn_sync_approvals_required();


-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS trg_sync_approvals_required ON request_required_approvers;
DROP TRIGGER IF EXISTS trg_enforce_request_status_transition ON requests;
DROP FUNCTION IF EXISTS fn_sync_approvals_required();
DROP FUNCTION IF EXISTS fn_enforce_request_status_transition();
DROP TRIGGER IF EXISTS trg_request_approvals_prevent_self_approval ON request_approvals;
DROP TRIGGER IF EXISTS trg_request_approvals_deny_update ON request_approvals;
DROP TRIGGER IF EXISTS trg_request_status_history_deny_update ON request_status_history;
DROP FUNCTION IF EXISTS fn_prevent_self_approval();
DROP FUNCTION IF EXISTS fn_deny_request_immutable_update();
DROP TRIGGER IF EXISTS trg_request_sla_violations_updated_at ON request_sla_violations;
DROP TRIGGER IF EXISTS trg_request_required_approvers_updated_at ON request_required_approvers;
DROP TRIGGER IF EXISTS trg_request_sla_config_updated_at ON request_sla_config;
DROP TRIGGER IF EXISTS trg_request_type_schemas_updated_at ON request_type_schemas;
DROP TRIGGER IF EXISTS trg_requests_updated_at ON requests;
DROP TRIGGER IF EXISTS trg_record_request_status_history ON requests;
DROP TRIGGER IF EXISTS trg_prevent_terminal_status_change ON requests;

DROP FUNCTION IF EXISTS fn_record_request_status_history();
DROP FUNCTION IF EXISTS fn_prevent_terminal_status_change();

-- +goose StatementEnd
