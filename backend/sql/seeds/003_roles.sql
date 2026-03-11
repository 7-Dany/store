-- +goose Up
-- +goose StatementBegin

-- 003_roles.sql — Seed admin, vendor, and customer roles plus their
-- role_permissions assignments and permission_request_approvers rows.
--
-- All INSERTs use ON CONFLICT … DO NOTHING for idempotency.
--
-- The owner role and its permissions are NOT seeded here — the owner role is
-- inserted by 001_roles.sql and its permissions are assigned below alongside
-- the other system roles.

-- ── Application roles ─────────────────────────────────────────────────────────

ALTER TABLE role_permissions
    ALTER COLUMN granted_by DROP NOT NULL;

INSERT INTO roles (name, description, is_system_role, is_owner_role, is_active)
VALUES
    ('admin',    'Full store administration access. High-blast-radius operations require approval.',  TRUE, FALSE, TRUE),
    ('vendor',   'Vendor account. Manages own products and participates in request workflow.',         TRUE, FALSE, TRUE),
    ('customer', 'Standard customer account. Read access to own requests.',                           TRUE, FALSE, TRUE)
ON CONFLICT (name) DO NOTHING;

-- ── granted_by CTE helper ─────────────────────────────────────────────────────
-- All role_permissions inserts below use NULL for granted_by when no owner user exists.
-- role_permissions.granted_by is nullable (see migration 009_rbac_seed_grants.sql);
-- NULL means "system seed — no individual accountable" and is only valid at seed time.
-- Once an owner exists, the application enforces non-NULL granted_by via the service layer.

-- ── Owner — all 13 permissions ────────────────────────────────────────────────

WITH owner_user AS (
    SELECT ur.user_id
    FROM   user_roles ur
    JOIN   roles r ON r.id = ur.role_id
    WHERE  r.is_owner_role = TRUE
      AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
    LIMIT 1
),
owner_role AS (
    SELECT id FROM roles WHERE is_owner_role = TRUE AND is_system_role = TRUE LIMIT 1
)
INSERT INTO role_permissions (role_id, permission_id, granted_by, granted_reason, access_type, scope, conditions)
SELECT
    owner_role.id,
    p.id,
    (SELECT user_id FROM owner_user),
    'System seed — owner role has unrestricted access',
    'direct'::permission_access_type,
    'all'::permission_scope,
    '{}'::jsonb
FROM owner_role
CROSS JOIN permissions p
WHERE p.is_active = TRUE
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- ── Admin — all 13 permissions (with overrides) ───────────────────────────────

WITH owner_user AS (
    SELECT ur.user_id
    FROM   user_roles ur
    JOIN   roles r ON r.id = ur.role_id
    WHERE  r.is_owner_role = TRUE
      AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
    LIMIT 1
),
admin_role AS (
    SELECT id FROM roles WHERE name = 'admin' LIMIT 1
)
INSERT INTO role_permissions (role_id, permission_id, granted_by, granted_reason, access_type, scope, conditions)
SELECT
    admin_role.id,
    p.id,
    (SELECT user_id FROM owner_user),
    'System seed — admin role baseline access',
    -- Override access_type for specific permissions
    CASE p.canonical_name
        WHEN 'job_queue:configure' THEN 'request'::permission_access_type
        WHEN 'user:lock'           THEN 'request'::permission_access_type
        ELSE                            'direct'::permission_access_type
    END,
    'all'::permission_scope,
    '{}'::jsonb
FROM admin_role
CROSS JOIN permissions p
WHERE p.is_active = TRUE
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- ── Vendor — 3 permissions ────────────────────────────────────────────────────

WITH owner_user AS (
    SELECT ur.user_id
    FROM   user_roles ur
    JOIN   roles r ON r.id = ur.role_id
    WHERE  r.is_owner_role = TRUE
      AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
    LIMIT 1
),
vendor_role AS (
    SELECT id FROM roles WHERE name = 'vendor' LIMIT 1
)
INSERT INTO role_permissions (role_id, permission_id, granted_by, granted_reason, access_type, scope, conditions)
SELECT
    vendor_role.id,
    p.id,
    (SELECT user_id FROM owner_user),
    'System seed — vendor role access',
    v.access_type::permission_access_type,
    v.scope::permission_scope,
    v.conditions::jsonb
