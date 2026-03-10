# Job Queue — Implementation Phases

**Companion to:** `0-design.md` (v5)
**Status:** Ready to implement
**Environment note:** Dev — apply migrations directly, no zero-downtime constraints.

Each phase has a clear entry state, a clear exit gate, and touches only what it needs.
Nothing in a later phase is needed to complete an earlier one. Complete each gate before
starting the next phase.

---

## Phase Map

```
Phase 1 ──────────────────────────────── DB Foundation
Phase 2 ──────────────────────────────── Redis PubSub
Phase 3 ── (needs 1 + 2) ──────────────── jobqueue core types + store + metrics
Phase 4 ── (needs 3) ──────────────────── Dispatcher + Scheduler + StallDetector
Phase 5 ── (needs 4) ──────────────────── Admin API + WebSocket
Phase 6 ── (needs 3) ──────────────────── Worker handlers  [parallel with 4+5]
Phase 7 ── (needs 4 + 5 + 6) ─────────── Wire into server
```

Phases 1 and 2 are fully independent — start either or both simultaneously.
Phase 6 only needs Phase 3 — it can run in parallel with Phases 4 and 5.

---

## Phase 1 — DB Foundation

**What exists:** `sql/schema/005_requests.sql` is the last migration.
**Goal:** 4 new tables created, old tables altered/dropped, server still boots.

### Files to create

```
sql/schema/006_jobqueue.sql
```

Full content is in `0-design.md §4`. Copy it verbatim.

### Apply

```bash
goose -dir sql/schema postgres "$DATABASE_URL" up
```

### Gate — all of these must be true before Phase 3 starts

```sql
-- All 4 tables exist
SELECT table_name FROM information_schema.tables
WHERE table_name IN ('jobs','job_schedules','workers','job_paused_kinds');
-- → 4 rows

-- request_executions is gone
SELECT to_regclass('public.request_executions');
-- → null

-- request_notifications lost its delivery columns
SELECT column_name FROM information_schema.columns
WHERE table_name = 'request_notifications'
  AND column_name IN ('delivery_attempts','last_attempt_at','delivery_error');
-- → 0 rows

-- Indexes exist
SELECT indexname FROM pg_indexes WHERE tablename = 'jobs';
-- → idx_jobs_claimable, idx_jobs_run_after, idx_jobs_kind_status, etc.
```

---

## Phase 2 — Redis PubSub

**What exists:** `RedisStore` in `internal/platform/kvstore/redis.go` — no Publish/Subscribe yet.
**Goal:** `RedisStore` satisfies the `PubSub` interface. Contract tests green.

### Files to create / modify

```
internal/platform/kvstore/redis.go          MODIFY — add Publish + Subscribe methods
internal/platform/kvstore/pubsub_test.go    CREATE — RunPubSubContractTests + TestRedisPubSub
```

### Publish + Subscribe to add to `redis.go`

```go
// Publish sends a message to channel. Returns nil if Redis is down —
// jobqueue treats publish as best-effort; the 10s poll is the fallback.
func (s *RedisStore) Publish(ctx context.Context, channel, message string) error {
    if err := s.client.Publish(ctx, channel, message).Err(); err != nil {
        return fmt.Errorf("kvstore.Publish: %w", err)
    }
    return nil
}

// Subscribe returns a channel that receives messages published to channel.
// The channel is closed when ctx is cancelled.
// Each call creates a new go-redis PubSub subscription — callers should
// hold a single subscription for the lifetime of the component.
func (s *RedisStore) Subscribe(ctx context.Context, channel string) (<-chan string, error) {
    sub := s.client.Subscribe(ctx, channel)
    // Verify the subscription is live before returning.
    if _, err := sub.Receive(ctx); err != nil {
        _ = sub.Close()
        return nil, fmt.Errorf("kvstore.Subscribe: initial receive: %w", err)
    }
    ch := make(chan string, 16)
    go func() {
        defer close(ch)
        defer sub.Close()
        for {
            msg, err := sub.ReceiveMessage(ctx)
            if err != nil {
                return // ctx cancelled or Redis down — dispatcher falls back to ticker
            }
            select {
            case ch <- msg.Payload:
            case <-ctx.Done():
                return
            }
        }
    }()
    return ch, nil
}

// compile-time check (add alongside existing checks at bottom of redis.go)
// var _ jobqueue.PubSub = (*RedisStore)(nil)  ← add after Phase 3 creates the interface
```

