-- +goose Up
-- +goose StatementBegin

/*
 * 007_jobqueue.sql — Persistent job queue and worker registry.
 *
 * Provides a DB-backed task queue with:
 * job_paused_kinds — persistent pause registry for job kinds (survives restarts)
 * jobs — persistent job queue; workers claim rows using SELECT FOR UPDATE SKIP LOCKED
 * workers — heartbeat registry, one row per running Dispatcher instance
 * job_schedules — DB-stored cron / interval schedule definitions
 *
 * Status flow: pending → running → succeeded | failed | dead | cancelled
 * Failed jobs with attempt < max_attempts are retried via run_after update (no goroutine sleep).
 *
 * Depends on: 001_core.sql (users), 002_core_functions.sql (fn_set_updated_at, fn_deny_created_at_change)
 */


/* ─────────────────────────────────────────────────────────────
 ENUMS
 ───────────────────────────────────────────────────────────── */

-- Lifecycle states for individual job rows.
-- Terminal states: succeeded, failed, dead, cancelled.
-- Add values with ALTER TYPE … ADD VALUE; never remove a value that may be stored in existing rows.
CREATE TYPE job_status_enum AS ENUM (
 'pending', -- waiting to be claimed by a worker
 'running', -- currently being executed by a worker
 'succeeded', -- handler completed successfully
 'failed', -- handler failed; may be retried if attempt < max_attempts
 'cancelled', -- explicitly cancelled before or during execution
 'dead' -- exhausted all retry attempts; needs manual intervention
);

COMMENT ON TYPE job_status_enum IS
 'Job lifecycle states. Terminal: succeeded, failed, dead, cancelled.
 Add values with ALTER TYPE … ADD VALUE; never remove a value referenced by existing rows.';

-- Status of a Dispatcher instance as seen by the cluster.
CREATE TYPE worker_status_enum AS ENUM (
 'idle', -- running but not currently executing any jobs
 'busy', -- actively running one or more jobs
 'draining', -- shutting down gracefully; finishing current jobs but not accepting new ones
 'offline' -- heartbeat TTL expired; StallDetector marks this status
);

COMMENT ON TYPE worker_status_enum IS
 'Dispatcher instance states. offline = heartbeat TTL expired.
 Add values with ALTER TYPE … ADD VALUE.';


/* ─────────────────────────────────────────────────────────────
 PAUSED KINDS
 ───────────────────────────────────────────────────────────── */

/*
 * Persistent pause registry for job kinds. Replaces an in-memory pause map so
 * pauses survive Dispatcher restarts. A row here means all jobs of this kind
 * will be skipped by workers until the row is deleted.
 */
CREATE TABLE job_paused_kinds (
 -- Job kind name matching jobs.kind.
 kind VARCHAR(100) PRIMARY KEY,

 -- User who issued the pause. SET NULL if the user is subsequently deleted.
 paused_by UUID REFERENCES users(id) ON DELETE SET NULL,

 -- Timestamp when the pause was issued.
 paused_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Optional human-readable reason for the pause (e.g. "dependency outage").
 reason TEXT
);


/* ─────────────────────────────────────────────────────────────
 JOBS (the persistent queue)
 ───────────────────────────────────────────────────────────── */

/*
 * One row per enqueued job. Workers claim rows using SELECT … FOR UPDATE SKIP LOCKED
 * to avoid contention between concurrent worker goroutines.
 *
 * Retry behaviour: on failure the worker updates run_after to a backoff timestamp and
 * resets status to 'pending'; no goroutine sleep is needed. When attempt reaches
 * max_attempts the worker transitions to 'dead'.
 *
 * Priority: base priority (-100 to 100) is boosted at claim time by up to +50 points
 * based on how long the job has been waiting (1 point per minute, configurable) to
 * prevent low-priority starvation under sustained high load.
 *
 * Idempotency: the partial unique index uq_jobs_idempotency_key enforces
 * deduplication on active rows only, so the same key can be reused after a job
 * reaches a terminal state.
 */
