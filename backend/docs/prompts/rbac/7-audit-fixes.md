# RBAC — Phase 7: Audit Bug Fixes

**Feature:** RBAC
**Phase:** 7 of 10
**Depends on:** Phases 0–6 (schema ✅, queries ✅, seeds ✅, platform/rbac ✅, bootstrap ✅, permissions ✅, roles ✅)
**Gate:** `go test -tags integration ./internal/platform/rbac/... && go test -tags integration_test ./internal/domain/rbac/roles/...` — all existing tests green plus new test IDs from this phase
**Design doc:** `docs/prompts/rbac/0-design.md`
**Audit source:** senior architecture review — 11 findings, 7 fixed here
**Go version:** 1.25

---

## Scope of this phase

This phase fixes **7 confirmed bugs** uncovered in the architecture audit. Three
further findings are deferred: items 5/7/10 depend on the `scope_policy` /
`allow_conditional` capability flags that land in Phase 8 (capability flags), and
item 3 (`access_type = 'request'` unimplemented) is a blocked feature that requires
the full requests domain and is tracked separately.

### Fixes in this phase (execution order)

| # | Finding | Severity | Files touched |
|---|---|---|---|
| F-1 | `fn_prevent_privilege_escalation` grants re-grant rights to `denied` grantors | 🔴 Critical | `sql/schema/004_rbac_functions.sql` |
| F-2 | `is_explicitly_denied` not checked in middleware — denial overridden by direct grant | 🔴 Critical | `internal/platform/rbac/checker.go`, `checker_test.go` |
| F-3 | `AddRolePermission ON CONFLICT DO NOTHING` is silent — duplicate returns 204 when nothing changed | 🟠 High | `sql/queries/rbac.sql`, `roles/store.go`, `roles/errors.go`, `roles/handler.go`, `roles/handler_test.go` |
| F-4 | `rbac.acting_user` session variable is honor-system only — DELETE audit gets wrong or NULL actor | 🟠 High | `internal/domain/rbac/shared/store.go`, `roles/store.go`, `roles/service.go`, `roles/handler.go`, fakes |
| F-5 | `permission_condition_templates` is entirely dead code — `allow_conditional` without a template is unconstrained | 🟡 Medium | `docs/prompts/rbac/0-design.md` only (tracked backlog) |
| F-6 | No FK from `requests.request_type` to `request_type_schemas` | 🟡 Medium | `sql/schema/005_requests.sql` |
| F-7 | Cache invalidation plan misses `permissions.is_active` changes | 🟡 Medium | `docs/prompts/rbac/0-design.md` only (tracked backlog) |

### Deferred to Phase 8 (capability flags)

| # | Finding | Why deferred |
|---|---|---|
| 5 | `scope` stored for non-scoped permissions | Requires `scope_policy` column which lands in Phase 8 |
| 7 | `allow_conditional` without template row is undefined | Requires `allow_conditional` column which lands in Phase 8 |
| 10 | `scope` default `'own'` silently wrong for meaningful permissions | Requires `scope_policy` to distinguish meaningful from non-meaningful |

### Tracked separately (out of scope for this phase)

| # | Finding | Status |
|---|---|---|
| 3 | `access_type = 'request'` entirely unimplemented | Blocked — requires full requests domain |

---

## Read before writing any code

| File | Why |
|---|---|
| `docs/prompts/rbac/0-design.md` | Full design including §9 middleware sketch, §12 test cases |
| `sql/schema/003_rbac.sql` | `role_permissions` table and constraint definitions |
| `sql/schema/004_rbac_functions.sql` | `fn_prevent_privilege_escalation` — function being patched (F-1) |
| `sql/schema/005_requests.sql` | `requests` table — adding FK (F-6) |
| `sql/queries/rbac.sql` | `AddRolePermission` query — changing to `:execrows` (F-3) |
| `internal/platform/rbac/checker.go` | `Require` middleware — adding `IsExplicitlyDenied` check (F-2) |
| `internal/platform/rbac/checker_test.go` | Existing test layout — new cases follow same pattern |
| `internal/domain/rbac/shared/store.go` | `BaseStore` — adding `WithActingUser` helper (F-4) |
| `internal/domain/rbac/roles/store.go` | `RemoveRolePermission` — must call `WithActingUser` (F-4) |
| `internal/domain/rbac/roles/errors.go` | Adding `ErrGrantAlreadyExists` (F-3) |
| `internal/domain/rbac/roles/handler.go` | Adding new error case for F-3; `mustUserID` for F-4 |
| `internal/domain/rbac/shared/testutil/fake_storer.go` | Existing fake layout — signature update for F-4 |
| `internal/domain/rbac/shared/testutil/fake_servicer.go` | Existing fake layout — signature update for F-4 |

