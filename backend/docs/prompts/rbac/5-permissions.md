# RBAC — Phase 5: Permissions Read API (`internal/domain/rbac/permissions/`)

**Feature:** RBAC
**Phase:** 5 of 10
**Depends on:** Phases 0–4 (schema ✅, queries ✅, seeds ✅, platform/rbac ✅, bootstrap ✅)
**Gate:** `go test -tags integration_test ./internal/domain/rbac/permissions/...` green — T-R32, T-R33
**Design doc:** `docs/prompts/rbac/0-design.md`
**Go version:** 1.25 — use modern idioms throughout (`any`, `min`/`max`, range-over-int, etc.)

---

## What this phase builds

```
internal/domain/rbac/
    permissions/
        handler.go              NEW
        service.go              NEW
        store.go                NEW
        models.go               NEW
        routes.go               NEW
        handler_test.go         NEW — handler unit tests (no build tag)
        service_test.go         NEW — service unit tests (no build tag)
        store_test.go           NEW — //go:build integration_test; TestMain, T-R32, T-R33

internal/domain/rbac/shared/testutil/
    fake_storer.go              MODIFY — add PermissionsFakeStorer
    fake_servicer.go            MODIFY — add PermissionsFakeServicer
    querier_proxy.go            MODIFY — add permissions Fail* flags + forwarding methods

internal/domain/rbac/routes.go  MODIFY — mount permissions sub-router in AdminRoutes
```

---

## Read before writing any code

| File | Why |
|---|---|
| `docs/prompts/rbac/0-design.md §4, §5, §8, §12` | Package structure, SQL queries, API routes 10–11, test cases T-R32/T-R33 |
| `docs/RULES.md` | §1.2 domain layout, §3.1 file layout, §3.3 layer types, §3.4 error handling, §3.8 testing, §3.13 checklist |
| `internal/db/rbac.sql.go` | Exact signatures for `GetPermissions`, `GetPermissionGroups`, `GetPermissionGroupMembers` |
| `internal/domain/rbac/shared/store.go` | `BaseStore`, `NewBaseStore`, `WithQuerier`, `ToPgtypeUUID`, `IsNoRows` |
| `internal/domain/rbac/shared/errors.go` | Shared sentinels |
| `internal/domain/rbac/shared/testutil/fake_storer.go` | BootstrapFakeStorer layout — follow exactly for PermissionsFakeStorer |
| `internal/domain/rbac/shared/testutil/fake_servicer.go` | BootstrapFakeServicer layout — follow exactly for PermissionsFakeServicer |
| `internal/domain/rbac/shared/testutil/querier_proxy.go` | Existing Fail* flags — add permissions section at the end |
| `internal/domain/rbac/shared/testutil/builders.go` | `RunTestMain`, `MustBeginTx`, `NewEmail`, `MustHashPassword` |
| `internal/domain/rbac/bootstrap/handler.go` | `mustUserID`, error switch, `respond.*` call pattern |
| `internal/domain/rbac/bootstrap/service.go` | Constructor, Storer interface, error-wrapping prefix format |
| `internal/domain/rbac/bootstrap/store.go` | `NewStore`, `WithQuerier`, `compile-time check` pattern |
| `internal/domain/rbac/bootstrap/handler_test.go` | Unit test layout with FakeServicer |
| `internal/domain/rbac/bootstrap/service_test.go` | Service unit test layout with FakeStorer |
| `internal/domain/rbac/bootstrap/store_test.go` | `txStores`, `withProxy`, integration test layout |
| `internal/domain/rbac/routes.go` | `AdminRoutes` stub — where to mount |
| `internal/platform/rbac/checker.go` | `PermRBACRead` constant |

---

## Generated DB types (reference)

```go
// GetPermissions returns all active permissions ordered by canonical_name.
func (q *Queries) GetPermissions(ctx context.Context) ([]GetPermissionsRow, error)

type GetPermissionsRow struct {
    ID            uuid.UUID
    CanonicalName pgtype.Text
    Name          string
    ResourceType  string
    Description   pgtype.Text
    IsActive      bool
    CreatedAt     time.Time
}

// GetPermissionGroups returns all active groups ordered by display_order, name.
func (q *Queries) GetPermissionGroups(ctx context.Context) ([]GetPermissionGroupsRow, error)

type GetPermissionGroupsRow struct {
    ID           uuid.UUID
    Name         string
    DisplayLabel pgtype.Text
    Icon         pgtype.Text
    ColorHex     pgtype.Text
    DisplayOrder int32
    IsVisible    bool
}

// GetPermissionGroupMembers returns active permissions belonging to a group.
func (q *Queries) GetPermissionGroupMembers(ctx context.Context, groupID pgtype.UUID) ([]GetPermissionGroupMembersRow, error)

type GetPermissionGroupMembersRow struct {
    ID            uuid.UUID
    CanonicalName pgtype.Text
    Name          string
    ResourceType  string
    Description   pgtype.Text
}
```

