# RBAC — Phase 9: User Role Management

**Feature:** RBAC
**Phase:** 9 of 10
**Depends on:** Phases 0–8 (schema ✅, queries ✅, seeds ✅, platform/rbac ✅, bootstrap ✅, permissions ✅, roles ✅, audit-fixes ✅, capability-flags ✅)
**Gate:** `go test -tags integration_test ./internal/domain/rbac/userroles/...` — T-R34 through T-R38 all green
**Design doc:** `docs/prompts/rbac/0-design.md`
**Go version:** 1.25

---

## What this phase builds

The `internal/domain/rbac/userroles/` package: three endpoints for reading,
assigning, and removing a user's role. This is a new package — no existing files
to modify except `internal/domain/rbac/routes.go` (one line to mount the routes)
and `shared/testutil/fake_storer.go` (add `UserRolesFakeStorer`).

Routes implemented:
- `GET  /admin/rbac/users/{user_id}/role`  — `rbac:read`
- `PUT  /admin/rbac/users/{user_id}/role`  — `rbac:manage`
- `DELETE /admin/rbac/users/{user_id}/role` — `rbac:manage`

---

## Read before writing any code

| File | Why |
|---|---|
| `docs/prompts/rbac/0-design.md` | §7 UserRole + AssignRoleInput models; §8 routes 12–14; §11 decisions D-R5/D-R6; §12 T-R34–T-R38 |
| `internal/domain/rbac/roles/routes.go` | Exact `Routes(ctx, r, deps)` pattern — copy it |
| `internal/domain/rbac/roles/handler.go` | Handler struct, method shape, error switch pattern |
| `internal/domain/rbac/roles/service.go` | Storer interface pattern, `parseID` helper |
| `internal/domain/rbac/roles/store.go` | `BaseStore` embedding, `WithActingUser` usage |
| `internal/domain/rbac/shared/store.go` | `BaseStore`, `WithActingUser`, `IsNoRows`, helper methods |
| `internal/domain/rbac/shared/testutil/fake_storer.go` | Existing `FakeStorer` layout — add `UserRolesFakeStorer` in same file |
| `internal/domain/rbac/routes.go` | Where to add `userroles.Routes(ctx, r, deps)` |
| `internal/platform/rbac/errors.go` | `ErrCannotReassignOwner`, `ErrCannotModifyOwnRole` — import these |
| `internal/platform/token/middleware.go` | `token.UserIDFromContext` — how handler reads calling user |

---

## Execution order

Work in this exact sequence. Each step must compile before the next.

```
1.  userroles/errors.go        — package sentinels
2.  userroles/models.go        — UserRole, AssignRoleInput, AssignRoleTxInput
3.  userroles/validators.go    — validateAssignRole
4.  userroles/requests.go      — HTTP request/response structs + mappers
5.  userroles/service.go       — Storer interface + Service methods
6.  userroles/store.go         — Store implementing Storer
7.  userroles/handler.go       — Handler with GetUserRole, AssignRole, RemoveRole
8.  userroles/routes.go        — Routes(ctx, r, deps)
9.  internal/domain/rbac/routes.go — add userroles.Routes(ctx, r, deps)
10. shared/testutil/fake_storer.go — UserRolesFakeStorer
11. userroles/service_test.go  — unit tests
12. userroles/handler_test.go  — unit tests
13. userroles/store_test.go    — integration tests
```

---

## Step 1 — `userroles/errors.go`

```go
package userroles

import "errors"

// ErrUserRoleNotFound is returned when GetUserRole finds no active assignment for the user.
var ErrUserRoleNotFound = errors.New("user has no active role assignment")

// ErrRoleNotFound is returned when AssignRole receives a role_id that does not
// correspond to an active role.
var ErrRoleNotFound = errors.New("role not found")

// ErrLastOwnerRemoval is returned when RemoveRole would leave the system with
// no active owner. Maps from the fn_prevent_orphaned_owner trigger
// (SQLSTATE 23000 — integrity_constraint_violation).
var ErrLastOwnerRemoval = errors.New("cannot remove the last active owner")
```

**Sentinel errors imported from `internal/platform/rbac` (do not redefine):**
- `rbac.ErrCannotReassignOwner` — "owner role cannot be reassigned via this route"
- `rbac.ErrCannotModifyOwnRole` — "you cannot modify your own role assignment"

