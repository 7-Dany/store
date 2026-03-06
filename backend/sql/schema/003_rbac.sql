-- +goose Up
-- +goose StatementBegin

-- 003_rbac.sql — RBAC schema: roles, permissions, permission groups,
-- condition templates, and the join/audit tables linking them.
-- Depends on: 001_core.sql


-- ------------------------------------------------------------
-- AUDIT ENUM
-- ------------------------------------------------------------

CREATE TYPE audit_change_type_enum AS ENUM (
    'created',
    'updated',
    'deleted'
);

COMMENT ON TYPE audit_change_type_enum IS
    'Mutation kind recorded in all *_audit tables. Populated by AFTER INSERT/UPDATE/DELETE triggers.';


-- ------------------------------------------------------------
-- POLICY CONSTANTS
-- ------------------------------------------------------------
-- Session-level defaults read by trigger functions in 004_rbac_functions.sql.
-- Override per-transaction in tests: SET LOCAL rbac.min_temp_grant_lead = '1 second'.
-- Triggers have hard-coded fallbacks, so a failure here is non-fatal.

DO $$ BEGIN
    PERFORM set_config('rbac.min_temp_grant_lead',     '5 minutes', FALSE);
    PERFORM set_config('rbac.max_temp_grant_interval', '90 days',   FALSE);
END $$;


-- ------------------------------------------------------------
-- ROLES
-- ------------------------------------------------------------

CREATE TABLE roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    name        VARCHAR(100) UNIQUE NOT NULL,
    description TEXT,

    is_system_role BOOLEAN DEFAULT FALSE,  -- built-in; cannot be deleted by end users
    is_owner_role  BOOLEAN DEFAULT FALSE,  -- unrestricted access; must also be a system role

    -- Soft-delete: hard DELETE would violate RESTRICT FKs on audit tables.
    -- Always filter: WHERE is_active = TRUE
    is_active BOOLEAN DEFAULT TRUE,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    -- Prevents a user-created role from claiming owner-level access.
    CONSTRAINT chk_roles_owner_must_be_system
        CHECK (NOT is_owner_role OR is_system_role = TRUE)
);

CREATE INDEX idx_roles_name   ON roles(name);
CREATE INDEX idx_roles_system ON roles(is_system_role) WHERE is_system_role = TRUE;
CREATE INDEX idx_roles_owner  ON roles(is_owner_role)  WHERE is_owner_role  = TRUE;
CREATE INDEX idx_roles_active ON roles(is_active)      WHERE is_active      = TRUE;

COMMENT ON TABLE  roles IS
    'Role definitions (Owner, Admin, Vendor, Customer, etc.). Soft-delete via is_active — hard DELETE is blocked by RESTRICT FKs on audit tables.';
COMMENT ON COLUMN roles.is_system_role IS
    'TRUE = built-in role managed by ops. Cannot be deleted by end users.';
COMMENT ON COLUMN roles.is_owner_role IS
    'TRUE = unrestricted access. chk_roles_owner_must_be_system requires is_system_role = TRUE simultaneously.';
COMMENT ON COLUMN roles.is_active IS
    'FALSE = soft-deleted. Always filter WHERE is_active = TRUE.';


-- ------------------------------------------------------------
-- PERMISSIONS
-- ------------------------------------------------------------

CREATE TABLE permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    name          VARCHAR(100) NOT NULL,
    resource_type VARCHAR(100) NOT NULL,
    description   TEXT,

    -- Avoids application-side string construction on every lookup.
    canonical_name VARCHAR(210) GENERATED ALWAYS AS (resource_type || ':' || name) STORED,

    is_active BOOLEAN DEFAULT TRUE,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    CONSTRAINT uq_permissions_name_resource UNIQUE (name, resource_type)
);

CREATE UNIQUE INDEX idx_permissions_canonical     ON permissions(canonical_name);
CREATE INDEX        idx_permissions_resource_name ON permissions(resource_type, name);
CREATE INDEX        idx_permissions_active        ON permissions(is_active) WHERE is_active = TRUE;

