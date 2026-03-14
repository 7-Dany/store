# RBAC — Resolved Context

**Design doc:** `docs/prompts/rbac/0-design.md`
**Last completed phase:** Phase 12 (server wiring + login/oauth/unlock guards)
**Next phase:** RBAC implementation complete — all 12 phases done ✅

---

## Package structure (multi-package feature)

| Phase | Package | Status |
|-------|---------|--------|
| 0 | Schema additions (`001_core.sql`, `003_rbac.sql`, `004_rbac_functions.sql`) | ✅ Done |
| 1 | `sql/queries/rbac.sql` + `sqlc generate` | ✅ Done |
| 2 | `sql/seeds/002_permissions.sql` + `003_roles.sql` | ✅ Done |
| 3 | `internal/platform/rbac/` — Checker, middleware, context, errors | ✅ Done |
| 4 | `internal/domain/rbac/bootstrap/` | ✅ Done |
| 5 | `internal/domain/rbac/permissions/` | ✅ Done |
| 6 | `internal/domain/rbac/roles/` | ✅ Done |
| 7 | Audit fixes + re-grant idempotency | ✅ Done |
| 8 | Permission capability flags (`scope_policy`, `allow_conditional`, `allow_request`) | ✅ Done |
| 9 | `internal/domain/rbac/userroles/` | ✅ Done |
| 10 | `internal/domain/rbac/userpermissions/` | ✅ Done |
| 11 | `internal/domain/rbac/userlock/` | ✅ Done |
| 12 | Wire: `app/deps.go` + `server/server.go` + `server/routes.go` + modify login/oauth/unlock | ✅ Done |

## Resolved paths

- SQL queries: `sql/queries/rbac.sql`
- Seeds: `sql/seeds/002_permissions.sql`, `sql/seeds/003_roles.sql`
- Generated DB: `internal/db/` — includes all rbac query methods
- Platform checker: `internal/platform/rbac/checker.go`
- Platform context helpers: `internal/platform/rbac/context.go`
- Platform errors: `internal/platform/rbac/errors.go`
- Domain assembler: `internal/domain/rbac/routes.go`
- Bootstrap: `internal/domain/rbac/bootstrap/` ✅
- Permissions: `internal/domain/rbac/permissions/` ✅
- Roles: `internal/domain/rbac/roles/` ✅
- User roles: `internal/domain/rbac/userroles/` ✅
- User permissions: `internal/domain/rbac/userpermissions/` ✅
- User lock: `internal/domain/rbac/userlock/` ← Phase 11
- Shared helpers: `internal/domain/rbac/shared/` (BaseStore, validators, errors)
- Shared test helpers: `internal/domain/rbac/shared/testutil/` (fake_storer.go, fake_servicer.go, builders.go, querier_proxy.go)
- Deps addition: `internal/app/deps.go` — `RBAC *rbac.Checker`
- Server wiring: `internal/server/server.go`, `internal/server/routes.go` ← Phase 12

## Key decisions (from 0-design.md §6)

- D-R1: Checker in `platform/rbac/`; admin API in `domain/rbac/`
- D-R2: Single `CheckUserAccess` query — one DB round-trip per guarded request
- D-R3: No caching in V1
- D-R4: `POST /owner/bootstrap` is unauthenticated (chicken-and-egg)
- D-R5: System roles are immutable via API (`is_system_role = FALSE` in WHERE)
- D-R6: Owner role reassignment out of scope for V1
- D-R7: `granted_by` FK on `users.id` RESTRICT — every grant has a named actor
- D-R8: Role deletion = soft-delete (`is_active = FALSE`)
- D-R9: `InjectPermissionsForTest` context helper for handler tests
- D-R10: Permission constants in `internal/platform/rbac/` — no raw string literals
- D-R11: Fail closed on DB error in `Require` (500, never grant)
- D-R12: `access_type = 'request'` reuses `005_requests.sql` workflow
- D-R15: `user:lock` for admin seeded as `access_type = 'request'`
- D-R17: Lock data lives in `user_secrets`, not `users`
- D-R18: Owner-check via `GetUserRole` (IsOwnerRole field) before lock
- D-R19: `GetUserLockStatus` as existence guard before Lock/Unlock
- D-R20: `WithActingUser` for both LockUser and UnlockUser (audit triggers)

