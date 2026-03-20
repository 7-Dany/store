# Stall Detection Technical — SQL, Loop, Worker Marking, Tests

> **What this file is:** Implementation details for the StallDetector: the tick
> loop, stall-reset SQL, dead-worker detection SQL, and test inventory.
>
> **Read first:** `stall-feature.md` — behavioral rules and design rationale.
> **Schema reference:** `sql/schema/007_jobqueue.sql`.

---

## Table of Contents

1. [StallDetector loop](#1--stalldetector-loop)
2. [RequeueStalledJobs SQL](#2--requeuestalledjobs-sql)
3. [MarkStaleWorkersOffline SQL](#3--markStaleworkersoffline-sql)
4. [Test inventory](#4--test-inventory)

---

## §1 — StallDetector loop

```go
// stall.go — single goroutine.

func (sd *StallDetector) Run(ctx context.Context) {
    ticker := time.NewTicker(sd.cfg.StallCheck) // default 30s
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            sd.tick(ctx)
        }
    }
}

func (sd *StallDetector) tick(ctx context.Context) {
    // 1. Mark stale workers offline first — their jobs may need requeuing.
    n, err := sd.store.MarkStaleWorkersOffline(ctx, sd.cfg.HeartbeatTTL)
    if err != nil {
        sd.logger.Error("mark stale workers offline failed", "err", err)
    } else if n > 0 {
        sd.logger.Warn("marked workers offline", "count", n)
    }

    // 2. Requeue stalled jobs (timeout stalls + dead-worker stalls).
    requeued, err := sd.store.RequeueStalledJobs(ctx)
    if err != nil {
        sd.logger.Error("requeue stalled jobs failed", "err", err)
        return
    }
    if requeued > 0 {
        sd.logger.Warn("requeued stalled jobs", "count", requeued)
        sd.metrics.OnJobsRequeued(requeued)
        // Publish so workers wake immediately for the re-queued jobs.
        if err := sd.pubsub.Publish(ctx, sd.cfg.NotifyChannel, ""); err != nil {
            sd.logger.Warn("stall requeue notify failed, workers will poll", "err", err)
        }
    }
}
```

---

## §2 — RequeueStalledJobs SQL

Two types of stalls handled in one query:

```sql
-- Reset jobs that have exceeded their timeout OR whose assigned worker is dead.
-- attempt is NOT incremented — stalls are not handler failures.
UPDATE jobs
   SET status     = 'pending',
       worker_id  = NULL,
       run_after  = NOW(),
       updated_at = NOW()
 WHERE status = 'running'
   AND (
       -- Timeout stall: running past timeout_seconds since started_at.
       started_at + (timeout_seconds * INTERVAL '1 second') < NOW()
       OR
       -- Dead-worker stall: assigned worker has not heartbeated within its TTL.
       worker_id IN (
           SELECT id FROM workers
            WHERE status = 'offline'
       )
   )
RETURNING id;
```

`idx_jobs_stall` (`WHERE status = 'running'`) keeps this scan bounded to active jobs
only. On a healthy system the running set is small.

---

## §3 — MarkStaleWorkersOffline SQL

```sql
-- $1 = now - heartbeat_ttl_seconds (the expiry threshold)
UPDATE workers
   SET status     = 'offline',
       stopped_at = NOW(),
       updated_at = NOW()
 WHERE status != 'offline'
   AND heartbeat_at + (heartbeat_ttl_seconds * INTERVAL '1 second') < NOW()
RETURNING id;
```

Called before `RequeueStalledJobs` so the dead-worker stall subquery in §2 finds
freshly marked offline workers in the same tick.

---

## §4 — Test inventory

| # | Case | Layer |
|---|------|-------|
| T-11 | Worker crash → stall detector resets job to pending | I |
| T-SL-1 | Timeout stall: job running > timeout_seconds is reset | I |
| T-SL-2 | Dead-worker stall: offline worker's running jobs are reset | I |
| T-SL-3 | Attempt counter is NOT incremented on stall reset | I |
| T-SL-4 | Stall reset publishes to notify channel | I+R |
| T-SL-5 | MarkStaleWorkersOffline marks only expired workers, not active ones | I |