Validation sentinels imported from `internal/domain/rbac/shared` (do not redefine):
- `rbacshared.ErrUserIDEmpty`

---

## Step 2 — `userroles/models.go`

```go
package userroles

import "time"

// UserRole is the service-layer representation of a user's active role assignment.
type UserRole struct {
    UserID      string
    RoleID      string
    RoleName    string
    IsOwnerRole bool
    GrantedBy   string
    GrantedAt   time.Time
    ExpiresAt   *time.Time
}

// AssignRoleInput is the service-layer input for PUT /users/:user_id/role.
// GrantedBy is the calling user's ID — set by the handler from the JWT context,
// never taken from the request body.
type AssignRoleInput struct {
    RoleID        string
    GrantedBy     string
    GrantedReason string
    ExpiresAt     *time.Time
}

// AssignRoleTxInput is the store-layer input for the upsert + re-read transaction.
// Carries parsed [16]byte IDs so the store does no string parsing.
type AssignRoleTxInput struct {
    UserID        [16]byte
    RoleID        [16]byte
    GrantedBy     [16]byte
    GrantedReason string
    ExpiresAt     *time.Time
}
```

---

## Step 3 — `userroles/validators.go`

```go
package userroles

import (
    "strings"
    rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
)

// ErrGrantedReasonEmpty is returned when granted_reason is blank after trimming.
var ErrGrantedReasonEmpty = errors.New("granted_reason is required")

// ErrRoleIDEmpty is returned when role_id is blank after trimming.
var ErrRoleIDEmpty = errors.New("role_id is required")

// validateAssignRole checks required fields on AssignRoleInput.
// Returns the first validation error encountered.
func validateAssignRole(in AssignRoleInput) error {
    if strings.TrimSpace(in.RoleID) == "" {
        return ErrRoleIDEmpty
    }
    if strings.TrimSpace(in.GrantedReason) == "" {
        return ErrGrantedReasonEmpty
    }
    return nil
}
```

---

## Step 4 — `userroles/requests.go`

```go
package userroles

import "time"

// ── HTTP request structs ──────────────────────────────────────────────────────

// assignRoleRequest is the JSON body for PUT /admin/rbac/users/{user_id}/role.
type assignRoleRequest struct {
    RoleID        string     `json:"role_id"`
    GrantedReason string     `json:"granted_reason"`
    ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

// ── HTTP response structs ─────────────────────────────────────────────────────

// userRoleResponse is the JSON shape returned for GET and PUT.
type userRoleResponse struct {
    UserID      string     `json:"user_id"`
    RoleID      string     `json:"role_id"`
    RoleName    string     `json:"role_name"`
    IsOwnerRole bool       `json:"is_owner_role"`
    GrantedAt   time.Time  `json:"granted_at"`
    ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// ── Mapper ────────────────────────────────────────────────────────────────────

func toUserRoleResponse(ur UserRole) userRoleResponse {
    return userRoleResponse{
        UserID:      ur.UserID,
        RoleID:      ur.RoleID,
        RoleName:    ur.RoleName,
        IsOwnerRole: ur.IsOwnerRole,
        GrantedAt:   ur.GrantedAt,
        ExpiresAt:   ur.ExpiresAt,
    }
}
```

---

## Step 5 — `userroles/service.go`

### `Storer` interface

```go
type Storer interface {
    // GetUserRole returns the user's current active role assignment.
    // Returns ErrUserRoleNotFound when no active assignment exists.
    GetUserRole(ctx context.Context, userID [16]byte) (UserRole, error)

    // AssignUserRole upserts the user's role and returns the full UserRole.
    // Returns ErrRoleNotFound when roleID does not correspond to an active role.
    AssignUserRole(ctx context.Context, in AssignRoleTxInput) (UserRole, error)

    // RemoveUserRole deletes the user's active role assignment.
    // actingUserID is written to rbac.acting_user so the audit trigger records
    // the correct deletion actor.
    // Returns ErrUserRoleNotFound when the user has no active assignment.
    // Returns ErrLastOwnerRemoval when fn_prevent_orphaned_owner fires (23000).
    RemoveUserRole(ctx context.Context, userID [16]byte, actingUserID string) error
}
```

### `Service` methods

