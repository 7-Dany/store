# RBAC — Phase 8: Permission Capability Flags

**Feature:** RBAC
**Phase:** 8 of 10
**Depends on:** Phases 0–7 (schema ✅, queries ✅, seeds ✅, platform/rbac ✅, bootstrap ✅, permissions ✅, roles ✅, audit-fixes ✅)
**Gate:** `go test -tags integration_test ./internal/domain/rbac/permissions/...` — T-R28 through T-R29 + new test IDs from this phase all green
**Design doc:** `docs/prompts/rbac/0-design.md`
**Audit source:** senior architecture review — items 5, 7, 10 resolved here
**Go version:** 1.25

---

## What this phase builds

Three capability columns on `permissions`, a new `GetPermissionByID` query, updated
`GetPermissions` and `GetPermissionGroupMembers` queries, updated seeds, a capability
validation step in `AddRolePermission`, and a `capabilities` object in the permissions
API response — so the admin UI knows what `access_type` and `scope` values are valid
for each permission without having to know the business rules client-side.

This also closes the three architecture findings that were blocked on this schema work:

| Finding | Resolution |
|---|---|
| 5 — `scope` stored for non-scoped permissions | `scope_policy = 'none'` → service writes `scope = 'all'` as neutral; validated at grant time |
| 7 — `allow_conditional` without template unconstrained | `allow_conditional` flag must be TRUE to use `access_type = 'conditional'` |
| 10 — `scope` default `'own'` silently wrong for meaningful permissions | `scope_policy = 'any'` permissions require explicit `scope`; validator rejects omission |

---

## Read before writing any code