---

## F-1 — `fn_prevent_privilege_escalation` grants re-grant rights to `denied` grantors

**File:** `sql/schema/004_rbac_functions.sql` — **edit this file directly**

**Problem:** The EXISTS subquery inside `fn_prevent_privilege_escalation` has no
`access_type` filter. A user whose role carries `access_type = 'denied'` for
permission X satisfies the check and can grant X to others, even though they are
explicitly blocked from using X themselves.

**Fix:** Edit `sql/schema/004_rbac_functions.sql` in-place. Add `AND rp.access_type != 'denied'`
to the EXISTS subquery inside `fn_prevent_privilege_escalation`. Do not create a new
migration file — `CREATE OR REPLACE FUNCTION` inside the existing file replaces the
function in-place on next `make migrate`.

Find this exact block inside `fn_prevent_privilege_escalation`:

```sql
    EXISTS (
        SELECT 1
        FROM role_permissions rp
        WHERE rp.role_id       = ur.role_id
        AND rp.permission_id = NEW.permission_id
    )
    INTO v_granter_is_owner, v_granter_has_perm
```

Replace with:

```sql
    EXISTS (
        SELECT 1
        FROM role_permissions rp
        WHERE rp.role_id       = ur.role_id
          AND rp.permission_id = NEW.permission_id
          AND rp.access_type  != 'denied'
    )
    INTO v_granter_is_owner, v_granter_has_perm
```

Also update the `COMMENT ON FUNCTION fn_prevent_privilege_escalation()` in the same
file to append:
`'Denied grants do not confer re-grant rights — enforced by access_type != ''denied'' filter on the EXISTS check.'`

---

## F-2 — `is_explicitly_denied` not checked in middleware

**Files:** `internal/platform/rbac/checker.go`, `internal/platform/rbac/checker_test.go`

**Problem:** `CheckUserAccess` returns `IsExplicitlyDenied` (already in the query
and the generated struct). The `Require` middleware acts on `access_type` only.
Because the COALESCE for `access_type` in the query filters out `denied` role grants,
a user who has both a `denied` role grant **and** a `direct` user_permissions grant
for the same permission receives `access_type = 'direct'` and is let through.
`IsExplicitlyDenied` is computed and returned but never consulted.

**Fix in `checker.go`:** The `Require` middleware guard order in the phase-3 spec is:

```
4. asBool(row.IsOwner)    → inject AccessResult; call next (owner bypasses everything)
5. row.IsExplicitlyDenied → 403 forbidden          ← THIS IS MISSING
6. !row.HasPermission     → 403 forbidden
7. switch access_type ...
```

Step 5 is absent. Insert it after the owner short-circuit (step 4) and before the
`HasPermission` check (step 6):

```go
// Step 5 — explicit denial takes absolute priority over any other grant.
// A 'denied' role grant overrides a direct user_permissions grant for the same
// permission. The owner path above is exempt — owner bypasses all access_type logic.
if row.IsExplicitlyDenied {
    respond.Error(w, http.StatusForbidden, "forbidden", "insufficient permissions")
    return
}
```

**Fix in `checker_test.go`:** Add integration test **T-R05b** after the existing T-R05:

| ID | Scenario | Layer | Key assertion |
|----|----------|-------|---------------|
| T-R05b | Role has `denied` grant + user also has `direct` user_permission for same permission → 403 | I | HTTP 403; next NOT called; `IsExplicitlyDenied` is the firing condition |

**Setup:**
1. Create a role with `access_type = 'denied'` for a test permission.
2. Assign that role to the test user.
3. Also insert a `user_permissions` row for the same permission with `access_type = 'direct'` (not expired).
4. Assert `Require` on that permission returns 403.

Update the existing T-R05 comment to clarify it tests `denied` access_type where
there is **no** competing direct grant. T-R05b is the case where both exist.

---

