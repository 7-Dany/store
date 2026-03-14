# RBAC Design — v2

## Changelog

| Version | Change |
|---------|--------|
| v1 | Initial design |
| v2 | Added `access_type` (direct/conditional/request/denied) and `scope` (own/all) to `role_permissions` and `user_permissions`; split `job_queue:manage` into `job_queue:manage` + `job_queue:configure`; replaced `user:ban` with `user:lock` backed by existing `admin_locked` field; added `user:lock` metadata columns to `users`; added `permission_request_approvers` table; updated `CheckUserAccess` to return access_type + scope + conditions; updated middleware to act on access_type; permission count 10 → 13; updated all seeds, gates, and file maps |
| v3 | Schema (Phase 0) and queries (Phase 1) complete. `CheckUserAccess` rewritten to single-pass CTE with `MATERIALIZED` hints; `fn_prevent_owner_role_escalation` collapsed to single query; `idx_roles_owner_active` partial index added; `idx_user_roles_lookup` removed (redundant with PK); `idx_ur_audit_change_type` added; colon-guard `CHECK` constraints added to `permissions`; `Issue 4` (access_type dead column) retracted — column is correct and intentional. Three operational TODOs carried forward to Phase 1 gate. |
| v4 (cleanup) | Stripped §2 (schema), §5 (SQL queries), §6 (seeds), §7 platform + bootstrap + roles + permissions type contracts, §9 (middleware), §10 (bootstrap flow) — all implemented through Phase 8. Remaining sections cover only what is still to be built (Phases 9–10). |
| v5 | Phase 11 complete: `internal/domain/rbac/userlock/` fully implemented (handler, service, store, models, routes, validators, errors, requests, handler_test, service_test, store_test, validators_test, export_test). Shared testutil already had `UserLockFakeStorer`, `UserLockFakeServicer`, and QuerierProxy flags added. `routes.go` already mounts `userlock.Routes`. Only Phase 12 (server wiring + login/oauth/unlock guards) remains. |
| v6 | Phase 12 complete: `authshared.ErrAdminLocked` sentinel added; `login.LoginUser.AdminLocked` field split from `IsLocked`; login service guard order updated (admin_locked before is_locked); login handler maps `ErrAdminLocked` → 423 `admin_locked`; `userlock.Service` wired with `kvStore` — sets `admin_lock:<uid>` on lock, deletes on unlock; `token.Auth` middleware step 3c checks `admin_lock:` key and rejects with 401; `userlock.NewService` nil-safe for unit tests; service_test.go updated to pass `nil` KVStore. Server + routes were already wired in Phase 11. |
| v7 (cleanup) | All 12 phases complete. Stripped §3 stale todo markers, §8 file map (all files exist), §9 wiring pseudocode (live in server/routes.go). Retitled §4/5/7 away from "Remaining". |

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
| 9 | User role management (routes 12–14) | ✅ Done |
| 10 | User permission management (routes 15–17) | ✅ Done |
| 11 | User lock management (routes 18–20) | ✅ Done |
| 12 | Wire into server; update login/oauth/unlock/token middleware | ✅ Done |

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
internal/platform/rbac/
    checker.go
    context.go
    errors.go

internal/domain/rbac/
    routes.go
    bootstrap/
    roles/
    permissions/
    userroles/
    userpermissions/
    userlock/
    shared/
    shared/testutil/
