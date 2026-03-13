# RBAC Design — v2

## Changelog

| Version | Change |
|---------|--------|
| v1 | Initial design |
| v2 | Added `access_type` (direct/conditional/request/denied) and `scope` (own/all) to `role_permissions` and `user_permissions`; split `job_queue:manage` into `job_queue:manage` + `job_queue:configure`; replaced `user:ban` with `user:lock` backed by existing `admin_locked` field; added `user:lock` metadata columns to `users`; added `permission_request_approvers` table; updated `CheckUserAccess` to return access_type + scope + conditions; updated middleware to act on access_type; permission count 10 → 13; updated all seeds, gates, and file maps |
| v3 | Schema (Phase 0) and queries (Phase 1) complete. `CheckUserAccess` rewritten to single-pass CTE with `MATERIALIZED` hints; `fn_prevent_owner_role_escalation` collapsed to single query; `idx_roles_owner_active` partial index added; `idx_user_roles_lookup` removed (redundant with PK); `idx_ur_audit_change_type` added; colon-guard `CHECK` constraints added to `permissions`; `Issue 4` (access_type dead column) retracted — column is correct and intentional. Three operational TODOs carried forward to Phase 1 gate. |

---

## 1. What Already Exists

| Already done | Still needed |
|---|---|
| `roles`, `permissions`, `permission_groups` tables | Permission seeds (`sql/seeds/002_permissions.sql`) |
| `role_permissions`, `user_roles`, `user_permissions` tables | Role seeds (`sql/seeds/003_roles.sql`) |
| `permission_condition_templates`, all audit tables | `internal/platform/rbac/` — checker + middleware |
| `permission_group_members` | `internal/domain/rbac/` — admin API |
| All triggers (audit, privilege escalation, orphaned owner, expiry) | ~~Schema additions in `003_rbac.sql`~~ ✅ done |
| `001_roles.sql` seed — owner role row | ~~`001_core.sql` additions (admin_locked metadata columns)~~ ✅ done |
| `users.admin_locked` + `users.is_locked` fields | |
| ✅ `sql/queries/rbac.sql` — all 25 queries | |
| ✅ `003_rbac.sql` — access_type/scope ENUMs, role_permissions/user_permissions columns, permission_request_approvers, all indexes | |
| ✅ `004_rbac_functions.sql` — all trigger functions updated | |

The only seed that exists is the `owner` role row. Every permission and every other
role needs to be seeded by this design.

---

## 2. Schema Additions (Phase 0 — before any queries or seeds)

These additions must be applied before Stage 1 begins.

### 2a. `001_core.sql` — `users` table additions

`admin_locked` already exists. These columns record who set it, when, and why —
so you don't have to dig through `auth_audit_log` to find the actor and reason.

```sql
-- Metadata for admin_locked. All three are NULL when admin_locked = FALSE.
admin_locked_by     UUID        REFERENCES users(id) ON DELETE SET NULL,
admin_locked_reason TEXT,
admin_locked_at     TIMESTAMPTZ,

CONSTRAINT chk_users_admin_lock_coherent CHECK (
    (admin_locked = FALSE AND admin_locked_reason IS NULL AND admin_locked_by IS NULL)
    OR
    (admin_locked = TRUE  AND admin_locked_reason IS NOT NULL AND admin_locked_at IS NOT NULL)
),
CONSTRAINT chk_users_no_self_lock
    CHECK (admin_locked_by IS NULL OR admin_locked_by != id)
```

### 2b. `003_rbac.sql` — new ENUMs

```sql
CREATE TYPE permission_access_type AS ENUM (
    'direct',       -- granted, no friction
    'conditional',  -- granted but conditions JSONB is enforced by app layer
    'request',      -- must submit a request that gets approved first
    'denied'        -- explicitly blocked
);

CREATE TYPE permission_scope AS ENUM (
    'own',   -- can only act on resources the user owns/created
    'all'    -- can act on any resource of this type
);
```

### 2c. `003_rbac.sql` — additions to `role_permissions`

```sql
-- Add after the existing conditions column:
access_type permission_access_type NOT NULL DEFAULT 'direct',
scope       permission_scope       NOT NULL DEFAULT 'own',

-- conditional grants must carry actual conditions
CONSTRAINT chk_rp_conditional_needs_conditions
    CHECK (access_type != 'conditional' OR conditions != '{}'),

-- denied and request grants carry no conditions
CONSTRAINT chk_rp_denied_no_conditions
    CHECK (access_type != 'denied' OR conditions = '{}'),
CONSTRAINT chk_rp_request_no_conditions
    CHECK (access_type != 'request' OR conditions = '{}'),
```

### 2d. `003_rbac.sql` — additions to `user_permissions`

```sql
-- Add after the existing conditions column:
-- access_type is always 'direct' on user_permissions — direct grants never
-- go through the request flow. scope controls resource visibility.
scope permission_scope NOT NULL DEFAULT 'own',
```

### 2e. `003_rbac.sql` — additions to audit tables

`role_permissions_audit` and `user_permissions_audit` must also snapshot the new
columns so you can see what changed:

```sql
-- role_permissions_audit additions:
access_type          permission_access_type,
previous_access_type permission_access_type,
scope                permission_scope,
previous_scope       permission_scope,

-- user_permissions_audit additions:
scope          permission_scope,
previous_scope permission_scope,
```

The trigger functions `fn_audit_role_permissions` and `fn_audit_user_permissions`
in `004_rbac_functions.sql` must be updated to capture these fields before/after.

### 2f. `003_rbac.sql` — new table: `permission_request_approvers`

