# Ceilings Technical — Machine-Level Mechanics, Diagnostics, Upgrade Paths

> **What this file is:** Concrete numbers, diagnostic SQL queries, machine-level
> mechanics, and step-by-step upgrade instructions for each ceiling.
>
> **Read first:** `ceilings-feature.md` — trade-off descriptions, when-to-act
> thresholds, upgrade compatibility guarantee.
> **Source of truth:** `../1-ceilings.md` (full deep-dive reference document).

---

## Table of Contents

1. [Ceiling 1 — N-1 Wasted Claim Attempts](#1--ceiling-1--n-1-wasted-claim-attempts)
2. [Ceiling 2 — Metrics Endpoint (resolved in V1)](#2--ceiling-2--metrics-endpoint-resolved-in-v1)
3. [Ceiling 3 — Postgres Write Bottleneck](#3--ceiling-3--postgres-write-bottleneck)

---

## §1 — Ceiling 1 — N-1 Wasted Claim Attempts

### What happens at the machine level

One job submitted → one `PUBLISH` → all N worker goroutines wake → each runs:

```sql
WITH claimed AS (...)
UPDATE jobs SET status='running' ... WHERE id = (SELECT id FROM claimed)
RETURNING *;
```

`FOR UPDATE SKIP LOCKED` means: try to acquire a row-level lock on the target row; if
another transaction holds it, skip and return nothing. The N-1 losers each complete a
full TCP round-trip to Postgres, scan the index, attempt the lock, fail, and return
zero rows. At 2ms per wasted query:

| Config | Jobs/min | Wasted queries/min |
|--------|----------|--------------------|
| 2 instances × 4 workers | 60 | 420 |
| 2 instances × 4 workers | 600 | 4,200 |
| 4 instances × 8 workers | 600 | 18,600 |

At the 2×4 / 600 jobs/min row, Postgres handles 4,200 empty queries per minute
trivially. At 4×8 / 6,000 jobs/min it becomes measurable.

### Diagnostic queries

```sql
-- Are there consistent lock waits on jobs rows?
SELECT pid, wait_event_type, wait_event, state, left(query, 80) AS query
FROM   pg_stat_activity
WHERE  query LIKE '%FOR UPDATE SKIP LOCKED%';
-- If this returns rows consistently → contention problem.

-- How fast are claim queries?
SELECT mean_exec_time, max_exec_time, calls, left(query, 80) AS query
FROM   pg_stat_statements
WHERE  query LIKE '%FOR UPDATE SKIP LOCKED%'
ORDER  BY mean_exec_time DESC;
-- P95 > 20ms at peak → act.
```

### The upgrade — Redis Streams with consumer groups

Replace the `PubSub` implementation with Redis Streams + consumer groups.
Work-queue semantics: exactly one goroutine receives each stream entry.

```
Before: PUBLISH jobqueue:notify ""   → all N goroutines wake
After:  XADD   jobqueue:stream * id <uuid>   → one goroutine receives via XREADGROUP
```

`PubSub` is an interface in `job.go`. The Dispatcher's `notify` channel source
changes; everything above is untouched. Pass `RunPubSubContractTests` before merging.

Memory note: Redis Streams store entries until ACK'd. With prompt ACK after each
SKIP LOCKED call (win or lose), entries are removed immediately. Memory cost ≈ one
entry per concurrent job submission burst — negligible.

---

## §2 — Ceiling 2 — Metrics Endpoint (resolved in V1)

### V1 implementation

`GET /admin/jobqueue/metrics` is live. `QueryMetricsRecorder.MetricsHandler()` runs
`store.GetStats()` once per scrape and writes Prometheus text exposition format.

```go
// api.go — AdminRouter():
r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
    m.metrics.MetricsHandler().ServeHTTP(w, r)
})
```

The SQL behind `GetStats()` runs in well under 10ms on a properly indexed table. At
a 15-second Prometheus scrape interval this is 4 queries per minute — negligible.

### V2 swap — in-process counters

When `prometheus/client_golang` in-process counters are preferred:

```go
// server.New — one line change:
rec := jobqueue.NewPrometheusMetricsRecorder(prometheus.DefaultRegisterer)
mgr := jobqueue.NewManager(jobqueue.ManagerConfig{
    Metrics: rec,
    // everything else unchanged
})
```

`PrometheusMetricsRecorder` event hooks increment `Counter` and `Histogram` objects
in-process. `MetricsHandler()` returns `promhttp.HandlerFor(registry, ...)`. No SQL
per scrape. No other files change.

Pass `RunMetricsRecorderContractTests` before merging.

---

## §3 — Ceiling 3 — Postgres Write Bottleneck

### Why Postgres is the ceiling (not Go)

Every job processed = 3 writes to `jobs`: INSERT (submit) + UPDATE (claim) + UPDATE
(complete). At 1,000 jobs/second: 3,000+ writes/second on one table. Bottlenecks:

- **WAL sync latency** — on cloud volumes (`gp2`), every commit waits for `fsync`.
- **MVCC dead tuple accumulation** — 3,000 UPDATEs/second = 3,000 dead tuples/second
  that autovacuum must reclaim.
- **Index maintenance** — every write updates multiple B-tree indexes.
- **Connection pressure** — each worker goroutine holds a Postgres connection.

### Practical ceiling by hardware

| Hardware | Config | Practical ceiling |
|----------|--------|-----------------|
| 4 vCPU, 8GB, gp3 EBS | pgBouncer transaction mode | ~200–400 jobs/s |
| 8 vCPU, 16GB, NVMe | pgBouncer + dedicated pool | ~500–1,000 jobs/s |
| 16 vCPU, 32GB, NVMe | Above + partitioning | ~1,500–2,500 jobs/s |

### Diagnostic queries

```sql
-- Autovacuum falling behind?
SELECT relname, n_dead_tup, last_autovacuum, autovacuum_count
FROM   pg_stat_user_tables
WHERE  relname = 'jobs';
-- n_dead_tup growing without last_autovacuum advancing = autovacuum cannot keep up.

-- Index bloat?
SELECT pg_size_pretty(pg_relation_size('idx_jobs_claimable')) AS idx_size,
       pg_size_pretty(pg_relation_size('jobs'))               AS tbl_size;

-- Lock waits?
SELECT count(*), state, wait_event_type, wait_event
FROM   pg_stat_activity
WHERE  query LIKE '%jobs%'
GROUP BY state, wait_event_type, wait_event;
```

### Step-by-step upgrade path

**Step 1 — pgBouncer in transaction mode**

Zero code change. One new process. Separates connection slots from goroutine count.
Eliminates connection exhaustion, reduces per-connection overhead 20–40%.

```ini
[pgbouncer]
pool_mode = transaction          ; essential
max_client_conn = 200
default_pool_size = 20
```

**Step 2 — Dedicated pgxpool for the job queue**

`ManagerConfig.Pool` exists for this. Create a second pool in `server.New`:

```go
jobPool := pgxpool.New(ctx, cfg.DatabaseURL, &pgxpool.Config{
    MaxConns: int32(cfg.JobWorkers) + 2,
})
mgr := jobqueue.NewManager(jobqueue.ManagerConfig{Pool: jobPool, ...})
```

Zero logic changes. Isolates job queue DB load from API handler DB load.

**Step 3 — Postgres table partitioning by status**

```sql
-- Recreate jobs as a partitioned table (maintenance window required):
CREATE TABLE jobs_partitioned (...) PARTITION BY LIST (status);
CREATE TABLE jobs_pending   PARTITION OF jobs_partitioned FOR VALUES IN ('pending');
CREATE TABLE jobs_running   PARTITION OF jobs_partitioned FOR VALUES IN ('running');
CREATE TABLE jobs_succeeded PARTITION OF jobs_partitioned FOR VALUES IN ('succeeded');
-- etc.
```

No application code change — `pgJobStore` SQL runs identically against a partitioned
table. VACUUM only processes the relevant partition. `pending` partition stays tiny
and hot; `succeeded` holds the bulk and is only written once per job.

**Step 4 — Swap JobStore for a Redis Streams implementation**

Removes Postgres from the hot execution path entirely. Redis Streams provide
>100,000 messages/second throughput with native consumer group exactly-one delivery.

What changes: `store.go` gets a new `redisJobStore` implementation + a new
`archiver.go` goroutine that writes completed jobs to Postgres asynchronously for
audit and query purposes.

What does NOT change: `Handler` interface, `Submitter` interface, REST API, WebSocket
protocol, all domain handlers.

Pass `RunJobStoreContractTests` with the new implementation before merging.

**Step 5 — NATS JetStream**

Same interface swap as Step 4. Adds multi-region fan-out, exactly-once delivery,
stream replay. Higher operational complexity (new NATS cluster). Justified only if
multi-region processing or event sourcing semantics are needed.
