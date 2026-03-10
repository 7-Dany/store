# Job Queue — Stage 0: Design & Decisions (v5)

**Feature:** General-purpose async job dispatcher, scheduler, and management API
**Target package:** `internal/platform/jobqueue/` (package `jobqueue`)
**Status:** Stage 0 — final design; approved for implementation

---

## Changelog

| Version | Summary |
|---------|---------|
| v1 | In-memory channel, per-goroutine scheduler, in-memory dead-letter ring buffer |
| v2 | PostgreSQL-backed queue, SKIP LOCKED claim, Postgres LISTEN/NOTIFY, REST + WebSocket admin |
| v3 | Redis pub/sub replaces LISTEN/NOTIFY; request_executions and notification delivery collapsed into jobs; 10s schedule poll; daily job table cleanup |
| v4 | Priority starvation fixed via SQL aging; WSHub backpressure via buffered channels; Redis downtime handling fully specified; three known ceilings documented |
| **v5** | `GET /metrics` promoted to V1; `MetricsRecorder` interface added — swap query-based metrics for Prometheus in-process counters without touching any caller code |

---

## Motivation

The V1 design established a clean platform primitive with a flat worker pool, inline retry,
and in-memory scheduler. That foundation is sound, but gaps emerged as the project grew:

1. **Ephemeral queue** — jobs disappear on restart; a crash during a batch run loses work.
2. **No observability** — no way to inspect queue depth, worker state, or failed jobs without
   attaching a debugger or reading logs.
3. **Memory-coupled scheduling** — one goroutine per `ScheduleEntry`; scaling to dozens of
   scheduled tasks burns goroutines unnecessarily.
4. **Duplicate retry systems** — `request_executions` and `request_notifications` each
   implemented their own retry counters, stall detection, and error tracking — a third system
   alongside the job queue would have been incoherent.

This design replaces the in-memory `chan Job` and `InMemoryDeadLetterStore` with a
**PostgreSQL-backed engine** using `SELECT FOR UPDATE SKIP LOCKED` for atomic job claiming
and **Redis pub/sub** (`PUBLISH`/`SUBSCRIBE`) for instant worker wake-up. Redis is already
in the stack (`go-redis/v9`) for rate limiting and token blocklisting — pub/sub adds zero
memory overhead (messages are ephemeral, never stored). A REST + WebSocket management layer
is added on top.

The `internal/platform/jobqueue` package remains a pure platform primitive — it imports
nothing from `internal/domain`, `internal/db`, or `internal/worker`.

---

## 1. Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│  HTTP / Domain layer  (deps.Jobs.Submit)                        │
└───────────────┬─────────────────────────────────────────────────┘
                │ INSERT INTO jobs (kind, payload, priority, ...)
                ▼
┌─────────────────────────────────────────────────────────────────┐
│  PostgreSQL  (source of truth for ALL job state)                │
│  ┌──────────┐  ┌──────────────┐  ┌────────────┐  ┌──────────┐  │
│  │  jobs    │  │ job_schedules│  │  workers   │  │paused_   │  │
│  │ (queue)  │  │ (cron/intrvl)│  │ (heartbeat)│  │kinds     │  │
│  └──────────┘  └──────────────┘  └────────────┘  └──────────┘  │
└──────────────────────┬──────────────────────────────────────────┘
                       │ SELECT FOR UPDATE SKIP LOCKED
                       ▼
┌─────────────────────────────────────────────────────────────────┐
│  Redis  (wake signal only — zero stored state)                  │
│  PUBLISH  jobqueue:notify  ←── on INSERT / schedule fire        │
│  SUBSCRIBE jobqueue:notify ←── each worker goroutine            │
└──────────────────────┬──────────────────────────────────────────┘
                       │ wake or 10s fallback poll
                       ▼
┌─────────────────────────────────────────────────────────────────┐
│  Manager  (internal/platform/jobqueue)                          │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  Dispatcher — N worker goroutines                        │   │
│  │  Each: SUBSCRIBE wake or 10s poll (Redis fallback)       │   │
│  │  → ClaimJob (SKIP LOCKED) → execute → update status      │   │
│  └──────────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  ScheduleWatcher — 1 goroutine for ALL schedules         │   │
│  │  → SELECT due rows FOR UPDATE SKIP LOCKED (every 10s)   │   │
│  │  → INSERT job rows → PUBLISH → UPDATE next_run_at       │   │
│  └──────────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  StallDetector — 1 goroutine (every 30s)                 │   │
│  │  → find running jobs past timeout → reset to pending     │   │
│  └──────────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  AdminRouter (chi.Router) — REST + WebSocket             │   │
│  │  Mounted at /admin/jobqueue by server.go                 │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### How workers claim jobs (SKIP LOCKED with priority aging)

```sql
-- Run by each worker goroutine when it needs work.
-- effective_priority ages the job +1 point per minute waited, capped at +50.
-- This prevents low-priority jobs from starving indefinitely under high load.
WITH claimed AS (
    SELECT id,
           priority + LEAST(
               FLOOR(EXTRACT(EPOCH FROM (NOW() - created_at)) / 60)::int,
               50
           ) AS effective_priority
    FROM   jobs
    WHERE  status    = 'pending'
      AND  kind      NOT IN (SELECT kind FROM job_paused_kinds)
      AND  run_after <= NOW()
    ORDER BY effective_priority DESC, created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED   -- atomic claim; other workers skip this row
)
UPDATE jobs
   SET status     = 'running',
       started_at = NOW(),
       worker_id  = $1,
       attempt    = attempt + 1
 WHERE id = (SELECT id FROM claimed)
RETURNING *;

-- On INSERT (or schedule fire), app code publishes:
-- redis.Publish(ctx, "jobqueue:notify", "")
-- Workers subscribed to this channel wake in < 1ms.
-- 10s Postgres poll acts as fallback when Redis is unavailable.
```

**Priority aging rationale:** A `priority=0` maintenance job waiting 150 minutes reaches
`effective_priority=50` and will beat any freshly submitted `priority=49` job. It will
never beat a fresh `priority=51+` job — high-priority work still wins. The aging rate
(default: 1 point/min) and cap (default: 50 points) are tunable via `ManagerConfig`.

### Redis pub/sub — zero memory overhead

`PUBLISH` messages are not stored in Redis. They fire to current subscribers and evaporate.
Memory cost of the `jobqueue:notify` channel: ~64 bytes × number of active subscribers
(worker goroutines). At 2 instances × 4 workers = 8 goroutines ≈ **512 bytes total**.
No TTL, no key, no cleanup needed. This is explicitly different from using Redis as a queue
(List, Stream, Sorted Set) which would store messages and consume memory proportional to
queue depth.

---

## 2. Redis Downtime Handling

Redis is the doorbell, not the safe. All job state lives in Postgres. If Redis goes down,
workers fall back to a Postgres poll and jobs continue to run.

### Three scenarios

| Scenario | What happens | Max extra latency |
|----------|-------------|-------------------|
| Redis blip (<1s) | go-redis reconnects internally; Subscribe channel stays open; ticker catches missed signals | ≤10s |
| Redis down (minutes) | Subscribe channel goes quiet; 10s ticker fires independently; workers keep claiming via Postgres poll | ≤10s per job |
| Redis down at startup | go-redis dials with retries; ticker fires immediately so workers poll from second 0 | ≤10s |

### Dispatcher loop — explicit Redis-down handling

```go
// dispatcher.go — per-worker goroutine inner loop

func (d *Dispatcher) workerLoop(ctx context.Context, workerID uuid.UUID) {
    // Ticker is the safety net — always running regardless of Redis state.
    ticker := time.NewTicker(d.cfg.RedisFallbackPoll) // default: 10s
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return

        case _, ok := <-d.notify:
            // d.notify is the channel from go-redis Subscribe.
            // ok=false means Manager is shutting down (not Redis downtime).
            // During Redis downtime go-redis pauses delivery but keeps channel open.
            if !ok {
                return
            }
            d.tryClaimAndRun(ctx, workerID)

        case <-ticker.C:
            // Fires every RedisFallbackPoll regardless of Redis state.
            // This is the only path that runs when Redis is down.
            // Also catches any notifications missed during the reconnect window.
            d.tryClaimAndRun(ctx, workerID)
        }
    }
}
```

### ScheduleWatcher during Redis downtime

