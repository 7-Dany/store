-- +goose Up
-- +goose StatementBegin

/*
 * 003_rbac.sql — Role-Based Access Control (RBAC) schema.
 * NOTE: role_permissions.granted_by is nullable — see migration 009_rbac_nullable_granted_by.sql.
 *
 * Implements a one-role-per-user RBAC model with temporary per-user direct grants
 * as escape hatches. Key design decisions:
 *
 * Roles — named permission bundles; soft-deleted (is_active), not hard-deleted
 * Permissions — atomic action/resource pairs (e.g. product:create)
 * role_permissions — maps roles to permissions with ABAC conditions, access_type, and scope
 * user_roles — assigns exactly ONE role per user (user_id PK enforces this)
 * user_permissions — time-bounded direct grants that bypass the role model
 * permission_groups — UI organisation; confer no permissions themselves
 * permission_condition_templates — per-permission ABAC condition vocabulary
 * permission_request_approvers — which roles must approve 'request'-type permissions
 * *_audit tables — immutable mutation history for every RBAC table
 *
 * Depends on: 001_core.sql
 */


/* ─────────────────────────────────────────────────────────────
 AUDIT ENUM
 ───────────────────────────────────────────────────────────── */

-- Mutation kind written into all *_audit tables by AFTER INSERT/UPDATE/DELETE triggers.
CREATE TYPE audit_change_type_enum AS ENUM (
 'created', -- row was inserted
 'updated', -- row was modified
 'deleted' -- row was deleted
);

COMMENT ON TYPE audit_change_type_enum IS
 'Mutation kind recorded in all *_audit tables. Populated by AFTER INSERT/UPDATE/DELETE triggers.';


/* ─────────────────────────────────────────────────────────────
 ACCESS CONTROL ENUMS
 ───────────────────────────────────────────────────────────── */

-- Controls how the permission middleware responds when a user with this grant hits the endpoint.
CREATE TYPE permission_access_type AS ENUM (
 'direct', -- access is granted immediately with no additional friction
 'conditional', -- access is granted but the conditions JSONB is enforced by the app layer
 'request', -- user must submit a request that must be approved before the action executes
 'denied' -- access is explicitly blocked regardless of other grants
);

COMMENT ON TYPE permission_access_type IS
 'How a role or direct grant surfaces a permission at runtime. Middleware acts on this value.';

-- Controls which resources the grantee may act on when the permission check passes.
CREATE TYPE permission_scope AS ENUM (
 'own', -- user may only act on resources they created or own
 'all' -- user may act on any resource of this type, including other users' resources
);

COMMENT ON TYPE permission_scope IS
 'Resource visibility injected into context by Require middleware for downstream query scoping.';


/* ─────────────────────────────────────────────────────────────
 POLICY CONSTANTS
 ───────────────────────────────────────────────────────────── */

-- Session-level defaults read by trigger functions in 004_rbac_functions.sql.
-- Override per-transaction in tests: SET LOCAL rbac.min_temp_grant_lead = '1 second'.
-- Triggers have hard-coded fallbacks, so a failure here is non-fatal.
DO $$ BEGIN
 PERFORM set_config('rbac.min_temp_grant_lead', '5 minutes', FALSE);
 PERFORM set_config('rbac.max_temp_grant_interval', '90 days', FALSE);
END $$;


/* ─────────────────────────────────────────────────────────────
 ROLES
 ───────────────────────────────────────────────────────────── */

/*
 * Named permission bundles (Owner, Admin, Vendor, Customer, etc.).
 * Roles are soft-deleted via is_active rather than hard-deleted because hard DELETE
 * would be blocked by RESTRICT foreign keys from all audit tables.
 *
 * system roles (is_system_role = TRUE) are managed by ops and cannot be deleted by end users.
 * The owner role (is_owner_role = TRUE) implies unrestricted access and must always be a
 * system role (enforced by chk_roles_owner_must_be_system).
 */
CREATE TABLE roles (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Unique human-readable name (e.g. 'admin', 'vendor'). Max 100 chars.
 name VARCHAR(100) UNIQUE NOT NULL,

 -- Optional description displayed in the admin UI.
 description TEXT,

 -- TRUE for built-in roles managed by ops; end users cannot delete these.
 is_system_role BOOLEAN NOT NULL DEFAULT FALSE,

 -- TRUE grants unrestricted access. Must also be a system role (see constraint below).
 is_owner_role BOOLEAN NOT NULL DEFAULT FALSE,

 -- Soft-delete: set FALSE to deactivate a role without breaking audit history FKs.
 -- All role-based access checks must filter WHERE is_active = TRUE.
 is_active BOOLEAN NOT NULL DEFAULT TRUE,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Prevents a non-system role from claiming owner-level unrestricted access.
 CONSTRAINT chk_roles_owner_must_be_system
 CHECK (NOT is_owner_role OR is_system_role = TRUE)
);