```go
// GetUserRole returns the active role for targetUserID.
func (s *Service) GetUserRole(ctx context.Context, targetUserID string) (UserRole, error)

// AssignRole assigns (or replaces) a role for targetUserID.
//
// Guards (in order):
//   1. Parse targetUserID → ErrUserRoleNotFound on bad UUID (consistent with GET)
//   2. Parse actingUserID (from JWT) — 500 on failure (should never happen)
//   3. Self-assignment: targetUserID == actingUserID → rbac.ErrCannotModifyOwnRole
//   4. Validate input: role_id + granted_reason required
//   5. Parse roleID → ErrRoleNotFound on bad UUID
//   6. GetUserRole for target: if row found and IsOwnerRole = true → rbac.ErrCannotReassignOwner
//      (no row = target has no role = not an owner = safe to proceed)
//   7. AssignUserRole in store (upsert)
func (s *Service) AssignRole(ctx context.Context, targetUserID, actingUserID string, in AssignRoleInput) (UserRole, error)

// RemoveRole removes the active role for targetUserID.
//
// Guards (in order):
//   1. Parse targetUserID → ErrUserRoleNotFound on bad UUID
//   2. Self-assignment: targetUserID == actingUserID → rbac.ErrCannotModifyOwnRole
//   3. GetUserRole for target: if not found → ErrUserRoleNotFound (nothing to remove)
//   4. If IsOwnerRole = true → rbac.ErrCannotReassignOwner
//   5. RemoveUserRole in store (WithActingUser sets audit actor)
func (s *Service) RemoveRole(ctx context.Context, targetUserID, actingUserID string) error
```

**Important:** `actingUserID` is always the calling user's ID extracted from the JWT
by `token.UserIDFromContext`. It is never taken from the URL or request body.

---

## Step 6 — `userroles/store.go`

Embed `rbacshared.BaseStore`. Implement the three `Storer` methods.

### `GetUserRole`

```go
func (s *Store) GetUserRole(ctx context.Context, userID [16]byte) (UserRole, error) {
    row, err := s.Queries.GetUserRole(ctx, s.ToPgtypeUUID(userID))
    if err != nil {
        if s.IsNoRows(err) {
            return UserRole{}, ErrUserRoleNotFound
        }
        return UserRole{}, fmt.Errorf("store.GetUserRole: %w", err)
    }
    return mapUserRole(row), nil
}
```

### `AssignUserRole`

The generated `db.AssignUserRole` returns only `user_id, role_id, expires_at,
created_at, updated_at` — it does not include `role_name` or `is_owner_role`.
After the upsert, call `db.GetUserRole` on the same connection to get the full row.
No transaction needed — both calls target the same row and the upsert is atomic.

```go
func (s *Store) AssignUserRole(ctx context.Context, in AssignRoleTxInput) (UserRole, error) {
    // Verify the role exists and is active before the upsert.
    // GetRoleByID returns ErrNoRows if inactive or missing.
    if _, err := s.Queries.GetRoleByID(ctx, s.ToPgtypeUUID(in.RoleID)); err != nil {
        if s.IsNoRows(err) {
            return UserRole{}, ErrRoleNotFound
        }
        return UserRole{}, fmt.Errorf("store.AssignUserRole: check role: %w", err)
    }

    var expiresAt pgtype.Timestamptz
    if in.ExpiresAt != nil {
        expiresAt = pgtype.Timestamptz{Time: *in.ExpiresAt, Valid: true}
    }

    _, err := s.Queries.AssignUserRole(ctx, db.AssignUserRoleParams{
        UserID:        s.ToPgtypeUUID(in.UserID),
        RoleID:        s.ToPgtypeUUID(in.RoleID),
        GrantedBy:     s.ToPgtypeUUID(in.GrantedBy),
        GrantedReason: in.GrantedReason,
        ExpiresAt:     expiresAt,
    })
    if err != nil {
        return UserRole{}, fmt.Errorf("store.AssignUserRole: upsert: %w", err)
    }

    // Re-read to get role_name and is_owner_role (not returned by the upsert query).
    row, err := s.Queries.GetUserRole(ctx, s.ToPgtypeUUID(in.UserID))
    if err != nil {
        return UserRole{}, fmt.Errorf("store.AssignUserRole: re-read: %w", err)
    }
    return mapUserRole(row), nil
}
```

### `RemoveUserRole`

Uses `WithActingUser` so `fn_audit_user_roles` records the correct deletion actor.

