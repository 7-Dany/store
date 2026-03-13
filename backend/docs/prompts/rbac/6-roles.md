# RBAC — Phase 6: Roles API (`internal/domain/rbac/roles/`)

**Feature:** RBAC
**Phase:** 6 of 10
**Depends on:** Phases 0–5 (schema ✅, queries ✅, seeds ✅, platform/rbac ✅, bootstrap ✅, permissions ✅)
**Gate:** `go test -tags integration_test ./internal/domain/rbac/roles/...` green — T-R23 through T-R31
**Design doc:** `docs/prompts/rbac/0-design.md`
**Go version:** 1.25 — use modern idioms throughout (`any`, `min`/`max`, range-over-int, etc.)

---

## What this phase builds

```
internal/domain/rbac/
    roles/
        handler.go              NEW
        service.go              NEW
        store.go                NEW
        models.go               NEW
        requests.go             NEW — createRoleRequest, updateRoleRequest, addRolePermissionRequest
        validators.go           NEW — name validation, access_type / scope validation
        errors.go               NEW — ErrRoleNotFound, ErrRolePermissionNotFound, ErrPermissionNotFound
        routes.go               NEW
        handler_test.go         NEW — handler unit tests (no build tag)
        service_test.go         NEW — service unit tests (no build tag)
        store_test.go           NEW — //go:build integration_test; TestMain, T-R23 through T-R31

internal/domain/rbac/shared/testutil/
    fake_storer.go              MODIFY — add RolesFakeStorer
    fake_servicer.go            MODIFY — add RolesFakeServicer
    querier_proxy.go            MODIFY — add roles Fail* flags + forwarding methods

internal/domain/rbac/routes.go  MODIFY — mount roles sub-router in adminRoutes
```

---

## Read before writing any code

| File | Why |
|---|---|
| `docs/prompts/rbac/0-design.md §4, §5, §7, §8, §12` | Package structure, SQL queries, type contracts, API routes 2–9, test cases T-R23 to T-R31 |
| `docs/RULES.md` | §1.2 domain layout, §3.1 file layout, §3.3 layer types, §3.4 error handling, §3.8 testing, §3.13 checklist |
| `internal/db/rbac.sql.go` | Exact signatures: `GetRoles`, `GetRoleByID`, `CreateRole`, `UpdateRole`, `DeactivateRole`, `GetRolePermissions`, `AddRolePermission`, `RemoveRolePermission`, `GetPermissionByCanonicalName` |
| `internal/db/models.go` | `db.Role`, `db.PermissionAccessType` enum constants, `db.PermissionScope` enum constants |
| `internal/domain/rbac/shared/store.go` | `BaseStore`, `NewBaseStore`, `WithQuerier`, `ToPgtypeUUID`, `IsNoRows` |
| `internal/domain/rbac/shared/errors.go` | Shared sentinels |
| `internal/domain/rbac/shared/testutil/fake_storer.go` | Existing FakeStorer layout — follow exactly for RolesFakeStorer |
| `internal/domain/rbac/shared/testutil/fake_servicer.go` | Existing FakeServicer layout — follow exactly for RolesFakeServicer |
| `internal/domain/rbac/shared/testutil/querier_proxy.go` | Existing Fail* flags — add roles section at the end |
| `internal/domain/rbac/shared/testutil/builders.go` | `RunTestMain`, `MustBeginTx`, `MustUUID`, `NewEmail` |
| `internal/domain/rbac/bootstrap/handler.go` | `mustUserID`, error switch, `respond.*` call pattern |
| `internal/domain/rbac/bootstrap/store.go` | `NewStore`, `WithQuerier`, compile-time check pattern |
| `internal/domain/rbac/bootstrap/handler_test.go` | Unit test layout, `decodeBody`, chi path param injection pattern |
| `internal/domain/rbac/permissions/store_test.go` | `txStores`, `withProxy`, integration test layout |
| `internal/domain/rbac/routes.go` | `adminRoutes` — where to mount |
| `internal/platform/rbac/errors.go` | `ErrSystemRoleImmutable` — **do not redefine this locally** |
| `internal/platform/rbac/checker.go` | `PermRBACRead`, `PermRBACManage` constants |

---

## Generated DB types (reference)

```go
// GetRoles returns all active roles ordered by name.
func (q *Queries) GetRoles(ctx context.Context) ([]Role, error)

// GetRoleByID returns one role row by primary key (no active filter).
func (q *Queries) GetRoleByID(ctx context.Context, id pgtype.UUID) (Role, error)

// CreateRole inserts a new non-system role (is_system_role = FALSE hardcoded).
func (q *Queries) CreateRole(ctx context.Context, arg CreateRoleParams) (Role, error)
type CreateRoleParams struct {
    Name        string
    Description pgtype.Text
}

// UpdateRole updates name/description WHERE is_system_role = FALSE.
// pgx.ErrNoRows when role is a system role or does not exist.
func (q *Queries) UpdateRole(ctx context.Context, arg UpdateRoleParams) (Role, error)
type UpdateRoleParams struct {
    Name        pgtype.Text  // {Valid: false} → keep current value (sqlc.narg)
    Description pgtype.Text  // {Valid: false} → keep current value (sqlc.narg)
    ID          pgtype.UUID
}

// DeactivateRole soft-deletes WHERE is_system_role = FALSE AND is_active = TRUE.
// Returns rows affected: 0 = system role or already inactive.
func (q *Queries) DeactivateRole(ctx context.Context, id pgtype.UUID) (int64, error)

// GetRolePermissions returns active permissions for a role ordered by canonical_name.
func (q *Queries) GetRolePermissions(ctx context.Context, roleID pgtype.UUID) ([]GetRolePermissionsRow, error)
type GetRolePermissionsRow struct {
    ID            uuid.UUID
    CanonicalName pgtype.Text
    Name          string
    ResourceType  string
    Description   pgtype.Text
    AccessType    PermissionAccessType  // "direct"|"conditional"|"request"|"denied"
    Scope         PermissionScope       // "own"|"all"
    Conditions    []byte                // JSON bytes; DB default '{}'
    GrantedAt     time.Time
}

// AddRolePermission inserts a role-permission grant.
// ON CONFLICT (role_id, permission_id) DO NOTHING — duplicate is a silent no-op.
func (q *Queries) AddRolePermission(ctx context.Context, arg AddRolePermissionParams) error
type AddRolePermissionParams struct {
    RoleID        pgtype.UUID
    PermissionID  pgtype.UUID
    GrantedBy     pgtype.UUID
    GrantedReason string
    AccessType    PermissionAccessType
    Scope         PermissionScope
    Conditions    []byte  // must be valid JSON; '{}' if no conditions
}

// RemoveRolePermission hard-deletes by (role_id, permission_id).
// Returns rows affected: 0 → grant did not exist.
func (q *Queries) RemoveRolePermission(ctx context.Context, arg RemoveRolePermissionParams) (int64, error)
type RemoveRolePermissionParams struct {
    RoleID       pgtype.UUID
    PermissionID pgtype.UUID
}

// GetPermissionByCanonicalName used by AddRolePermission service to validate
// the permission exists before delegating to the store (FK gives 500 otherwise).
func (q *Queries) GetPermissionByCanonicalName(ctx context.Context, canonicalName pgtype.Text) (GetPermissionByCanonicalNameRow, error)

// db.Role (from models.go)
type Role struct {
    ID           uuid.UUID
    Name         string
    Description  pgtype.Text
    IsSystemRole bool
    IsOwnerRole  bool
    IsActive     bool
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

// db enum constants
const (
    PermissionAccessTypeDirect      PermissionAccessType = "direct"
    PermissionAccessTypeConditional PermissionAccessType = "conditional"
    PermissionAccessTypeRequest     PermissionAccessType = "request"
    PermissionAccessTypeDenied      PermissionAccessType = "denied"
)
const (
    PermissionScopeOwn PermissionScope = "own"
    PermissionScopeAll PermissionScope = "all"
)
```

