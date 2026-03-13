# RBAC Design — v2

## Changelog

| Version | Change |
|---------|--------|
| v1 | Initial design |
| v2 | Added `access_type` (direct/conditional/request/denied) and `scope` (own/all) to `role_permissions` and `user_permissions`; split `job_queue:manage` into `job_queue:manage` + `job_queue:configure`; replaced `user:ban` with `user:lock` backed by existing `admin_locked` field; added `user:lock` metadata columns to `users`; added `permission_request_approvers` table; updated `CheckUserAccess` to return access_type + scope + conditions; updated middleware to act on access_type; permission count 10 → 13; updated all seeds, gates, and file maps |
| v3 | Schema (Phase 0) and queries (Phase 1) complete. `CheckUserAccess` rewritten to single-pass CTE with `MATERIALIZED` hints; `fn_prevent_owner_role_escalation` collapsed to single query; `idx_roles_owner_active` partial index added; `idx_user_roles_lookup` removed (redundant with PK); `idx_ur_audit_change_type` added; colon-guard `CHECK` constraints added to `permissions`; `Issue 4` (access_type dead column) retracted — column is correct and intentional. Three operational TODOs carried forward to Phase 1 gate. |
| v4 (cleanup) | Stripped §2 (schema), §5 (SQL queries), §6 (seeds), §7 platform + bootstrap + roles + permissions type contracts, §9 (middleware), §10 (bootstrap flow) — all implemented through Phase 8. Remaining sections cover only what is still to be built (Phases 9–10). |

---

## 1. Implementation Status

| Phase | What | Status |
|-------|------|--------|
| 0 | Schema additions (`001_core.sql`, `003_rbac.sql`, `004_rbac_functions.sql`) | ✅ Done |
| 1 | `sql/queries/rbac.sql` + `sqlc generate` | ✅ Done |
| 2 | `sql/seeds/002_permissions.sql` + `003_roles.sql` | ✅ Done |
| 3 | `internal/platform/rbac/` — checker + middleware | ✅ Done |
| 4 | Bootstrap route (`/owner/bootstrap`) | ✅ Done |
| 5 | Permissions read API (routes 10–11) | ✅ Done |
| 6 | Roles API (routes 2–9) | ✅ Done |
| 7 | Audit fixes + re-grant idempotency | ✅ Done |
| 8 | Permission capability flags (`scope_policy`, `allow_conditional`, `allow_request`) | ✅ Done |
| 9 | User role management (routes 12–14) | ⬜ Todo |
| 10 | User permission management (routes 15–17) | ⬜ Todo |
| 11 | User lock management (routes 18–20) | ⬜ Todo |
| 12 | Wire into server; update login/oauth/unlock/token middleware | ⬜ Todo |

---

## 2. The Access Model

### Four access types

| access_type | What happens | Who decides |
|---|---|---|
| `direct` | Granted, no friction | Role assignment |
| `conditional` | Granted, conditions JSONB enforced by app layer | Role assignment + conditions |
| `request` | Must submit approval request first, 202 returned | Role assignment + approvers table |
| `denied` | Explicitly blocked, 403 returned | Role assignment |

### Two scopes

| scope | What it means |
|---|---|
| `own` | Can only act on resources the user created or owns |
| `all` | Can act on any resource of this type |

### Three-layer check model

```
Request arrives with Bearer token
        │
        ▼
token.Auth middleware          ← already exists, injects userID into context
        │
        ▼
rbac.Require("resource:action") ← reads userID from context
        │
        ├─ is owner role? ──────────────────────── YES → pass (unrestricted)
        │
        └─ CheckUserAccess query returns row
               │
               ├─ access_type = 'direct'      → pass, inject scope into context
               ├─ access_type = 'conditional' → pass, inject scope + conditions into context
               ├─ access_type = 'request'     → 202, {"code": "approval_required"}
               ├─ access_type = 'denied'      → 403
               └─ not found                   → 403
```

---

## 3. Package Structure