When `access_type = 'request'` fires for a permission, this table tells the app
which roles must approve before the action executes. It reuses the existing
`005_requests.sql` approval workflow — no duplicate machinery.

```sql
CREATE TABLE permission_request_approvers (
    permission_id  UUID    NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    role_id        UUID    NOT NULL REFERENCES roles(id)       ON DELETE CASCADE,
    approval_level INTEGER NOT NULL DEFAULT 0,
    min_required   INTEGER NOT NULL DEFAULT 1,

    created_at TIMESTAMPTZ DEFAULT NOW(),

    PRIMARY KEY (permission_id, role_id),
    CONSTRAINT chk_pra_level_non_negative CHECK (approval_level >= 0),
    CONSTRAINT chk_pra_min_required_pos   CHECK (min_required > 0)
);

CREATE INDEX idx_pra_permission ON permission_request_approvers(permission_id);
CREATE INDEX idx_pra_role       ON permission_request_approvers(role_id);
CREATE INDEX idx_pra_level      ON permission_request_approvers(permission_id, approval_level);
```

**How `access_type = 'request'` works at runtime:**
1. User attempts an action guarded by a `request`-type permission.
2. Middleware reads `access_type = 'request'` from `CheckUserAccess`.
3. App creates a `requests` row (`request_type = 'permission_action'`,
   `request_data = {"permission": "job_queue:configure", "resource": "..."}`)
4. `request_required_approvers` is populated from `permission_request_approvers`.
5. Middleware returns HTTP 202 with `{"code": "approval_required", "request_id": "..."}`.
6. Once approved, the job queue (`execute_request` handler) executes the action.

---

## 3. The Access Model

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

Scope is injected into context by the middleware and enforced in queries/handlers —
not in the permission check itself.

### Three-layer check model (updated)

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

### Three sources of a permission (unchanged)

**Layer 1 — Owner role.** `is_owner_role = TRUE` bypasses all checks.

**Layer 2 — Role permission.** The user's role has a `role_permissions` row for
the matching permission. `access_type` and `scope` come from this row.

**Layer 3 — Direct grant.** A `user_permissions` row gives the user this permission
temporarily. `access_type` is implicitly `direct`. `scope` comes from the row.

---

## 4. Package Structure

```
internal/platform/rbac/
    checker.go          — Checker struct: IsOwner, HasPermission, Require middleware
    context.go          — inject/read from context for tests + scope/conditions helpers
    errors.go           — ErrForbidden, ErrUnauthenticated, etc.

internal/domain/rbac/
    routes.go           — assembles owner + admin sub-routers
    bootstrap/          — POST /owner/bootstrap (unauthenticated, first-run only)
    roles/              — role CRUD
    permissions/        — permission + group read endpoints
    userroles/          — assign / remove a user's role
    userpermissions/    — grant / revoke direct user permissions
    userlock/           — lock / unlock a user (admin_locked)

sql/queries/rbac.sql    — all RBAC queries (sqlc-generated)
sql/seeds/002_permissions.sql
sql/seeds/003_roles.sql
```

---

## 5. SQL Queries (`sql/queries/rbac.sql`)

### Permission check (hot path)

`CheckUserAccess` now returns `access_type`, `scope`, and `conditions` in addition
to `is_owner` and `has_permission`. The middleware acts on `access_type` directly.

```sql
-- name: CheckUserAccess :one
-- Returns full access context in one round-trip.
-- is_owner short-circuits in Go — all other fields ignored when is_owner = true.
-- Index usage:
--   is_owner:       idx_roles_owner + idx_user_roles_lookup
--   role path:      idx_role_perms_covering + idx_user_roles_lookup
--   direct path:    idx_user_permissions_user_expires
SELECT
    -- Layer 1: owner check
    EXISTS(
        SELECT 1
        FROM   user_roles ur
        JOIN   roles r ON r.id = ur.role_id
        WHERE  ur.user_id      = @user_id::uuid
          AND  r.is_owner_role = TRUE
          AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
    ) AS is_owner,

    -- Layer 2 + 3: permission existence
    EXISTS(
        SELECT 1
        FROM   user_roles ur
        JOIN   role_permissions rp ON rp.role_id    = ur.role_id
        JOIN   permissions p       ON p.id          = rp.permission_id
        WHERE  ur.user_id          = @user_id::uuid
          AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
          AND  p.canonical_name    = @permission
          AND  p.is_active         = TRUE
          AND  rp.access_type     != 'denied'
        UNION ALL
        SELECT 1
        FROM   user_permissions up
        JOIN   permissions p ON p.id = up.permission_id
        WHERE  up.user_id         = @user_id::uuid
          AND  up.expires_at      > NOW()
          AND  p.canonical_name   = @permission
          AND  p.is_active        = TRUE
    ) AS has_permission,

    -- access_type: role path takes priority over direct grant
    COALESCE(
        (SELECT rp.access_type
         FROM   user_roles ur
         JOIN   role_permissions rp ON rp.role_id    = ur.role_id
         JOIN   permissions p       ON p.id          = rp.permission_id
         WHERE  ur.user_id          = @user_id::uuid
           AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
           AND  p.canonical_name    = @permission
           AND  p.is_active         = TRUE
         LIMIT 1),
        'direct'::permission_access_type
    ) AS access_type,

    -- scope: role path takes priority over direct grant
    COALESCE(
        (SELECT rp.scope
         FROM   user_roles ur
         JOIN   role_permissions rp ON rp.role_id    = ur.role_id
         JOIN   permissions p       ON p.id          = rp.permission_id
         WHERE  ur.user_id          = @user_id::uuid
           AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
           AND  p.canonical_name    = @permission
           AND  p.is_active         = TRUE
         LIMIT 1),
        (SELECT up.scope
         FROM   user_permissions up
         JOIN   permissions p ON p.id = up.permission_id
         WHERE  up.user_id         = @user_id::uuid
           AND  up.expires_at      > NOW()
           AND  p.canonical_name   = @permission
           AND  p.is_active        = TRUE
         LIMIT 1)
    ) AS scope,

    -- conditions: role path takes priority over direct grant
    COALESCE(
        (SELECT rp.conditions
         FROM   user_roles ur
         JOIN   role_permissions rp ON rp.role_id    = ur.role_id
         JOIN   permissions p       ON p.id          = rp.permission_id
         WHERE  ur.user_id          = @user_id::uuid
           AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
           AND  p.canonical_name    = @permission
           AND  p.is_active         = TRUE
         LIMIT 1),
        (SELECT up.conditions
         FROM   user_permissions up
         JOIN   permissions p ON p.id = up.permission_id
         WHERE  up.user_id         = @user_id::uuid
           AND  up.expires_at      > NOW()
           AND  p.canonical_name   = @permission
           AND  p.is_active        = TRUE
         LIMIT 1),
        '{}'::jsonb
    ) AS conditions;
```