### Contract test cases

| # | Case | Layer |
|---|------|-------|
| T-C1 | Publish → Subscribe receives message within 100ms | I+R |
| T-C2 | Multiple subscribers all receive the same publish (fan-out) | I+R |
| T-C3 | Publish returns error when broker is unreachable | I+R |
| T-C4 | Subscribe channel is closed when ctx is cancelled | I+R |
| T-C5 | Publish succeeds after broker recovers from downtime | I+R |

### Gate

```bash
go test ./internal/platform/kvstore/... -run TestRedisPubSub -v
# All 5 contract cases pass
```

---

## Phase 3 — jobqueue Core Types, Store, Metrics

**Needs:** Phase 1 (DB tables exist), Phase 2 (Publish/Subscribe methods exist).
**Goal:** The `jobqueue` package compiles, `pgJobStore` works against the real DB,
contract tests are written and green. No Dispatcher yet — just the data layer.

### Files to create

```
internal/platform/jobqueue/job.go                  — all interfaces + types
internal/platform/jobqueue/store.go                — JobStore interface + pgJobStore
internal/platform/jobqueue/metrics.go              — QueryMetricsRecorder + NoopMetricsRecorder
internal/platform/jobqueue/store_contract_test.go  — RunJobStoreContractTests
internal/platform/jobqueue/pg_store_test.go        — TestPgJobStore
internal/platform/jobqueue/metrics_contract_test.go
```

### `job.go` — what goes here

- `Kind`, `Status` types and all status constants
- `Job` struct (full field set from `0-design.md §6`)
- `Handler` interface + `HandlerFunc`
- `Submitter` interface (`Submit` only)
- `SubmitRequest`, `WorkerInfo`, `Schedule`, `ScheduleInput`, `PausedKind`, `JobFilter` structs
- `PubSub` interface (`Publish` + `Subscribe`)
- `MetricsRecorder` interface (all hooks + `MetricsHandler`)

After this file exists, add the compile-time check back in `redis.go`:
```go
var _ jobqueue.PubSub = (*RedisStore)(nil)
```

### `store.go` — what goes here

- `JobStore` interface (full method set from `0-design.md §6`)
- `pgJobStore` struct implementing every method
- `NewPgJobStore(pool *pgxpool.Pool) JobStore`
- The aging claim query verbatim from `0-design.md §1`
- `GetStats()` method (needed by QueryMetricsRecorder for `/metrics`)

### `metrics.go` — what goes here

- `QueryMetricsRecorder` — all hooks are no-ops; `MetricsHandler` runs `GetStats()` and
  writes Prometheus text
- `NoopMetricsRecorder` — all hooks no-ops; `MetricsHandler` returns 404
- `NewQueryMetricsRecorder(store JobStore) *QueryMetricsRecorder`

Full implementations are in `0-design.md §6 metrics.go`.

### Contract test structure

```go
// store_contract_test.go
func RunJobStoreContractTests(t *testing.T, newStore func(t *testing.T) JobStore) {
    // T-26 through T-32
}

// pg_store_test.go
func TestPgJobStore(t *testing.T) {
    RunJobStoreContractTests(t, func(t *testing.T) JobStore {
        return newTestPgJobStore(t) // connects to test DB from env
    })
}

// metrics_contract_test.go
func RunMetricsRecorderContractTests(t *testing.T, rec MetricsRecorder) {
    // all hooks callable without panic
    // MetricsHandler returns non-nil
    // MetricsHandler responds without 500
}

func TestQueryMetricsRecorder(t *testing.T) {
    RunMetricsRecorderContractTests(t, NewQueryMetricsRecorder(newTestStore(t)))
}
func TestNoopMetricsRecorder(t *testing.T) {
    RunMetricsRecorderContractTests(t, NoopMetricsRecorder{})
}
```

