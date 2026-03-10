# RBAC Design — v1

## Changelog

| Version | Change |
|---------|--------|
| v1 | Initial design |

---

## 1. What Already Exists

The schema and triggers are **complete**. Nothing in this design touches `003_rbac.sql`
or `004_rbac_functions.sql`.

| Already done | Still needed |
|---|---|
| `roles`, `permissions`, `permission_groups` tables | SQL queries (`sql/queries/rbac.sql`) |
| `role_permissions`, `user_roles`, `user_permissions` tables | Permission seeds (`sql/seeds/002_permissions.sql`) |
| `permission_condition_templates`, all audit tables | Role seeds (`sql/seeds/003_roles.sql`) |
| `permission_group_members` | `internal/platform/rbac/` — checker + middleware |
| All triggers (audit, privilege escalation, orphaned owner, expiry) | `internal/domain/rbac/` — admin API |
| `001_roles.sql` seed — owner role row | `app.Deps` wiring, route mounting |

The only seed that exists is the `owner` role row. Every permission and every other
role (admin, vendor, customer, etc.) needs to be seeded by this design.

---

## 2. The Three-Layer Check Model

```
Request arrives with Bearer token
        │
        ▼
token.Auth middleware          ← already exists, injects userID into context
        │
        ▼
rbac.Require("resource:action") ← NEW, reads userID from context
        │
        ├─ is owner role? ──────────────────────────── YES → pass (unrestricted)
        │
        └─ has permission via role OR direct grant? ── YES → pass
                                                        NO  → 403 forbidden
```

The three sources of a permission:

**Layer 1 — Owner role.** `is_owner_role = TRUE` on the user's role bypasses all
permission checks. Only one role in the system has this flag. The DB trigger
`fn_prevent_orphaned_owner` guarantees at least one active owner always exists.

**Layer 2 — Role permission.** The user's role has a row in `role_permissions` for
the matching permission. This is the normal path for admin, vendor, customer.

**Layer 3 — Direct grant.** A row in `user_permissions` gives the user this specific
permission temporarily (`expires_at` is required and bounded to 90 days max by the DB
trigger). Used for time-limited exceptions without changing the user's role.

The middleware runs one DB query that checks all three layers in a single round-trip.

---

## 3. Package Structure

```
internal/platform/rbac/
    checker.go          — Checker struct: IsOwner, HasPermission, Require middleware
    context.go          — inject/read from context for tests
    errors.go           — ErrForbidden, ErrUnauthenticated

internal/domain/rbac/
    routes.go           — assembles owner + admin sub-routers
    bootstrap/          — POST /owner/bootstrap (unauthenticated, first-run only)
    roles/              — role CRUD
    permissions/        — permission + group read endpoints
    userroles/          — assign / remove a user's role
    userpermissions/    — grant / revoke direct user permissions

sql/queries/rbac.sql    — all RBAC queries (sqlc-generated)
sql/seeds/002_permissions.sql
sql/seeds/003_roles.sql
```

`internal/platform/rbac/` is infrastructure — used by every domain. It knows about
the DB but not about any domain business logic.

`internal/domain/rbac/` is the admin API — follows the exact same pattern as
`internal/domain/auth/{feature}/`: routes → handler → service → store → models.

---

## 4. SQL Queries (`sql/queries/rbac.sql`)

### Permission check (hot path — called on every guarded request)

```sql
-- name: CheckUserAccess :one
-- Returns (is_owner, has_permission) in one round-trip.
-- The middleware calls this once per guarded request.
-- Owner users short-circuit in Go code — has_permission is still evaluated
-- by the DB but the result is ignored when is_owner = true.
-- Index usage:
--   is_owner:        idx_roles_owner + idx_user_roles_lookup
--   has_permission:  idx_role_perms_covering + idx_user_permissions_user_expires
SELECT
    EXISTS(
        SELECT 1
        FROM   user_roles ur
        JOIN   roles r ON r.id = ur.role_id
        WHERE  ur.user_id      = @user_id::uuid
          AND  r.is_owner_role = TRUE
          AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
    ) AS is_owner,
    EXISTS(
        SELECT 1
        FROM   user_roles ur
        JOIN   role_permissions rp ON rp.role_id       = ur.role_id
        JOIN   permissions p       ON p.id             = rp.permission_id
        WHERE  ur.user_id          = @user_id::uuid
          AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
          AND  p.canonical_name    = @permission
          AND  p.is_active         = TRUE
        UNION ALL
        SELECT 1
        FROM   user_permissions up
        JOIN   permissions p ON p.id = up.permission_id
        WHERE  up.user_id         = @user_id::uuid
          AND  up.expires_at      > NOW()
          AND  p.canonical_name   = @permission
          AND  p.is_active        = TRUE
    ) AS has_permission;
```

