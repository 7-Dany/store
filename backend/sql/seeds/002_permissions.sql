-- +goose Up
-- +goose StatementBegin

-- 002_permissions.sql — Seed all application permissions, permission groups,
-- and group membership required by the RBAC system.
--
-- canonical_name is a GENERATED column (resource_type || ':' || name) — do not supply it.
-- All INSERTs use ON CONFLICT … DO NOTHING for idempotency against repeated `make seed` runs.

-- ── Permissions ───────────────────────────────────────────────────────────────

INSERT INTO permissions (name, resource_type, description) VALUES
    ('read',                'rbac',       'List roles, permissions, user assignments, and audit logs'),
    ('manage',              'rbac',       'Create/update/soft-delete roles; add/remove role permissions; assign/remove user roles'),
    ('grant_user_permission','rbac',      'Grant/revoke time-limited direct permissions on individual users'),
    ('read',                'job_queue',  'View jobs, workers, queues, schedules, stats, metrics, and WS stream'),
    ('manage',              'job_queue',  'Cancel jobs, retry dead/failed jobs, update job priority, purge dead jobs'),
    ('configure',           'job_queue',  'Pause/resume job kinds, force-drain workers, create/update/delete/trigger schedules'),
    ('read',                'user',       'List users, view profiles, view audit and login history'),
    ('manage',              'user',       'Edit user details (email, name, etc.)'),
    ('lock',                'user',       'Admin-lock and admin-unlock a user account (admin_locked field)'),
    ('read',                'request',    'View requests and their history and status'),
    ('manage',              'request',    'Create/edit/cancel requests; manage lifecycle non-approval steps'),
    ('approve',             'request',    'Approve or reject a pending request'),
    ('manage',              'product',    'Create/update/delete products (placeholder for store domain)')
ON CONFLICT (resource_type, name) DO NOTHING;

-- ── Permission groups ─────────────────────────────────────────────────────────

INSERT INTO permission_groups (name, display_label, icon, color_hex, display_order, is_visible, is_active)
VALUES
    ('system_administration', 'System Administration', 'shield',  '#6366f1', 0, TRUE, TRUE),
    ('job_queue',             'Job Queue',             'queue',   '#f59e0b', 1, TRUE, TRUE),
    ('users',                 'Users',                 'users',   '#10b981', 2, TRUE, TRUE),
    ('requests',              'Requests',              'inbox',   '#3b82f6', 3, TRUE, TRUE),
    ('products',              'Products',              'package', '#8b5cf6', 4, TRUE, TRUE)
ON CONFLICT (name) DO NOTHING;

-- ── Permission group members ──────────────────────────────────────────────────

-- system_administration: rbac:read, rbac:manage, rbac:grant_user_permission
WITH grp AS (
    SELECT id FROM permission_groups WHERE name = 'system_administration'
),
perms AS (
    SELECT id, canonical_name FROM permissions
    WHERE canonical_name IN ('rbac:read', 'rbac:manage', 'rbac:grant_user_permission')
)
INSERT INTO permission_group_members (group_id, permission_id)
SELECT grp.id, perms.id FROM grp CROSS JOIN perms
ON CONFLICT (group_id, permission_id) DO NOTHING;

-- job_queue: job_queue:read, job_queue:manage, job_queue:configure
WITH grp AS (
    SELECT id FROM permission_groups WHERE name = 'job_queue'
),
perms AS (
    SELECT id FROM permissions
    WHERE canonical_name IN ('job_queue:read', 'job_queue:manage', 'job_queue:configure')
)
INSERT INTO permission_group_members (group_id, permission_id)
SELECT grp.id, perms.id FROM grp CROSS JOIN perms
ON CONFLICT (group_id, permission_id) DO NOTHING;

-- users: user:read, user:manage, user:lock
WITH grp AS (
    SELECT id FROM permission_groups WHERE name = 'users'
),
perms AS (
    SELECT id FROM permissions
    WHERE canonical_name IN ('user:read', 'user:manage', 'user:lock')
)
INSERT INTO permission_group_members (group_id, permission_id)
SELECT grp.id, perms.id FROM grp CROSS JOIN perms
ON CONFLICT (group_id, permission_id) DO NOTHING;

-- requests: request:read, request:manage, request:approve
WITH grp AS (
    SELECT id FROM permission_groups WHERE name = 'requests'
),
perms AS (
    SELECT id FROM permissions
    WHERE canonical_name IN ('request:read', 'request:manage', 'request:approve')
)
INSERT INTO permission_group_members (group_id, permission_id)
SELECT grp.id, perms.id FROM grp CROSS JOIN perms
ON CONFLICT (group_id, permission_id) DO NOTHING;

-- products: product:manage
WITH grp AS (
    SELECT id FROM permission_groups WHERE name = 'products'
),
perms AS (
    SELECT id FROM permissions
    WHERE canonical_name = 'product:manage'
)
INSERT INTO permission_group_members (group_id, permission_id)
SELECT grp.id, perms.id FROM grp CROSS JOIN perms
ON CONFLICT (group_id, permission_id) DO NOTHING;

-- +goose StatementEnd


-- +goose Down
-- +goose StatementBegin

-- Remove group members first (FK), then groups, then permissions.
DELETE FROM permission_group_members
WHERE group_id IN (
    SELECT id FROM permission_groups
    WHERE name IN ('system_administration', 'job_queue', 'users', 'requests', 'products')
);

DELETE FROM permission_groups
WHERE name IN ('system_administration', 'job_queue', 'users', 'requests', 'products');

DELETE FROM permissions
WHERE (resource_type, name) IN (
    ('rbac',      'read'),
    ('rbac',      'manage'),
    ('rbac',      'grant_user_permission'),
    ('job_queue', 'read'),
    ('job_queue', 'manage'),
    ('job_queue', 'configure'),
    ('user',      'read'),
    ('user',      'manage'),
    ('user',      'lock'),
    ('request',   'read'),
    ('request',   'manage'),
    ('request',   'approve'),
    ('product',   'manage')
);

-- +goose StatementEnd
