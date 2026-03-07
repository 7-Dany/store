/* ============================================================
   sql/queries/auth/auth.sql
   Consolidated auth queries for sqlc code generation.
   One file per domain; grouped by auth flow top-to-bottom.

   Sections:
     Registration
     Email verification
     Resend verification
     Login
     Login lockout & account unlock
     Refresh token lifecycle
     Sessions
     Mass revocation
     Forgot / reset password
     Change password
     Profile
   ============================================================ */


/* ── Registration ──────────────────────────────────────────────────────────── */

/*
  Signup flow — three statements, one transaction.

  Caller responsibilities before invoking:
    1. bcrypt(password, cost>=12)          → $password_hash
    2. generateCodeHash()                  → raw_code, code_hash
       code_hash format: bcrypt hash produced by golang.org/x/crypto/bcrypt.
       The salt$sha256 format is obsolete — never generate new tokens with it.
    3. Send raw_code in the verification email body — never store it.

  On 23505 (unique_violation): inspect constraint name:
    idx_users_email → email already registered
*/

-- name: CreateUser :one
INSERT INTO users (
    email,
    display_name,
    password_hash,
    username,
    is_active,
    email_verified
)
VALUES (
    @email,
    @display_name,
    @password_hash,
    sqlc.narg('username'),
    FALSE,
    FALSE
)
RETURNING
    id,
    email,
    display_name,
    is_active,
    email_verified,
    created_at;


-- name: InvalidateAllUserTokens :exec
-- Voids all unused email_verification tokens for this user.
-- Scoped to token_type = 'email_verification' so that in-flight password-reset
-- or magic-link tokens are not silently nuked on a re-registration attempt.
-- Called inside the same transaction as CreateEmailVerificationToken.
UPDATE one_time_tokens
SET used_at = NOW()
WHERE user_id    = @user_id::uuid
  AND token_type = 'email_verification'
  AND used_at    IS NULL;


-- name: CreateEmailVerificationToken :one
-- Issues a new email_verification OTP token with a caller-controlled TTL and max 3 attempts.
-- 3 attempts × 1-in-1,000,000 chance per attempt = 0.0003% brute-force success rate per token.
--
-- TTL is passed as @ttl_seconds (float8) so PostgreSQL computes expires_at = NOW() + ttl,
-- keeping both timestamps on the same clock and preventing chk_ott_ev_ttl_max violations
-- caused by application/DB clock skew.
-- The DB constraint chk_ott_ev_ttl_max caps the TTL at 15 minutes (see 001_core.sql).
-- The authoritative TTL value lives in config.Config.OTPValidMinutes (env: OTP_VALID_MINUTES).
INSERT INTO one_time_tokens (
    token_type,
    user_id,
    email,
    code_hash,
    expires_at,
    ip_address,
    max_attempts
)
VALUES (
    'email_verification',
    @user_id::uuid,
    @email,
    @code_hash,
    NOW() + make_interval(secs => @ttl_seconds::float8),
    sqlc.narg('ip_address')::inet,
    3
)
RETURNING
    id,
    expires_at;


-- name: InsertAuditLog :exec
-- provider is typed as non-nullable auth_provider even though the DB column allows NULL.
-- Every event in the auth domain always has a provider context (the user authenticated
-- via email, google, etc.), so the non-nullable type produces a cleaner Go API without
-- requiring pgtype.NullAuthProvider wrapping at every call site. If a future domain
-- needs to log events without a provider, add a separate InsertAuditLogNoProvider query.
INSERT INTO auth_audit_log (
    user_id,
    event_type,
    provider,
    ip_address,
    user_agent,
    metadata
)
VALUES (
    @user_id::uuid,
    @event_type,
    @provider::auth_provider,
    sqlc.narg('ip_address')::inet,
    @user_agent,
    @metadata
);


/* ── Email verification ─────────────────────────────────────────────────────── */

/*
  Email verification flow — OTP path.

  Caller responsibilities:
    1. Receive 6-digit code from user.
    2. Call GetEmailVerificationToken(email) inside a transaction using
       SELECT FOR UPDATE to prevent concurrent double-use.
    3. Validate expiry and attempts in the application layer.
       used_at IS NULL is enforced here; ConsumeEmailVerificationToken rowsAffected==0
       is the concurrency guard.
    4. Recompute hash: recomputeHash(presentedCode, token.CodeHash.String)
    5. On valid code: ConsumeEmailVerificationToken + RevokePreVerificationTokens
       + MarkEmailVerified + InsertAuditLog — all in the same transaction.
    6. On invalid code: IncrementVerificationAttempts in a separate transaction.
*/

-- name: GetEmailVerificationToken :one
-- Looks up by email only — the client sends email + OTP code, no user_id required.
-- ORDER BY created_at DESC, id DESC picks the most recent valid token.
-- FOR UPDATE prevents concurrent double-use (two simultaneous correct submissions).
SELECT
    id,
    user_id,
    email,
    code_hash,
    attempts,
    max_attempts,
    expires_at,
    used_at
FROM one_time_tokens
WHERE email      = @email
  AND token_type = 'email_verification'
  AND code_hash  IS NOT NULL
  AND used_at    IS NULL
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;


-- name: ConsumeEmailVerificationToken :execrows
-- Marks the token as used. The AND used_at IS NULL guard ensures idempotency:
-- a race between two concurrent correct submissions cannot consume the same token twice.
UPDATE one_time_tokens
SET used_at = NOW()
WHERE id      = @id::uuid
  AND used_at IS NULL;