-- No explicit idx_roles_name: the UNIQUE constraint on name creates an implicit index
-- that already covers name-based lookups. A duplicate explicit index wastes write overhead.

COMMENT ON TABLE roles IS
 'Role definitions (Owner, Admin, Vendor, Customer, etc.). Soft-delete via is_active — hard DELETE is blocked by RESTRICT FKs on audit tables.';
COMMENT ON COLUMN roles.is_system_role IS
 'TRUE = built-in role managed by ops; cannot be deleted by end users.';
COMMENT ON COLUMN roles.is_owner_role IS
 'TRUE = unrestricted access. chk_roles_owner_must_be_system requires is_system_role = TRUE simultaneously.';

-- Partial index: active-only listing ordered by name, used by GetRoles.
CREATE INDEX idx_roles_active_name ON roles(name) WHERE is_active = TRUE;


/* ─────────────────────────────────────────────────────────────
 PERMISSIONS
 ───────────────────────────────────────────────────────────── */

/*
 * Atomic action/resource pairs that can be granted to roles or directly to users.
 * Canonical form: resource_type:name (e.g. product:create, vendor_payout:approve).
 *
 * The canonical_name column is computed (GENERATED ALWAYS AS … STORED) to avoid
 * application-side string construction on every permission lookup.
 */