ScheduleWatcher polls Postgres every 10s independently — it never blocks on Redis.
After inserting a job it calls `PUBLISH`. If Redis is down, that publish fails — but the
job row is already safely in Postgres. Workers will claim it on their next poll cycle.
The publish failure is logged at `WARN` level and never prevents the schedule from firing.

```go
// scheduler.go — after inserting the job row:
if err := d.pubsub.Publish(ctx, "jobqueue:notify", ""); err != nil {
    // Redis is down. Job row is already in Postgres — workers will find it on next poll.
    d.logger.Warn("notify publish failed, workers will poll", "err", err)
}
```

### WebSocket during Redis downtime

The WSHub uses Redis pub/sub to broadcast events to admin clients. During a Redis outage,
real-time WebSocket events stop — connected admin dashboards go stale. This is an acceptable
graceful degradation: the REST API (`GET /jobs`, `GET /stats`, `GET /workers`) hits Postgres
directly and remains fully operational. Job execution is unaffected.

---

## 3. Schema Consolidation

Before this design, three separate systems implemented retry, stall detection, and error
tracking redundantly:

| System | Columns that duplicated job queue concerns |
|--------|------------------------------------------|
| `request_executions` | `status` (pending/in_progress/retrying/failed/success), `retry_count`, `idempotency_key`, `error_message`, stalled-job index |
| `request_notifications` | `delivery_attempts`, `last_attempt_at`, `delivery_error`, retry-candidate index |
| `jobqueue` (new) | All of the above, done once and correctly |

**Resolution:**
- `request_executions` is **dropped** in `006_jobqueue.sql`. Approved requests now submit a
  job of `kind = "execute_request"`. The job queue's `attempt`, `last_error`, `max_attempts`,
  `idempotency_key`, and dead-letter mechanism cover everything the old table did.
- `request_notifications` **delivery retry columns are dropped**. A `send_notification` job
  handles delivery with retry. The `request_notifications` table keeps only the inbox record
  (`read_at`, `title`, `message`) — not the delivery machinery.

---

## 4. New SQL Schema — `006_jobqueue.sql`

Depends on: `001_core.sql` (users).

```sql
-- +goose Up
-- +goose StatementBegin

-- ------------------------------------------------------------
-- PAUSED KINDS  (replaces in-memory pause map; survives restart)
-- ------------------------------------------------------------
CREATE TABLE job_paused_kinds (
    kind       VARCHAR(100) PRIMARY KEY,
    paused_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    paused_at  TIMESTAMPTZ DEFAULT NOW(),
    reason     TEXT
);

-- ------------------------------------------------------------
-- JOBS  (the persistent queue)
-- ------------------------------------------------------------
CREATE TABLE jobs (
    id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind    VARCHAR(100) NOT NULL,
    payload JSONB        NOT NULL DEFAULT '{}',

    -- Lifecycle
    status   VARCHAR(20) NOT NULL DEFAULT 'pending'
             CHECK (status IN ('pending','running','succeeded','failed','cancelled','dead')),

    priority     INTEGER NOT NULL DEFAULT 0,
    attempt      INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,

    -- Scheduling: worker will not claim before run_after
    run_after       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    timeout_seconds INTEGER     NOT NULL DEFAULT 600,

    -- Routing
    queue_name VARCHAR(100) NOT NULL DEFAULT 'default',

    -- Traceability
    worker_id       UUID,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    idempotency_key VARCHAR(255) UNIQUE,

    -- Results
    result     JSONB,
    last_error TEXT,

    -- Timestamps
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    dead_at      TIMESTAMPTZ,

    CONSTRAINT chk_jobs_payload_object   CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT chk_jobs_priority_range   CHECK (priority BETWEEN -100 AND 100),
    CONSTRAINT chk_jobs_max_attempts_pos CHECK (max_attempts > 0)
);

-- Worker claim query: pending, due, not paused.
-- effective_priority computed at query time — no stored column needed.
CREATE INDEX idx_jobs_claimable    ON jobs(priority DESC, created_at ASC)
    WHERE status = 'pending';
-- Scheduled jobs: what is due?
CREATE INDEX idx_jobs_run_after    ON jobs(run_after)
    WHERE status = 'pending';
-- Management API queries
CREATE INDEX idx_jobs_kind_status  ON jobs(kind, status);
CREATE INDEX idx_jobs_queue_status ON jobs(queue_name, status, priority DESC, created_at ASC);
CREATE INDEX idx_jobs_worker       ON jobs(worker_id) WHERE worker_id IS NOT NULL;
CREATE INDEX idx_jobs_created      ON jobs(created_at DESC);
-- Dead-letter view
CREATE INDEX idx_jobs_dead         ON jobs(dead_at DESC) WHERE status = 'dead';
-- Stall detection
CREATE INDEX idx_jobs_stall        ON jobs(started_at)
    WHERE status = 'running';

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
COMMENT ON COLUMN jobs.idempotency_key IS
    'Optional caller-supplied dedup key. INSERT … ON CONFLICT DO NOTHING returns the existing row.';
COMMENT ON COLUMN jobs.priority IS
    'Base priority -100 to 100. Effective priority at claim time adds up to +50 points based on
     how long the job has been waiting (1 point per minute, configurable via ManagerConfig).';

-- ------------------------------------------------------------
-- WORKERS  (heartbeat registry — one row per running Dispatcher)
-- ------------------------------------------------------------
CREATE TABLE workers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    host        VARCHAR(255) NOT NULL,
    pid         INTEGER      NOT NULL,
    concurrency INTEGER      NOT NULL DEFAULT 4,

    status VARCHAR(20) NOT NULL DEFAULT 'idle'
           CHECK (status IN ('idle','busy','draining','offline')),

    heartbeat_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    heartbeat_ttl_seconds INTEGER     NOT NULL DEFAULT 30,

    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    stopped_at TIMESTAMPTZ,

    -- Runtime counters (reset on restart)
    jobs_succeeded INTEGER NOT NULL DEFAULT 0,
    jobs_failed    INTEGER NOT NULL DEFAULT 0,
    jobs_dead      INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_workers_active ON workers(heartbeat_at DESC)
    WHERE status != 'offline';

COMMENT ON TABLE workers IS
    'One row per running Dispatcher instance. Updated every heartbeat_ttl_seconds/2.
     StallDetector marks rows offline when heartbeat_at + heartbeat_ttl_seconds < NOW().
     Stalled workers'' running jobs are reset to pending.';

-- ------------------------------------------------------------
-- JOB SCHEDULES  (cron / interval — replaces in-memory Scheduler)
-- ------------------------------------------------------------
CREATE TABLE job_schedules (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(100) UNIQUE NOT NULL,
    kind       VARCHAR(100) NOT NULL,
    queue_name VARCHAR(100) NOT NULL DEFAULT 'default',

    -- Exactly one must be non-null (enforced by check constraint below)
    cron_expression  VARCHAR(100),   -- standard 5-field cron, e.g. "0 * * * *"
    interval_seconds INTEGER,        -- simple interval, e.g. 3600

    payload_template JSONB   NOT NULL DEFAULT '{}',
    priority         INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 5,
    timeout_seconds  INTEGER NOT NULL DEFAULT 600,

    -- Skip inserting a new job if one of this kind is already pending/running
    skip_if_running BOOLEAN NOT NULL DEFAULT FALSE,

    is_active BOOLEAN NOT NULL DEFAULT TRUE,

    last_enqueued_at TIMESTAMPTZ,
    next_run_at      TIMESTAMPTZ,

    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_js_schedule_type CHECK (
        (cron_expression IS NOT NULL AND interval_seconds IS NULL) OR
        (cron_expression IS NULL     AND interval_seconds IS NOT NULL)
    ),
    CONSTRAINT chk_js_interval_positive  CHECK (interval_seconds IS NULL OR interval_seconds > 0),
    CONSTRAINT chk_js_payload_object     CHECK (jsonb_typeof(payload_template) = 'object')
);

CREATE INDEX idx_job_schedules_due    ON job_schedules(next_run_at) WHERE is_active = TRUE;
CREATE INDEX idx_job_schedules_kind   ON job_schedules(kind);
CREATE INDEX idx_job_schedules_active ON job_schedules(is_active) WHERE is_active = TRUE;

COMMENT ON TABLE job_schedules IS
    'DB-stored cron / interval schedule definitions. ScheduleWatcher polls every 10s,
     inserts job rows for due entries, and updates next_run_at. Zero goroutines per schedule.
     Multi-instance safe via FOR UPDATE SKIP LOCKED on the due-schedule query.
     next_run_at computed by robfig/cron parser for cron_expression, or NOW() + interval_seconds.';

-- ------------------------------------------------------------
-- SCHEMA CONSOLIDATION
-- ------------------------------------------------------------
-- request_executions is fully replaced by jobs with kind='execute_request'.
-- Its retry_count, idempotency_key, status, and error_message are now handled
-- by the jobs table. Approved requests submit a job instead of executing inline.
DROP TABLE IF EXISTS request_executions CASCADE;
DROP TYPE  IF EXISTS execution_status_enum CASCADE;

-- request_notifications keeps its inbox record (read_at, title, message) but
-- loses the delivery retry machinery — now handled by kind='send_notification' jobs.
ALTER TABLE request_notifications
    DROP COLUMN IF EXISTS delivery_attempts,
    DROP COLUMN IF EXISTS last_attempt_at,
    DROP COLUMN IF EXISTS delivery_error;

DROP INDEX IF EXISTS idx_request_notifications_failed;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE request_notifications
    ADD COLUMN IF NOT EXISTS delivery_attempts INTEGER DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_attempt_at   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delivery_error    TEXT;

CREATE INDEX IF NOT EXISTS idx_request_notifications_failed
    ON request_notifications(delivery_attempts, last_attempt_at DESC)
    WHERE delivery_error IS NOT NULL;

CREATE TYPE execution_status_enum AS ENUM (
    'pending', 'in_progress', 'success', 'failed', 'retrying'
);

CREATE TABLE IF NOT EXISTS request_executions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id       UUID REFERENCES requests(id) ON DELETE CASCADE,
    idempotency_key  UUID UNIQUE,
    executed_action  VARCHAR(100) NOT NULL,
    execution_result JSONB,
    status           execution_status_enum NOT NULL DEFAULT 'pending',
    error_message    TEXT,
    retry_count      INTEGER DEFAULT 0,
    executed_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    started_at       TIMESTAMPTZ DEFAULT NOW(),
    completed_at     TIMESTAMPTZ
);

DROP TABLE IF EXISTS job_schedules    CASCADE;
DROP TABLE IF EXISTS workers          CASCADE;
DROP TABLE IF EXISTS jobs             CASCADE;
DROP TABLE IF EXISTS job_paused_kinds CASCADE;
-- +goose StatementEnd
```