-- name: IncrementVerificationAttempts :one
-- AND attempts < max_attempts prevents incrementing past the brute-force ceiling.
-- Returns the post-increment attempts value so the caller can compare it to
-- max_attempts without relying on the stale caller-supplied count (TOCTOU fix).
-- Returns pgx.ErrNoRows when the token is already at max_attempts (no row updated);
-- callers must treat ErrNoRows as "already at ceiling" and proceed to lock logic.
UPDATE one_time_tokens
SET attempts        = attempts + 1,
    last_attempt_at = NOW()
WHERE id       = @id::uuid
  AND attempts < max_attempts
RETURNING attempts;


-- name: MarkEmailVerified :execrows
-- Activates the account and marks email_verified = TRUE in one statement.
-- Guards:
--   email_verified = FALSE → prevents double-activation (idempotency guard)
--   is_locked      = FALSE → blocks an OTP-brute-force-locked account from being
--                            re-activated via the verification path.
--   admin_locked   = FALSE → blocks an admin-locked account from verifying;
--                            only admin action can clear admin_locked.
-- Returns rows affected so callers can detect a no-op and investigate the cause
-- with GetUserVerifiedAndLocked.
UPDATE users
SET email_verified = TRUE,
    is_active      = TRUE
WHERE id             = @user_id::uuid
  AND email_verified = FALSE
  AND is_locked      = FALSE
  AND admin_locked   = FALSE;


-- name: RevokePreVerificationTokens :exec
-- Revokes all non-revoked refresh tokens issued before email verification completes.
-- Pre-verification tokens must never create authenticated sessions — they were issued
-- during the registration window before the user proved ownership of the email address.
UPDATE refresh_tokens
SET revoked_at    = NOW(),
    revoke_reason = 'pre_verification'
WHERE user_id    = @user_id::uuid
  AND revoked_at IS NULL;


-- name: GetUserVerifiedAndLocked :one
-- Returns email_verified, is_locked, admin_locked, and is_active in a single round-trip.
-- Called when MarkEmailVerified returns 0 rows to distinguish:
--   is_locked = TRUE or admin_locked = TRUE → ErrAccountLocked
--   email_verified = TRUE                   → already verified (no-op, not an error)
-- Avoids a second query race window compared to checking each column separately.
SELECT email_verified, is_locked, admin_locked, is_active
FROM users
WHERE id = @user_id::uuid
LIMIT 1;


-- name: GetUserEmailVerified :one
-- Returns email_verified for a user looked up by email.
-- NOTE: called by store tests only — not used in production store code.
-- Used to assert that VerifyEmailTx actually flipped the flag without
-- resorting to raw tx.QueryRow calls in tests.
SELECT email_verified
FROM users
WHERE email = @email
LIMIT 1;


-- name: LockAccount :execrows
-- Sets is_locked = TRUE after max OTP attempts are exhausted.
-- Does NOT touch is_active: an unverified account (is_active=FALSE) stays inactive;
-- a verified account (is_active=TRUE) stays active — in both cases the auth path
-- sees is_locked=TRUE and rejects the request.
-- AND is_locked = FALSE makes the operation idempotent. Rowcount tells the caller
-- whether the lock actually changed state. Clearing requires admin action or the
-- account-unlock OTP flow.
UPDATE users
SET is_locked = TRUE
WHERE id        = @user_id::uuid
  AND is_locked = FALSE;


/* ── Resend verification ────────────────────────────────────────────────────── */

/*
  Resend email verification flow.

  Caller responsibilities:
    1. generateCodeHash() → raw_code, code_hash
    2. Send raw_code in the verification email body — never store it.

  Anti-enumeration: always returns the same 202 body regardless of whether
  the email exists, is already verified, or is locked.

  Rate-limiting is enforced at the HTTP layer.
*/

-- name: GetUserForResend :one
-- Fetches the minimal fields needed to decide whether a resend is valid.
-- Returns the row regardless of is_locked / email_verified so the handler can
-- respond uniformly (anti-enumeration) while still making the right decision internally.
-- is_active is intentionally excluded — a brand-new unverified account has is_active=FALSE
-- and must still receive a resend. Only is_locked=TRUE accounts (brute-force lockout)
-- and already-verified accounts are suppressed.
SELECT
    id,
    is_locked,
    admin_locked,
    email_verified
FROM users
WHERE email = @email
LIMIT 1;


-- name: GetLatestVerificationTokenCreatedAt :one
-- Returns created_at of the most recent unused email_verification token for this user.
-- Used to enforce the resend cooldown window in the application layer.
-- Index note (F11): idx_ott_active covers the filter (user_id, token_type, used_at IS NULL)
-- but not the ORDER BY created_at DESC; Postgres sorts a tiny in-memory result set.
-- For the cooldown check the result set is at most a handful of rows, which is acceptable.
SELECT created_at
FROM one_time_tokens
WHERE user_id    = @user_id::uuid
  AND token_type = 'email_verification'
  AND used_at    IS NULL
ORDER BY created_at DESC
LIMIT 1;


/* ── Login ──────────────────────────────────────────────────────────────────── */

