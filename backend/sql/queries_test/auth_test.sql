/*
  Test-support queries for the auth package.

  !! FOR TEST USE ONLY !!
  These queries expose mutations that production code must never perform directly:
  back-dating timestamps, pinning attempt counters, force-deleting users, and
  coercing account states. They are generated into the db package alongside
  production queries and restricted to integration test binaries via the
  //go:build integration_test tag added by the sqlc-generate make target.
*/


-- name: DeleteOTPTokenByID :exec
-- Permanently removes a one_time_tokens row by primary key.
-- Used in tests that commit token rows outside a rolled-back transaction
-- (e.g. IncrementAttemptsTx independence tests) to clean up via t.Cleanup
-- without cascading through user deletion.
DELETE FROM one_time_tokens
WHERE id = @id::uuid;


-- name: DeleteUserByEmail :exec
-- Permanently deletes a user row by email address.
-- Used in tests that commit data to the DB (e.g. IncrementAttemptsTx independence
-- tests that require a real INSERT) and then clean up via t.Cleanup.
-- @email::text produces a string parameter instead of pgtype.Text, matching
-- every other email parameter in the test suite.
DELETE FROM users
WHERE email = @email::text;


-- name: BackdateTokenCreatedAt :exec
-- Moves created_at backwards past the resend cooldown window (2 minutes).
-- Shifts only created_at, not expires_at, so the token remains valid for consumption.
--
-- Constraint check:
--   chk_ott_expires_future (expires_at > created_at):
--     expires_at stays at NOW() + 15 min; created_at moves to NOW() - 3 min → holds.
--   chk_ott_ev_ttl_max (expires_at <= created_at + 30 min):
--     (NOW() + 15 min) <= (NOW() - 3 min + 30 min) = NOW() + 27 min → holds.
UPDATE one_time_tokens
SET created_at = NOW() - INTERVAL '3 minutes'
WHERE email      = $1
  AND token_type = 'email_verification'
  AND used_at    IS NULL;


-- name: ExpireVerificationToken :exec
-- Back-dates a token so the service sees it as already expired (expires_at < NOW()).
-- Sets created_at 10 minutes in the past and expires_at 1 second in the past.
--
-- Constraint check:
--   chk_ott_expires_future (expires_at > created_at):
--     (NOW() - 1 s) > (NOW() - 10 min) → holds.
--   chk_ott_ev_ttl_max (expires_at <= created_at + 30 min):
--     (NOW() - 1 s) <= (NOW() - 10 min + 30 min) = NOW() + 20 min → holds.
UPDATE one_time_tokens
SET created_at = NOW() - INTERVAL '10 minutes',
    expires_at = NOW() - INTERVAL '1 second'
WHERE email      = $1
  AND token_type = 'email_verification'
  AND used_at    IS NULL;


-- name: PinTokenAttemptsToMax :exec
-- Sets attempts = max_attempts for the active email_verification token.
-- Used to exercise the brute-force-ceiling guard (Guard 2) in VerifyEmail without
-- actually submitting max_attempts wrong codes, which would be slow.
UPDATE one_time_tokens
SET attempts = max_attempts
WHERE email      = $1
  AND token_type = 'email_verification'
  AND used_at    IS NULL;


-- name: LockUserForTest :exec
-- Locks an account by setting is_locked = TRUE (OTP-brute-force path) and
-- is_active = FALSE for test isolation purposes.
-- is_active = FALSE keeps the account in a consistent pre-verification-like state
-- for tests that verify the guard order: is_locked is checked BEFORE is_active
-- in the login service, so ErrAccountLocked fires regardless of is_active.
-- Note: chk_users_not_active_and_locked was removed in favour of separate
-- is_locked (OTP path) and admin_locked (RBAC path) columns.
-- @email::text produces a string parameter instead of pgtype.Text.
-- Production lock logic lives in LockAccount (OTP brute-force path).
UPDATE users
SET is_locked = TRUE,
    is_active = FALSE
WHERE email = @email::text;


-- name: SuspendUserForTest :exec
-- Suspends an active account by setting is_active = FALSE while leaving is_locked = FALSE.
-- The account remains email_verified = TRUE so the is_locked and email_verified guards
-- pass, and the is_active guard fires (ErrAccountInactive path in service.Login).
-- @email::text produces a string parameter instead of pgtype.Text.
UPDATE users
SET is_active = FALSE
WHERE email = @email::text;


-- name: CreateVerifiedUserWithUsername :one
-- Inserts a fully-verified, active user with both email and username set.
-- Sets is_active = TRUE and email_verified = TRUE directly, bypassing the OTP flow.
-- Used in TestLogin_SuccessByUsername to exercise the username login path without
-- a real registration + verification cycle.
-- display_name is pinned to 'Test User' — irrelevant to the login path under test.
INSERT INTO users (
    email,
    username,
    display_name,
    password_hash,
    is_active,
    email_verified
)
VALUES (
    @email,
    @username,
    'Test User',
    @password_hash,
    TRUE,
    TRUE
)
RETURNING id;


