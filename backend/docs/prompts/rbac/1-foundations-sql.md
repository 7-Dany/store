# RBAC — Stage 1: Foundations (SQL Queries + Seeds)

**Feature:** RBAC
**Phase:** 1 of 8
**Design doc:** `docs/prompts/rbac/0-design.md`
**Context:** `docs/prompts/rbac/context.md`

**Gate:** `internal/db/` package compiles cleanly after `make sqlc` with all 23 new
generated methods present. `SELECT COUNT(*) FROM permissions` = 10.
`SELECT COUNT(*) FROM roles WHERE is_system_role = TRUE` = 2 (owner + admin).

---

## What this phase produces

| File | Action |
|------|--------|
| `sql/queries/rbac.sql` | CREATE — all 23 RBAC queries |
| `sql/seeds/002_permissions.sql` | CREATE — 10 permissions + 4 groups + group members |
| `sql/seeds/003_roles.sql` | CREATE — admin/vendor/customer roles + their default permissions |
| `internal/db/` | REGENERATE — run `make sqlc` after writing rbac.sql |

No Go files are written in this phase. All Go code depends on the generated `db` package.

---

## Read first

| File | What to extract |
|------|-----------------|
| `docs/prompts/rbac/0-design.md §4` | All 23 SQL query definitions (verbatim) |
| `docs/prompts/rbac/0-design.md §5` | Seed data: permission table, group table, role table |
| `sql/queries/auth.sql` (head 20 lines) | File header format to match |
| `sqlc.yaml` or `sqlc.yml` (project root) | Confirm how new query files are picked up |

---

## Step 1 — Create `sql/queries/rbac.sql`

Use the file header format from `auth.sql`. The file must begin with a block comment
listing its sections, then group queries under section banners. Copy every query
**verbatim** from `0-design.md §4` — do not rewrite or paraphrase them.

Sections in order:
1. Permission check (hot path)
2. Bootstrap
3. Roles
4. Role permissions
5. Permissions
6. User role
7. User permissions (direct grants)

File header to write at the top:

```sql
/* ============================================================
   sql/queries/rbac.sql
   RBAC queries for sqlc code generation.

   Sections:
     Permission check (hot path)
     Bootstrap
     Roles
     Role permissions
     Permissions
     User role
     User permissions
   ============================================================ */
```

### Checklist before saving

- [ ] All 23 query names present (see §Context → New SQL queries)
- [ ] `CheckUserAccess` uses `@user_id::uuid` and `@permission` params
- [ ] `AssignUserRole` uses `sqlc.narg('expires_at')::timestamptz`
- [ ] `UpdateRole` uses `sqlc.narg('name')` and `sqlc.narg('description')`
- [ ] `GrantUserPermission` uses `sqlc.narg('conditions')::jsonb`
- [ ] `AddRolePermission` uses `COALESCE(sqlc.narg('conditions')::jsonb, '{}')`
- [ ] All `:one`, `:many`, `:exec`, `:execrows` result modes match the design exactly
- [ ] Section comment banners present (`/* ── Section name ── */`)

---

## Step 2 — Create `sql/seeds/002_permissions.sql`

This file inserts all permissions the application will ever check. It must be
**idempotent** — use `ON CONFLICT DO NOTHING` on every insert.

### Permission rows

Insert into `permissions (resource_type, name)` — `canonical_name` is generated
by the DB as `resource_type || ':' || name`.

| resource_type | name |
|---|---|
| rbac | read |
| rbac | manage |
| rbac | grant_user_permission |
| job_queue | read |
| job_queue | manage |
| user | read |
| user | manage |
| request | read |
| request | manage |
| request | approve |

### Permission group rows

Insert into `permission_groups (name, display_label, icon, color_hex, display_order)`:

| name | display_label | icon | color_hex | display_order |
|---|---|---|---|---|
| system_administration | System Administration | shield | #6366f1 | 1 |
| job_queue | Job Queue | queue | #f59e0b | 2 |
| users | Users | users | #10b981 | 3 |
| requests | Requests | inbox | #3b82f6 | 4 |

### Permission group member rows

Insert into `permission_group_members (group_id, permission_id)` using CTEs to
look up IDs by name rather than hardcoding UUIDs. Example pattern:

```sql
WITH
  grp AS (SELECT id FROM permission_groups WHERE name = 'system_administration'),
  perm AS (SELECT id FROM permissions WHERE canonical_name = 'rbac:read')
INSERT INTO permission_group_members (group_id, permission_id)
SELECT grp.id, perm.id FROM grp, perm
ON CONFLICT DO NOTHING;
```

Group memberships:
- `system_administration` → `rbac:read`, `rbac:manage`, `rbac:grant_user_permission`
- `job_queue` → `job_queue:read`, `job_queue:manage`
- `users` → `user:read`, `user:manage`
- `requests` → `request:read`, `request:manage`, `request:approve`

### Checklist before saving

- [ ] All 10 permission rows present
- [ ] All 4 group rows present
- [ ] All 10 group membership rows present (3 + 2 + 2 + 3)
- [ ] Every INSERT uses `ON CONFLICT DO NOTHING`
- [ ] Group member inserts use CTE pattern (no hardcoded UUIDs)

---

## Step 3 — Create `sql/seeds/003_roles.sql`

This file seeds the non-owner system roles and their default permissions.
The owner role already exists from `001_roles.sql` — do not touch it.

### Role rows

Insert into `roles (name, description, is_system_role, is_owner_role)`:

| name | is_system_role | is_owner_role | Default permissions |
|---|---|---|---|
| admin | TRUE | FALSE | All 10 permissions |
| vendor | FALSE | FALSE | request:read, request:manage |
| customer | FALSE | FALSE | request:read |

Use `ON CONFLICT (name) DO NOTHING` for idempotency.

### Role permission rows

Use a CTE to look up the role ID and permission ID by name for every
`role_permissions` insert. For `granted_by`, use a sub-select that finds
the first active owner user, falling back to a sentinel UUID
(`'00000000-0000-0000-0000-000000000000'::uuid`) when no owner exists yet
(first-deploy scenario before bootstrap has run).

Pattern:

```sql
WITH
  actor AS (
    SELECT COALESCE(
      (SELECT ur.user_id FROM user_roles ur
       JOIN roles r ON r.id = ur.role_id
       WHERE r.is_owner_role = TRUE
         AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
       LIMIT 1),
      '00000000-0000-0000-0000-000000000000'::uuid
    ) AS id
  ),
  role AS (SELECT id FROM roles WHERE name = 'admin'),
  perm AS (SELECT id FROM permissions WHERE canonical_name = 'rbac:read')
INSERT INTO role_permissions (role_id, permission_id, granted_by, granted_reason)
SELECT role.id, perm.id, actor.id, 'system seed'
FROM role, perm, actor
ON CONFLICT (role_id, permission_id) DO NOTHING;
```

Repeat this pattern for every role–permission pair. Admin gets all 10 permissions
so there will be 10 inserts for admin, 2 for vendor, 1 for customer.

### Checklist before saving

- [ ] `admin` role: `is_system_role = TRUE`, `is_owner_role = FALSE`
- [ ] `vendor` and `customer` roles: `is_system_role = FALSE`, `is_owner_role = FALSE`
- [ ] Admin has all 10 permissions (10 inserts)
- [ ] Vendor has `request:read` and `request:manage` (2 inserts)
- [ ] Customer has `request:read` (1 insert)
- [ ] Actor CTE used for `granted_by` in all role_permissions inserts
- [ ] All inserts idempotent (`ON CONFLICT DO NOTHING`)

---

## Step 4 — Run `make sqlc`

After writing `sql/queries/rbac.sql`, regenerate the `internal/db/` package:

```sh
make sqlc
```

Confirm the following generated methods now exist in `internal/db/querier.go`:

```
CheckUserAccess(ctx, CheckUserAccessParams) (CheckUserAccessRow, error)
CountActiveOwners(ctx) (int64, error)
GetOwnerRoleID(ctx) (pgtype.UUID, error)
GetActiveUserByID(ctx, pgtype.UUID) (GetActiveUserByIDRow, error)
AssignUserRole(ctx, AssignUserRoleParams) (AssignUserRoleRow, error)
RemoveUserRole(ctx, pgtype.UUID) (int64, error)
GetRoles(ctx) ([]GetRolesRow, error)
GetRoleByID(ctx, pgtype.UUID) (GetRoleByIDRow, error)
GetRoleByName(ctx, string) (GetRoleByNameRow, error)
CreateRole(ctx, CreateRoleParams) (CreateRoleRow, error)
UpdateRole(ctx, UpdateRoleParams) (UpdateRoleRow, error)
DeactivateRole(ctx, pgtype.UUID) (int64, error)
GetRolePermissions(ctx, pgtype.UUID) ([]GetRolePermissionsRow, error)
AddRolePermission(ctx, AddRolePermissionParams) error
RemoveRolePermission(ctx, RemoveRolePermissionParams) (int64, error)
GetPermissions(ctx) ([]GetPermissionsRow, error)
GetPermissionByCanonicalName(ctx, string) (GetPermissionByCanonicalNameRow, error)
GetPermissionGroups(ctx) ([]GetPermissionGroupsRow, error)
GetPermissionGroupMembers(ctx, pgtype.UUID) ([]GetPermissionGroupMembersRow, error)
GetUserRole(ctx, pgtype.UUID) (GetUserRoleRow, error)
GetUserPermissions(ctx, pgtype.UUID) ([]GetUserPermissionsRow, error)
GrantUserPermission(ctx, GrantUserPermissionParams) (GrantUserPermissionRow, error)
RevokeUserPermission(ctx, RevokeUserPermissionParams) (int64, error)
```

If `make sqlc` fails, fix the SQL syntax in `rbac.sql` before proceeding. Common
issues: missing `::` casts on named params, wrong result mode (`:one` vs `:many`),
`sqlc.narg` on a non-nullable column.

---

## Gate conditions

All must be true before moving to Phase 2 (`internal/platform/rbac/`):

- [ ] `sql/queries/rbac.sql` committed with all 23 queries
- [ ] `sql/seeds/002_permissions.sql` committed (10 permissions, 4 groups, 10 members)
- [ ] `sql/seeds/003_roles.sql` committed (3 roles, 13 role–permission rows)
- [ ] `make sqlc` runs without errors
- [ ] `go build ./internal/db/...` passes
- [ ] Seeds applied to local DB: `SELECT COUNT(*) FROM permissions` = 10
- [ ] Seeds applied to local DB: `SELECT COUNT(*) FROM roles WHERE is_system_role = TRUE` = 2

---

## Next

Once the gate is green, open a fresh session with:

```
docs/prompts/rbac/context.md
docs/prompts/rbac/0-design.md §6 §8 §10 §11
```

and request: **"RBAC Phase 2 — Stage 1 for internal/platform/rbac/"**

The next prompt will cover: `checker.go`, `context.go`, `errors.go`, and
`checker_test.go` (T-R01 through T-R13).
