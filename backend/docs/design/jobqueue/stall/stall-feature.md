# Stall Detection Feature — Behavior & Design Rationale

> **What this file is:** A plain-language description of what stall detection is,
> when it fires, and the behavioral guarantees it provides. Read this before the
> technical file.
>
> **Companion:** `stall-technical.md` — SQL queries, StallDetector loop, worker
> offline marking, test inventory.
> **Schema reference:** `sql/schema/007_jobqueue.sql` (`jobs`, `workers` tables).

---

## What stall detection is

A running job becomes stalled when the worker executing it crashes or hangs and
never writes a completion. Without recovery, the job stays in `running` status
forever, blocked behind a dead worker.

The StallDetector is a single background goroutine that runs every 30 seconds and
finds these stalled jobs. It resets them to `pending` so another worker can pick
them up.

---

## Two kinds of stalls

**Timeout stalls** — a job's `timeout_seconds` has elapsed since `started_at` but
the job is still `running`. This covers handlers that are stuck in an infinite loop,
waiting on a dependency that will never respond, or otherwise not making progress.
The timeout gives each job a bounded maximum run time.

**Dead-worker stalls** — a worker's `heartbeat_at` has not been updated within its
`heartbeat_ttl_seconds`. The worker is presumed dead. Any job assigned to that worker
(`jobs.worker_id = dead_worker.id`) and still `running` is reset. The dead worker row
is also marked `offline`.

Both checks run in the same StallDetector tick.

---

## What "reset to pending" means

A stalled job is reset by setting `status = 'pending'`, clearing `worker_id`, and
setting `run_after = NOW()`. The `attempt` counter is NOT incremented — a stall is
not a handler failure. The job simply restarts from where it was lost, on the next
available worker.

This means a job with `max_attempts = 5` can stall and be reset multiple times without
consuming its retry budget. Only actual handler errors (the handler returning a
non-nil error) consume attempts.

---

## At-least-once execution guarantee

Stall detection provides an at-least-once execution guarantee: a job will eventually
run to completion even if the worker running it crashes. The flip side is that a job
may run more than once if:
- The worker completes the job successfully but crashes before writing the completion.
- The StallDetector resets the job before the worker's result is written.

Handlers that must not run twice should use the `idempotency_key` field. The
`execute_request` handler is a good example — it uses a key derived from the request
ID so that duplicate executions are no-ops.

---

## HeartbeatTTL and detection latency

With the defaults:
- Workers heartbeat every 15s (`HeartbeatEvery`)
- Worker TTL is 30s (`heartbeat_ttl_seconds`)
- StallDetector checks every 30s (`StallCheck`)

A worker crash is detected within at most 60 seconds: up to 30s until the heartbeat
TTL expires, plus up to 30s until the next StallDetector tick. Jobs assigned to the
dead worker are reset within that window. This is acceptable for a background job
system where sub-minute recovery is not a hard requirement.