## SQL queries in `sql/queries/rbac.sql`

CheckUserAccess, CountActiveOwners, GetOwnerRoleID, GetActiveUserByID,
AssignUserRole, RemoveUserRole, GetRoles, GetRoleByID, GetRoleByName,
CreateRole, UpdateRole, DeactivateRole, GetRolePermissions, AddRolePermission,
RemoveRolePermission, GetPermissions, GetPermissionByID, GetPermissionByCanonicalName,
GetPermissionGroups, GetPermissionGroupMembers, GetUserRole,
GetUserPermissions, GrantUserPermission, RevokeUserPermission,
LockUser, UnlockUser, GetUserLockStatus, SetActingUser

## Generated DB types relevant to Phase 11

```go
// LockUser(ctx, LockUserParams) error
type LockUserParams struct {
    LockedBy pgtype.UUID `db:"locked_by"`
    Reason   pgtype.Text `db:"reason"`       // pgtype.Text — nullable wrapper
    UserID   pgtype.UUID `db:"user_id"`
}

// UnlockUser(ctx, userID pgtype.UUID) error  — no Params struct

// GetUserLockStatus(ctx, userID pgtype.UUID) (GetUserLockStatusRow, error)
type GetUserLockStatusRow struct {
    ID                uuid.UUID          `db:"id"`
    AdminLocked       bool               `db:"admin_locked"`
    AdminLockedBy     pgtype.UUID        `db:"admin_locked_by"`
    AdminLockedReason pgtype.Text        `db:"admin_locked_reason"`
    AdminLockedAt     pgtype.Timestamptz `db:"admin_locked_at"`
    IsLocked          bool               `db:"is_locked"`           // OTP lock
    LoginLockedUntil  pgtype.Timestamptz `db:"login_locked_until"`
}
// Returns pgx.ErrNoRows when user_id not found or deleted_at IS NOT NULL.
```

## DB constraints relevant to Phase 11

- `chk_us_admin_lock_coherent` — reason + at non-NULL when `admin_locked = TRUE`; all NULL when FALSE
- `chk_us_no_self_lock` — `admin_locked_by != user_id` (DB backstop; service guard fires first)

## Route mount point

Routes 18–20 mount under `/admin` (not `/admin/rbac`):
```
POST   /admin/users/{user_id}/lock   → handler.LockUser
DELETE /admin/users/{user_id}/lock   → handler.UnlockUser
GET    /admin/users/{user_id}/lock   → handler.GetLockStatus
```
`userlock.Routes(ctx, r, deps)` registers `r.Post("/users/{user_id}/lock", ...)` etc.

## Capability flags (Phase 8) — on `permissions` table

Three new columns: `scope_policy` (enum: none/own/all/any), `allow_conditional` (bool), `allow_request` (bool).

## Audit events

None — RBAC mutations are audited via DB-level triggers. No `internal/audit/audit.go` constants needed.

## Sentinel errors

### `internal/platform/rbac/errors.go`
`ErrForbidden`, `ErrUnauthenticated`, `ErrApprovalRequired`,
`ErrSystemRoleImmutable`, `ErrCannotReassignOwner`, `ErrCannotModifyOwnRole`,
`ErrOwnerAlreadyExists`, `ErrCannotLockOwner`, `ErrCannotLockSelf`