### Bootstrap

```sql
-- name: CountActiveOwners :one
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
SELECT id, email, is_active, email_verified
FROM   users
WHERE  id         = @user_id::uuid
  AND  deleted_at IS NULL;

-- name: AssignUserRole :one
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
-- is_system_role = FALSE guard prevents renaming owner/admin/system roles.
UPDATE roles
SET name        = COALESCE(sqlc.narg('name'),        name),
    description = COALESCE(sqlc.narg('description'), description)
WHERE id             = @id::uuid
  AND is_system_role = FALSE
RETURNING id, name, description, is_system_role, is_owner_role, is_active, created_at, updated_at;

-- name: DeactivateRole :execrows
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
       rp.access_type, rp.scope, rp.conditions, rp.created_at AS granted_at
FROM   role_permissions rp
JOIN   permissions p ON p.id = rp.permission_id
WHERE  rp.role_id  = @role_id::uuid
  AND  p.is_active = TRUE
ORDER  BY p.canonical_name;

-- name: AddRolePermission :exec
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
SELECT up.id, p.canonical_name, p.name, p.resource_type,
       up.scope, up.conditions, up.expires_at,
       up.created_at AS granted_at, up.granted_reason
FROM   user_permissions up
JOIN   permissions p ON p.id = up.permission_id
WHERE  up.user_id    = @user_id::uuid
  AND  up.expires_at > NOW()
  AND  p.is_active   = TRUE
ORDER  BY p.canonical_name;

-- name: GrantUserPermission :one
INSERT INTO user_permissions (
    user_id, permission_id, granted_by, granted_reason,
    expires_at, scope, conditions
)
VALUES (
    @user_id::uuid,
    @permission_id::uuid,
    @granted_by::uuid,
    @granted_reason,
    @expires_at::timestamptz,
    @scope,
    COALESCE(sqlc.narg('conditions')::jsonb, '{}')
)
ON CONFLICT (user_id, permission_id) DO UPDATE
    SET granted_by     = EXCLUDED.granted_by,
        granted_reason = EXCLUDED.granted_reason,
        expires_at     = EXCLUDED.expires_at,
        scope          = EXCLUDED.scope,
        conditions     = EXCLUDED.conditions,
        updated_at     = NOW()
RETURNING id, user_id, permission_id, expires_at, created_at;

-- name: RevokeUserPermission :execrows
DELETE FROM user_permissions WHERE id = @id::uuid AND user_id = @user_id::uuid;
```

### User lock (admin_locked)

```sql
-- name: LockUser :exec
-- Sets admin_locked = TRUE with actor, reason, and timestamp.
UPDATE users
SET admin_locked        = TRUE,
    admin_locked_by     = @locked_by::uuid,
    admin_locked_reason = @reason,
    admin_locked_at     = NOW(),
    updated_at          = NOW()
WHERE id         = @user_id::uuid
  AND deleted_at IS NULL;

-- name: UnlockUser :exec
UPDATE users
SET admin_locked        = FALSE,
    admin_locked_by     = NULL,
    admin_locked_reason = NULL,
    admin_locked_at     = NULL,
    updated_at          = NOW()
WHERE id         = @user_id::uuid
  AND deleted_at IS NULL;

-- name: GetUserLockStatus :one
SELECT id, admin_locked, admin_locked_by, admin_locked_reason, admin_locked_at,
       is_locked, login_locked_until
FROM   users
WHERE  id         = @user_id::uuid
  AND  deleted_at IS NULL;
```

---

## 6. Seeds

### `sql/seeds/002_permissions.sql` — 13 permissions

**Permission naming rule:** `resource_type:action`

| canonical_name | resource_type | name | Capabilities |
|---|---|---|---|
| `rbac:read` | rbac | read | List roles, permissions, user assignments, audit logs |
| `rbac:manage` | rbac | manage | Create/update/soft-delete roles; add/remove role permissions; assign/remove user roles |
| `rbac:grant_user_permission` | rbac | grant_user_permission | Grant/revoke direct time-limited permissions on users |
| `job_queue:read` | job_queue | read | View jobs, workers, queues, schedules, stats, metrics, WS stream |
| `job_queue:manage` | job_queue | manage | Cancel jobs, retry dead/failed jobs, update job priority, purge dead jobs |
| `job_queue:configure` | job_queue | configure | Pause/resume job kinds, force-drain workers, create/update/delete/trigger schedules |
| `user:read` | user | read | List users, view profiles, view audit/login history |
| `user:manage` | user | manage | Edit user details (email, name, etc.) |
| `user:lock` | user | lock | Admin-lock / admin-unlock a user account (`admin_locked` field) |
| `request:read` | request | read | View requests and their history/status |
| `request:manage` | request | manage | Create/edit/cancel requests, manage lifecycle (non-approval steps) |
| `request:approve` | request | approve | Approve or reject a pending request |
| `product:manage` | product | manage | Create/update/delete products (placeholder for store domain) |