COMMENT ON TABLE  permissions IS
    'Permission definitions. Canonical form: resource_type:name (e.g. product:create). Soft-delete via is_active.';
COMMENT ON COLUMN permissions.name IS
    'Action verb: create, read, update, delete, approve, export, etc.';
COMMENT ON COLUMN permissions.resource_type IS
    'Resource domain: product, request, vendor_payout, analytics, etc.';
COMMENT ON COLUMN permissions.canonical_name IS
    'Generated: resource_type || '':'' || name. Used for fast canonical-name lookups.';


-- ------------------------------------------------------------
-- PERMISSION GROUPS
-- ------------------------------------------------------------
-- Groups serve three purposes: UI organisation, bulk role assignment, and
-- permission discoverability. A permission may belong to multiple groups.
-- Groups are purely organisational — they confer no permissions themselves.

CREATE TABLE permission_groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    name        VARCHAR(100) UNIQUE NOT NULL,
    description TEXT,

    display_label VARCHAR(150),          -- falls back to name if NULL
    icon          VARCHAR(100),          -- icon key (e.g. Lucide: 'shield', 'dollar-sign')
    color_hex     CHAR(7),              -- badge colour: '#3B82F6'
    display_order INTEGER DEFAULT 0,    -- ascending sort order in admin UI
    is_visible    BOOLEAN DEFAULT TRUE, -- FALSE = hidden from non-admin interfaces

    is_active BOOLEAN DEFAULT TRUE,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    CONSTRAINT chk_pg_color_hex_format
        CHECK (color_hex IS NULL OR color_hex ~ '^#[0-9A-Fa-f]{6}$'),
    CONSTRAINT chk_pg_display_order_non_negative
        CHECK (display_order >= 0)
);

CREATE INDEX idx_permission_groups_order  ON permission_groups(display_order) WHERE is_active = TRUE;
CREATE INDEX idx_permission_groups_active ON permission_groups(is_active)     WHERE is_active = TRUE;

COMMENT ON TABLE  permission_groups IS
    'Groups permissions for UI organisation and bulk role assignment. A permission may belong to multiple groups.';
COMMENT ON COLUMN permission_groups.color_hex IS
    'Hex colour for UI badges. Must be #RRGGBB — enforced by chk_pg_color_hex_format.';
COMMENT ON COLUMN permission_groups.is_visible IS
    'FALSE = hidden from non-admin interfaces (e.g. internal or system-only groups).';


-- ------------------------------------------------------------
-- PERMISSION GROUP MEMBERS
-- ------------------------------------------------------------

CREATE TABLE permission_group_members (
    group_id      UUID NOT NULL REFERENCES permission_groups(id) ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id)       ON DELETE CASCADE,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (group_id, permission_id)
);

-- Reverse lookup: which groups does permission X belong to?
CREATE INDEX idx_pgm_permission ON permission_group_members(permission_id);

COMMENT ON TABLE permission_group_members IS
    'Many-to-many join between permission_groups and permissions. CASCADE DELETE on both sides.';


-- ------------------------------------------------------------
-- PERMISSION CONDITION TEMPLATES
-- ------------------------------------------------------------
-- Defines the valid ABAC condition vocabulary per permission.
--
-- Validation split:
--   DB trigger (004_rbac_functions.sql) — structural: required/forbidden key presence.
--   App layer — value/type/range/enum checks from validation_rules.
--
-- App-layer validation is intentional: new rule types (e.g. "regex") require no
-- DB migration and are easier to unit-test in isolation.

CREATE TABLE permission_condition_templates (
    permission_id UUID PRIMARY KEY REFERENCES permissions(id) ON DELETE CASCADE,

    required_conditions  JSONB,  -- keys that MUST be present in any conditions grant
    forbidden_conditions JSONB,  -- keys that MUST NOT appear (prevents known escalation paths)

    -- Per-key value constraints evaluated in the app layer.
    -- Format: { "amount_max": { "type": "number", "max": 10000 },
    --           "resource_ownership": { "enum": ["own", "any"] } }
    validation_rules JSONB,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    CONSTRAINT chk_pct_valid_jsonb_shapes CHECK (
        (required_conditions  IS NULL OR jsonb_typeof(required_conditions)  = 'object') AND
        (forbidden_conditions IS NULL OR jsonb_typeof(forbidden_conditions) = 'object') AND
        (validation_rules     IS NULL OR jsonb_typeof(validation_rules)     = 'object')
    )
);

