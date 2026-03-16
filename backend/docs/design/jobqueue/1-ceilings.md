# Job Queue — Known Ceilings: Deep Dive

**Companion to:** `0-design.md` §11
**Audience:** Engineers who need to understand exactly what each ceiling means,
how to detect it, and how to cross it when the time comes.

These are not design mistakes. They are documented trade-offs of the chosen
Postgres-backed, Redis-signaled architecture. Each section explains what happens
at the machine level, gives concrete numbers, and provides a step-by-step upgrade
path that does not require rewriting the application layer.

---

## Ceiling 1 — DB Efficiency: N-1 Wasted Claim Attempts (9/10)

### What actually happens at the machine level

When a job is submitted, `InsertJob` completes and the Dispatcher publishes:

```go
redisStore.Publish(ctx, "jobqueue:notify", "")
```

Redis delivers this to every active subscriber simultaneously — it is a broadcast,
not a queue. Every worker goroutine blocked on `<-d.notify` unblocks at the same
instant. Each independently races to the database and executes:

```sql
WITH claimed AS (
    SELECT id
    FROM   jobs
    WHERE  status = 'pending' AND run_after <= NOW()
      AND  kind NOT IN (SELECT kind FROM job_paused_kinds)
    ORDER BY effective_priority DESC, created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED   -- ← the key line
)
UPDATE jobs
   SET status = 'running', worker_id = $1, attempt = attempt + 1
 WHERE id = (SELECT id FROM claimed)
RETURNING *;
```

Inside Postgres, `FOR UPDATE SKIP LOCKED` means: try to acquire a row-level lock on
the target row; if another transaction already holds it, skip it and move to the next
candidate. It does **not** wait — it skips instantly.

So when 12 goroutines fire simultaneously for one new job:

1. The fastest transaction reaches the target row first and acquires the lock.
2. It runs the UPDATE, commits, returns the job row to the winning goroutine.
3. The other 11 transactions each try to lock that same row, see it is already locked,
   skip it via SKIP LOCKED, find no other candidates, return zero rows from the CTE.
4. The UPDATE for each of those 11 transactions updates zero rows (WHERE id = NULL).
5. All 11 return `nil` to their goroutines, which go back to sleep.

The critical point: those 11 transactions were not free. Each one made a full TCP round
trip to Postgres, acquired a shared lock on the index pages it scanned, evaluated the
WHERE clause, attempted the row lock, failed, and returned. The latency per wasted query
on a local network is ~1–3ms. On a cloud network it can be 5–10ms.

### Concrete numbers

Assumptions: 2ms per wasted claim query, 4 workers per instance.

| Config | Jobs/min | Wasted queries/min | Cumulative wasted RTTs/min |
|--------|----------|--------------------|---------------------------|
| 1 instance × 4 workers | 60 | 180 | 360ms |
| 2 instances × 4 workers | 60 | 420 | 840ms |
| 2 instances × 4 workers | 600 | 4,200 | 8.4s |
| 4 instances × 8 workers | 600 | 18,600 | 37.2s |
| 4 instances × 8 workers | 6,000 | 186,000 | 372s |

"Cumulative wasted RTTs" is distributed across all connections — not a single bottleneck.
But it does represent real Postgres CPU time, real connection slot usage, and real index
lock traffic. At the 4-instance, 600 jobs/min row you are running 4,200 empty queries per
minute that accomplish nothing. Postgres handles this fine. At 6,000 jobs/min it becomes
a problem before you hit the INSERT/UPDATE write ceiling.

### Why the partial index does not fully solve this

The index `idx_jobs_claimable ON jobs(priority DESC, created_at ASC) WHERE status='pending'`
means the SKIP LOCKED scan only touches rows with `status='pending'`. In a healthy system
that set is small — maybe tens to hundreds of rows. The scan itself is fast. The wasted
cost is not the scan, it is the round-trip per goroutine per notification. You cannot
index away a network round-trip.

### When does this actually matter