Permission groups:

| group name | icon | color | permissions |
|---|---|---|---|
| System Administration | shield | #6366f1 | rbac:read, rbac:manage, rbac:grant_user_permission |
| Job Queue | queue | #f59e0b | job_queue:read, job_queue:manage, job_queue:configure |
| Users | users | #10b981 | user:read, user:manage, user:lock |
| Requests | inbox | #3b82f6 | request:read, request:manage, request:approve |
| Products | package | #8b5cf6 | product:manage |

### `sql/seeds/003_roles.sql` — role assignments

| Permission | owner | admin | vendor | customer |
|---|---|---|---|---|
| rbac:read | ✓ | ✓ | | |
| rbac:manage | ✓ | ✓ | | |
| rbac:grant_user_permission | ✓ | ✓ | | |
| job_queue:read | ✓ | ✓ | | |
| job_queue:manage | ✓ | ✓ | | |
| job_queue:configure | ✓ | ✓ (request) | | |
| user:read | ✓ | ✓ | | |
| user:manage | ✓ | ✓ | | |
| user:lock | ✓ | ✓ (request) | | |
| request:read | ✓ | ✓ | ✓ | ✓ |
| request:manage | ✓ | ✓ | ✓ | |
| request:approve | ✓ | ✓ | | |
| product:manage | ✓ | ✓ | ✓ (conditional, own) | |

Notes on access_type in seeds:
- `job_queue:configure` for admin → `access_type = 'request'` (pausing all workers is high blast radius; requires owner approval)
- `user:lock` for admin → `access_type = 'request'` (locking a user is a policy decision; requires owner approval)
- `product:manage` for vendor → `access_type = 'conditional'`, `scope = 'own'`, `conditions = {"max_price": 1000}` (vendor manages their own products up to $1000; above that requires a request)

Owner bypasses all checks at the middleware level — the table above shows what
gets seeded for audit trail purposes only.

`granted_by` for seed grants uses the owner role user looked up via CTE —
falls back to sentinel UUID `'00000000-0000-0000-0000-000000000000'` if no owner
exists yet (first-deploy scenario).

Total role-permission rows seeded: 16 (owner gets all 13 + admin gets 13 minus
the 2 request-type ones are still seeded but with access_type='request').

---

## 7. Type Contracts

### `internal/platform/rbac/checker.go`

```go
// Permission constants — import these everywhere; never use raw string literals.
const (
    PermRBACRead              = "rbac:read"
    PermRBACManage            = "rbac:manage"
    PermRBACGrantUserPerm     = "rbac:grant_user_permission"
    PermJobQueueRead          = "job_queue:read"
    PermJobQueueManage        = "job_queue:manage"
    PermJobQueueConfigure     = "job_queue:configure"
    PermUserRead              = "user:read"
    PermUserManage            = "user:manage"
    PermUserLock              = "user:lock"
    PermRequestRead           = "request:read"
    PermRequestManage         = "request:manage"
    PermRequestApprove        = "request:approve"
    PermProductManage         = "product:manage"
)

// AccessResult is the full access context returned by CheckUserAccess.
// Injected into context by Require middleware for downstream handlers.
type AccessResult struct {
    IsOwner       bool
    HasPermission bool
    AccessType    string          // "direct" | "conditional" | "request" | "denied"
    Scope         string          // "own" | "all"
    Conditions    json.RawMessage // '{}' when no conditions
}

// Checker performs RBAC permission checks against the database.
// All methods are safe for concurrent use from multiple goroutines.
type Checker struct {
    pool *pgxpool.Pool
    q    db.Querier
}

func NewChecker(pool *pgxpool.Pool) *Checker

// IsOwner reports whether userID holds the active owner role.
func (c *Checker) IsOwner(ctx context.Context, userID string) (bool, error)

// HasPermission reports whether userID holds the given canonical permission.
func (c *Checker) HasPermission(ctx context.Context, userID, permission string) (bool, error)

// Require returns chi-compatible middleware that enforces the given permission.
// Prerequisite: token.Auth must run first.
//
// Behaviour by access_type:
//   direct      → 200 path, inject scope+conditions into context
//   conditional → 200 path, inject scope+conditions into context (handler enforces)
//   request     → 202, {"code":"approval_required","request_id":"<uuid>"}
//   denied      → 403
//   not found   → 403
//   owner       → 200 path, unrestricted (access_type ignored)
//
// 401 when no userID in context.
// 500 on DB error — fails closed.
func (c *Checker) Require(permission string) func(http.Handler) http.Handler
```

### `internal/platform/rbac/context.go`

```go
// InjectPermissionsForTest writes allowed permissions into ctx for handler tests.
// Never call from production code.
func InjectPermissionsForTest(ctx context.Context, perms ...string) context.Context

// HasPermissionInContext checks test-injected permissions.
// Returns (false, false) when no test set present — Checker falls through to DB.
func HasPermissionInContext(ctx context.Context, permission string) (allowed, found bool)

// AccessResultFromContext returns the AccessResult injected by Require middleware.
// Returns nil when called outside a guarded route.
func AccessResultFromContext(ctx context.Context) *AccessResult

// ScopeFromContext returns the scope ("own"|"all") from the current request context.
// Returns "own" as the safe default when not set.
func ScopeFromContext(ctx context.Context) string

// ConditionsFromContext returns the conditions JSONB from the current request context.
// Returns '{}' when not set.
func ConditionsFromContext(ctx context.Context) json.RawMessage
```

