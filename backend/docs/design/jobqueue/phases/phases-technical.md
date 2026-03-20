# Phases Technical — File Lists, Gates, Smoke Tests

> **What this file is:** Per-phase file lists (create / modify), gate commands,
> SQL verification queries, and manual smoke test instructions.
>
> **Read first:** `phases-feature.md` — phase descriptions, parallelization rules,
> done criteria.
> **Source of truth:** `../2-implementation-phases.md`.

---

## Phase 1 — DB Foundation

### Files

```
sql/schema/007_jobqueue.sql        CREATE — tables, enums, indexes, triggers
sql/schema/008_jobqueue_functions.sql  CREATE — terminal-state protection trigger
```

### Apply

```bash
goose -dir sql/schema postgres "$DATABASE_URL" up
```

### Gate queries

```sql
-- All 4 tables exist (expect 4 rows)
SELECT table_name FROM information_schema.tables
WHERE  table_name IN ('jobs','job_schedules','workers','job_paused_kinds');

-- request_executions is gone (expect null)
SELECT to_regclass('public.request_executions');

-- request_notifications lost delivery columns (expect 0 rows)
SELECT column_name FROM information_schema.columns
WHERE  table_name  = 'request_notifications'
  AND  column_name IN ('delivery_attempts','last_attempt_at','delivery_error');

-- Enums exist
SELECT typname FROM pg_type
WHERE  typname IN ('job_status_enum','worker_status_enum');

-- Indexes exist on jobs
SELECT indexname FROM pg_indexes WHERE tablename = 'jobs';

-- Terminal-state trigger exists
SELECT tgname FROM pg_trigger WHERE tgname = 'trg_jobs_prevent_terminal_change';
```

---

## Phase 2 — Redis PubSub

### Files

```
internal/platform/kvstore/redis.go          MODIFY — add Publish + Subscribe methods
internal/platform/kvstore/pubsub_test.go    CREATE — RunPubSubContractTests + TestRedisPubSub
```

### Publish + Subscribe

```go
func (s *RedisStore) Publish(ctx context.Context, channel, message string) error {
    if err := s.client.Publish(ctx, channel, message).Err(); err != nil {
        return fmt.Errorf("kvstore.Publish: %w", err)
    }
    return nil
}

func (s *RedisStore) Subscribe(ctx context.Context, channel string) (<-chan string, error) {
    sub := s.client.Subscribe(ctx, channel)
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
            if err != nil { return }
            select {
            case ch <- msg.Payload:
            case <-ctx.Done(): return
            }
        }
    }()
    return ch, nil
}
```

### Gate

```bash
go test ./internal/platform/kvstore/... -run TestRedisPubSub -v
# All 5 contract cases (T-C1 through T-C5) pass
```

---

## Phase 3 — Core Types, Store, Metrics

### Files

```
internal/platform/jobqueue/job.go                  CREATE
internal/platform/jobqueue/store.go                CREATE
internal/platform/jobqueue/metrics.go              CREATE
internal/platform/jobqueue/store_contract_test.go  CREATE
internal/platform/jobqueue/pg_store_test.go        CREATE
internal/platform/jobqueue/metrics_contract_test.go CREATE
internal/platform/kvstore/redis.go                 MODIFY — add compile-time PubSub check
```

### job.go must define

- `Kind`, `Status` types and all status constants
- `Job`, `WorkerInfo`, `Schedule`, `ScheduleInput`, `PausedKind`, `JobFilter`,
  `SubmitRequest`, `QueueStats` structs
- `Handler` interface + `HandlerFunc`
- `Submitter` interface
- `PubSub` interface
- `MetricsRecorder` interface
- `PermanentError` sentinel type

After `job.go` is written, add to `redis.go`:
```go
var _ jobqueue.PubSub = (*RedisStore)(nil)
```

### Gate

```bash
go build ./internal/platform/jobqueue/...
go build ./internal/platform/kvstore/...   # compile-time PubSub check resolves

go test ./internal/platform/jobqueue/... \
    -run "TestPgJobStore|TestQueryMetricsRecorder|TestNoopMetricsRecorder" -v
# T-26 through T-32, T-47 through T-53 pass
```

---

## Phase 4 — Dispatcher, Scheduler, StallDetector, Manager

### Files

```
internal/platform/jobqueue/dispatcher.go   CREATE
internal/platform/jobqueue/scheduler.go    CREATE
internal/platform/jobqueue/stall.go        CREATE
internal/platform/jobqueue/deadletter.go   CREATE
internal/platform/jobqueue/manager.go      CREATE — AdminRouter() stubbed as chi.NewRouter()
```

### Smoke test (manual)