---

## `models.go`

Types returned by the service and marshalled directly by the handler.
These have `json:` tags because they are also the HTTP response shapes — the same
pattern used by `bootstrap.BootstrapResult`.

```go
// Package permissions provides the read-only HTTP handler, service, and store
// for listing RBAC permissions and permission groups.
package permissions

// Permission is the service-layer representation of a single RBAC permission,
// also marshalled as the JSON response element.
type Permission struct {
    ID            string `json:"id"`
    CanonicalName string `json:"canonical_name"`
    ResourceType  string `json:"resource_type"`
    Name          string `json:"name"`
    Description   string `json:"description,omitempty"`
}

// PermissionGroupMember is a slim permission summary embedded inside a
// PermissionGroup response.
type PermissionGroupMember struct {
    ID            string `json:"id"`
    CanonicalName string `json:"canonical_name"`
    ResourceType  string `json:"resource_type"`
    Name          string `json:"name"`
    Description   string `json:"description,omitempty"`
}

// PermissionGroup is the service-layer representation of a permission group
// with its members embedded, also marshalled as the JSON response element.
type PermissionGroup struct {
    ID           string                  `json:"id"`
    Name         string                  `json:"name"`
    DisplayLabel string                  `json:"display_label,omitempty"`
    Icon         string                  `json:"icon,omitempty"`
    ColorHex     string                  `json:"color_hex,omitempty"`
    DisplayOrder int32                   `json:"display_order"`
    IsVisible    bool                    `json:"is_visible"`
    Members      []PermissionGroupMember `json:"members"`
}
```

No `requests.go` — both endpoints are `GET` with no request body.
No `errors.go` — no feature-exclusive sentinel errors; only the `default` 500 path exists.
No `validators.go` — no request body to validate.

---

## `store.go`

### Storer interface (defined in `service.go` per ADR-007 — shown here for clarity)

```go
type Storer interface {
    GetPermissions(ctx context.Context) ([]Permission, error)
    GetPermissionGroups(ctx context.Context) ([]PermissionGroup, error)
}
```

### Store struct

```go
// compile-time check: *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store is the data-access implementation for the permissions package.
type Store struct {
    rbacshared.BaseStore
}

// NewStore constructs a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
    return &Store{BaseStore: rbacshared.NewBaseStore(pool)}
}

// WithQuerier returns a copy of the Store with its querier replaced by q and
// TxBound set to true. Used in integration tests to bind writes to a
// rolled-back test transaction.
func (s *Store) WithQuerier(q db.Querier) *Store {
    c := *s
    c.BaseStore = s.BaseStore.WithQuerier(q)
    return &c
}
```

### `GetPermissions`

Calls `s.Queries.GetPermissions(ctx)`. Maps each `db.GetPermissionsRow` to a
`Permission`:

```
ID            → uuid.UUID(row.ID).String()
CanonicalName → row.CanonicalName.String   (pgtype.Text.String; "" when Valid==false)
Name          → row.Name
ResourceType  → row.ResourceType
Description   → row.Description.String
```

Return an initialised empty slice (`[]Permission{}`) rather than nil when the query
returns zero rows, so the handler always marshals `[]` not `null`.

Error wrapping: `fmt.Errorf("store.GetPermissions: %w", err)`.

### `GetPermissionGroups`

1. Call `s.Queries.GetPermissionGroups(ctx)` → `[]db.GetPermissionGroupsRow`.
   On error: wrap `"store.GetPermissionGroups: groups: %w"`.

2. For each group row, call:
   ```go
   s.Queries.GetPermissionGroupMembers(ctx, pgtype.UUID{Bytes: [16]byte(g.ID), Valid: true})
   ```
   On error: wrap `"store.GetPermissionGroups: members for %s: %w", g.Name`.

3. Map each `db.GetPermissionGroupMembersRow` to a `PermissionGroupMember`.