---

## `errors.go`

```go
package roles

import "errors"

// ErrRoleNotFound is returned when GetRoleByID finds no matching row, or when
// a service method receives an ID string that is not a valid UUID.
var ErrRoleNotFound = errors.New("role not found")

// ErrRolePermissionNotFound is returned when RemoveRolePermission affects 0 rows,
// or when either ID string supplied to RemoveRolePermission is not a valid UUID.
var ErrRolePermissionNotFound = errors.New("role permission grant not found")

// ErrPermissionNotFound is returned when AddRolePermission receives a
// permission_id that does not correspond to any active permission.
var ErrPermissionNotFound = errors.New("permission not found")
```

`rbac.ErrSystemRoleImmutable` lives in `internal/platform/rbac/errors.go` — import it from there; **do not redefine it locally**.

---

## `models.go`

```go
// Package roles provides the HTTP handler, service, and store for the roles
// admin API: CRUD on roles and management of role-permission grants.
package roles

import (
    "encoding/json"
    "time"
)

// Role is the service-layer representation of an RBAC role,
// also marshalled as the JSON response element.
type Role struct {
    ID           string    `json:"id"`
    Name         string    `json:"name"`
    Description  string    `json:"description,omitempty"`
    IsSystemRole bool      `json:"is_system_role"`
    IsOwnerRole  bool      `json:"is_owner_role"`
    IsActive     bool      `json:"is_active"`
    CreatedAt    time.Time `json:"created_at"`
}

// RolePermission is a permission assigned to a role with its access metadata.
// Embedded inside the GET /roles/:id/permissions response.
type RolePermission struct {
    PermissionID  string          `json:"permission_id"`
    CanonicalName string          `json:"canonical_name"`
    ResourceType  string          `json:"resource_type"`
    Name          string          `json:"name"`
    AccessType    string          `json:"access_type"`
    Scope         string          `json:"scope"`
    Conditions    json.RawMessage `json:"conditions,omitempty"`
    GrantedAt     time.Time       `json:"granted_at"`
}

// CreateRoleInput is the service-layer input for creating a role.
type CreateRoleInput struct {
    Name        string
    Description string
}

// UpdateRoleInput is the service-layer input for patching a role.
// Only non-nil fields are applied (partial update).
type UpdateRoleInput struct {
    Name        *string
    Description *string
}

// AddRolePermissionInput is the service-layer input for adding a permission to a role.
type AddRolePermissionInput struct {
    PermissionID  [16]byte
    GrantedBy     [16]byte
    GrantedReason string
    AccessType    string          // validated against db.PermissionAccessType values before passing here
    Scope         string          // validated against db.PermissionScope values before passing here
    Conditions    json.RawMessage // '{}' when not provided
}
```

---

## `requests.go`

```go
package roles

import "encoding/json"

// createRoleRequest is the JSON body for POST /admin/rbac/roles.
type createRoleRequest struct {
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
}

// updateRoleRequest is the JSON body for PATCH /admin/rbac/roles/:id.
// All fields are optional — at least one must be non-nil after parsing
// (enforced by validateUpdateRole).
type updateRoleRequest struct {
    Name        *string `json:"name"`
    Description *string `json:"description"`
}

// addRolePermissionRequest is the JSON body for POST /admin/rbac/roles/:id/permissions.
type addRolePermissionRequest struct {
    PermissionID  string          `json:"permission_id"`
    AccessType    string          `json:"access_type"`
    Scope         string          `json:"scope"`
    Conditions    json.RawMessage `json:"conditions,omitempty"`
    GrantedReason string          `json:"granted_reason"`
}
```

---

## `validators.go`

```go
package roles

import (
    "errors"
    "strings"

    "github.com/7-Dany/store/backend/internal/db"
)

var (
    ErrNameEmpty          = errors.New("name is required")
    ErrNameTooLong        = errors.New("name must be 100 characters or fewer")
    ErrNoUpdateFields     = errors.New("at least one field (name or description) must be provided")
    ErrInvalidAccessType  = errors.New("access_type must be one of: direct, conditional, request, denied")
    ErrInvalidScope       = errors.New("scope must be one of: own, all")
    ErrPermissionIDEmpty  = errors.New("permission_id is required")
    ErrGrantedReasonEmpty = errors.New("granted_reason is required")
)

// validateCreateRole validates a createRoleRequest.
func validateCreateRole(req *createRoleRequest) error {
    name := strings.TrimSpace(req.Name)
    if name == "" {
        return ErrNameEmpty
    }
    if len(name) > 100 {
        return ErrNameTooLong
    }
    return nil
}

// validateUpdateRole validates an updateRoleRequest.
// At least one field must be non-nil; if Name is provided it must be non-empty.
func validateUpdateRole(req *updateRoleRequest) error {
    if req.Name == nil && req.Description == nil {
        return ErrNoUpdateFields
    }
    if req.Name != nil {
        if strings.TrimSpace(*req.Name) == "" {
            return ErrNameEmpty
        }
        if len(*req.Name) > 100 {
            return ErrNameTooLong
        }
    }
    return nil
}

// validateAddRolePermission validates an addRolePermissionRequest.
func validateAddRolePermission(req *addRolePermissionRequest) error {
    if strings.TrimSpace(req.PermissionID) == "" {
        return ErrPermissionIDEmpty
    }
    if strings.TrimSpace(req.GrantedReason) == "" {
        return ErrGrantedReasonEmpty
    }
    switch db.PermissionAccessType(req.AccessType) {
    case db.PermissionAccessTypeDirect, db.PermissionAccessTypeConditional,
        db.PermissionAccessTypeRequest, db.PermissionAccessTypeDenied:
        // valid
    default:
        return ErrInvalidAccessType
    }
    switch db.PermissionScope(req.Scope) {
    case db.PermissionScopeOwn, db.PermissionScopeAll:
        // valid
    default:
        return ErrInvalidScope
    }
    return nil
}
```