```
internal/platform/rbac/           ✅ done
    checker.go
    context.go
    errors.go

internal/domain/rbac/
    routes.go                     ✅ done (phases 9–11 will add mounts here)
    bootstrap/                    ✅ done
    roles/                        ✅ done
    permissions/                  ✅ done
    userroles/                    ⬜ Phase 9
    userpermissions/              ⬜ Phase 10
    userlock/                     ⬜ Phase 11
```

---

## 4. REST API — Remaining Routes

| # | Method | Path | Auth | Permission | Description |
|---|--------|------|------|------------|-------------|
| 12 | GET | `/api/v1/admin/rbac/users/:user_id/role` | JWT | `rbac:read` | Get user's current role |
| 13 | PUT | `/api/v1/admin/rbac/users/:user_id/role` | JWT | `rbac:manage` | Assign or replace user's role |
| 14 | DELETE | `/api/v1/admin/rbac/users/:user_id/role` | JWT | `rbac:manage` | Remove user's role |
| 15 | GET | `/api/v1/admin/rbac/users/:user_id/permissions` | JWT | `rbac:read` | List active direct grants |
| 16 | POST | `/api/v1/admin/rbac/users/:user_id/permissions` | JWT | `rbac:grant_user_permission` | Grant direct permission to user |
| 17 | DELETE | `/api/v1/admin/rbac/users/:user_id/permissions/:grant_id` | JWT | `rbac:grant_user_permission` | Revoke direct permission grant |
| 18 | POST | `/api/v1/admin/users/:user_id/lock` | JWT | `user:lock` | Admin-lock a user account |
| 19 | DELETE | `/api/v1/admin/users/:user_id/lock` | JWT | `user:lock` | Admin-unlock a user account |
| 20 | GET | `/api/v1/admin/users/:user_id/lock` | JWT | `user:read` | Get user lock status |

**Route 13 (assign role) guards:**
- Cannot reassign owner users → `ErrCannotReassignOwner` → 409
- Cannot assign to self → `ErrCannotModifyOwnRole` → 409
- `fn_prevent_orphaned_owner` fires at DB level if re-assigning the last owner

**Routes 18/19 (lock/unlock) guards:**
- Cannot lock owner accounts → `ErrCannotLockOwner` → 409
- Cannot lock self → `ErrCannotLockSelf` → 409
- `user:lock` for admin is seeded as `access_type = 'request'` → admin gets 202 with an approval request; owner approves it directly.

**Unlock flow guard (existing `domain/auth/unlock`):**
- If `admin_locked = TRUE`, the user-facing OTP unlock flow returns `ErrAdminLocked`
  and refuses to clear `admin_locked`. Only routes 18/19 touch that field.

---

## 5. Type Contracts — Remaining Packages

### `userroles/models.go`

```go
type UserRole struct {
    UserID      string
    RoleID      string
    RoleName    string
    IsOwnerRole bool
    GrantedBy   string
    GrantedAt   time.Time
    ExpiresAt   *time.Time
}
type AssignRoleInput struct {
    RoleID        string
    GrantedBy     string
    GrantedReason string
    ExpiresAt     *time.Time
}
```

### `userpermissions/models.go`

```go
type UserPermission struct {
    ID            string
    CanonicalName string
    ResourceType  string
    Scope         string
    Conditions    json.RawMessage
    ExpiresAt     time.Time
    GrantedAt     time.Time
    GrantedReason string
}
type GrantPermissionInput struct {
    PermissionID  string
    GrantedBy     string
    GrantedReason string
    Scope         string
    ExpiresAt     time.Time
    Conditions    json.RawMessage
}
```

### `userlock/models.go`

```go
type LockUserInput struct {
    Reason string `json:"reason"`
}
type UserLockStatus struct {
    UserID       string     `json:"user_id"`
    AdminLocked  bool       `json:"admin_locked"`
    LockedBy     *string    `json:"locked_by,omitempty"`
    LockedReason *string    `json:"locked_reason,omitempty"`
    LockedAt     *time.Time `json:"locked_at,omitempty"`
    IsLocked     bool       `json:"is_locked"`
}
```

---

## 6. Decisions

