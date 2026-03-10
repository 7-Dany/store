# Auth System — Remaining Routes to Implement

Routes **not yet in** `E2E_CHECKLIST.md`. Everything here needs to be designed,
implemented, and then get its own E2E section before being marked production-ready.

**Legend**
- `[ ]` — not yet started
- `[~]` — in progress
- `[x]` — implemented (move to E2E_CHECKLIST.md when done)

---

## Implementation Order

Routes are sequenced so every item builds only on what is already working.
The RBAC platform primitive must land before any permission-guarded route can
be wired. The job queue migration and platform package must land before the
worker handlers or the admin job queue API can be written. Admin-domain routes
(Group F) follow after their prerequisites are met.

```
internal/platform/
│
├── rbac/          ← Checker, Require middleware, context helpers        §G-0  ← START HERE
│
└── jobqueue/      ← Persistent queue, admin API, WebSocket              §H-0  (requires §G-0)

internal/domain/
│
├── owner/
│   └── bootstrap/ ← POST /owner/bootstrap                              §G-1  (requires §G-0)
│
├── admin/
│   ├── rbac/      ← roles, permissions, user-role, user-permission API  §G-2  (requires §G-1)
│   ├── users/                                                            §F-1  (requires §G-0)
│   ├── audit/                                                            §F-2  (requires §G-0)
│   ├── sessions/                                                         §F-3  (requires §G-0)
│   ├── lock/                                                             §F-4  (requires §G-0)
│   └── recovery/  ← paired with auth/magiclink/                         §F-5  (requires §G-0)
│
└── auth/
    └── magiclink/ ← GET /magic-link/verify                              §F-5  (paired with admin/recovery/)
```

**Dependency order:**

1. **§G-0** — RBAC SQL queries + seeds + platform checker + `deps.RBAC` wiring.
   No dependencies. Platform only — no HTTP routes. This is the unlock for every
   permission-guarded route in every domain.

2. **§H-0** — Job queue migration (`006_jobqueue.sql`) + `internal/platform/jobqueue/`
   package + `internal/worker/` handlers + server wiring. Requires §G-0 because the
   job queue admin API uses `deps.RBAC.Require(rbac.PermJobQueueRead/Manage)`.
   Also requires `kvstore.RedisStore` to gain `Publish` / `Subscribe`.

3. **§G-1** — Owner bootstrap route. Requires §G-0 (checker, seeds, generated queries).
   Unauthenticated write route but uses RBAC store queries.

4. **§G-2** — RBAC admin API. Requires §G-1 so a real owner exists to call them.

5. **§F-1 through §F-5** — Admin domain routes. Each requires §G-0 for RBAC guards.
   §F-1/F-2/F-3/F-4 can proceed in parallel once §G-0 is done. §F-5 must be
   implemented together with `auth/magiclink/` (same workflow, same PR).

---

## Group G — RBAC (Role-Based Access Control)

Full design: `docs/prompts/rbac/0-design.md`

The schema and DB triggers (`003_rbac.sql`, `004_rbac_functions.sql`) are **already
in place**. The `001_roles.sql` seed already inserts the owner role row. What remains
is SQL queries, permission/role seeds, the platform checker, and the admin API.

---

### §G-0 — RBAC Platform (SQL + seeds + checker — no HTTP routes)

**`sql/queries/rbac.sql`** (NEW):
- [ ] `CheckUserAccess` — single round-trip returning `is_owner` + `has_permission`
      via UNION ALL (role path + direct-grant path)
- [ ] `CountActiveOwners`, `GetOwnerRoleID`, `GetActiveUserByID`
- [ ] `AssignUserRole` (upsert), `RemoveUserRole` (hard delete — history in audit table)
- [ ] `GetRoles`, `GetRoleByID`, `GetRoleByName`, `CreateRole`, `UpdateRole` (non-system guard),
      `DeactivateRole` (soft-delete, non-system guard)
- [ ] `GetRolePermissions`, `AddRolePermission`, `RemoveRolePermission`
- [ ] `GetPermissions`, `GetPermissionByCanonicalName`, `GetPermissionGroups`,
      `GetPermissionGroupMembers`
- [ ] `GetUserRole`, `GetUserPermissions`, `GrantUserPermission`, `RevokeUserPermission`

Run `sqlc generate` after writing queries.