---

## `store.go`

### Storer interface (defined in `service.go` per ADR-007 — shown here for clarity)

```go
type Storer interface {
    GetRoles(ctx context.Context) ([]Role, error)
    GetRoleByID(ctx context.Context, roleID [16]byte) (Role, error)
    CreateRole(ctx context.Context, in CreateRoleInput) (Role, error)
    UpdateRole(ctx context.Context, roleID [16]byte, in UpdateRoleInput) (Role, error)
    DeactivateRole(ctx context.Context, roleID [16]byte) error
    GetRolePermissions(ctx context.Context, roleID [16]byte) ([]RolePermission, error)
    AddRolePermission(ctx context.Context, roleID [16]byte, in AddRolePermissionInput) error
    RemoveRolePermission(ctx context.Context, roleID, permID [16]byte) error
}
```

### Store struct

```go
// compile-time check: *Store satisfies Storer.
var _ Storer = (*Store)(nil)

type Store struct {
    rbacshared.BaseStore
}

func NewStore(pool *pgxpool.Pool) *Store {
    return &Store{BaseStore: rbacshared.NewBaseStore(pool)}
}

func (s *Store) WithQuerier(q db.Querier) *Store {
    c := *s
    c.BaseStore = s.BaseStore.WithQuerier(q)
    return &c
}
```

### `GetRoles`

Calls `s.Queries.GetRoles(ctx)`. Maps each `db.Role` to `Role`:
```
ID           → uuid.UUID(row.ID).String()
Name         → row.Name
Description  → row.Description.String
IsSystemRole → row.IsSystemRole
IsOwnerRole  → row.IsOwnerRole
IsActive     → row.IsActive
CreatedAt    → row.CreatedAt
```
Return `[]Role{}` (not nil) on zero results. Error wrap: `"store.GetRoles: %w"`.

### `GetRoleByID`

Calls `s.Queries.GetRoleByID(ctx, s.ToPgtypeUUID(roleID))`.
- No rows (`s.IsNoRows(err)`) → `return Role{}, ErrRoleNotFound`.
- Error wrap: `"store.GetRoleByID: %w"`.
- Maps `db.Role` to `Role` using the same mapping as `GetRoles`.

### `CreateRole`

Calls `s.Queries.CreateRole(ctx, db.CreateRoleParams{...})`.
- `Description`: `pgtype.Text{String: in.Description, Valid: in.Description != ""}`.
- Maps returned `db.Role` to `Role`.
- Error wrap: `"store.CreateRole: %w"`.

### `UpdateRole`

Calls `s.Queries.UpdateRole(ctx, db.UpdateRoleParams{...})`.
- `Name`: `pgtype.Text{String: *in.Name, Valid: in.Name != nil}`.
- `Description`: `pgtype.Text{String: *in.Description, Valid: in.Description != nil}`.
- No rows (`s.IsNoRows(err)`) → `return Role{}, rbac.ErrSystemRoleImmutable`.
- Error wrap: `"store.UpdateRole: %w"`.

### `DeactivateRole`

Calls `s.Queries.DeactivateRole(ctx, s.ToPgtypeUUID(roleID))`.
- `rows == 0` → `return rbac.ErrSystemRoleImmutable`.
- On error: `return fmt.Errorf("store.DeactivateRole: %w", err)`.
- On success (rows > 0): `return nil`.

### `GetRolePermissions`

Calls `s.Queries.GetRolePermissions(ctx, s.ToPgtypeUUID(roleID))`.
Maps each `db.GetRolePermissionsRow` to `RolePermission`:
```
PermissionID  → uuid.UUID(row.ID).String()
CanonicalName → row.CanonicalName.String
Name          → row.Name
ResourceType  → row.ResourceType
AccessType    → string(row.AccessType)
Scope         → string(row.Scope)
Conditions    → json.RawMessage(row.Conditions)   (DB always returns at least '{}')
GrantedAt     → row.GrantedAt
```
Return `[]RolePermission{}` (not nil) on zero results. Error wrap: `"store.GetRolePermissions: %w"`.

### `AddRolePermission`

Calls `s.Queries.AddRolePermission(ctx, db.AddRolePermissionParams{...})`.
```
RoleID        → s.ToPgtypeUUID(roleID)
PermissionID  → s.ToPgtypeUUID(in.PermissionID)
GrantedBy     → s.ToPgtypeUUID(in.GrantedBy)
GrantedReason → in.GrantedReason
AccessType    → db.PermissionAccessType(in.AccessType)
Scope         → db.PermissionScope(in.Scope)
Conditions    → condBytes  (nil/empty in.Conditions → []byte("{}"))
```
ON CONFLICT DO NOTHING — no error on duplicate (idempotent). Error wrap: `"store.AddRolePermission: %w"`.

### `RemoveRolePermission`

Calls `s.Queries.RemoveRolePermission(ctx, db.RemoveRolePermissionParams{...})`.
- `rows == 0` → `return ErrRolePermissionNotFound`.
- Error wrap: `"store.RemoveRolePermission: %w"`.

---

## `service.go`

### Storer interface

Defined here per ADR-007 (same definition as shown in store.go section above).

### Service struct

```go
type Service struct {
    store Storer
}

func NewService(store Storer) *Service {
    return &Service{store: store}
}

// compile-time check: Service satisfies Servicer.
var _ Servicer = (*Service)(nil)
```