/*
  Login flow — composed from the queries below by LoginTx in store.go.

  Caller responsibilities:
    1. Call GetUserForLogin(identifier) — may return pgx.ErrNoRows (unknown email/username).
    2. Run bcrypt.CompareHashAndPassword REGARDLESS of whether the user was found.
       Always compare even on no-rows to equalise timing (use getDummyPasswordHash()).
    3. After password check passes, verify flags in this order:
         is_locked          → ErrAccountLocked
         !email_verified    → ErrEmailNotVerified
         !is_active         → ErrAccountInactive
         login_locked_until → ErrLoginLocked (time-limited lockout)
    4. CreateUserSession → session_id
    5. CreateRefreshToken → jti, family_id, expires_at
    6. UpdateLastLoginAt
    7. ResetLoginFailures (clears time-limited lockout counter)
    8. InsertAuditLog (event_type = "login")
    9. Generate access JWT (user_id, session_id) in the handler layer, not here.

  Steps 4–8 run inside a single transaction (LoginTx in store.go).
*/

-- name: GetUserForLogin :one
-- Fetches the fields needed to authenticate a login by either email or username.
-- The caller passes the raw identifier; the query matches against whichever column equals it.
-- Only one can match at a time because idx_users_email and idx_users_username are each unique.
--
-- password_hash IS NOT NULL filters out OAuth-only accounts that have no bcrypt path —
-- they must authenticate via their identity provider.
--
-- Returns pgx.ErrNoRows when no match; caller must still run a dummy bcrypt compare
-- before surfacing ErrInvalidCredentials to equalise response timing.
--
-- Index usage:
--   email    branch → idx_users_email    (partial unique on email WHERE NOT NULL)
--   username branch → idx_users_username (partial unique on username WHERE NOT NULL)
-- Postgres evaluates both OR branches and uses whichever index is available.
SELECT
    id,
    email,
    username,
    password_hash,
    is_active,
    email_verified,
    is_locked,
    admin_locked,
    login_locked_until
FROM users
WHERE (email = @identifier OR username = @identifier)
  AND password_hash IS NOT NULL
LIMIT 1;


-- name: CreateUserSession :one
-- Opens a new login session row. The returned id is embedded in JWT claims and
-- stored on the refresh_token row so tokens can be tied back to a specific device session.
INSERT INTO user_sessions (
    user_id,
    auth_provider,
    ip_address,
    user_agent
)
VALUES (
    @user_id::uuid,
    @auth_provider::auth_provider,
    sqlc.narg('ip_address')::inet,
    @user_agent
)
RETURNING id, started_at;


-- name: CreateRefreshToken :one
-- Issues a new root refresh token (no parent_jti) with a 30-day TTL.
-- family_id is generated by the DB DEFAULT (gen_random_uuid()) so each fresh
-- login starts an independent token family with no shared revocation surface.
-- The caller embeds jti in a signed JWT — never expose the raw UUID directly.
WITH cfg AS (
    SELECT INTERVAL '30 days' AS refresh_ttl
)
INSERT INTO refresh_tokens (
    user_id,
    session_id,
    expires_at
)
SELECT
    @user_id::uuid,
    @session_id::uuid,
    NOW() + refresh_ttl
FROM cfg
RETURNING
    jti,
    family_id,
    expires_at,
    created_at;


-- name: UpdateLastLoginAt :exec
-- Stamps last_login_at after a successful authentication.
-- updated_at is intentionally omitted: trg_users_updated_at (BEFORE UPDATE trigger)
-- already sets updated_at = NOW() on every UPDATE, making an explicit assignment a
-- dead no-op that wastes a column write.
-- Called inside the same transaction as CreateUserSession so a rolled-back login
-- does not leave a stale last_login_at.
UPDATE users
SET last_login_at = NOW()
WHERE id = @user_id::uuid;


/* ── Login lockout & account unlock ─────────────────────────────────────────── */

/*
  Login-lockout and account-unlock flow.

  AUTH-1: Time-limited login lockout — after 10 consecutive wrong passwords,
  login_locked_until is set 15 minutes into the future. This is separate from
  is_locked (permanent OTP-brute-force lockout).

  STATE-1: Self-service account unlock via OTP emailed to the user. Used when
  is_locked = TRUE to allow the account owner to re-authenticate.
*/

-- name: IncrementLoginFailures :one
-- Increments failed_login_attempts and sets login_locked_until to 15 minutes
-- in the future when the threshold (10) is reached.
-- Returns the updated counter and the new (possibly null) lock timestamp so the
-- caller can decide whether to emit a login_lockout audit row.
UPDATE users
SET failed_login_attempts = failed_login_attempts + 1,
    login_locked_until = CASE
        WHEN failed_login_attempts + 1 >= 10
        THEN NOW() + INTERVAL '15 minutes'
        ELSE login_locked_until
    END
WHERE id = @user_id::uuid
RETURNING failed_login_attempts, login_locked_until;


-- name: ResetLoginFailures :exec
-- Clears the failed-attempt counter and removes any time-based login lock.
-- Called inside LoginTx after a successful authentication so the next wrong password
-- starts a fresh count from zero.
UPDATE users
SET failed_login_attempts = 0,
    login_locked_until    = NULL
WHERE id = @user_id::uuid;