-- name: CountAuditEventsByUser :one
-- Returns the count of auth_audit_log rows matching a (user_id, event_type) pair.
-- Used in store tests to assert that CreateUserTx and LoginTx write the expected
-- audit rows without resorting to raw tx.QueryRow calls.
SELECT COUNT(*)::int AS count
FROM auth_audit_log
WHERE user_id    = @user_id::uuid
  AND event_type = @event_type;


-- name: GetUserLastLoginAt :one
-- Returns last_login_at for the given user UUID.
-- Used in TestLoginTx_LastLoginAtIsStamped to verify that LoginTx stamps the
-- column without resorting to a raw tx.QueryRow call.
SELECT last_login_at
FROM users
WHERE id = @user_id::uuid;


-- name: GetLatestRefreshTokenByUser :one
-- Returns jti, revoked_at and revoke_reason for the most recently created
-- refresh token owned by the user.
-- Used in TestRevokeAllUserTokens to assert that RevokeAllUserTokens stamps
-- revoked_at without resorting to raw SQL.
SELECT jti, revoked_at, revoke_reason
FROM refresh_tokens
WHERE user_id = @user_id::uuid
ORDER BY created_at DESC
LIMIT 1;


-- name: GetLatestSessionByUser :one
-- Returns id and ended_at for the most recently started session owned by the user.
-- Used in TestRevokeAllUserTokens to assert that RevokeAllUserTokens stamps
-- ended_at without resorting to raw SQL.
SELECT id, ended_at
FROM user_sessions
WHERE user_id = @user_id::uuid
ORDER BY started_at DESC
LIMIT 1;


-- name: GetRefreshTokenExpiry :one
-- Returns expires_at for a refresh token by jti.
-- Used in rotation tests to verify that the new token has a fresh 30-day TTL
-- rather than inheriting the remaining TTL of the rotated parent.
SELECT expires_at
FROM refresh_tokens
WHERE jti = @jti::uuid;


-- name: GetTokenAttempts :one
-- Returns the current attempts counter for a one-time token.
-- Used in tests to verify that IncrementAttemptsTx actually incremented the counter.
SELECT attempts
FROM one_time_tokens
WHERE id = @id::uuid;


-- name: GetUserIsLocked :one
-- Returns the is_locked flag for a user.
-- Used in tests to assert that account locking logic fired correctly.
SELECT is_locked
FROM users
WHERE id = @id::uuid;


-- name: AdminLockUserForTest :exec
-- Locks an account by setting admin_locked = TRUE for test isolation.
-- Mirrors LockUserForTest but exercises the admin-lock code path. Production
-- admin-lock logic is handled by the RBAC admin flow; this helper exists only
-- to drive the admin_locked guard in store integration tests.
-- @email::text produces a string parameter instead of pgtype.Text.
UPDATE users
SET admin_locked = TRUE
WHERE email = @email::text;


-- name: CountOpenSessionsByUser :one
-- Returns the number of open sessions (ended_at IS NULL) for a user.
-- Used in tests to verify that UpdatePasswordHashTx ends all sessions.
SELECT COUNT(*)::int AS count
FROM user_sessions
WHERE user_id = @user_id::uuid
  AND ended_at IS NULL;


-- name: CountActiveRefreshTokensByUser :one
-- Returns the number of active (non-revoked, non-expired) refresh tokens for a user.
-- Used in tests to verify that UpdatePasswordHashTx revokes all tokens.
SELECT COUNT(*)::int AS count
FROM refresh_tokens
WHERE user_id = @user_id::uuid
  AND revoked_at IS NULL
  AND expires_at > NOW();


-- name: CountActiveRefreshTokensBySession :one
-- Returns the number of active (non-revoked, non-expired) refresh tokens for a session.
-- Used in profile tests to verify that RevokeSessionTx revokes all session tokens.
SELECT COUNT(*)::int AS count
FROM refresh_tokens
WHERE session_id = @session_id::uuid
  AND revoked_at IS NULL
  AND expires_at > NOW();


-- name: SetAvatarURLForTest :exec
-- Sets avatar_url to a given value for test isolation.
-- Used to assert that GetUserProfile correctly populates AvatarURL when non-NULL.
-- @avatar_url::text accepts a plain string; @user_id::uuid accepts pgtype.UUID.
UPDATE users
SET avatar_url = @avatar_url::text
WHERE id = @user_id::uuid;


-- name: GetAuditEventsByUser :many
-- Returns all event_type values from auth_audit_log for a given user.
-- Used in register tests to verify that the correct audit event was written.
SELECT event_type
FROM auth_audit_log
WHERE user_id = @user_id::uuid
ORDER BY created_at DESC;


-- name: ExpirePasswordResetToken :exec
-- Back-dates a password_reset token so the service sees it as already expired (expires_at < NOW()).
-- Sets created_at 10 minutes in the past and expires_at 1 second in the past.
--
-- Constraint check:
--   chk_ott_expires_future (expires_at > created_at):
--     (NOW() - 1 s) > (NOW() - 10 min) → holds.
UPDATE one_time_tokens
SET created_at = NOW() - INTERVAL '10 minutes',
    expires_at = NOW() - INTERVAL '1 second'
WHERE email      = $1
  AND token_type = 'password_reset'
  AND used_at    IS NULL;