### Methods

**`ListRoles`** — delegates to `s.store.GetRoles(ctx)`.
Error wrap: `"roles.ListRoles: %w"`.

**`GetRole(ctx, roleID string)`** — parses `roleID` → `[16]byte`.
- Parse error → `return Role{}, ErrRoleNotFound` (invalid UUID cannot exist in DB).
- Delegates to `s.store.GetRoleByID`. Error wrap: `"roles.GetRole: %w"`.

**`CreateRole(ctx, in CreateRoleInput)`** — delegates to `s.store.CreateRole`.
Error wrap: `"roles.CreateRole: %w"`.

**`UpdateRole(ctx, roleID string, in UpdateRoleInput)`** — parses `roleID` → `[16]byte`.
- Parse error → `return Role{}, ErrRoleNotFound`.
- `rbac.ErrSystemRoleImmutable` propagates via `%w` so `errors.Is` finds it.
- Error wrap: `"roles.UpdateRole: %w"`.

**`DeleteRole(ctx, roleID string)`** — parses `roleID` → `[16]byte`.
- Parse error → `return ErrRoleNotFound`.
- `rbac.ErrSystemRoleImmutable` propagates via `%w`.
- Error wrap: `"roles.DeleteRole: %w"`.

**`ListRolePermissions(ctx, roleID string)`** — parses `roleID` → `[16]byte`.
- Parse error → `return nil, ErrRoleNotFound`.
- Error wrap: `"roles.ListRolePermissions: %w"`.

**`AddRolePermission(ctx, roleID string, in AddRolePermissionInput)`** — parses `roleID` → `[16]byte`.
- Parse error → `return ErrRoleNotFound`.
- `in.PermissionID` is already `[16]byte` (parsed by handler).
- Error wrap: `"roles.AddRolePermission: %w"`.

**`RemoveRolePermission(ctx, roleID, permID string)`** — parses both → `[16]byte`.
- Either parse error → `return ErrRolePermissionNotFound`.
- Error wrap: `"roles.RemoveRolePermission: %w"`.

---

## `handler.go`

### Servicer interface

```go
type Servicer interface {
    ListRoles(ctx context.Context) ([]Role, error)
    GetRole(ctx context.Context, roleID string) (Role, error)
    CreateRole(ctx context.Context, in CreateRoleInput) (Role, error)
    UpdateRole(ctx context.Context, roleID string, in UpdateRoleInput) (Role, error)
    DeleteRole(ctx context.Context, roleID string) error
    ListRolePermissions(ctx context.Context, roleID string) ([]RolePermission, error)
    AddRolePermission(ctx context.Context, roleID string, in AddRolePermissionInput) error
    RemoveRolePermission(ctx context.Context, roleID, permID string) error
}
```

### Handler struct

```go
type Handler struct {
    svc Servicer
}

func NewHandler(svc Servicer) *Handler {
    return &Handler{svc: svc}
}
```

### `ListRoles` — `GET /admin/rbac/roles`

```
1. roles, err := h.svc.ListRoles(r.Context())
2. On error: log + respond.Error(w, 500, "internal_error", "internal server error")
3. On success: respond.JSON(w, 200, map[string]any{"roles": roles})
```

No body, no `mustUserID` — RBAC middleware handles auth.

### `CreateRole` — `POST /admin/rbac/roles`

```
1. r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
2. req, ok := respond.DecodeJSON[createRoleRequest](w, r)
3. validateCreateRole(&req) → 422 "validation_error" on failure
4. role, err := h.svc.CreateRole(r.Context(), CreateRoleInput{Name: req.Name, Description: req.Description})
5. On error: log + 500
6. On success: respond.JSON(w, 201, role)
```

### `GetRole` — `GET /admin/rbac/roles/{id}`

```
1. id := chi.URLParam(r, "id")
2. role, err := h.svc.GetRole(r.Context(), id)
3. Error switch:
   - errors.Is(err, ErrRoleNotFound) → 404 "role_not_found" "role not found"
   - default: log + 500
4. On success: respond.JSON(w, 200, role)
```

### `UpdateRole` — `PATCH /admin/rbac/roles/{id}`

```
1. id := chi.URLParam(r, "id")
2. r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
3. req, ok := respond.DecodeJSON[updateRoleRequest](w, r)
4. validateUpdateRole(&req) → 422 on failure
5. role, err := h.svc.UpdateRole(r.Context(), id, UpdateRoleInput{Name: req.Name, Description: req.Description})
6. Error switch:
   - errors.Is(err, ErrRoleNotFound) → 404 "role_not_found" "role not found"
   - errors.Is(err, rbac.ErrSystemRoleImmutable) → 409 "system_role_immutable" "system roles cannot be modified"
   - default: log + 500
7. On success: respond.JSON(w, 200, role)
```

### `DeleteRole` — `DELETE /admin/rbac/roles/{id}`

```
1. id := chi.URLParam(r, "id")
2. err := h.svc.DeleteRole(r.Context(), id)
3. Error switch:
   - errors.Is(err, ErrRoleNotFound) → 404 "role_not_found" "role not found"
   - errors.Is(err, rbac.ErrSystemRoleImmutable) → 409 "system_role_immutable" "system roles cannot be modified"
   - default: log + 500
4. On success: respond.NoContent(w)
```

### `ListRolePermissions` — `GET /admin/rbac/roles/{id}/permissions`

```
1. id := chi.URLParam(r, "id")
2. perms, err := h.svc.ListRolePermissions(r.Context(), id)
3. Error switch:
   - errors.Is(err, ErrRoleNotFound) → 404 "role_not_found" "role not found"
   - default: log + 500
4. On success: respond.JSON(w, 200, map[string]any{"permissions": perms})
```

### `AddRolePermission` — `POST /admin/rbac/roles/{id}/permissions`