Rule of thumb: **start worrying when wasted queries exceed 10,000/minute sustained**,
or when `pg_stat_activity` shows consistent lock waits on the `jobs` table. At store
backend scale (10–100 jobs/min, 1–2 instances) you will never come close.

```sql
-- Diagnostic query: how many idle-in-transaction or lock-wait states?
SELECT count(*), state, wait_event_type, wait_event
FROM   pg_stat_activity
WHERE  query LIKE '%FOR UPDATE SKIP LOCKED%'
GROUP  BY state, wait_event_type, wait_event;

-- If you see lock waits here consistently, you have a contention problem.
```

### The fix: work-queue delivery semantics

The root cause is that Redis pub/sub is fan-out: every subscriber gets every message.
What you want for a job queue is work-queue: exactly one subscriber gets each message.

**Option A — Redis Streams with consumer groups (recommended first step)**

Redis Streams are an ordered log with consumer group support. A consumer group ensures
each message is delivered to exactly one consumer, acknowledged, and then removed. This
is native work-queue semantics built into Redis — the infrastructure you already run.

```
XADD  jobqueue:stream * event notify    -- producer (instead of PUBLISH)
XREADGROUP GROUP workers consumer1      -- each goroutine reads its own entry
           COUNT 1 BLOCK 5000
           STREAMS jobqueue:stream >
XACK  jobqueue:stream workers <id>      -- after claiming job from Postgres
```

With consumer groups: 12 goroutines watching → 1 job submitted → 1 goroutine receives
the stream entry → 1 goroutine runs SKIP LOCKED → 1 winner → 0 wasted queries.

The JobStore interface is unchanged. Only the Dispatcher's `notify` channel source
changes — instead of a Redis pub/sub channel, it reads from an `XREADGROUP` call.
The `PubSub` interface in `job.go` would gain a `Read` method or be replaced by a
new `WorkQueue` interface. Everything else in the codebase is untouched.

Memory overhead: Redis Streams do store entries until acknowledged. With prompt ACK
after each successful SKIP LOCKED claim (regardless of whether the claim won or lost),
entries are removed immediately. Memory cost ≈ one entry per concurrent job submission
burst, typically < 1KB at store backend scale.

**Option B — NATS JetStream**

NATS JetStream is a distributed messaging system with persistent streams, consumer
groups, exactly-once delivery, and multi-region support. It is operationally heavier
than Redis Streams (new process to run, new infra to monitor) but provides a
significantly higher ceiling — millions of messages/second across multiple nodes.

The upgrade path is identical: swap the `PubSub` / `WorkQueue` implementation. The
`JobStore` interface, Dispatcher logic, handlers, REST API, and WebSocket protocol
are all untouched.

**Why not fix this now:** At current scale, the wasted queries are invisible in any
monitoring system. Fixing it would add complexity to the Dispatcher for no measurable
benefit. The interface boundary in `job.go` means this is a refactor when needed, not
a rewrite.

---

## Ceiling 2 — Observability: No Metrics Endpoint (9/10)

### What is already excellent

The current design has three observability layers:

**Layer 1 — Postgres as queryable state store.** Every job, every worker, every
schedule, every dead-letter entry is a row in a table. Any operator with `psql` access
can answer any operational question in real time:

```sql
-- How backed up are we right now?
SELECT kind, count(*), min(created_at) AS oldest
FROM   jobs WHERE status = 'pending'
GROUP  BY kind ORDER BY count DESC;

-- Which jobs are taking the longest?
SELECT id, kind, extract(epoch FROM NOW()-started_at) AS seconds_running
FROM   jobs WHERE status = 'running'
ORDER  BY seconds_running DESC LIMIT 10;

-- Dead job rate by kind this week
SELECT kind, count(*) AS dead_count
FROM   jobs WHERE status = 'dead' AND dead_at > NOW() - INTERVAL '7 days'
GROUP  BY kind ORDER BY dead_count DESC;
```