| ID | Decision | Rationale |
|----|----------|-----------|
| D-R1 | Platform package for checker, domain package for admin API | Checker is used by every domain — belongs in `platform/`. Admin API is a domain concern. |
| D-R2 | `CheckUserAccess` returns access_type + scope + conditions, not just booleans | Middleware needs to act on access_type; handlers need scope and conditions. One round-trip still. |
| D-R3 | No caching in V1 | Query is fast and fully indexed. Premature caching adds invalidation complexity before there is a measured problem. |
| D-R4 | `POST /owner/bootstrap` is unauthenticated | Chicken-and-egg: can't authenticate to get owner permissions if no owner exists. Rate-limited + 409 guard makes it safe. |
| D-R5 | System roles are immutable via API | DB WHERE `is_system_role = FALSE` guard enforces this. |
| D-R6 | Owner role reassignment out of scope for V1 | High-risk, needs own flow (confirmation, two-owner window). |
| D-R7 | `granted_by` RESTRICT FK on all grants | Every grant has a named human accountable. You cannot delete a user who has granted permissions. |
| D-R8 | Role deletion = soft-delete | RESTRICT FKs on audit tables block hard DELETE. Soft-delete preserves audit trail. |
| D-R9 | Context injection for tests (`InjectPermissionsForTest`) | Same pattern as `token.InjectUserIDForTest`. Handler tests bypass DB. |
| D-R10 | Permission constants in `internal/platform/rbac/` | Raw string literals are a typo risk. Constants are compile-checked. |
| D-R11 | Fail closed on DB error | Return 500, never grant on uncertainty. |
| D-R12 | `access_type = 'request'` reuses `005_requests.sql` workflow | No duplicate approval machinery. |
| D-R13 | `job_queue:manage` vs `job_queue:configure` split | manage = job-level ops (low blast radius). configure = system-level ops (high blast radius). |
| D-R14 | `user:lock` over separate `user:ban` | `admin_locked` already exists. Adding metadata columns gives everything a ban would without a redundant field. |
| D-R15 | `user:lock` for admin seeded as `access_type = 'request'` | Locking a user is a policy decision — admin initiates, owner approves. |
| D-R16 | `scope` on `user_permissions` (direct grants) | Even a direct grant should specify resource visibility. Defaults to 'own'. |

---

## 7. Tests — Remaining

### User role management (routes 12–14)

| # | Case | Layer |
|---|------|-------|
| T-R34 | `PUT /users/:id/role` assigns role; `GET` returns it | I |
| T-R35 | `PUT /users/:id/role` replaces an existing role | I |
| T-R36 | `PUT /users/:id/role` returns 409 for owner target user | I |
| T-R37 | `PUT /users/:id/role` returns 409 for self-assignment | I |
| T-R38 | `DELETE /users/:id/role` removes role | I |

### User permissions management (routes 15–17)

| # | Case | Layer |
|---|------|-------|
| T-R39 | `POST /users/:id/permissions` grants direct permission with scope | I |
| T-R40 | `GET /users/:id/permissions` returns only active grants with scope | I |
| T-R41 | `DELETE /users/:id/permissions/:grant_id` revokes grant | I |
| T-R42 | Expired grant does not appear in `GET` and does not pass `Require` | I |
| T-R43 | Grant with `expires_at > 90 days` returns 422 (DB trigger fires) | I |
| T-R44 | Granter without the permission cannot grant it (privilege escalation trigger) | I |

### User lock management (routes 18–20)

| # | Case | Layer |
|---|------|-------|
| T-R45 | `POST /users/:id/lock` admin-locks user; `GET` reflects locked state | I |
| T-R46 | `DELETE /users/:id/lock` admin-unlocks user; `GET` reflects unlocked state | I |
| T-R47 | Lock returns 409 when target is owner | I |
| T-R48 | Lock returns 409 when target is self | I |
| T-R49 | Admin (request access_type) gets 202 on lock attempt, not direct lock | I |
| T-R50 | `auth/unlock` OTP flow refuses to clear `admin_locked` | I |
| T-R51 | Admin-locked user cannot log in (login service guard) | I |
| T-R52 | Admin-locked user's existing tokens are invalidated via Redis key | I |

---

## 8. File Map — Remaining