-- name: CreateUnlockToken :one
-- Issues a new account_unlock OTP token. TTL is caller-controlled via @ttl_seconds.
-- expires_at is computed as NOW() + make_interval(secs => ttl_seconds) so both
-- created_at and expires_at are on the same PostgreSQL clock, preventing
-- chk_ott_au_ttl_max violations caused by application/DB clock skew.
-- No token_hash needed — account_unlock tokens use the OTP code path exclusively.
INSERT INTO one_time_tokens (token_type, user_id, email, code_hash,
    expires_at, ip_address, max_attempts)
VALUES (
    'account_unlock',
    @user_id::uuid,
    @email,
    @code_hash,
    NOW() + make_interval(secs => @ttl_seconds::float8),
    sqlc.narg('ip_address')::inet,
    3
)
RETURNING id, expires_at;


-- name: GetUnlockToken :one
-- Fetches the active account_unlock OTP for the given email.
-- No is_locked guard — the token exists precisely because the account is locked.
-- ORDER BY created_at DESC, id DESC picks the most recently issued token.
-- FOR UPDATE prevents concurrent double-consumption.
SELECT
    id,
    user_id,
    email,
    code_hash,
    attempts,
    max_attempts,
    expires_at
FROM one_time_tokens
WHERE email      = @email
  AND token_type = 'account_unlock'
  AND code_hash  IS NOT NULL
  AND used_at    IS NULL
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;


-- name: ConsumeUnlockToken :execrows
-- Marks an account_unlock token as used. The AND used_at IS NULL guard ensures
-- idempotency: a race between two concurrent correct submissions cannot consume
-- the same token twice.
UPDATE one_time_tokens
SET used_at = NOW()
WHERE id      = @id::uuid
  AND used_at IS NULL;


-- name: HasConsumedUnlockToken :one
-- Returns true when a consumed (used_at IS NOT NULL) account_unlock token exists
-- for the email. Called by ConsumeUnlockTokenTx when GetUnlockToken returns no
-- active rows, to distinguish ErrTokenAlreadyUsed from ErrTokenNotFound.
SELECT EXISTS(
    SELECT 1
    FROM one_time_tokens
    WHERE email      = @email
      AND token_type = 'account_unlock'
      AND used_at    IS NOT NULL
) AS consumed;


-- name: UnlockAccount :exec
-- Clears is_locked, failed_login_attempts, and login_locked_until atomically.
-- Called after a successful account-unlock OTP confirmation.
UPDATE users
SET is_locked             = FALSE,
    failed_login_attempts = 0,
    login_locked_until    = NULL
WHERE id = @user_id::uuid;


-- name: GetUserForUnlock :one
-- Fetches the minimal fields needed to gate a self-service unlock request.
-- Returns the row regardless of lock state so the service can decide whether to
-- issue a token without leaking information to unauthenticated callers.
SELECT id, email_verified, is_locked, admin_locked, login_locked_until
FROM users
WHERE email = @email
LIMIT 1;


/* ── Refresh token lifecycle ────────────────────────────────────────────────── */

/*
  Refresh token rotation and revocation — building blocks for /refresh and /logout.
  Composed by RotateRefreshTokenTx and LogoutTx in store.go.

  Token-family reuse detection (RFC 6819 §5.2.2.3):
    - Every rotation stamps the presented token with revoke_reason = 'rotated'.
    - If a revoked token is re-presented, RevokeFamilyRefreshTokens fires with
      reason = 'reuse_detected', killing every active sibling in the family.
    - Logout uses RevokeRefreshTokenByJTI with reason = 'logout' — fn_revoke_token_family
      skips 'logout' so no cascade fires on voluntary logout.
*/

-- name: GetRefreshTokenByJTI :one
-- Fetches a refresh_tokens row by jti (primary key).
-- Used by the /refresh endpoint to validate the presented token before rotation.
-- Returns pgx.ErrNoRows when the jti does not exist in the table.
SELECT jti, user_id, session_id, family_id, expires_at, revoked_at
FROM refresh_tokens
WHERE jti = @jti::uuid;


-- name: RevokeRefreshTokenByJTI :execresult
-- Marks a single refresh token as revoked.
-- AND revoked_at IS NULL makes the operation idempotent.
-- Called with reason = 'rotated' during token rotation and reason = 'logout'
-- during an explicit logout. The 'logout' reason is excluded from the family-cascade
-- trigger so only the presented token is revoked, not the entire family.
UPDATE refresh_tokens
SET revoked_at    = NOW(),
    revoke_reason = @reason::text
WHERE jti        = @jti::uuid
  AND revoked_at IS NULL;


-- name: RevokeFamilyRefreshTokens :exec
-- Revokes every non-revoked token in the given token family.
-- Called with reason = 'reuse_detected' when the refresh endpoint receives a token
-- that has already been consumed (token-replay attack). Kills the entire family to
-- force re-authentication regardless of which generation the attacker holds.
UPDATE refresh_tokens
SET revoked_at    = NOW(),
    revoke_reason = @reason::text
WHERE family_id   = @family_id::uuid
  AND revoked_at IS NULL;


-- name: CreateRotatedRefreshToken :one
-- Issues a child refresh token linked to the presented (now-revoked) parent_jti.
-- Inherits family_id and session_id from the caller. TTL resets to 30 days from NOW()
-- rather than inheriting the parent's remaining TTL, consistent with initial login issuance.
-- NOTE: If ancestry traversal is ever implemented, reinstate:
--   CREATE INDEX idx_rt_parent_jti ON refresh_tokens(parent_jti) WHERE parent_jti IS NOT NULL;
WITH cfg AS (
    SELECT INTERVAL '30 days' AS refresh_ttl
)
INSERT INTO refresh_tokens (
    user_id,
    session_id,
    family_id,
    parent_jti,
    expires_at
)
SELECT
    @user_id::uuid,
    @session_id::uuid,
    @family_id::uuid,
    @parent_jti::uuid,
    NOW() + cfg.refresh_ttl