```
1. userID, ok := h.mustUserID(w, r)          — granter is the authenticated caller
2. id := chi.URLParam(r, "id")                — role ID (string; parsed by service)
3. r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
4. req, ok := respond.DecodeJSON[addRolePermissionRequest](w, r)
5. validateAddRolePermission(&req) → 422 on failure
6. Parse req.PermissionID → uuid.Parse → [16]byte
   - error → 422 "validation_error" "invalid permission_id"
7. Parse userID → uuid.Parse → [16]byte  (always valid — from JWT)
8. conditions: if len(req.Conditions) == 0 → json.RawMessage("{}")
9. err := h.svc.AddRolePermission(r.Context(), id, AddRolePermissionInput{
       PermissionID:  permUUID,
       GrantedBy:     callerUUID,
       GrantedReason: req.GrantedReason,
       AccessType:    req.AccessType,
       Scope:         req.Scope,
       Conditions:    conditions,
   })
10. Error switch:
    - errors.Is(err, ErrRoleNotFound)      → 404 "role_not_found" "role not found"
    - errors.Is(err, ErrPermissionNotFound) → 404 "permission_not_found" "permission not found"
    - default: log + 500
11. On success: respond.NoContent(w)     — 204; duplicate is a silent no-op
```

### `RemoveRolePermission` — `DELETE /admin/rbac/roles/{id}/permissions/{perm_id}`

```
1. roleID := chi.URLParam(r, "id")
2. permID := chi.URLParam(r, "perm_id")
3. err := h.svc.RemoveRolePermission(r.Context(), roleID, permID)
4. Error switch:
   - errors.Is(err, ErrRolePermissionNotFound) → 404 "role_permission_not_found" "role permission grant not found"
   - default: log + 500
5. On success: respond.NoContent(w)
```

### `mustUserID` helper

Copy verbatim from `bootstrap/handler.go`:

```go
func (h *Handler) mustUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
    userID, ok := token.UserIDFromContext(r.Context())
    if !ok || userID == "" {
        respond.Error(w, http.StatusUnauthorized, "unauthorized", "missing or invalid access token")
        return "", false
    }
    return userID, true
}
```

---

## `routes.go`

```go
// Routes registers all roles endpoints on r.
// Called from adminRoutes in internal/domain/rbac/routes.go:
//
//   roles.Routes(ctx, r, deps)
func Routes(ctx context.Context, r chi.Router, deps *app.Deps)
```

Wiring:

```go
store := NewStore(deps.Pool)
svc   := NewService(store)
h     := NewHandler(svc)

r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
    Get("/rbac/roles", h.ListRoles)

r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
    Post("/rbac/roles", h.CreateRole)

r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
    Get("/rbac/roles/{id}", h.GetRole)

r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
    Patch("/rbac/roles/{id}", h.UpdateRole)

r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
    Delete("/rbac/roles/{id}", h.DeleteRole)

r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
    Get("/rbac/roles/{id}/permissions", h.ListRolePermissions)

r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
    Post("/rbac/roles/{id}/permissions", h.AddRolePermission)

r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACManage)).
    Delete("/rbac/roles/{id}/permissions/{perm_id}", h.RemoveRolePermission)
```

No rate limiters — admin routes are JWT + RBAC gated, not public-facing.

---

## `internal/domain/rbac/routes.go` — modification

Mount the roles sub-router inside `adminRoutes`:

```go
import "github.com/7-Dany/store/backend/internal/domain/rbac/roles"

func adminRoutes(ctx context.Context, deps *app.Deps) *chi.Mux {
    r := chi.NewRouter()
    r.Use(chimiddleware.AllowContentType("application/json"))
    permissions.Routes(ctx, r, deps)
    roles.Routes(ctx, r, deps)          // ← Phase 6
    // Phases 7–9 will mount here.
    return r
}
```

---

## Testutil updates (must be done atomically with the store files)

### `shared/testutil/fake_storer.go` — add `RolesFakeStorer`

Append after the `PermissionsFakeStorer` section. Follow the exact layout of existing fakers.

```go
// ─────────────────────────────────────────────────────────────────────────────
// RolesFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// RolesFakeStorer is a hand-written implementation of roles.Storer for service
// unit tests. Nil Fn fields return safe defaults so tests only configure what
// they need.
//
// Defaults:
//   GetRoles              → ([]roles.Role{}, nil)
//   GetRoleByID           → (roles.Role{}, nil)
//   CreateRole            → (roles.Role{}, nil)
//   UpdateRole            → (roles.Role{}, nil)
//   DeactivateRole        → nil
//   GetRolePermissions    → ([]roles.RolePermission{}, nil)
//   AddRolePermission     → nil
//   RemoveRolePermission  → nil
type RolesFakeStorer struct {
    GetRolesFn             func(ctx context.Context) ([]roles.Role, error)
    GetRoleByIDFn          func(ctx context.Context, roleID [16]byte) (roles.Role, error)
    CreateRoleFn           func(ctx context.Context, in roles.CreateRoleInput) (roles.Role, error)
    UpdateRoleFn           func(ctx context.Context, roleID [16]byte, in roles.UpdateRoleInput) (roles.Role, error)
    DeactivateRoleFn       func(ctx context.Context, roleID [16]byte) error
    GetRolePermissionsFn   func(ctx context.Context, roleID [16]byte) ([]roles.RolePermission, error)
    AddRolePermissionFn    func(ctx context.Context, roleID [16]byte, in roles.AddRolePermissionInput) error
    RemoveRolePermissionFn func(ctx context.Context, roleID, permID [16]byte) error
}

// compile-time interface check.
var _ roles.Storer = (*RolesFakeStorer)(nil)
```

Add a forwarding method per Fn field following the exact same pattern as `BootstrapFakeStorer`. Add the import `"github.com/7-Dany/store/backend/internal/domain/rbac/roles"`.

### `shared/testutil/fake_servicer.go` — add `RolesFakeServicer`

```go
// ─────────────────────────────────────────────────────────────────────────────
// RolesFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// RolesFakeServicer is a hand-written implementation of roles.Servicer for
// handler unit tests. Nil Fn fields return safe defaults.
//
// Defaults:
//   ListRolesFn            → ([]roles.Role{}, nil)
//   GetRoleFn              → (roles.Role{}, nil)
//   CreateRoleFn           → (roles.Role{}, nil)
//   UpdateRoleFn           → (roles.Role{}, nil)
//   DeleteRoleFn           → nil
//   ListRolePermissionsFn  → ([]roles.RolePermission{}, nil)
//   AddRolePermissionFn    → nil
//   RemoveRolePermissionFn → nil
type RolesFakeServicer struct {
    ListRolesFn            func(ctx context.Context) ([]roles.Role, error)
    GetRoleFn              func(ctx context.Context, roleID string) (roles.Role, error)
    CreateRoleFn           func(ctx context.Context, in roles.CreateRoleInput) (roles.Role, error)
    UpdateRoleFn           func(ctx context.Context, roleID string, in roles.UpdateRoleInput) (roles.Role, error)
    DeleteRoleFn           func(ctx context.Context, roleID string) error
    ListRolePermissionsFn  func(ctx context.Context, roleID string) ([]roles.RolePermission, error)
    AddRolePermissionFn    func(ctx context.Context, roleID string, in roles.AddRolePermissionInput) error
    RemoveRolePermissionFn func(ctx context.Context, roleID, permID string) error
}

// compile-time interface check.
var _ roles.Servicer = (*RolesFakeServicer)(nil)
```

