# Auth System — Remaining Routes to Implement

Routes **not yet built**. Everything here needs to be designed, implemented, and
then get its own E2E section in `CHECKLIST.md` before being marked production-ready.

**Legend**
- `[ ]` — not yet started
- `[~]` — in progress

---

## Implementation Order

```
internal/platform/
│
└── jobqueue/      ← Persistent queue, admin API, WebSocket              §H-0  ← START HERE

internal/domain/
│
├── admin/
│   ├── users/                                                            §F-1
│   ├── audit/                                                            §F-2
│   ├── sessions/                                                         §F-3
│   └── recovery/  ← paired with auth/magiclink/                          §F-5
│
└── auth/
    └── magiclink/ ← GET /magic-link/verify                               §F-5  (paired with admin/recovery/)
```

**Dependency order:**

1. **§H-0** — Job queue migration + `internal/platform/jobqueue/` package + worker
   handlers + server wiring. Requires `kvstore.RedisStore` to gain `Publish` / `Subscribe`.

2. **§F-1 / §F-2 / §F-3** — Admin user listing, audit log, session administration.
   Can proceed in parallel — all already unblocked by §G-0 (done).

3. **§F-5** — CS-assisted recovery + magic-link verify. Must be implemented together
   in one PR (`admin/recovery/` + `auth/magiclink/`).

---

## Group H — Job Queue

Full design: `docs/prompts/jobqueue/0-design.md`

### §H-0 — Job Queue Platform + Worker Handlers + Server Wiring

**`sql/schema/006_jobqueue.sql`** (NEW):
- [ ] Creates `job_paused_kinds`, `jobs`, `workers`, `job_schedules` tables with all indexes and constraints
- [ ] Drops `request_executions` (replaced by `kind="execute_request"` jobs)
- [ ] Removes delivery retry columns from `request_notifications` (`delivery_attempts`, `last_attempt_at`, `delivery_error`)

**`internal/platform/kvstore/redis.go`** (MODIFY):
- [ ] Add `Publish(ctx, channel, message string) error`
- [ ] Add `Subscribe(ctx, channel string) (<-chan string, error)`
- [ ] `RedisStore` now satisfies `jobqueue.PubSub` interface

**`internal/platform/jobqueue/`** (NEW package — pure platform, no domain imports):
- [ ] `job.go` — `Job`, `Kind`, `Status`, `Handler`, `HandlerFunc`, `Submitter`,
      `SubmitRequest`, `WorkerInfo`, `Schedule`, `PausedKind`, `PubSub` interface,
      `MetricsRecorder` interface (all 9 hooks + `MetricsHandler() http.Handler`)
- [ ] `store.go` — `JobStore` interface + `pgJobStore`; claim query uses aging formula
      `effective_priority = priority + LEAST(minutes_waited, AgingCap)`
- [ ] `dispatcher.go` — N worker goroutines; Redis SUBSCRIBE wake + 10s fallback ticker;
      SKIP LOCKED claim; heartbeat upsert
- [ ] `scheduler.go` — single `ScheduleWatcher` goroutine; 10s poll; cron (`robfig/cron`)
      + interval support; PUBLISH failure is warn-only (job row already in Postgres)
- [ ] `deadletter.go` — DB-backed dead-letter queries (delegates to `JobStore`)
- [ ] `metrics.go` — `QueryMetricsRecorder` (V1 default, zero new deps, SQL query per scrape)
      + `NoopMetricsRecorder` (tests)
- [ ] `api.go` — 21-route `chi.Router` factory (20 REST + 1 WebSocket); all routes require
      `deps.RBAC.Require(rbac.PermJobQueueRead)` or `PermJobQueueManage`; includes
      `GET /metrics` delegating to `MetricsRecorder.MetricsHandler()`
- [ ] `ws.go` — `WSHub`; per-client 64-event buffered channel; `writePump` goroutine
      per client (hub never blocks on slow clients)
- [ ] `manager.go` — `Manager` lifecycle: `Register`, `Start`, `Shutdown`, `Submit`,
      `EnsureSchedule`, `AdminRouter`

