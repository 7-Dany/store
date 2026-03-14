/*
  Test-support queries for the rbac package.

  !! FOR TEST USE ONLY !!
  These queries expose mutations that production code must never perform directly.
  They are generated into the db package alongside production queries and restricted
  to integration test binaries via the //go:build integration_test tag added by the
  sqlc-generate make target.
*/


-- name: CreateActiveUnverifiedUserForTest :one
-- Inserts an active but email-unverified user, bypassing the OTP flow.
-- Sets is_active = TRUE and email_verified = FALSE directly.
-- Used by T-R21 to verify that bootstrap rejects users who have not yet
-- confirmed their email address.
WITH new_user AS (
    INSERT INTO users (email, display_name, is_active, email_verified)
    VALUES (@email, 'Test User', TRUE, FALSE)
    RETURNING id
),
secrets AS (
    INSERT INTO user_secrets (user_id, password_hash)
    SELECT id, @password_hash FROM new_user
)
SELECT id FROM new_user;

-- name: DeactivateAllPermissionsForTest :exec
-- Soft-deactivates every permission row inside the current transaction.
-- Used by TestGetPermissions_Empty to verify that GetPermissions returns
-- an allocated empty slice (not nil) when no active rows exist.
UPDATE permissions SET is_active = FALSE;

-- name: CreatePermissionGroupForTest :one
-- Inserts a bare permission group with no members, returning its id.
-- Used by TestGetPermissionGroups_ZeroMemberGroup to verify that a group
-- with no permission_group_members rows returns Members = [] (not nil).
INSERT INTO permission_groups (name, display_order)
VALUES (@name, 999)
RETURNING id;

-- name: GetLatestRolePermissionAuditEntry :one
-- Returns the most recent audit row for a given (role_id, permission_id, change_type).
-- Used by integration tests to assert that the correct actor was recorded by
-- fn_audit_role_permissions after a DELETE.
SELECT changed_by, changed_at, change_type
FROM   role_permissions_audit
WHERE  role_id       = @role_id::uuid
  AND  permission_id = @permission_id::uuid
  AND  change_type   = @change_type::audit_change_type_enum
ORDER  BY changed_at DESC
LIMIT  1;

-- name: CreateVerifiedActiveUserForTest :one
-- Inserts a fully active and email-verified user, bypassing the OTP and registration flow.
-- Returns the new user's UUID.
-- Used by owner integration tests that need a ready-to-use user for AssignOwnerTx
-- and ownership-transfer operations.
WITH new_user AS (
    INSERT INTO users (email, display_name, is_active, email_verified)
    VALUES (@email, 'Test User', TRUE, TRUE)
    RETURNING id
),
secrets AS (
    INSERT INTO user_secrets (user_id, password_hash)
    SELECT id, @password_hash FROM new_user
)
SELECT id FROM new_user;

-- name: GetUserRoleNameForTest :one
-- Returns the role name currently assigned to the given user (active assignment only).
-- Returns no-rows if the user has no role.
-- Used by owner integration tests to verify that AssignOwnerTx and AcceptTransferTx
-- set and clear the owner role on the correct users.
SELECT r.name
FROM   user_roles ur
JOIN   roles r ON r.id = ur.role_id
WHERE  ur.user_id = @user_id::uuid;

-- name: GetAuditLogEventCountForTest :one
-- Returns the count of auth_audit_log entries matching the given user_id and event_type.
-- Used by owner integration tests to verify that store methods write the expected
-- audit entries.
SELECT COUNT(*)
FROM   auth_audit_log
WHERE  user_id    = @user_id::uuid
  AND  event_type = @event_type::text;