CREATE TABLE jobs (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Handler type name used by the Dispatcher to route the job to the correct handler function.
 kind VARCHAR(100) NOT NULL,

 -- Arbitrary handler-specific JSON payload. Must be a JSON object.
 payload JSONB NOT NULL DEFAULT '{}',

 -- Current lifecycle state.
 status job_status_enum NOT NULL DEFAULT 'pending',

 -- Base priority for the claim queue (-100 to 100). Higher = claimed sooner.
 priority INTEGER NOT NULL DEFAULT 0,

 -- Number of times this job has been attempted. Starts at 0; incremented on each attempt.
 attempt INTEGER NOT NULL DEFAULT 0,

 -- Maximum retry attempts before the job is marked 'dead'.
 max_attempts INTEGER NOT NULL DEFAULT 5,

 -- Worker will not claim this job before this timestamp.
 -- Used for initial deferred scheduling and exponential retry backoff.
 run_after TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Maximum seconds a single attempt may run before StallDetector resets the job to 'pending'.
 timeout_seconds INTEGER NOT NULL DEFAULT 600,

 -- Routes this job to a named worker pool for priority isolation or tenant separation.
 -- 'default' = general-purpose pool.
 queue_name VARCHAR(100) NOT NULL DEFAULT 'default',

 -- Advisory reference to the worker currently running this job.
 -- No FK: workers table is defined later in this migration; FK added via ALTER TABLE below.
 -- May be stale after worker cleanup.
 worker_id UUID,

 -- User who enqueued the job (for audit). SET NULL if the user is deleted.
 created_by UUID REFERENCES users(id) ON DELETE SET NULL,

 -- Optional caller-supplied deduplication key. Enforced by the partial unique index
 -- uq_jobs_idempotency_key on active rows only.
 idempotency_key VARCHAR(255),

 -- JSON result output written by the handler on success. NULL until succeeded.
 result JSONB,

 -- Last error message from a failed attempt. Overwritten on each retry.
 last_error TEXT,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Timestamp when a worker first claimed and started this job.
 started_at TIMESTAMPTZ,

 -- Timestamp when the job reached succeeded, failed (terminal), or cancelled.
 completed_at TIMESTAMPTZ,

 -- Timestamp when the job was moved to 'dead' after exhausting retries.
 dead_at TIMESTAMPTZ,

 CONSTRAINT chk_jobs_payload_object CHECK (jsonb_typeof(payload) = 'object'),
 CONSTRAINT chk_jobs_priority_range CHECK (priority BETWEEN -100 AND 100),
 CONSTRAINT chk_jobs_max_attempts_pos CHECK (max_attempts > 0),
 CONSTRAINT chk_jobs_timeout_positive CHECK (timeout_seconds > 0),
 CONSTRAINT chk_jobs_attempt_non_neg CHECK (attempt >= 0),
 CONSTRAINT chk_jobs_attempt_within_max CHECK (attempt <= max_attempts),

 -- Status/timestamp coherence prevents silent metric calculation errors.
 -- A running job must have started_at set so duration can be computed.
 CONSTRAINT chk_jobs_running_has_started CHECK (status != 'running' OR started_at IS NOT NULL),
 -- A succeeded job must have both timestamps to compute total and run duration.
 CONSTRAINT chk_jobs_succeeded_timestamps CHECK (status != 'succeeded' OR (completed_at IS NOT NULL AND started_at IS NOT NULL)),
 -- A dead job must have dead_at set for forensic ordering.
 CONSTRAINT chk_jobs_dead_has_dead_at CHECK (status != 'dead' OR dead_at IS NOT NULL),
 -- A failed job must have started_at so StallDetector can compute how long it ran.
 CONSTRAINT chk_jobs_failed_has_started CHECK (status != 'failed' OR started_at IS NOT NULL)
);