4. Assemble `PermissionGroup`:
   ```
   ID           → uuid.UUID(g.ID).String()
   Name         → g.Name
   DisplayLabel → g.DisplayLabel.String
   Icon         → g.Icon.String
   ColorHex     → g.ColorHex.String
   DisplayOrder → g.DisplayOrder
   IsVisible    → g.IsVisible
   Members      → mapped slice (never nil — use make([]PermissionGroupMember, 0))
   ```

5. Return `[]PermissionGroup{}` (not nil) when the outer query returns zero rows.

This is an intentional N+1 pattern: at most ~30 groups in practice. Caching is
deferred to TODO-3 in `0-design.md §16`.

---

## `service.go`

### Storer interface

Defined here per ADR-007:

```go
// Storer is the data-access contract for the permissions service.
type Storer interface {
    GetPermissions(ctx context.Context) ([]Permission, error)
    GetPermissionGroups(ctx context.Context) ([]PermissionGroup, error)
}
```

### Service struct

```go
// Service implements Servicer for the permissions package.
type Service struct {
    store Storer
}

// NewService constructs a Service with the given store.
func NewService(store Storer) *Service {
    return &Service{store: store}
}

// compile-time check: Service satisfies Servicer.
var _ Servicer = (*Service)(nil)
```

### `ListPermissions`

Delegates directly to the store — no business logic:

```go
// ListPermissions returns all active RBAC permissions ordered by canonical_name.
func (s *Service) ListPermissions(ctx context.Context) ([]Permission, error) {
    perms, err := s.store.GetPermissions(ctx)
    if err != nil {
        return nil, fmt.Errorf("permissions.ListPermissions: %w", err)
    }
    return perms, nil
}
```

### `ListPermissionGroups`

```go
// ListPermissionGroups returns all active permission groups with their members embedded.
func (s *Service) ListPermissionGroups(ctx context.Context) ([]PermissionGroup, error) {
    groups, err := s.store.GetPermissionGroups(ctx)
    if err != nil {
        return nil, fmt.Errorf("permissions.ListPermissionGroups: %w", err)
    }
    return groups, nil
}
```

---

## `handler.go`

### Servicer interface

Defined here per RULES §3.3:

```go
// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests supply a PermissionsFakeServicer.
type Servicer interface {
    ListPermissions(ctx context.Context) ([]Permission, error)
    ListPermissionGroups(ctx context.Context) ([]PermissionGroup, error)
}
```

### Handler struct

```go
// Handler is the HTTP layer for the permissions package.
type Handler struct {
    svc Servicer
}

// NewHandler constructs a Handler.
func NewHandler(svc Servicer) *Handler {
    return &Handler{svc: svc}
}
```

### `ListPermissions` — handles `GET /admin/rbac/permissions`

```
1. Call h.svc.ListPermissions(r.Context()).
2. On error: log then respond.Error(w, 500, "internal_error", "internal server error").
3. On success: respond.JSON(w, 200, map[string]any{"permissions": perms}).
```

Always pass the slice from the service (already guaranteed non-nil by the store).
No `http.MaxBytesReader` — GET endpoint, no request body.
No `mustUserID` needed — `rbac.Require` middleware applied in routes.go enforces auth.

### `ListPermissionGroups` — handles `GET /admin/rbac/permissions/groups`

Same pattern; response key is `"groups"`.

### Error switch for both methods

```go
switch {
default:
    slog.ErrorContext(r.Context(), "permissions.ListPermissions: service error", "error", err)
    respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
}
```

There are no domain-specific sentinels for these read endpoints; the only non-nil
error path is a store failure (DB down, etc.) which maps to 500.

---

## `routes.go`

```go
// Routes registers GET /permissions and GET /permissions/groups on r.
// Call from AdminRoutes in internal/domain/rbac/routes.go:
//
//	permissions.Routes(ctx, r, deps)
//
// Both routes require a valid JWT and the rbac:read permission.
// No additional rate limiter — admin routes are already JWT-gated.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps)
```

Wiring:

```go
store := NewStore(deps.Pool)
svc   := NewService(store)
h     := NewHandler(svc)

r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
    Get("/permissions", h.ListPermissions)

r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermRBACRead)).
    Get("/permissions/groups", h.ListPermissionGroups)
```

`deps.RBAC` is `*rbac.Checker` (wired in Phase 3). `rbac.PermRBACRead` is the
constant `"rbac:read"` from `internal/platform/rbac/checker.go`. No rate limiter
goroutine is started (no limiter for these endpoints).

---

## `internal/domain/rbac/routes.go` — modification

Mount the permissions sub-router inside `AdminRoutes`. The import block already
exists from Phase 4; add:

