/*
  Test-support queries for the oauth package.

  !! FOR TEST USE ONLY !!
  These queries expose read-only assertions that production code must never
  perform directly. They are generated into the db package alongside production
  queries and restricted to integration test binaries via the
  //go:build integration_test tag added by the sqlc-generate make target.
*/


-- name: TestGetTelegramIdentityDetails :one
-- Returns display_name and avatar_url for a user's Telegram identity row.
-- Used in T-S08 to verify InsertUserIdentity wrote the correct values.
SELECT display_name, avatar_url
FROM user_identities
WHERE user_id  = @user_id::uuid
  AND provider = 'telegram';


-- name: TestCountTelegramIdentities :one
-- Returns the number of telegram identity rows for a user.
-- Used in T-S09 to verify DeleteUserIdentity removed the row.
SELECT COUNT(*) AS count
FROM user_identities
WHERE user_id  = @user_id::uuid
  AND provider = 'telegram';


-- name: TestCountUserSessions :one
-- Returns the total number of session rows for a user (open or closed).
-- Used in T-S11 to verify OAuthLoginTx created a session row.
SELECT COUNT(*) AS count
FROM user_sessions
WHERE user_id = @user_id::uuid;


-- name: TestGetUserFlags :one
-- Returns the boolean and nullable columns that OAuth registration must set correctly.
-- Used in T-S12 to assert new Telegram users have:
--   email_verified = TRUE, is_active = TRUE, password_hash = NULL, email = NULL/empty.
SELECT email_verified, password_hash, is_active, email
FROM users
WHERE id = @user_id::uuid;


-- name: TestGetTelegramIdentityProviderDetails :one
-- Returns the provider-specific fields for a user's Telegram identity row.
-- Used in T-S13 to verify that provider_uid is set and access_token /
-- provider_email are empty (Telegram does not use them — D-04).
SELECT provider_uid, access_token, provider_email
FROM user_identities
WHERE user_id  = @user_id::uuid
  AND provider = 'telegram';


-- name: TestGetGoogleIdentityDisplayName :one
-- Returns display_name for a user's Google identity row.
-- Used in T-37 to verify UpsertUserIdentity updated the correct column.
SELECT display_name
FROM user_identities
WHERE user_id  = @user_id::uuid
  AND provider = 'google';


-- name: TestCountGoogleIdentities :one
-- Returns the number of Google identity rows for a user.
-- Used in T-37 and T-51 to assert no duplicate rows exist and rows are deleted.
SELECT COUNT(*) AS count
FROM user_identities
WHERE user_id  = @user_id::uuid
  AND provider = 'google';


-- name: TestGetIdentityUserIDByProviderUID :one
-- Returns the user_id linked to a given provider_uid.
-- Used in T-38 to verify the identity was linked to the correct seeded user.
SELECT user_id
FROM user_identities
WHERE provider_uid = @provider_uid;


-- name: TestGetLatestAuditLogByUser :one
-- Returns event_type and provider (as text) for the most recent audit_log row
-- matching the given user_id and event_type.
-- Used in T-S15 to verify InsertAuditLogTx wrote the correct row.
-- provider is cast to text so the test can compare against a plain string.
SELECT event_type, provider::text AS provider
FROM auth_audit_log
WHERE user_id    = @user_id::uuid
  AND event_type = @event_type
ORDER BY created_at DESC
LIMIT 1;