---

## 5. Package Structure

```
internal/platform/jobqueue/
    job.go          — Job, Kind, Status, Handler, HandlerFunc, Submitter,
                      SubmitRequest, WorkerInfo, Schedule, PausedKind,
                      PubSub interface, MetricsRecorder interface
    store.go        — JobStore interface + pgJobStore (all SQL including aging claim query)
    dispatcher.go   — Dispatcher: Redis SUBSCRIBE wake + 10s fallback poll,
                      SKIP LOCKED claim, N worker goroutines, heartbeat
    scheduler.go    — ScheduleWatcher: single goroutine, 10s poll, cron + interval support
    deadletter.go   — DB-backed dead-letter queries (JobStore methods)
    metrics.go      — QueryMetricsRecorder (V1 default) + NoopMetricsRecorder (tests)
    api.go          — chi.Router factory: REST admin endpoints (20 routes incl. /metrics)
    ws.go           — WSHub: per-client buffered channels (64 events), writePump goroutine
    manager.go      — Manager: composes all components; satisfies Submitter

internal/worker/
    kinds.go              — Kind constants
    purge.go              — PurgeHandler (existing, signature updated: Job not any)
    execute_request.go    — NEW: replaces request_executions inline execution
    send_notification.go  — NEW: replaces request_notifications delivery retry
    purge_completed.go    — NEW: daily cleanup of succeeded/cancelled jobs
```

**Files NOT needed:**
- No `errors.go` — inline sentinels per file.
- No `models.go` — types live in `job.go`.

---

## 6. Detailed Type Contracts

### `job.go`

```go
type Kind   string
type Status string

const (
    StatusPending   Status = "pending"
    StatusRunning   Status = "running"
    StatusSucceeded Status = "succeeded"
    StatusFailed    Status = "failed"
    StatusCancelled Status = "cancelled"
    StatusDead      Status = "dead"
)

// Job is a unit of work read from the jobs table.
type Job struct {
    ID             uuid.UUID
    Kind           Kind
    QueueName      string
    Payload        json.RawMessage
    Status         Status
    Priority       int
    Attempt        int
    MaxAttempts    int
    RunAfter       time.Time
    TimeoutSeconds int
    WorkerID       *uuid.UUID
    CreatedBy      *uuid.UUID
    IdempotencyKey *string
    Result         json.RawMessage
    LastError      string
    CreatedAt      time.Time
    UpdatedAt      time.Time
    StartedAt      *time.Time
    CompletedAt    *time.Time
    DeadAt         *time.Time
}

// Handler processes a single job. Must be safe for concurrent use.
type Handler interface {
    Handle(ctx context.Context, job Job) error
}

type HandlerFunc func(ctx context.Context, job Job) error
func (f HandlerFunc) Handle(ctx context.Context, job Job) error { return f(ctx, job) }

// Submitter is the narrow interface domain code and tests use.
// Manager satisfies it.
type Submitter interface {
    Submit(ctx context.Context, req SubmitRequest) (*Job, error)
}

type SubmitRequest struct {
    Kind           Kind
    QueueName      string     // default: "default"
    Payload        any
    Priority       int        // -100 to 100; default 0
    RunAfter       *time.Time // nil = run immediately
    MaxAttempts    int        // 0 = use manager default (5)
    TimeoutSeconds int        // 0 = use manager default (600)
    IdempotencyKey *string    // nil = no dedup
    CreatedBy      *uuid.UUID
}

// PubSub is the narrow interface the jobqueue package uses for Redis.
// RedisStore in internal/platform/kvstore satisfies this after adding
// Publish and Subscribe methods. This keeps jobqueue decoupled from go-redis.
type PubSub interface {
    Publish(ctx context.Context, channel, message string) error
    Subscribe(ctx context.Context, channel string) (<-chan string, error)
}

// MetricsRecorder is the narrow observability interface injected into Manager.
// Every component (Dispatcher, ScheduleWatcher, StallDetector, Manager) calls
// it on significant events. Swap implementations in ManagerConfig.Metrics —
// nothing else in the codebase changes.
//
// V1 ships QueryMetricsRecorder: event hooks are no-ops; GET /metrics runs a
// Postgres query on each scrape. Zero new dependencies, works immediately.
//
// When a Prometheus stack exists, swap to PrometheusMetricsRecorder: event
// hooks increment in-process counters/histograms; GET /metrics delegates to
// promhttp.Handler(). One line in server.New, nothing else touches.
//
// All implementations must be safe for concurrent use. Hooks must not block.
type MetricsRecorder interface {
    // Dispatcher hooks
    OnJobSubmitted(job Job)
    OnJobClaimed(job Job)
    OnJobSucceeded(job Job, duration time.Duration)
    OnJobFailed(job Job, err error, willRetry bool)
    OnJobDead(job Job)
    OnJobCancelled(job Job)

    // ScheduleWatcher hook
    OnScheduleFired(scheduleID uuid.UUID, kind Kind)

    // StallDetector hook
    OnJobsRequeued(count int)

    // MetricsHandler returns an http.Handler served at GET /metrics.
    // QueryMetricsRecorder runs a GetStats() SQL query on each scrape and
    // formats the result as Prometheus text exposition.
    // PrometheusMetricsRecorder returns promhttp.HandlerFor(registry, ...).
    MetricsHandler() http.Handler
}
```

### `store.go`

