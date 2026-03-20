# Manager Feature — Lifecycle & Wiring

> **What this file is:** A plain-language description of what the Manager is,
> the rules around handler registration, the startup and shutdown sequence, and
> how the Manager wires into the rest of the application. Read this before the
> technical file.
>
> **Companion:** `manager-technical.md` — `ManagerConfig`, `NewManager`, `server.New`
> wiring, cleanup order, test inventory.

---

## What the Manager is

The Manager is the top-level coordinator for the entire job queue system. It composes
the Dispatcher, ScheduleWatcher, StallDetector, WSHub, store, and metrics recorder
into a single lifecycle object. Application code interacts with the Manager through
two narrow interfaces:

- **`Submitter`** — the single-method interface (`Submit`) that domain code uses to
  enqueue jobs. Only this interface is placed on `app.Deps`, keeping domain code
  completely decoupled from the full Manager.
- **`*Manager`** — the full type placed on `app.Deps.JobMgr`, used only by
  `server.go` for lifecycle management (Start, Shutdown, AdminRouter).

---

## Handler registration rules

Handlers are registered with `mgr.Register(kind, handler)` before calling
`mgr.Start()`. Two rules are strictly enforced:

**No registration after Start.** Calling `Register` after `Start` panics. This
prevents handlers from being silently ignored because the Dispatcher is already
running. If a handler must be added dynamically, it belongs in application startup
logic, not at runtime.

**No duplicate registration.** Registering the same `Kind` twice panics. This catches
copy-paste mistakes during wiring — a kind can only have one handler.

Both rules produce panics rather than errors because they represent programmer
mistakes, not runtime conditions. They will always be caught during development.

---

## Startup sequence

`mgr.Start(ctx)` starts all components in a defined order:

1. Upsert the worker row in the `workers` table (`UpsertWorker`).
2. Start the Dispatcher worker goroutines (Redis SUBSCRIBE + ticker).
3. Start the ScheduleWatcher poll loop.
4. Start the StallDetector tick loop.
5. Start the WSHub goroutine.
6. Start the heartbeat goroutine.

All components share the same `ctx`. Cancelling `ctx` (by calling `Shutdown`) stops
all goroutines in reverse order.

---

## Shutdown sequence

`mgr.Shutdown()` performs a graceful drain:

1. Stops the ScheduleWatcher — no new scheduled jobs are inserted.
2. Signals worker goroutines to stop accepting new claims.
3. Waits for in-flight jobs to complete (bounded drain timeout).
4. Stops the StallDetector.
5. Calls `store.MarkWorkerOffline` to update the `workers` row.
6. Closes the WSHub (disconnects all WebSocket clients).

`pool.Close()` must be called only after `mgr.Shutdown()` completes — the Manager
uses the pool for the final offline write, and the mail queue (if any) may also
use the pool. The cleanup order in `server.go` enforces this.

---

## Submitter interface

Domain code and tests use only the narrow `Submitter` interface:

```go
type Submitter interface {
    Submit(ctx context.Context, req SubmitRequest) (*Job, error)
}
```

`Manager` satisfies this interface. Tests can use a mock `Submitter` without
instantiating a full Manager. Domain handlers that need to enqueue follow-up jobs
receive `deps.Jobs` (a `Submitter`) not `deps.JobMgr` (a `*Manager`).

---

## Defaults applied by NewManager

When `ManagerConfig` fields are zero-valued, `NewManager` applies sensible defaults:

| Field | Default |
|-------|---------|
| Metrics | `QueryMetricsRecorder(store)` |
| Workers | 4 |
| Queues | `["default"]` |
| DefaultTimeout | 10 minutes |
| DefaultAttempts | 5 |
| RetentionDays | 30 |
| HeartbeatEvery | 15 seconds |
| StallCheck | 30 seconds |
| ScheduleCheck | 10 seconds |
| NotifyChannel | `"jobqueue:notify"` |
| RedisFallbackPoll | 10 seconds |
| AgingRateSeconds | 60 |
| AgingCap | 50 |
