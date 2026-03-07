-- +goose Up
-- +goose StatementBegin

-- 001_roles.sql — Seed the built-in roles required before any route can
-- assign permissions or bootstrap the first owner account (§C-1).
--
-- The owner role is the only role inserted here. All other application roles
-- (admin, vendor, customer, etc.) are inserted by their own seed files as
-- the corresponding domain features are implemented.
--
-- Why ON CONFLICT DO NOTHING:
--   Idempotent against repeated `make seed` runs and CI resets.
--   If the row already exists (e.g. from a prior bootstrap), it is left
--   unchanged — name is the unique key.

INSERT INTO roles (
    name,
    description,
    is_system_role,
    is_owner_role,
    is_active
)
VALUES (
    'owner',
    'Unrestricted system access. Assigned exclusively via POST /owner/bootstrap. '
    'Cannot be deleted or soft-deactivated by end users (is_system_role = TRUE). '
    'chk_roles_owner_must_be_system enforces that is_owner_role implies is_system_role.',
    TRUE,   -- is_system_role: managed by ops, not end users
    TRUE,   -- is_owner_role: unrestricted access
    TRUE    -- is_active
)
ON CONFLICT (name) DO NOTHING;

-- +goose StatementEnd


-- +goose Down
-- +goose StatementBegin

-- Only removes the owner role if no user currently holds it.
-- If user_roles.role_id references this row, the DELETE will fail on the
-- ON DELETE RESTRICT FK — protecting audit history and preventing orphaned grants.
DELETE FROM roles
WHERE name = 'owner'
  AND is_owner_role = TRUE
  AND is_system_role = TRUE;

-- +goose StatementEnd
