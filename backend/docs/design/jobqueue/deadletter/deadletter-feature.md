# Dead Letter Feature — Behavior & Design Rationale

> **What this file is:** A plain-language description of when jobs die, what dead
> means for operators, how retry-from-dead works, and the purge policy. Read this
> before the technical file.
>
> **Companion:** `deadletter-technical.md` — DB queries, purge SQL, test inventory.
> **Schema reference:** `sql/schema/007_jobqueue.sql` (`jobs` table, `idx_jobs_dead`).

---

## What "dead" means

A job is dead when it has failed `max_attempts` times and there are no more retries.
It will not run again without explicit operator action. Dead jobs are the queue's
equivalent of a permanent failure signal — they indicate that a handler is broken,
a dependency is down, or an input is malformed in a way the system cannot self-heal.

Dead jobs are kept in the `jobs` table rather than a separate table. The `status =
'dead'` partial index and the `dead_at` column make them efficient to query. They are
visible in the admin interface at `GET /admin/jobqueue/dead`.

---

## When a job dies

A job transitions to `dead` when `attempt >= max_attempts` and the handler returns an
error. The final `last_error` is written to the row. `dead_at` is set to the current
timestamp.

Unknown-kind jobs are also dead-lettered, but without consuming an attempt. If a
handler kind is removed from the codebase while jobs of that kind are still in the
queue, those jobs are dead-lettered on the first claim attempt with a clear error
message identifying the missing kind.

---

## Retrying a dead job

An operator can manually re-queue a dead job via `POST /admin/jobqueue/jobs/:id/retry`.
This resets the job to `pending` status, clears `dead_at`, resets `attempt` to 0
(giving the job its full retry budget again), and sets `run_after = NOW()`. The job
will be claimed by the next available worker.

This is the recovery path when a dependency was down, a handler bug was fixed, or
the payload needs reprocessing after a code change.

---

## Purging dead jobs

Dead jobs accumulate over time. The `DELETE /admin/jobqueue/dead?older_than=7d`
endpoint (and the underlying `PurgeDeadJobs` store method) removes dead jobs older
than the specified threshold. This is an operator-initiated action, not an automatic
one.

For completed jobs (succeeded, failed, cancelled), the `purge_completed_jobs_daily`
scheduled job handles automatic cleanup based on the `RetentionDays` setting (default
30 days). Dead jobs are intentionally kept longer than completed jobs — they represent
unresolved failures that may need forensic investigation.

---

## The dead-letter queue is not a separate store

Unlike some queue systems that move dead jobs to a separate dead-letter queue (DLQ)
topic or table, this system keeps dead jobs as rows in the main `jobs` table with
`status = 'dead'`. This simplifies the schema and means dead jobs are queryable with
the same filters as live jobs (by kind, by time range, by creator). The
`idx_jobs_dead` partial index ensures dead-job queries remain efficient regardless
of total table size.
