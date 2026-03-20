# Ceilings Feature — Known Trade-offs & When to Act

> **What this file is:** A plain-language description of the three documented
> ceilings of the Postgres-backed, Redis-signaled design. Each ceiling explains
> what the trade-off is, why it is acceptable now, and the leading indicators that
> tell you when to act. Read this before the technical deep-dive.
>
> **Companion:** `ceilings-technical.md` — machine-level mechanics, diagnostic
> SQL, concrete numbers, step-by-step upgrade paths.
> **Source of truth:** `../0-design.md §11` and `../1-ceilings.md`.

---

## These are not bugs

Each ceiling is a documented, deliberate trade-off of the chosen architecture — not
an oversight or a design mistake. They are recorded here so a future engineer who
encounters them does not mistake them for defects, and so the upgrade path is
pre-planned before it is needed.

The key architectural guarantee: because `JobStore` and `PubSub` are interfaces
defined from day one, every upgrade path below is a refactor (swap one
implementation) — not a rewrite. The Dispatcher logic, ScheduleWatcher, admin API,
WebSocket protocol, and all domain handlers are permanently insulated from the
storage and signaling backend choices.

---

## Ceiling 1 — N-1 Wasted Claim Attempts

**The trade-off:** Redis pub/sub is fan-out — every subscriber receives every
message. When one job is submitted, all N worker goroutines across all instances
wake and race to claim it. Only one wins. The other N-1 run a `SKIP LOCKED` query
that returns nothing and go back to sleep. These are wasted database round-trips.

**Why it is acceptable now:** At store-backend scale (tens to hundreds of jobs per
minute, 1–2 instances), these wasted queries are sub-millisecond indexed reads on a
small partial-indexed set. They are invisible in any monitoring system.

**When to act:** When sustained throughput exceeds ~500 jobs/second with 10+
worker instances, or when `pg_stat_activity` shows consistent lock waits on `jobs`
rows at claim time.

**The upgrade:** Swap the `PubSub` implementation to use Redis Streams with consumer
groups, which deliver each message to exactly one subscriber (work-queue semantics
instead of fan-out). Because `PubSub` is an interface in `job.go`, the Dispatcher,
ScheduleWatcher, and everything above are untouched.

---

## Ceiling 2 — Metrics Endpoint

**The status:** `GET /admin/jobqueue/metrics` ships in V1 via `QueryMetricsRecorder`.
This is not a missing ceiling — it was promoted from the V2 backlog in the v5
design revision.

**The trade-off in V1:** The endpoint runs one SQL query per Prometheus scrape
(every 15–30 seconds) rather than maintaining in-process counters. This is one
fast query per scrape interval — not a concern at any scale.

**The upgrade (V2):** When in-process Prometheus counters are preferred, swap
`ManagerConfig.Metrics` to `NewPrometheusMetricsRecorder(prometheus.DefaultRegisterer)`.
The event hooks (currently no-ops in `QueryMetricsRecorder`) become counter/histogram
increments. `MetricsHandler()` returns `promhttp.Handler()` instead of running SQL.
One line change in `server.New`. Nothing else changes.

---

## Ceiling 3 — Postgres as the Write Bottleneck

**The trade-off:** Every job requires three writes to Postgres (INSERT on submit,
UPDATE on claim, UPDATE on completion). At sustained high throughput, write
contention on the `jobs` table becomes the bottleneck.

**Why it is acceptable now:** A store backend processing approval workflows, hourly
maintenance schedules, and notification delivery handles 10–100 jobs per minute at
peak — 100× below the practical ceiling of 500–1,000 jobs/second on a well-tuned
Postgres instance.

**When to act:** Each step below corresponds to a specific leading indicator. Do
not skip steps — each is significantly cheaper than the next.

| Leading indicator | Step to take |
|-------------------|-------------|
| Connection timeouts under load | Step 1: pgBouncer in transaction mode |
| Job queue load slowing API responses | Step 2: dedicated pgxpool for job queue |
| Autovacuum cannot keep up (n_dead_tup growing) | Step 3: table partitioning by status |
| ClaimJob P95 > 50ms sustained at peak | Steps 3 then 4 |
| Jobs/sec ceiling reached at current hardware | Step 4: Redis Streams JobStore |
| Multi-region or exactly-once requirements | Step 5: NATS JetStream |

**The upgrade path is a series of interface swaps:**
- Steps 1–2: configuration/infrastructure changes, zero code.
- Step 3: SQL migration only, no Go code change.
- Steps 4–5: swap `JobStore` implementation. Pass `RunJobStoreContractTests` before
  merging — if it passes, the swap cannot break the application layer.

---

## Upgrade compatibility guarantee

| Ceiling | What changes | Gate before merging |
|---------|-------------|---------------------|
| C1 — N-1 waste | `PubSub` implementation | `RunPubSubContractTests` with new impl |
| C2 — Metrics | `MetricsRecorder` implementation | `RunMetricsRecorderContractTests` with new impl |
| C3 Step 3 — partitioning | SQL migration only | Migration dry-run + full integration suite |
| C3 Step 4 — Redis Streams | `JobStore` implementation | `RunJobStoreContractTests` with new impl |
| C3 Step 5 — NATS | `JobStore` implementation | `RunJobStoreContractTests` with new impl |