COMMENT ON TABLE  permission_condition_templates IS
    'Per-permission rules defining valid ABAC conditions on grants. Structural checks enforced by DB trigger; value validation in app layer.';
COMMENT ON COLUMN permission_condition_templates.required_conditions IS
    'Keys that MUST be present in conditions on any grant for this permission.';
COMMENT ON COLUMN permission_condition_templates.forbidden_conditions IS
    'Keys that MUST NOT appear in conditions — prevents known privilege escalation paths.';
COMMENT ON COLUMN permission_condition_templates.validation_rules IS
    'Per-key type/range/enum constraints. Evaluated in the app layer; adding new rule types requires no DB migration.';


-- ------------------------------------------------------------
-- ROLE PERMISSIONS
-- ------------------------------------------------------------

CREATE TABLE role_permissions (
    role_id       UUID NOT NULL REFERENCES roles(id)       ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,

    -- Optional ABAC conditions narrowing when this permission applies.
    conditions JSONB DEFAULT '{}',

    -- Every grant must name a human accountable — no anonymous permission grants.
    granted_by     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    granted_reason TEXT NOT NULL,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    PRIMARY KEY (role_id, permission_id),

    CONSTRAINT chk_rp_conditions_is_object
        CHECK (jsonb_typeof(conditions) = 'object')
);

CREATE INDEX idx_role_permissions_perm       ON role_permissions(permission_id);
-- GIN index supports JSONB containment queries on conditions (@>).
CREATE INDEX idx_role_permissions_conditions ON role_permissions USING GIN(conditions);
-- Covering index avoids a heap fetch on the hot permission-check join path.
CREATE INDEX idx_role_perms_covering         ON role_permissions(role_id, permission_id) INCLUDE (conditions);

COMMENT ON TABLE  role_permissions IS
    'Maps roles → permissions with optional ABAC conditions. Every grant requires a named accountable human. All mutations logged to role_permissions_audit.';
COMMENT ON COLUMN role_permissions.conditions IS
    'JSONB object narrowing when the permission applies. Validated against permission_condition_templates in the app layer.';
COMMENT ON COLUMN role_permissions.granted_by IS
    'RESTRICT prevents that user being deleted while the grant exists.';


-- ------------------------------------------------------------
-- ROLE PERMISSIONS AUDIT
-- ------------------------------------------------------------

CREATE TABLE role_permissions_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    role_id             UUID                   NOT NULL,
    permission_id       UUID                   NOT NULL,
    conditions          JSONB,           -- state after the change; NULL on DELETE
    previous_conditions JSONB,           -- state before the change; NULL on INSERT

    change_type   audit_change_type_enum NOT NULL,
    changed_by    UUID        NOT NULL,
    changed_at    TIMESTAMPTZ DEFAULT NOW(),
    change_reason TEXT,  -- from SET LOCAL rbac.change_reason if set by the app

    CONSTRAINT fk_rp_audit_role       FOREIGN KEY (role_id)       REFERENCES roles(id)       ON DELETE RESTRICT,
    CONSTRAINT fk_rp_audit_permission FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE RESTRICT,
    CONSTRAINT fk_rp_audit_changed_by FOREIGN KEY (changed_by)    REFERENCES users(id)       ON DELETE RESTRICT
);

CREATE INDEX idx_rp_audit_time_bucket ON role_permissions_audit(changed_at, role_id);
CREATE INDEX idx_rp_audit_recent      ON role_permissions_audit(changed_at DESC);
CREATE INDEX idx_rp_audit_changer     ON role_permissions_audit(changed_by, changed_at DESC);
CREATE INDEX idx_rp_audit_change_type ON role_permissions_audit(change_type, changed_at DESC);

COMMENT ON TABLE  role_permissions_audit IS
    'Immutable audit log for role_permissions. Populated by trg_audit_role_permissions. RESTRICT FKs prevent deletion of referenced rows while history exists.';