```go
func (s *Store) RemoveUserRole(ctx context.Context, userID [16]byte, actingUserID string) error {
    var rowsAffected int64
    err := s.WithActingUser(ctx, actingUserID, func() error {
        n, err := s.Queries.RemoveUserRole(ctx, s.ToPgtypeUUID(userID))
        if err != nil {
            return err
        }
        rowsAffected = n
        return nil
    })
    if err != nil {
        if isOrphanedOwnerViolation(err) {
            return ErrLastOwnerRemoval
        }
        return fmt.Errorf("store.RemoveUserRole: %w", err)
    }
    if rowsAffected == 0 {
        return ErrUserRoleNotFound
    }
    return nil
}
```

### `isOrphanedOwnerViolation` helper (unexported, in store.go)

`fn_prevent_orphaned_owner` raises `ERRCODE = 'integrity_constraint_violation'`
(SQLSTATE `23000`). Check the code AND a substring of the message so we don't
misclassify other integrity violations as this error.

```go
// isOrphanedOwnerViolation reports whether err is the fn_prevent_orphaned_owner
// trigger error (SQLSTATE 23000 + "last active owner" in the message).
func isOrphanedOwnerViolation(err error) bool {
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) {
        return pgErr.Code == "23000" && strings.Contains(pgErr.Message, "last active owner")
    }
    return false
}
```

### `mapUserRole` helper (unexported)

```go
func mapUserRole(row db.GetUserRoleRow) UserRole {
    ur := UserRole{
        UserID:      row.UserID.String(),
        RoleID:      row.RoleID.String(),
        RoleName:    row.RoleName,
        IsOwnerRole: row.IsOwnerRole,
        GrantedAt:   row.GrantedAt.Time,
    }
    if row.ExpiresAt.Valid {
        t := row.ExpiresAt.Time
        ur.ExpiresAt = &t
    }
    return ur
}
```

---

## Step 7 — `userroles/handler.go`

### Handler struct

```go
type Servicer interface {
    GetUserRole(ctx context.Context, targetUserID string) (UserRole, error)
    AssignRole(ctx context.Context, targetUserID, actingUserID string, in AssignRoleInput) (UserRole, error)
    RemoveRole(ctx context.Context, targetUserID, actingUserID string) error
}

type Handler struct{ svc Servicer }

func NewHandler(svc Servicer) *Handler { return &Handler{svc: svc} }
```

### `GetUserRole`

```
GET /admin/rbac/users/{user_id}/role
1. chi.URLParam(r, "user_id")
2. svc.GetUserRole(ctx, userID)
3. 200 + toUserRoleResponse(role)

Errors:
  ErrUserRoleNotFound → 404, "user_role_not_found"
  default             → 500
```

### `AssignRole`

```
PUT /admin/rbac/users/{user_id}/role
1. http.MaxBytesReader + respond.MaxBodyBytes
2. chi.URLParam(r, "user_id")
3. token.UserIDFromContext → actingUserID (401 if missing)
4. respond.DecodeJSON[assignRoleRequest]
5. svc.AssignRole(ctx, userID, actingUserID, input)
6. 200 + toUserRoleResponse(role)

Errors:
  rbac.ErrCannotModifyOwnRole   → 409, "cannot_modify_own_role"
  rbac.ErrCannotReassignOwner   → 409, "cannot_reassign_owner"
  ErrRoleIDEmpty                → 422, "role_id_required"
  ErrGrantedReasonEmpty         → 422, "granted_reason_required"
  ErrRoleNotFound               → 422, "role_not_found"
  ErrLastOwnerRemoval           → 409, "last_owner_removal" (shouldn't fire on assign, but guard it)
  default                       → 500
```

### `RemoveRole`

```
DELETE /admin/rbac/users/{user_id}/role
1. chi.URLParam(r, "user_id")
2. token.UserIDFromContext → actingUserID (401 if missing)
3. svc.RemoveRole(ctx, userID, actingUserID)
4. 204

Errors:
  rbac.ErrCannotModifyOwnRole  → 409, "cannot_modify_own_role"
  rbac.ErrCannotReassignOwner  → 409, "cannot_reassign_owner"
  ErrUserRoleNotFound          → 404, "user_role_not_found"
  ErrLastOwnerRemoval          → 409, "last_owner_removal"
  default                      → 500
```

---

## Step 8 — `userroles/routes.go`

