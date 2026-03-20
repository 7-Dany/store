# Workers Feature — Handler Behavior & Responsibilities

> **What this file is:** A plain-language description of each job handler in
> `internal/worker/`: what it does, what it replaces, idempotency rules, and
> retry expectations. Read this before the technical file.
>
> **Companion:** `workers-technical.md` — handler signatures, kind constants,
> schedule seeds, retry behavior, test inventory.
> **Schema reference:** `sql/schema/007_jobqueue.sql` (`jobs` table,
> `job_schedules` table).

---

## Overview

`internal/worker/` contains all job handler implementations. Each handler is a
struct that satisfies `jobqueue.Handler`. Handlers are registered with the Manager
in `server.New` and run by the Dispatcher when a job of the matching kind is claimed.

---

## KindExecuteRequest — `execute_request`

**Replaces:** the `request_executions` table and its inline execution path.

When a request is approved, instead of executing inline (blocking the HTTP handler)
or inserting into `request_executions`, the system submits a `kind="execute_request"`
job. This job carries the request ID in its payload.

**What the handler does:**

1. Reads the approved request by ID from the database.
2. Verifies the request is still in `approved` status (idempotency guard — the job
   may have been retried after a crash mid-execution).
3. Executes the request action (the same logic that was previously inline).
4. Updates the request status to `executed` and writes the result.

**Idempotency:** The job is submitted with `idempotency_key = request_id`. Re-submitting
an already-executing request returns the existing job row silently. The handler itself
also checks request status before executing, so a second run on an already-executed
request is a no-op.

**Retry behavior:** Transient errors (DB connectivity, downstream service timeouts)
are returned as errors and trigger the queue's normal retry with backoff. Permanent
errors (request not found, already cancelled) are returned as non-retriable errors
by returning a sentinel that the Dispatcher recognizes as dead-letter immediately
without consuming retry budget.

---

## KindSendNotification — `send_notification`

**Replaces:** the `delivery_attempts`, `last_attempt_at`, and `delivery_error` columns
on `request_notifications` and their associated retry loop.

When a notification must be delivered (email, push, webhook), a
`kind="send_notification"` job is submitted with the notification ID in its payload.

**What the handler does:**

1. Reads the notification record by ID.
2. Checks delivery status — if already delivered, returns nil (idempotent no-op).
3. Calls the mailer with the notification content.
4. On success: marks the notification as delivered in `request_notifications`.
5. On failure: returns the error — the queue retries with exponential backoff up to
   `max_attempts`.

**Idempotency:** The mailer is expected to be idempotent (most email providers provide
message deduplication by message ID). The handler uses the notification ID as the
mailer's message ID where possible.

---

## KindPurgeCompleted — `purge_completed_jobs`

**New.** Runs on the `purge_completed_jobs_daily` schedule (every 24 hours).

**What the handler does:**

Calls `store.PurgeCompletedJobs(ctx, retentionDays)`, which deletes all rows in
`succeeded`, `failed`, and `cancelled` status older than the configured retention
window (default 30 days). Returns the count of deleted rows, which is logged.

**Skip if running:** the schedule has `skip_if_running = true`. If a previous daily
purge is still running (unlikely but possible on large tables), the next fire is
skipped rather than running two purges simultaneously.

---

## KindPurgeAccounts — `purge_accounts`

**Existing handler, updated signature.** Previously ran on its own goroutine
(`PurgeWorker.Start()`). Now registered as a job handler and fired by the
`purge_accounts_hourly` schedule.

**What the handler does:** same logic as before — finds user accounts past their
deletion grace period and permanently removes them from the database. The goroutine
in `server.go` that called `PurgeWorker.Start()` is removed in Phase 7.

---

## KindPurgeExpiredPermissions — `purge_expired_permissions`

**New.** Runs on the `purge_expired_permissions_5min` schedule (every 5 minutes).

**What the handler does:**

Deletes rows from `user_permissions` where `expires_at <= NOW()`. This is the
reliability gate for RBAC re-grant: when a temporary permission expires in the DB,
the next RBAC check will see it as absent and force re-grant if the underlying
condition still holds. Without this handler, stale permission rows accumulate and
RBAC checks may incorrectly see an expired permission as still active.

See `rbac/0-design.md §16 TODO-1` for the full context of why this schedule exists.

---

## Handler contract

Every handler must:
- Return `nil` on success (the Dispatcher calls `store.CompleteJob`).
- Return a non-nil `error` on transient failure (the Dispatcher retries).
- Be safe for concurrent use — the Dispatcher may run multiple instances of any
  handler kind simultaneously across worker goroutines.
- Never panic in a way that crashes the process — the Dispatcher recovers panics
  and treats them as errors.