FROM cfg
RETURNING jti, expires_at;


-- name: EndUserSession :exec
-- Closes a single session row identified by its id.
-- AND ended_at IS NULL makes the operation idempotent.
-- Called during logout to mark the device's session as explicitly ended.
-- For mass session termination (password change, forced logout) use EndAllUserSessions.
UPDATE user_sessions
SET ended_at = NOW()
WHERE id        = @id::uuid
  AND ended_at IS NULL;


/* ── Sessions ───────────────────────────────────────────────────────────────── */

-- name: GetActiveSessions :many
-- Returns all open sessions for the user, newest-activity first.
SELECT
    id,
    ip_address,
    user_agent,
    started_at,
    last_active_at
FROM user_sessions
WHERE user_id = @user_id::uuid
  AND ended_at IS NULL
ORDER BY last_active_at DESC
LIMIT 50;


-- name: GetSessionByID :one
-- Used by DELETE /sessions/:id to verify the session belongs to the calling user.
SELECT id, user_id
FROM user_sessions
WHERE id = @id::uuid
LIMIT 1;


-- name: RevokeSessionRefreshTokens :exec
-- Revokes all non-revoked refresh tokens for a specific session.
-- Called by RevokeSessionTx when a user explicitly ends a single device session.
UPDATE refresh_tokens
SET revoked_at    = NOW(),
    revoke_reason = 'session_revoked'
WHERE session_id  = @session_id::uuid
  AND revoked_at IS NULL;


-- name: UpdateSessionLastActive :exec
-- Stamps last_active_at = NOW() for a session that is still open.
-- Called by the /refresh endpoint after successful token rotation so the
-- device session shows real activity, not just creation time.
-- AND ended_at IS NULL makes the update a no-op for already-closed sessions.
UPDATE user_sessions
SET last_active_at = NOW()
WHERE id        = @id::uuid
  AND ended_at IS NULL;


/* ── Mass revocation ────────────────────────────────────────────────────────── */

/*
  Mass-revocation queries — used by RevokeAllUserTokens in store.go.
  Building blocks for password-change and forced-logout flows.
  Both queries are idempotent (IS NULL / IS NULL guards).
*/

-- name: RevokeAllUserRefreshTokens :exec
-- Revokes every active (non-expired, non-revoked) refresh token for the user.
-- reason distinguishes mass-revocations from individual reuse events in the audit trail
-- (e.g. 'password_changed', 'forced_logout').
-- Scoped to expires_at > NOW() so already-expired rows are left untouched —
-- they carry no security risk and bulk-updating them wastes I/O.
UPDATE refresh_tokens
SET revoked_at    = NOW(),
    revoke_reason = @reason::text
WHERE user_id   = @user_id::uuid
  AND revoked_at IS NULL
  AND expires_at > NOW();


-- name: EndAllUserSessions :exec
-- Closes every open session for the user.
-- Called in the same transaction as RevokeAllUserRefreshTokens so the token ledger
-- and the session list stay consistent.
UPDATE user_sessions
SET ended_at = NOW()
WHERE user_id  = @user_id::uuid
  AND ended_at IS NULL;


/* ── Forgot / reset password ────────────────────────────────────────────────── */

/*
  Forgot-password / reset-password flow — OTP path.

  Caller responsibilities:
    1. Call GetUserForPasswordReset(email) to gate the request (anti-enumeration).
    2. generateCodeHash() → raw_code, code_hash
       Send raw_code in the forgot-password email — never store or log it.
    3. Call InvalidateAllUserPasswordResetTokens(user_id) inside the same transaction
       before CreatePasswordResetToken to prevent token accumulation.
    4. Call CreatePasswordResetToken(user_id, email, code_hash, ip_address).
    5. On reset: call GetPasswordResetToken(email) FOR UPDATE inside a transaction,
       validate expiry, attempts, and hash in the application layer.
    6. ConsumePasswordResetToken, UpdatePasswordHash, RevokeAllUserRefreshTokens,
       EndAllUserSessions, and InsertAuditLog — all in the same transaction.
    7. On invalid code: IncrementVerificationAttempts in a separate transaction
       (same pattern as email verification).
*/

-- name: GetPasswordResetTokenCreatedAt :one
-- Returns created_at of the most recent active (used_at IS NULL) password_reset
-- token for the given email. Used by the service to enforce a 60-second cooldown
-- between reset requests without relying solely on the unique-index constraint.
-- Returns pgx.ErrNoRows when no active token exists.
SELECT created_at
FROM one_time_tokens
WHERE email      = @email
  AND token_type = 'password_reset'
  AND used_at    IS NULL
ORDER BY created_at DESC
LIMIT 1;

-- name: GetUserForPasswordReset :one
-- Fetches the minimal fields needed to gate a password-reset request.
-- Returns the row regardless of lock state so the service can make its own
-- anti-enumeration decision without leaking information about account state.
SELECT
    id,
    email_verified,
    is_locked,
    admin_locked,
    is_active
FROM users
WHERE email = @email
LIMIT 1;


