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
