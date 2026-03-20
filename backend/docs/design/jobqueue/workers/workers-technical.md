# Workers Technical — Handler Signatures, Kinds, Schedule Seeds, Tests

> **What this file is:** Kind constants, handler constructor signatures, schedule
> seed inputs, retry sentinel pattern, and test inventory.
>
> **Read first:** `workers-feature.md` — what each handler does and why it exists.

---

## Table of Contents

1. [Kind constants](#1--kind-constants)
2. [Handler signatures](#2--handler-signatures)
3. [Schedule seeds](#3--schedule-seeds)
4. [Non-retriable error sentinel](#4--non-retriable-error-sentinel)
5. [PurgeHandler migration from goroutine](#5--purgehandler-migration-from-goroutine)
6. [Test inventory](#6--test-inventory)

---

## §1 — Kind constants

```go
// internal/worker/kinds.go

package worker

import "github.com/yourorg/store/internal/platform/jobqueue"

const (
    KindPurgeAccounts           jobqueue.Kind = "purge_accounts"
    KindExecuteRequest          jobqueue.Kind = "execute_request"
    KindSendNotification        jobqueue.Kind = "send_notification"
    KindPurgeCompleted          jobqueue.Kind = "purge_completed_jobs"
    KindPurgeExpiredPermissions jobqueue.Kind = "purge_expired_permissions"
)
```

Kind strings are the stable identifiers stored in `jobs.kind`. Changing a kind
constant renames the kind — any jobs already in the queue under the old name become
unknown-kind and are dead-lettered. Treat kind strings as API surface.

---

## §2 — Handler signatures

```go
// internal/worker/execute_request.go
type ExecuteRequestHandler struct{ pool *pgxpool.Pool }
func NewExecuteRequestHandler(pool *pgxpool.Pool) *ExecuteRequestHandler
func (h *ExecuteRequestHandler) Handle(ctx context.Context, job jobqueue.Job) error

// internal/worker/send_notification.go
type SendNotificationHandler struct {
    pool   *pgxpool.Pool
    mailer mail.Mailer
}
func NewSendNotificationHandler(pool *pgxpool.Pool, mailer mail.Mailer) *SendNotificationHandler
func (h *SendNotificationHandler) Handle(ctx context.Context, job jobqueue.Job) error

// internal/worker/purge_completed.go
type PurgeCompletedHandler struct {
    store         jobqueue.JobStore
    retentionDays int
}
func NewPurgeCompletedHandler(store jobqueue.JobStore, retentionDays int) *PurgeCompletedHandler
func (h *PurgeCompletedHandler) Handle(ctx context.Context, job jobqueue.Job) error

// internal/worker/purge.go — updated signature
type PurgeHandler struct{ pool *pgxpool.Pool }
func NewPurgeHandler(pool *pgxpool.Pool) *PurgeHandler
func (h *PurgeHandler) Handle(ctx context.Context, job jobqueue.Job) error

// internal/worker/purge_expired_permissions.go
type PurgeExpiredPermissionsHandler struct{ pool *pgxpool.Pool }
func NewPurgeExpiredPermissionsHandler(pool *pgxpool.Pool) *PurgeExpiredPermissionsHandler
func (h *PurgeExpiredPermissionsHandler) Handle(ctx context.Context, job jobqueue.Job) error
```

All constructors return the concrete type (not the interface) so callers can pass
them directly to `mgr.Register` without a cast.

---

## §3 — Schedule seeds

Seeded in `server.New` via `mgr.EnsureSchedule` — idempotent on every restart.

| Name | Kind | Interval | SkipIfRunning |
|------|------|----------|---------------|
| `purge_accounts_hourly` | `purge_accounts` | 3600s (1h) | true |
| `purge_completed_jobs_daily` | `purge_completed_jobs` | 86400s (24h) | true |
| `purge_expired_permissions_5min` | `purge_expired_permissions` | 300s (5min) | true |

`SkipIfRunning: true` on all three prevents pile-up if a previous run is still active.

---

## §4 — Non-retriable error sentinel

Some handler failures should dead-letter immediately rather than consuming retry
budget. The pattern:

```go
// jobqueue/job.go — sentinel for permanent failures
type PermanentError struct{ Err error }
func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Dispatcher checks:
var permErr *PermanentError
if errors.As(runErr, &permErr) {
    // Dead-letter without incrementing attempt
    _ = d.store.DeadLetterJob(ctx, job.ID, runErr)
    d.metrics.OnJobDead(*job)
    return
}
```

Usage in `execute_request.go`:
```go
req, err := db.GetRequest(ctx, requestID)
if errors.Is(err, pgx.ErrNoRows) {
    return &jobqueue.PermanentError{Err: fmt.Errorf("request %s not found", requestID)}
}
```

---

## §5 — PurgeHandler migration from goroutine

The existing `PurgeWorker` type and its `Start()` goroutine remain untouched until
Phase 7. In Phase 6, `PurgeHandler` is added alongside it — the same logic, new
interface. In Phase 7, the `go worker.NewPurgeWorker(...).Start(ctx)` call is
removed from `server.go`. No logic changes; just a structural migration.

```go
// Phase 6 — add alongside existing PurgeWorker, do not touch PurgeWorker yet:
type PurgeHandler struct{ pool *pgxpool.Pool }

func NewPurgeHandler(pool *pgxpool.Pool) *PurgeHandler {
    return &PurgeHandler{pool: pool}
}

func (h *PurgeHandler) Handle(ctx context.Context, job jobqueue.Job) error {
    return runOnce(ctx, h.pool) // reuse the same private function PurgeWorker calls
}
```

---

## §6 — Test inventory

| # | Case | Layer |
|---|------|-------|
| T-41 | execute_request handler executes approved request idempotently | I |
| T-42 | send_notification handler delivers via mailer, retries on transient error | I |
| T-43 | purge_completed_jobs deletes only jobs older than RetentionDays | I |
| T-44 | PurgeHandler purges accounts past grace period | I |
| T-45 | PurgeHandler skips accounts within grace period | I |
| T-46 | PurgeHandler returns nil when no accounts are due | I |
| T-WK-1 | execute_request with non-existent request returns PermanentError (dead-lettered, no retry) | I |
| T-WK-2 | execute_request with already-executed request is a no-op (idempotent) | I |
| T-WK-3 | send_notification with already-delivered notification is a no-op | I |
| T-WK-4 | purge_expired_permissions deletes only rows where expires_at <= NOW() | I |