### Bootstrap

```sql
-- name: CountActiveOwners :one
-- Used by the bootstrap handler to gate POST /owner/bootstrap.
-- Must return 0 for the bootstrap to proceed.
SELECT COUNT(*) AS count
FROM   user_roles ur
JOIN   roles r ON r.id = ur.role_id
WHERE  r.is_owner_role = TRUE
  AND  (ur.expires_at IS NULL OR ur.expires_at > NOW());

-- name: GetOwnerRoleID :one
SELECT id FROM roles
WHERE  is_owner_role  = TRUE
  AND  is_system_role = TRUE
LIMIT 1;

-- name: GetActiveUserByID :one
-- Used by bootstrap to verify the target user exists and is eligible.
SELECT id, email, is_active, email_verified
FROM   users
WHERE  id         = @user_id::uuid
  AND  deleted_at IS NULL;

-- name: AssignUserRole :one
-- Upserts a user_role row. On conflict (user already has a role), replaces it.
-- granted_by is the actor performing the assignment.
INSERT INTO user_roles (user_id, role_id, granted_by, granted_reason, expires_at)
VALUES (
    @user_id::uuid,
    @role_id::uuid,
    @granted_by::uuid,
    @granted_reason,
    sqlc.narg('expires_at')::timestamptz
)
ON CONFLICT (user_id) DO UPDATE
    SET role_id        = EXCLUDED.role_id,
        granted_by     = EXCLUDED.granted_by,
        granted_reason = EXCLUDED.granted_reason,
        expires_at     = EXCLUDED.expires_at,
        updated_at     = NOW()
RETURNING user_id, role_id, expires_at, created_at, updated_at;

-- name: RemoveUserRole :execrows
-- Hard-deletes a user_role row (revocation = delete; history in audit table).
-- fn_prevent_orphaned_owner fires and blocks if this is the last active owner.
DELETE FROM user_roles WHERE user_id = @user_id::uuid;
```

### Roles

```sql
-- name: GetRoles :many
SELECT id, name, description, is_system_role, is_owner_role, is_active, created_at, updated_at
FROM   roles
WHERE  is_active = TRUE
ORDER  BY name;

-- name: GetRoleByID :one
SELECT id, name, description, is_system_role, is_owner_role, is_active, created_at, updated_at
FROM   roles WHERE id = @id::uuid;

-- name: GetRoleByName :one
SELECT id, name, description, is_system_role, is_owner_role, is_active, created_at, updated_at
FROM   roles WHERE name = @name;

-- name: CreateRole :one
INSERT INTO roles (name, description, is_system_role, is_owner_role)
VALUES (@name, sqlc.narg('description'), FALSE, FALSE)
RETURNING id, name, description, is_system_role, is_owner_role, is_active, created_at, updated_at;

-- name: UpdateRole :one
-- is_system_role = FALSE guard in WHERE prevents renaming owner/admin/system roles.
-- Returns no rows when the role is a system role → ErrSystemRoleImmutable in service.
UPDATE roles
SET name        = COALESCE(sqlc.narg('name'),        name),
    description = COALESCE(sqlc.narg('description'), description)
WHERE id             = @id::uuid
  AND is_system_role = FALSE
RETURNING id, name, description, is_system_role, is_owner_role, is_active, created_at, updated_at;

-- name: DeactivateRole :execrows
-- Soft-delete. Hard DELETE is blocked by RESTRICT FKs on audit tables.
-- is_system_role = FALSE guard prevents deactivating owner/admin/system roles.
UPDATE roles
SET is_active = FALSE
WHERE id             = @id::uuid
  AND is_system_role = FALSE
  AND is_active      = TRUE;
```

### Role permissions

