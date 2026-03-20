-- +goose Up
-- +goose StatementBegin

/*
 * 008_jobqueue_functions.sql — Trigger functions and triggers for job queue integrity.
 *
 * Covers:
 * fn_prevent_terminal_job_change — blocks mutations on jobs that have reached a terminal state,
 *                                  with one explicit carve-out: dead → pending is allowed so
 *                                  that RetryDeadJob can re-queue a dead job for re-execution.
 *
 * Depends on: 007_jobqueue.sql (jobs table, job_status_enum)
 */


/* ─────────────────────────────────────────────────────────────
 TERMINAL STATE PROTECTION
 ───────────────────────────────────────────────────────────── */

/*
 * Prevents updates to jobs that have reached a terminal state, with one
 * explicit carve-out: dead → pending is permitted so that RetryDeadJob can
 * re-queue a dead job for re-execution via the admin API.
 *
 * Allowed transitions from terminal states:
 *   dead      → pending    (RetryDeadJob — operator re-queues for re-execution)
 *
 * All other mutations on terminal rows (succeeded, failed, dead, cancelled)
 * are rejected with a check_violation error.
 *
 * The trigger WHEN clause pre-filters so this function only fires when:
 *   - the row is in a terminal state, AND
 *   - the transition is NOT the allowed dead → pending carve-out.
 * This keeps the hot path (non-terminal updates and the retry carve-out) cheap —
 * the function body is only entered for genuinely illegal mutations.
 *
 * Mirrors fn_prevent_terminal_status_change in 006_request_functions.sql
 * but uses job-specific terminal states, error messaging, and the retry carve-out.
 */
CREATE OR REPLACE FUNCTION fn_prevent_terminal_job_change()
RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    -- The WHEN clause on the trigger already filters to:
    --   OLD.status IN ('succeeded', 'failed', 'dead', 'cancelled')
    --   AND NOT (OLD.status = 'dead' AND NEW.status = 'pending')
    -- So if we reach here the transition is illegal.
    RAISE EXCEPTION
        'Job % is in terminal state % and cannot be updated (transition to % is not permitted).',
        OLD.id, OLD.status, NEW.status
    USING ERRCODE = 'check_violation';
END;
$$;

COMMENT ON FUNCTION fn_prevent_terminal_job_change() IS
    'Blocks any UPDATE on jobs rows that are already in a terminal state '
    '(succeeded, failed, dead, cancelled), except for the dead → pending transition '
    'used by RetryDeadJob. Terminal states other than dead are fully irreversible. '
    'Dead rows may be reset to pending by an operator via the admin retry endpoint.';

-- WHEN clause pre-filters so the function body only runs for illegal mutations:
--   - row is in any terminal state, AND
--   - the transition is NOT the allowed dead → pending carve-out.
-- Consequence: RetryDeadJob (dead → pending) bypasses the trigger entirely at
-- the Postgres level — no session variable or advisory lock needed.
CREATE TRIGGER trg_jobs_prevent_terminal_change
    BEFORE UPDATE ON jobs
    FOR EACH ROW
    WHEN (
        OLD.status IN ('succeeded', 'failed', 'dead', 'cancelled')
        AND NOT (OLD.status = 'dead' AND NEW.status = 'pending')
    )
    EXECUTE FUNCTION fn_prevent_terminal_job_change();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_jobs_prevent_terminal_change ON jobs;
DROP FUNCTION IF EXISTS fn_prevent_terminal_job_change();
-- +goose StatementEnd
