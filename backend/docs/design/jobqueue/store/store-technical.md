# Store Technical — Interface, SQL, Contract Tests

> **What this file is:** The full `JobStore` interface, the priority aging claim
> query, `pgJobStore` implementation notes, and the contract test structure.
>
> **Read first:** `store-feature.md` — behavioral rules and design rationale.
> **Schema reference:** `sql/schema/007_jobqueue.sql`.

---

## Table of Contents

1. [JobStore interface](#1--jobstore-interface)
2. [Priority aging claim query](#2--priority-aging-claim-query)
3. [Idempotency insert](#3--idempotency-insert)
4. [pgJobStore notes](#4-pgJobStore-notes)
5. [GetStats query](#5--getstats-query)
6. [Contract test structure](#6--contract-test-structure)
7. [Test inventory](#7--test-inventory)

---

## §1 — JobStore interface

```go
// internal/platform/jobqueue/store.go

type JobStore interface {
    // ── Worker operations ─────────────────────────────────────────────────────
    // ClaimJob uses effective_priority = priority + LEAST(minutes_waited, AgingCap).
    // Returns nil, nil when no claimable job exists.
    ClaimJob(ctx context.Context, workerID uuid.UUID, queues []string) (*Job, error)
    CompleteJob(ctx context.Context, id uuid.UUID, result any) error
    FailJob(ctx context.Context, id uuid.UUID, err error, retryAt *time.Time) error
    DeadLetterJob(ctx context.Context, id uuid.UUID, err error) error

    // ── Submit ────────────────────────────────────────────────────────────────
    InsertJob(ctx context.Context, req SubmitRequest) (*Job, error)
    CancelJob(ctx context.Context, id uuid.UUID) error

    // ── Pause / Resume ────────────────────────────────────────────────────────
    PauseKind(ctx context.Context, kind Kind, by uuid.UUID, reason string) error
    ResumeKind(ctx context.Context, kind Kind) error
    ListPausedKinds(ctx context.Context) ([]PausedKind, error)

    // ── Management API ────────────────────────────────────────────────────────
    ListJobs(ctx context.Context, f JobFilter) ([]Job, int64, error)
    GetJob(ctx context.Context, id uuid.UUID) (*Job, error)
    RetryDeadJob(ctx context.Context, id uuid.UUID) error
    UpdateJobPriority(ctx context.Context, id uuid.UUID, priority int) error
    ListDeadJobs(ctx context.Context, f JobFilter) ([]Job, int64, error)
    PurgeDeadJobs(ctx context.Context, olderThan time.Duration) (int64, error)
    PurgeCompletedJobs(ctx context.Context, olderThan time.Duration) (int64, error)

    // ── Workers ───────────────────────────────────────────────────────────────
    UpsertWorker(ctx context.Context, w WorkerInfo) error
    HeartbeatWorker(ctx context.Context, id uuid.UUID, activeJobs int) error
    MarkWorkerOffline(ctx context.Context, id uuid.UUID) error
    ListWorkers(ctx context.Context) ([]WorkerInfo, error)
    MarkStaleWorkersOffline(ctx context.Context, threshold time.Duration) (int, error)

    // ── Stall detection ───────────────────────────────────────────────────────
    RequeueStalledJobs(ctx context.Context) (int, error)

    // ── Schedules ─────────────────────────────────────────────────────────────
    ListDueSchedules(ctx context.Context, now time.Time) ([]Schedule, error)
    UpdateScheduleNextRun(ctx context.Context, id uuid.UUID, next time.Time) error
    EnsureSchedule(ctx context.Context, s ScheduleInput) (*Schedule, error)
    UpdateSchedule(ctx context.Context, id uuid.UUID, s ScheduleInput) (*Schedule, error)
    DeleteSchedule(ctx context.Context, id uuid.UUID) error
    ListSchedules(ctx context.Context) ([]Schedule, error)

    // ── Metrics ───────────────────────────────────────────────────────────────
    // Called once per Prometheus scrape by QueryMetricsRecorder.MetricsHandler().
    GetStats(ctx context.Context) (*QueueStats, error)
}
```

---

## §2 — Priority aging claim query

```sql
-- Run by each worker goroutine when it needs work.
-- $1 = workerID  $2 = queue names array  $3 = aging_rate_seconds  $4 = aging_cap
WITH claimed AS (
    SELECT id,
           priority + LEAST(
               FLOOR(EXTRACT(EPOCH FROM (NOW() - created_at)) / $3)::int,
               $4
           ) AS effective_priority
    FROM   jobs
    WHERE  status    = 'pending'
      AND  kind      NOT IN (SELECT kind FROM job_paused_kinds)
      AND  run_after <= NOW()
      AND  queue_name = ANY($2)
    ORDER BY effective_priority DESC, created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE jobs
   SET status     = 'running',
       started_at = NOW(),
       updated_at = NOW(),
       worker_id  = $1,
       attempt    = attempt + 1
 WHERE id = (SELECT id FROM claimed)
RETURNING *;
```

The `idx_jobs_claimable` partial index (`WHERE status = 'pending'`) keeps the scanned
rowset small regardless of table size. `FOR UPDATE SKIP LOCKED` makes this atomic:
other worker goroutines skip a row that is already locked mid-claim.

**Important:** `trg_jobs_prevent_terminal_change` (from `008_jobqueue_functions.sql`)
fires on `BEFORE UPDATE` for terminal rows. The claim query targets `status='pending'`
so it will never attempt to update a terminal row, but the trigger is a safety net
against any code path that tries.

---

## §3 — Idempotency insert

```sql
-- $1=kind  $2=payload  $3=priority  $4=run_after  $5=max_attempts
-- $6=timeout_seconds  $7=queue_name  $8=idempotency_key  $9=created_by

INSERT INTO jobs (kind, payload, priority, run_after, max_attempts,
                  timeout_seconds, queue_name, idempotency_key, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT ON CONSTRAINT uq_jobs_idempotency_key DO NOTHING
RETURNING *;
```

The partial unique index `uq_jobs_idempotency_key` only enforces uniqueness on active
rows (not in terminal states). `ON CONFLICT DO NOTHING` returns zero rows when a
conflict is found; the caller fetches the existing row with a follow-up `GetJob` query
keyed on `idempotency_key`.

---

## §4 — pgJobStore notes

`pgJobStore` implements `JobStore` against `sql/schema/007_jobqueue.sql`.

```go
type pgJobStore struct {
    pool              *pgxpool.Pool
    agingRateSeconds  int  // from ManagerConfig.AgingRateSeconds (default 60)
    agingCap          int  // from ManagerConfig.AgingCap (default 50)
}

func NewPgJobStore(pool *pgxpool.Pool, agingRateSeconds, agingCap int) JobStore
```

Key implementation notes:
- All writes use `pgx` named parameters for readability and to avoid positional
  binding errors on the large `jobs` INSERT.
- `RequeueStalledJobs` joins `jobs` and `workers` to find jobs where
  `started_at + timeout_seconds < NOW()` AND the assigned worker's `heartbeat_at`
  is stale. Both conditions must be true to reset the job.
- `ListJobs` uses a cursor-based pagination pattern (keyed on `created_at DESC, id`)
  rather than `LIMIT + OFFSET` to avoid full-index scans on large result sets.
- `PurgeCompletedJobs` and `PurgeDeadJobs` use `idx_jobs_terminal_cleanup` and
  `idx_jobs_dead` respectively for bounded deletes — neither runs a full table scan.

---

## §5 — GetStats query

Called once per Prometheus scrape. Returns `QueueStats`.

```go
type QueueStats struct {
    ByKindStatus    []KindStatusCount
    WorkersByStatus []WorkerStatusCount
    TotalSucceeded  int64
    TotalFailed     int64
    TotalDead       int64
    DurationBuckets []DurationBucket
}
```

The SQL aggregates four concerns in one round-trip:

```sql
-- Per kind+status counts
SELECT kind, status::text, COUNT(*) AS count
FROM   jobs
WHERE  status NOT IN ('succeeded','failed','dead','cancelled')
GROUP BY kind, status;

-- Worker counts by status
SELECT status::text, COUNT(*) AS count
FROM   workers
WHERE  status != 'offline'
GROUP BY status;

-- All-time totals from worker counters
SELECT SUM(jobs_succeeded), SUM(jobs_failed), SUM(jobs_dead)
FROM   workers;

-- Duration histogram (last 1 hour rolling window)
SELECT kind,
       COUNT(*) FILTER (WHERE dur < 1)   AS le_1,
       COUNT(*) FILTER (WHERE dur < 5)   AS le_5,
       COUNT(*) FILTER (WHERE dur < 30)  AS le_30,
       COUNT(*) FILTER (WHERE dur < 60)  AS le_60,
       COUNT(*) FILTER (WHERE dur < 300) AS le_300,
       COUNT(*)                           AS le_inf
FROM (
    SELECT kind,
           EXTRACT(EPOCH FROM (completed_at - started_at)) AS dur
    FROM   jobs
    WHERE  status = 'succeeded'
      AND  completed_at > NOW() - INTERVAL '1 hour'
      AND  started_at IS NOT NULL
      AND  completed_at IS NOT NULL
) sub
GROUP BY kind;
```

---

## §6 — Contract test structure

Tests are written against the `JobStore` **interface**, not against `pgJobStore`
directly. This means any future `redisJobStore` or `natsJobStore` implementation can
run the same suite and be validated as a safe drop-in.

```go
// store_contract_test.go
func RunJobStoreContractTests(t *testing.T, newStore func(t *testing.T) JobStore) {
    t.Run("ClaimJob/returns nil when no pending jobs", ...)
    t.Run("ClaimJob/respects paused kinds", ...)
    t.Run("ClaimJob/respects run_after", ...)
    t.Run("ClaimJob/effective_priority ordering", ...)
    t.Run("PurgeDeadJobs/removes only jobs older than threshold", ...)
    t.Run("PurgeCompletedJobs/removes succeeded and cancelled beyond retention", ...)
    t.Run("RetryDeadJob/resets status to pending", ...)
}

// pg_store_test.go
func TestPgJobStore(t *testing.T) {
    RunJobStoreContractTests(t, func(t *testing.T) JobStore {
        return newTestPgJobStore(t)
    })
}
```

If `TestRedisJobStore` passes `RunJobStoreContractTests`, the swap is safe.
If any case fails, it is not a drop-in replacement.

---

## §7 — Test inventory

| # | Case | Layer |
|---|------|-------|
| T-26 | ClaimJob: returns nil when no pending jobs | I |
| T-27 | ClaimJob: respects paused kinds | I |
| T-28 | ClaimJob: respects run_after | I |
| T-29 | ClaimJob: effective_priority ordering | I |
| T-30 | PurgeDeadJobs: removes only jobs older than threshold | I |
| T-31 | PurgeCompletedJobs: removes succeeded/cancelled beyond RetentionDays | I |
| T-32 | RetryDeadJob: resets status to pending | I |