```go
type JobStore interface {
    // ── Worker operations ────────────────────────────────────────────────────
    // ClaimJob uses effective_priority = priority + LEAST(minutes_waited, AgingCap).
    ClaimJob(ctx context.Context, workerID uuid.UUID, queues []string) (*Job, error)
    CompleteJob(ctx context.Context, id uuid.UUID, result any) error
    FailJob(ctx context.Context, id uuid.UUID, err error, retryAt *time.Time) error
    DeadLetterJob(ctx context.Context, id uuid.UUID, err error) error

    // ── Submit ───────────────────────────────────────────────────────────────
    InsertJob(ctx context.Context, req SubmitRequest) (*Job, error)
    CancelJob(ctx context.Context, id uuid.UUID) error

    // ── Pause / Resume ───────────────────────────────────────────────────────
    PauseKind(ctx context.Context, kind Kind, by uuid.UUID, reason string) error
    ResumeKind(ctx context.Context, kind Kind) error
    ListPausedKinds(ctx context.Context) ([]PausedKind, error)

    // ── Management API ───────────────────────────────────────────────────────
    ListJobs(ctx context.Context, f JobFilter) ([]Job, int64, error)
    GetJob(ctx context.Context, id uuid.UUID) (*Job, error)
    RetryDeadJob(ctx context.Context, id uuid.UUID) error
    UpdateJobPriority(ctx context.Context, id uuid.UUID, priority int) error
    ListDeadJobs(ctx context.Context, f JobFilter) ([]Job, int64, error)
    PurgeDeadJobs(ctx context.Context, olderThan time.Duration) (int64, error)
    PurgeCompletedJobs(ctx context.Context, olderThan time.Duration) (int64, error)

    // ── Workers ──────────────────────────────────────────────────────────────
    UpsertWorker(ctx context.Context, w WorkerInfo) error
    HeartbeatWorker(ctx context.Context, id uuid.UUID, activeJobs int) error
    MarkWorkerOffline(ctx context.Context, id uuid.UUID) error
    ListWorkers(ctx context.Context) ([]WorkerInfo, error)
    MarkStaleWorkersOffline(ctx context.Context, threshold time.Duration) (int, error)

    // ── Stall detection ──────────────────────────────────────────────────────
    RequeueStalledJobs(ctx context.Context) (int, error)

    // ── Schedules ────────────────────────────────────────────────────────────
    ListDueSchedules(ctx context.Context, now time.Time) ([]Schedule, error)
    UpdateScheduleNextRun(ctx context.Context, id uuid.UUID, next time.Time) error
    EnsureSchedule(ctx context.Context, s ScheduleInput) (*Schedule, error)
    UpdateSchedule(ctx context.Context, id uuid.UUID, s ScheduleInput) (*Schedule, error)
    DeleteSchedule(ctx context.Context, id uuid.UUID) error
    ListSchedules(ctx context.Context) ([]Schedule, error)
}
```

### `ws.go` — buffered per-client channels

```go
// Every connected WebSocket client gets a buffered send channel.
// The hub goroutine only performs non-blocking writes to these channels.
// A dedicated writePump goroutine per client handles the actual network I/O.
// This ensures one slow client can never delay event delivery to other clients.

const clientSendBuf = 64 // events buffered per client before drop

type client struct {
    conn *websocket.Conn
    send chan []byte   // buffered — hub never blocks writing here
    hub  *WSHub
}

// Hub goroutine — sole owner of hub.clients map, never blocks on network I/O.
func (h *WSHub) Run() {
    for {
        select {
        case c := <-h.register:
            h.clients[c] = struct{}{}
        case c := <-h.unregister:
            if _, ok := h.clients[c]; ok {
                delete(h.clients, c)
                close(c.send) // signals writePump to exit
            }
        case msg := <-h.broadcast:
            for c := range h.clients {
                select {
                case c.send <- msg: // non-blocking
                default:
                    // Buffer full → client is too slow → disconnect.
                    // Client will reconnect and re-subscribe.
                    close(c.send)
                    delete(h.clients, c)
                }
            }
        }
    }
}

// Per-client goroutine — slow network I/O lives here, not in the hub.
func (c *client) writePump() {
    defer func() {
        c.hub.unregister <- c
        c.conn.Close()
    }()
    for msg := range c.send { // exits when channel is closed
        c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
        if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
            return
        }
    }
}
```

### `manager.go`

```go
type Manager struct {
    dispatcher *Dispatcher
    watcher    *ScheduleWatcher
    stall      *StallDetector
    hub        *WSHub
    store      JobStore
    pubsub     PubSub
    metrics    MetricsRecorder
}

type ManagerConfig struct {
    Store             JobStore
    Pool              *pgxpool.Pool
    PubSub            PubSub            // Redis pub/sub — RedisStore satisfies this
    Metrics           MetricsRecorder   // nil → defaults to QueryMetricsRecorder(store)
                                        //   swap to PrometheusMetricsRecorder for in-process counters
    Workers           int
    Queues            []string       // default: ["default"]
    DefaultTimeout    time.Duration  // default: 10m
    DefaultAttempts   int            // default: 5
    RetentionDays     int            // default: 30 — purge_completed_jobs threshold
    HeartbeatEvery    time.Duration  // default: 15s
    StallCheck        time.Duration  // default: 30s
    ScheduleCheck     time.Duration  // default: 10s
    NotifyChannel     string         // default: "jobqueue:notify"
    RedisFallbackPoll time.Duration  // default: 10s — poll interval when Redis is silent
    AgingRateSeconds  int            // default: 60 — seconds per +1 effective_priority point
    AgingCap          int            // default: 50 — max points a job can gain from aging
}

func NewManager(cfg ManagerConfig) *Manager
// NewManager defaults cfg.Metrics to QueryMetricsRecorder(cfg.Store) when nil.
func (m *Manager) Register(k Kind, h Handler)                    // panics if called after Start or duplicate
func (m *Manager) Start(ctx context.Context)
func (m *Manager) Shutdown()                                      // drain workers, update DB, close WS
func (m *Manager) Submit(ctx context.Context, r SubmitRequest) (*Job, error)
func (m *Manager) EnsureSchedule(ctx context.Context, s ScheduleInput) error
func (m *Manager) AdminRouter() chi.Router
```

### `metrics.go`

```go
// QueryMetricsRecorder is the V1 default. Event hooks are all no-ops — the
// data lives in Postgres and is read fresh on each scrape. Zero new dependencies.
//
// Replace ManagerConfig.Metrics with PrometheusMetricsRecorder when you want
// in-process counters. That is the only change required anywhere.
type QueryMetricsRecorder struct{ store JobStore }

func NewQueryMetricsRecorder(store JobStore) *QueryMetricsRecorder

func (r *QueryMetricsRecorder) OnJobSubmitted(job Job)                        {}
func (r *QueryMetricsRecorder) OnJobClaimed(job Job)                          {}
func (r *QueryMetricsRecorder) OnJobSucceeded(job Job, d time.Duration)       {}
func (r *QueryMetricsRecorder) OnJobFailed(job Job, err error, retry bool)    {}
func (r *QueryMetricsRecorder) OnJobDead(job Job)                             {}
func (r *QueryMetricsRecorder) OnJobCancelled(job Job)                        {}
func (r *QueryMetricsRecorder) OnScheduleFired(id uuid.UUID, k Kind)          {}
func (r *QueryMetricsRecorder) OnJobsRequeued(count int)                      {}

func (r *QueryMetricsRecorder) MetricsHandler() http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
        stats, err := r.store.GetStats(req.Context())
        if err != nil {
            http.Error(w, "internal error", http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "text/plain; version=0.0.4")
        for _, row := range stats.ByKindStatus {
            fmt.Fprintf(w, "jobqueue_jobs_total{kind=%q,status=%q} %d\n",
                row.Kind, row.Status, row.Count)
        }
        for _, row := range stats.WorkersByStatus {
            fmt.Fprintf(w, "jobqueue_workers_total{status=%q} %d\n",
                row.Status, row.Count)
        }
        fmt.Fprintf(w, "jobqueue_succeeded_total %d\n", stats.TotalSucceeded)
        fmt.Fprintf(w, "jobqueue_failed_total %d\n",    stats.TotalFailed)
        fmt.Fprintf(w, "jobqueue_dead_total %d\n",      stats.TotalDead)
        for _, b := range stats.DurationBuckets {
            fmt.Fprintf(w, "jobqueue_duration_seconds_bucket{kind=%q,le=%q} %d\n",
                b.Kind, b.Le, b.Count)
        }
    })
}

// NoopMetricsRecorder satisfies MetricsRecorder for tests.
// All hooks are no-ops; MetricsHandler returns 404.
type NoopMetricsRecorder struct{}
func (NoopMetricsRecorder) OnJobSubmitted(Job)                {}
func (NoopMetricsRecorder) OnJobClaimed(Job)                  {}
func (NoopMetricsRecorder) OnJobSucceeded(Job, time.Duration) {}
func (NoopMetricsRecorder) OnJobFailed(Job, error, bool)      {}
func (NoopMetricsRecorder) OnJobDead(Job)                     {}
func (NoopMetricsRecorder) OnJobCancelled(Job)                {}
func (NoopMetricsRecorder) OnScheduleFired(uuid.UUID, Kind)   {}
func (NoopMetricsRecorder) OnJobsRequeued(int)                {}
func (NoopMetricsRecorder) MetricsHandler() http.Handler      { return http.NotFoundHandler() }

// V2 — PrometheusMetricsRecorder (prometheus/client_golang)
// Defined here when ready. Swap in ManagerConfig.Metrics in server.New.
// Event hooks increment counters/histograms in-process.
// MetricsHandler() returns promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).
// No other file changes required.
```