-- Hot path for the worker claim query: pending jobs due to run, ordered by effective priority.
-- run_after leads the index so the planner can satisfy the due-time filter efficiently.
CREATE INDEX idx_jobs_claimable ON jobs(run_after, priority DESC, created_at ASC)
 WHERE status = 'pending';

-- Supports ScheduleWatcher's "what jobs are due?" scan.
CREATE INDEX idx_jobs_run_after ON jobs(run_after)
 WHERE status = 'pending';

-- Supports management API queries like "show all failed jobs of kind X".
CREATE INDEX idx_jobs_kind_status ON jobs(kind, status);

-- Supports queue-scoped worker claim queries when queue_name routing is in use.
CREATE INDEX idx_jobs_queue_status ON jobs(queue_name, status, priority DESC, created_at ASC);

-- Supports "which jobs is worker W running?" (used by StallDetector to reassign stale jobs).
CREATE INDEX idx_jobs_worker ON jobs(worker_id) WHERE worker_id IS NOT NULL;

-- Supports recency-ordered job history queries.
CREATE INDEX idx_jobs_created ON jobs(created_at DESC);

-- Dead-letter queue view: all dead jobs ordered by when they died.
CREATE INDEX idx_jobs_dead ON jobs(dead_at DESC) WHERE status = 'dead';

-- Supports the terminal-job cleanup job: time-bounded deletion of old completed/failed/cancelled rows.
CREATE INDEX idx_jobs_terminal_cleanup ON jobs(completed_at)
 WHERE status IN ('succeeded','failed','cancelled');

-- Stall detection: find jobs that have been running past their timeout.
CREATE INDEX idx_jobs_stall ON jobs(started_at)
 WHERE status = 'running';

-- Partial unique index: allows re-submission with the same idempotency_key after the job
-- reaches a terminal state. A full UNIQUE column would block re-submission forever.
CREATE UNIQUE INDEX uq_jobs_idempotency_key
 ON jobs (idempotency_key)
 WHERE idempotency_key IS NOT NULL
 AND status NOT IN ('succeeded', 'failed', 'dead', 'cancelled');

COMMENT ON TABLE jobs IS
 'Persistent job queue. Workers claim rows using SELECT FOR UPDATE SKIP LOCKED.
 Status flow: pending → running → succeeded | failed | dead | cancelled.
 Failed jobs with attempt < max_attempts are retried via run_after update (no goroutine sleep).
 Claim query uses effective_priority = priority + LEAST(minutes_waited, 50) to prevent
 low-priority job starvation under sustained high load.';
COMMENT ON COLUMN jobs.run_after IS
 'Worker will not claim this job before this timestamp. Used for retry backoff and deferred scheduling.';
COMMENT ON COLUMN jobs.timeout_seconds IS
 'If a running job exceeds this many seconds, StallDetector resets it to pending.';
COMMENT ON COLUMN jobs.queue_name IS
 'Routes this job to the named worker pool. Default = ''default''. '
 'Workers poll only their assigned queue(s). Use dedicated queues for priority '
 'isolation (e.g. ''critical'', ''bulk'') or tenant separation.';
COMMENT ON COLUMN jobs.idempotency_key IS
 'Optional caller-supplied dedup key. INSERT ... ON CONFLICT DO NOTHING returns the existing row. '
 'uniqueness is enforced by partial index uq_jobs_idempotency_key on active rows only, '
 'so the same key may be reused after a job reaches a terminal state.';
COMMENT ON COLUMN jobs.result IS
 'JSON output of the handler. NULL until the job transitions to succeeded.';
COMMENT ON COLUMN jobs.last_error IS
 'Last error message from a failed attempt. Overwritten on each retry.';
COMMENT ON COLUMN jobs.dead_at IS
 'Set when attempt = max_attempts and the job transitions to dead. NULL otherwise.';
COMMENT ON COLUMN jobs.worker_id IS
 'Advisory reference to workers.id. No FK is enforced because workers is defined after jobs. May be stale after worker cleanup.';