COMMENT ON COLUMN role_permissions_audit.previous_conditions IS
    'Snapshot of conditions before the mutation. NULL on INSERT.';


-- ------------------------------------------------------------
-- USER ROLES  (ONE ROLE PER USER)
-- ------------------------------------------------------------
-- One role per user, enforced by the user_id PK.
-- Multi-role complexity (priority, conflict resolution) is deliberately avoided.
-- Orthogonal access needs are handled via user_permissions (temporary direct grants).

CREATE TABLE user_roles (
    -- PK enforces at most one role per user at the DB level.
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    role_id UUID NOT NULL   REFERENCES roles(id)  ON DELETE RESTRICT,

    granted_by     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    granted_reason TEXT NOT NULL,

    -- NULL = permanent. Always filter: (expires_at IS NULL OR expires_at > NOW())
    expires_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Hot path: what role does user X have, and has it expired?
CREATE INDEX idx_user_roles_lookup    ON user_roles(user_id, role_id) INCLUDE (expires_at);
-- Reverse: who holds role Y? — admin bulk queries.
CREATE INDEX idx_user_roles_role_user ON user_roles(role_id, user_id) INCLUDE (expires_at);

COMMENT ON TABLE  user_roles IS
    'Assigns exactly one role per user. user_id PK enforces this at the DB level. Deletion of the last active owner is blocked by trg_prevent_orphaned_owner.';
COMMENT ON COLUMN user_roles.expires_at IS
    'NULL = permanent. Always filter: (expires_at IS NULL OR expires_at > NOW()).';
COMMENT ON COLUMN user_roles.granted_by IS
    'RESTRICT prevents that user being deleted while the assignment exists.';


-- ------------------------------------------------------------
-- USER ROLES AUDIT
-- ------------------------------------------------------------

CREATE TABLE user_roles_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id          UUID NOT NULL,
    role_id          UUID NOT NULL,
    previous_role_id UUID,  -- role before the change; NULL on first grant

    change_type   audit_change_type_enum NOT NULL,
    changed_by    UUID        NOT NULL,
    changed_at    TIMESTAMPTZ DEFAULT NOW(),
    change_reason TEXT,  -- from SET LOCAL rbac.change_reason if set by the app

    CONSTRAINT fk_ur_audit_user       FOREIGN KEY (user_id)    REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT fk_ur_audit_role       FOREIGN KEY (role_id)    REFERENCES roles(id) ON DELETE RESTRICT,
    CONSTRAINT fk_ur_audit_changed_by FOREIGN KEY (changed_by) REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX idx_ur_audit_time_bucket ON user_roles_audit(changed_at, user_id);
CREATE INDEX idx_ur_audit_recent      ON user_roles_audit(changed_at DESC);
CREATE INDEX idx_ur_audit_changer     ON user_roles_audit(changed_by, changed_at DESC);

COMMENT ON TABLE  user_roles_audit IS
    'Immutable audit log for user_roles. Populated by trg_audit_user_roles. RESTRICT FKs prevent deletion of referenced rows while history exists.';
COMMENT ON COLUMN user_roles_audit.previous_role_id IS
    'Snapshot of role_id before the change. NULL on the initial grant.';


-- ------------------------------------------------------------
-- USER PERMISSIONS  (TEMPORARY EXCEPTIONS)
-- ------------------------------------------------------------
-- Direct grants to a specific user, bypassing the role model.
-- Intended for time-bounded exceptions only — expires_at is REQUIRED
-- and bounded by policy (min 5 min, max 90 days).
-- Revocation = hard DELETE; history is preserved in the audit table.

CREATE TABLE user_permissions (
    -- Surrogate PK decouples identity from (user_id, permission_id),
    -- allowing clean re-grant after revocation without PK conflicts.
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id       UUID NOT NULL REFERENCES users(id)       ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,

    conditions JSONB DEFAULT '{}',

    granted_by     UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    granted_reason TEXT NOT NULL,

    -- REQUIRED. Bounds enforced by trg_validate_user_permission_expiry:
    --   min: NOW() + rbac.min_temp_grant_lead     (default 5 min)
    --   max: NOW() + rbac.max_temp_grant_interval (default 90 days)
    expires_at TIMESTAMPTZ NOT NULL,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    -- One active grant per (user, permission). Re-grant after expiry = fresh INSERT.
    CONSTRAINT uq_up_one_active_grant_per_user_perm UNIQUE (user_id, permission_id),

    CONSTRAINT chk_up_conditions_is_object
        CHECK (jsonb_typeof(conditions) = 'object')
);

CREATE INDEX idx_user_permissions_user         ON user_permissions(user_id);
CREATE INDEX idx_user_permissions_perm         ON user_permissions(permission_id);
CREATE INDEX idx_user_permissions_expires      ON user_permissions(expires_at);
-- Hot path for active-grant lookup combines user filter with expiry check.
CREATE INDEX idx_user_permissions_user_expires ON user_permissions(user_id, expires_at);

COMMENT ON TABLE  user_permissions IS
    'Temporary direct permission grants — exceptions to the role model. Revocation = DELETE; history preserved in user_permissions_audit. Granter must hold the permission themselves (trg_prevent_privilege_escalation).';
COMMENT ON COLUMN user_permissions.expires_at IS
    'REQUIRED. Bounded by trg_validate_user_permission_expiry: min 5 min, max 90 days from now.';
COMMENT ON COLUMN user_permissions.conditions IS
    'ABAC conditions in the same vocabulary as role_permissions. Validated against permission_condition_templates in the app layer.';


-- ------------------------------------------------------------
-- USER PERMISSIONS AUDIT
-- ------------------------------------------------------------

CREATE TABLE user_permissions_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id             UUID  NOT NULL,
    permission_id       UUID  NOT NULL,
    conditions          JSONB,  -- state after the change; NULL on DELETE
    previous_conditions JSONB,  -- state before the change; NULL on INSERT

    change_type   audit_change_type_enum NOT NULL,
    changed_by    UUID        NOT NULL,
    changed_at    TIMESTAMPTZ DEFAULT NOW(),
    change_reason TEXT,  -- from SET LOCAL rbac.change_reason if set by the app

    CONSTRAINT fk_up_audit_user       FOREIGN KEY (user_id)       REFERENCES users(id)       ON DELETE RESTRICT,
    CONSTRAINT fk_up_audit_permission FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE RESTRICT,
    CONSTRAINT fk_up_audit_changed_by FOREIGN KEY (changed_by)    REFERENCES users(id)       ON DELETE RESTRICT
);