**Layer 2 — REST API.** `GET /stats` provides aggregate throughput, error rate, and
queue depth. `GET /workers` shows per-instance state. `GET /dead` gives paginated
dead-letter entries. All sourced from Postgres, always accurate, requires no extra
infrastructure to query.

**Layer 3 — WebSocket real-time stream.** Every job state transition, every worker
status change, every schedule fire is broadcast as a typed JSON event within
milliseconds of occurring. An admin dashboard built on this stream has sub-second
visibility into system behaviour without polling.

### What is missing and why it matters

A **Prometheus-compatible metrics endpoint** (`GET /metrics` serving text/plain in
the Prometheus exposition format) is the standard interface for:

- **Prometheus scraping** — Prometheus pulls `/metrics` on a configurable interval
  (typically 15–30s) and stores time-series data. Without this, the job queue is
  invisible to Prometheus entirely.
- **Grafana dashboards** — Grafana queries Prometheus. Without a metrics endpoint,
  you cannot build historical graphs of queue depth, throughput, or error rate. You
  can only see current state via the REST API.
- **Alertmanager rules** — Prometheus evaluates alert rules against scraped metrics.
  Without the endpoint, you cannot write `dead_job_count > 0 for 5m` alerts that
  page on-call automatically.
- **SLO tracking** — measuring "99% of jobs complete within 30 seconds" requires
  a histogram of job durations over time. The REST API gives you point-in-time counts,
  not historical distributions.

The gap is: **you have all the data, you just cannot expose it to standard tooling.**

### Exactly what the V2 endpoint looks like

Adding `GET /metrics` requires no schema changes, no new data access patterns, and no
new infrastructure. It reads the same aggregates that `GET /stats` already computes
and formats them differently.

```go
// api.go — one new route in AdminRouter():
r.Get("/metrics", mgr.handleMetrics)

// The handler runs one SQL query (same as GET /stats) and writes Prometheus text:
func (m *Manager) handleMetrics(w http.ResponseWriter, r *http.Request) {
    stats, err := m.store.GetStats(r.Context())
    if err != nil {
        http.Error(w, "internal error", 500)
        return
    }
    w.Header().Set("Content-Type", "text/plain; version=0.0.4")

    // Gauge: current queue depth by status and kind
    for _, row := range stats.ByKindStatus {
        fmt.Fprintf(w, "jobqueue_jobs_total{kind=%q,status=%q} %d\n",
            row.Kind, row.Status, row.Count)
    }

    // Gauge: worker count by status
    for _, row := range stats.WorkersByStatus {
        fmt.Fprintf(w, "jobqueue_workers_total{status=%q} %d\n",
            row.Status, row.Count)
    }

    // Counter: jobs processed since last restart (from workers table)
    fmt.Fprintf(w, "jobqueue_succeeded_total %d\n", stats.TotalSucceeded)
    fmt.Fprintf(w, "jobqueue_failed_total %d\n",    stats.TotalFailed)
    fmt.Fprintf(w, "jobqueue_dead_total %d\n",      stats.TotalDead)

    // Histogram: job duration in seconds (requires bucketing completed jobs)
    // started_at and completed_at already exist — just need a histogram query
    for _, bucket := range stats.DurationBuckets {
        fmt.Fprintf(w, "jobqueue_duration_seconds_bucket{kind=%q,le=%q} %d\n",
            bucket.Kind, bucket.Le, bucket.Count)
    }
}
```

The full Prometheus client library (`prometheus/client_golang`) is optional — the
exposition format is plain text and trivial to write manually for a fixed set of
metrics. Using the library gives you process metrics (CPU, memory, GC) for free
alongside the job queue metrics.

### The SQL behind the duration histogram