## F-3 — `AddRolePermission ON CONFLICT DO NOTHING` is silent

**Problem:** `AddRolePermission` is `:exec` — returns no row count. A duplicate
`(role_id, permission_id)` pair silently does nothing; the service returns nil; the
handler returns 204. The caller cannot tell whether their grant was created or
ignored, and cannot update an existing grant's `access_type` or `scope` without
first removing it — but the 204 on re-post silently confirms nothing changed.

**Fix:** Switch to `:execrows`, detect zero rows as a conflict, return a new
`ErrGrantAlreadyExists` sentinel mapping to 409.

### `sql/queries/rbac.sql`

Change the `AddRolePermission` annotation from `:exec` to `:execrows`:

```sql
-- name: AddRolePermission :execrows
-- ON CONFLICT (role_id, permission_id) DO NOTHING: returns 0 when the grant
-- already exists. The service maps 0 rows → ErrGrantAlreadyExists → 409.
-- To update access_type/scope on an existing grant, remove it first then re-add.
INSERT INTO role_permissions (
    role_id, permission_id, granted_by, granted_reason,
    access_type, scope, conditions
)
VALUES (
    @role_id::uuid,
    @permission_id::uuid,
    @granted_by::uuid,
    @granted_reason,
    @access_type,
    @scope,
    COALESCE(sqlc.narg('conditions')::jsonb, '{}')
)
ON CONFLICT (role_id, permission_id) DO NOTHING;
```

Run `make sqlc` after this change — the generated signature changes from `error` to
`(int64, error)`. Do not run `make sqlc` until all callsite changes in this section
are also ready, or compilation will break.

### `roles/errors.go`

Add one sentinel:

```go
// ErrGrantAlreadyExists is returned when AddRolePermission finds the
// (role_id, permission_id) grant already exists on this role.
// The caller must remove the existing grant before re-adding with different
// access_type or scope.
var ErrGrantAlreadyExists = errors.New("permission grant already exists on this role")
```

### `roles/store.go` — `AddRolePermission`

Update to handle the new `(int64, error)` return signature:

```go
func (s *Store) AddRolePermission(ctx context.Context, roleID [16]byte, in AddRolePermissionInput) error {
    rows, err := s.Queries.AddRolePermission(ctx, db.AddRolePermissionParams{
        RoleID:        s.ToPgtypeUUID(roleID),
        PermissionID:  s.ToPgtypeUUID(in.PermissionID),
        GrantedBy:     s.ToPgtypeUUID(in.GrantedBy),
        GrantedReason: in.GrantedReason,
        AccessType:    db.PermissionAccessType(in.AccessType),
        Scope:         db.PermissionScope(in.Scope),
        Conditions:    condBytes, // nil/empty → []byte("{}")
    })
    if err != nil {
        if s.IsForeignKeyViolation(err, "role_permissions_permission_id_fkey") {
            return ErrPermissionNotFound
        }
        if s.IsForeignKeyViolation(err, "role_permissions_role_id_fkey") {
            return ErrRoleNotFound
        }
        return fmt.Errorf("store.AddRolePermission: %w", err)
    }
    if rows == 0 {
        return ErrGrantAlreadyExists
    }
    return nil
}
```

### `roles/handler.go` — `AddRolePermission`

Add one new error case to the switch **before** the `default:` branch:

```go
case errors.Is(err, ErrGrantAlreadyExists):
    respond.Error(w, http.StatusConflict, "grant_already_exists",
        "this permission is already granted to the role — remove it first to change access_type or scope")
```

### `roles/handler_test.go`

Add to `TestHandler_AddRolePermission`:

```
"duplicate grant returns 409 grant_already_exists"
    svc.AddRolePermissionFn = func(...) error { return roles.ErrGrantAlreadyExists }
    // POST with valid body and token context
    assert 409, code == "grant_already_exists"
```

Remove any existing comment or sub-test that said "second identical call is also 204
(no-op)" — it is now 409.

---

## F-4 — `rbac.acting_user` session variable is honor-system only

**Problem:** Every DELETE on `role_permissions`, `user_roles`, and `user_permissions`
relies on calling code having first executed `SET LOCAL rbac.acting_user = '<uuid>'`.
The trigger falls back to `OLD.granted_by` (original granter, not the person
deleting) for `role_permissions` DELETEs, and to NULL for other DELETEs. Forgetting
this call is a silent failure — audit records get the wrong actor with no warning.

