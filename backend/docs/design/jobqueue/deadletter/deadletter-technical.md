# Dead Letter Technical — Queries, Purge SQL, Tests

> **What this file is:** Implementation details for dead-letter operations: the
> DeadLetterJob write, RetryDeadJob reset, purge queries, and test inventory.
>
> **Read first:** `deadletter-feature.md` — behavioral rules and design rationale.
> **Schema reference:** `sql/schema/007_jobqueue.sql`.
> **Terminal state protection:** `sql/schema/008_jobqueue_functions.sql`
> (`trg_jobs_prevent_terminal_change` — blocks UPDATE on terminal rows, with an
> explicit carve-out for `dead → pending` so `RetryDeadJob` works without any
> special application-layer bypass).

---

## Table of Contents

1. [DeadLetterJob](#1--deadletterjob)
2. [RetryDeadJob](#2--retrydeadjob)
3. [Purge queries](#3--purge-queries)
4. [Terminal state protection — how the carve-out works](#4--terminal-state-protection--how-the-carve-out-works)
5. [Test inventory](#5--test-inventory)

---

## §1 — DeadLetterJob

Called by the Dispatcher when `attempt >= max_attempts` or when an unknown kind is
encountered.

```sql
UPDATE jobs
   SET status      = 'dead',
       dead_at     = NOW(),
       last_error  = $2,
       completed_at = NOW(),
       updated_at  = NOW()
 WHERE id     = $1
   AND status = 'running';
```

The `AND status = 'running'` guard ensures this only fires on the current running
attempt. Once a job is `dead`, `trg_jobs_prevent_terminal_change` blocks any further
update (except the explicit `dead → pending` retry carve-out).

---

## §2 — RetryDeadJob

Called by `POST /admin/jobqueue/jobs/:id/retry`. Resets the job to `pending` with a
full retry budget.

```sql
UPDATE jobs
   SET status       = 'pending',
       dead_at      = NULL,
       attempt      = 0,
       last_error   = NULL,
       run_after    = NOW(),
       worker_id    = NULL,
       started_at   = NULL,
       completed_at = NULL,
       updated_at   = NOW()
 WHERE id     = $1
   AND status = 'dead';
```

The `AND status = 'dead'` guard makes this idempotent — calling retry on a job that
is already `pending` (already retried) returns 0 rows affected. The API layer returns
404 in that case.

`trg_jobs_prevent_terminal_change` has an explicit `NOT (OLD.status = 'dead' AND
NEW.status = 'pending')` carve-out in its WHEN clause, so this UPDATE bypasses the
trigger at the Postgres level. No session variable, advisory lock, or application-layer
bypass is needed.

---

## §3 — Purge queries

**PurgeDeadJobs** — operator-initiated via admin API:
```sql
DELETE FROM jobs
 WHERE status  = 'dead'
   AND dead_at < NOW() - $1::interval   -- $1 e.g. '7 days'
RETURNING id;
```

Uses `idx_jobs_dead` (partial index `WHERE status = 'dead'`, ordered by `dead_at DESC`).

**PurgeCompletedJobs** — run by `purge_completed_jobs_daily` schedule:
```sql
DELETE FROM jobs
 WHERE status IN ('succeeded', 'failed', 'cancelled')
   AND completed_at < NOW() - $1::interval   -- $1 = RetentionDays
RETURNING id;
```

Uses `idx_jobs_terminal_cleanup` (partial index on `completed_at WHERE status IN
('succeeded','failed','cancelled')`). Bounded delete — never scans the full table.

---

## §4 — Terminal state protection — how the carve-out works

`trg_jobs_prevent_terminal_change` (from `008_jobqueue_functions.sql`) fires
`BEFORE UPDATE` only when both conditions in the WHEN clause are true:

```sql
WHEN (
    OLD.status IN ('succeeded', 'failed', 'dead', 'cancelled')
    AND NOT (OLD.status = 'dead' AND NEW.status = 'pending')
)
```

This means:

| OLD.status | NEW.status | Trigger fires? | Effect |
|------------|------------|----------------|--------|
| `succeeded` | anything | Yes | Blocked — fully irreversible |
| `failed` | anything | Yes | Blocked — fully irreversible |
| `cancelled` | anything | Yes | Blocked — fully irreversible |
| `dead` | `pending` | **No** | Allowed — `RetryDeadJob` carve-out |
| `dead` | anything else | Yes | Blocked |
| `pending` / `running` | anything | No | Not a terminal state; trigger skipped |

The function body is only entered for genuinely illegal mutations. `RetryDeadJob`
never touches the function — the WHEN clause filters it out at zero cost.

---

## §5 — Test inventory

| # | Case | Layer |
|---|------|-------|
| T-DL-1 | Job exhausting max_attempts transitions to dead | I |
| T-DL-2 | Unknown kind dead-lettered without incrementing attempt | I |
| T-DL-3 | RetryDeadJob resets to pending with full attempt budget | I |
| T-DL-4 | RetryDeadJob on non-dead job returns 0 rows (idempotent) | I |
| T-DL-5 | PurgeDeadJobs removes only jobs older than threshold | I |
| T-DL-6 | PurgeCompletedJobs removes only terminal jobs older than retention | I |
| T-DL-7 | Dead job is visible in GET /dead response | I |
| T-DL-8 | Trigger blocks UPDATE on succeeded row (check_violation raised) | I |
| T-DL-9 | Trigger blocks UPDATE on cancelled row (check_violation raised) | I |
| T-DL-10 | Trigger allows dead → pending transition (RetryDeadJob carve-out) | I |
