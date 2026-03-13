/* ============================================================
   sql/queries/rbac.sql
   RBAC domain queries for sqlc code generation.

   Covers all role-based access control flows in dependency order:
     Permission check (hot path)
     Bootstrap
     Roles
     Role permissions
     Permissions
     User role
     User permissions (direct grants)
     User lock

   Security-sensitive admin-lock fields (admin_locked, admin_locked_by,
   admin_locked_reason, admin_locked_at, is_locked, login_locked_until)
   live in user_secrets, not users. Every query that reads or writes those
   columns JOINs or targets user_secrets directly.
   ============================================================ */


/* ── Permission check (hot path) ──────────────────────────────────────────── */

-- name: CheckUserAccess :one
-- Returns the full access context for (user_id, permission) in one round-trip.
-- is_owner short-circuits in Go — all other fields are ignored when is_owner = true.
--
-- Single-pass rewrite: resolves user_role_ctx and perm_id exactly once each,
-- then derives all output columns from those two materialized CTEs. Eliminates the
-- previous triple traversal of user_roles and the duplicate 4-table join chain.
--
-- Index usage:
--   user_role_ctx: user_roles PK (user_id) — at most 1 row
--   perm_id:       idx_permissions_canonical — O(1) index-only seek
--   role_grant:    idx_role_perms_covering(role_id, permission_id) INCLUDE(conditions,access_type,scope)
--   direct_grant:  uq_up_one_active_grant_per_user_perm(user_id, permission_id) — point lookup,
--                  replaces previous (user_id, expires_at) range scan
WITH user_role_ctx AS MATERIALIZED (
    -- Single lookup: the user's one role row + owner/active flags.
    -- user_id is the PK of user_roles so this is always at most one row.
    SELECT
        ur.role_id,
        ur.expires_at,
        r.is_owner_role,
        r.is_active AS role_active
    FROM   user_roles ur
    JOIN   roles r ON r.id = ur.role_id
    WHERE  ur.user_id  = @user_id::uuid
      AND  r.is_active = TRUE
      AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
),
perm_id AS MATERIALIZED (
    -- Resolve permission_id once; shared by both role_grant and direct_grant.
    -- idx_permissions_canonical gives an O(1) index-only seek on canonical_name.
    SELECT id AS permission_id
    FROM   permissions
    WHERE  canonical_name = @permission
      AND  is_active      = TRUE
),
role_grant AS MATERIALIZED (
    -- Point lookup: (role_id, permission_id) on role_permissions.
    -- idx_role_perms_covering INCLUDE(conditions, access_type, scope) → index-only scan,
    -- no heap fetch required.
    SELECT rp.access_type, rp.scope, rp.conditions
    FROM   user_role_ctx rc
    CROSS JOIN perm_id pi
    JOIN   role_permissions rp
               ON rp.role_id       = rc.role_id
              AND rp.permission_id = pi.permission_id
),
direct_grant AS MATERIALIZED (
    -- Point lookup: (user_id, permission_id) on user_permissions.
    -- uq_up_one_active_grant_per_user_perm replaces the previous (user_id, expires_at)
    -- range scan; O(1) regardless of how many grants the user holds.
    SELECT up.scope, up.conditions
    FROM   perm_id pi
    JOIN   user_permissions up
               ON up.user_id       = @user_id::uuid
              AND up.permission_id = pi.permission_id
    WHERE  up.expires_at > NOW()
    -- No LIMIT: uq_up_one_active_grant_per_user_perm guarantees at most 1 row
)
SELECT
    -- Layer 1: owner check — read from already-materialized user_role_ctx; zero extra seeks.
    COALESCE((SELECT is_owner_role FROM user_role_ctx), FALSE) AS is_owner,

    -- Layer 2: explicit denial — denied takes priority over all other grants.
    EXISTS(
        SELECT 1 FROM role_grant WHERE access_type = 'denied'
    ) AS is_explicitly_denied,

    -- Layer 3: permission existence via role (non-denied) or direct grant.
    (
        EXISTS(SELECT 1 FROM role_grant WHERE access_type != 'denied')
        OR
        EXISTS(SELECT 1 FROM direct_grant)
    ) AS has_permission,

    -- access_type: role path (non-denied) takes priority; fallback to 'direct' for direct grants.
    COALESCE(
        (SELECT access_type FROM role_grant WHERE access_type != 'denied'),
        'direct'::permission_access_type
    ) AS access_type,

    -- scope: role path (non-denied) takes priority; fallback to direct grant scope.
    COALESCE(
        (SELECT scope FROM role_grant WHERE access_type != 'denied'),
        (SELECT scope FROM direct_grant)
    ) AS scope,

    -- conditions: role path (non-denied) takes priority; fallback to direct grant; default '{}'.
    COALESCE(
        (SELECT conditions FROM role_grant WHERE access_type != 'denied'),
        (SELECT conditions FROM direct_grant),
        '{}'::jsonb
    ) AS conditions;


