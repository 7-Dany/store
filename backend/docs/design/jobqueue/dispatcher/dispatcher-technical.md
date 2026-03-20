# Dispatcher Technical — Worker Loop, Redis Downtime, Tests

> **What this file is:** Implementation details for the Dispatcher: the worker loop,
> Redis downtime handling, retry backoff, panic recovery, and test inventory.
>
> **Read first:** `dispatcher-feature.md` — behavioral rules and design rationale.

---

## Table of Contents

1. [Worker loop](#1--worker-loop)
2. [Redis downtime handling](#2--redis-downtime-handling)
3. [tryClaimAndRun](#3--tryclaimandrun)
4. [Retry backoff](#4--retry-backoff)
5. [Heartbeat goroutine](#5--heartbeat-goroutine)
6. [Test inventory](#6--test-inventory)

---

## §1 — Worker loop

```go
// dispatcher.go — per-worker goroutine inner loop.
// d.notify is the channel from go-redis Subscribe (jobqueue:notify).
// d.cfg.RedisFallbackPoll defaults to 10s.

func (d *Dispatcher) workerLoop(ctx context.Context, workerID uuid.UUID) {
    ticker := time.NewTicker(d.cfg.RedisFallbackPoll)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return

        case _, ok := <-d.notify:
            // ok=false means Manager is shutting down — not Redis downtime.
            // During Redis downtime go-redis pauses delivery but keeps the channel open.
            if !ok {
                return
            }
            d.tryClaimAndRun(ctx, workerID)

        case <-ticker.C:
            // Fires every RedisFallbackPoll regardless of Redis state.
            // Only active path when Redis is down.
            // Also catches notifications missed during reconnect windows.
            d.tryClaimAndRun(ctx, workerID)
        }
    }
}
```

---

## §2 — Redis downtime handling

Three scenarios, all safe:

| Scenario | What happens | Max extra latency |
|----------|-------------|-------------------|
| Redis blip (<1s) | go-redis reconnects; ticker catches missed signals | ≤10s |
| Redis down (minutes) | Subscribe channel goes quiet; 10s ticker fires independently | ≤10s per job |
| Redis down at startup | go-redis dials with retries; ticker fires immediately | ≤10s |

After inserting a job row, the server calls `PUBLISH`. If Redis is down, the publish
fails — the job row is already safely in Postgres. Workers pick it up on the next
ticker fire. Publish failures are logged at WARN and never block the submit path:

```go
// scheduler.go and manager.go — after inserting any job row:
if err := d.pubsub.Publish(ctx, "jobqueue:notify", ""); err != nil {
    d.logger.Warn("notify publish failed, workers will poll", "err", err)
    // job row is in postgres; workers will find it within RedisFallbackPoll seconds
}
```

WebSocket events stop arriving during Redis downtime (the WSHub uses pub/sub for
broadcast). The REST API (`GET /jobs`, `GET /stats`, `GET /workers`) continues to
serve from Postgres and remains fully operational.

---

## §3 — tryClaimAndRun

```go
func (d *Dispatcher) tryClaimAndRun(ctx context.Context, workerID uuid.UUID) {
    job, err := d.store.ClaimJob(ctx, workerID, d.cfg.Queues)
    if err != nil {
        d.logger.Error("claim job failed", "err", err)
        return
    }
    if job == nil {
        return // nothing to claim
    }

    d.metrics.OnJobClaimed(*job)

    handler, ok := d.handlers[job.Kind]
    if !ok {
        // Unknown kind — dead-letter immediately, no attempt consumed.
        d.logger.Warn("no handler registered for kind", "kind", job.Kind)
        _ = d.store.DeadLetterJob(ctx, job.ID, fmt.Errorf("no handler for kind %q", job.Kind))
        d.metrics.OnJobDead(*job)
        return
    }

    start := time.Now()
    runErr := d.safeHandle(ctx, job, handler)
    duration := time.Since(start)

    if runErr == nil {
        _ = d.store.CompleteJob(ctx, job.ID, nil)
        d.metrics.OnJobSucceeded(*job, duration)
        d.hub.Broadcast(WSEvent{Event: "job.succeeded", Data: jobSucceededPayload(*job, duration)})
        return
    }

    willRetry := job.Attempt < job.MaxAttempts
    if willRetry {
        retryAt := time.Now().Add(backoffDuration(job.Attempt))
        _ = d.store.FailJob(ctx, job.ID, runErr, &retryAt)
        d.metrics.OnJobFailed(*job, runErr, true)
    } else {
        _ = d.store.DeadLetterJob(ctx, job.ID, runErr)
        d.metrics.OnJobDead(*job)
    }
}

// safeHandle recovers panics and converts them to errors.
func (d *Dispatcher) safeHandle(ctx context.Context, job *Job, h Handler) (err error) {
    defer func() {
        if r := recover(); r != nil {
            err = fmt.Errorf("handler panic: %v", r)
            d.logger.Error("handler panicked", "kind", job.Kind, "id", job.ID, "panic", r)
        }
    }()
    jobCtx, cancel := context.WithTimeout(ctx,
        time.Duration(job.TimeoutSeconds)*time.Second)
    defer cancel()
    return h.Handle(jobCtx, *job)
}
```

---

## §4 — Retry backoff

Default exponential backoff with jitter. Configurable in `ManagerConfig`.

```go
func backoffDuration(attempt int) time.Duration {
    // attempt is 1-indexed at this point (already incremented by the claim query).
    base := time.Duration(attempt*attempt) * 10 * time.Second // 10s, 40s, 90s, 160s, 250s
    jitter := time.Duration(rand.Int63n(int64(base / 4)))
    return base + jitter
}
```

`FailJob` sets `run_after = NOW() + backoffDuration` and resets `status` to
`pending`. No goroutine sleeps. The job is invisible to workers until `run_after`
passes.

---

## §5 — Heartbeat goroutine

A separate goroutine inside the Dispatcher sends a heartbeat every `HeartbeatEvery`
(default 15s):

```go
func (d *Dispatcher) heartbeatLoop(ctx context.Context, workerID uuid.UUID) {
    ticker := time.NewTicker(d.cfg.HeartbeatEvery)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            activeJobs := d.activeJobCount.Load()
            if err := d.store.HeartbeatWorker(ctx, workerID, int(activeJobs)); err != nil {
                d.logger.Warn("heartbeat failed", "err", err)
            }
        }
    }
}
```

`HeartbeatWorker` updates `workers.heartbeat_at` and adjusts `workers.status` between
`idle` and `busy` based on `activeJobs`. The StallDetector uses `heartbeat_at` to
detect crashed workers.

---

## §6 — Test inventory

| # | Case | Layer |
|---|------|-------|
| T-01 | Happy path — job claimed, handled, completed | I |
| T-02 | Handler returns error → retried up to MaxAttempts | I |
| T-03 | Handler succeeds on 3rd attempt | I |
| T-04 | Unknown kind → dead-lettered immediately | I |
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
| T-17 | Low-priority job beats higher-priority after AgingCap minutes | I |
| T-18 | Aging does not exceed cap regardless of wait time | I |
| T-19 | priority=100 job still wins over aged priority=50 job | I |
