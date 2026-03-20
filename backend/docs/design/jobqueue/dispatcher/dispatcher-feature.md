# Dispatcher Feature — Behavior & Design Rationale

> **What this file is:** A plain-language description of how worker goroutines claim
> and run jobs, how Redis wake-up and the Postgres fallback interact, and the
> behavioral rules the Dispatcher enforces. Read this before the technical file.
>
> **Companion:** `dispatcher-technical.md` — worker loop code, Redis downtime
> handling, stall reset logic, test inventory.
> **Schema reference:** `sql/schema/007_jobqueue.sql` (`jobs`, `workers` tables).

---

## What the Dispatcher is

The Dispatcher is the component that runs jobs. It owns a pool of N worker goroutines
(configured via `ManagerConfig.Workers`). Each goroutine continuously looks for
claimable jobs, runs the appropriate handler, and writes the outcome back to the
database. There is one Dispatcher per running application instance.

The Dispatcher registers itself in the `workers` table on startup and sends a
heartbeat every 15 seconds while running.

---

## How workers find jobs — Redis wake + Postgres fallback

Workers do not poll the database on a tight loop. That would waste DB connections and
CPU for no benefit. Instead, each worker goroutine waits on two signals simultaneously:

**Redis wake signal.** When any job is submitted (or when a schedule fires), the
server publishes an empty message on the `jobqueue:notify` Redis channel. All worker
goroutines subscribed to that channel wake up immediately — latency under 1ms.

**10-second Postgres ticker.** Always running, regardless of Redis state. This is the
fallback. If Redis is down, workers keep polling Postgres every 10 seconds and jobs
continue to run. The maximum extra latency during a Redis outage is 10 seconds.

The combination means: under normal conditions, jobs start within milliseconds of
being submitted. Under Redis downtime, jobs start within 10 seconds. Job state is
never at risk — it lives entirely in Postgres.

---

## What happens when N workers wake for 1 job

When one job is submitted, one Redis PUBLISH wakes all N worker goroutines across all
instances simultaneously. Each goroutine runs the SKIP LOCKED claim query. Only one
wins. The rest get nothing back and return to waiting. This is the documented N-1
wasted claim ceiling — acceptable at store-backend scale, documented with an upgrade
path in the ceilings section.

---

## How a job is processed

Once a worker claims a job, it:

1. Looks up the registered handler for the job's `kind`.
2. Creates a context with a deadline based on the job's `timeout_seconds`.
3. Calls `handler.Handle(ctx, job)`.
4. On success: calls `store.CompleteJob`, notifies MetricsRecorder, broadcasts
   `job.succeeded` to WebSocket clients.
5. On error: calls `store.FailJob`. If `attempt < max_attempts`, the job is reset to
   `pending` with an exponential backoff `run_after`. If attempts are exhausted,
   `store.DeadLetterJob` is called instead.

A handler panic is recovered and treated as an error — it counts against
`max_attempts`. The process does not crash.

---

## Unknown kind

If a job's `kind` has no registered handler, the job is dead-lettered immediately
(without consuming an attempt) and a warning is logged. The Dispatcher never panics
on an unknown kind. This allows operators to remove a handler kind from the codebase
and cleanly handle any residual jobs that were already in the queue.

---

## Pause behavior

Paused kinds are checked in the claim SQL query (`kind NOT IN (SELECT kind FROM
job_paused_kinds)`). The Dispatcher does not need to know about pauses at the Go
level. Pausing a kind means workers simply never claim jobs of that kind until the
pause is lifted. Jobs remain in `pending` status and accumulate in the queue without
being lost.

---

## Graceful shutdown

When `Manager.Shutdown()` is called, the Dispatcher:

1. Stops the Redis SUBSCRIBE listener — no new wake signals are processed.
2. Signals all worker goroutines to stop accepting new jobs.
3. Waits for in-flight jobs to complete (bounded by a drain timeout).
4. Calls `store.MarkWorkerOffline` to update the `workers` row.

Jobs that were running at shutdown time finish normally. No job is abandoned
mid-execution during a graceful shutdown.