```sql
-- Called once per scrape (every 15–30s from Prometheus)
SELECT
    kind,
    COUNT(*) FILTER (WHERE extract(epoch FROM completed_at - started_at) < 1)    AS le_1,
    COUNT(*) FILTER (WHERE extract(epoch FROM completed_at - started_at) < 5)    AS le_5,
    COUNT(*) FILTER (WHERE extract(epoch FROM completed_at - started_at) < 30)   AS le_30,
    COUNT(*) FILTER (WHERE extract(epoch FROM completed_at - started_at) < 60)   AS le_60,
    COUNT(*) FILTER (WHERE extract(epoch FROM completed_at - started_at) < 300)  AS le_300,
    COUNT(*)                                                                       AS le_inf
FROM   jobs
WHERE  status = 'succeeded'
  AND  completed_at > NOW() - INTERVAL '1 hour'  -- rolling window, not all-time
  AND  started_at IS NOT NULL
  AND  completed_at IS NOT NULL
GROUP  BY kind;
```

This is one query, runs in well under 10ms on a properly indexed table, and gives
Prometheus everything it needs to compute P50/P95/P99 latency per job kind.

### Alert rules to write when the endpoint exists

```yaml
# prometheus/alerts/jobqueue.yml

groups:
  - name: jobqueue
    rules:

      # Dead jobs accumulating — handler is broken or dependencies are down
      - alert: JobQueueDeadJobsAccumulating
        expr: increase(jobqueue_jobs_total{status="dead"}[5m]) > 0
        for: 5m
        labels:
          severity: page
        annotations:
          summary: "Dead jobs accumulating in {{ $labels.kind }}"
          description: "{{ $value }} jobs dead in last 5 min. Check handler logs."

      # Queue backing up — workers may be too slow or too few
      - alert: JobQueuePendingBacklog
        expr: jobqueue_jobs_total{status="pending"} > 500
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Job queue backlog: {{ $value }} pending jobs"

      # Worker went offline unexpectedly
      - alert: JobQueueWorkerOffline
        expr: jobqueue_workers_total{status="offline"} > 0
        for: 2m
        labels:
          severity: page
        annotations:
          summary: "Job queue worker is offline"

      # execute_request jobs taking too long — approval execution SLA breach
      - alert: JobQueueExecutionSLABreach
        expr: |
          histogram_quantile(0.95,
            rate(jobqueue_duration_seconds_bucket{kind="execute_request"}[10m])
          ) > 30
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "execute_request P95 latency {{ $value }}s exceeds 30s SLA"
```

### Why this is a V2 addition and not V1

The REST API and WebSocket stream cover 100% of operational needs during the initial
launch period. The Prometheus endpoint becomes valuable only once:

1. The system has been running long enough to need historical trend analysis.
2. You have a Prometheus + Grafana stack running (adding this before you have that
   stack has no benefit).
3. You are setting SLOs that require measurement over time.

None of those conditions exist at launch. The endpoint is a single `api.go` addition
that can be shipped in a one-hour PR. There is no reason to block V1 on it.

---

## Ceiling 3 — Scalability: Postgres as the Write Bottleneck (9/10)

### Why Postgres is the ceiling and not Go

Go's goroutine scheduler can handle tens of thousands of concurrent goroutines on
commodity hardware. The Dispatcher's worker goroutines spend most of their time
blocked on I/O — waiting for Postgres to return a claim result, or waiting for a
handler to complete. The Go runtime is not the bottleneck at any scale you will reach
with a store backend.

The bottleneck is Postgres write throughput on a single table.

Every job processed requires exactly three writes to the `jobs` table:
1. `INSERT` — when submitted.
2. `UPDATE SET status='running'` — when claimed by a worker.
3. `UPDATE SET status='succeeded'` (or failed/dead) — when completed.

Plus periodic `DELETE` from the daily purge job. Plus `UPDATE` on retry for failed
jobs. At 1,000 jobs/second sustained, the `jobs` table receives 3,000+ writes/second
on a single heap. That is the threshold at which Postgres's WAL write rate, tuple
visibility management (MVCC), and autovacuum overhead start to visibly compete with
query throughput.

### The breakdown by component