**Fix:** Add `WithActingUser` to `BaseStore`. All delete methods that fire the audit
trigger must call it. In this phase, only `roles/store.go`'s `RemoveRolePermission`
is in scope. `userroles/` and `userpermissions/` will use it when those packages are
built in Phases 9–10.

### `internal/domain/rbac/shared/store.go`

Add this method to `BaseStore`:

```go
// WithActingUser sets rbac.acting_user to userID for the duration of fn, then
// clears it. This ensures audit triggers (fn_audit_role_permissions, etc.) in
// 004_rbac_functions.sql record the correct actor on DELETE operations.
//
// Must be called for every DELETE on role_permissions, user_roles, or
// user_permissions. Omitting it causes audit rows to record OLD.granted_by
// (original granter) or NULL instead of the actual deletion actor.
//
// SET LOCAL is transaction-scoped in Postgres — the variable is automatically
// cleared at transaction end. The explicit clear after fn is belt-and-suspenders
// for long-lived connections where autocommit wraps each statement in its own
// implicit transaction.
//
// Constraint: SET LOCAL only works inside an explicit transaction. All domain
// delete routes in V1 are single-statement and run in autocommit, which means
// SET LOCAL behaves as SET SESSION (session-scoped). This is acceptable in V1.
// When multi-statement transactions are introduced, audit actors will be correct
// automatically. Document this on any multi-statement delete that is added.
//
// If userID is empty, rbac.acting_user is cleared (triggers treat empty as NULL).
func (b *BaseStore) WithActingUser(ctx context.Context, userID string, fn func() error) error {
    if _, err := b.Pool.Exec(ctx, "SET LOCAL rbac.acting_user = $1", userID); err != nil {
        return fmt.Errorf("rbacshared.WithActingUser: set acting_user: %w", err)
    }
    err := fn()
    _, _ = b.Pool.Exec(ctx, "SET LOCAL rbac.acting_user = ''")
    return err
}
```

### `roles/store.go` — `RemoveRolePermission`

Change the signature to accept `actingUserID string` and wrap the delete in
`WithActingUser`:

```go
func (s *Store) RemoveRolePermission(ctx context.Context, roleID, permID [16]byte, actingUserID string) error {
    return s.WithActingUser(ctx, actingUserID, func() error {
        rows, err := s.Queries.RemoveRolePermission(ctx, db.RemoveRolePermissionParams{
            RoleID:       s.ToPgtypeUUID(roleID),
            PermissionID: s.ToPgtypeUUID(permID),
        })
        if err != nil {
            return fmt.Errorf("store.RemoveRolePermission: %w", err)
        }
        if rows == 0 {
            return ErrRolePermissionNotFound
        }
        return nil
    })
}
```

### `roles/service.go` — cascade signature change

Update `Storer` interface:
```go
RemoveRolePermission(ctx context.Context, roleID, permID [16]byte, actingUserID string) error
```

Update `Servicer` interface and service method:
```go
// Servicer interface
RemoveRolePermission(ctx context.Context, roleID, permID, actingUserID string) error

// Service method
func (s *Service) RemoveRolePermission(ctx context.Context, roleID, permID, actingUserID string) error {
    rid, err := parseID(roleID)
    if err != nil {
        return ErrRolePermissionNotFound
    }
    pid, err := parseID(permID)
    if err != nil {
        return ErrRolePermissionNotFound
    }
    if err := s.store.RemoveRolePermission(ctx, rid, pid, actingUserID); err != nil {
        return fmt.Errorf("roles.RemoveRolePermission: %w", err)
    }
    return nil
}
```

### `roles/handler.go` — `RemoveRolePermission`

Add `mustUserID` call to extract the authenticated caller and pass it through:

```go
func (h *Handler) RemoveRolePermission(w http.ResponseWriter, r *http.Request) {
    actingUserID, ok := h.mustUserID(w, r)
    if !ok {
        return
    }
    roleID := chi.URLParam(r, "id")
    permID := chi.URLParam(r, "perm_id")
    err := h.svc.RemoveRolePermission(r.Context(), roleID, permID, actingUserID)
    if err != nil {
        switch {
        case errors.Is(err, ErrRolePermissionNotFound):
            respond.Error(w, http.StatusNotFound, "role_permission_not_found", "role permission grant not found")
        default:
            slog.ErrorContext(r.Context(), "roles.RemoveRolePermission: service error", "error", err)
            respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
        }
        return
    }
    respond.NoContent(w)
}
```