```go
mgr := jobqueue.NewManager(jobqueue.ManagerConfig{
    Store: store, Pool: pool, PubSub: redisStore, Workers: 2,
})
mgr.Register("smoke", jobqueue.HandlerFunc(func(ctx context.Context, job jobqueue.Job) error {
    slog.Info("job ran", "id", job.ID)
    return nil
}))
mgr.Start(ctx)
job, _ := mgr.Submit(ctx, jobqueue.SubmitRequest{Kind: "smoke"})
// → jobs row transitions pending → running → succeeded within ~1s
// → workers row has heartbeat_at updated
```

### Gate

```bash
go test ./internal/platform/jobqueue/... \
    -run "TestDispatcher|TestScheduleWatcher|TestStall|TestPriority" -v
# T-01 through T-25, T-SL-1 through T-SL-5 pass
```

---

## Phase 5 — Admin API + WebSocket

### Files

```
internal/platform/jobqueue/api.go      CREATE — 20 REST endpoints
internal/platform/jobqueue/ws.go       CREATE — WSHub, writePump, readPump
internal/platform/jobqueue/manager.go  MODIFY — replace stubbed AdminRouter()
```

### Smoke test (manual)

```bash
curl http://localhost:8080/admin/jobqueue/stats
# → {"pending":0,"running":0,...}

curl -H "Accept: text/plain" http://localhost:8080/admin/jobqueue/metrics
# → jobqueue_jobs_total{...} 0

wscat -c ws://localhost:8080/admin/jobqueue/ws
# → {"event":"stats.tick","data":{"pending":0,"running":0,"dead":0,"tps":0}}
```

### Gate

```bash
go test ./internal/platform/jobqueue/... \
    -run "TestWSHub|TestAdminAPI|TestMetrics" -v
# T-33 through T-40, T-47 through T-49, T-API-1 through T-API-4 pass
```

---

## Phase 6 — Worker Handlers

### Files

```
internal/worker/kinds.go                         MODIFY — add new Kind constants
internal/worker/purge.go                         MODIFY — add PurgeHandler alongside PurgeWorker
internal/worker/execute_request.go               CREATE
internal/worker/send_notification.go             CREATE
internal/worker/purge_completed.go               CREATE
internal/worker/purge_expired_permissions.go     CREATE
```

### Gate

```bash
go test ./internal/worker/... -v
# T-41 through T-46, T-WK-1 through T-WK-4 pass
```

---

## Phase 7 — Wire into Server

### Files

```
internal/config/config.go    MODIFY — add JobWorkers int, JobRetentionDays int
internal/app/deps.go         MODIFY — add Jobs jobqueue.Submitter, JobMgr *jobqueue.Manager
internal/server/server.go    MODIFY — wire Manager, seed schedules, mount /admin/jobqueue,
                                       remove PurgeWorker goroutine, update cleanup order
```

### Changes in server.go

1. Add the full job queue wiring block (see `manager/manager-technical.md §3`).
2. Remove `go worker.NewPurgeWorker(...).Start(ctx)`.
3. Update cleanup order: `mgr.Shutdown()` → `q.Shutdown()` → `pool.Close()`.

### Gate

```bash
# Server boots cleanly
go run ./cmd/... &
curl http://localhost:8080/health    # → 200

# Job queue is live
curl http://localhost:8080/admin/jobqueue/workers
# → [{"status":"idle","concurrency":N,"host":"..."}]

curl http://localhost:8080/admin/jobqueue/schedules
# → [purge_accounts_hourly, purge_completed_jobs_daily, purge_expired_permissions_5min]

curl http://localhost:8080/admin/jobqueue/metrics
# → Prometheus text with jobqueue_jobs_total etc.

# Full suite green
go test ./... -count=1
```

---

## Summary table

| Phase | Files | Needs | Parallel with | Gate |
|-------|-------|-------|---------------|------|
| 1 | 007, 008 SQL | nothing | Phase 2 | 4 tables in DB, old table gone |
| 2 | redis.go, pubsub_test.go | nothing | Phase 1 | `TestRedisPubSub` green |
| 3 | job.go, store.go, metrics.go, tests | 1 + 2 | — | contract tests green, package builds |
| 4 | dispatcher, scheduler, stall, deadletter, manager | 3 | Phase 6 | T-01–T-25 green |
| 5 | api.go, ws.go, manager update | 4 | Phase 6 | T-33–T-40, T-47–T-49 green |
| 6 | kinds, purge, 4 new handlers | 3 only | Phases 4 + 5 | T-41–T-46, T-WK-1–T-WK-4 green |
| 7 | config, deps, server | 4 + 5 + 6 | — | server boots, `go test ./...` green |