-- name: InvalidateAllUserPasswordResetTokens :exec
-- Voids all unused password_reset tokens for this user before issuing a new one.
-- Prevents token accumulation and reduces the concurrent-reset attack window.
-- Called by RequestPasswordResetTx inside the same transaction, immediately
-- before CreatePasswordResetToken.
UPDATE one_time_tokens
SET used_at = NOW()
WHERE user_id    = @user_id::uuid
  AND token_type = 'password_reset'
  AND used_at    IS NULL;


-- name: CreatePasswordResetToken :one
-- Issues a new password_reset OTP token with a caller-controlled TTL and max 3 attempts.
-- Caller must call InvalidateAllUserPasswordResetTokens first (within the same
-- transaction) to void any outstanding unused tokens.
-- TTL is passed as @ttl_seconds (float8) — same pattern as CreateEmailVerificationToken
-- and CreateUnlockToken. The authoritative value is config.Config.OTPValidMinutes.
INSERT INTO one_time_tokens (
    token_type,
    user_id,
    email,
    code_hash,
    expires_at,
    ip_address,
    max_attempts
)
VALUES (
    'password_reset',
    @user_id::uuid,
    @email,
    @code_hash,
    NOW() + make_interval(secs => @ttl_seconds::float8),
    sqlc.narg('ip_address')::inet,
    3
)
RETURNING
    id,
    expires_at;


-- name: GetPasswordResetToken :one
-- Fetches the most recent active (unused) password_reset token for the email.
-- FOR UPDATE prevents concurrent double-consumption (same pattern as
-- GetEmailVerificationToken and GetUnlockToken).
SELECT
    id,
    user_id,
    email,
    code_hash,
    attempts,
    max_attempts,
    expires_at,
    used_at
FROM one_time_tokens
WHERE email      = @email
  AND token_type = 'password_reset'
  AND code_hash  IS NOT NULL
  AND used_at    IS NULL
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;


-- name: GetPasswordResetTokenForVerify :one
-- Returns the token row for OTP validation without locking the row.
-- Used by VerifyResetCode: no FOR UPDATE because the token is not consumed here.
-- The consuming query (GetPasswordResetToken) uses FOR UPDATE separately.
SELECT
    id,
    user_id,
    email,
    code_hash,
    attempts,
    max_attempts,
    expires_at
FROM one_time_tokens
WHERE email      = @email
  AND token_type = 'password_reset'
  AND code_hash  IS NOT NULL
  AND used_at    IS NULL
ORDER BY created_at DESC, id DESC
LIMIT 1;


-- name: ConsumePasswordResetToken :execrows
-- Marks the token as used. The AND used_at IS NULL guard ensures idempotency:
-- a race between two concurrent reset submissions cannot consume the same token twice.
UPDATE one_time_tokens
SET used_at = NOW()
WHERE id      = @id::uuid
  AND used_at IS NULL;


-- name: UpdatePasswordHash :exec
-- Replaces the stored bcrypt password hash for a user.
-- Called after successful OTP validation in a transaction that also revokes all
-- existing sessions — a password change must invalidate every active device.
UPDATE users
SET password_hash = @password_hash
WHERE id = @user_id::uuid;


/* ── Change password ─────────────────────────────────────────────────────────── */

-- name: GetUserPasswordHash :one
-- Fetches the current bcrypt hash for credential re-verification before a password change.
SELECT id, password_hash
FROM users
WHERE id = @user_id::uuid
LIMIT 1;


-- name: IncrementChangePasswordFailures :one
-- Increments failed_change_password_attempts and returns the new count.
-- Called when the user submits a wrong old_password on POST /change-password.
-- AND failed_change_password_attempts < 32767 guards against SMALLINT overflow
-- on pathological inputs (the service threshold is 5, so overflow is unreachable
-- in normal operation).
-- Returns pgx.ErrNoRows when the user row no longer exists (deleted between
-- GetUserPasswordHash and this call) — callers treat this as a non-fatal log.
UPDATE users
SET failed_change_password_attempts = failed_change_password_attempts + 1
WHERE id = @user_id::uuid
  AND failed_change_password_attempts < 32767
RETURNING failed_change_password_attempts;


-- name: ResetChangePasswordFailures :exec
-- Resets failed_change_password_attempts to 0 after a successful password change.
-- Ensures the user starts with a clean counter on their next change-password attempt.
-- AND failed_change_password_attempts > 0 makes the update a no-op when already zero,
-- avoiding a write on the happy path for users who never had a failed attempt.
UPDATE users
SET failed_change_password_attempts = 0
WHERE id                             = @user_id::uuid
  AND failed_change_password_attempts > 0;


/* ── Profile ─────────────────────────────────────────────────────────────────── */

-- name: GetUserProfile :one
SELECT
    id,
    email,
    display_name,
    username,
    avatar_url,
    email_verified,
    is_active,
    is_locked,
    admin_locked,
    last_login_at,
    created_at
FROM users
WHERE id = @user_id::uuid
LIMIT 1;

-- name: UpdateUserProfile :exec
-- Updates display_name and/or avatar_url using COALESCE so that a NULL
-- parameter leaves the current column value unchanged (partial-update pattern).
-- Called by UpdateProfileTx after input validation in the handler confirms
-- at least one field is non-nil.
UPDATE users
SET
    display_name = COALESCE(@display_name, display_name),
    avatar_url   = COALESCE(@avatar_url,   avatar_url)