**WAL (Write-Ahead Log):** Every write is recorded in the WAL before it is applied to
the heap. At 3,000 writes/second, WAL generation is continuous. On an NVMe SSD with
sequential write throughput of 2–3 GB/s, WAL is not the bottleneck. On a cloud volume
with high write latency (AWS `gp2`, for example), WAL sync latency (`fsync`) directly
adds to commit latency for every INSERT and UPDATE.

**MVCC and dead tuple accumulation:** Postgres uses MVCC — old row versions are kept
in the heap until autovacuum removes them. Every UPDATE creates a new row version and
marks the old one dead. At 3,000 writes/second, the `jobs` table generates 3,000 dead
tuples/second. Autovacuum runs periodically to reclaim them. If autovacuum cannot keep
up, the table bloats, indexes grow stale, and query plans degrade.

**Index maintenance:** Every INSERT and UPDATE on `jobs` must update multiple indexes:
`idx_jobs_claimable`, `idx_jobs_stall`, `idx_jobs_created`, `idx_jobs_kind_status`,
and others. Each index update is a B-tree insertion or deletion. At high write rates,
index pages become contention points — multiple transactions competing to modify the
same index leaf page.

**Connection overhead:** Each worker goroutine holds a Postgres connection for the
duration of its claim + execute cycle. At 4 workers × 4 instances = 16 connections
just for the job queue. Plus the main application pool. Postgres's default
`max_connections` is 100 — shared across all applications. Without pgBouncer, a
sudden worker scale-out can exhaust connection slots.

### Concrete ceiling per configuration

These are approximate thresholds based on Postgres benchmark data (pgbench, River,
Que-Go benchmarks) on a well-tuned instance:

| Hardware | Config | Practical jobs/sec ceiling |
|----------|--------|---------------------------|
| 2 vCPU, 4GB RAM, gp2 EBS | Vanilla pgxpool | ~50–100 |
| 4 vCPU, 8GB RAM, gp3 EBS | pgBouncer transaction mode | ~200–400 |
| 8 vCPU, 16GB RAM, NVMe SSD | pgBouncer + dedicated job pool | ~500–1,000 |
| 16 vCPU, 32GB RAM, NVMe SSD | All of the above + partitioning | ~1,500–2,500 |

A store backend in production likely runs on a configuration similar to the second
or third row. The ceiling for practical purposes is **several hundred jobs/second**
— far above what approval workflows, hourly maintenance jobs, and notification
delivery will ever generate.

### The leading indicators — measure before acting

Do not tune preemptively. Act only when you see these in production:

```sql
-- 1. Lock wait time on jobs rows
SELECT pid, wait_event_type, wait_event, state, left(query, 80) AS query
FROM   pg_stat_activity
WHERE  wait_event_type = 'Lock'
  AND  query LIKE '%jobs%';
-- If this returns rows consistently, you have row-level lock contention.

-- 2. Autovacuum falling behind
SELECT relname, n_dead_tup, last_autovacuum, autovacuum_count
FROM   pg_stat_user_tables
WHERE  relname = 'jobs';
-- n_dead_tup growing without last_autovacuum updating = vacuum cannot keep up.

-- 3. Index bloat
SELECT pg_size_pretty(pg_relation_size('idx_jobs_claimable')) AS index_size,
       pg_size_pretty(pg_relation_size('jobs'))               AS table_size;
-- Index size growing disproportionate to table size = bloat from dead tuples.

-- 4. Claim query P95 latency
-- Instrument ClaimJob() in Go with time.Since(start) and log when > 50ms.
-- Or use pganalyze / pg_stat_statements:
SELECT mean_exec_time, max_exec_time, calls, left(query, 80)
FROM   pg_stat_statements
WHERE  query LIKE '%FOR UPDATE SKIP LOCKED%'
ORDER  BY mean_exec_time DESC;
```

### The upgrade path — step by step, in order

Each step is independent. Do not skip to step 4 without verifying steps 1–3 are
insufficient. Later steps cost more operationally and are irreversible without effort.

---

**Step 1 — pgBouncer in transaction mode**