FROM vendor_role
CROSS JOIN (VALUES
    ('request:read',   'direct',      'own', '{}'),
    ('request:manage', 'direct',      'own', '{}'),
    ('product:manage', 'conditional', 'own', '{"max_price": 1000}')
) AS v(canonical_name, access_type, scope, conditions)
JOIN permissions p ON p.canonical_name = v.canonical_name AND p.is_active = TRUE
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- ── Customer — 1 permission ───────────────────────────────────────────────────

WITH owner_user AS (
    SELECT ur.user_id
    FROM   user_roles ur
    JOIN   roles r ON r.id = ur.role_id
    WHERE  r.is_owner_role = TRUE
      AND  (ur.expires_at IS NULL OR ur.expires_at > NOW())
    LIMIT 1
),
customer_role AS (
    SELECT id FROM roles WHERE name = 'customer' LIMIT 1
)
INSERT INTO role_permissions (role_id, permission_id, granted_by, granted_reason, access_type, scope, conditions)
SELECT
    customer_role.id,
    p.id,
    (SELECT user_id FROM owner_user),
    'System seed — customer role access',
    'direct'::permission_access_type,
    'own'::permission_scope,
    '{}'::jsonb
FROM customer_role
JOIN permissions p ON p.canonical_name = 'request:read' AND p.is_active = TRUE
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- ── permission_request_approvers ──────────────────────────────────────────────

INSERT INTO permission_request_approvers (permission_id, role_id, approval_level, min_required)
SELECT
    p.id,
    r.id,
    v.approval_level,
    v.min_required
FROM (VALUES
    ('job_queue:configure', 'owner', 0, 1),
    ('user:lock',           'owner', 0, 1)
) AS v(canonical_name, role_name, approval_level, min_required)
JOIN permissions p ON p.canonical_name = v.canonical_name
JOIN roles r       ON r.name           = v.role_name
ON CONFLICT (permission_id, role_id) DO NOTHING;

-- +goose StatementEnd


-- +goose Down
-- +goose StatementBegin

-- Step 1: Remove permission_request_approvers rows (audit trigger fires → inserts into pra_audit).
DELETE FROM permission_request_approvers
WHERE permission_id IN (
    SELECT id FROM permissions WHERE canonical_name IN ('job_queue:configure', 'user:lock')
);

-- Step 2: Remove role_permissions rows (audit trigger fires → inserts into rp_audit).
DELETE FROM role_permissions
WHERE role_id IN (
    SELECT id FROM roles WHERE name IN ('owner', 'admin', 'vendor', 'customer')
);

-- Step 3: Clean all audit tables whose RESTRICT FKs reference the roles we are about to delete.
-- These tables have ON DELETE RESTRICT on role_id, so they must be cleared before the DELETE on
-- roles. The audit rows for admin/vendor/customer were (re-)created by the trigger calls above,
-- plus any rows written by the test seed's user_role assignments.
-- Owner is not being deleted, so we scope cleanup to admin/vendor/customer only.
DELETE FROM role_permissions_audit
WHERE role_id IN (
    SELECT id FROM roles WHERE name IN ('admin', 'vendor', 'customer')
);

DELETE FROM permission_request_approvers_audit
WHERE role_id IN (
    SELECT id FROM roles WHERE name IN ('admin', 'vendor', 'customer')
);

DELETE FROM user_roles_audit
WHERE role_id IN (
    SELECT id FROM roles WHERE name IN ('admin', 'vendor', 'customer')
);

-- Step 4: Remove the application roles now that no RESTRICT FK rows reference them.
DELETE FROM roles
WHERE name IN ('admin', 'vendor', 'customer')
  AND is_system_role = TRUE;

ALTER TABLE role_permissions
     ALTER COLUMN granted_by SET NOT NULL;

-- +goose StatementEnd