```sql
-- name: GetRolePermissions :many
SELECT p.id, p.canonical_name, p.name, p.resource_type, p.description,
       rp.conditions, rp.created_at AS granted_at
FROM   role_permissions rp
JOIN   permissions p ON p.id = rp.permission_id
WHERE  rp.role_id  = @role_id::uuid
  AND  p.is_active = TRUE
ORDER  BY p.canonical_name;

-- name: AddRolePermission :exec
INSERT INTO role_permissions (role_id, permission_id, granted_by, granted_reason, conditions)
VALUES (
    @role_id::uuid,
    @permission_id::uuid,
    @granted_by::uuid,
    @granted_reason,
    COALESCE(sqlc.narg('conditions')::jsonb, '{}')
)
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- name: RemoveRolePermission :execrows
DELETE FROM role_permissions
WHERE role_id = @role_id::uuid AND permission_id = @permission_id::uuid;
```

### Permissions

```sql
-- name: GetPermissions :many
SELECT id, canonical_name, name, resource_type, description, is_active, created_at
FROM   permissions
WHERE  is_active = TRUE
ORDER  BY canonical_name;

-- name: GetPermissionByCanonicalName :one
SELECT id, canonical_name, name, resource_type, description, is_active
FROM   permissions
WHERE  canonical_name = @canonical_name
  AND  is_active      = TRUE;

-- name: GetPermissionGroups :many
SELECT id, name, display_label, icon, color_hex, display_order, is_visible
FROM   permission_groups
WHERE  is_active = TRUE
ORDER  BY display_order, name;

-- name: GetPermissionGroupMembers :many
SELECT p.id, p.canonical_name, p.name, p.resource_type, p.description
FROM   permission_group_members pgm
JOIN   permissions p ON p.id = pgm.permission_id
WHERE  pgm.group_id = @group_id::uuid
  AND  p.is_active  = TRUE
ORDER  BY p.canonical_name;
```

### User role

```sql
-- name: GetUserRole :one
-- Returns the user's current role if one exists and has not expired.
-- Returns pgx.ErrNoRows when the user has no role assignment.
SELECT ur.user_id, ur.role_id, r.name AS role_name, r.is_owner_role,
       ur.expires_at, ur.created_at AS granted_at, ur.granted_reason
FROM   user_roles ur
JOIN   roles r ON r.id = ur.role_id
WHERE  ur.user_id = @user_id::uuid
  AND  (ur.expires_at IS NULL OR ur.expires_at > NOW());
```

### User permissions (direct grants)

```sql
-- name: GetUserPermissions :many
-- Returns all active direct-grant permissions for the user.
SELECT up.id, p.canonical_name, p.name, p.resource_type,
       up.conditions, up.expires_at, up.created_at AS granted_at, up.granted_reason
FROM   user_permissions up
JOIN   permissions p ON p.id = up.permission_id
WHERE  up.user_id    = @user_id::uuid
  AND  up.expires_at > NOW()
  AND  p.is_active   = TRUE
ORDER  BY p.canonical_name;

-- name: GrantUserPermission :one
INSERT INTO user_permissions (user_id, permission_id, granted_by, granted_reason, expires_at, conditions)
VALUES (
    @user_id::uuid,
    @permission_id::uuid,
    @granted_by::uuid,
    @granted_reason,
    @expires_at::timestamptz,
    COALESCE(sqlc.narg('conditions')::jsonb, '{}')
)
ON CONFLICT (user_id, permission_id) DO UPDATE
    SET granted_by     = EXCLUDED.granted_by,
        granted_reason = EXCLUDED.granted_reason,
        expires_at     = EXCLUDED.expires_at,
        conditions     = EXCLUDED.conditions,
        updated_at     = NOW()
RETURNING id, user_id, permission_id, expires_at, created_at;

-- name: RevokeUserPermission :execrows
-- Revocation = DELETE; history preserved in user_permissions_audit.
DELETE FROM user_permissions WHERE id = @id::uuid AND user_id = @user_id::uuid;
```

---

## 5. Seeds

### `sql/seeds/002_permissions.sql`

Inserts all permissions the application will ever check. New features add rows here
before writing middleware guards. `ON CONFLICT DO NOTHING` makes it idempotent.

**Permission naming rule:** `resource_type:action` — canonical_name is generated by the DB.

| canonical_name | resource_type | name | Notes |
|---|---|---|---|
| `rbac:read` | rbac | read | List roles, permissions, user assignments |
| `rbac:manage` | rbac | manage | Create/edit roles, assign role permissions |
| `rbac:grant_user_permission` | rbac | grant_user_permission | Grant direct user permissions (higher sensitivity) |
| `job_queue:read` | job_queue | read | View jobs, stats, metrics, schedules |
| `job_queue:manage` | job_queue | manage | Pause, retry, cancel, update priority |
| `user:read` | user | read | List/view users (future) |
| `user:manage` | user | manage | Edit/suspend users (future) |
| `request:read` | request | read | View requests (future) |
| `request:manage` | request | manage | Manage requests (future) |
| `request:approve` | request | approve | Approve requests (future) |