---

## 7. Lifecycle in `server.New`

```go
// ── Job queue wiring ──────────────────────────────────────────────────────────

store := jobqueue.NewPgJobStore(pool)

mgr := jobqueue.NewManager(jobqueue.ManagerConfig{
    Store:             store,
    Pool:              pool,
    PubSub:            redisStore, // *kvstore.RedisStore — already wired for rate limiting
    // Metrics not set → NewManager defaults to QueryMetricsRecorder(store).
    // Swap to PrometheusMetricsRecorder when a Prometheus stack is running (one line change).
    Workers:           cfg.JobWorkers,
    DefaultTimeout:    10 * time.Minute,
    DefaultAttempts:   5,
    RetentionDays:     30,
    HeartbeatEvery:    15 * time.Second,
    StallCheck:        30 * time.Second,
    ScheduleCheck:     10 * time.Second,
    RedisFallbackPoll: 10 * time.Second,
    AgingRateSeconds:  60,
    AgingCap:          50,
})

// Register handlers — must happen before Start
mgr.Register(worker.KindPurgeAccounts,    worker.NewPurgeHandler(pool))
mgr.Register(worker.KindExecuteRequest,   worker.NewExecuteRequestHandler(pool))
mgr.Register(worker.KindSendNotification, worker.NewSendNotificationHandler(pool, mailer))
mgr.Register(worker.KindPurgeCompleted,   worker.NewPurgeCompletedHandler(pool))

// Seed schedules (idempotent — upserts by name)
mgr.EnsureSchedule(ctx, jobqueue.ScheduleInput{
    Name:            "purge_accounts_hourly",
    Kind:            worker.KindPurgeAccounts,
    IntervalSeconds: 3600,
    SkipIfRunning:   true,
})
mgr.EnsureSchedule(ctx, jobqueue.ScheduleInput{
    Name:            "purge_completed_jobs_daily",
    Kind:            worker.KindPurgeCompleted,
    IntervalSeconds: 86400,
    SkipIfRunning:   true,
})

mgr.Start(ctx)

deps.Jobs   = mgr
deps.JobMgr = mgr

r.Mount("/admin/jobqueue", mgr.AdminRouter())

// ── Cleanup order ─────────────────────────────────────────────────────────────
// 1. mgr.Shutdown()  — stop scheduler, drain workers, mark worker offline
// 2. q.Shutdown()    — drain mail queue (existing)
// 3. pool.Close()    — safe only after all queue consumers have stopped
```

---

## 8. REST Admin API

All routes mounted at `/admin/jobqueue`. Require `job_queue:read` or `job_queue:manage`
permission (seeded by SQL migration into the `permissions` table).

| Method   | Path                          | Description                                          | Permission       |
|----------|-------------------------------|------------------------------------------------------|-----------------|
| GET      | /workers                      | List all workers with status, heartbeat, counters    | job_queue:read  |
| GET      | /workers/:id                  | Single worker detail + currently running jobs        | job_queue:read  |
| DELETE   | /workers/:id                  | Force-drain a specific worker (graceful)             | job_queue:manage|
| GET      | /jobs                         | List jobs (filter: kind, status, queue, from/to)     | job_queue:read  |
| GET      | /jobs/:id                     | Single job detail                                    | job_queue:read  |
| DELETE   | /jobs/:id                     | Cancel a pending job                                 | job_queue:manage|
| PATCH    | /jobs/:id/priority            | Update priority `{priority: int}`                    | job_queue:manage|
| POST     | /jobs/:id/retry               | Re-queue a dead/failed job immediately               | job_queue:manage|
| GET      | /dead                         | List dead-lettered jobs (paginated)                  | job_queue:read  |
| DELETE   | /dead                         | Purge dead jobs `?older_than=7d`                     | job_queue:manage|
| GET      | /queues                       | List queues with depth, paused state                 | job_queue:read  |
| POST     | /queues/:kind/pause           | Pause a kind `{reason: string}`                      | job_queue:manage|
| POST     | /queues/:kind/resume          | Resume a paused kind                                 | job_queue:manage|
| GET      | /schedules                    | List all schedules                                   | job_queue:read  |
| POST     | /schedules                    | Create a schedule                                    | job_queue:manage|
| PUT      | /schedules/:id                | Update schedule (interval, payload, active flag)     | job_queue:manage|
| DELETE   | /schedules/:id                | Delete a schedule                                    | job_queue:manage|
| POST     | /schedules/:id/trigger        | Manually trigger a schedule now                      | job_queue:manage|
| GET      | /stats                        | Aggregate stats: throughput, error rate, queue depth | job_queue:read  |
| GET      | /metrics                      | Prometheus text exposition; delegates to `MetricsRecorder.MetricsHandler()` | job_queue:read  |
| GET      | /ws                           | WebSocket upgrade — real-time event stream           | job_queue:read  |

---

## 9. WebSocket Protocol

Connect to `ws://host/admin/jobqueue/ws`.

Each client connection is served by two goroutines: a `readPump` (client commands) and a
`writePump` (server events). The writePump reads from a 64-event buffered channel. If the
buffer fills, the client is disconnected — it can reconnect and re-subscribe. The hub
goroutine never blocks on client network I/O.

### Server → Client events

```jsonc
// Job lifecycle
{ "event": "job.created",   "data": { "id": "…", "kind": "send_email", "priority": 0 } }
{ "event": "job.claimed",   "data": { "id": "…", "worker_id": "…", "attempt": 1 } }
{ "event": "job.succeeded", "data": { "id": "…", "duration_ms": 142 } }
{ "event": "job.failed",    "data": { "id": "…", "attempt": 2, "retry_at": "…", "error": "…" } }
{ "event": "job.dead",      "data": { "id": "…", "attempts": 5, "error": "…" } }
{ "event": "job.cancelled", "data": { "id": "…" } }

// Worker lifecycle
{ "event": "worker.online",  "data": { "id": "…", "host": "…", "concurrency": 4 } }
{ "event": "worker.idle",    "data": { "id": "…" } }
{ "event": "worker.busy",    "data": { "id": "…", "active_jobs": 3 } }
{ "event": "worker.offline", "data": { "id": "…", "reason": "graceful_shutdown" } }

// Queue management
{ "event": "queue.paused",   "data": { "kind": "send_email", "by": "uuid", "reason": "…" } }
{ "event": "queue.resumed",  "data": { "kind": "send_email" } }

// Scheduler
{ "event": "schedule.fired", "data": { "schedule_id": "…", "job_id": "…", "kind": "…" } }

// Periodic stats tick (every 5s) — sourced from Postgres, unaffected by Redis downtime
{ "event": "stats.tick", "data": { "pending": 14, "running": 3, "dead": 0, "tps": 8.2 } }
```

### Client → Server commands

```jsonc
{ "cmd": "subscribe",   "filter": { "kinds": ["send_email"], "queues": ["default"] } }
{ "cmd": "unsubscribe" }
{ "cmd": "ping" }  // server replies { "event": "pong" }
```

---

## 10. Decisions