```go
package userroles

import (
    "context"
    "github.com/7-Dany/store/backend/internal/app"
    "github.com/7-Dany/store/backend/internal/platform/rbac"
    "github.com/go-chi/chi/v5"
)

func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
    store := NewStore(deps.Pool)
    svc := NewService(store)
    h := NewHandler(svc)

    r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
        Get("/rbac/users/{user_id}/role", h.GetUserRole)

    r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
        Put("/rbac/users/{user_id}/role", h.AssignRole)

    r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
        Delete("/rbac/users/{user_id}/role", h.RemoveRole)
}
```

---

## Step 9 — `internal/domain/rbac/routes.go`

In `adminRoutes`, add after the `roles.Routes` line:

```go
userroles.Routes(ctx, r, deps) // Phase 9
```

Update the import block to include `userroles`.

---

## Step 10 — `shared/testutil/fake_storer.go`

Add `UserRolesFakeStorer` after the existing storer types. Follow the exact same
nil-check + default pattern as `RolesFakeStorer`.

**Defaults (safe happy-path values):**
- `GetUserRoleFn`: returns `(UserRole{RoleName: "admin"}, nil)` — a non-owner role so guards pass by default
- `AssignUserRoleFn`: returns `(UserRole{}, nil)`
- `RemoveUserRoleFn`: returns `nil`

```go
type UserRolesFakeStorer struct {
    GetUserRoleFn    func(ctx context.Context, userID [16]byte) (userroles.UserRole, error)
    AssignUserRoleFn func(ctx context.Context, in userroles.AssignRoleTxInput) (userroles.UserRole, error)
    RemoveUserRoleFn func(ctx context.Context, userID [16]byte, actingUserID string) error
}

var _ userroles.Storer = (*UserRolesFakeStorer)(nil)
```

Add forwarding methods following the same nil-guard pattern as the existing types.

Add the `userroles` import to the import block.

---

## Step 11 — `userroles/export_test.go`

```go
package userroles

// Exported for unit tests in the userroles_test package.
var IsOrphanedOwnerViolation = isOrphanedOwnerViolation
```

---

## Step 12 — Tests

### Test IDs

| ID | File | Type | Description |
|----|------|------|-------------|
| T-R34 | `store_test.go` | I | `AssignUserRole` assigns role; `GetUserRole` returns it |
| T-R34b | `store_test.go` | I | `AssignUserRole` replaces an existing role |
| T-R35 | `store_test.go` | I | `AssignUserRole` returns `ErrRoleNotFound` for unknown role_id |
| T-R36 | `store_test.go` | I | `RemoveUserRole` removes the assignment; subsequent `GetUserRole` returns `ErrUserRoleNotFound` |
| T-R36b | `store_test.go` | I | `RemoveUserRole` returns `ErrUserRoleNotFound` when no assignment exists |
| T-R36c | `store_test.go` | I | `RemoveUserRole` returns `ErrLastOwnerRemoval` when removing the last owner |
| T-R37 | `service_test.go` | U | `AssignRole` returns `rbac.ErrCannotModifyOwnRole` when targetUserID == actingUserID |
| T-R37b | `service_test.go` | U | `AssignRole` returns `rbac.ErrCannotReassignOwner` when target user is an owner |
| T-R37c | `service_test.go` | U | `AssignRole` proceeds when target user has no role |
| T-R38 | `handler_test.go` | U | `PUT` returns 409 `cannot_reassign_owner` |
| T-R38b | `handler_test.go` | U | `PUT` returns 409 `cannot_modify_own_role` |
| T-R38c | `handler_test.go` | U | `DELETE` returns 409 `last_owner_removal` |
| T-R38d | `handler_test.go` | U | `GET` returns 404 `user_role_not_found` |

### Service unit test — T-R37b (owner guard)

```go
func TestService_AssignRole_OwnerGuard(t *testing.T) {
    t.Parallel()
    targetID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
    actorID  := "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"

    store := &rbacsharedtest.UserRolesFakeStorer{
        GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
            return userroles.UserRole{IsOwnerRole: true, RoleName: "owner"}, nil
        },
    }
    svc := userroles.NewService(store)
    _, err := svc.AssignRole(context.Background(), targetID, actorID, userroles.AssignRoleInput{
        RoleID: "cccccccc-dddd-eeee-ffff-aaaaaaaaaaaa", GrantedReason: "test",
    })
    require.ErrorIs(t, err, platformrbac.ErrCannotReassignOwner)
}
```

### T-R37c — no existing role (safe to proceed)