### Per-package sentinels (domain layer)
- `bootstrap`: `ErrOwnerAlreadyExists`, `ErrUserNotActive`, `ErrEmailNotVerified`
- `roles`: `ErrRoleNotFound`, `ErrPermissionNotFound`, `ErrGrantAlreadyExists`, `ErrRoleNameConflict`, `ErrAccessTypeNotAllowed`, `ErrScopeNotAllowed`
- `userroles`: `ErrUserRoleNotFound`, `ErrRoleNotFound`, `ErrLastOwnerRemoval`
- `userpermissions`: `ErrPermissionNotFound`, `ErrGrantNotFound`, `ErrPermissionAlreadyGranted`, `ErrPrivilegeEscalation`
- `userlock` (Phase 11): `ErrUserNotFound`, `ErrReasonRequired`, plus platform-level `ErrCannotLockOwner`, `ErrCannotLockSelf`

## Rate-limit prefixes

- `bstrp:ip:` — POST /owner/bootstrap (3 req / 15 min per IP)
- No rate limiters on admin routes (JWT-auth + RBAC guarded)

## Permission constants (internal/platform/rbac/checker.go)

`PermRBACRead`, `PermRBACManage`, `PermRBACGrantUserPerm`,
`PermJobQueueRead`, `PermJobQueueManage`, `PermJobQueueConfigure`,
`PermUserRead`, `PermUserManage`, `PermUserLock`,
`PermRequestRead`, `PermRequestManage`, `PermRequestApprove`,
`PermProductManage`

## Test case IDs

- Platform checker (U/I): T-R01 to T-R17
- Bootstrap:             T-R18 to T-R22
- Roles API:             T-R23 to T-R31 + T-R31e
- Permissions API:       T-R28 to T-R29 + T-R28b + T-R29b
- Capability flags:      T-R32 to T-R39 (in roles/ and permissions/)
- User role mgmt:        T-R34 to T-R38d  ← Phase 9
- User perm mgmt:        T-R39 to T-R44   ← Phase 10
- User lock mgmt:        T-R45 to T-R52   ← Phase 11

## DB trigger escape hatches (test use only)

- `SET LOCAL rbac.skip_escalation_check = '1'` — bypasses `fn_prevent_privilege_escalation` + `fn_prevent_owner_role_escalation`
- `SET LOCAL rbac.skip_orphan_check = '1'` — bypasses `fn_prevent_orphaned_owner`
- Must never appear in production code paths

## `shared/testutil` — current state

| File | Contents |
|---|---|
| `fake_storer.go` | `BootstrapFakeStorer`, `PermissionsFakeStorer`, `RolesFakeStorer`, `UserRolesFakeStorer`, `UserPermissionsFakeStorer` |
| `fake_servicer.go` | `BootstrapFakeServicer`, `PermissionsFakeServicer`, `RolesFakeServicer`, `UserRolesFakeServicer`, `UserPermissionsFakeServicer` |
| `querier_proxy.go` | `QuerierProxy` — wraps `db.Querier`, `Fail*` fields for integration tests. Has flags up through `FailGetPermissionByID`, `FailGetUserPermissions`, `FailGrantUserPermission`, `FailRevokeUserPermission` |
| `builders.go` | `MustUUID`, `RandomUUID`, `ShortID`, `NewEmail`, `MustHashPassword`, `MustNewTestPool`, `RunTestMain`, `MustBeginTx` |

**Phase 11 adds:** `UserLockFakeStorer`, `UserLockFakeServicer` to fake files; `FailLockUser`, `FailUnlockUser`, `FailGetUserLockStatus` flags + method overrides to querier_proxy.

## Phase 10 (userpermissions) — key patterns established

The `userpermissions` package is the closest analogue to `userlock`. Key patterns:
- `Store` embeds `rbacshared.BaseStore` for `ToPgtypeUUID`, `IsNoRows`, `IsUniqueViolation`, `WithActingUser`
- `Store.WithQuerier(q db.Querier) *Store` for integration test tx-binding
- `handler.mustUserID(w, r)` extracts acting userID from JWT context
- Handler writes errors in one `switch` block — no scattered `if err` chains
- Service parses all UUIDs before any DB calls
- `context.WithoutCancel(ctx)` used in store for irreversible operations (revoke/delete)