| File | Why |
|---|---|
| `docs/prompts/rbac/0-design.md` | Full design; §7 type contracts, §12 test cases |
| `sql/schema/003_rbac.sql` | `permissions` table being extended; `role_permissions` constraints |
| `sql/queries/rbac.sql` | Existing queries; three are modified, one is new |
| `sql/seeds/002_permissions.sql` | `permissions` INSERT — adding 3 new columns |
| `sql/seeds/003_roles.sql` | `role_permissions` INSERT — must satisfy capability constraints after this phase |
| `internal/domain/rbac/permissions/models.go` | `Permission`, `PermissionGroupMember` — adding `Capabilities` |
| `internal/domain/rbac/permissions/requests.go` | Response types — adding `CapabilitiesResponse` |
| `internal/domain/rbac/permissions/handler.go` | Response mapper — adding capabilities field |
| `internal/domain/rbac/permissions/store.go` | Mapping new columns from query results |
| `internal/domain/rbac/roles/store.go` | `AddRolePermission` — capability validation |
| `internal/domain/rbac/roles/service.go` | `Storer` interface — new `GetPermissionCaps` method |
| `internal/domain/rbac/roles/errors.go` | Two new sentinels |
| `internal/domain/rbac/roles/handler.go` | Two new error cases |
| `internal/domain/rbac/shared/testutil/fake_storer.go` | `RolesFakeStorer` — new Fn field |
| `docs/prompts/rbac/7-audit-fixes.md` | TODO-4 — condition template enforcement (still deferred; note in this phase's TODO) |

---

## Execution order

Work in this exact sequence. Each step must compile before moving to the next.

```
1. sql/schema/003_rbac.sql          — add enum + 3 columns
2. sql/queries/rbac.sql             — new GetPermissionByID; update GetPermissions + GetPermissionGroupMembers
3. make sqlc                        — regenerate internal/db/
4. sql/seeds/002_permissions.sql    — add capability columns to INSERT
5. roles/errors.go                  — 2 new sentinels
6. roles/models.go                  — PermissionCaps struct
7. roles/service.go                 — Storer.GetPermissionCaps; AddRolePermission validation
8. roles/store.go                   — implement GetPermissionCaps
9. roles/handler.go                 — 2 new error cases
10. shared/testutil/fake_storer.go  — GetPermissionCapsFn
11. permissions/models.go           — Capabilities field on Permission + PermissionGroupMember
12. permissions/requests.go         — CapabilitiesResponse + PermissionCapabilitiesResponse
13. permissions/store.go            — map 3 new columns
14. permissions/handler.go          — include capabilities in response mappers
15. Tests
16. Docs
```

---

## Step 1 — `sql/schema/003_rbac.sql` — edit directly

**Edit `sql/schema/003_rbac.sql` in-place. Do not create a new migration file.**
All additions below are inserted directly into the existing Up block.
The Down block must also be updated in the same file.

### New enum

Add after the existing `permission_scope` enum definition and before the POLICY
CONSTANTS block:

```sql
-- Controls whether and how scope applies to a permission.
-- Used by the admin UI and AddRolePermission validator to determine which scope
-- values are valid for a given permission.
CREATE TYPE permission_scope_policy AS ENUM (
    'none', -- scope is not applicable (rbac:manage, job_queue:configure, etc.)
    'own',  -- only scope = 'own' is valid
    'all',  -- only scope = 'all' is valid
    'any'   -- both 'own' and 'all' are valid (e.g. product:manage)
);

COMMENT ON TYPE permission_scope_policy IS
    'Declares which scope values are valid when creating a grant for this permission. '
    '''none'' = scope field is not meaningful; ''any'' = caller chooses own or all.';
```

### New columns on `permissions`

In the same file, add after the existing `is_active` column definition, before `created_at`:

```sql
-- ── Capability flags ──────────────────────────────────────────────────────────

-- Declares which scope values make sense for this permission.
-- 'none'  = scope is not applicable (e.g. rbac:manage has no 'own' concept)
-- 'own'   = only 'own' scope is valid
-- 'all'   = only 'all' scope is valid
-- 'any'   = caller may choose 'own' or 'all' (e.g. product:manage)
-- The AddRolePermission service validates incoming scope against this.
-- When scope_policy = 'none', the service writes scope = 'all' as a neutral value.
scope_policy permission_scope_policy NOT NULL DEFAULT 'none',

-- TRUE if access_type = 'conditional' is allowed for grants of this permission.
-- FALSE (default) means the conditional approval path is disabled and the
-- permission_condition_templates table is not consulted.
-- Must match the presence of a permission_condition_templates row (see TODO-4).
allow_conditional BOOLEAN NOT NULL DEFAULT FALSE,

-- TRUE if access_type = 'request' is allowed for grants of this permission.
-- FALSE (default) means the approval-request path is disabled.
-- Requires a permission_request_approvers row before any grant can use it
-- (enforced in the service layer, not at the DB level in V1).
allow_request BOOLEAN NOT NULL DEFAULT FALSE,
```

### Down block (same file)

Add `DROP TYPE IF EXISTS permission_scope_policy CASCADE;` to the existing Down block, after
`DROP TYPE IF EXISTS permission_scope CASCADE;`.

### No new indexes

The three capability columns are write-once metadata read only at grant-creation time
(`AddRolePermission`) and at list time (`GetPermissions`). A full-table scan on 13
rows is faster than any index. No indexes added.

---

## Step 2 — `sql/queries/rbac.sql` — edit directly

**Edit `sql/queries/rbac.sql` in-place. Do not create a new file.**

### New query — `GetPermissionByID`

Add in the `── Permissions ──` section, after `GetPermissionByCanonicalName`:

```sql
-- name: GetPermissionByID :one
-- Returns the capability flags for a single permission by primary key.
-- Used by AddRolePermission to validate access_type and scope before inserting.
SELECT id, canonical_name, scope_policy, allow_conditional, allow_request
FROM   permissions
WHERE  id        = @id::uuid
  AND  is_active = TRUE;
```

### Update — `GetPermissions` (same file)

Find the existing `GetPermissions` query and add the three new columns to the SELECT list:

```sql
-- name: GetPermissions :many
SELECT id, canonical_name, name, resource_type, description,
       scope_policy, allow_conditional, allow_request,
       is_active, created_at
FROM   permissions
WHERE  is_active = TRUE
ORDER  BY canonical_name;
```

### Update — `GetPermissionGroupMembers` (same file)

Find the existing `GetPermissionGroupMembers` query and add the three new columns to the SELECT list:

```sql
-- name: GetPermissionGroupMembers :many
SELECT p.id, p.canonical_name, p.name, p.resource_type, p.description,
       p.scope_policy, p.allow_conditional, p.allow_request
FROM   permission_group_members pgm
JOIN   permissions p ON p.id = pgm.permission_id
WHERE  pgm.group_id = @group_id::uuid
  AND  p.is_active  = TRUE
ORDER  BY p.canonical_name;
```

---

## Step 3 — `make sqlc`

Run `make sqlc` after Steps 1 and 2 are complete. The generated `internal/db/` code
must include:

- `GetPermissionByIDRow` with fields `ID`, `CanonicalName`, `ScopePolicy`,
  `AllowConditional`, `AllowRequest`
- Updated `GetPermissionsRow` with `ScopePolicy`, `AllowConditional`, `AllowRequest`
- Updated `GetPermissionGroupMembersRow` with the same three fields

Do not proceed to Step 4 until `go build ./internal/db/...` passes.

---

## Step 4 — `sql/seeds/002_permissions.sql` — edit directly

**Edit `sql/seeds/002_permissions.sql` in-place. Replace the existing permissions INSERT block.**

### Capability matrix

| Permission | scope_policy | allow_conditional | allow_request |
|---|---|---|---|
| `rbac:read` | `none` | false | false |
| `rbac:manage` | `none` | false | false |
| `rbac:grant_user_permission` | `none` | false | false |
| `job_queue:read` | `none` | false | false |
| `job_queue:manage` | `none` | false | false |
| `job_queue:configure` | `none` | false | true |
| `user:read` | `none` | false | false |
| `user:manage` | `none` | false | false |
| `user:lock` | `none` | false | true |
| `request:read` | `none` | false | false |
| `request:manage` | `none` | false | false |
| `request:approve` | `none` | false | false |
| `product:manage` | `any` | true | true |

### Updated INSERT

Replace the existing `INSERT INTO permissions` block with one that includes the three
new columns. The existing `ON CONFLICT (canonical_name) DO NOTHING` must change to
`DO UPDATE` so re-running seeds backfills existing rows:

```sql
INSERT INTO permissions (name, resource_type, description, scope_policy, allow_conditional, allow_request)
VALUES
    ('read',                 'rbac',       'List roles, permissions, user assignments, and audit logs',                              'none', FALSE, FALSE),
    ('manage',               'rbac',       'Create/update/soft-delete roles; add/remove role permissions; assign/remove user roles', 'none', FALSE, FALSE),
    ('grant_user_permission','rbac',       'Grant/revoke time-limited direct permissions on individual users',                       'none', FALSE, FALSE),
    ('read',                 'job_queue',  'View jobs, workers, queues, schedules, stats, metrics, and WS stream',                  'none', FALSE, FALSE),
    ('manage',               'job_queue',  'Cancel jobs, retry dead/failed jobs, update job priority, purge dead jobs',             'none', FALSE, FALSE),
    ('configure',            'job_queue',  'Pause/resume job kinds, force-drain workers, create/update/delete/trigger schedules',   'none', FALSE, TRUE),
    ('read',                 'user',       'List users, view profiles, view audit and login history',                               'none', FALSE, FALSE),
    ('manage',               'user',       'Edit user details (email, name, etc.)',                                                 'none', FALSE, FALSE),
    ('lock',                 'user',       'Admin-lock and admin-unlock a user account (admin_locked field)',                       'none', FALSE, TRUE),
    ('read',                 'request',    'View requests and their history and status',                                            'none', FALSE, FALSE),
    ('manage',               'request',    'Create/edit/cancel requests; manage lifecycle non-approval steps',                     'none', FALSE, FALSE),
    ('approve',              'request',    'Approve or reject a pending request',                                                   'none', FALSE, FALSE),
    ('manage',               'product',    'Create/update/delete products (placeholder for store domain)',                         'any',  TRUE,  TRUE)
ON CONFLICT (canonical_name) DO UPDATE SET
    scope_policy      = EXCLUDED.scope_policy,
    allow_conditional = EXCLUDED.allow_conditional,
    allow_request     = EXCLUDED.allow_request;
```

**Why `DO UPDATE`:** re-running `make seed` must backfill existing rows with the
correct capability values. `DO NOTHING` on a re-run would silently leave existing
rows with `scope_policy = 'none'` defaults on all permissions, breaking the
capability check for `product:manage`.

Leave the permission_groups and permission_group_members INSERTs unchanged.

---

## Step 5 — `roles/errors.go`

Add two new sentinels after the existing ones:

```go
// ErrAccessTypeNotAllowed is returned when AddRolePermission receives an
// access_type that the permission's capability flags do not permit.
// E.g. access_type = 'conditional' when allow_conditional = FALSE.
var ErrAccessTypeNotAllowed = errors.New("access_type is not permitted for this permission")

// ErrScopeNotAllowed is returned when AddRolePermission receives a scope value
// that the permission's scope_policy does not permit.
// E.g. scope = 'own' when scope_policy = 'all'.
var ErrScopeNotAllowed = errors.New("scope is not permitted for this permission")
```

---

## Step 6 — `roles/models.go`

Add `PermissionCaps` struct after the existing model definitions:

```go
// PermissionCaps carries the capability flags for a single permission.
// Returned by GetPermissionCaps and used by AddRolePermission to validate
// that the incoming access_type and scope are legal for this permission.
type PermissionCaps struct {
    ID               [16]byte
    CanonicalName    string
    ScopePolicy      string // "none" | "own" | "all" | "any"
    AllowConditional bool
    AllowRequest     bool
}
```

---

## Step 7 — `roles/service.go`

### `Storer` interface — add one method

```go
// GetPermissionCaps returns the capability flags for a permission by ID.
// Used by AddRolePermission to validate access_type and scope before inserting.
// Returns ErrPermissionNotFound when the permission does not exist or is inactive.
GetPermissionCaps(ctx context.Context, permissionID [16]byte) (PermissionCaps, error)
```

### `AddRolePermission` — add capability validation

After the existing UUID parse and before the `s.store.AddRolePermission` call, add
capability validation. The full updated method:

```go
func (s *Service) AddRolePermission(ctx context.Context, roleID string, in AddRolePermissionInput) error {
    rid, err := parseID(roleID)
    if err != nil {
        return ErrRoleNotFound
    }

    // Fetch capability flags — validates the permission exists and is active.
    caps, err := s.store.GetPermissionCaps(ctx, in.PermissionID)
    if err != nil {
        return fmt.Errorf("roles.AddRolePermission: %w", err)
    }

    // Validate access_type against capability flags.
    // 'direct' and 'denied' are always allowed — no flag needed.
    switch in.AccessType {
    case "conditional":
        if !caps.AllowConditional {
            return ErrAccessTypeNotAllowed
        }
    case "request":
        if !caps.AllowRequest {
            return ErrAccessTypeNotAllowed
        }
    }

    // Validate and normalise scope against scope_policy.
    switch caps.ScopePolicy {
    case "none":
        // Scope is not meaningful — silently normalise to 'all' so the DB
        // holds a consistent value rather than whatever the caller passed in.
        // The admin UI should hide the scope field entirely when scope_policy = 'none'.
        in.Scope = "all"
    case "own":
        if in.Scope != "own" {
            return ErrScopeNotAllowed
        }
    case "all":
        if in.Scope != "all" {
            return ErrScopeNotAllowed
        }
    case "any":
        // Both 'own' and 'all' are valid — pass through.
        // The existing ErrInvalidScope validator in the handler already ensures
        // the value is one of the two legal options before we reach here.
    }

    if err := s.store.AddRolePermission(ctx, rid, in); err != nil {
        return fmt.Errorf("roles.AddRolePermission: %w", err)
    }
    return nil
}
```

---

## Step 8 — `roles/store.go`

### Implement `GetPermissionCaps`

```go
// GetPermissionCaps returns capability flags for a permission by ID.
// Returns ErrPermissionNotFound when the permission does not exist or is inactive.
func (s *Store) GetPermissionCaps(ctx context.Context, permissionID [16]byte) (PermissionCaps, error) {
    row, err := s.Queries.GetPermissionByID(ctx, s.ToPgtypeUUID(permissionID))
    if err != nil {
        if s.IsNoRows(err) {
            return PermissionCaps{}, ErrPermissionNotFound
        }
        return PermissionCaps{}, fmt.Errorf("store.GetPermissionCaps: %w", err)
    }
    return PermissionCaps{
        ID:               [16]byte(row.ID),
        CanonicalName:    row.CanonicalName,
        ScopePolicy:      string(row.ScopePolicy),
        AllowConditional: row.AllowConditional,
        AllowRequest:     row.AllowRequest,
    }, nil
}
```

The generated `GetPermissionByIDRow.ScopePolicy` field is a `db.PermissionScopePolicy`
(a `string`-underlying type). Cast it to `string` directly.

---

## Step 9 — `roles/handler.go`

Add two new error cases to `AddRolePermission`'s error switch, before the `default:`
branch:

```go
case errors.Is(err, ErrAccessTypeNotAllowed):
    respond.Error(w, http.StatusUnprocessableEntity, "access_type_not_allowed",
        "access_type is not permitted for this permission")
case errors.Is(err, ErrScopeNotAllowed):
    respond.Error(w, http.StatusUnprocessableEntity, "scope_not_allowed",
        "scope is not permitted for this permission")
```

Use 422 (`StatusUnprocessableEntity`) — this is a semantic validation failure, not a
missing resource.

---

## Step 10 — `shared/testutil/fake_storer.go`

Add `GetPermissionCapsFn` to `RolesFakeStorer`, following the existing Fn field
pattern:

```go
// In the RolesFakeStorer struct:
GetPermissionCapsFn func(ctx context.Context, permissionID [16]byte) (roles.PermissionCaps, error)

// Forwarding method:
func (f *RolesFakeStorer) GetPermissionCaps(ctx context.Context, permissionID [16]byte) (roles.PermissionCaps, error) {
    if f.GetPermissionCapsFn != nil {
        return f.GetPermissionCapsFn(ctx, permissionID)
    }
    return roles.PermissionCaps{}, nil
}
```

Update the compile-time check `var _ roles.Storer = (*RolesFakeStorer)(nil)` — it
must still pass.

---

## Step 11 — `permissions/models.go`

Add `Capabilities` to both `Permission` and `PermissionGroupMember`. Also add the new
`PermissionCapabilities` struct:

```go
// PermissionCapabilities carries the capability flags for a permission.
// Consumed by the admin UI to determine which access_type and scope values
// are valid for this permission when creating a role grant.
type PermissionCapabilities struct {
    // ScopePolicy declares which scope values are valid for grants of this permission.
    // "none"  = scope is not applicable; the scope field should be hidden in the UI.
    // "own"   = only 'own' is valid; pre-fill and lock the scope field.
    // "all"   = only 'all' is valid; pre-fill and lock the scope field.
    // "any"   = both 'own' and 'all' are valid; show scope selector.
    ScopePolicy string

    // AccessTypes is the ordered list of access_type values this permission allows.
    // Always includes "direct" and "denied".
    // Includes "conditional" when allow_conditional = TRUE.
    // Includes "request" when allow_request = TRUE.
    AccessTypes []string
}

// Permission is the service-layer representation of a single RBAC permission.
type Permission struct {
    ID            string
    CanonicalName string
    ResourceType  string
    Name          string
    Description   string
    Capabilities  PermissionCapabilities
}

// PermissionGroupMember is a slim permission summary embedded inside a PermissionGroup.
type PermissionGroupMember struct {
    ID            string
    CanonicalName string
    ResourceType  string
    Name          string
    Description   string
    Capabilities  PermissionCapabilities
}

// PermissionGroup is the service-layer representation of a permission group
// with its member permissions embedded.
// (unchanged — no capabilities field at the group level)
type PermissionGroup struct {
    ID           string
    Name         string
    DisplayLabel string
    Icon         string
    ColorHex     string
    DisplayOrder int32
    IsVisible    bool
    Members      []PermissionGroupMember
}
```

### `buildCapabilities` helper

Add this unexported helper in `models.go` (or a new `capabilities.go` if preferred):

```go
// buildCapabilities constructs a PermissionCapabilities from raw capability
// columns returned by the DB. Called from both GetPermissions and
// GetPermissionGroupMembers store methods.
func buildCapabilities(scopePolicy string, allowConditional, allowRequest bool) PermissionCapabilities {
    types := []string{"direct"}
    if allowConditional {
        types = append(types, "conditional")
    }
    if allowRequest {
        types = append(types, "request")
    }
    types = append(types, "denied")
    return PermissionCapabilities{
        ScopePolicy: scopePolicy,
        AccessTypes: types,
    }
}
```

`AccessTypes` always starts with `"direct"` and ends with `"denied"`. `"conditional"`
and `"request"` are inserted in that order between them when their flags are true.

---

## Step 12 — `permissions/requests.go`

Add two new response types and update the existing ones:

```go
// PermissionCapabilitiesResponse is the JSON form of PermissionCapabilities.
// Consumed by the admin UI when building the AddRolePermission form.
type PermissionCapabilitiesResponse struct {
    // ScopePolicy: "none" | "own" | "all" | "any".
    // "none"  → hide scope field in the UI.
    // "own"   → pre-fill scope = 'own' and lock (read-only).
    // "all"   → pre-fill scope = 'all' and lock (read-only).
    // "any"   → show scope selector with both options.
    ScopePolicy string `json:"scope_policy"`

    // AccessTypes: always contains "direct" and "denied".
    // Contains "conditional" when the permission allows conditional grants.
    // Contains "request" when the permission requires approval.
    AccessTypes []string `json:"access_types"`
}

// Updated PermissionResponse — add Capabilities field.
type PermissionResponse struct {
    ID            string                         `json:"id"`
    CanonicalName string                         `json:"canonical_name"`
    ResourceType  string                         `json:"resource_type"`
    Name          string                         `json:"name"`
    Description   string                         `json:"description,omitempty"`
    Capabilities  PermissionCapabilitiesResponse `json:"capabilities"`
}

// Updated PermissionGroupMemberResponse — add Capabilities field.
type PermissionGroupMemberResponse struct {
    ID            string                         `json:"id"`
    CanonicalName string                         `json:"canonical_name"`
    ResourceType  string                         `json:"resource_type"`
    Name          string                         `json:"name"`
    Description   string                         `json:"description,omitempty"`
    Capabilities  PermissionCapabilitiesResponse `json:"capabilities"`
}

// PermissionGroupResponse — unchanged.
type PermissionGroupResponse struct {
    ID           string                          `json:"id"`
    Name         string                          `json:"name"`
    DisplayLabel string                          `json:"display_label,omitempty"`
    Icon         string                          `json:"icon,omitempty"`
    ColorHex     string                          `json:"color_hex,omitempty"`
    DisplayOrder int32                           `json:"display_order"`
    IsVisible    bool                            `json:"is_visible"`
    Members      []PermissionGroupMemberResponse `json:"members"`
}
```

---

## Step 13 — `permissions/store.go`

### `GetPermissions` — map new columns

Update the mapping loop to include capabilities:

```go
// In GetPermissions store method, inside the mapping loop:
out[i] = Permission{
    ID:            row.ID.String(),
    CanonicalName: row.CanonicalName,
    Name:          row.Name,
    ResourceType:  row.ResourceType,
    Description:   row.Description.String,
    Capabilities:  buildCapabilities(
        string(row.ScopePolicy),
        row.AllowConditional,
        row.AllowRequest,
    ),
}
```

### `GetPermissionGroupMembers` — map new columns

Same pattern in the group member mapping:

```go
members[j] = PermissionGroupMember{
    ID:            row.ID.String(),
    CanonicalName: row.CanonicalName,
    Name:          row.Name,
    ResourceType:  row.ResourceType,
    Description:   row.Description.String,
    Capabilities:  buildCapabilities(
        string(row.ScopePolicy),
        row.AllowConditional,
        row.AllowRequest,
    ),
}
```

---

## Step 14 — `permissions/handler.go`

Update `toPermissionResponses` and the group member mapping in
`toPermissionGroupResponses` to include `Capabilities`:

```go
func toPermissionResponses(perms []Permission) []PermissionResponse {
    out := make([]PermissionResponse, len(perms))
    for i, p := range perms {
        out[i] = PermissionResponse{
            ID:            p.ID,
            CanonicalName: p.CanonicalName,
            ResourceType:  p.ResourceType,
            Name:          p.Name,
            Description:   p.Description,
            Capabilities: PermissionCapabilitiesResponse{
                ScopePolicy: p.Capabilities.ScopePolicy,
                AccessTypes: p.Capabilities.AccessTypes,
            },
        }
    }
    return out
}
```

Same for the group member mapping inside `toPermissionGroupResponses`:

```go
members[j] = PermissionGroupMemberResponse{
    ID:            m.ID,
    CanonicalName: m.CanonicalName,
    ResourceType:  m.ResourceType,
    Name:          m.Name,
    Description:   m.Description,
    Capabilities: PermissionCapabilitiesResponse{
        ScopePolicy: m.Capabilities.ScopePolicy,
        AccessTypes: m.Capabilities.AccessTypes,
    },
}
```

---

## Step 15 — Tests

### New test IDs

| ID | File | Description |
|----|------|-------------|
| T-R28b | `permissions/store_test.go` | `GetPermissions` returns `scope_policy`, `allow_conditional`, `allow_request` for seeded permissions |
| T-R29b | `permissions/store_test.go` | `GetPermissionGroupMembers` returns capability fields in group members |
| T-R32 | `roles/service_test.go` | `AddRolePermission` with `access_type = 'conditional'` when `allow_conditional = FALSE` → `ErrAccessTypeNotAllowed` |
| T-R33 | `roles/service_test.go` | `AddRolePermission` with `access_type = 'request'` when `allow_request = FALSE` → `ErrAccessTypeNotAllowed` |
| T-R34 | `roles/service_test.go` | `AddRolePermission` with `scope = 'own'` when `scope_policy = 'all'` → `ErrScopeNotAllowed` |
| T-R35 | `roles/service_test.go` | `AddRolePermission` with `scope_policy = 'none'` normalises scope to `'all'` regardless of input |
| T-R36 | `roles/handler_test.go` | `AddRolePermission` returns 422 `access_type_not_allowed` |
| T-R37 | `roles/handler_test.go` | `AddRolePermission` returns 422 `scope_not_allowed` |
| T-R38 | `roles/store_test.go` | `GetPermissionCaps` returns correct flags for `product:manage` (seeded) |
| T-R39 | `roles/store_test.go` | `GetPermissionCaps` returns `ErrPermissionNotFound` for unknown ID |

### T-R28b — integration test

```go
func TestGetPermissions_IncludesCapabilities_Integration(t *testing.T) {
    s, _ := txStores(t)
    perms, err := s.GetPermissions(context.Background())
    require.NoError(t, err)
    require.NotEmpty(t, perms)

    byName := make(map[string]permissions.Permission, len(perms))
    for _, p := range perms {
        byName[p.CanonicalName] = p
    }

    // Verify a 'none' policy permission
    rbacRead := byName["rbac:read"]
    require.Equal(t, "none", rbacRead.Capabilities.ScopePolicy)
    require.False(t, hasAccessType(rbacRead.Capabilities.AccessTypes, "conditional"))
    require.False(t, hasAccessType(rbacRead.Capabilities.AccessTypes, "request"))
    require.True(t, hasAccessType(rbacRead.Capabilities.AccessTypes, "direct"))
    require.True(t, hasAccessType(rbacRead.Capabilities.AccessTypes, "denied"))

    // Verify a 'request' permission
    jqConfigure := byName["job_queue:configure"]
    require.Equal(t, "none", jqConfigure.Capabilities.ScopePolicy)
    require.True(t, hasAccessType(jqConfigure.Capabilities.AccessTypes, "request"))
    require.False(t, hasAccessType(jqConfigure.Capabilities.AccessTypes, "conditional"))

    // Verify the 'any' + conditional + request permission
    productManage := byName["product:manage"]
    require.Equal(t, "any", productManage.Capabilities.ScopePolicy)
    require.True(t, hasAccessType(productManage.Capabilities.AccessTypes, "conditional"))
    require.True(t, hasAccessType(productManage.Capabilities.AccessTypes, "request"))
}

// hasAccessType is a test helper — checks if a string is in the AccessTypes slice.
func hasAccessType(types []string, t string) bool {
    for _, v := range types {
        if v == t {
            return true
        }
    }
    return false
}
```

### T-R32 through T-R35 — service unit tests

All use `RolesFakeStorer` with `GetPermissionCapsFn` configured:

```go
func TestService_AddRolePermission_CapabilityValidation(t *testing.T) {
    t.Parallel()

    permID := rbacsharedtest.MustUUID("11111111-2222-3333-4444-555555555555")

    tests := []struct {
        name        string
        caps        roles.PermissionCaps
        in          roles.AddRolePermissionInput
        wantErr     error
        wantScope   string // non-empty = assert normalised scope
    }{
        {
            name: "T-R32 conditional disallowed",
            caps: roles.PermissionCaps{ID: permID, ScopePolicy: "none", AllowConditional: false, AllowRequest: false},
            in:   roles.AddRolePermissionInput{PermissionID: permID, AccessType: "conditional", Scope: "all"},
            wantErr: roles.ErrAccessTypeNotAllowed,
        },
        {
            name: "T-R33 request disallowed",
            caps: roles.PermissionCaps{ID: permID, ScopePolicy: "none", AllowConditional: false, AllowRequest: false},
            in:   roles.AddRolePermissionInput{PermissionID: permID, AccessType: "request", Scope: "all"},
            wantErr: roles.ErrAccessTypeNotAllowed,
        },
        {
            name: "T-R34 scope mismatch — all policy, own given",
            caps: roles.PermissionCaps{ID: permID, ScopePolicy: "all", AllowConditional: false, AllowRequest: false},
            in:   roles.AddRolePermissionInput{PermissionID: permID, AccessType: "direct", Scope: "own"},
            wantErr: roles.ErrScopeNotAllowed,
        },
        {
            name: "T-R35 scope_policy none normalises to all",
            caps: roles.PermissionCaps{ID: permID, ScopePolicy: "none", AllowConditional: false, AllowRequest: false},
            in:   roles.AddRolePermissionInput{PermissionID: permID, AccessType: "direct", Scope: "own"},
            wantScope: "all", // normalised
        },
    }

    for _, tc := range tests {
        tc := tc
        t.Run(tc.name, func(t *testing.T) {
            t.Parallel()
            captured := roles.AddRolePermissionInput{}
            store := &rbacsharedtest.RolesFakeStorer{
                GetPermissionCapsFn: func(_ context.Context, _ [16]byte) (roles.PermissionCaps, error) {
                    return tc.caps, nil
                },
                AddRolePermissionFn: func(_ context.Context, _ [16]byte, in roles.AddRolePermissionInput) error {
                    captured = in
                    return nil
                },
            }
            svc := roles.NewService(store)
            roleID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
            err := svc.AddRolePermission(context.Background(), roleID, tc.in)

            if tc.wantErr != nil {
                require.ErrorIs(t, err, tc.wantErr)
                return
            }
            require.NoError(t, err)
            if tc.wantScope != "" {
                require.Equal(t, tc.wantScope, captured.Scope)
            }
        })
    }
}
```

### T-R36 and T-R37 — handler unit tests

Add to `TestHandler_AddRolePermission` in `handler_test.go`:

```
"T-R36 ErrAccessTypeNotAllowed returns 422 access_type_not_allowed"
    svc.AddRolePermissionFn = func(...) error { return roles.ErrAccessTypeNotAllowed }
    assert 422, code == "access_type_not_allowed"

"T-R37 ErrScopeNotAllowed returns 422 scope_not_allowed"
    svc.AddRolePermissionFn = func(...) error { return roles.ErrScopeNotAllowed }
    assert 422, code == "scope_not_allowed"
```

### T-R38 and T-R39 — store integration tests

Add to `roles/store_test.go`:

```go
func TestGetPermissionCaps_Integration(t *testing.T) {
    s, q := txStores(t)
    ctx := context.Background()

    t.Run("T-R38 returns correct caps for product:manage", func(t *testing.T) {
        perm, err := q.GetPermissionByCanonicalName(ctx,
            pgtype.Text{String: "product:manage", Valid: true})
        require.NoError(t, err)

        caps, err := s.GetPermissionCaps(ctx, [16]byte(perm.ID))
        require.NoError(t, err)
        require.Equal(t, "any",  caps.ScopePolicy)
        require.True(t,          caps.AllowConditional)
        require.True(t,          caps.AllowRequest)
        require.Equal(t, "product:manage", caps.CanonicalName)
    })

    t.Run("T-R39 unknown permission ID returns ErrPermissionNotFound", func(t *testing.T) {
        unknownID := rbacsharedtest.MustUUID("ffffffff-ffff-ffff-ffff-ffffffffffff")
        _, err := s.GetPermissionCaps(ctx, unknownID)
        require.ErrorIs(t, err, roles.ErrPermissionNotFound)
    })
}
```

### `QuerierProxy` update — `shared/testutil/querier_proxy.go`

Add `FailGetPermissionByID bool` to the `QuerierProxy` struct under the existing
roles section, and add a forwarding method:

```go
// In struct, under roles section:
FailGetPermissionByID bool

// Forwarding method:
func (p *QuerierProxy) GetPermissionByID(ctx context.Context, id pgtype.UUID) (db.GetPermissionByIDRow, error) {
    if p.FailGetPermissionByID {
        return db.GetPermissionByIDRow{}, rbacsharedtest.ErrProxy
    }
    return p.Querier.GetPermissionByID(ctx, id)
}
```

Run `go build ./internal/domain/rbac/shared/testutil/...` to confirm the compile-time
check passes.

---

## Step 16 — Docs

### `mint/api-reference/rbac/permissions/list-permissions.mdx`

Add a `capabilities` section to the response schema documentation:

```mdx
Each permission object now includes a `capabilities` field:

| Field | Type | Description |
|---|---|---|
| `capabilities.scope_policy` | string | `"none"` \| `"own"` \| `"all"` \| `"any"` — which scope values are valid for grants |
| `capabilities.access_types` | string[] | The access_type values this permission allows. Always contains `"direct"` and `"denied"`. Contains `"conditional"` and/or `"request"` when enabled. |

**UI guidance for `scope_policy`:**
- `"none"` — hide the scope field; it has no meaning for this permission
- `"own"` or `"all"` — pre-fill scope and render read-only
- `"any"` — render scope selector with both options visible
```

### `mint/guides/rbac/permissions-setup-guide.mdx`

Add a new section **"Understanding permission capabilities"** after the permissions
overview and before the role assignment steps:

```mdx
### Understanding permission capabilities

Each permission exposes a `capabilities` object in the API response. Use this object
to build the grant form correctly — it tells you which `access_type` and `scope`
values are valid before you POST to `AddRolePermission`.

```json
// GET /admin/rbac/permissions → permissions[n].capabilities
{
  "scope_policy": "any",        // show scope selector
  "access_types": ["direct", "conditional", "request", "denied"]
}
```

If you try to create a grant with an `access_type` not in `access_types`, the API
returns `422 access_type_not_allowed`. If you pass a `scope` value incompatible with
`scope_policy`, it returns `422 scope_not_allowed`.
```

### `mint/guides/rbac/permissions-setup-guide.mdx` — update example grant

Update the admin role `job_queue:configure` example to show `access_type = "request"`:

```mdx
// job_queue:configure has allow_request = true, scope_policy = "none"
{
  "permission_id": "<job_queue:configure uuid>",
  "access_type": "request",
  "scope": "all",
  "granted_reason": "admin must request approval to pause job queues"
}
```

---

## Expected API response shapes after this phase

### `GET /admin/rbac/permissions`

```json
{
  "permissions": [
    {
      "id": "...",
      "canonical_name": "rbac:read",
      "resource_type": "rbac",
      "name": "read",
      "capabilities": {
        "scope_policy": "none",
        "access_types": ["direct", "denied"]
      }
    },
    {
      "id": "...",
      "canonical_name": "product:manage",
      "resource_type": "product",
      "name": "manage",
      "capabilities": {
        "scope_policy": "any",
        "access_types": ["direct", "conditional", "request", "denied"]
      }
    }
  ]
}
```

### `POST /admin/rbac/roles/{id}/permissions` — new 422 cases

```json
// 422 — access_type not permitted
{ "code": "access_type_not_allowed", "message": "access_type is not permitted for this permission" }

// 422 — scope not permitted
{ "code": "scope_not_allowed", "message": "scope is not permitted for this permission" }
```

---

## What NOT to do in this phase

- Do not implement `permission_condition_templates` enforcement (TODO-4 from Phase 7
  design doc). The `allow_conditional` flag gates whether `conditional` grants are
  created; template enforcement is a separate gate that must be added before any
  permission is set to `allow_conditional = TRUE` in production.
- Do not add a `scope` field to `PermissionCaps` in `models.go` — the service's
  scope normalisation logic derives the target scope from `ScopePolicy` and the
  caller's input; the caps struct only needs the policy string.
- Do not modify `CheckUserAccess` — capability flags are a write-time concern only.
  The access check reads `access_type` and `scope` from the stored grant, which was
  already validated at write time.
- Do not add an `is_active` filter to `GetPermissionByID` — the service only calls
  it during `AddRolePermission`, where the permission was just looked up as active by
  the handler's UUID parse path. The `AND is_active = TRUE` in the query handles
  this anyway.
- Do not add capability columns to `role_permissions` or any audit table — capabilities
  are a property of the permission definition, not of individual grants.
- Do not run `make sqlc` until both schema (Step 1) and query (Step 2) changes are
  in place — sqlc needs the new columns in the schema to generate the correct struct
  fields.

---

## Gate checklist

- [ ] `go build ./internal/db/...` — clean after `make sqlc`
- [ ] `go build ./internal/domain/rbac/...` — zero errors
- [ ] `go build ./internal/domain/rbac/shared/testutil/...` — QuerierProxy compile-time check passes
- [ ] `go vet ./...` — zero warnings
- [ ] `make migrate` (clean DB) — `003_rbac.sql` runs with enum + 3 new columns without error
- [ ] `make seed` — `002_permissions.sql` `DO UPDATE` backfills capability values on existing rows
- [ ] `go test ./internal/domain/rbac/roles/...` — unit tests including T-R32..T-R37 green
- [ ] `go test ./internal/domain/rbac/permissions/...` — unit tests green
- [ ] `go test -tags integration_test ./internal/domain/rbac/permissions/...` — T-R28 + T-R28b + T-R29 + T-R29b green
- [ ] `go test -tags integration_test ./internal/domain/rbac/roles/...` — T-R23..T-R31e + T-R38 + T-R39 green
- [ ] `GET /admin/rbac/permissions` — every permission includes `capabilities.scope_policy` and `capabilities.access_types`
- [ ] `GET /admin/rbac/permissions/groups` — every member includes `capabilities`
- [ ] `POST /admin/rbac/roles/{id}/permissions` with `access_type = 'conditional'` on `rbac:read` → 422 `access_type_not_allowed`
- [ ] `POST /admin/rbac/roles/{id}/permissions` with `scope = 'own'` on `job_queue:configure` → grant stored with `scope = 'all'` (normalised)
- [ ] `POST /admin/rbac/roles/{id}/permissions` with `access_type = 'request'` on `product:manage` → 204 (allowed)
- [ ] No circular imports introduced