### `internal/platform/rbac/errors.go`

```go
var ErrForbidden              = errors.New("insufficient permissions")
var ErrUnauthenticated        = errors.New("authentication required")
var ErrApprovalRequired       = errors.New("action requires approval — request submitted")
var ErrSystemRoleImmutable    = errors.New("system roles cannot be modified")
var ErrCannotReassignOwner    = errors.New("owner role cannot be reassigned via this route")
var ErrCannotModifyOwnRole    = errors.New("you cannot modify your own role assignment")
var ErrOwnerAlreadyExists     = errors.New("an active owner already exists")
var ErrCannotLockOwner        = errors.New("owner accounts cannot be admin-locked")
var ErrCannotLockSelf         = errors.New("you cannot lock your own account")
```

### `internal/app/deps.go` additions

```go
RBAC *rbac.Checker  // use deps.RBAC.Require("resource:action")
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
    AccessType    string          `json:"access_type"`
    Scope         string          `json:"scope"`
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
    Scope         string          `json:"scope"`
    Conditions    json.RawMessage `json:"conditions,omitempty"`
    ExpiresAt     time.Time       `json:"expires_at"`
    GrantedAt     time.Time       `json:"granted_at"`
    GrantedReason string          `json:"granted_reason"`
}
type GrantPermissionInput struct {
    PermissionID  string          `json:"permission_id"`
    GrantedReason string          `json:"granted_reason"`
    Scope         string          `json:"scope"`
    ExpiresAt     time.Time       `json:"expires_at"`
    Conditions    json.RawMessage `json:"conditions,omitempty"`
}

// userlock/models.go
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

## 8. REST API

| # | Method | Path | Auth | Permission | Description |
|---|--------|------|------|------------|-------------|
| 1 | POST | `/api/v1/owner/bootstrap` | none | — | Assign owner role; 409 if owner exists |
| 2 | GET | `/api/v1/admin/rbac/roles` | JWT | `rbac:read` | List all active roles |
| 3 | POST | `/api/v1/admin/rbac/roles` | JWT | `rbac:manage` | Create a new role |
| 4 | GET | `/api/v1/admin/rbac/roles/:id` | JWT | `rbac:read` | Get role by ID |
| 5 | PATCH | `/api/v1/admin/rbac/roles/:id` | JWT | `rbac:manage` | Update name/description (non-system only) |
| 6 | DELETE | `/api/v1/admin/rbac/roles/:id` | JWT | `rbac:manage` | Soft-delete role (non-system only) |
| 7 | GET | `/api/v1/admin/rbac/roles/:id/permissions` | JWT | `rbac:read` | List permissions on role (includes access_type + scope) |
| 8 | POST | `/api/v1/admin/rbac/roles/:id/permissions` | JWT | `rbac:manage` | Add permission to role (with access_type + scope) |
| 9 | DELETE | `/api/v1/admin/rbac/roles/:id/permissions/:perm_id` | JWT | `rbac:manage` | Remove permission from role |
| 10 | GET | `/api/v1/admin/rbac/permissions` | JWT | `rbac:read` | List all active permissions |
| 11 | GET | `/api/v1/admin/rbac/permissions/groups` | JWT | `rbac:read` | List permission groups with members |
| 12 | GET | `/api/v1/admin/rbac/users/:user_id/role` | JWT | `rbac:read` | Get user's current role |
| 13 | PUT | `/api/v1/admin/rbac/users/:user_id/role` | JWT | `rbac:manage` | Assign or replace user's role |
| 14 | DELETE | `/api/v1/admin/rbac/users/:user_id/role` | JWT | `rbac:manage` | Remove user's role |
| 15 | GET | `/api/v1/admin/rbac/users/:user_id/permissions` | JWT | `rbac:read` | List active direct grants |
| 16 | POST | `/api/v1/admin/rbac/users/:user_id/permissions` | JWT | `rbac:grant_user_permission` | Grant direct permission to user |
| 17 | DELETE | `/api/v1/admin/rbac/users/:user_id/permissions/:grant_id` | JWT | `rbac:grant_user_permission` | Revoke direct permission grant |
| 18 | POST | `/api/v1/admin/users/:user_id/lock` | JWT | `user:lock` | Admin-lock a user account |
| 19 | DELETE | `/api/v1/admin/users/:user_id/lock` | JWT | `user:lock` | Admin-unlock a user account |
| 20 | GET | `/api/v1/admin/users/:user_id/lock` | JWT | `user:read` | Get user lock status |

**Route 1 (bootstrap) notes:**
- Unauthenticated — the only unauthenticated write route in the system.
- 409 `owner_already_exists` if any active owner role assignment exists.
- Rate-limited: 3 req / 15 min per IP.
- Target `user_id` must be an existing active, email-verified user.
- Uses `user_id` as its own `granted_by` (self-grant for bootstrap only).

**Routes 5, 6 (system role guard):**
- The `UpdateRole` and `DeactivateRole` queries enforce `is_system_role = FALSE`
  at the DB level. Zero rows → service returns `ErrSystemRoleImmutable` → 409.

**Route 13 (assign role) guards:**
- Cannot reassign owner users → `ErrCannotReassignOwner` → 409.
- Cannot assign to self → `ErrCannotModifyOwnRole` → 409.
- `fn_prevent_orphaned_owner` fires at DB level if re-assigning the last owner.

**Routes 18/19 (lock/unlock) guards:**
- Cannot lock owner accounts → `ErrCannotLockOwner` → 409.
- Cannot lock self → `ErrCannotLockSelf` → 409.
- `user:lock` for admin is seeded as `access_type = 'request'` → admin gets 202 with an approval request; owner approves it directly.
- Owner-role users bypass `access_type` checks and can lock directly.

**Unlock flow guard (existing `domain/auth/unlock`):**
- If `admin_locked = TRUE`, the user-facing OTP unlock flow returns `ErrAdminLocked`
  and refuses to clear `admin_locked`. Only routes 18/19 touch that field.

---

## 9. Middleware Design

### Updated `Require` implementation sketch

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
                respond.Error(w, http.StatusInternalServerError, "internal_error",
                    "internal server error")
                return
            }

            // Owner bypasses all access_type logic.
            if row.IsOwner {
                ctx := injectAccessResult(r.Context(), &AccessResult{
                    IsOwner: true, Scope: "all",
                })
                next.ServeHTTP(w, r.WithContext(ctx))
                return
            }

            if !row.HasPermission {
                respond.Error(w, http.StatusForbidden, "forbidden",
                    "insufficient permissions")
                return
            }

            switch row.AccessType {
            case "denied":
                respond.Error(w, http.StatusForbidden, "forbidden",
                    "insufficient permissions")
                return

            case "request":
                // Submit an approval request and return 202.
                reqID, reqErr := c.submitApprovalRequest(r.Context(), userID, permission, r)
                if reqErr != nil {
                    slog.ErrorContext(r.Context(), "rbac.Require: submit request", "error", reqErr)
                    respond.Error(w, http.StatusInternalServerError, "internal_error",
                        "internal server error")
                    return
                }
                respond.JSON(w, http.StatusAccepted, map[string]any{
                    "code":       "approval_required",
                    "request_id": reqID,
                    "message":    "this action requires approval — request submitted",
                })
                return

            default: // "direct" or "conditional"
                ctx := injectAccessResult(r.Context(), &AccessResult{
                    HasPermission: true,
                    AccessType:    row.AccessType,
                    Scope:         row.Scope,
                    Conditions:    row.Conditions,
                })
                next.ServeHTTP(w, r.WithContext(ctx))
            }
        })
    }
}
```