CREATE INDEX idx_up_audit_time_bucket ON user_permissions_audit(changed_at, user_id);
CREATE INDEX idx_up_audit_recent      ON user_permissions_audit(changed_at DESC);
CREATE INDEX idx_up_audit_changer     ON user_permissions_audit(changed_by, changed_at DESC);

COMMENT ON TABLE  user_permissions_audit IS
    'Immutable audit log for user_permissions — highest-risk RBAC table; every mutation is tracked unconditionally. Populated by trg_audit_user_permissions.';
COMMENT ON COLUMN user_permissions_audit.previous_conditions IS
    'Snapshot of conditions before the mutation. NULL on INSERT.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS user_permissions_audit         CASCADE;
DROP TABLE IF EXISTS user_permissions               CASCADE;
DROP TABLE IF EXISTS user_roles_audit               CASCADE;
DROP TABLE IF EXISTS user_roles                     CASCADE;
DROP TABLE IF EXISTS role_permissions_audit         CASCADE;
DROP TABLE IF EXISTS role_permissions               CASCADE;
DROP TABLE IF EXISTS permission_group_members       CASCADE;
DROP TABLE IF EXISTS permission_condition_templates CASCADE;
DROP TABLE IF EXISTS permission_groups              CASCADE;
DROP TABLE IF EXISTS permissions                    CASCADE;
DROP TABLE IF EXISTS roles                          CASCADE;

DROP TYPE IF EXISTS audit_change_type_enum CASCADE;

-- +goose StatementEnd