```go
import "github.com/7-Dany/store/backend/internal/domain/rbac/permissions"

func AdminRoutes(ctx context.Context, deps *app.Deps) *chi.Mux {
    r := chi.NewRouter()
    r.Use(chimiddleware.AllowContentType("application/json"))
    permissions.Routes(ctx, r, deps)
    // Phases 6–9 will mount here.
    return r
}
```

---

## Testutil updates (sync S-2 — must be done atomically with the store)

### `shared/testutil/fake_storer.go` — add `PermissionsFakeStorer`

Append after the bootstrap section. Follow the `BootstrapFakeStorer` layout exactly.

```go
// ─────────────────────────────────────────────────────────────────────────────
// PermissionsFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// PermissionsFakeStorer is a hand-written implementation of permissions.Storer
// for service unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns a safe default so tests only configure the fields they need.
//
// Defaults are chosen so that the happy path succeeds without any configuration:
//   - GetPermissions      → ([]permissions.Permission{}, nil)
//   - GetPermissionGroups → ([]permissions.PermissionGroup{}, nil)
type PermissionsFakeStorer struct {
    GetPermissionsFn      func(ctx context.Context) ([]permissions.Permission, error)
    GetPermissionGroupsFn func(ctx context.Context) ([]permissions.PermissionGroup, error)
}

// compile-time interface check.
var _ permissions.Storer = (*PermissionsFakeStorer)(nil)

// GetPermissions delegates to GetPermissionsFn if set.
// Default: returns ([]permissions.Permission{}, nil).
func (f *PermissionsFakeStorer) GetPermissions(ctx context.Context) ([]permissions.Permission, error) {
    if f.GetPermissionsFn != nil {
        return f.GetPermissionsFn(ctx)
    }
    return []permissions.Permission{}, nil
}

// GetPermissionGroups delegates to GetPermissionGroupsFn if set.
// Default: returns ([]permissions.PermissionGroup{}, nil).
func (f *PermissionsFakeStorer) GetPermissionGroups(ctx context.Context) ([]permissions.PermissionGroup, error) {
    if f.GetPermissionGroupsFn != nil {
        return f.GetPermissionGroupsFn(ctx)
    }
    return []permissions.PermissionGroup{}, nil
}
```

Add the import `"github.com/7-Dany/store/backend/internal/domain/rbac/permissions"`.

### `shared/testutil/fake_servicer.go` — add `PermissionsFakeServicer`

```go
// ─────────────────────────────────────────────────────────────────────────────
// PermissionsFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// PermissionsFakeServicer is a hand-written implementation of permissions.Servicer
// for handler unit tests. Set the Fn fields to control responses; leave nil
// to return an empty slice and nil error.
type PermissionsFakeServicer struct {
    ListPermissionsFn      func(ctx context.Context) ([]permissions.Permission, error)
    ListPermissionGroupsFn func(ctx context.Context) ([]permissions.PermissionGroup, error)
}

// compile-time interface check.
var _ permissions.Servicer = (*PermissionsFakeServicer)(nil)

// ListPermissions delegates to ListPermissionsFn if set.
// Default: returns ([]permissions.Permission{}, nil).
func (f *PermissionsFakeServicer) ListPermissions(ctx context.Context) ([]permissions.Permission, error) {
    if f.ListPermissionsFn != nil {
        return f.ListPermissionsFn(ctx)
    }
    return []permissions.Permission{}, nil
}

// ListPermissionGroups delegates to ListPermissionGroupsFn if set.
// Default: returns ([]permissions.PermissionGroup{}, nil).
func (f *PermissionsFakeServicer) ListPermissionGroups(ctx context.Context) ([]permissions.PermissionGroup, error) {
    if f.ListPermissionGroupsFn != nil {
        return f.ListPermissionGroupsFn(ctx)
    }
    return []permissions.PermissionGroup{}, nil
}
```

### `shared/testutil/querier_proxy.go` — add permissions section

Add the following Fail* fields to the `QuerierProxy` struct under a new section separator:

```go
// ── permissions ───────────────────────────────────────────────────────────────
FailGetPermissions           bool
FailGetPermissionGroups      bool
FailGetPermissionGroupMembers bool
```

Add the three forwarding methods:

```go
func (p *QuerierProxy) GetPermissions(ctx context.Context) ([]db.GetPermissionsRow, error) {
    if p.FailGetPermissions {
        return nil, ErrProxy
    }
    return p.Querier.GetPermissions(ctx)
}

func (p *QuerierProxy) GetPermissionGroups(ctx context.Context) ([]db.GetPermissionGroupsRow, error) {
    if p.FailGetPermissionGroups {
        return nil, ErrProxy
    }
    return p.Querier.GetPermissionGroups(ctx)
}

func (p *QuerierProxy) GetPermissionGroupMembers(ctx context.Context, groupID pgtype.UUID) ([]db.GetPermissionGroupMembersRow, error) {
    if p.FailGetPermissionGroupMembers {
        return nil, ErrProxy
    }
    return p.Querier.GetPermissionGroupMembers(ctx, groupID)
}
```

After these additions run:
```bash
go build ./internal/domain/rbac/shared/testutil/...
```
to confirm the `var _ db.Querier = (*QuerierProxy)(nil)` compile-time check passes.

---

## `handler_test.go` — handler unit tests

```go
package permissions_test
```

No `//go:build` tag. Uses `PermissionsFakeServicer`. Tests are parallel.

### Helper

```go
func newTestHandler(svc permissions.Servicer) *permissions.Handler {
    return permissions.NewHandler(svc)
}
```

### `TestHandler_ListPermissions`

| Sub-test | Setup | Assert |
|---|---|---|
| returns 200 with permissions key | `ListPermissionsFn` returns 2 Permission items | 200, `body["permissions"]` is a 2-item array |
| empty slice marshals as `[]` not null | `ListPermissionsFn` returns `[]Permission{}` | 200, `body["permissions"]` is an empty JSON array |
| service error returns 500 | `ListPermissionsFn` returns `errors.New("db error")` | 500, `code == "internal_error"` |

### `TestHandler_ListPermissionGroups`

| Sub-test | Setup | Assert |
|---|---|---|
| returns 200 with groups key | `ListPermissionGroupsFn` returns 2 PermissionGroup items | 200, `body["groups"]` is a 2-item array |
| empty slice marshals as `[]` not null | `ListPermissionGroupsFn` returns `[]PermissionGroup{}` | 200, `body["groups"]` is an empty JSON array |
| service error returns 500 | `ListPermissionGroupsFn` returns `errors.New("db error")` | 500, `code == "internal_error"` |

Use `httptest.NewRequest(http.MethodGet, "/permissions", nil)` — no request body.
No auth context injection needed: handler does not call `mustUserID`; the
`rbac.Require` middleware is applied in routes.go, not in the handler itself.

---

## `service_test.go` — service unit tests

```go
package permissions_test
```

No `//go:build` tag. Uses `PermissionsFakeStorer`. Tests are parallel.

### `TestService_ListPermissions`

| Sub-test | Setup | Assert |
|---|---|---|
| delegates to store and returns result | `GetPermissionsFn` returns 3 Permission items | returned slice has length 3, nil error |
| store error is wrapped and returned | `GetPermissionsFn` returns `errors.New("db down")` | `errors.Is(err, dbErr)`, err message contains `"permissions.ListPermissions:"` |

### `TestService_ListPermissionGroups`

| Sub-test | Setup | Assert |
|---|---|---|
| delegates to store and returns result | `GetPermissionGroupsFn` returns 2 PermissionGroup items | returned slice has length 2, nil error |
| store error is wrapped and returned | `GetPermissionGroupsFn` returns `errors.New("db down")` | `errors.Is(err, dbErr)`, err message contains `"permissions.ListPermissionGroups:"` |

---

## `store_test.go` — store integration tests

```go
//go:build integration_test

package permissions_test
```

### Boilerplate

```go
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
    rbacsharedtest.RunTestMain(m, &testPool, 20)
}

// txStores begins a rolled-back transaction and returns a Store bound to it
// alongside *db.Queries for direct assertion queries. Skips when testPool is nil.
func txStores(t *testing.T) (*permissions.Store, *db.Queries) {
    t.Helper()
    if testPool == nil {
        t.Skip("no test database configured")
    }
    _, q := rbacsharedtest.MustBeginTx(t, testPool)
    return permissions.NewStore(testPool).WithQuerier(q), q
}

// withProxy wires q into proxy.Querier and returns a Store bound to it.
func withProxy(q db.Querier, proxy *rbacsharedtest.QuerierProxy) *permissions.Store {
    proxy.Querier = q
    return permissions.NewStore(testPool).WithQuerier(proxy)
}
```

### T-R32 — `TestGetPermissions_Integration`

**Scenario:** Seeds are loaded by the test database. Assert the store returns
all 13 seeded permissions.