Permission groups (for admin UI organisation):

| group name | permissions |
|---|---|
| System Administration | rbac:read, rbac:manage, rbac:grant_user_permission |
| Job Queue | job_queue:read, job_queue:manage |
| Users | user:read, user:manage |
| Requests | request:read, request:manage, request:approve |

### `sql/seeds/003_roles.sql`

Seeds the non-owner system roles and their default permissions. Owner role already
exists from `001_roles.sql`. `granted_by` for seed grants uses the owner role user
looked up via a CTE — if no owner exists yet, uses a sentinel system UUID.

| Role | is_system_role | Default permissions |
|---|---|---|
| admin | TRUE | All 10 permissions above |
| vendor | FALSE | request:read, request:manage |
| customer | FALSE | request:read |

---

## 6. Type Contracts

### `internal/platform/rbac/checker.go`

```go
// Permission constants — import these everywhere; never use raw string literals.
const (
    PermRBACRead              = "rbac:read"
    PermRBACManage            = "rbac:manage"
    PermRBACGrantUserPerm     = "rbac:grant_user_permission"
    PermJobQueueRead          = "job_queue:read"
    PermJobQueueManage        = "job_queue:manage"
    PermUserRead              = "user:read"
    PermUserManage            = "user:manage"
    PermRequestRead           = "request:read"
    PermRequestManage         = "request:manage"
    PermRequestApprove        = "request:approve"
)

// Checker performs RBAC permission checks against the database.
// All methods are safe for concurrent use from multiple goroutines.
type Checker struct {
    pool *pgxpool.Pool
    q    db.Querier
}

func NewChecker(pool *pgxpool.Pool) *Checker

// IsOwner reports whether userID holds the active owner role.
func (c *Checker) IsOwner(ctx context.Context, userID string) (bool, error)

// HasPermission reports whether userID holds the given canonical permission
// via their role or a direct grant.
func (c *Checker) HasPermission(ctx context.Context, userID, permission string) (bool, error)

// Require returns chi-compatible middleware that enforces the given permission.
// Prerequisite: token.Auth must run first — it injects userID into context.
//
//   r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermJobQueueRead)).Get("/stats", h.Stats)
//
// 401 when no userID is in context (token.Auth did not run or token was invalid).
// 403 when authenticated but permission is not held.
// 500 on a transient DB error — fails closed, never grants access on error.
func (c *Checker) Require(permission string) func(http.Handler) http.Handler
```

### `internal/platform/rbac/errors.go`

```go
var ErrForbidden       = errors.New("insufficient permissions")
var ErrUnauthenticated = errors.New("authentication required")
var ErrSystemRoleImmutable = errors.New("system roles cannot be modified")
var ErrCannotReassignOwner = errors.New("owner role cannot be reassigned via this route")
var ErrCannotModifyOwnRole = errors.New("you cannot modify your own role assignment")
var ErrOwnerAlreadyExists  = errors.New("an active owner already exists")
```

### `internal/platform/rbac/context.go`

```go
// InjectPermissionsForTest writes a set of allowed permissions into ctx so
// handler tests can bypass the DB check. Same pattern as token.InjectUserIDForTest.
// Never call from production code.
func InjectPermissionsForTest(ctx context.Context, perms ...string) context.Context

// HasPermissionInContext checks if a test-injected permission set contains
// the given permission. Returns (false, false) when no test set is present —
// Checker falls through to the DB.
func HasPermissionInContext(ctx context.Context, permission string) (allowed, found bool)
```

### `internal/app/deps.go` additions

```go
// RBAC is the permission checker. Used by domain routes via deps.RBAC.Require("...").
// Constructed once in server.New from the shared DB pool.
RBAC *rbac.Checker
```

### Domain models