`RemoveRolePermission` previously did not call `mustUserID`. It does now — the
caller identity is required for the audit trail, and this is also a minor security
improvement ensuring an authenticated actor is always recorded.

### `shared/testutil/fake_storer.go` — update `RolesFakeStorer`

Update `RemoveRolePermissionFn` signature:

```go
RemoveRolePermissionFn func(ctx context.Context, roleID, permID [16]byte, actingUserID string) error
```

Update the forwarding method and the compile-time interface check.

### `shared/testutil/fake_servicer.go` — update `RolesFakeServicer`

Update `RemoveRolePermissionFn` signature:

```go
RemoveRolePermissionFn func(ctx context.Context, roleID, permID, actingUserID string) error
```

Update the forwarding method and compile-time check.

### `roles/handler_test.go` — update `TestHandler_RemoveRolePermission`

Add sub-test:

```
"no user ID in context returns 401"
    // request without token context (do not call authedReq)
    assert 401, code == "unauthorized"
```

All other sub-tests must now use `authedReq` to provide a token context (update any
that don't already).

---

## F-5 — `permission_condition_templates` dead code (tracked backlog)

**File:** `docs/prompts/rbac/0-design.md`

No code changes. Add the following entry to **§16 Operational TODOs**:

```markdown
### TODO-4 · Condition template enforcement ⚠️ Pre-conditional-grants blocker

`permission_condition_templates` defines the valid ABAC condition vocabulary per
permission but is never read at runtime. There are no queries, no trigger, and no
app-layer validation against it. When `allow_conditional = TRUE` is set on a
permission in Phase 8, any JSON object will be accepted as conditions — including
keys that are semantically wrong or dangerous (e.g. `{"bypass_everything": true}`).

Must be resolved before any permission has `allow_conditional = TRUE` in production:

1. Add `GetConditionTemplate` query:
   `SELECT required_conditions, forbidden_conditions, validation_rules
    FROM permission_condition_templates WHERE permission_id = @id`.
2. In `AddRolePermission` service: when `access_type = 'conditional'`, fetch the
   template and validate `conditions` against `required_conditions` (all keys must be
   present) and `forbidden_conditions` (no matching keys may be present). Value/type/
   range rules in `validation_rules` are evaluated in the app layer.
3. Add a trigger in `004_rbac_functions.sql` that enforces: `allow_conditional = TRUE`
   requires a matching `permission_condition_templates` row (BEFORE INSERT/UPDATE on
   `permissions`).

This is a hard gate for Phase 8 — do not seed any permission with
`allow_conditional = TRUE` until this TODO is closed.
```

---

## F-6 — No FK from `requests.request_type` to `request_type_schemas`

**File:** `sql/schema/005_requests.sql` — **edit this file directly**

**Problem:** `requests.request_type` is a free `VARCHAR(100)` with no FK to
`request_type_schemas`. A missed validation check in the service layer can insert a
request with an unknown type — no schema, no SLA config, no approver template.

**Fix:** Edit `sql/schema/005_requests.sql` in-place. Add the FK constraint directly
in the `requests` table column definition. Do not create a new migration file.

Find this exact block in the `requests` table:

```sql
    -- Discriminator: product_creation, vendor_withdrawal, permission_action, etc.
    -- Controls which JSON Schema is used to validate request_data.
    request_type VARCHAR(100) NOT NULL,
```

Replace with:

```sql
    -- Discriminator: product_creation, vendor_withdrawal, permission_action, etc.
    -- Controls which JSON Schema is used to validate request_data.
    -- FK ensures a request_type_schemas row exists before any request of this type
    -- can be inserted. ON DELETE RESTRICT prevents schema removal while requests exist.
    request_type VARCHAR(100) NOT NULL
        REFERENCES request_type_schemas(request_type) ON DELETE RESTRICT,
```

Also update `COMMENT ON COLUMN requests.request_type` in the same file to note:
`'FK to request_type_schemas ensures a schema row exists before requests of this type can be inserted.'`

**Forward-reference note:** `request_type_schemas` is defined later in the same
file. Postgres evaluates FK references at constraint enforcement time (not parse
time), so the forward reference is valid within a single `goose` transaction block.
Verify the `Down` section drops `requests` before `request_type_schemas` — this is
already correct since `request_sla_config` (which references `request_type_schemas`)
is dropped with `CASCADE`.

---

## F-7 — Cache invalidation plan misses `permissions.is_active` changes (tracked backlog)

**File:** `docs/prompts/rbac/0-design.md`

No code changes (caching is a V2 concern per D-R3). In **§9 Middleware Design**
under `V2: Caching (post-launch)`, find the invalidation list:

```
Invalidate on any of:
AssignUserRole, RemoveUserRole, GrantUserPermission, RevokeUserPermission,
AddRolePermission, RemoveRolePermission.
```

Replace with:

```
Invalidate on any of:
AssignUserRole, RemoveUserRole, GrantUserPermission, RevokeUserPermission,
AddRolePermission, RemoveRolePermission, DeactivatePermission
(permissions.is_active = FALSE).

Note: when a permission is deactivated, all cache entries keyed on its
canonical_name must be evicted regardless of user. A wildcard eviction on
canonical_name is simpler than tracking individual (user, permission) pairs
and is correct since permission deactivation is operationally rare.
```

---

## Tests summary — new test IDs introduced in this phase

| ID | File | Description |
|----|------|-------------|
| T-R05b | `platform/rbac/checker_test.go` | `denied` role + `direct` user_permission → 403; `IsExplicitlyDenied` fires |
| T-R31b | `roles/handler_test.go` | `RemoveRolePermission` without token → 401 |
| T-R31c | `roles/handler_test.go` | `AddRolePermission` duplicate → 409 `grant_already_exists` |
| T-R31d | `roles/store_test.go` (integration) | `AddRolePermission` duplicate → `ErrGrantAlreadyExists` |
| T-R31e | `roles/store_test.go` (integration) | `RemoveRolePermission` audit row `changed_by` == actingUserID |

### T-R31d — full integration test

```go
func TestAddRolePermission_Duplicate_Integration(t *testing.T) {
    s, q := txStores(t)
    ctx := context.Background()

    role, _ := q.CreateRole(ctx, db.CreateRoleParams{Name: "dup_test_role"})
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
    // First insert succeeds
    err := s.AddRolePermission(ctx, [16]byte(role.ID), in)
    require.NoError(t, err)

    // Second insert returns ErrGrantAlreadyExists
    err = s.AddRolePermission(ctx, [16]byte(role.ID), in)
    require.ErrorIs(t, err, roles.ErrGrantAlreadyExists)
}
```

### T-R31e — full integration test

```go
func TestRemoveRolePermission_AuditActor_Integration(t *testing.T) {
    s, q := txStores(t)
    ctx := context.Background()

    role, _ := q.CreateRole(ctx, db.CreateRoleParams{Name: "audit_actor_role"})
    perm, _ := q.GetPermissionByCanonicalName(ctx, pgtype.Text{String: "rbac:read", Valid: true})
    ownerID, _ := q.GetOwnerRoleID(ctx)

    // actingUserID must be a real user for the FK to hold; create one or use
    // an existing seeded user. In tests, use rbacsharedtest.CreateUser.
    actingUser := rbacsharedtest.CreateUser(t, testPool)

    _ = q.AddRolePermission(ctx, db.AddRolePermissionParams{
        RoleID:        pgtype.UUID{Bytes: [16]byte(role.ID), Valid: true},
        PermissionID:  pgtype.UUID{Bytes: [16]byte(perm.ID), Valid: true},
        GrantedBy:     pgtype.UUID{Bytes: [16]byte(ownerID), Valid: true},
        GrantedReason: "setup",
        AccessType:    db.PermissionAccessTypeDirect,
        Scope:         db.PermissionScopeAll,
        Conditions:    []byte("{}"),
    })

    err := s.RemoveRolePermission(ctx, [16]byte(role.ID), [16]byte(perm.ID), actingUser.ID)
    require.NoError(t, err)

    // Assert the audit row records actingUser.ID, not ownerID
    var changedBy pgtype.UUID
    err = testPool.QueryRow(ctx,
        `SELECT changed_by FROM role_permissions_audit
         WHERE role_id = $1 AND permission_id = $2 AND change_type = 'deleted'
         ORDER BY changed_at DESC LIMIT 1`,
        pgtype.UUID{Bytes: [16]byte(role.ID), Valid: true},
        pgtype.UUID{Bytes: [16]byte(perm.ID), Valid: true},
    ).Scan(&changedBy)
    require.NoError(t, err)
    require.True(t, changedBy.Valid)

    actingUUID, _ := uuid.Parse(actingUser.ID)
    require.Equal(t, [16]byte(actingUUID), changedBy.Bytes)
}
```

---

## Docs updates

**`mint/api-reference/rbac/roles/add-role-permission.mdx`**

Add a `409` response entry to the responses section:

```mdx
**409 grant_already_exists** — The permission is already granted to this role.
Remove the existing grant first using
[`DELETE /admin/rbac/roles/{id}/permissions/{perm_id}`](/api-reference/rbac/roles/remove-role-permission)
before re-adding with a different `access_type` or `scope`.
```

**`mint/guides/rbac/permissions-setup-guide.mdx`**

In Step 3, after the example grant block, replace:

```mdx
Repeat this for every permission the role needs.
```

With:

```mdx
Repeat this for every permission the role needs.

If a permission is already granted to the role, the API returns `409 grant_already_exists`.
To change the `access_type` or `scope` of an existing grant, remove it first with
[`DELETE /admin/rbac/roles/{id}/permissions/{perm_id}`](/api-reference/rbac/roles/remove-role-permission),
then re-add it with the corrected values.
```

---

## What NOT to do in this phase

- Do not implement `access_type = 'request'` approval submission — tracked separately.
- Do not add `scope_policy`, `allow_conditional`, or `allow_request` columns — Phase 8.
- Do not implement condition template enforcement beyond TODO-4 in the design doc.
- Do not modify `CheckUserAccess` in `rbac.sql` — the query already returns
  `is_explicitly_denied` correctly; the fix is in the middleware only.
- Do not add `WithActingUser` calls to INSERT operations — audit triggers use
  `NEW.granted_by` for INSERTs, which is always set by the application.
- Do not change `AddRolePermission` to `ON CONFLICT DO UPDATE` — the intended
  semantic is remove + re-add for changes. The 409 makes this explicit to callers.
- Do not modify `user_roles` or `user_permissions` stores for `WithActingUser` in
  this phase — those packages are built in Phases 9–10. Add the helper to `BaseStore`
  now so it is available when those phases begin.
- Do not run `make sqlc` until the `:execrows` annotation change **and** all callsite
  signature changes (`store.go`, `service.go`, `handler.go`, fakes) are ready in the
  same step — the generated signature change breaks compilation until all callsites
  are updated.

---

## Gate checklist

- [ ] `go build ./internal/platform/rbac/...` — zero errors
- [ ] `go build ./internal/domain/rbac/...` — zero errors
- [ ] `go build ./internal/domain/rbac/shared/testutil/...` — QuerierProxy compile-time check passes
- [ ] `go vet ./...` — zero warnings
- [ ] `make sqlc` — regenerates cleanly after `:execrows` annotation change
- [ ] `make migrate` (clean DB) — `fn_prevent_privilege_escalation` runs updated; FK constraint in `005_requests.sql` applies without error
- [ ] `go test ./internal/platform/rbac/...` — T-R01..T-R17 + T-R05b + T-RAG01..T-RAG04 green
- [ ] `go test ./internal/domain/rbac/roles/...` — all unit tests green (including T-R31b, T-R31c)
- [ ] `go test -tags integration ./internal/platform/rbac/...` — T-R05b integration case green
- [ ] `go test -tags integration_test ./internal/domain/rbac/roles/...` — T-R23..T-R31 + T-R31d + T-R31e green
- [ ] Manually verify: `POST /admin/rbac/roles/{id}/permissions` twice → first 204, second 409 `grant_already_exists`
- [ ] Manually verify: user with `denied` role grant + `direct` user_permissions grant for same permission → guarded endpoint returns 403
- [ ] Manually verify: `DELETE /admin/rbac/roles/{id}/permissions/{perm_id}` → `role_permissions_audit.changed_by` matches the JWT user, not the original `granted_by`
- [ ] No circular imports introduced