### v5 decisions (this revision)

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| D-V5-1 | Ship `GET /metrics` in V1? | Yes — `QueryMetricsRecorder` requires no new deps; one SQL query per scrape | No schema change, no new infra. Without it, dead jobs can’t auto-page anyone and there are no historical graphs. |
| D-V5-2 | How to swap metrics backends without breaking callers? | `MetricsRecorder` interface in `job.go`; `ManagerConfig.Metrics` field; `NewManager` defaults to `QueryMetricsRecorder(store)` when nil | Same pattern as `PubSub`. Swap to `prometheus/client_golang` in-process counters by changing one line in `server.New`. Dispatcher, ScheduleWatcher, StallDetector, handlers — all permanently insulated from the metrics backend choice. |

### v4 decisions (preserved)

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| D-V4-1 | Worker wake mechanism? | Redis pub/sub via existing `RedisStore` | Redis already in stack. Pub/sub is ephemeral — zero memory overhead. Eliminates dedicated pgx conn footgun from LISTEN/NOTIFY. |
| D-V4-2 | Priority starvation? | SQL aging: `effective_priority = priority + LEAST(minutes_waited, 50)` | No schema change. Pure query-time computation. Guarantees no job waits more than ~150 min. Tunable via `AgingRateSeconds`, `AgingCap`. |
| D-V4-3 | WSHub backpressure? | Per-client buffered channel (64 events) + `writePump` goroutine | Hub never blocks on slow clients. One slow client cannot delay others. |
| D-V4-4 | Redis downtime? | 10s Postgres fallback poll always running in ticker | Jobs never lost — state is in Postgres. Max 10s extra latency. go-redis reconnects automatically. |
| D-V4-5 | request_executions? | Dropped — replaced by `kind="execute_request"` job | One retry system. Idempotency, dead-letter, observability all inherited. |
| D-V4-6 | Notification delivery retry? | Dropped from `request_notifications` — replaced by `kind="send_notification"` job | Same rationale as D-V4-5. |
| D-V4-7 | Job table growth? | Daily `purge_completed_jobs` schedule — 30-day retention default | Bounded table size. Dead jobs kept longer (audit trail). Configurable via `RetentionDays`. |
| D-V4-8 | ScheduleWatcher poll interval? | 10s (was 1s) | Schedules in this system run hourly+. 10s precision is imperceptible. 10× less DB write load. |

### v3/v2 decisions (preserved)

| # | Decision | Status |
|---|----------|--------|
| D-N1 | DB-backed queue (`SELECT FOR UPDATE SKIP LOCKED`) | ✅ Held |
| D-N3 | Single `ScheduleWatcher` goroutine | ✅ Held |
| D-N4 | `jobs` table rows with `status='dead'` as dead-letter | ✅ Held |
| D-N5 | `job_paused_kinds` table (survives restart) | ✅ Held |
| D-N6 | `workers` table heartbeat | ✅ Held |
| D-N7 | Retry via `run_after` update (no goroutine sleep) | ✅ Held |
| D-N8 | `idempotency_key UNIQUE` dedup | ✅ Held |
| D-N9 | `StallDetector` resets jobs past `timeout_seconds` | ✅ Held |
| D-N10 | Multi-instance safe by design | ✅ Held |
| D-N12 | No `WithQueueSize` — DB is the unbounded buffer | ✅ Held |
| D-03 | `any` payload (now `json.RawMessage`) | ✅ Held |
| D-11 | Context rules (detached, deadline via `timeout_seconds`) | ✅ Held |
| D-12 | Panic on duplicate/post-Start Register | ✅ Held |
| D-13 | Unknown kind → log + dead-letter, no panic | ✅ Held |
| D-14 | `deps.Jobs` as `Submitter` interface | ✅ Held |
| D-20 | `internal/worker/` as handler package | ✅ Held |
| D-21 | `Submitter` one-method interface | ✅ Held |
| D-22 | Handlers may re-submit jobs | ✅ Held |

---

## 11. Known Ceilings (by design — not fixable without infra changes)

These are documented trade-offs of the Postgres-backed, Redis-signaled design, not bugs
or oversights. Each has a clear threshold, leading indicators to watch, and a defined
upgrade path that does not require rewriting the application layer.

### 11.1 DB Efficiency — N-1 Wasted Claim Attempts (rated 9/10)

**What happens:** Every `PUBLISH` on `jobqueue:notify` wakes all subscribed worker
goroutines across all instances simultaneously. Each goroutine races to execute
`SELECT FOR UPDATE SKIP LOCKED`. Only one wins and gets a job row. The other N-1 goroutines
complete their query, find nothing, and go back to sleep.

**Concrete numbers:** 3 instances × 4 workers = 12 goroutines. One job submitted →
one `PUBLISH` → 12 goroutines wake → 12 SKIP LOCKED queries → 1 winner → 11 empty
returns. At 100 jobs/min that is 1,100 wasted queries per minute. These are fast,
indexed reads on a small partial-indexed set, but they are real DB round-trips.

**Why it is acceptable now:** At store backend scale (tens to hundreds of jobs/minute),
these are sub-millisecond indexed reads. Postgres handles this trivially. The partial
index `WHERE status='pending'` keeps the scanned rowset tiny regardless of total
table size.

**When it becomes a problem:** Sustained throughput above ~500 jobs/second with 10+
worker instances. At that point SKIP LOCKED contention becomes measurable in
`pg_stat_activity` wait times.

**What to watch:** `pg_stat_activity` for lock wait on `jobs` rows; P95 latency of
`ClaimJob()` calls in the Dispatcher; `pg_stat_statements` for `jobs`-related query
share of total DB CPU.

**The upgrade path:** Replace the `PubSub` implementation with NATS JetStream or a
Redis Stream with consumer groups. These deliver each message to exactly one subscriber
(work-queue semantics) rather than broadcasting to all (fan-out semantics), eliminating
the N-1 waste entirely. Because `PubSub` is a defined interface in `job.go`, this is a
drop-in swap — the `JobStore`, Dispatcher business logic, ScheduleWatcher, and admin API
are all untouched.

---

### 11.2 Observability — Metrics Endpoint (resolved in V1)

**What's there:** Full REST API (`GET /stats`, `GET /jobs`, `GET /workers`, `GET /dead`),
real-time WebSocket event stream with 14 event types, per-worker counters in the `workers`
table, and all job state queryable directly from Postgres. **`GET /metrics`** ships in V1
(promoted from V2 backlog — see D-V5-1).

**V1 implementation — `QueryMetricsRecorder` (zero new dependencies):**

`api.go` delegates entirely to `MetricsRecorder.MetricsHandler()`. The `QueryMetricsRecorder`
runs one `GetStats()` SQL query per Prometheus scrape and writes Prometheus text. No schema change.

```go
// api.go — AdminRouter:
r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
    m.metrics.MetricsHandler().ServeHTTP(w, r)
})
```

**Metrics exposed (from `GetStats()`):**
```
jobqueue_jobs_total{kind, status}            gauge    — current count by kind and status
jobqueue_workers_total{status}               gauge    — worker count by status
jobqueue_succeeded_total                     counter  — all-time succeeded (workers table)
jobqueue_failed_total                        counter  — all-time failed
jobqueue_dead_total                          counter  — all-time dead-lettered
jobqueue_duration_seconds_bucket{kind, le}   histogram — job duration (last 1h rolling window)
```

The histogram adds one bucketed aggregate to `GetStats()` — `started_at` and `completed_at`
already exist, so this is a query addition, not a schema change.

**Safe swap path (D-V5-2):**

When an in-Prometheus stack exists and in-process counters are preferred, change one line:

```go
// V1 (nil → QueryMetricsRecorder auto-wired):
mgr := jobqueue.NewManager(jobqueue.ManagerConfig{ /* Metrics not set */ })

// V2 (prometheus/client_golang):
rec := jobqueue.NewPrometheusMetricsRecorder(prometheus.DefaultRegisterer)
mgr := jobqueue.NewManager(jobqueue.ManagerConfig{
    Metrics: rec,   // ← one line; nothing else changes
})
```

**Alerting rules (wire once Prometheus is scraping):**
```yaml
- alert: JobQueueDeadJobsAccumulating
  expr: increase(jobqueue_jobs_total{status="dead"}[5m]) > 0
  for: 5m   # → page on-call

- alert: JobQueuePendingBacklog
  expr: jobqueue_jobs_total{status="pending"} > 500
  for: 10m  # → queue backup warning

- alert: JobQueueWorkerOffline
  expr: jobqueue_workers_total{status="offline"} > 0
  for: 2m   # → worker crash alert

- alert: JobQueueExecutionSLABreach
  expr: histogram_quantile(0.95,
          rate(jobqueue_duration_seconds_bucket{kind="execute_request"}[10m])) > 30
  for: 5m   # → execution SLA warning
```