Add a forwarding method per Fn field with safe defaults. Add the import.

### `shared/testutil/querier_proxy.go` — add roles section

Add these `Fail*` fields to the `QuerierProxy` struct under a new section separator:

```go
// ── roles ─────────────────────────────────────────────────────────────────────
FailGetRoles             bool
FailGetRoleByID          bool
FailGetRoleByName        bool
FailCreateRole           bool
FailUpdateRole           bool
FailDeactivateRole       bool
FailGetRolePermissions   bool
FailAddRolePermission    bool
FailRemoveRolePermission bool
```

Add nine forwarding methods using the same pattern as the existing permissions methods. After adding, run:

```bash
go build ./internal/domain/rbac/shared/testutil/...
```

to confirm the `var _ db.Querier = (*QuerierProxy)(nil)` compile-time check still passes.

---

## `handler_test.go` — handler unit tests

```go
package roles_test
```

No `//go:build` tag. Uses `RolesFakeServicer`. All sub-tests are parallel.

### Helpers

```go
const testRoleID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
const testPermID = "11111111-2222-3333-4444-555555555555"
const testUserID = "ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb"

// authedReq creates a request with testUserID injected into the context.
func authedReq(t *testing.T, method, path string, body io.Reader) *http.Request { ... }

func jsonBodyBytes(t *testing.T, v any) *bytes.Buffer { ... }
func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any { ... }

// injectChi sets chi URL params on r to simulate chi router extraction.
func injectChi(r *http.Request, params map[string]string) *http.Request {
    rctx := chi.NewRouteContext()
    for k, v := range params {
        rctx.URLParams.Add(k, v)
    }
    return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
```

### `TestHandler_ListRoles`

| Sub-test | Setup | Assert |
|---|---|---|
| returns 200 with roles key | `ListRolesFn` returns 2 items | 200, `body["roles"]` is 2-item array |
| empty slice marshals as `[]` | returns `[]Role{}` | body contains `"roles":[]` |
| service error returns 500 | returns `errors.New("db")` | 500, `code == "internal_error"` |

### `TestHandler_CreateRole`

| Sub-test | Assert |
|---|---|
| valid body returns 201 with role | 201, response contains `name` field |
| empty name returns 422 validation_error | 422, `code == "validation_error"` |
| malformed JSON returns 400 | 400 |
| body exceeds MaxBodyBytes returns 413 | 413 |
| service error returns 500 | 500, `code == "internal_error"` |

### `TestHandler_GetRole`

| Sub-test | Assert |
|---|---|
| existing role returns 200 | 200, role body |
| ErrRoleNotFound returns 404 role_not_found | 404, `code == "role_not_found"` |
| service error returns 500 | 500 |

### `TestHandler_UpdateRole`

| Sub-test | Assert |
|---|---|
| valid patch returns 200 with updated role | 200 |
| empty body (no fields) returns 422 | 422, `code == "validation_error"` |
| ErrRoleNotFound returns 404 | 404, `code == "role_not_found"` |
| ErrSystemRoleImmutable returns 409 | 409, `code == "system_role_immutable"` |
| service error returns 500 | 500 |

### `TestHandler_DeleteRole`

| Sub-test | Assert |
|---|---|
| success returns 204 | 204 |
| ErrRoleNotFound returns 404 | 404 `role_not_found` |
| ErrSystemRoleImmutable returns 409 | 409 `system_role_immutable` |
| service error returns 500 | 500 |

### `TestHandler_ListRolePermissions`

| Sub-test | Assert |
|---|---|
| returns 200 with permissions key | 200, `body["permissions"]` is array |
| empty slice marshals as `[]` | body contains `"permissions":[]` |
| ErrRoleNotFound returns 404 | 404 `role_not_found` |
| service error returns 500 | 500 |

### `TestHandler_AddRolePermission`

| Sub-test | Assert |
|---|---|
| no user ID in context returns 401 | 401 `unauthorized` |
| valid body returns 204 | 204 |
| empty permission_id returns 422 | 422 `validation_error` |
| empty granted_reason returns 422 | 422 `validation_error` |
| invalid access_type returns 422 | 422 `validation_error` |
| invalid scope returns 422 | 422 `validation_error` |
| malformed permission_id UUID returns 422 | 422 `validation_error` |
| ErrRoleNotFound returns 404 | 404 `role_not_found` |
| ErrPermissionNotFound returns 404 | 404 `permission_not_found` |
| service error returns 500 | 500 |

### `TestHandler_RemoveRolePermission`

| Sub-test | Assert |
|---|---|
| success returns 204 | 204 |
| ErrRolePermissionNotFound returns 404 | 404 `role_permission_not_found` |
| service error returns 500 | 500 |

---

## `service_test.go` — service unit tests

```go
package roles_test
```

No `//go:build` tag. Uses `RolesFakeStorer`. Tests are parallel.

### `TestService_ListRoles`
| Sub-test | Assert |
|---|---|
| delegates to store and returns slice | length matches |
| store error is wrapped | `errors.Is(err, dbErr)`, message contains `"roles.ListRoles:"` |

### `TestService_GetRole`
| Sub-test | Assert |
|---|---|
| valid UUID delegates to store | returned role matches |
| invalid UUID string returns ErrRoleNotFound | `errors.Is(err, ErrRoleNotFound)` |
| store returns ErrRoleNotFound → propagated | `errors.Is(err, ErrRoleNotFound)` |
| other store error is wrapped | message contains `"roles.GetRole:"` |

### `TestService_CreateRole`
| Sub-test | Assert |
|---|---|
| delegates and returns role | no error, role returned |
| store error is wrapped | message contains `"roles.CreateRole:"` |