Contract test files (interface-gated swap safety):
- [ ] `store_contract_test.go` — `RunJobStoreContractTests(t, newStore func)`
- [ ] `pubsub_contract_test.go` — `RunPubSubContractTests(t, newPubSub func)`
- [ ] `metrics_contract_test.go` — `RunMetricsRecorderContractTests(t, rec)`

**`internal/worker/`** (MODIFY / NEW):
- [ ] `kinds.go` — add `KindExecuteRequest`, `KindSendNotification`, `KindPurgeCompleted`
- [ ] `purge.go` — signature update: `Job` not `any` (logic unchanged)
- [ ] `execute_request.go` (NEW) — replaces `request_executions` inline execution
- [ ] `send_notification.go` (NEW) — replaces `request_notifications` delivery retry
- [ ] `purge_completed.go` (NEW) — deletes `succeeded`/`cancelled` jobs older than `RetentionDays`

**Server wiring** (MODIFY):
- [ ] `internal/app/deps.go` — add `Jobs jobqueue.Submitter`, `JobMgr *jobqueue.Manager`
- [ ] `internal/server/server.go` — construct `Manager`; register handlers; seed schedules
      (`purge_accounts_hourly`, `purge_completed_jobs_daily`); `mgr.Start(ctx)`;
      mount `r.Mount("/admin/jobqueue", mgr.AdminRouter())`;
      shutdown order: `mgr.Shutdown()` → `q.Shutdown()` → `pool.Close()`
- [ ] `internal/config/config.go` — add `JobWorkers`, `JobRetentionDays`; remove `JobQueueSize`

**Redis downtime guarantee:** 10s Postgres fallback ticker always runs in every worker
goroutine — jobs are never lost. Max extra latency when Redis is down: ≤ 10s.

**Admin API endpoints** (mounted at `/admin/jobqueue`, all require RBAC):

| Method | Path | Permission |
|--------|------|------------|
| GET | /workers | `job_queue:read` |
| GET | /workers/:id | `job_queue:read` |
| DELETE | /workers/:id | `job_queue:manage` |
| GET | /jobs | `job_queue:read` |
| GET | /jobs/:id | `job_queue:read` |
| DELETE | /jobs/:id | `job_queue:manage` |
| PATCH | /jobs/:id/priority | `job_queue:manage` |
| POST | /jobs/:id/retry | `job_queue:manage` |
| GET | /dead | `job_queue:read` |
| DELETE | /dead | `job_queue:manage` |
| GET | /queues | `job_queue:read` |
| POST | /queues/:kind/pause | `job_queue:manage` |
| POST | /queues/:kind/resume | `job_queue:manage` |
| GET | /schedules | `job_queue:read` |
| POST | /schedules | `job_queue:manage` |
| PUT | /schedules/:id | `job_queue:manage` |
| DELETE | /schedules/:id | `job_queue:manage` |
| POST | /schedules/:id/trigger | `job_queue:manage` |
| GET | /stats | `job_queue:read` |
| GET | /metrics | `job_queue:read` |
| GET | /ws | `job_queue:read` |

---

## Group F — Admin Domain

`internal/domain/admin/` follows the identical three-layer layout as auth.
All admin routes require JWT + an RBAC permission (§G-0 must be live).

---

### §F-1 — User Listing and Detail

`GET /api/v1/admin/users`
- [ ] Paginated (cursor-based)
- [ ] Filters: `is_locked`, `admin_locked`, `is_active`, `email_verified`,
      `created_after`, `search` (partial email or username match)
- [ ] Never return `password_hash`

`GET /api/v1/admin/users/{id}`
- [ ] Full profile: all `users` columns except `password_hash`
- [ ] Includes current role from `user_roles`
- [ ] Includes counts: `session_count`, `recent_failed_logins`

---

### §F-2 — User Audit Log

`GET /api/v1/admin/users/{id}/audit`
- [ ] Paginated rows from `auth_audit_log` filtered by `user_id`
- [ ] Query params: `limit` (default 50, max 200), `cursor`, `event_type`, `from`, `to`
- [ ] Masks sensitive `metadata` fields before returning
- [ ] Rate-limit: 30 req / 1 min per admin (key `aaud:usr:`)

