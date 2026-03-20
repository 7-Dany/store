# Job Queue Metrics — Technical Design

> **Read first:** `metrics-feature.md` — hook semantics, implementation, import cycle rationale.
> **Alert rules + metric inventory:** `../../monitoring/monitoring-technical.md §18–20`

---

## Table of Contents

1. [MetricsRecorder interface](#1--metricsrecorder-interface)
2. [NoopMetricsRecorder](#2--noopmetricsrecorder)
3. [Compile-time checks](#3--compile-time-checks)
4. [Contract test structure](#4--contract-test-structure)
5. [Test inventory](#5--test-inventory)

---

## §1 — MetricsRecorder interface

```go
// internal/platform/jobqueue/metrics.go

// MetricsRecorder is the narrow observability interface injected into Manager.
// Implementations must be safe for concurrent use. Hooks must not block.
//
// All parameters are primitive types (string, not Kind or uuid.UUID) to avoid
// an import cycle. telemetry is the foundational observability package and must
// not import jobqueue. If the interface used jobqueue.Kind, telemetry would need
// to import jobqueue to name that type — the forbidden direction.
// Call sites do string(job.Kind) before passing — one character of overhead.
//
// *telemetry.Registry satisfies this interface structurally. Pass it directly
// as ManagerConfig.Metrics in server.New. Pass nil to get NoopMetricsRecorder.
type MetricsRecorder interface {
    OnJobSubmitted(kind string)
    OnJobClaimed(kind string)
    OnJobSucceeded(kind string, duration time.Duration)
    OnJobFailed(kind string, err error, willRetry bool)
    OnJobDead(kind string)
    OnJobCancelled(kind string)
    OnScheduleFired(scheduleID string, kind string)
    OnJobsRequeued(count int)
}
```

---

## §2 — NoopMetricsRecorder

The only implementation in this package. All hooks are empty. Used as the
default when `ManagerConfig.Metrics` is nil, and explicitly in tests.

```go
// internal/platform/jobqueue/metrics.go

type NoopMetricsRecorder struct{}

func (NoopMetricsRecorder) OnJobSubmitted(string)                  {}
func (NoopMetricsRecorder) OnJobClaimed(string)                    {}
func (NoopMetricsRecorder) OnJobSucceeded(string, time.Duration)   {}
func (NoopMetricsRecorder) OnJobFailed(string, error, bool)        {}
func (NoopMetricsRecorder) OnJobDead(string)                       {}
func (NoopMetricsRecorder) OnJobCancelled(string)                  {}
func (NoopMetricsRecorder) OnScheduleFired(string, string)         {}
func (NoopMetricsRecorder) OnJobsRequeued(int)                     {}
```

`NewManager` wires it automatically:

```go
func NewManager(cfg ManagerConfig) *Manager {
    if cfg.Metrics == nil {
        cfg.Metrics = NoopMetricsRecorder{}
    }
    // ...
}
```

---

## §3 — Compile-time checks

```go
// internal/platform/jobqueue/metrics.go

var _ MetricsRecorder = (NoopMetricsRecorder)(nil)

// *telemetry.Registry check lives in server.go — not here, import cycle:
// var _ jobqueue.MetricsRecorder = (*telemetry.Registry)(nil)
```

---

## §4 — Contract test structure

```go
// internal/platform/jobqueue/metrics_contract_test.go

func RunMetricsRecorderContractTests(t *testing.T, rec MetricsRecorder) {
    t.Helper()

    t.Run("all hooks callable without panic", func(t *testing.T) {
        rec.OnJobSubmitted("send_notification")
        rec.OnJobClaimed("send_notification")
        rec.OnJobSucceeded("send_notification", 150*time.Millisecond)
        rec.OnJobFailed("send_notification", errors.New("smtp error"), true)
        rec.OnJobFailed("send_notification", errors.New("smtp error"), false)
        rec.OnJobDead("send_notification")
        rec.OnJobCancelled("send_notification")
        rec.OnScheduleFired("550e8400-e29b-41d4-a716-446655440000", "purge_accounts")
        rec.OnJobsRequeued(3)
    })

    t.Run("hooks safe for concurrent use", func(t *testing.T) {
        var wg sync.WaitGroup
        for i := 0; i < 50; i++ {
            wg.Add(1)
            go func() {
                defer wg.Done()
                rec.OnJobSucceeded("execute_request", 100*time.Millisecond)
                rec.OnJobFailed("execute_request", errors.New("err"), true)
                rec.OnJobDead("execute_request")
            }()
        }
        wg.Wait()
    })
}

func TestNoopMetricsRecorder(t *testing.T) {
    RunMetricsRecorderContractTests(t, NoopMetricsRecorder{})
}

// *telemetry.Registry tested in server_test.go where both packages are imported.
```

---

## §5 — Test inventory

| # | Case | Type |
|---|------|------|
| T-M1 | `NoopMetricsRecorder` satisfies `MetricsRecorder` (compile-time) | U |
| T-M2 | All hooks callable without panic | U |
| T-M3 | Hooks safe under 50 concurrent goroutines | U |
| T-M4 | `NewManager` with nil Metrics defaults to `NoopMetricsRecorder` | U |
| T-M5 | `NewManager` with explicit `NoopMetricsRecorder` uses it directly | U |
| T-M6 | Dispatcher calls `OnJobSucceeded` exactly once after handler returns nil | I |
| T-M7 | Dispatcher calls `OnJobFailed(willRetry=true)` when attempt < max | I |
| T-M8 | Dispatcher calls `OnJobFailed(willRetry=false)` then `OnJobDead` when attempt == max | I |
| T-M9 | Dispatcher calls `OnJobDead` for unknown kind (no retry) | I |
| T-M9b | Dispatcher calls `OnJobFailed(willRetry=false)` then `OnJobDead` for unknown kind — same ordering as T-M8 | I |
| T-M10 | ScheduleWatcher calls `OnScheduleFired` with correct scheduleID and kind | I |
| T-M11 | StallDetector calls `OnJobsRequeued` with correct count | I |
| T-M12 | Swap `NoopMetricsRecorder` → `*telemetry.Registry` in ManagerConfig — no other code changes | U |