---

### 11.3 Scalability Ceiling — Postgres as the Write Bottleneck (rated 9/10)

**The ceiling:** This design sustains approximately **500–1,000 jobs/second** on a
well-tuned Postgres instance (dedicated server, NVMe SSD, pgBouncer in transaction mode).
Above that, write contention on the `jobs` table — `INSERT` for new jobs, `UPDATE` for
claim and completion, `DELETE` for daily purge — becomes the bottleneck, not the Go code.

**Why this is far away:** A store backend processing approval workflows, hourly maintenance
schedules, and notification delivery might handle 10–100 jobs/minute at peak. That is
100× below the ceiling. The ceiling is documented so a future engineer does not
unknowingly approach it without a plan.

**Leading indicators — act only when these appear, not before:**
1. `pg_stat_activity` consistently shows `jobs` rows in `lock wait` state.
2. `ClaimJob()` P95 latency in the Dispatcher exceeds 50ms.
3. `pg_stat_statements` shows `jobs`-related queries dominating total DB CPU.
4. Jobs table grows despite the daily purge (INSERT rate exceeds DELETE rate).

**The upgrade path — in order of invasiveness:**

**Step 1 — pgBouncer in transaction mode.** Reduces connection overhead significantly.
Zero code change. Eliminates connection count pressure at the first sign of connection
exhaustion. If not already in place, add it before anything else.

**Step 2 — Dedicated pool for the job queue.** `ManagerConfig.Pool` already exists for
this. Give the Dispatcher its own `pgxpool` isolated from the main HTTP handler pool.
Prevents job queue load from starving API response times. A configuration change in
`server.New`, not a code change.

**Step 3 — Postgres table partitioning by `status`.** `pending` and `running` partitions
stay small and hot; `succeeded` partition holds the bulk and is only written once per job
(at completion). Reduces index bloat and VACUUM pressure on hot partitions. Requires a
migration (partitioned table recreation) but no application code change — the `JobStore`
SQL remains identical.

**Step 4 — Swap `JobStore` for a Redis Streams implementation.** Redis Streams with
consumer groups provide native work-queue semantics (exactly-one delivery) at >100,000
messages/second. Because `JobStore` is an interface, the Dispatcher, ScheduleWatcher, and
entire admin API are untouched. Only `store.go` is replaced. Postgres transitions from the
hot queue to an archive backend — completed and dead jobs are written there asynchronously
for queryability and long-term retention.

**Step 5 — NATS JetStream.** Same interface-swap as Step 4, higher operational complexity,
higher ceiling, built-in persistence and exactly-once guarantees. The correct choice if
the system eventually needs multi-region fan-out or event sourcing semantics beyond what
Redis Streams provides.

**The key architectural guarantee:** Because `JobStore` is an interface defined in
`job.go` from day one, Steps 4 and 5 are refactors — not rewrites. The handlers in
`internal/worker/`, the REST API, the WebSocket protocol, and the `Submitter` interface
used by domain code are all permanently insulated from the storage backend choice.

---

## 12. File Map

| Path | Status | What |
|------|--------|------|
| `sql/schema/006_jobqueue.sql` | **NEW** | Creates job tables; drops `request_executions`; alters `request_notifications` |
| `internal/platform/jobqueue/job.go` | CHANGED | Adds `PubSub` and `MetricsRecorder` interfaces; `ManagerConfig` gains `Metrics MetricsRecorder` field |
| `internal/platform/jobqueue/store.go` | **NEW** | `JobStore` + `pgJobStore`; claim query uses aging formula; adds `PurgeCompletedJobs` |
| `internal/platform/jobqueue/dispatcher.go` | CHANGED | Redis SUBSCRIBE + 10s fallback ticker; no pgx LISTEN conn |
| `internal/platform/jobqueue/scheduler.go` | CHANGED | 10s poll interval; cron via `robfig/cron`; PUBLISH failure is warn-only |
| `internal/platform/jobqueue/deadletter.go` | CHANGED | DB queries; no ring-buffer |
| `internal/platform/jobqueue/metrics.go` | **NEW** | `QueryMetricsRecorder` (V1 default) + `NoopMetricsRecorder` (tests) |
| `internal/platform/jobqueue/api.go` | **NEW** | 20 REST endpoints including `GET /metrics`; delegates to `MetricsRecorder.MetricsHandler()` |
| `internal/platform/jobqueue/ws.go` | **NEW** | `WSHub` + per-client 64-event buffered channel + `writePump` goroutine per client |
| `internal/platform/jobqueue/manager.go` | **NEW** | `Manager` lifecycle; accepts `PubSub` in config |
| `internal/platform/kvstore/redis.go` | CHANGED | Add `Publish()` and `Subscribe()` methods to `RedisStore` |
| `internal/worker/kinds.go` | CHANGED | Add `KindExecuteRequest`, `KindSendNotification`, `KindPurgeCompleted` |
| `internal/worker/purge.go` | SAME | Logic unchanged; signature update `Job` not `any` |
| `internal/worker/execute_request.go` | **NEW** | Replaces `request_executions` inline execution |
| `internal/worker/send_notification.go` | **NEW** | Replaces notification delivery retry loop |
| `internal/worker/purge_completed.go` | **NEW** | Deletes `succeeded`/`cancelled` jobs older than `RetentionDays` |
| `internal/app/deps.go` | CHANGED | Add `Jobs jobqueue.Submitter`, `JobMgr *jobqueue.Manager` |
| `internal/server/server.go` | CHANGED | Wire `Manager`; mount `/admin/jobqueue`; updated cleanup order |
| `internal/config/config.go` | CHANGED | `JobWorkers`, `JobRetentionDays`; remove `JobQueueSize` |

---

## 13. Test Case Inventory

**Legend:** U = unit (no DB), I = integration (requires DB), R = requires Redis

### Dispatcher / Worker loop

| # | Case | Layer |
|---|------|-------|
| T-01 | Happy path — job claimed, handled, completed | I |
| T-02 | Handler returns error → retried up to MaxAttempts | I |
| T-03 | Handler succeeds on 3rd attempt | I |
| T-04 | Unknown kind → dead-lettered | I |
| T-05 | Paused kind → job not claimed while paused | I |
| T-06 | Paused kind → claimed after Resume | I |
| T-07 | Submit after Shutdown → error returned | U |
| T-08 | Register after Start → panic | U |
| T-09 | Register same Kind twice → panic | U |
| T-10 | Multiple workers claim distinct jobs (SKIP LOCKED) | I |
| T-11 | Worker crash → stall detector resets job | I |
| T-12 | Idempotency key deduplicates concurrent Submits | I |
| T-13 | Retry sets run_after; job not re-claimed before it | I |
| T-14 | Graceful shutdown drains in-flight jobs | I |
| T-15 | Redis down → workers fall back to 10s poll, jobs still run | I+R |
| T-16 | Redis recovers → workers resume SUBSCRIBE wake | I+R |

### Priority Aging

| # | Case | Layer |
|---|------|-------|
| T-17 | Low-priority job beats higher-priority after AgingCap minutes | I |
| T-18 | Aging does not exceed cap regardless of wait time | I |
| T-19 | priority=100 job still wins over aged priority=50 job | I |

### ScheduleWatcher

| # | Case | Layer |
|---|------|-------|
| T-20 | Due interval schedule fires and inserts job | I |
| T-21 | Not-yet-due schedule is skipped | I |
| T-22 | `skip_if_running` prevents duplicate job insertion | I |
| T-23 | Two watchers (multi-instance) do not double-insert | I |
| T-24 | next_run_at updated correctly after firing | I |
| T-25 | PUBLISH failure during Redis downtime does not prevent job insertion | I+R |

### JobStore — Contract Tests (`store_contract_test.go`)

These tests are written against the `JobStore` **interface**, not against `pgJobStore`
directly. The contract function is called once for each implementation:

```go
// store_contract_test.go
func RunJobStoreContractTests(t *testing.T, newStore func(t *testing.T) JobStore) {
    t.Run("ClaimJob/returns nil when no pending jobs", func(t *testing.T) { ... })
    t.Run("ClaimJob/respects paused kinds",            func(t *testing.T) { ... })
    // ... all cases below
}

// pg_store_test.go — runs today
func TestPgJobStore(t *testing.T) {
    RunJobStoreContractTests(t, func(t *testing.T) JobStore {
        return newTestPgJobStore(t) // spins up test DB
    })
}

// redis_store_test.go — wired when Ceiling 3 Step 4 is crossed
func TestRedisJobStore(t *testing.T) {
    RunJobStoreContractTests(t, func(t *testing.T) JobStore {
        return newTestRedisJobStore(t) // spins up test Redis
    })
}
```

If `TestRedisJobStore` passes `RunJobStoreContractTests`, the swap is safe. If it
fails any case, it is not a drop-in replacement — fix it before merging.

| # | Case | Layer |
|---|------|-------|
| T-26 | ClaimJob: returns nil when no pending jobs | I |
| T-27 | ClaimJob: respects paused kinds | I |
| T-28 | ClaimJob: respects run_after | I |
| T-29 | ClaimJob: effective_priority ordering | I |
| T-30 | PurgeDeadJobs removes only jobs older than threshold | I |
| T-31 | PurgeCompletedJobs removes succeeded/cancelled beyond RetentionDays | I |
| T-32 | RetryDeadJob resets status to pending | I |

### WSHub

| # | Case | Layer |
|---|------|-------|
| T-33 | Slow client buffer fills → client disconnected, others unaffected | U |
| T-34 | WS client receives job.succeeded event | I |
| T-35 | WS client reconnects and re-subscribes after disconnect | I |

### PubSub — Contract Tests (`pubsub_contract_test.go`)

Same pattern as JobStore. Written against the `PubSub` interface. Both
`RedisStore` (current) and any future `NATSPubSub` or `StreamPubSub` must pass.

```go
func RunPubSubContractTests(t *testing.T, newPubSub func(t *testing.T) PubSub) {
    t.Run("Publish/Subscribe roundtrip",            func(t *testing.T) { ... })
    t.Run("Publish fails gracefully when down",     func(t *testing.T) { ... })
    t.Run("Subscribe channel closed on context done", func(t *testing.T) { ... })
}

func TestRedisPubSub(t *testing.T) {
    RunPubSubContractTests(t, func(t *testing.T) PubSub {
        return newTestRedisStore(t)
    })
}
```

| # | Case | Layer |
|---|------|-------|
| T-C1 | Publish → Subscribe receives message within 100ms | I+R |
| T-C2 | Multiple subscribers all receive the same publish (fan-out) | I+R |
| T-C3 | Publish returns non-nil error when broker is unreachable | I+R |
| T-C4 | Subscribe channel is closed when ctx is cancelled | I+R |
| T-C5 | Publish succeeds after broker recovers from downtime | I+R |

### MetricsRecorder — Contract Tests (`metrics_contract_test.go`)

```go
func RunMetricsRecorderContractTests(t *testing.T, rec MetricsRecorder) {
    t.Run("all hooks callable without panic",  func(t *testing.T) { ... })
    t.Run("MetricsHandler returns non-nil",    func(t *testing.T) { ... })
    t.Run("MetricsHandler serves without 500", func(t *testing.T) { ... })
}

func TestQueryMetricsRecorder(t *testing.T)  { RunMetricsRecorderContractTests(t, NewQueryMetricsRecorder(store)) }
func TestNoopMetricsRecorder(t *testing.T)   { RunMetricsRecorderContractTests(t, NoopMetricsRecorder{}) }
// Future: func TestPrometheusMetricsRecorder(t *testing.T) { RunMetricsRecorderContractTests(t, NewPrometheusMetricsRecorder(...)) }
```

| # | Case | Layer |
|---|------|-------|
| T-47 | QueryMetricsRecorder: GET /metrics returns valid Prometheus text (content-type, label format) | I |
| T-48 | QueryMetricsRecorder: jobqueue_jobs_total counts match GET /stats JSON response | I |
| T-49 | QueryMetricsRecorder: jobqueue_duration_seconds_bucket present for recently completed jobs | I |
| T-50 | NoopMetricsRecorder: GET /metrics returns 404 | U |
| T-51 | MetricsRecorder.OnJobSucceeded called exactly once after handler returns nil | U |
| T-52 | MetricsRecorder.OnJobDead called after MaxAttempts exceeded | U |
| T-53 | Swap QueryMetricsRecorder → NoopMetricsRecorder in ManagerConfig — no other code change required | U |

### Admin API

| # | Case | Layer |
|---|------|-------|
| T-36 | GET /jobs returns paginated results with filter | I |
| T-37 | PATCH /jobs/:id/priority updates claim order | I |
| T-38 | POST /jobs/:id/retry re-queues dead job | I |
| T-39 | POST /queues/:kind/pause prevents new claims | I |
| T-40 | POST /schedules creates and evaluates on next watcher tick | I |

### New handlers

| # | Case | Layer |
|---|------|-------|
| T-41 | execute_request handler executes approved request idempotently | I |
| T-42 | send_notification handler delivers via mailer, retries on transient error | I |
| T-43 | purge_completed_jobs deletes only jobs older than RetentionDays | I |

### PurgeHandler (unchanged from V1)

| # | Case | Layer |
|---|------|-------|
| T-44 | Handle purges accounts past grace period | I |
| T-45 | Handle skips accounts within grace period | I |
| T-46 | Handle returns nil when no accounts due | I |

---

---

## 14. Upgrade Compatibility Guarantee

Every ceiling upgrade path is a swap of one interface implementation. The contract
tests above are the compatibility gate — before any ceiling upgrade merges to main:

| Ceiling | What changes | Gate to pass before merging |
|---------|-------------|-----------------------------|
| C1 — N-1 waste | `PubSub` implementation | `RunPubSubContractTests` with new impl |
| C3 Step 3 — partitioning | SQL migration only, no Go code | Migration dry-run + full integration test suite |
| C3 Step 4 — Redis Streams `JobStore` | `JobStore` implementation | `RunJobStoreContractTests` with new impl |
| C3 Step 5 — NATS `JobStore` | `JobStore` implementation | `RunJobStoreContractTests` with new impl |
| Metrics — Prometheus in-process | `MetricsRecorder` implementation | `RunMetricsRecorderContractTests` with new impl |

**The rule is simple:** if the new implementation passes the contract suite, the swap
cannot break the application layer. If it fails any case, it is not yet a safe
replacement — regardless of whether it works in manual testing.

This means:
- `internal/worker/` handlers need zero changes for any ceiling upgrade
- `Submitter` callers in domain code need zero changes
- REST API and WebSocket protocol are unaffected
- `server.New` changes exactly one field in `ManagerConfig`

**File to add during implementation:**
```
internal/platform/jobqueue/
    store_contract_test.go    — RunJobStoreContractTests(t, newStore func)
    pubsub_contract_test.go   — RunPubSubContractTests(t, newPubSub func)
    metrics_contract_test.go  — RunMetricsRecorderContractTests(t, rec)
```

Each of these files has no build tag and no external dependency — they are plain
`_test.go` files in the `jobqueue` package that any implementation can import and run.

---

## 15. Implementation Phases

| Phase | What | Dependency |
|-------|------|------------|
| **1** | `sql/schema/006_jobqueue.sql` — run migration | None |
| **2** | `internal/platform/kvstore/redis.go` — add `Publish`, `Subscribe` | Phase 1 |
| **3** | `internal/platform/jobqueue/` — all package files + tests | Phase 2 |
| **4** | `internal/worker/` — new handlers + updated kinds | Phase 3 |
| **5** | Wire: `app/deps.go`, `server/server.go`, `config/config.go` | Phase 4 |

---

## 15. V2 Backlog (post-launch)

- `PrometheusMetricsRecorder` in `metrics.go` — swap `ManagerConfig.Metrics` for in-process counters/histograms via `prometheus/client_golang`; `MetricsHandler()` returns `promhttp.Handler()`. One line in `server.New`, zero other changes. (See §11.2 for full swap pattern.)
- Per-kind concurrency limits (`job_kind_limits` table)
- Per-instance queue routing (workers filter by `Queues` config field)
- Priority aging rate as a per-kind override
- Postgres table partitioning by `status` if write throughput warrants it
