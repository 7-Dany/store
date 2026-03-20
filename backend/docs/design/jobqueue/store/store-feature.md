# Store Feature — Behavior & Design Rationale

> **What this file is:** A plain-language description of what the `JobStore`
> interface is responsible for, how priority aging works, and the behavioral
> rules callers must understand. Read this before the technical file.
>
> **Companion:** `store-technical.md` — `JobStore` interface, aging SQL, `pgJobStore`
> notes, contract test structure.
> **Schema reference:** `sql/schema/007_jobqueue.sql` (tables, enums, indexes).

---

## What the store is

`JobStore` is the single data access boundary for the entire job queue system. Every
component — Dispatcher, ScheduleWatcher, StallDetector, Manager, admin API — talks to
the database exclusively through this interface. No component holds its own `pgxpool`
and runs ad-hoc queries. This boundary is what makes the ceiling upgrade paths safe:
swapping `pgJobStore` for a Redis Streams implementation requires changing only this
layer.

---

## What the store owns

The store is responsible for every read and write against the four job queue tables:
`jobs`, `workers`, `job_paused_kinds`, and `job_schedules`. Its operations fall into
six groups:

**Worker operations** — claiming and completing jobs. These are the hot path. `ClaimJob`
runs the `SELECT FOR UPDATE SKIP LOCKED` query that atomically grabs one pending job
for a worker goroutine. `CompleteJob`, `FailJob`, and `DeadLetterJob` write the final
outcome.

**Submit** — inserting new job rows and cancelling existing ones. `InsertJob` uses
the idempotency key partial unique index to deduplicate concurrent submissions of the
same logical operation.

**Pause / Resume** — inserting and deleting rows in `job_paused_kinds`. Workers check
this table on every claim attempt; pausing a kind has effect within one poll cycle
without restarting any process.

**Management API** — list, filter, inspect, and mutate jobs for the admin interface.
`ListJobs`, `GetJob`, `RetryDeadJob`, `UpdateJobPriority`, `ListDeadJobs`,
`PurgeDeadJobs`, `PurgeCompletedJobs`.

**Workers** — upsert, heartbeat, offline marking, and listing for the Dispatcher and
StallDetector.

**Schedules** — CRUD for `job_schedules` rows. `EnsureSchedule` is idempotent —
called on server startup to seed the built-in schedules without risk of duplicates.

---

## Priority aging — what it is and why it exists

Every job has a base `priority` between -100 and 100. Under sustained high load, a
low-priority job (say, priority 0) could theoretically wait forever behind a stream
of freshly submitted high-priority jobs (say, priority 50). This is priority
starvation.

The fix is **effective priority**, computed at claim time:

```
effective_priority = base_priority + min(minutes_waited, aging_cap)
```

With the defaults (1 point per minute, cap 50), a `priority=0` job waiting 50 minutes
reaches `effective_priority=50` and will beat any fresh `priority=49` job. It will
never beat a fresh `priority=51+` job — high-priority work still wins. No job waits
more than ~150 minutes under any load pattern.

The aging formula is computed in SQL at claim time. There is no stored column, no
background job updating priorities, and no schema change needed to adjust the rate or
cap — they are parameters passed into the claim query from `ManagerConfig`.

---

## Idempotency key behavior

When a caller supplies an `idempotency_key`, the store enforces at-most-once
submission for that key across **active** rows only. The key is stored in a partial
unique index that excludes terminal-state rows (`succeeded`, `failed`, `dead`,
`cancelled`). This means:

- Submitting the same key twice for an active job returns the existing row silently.
- After a job completes (succeeds or dies), the same key can be reused for a new
  submission.

This is more useful than a global unique constraint, which would permanently block
resubmission of recurring operations identified by the same logical key.

---

## Behavioral rules

**Claim is atomic.** `FOR UPDATE SKIP LOCKED` means two workers can never claim the
same job. The claim and the status update (`pending` → `running`) happen in the same
transaction.

**Failure does not mean the job is done.** `FailJob` resets the job to `pending` with
a future `run_after` timestamp if `attempt < max_attempts`. Only when all attempts are
exhausted does the job become `dead` via `DeadLetterJob`.

**Purge only affects terminal rows.** `PurgeDeadJobs` and `PurgeCompletedJobs` only
delete rows in terminal states older than the supplied threshold. Running and pending
rows are never touched by purge operations.

**`GetStats` is scrape-oriented.** The `GetStats` method runs one SQL query designed
to produce all the data needed for a Prometheus scrape: per-kind-status counts,
worker counts by status, all-time totals from the `workers` table, and a job-duration
histogram. It is called on demand by `QueryMetricsRecorder.MetricsHandler()` — not
on a background timer.