WHERE id = @user_id::uuid;


/* ── Set password (OAuth-only accounts) ──────────────────────────────────── */

-- name: GetUserForSetPassword :one
-- Returns whether the user currently has no password (signed up via OAuth only).
-- Used by POST /set-password to gate the operation before attempting the write.
SELECT
    id,
    (password_hash IS NULL) AS has_no_password
FROM users
WHERE id = @user_id::uuid;

-- name: SetPasswordHash :execrows
-- Sets password_hash for an OAuth-only account.
-- The WHERE password_hash IS NULL guard is the DB-level concurrency check:
-- a concurrent set-password call that races past the service guard returns
-- 0 rows affected, which the store maps to ErrPasswordAlreadySet.
UPDATE users
SET    password_hash = @password_hash
WHERE  id            = @user_id::uuid
  AND  password_hash IS NULL;


/* ── Username ─────────────────────────────────────────────────────────────────── */

-- name: CheckUsernameAvailable :one
-- Returns true when no row with username = @username exists in users.
-- No FOR UPDATE — this is a point-in-time availability check; the write path
-- enforces uniqueness via idx_users_username (23505 on conflict).
SELECT EXISTS(
    SELECT 1
    FROM users
    WHERE username = @username
) AS exists;


-- name: GetUserForUsernameUpdate :one
-- Returns id and current username for the calling user.
-- FOR UPDATE locks the row inside UpdateUsernameTx to prevent a concurrent
-- rename from racing past the same-username guard or producing stale audit
-- metadata (i.e. old_username in the audit log).
SELECT id, username
FROM users
WHERE id = @user_id::uuid
LIMIT 1
FOR UPDATE;


-- name: SetUsername :execrows
-- Sets username for the user identified by id.
-- Returns rows affected so the store can distinguish:
--   23505 unique_violation on idx_users_username → ErrUsernameTaken
--   rows == 0                                    → ErrUserNotFound
UPDATE users
SET username = @username
WHERE id = @user_id::uuid;


/* ── Email change ─────────────────────────────────────────────────────────── */

/*
  Email-change flow — three steps, each with its own OTP.

  Step 1 (POST /email/request-change):
    1. Validate new_email; check it differs from current and is not taken.
    2. Enforce 2-minute cooldown via GetLatestEmailChangeVerifyTokenCreatedAt.
    3. Invalidate any existing email_change_verify tokens for this user.
    4. Create a new email_change_verify token; store new_email in metadata column.
    5. Email the OTP to the user's CURRENT email address.

  Step 2 (POST /email/verify-current):
    1. Fetch and lock the email_change_verify token (FOR UPDATE).
    2. Validate expiry, attempts, and OTP hash in the application layer.
    3. Consume the verify token; invalidate any existing email_change_confirm tokens.
    4. Create a new email_change_confirm token (email column = new_email).
    5. Email the OTP to the new address.
    6. Issue a short-lived KV grant token (echg:gt:, 10 min); return it to the caller.

  Step 3 (POST /email/confirm-change):
    1. Validate the KV grant token (proves step 2 was completed).
    2. Fetch and lock the email_change_confirm token (FOR UPDATE).
    3. Validate expiry, attempts, and OTP hash in the application layer.
    4. Run ConfirmEmailChangeTx: ConsumeEmailChangeToken + SetUserEmail +
       RevokeAllUserRefreshTokens + EndAllUserSessions + blocklist access token.

  Index notes:
    - idx_ott_active (user_id, token_type) covers the verify/confirm token lookups.
    - idx_email_change_verify_tokens_user_active (partial unique on user_id) prevents
      duplicate active verify tokens; 23505 → ErrCooldownActive in the store.
    - idx_email_change_confirm_tokens_user_active (partial unique on user_id) prevents
      duplicate active confirm tokens.
    - D-01 resolved: new_email is carried step 1 → 2 via the metadata JSONB column on
      the email_change_verify token row (001_core.sql comment). No KV needed for this leg.
*/

-- name: CheckEmailAvailableForChange :one
-- Returns true when no active (non-deleted) user holds @new_email.
-- Excludes the calling user (id != @user_id) so a same-address re-request
-- is not rejected here — the same-email guard in the service handles that.
-- No FOR UPDATE — point-in-time check; SetUserEmail catches 23505 via
-- idx_users_email_active as the definitive uniqueness guard.
SELECT EXISTS(
    SELECT 1
    FROM users
    WHERE email      = @new_email
      AND id        != @user_id::uuid
      AND deleted_at IS NULL
) AS exists;


-- name: GetLatestEmailChangeVerifyTokenCreatedAt :one
-- Returns created_at of the most recent active (used_at IS NULL) email_change_verify
-- token for this user. Used by the service to enforce the 2-minute cooldown
-- before allowing a new request-change call.
-- Returns pgx.ErrNoRows when no active verify token exists.
-- Index: idx_ott_active covers (user_id, token_type, used_at IS NULL).
SELECT created_at
FROM one_time_tokens
WHERE user_id    = @user_id::uuid
  AND token_type = 'email_change_verify'
  AND used_at    IS NULL
ORDER BY created_at DESC
LIMIT 1;