Separates connection slots from goroutine count. Workers connect to pgBouncer, which
maintains a smaller pool of actual Postgres connections. A 16-goroutine worker pool can
share 8 Postgres connections efficiently because goroutines spend most of their time
executing handlers, not holding DB connections.

Cost: zero code change, one new process (pgBouncer container/sidecar), one config file.
Benefit: eliminates connection exhaustion, reduces per-connection overhead in Postgres,
improves throughput by 20–40% under connection pressure.

Configuration:
```ini
; pgbouncer.ini
[databases]
store = host=postgres port=5432 dbname=store

[pgbouncer]
pool_mode = transaction          ; essential — session mode defeats the purpose
max_client_conn = 200            ; total clients that can connect to pgBouncer
default_pool_size = 20           ; actual Postgres connections per database
```

---

**Step 2 — Dedicated pgxpool for the job queue**

`ManagerConfig.Pool` is already a separate field for this reason. Create a second
`pgxpool.Pool` in `server.New` and pass it to the Manager. The main application pool
and the job queue pool are now isolated — a job queue spike cannot starve HTTP handlers
of connections, and vice versa.

```go
// server.go
mainPool := pgxpool.New(ctx, cfg.DatabaseURL, &pgxpool.Config{MaxConns: 20})
jobPool  := pgxpool.New(ctx, cfg.DatabaseURL, &pgxpool.Config{
    MaxConns: int32(cfg.JobWorkers) + 2, // workers + ScheduleWatcher + StallDetector
})

mgr := jobqueue.NewManager(jobqueue.ManagerConfig{
    Pool:   jobPool,
    // ...
})
```

Cost: one extra pool config, a few lines in `server.New`. No schema change.
Benefit: complete isolation of job queue and API connection pressure.

---

**Step 3 — Postgres table partitioning by status**

Partition `jobs` by `status` so that the hot partitions (`pending`, `running`) contain
only the rows that workers need to scan, and the cold partition (`succeeded`) holds the
bulk of historical data without polluting the hot path.

```sql
-- Migration: convert jobs to a partitioned table
-- (Requires recreating the table — do this during a maintenance window)

CREATE TABLE jobs_partitioned (
    -- same columns as jobs
) PARTITION BY LIST (status);

CREATE TABLE jobs_pending   PARTITION OF jobs_partitioned FOR VALUES IN ('pending');
CREATE TABLE jobs_running   PARTITION OF jobs_partitioned FOR VALUES IN ('running');
CREATE TABLE jobs_succeeded PARTITION OF jobs_partitioned FOR VALUES IN ('succeeded');
CREATE TABLE jobs_failed    PARTITION OF jobs_partitioned FOR VALUES IN ('failed');
CREATE TABLE jobs_cancelled PARTITION OF jobs_partitioned FOR VALUES IN ('cancelled');
CREATE TABLE jobs_dead      PARTITION OF jobs_partitioned FOR VALUES IN ('dead');
```

The SKIP LOCKED claim query targets `WHERE status='pending'` — with partitioning,
Postgres executes this query exclusively against the `jobs_pending` partition, which
is tiny and hot. VACUUM only needs to process dead tuples in the partition they belong
to, not the entire table.

Cost: migration (table recreation, index recreation, rename). No application code change
— the `JobStore` SQL runs identically against a partitioned table.
Benefit: dramatically reduced VACUUM pressure, smaller hot-path index scans, better
cache hit rate for the `pending` partition.

---

**Step 4 — Swap JobStore for a Redis Streams implementation**

This is the inflection point. Steps 1–3 push the Postgres-backed design as far as it
can go. Step 4 removes Postgres from the hot path entirely.

Redis Streams with consumer groups provide:
- Native work-queue semantics (exactly-one delivery per consumer group)
- Throughput measured in hundreds of thousands of messages/second
- Built-in acknowledgement and pending entry list (equivalent to the `running` status)
- Stream trimming (equivalent to the daily purge)