```

---

## 4. REST API — User Lock Routes

| # | Method | Path | Auth | Permission | Description |
|---|--------|------|------|------------|-------------|
| 18 | POST | `/api/v1/admin/users/:user_id/lock` | JWT | `user:lock` | Admin-lock a user account |
| 19 | DELETE | `/api/v1/admin/users/:user_id/lock` | JWT | `user:lock` | Admin-unlock a user account |
| 20 | GET | `/api/v1/admin/users/:user_id/lock` | JWT | `user:read` | Get user lock status |

**Routes 18/19 (lock/unlock) guards:**
- Cannot lock owner accounts → `ErrCannotLockOwner` → 409
- Cannot lock self → `ErrCannotLockSelf` → 409
- `user:lock` for admin is seeded as `access_type = 'request'` → admin gets 202 with an approval request; owner approves it directly.

**Unlock flow guard (`domain/auth/unlock`):**
- If `admin_locked = TRUE`, the user-facing OTP unlock flow silently suppresses the request.
  Only routes 18/19 touch that field.

**Token invalidation:**
- `LockUser` writes `admin_lock:<uid>` to KV (no TTL). `token.Auth` step 3c rejects 401 when key is present.
- `UnlockUser` deletes the key. Both KV operations are best-effort (logged on failure, do not abort the DB write).

**DB constraints that fire during lock:**
- `chk_us_admin_lock_coherent` — reason + at must be non-NULL when `admin_locked = TRUE`; all three must be NULL when `admin_locked = FALSE`
- `chk_us_no_self_lock` — `admin_locked_by != user_id` (DB backstop; service checks first)

**Note on lock fields location:** `admin_locked`, `admin_locked_by`, `admin_locked_reason`, `admin_locked_at`, `is_locked`, `login_locked_until` all live in `user_secrets`, not `users`. Every lock query JOINs or targets `user_secrets` directly.

---

## 5. Type Contracts — `userlock` Package

### `userlock/models.go`

```go
type LockUserInput struct {
    Reason string `json:"reason"`
}
type UserLockStatus struct {
    UserID           string     `json:"user_id"`
    AdminLocked      bool       `json:"admin_locked"`
    LockedBy         *string    `json:"locked_by,omitempty"`
    LockedReason     *string    `json:"locked_reason,omitempty"`
    LockedAt         *time.Time `json:"locked_at,omitempty"`
    IsLocked         bool       `json:"is_locked"`          // OTP lock (separate from admin lock)
    LoginLockedUntil *time.Time `json:"login_locked_until,omitempty"`
}

// LockUserTxInput is the store-layer input with parsed [16]byte IDs.
type LockUserTxInput struct {
    UserID   [16]byte
    LockedBy [16]byte
    Reason   string
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
| D-R17 | Lock data in `user_secrets`, not `users` | Security-sensitive admin-lock fields live in `user_secrets`. Every lock query JOINs or targets `user_secrets` directly. |
| D-R18 | Owner-check via `GetUserRole` before lock | Owner is defined by having a role with `is_owner_role = TRUE`. Checking the role is the canonical source of truth. Returns false (non-owner) when user has no role. |
| D-R19 | `GetUserLockStatus` as existence guard | Called before Lock/Unlock to verify the user exists and is not deleted. Returns `ErrUserNotFound` on no rows. |
| D-R20 | `WithActingUser` for both LockUser and UnlockUser | DB audit triggers record the acting admin for both actions. |

---

## 7. Tests — User Lock (routes 18–20)

| # | Case | Layer |
|---|------|-------|
| T-R45 | `POST /users/:id/lock` admin-locks user; `GET` reflects locked state | I |
| T-R46 | `DELETE /users/:id/lock` admin-unlocks user; `GET` reflects unlocked state | I |
| T-R47 | Lock returns 409 when target is owner | I |
| T-R48 | Lock returns 409 when target is self | I |
| T-R49 | Admin (request access_type) gets 202 on lock attempt, not direct lock | I |
| T-R50 | `auth/unlock` OTP flow silently suppresses when `admin_locked = TRUE` | I |
| T-R51 | Admin-locked user cannot log in — 423 `admin_locked` (login service guard) | I |
| T-R52 | Admin-locked user's existing tokens rejected via `admin_lock:` KV key | I |

---

## 8. Operational TODOs (required before go-live)

### TODO-1 · Expired grant cleanup job ⚠️ Reliability blocker

**Implemented in the job queue design** — see `internal/worker/purge_expired_permissions.go`
and the `KindPurgeExpiredPermissions` schedule entry in `jobqueue/0-design.md §7`.

An expired `user_permissions` row that has not been deleted will cause
`GrantUserPermission` to return `23505 → ErrPermissionAlreadyGranted → 409`.
The cleanup job runs every 5 minutes via the job queue scheduler.

### TODO-2 · Re-grant idempotency in `GrantUserPermission` service

V1 decision: always return `ErrPermissionAlreadyGranted → 409` on duplicate.
Callers must revoke the existing grant before re-granting.
Implemented in `userpermissions/store.go handleDuplicateGrant`.

### TODO-3 · Static catalog caching (post-launch, not a blocker)

`GetRoles`, `GetPermissions`, `GetPermissionGroups`, `GetOwnerRoleID` — all return
data that changes rarely. Add a 5-minute in-process TTL cache after launch when
DB query latency appears in profiling.

### TODO-4 · Condition template enforcement ⚠️ Pre-conditional-grants blocker

`permission_condition_templates` is never read at runtime. Must be resolved before
any permission has `allow_conditional = TRUE` in production. See details in
`docs/prompts/rbac/DOCS-TODOS.md §TODO-A`.