### `TestService_UpdateRole`
| Sub-test | Assert |
|---|---|
| valid UUID delegates to store | updated role returned |
| invalid UUID string returns ErrRoleNotFound | `errors.Is` |
| store ErrSystemRoleImmutable propagates | `errors.Is(err, rbac.ErrSystemRoleImmutable)` |

### `TestService_DeleteRole`
| Sub-test | Assert |
|---|---|
| valid UUID delegates to store | nil error |
| invalid UUID string returns ErrRoleNotFound | `errors.Is` |
| store ErrSystemRoleImmutable propagates | `errors.Is(err, rbac.ErrSystemRoleImmutable)` |

### `TestService_RemoveRolePermission`
| Sub-test | Assert |
|---|---|
| valid IDs delegate to store | nil error |
| invalid roleID string returns ErrRolePermissionNotFound | `errors.Is` |
| invalid permID string returns ErrRolePermissionNotFound | `errors.Is` |
| store ErrRolePermissionNotFound propagates | `errors.Is` |

---

## `store_test.go` — store integration tests

```go
//go:build integration_test

package roles_test
```

### Boilerplate

```go
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
    rbacsharedtest.RunTestMain(m, &testPool, 20)
}

func txStores(t *testing.T) (*roles.Store, *db.Queries) {
    t.Helper()
    if testPool == nil {
        t.Skip("no test database configured")
    }
    _, q := rbacsharedtest.MustBeginTx(t, testPool)
    return roles.NewStore(testPool).WithQuerier(q), q
}

func withProxy(q db.Querier, proxy *rbacsharedtest.QuerierProxy) *roles.Store {
    proxy.Querier = q
    return roles.NewStore(testPool).WithQuerier(proxy)
}
```

### T-R23 — `TestGetRoles_Integration`

```
Sub-tests:
  "returns seeded roles"
      s, _ := txStores(t)
      rolesList, err := s.GetRoles(ctx)
      require.NoError(t, err)
      // Seeds create owner, admin, vendor, customer — at least 4 active roles.
      require.GreaterOrEqual(t, len(rolesList), 4)
      var found bool
      for _, r := range rolesList {
          if r.Name == "admin" {
              require.True(t, r.IsSystemRole)
              require.False(t, r.IsOwnerRole)
              found = true
          }
      }
      require.True(t, found, "admin role must be present in seeded roles")

  "result is never nil"
      s, _ := txStores(t)
      rolesList, err := s.GetRoles(ctx)
      require.NoError(t, err)
      require.NotNil(t, rolesList)

  "FailGetRoles returns ErrProxy"
      _, q := txStores(t)
      _, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetRoles: true}).GetRoles(ctx)
      require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
```

### T-R24 — `TestCreateRole_Integration`

```
Sub-tests:
  "creates non-system role and returns it"
      s, _ := txStores(t)
      role, err := s.CreateRole(ctx, roles.CreateRoleInput{Name: "test_vendor_plus", Description: "test"})
      require.NoError(t, err)
      require.Equal(t, "test_vendor_plus", role.Name)
      require.False(t, role.IsSystemRole)
      require.False(t, role.IsOwnerRole)
      require.NotEmpty(t, role.ID)

  "FailCreateRole returns ErrProxy"
      _, q := txStores(t)
      _, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailCreateRole: true}).
          CreateRole(ctx, roles.CreateRoleInput{Name: "x"})
      require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
```

### T-R25 — `TestUpdateRole_Integration`

```
Sub-tests:
  "updates name for non-system role"
      s, q := txStores(t)
      created, _ := q.CreateRole(ctx, db.CreateRoleParams{Name: "mutable_role"})
      updated, err := s.UpdateRole(ctx, [16]byte(created.ID),
          roles.UpdateRoleInput{Name: ptr("mutable_role_v2")})
      require.NoError(t, err)
      require.Equal(t, "mutable_role_v2", updated.Name)

  "FailUpdateRole returns ErrProxy"
      // withProxy(...)
```

### T-R26 — `TestUpdateRole_SystemRole_Integration`

```
Sub-tests:
  "returns ErrSystemRoleImmutable for system role"
      s, q := txStores(t)
      adminRole, err := q.GetRoleByName(ctx, "admin")
      require.NoError(t, err)
      newName := "hacked"
      _, err = s.UpdateRole(ctx, [16]byte(adminRole.ID), roles.UpdateRoleInput{Name: &newName})
      require.ErrorIs(t, err, rbac.ErrSystemRoleImmutable)
```

### T-R27 — `TestDeactivateRole_Integration`

```
Sub-tests:
  "soft-deletes non-system role; GetRoleByID confirms is_active = FALSE"
      s, q := txStores(t)
      created, _ := q.CreateRole(ctx, db.CreateRoleParams{Name: "deletable_role"})
      err := s.DeactivateRole(ctx, [16]byte(created.ID))
      require.NoError(t, err)
      row, err := q.GetRoleByID(ctx, pgtype.UUID{Bytes: [16]byte(created.ID), Valid: true})
      require.NoError(t, err)
      require.False(t, row.IsActive)

  "FailDeactivateRole returns ErrProxy"
      // withProxy(...)
```

### T-R28 — `TestDeactivateRole_SystemRole_Integration`

```
Sub-tests:
  "returns ErrSystemRoleImmutable for system role"
      s, q := txStores(t)
      adminRole, _ := q.GetRoleByName(ctx, "admin")
      err := s.DeactivateRole(ctx, [16]byte(adminRole.ID))
      require.ErrorIs(t, err, rbac.ErrSystemRoleImmutable)
```

### T-R29 — `TestGetRolePermissions_Integration`

```
Sub-tests:
  "returns permissions for admin role with access_type and scope"
      s, q := txStores(t)
      adminRole, _ := q.GetRoleByName(ctx, "admin")
      perms, err := s.GetRolePermissions(ctx, [16]byte(adminRole.ID))
      require.NoError(t, err)
      require.NotEmpty(t, perms)
      var found bool
      for _, p := range perms {
          if p.CanonicalName == "rbac:read" {
              require.NotEmpty(t, p.AccessType)
              require.NotEmpty(t, p.Scope)
              require.NotEmpty(t, p.PermissionID)
              found = true
          }
      }
      require.True(t, found, "rbac:read must be in admin role permissions")

  "result is never nil"
      s, _ := txStores(t)
      ownerRole, _ := q.GetOwnerRoleID(ctx)
      perms, err := s.GetRolePermissions(ctx, [16]byte(ownerRole))
      require.NotNil(t, perms)

  "FailGetRolePermissions returns ErrProxy"
      // withProxy(...)
```