### Usage examples

```go
// direct — passes through cleanly
r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermJobQueueRead)).
    Get("/stats", h.Stats)

// conditional — handler reads scope + conditions from context and enforces them
r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermProductManage)).
    Post("/products", h.CreateProduct)
// In handler:
// conditions := rbac.ConditionsFromContext(r.Context()) → {"max_price": 1000}
// scope      := rbac.ScopeFromContext(r.Context())      → "own"

// request — middleware submits approval request and returns 202; handler never called
r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermJobQueueConfigure)).
    Post("/queues/{kind}/pause", h.PauseKind)
```

### V2: Caching (post-launch)

V1 makes one DB query per guarded request. When RBAC check latency shows up in
profiling, add a short-TTL in-memory cache in `Checker` keyed by
`(userID, permission)` with a 30-second TTL. Invalidate on any of:
`AssignUserRole`, `RemoveUserRole`, `GrantUserPermission`, `RevokeUserPermission`,
`AddRolePermission`, `RemoveRolePermission`, `DeactivatePermission`
(`permissions.is_active = FALSE`). Zero changes to `Require`.

Note: when a permission is deactivated, all cache entries keyed on its
`canonical_name` must be evicted regardless of user. A wildcard eviction on
`canonical_name` is simpler than tracking individual `(user, permission)` pairs
and is correct since permission deactivation is operationally rare.

---

## 10. Bootstrap Flow

`POST /owner/bootstrap` is unauthenticated. It:

1. `CountActiveOwners` — if > 0, return 409 `owner_already_exists`.
2. `GetActiveUserByID` — verify target user exists, is active, is email-verified.
3. `GetOwnerRoleID` — look up owner role from `001_roles.sql`.
4. `AssignUserRole` — insert with `granted_by = user_id` (self-grant, bootstrap only).
5. Return `{ user_id, role_name, granted_at }`.

Rate-limited: 3 req / 15 min per IP. Permanently returns 409 after first success.

---

## 11. Decisions

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
| D-R12 | `access_type = 'request'` reuses `005_requests.sql` workflow | No duplicate approval machinery. `permission_request_approvers` seeds which roles must approve per permission. |
| D-R13 | `job_queue:manage` vs `job_queue:configure` split | manage = job-level ops (low blast radius: retry, cancel, priority). configure = system-level ops (pause all workers, delete schedules — high blast radius). |
| D-R14 | `user:lock` over separate `user:ban` | `admin_locked` already exists and is admin-only. Adding metadata columns (locked_by, reason, at) gives everything a ban would without a redundant field. One concept, not two overlapping ones. |
| D-R15 | `user:lock` for admin seeded as `access_type = 'request'` | Locking a user is a policy decision — admin initiates, owner approves. |
| D-R16 | `scope` on `user_permissions` (direct grants) | Even a direct grant should specify resource visibility. Defaults to 'own' — the safe default. |

---

## 12. Tests

### Checker