```go
// bootstrap/models.go
type BootstrapInput  struct { UserID string }
type BootstrapResult struct {
    UserID    string    `json:"user_id"`
    RoleName  string    `json:"role_name"`
    GrantedAt time.Time `json:"granted_at"`
}

// roles/models.go
type Role struct {
    ID           string    `json:"id"`
    Name         string    `json:"name"`
    Description  string    `json:"description,omitempty"`
    IsSystemRole bool      `json:"is_system_role"`
    IsOwnerRole  bool      `json:"is_owner_role"`
    CreatedAt    time.Time `json:"created_at"`
}
type RolePermission struct {
    PermissionID  string          `json:"permission_id"`
    CanonicalName string          `json:"canonical_name"`
    ResourceType  string          `json:"resource_type"`
    Name          string          `json:"name"`
    Conditions    json.RawMessage `json:"conditions,omitempty"`
}
type CreateRoleInput struct { Name string; Description string }
type UpdateRoleInput struct { Name *string; Description *string }

// userroles/models.go
type UserRole struct {
    UserID      string     `json:"user_id"`
    RoleID      string     `json:"role_id"`
    RoleName    string     `json:"role_name"`
    IsOwnerRole bool       `json:"is_owner_role"`
    ExpiresAt   *time.Time `json:"expires_at,omitempty"`
    GrantedAt   time.Time  `json:"granted_at"`
}
type AssignRoleInput struct {
    RoleID        string     `json:"role_id"`
    GrantedReason string     `json:"granted_reason"`
    ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

// userpermissions/models.go
type UserPermission struct {
    ID            string          `json:"id"`
    CanonicalName string          `json:"canonical_name"`
    ResourceType  string          `json:"resource_type"`
    Conditions    json.RawMessage `json:"conditions,omitempty"`
    ExpiresAt     time.Time       `json:"expires_at"`
    GrantedAt     time.Time       `json:"granted_at"`
    GrantedReason string          `json:"granted_reason"`
}
type GrantPermissionInput struct {
    PermissionID  string          `json:"permission_id"`
    GrantedReason string          `json:"granted_reason"`
    ExpiresAt     time.Time       `json:"expires_at"`
    Conditions    json.RawMessage `json:"conditions,omitempty"`
}
```

---

## 7. REST API

| # | Method | Path | Auth | Permission | Description |
|---|--------|------|------|------------|-------------|
| 1 | POST | `/api/v1/owner/bootstrap` | none | — | Assign owner role; 409 if owner exists |
| 2 | GET | `/api/v1/admin/rbac/roles` | JWT | `rbac:read` | List all active roles |
| 3 | POST | `/api/v1/admin/rbac/roles` | JWT | `rbac:manage` | Create a new role |
| 4 | GET | `/api/v1/admin/rbac/roles/:id` | JWT | `rbac:read` | Get role by ID |
| 5 | PATCH | `/api/v1/admin/rbac/roles/:id` | JWT | `rbac:manage` | Update name/description (non-system only) |
| 6 | DELETE | `/api/v1/admin/rbac/roles/:id` | JWT | `rbac:manage` | Soft-delete role (non-system only) |
| 7 | GET | `/api/v1/admin/rbac/roles/:id/permissions` | JWT | `rbac:read` | List permissions on role |
| 8 | POST | `/api/v1/admin/rbac/roles/:id/permissions` | JWT | `rbac:manage` | Add permission to role |
| 9 | DELETE | `/api/v1/admin/rbac/roles/:id/permissions/:perm_id` | JWT | `rbac:manage` | Remove permission from role |
| 10 | GET | `/api/v1/admin/rbac/permissions` | JWT | `rbac:read` | List all active permissions |
| 11 | GET | `/api/v1/admin/rbac/permissions/groups` | JWT | `rbac:read` | List permission groups with members |
| 12 | GET | `/api/v1/admin/rbac/users/:user_id/role` | JWT | `rbac:read` | Get user's current role |
| 13 | PUT | `/api/v1/admin/rbac/users/:user_id/role` | JWT | `rbac:manage` | Assign or replace user's role |
| 14 | DELETE | `/api/v1/admin/rbac/users/:user_id/role` | JWT | `rbac:manage` | Remove user's role |
| 15 | GET | `/api/v1/admin/rbac/users/:user_id/permissions` | JWT | `rbac:read` | List active direct grants |
| 16 | POST | `/api/v1/admin/rbac/users/:user_id/permissions` | JWT | `rbac:grant_user_permission` | Grant direct permission to user |
| 17 | DELETE | `/api/v1/admin/rbac/users/:user_id/permissions/:grant_id` | JWT | `rbac:grant_user_permission` | Revoke direct permission grant |

**Route 1 (bootstrap) notes:**
- Unauthenticated — the only unauthenticated write route in the system.
- 409 `owner_already_exists` if any active owner role assignment exists.
- Rate-limited: 3 req / 15 min per IP.
- Target `user_id` must be an existing active, email-verified user.
- Uses `user_id` as its own `granted_by` (self-grant for bootstrap only).