CREATE TABLE permissions (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Action verb part of the canonical name (create, read, update, delete, approve, export).
 name VARCHAR(100) NOT NULL,

 -- Resource domain part of the canonical name (product, request, vendor_payout, analytics).
 resource_type VARCHAR(100) NOT NULL,

 -- Human-readable description for the admin UI.
 description TEXT,

 -- Stored generated column: resource_type || ':' || name. Max 210 chars (100+1+100+padding).
 -- Used for fast canonical-name lookups without application-side string concatenation.
 canonical_name VARCHAR(210) GENERATED ALWAYS AS (resource_type || ':' || name) STORED,

 -- Soft-delete; CHECK constraints in role_permissions prevent granting inactive permissions.
 is_active BOOLEAN NOT NULL DEFAULT TRUE,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Speeds up canonical-name lookups (the most common permission check pattern).
CREATE UNIQUE INDEX idx_permissions_canonical ON permissions(canonical_name);

-- Supports listing all permissions for a given resource type (admin UI, permission browser).
CREATE INDEX idx_permissions_resource_name ON permissions(resource_type, name);

-- Partial index: active-only listing ordered by canonical_name, used by GetPermissions.
CREATE INDEX idx_permissions_active_canonical ON permissions(canonical_name) WHERE is_active = TRUE;

COMMENT ON TABLE permissions IS
 'Permission definitions. Canonical form: resource_type:name (e.g. product:create). Soft-delete via is_active.';
COMMENT ON COLUMN permissions.name IS
 'Action verb: create, read, update, delete, approve, export, etc.';
COMMENT ON COLUMN permissions.resource_type IS
 'Resource domain: product, request, vendor_payout, analytics, etc.';
COMMENT ON COLUMN permissions.canonical_name IS
 'Generated: resource_type || '':'' || name. Used for fast canonical-name lookups.';


/* ─────────────────────────────────────────────────────────────
 PERMISSION GROUPS
 ───────────────────────────────────────────────────────────── */

/*
 * Groups serve three purposes: UI organisation, bulk role assignment, and
 * permission discoverability. A single permission may belong to multiple groups.
 * Groups themselves confer no permissions — they are purely organisational.
 */
CREATE TABLE permission_groups (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Unique machine-readable name (e.g. 'billing', 'content_management').
 name VARCHAR(100) UNIQUE NOT NULL,
 description TEXT,

 -- Human-readable label shown in the admin UI. Falls back to name if NULL.
 display_label VARCHAR(150),

 -- Lucide icon key (e.g. 'shield', 'dollar-sign') for the admin UI badge.
 icon VARCHAR(100),

 -- CSS hex colour for the badge (e.g. '#3B82F6'). Must match #RRGGBB format.
 color_hex CHAR(7),

 -- Ascending sort order for the admin UI permission browser. 0 = first.
 display_order INTEGER NOT NULL DEFAULT 0,

 -- FALSE = hidden from non-admin interfaces (e.g. internal or system-only groups).
 is_visible BOOLEAN NOT NULL DEFAULT TRUE,

 -- Soft-delete; inactive groups are hidden from the UI and ignored by bulk assignment.
 is_active BOOLEAN NOT NULL DEFAULT TRUE,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Rejects any color_hex value that doesn't follow the #RRGGBB format.
 CONSTRAINT chk_pg_color_hex_format
 CHECK (color_hex IS NULL OR color_hex ~ '^#[0-9A-Fa-f]{6}$'),

 CONSTRAINT chk_pg_display_order_non_negative
 CHECK (display_order >= 0)
);

-- Used by the admin UI to render groups in display_order sequence.
-- Partial index: excludes inactive groups from the hot-path sort.
CREATE INDEX idx_permission_groups_order ON permission_groups(display_order, name) WHERE is_active = TRUE;

COMMENT ON TABLE permission_groups IS
 'Groups permissions for UI organisation and bulk role assignment. A permission may belong to multiple groups.';
COMMENT ON COLUMN permission_groups.color_hex IS
 'Hex colour for UI badges. Must be #RRGGBB — enforced by chk_pg_color_hex_format.';
COMMENT ON COLUMN permission_groups.is_visible IS
 'FALSE = hidden from non-admin interfaces (e.g. internal or system-only groups).';


/* ─────────────────────────────────────────────────────────────
 PERMISSION GROUP MEMBERS
 ───────────────────────────────────────────────────────────── */

-- Many-to-many join table between permission_groups and permissions.
-- A permission may belong to multiple groups; a group holds multiple permissions.
-- CASCADE DELETE on both sides: removing a group or permission cleans up membership rows.
CREATE TABLE permission_group_members (
 group_id UUID NOT NULL REFERENCES permission_groups(id) ON DELETE CASCADE,
 permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY (group_id, permission_id)
);

-- Reverse-lookup index: "which groups does permission X belong to?"
-- The PK covers (group_id, permission_id) lookups; this covers the reverse direction.
CREATE INDEX idx_pgm_permission ON permission_group_members(permission_id);

COMMENT ON TABLE permission_group_members IS
 'Many-to-many join between permission_groups and permissions. CASCADE DELETE on both sides.';


/* ─────────────────────────────────────────────────────────────
 PERMISSION CONDITION TEMPLATES
 ───────────────────────────────────────────────────────────── */

/*
 * Defines the valid ABAC condition vocabulary for each permission.
 * Validation is intentionally split between DB and app layers:
 *
 * DB trigger (004_rbac_functions.sql) — structural: required/forbidden key presence.
 * App layer — value/type/range/enum checks from validation_rules.
 *
 * App-layer validation is preferred for value rules because adding new rule types
 * (e.g. "regex") requires no DB migration and is easier to unit-test.
 */
CREATE TABLE permission_condition_templates (
 -- 1:1 with permissions; CASCADE means the template is removed when its permission is deleted.
 permission_id UUID PRIMARY KEY REFERENCES permissions(id) ON DELETE CASCADE,

 -- Keys that MUST be present in any conditions JSONB on a grant for this permission.
 -- e.g. {"amount_max": true} means every conditional grant must supply amount_max.
 required_conditions JSONB,

 -- Keys that MUST NOT appear in conditions — prevents known privilege escalation paths.
 -- e.g. {"bypass_approval": true} blocks a dangerous escape hatch from being granted.
 forbidden_conditions JSONB,

 -- Per-key value constraints evaluated in the app layer.
 -- Format: { "amount_max": { "type": "number", "max": 10000 },
 -- "resource_ownership": { "enum": ["own", "any"] } }
 validation_rules JSONB,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- All three JSONB columns must be objects (not arrays or scalars) if provided.
 CONSTRAINT chk_pct_valid_jsonb_shapes CHECK (
 (required_conditions IS NULL OR jsonb_typeof(required_conditions) = 'object') AND
 (forbidden_conditions IS NULL OR jsonb_typeof(forbidden_conditions) = 'object') AND
 (validation_rules IS NULL OR jsonb_typeof(validation_rules) = 'object')
 )
);

COMMENT ON TABLE permission_condition_templates IS
 'Per-permission rules defining valid ABAC conditions on grants. Structural checks enforced by DB trigger; value validation in app layer.';
COMMENT ON COLUMN permission_condition_templates.required_conditions IS
 'Keys that MUST be present in conditions on any grant for this permission.';
COMMENT ON COLUMN permission_condition_templates.forbidden_conditions IS
 'Keys that MUST NOT appear in conditions — prevents known privilege escalation paths.';
COMMENT ON COLUMN permission_condition_templates.validation_rules IS
 'Per-key type/range/enum constraints. Evaluated in the app layer; adding new rule types requires no DB migration.';


/* ─────────────────────────────────────────────────────────────
 ROLE PERMISSIONS
 ───────────────────────────────────────────────────────────── */

/*
 * Maps roles to permissions with ABAC conditions, access_type, and scope.
 * Every grant requires a named accountable human (granted_by); no anonymous grants.
 *
 * access_type controls middleware behaviour:
 * direct — pass immediately
 * conditional — pass but enforce conditions JSONB in the app layer
 * request — return 202 with approval_required; see permission_request_approvers
 * denied — always return 403 regardless of other grants
 */
CREATE TABLE role_permissions (
 -- Composite PK; one row per (role, permission) pair.
 role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
 permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,

 -- Optional ABAC conditions that narrow when this permission applies.
 -- Must be a JSON object. Validated against permission_condition_templates in the app layer.
 conditions JSONB NOT NULL DEFAULT '{}',

 -- Controls how the middleware responds to this grant (see type comment above).
 access_type permission_access_type NOT NULL DEFAULT 'direct',

 -- Controls which resources the grantee may act on: 'own' or 'all'.
 scope permission_scope NOT NULL DEFAULT 'own',

 -- Human accountable for creating this grant. RESTRICT prevents their deletion while
 -- the grant exists. NULL only for system-seeded grants (no individual accountable);
 -- the service layer enforces non-NULL for all application-created grants.
 granted_by UUID REFERENCES users(id) ON DELETE RESTRICT,

 -- Free-text business justification. Required for accountability; max 500 chars.
 granted_reason TEXT NOT NULL,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 PRIMARY KEY (role_id, permission_id),

 -- conditions must always be a JSON object, never an array or scalar.
 CONSTRAINT chk_rp_conditions_is_object
 CHECK (jsonb_typeof(conditions) = 'object'),

 -- conditional grants must carry non-empty conditions (otherwise they behave like direct).
 CONSTRAINT chk_rp_conditional_needs_conditions
 CHECK (access_type != 'conditional' OR conditions != '{}'),

 -- denied and request grants make conditions meaningless — require them to be empty.
 CONSTRAINT chk_rp_denied_no_conditions
 CHECK (access_type != 'denied' OR conditions = '{}'),
 CONSTRAINT chk_rp_request_no_conditions
 CHECK (access_type != 'request' OR conditions = '{}'),

 CONSTRAINT chk_rp_granted_reason_length
 CHECK (length(granted_reason) <= 500)
);

-- Reverse-lookup: "which roles have permission X?" (admin permission browser).
CREATE INDEX idx_role_permissions_perm ON role_permissions(permission_id);

-- GIN index supports JSONB containment queries (@>) on conditions, used by the app layer
-- to find grants that match a specific ABAC condition set.
CREATE INDEX idx_role_permissions_conditions ON role_permissions USING GIN(conditions jsonb_ops);

-- Covering index avoids a heap fetch on the role-first permission-check JOIN path
-- (user_id → role_id → role_permissions). Includes the three payload columns so no
-- heap fetch is needed when the planner approaches via role_id.
CREATE INDEX idx_role_perms_covering ON role_permissions(role_id, permission_id) INCLUDE (conditions, access_type, scope);

-- Covering index for the permission-first lookup path used by CheckUserAccess after
-- the perm_id CTE resolves permission_id up-front. Enables a fully index-only scan
-- from (permission_id, role_id) without visiting the heap.
CREATE INDEX idx_rp_perm_role_covering ON role_permissions(permission_id, role_id) INCLUDE (access_type, scope, conditions);

COMMENT ON TABLE role_permissions IS
 'Maps roles → permissions with optional ABAC conditions, access_type, and scope. Every grant requires a named accountable human. All mutations logged to role_permissions_audit.';
COMMENT ON COLUMN role_permissions.conditions IS
 'JSONB object narrowing when the permission applies. Validated against permission_condition_templates in the app layer.';
COMMENT ON COLUMN role_permissions.access_type IS
 'direct=pass, conditional=pass+enforce conditions, request=202 approval flow, denied=403.';
COMMENT ON COLUMN role_permissions.scope IS
 'own=resources user owns, all=any resource of this type.';
COMMENT ON COLUMN role_permissions.granted_by IS
 'RESTRICT prevents that user being deleted while the grant exists.';


/* ─────────────────────────────────────────────────────────────
 ROLE PERMISSIONS AUDIT
 ───────────────────────────────────────────────────────────── */

-- Immutable audit log for every INSERT/UPDATE/DELETE on role_permissions.
-- Snapshots both before and after state so changes can be fully reconstructed.
-- RESTRICT FKs on role and permission prevent deletion while audit history exists.
CREATE TABLE role_permissions_audit (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Denormalised copies of the composite PK from role_permissions.
 role_id UUID NOT NULL,
 permission_id UUID NOT NULL,

 -- State after the mutation. NULL on DELETE (no "new" state).
 conditions JSONB,
 access_type permission_access_type,
 scope permission_scope,

 -- State before the mutation. NULL on INSERT (no "previous" state).
 previous_conditions JSONB,
 previous_access_type permission_access_type,
 previous_scope permission_scope,

 change_type audit_change_type_enum NOT NULL,

 -- Actor who made the change. Nullable: SET NULL when the actor is hard-purged
 -- so the audit row is preserved even after the actor's account is deleted.
 changed_by UUID,
 changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Optional context string, populated from SET LOCAL rbac.change_reason if the app sets it.
 change_reason TEXT,

 -- RESTRICT: prevents deletion of the role or permission while audit history references them.
 CONSTRAINT fk_rp_audit_role FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE RESTRICT,
 CONSTRAINT fk_rp_audit_permission FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE RESTRICT,
 -- SET NULL: preserves the audit row when the actor is hard-purged.
 CONSTRAINT fk_rp_audit_changed_by FOREIGN KEY (changed_by) REFERENCES users(id) ON DELETE SET NULL
);

-- Supports time-range audit queries filtered by role (e.g. "all changes to role X in the last week").
CREATE INDEX idx_rp_audit_time_bucket ON role_permissions_audit(changed_at, role_id);

-- Supports full audit log ordered by recency.
CREATE INDEX idx_rp_audit_recent ON role_permissions_audit(changed_at DESC);

-- Supports "what did admin Y change?" queries. Partial index skips NULL changed_by rows.
CREATE INDEX idx_rp_audit_changer ON role_permissions_audit(changed_by, changed_at DESC)
 WHERE changed_by IS NOT NULL;

-- Supports filtering by mutation kind (e.g. "all deletions in the last month").
CREATE INDEX idx_rp_audit_change_type ON role_permissions_audit(change_type, changed_at DESC);

COMMENT ON TABLE role_permissions_audit IS
 'Immutable audit log for role_permissions. Populated by trg_audit_role_permissions.
 RESTRICT FKs on role/permission prevent deletion while history exists.
 changed_by is nullable: SET NULL when actor is hard-purged so audit rows are preserved.
 Retention: rows older than 90 days swept by retention job using idx_rp_audit_recent (DESC scan).';
COMMENT ON COLUMN role_permissions_audit.previous_conditions IS
 'Snapshot of conditions before the mutation.';
COMMENT ON COLUMN role_permissions_audit.previous_access_type IS
 'Snapshot of access_type before the mutation.';
COMMENT ON COLUMN role_permissions_audit.previous_scope IS
 'Snapshot of scope before the mutation.';


/* ─────────────────────────────────────────────────────────────
 USER ROLES (one role per user)
 ───────────────────────────────────────────────────────────── */

/*
 * Assigns exactly one role to each user. The user_id PRIMARY KEY is the
 * DB-level enforcement of the one-role-per-user invariant.
 *
 * Multi-role complexity (priority ordering, conflict resolution) is deliberately
 * avoided. Orthogonal access needs (e.g. a temporary admin window) are handled
 * via user_permissions (time-bounded direct grants).
 *
 * Permanent grants have expires_at = NULL. Temporary grants must pass the
 * minimum lead-time check enforced by trg_validate_user_role_expiry.
 */
CREATE TABLE user_roles (
 -- PK enforces at most one role per user at the DB level.
 user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,

 -- RESTRICT: the role cannot be deleted while any user holds it.
 role_id UUID NOT NULL REFERENCES roles(id) ON DELETE RESTRICT,

 -- RESTRICT: the granter cannot be deleted while the assignment exists.
 granted_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,

 -- Business justification for the assignment. Required; max 500 chars.
 granted_reason TEXT NOT NULL,

 -- NULL = permanent grant. Non-NULL = expires at this timestamp.
 -- Always filter: (expires_at IS NULL OR expires_at > NOW()) when checking active grants.
 expires_at TIMESTAMPTZ,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 CONSTRAINT chk_ur_granted_reason_length
 CHECK (length(granted_reason) <= 500)
);

-- Hot path: check a specific user's current role and expiry in one index scan.
-- INCLUDE avoids a heap fetch for the common case.
CREATE INDEX idx_user_roles_lookup ON user_roles(user_id, role_id) INCLUDE (expires_at);

-- Reverse lookup: "who holds role Y?" used by admin bulk queries and the orphaned-owner check.
CREATE INDEX idx_user_roles_role_user ON user_roles(role_id, user_id) INCLUDE (expires_at);

COMMENT ON TABLE user_roles IS
 'Assigns exactly one role per user. user_id PK enforces this at the DB level. Deletion of the last active owner is blocked by trg_prevent_orphaned_owner.';
COMMENT ON COLUMN user_roles.expires_at IS
 'NULL = permanent. Always filter: (expires_at IS NULL OR expires_at > NOW()).';
COMMENT ON COLUMN user_roles.granted_by IS
 'RESTRICT prevents that user being deleted while the assignment exists.';


/* ─────────────────────────────────────────────────────────────
 USER ROLES AUDIT
 ───────────────────────────────────────────────────────────── */

CREATE TABLE user_roles_audit (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 user_id UUID NOT NULL,
 role_id UUID NOT NULL,

 -- Role held before the change. NULL on the first (INSERT) grant for a user.
 previous_role_id UUID,

 expires_at          TIMESTAMPTZ,
 previous_expires_at TIMESTAMPTZ,

 change_type audit_change_type_enum NOT NULL,
 changed_by UUID, -- nullable: SET NULL when the actor is hard-purged
 changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 change_reason TEXT, -- from SET LOCAL rbac.change_reason if set by the app

 CONSTRAINT fk_ur_audit_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
 CONSTRAINT fk_ur_audit_role FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE RESTRICT,
 CONSTRAINT fk_ur_audit_changed_by FOREIGN KEY (changed_by) REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_ur_audit_time_bucket ON user_roles_audit(changed_at, user_id);
CREATE INDEX idx_ur_audit_recent ON user_roles_audit(changed_at DESC);
CREATE INDEX idx_ur_audit_changer ON user_roles_audit(changed_by, changed_at DESC)
 WHERE changed_by IS NOT NULL;

COMMENT ON TABLE user_roles_audit IS
 'Immutable audit log for user_roles. Populated by trg_audit_user_roles.
 RESTRICT FKs on user/role prevent deletion while history exists.
 changed_by is nullable: SET NULL when actor is hard-purged so audit rows are preserved.
 Retention: rows older than 90 days swept by retention job using idx_ur_audit_recent (DESC scan).';
COMMENT ON COLUMN user_roles_audit.previous_role_id IS
 'Snapshot of role_id before the change.';


/* ─────────────────────────────────────────────────────────────
 USER PERMISSIONS (temporary direct grants)
 ───────────────────────────────────────────────────────────── */

/*
 * Time-bounded direct permission grants that bypass the role model.
 * Intended for short-lived exceptions only (e.g. "grant vendor X access to analytics
 * for 48 hours while we diagnose a billing issue").
 *
 * Key constraints:
 * expires_at is REQUIRED and bounded by policy: min 5 min, max 90 days from now.
 * access_type is always 'direct' (no approval gate at this layer).
 * Revocation = hard DELETE; history is preserved in user_permissions_audit.
 * The granter must hold the permission themselves (fn_prevent_privilege_escalation).
 *
 * uq_up_one_active_grant_per_user_perm is a FULL unique index on (user_id, permission_id)
 * — not a partial index. NOW() is STABLE (not IMMUTABLE) so it cannot be used in an
 * index predicate. This means re-granting the same permission requires the previous
 * grant to be hard-deleted first (call RevokeUserPermission before GrantUserPermission).
 * Attempting to re-grant an expired but not-yet-cleaned-up grant will return 23505
 * (unique_violation), which the service maps to ErrPermissionAlreadyGranted → 409.
 */
CREATE TABLE user_permissions (
 -- Surrogate PK decouples identity from (user_id, permission_id), allowing clean
 -- re-grant after revocation without needing to DELETE the old row first.
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Recipient of the grant. CASCADE on user deletion.
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

 -- The permission being granted. CASCADE on permission deletion.
 permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,

 -- Optional ABAC conditions; uses the same vocabulary as role_permissions.conditions.
 conditions JSONB NOT NULL DEFAULT '{}',

 -- Always 'direct' for per-user grants — direct grants do not go through an approval gate.
 access_type permission_access_type NOT NULL DEFAULT 'direct',

 -- Controls resource visibility: 'own' (default, safe) or 'all'.
 scope permission_scope NOT NULL DEFAULT 'own',

 -- RESTRICT: the granter cannot be deleted while this grant exists.
 granted_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 granted_reason TEXT NOT NULL,

 -- Required. Bounded by trg_validate_user_permission_expiry: min 5 min, max 90 days from now.
 expires_at TIMESTAMPTZ NOT NULL,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 CONSTRAINT chk_up_conditions_is_object
 CHECK (jsonb_typeof(conditions) = 'object'),

 -- Direct grants to individual users do not confer grant rights to the grantee.
 CONSTRAINT chk_up_access_type_direct
 CHECK (access_type = 'direct')
);

-- Full unique index on (user_id, permission_id). One active grant per user per permission.
-- A partial index predicate (e.g. WHERE expires_at > NOW()) is not possible because NOW()
-- is STABLE, not IMMUTABLE. Re-granting after expiry therefore requires an explicit
-- RevokeUserPermission (DELETE) before calling GrantUserPermission. The cleanup job
-- must also run regularly to prevent 409s on re-grants of naturally expired rows.
CREATE UNIQUE INDEX uq_up_one_active_grant_per_user_perm
 ON user_permissions (user_id, permission_id);

-- Reverse lookup: "which users have permission X?" (admin UI).
CREATE INDEX idx_user_permissions_perm ON user_permissions(permission_id);

-- Supports cleanup job scanning for expired grants.
CREATE INDEX idx_user_permissions_expires ON user_permissions(expires_at);

-- Hot path for "what active grants does user X have?" — filters on both user_id and expiry.
-- INCLUDE avoids heap fetches for permission_id, scope, and conditions on the CheckUserAccess path.
CREATE INDEX idx_user_permissions_user_expires ON user_permissions(user_id, expires_at) INCLUDE (permission_id, scope, conditions);

COMMENT ON TABLE user_permissions IS
 'Temporary direct permission grants — exceptions to the role model. Revocation = DELETE; history preserved in user_permissions_audit.
 Granter must hold the permission themselves (trg_prevent_privilege_escalation).
 UNIQUE(user_id, permission_id) enforced by uq_up_one_active_grant_per_user_perm (full index, not partial —
 NOW() cannot be used in index predicates). Re-granting requires the previous grant to be
 hard-deleted first; the cleanup job must run regularly to avoid stale-grant 409s.';
COMMENT ON COLUMN user_permissions.expires_at IS
 'REQUIRED. Bounded by trg_validate_user_permission_expiry: min 5 min, max 90 days from now.';
COMMENT ON COLUMN user_permissions.access_type IS
 'Always ''direct'' for per-user grants. Enforced by chk_up_access_type_direct.
 Direct grants to individual users do not confer grant rights to the grantee.';
COMMENT ON COLUMN user_permissions.conditions IS
 'ABAC conditions in the same vocabulary as role_permissions. Validated against permission_condition_templates in the app layer.';
COMMENT ON COLUMN user_permissions.scope IS
 'own=resources user owns, all=any resource of this type. Defaults to own (safe default).';


/* ─────────────────────────────────────────────────────────────
 USER PERMISSIONS AUDIT
 ───────────────────────────────────────────────────────────── */

-- This is the highest-risk RBAC table; every mutation is tracked unconditionally.
CREATE TABLE user_permissions_audit (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 user_id UUID NOT NULL,
 permission_id UUID NOT NULL,

 -- State after the mutation. NULL on DELETE.
 conditions JSONB,
 scope permission_scope,

 -- State before the mutation. NULL on INSERT.
 previous_conditions JSONB,
 previous_scope permission_scope,

 expires_at          TIMESTAMPTZ,
 previous_expires_at TIMESTAMPTZ,

 change_type audit_change_type_enum NOT NULL,
 changed_by UUID, -- nullable: SET NULL when actor is hard-purged
 changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 change_reason TEXT, -- from SET LOCAL rbac.change_reason if set by the app

 CONSTRAINT fk_up_audit_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
 CONSTRAINT fk_up_audit_permission FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE RESTRICT,
 CONSTRAINT fk_up_audit_changed_by FOREIGN KEY (changed_by) REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_up_audit_time_bucket ON user_permissions_audit(changed_at, user_id);
CREATE INDEX idx_up_audit_recent ON user_permissions_audit(changed_at DESC);
CREATE INDEX idx_up_audit_changer ON user_permissions_audit(changed_by, changed_at DESC)
 WHERE changed_by IS NOT NULL;

COMMENT ON TABLE user_permissions_audit IS
 'Immutable audit log for user_permissions — highest-risk RBAC table; every mutation is tracked unconditionally. Populated by trg_audit_user_permissions.
 changed_by is nullable: SET NULL when actor is hard-purged so audit rows are preserved.
 Retention: rows older than 90 days swept by retention job using idx_up_audit_recent (DESC scan).';
COMMENT ON COLUMN user_permissions_audit.previous_conditions IS
 'Snapshot of conditions before the mutation.';
COMMENT ON COLUMN user_permissions_audit.previous_scope IS
 'Snapshot of scope before the mutation.';


/* ─────────────────────────────────────────────────────────────
 PERMISSION REQUEST APPROVERS
 ───────────────────────────────────────────────────────────── */

/*
 * When a role_permissions row has access_type = 'request', this table defines
 * which roles must approve before the action executes. It hooks into the existing
 * 005_requests.sql approval workflow — no duplicate approval machinery.
 *
 * Runtime flow:
 * 1. User hits an endpoint guarded by a 'request'-type permission.
 * 2. Middleware reads access_type = 'request' from CheckUserAccess.
 * 3. App creates a requests row (request_type = 'permission_action').
 * 4. request_required_approvers is populated from this table.
 * 5. Middleware returns 202 {"code":"approval_required","request_id":"..."}.
 * 6. Once approved, the execute_request handler runs the action.
 */
CREATE TABLE permission_request_approvers (
 -- CASCADE: removing a permission removes its approval requirements.
 permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,

 -- CASCADE: removing a role removes it from all approval chains.
 role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,

 -- Hierarchical tier: 0 = first approver tier, 1 = second, etc.
 approval_level INTEGER NOT NULL DEFAULT 0,

 -- Minimum number of approvals required from this role at this level.
 min_required INTEGER NOT NULL DEFAULT 1,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 PRIMARY KEY (permission_id, role_id),

 CONSTRAINT chk_pra_level_non_negative CHECK (approval_level >= 0),
 CONSTRAINT chk_pra_min_required_pos CHECK (min_required > 0)
);

-- Reverse lookup: "which permissions require approval from role Y?"
CREATE INDEX idx_pra_role ON permission_request_approvers(role_id);

-- Lookup by level: "who approves at level 0 for permission X?" (sequential approval logic).
CREATE INDEX idx_pra_level ON permission_request_approvers(permission_id, approval_level);

COMMENT ON TABLE permission_request_approvers IS
 'Defines which roles must approve a permission-action request when access_type = ''request''. Feeds request_required_approvers at runtime.';
COMMENT ON COLUMN permission_request_approvers.approval_level IS
 'Hierarchical level: 0 = first approver tier, 1 = second, etc.';
COMMENT ON COLUMN permission_request_approvers.min_required IS
 'Minimum number of approvals needed from this role at this level.';


/* ─────────────────────────────────────────────────────────────
 PERMISSION REQUEST APPROVERS AUDIT
 ───────────────────────────────────────────────────────────── */

-- Changes to who-approves-what are high-risk RBAC mutations; immutable history is required.
CREATE TABLE permission_request_approvers_audit (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 permission_id UUID NOT NULL,
 role_id UUID NOT NULL,

 -- State after the mutation. NULL on DELETE.
 approval_level INTEGER,
 min_required INTEGER,

 -- State before the mutation. NULL on INSERT.
 previous_approval_level INTEGER,
 previous_min_required INTEGER,

 change_type audit_change_type_enum NOT NULL,
 changed_by UUID, -- NULL when actor is unknown or has been hard-purged
 changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 change_reason TEXT,

 CONSTRAINT fk_pra_audit_permission FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE RESTRICT,
 CONSTRAINT fk_pra_audit_role FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE RESTRICT,
 CONSTRAINT fk_pra_audit_changed_by FOREIGN KEY (changed_by) REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_pra_audit_recent ON permission_request_approvers_audit(changed_at DESC);
CREATE INDEX idx_pra_audit_permission ON permission_request_approvers_audit(permission_id, changed_at DESC);

COMMENT ON TABLE permission_request_approvers_audit IS
 'Immutable audit log for permission_request_approvers. Every mutation is tracked because
 changes to approval chains are high-risk RBAC events. changed_by is nullable:
 SET NULL when the actor is hard-purged to preserve audit row integrity.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_pra_audit_permission;
DROP INDEX IF EXISTS idx_pra_audit_recent;
DROP TABLE IF EXISTS permission_request_approvers_audit CASCADE;
DROP INDEX IF EXISTS uq_up_one_active_grant_per_user_perm;
DROP INDEX IF EXISTS idx_rp_perm_role_covering;
DROP TABLE IF EXISTS permission_request_approvers CASCADE;
DROP TABLE IF EXISTS user_permissions_audit CASCADE;
DROP TABLE IF EXISTS user_permissions CASCADE;
DROP INDEX IF EXISTS idx_ur_audit_change_type;
DROP TABLE IF EXISTS user_roles_audit CASCADE;
DROP TABLE IF EXISTS user_roles CASCADE;
DROP TABLE IF EXISTS role_permissions_audit CASCADE;
DROP TABLE IF EXISTS role_permissions CASCADE;
DROP TABLE IF EXISTS permission_group_members CASCADE;
DROP TABLE IF EXISTS permission_condition_templates CASCADE;
DROP TABLE IF EXISTS permission_groups CASCADE;
DROP TABLE IF EXISTS permissions CASCADE;
DROP INDEX IF EXISTS idx_roles_owner_active;
DROP TABLE IF EXISTS roles CASCADE;

DROP TYPE IF EXISTS permission_scope CASCADE;
DROP TYPE IF EXISTS permission_access_type CASCADE;
DROP TYPE IF EXISTS audit_change_type_enum CASCADE;

-- +goose StatementEnd