### Gate

```bash
go build ./internal/platform/jobqueue/...
go build ./internal/platform/kvstore/...   # PubSub compile check now resolves

go test ./internal/platform/jobqueue/... \
    -run "TestPgJobStore|TestQueryMetricsRecorder|TestNoopMetricsRecorder" -v
# T-26 through T-32 pass
# Metrics contract cases pass
```

---

## Phase 4 — Dispatcher, ScheduleWatcher, StallDetector, Manager

**Needs:** Phase 3.
**Goal:** Jobs can be submitted, claimed, executed, retried, and dead-lettered.
Manager lifecycle (Start/Shutdown) works. `AdminRouter()` stubbed — no routes yet.

### Files to create

```
internal/platform/jobqueue/dispatcher.go   — N worker goroutines, Redis wake + 10s ticker
internal/platform/jobqueue/scheduler.go    — ScheduleWatcher, 10s poll, cron + interval
internal/platform/jobqueue/deadletter.go   — dead-letter helpers (thin wrappers over store)
internal/platform/jobqueue/manager.go      — Manager + NewManager + Start/Shutdown/Submit/EnsureSchedule
                                             AdminRouter() stubbed as chi.NewRouter() for now
```

### Key implementation notes

**`dispatcher.go`:**
- Each worker goroutine runs `workerLoop` — select on `d.notify` and `ticker.C`
- `tryClaimAndRun`: `store.ClaimJob` → run handler with `timeout_seconds` deadline
  → `store.CompleteJob` or `store.FailJob` → call metrics hooks
- Unknown kind → `store.DeadLetterJob` + log (D-13: no panic)
- Handler panic → recover, treat as error, counts against MaxAttempts
- Call `metrics.OnJobClaimed`, `metrics.OnJobSucceeded`, `metrics.OnJobFailed`,
  `metrics.OnJobDead` at the right moments

**`scheduler.go`:**
- Single goroutine, `time.NewTicker(cfg.ScheduleCheck)` (default 10s)
- On tick: `store.ListDueSchedules` → insert job → publish notify → update next_run_at
- PUBLISH failure is warn-only — job row already in Postgres
- Use `robfig/cron` parser for cron expressions

**`manager.go`:**
- `Register` panics if called after `Start` or duplicate kind (D-12)
- `NewManager` defaults `cfg.Metrics` to `NewQueryMetricsRecorder(store)` when nil
- `Shutdown`: stop ScheduleWatcher → drain Dispatcher workers → `store.MarkWorkerOffline`

### Tests to pass in this phase

- Dispatcher / Worker loop: T-01 through T-16
- Priority aging: T-17 through T-19
- ScheduleWatcher: T-20 through T-25

### Gate

```bash
go test ./internal/platform/jobqueue/... \
    -run "TestDispatcher|TestScheduleWatcher|TestPriority" -v
# T-01 through T-25 pass
```

Manual smoke:
```go
mgr := jobqueue.NewManager(jobqueue.ManagerConfig{
    Store: store, Pool: pool, PubSub: redisStore, Workers: 2,
})
mgr.Register("smoke", jobqueue.HandlerFunc(func(ctx context.Context, job jobqueue.Job) error {
    slog.Info("job ran", "id", job.ID)
    return nil
}))
mgr.Start(ctx)
mgr.Submit(ctx, jobqueue.SubmitRequest{Kind: "smoke"})
// → job row in DB transitions pending → running → succeeded within ~1s
```

---

## Phase 5 — Admin API + WebSocket