COMMENT ON COLUMN jobs.priority IS
 'Base priority -100 to 100. Effective priority at claim time adds up to +50 points based on
 how long the job has been waiting (1 point per minute, configurable via ManagerConfig).';


/* ─────────────────────────────────────────────────────────────
 WORKERS (heartbeat registry)
 ───────────────────────────────────────────────────────────── */

/*
 * One row per running Dispatcher instance, updated every heartbeat_ttl_seconds / 2.
 * StallDetector marks a worker 'offline' when:
 * heartbeat_at + heartbeat_ttl_seconds < NOW()
 * and then resets that worker's running jobs to 'pending'.
 *
 * Offline workers older than the retention window should be purged periodically
 * using idx_workers_stopped.
 */
CREATE TABLE workers (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Hostname of the machine running this Dispatcher instance. Used for operational identification.
 host VARCHAR(255) NOT NULL,

 -- OS process ID of the Dispatcher. Used for signal delivery and health checks.
 pid INTEGER NOT NULL,

 -- Maximum concurrent job goroutines for this Dispatcher instance.
 concurrency INTEGER NOT NULL DEFAULT 4,

 -- Current operational state of this Dispatcher.
 status worker_status_enum NOT NULL DEFAULT 'idle',

 -- Timestamp of the most recent heartbeat. Used by StallDetector to detect dead workers.
 heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Heartbeat interval in seconds. StallDetector considers the worker offline when
 -- heartbeat_at + heartbeat_ttl_seconds < NOW().
 heartbeat_ttl_seconds INTEGER NOT NULL DEFAULT 30,

 started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Set when the Dispatcher gracefully shuts down. NULL for running workers.
 stopped_at TIMESTAMPTZ,

 -- Runtime counters; reset to 0 each time the Dispatcher process restarts.
 jobs_succeeded INTEGER NOT NULL DEFAULT 0,
 jobs_failed INTEGER NOT NULL DEFAULT 0,
 jobs_dead INTEGER NOT NULL DEFAULT 0,

 CONSTRAINT chk_workers_concurrency_pos CHECK (concurrency > 0),
 CONSTRAINT chk_workers_heartbeat_ttl_pos CHECK (heartbeat_ttl_seconds > 0)
);

COMMENT ON COLUMN workers.host IS
 'Hostname of the machine running this Dispatcher instance.';
COMMENT ON COLUMN workers.pid IS
 'OS process ID of the Dispatcher. Used for signal delivery and process health checks.';
COMMENT ON COLUMN workers.concurrency IS
 'Maximum concurrent job goroutines for this Dispatcher instance.';
COMMENT ON COLUMN workers.heartbeat_ttl_seconds IS
 'Heartbeat interval in seconds. StallDetector marks this worker offline when heartbeat_at + heartbeat_ttl_seconds < NOW().';
COMMENT ON COLUMN workers.jobs_succeeded IS 'Runtime counter. Resets to 0 on Dispatcher restart.';
COMMENT ON COLUMN workers.jobs_failed IS 'Runtime counter. Resets to 0 on Dispatcher restart.';
COMMENT ON COLUMN workers.jobs_dead IS 'Runtime counter. Resets to 0 on Dispatcher restart.';

-- Supports the "list active workers" management API query; excludes offline workers.
CREATE INDEX idx_workers_active ON workers(heartbeat_at DESC)
 WHERE status != 'offline';

-- Supports the periodic purge of old offline worker rows.
CREATE INDEX idx_workers_stopped ON workers(stopped_at)
 WHERE status = 'offline';

COMMENT ON TABLE workers IS
 'One row per running Dispatcher instance. Updated every heartbeat_ttl_seconds/2.
 StallDetector marks rows offline when heartbeat_at + heartbeat_ttl_seconds < NOW().
 Stalled workers'' running jobs are reset to pending.
 Offline workers older than the retention window should be purged periodically; use idx_workers_stopped.';


/* ─────────────────────────────────────────────────────────────
 JOB SCHEDULES (cron / interval)
 ───────────────────────────────────────────────────────────── */