-- name: InvalidateUserEmailChangeVerifyTokens :exec
-- Voids all unused email_change_verify tokens for this user before issuing a new one.
-- Prevents token accumulation and ensures the partial unique index
-- (idx_email_change_verify_tokens_user_active) never blocks a legitimate re-request.
-- Called inside the same transaction as CreateEmailChangeVerifyToken.
UPDATE one_time_tokens
SET used_at = NOW()
WHERE user_id    = @user_id::uuid
  AND token_type = 'email_change_verify'
  AND used_at    IS NULL;


-- name: CreateEmailChangeVerifyToken :one
-- Issues a new email_change_verify OTP token.
-- @email is the user's CURRENT address (for audit readability).
-- @metadata stores {"new_email": "..."} so step 2 can retrieve the destination
-- without a separate KV lookup (one_time_tokens.metadata JSONB, see 001_core.sql).
-- TTL via @ttl_seconds::float8 — same clock pattern as CreateEmailVerificationToken.
-- The DB constraint chk_ott_ecv_ttl_max caps the TTL at 15 minutes.
-- max_attempts = 5 (Stage 0 D-12).
INSERT INTO one_time_tokens (
    token_type,
    user_id,
    email,
    code_hash,
    metadata,
    expires_at,
    ip_address,
    max_attempts
)
VALUES (
    'email_change_verify',
    @user_id::uuid,
    @email,
    @code_hash,
    @metadata,
    NOW() + make_interval(secs => @ttl_seconds::float8),
    sqlc.narg('ip_address')::inet,
    5
)
RETURNING
    id,
    expires_at;


-- name: GetEmailChangeVerifyToken :one
-- Fetches the active email_change_verify token for the given authenticated user.
-- Lookup is by (user_id, token_type) — no email parameter needed because the user
-- is already authenticated via JWT.
-- Returns metadata so the caller can extract new_email.
-- FOR UPDATE prevents concurrent double-consumption.
-- ORDER BY created_at DESC, id DESC picks the most recent token in the unlikely
-- event that two rows exist (race between invalidation and insert).
SELECT
    id,
    user_id,
    email,
    code_hash,
    metadata,
    attempts,
    max_attempts,
    expires_at,
    used_at
FROM one_time_tokens
WHERE user_id    = @user_id::uuid
  AND token_type = 'email_change_verify'
  AND code_hash  IS NOT NULL
  AND used_at    IS NULL
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;


-- name: ConsumeEmailChangeToken :execrows
-- Marks an email_change_verify or email_change_confirm token as used.
-- AND used_at IS NULL ensures idempotency: a concurrent correct submission
-- cannot consume the same token twice.
-- Shared by step 2 (consume verify token) and step 3 (consume confirm token).
UPDATE one_time_tokens
SET used_at = NOW()
WHERE id      = @id::uuid
  AND used_at IS NULL;


-- name: InvalidateUserEmailChangeConfirmTokens :exec
-- Voids all unused email_change_confirm tokens for this user before issuing a new one.
-- Called inside the same transaction as CreateEmailChangeConfirmToken (step 2).
UPDATE one_time_tokens
SET used_at = NOW()
WHERE user_id    = @user_id::uuid
  AND token_type = 'email_change_confirm'
  AND used_at    IS NULL;


-- name: CreateEmailChangeConfirmToken :one
-- Issues a new email_change_confirm OTP token for step 2.
-- @new_email is stored in the email column (D-09: for audit readability; also
-- lets step 3 retrieve new_email directly from the token row without a separate query).
-- max_attempts = 5 (Stage 0 D-12).
-- The DB constraint chk_ott_ecc_ttl_max caps the TTL at 15 minutes.
INSERT INTO one_time_tokens (
    token_type,
    user_id,
    email,
    code_hash,
    expires_at,
    ip_address,
    max_attempts
)
VALUES (
    'email_change_confirm',
    @user_id::uuid,
    @new_email,
    @code_hash,
    NOW() + make_interval(secs => @ttl_seconds::float8),
    sqlc.narg('ip_address')::inet,
    5
)
RETURNING
    id,
    expires_at;


-- name: GetEmailChangeConfirmToken :one
-- Fetches the active email_change_confirm token for the given authenticated user.
-- Lookup by (user_id, token_type) — the user is authenticated via grant token.
-- The email column holds the new_email address for use in step 3.
-- FOR UPDATE prevents concurrent double-consumption.
SELECT
    id,
    user_id,
    email,
    code_hash,
    attempts,
    max_attempts,
    expires_at,
    used_at
FROM one_time_tokens
WHERE user_id    = @user_id::uuid
  AND token_type = 'email_change_confirm'
  AND code_hash  IS NOT NULL
  AND used_at    IS NULL
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;


-- name: GetUserForEmailChangeTx :one
-- Returns id and current email for the authenticated user inside a transaction.
-- FOR UPDATE locks the row to prevent a concurrent email-change from racing past
-- the uniqueness re-check (D-05) or producing stale old_email in audit metadata.
SELECT id, email
FROM users
WHERE id         = @user_id::uuid
  AND deleted_at IS NULL
LIMIT 1
FOR UPDATE;


-- name: SetUserEmail :execrows
-- Updates users.email for the given active user.
-- Returns rows affected so the store can distinguish:
--   23505 on idx_users_email_active → ErrEmailTaken (concurrent race past service guard)
--   rows == 0                       → ErrUserNotFound (account deleted between steps)
UPDATE users
SET email = @new_email
WHERE id         = @user_id::uuid
  AND deleted_at IS NULL;
