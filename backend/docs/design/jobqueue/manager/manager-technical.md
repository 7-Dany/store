# Manager Technical — ManagerConfig, Wiring, Cleanup Order

> **What this file is:** `ManagerConfig` fields and defaults, `server.New` wiring
> code, cleanup order, compile-time interface checks, and test inventory.
>
> **Read first:** `manager-feature.md` — lifecycle rules, registration rules,
> Submitter interface, startup and shutdown sequence.

---

## Table of Contents

1. [ManagerConfig](#1--managerconfig)
2. [Manager struct and constructor](#2--manager-struct-and-constructor)
3. [server.New wiring](#3--servernew-wiring)
4. [Cleanup order](#4--cleanup-order)
5. [Compile-time interface checks](#5--compile-time-interface-checks)
6. [Test inventory](#6--test-inventory)

---

## §1 — ManagerConfig

```go
// internal/platform/jobqueue/manager.go

type ManagerConfig struct {
    Store   JobStore       // required
    Pool    *pgxpool.Pool  // required — used for GetStats and direct queries
    PubSub  PubSub         // required — *kvstore.RedisStore satisfies this

    // Metrics is optional. Nil → NewManager defaults to QueryMetricsRecorder(Store).
    // Swap to NewPrometheusMetricsRecorder(...) in V2 — one line change.
    Metrics MetricsRecorder

    Workers         int           // number of worker goroutines per instance (default 4)
    Queues          []string      // queue names this instance polls (default ["default"])
    DefaultTimeout  time.Duration // per-job timeout if not specified (default 10m)
    DefaultAttempts int           // max attempts if not specified (default 5)
    RetentionDays   int           // purge_completed_jobs threshold (default 30)

    HeartbeatEvery    time.Duration // worker heartbeat interval (default 15s)
    StallCheck        time.Duration // StallDetector tick interval (default 30s)
    ScheduleCheck     time.Duration // ScheduleWatcher poll interval (default 10s)
    NotifyChannel     string        // Redis pub/sub channel name (default "jobqueue:notify")
    RedisFallbackPoll time.Duration // Postgres poll when Redis is silent (default 10s)

    AgingRateSeconds int // seconds per +1 effective_priority point (default 60)
    AgingCap         int // max points a job gains from aging (default 50)
}
```

---

## §2 — Manager struct and constructor

```go
type Manager struct {
    dispatcher *Dispatcher
    watcher    *ScheduleWatcher
    stall      *StallDetector
    hub        *WSHub
    store      JobStore
    pubsub     PubSub
    metrics    MetricsRecorder
    handlers   map[Kind]Handler
    cfg        ManagerConfig
    started    bool
    mu         sync.Mutex // guards started + handlers
    cancel     context.CancelFunc
    wg         sync.WaitGroup
}

func NewManager(cfg ManagerConfig) *Manager {
    if cfg.Metrics == nil {
        cfg.Metrics = NewQueryMetricsRecorder(cfg.Store)
    }
    // apply all other defaults
    if cfg.Workers == 0          { cfg.Workers = 4 }
    if len(cfg.Queues) == 0      { cfg.Queues = []string{"default"} }
    if cfg.DefaultTimeout == 0   { cfg.DefaultTimeout = 10 * time.Minute }
    if cfg.DefaultAttempts == 0  { cfg.DefaultAttempts = 5 }
    if cfg.RetentionDays == 0    { cfg.RetentionDays = 30 }
    if cfg.HeartbeatEvery == 0   { cfg.HeartbeatEvery = 15 * time.Second }
    if cfg.StallCheck == 0       { cfg.StallCheck = 30 * time.Second }
    if cfg.ScheduleCheck == 0    { cfg.ScheduleCheck = 10 * time.Second }
    if cfg.NotifyChannel == ""   { cfg.NotifyChannel = "jobqueue:notify" }
    if cfg.RedisFallbackPoll == 0 { cfg.RedisFallbackPoll = 10 * time.Second }
    if cfg.AgingRateSeconds == 0 { cfg.AgingRateSeconds = 60 }
    if cfg.AgingCap == 0         { cfg.AgingCap = 50 }

    return &Manager{
        cfg:      cfg,
        store:    cfg.Store,
        pubsub:   cfg.PubSub,
        metrics:  cfg.Metrics,
        handlers: make(map[Kind]Handler),
        hub:      NewWSHub(),
    }
}

// Register panics if called after Start or if kind is already registered.
func (m *Manager) Register(k Kind, h Handler) {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.started {
        panic(fmt.Sprintf("jobqueue: Register called after Start for kind %q", k))
    }
    if _, exists := m.handlers[k]; exists {
        panic(fmt.Sprintf("jobqueue: duplicate handler registration for kind %q", k))
    }
    m.handlers[k] = h
}

func (m *Manager) Start(ctx context.Context) { ... }
func (m *Manager) Shutdown()                  { ... }
func (m *Manager) Submit(ctx context.Context, r SubmitRequest) (*Job, error) { ... }
func (m *Manager) EnsureSchedule(ctx context.Context, s ScheduleInput) error { ... }
func (m *Manager) AdminRouter() chi.Router    { ... }
```

---

## §3 — server.New wiring

Full wiring block from `internal/server/server.go`:

```go
// ── Job queue wiring ─────────────────────────────────────────────────────────

jobStore := jobqueue.NewPgJobStore(pool,
    cfg.JobAgingRateSeconds,
    cfg.JobAgingCap,
)

mgr := jobqueue.NewManager(jobqueue.ManagerConfig{
    Store:             jobStore,
    Pool:              pool,
    PubSub:            redisStore, // *kvstore.RedisStore — already wired for rate limiting
    // Metrics: nil → NewManager defaults to QueryMetricsRecorder(jobStore).
    // V2: Metrics: jobqueue.NewPrometheusMetricsRecorder(prometheus.DefaultRegisterer)
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

// Register handlers — must happen before Start.
mgr.Register(worker.KindPurgeAccounts,    worker.NewPurgeHandler(pool))
mgr.Register(worker.KindExecuteRequest,   worker.NewExecuteRequestHandler(pool))
mgr.Register(worker.KindSendNotification, worker.NewSendNotificationHandler(pool, mailer))
mgr.Register(worker.KindPurgeCompleted,   worker.NewPurgeCompletedHandler(pool))
mgr.Register(worker.KindPurgeExpiredPermissions, worker.NewPurgeExpiredPermissionsHandler(pool))

// Seed built-in schedules (idempotent — upserts by name unique constraint).
if err := mgr.EnsureSchedule(ctx, jobqueue.ScheduleInput{
    Name:            "purge_accounts_hourly",
    Kind:            worker.KindPurgeAccounts,
    IntervalSeconds: ptr(3600),
    SkipIfRunning:   true,
}); err != nil {
    return nil, fmt.Errorf("seed schedule purge_accounts_hourly: %w", err)
}
if err := mgr.EnsureSchedule(ctx, jobqueue.ScheduleInput{
    Name:            "purge_completed_jobs_daily",
    Kind:            worker.KindPurgeCompleted,
    IntervalSeconds: ptr(86400),
    SkipIfRunning:   true,
}); err != nil {
    return nil, fmt.Errorf("seed schedule purge_completed_jobs_daily: %w", err)
}
if err := mgr.EnsureSchedule(ctx, jobqueue.ScheduleInput{
    Name:            "purge_expired_permissions_5min",
    Kind:            worker.KindPurgeExpiredPermissions,
    IntervalSeconds: ptr(300),
    SkipIfRunning:   true,
}); err != nil {
    return nil, fmt.Errorf("seed schedule purge_expired_permissions_5min: %w", err)
}

mgr.Start(ctx)

deps.Jobs   = mgr   // jobqueue.Submitter
deps.JobMgr = mgr   // *jobqueue.Manager

r.Mount("/admin/jobqueue", mgr.AdminRouter())
```

---

## §4 — Cleanup order

The cleanup order in `server.go` is non-negotiable. Out-of-order shutdown causes
use-after-close panics on the pool or mail queue.

```go
cleanup := func() {
    // 1. Stop scheduler + drain workers + mark worker offline in DB.
    //    Must happen before pool.Close() — MarkWorkerOffline needs a connection.
    mgr.Shutdown()

    // 2. Drain mail queue — may use DB connections for outbox reads.
    //    Must happen before pool.Close().
    q.Shutdown()

    // 3. Close pool — safe only after all consumers have stopped.
    pool.Close()
}
```

---

## §5 — Compile-time interface checks

Add to the bottom of the relevant files:

```go
// manager.go
var _ Submitter = (*Manager)(nil)

// metrics.go
var _ MetricsRecorder = (*QueryMetricsRecorder)(nil)
var _ MetricsRecorder = (NoopMetricsRecorder)(nil)

// internal/platform/kvstore/redis.go — added after Phase 3 creates the interface
var _ jobqueue.PubSub = (*RedisStore)(nil)
```

---

## §6 — Test inventory

| # | Case | Layer |
|---|------|-------|
| T-07 | Submit after Shutdown → error returned | U |
| T-08 | Register after Start → panic | U |
| T-09 | Register same Kind twice → panic | U |
| T-14 | Graceful shutdown drains in-flight jobs | I |
| T-MGR-1 | NewManager with nil Metrics defaults to QueryMetricsRecorder | U |
| T-MGR-2 | EnsureSchedule is idempotent — calling twice does not duplicate | I |
| T-MGR-3 | AdminRouter is non-nil after Start | U |