/*
 * DB-stored schedule definitions replacing an in-memory Scheduler.
 * ScheduleWatcher polls every 10 seconds, inserts job rows for due entries,
 * and updates next_run_at. Zero goroutines per schedule.
 *
 * Multi-instance safe: ScheduleWatcher uses FOR UPDATE SKIP LOCKED on the
 * due-schedule query so only one instance enqueues each schedule.
 *
 * Exactly one of cron_expression or interval_seconds must be set per row;
 * enforced by chk_js_schedule_type.
 */
CREATE TABLE job_schedules (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Unique human-readable name for this schedule (e.g. 'daily_report', 'hourly_cleanup').
 name VARCHAR(100) UNIQUE NOT NULL,

 -- Job kind to enqueue when this schedule fires; matches jobs.kind.
 kind VARCHAR(100) NOT NULL,

 -- Queue to route the enqueued job to.
 queue_name VARCHAR(100) NOT NULL DEFAULT 'default',

 -- Standard 5-field cron expression (e.g. "0 * * * *" for hourly).
 -- Exactly one of cron_expression or interval_seconds must be non-NULL.
 cron_expression VARCHAR(100),

 -- Simple repeat interval in seconds (e.g. 3600 for hourly).
 -- Exactly one of cron_expression or interval_seconds must be non-NULL.
 interval_seconds INTEGER,

 -- JSON object merged into the enqueued job's payload.
 payload_template JSONB NOT NULL DEFAULT '{}',

 -- Priority for enqueued jobs from this schedule.
 priority INTEGER NOT NULL DEFAULT 0,

 -- Max retry attempts for jobs created from this schedule.
 max_attempts INTEGER NOT NULL DEFAULT 5,

 -- Timeout in seconds for jobs created from this schedule.
 timeout_seconds INTEGER NOT NULL DEFAULT 600,

 -- TRUE = do not enqueue a new job if one of this kind is already pending or running.
 -- Prevents job pile-up when a schedule fires faster than jobs complete.
 skip_if_running BOOLEAN NOT NULL DEFAULT FALSE,

 -- FALSE = schedule is paused; ScheduleWatcher will not enqueue new jobs.
 is_active BOOLEAN NOT NULL DEFAULT TRUE,

 -- Timestamp of the last successful job enqueue. NULL until first enqueue.
 last_enqueued_at TIMESTAMPTZ,

 -- Next computed run time. NULL until first poll after schedule creation.
 -- Updated by ScheduleWatcher after each enqueue using the cron parser or interval arithmetic.
 next_run_at TIMESTAMPTZ,

 -- Last error encountered during schedule processing (parse failure, enqueue error, etc.).
 -- NULL when the most recent poll succeeded. Used for ops alerting.
 last_schedule_error TEXT,

 -- User who created this schedule. SET NULL if the user is deleted.
 created_by UUID REFERENCES users(id) ON DELETE SET NULL,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Exactly one scheduling mechanism must be specified.
 CONSTRAINT chk_js_schedule_type CHECK (
 (cron_expression IS NOT NULL AND interval_seconds IS NULL) OR
 (cron_expression IS NULL AND interval_seconds IS NOT NULL)
 ),
 CONSTRAINT chk_js_interval_positive CHECK (interval_seconds IS NULL OR interval_seconds > 0),
 CONSTRAINT chk_js_payload_object CHECK (jsonb_typeof(payload_template) = 'object'),
 CONSTRAINT chk_js_max_attempts_pos CHECK (max_attempts > 0),
 CONSTRAINT chk_js_timeout_positive CHECK (timeout_seconds > 0),
 CONSTRAINT chk_js_priority_range CHECK (priority BETWEEN -100 AND 100)
);

-- Hot path for ScheduleWatcher: find active schedules whose next_run_at is in the past.
CREATE INDEX idx_job_schedules_due ON job_schedules(next_run_at) WHERE is_active = TRUE;