```go
func TestService_AssignRole_NoExistingRole(t *testing.T) {
    t.Parallel()
    targetID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
    actorID  := "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
    expected := userroles.UserRole{RoleName: "admin", RoleID: "cccccccc-..."}

    store := &rbacsharedtest.UserRolesFakeStorer{
        GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
            return userroles.UserRole{}, userroles.ErrUserRoleNotFound // no existing role
        },
        AssignUserRoleFn: func(_ context.Context, _ userroles.AssignRoleTxInput) (userroles.UserRole, error) {
            return expected, nil
        },
    }
    svc := userroles.NewService(store)
    got, err := svc.AssignRole(context.Background(), targetID, actorID, userroles.AssignRoleInput{
        RoleID: "cccccccc-dddd-eeee-ffff-aaaaaaaaaaaa", GrantedReason: "test",
    })
    require.NoError(t, err)
    require.Equal(t, expected.RoleName, got.RoleName)
}
```

### Handler test stubs (T-R38 through T-R38d)

All use `rbacsharedtest.UserRolesFakeServicer` (add to `fake_servicer.go` following
the same pattern as existing servicers). Configure the service Fn field to return
the target error, then assert the HTTP status + error code.

### Integration test setup — T-R36c (last owner removal)

The `fn_prevent_orphaned_owner` trigger blocks removal of the last owner. To test
this without modifying the real owner:

```go
// Create a test user, assign them the owner role via seeds/fixture,
// then attempt to remove their role. The trigger should fire.
// Use SET rbac.skip_orphan_check = '1' in the test transaction for
// all OTHER fixture teardown operations so only the target DELETE is unguarded.
```

The trigger escape hatch `SET LOCAL rbac.skip_orphan_check = '1'` is available for
test fixture teardown but must not be used in the assertion path itself.

---

## Step 13 — `shared/testutil/fake_servicer.go`

Add `UserRolesFakeServicer` following the `RolesFakeServicer` pattern. Defaults:
- `GetUserRoleFn`: returns `(UserRole{RoleName: "admin"}, nil)`
- `AssignRoleFn`: returns `(UserRole{}, nil)`
- `RemoveRoleFn`: returns `nil`

Add a compile-time check: `var _ userroles.Servicer = (*UserRolesFakeServicer)(nil)`.

---

## What NOT to do in this phase

- Do not add user existence validation in the service — the DB FK on `user_roles.user_id`
  enforces this; a missing user_id will return a FK violation which maps to a 500.
  Intentional: admin routes assume target users are looked up before role assignment.
- Do not add `GetActiveUserByID` to the Storer — user existence checks are out of
  scope for V1 role assignment. The handler reads `user_id` from the URL and passes it
  directly to the service.
- Do not add `GrantedBy` to `userRoleResponse` — callers can look up the actor in
  the audit log. Exposing it in the response is a privacy consideration deferred to
  a future phase.
- Do not modify `CheckUserAccess` — role assignment does not touch the hot-path
  query.
- Do not use `rbac.skip_orphan_check` in any production code path — it is
  exclusively for test fixtures.

---

## Gate checklist

- [ ] `go build ./internal/domain/rbac/userroles/...` — zero errors
- [ ] `go build ./internal/domain/rbac/...` — zero errors (routes.go updated)
- [ ] `go build ./internal/domain/rbac/shared/testutil/...` — compile-time checks pass
- [ ] `go vet ./internal/domain/rbac/...` — zero warnings
- [ ] `go test ./internal/domain/rbac/userroles/...` — unit tests pass (T-R37, T-R37b, T-R37c, T-R38–T-R38d)
- [ ] `go test -tags integration_test ./internal/domain/rbac/userroles/...` — T-R34–T-R38d all green
- [ ] `GET  /admin/rbac/users/{id}/role` — returns role object with `role_name` and `is_owner_role`
- [ ] `PUT  /admin/rbac/users/{id}/role` — upserts and returns full role object
- [ ] `PUT  /admin/rbac/users/{owner_id}/role` — returns 409 `cannot_reassign_owner`
- [ ] `PUT  /admin/rbac/users/{self}/role` — returns 409 `cannot_modify_own_role`
- [ ] `DELETE /admin/rbac/users/{id}/role` — returns 204
- [ ] `DELETE /admin/rbac/users/{last_owner}/role` — returns 409 `last_owner_removal`
- [ ] No circular imports introduced