**How the architecture changes:**

```
Before (v4):
  Submit → INSERT INTO jobs → PUBLISH jobqueue:notify
  Worker  → SUBSCRIBE → SKIP LOCKED claim → UPDATE status='running'

After (Step 4):
  Submit → XADD jobqueue:stream * id <uuid> kind <kind> payload <json>
  Worker  → XREADGROUP GROUP workers consumer-N BLOCK 5000 STREAMS jobqueue:stream >
          → receives entry → marks it as being processed (no SKIP LOCKED needed)
          → executes handler
          → on success: XACK + write completed row to Postgres asynchronously
          → on failure: XNACK or leave in pending entries for re-delivery
```

Postgres transitions from the hot queue to an **archive and audit store**:
- Completed, failed, and dead jobs are written to Postgres asynchronously by a
  dedicated archiver goroutine, not in the critical path of job execution.
- The REST API and WebSocket events continue to read from Postgres (which now
  reflects committed history, not live queue state).
- A new `GET /live` endpoint (or WebSocket update) reads live state from the
  Redis Stream directly.

**What does NOT change:**
- `Handler` interface — handlers are completely unaware of the storage backend.
- `Submitter` interface — `Submit()` is still the one method domain code calls.
- REST API endpoints — all 19 routes still exist, reading from Postgres history.
- WebSocket protocol — all event types unchanged.
- `internal/worker/` handlers — zero changes.

**What changes:**
- `store.go` — a new `redisJobStore` implementing `JobStore` using `go-redis` Streams.
- `dispatcher.go` — `tryClaimAndRun` reads from `XREADGROUP` instead of SKIP LOCKED.
- `manager.go` — wires `redisJobStore` instead of `pgJobStore` as the hot path.
- A new `archiver.go` — async goroutine that writes completed entries to Postgres.

The `JobStore` interface absorbs this entirely. Everything above the interface boundary
is untouched.

---

**Step 5 — NATS JetStream**

Same interface swap as Step 4. NATS JetStream adds:
- Multi-region fan-out (publish once, consume in any region)
- Built-in persistence with configurable retention policies
- Exactly-once delivery with deduplication by message ID
- Stream replay for debugging (re-process a range of historical messages)
- Subject-based routing (equivalent to queue_name routing, natively)

Operational cost: a new NATS cluster to run and monitor. Not justified unless you need
multi-region job processing or event sourcing semantics.

The upgrade path from Step 4 to Step 5 is another `JobStore` implementation swap —
`natsJobStore` replacing `redisJobStore`. Postgres archiving and the application layer
remain identical.

---

### Summary: when to take each step

| Symptom observed | Step to take |
|-----------------|-------------|
| Connection timeouts under load | Step 1 (pgBouncer) |
| Job queue slowing API response times | Step 2 (dedicated pool) |
| Autovacuum cannot keep up with dead tuples | Step 3 (partitioning) |
| `ClaimJob` P95 > 50ms sustained at peak | Step 3, then Step 4 |
| Jobs/sec ceiling reached at current hardware | Step 4 (Redis Streams) |
| Multi-region or exactly-once requirements | Step 5 (NATS JetStream) |

For a store backend that processes approval workflows and scheduled maintenance jobs,
Steps 1 and 2 will never be needed from a throughput perspective — they are only
relevant if connection count becomes an operational concern during horizontal scaling.
Steps 3, 4, and 5 are documented for completeness and for a future engineering team
that inherits this system at a different scale.

---

## Reading Order

| If you are... | Read... |
|--------------|---------|
| Building V1 | `0-design.md` only. Ceilings are far away. |
| Seeing claim P95 > 20ms in production | This document §3, Steps 1–2. |
| Seeing autovacuum lag in `pg_stat_user_tables` | This document §3, Step 3. |
| Planning a scale milestone | This document §1 and §3 in full. |
| Adding Grafana dashboards | This document §2 in full. |
| Evaluating NATS or Redis Streams | This document §3, Steps 4–5. |