**Needs:** Phase 4.
**Goal:** All 20 REST endpoints work. WebSocket stream works. `/metrics` serves
Prometheus text. `AdminRouter()` fully implemented.

### Files to create / modify

```
internal/platform/jobqueue/api.go      CREATE — 20 REST endpoints
internal/platform/jobqueue/ws.go       CREATE — WSHub + writePump + readPump
internal/platform/jobqueue/manager.go  MODIFY — replace stubbed AdminRouter() with real impl
```

### Notes

- All routes require `job_queue:read` or `job_queue:manage` — use the same permission
  middleware pattern already in the project
- `GET /metrics` is just:
  ```go
  r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
      m.metrics.MetricsHandler().ServeHTTP(w, r)
  })
  ```
  No metrics logic in `api.go` itself
- `GET /stats` calls `store.GetStats()` — already added in Phase 3
- WSHub: per-client buffered channel (`clientSendBuf = 64`), hub goroutine never blocks
  on network I/O, `writePump` goroutine per client

### Tests to pass in this phase

- WSHub: T-33 through T-35
- Admin API: T-36 through T-40
- Metrics integration: T-47 through T-49

### Gate

```bash
go test ./internal/platform/jobqueue/... \
    -run "TestWSHub|TestAdminAPI|TestMetrics" -v
```

Manual smoke:
```bash
curl http://localhost:8080/admin/jobqueue/stats
# → JSON with pending/running/dead counts

curl http://localhost:8080/admin/jobqueue/metrics
# → Prometheus text: jobqueue_jobs_total{...} N

wscat -c ws://localhost:8080/admin/jobqueue/ws
# → stats.tick event every 5s
```

---

## Phase 6 — Worker Handlers

**Needs:** Phase 3 only. Run in parallel with Phases 4 and 5.
**Goal:** Three new handlers exist and are tested. `purge.go` adapted to the new signature.

### Files to create / modify

```
internal/worker/kinds.go              MODIFY — add KindExecuteRequest, KindSendNotification, KindPurgeCompleted
internal/worker/purge.go              MODIFY — add PurgeHandler implementing jobqueue.Handler
internal/worker/execute_request.go    CREATE — replaces request_executions inline execution
internal/worker/send_notification.go  CREATE — replaces notification delivery retry
internal/worker/purge_completed.go    CREATE — deletes succeeded/cancelled beyond RetentionDays
```

### `purge.go` strategy

The existing `PurgeWorker` and its `Start()` goroutine still compile unchanged — don't
touch them yet. Add a new `PurgeHandler` alongside it that wraps the same logic:

```go
type PurgeHandler struct{ pool *pgxpool.Pool }

func NewPurgeHandler(pool *pgxpool.Pool) *PurgeHandler
func (h *PurgeHandler) Handle(ctx context.Context, job jobqueue.Job) error
    // same logic as PurgeWorker.runOnce — reuse or call it directly
```

The `PurgeWorker.Start()` goroutine gets removed in Phase 7 when the schedule takes over.
Keeping it here means this phase compiles and tests without touching server wiring.

### Tests to pass in this phase

T-41 through T-46

### Gate

```bash
go test ./internal/worker/... -v
# All pass including T-41 through T-46
```

---

## Phase 7 — Wire into Server

**Needs:** Phases 4, 5, and 6 all green.
**Goal:** Server starts with job queue running. `PurgeWorker` goroutine removed.
All existing tests still pass. Full end-to-end smoke test passes.

### Files to modify

```
internal/config/config.go    — add JobWorkers int, JobRetentionDays int
internal/app/deps.go         — add Jobs jobqueue.Submitter, JobMgr *jobqueue.Manager
internal/server/server.go    — wire Manager, seed schedules, mount /admin/jobqueue,
                               remove PurgeWorker goroutine, update cleanup order
```

### `server.go` wiring (from `0-design.md §7`)