**Routes 5, 6 (system role guard):**
- The `UpdateRole` and `DeactivateRole` queries enforce `is_system_role = FALSE`
  at the DB level. Zero rows affected → service returns `ErrSystemRoleImmutable` → 409.

**Route 13 (assign role) guards:**
- Cannot reassign owner users → `ErrCannotReassignOwner` → 409.
- Cannot assign to self → `ErrCannotModifyOwnRole` → 409.
- `fn_prevent_orphaned_owner` fires at DB level if re-assigning the last owner.

---

## 8. Middleware Design

### Usage in any domain route

```go
// Any domain's routes.go — after deps.RBAC is wired
r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermJobQueueRead)).
    Get("/stats", h.Stats)

r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermJobQueueManage)).
    Post("/jobs/{id}/retry", h.Retry)
```

### `Require` implementation sketch

```go
func (c *Checker) Require(permission string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            userID, ok := token.UserIDFromContext(r.Context())
            if !ok || userID == "" {
                respond.Error(w, http.StatusUnauthorized, "authentication_required",
                    "authentication is required")
                return
            }

            // Test hook: bypass DB when permissions are injected in context.
            if allowed, found := HasPermissionInContext(r.Context(), permission); found {
                if !allowed {
                    respond.Error(w, http.StatusForbidden, "forbidden",
                        "insufficient permissions")
                    return
                }
                next.ServeHTTP(w, r)
                return
            }

            row, err := c.q.CheckUserAccess(r.Context(), db.CheckUserAccessParams{
                UserID:     parseUUID(userID),
                Permission: permission,
            })
            if err != nil {
                slog.ErrorContext(r.Context(), "rbac.Require: db check", "error", err)
                // Fail closed — never grant access on a transient DB error.
                respond.Error(w, http.StatusInternalServerError, "internal_error",
                    "internal server error")
                return
            }

            if !row.IsOwner && !row.HasPermission {
                respond.Error(w, http.StatusForbidden, "forbidden",
                    "insufficient permissions")
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}
```

### V2: Caching (post-launch)

V1 makes one DB query per guarded request. The query is fast — two EXISTS subqueries
on covering indexes. Acceptable at current scale.

When RBAC check latency shows up in profiling, add a short-TTL in-memory cache in
`Checker` keyed by `(userID, permission)` with a 30-second TTL. Invalidate entries
on any of: `AssignUserRole`, `RemoveUserRole`, `GrantUserPermission`,
`RevokeUserPermission`, `AddRolePermission`, `RemoveRolePermission`. One method
added to `Checker` (`invalidate(userID)`), called by each store method. Zero changes
to `Require` — it checks cache first, falls back to DB on miss.

---

## 9. Bootstrap Flow

### The problem

How does the first admin user get the owner role if every write route requires auth
and RBAC? The system can't grant what doesn't exist yet.

### Solution

`POST /owner/bootstrap` is unauthenticated. It:

1. `CountActiveOwners` — if > 0, return 409 `owner_already_exists`.
2. `GetActiveUserByID` — verify target user exists, is active, is email-verified.
3. `GetOwnerRoleID` — look up the owner role seeded in `001_roles.sql`.
4. `AssignUserRole` — insert with `granted_by = user_id` (self-grant, acceptable only here).
5. Return `{ user_id, role_name, granted_at }`.

Once step 4 succeeds, the endpoint permanently returns 409 on every future call.

### First-run sequence

```
1. Deploy the app (schema + seeds applied)
2. Register a user account: POST /api/v1/auth/register
3. Verify the email
4. POST /api/v1/owner/bootstrap  { "user_id": "<uuid>" }
   → assigns owner role
5. All RBAC-guarded routes are now accessible for that user
6. Bootstrap permanently returns 409 from now on
```

---

## 10. Decisions