### T-R30 — `TestAddRolePermission_Integration`

```
Sub-tests:
  "adds permission to role; subsequent duplicate is no-op"
      s, q := txStores(t)
      created, _ := q.CreateRole(ctx, db.CreateRoleParams{Name: "perm_test_role"})
      perm, _ := q.GetPermissionByCanonicalName(ctx, pgtype.Text{String: "rbac:read", Valid: true})
      ownerID, _ := q.GetOwnerRoleID(ctx)
      in := roles.AddRolePermissionInput{
          PermissionID:  [16]byte(perm.ID),
          GrantedBy:     [16]byte(ownerID),
          GrantedReason: "integration test",
          AccessType:    "direct",
          Scope:         "all",
          Conditions:    json.RawMessage("{}"),
      }
      err := s.AddRolePermission(ctx, [16]byte(created.ID), in)
      require.NoError(t, err)
      // Second call must be a silent no-op (ON CONFLICT DO NOTHING)
      err = s.AddRolePermission(ctx, [16]byte(created.ID), in)
      require.NoError(t, err)
      // Confirm the permission is on the role
      perms, _ := s.GetRolePermissions(ctx, [16]byte(created.ID))
      require.Len(t, perms, 1)
      require.Equal(t, "direct", perms[0].AccessType)

  "FailAddRolePermission returns ErrProxy"
      // withProxy(...)
```

### T-R31 — `TestRemoveRolePermission_Integration`

```
Sub-tests:
  "removes existing grant; GetRolePermissions returns empty after removal"
      s, q := txStores(t)
      created, _ := q.CreateRole(ctx, db.CreateRoleParams{Name: "remove_perm_role"})
      perm, _ := q.GetPermissionByCanonicalName(ctx, pgtype.Text{String: "rbac:read", Valid: true})
      ownerID, _ := q.GetOwnerRoleID(ctx)
      _ = q.AddRolePermission(ctx, db.AddRolePermissionParams{
          RoleID:        pgtype.UUID{Bytes: [16]byte(created.ID), Valid: true},
          PermissionID:  pgtype.UUID{Bytes: [16]byte(perm.ID), Valid: true},
          GrantedBy:     pgtype.UUID{Bytes: [16]byte(ownerID), Valid: true},
          GrantedReason: "test",
          AccessType:    db.PermissionAccessTypeDirect,
          Scope:         db.PermissionScopeAll,
          Conditions:    []byte("{}"),
      })
      err := s.RemoveRolePermission(ctx, [16]byte(created.ID), [16]byte(perm.ID))
      require.NoError(t, err)
      perms, _ := s.GetRolePermissions(ctx, [16]byte(created.ID))
      require.Empty(t, perms)

  "returns ErrRolePermissionNotFound when grant does not exist"
      s, _ := txStores(t)
      // Use random UUIDs that have no matching grant.
      err := s.RemoveRolePermission(ctx,
          rbacsharedtest.MustUUID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
          rbacsharedtest.MustUUID("11111111-2222-3333-4444-555555555555"))
      require.ErrorIs(t, err, roles.ErrRolePermissionNotFound)

  "FailRemoveRolePermission returns ErrProxy"
      _, q := txStores(t)
      _, _ = q.CreateRole(ctx, db.CreateRoleParams{Name: "proxy_test"})
      // Add a real grant so the proxy's Fail flag is the only failure path.
      // (withProxy pattern — same as permissions tests)
      _, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailRemoveRolePermission: true}).
          RemoveRolePermission(ctx,
              rbacsharedtest.MustUUID(testRoleID),
              rbacsharedtest.MustUUID(testPermID))
      require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
```

All function names must end with `_Integration` per RULES §3.8.

---

## What NOT to do in this phase

- Do not implement caching — TODO-3 in `0-design.md §16` covers this post-launch.
- Do not modify `sql/queries/rbac.sql` or seed files — those are done (Phases 1–2).
- Do not create a per-feature `testutil/` folder — all fakes live in `rbac/shared/testutil/`.
- Do not redefine `rbac.ErrSystemRoleImmutable` locally — import it from `internal/platform/rbac`.
- Do not add rate limiters — these are admin routes, JWT + RBAC gated.
- Do not write audit logs in the service layer — all role mutations are audited via DB triggers on `roles_audit` and `role_permissions_audit` automatically.
- Do not add `GetRoleByName` to the Storer interface — it is only used in tests via direct `*db.Queries` access.
- Do not use `respond.DecodeJSON` for GET/DELETE handlers — they have no request body.
- Do not check for ErrRoleNotFound in `AddRolePermission` store method — the FK violation from a bad role_id will surface as a DB error, which the handler's default case maps to 500. The service's UUID parse guard covers the actual 404 path.

---

## Gate checklist

- [ ] `go build ./internal/domain/rbac/...` — zero errors
- [ ] `go build ./internal/domain/rbac/shared/testutil/...` — zero errors (QuerierProxy compile-time check passes)
- [ ] `go vet ./internal/domain/rbac/...` — zero warnings
- [ ] `go build ./internal/server/...` — compiles after `roles.Routes` mount addition
- [ ] `go test ./internal/domain/rbac/roles/...` — handler + service unit tests pass without a DB
- [ ] `go test -tags integration_test ./internal/domain/rbac/roles/...` — T-R23 through T-R31 green
- [ ] `GET /admin/rbac/roles` returns 200 with at least 4 seeded roles
- [ ] `POST /admin/rbac/roles` creates a role and returns 201
- [ ] `GET /admin/rbac/roles/:id` returns 200; unknown ID returns 404
- [ ] `PATCH /admin/rbac/roles/:id` updates name; system role returns 409
- [ ] `DELETE /admin/rbac/roles/:id` returns 204; system role returns 409
- [ ] `GET /admin/rbac/roles/:id/permissions` returns permissions with `access_type` and `scope`
- [ ] `POST /admin/rbac/roles/:id/permissions` returns 204; second identical call is also 204 (no-op)
- [ ] `DELETE /admin/rbac/roles/:id/permissions/:perm_id` returns 204; missing grant returns 404
- [ ] No circular imports — `roles` imports `platform/rbac` and `rbacshared`; `platform/rbac` does not import `domain/rbac`
- [ ] §3.13 sub-package split checklist passed for every new file