```go
// After pool and kvStore are ready:
jobStore := jobqueue.NewPgJobStore(pool)

mgr := jobqueue.NewManager(jobqueue.ManagerConfig{
    Store:             jobStore,
    Pool:              pool,
    PubSub:            redisStore, // already wired — *kvstore.RedisStore
    // Metrics: nil → NewManager defaults to QueryMetricsRecorder(jobStore)
    Workers:           cfg.JobWorkers,
    DefaultTimeout:    10 * time.Minute,
    DefaultAttempts:   5,
    RetentionDays:     cfg.JobRetentionDays,
    HeartbeatEvery:    15 * time.Second,
    StallCheck:        30 * time.Second,
    ScheduleCheck:     10 * time.Second,
    RedisFallbackPoll: 10 * time.Second,
    AgingRateSeconds:  60,
    AgingCap:          50,
})

mgr.Register(worker.KindPurgeAccounts,    worker.NewPurgeHandler(pool))
mgr.Register(worker.KindExecuteRequest,   worker.NewExecuteRequestHandler(pool))
mgr.Register(worker.KindSendNotification, worker.NewSendNotificationHandler(pool, m))
mgr.Register(worker.KindPurgeCompleted,   worker.NewPurgeCompletedHandler(pool))

mgr.EnsureSchedule(ctx, jobqueue.ScheduleInput{
    Name: "purge_accounts_hourly", Kind: worker.KindPurgeAccounts,
    IntervalSeconds: 3600, SkipIfRunning: true,
})
mgr.EnsureSchedule(ctx, jobqueue.ScheduleInput{
    Name: "purge_completed_jobs_daily", Kind: worker.KindPurgeCompleted,
    IntervalSeconds: 86400, SkipIfRunning: true,
})

mgr.Start(ctx)
deps.Jobs   = mgr
deps.JobMgr = mgr

r.Mount("/admin/jobqueue", mgr.AdminRouter())
```

### Cleanup order (replaces existing cleanup in server.go)

```go
cleanup := func() {
    mgr.Shutdown()  // 1. drain workers, mark offline in DB
    q.Shutdown()    // 2. drain mail queue (may use DB — must come before pool.Close)
    pool.Close()    // 3. safe only after all consumers stopped
}
```

### Remove PurgeWorker

Delete any `go worker.NewPurgeWorker(...).Start(ctx)` call from `server.go`.
The `purge_accounts_hourly` schedule now owns this responsibility.

### Gate

```bash
# Server boots
go run ./cmd/... &
curl http://localhost:8080/health   # → 200

# Job queue is live
curl http://localhost:8080/admin/jobqueue/workers
# → [{ "status": "idle", "concurrency": N }]

curl http://localhost:8080/admin/jobqueue/schedules
# → purge_accounts_hourly and purge_completed_jobs_daily both present

curl http://localhost:8080/admin/jobqueue/metrics
# → Prometheus text with jobqueue_jobs_total etc.

# Full test suite still green
go test ./... -v
```

---

## Summary Table

| Phase | Files | Needs | Can run in parallel with | Gate |
|-------|-------|-------|--------------------------|------|
| 1 | `006_jobqueue.sql` | nothing | Phase 2 | 4 tables in DB, old table gone |
| 2 | `redis.go` + `pubsub_test.go` | nothing | Phase 1 | `TestRedisPubSub` green |
| 3 | `job.go`, `store.go`, `metrics.go`, contract tests | 1 + 2 | — | contract tests green, package builds |
| 4 | `dispatcher.go`, `scheduler.go`, `deadletter.go`, `manager.go` | 3 | Phase 6 | T-01–T-25 green |
| 5 | `api.go`, `ws.go`, `manager.go` update | 4 | Phase 6 | T-33–T-40, T-47–T-49 green |
| 6 | `kinds.go`, `purge.go`, 3 new handlers | 3 only | Phases 4 + 5 | T-41–T-46 green |
| 7 | `config.go`, `deps.go`, `server.go` | 4 + 5 + 6 | — | server boots, `go test ./...` green |