`GET /api/v1/admin/audit`
- [ ] Global audit log — same schema, no `user_id` filter
- [ ] Additional filters: `ip_address`, `provider`
- [ ] Rate-limit: 10 req / 1 min per admin (key `gaud:usr:`)

---

### §F-3 — Session Administration

`GET /api/v1/admin/users/{id}/sessions`
- [ ] Returns all active sessions for any user

`DELETE /api/v1/admin/users/{id}/sessions`
- [ ] Force-revokes all active sessions; revokes all refresh tokens (`forced_logout`)
- [ ] Audit row: `forced_logout` on target user

`DELETE /api/v1/admin/users/{id}/sessions/{session_id}`
- [ ] Force-revoke a single session
- [ ] Audit row: `session_force_revoked`

---

### §F-5 — CS-Assisted Account Recovery

These three admin routes and the user-facing magic-link verify endpoint form a
single recovery workflow. Implement them together in one PR.

`PATCH /api/v1/admin/users/{id}/email`
- [ ] Body: `{ "new_email": "...", "reason": "ticket:#1234" }` — reason required
- [ ] Validates `new_email` format + uniqueness
- [ ] Notifies old email; confirms to new email
- [ ] Revokes all refresh tokens (`email_changed_by_admin`); blocklists access tokens
- [ ] Cannot no-op (guard if email already matches)
- [ ] Cannot target another owner unless actor is also an owner
- [ ] Audit row: `admin_email_changed` (old + new email, `admin_id` in `metadata`)
- [ ] Rate-limit: 10 req / 1 min per admin (key `adm:echg:usr:`)

`POST /api/v1/admin/users/{id}/magic-link`
- [ ] Body: `{ "send_to": "...", "redirect_to": "...", "reason": "ticket:#1234" }`
- [ ] `send_to` validated but need not match user's registered email (recovery scenario)
- [ ] `redirect_to` validated against internal allowlist (not bypassed for admins)
- [ ] One active magic-link token per user at a time (new one invalidates existing)
- [ ] TTL: 1 hour
- [ ] Sends email to `send_to`; never to `users.email`
- [ ] Audit row: `admin_magic_link_issued` (`admin_id`, `send_to`, `reason` in `metadata`)
- [ ] Rate-limit: 5 req / 15 min per admin (key `adm:ml:usr:`)

`POST /api/v1/admin/users/{id}/force-password-reset`
- [ ] Body: `{ "reason": "ticket:#1234" }` — required
- [ ] Sets `password_hash = NULL`
- [ ] Revokes all refresh tokens (`force_password_reset`); blocklists access tokens
- [ ] Immediately triggers `forgot-password` OTP flow to `users.email`
- [ ] Cannot target another owner unless actor is also an owner
- [ ] Audit row: `admin_force_password_reset` (`admin_id`, `reason` in `metadata`)
- [ ] Rate-limit: 5 req / 15 min per admin (key `adm:fpr:usr:`)

#### Magic Link — user-facing verify (paired with admin/recovery/)

New package: `internal/domain/auth/magiclink/`

> Magic links are admin-controlled recovery tools only. Self-service issuance
> is intentionally omitted. All issuance goes through the admin routes above.

`GET /api/v1/auth/magic-link/verify?token=<token_hash>`
- [ ] Public — the token is the credential
- [ ] Validates `token_hash` against `one_time_tokens` where
      `token_type = 'magic_link'` and `used_at IS NULL` and `expires_at > NOW()`
- [ ] Checks linked user is not `admin_locked` or `is_locked` at verify-time
- [ ] Marks token consumed (`used_at`)
- [ ] Creates session + issues refresh/access token pair (same as `POST /login`)
- [ ] Redirects to `redirect_to` URL stored on the token row (not caller-supplied)
- [ ] Unknown / expired / used token → redirect to generic frontend error page
- [ ] Audit row: `magic_link_verified` (includes `admin_id` from token `metadata`)

---

## Cross-cutting Reminders

- Every mutation must write an audit row to `auth_audit_log`
- Admin routes verify the RBAC check **before** any DB read
- Rate-limit key prefixes must not reuse any prefix already defined in `CHECKLIST.md`
- `deps.RBAC.Require("resource:action")` always chains after `deps.JWTAuth` in the middleware stack — never standalone