| ID | Decision | Rationale |
|----|----------|-----------|
| D-R1 | Platform package for checker, domain package for admin API | Checker is used by every domain — it belongs in `platform/`. The admin API is a domain concern. |
| D-R2 | Single `CheckUserAccess` query combining `is_owner` + `has_permission` | One DB round-trip per guarded request. UNION ALL handles role + direct-grant paths. |
| D-R3 | No caching in V1 | Query is fast and fully indexed. Premature caching adds invalidation complexity before there is a measured problem. |
| D-R4 | `POST /owner/bootstrap` is unauthenticated | Chicken-and-egg: can't authenticate to get owner permissions if no owner exists. Rate-limited + 409 guard makes it safe. |
| D-R5 | System roles are immutable via API | The DB WHERE `is_system_role = FALSE` guard enforces this. Renaming `owner` or `admin` would break seed-based assumptions. |
| D-R6 | Owner role reassignment is out of scope for V1 | High-risk operation that needs its own flow (confirmation, two-owner window). Not needed at launch scale. |
| D-R7 | `granted_by` references `users.id` with RESTRICT FK | Every grant has a named human accountable. You cannot delete a user who has granted permissions — they must be transferred first. |
| D-R8 | Role deletion = soft-delete | RESTRICT FKs on audit tables block hard DELETE. Soft-delete preserves audit trail. |
| D-R9 | Context injection for tests (`InjectPermissionsForTest`) | Same pattern as `token.InjectUserIDForTest`. Handler tests can exercise RBAC paths without a real DB. |
| D-R10 | Permission constants in `internal/platform/rbac/` | Raw string literals scattered across domains are a typo risk. Constants are compile-checked. |
| D-R11 | Fail closed on DB error in `Require` | On a transient DB failure, return 500 rather than granting access. Denying on uncertainty is the correct security posture. |

---

## 11. Tests

### Checker (unit — uses `InjectPermissionsForTest` or test DB)

| # | Case | Layer |
|---|------|-------|
| T-R01 | `Require` passes for owner regardless of permission name | U |
| T-R02 | `Require` passes when user has permission via role | I |
| T-R03 | `Require` passes when user has permission via direct grant | I |
| T-R04 | `Require` returns 403 when user has no role and no direct grant | I |
| T-R05 | `Require` returns 403 when direct grant is expired | I |
| T-R06 | `Require` returns 401 when no userID in context | U |
| T-R07 | `Require` uses test-injected permissions when present (no DB hit) | U |
| T-R08 | `Require` returns 500 and denies access on DB error (fail closed) | U |
| T-R09 | `IsOwner` returns true for owner role user | I |
| T-R10 | `IsOwner` returns false for non-owner user | I |
| T-R11 | `HasPermission` returns true via role path | I |
| T-R12 | `HasPermission` returns true via direct-grant path | I |
| T-R13 | `HasPermission` returns false after role permission is removed | I |

### Bootstrap

| # | Case | Layer |
|---|------|-------|
| T-R14 | Bootstrap succeeds when no owner exists | I |
| T-R15 | Bootstrap returns 409 when owner already exists | I |
| T-R16 | Bootstrap returns 422 when `user_id` is unknown | I |
| T-R17 | Bootstrap returns 422 when user is not email-verified | I |
| T-R18 | Bootstrap is rate-limited (3 req / 15 min per IP) | U |

### Roles API

| # | Case | Layer |
|---|------|-------|
| T-R19 | `GET /roles` returns seeded roles | I |
| T-R20 | `POST /roles` creates a new role | I |
| T-R21 | `PATCH /roles/:id` updates name for non-system role | I |
| T-R22 | `PATCH /roles/:id` returns 409 for system role | I |
| T-R23 | `DELETE /roles/:id` soft-deletes non-system role | I |
| T-R24 | `DELETE /roles/:id` returns 409 for system role | I |
| T-R25 | `GET /roles/:id/permissions` lists role permissions | I |
| T-R26 | `POST /roles/:id/permissions` adds permission to role | I |
| T-R27 | `DELETE /roles/:id/permissions/:perm_id` removes permission | I |

### Permissions API

| # | Case | Layer |
|---|------|-------|
| T-R28 | `GET /permissions` returns all seeded active permissions | I |
| T-R29 | `GET /permissions/groups` returns groups with members | I |

### User role management

| # | Case | Layer |
|---|------|-------|
| T-R30 | `PUT /users/:id/role` assigns role; `GET` returns it | I |
| T-R31 | `PUT /users/:id/role` replaces an existing role | I |
| T-R32 | `PUT /users/:id/role` returns 409 for owner target user | I |
| T-R33 | `PUT /users/:id/role` returns 409 for self-assignment | I |
| T-R34 | `DELETE /users/:id/role` removes role | I |

### User permissions management