| # | Case | Layer |
|---|------|-------|
| T-R01 | `Require` passes for owner regardless of permission name | U |
| T-R02 | `Require` passes and injects scope for `access_type = 'direct'` | I |
| T-R03 | `Require` passes and injects scope + conditions for `access_type = 'conditional'` | I |
| T-R04 | `Require` returns 202 with request_id for `access_type = 'request'` | I |
| T-R05 | `Require` returns 403 for `access_type = 'denied'` | I |
| T-R06 | `Require` returns 403 when user has no role and no direct grant | I |
| T-R07 | `Require` returns 403 when direct grant is expired | I |
| T-R08 | `Require` returns 401 when no userID in context | U |
| T-R09 | `Require` uses test-injected permissions (no DB hit) | U |
| T-R10 | `Require` returns 500 and denies access on DB error (fail closed) | U |
| T-R11 | `IsOwner` returns true for owner role user | I |
| T-R12 | `IsOwner` returns false for non-owner user | I |
| T-R13 | `HasPermission` returns true via role path | I |
| T-R14 | `HasPermission` returns true via direct-grant path | I |
| T-R15 | `HasPermission` returns false after role permission is removed | I |
| T-R16 | `ScopeFromContext` returns 'all' for admin, 'own' for vendor | I |
| T-R17 | `ConditionsFromContext` returns conditions for conditional grant | I |

### Bootstrap

| # | Case | Layer |
|---|------|-------|
| T-R18 | Bootstrap succeeds when no owner exists | I |
| T-R19 | Bootstrap returns 409 when owner already exists | I |
| T-R20 | Bootstrap returns 422 when `user_id` is unknown | I |
| T-R21 | Bootstrap returns 422 when user is not email-verified | I |
| T-R22 | Bootstrap is rate-limited (3 req / 15 min per IP) | U |

### Roles API

| # | Case | Layer |
|---|------|-------|
| T-R23 | `GET /roles` returns seeded roles | I |
| T-R24 | `POST /roles` creates a new role | I |
| T-R25 | `PATCH /roles/:id` updates name for non-system role | I |
| T-R26 | `PATCH /roles/:id` returns 409 for system role | I |
| T-R27 | `DELETE /roles/:id` soft-deletes non-system role | I |
| T-R28 | `DELETE /roles/:id` returns 409 for system role | I |
| T-R29 | `GET /roles/:id/permissions` lists permissions with access_type + scope | I |
| T-R30 | `POST /roles/:id/permissions` adds permission with access_type + scope | I |
| T-R31 | `DELETE /roles/:id/permissions/:perm_id` removes permission | I |

### Permissions API

| # | Case | Layer |
|---|------|-------|
| T-R32 | `GET /permissions` returns all 13 seeded active permissions | I |
| T-R33 | `GET /permissions/groups` returns groups with members | I |

### User role management

| # | Case | Layer |
|---|------|-------|
| T-R34 | `PUT /users/:id/role` assigns role; `GET` returns it | I |
| T-R35 | `PUT /users/:id/role` replaces an existing role | I |
| T-R36 | `PUT /users/:id/role` returns 409 for owner target user | I |
| T-R37 | `PUT /users/:id/role` returns 409 for self-assignment | I |
| T-R38 | `DELETE /users/:id/role` removes role | I |

### User permissions management

| # | Case | Layer |
|---|------|-------|
| T-R39 | `POST /users/:id/permissions` grants direct permission with scope | I |
| T-R40 | `GET /users/:id/permissions` returns only active grants with scope | I |
| T-R41 | `DELETE /users/:id/permissions/:grant_id` revokes grant | I |
| T-R42 | Expired grant does not appear in `GET` and does not pass `Require` | I |
| T-R43 | Grant with `expires_at > 90 days` returns 422 (DB trigger fires) | I |
| T-R44 | Granter without the permission cannot grant it (privilege escalation trigger) | I |

### User lock management

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

## 13. File Map

```
-- Phase 0: schema additions
001_core.sql                                 MODIFY — admin_locked_by, admin_locked_reason,
                                                       admin_locked_at + constraints
003_rbac.sql                                 MODIFY — permission_access_type + permission_scope ENUMs;
                                                       access_type + scope on role_permissions;
                                                       scope on user_permissions;
                                                       access_type/scope snapshot columns on both audit tables;
                                                       permission_request_approvers table
004_rbac_functions.sql                       MODIFY — fn_audit_role_permissions + fn_audit_user_permissions
                                                       capture access_type/scope before/after snapshots

-- Phase 1: queries + seeds
sql/queries/rbac.sql                         NEW — all 25 queries (23 v1 + LockUser, UnlockUser, GetUserLockStatus)
sql/seeds/002_permissions.sql                NEW — 13 permissions + 5 groups + group members
sql/seeds/003_roles.sql                      NEW — admin/vendor/customer roles + role-permission rows with access_type + scope

-- Phase 3: platform package
internal/platform/rbac/
    checker.go                               NEW — Checker, IsOwner, HasPermission, Require (access_type aware)
    context.go                               NEW — AccessResult, scope, conditions context helpers
    errors.go                                NEW — all sentinels incl. ErrApprovalRequired, ErrCannotLockOwner
    checker_test.go                          NEW — T-R01 through T-R17

-- Phases 4-9: domain package
internal/domain/rbac/
    routes.go                                NEW
    bootstrap/   handler.go, service.go, store.go, models.go, routes.go, validators.go  NEW
    roles/       handler.go, service.go, store.go, models.go, routes.go, validators.go  NEW
    permissions/ handler.go, service.go, store.go, models.go, routes.go                NEW
    userroles/   handler.go, service.go, store.go, models.go, routes.go, validators.go  NEW
    userpermissions/ handler.go, service.go, store.go, models.go, routes.go, validators.go NEW
    userlock/    handler.go, service.go, store.go, models.go, routes.go, validators.go  NEW

-- Phase 10: existing files to modify
internal/domain/auth/login/service.go        MODIFY — add admin_locked guard in step 6 guard chain
internal/domain/auth/unlock/service.go       MODIFY — refuse to clear admin_locked
internal/domain/auth/shared/errors.go        MODIFY — add ErrAdminLocked sentinel
internal/domain/oauth/google/service.go      MODIFY — add admin_locked guard before session creation
internal/domain/oauth/telegram/service.go    MODIFY — add admin_locked guard before session creation
internal/platform/token/middleware.go        MODIFY — add step 3c: Redis "admin_locked_user:<uid>" check
internal/app/deps.go                         MODIFY — add RBAC *rbac.Checker
internal/server/server.go                    MODIFY — construct Checker, add to deps
internal/server/routes.go                    MODIFY — mount /owner and /admin/rbac + /admin/users sub-routers
```