-- Supports "list all schedules for kind X" management queries.
CREATE INDEX idx_job_schedules_kind ON job_schedules(kind);

COMMENT ON COLUMN job_schedules.next_run_at IS
 'Next scheduled execution time. Computed by ScheduleWatcher after each successful enqueue. '
 'NULL until the first poll after the schedule is created.';
COMMENT ON COLUMN job_schedules.last_schedule_error IS
 'Last error encountered during schedule processing (cron parse failure, enqueue error, etc.). '
 'NULL when the most recent poll succeeded. Used for ops alerting and debugging.';
COMMENT ON COLUMN job_schedules.last_enqueued_at IS
 'Timestamp of the last successful job enqueue for this schedule. NULL until first enqueue.';
COMMENT ON COLUMN job_schedules.skip_if_running IS
 'TRUE = do not enqueue a new job if a job of this kind is already pending or running. '
 'Prevents job pile-up when a schedule fires faster than jobs complete.';

COMMENT ON TABLE job_schedules IS
 'DB-stored cron / interval schedule definitions. ScheduleWatcher polls every 10s,
 inserts job rows for due entries, and updates next_run_at. Zero goroutines per schedule.
 Multi-instance safe via FOR UPDATE SKIP LOCKED on the due-schedule query.
 next_run_at computed by robfig/cron parser for cron_expression, or NOW() + interval_seconds.';

COMMENT ON TABLE job_paused_kinds IS
 'Persistent pause registry for job kinds. Replaces the in-memory pause map —
 survives Dispatcher restarts. A row here means all jobs of that kind are paused.';
COMMENT ON COLUMN job_paused_kinds.paused_by IS
 'User who paused this kind. NULL if the user was subsequently deleted.';
COMMENT ON COLUMN job_paused_kinds.reason IS
 'Human-readable reason for the pause.';

-- Referential integrity for jobs.worker_id: workers is defined after jobs, so the FK is added here.
-- ON DELETE SET NULL: removing a stale worker row nulls the advisory reference on its jobs.
ALTER TABLE jobs
 ADD CONSTRAINT fk_jobs_worker FOREIGN KEY (worker_id) REFERENCES workers(id) ON DELETE SET NULL;


/* ─────────────────────────────────────────────────────────────
 updated_at TRIGGERS
 ───────────────────────────────────────────────────────────── */

-- Keeps jobs.updated_at current on every mutation (status change, retry backoff update, etc.).
CREATE TRIGGER trg_jobs_updated_at
 BEFORE UPDATE ON jobs
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- Keeps job_schedules.updated_at current when schedule parameters change.
CREATE TRIGGER trg_job_schedules_updated_at
 BEFORE UPDATE ON job_schedules
 FOR EACH ROW EXECUTE FUNCTION fn_set_updated_at();

-- jobs.created_at is semantically write-once; reuses fn_deny_created_at_change from 002.
CREATE TRIGGER trg_jobs_deny_created_at_change
 BEFORE UPDATE OF created_at ON jobs
 FOR EACH ROW EXECUTE FUNCTION fn_deny_created_at_change();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE IF EXISTS jobs DROP CONSTRAINT IF EXISTS fk_jobs_worker;
DROP TRIGGER IF EXISTS trg_jobs_deny_created_at_change ON jobs;
DROP TRIGGER IF EXISTS trg_job_schedules_updated_at ON job_schedules;
DROP TRIGGER IF EXISTS trg_jobs_updated_at ON jobs;
DROP INDEX IF EXISTS uq_jobs_idempotency_key;
DROP TABLE IF EXISTS job_schedules CASCADE;
DROP TABLE IF EXISTS workers CASCADE;
DROP TABLE IF EXISTS jobs CASCADE;
DROP TABLE IF EXISTS job_paused_kinds CASCADE;
DROP TYPE IF EXISTS worker_status_enum CASCADE;
DROP TYPE IF EXISTS job_status_enum CASCADE;
-- +goose StatementEnd