/* ── Bootstrap ─────────────────────────────────────────────────────────────── */

-- name: CountActiveOwners :one
-- Used by /owner/bootstrap to gate on whether an active owner already exists.
SELECT COUNT(*) AS count
FROM   user_roles ur
JOIN   roles r ON r.id = ur.role_id
WHERE  r.is_owner_role = TRUE
  AND  r.is_active     = TRUE
  AND  (ur.expires_at IS NULL OR ur.expires_at > NOW());

-- name: GetOwnerRoleID :one
-- Looks up the owner role seeded by 001_roles.sql.
SELECT id FROM roles
WHERE  is_owner_role  = TRUE
  AND  is_system_role = TRUE
LIMIT 1;

-- name: GetActiveUserByID :one
-- Verifies a target user exists, is active, and has confirmed their email.
-- Used by bootstrap to reject unknown or unverified user_ids.
SELECT id, email, is_active, email_verified
FROM   users
WHERE  id         = @user_id::uuid
  AND  deleted_at IS NULL;

-- name: AssignUserRole :one
-- Inserts or replaces a user's role assignment. ON CONFLICT targets the user_id
-- PRIMARY KEY (one-role-per-user invariant). fn_prevent_owner_role_escalation fires
-- as a DB backstop before each INSERT/UPDATE OF role_id.
--
-- The bootstrap handler calls this with granted_by = user_id (self-grant; only valid
-- because bootstrap is the chicken-and-egg path before any owner exists).
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
-- Hard-deletes a user's role assignment. Returns rows affected (0 = no assignment found).
-- fn_prevent_orphaned_owner fires as a DB backstop before DELETE.
DELETE FROM user_roles WHERE user_id = @user_id::uuid;


/* ── Roles ─────────────────────────────────────────────────────────────────── */

-- name: GetRoles :many
SELECT id, name, description, is_system_role, is_owner_role, is_active, created_at, updated_at
FROM   roles
WHERE  is_active = TRUE
ORDER  BY name;

-- name: GetRoleByID :one
SELECT id, name, description, is_system_role, is_owner_role, is_active, created_at, updated_at
FROM   roles
WHERE  id = @id::uuid;

-- name: GetRoleByName :one
SELECT id, name, description, is_system_role, is_owner_role, is_active, created_at, updated_at
FROM   roles
WHERE  name = @name;

-- name: CreateRole :one
-- is_system_role and is_owner_role are always FALSE for end-user-created roles.
INSERT INTO roles (name, description, is_system_role, is_owner_role)
VALUES (@name, sqlc.narg('description'), FALSE, FALSE)
RETURNING id, name, description, is_system_role, is_owner_role, is_active, created_at, updated_at;

-- name: UpdateRole :one
-- WHERE is_system_role = FALSE prevents renaming system roles at the DB level.
-- Zero rows affected → service returns ErrSystemRoleImmutable → 409.
UPDATE roles
SET name        = COALESCE(sqlc.narg('name'),        name),
    description = COALESCE(sqlc.narg('description'), description)
WHERE id             = @id::uuid
  AND is_system_role = FALSE
RETURNING id, name, description, is_system_role, is_owner_role, is_active, created_at, updated_at;

-- name: DeactivateRole :execrows
-- Soft-deletes a non-system role. Zero rows → ErrSystemRoleImmutable → 409.
UPDATE roles
SET is_active = FALSE
WHERE id             = @id::uuid
  AND is_system_role = FALSE
  AND is_active      = TRUE;


/* ── Role permissions ──────────────────────────────────────────────────────── */

-- name: GetRolePermissions :many
SELECT p.id, p.canonical_name, p.name, p.resource_type, p.description,
       rp.access_type, rp.scope, rp.conditions, rp.created_at AS granted_at
FROM   role_permissions rp
JOIN   permissions p ON p.id = rp.permission_id
WHERE  rp.role_id  = @role_id::uuid
  AND  p.is_active = TRUE
ORDER  BY p.canonical_name;

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

-- name: RemoveRolePermission :execrows
DELETE FROM role_permissions
WHERE role_id = @role_id::uuid AND permission_id = @permission_id::uuid;

