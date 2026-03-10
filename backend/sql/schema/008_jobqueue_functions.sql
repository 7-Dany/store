-- +goose Up
-- +goose StatementBegin

/*
 * 008_jobqueue_functions.sql — Trigger functions and triggers for job queue integrity.
 *
 * Covers:
 * fn_prevent_terminal_job_change — blocks mutations on jobs that have reached a terminal state
 *
 * Depends on: 007_jobqueue.sql (jobs table, job_status_enum)
 */


/* ─────────────────────────────────────────────────────────────
 TERMINAL STATE PROTECTION
 ───────────────────────────────────────────────────────────── */

/*
 * Prevents updates to jobs that have reached a terminal state.
 * Terminal states (succeeded, failed, dead, cancelled) are irreversible;
 * any attempted mutation is rejected with a check_violation error.
 *
 * The trigger WHEN clause pre-filters so this function only fires for rows
 * already in a terminal state, keeping the hot path (non-terminal updates) cheap.
 *
 * Mirrors fn_prevent_terminal_status_change in 006_request_functions.sql
 * but uses job-specific terminal states and error messaging.
 */
CREATE OR REPLACE FUNCTION fn_prevent_terminal_job_change()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status IN ('succeeded', 'failed', 'dead', 'cancelled') THEN
  RAISE EXCEPTION
   'Job % is in terminal state % and cannot be updated.',
   OLD.id, OLD.status
  USING ERRCODE = 'check_violation';
 END IF;
 RETURN NEW;
END;
$$;

COMMENT ON FUNCTION fn_prevent_terminal_job_change() IS
 'Blocks any UPDATE on jobs rows that are already in a terminal state '
 '(succeeded, failed, dead, cancelled). Terminal states are irreversible.';

-- WHEN clause pre-filters: only fires for rows already in a terminal state.
CREATE TRIGGER trg_jobs_prevent_terminal_change
 BEFORE UPDATE ON jobs
 FOR EACH ROW
 WHEN (OLD.status IN ('succeeded', 'failed', 'dead', 'cancelled'))
 EXECUTE FUNCTION fn_prevent_terminal_job_change();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_jobs_prevent_terminal_change ON jobs;
DROP FUNCTION IF EXISTS fn_prevent_terminal_job_change();
-- +goose StatementEnd