```
-- Phase 9: user role management
internal/domain/rbac/userroles/
    handler.go, service.go, store.go, models.go, routes.go,
    validators.go, errors.go, handler_test.go, service_test.go,
    store_test.go, export_test.go                              NEW
internal/domain/rbac/routes.go                                 MODIFY — mount userroles.Routes

-- Phase 10: user permission management
internal/domain/rbac/userpermissions/
    handler.go, service.go, store.go, models.go, routes.go,
    validators.go, errors.go, handler_test.go, service_test.go,
    store_test.go, export_test.go                              NEW
internal/domain/rbac/routes.go                                 MODIFY — mount userpermissions.Routes

-- Phase 11: user lock management
internal/domain/rbac/userlock/
    handler.go, service.go, store.go, models.go, routes.go,
    validators.go, errors.go, handler_test.go, service_test.go,
    store_test.go, export_test.go                              NEW
internal/domain/rbac/routes.go                                 MODIFY — mount userlock.Routes

-- Phase 12: wire into server + modify existing files
internal/domain/auth/login/service.go                          MODIFY — add admin_locked guard in step 6
internal/domain/auth/unlock/service.go                         MODIFY — refuse to clear admin_locked
internal/domain/auth/shared/errors.go                          MODIFY — add ErrAdminLocked sentinel
internal/domain/oauth/google/service.go                        MODIFY — add admin_locked guard
internal/domain/oauth/telegram/service.go                      MODIFY — add admin_locked guard
internal/platform/token/middleware.go                          MODIFY — add step 3c: Redis "admin_locked_user:<uid>" check
internal/app/deps.go                                           MODIFY — add RBAC *rbac.Checker (already added?)
internal/server/server.go                                      MODIFY — construct Checker, add to deps
internal/server/routes.go                                      MODIFY — mount /owner and /admin/rbac + /admin/users
```

---

## 9. Wiring (Phase 12)

### `internal/app/deps.go`

```go
RBAC *rbac.Checker  // use deps.RBAC.Require("resource:action")
```

### `internal/server/server.go`

```go
deps.RBAC = rbac.NewChecker(pool)
```

### `internal/server/routes.go`

```go
r.Route("/api/v1", func(r chi.Router) {
    r.Mount("/auth",    auth.Routes(ctx, deps))
    r.Mount("/oauth",   oauth.Routes(ctx, deps))
    r.Mount("/profile", profile.Routes(ctx, deps))
    r.Mount("/owner",   rbacdomain.OwnerRoutes(ctx, deps))
    r.Mount("/admin",   rbacdomain.AdminRoutes(ctx, deps))
})
```

---

## 10. Operational TODOs (required before go-live)

### TODO-1 · Expired grant cleanup job ⚠️ Reliability blocker

**Implemented in the job queue design** — see `internal/worker/purge_expired_permissions.go`
and the `KindPurgeExpiredPermissions` schedule entry in `jobqueue/0-design.md §7`.

An expired `user_permissions` row that has not been deleted will cause
`GrantUserPermission` to return `23505 → ErrPermissionAlreadyGranted → 409`.
The cleanup job runs every 5 minutes via the job queue scheduler.

### TODO-2 · Re-grant idempotency in `GrantUserPermission` service

On receipt of `23505` from `GrantUserPermission`, the service should:
1. Attempt `DELETE FROM user_permissions WHERE user_id = $1 AND permission_id = $2 AND expires_at <= NOW()`
2. If 1 row deleted → retry the insert.
3. If 0 rows deleted (grant is still active) → surface `ErrPermissionAlreadyGranted → 409`.

Implement in `userpermissions/service.go` (Phase 10).

### TODO-3 · Static catalog caching (post-launch, not a blocker)

`GetRoles`, `GetPermissions`, `GetPermissionGroups`, `GetOwnerRoleID` — all return
data that changes rarely. Add a 5-minute in-process TTL cache after launch when
DB query latency appears in profiling.

### TODO-4 · Condition template enforcement ⚠️ Pre-conditional-grants blocker

`permission_condition_templates` is never read at runtime. Must be resolved before
any permission has `allow_conditional = TRUE` in production. See details in
`docs/prompts/rbac/DOCS-TODOS.md §TODO-A`.
