# Job Queue Metrics — Feature Design

> **Package:** `internal/platform/jobqueue`
> **Companion:** `metrics-technical.md` — interface definition, implementation, contract tests.
> **Alert rules + metric inventory:** `../../monitoring/monitoring-technical.md §18–20`

---

## What MetricsRecorder is

A narrow hook interface injected into `Manager` via `ManagerConfig.Metrics`. Every
significant job event calls a hook. The Dispatcher, ScheduleWatcher, and StallDetector
never know or care what is on the other side — they call the hook and move on.

Two rules apply to every implementation:
- **Hooks must not block.** They are called from worker goroutines mid-job.
- **Implementations must be safe for concurrent use.** Multiple workers call hooks simultaneously.

---

## What each hook signals

| Hook | Called by | When |
|------|-----------|------|
| `OnJobSubmitted` | Manager.Submit | Job row successfully inserted |
| `OnJobClaimed` | Dispatcher | Worker claimed a job row |
| `OnJobSucceeded` | Dispatcher | Handler returned nil |
| `OnJobFailed` | Dispatcher | Handler returned error |
| `OnJobDead` | Dispatcher | Attempt == max_attempts, or unknown kind |
| `OnJobCancelled` | AdminRouter, Manager.Shutdown | Job cancelled via admin API or during Shutdown |
| `OnScheduleFired` | ScheduleWatcher | Schedule inserted a job row |
| `OnJobsRequeued` | StallDetector | Stalled jobs reset to pending |

> **Note — `OnJobCancelled` and Shutdown:** Any code path that transitions a job
> to cancelled state — including `Manager.Shutdown` — must call `OnJobCancelled`.
> Jobs aborted or left pending during shutdown that are transitioned to cancelled
> state are not exempt. The Shutdown design doc must specify which job states are
> cancelled vs re-queued, and assert that `OnJobCancelled` fires for every
> cancelled transition regardless of the initiating path.

> **Note — `OnJobDead` and Shutdown:** If Shutdown aborts running jobs and
> transitions them to dead state (rather than re-queuing them), `OnJobDead` must
> also be called. The Shutdown design doc must explicitly specify this. Until it
> does, jobs aborted by Shutdown may be invisible to `jobqueue_jobs_dead_total`
> and the SLO-3 ratio.

`OnJobFailed` carries `willRetry bool`. When false, `OnJobDead` fires immediately
after for the same job. This lets implementations track total failures and dead
jobs independently without double-counting.

> **Invariant:** `OnJobDead` is **always** preceded by `OnJobFailed(willRetry=false)`
> in the same code path — for every dead-letter reason, including unknown kind.
> There is no path where `OnJobDead` fires without a preceding
> `OnJobFailed(willRetry=false)` for the same job. Implementations may rely on
> this to avoid double-counting. Tests must verify both calls for every
> dead-letter path (see T-M8 and T-M9b in `metrics-technical.md`).

---

## The one implementation in this package

### NoopMetricsRecorder — the default

All hooks are empty. No SQL, no Prometheus, no side effects. Wired automatically
when `ManagerConfig.Metrics` is nil:

```go
// NewManager defaults to this when Metrics is nil
if cfg.Metrics == nil {
    cfg.Metrics = NoopMetricsRecorder{}
}
```

Use it explicitly in tests to make intent clear:

```go
mgr := jobqueue.NewManager(jobqueue.ManagerConfig{
    Metrics: jobqueue.NoopMetricsRecorder{},
})
```

---

## How *telemetry.Registry satisfies this interface

`*telemetry.Registry` implements all hook methods and satisfies `MetricsRecorder`
structurally via Go's duck typing — no explicit declaration needed anywhere.

In `server.New`:

```go
mgr := jobqueue.NewManager(jobqueue.ManagerConfig{
    Metrics: registry,   // *telemetry.Registry satisfies MetricsRecorder
})
```

The Registry's hook methods increment Prometheus counters and histograms under
the `jobqueue_*` metric family. All metrics are served from the single
application-wide `GET /metrics` endpoint. There is no separate `GET /admin/jobqueue/metrics` scrape endpoint.

---

## Why hook parameters are primitive strings

If the interface used `jobqueue.Kind` or `uuid.UUID`, then `telemetry` would
need to import `jobqueue` to name those types in its method signatures.
`telemetry` is the foundational observability package — it must not import
domain or platform packages that depend on it. That direction is the forbidden
one: `telemetry → jobqueue` is the import that cannot exist.

The fix: all hook parameters are primitives (`string`, `time.Duration`, `error`,
`bool`, `int`). `telemetry` implements the interface using only primitives — no
`jobqueue` import required. The dispatcher calls `string(job.Kind)` before
passing to the hook. One character of call-site overhead breaks the cycle
entirely.