**`sql/seeds/002_permissions.sql`** (NEW — idempotent, `ON CONFLICT DO NOTHING`):

| canonical_name | resource_type | Notes |
|---|---|---|
| `rbac:read` | rbac | List roles, permissions, user assignments |
| `rbac:manage` | rbac | Create/edit roles, assign role permissions |
| `rbac:grant_user_permission` | rbac | Grant direct user permissions (higher sensitivity) |
| `job_queue:read` | job_queue | View jobs, stats, metrics, schedules |
| `job_queue:manage` | job_queue | Pause, retry, cancel, update priority |
| `user:read` | user | List/view users (future) |
| `user:manage` | user | Edit/suspend users (future) |
| `request:read` | request | View requests (future) |
| `request:manage` | request | Manage requests (future) |
| `request:approve` | request | Approve requests (future) |

Permission groups: System Administration (`rbac:*`), Job Queue (`job_queue:*`),
Users (`user:*`), Requests (`request:*`).

**`sql/seeds/003_roles.sql`** (NEW — idempotent):

| Role | is_system_role | Default permissions |
|---|---|---|
| admin | TRUE | All 10 permissions |
| vendor | FALSE | `request:read`, `request:manage` |
| customer | FALSE | `request:read` |

`granted_by` for seed permission grants uses a CTE to look up the owner user;
falls back to a sentinel system UUID if no owner exists yet.

**`internal/platform/rbac/checker.go`** (NEW):
- [ ] Permission constants (never use raw string literals — these are the canonical source):
      `PermRBACRead`, `PermRBACManage`, `PermRBACGrantUserPerm`,
      `PermJobQueueRead`, `PermJobQueueManage`,
      `PermUserRead`, `PermUserManage`,
      `PermRequestRead`, `PermRequestManage`, `PermRequestApprove`
- [ ] `Checker` struct with `pool *pgxpool.Pool` + `q db.Querier`
- [ ] `NewChecker(pool *pgxpool.Pool) *Checker`
- [ ] `IsOwner(ctx, userID string) (bool, error)`
- [ ] `HasPermission(ctx, userID, permission string) (bool, error)`
- [ ] `Require(permission string) func(http.Handler) http.Handler` — chi middleware:
      - 401 when no `userID` in context (token.Auth did not run)
      - 403 when authenticated but permission not held
      - 500 (fail closed) on transient DB error — never grants on error
      - Test hook: bypasses DB when `HasPermissionInContext` finds injected set

**`internal/platform/rbac/context.go`** (NEW):
- [ ] `InjectPermissionsForTest(ctx, perms ...string) context.Context`
- [ ] `HasPermissionInContext(ctx, permission string) (allowed, found bool)`

**`internal/platform/rbac/errors.go`** (NEW):
- [ ] `ErrForbidden`, `ErrUnauthenticated`, `ErrSystemRoleImmutable`,
      `ErrCannotReassignOwner`, `ErrCannotModifyOwnRole`, `ErrOwnerAlreadyExists`

**`internal/app/deps.go`** — Add `RBAC *rbac.Checker`
**`internal/server/server.go`** — `deps.RBAC = rbac.NewChecker(pool)` (one line, after pool init)

---

### §G-1 — Owner Bootstrap

New package: `internal/domain/owner/bootstrap/`

Route mounting: `r.Mount("/owner", rbacdomain.OwnerRoutes(ctx, deps))`

`POST /api/v1/owner/bootstrap`
- [ ] **Unauthenticated** — only unauthenticated write route in the system
- [ ] Body: `{ "user_id": "<uuid>" }`
- [ ] Guard 1: `CountActiveOwners` → 409 `owner_already_exists` if > 0
- [ ] Guard 2: `GetActiveUserByID` → 422 if unknown, not active, or not email-verified
- [ ] `GetOwnerRoleID` — look up the owner role from `001_roles.sql` seed
- [ ] `AssignUserRole` with `granted_by = user_id` (self-grant — acceptable only here,
      must be documented in code comment)
- [ ] Response: `{ "user_id", "role_name", "granted_at" }`
- [ ] Rate-limit: 3 req / 15 min per IP (key `bstrp:ip:`)
- [ ] Permanently returns 409 after the first successful bootstrap

---

### §G-2 — RBAC Admin API

New package: `internal/domain/admin/rbac/` with sub-packages:
`roles/`, `permissions/`, `userroles/`, `userpermissions/`