```
Sub-tests:
  "returns all seeded permissions"
      s, _ := txStores(t)
      perms, err := s.GetPermissions(ctx)
      require.NoError
      require.Len(perms, 13)
      // Spot-check
      var found bool
      for _, p := range perms {
          if p.CanonicalName == "rbac:read" {
              require.Equal(t, "rbac", p.ResourceType)
              require.NotEmpty(t, p.ID)
              found = true
          }
      }
      require.True(t, found, "rbac:read must be present in seeded permissions")

  "result slice is never nil"
      s, _ := txStores(t)
      perms, err := s.GetPermissions(ctx)
      require.NoError
      require.NotNil(t, perms)

  "FailGetPermissions returns ErrProxy"
      _, q := txStores(t)
      _, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetPermissions: true}).
          GetPermissions(ctx)
      require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
```

### T-R33 — `TestGetPermissionGroups_Integration`

**Scenario:** Seeds are loaded. Assert the store returns all 5 groups with members.

```
Sub-tests:
  "returns all 5 seeded groups with members"
      s, _ := txStores(t)
      groups, err := s.GetPermissionGroups(ctx)
      require.NoError
      require.Len(groups, 5)
      // Spot-check System Administration group
      var sysAdmin *permissions.PermissionGroup
      for i := range groups {
          if groups[i].Name == "System Administration" {
              sysAdmin = &groups[i]
          }
      }
      require.NotNil(t, sysAdmin, "System Administration group must be present")
      require.Len(t, sysAdmin.Members, 3)
      // Total members across all groups == 13
      total := 0
      for _, g := range groups {
          total += len(g.Members)
      }
      require.Equal(t, 13, total)

  "members slice is never nil for any group"
      s, _ := txStores(t)
      groups, err := s.GetPermissionGroups(ctx)
      require.NoError
      for _, g := range groups {
          require.NotNil(t, g.Members,
              "Members must not be nil for group %q", g.Name)
      }

  "result slice is never nil"
      s, _ := txStores(t)
      groups, err := s.GetPermissionGroups(ctx)
      require.NoError
      require.NotNil(t, groups)

  "FailGetPermissionGroups returns ErrProxy"
      _, q := txStores(t)
      _, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetPermissionGroups: true}).
          GetPermissionGroups(ctx)
      require.ErrorIs(t, err, rbacsharedtest.ErrProxy)

  "FailGetPermissionGroupMembers returns ErrProxy"
      _, q := txStores(t)
      _, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetPermissionGroupMembers: true}).
          GetPermissionGroups(ctx)
      require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
```

Function names must end with `_Integration` per RULES §3.8:
`TestGetPermissions_Integration`, `TestGetPermissionGroups_Integration`.

---

## What NOT to do in this phase

- Do not add write endpoints — this package is read-only.
- Do not add `validators.go` — no request body.
- Do not add `errors.go` — no feature-exclusive sentinel errors.
- Do not add `requests.go` — no request body; GET response shapes are in `models.go`.
- Do not add rate limiters — the JWT + `rbac.Require` middleware is the access gate.
- Do not implement caching — TODO-3 in `0-design.md §16` covers this post-launch.
- Do not modify `sql/queries/rbac.sql` or seed files — those are done (Phase 1–2).
- Do not create a per-feature `testutil/` folder — all fakes live in `rbac/shared/testutil/`.
- Do not import `internal/domain/auth/shared` directly — use `rbacshared` for shared sentinels.
- Do not import `authsharedtest` — use `rbacsharedtest` (`internal/domain/rbac/shared/testutil`).

---

## Gate checklist

- [ ] `go build ./internal/domain/rbac/...` — zero errors
- [ ] `go build ./internal/domain/rbac/shared/testutil/...` — zero errors (QuerierProxy compile-time check passes)
- [ ] `go vet ./internal/domain/rbac/...` — zero warnings
- [ ] `go build ./internal/server/...` — compiles after AdminRoutes mount addition
- [ ] `go test ./internal/domain/rbac/permissions/...` — unit tests (handler + service) pass without a DB
- [ ] `go test -tags integration_test ./internal/domain/rbac/permissions/...` — T-R32 and T-R33 green
- [ ] `GET /admin/rbac/permissions` returns 200 with 13 permissions on a seeded DB
- [ ] `GET /admin/rbac/permissions/groups` returns 200 with 5 groups each containing members
- [ ] No circular imports — permissions imports `platform/rbac` (for `PermRBACRead`) and `rbacshared`; `platform/rbac` does not import `domain/rbac`
- [ ] §3.13 sub-package split checklist passed for every new file