-- name: SetActingUser :exec
-- Sets rbac.acting_user for the current transaction so audit triggers
-- (fn_audit_role_permissions, etc.) record the correct deletion actor.
-- set_config(name, value, is_local=true) is equivalent to SET LOCAL and
-- accepts a parameterized value, unlike the SET statement.
SELECT set_config('rbac.acting_user', @user_id::text, true);


/* ── Permissions ───────────────────────────────────────────────────────────── */

-- name: GetPermissions :many
SELECT id, canonical_name, name, resource_type, description,
       scope_policy, allow_conditional, allow_request,
       is_active, created_at
FROM   permissions
WHERE  is_active = TRUE
ORDER  BY canonical_name;

-- name: GetPermissionByID :one
-- Returns the capability flags for a single permission by primary key.
-- Used by AddRolePermission to validate access_type and scope before inserting.
SELECT id, canonical_name, scope_policy, allow_conditional, allow_request
FROM   permissions
WHERE  id        = @id::uuid
  AND  is_active = TRUE;

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
SELECT p.id, p.canonical_name, p.name, p.resource_type, p.description,
       p.scope_policy, p.allow_conditional, p.allow_request
FROM   permission_group_members pgm
JOIN   permissions p ON p.id = pgm.permission_id
WHERE  pgm.group_id = @group_id::uuid
  AND  p.is_active  = TRUE
ORDER  BY p.canonical_name;


/* ── User role ─────────────────────────────────────────────────────────────── */

-- name: GetUserRole :one
-- Returns nil (no rows) when the user has no active role assignment.
SELECT ur.user_id, ur.role_id, r.name AS role_name, r.is_owner_role,
       ur.expires_at, ur.created_at AS granted_at, ur.granted_reason
FROM   user_roles ur
JOIN   roles r ON r.id = ur.role_id
WHERE  ur.user_id  = @user_id::uuid
  AND  r.is_active = TRUE
  AND  (ur.expires_at IS NULL OR ur.expires_at > NOW());


/* ── User permissions (direct grants) ─────────────────────────────────────── */

-- name: GetUserPermissions :many
-- Returns only active (non-expired) grants for the given user.
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
-- Privilege escalation is blocked by fn_prevent_privilege_escalation trigger.
-- uq_up_one_active_grant_per_user_perm is a full UNIQUE index on (user_id, permission_id),
-- so re-granting the same permission requires revoking the previous grant first.
-- The service layer should call RevokeUserPermission before this when re-granting.
--
-- On 23505 (unique_violation) on uq_up_one_active_grant_per_user_perm:
-- service maps → ErrPermissionAlreadyGranted → 409.
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
RETURNING id, user_id, permission_id, scope, expires_at, created_at;

-- name: RevokeUserPermission :execrows
-- Hard-deletes by surrogate id scoped to user_id (prevents cross-user deletion).
DELETE FROM user_permissions WHERE id = @id::uuid AND user_id = @user_id::uuid;


/* ── User lock ─────────────────────────────────────────────────────────────── */

-- NOTE: admin_locked, admin_locked_by, admin_locked_reason, admin_locked_at,
-- is_locked, and login_locked_until are all in user_secrets, not users.

-- name: LockUser :exec
-- Sets admin_locked = TRUE with actor, reason, and timestamp in user_secrets.
-- chk_us_admin_lock_coherent: reason + at required when locked = TRUE.
-- chk_us_no_self_lock: admin_locked_by must differ from user_id.
UPDATE user_secrets
SET admin_locked        = TRUE,
    admin_locked_by     = @locked_by::uuid,
    admin_locked_reason = @reason,
    admin_locked_at     = NOW(),
    updated_at          = NOW()
WHERE user_id = @user_id::uuid;

-- name: UnlockUser :exec
-- Clears admin_locked and all metadata fields in user_secrets.
-- chk_us_admin_lock_coherent requires all three metadata fields to be NULL
-- when admin_locked = FALSE, so all four must be cleared atomically.
UPDATE user_secrets
SET admin_locked        = FALSE,
    admin_locked_by     = NULL,
    admin_locked_reason = NULL,
    admin_locked_at     = NULL,
    updated_at          = NOW()
WHERE user_id = @user_id::uuid;

-- name: GetUserLockStatus :one
-- Returns the full lock state: both OTP lock (is_locked, login_locked_until)
-- and admin lock (admin_locked + metadata). JOIN to users guards deleted_at.
SELECT
    u.id,
    us.admin_locked,
    us.admin_locked_by,
    us.admin_locked_reason,
    us.admin_locked_at,
    us.is_locked,
    us.login_locked_until
FROM   users u
JOIN   user_secrets us ON us.user_id = u.id
WHERE  u.id         = @user_id::uuid
  AND  u.deleted_at IS NULL;