Route mounting: under `r.Mount("/admin", rbacdomain.AdminRoutes(ctx, deps))`

All routes: JWT required. Permission per route listed below.

**Roles** (`rbac:read` to read, `rbac:manage` to write):
- [ ] `GET    /admin/rbac/roles`                           — list all active roles
- [ ] `POST   /admin/rbac/roles`                          — create non-system role
- [ ] `GET    /admin/rbac/roles/:id`                      — get by ID
- [ ] `PATCH  /admin/rbac/roles/:id`                      — update name/description;
      zero rows from `UpdateRole` → 409 `system_role_immutable`
- [ ] `DELETE /admin/rbac/roles/:id`                      — soft-delete;
      zero rows from `DeactivateRole` → 409 `system_role_immutable`
- [ ] `GET    /admin/rbac/roles/:id/permissions`          — list role's permissions
- [ ] `POST   /admin/rbac/roles/:id/permissions`          — add permission to role
- [ ] `DELETE /admin/rbac/roles/:id/permissions/:perm_id` — remove permission from role

**Permissions** (`rbac:read`):
- [ ] `GET /admin/rbac/permissions`        — list all active permissions
- [ ] `GET /admin/rbac/permissions/groups` — list groups with members

**User role** (`rbac:read` to read, `rbac:manage` to write):
- [ ] `GET    /admin/rbac/users/:user_id/role` — get current role (no rows → 404)
- [ ] `PUT    /admin/rbac/users/:user_id/role` — assign or replace role;
      guard: 409 if target is owner (`ErrCannotReassignOwner`);
      guard: 409 if self-assignment (`ErrCannotModifyOwnRole`);
      DB trigger fires if re-assigning the last owner
- [ ] `DELETE /admin/rbac/users/:user_id/role` — remove role;
      DB trigger fires if this is the last owner

**User permissions** (`rbac:grant_user_permission`):
- [ ] `GET    /admin/rbac/users/:user_id/permissions`           — list active direct grants
- [ ] `POST   /admin/rbac/users/:user_id/permissions`           — grant direct permission;
      `expires_at` required; DB trigger enforces ≤ 90 days and blocks privilege escalation
- [ ] `DELETE /admin/rbac/users/:user_id/permissions/:grant_id` — revoke grant

---

## Group H — Job Queue

Full design: `docs/prompts/jobqueue/0-design.md`

### §H-0 — Job Queue Platform + Worker Handlers + Server Wiring

**`sql/schema/006_jobqueue.sql`** (NEW):
- [ ] Creates `job_paused_kinds`, `jobs`, `workers`, `job_schedules` tables with
      all indexes and constraints
- [ ] Drops `request_executions` (replaced by `kind="execute_request"` jobs)
- [ ] Removes delivery retry columns from `request_notifications`
      (`delivery_attempts`, `last_attempt_at`, `delivery_error`)

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

### §F-4 — Lock / Unlock (admin_locked field)

> **Doc TODO when implemented:** Update
> `mint/api-reference/auth/unlock/request-unlock.mdx` and
> `mint/api-reference/auth/unlock/confirm-unlock.mdx` — both reference this
> admin endpoint as "planned". Remove the qualifier and confirm behaviour is accurate.

`PATCH /api/v1/admin/users/{id}/lock`
- [ ] Sets `admin_locked = TRUE`
- [ ] Body: `{ "reason": "..." }`
- [ ] Immediately force-revokes all sessions and refresh tokens
- [ ] Cannot lock another owner (check target role via RBAC store)
- [ ] Audit row: `admin_lock_applied` on target; `admin_action` on acting admin

`PATCH /api/v1/admin/users/{id}/unlock`
- [ ] Clears `admin_locked = FALSE` (does NOT touch `is_locked` or
      `login_locked_until` — those belong to the user-facing OTP unlock flow)
- [ ] Body: `{ "reason": "..." }`
- [ ] Audit row: `admin_lock_removed`

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
- Rate-limit key prefixes must not reuse any prefix defined in `E2E_CHECKLIST.md`
- `deps.RBAC.Require("resource:action")` always chains after `deps.JWTAuth` in the
  middleware stack — never standalone
- `MetricsRecorder` swap path: change one field in `ManagerConfig.Metrics` in
  `server.New`; nothing else in the codebase needs to change