---

## 14. Implementation Phases

| Phase | What | Needs | Gate | Status |
|-------|------|-------|------|--------|
| 0 | Schema additions to `001_core.sql`, `003_rbac.sql`, `004_rbac_functions.sql` | nothing | `make sqlc` compiles; new columns present in DB | ✅ Done |
| 1 | `sql/queries/rbac.sql` + `sqlc generate` | Phase 0 | `db` package compiles with all 25 new generated methods; **operational TODOs below closed** | ✅ Done |
| 2 | `sql/seeds/002_permissions.sql` + `003_roles.sql` | Phase 1 | `SELECT COUNT(*) FROM permissions` = 13; roles + role-permission rows present | ✅ Done|
| 3 | `internal/platform/rbac/` — checker + middleware | Phase 1 | T-R01 through T-R17 green; `go build ./...` passes | ⬜ Todo |
| 4 | Bootstrap route (`/owner/bootstrap`) | Phase 3 | T-R18 through T-R22 green | ⬜ Todo |
| 5 | Permissions read API (routes 10–11) | Phase 3 | T-R32, T-R33 green | ⬜ Todo |
| 6 | Roles API (routes 2–9) | Phase 3 | T-R23 through T-R31 green | ⬜ Todo |
| 7 | User role management (routes 12–14) | Phase 6 | T-R34 through T-R38 green | ⬜ Todo |
| 8 | User permission management (routes 15–17) | Phase 7 | T-R39 through T-R44 green | ⬜ Todo |
| 9 | User lock management (routes 18–20) | Phase 7 | T-R45 through T-R52 green | ⬜ Todo |
| 10 | Wire into server; update login/oauth/unlock/token middleware | Phases 4–9 | Server boots; `go test ./...` green | ⬜ Todo |

**Phase 0 is the new prerequisite** — schema additions land before Stage 1.
**Phase 3 is the unlock** — once `Require` exists every other domain can add guards immediately.
Phases 4–9 can run in parallel once Phase 3 is green.
Phase 10 is the only phase that modifies existing files outside the rbac domain.

---

## 16. Operational TODOs (from Phase 1 audit — required before go-live)

These are not schema or query changes. They must be implemented as part of Phase 2 / Phase 3 work or as standalone infra tasks before the system takes real traffic.

### TODO-1 · Expired grant cleanup job ⚠️ Reliability blocker

**Implemented in the job queue design** — see `internal/worker/purge_expired_permissions.go` and the `KindPurgeExpiredPermissions` schedule entry in `jobqueue/0-design.md §7`.

Summary: `uq_up_one_active_grant_per_user_perm` is a full unique index (not partial — `NOW()` cannot appear in an index predicate). An expired `user_permissions` row that has not been deleted will cause `GrantUserPermission` to return `23505 → ErrPermissionAlreadyGranted → 409`. The cleanup job runs every 5 minutes via the job queue scheduler and deletes rows where `expires_at <= NOW()` using `idx_user_permissions_expires`.

### TODO-2 · Re-grant idempotency in `GrantUserPermission` service

Belt-and-suspenders on top of TODO-1. If the cleanup job has lag and a caller tries to re-grant an expired permission, the service will receive a `23505`. Rather than surfacing that as a 409, the service should:

1. On receipt of `23505` from `GrantUserPermission`, attempt:
   ```sql
   DELETE FROM user_permissions
   WHERE user_id = $1 AND permission_id = $2 AND expires_at <= NOW();
   ```
2. If that deletes 1 row, retry the `GrantUserPermission` insert.
3. If it deletes 0 rows (the existing grant is still active), surface `ErrPermissionAlreadyGranted → 409` as today.

This makes re-grants self-healing without depending solely on cleanup cadence. Implement in `userpermissions/service.go`.

### TODO-3 · Static catalog caching (post-launch, not a blocker)

These queries return data that changes at most a few times a year and are called on admin UI page loads:

| Query | Table | Typical rows |
|---|---|---|
| `GetRoles` | `roles` | < 20 |
| `GetPermissions` | `permissions` | < 200 |
| `GetPermissionGroups` | `permission_groups` | < 30 |
| `GetOwnerRoleID` | `roles` | 1 |

Add a 5-minute in-process TTL cache in the relevant service layer, invalidated on any mutation (create/update/deactivate role, permission seed reload). `GetOwnerRoleID` in particular can be a process-lifetime singleton after first load. Implement after launch when DB query latency appears in profiling.

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

---

## 15. Wiring (Phase 10)

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

### Job queue routes

```go
// read — direct pass-through
r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermJobQueueRead)).
    Get("/stats", h.Stats)

// manage — direct pass-through
r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermJobQueueManage)).
    Post("/jobs/{id}/retry", h.Retry)

// configure — admin gets 202 (approval_required); owner passes directly
r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermJobQueueConfigure)).
    Post("/queues/{kind}/pause", h.PauseKind)
```