| # | Case | Layer |
|---|------|-------|
| T-R35 | `POST /users/:id/permissions` grants direct permission | I |
| T-R36 | `GET /users/:id/permissions` returns only active grants | I |
| T-R37 | `DELETE /users/:id/permissions/:grant_id` revokes grant | I |
| T-R38 | Expired grant does not appear in `GET` and does not pass `Require` | I |
| T-R39 | Grant with `expires_at > 90 days` returns 422 (DB trigger fires) | I |
| T-R40 | Granter without the permission cannot grant it (privilege escalation trigger) | I |

---

## 12. File Map

```
sql/queries/rbac.sql                         NEW — all queries above
sql/seeds/002_permissions.sql                NEW — all permissions + groups
sql/seeds/003_roles.sql                      NEW — admin/vendor/customer + their permissions

internal/platform/rbac/
    checker.go                               NEW — Checker, IsOwner, HasPermission, Require
    context.go                               NEW — InjectPermissionsForTest, HasPermissionInContext
    errors.go                                NEW — all sentinel errors
    checker_test.go                          NEW — T-R01 through T-R13

internal/domain/rbac/
    routes.go                                NEW — assembles owner + admin sub-routers
    bootstrap/
        handler.go, service.go, store.go     NEW
        models.go, routes.go, validators.go  NEW
    roles/
        handler.go, service.go, store.go     NEW
        models.go, routes.go, validators.go  NEW
    permissions/
        handler.go, service.go, store.go     NEW
        models.go, routes.go                 NEW
    userroles/
        handler.go, service.go, store.go     NEW
        models.go, routes.go, validators.go  NEW
    userpermissions/
        handler.go, service.go, store.go     NEW
        models.go, routes.go, validators.go  NEW

internal/app/deps.go                         MODIFY — add RBAC *rbac.Checker
internal/server/server.go                    MODIFY — construct Checker, add to deps
internal/server/routes.go                    MODIFY — mount /owner and /admin/rbac sub-routers
```

---

## 13. Implementation Phases

| Phase | What | Needs | Gate |
|-------|------|-------|------|
| 1 | `sql/queries/rbac.sql` + `sqlc generate` | nothing | `db` package compiles with new generated methods |
| 2 | `sql/seeds/002_permissions.sql` + `003_roles.sql` | Phase 1 | All permissions + roles present in DB; `SELECT COUNT(*) FROM permissions` = 10 |
| 3 | `internal/platform/rbac/` — checker + middleware | Phase 1 | T-R01 through T-R13 green; `go build ./...` passes |
| 4 | Bootstrap route (`/owner/bootstrap`) | Phase 3 | T-R14 through T-R18 green |
| 5 | Permissions read API (routes 10–11) | Phase 3 | T-R28, T-R29 green |
| 6 | Roles API (routes 2–9) | Phase 3 | T-R19 through T-R27 green |
| 7 | User role management (routes 12–14) | Phase 6 | T-R30 through T-R34 green |
| 8 | User permission management (routes 15–17) | Phase 7 | T-R35 through T-R40 green |
| 9 | Wire into server + mount routes | Phases 4–8 | Server boots; `go test ./...` green |

**Phase 3 is the unlock for everything else.** Once the checker and middleware exist
and compile, every other domain — job queue, user management, requests — can add
`deps.RBAC.Require(...)` guards to their routes immediately. Phases 4–8 can even
run in parallel with the job queue implementation phases.

Phase 9 is the only phase that modifies existing files.

---

## 14. Wiring (Phase 9)

### `internal/app/deps.go`

```go
RBAC *rbac.Checker  // permission checker; use deps.RBAC.Require("resource:action")
```

### `internal/server/server.go`

```go
// After pool is available — one line:
deps.RBAC = rbac.NewChecker(pool)
```

### `internal/server/routes.go`

```go
r.Route("/api/v1", func(r chi.Router) {
    r.Mount("/auth",    auth.Routes(ctx, deps))
    r.Mount("/oauth",   oauth.Routes(ctx, deps))
    r.Mount("/profile", profile.Routes(ctx, deps))
    r.Mount("/owner",   rbacdomain.OwnerRoutes(ctx, deps))  // bootstrap only
    r.Mount("/admin",   rbacdomain.AdminRoutes(ctx, deps))  // /admin/rbac/*
})
```

### Job queue routes (now possible after Phase 3)

```go
// internal/platform/jobqueue/api.go
r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermJobQueueRead)).
    Get("/stats", h.Stats)

r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermJobQueueManage)).
    Post("/jobs/{id}/retry", h.Retry)
```
