/* ============================================================
   sql/queries/oauth.sql
   OAuth identity domain queries for sqlc code generation.

   Covers Google OAuth and is structured to accommodate future providers
   (e.g. Telegram) without merging into auth.sql.

   Design notes:
     - Encrypted OAuth tokens (access_token, refresh_token_provider) live in
       user_identity_tokens, not user_identities. This prevents SELECT * on
       user_identities from ever returning ciphertext in API responses.
       UpsertUserIdentity handles metadata only; UpsertUserIdentityTokens
       handles tokens. Both must be called in the same transaction.
     - Security-sensitive fields (password_hash, is_locked, admin_locked)
       live in user_secrets. Queries that need them JOIN user_secrets.
     - CreateOAuthUser also inserts a user_secrets row (with NULL password_hash)
       so trg_require_auth_method is satisfied by the identity INSERT that
       follows in the same deferred transaction.
   ============================================================ */


/* ── OAuth Identity ── */

-- name: GetIdentityByProviderUID :one
-- Looks up an identity by provider + provider_uid (the external account's stable ID).
-- Used during OAuth callback to determine whether an incoming provider account is
-- already linked to an internal user.
-- access_token is intentionally excluded — it lives in user_identity_tokens and
-- must never be returned in bulk identity lookups (ciphertext in API response risk).
-- Callers that need the token must query user_identity_tokens separately by identity_id.
SELECT id, user_id, provider_email, display_name, avatar_url
FROM user_identities
WHERE provider = @provider
  AND provider_uid = @provider_uid;

-- name: GetIdentityByUserAndProvider :one
-- Returns the identity row for a specific (user, provider) pair.
-- Used to check whether a user already has a linked account for a given provider
-- before attempting to link or unlink.
SELECT id, user_id, provider_uid
FROM user_identities
WHERE user_id = @user_id::uuid
  AND provider = @provider;

-- name: UpsertUserIdentity :one
-- Inserts or updates the identity metadata row for an OAuth account.
-- Token columns (access_token, access_token_expires_at, refresh_token_provider)
-- are NOT handled here — call UpsertUserIdentityTokens immediately after in the
-- same transaction to persist encrypted tokens.
--
-- ON CONFLICT ON CONSTRAINT uq_identity_user_provider refreshes provider-side
-- metadata (display name, avatar, raw profile) on each login without creating duplicate rows.
--
-- RETURNING uses an explicit column list (not *) so future column additions to
-- user_identities do not silently change the generated Go struct.
INSERT INTO user_identities (
    user_id, provider, provider_uid,
    provider_email, display_name, avatar_url, raw_profile
)
VALUES (
    @user_id::uuid,
    @provider,
    @provider_uid,
    @provider_email,
    @display_name,
    @avatar_url,
    sqlc.narg('raw_profile')::jsonb
)
ON CONFLICT ON CONSTRAINT uq_identity_user_provider DO UPDATE
    SET provider_email = EXCLUDED.provider_email,
        display_name   = EXCLUDED.display_name,
        avatar_url     = EXCLUDED.avatar_url,
        raw_profile    = EXCLUDED.raw_profile,
        updated_at     = NOW()
RETURNING
    id,
    user_id,
    provider,
    provider_uid,
    provider_email,
    display_name,
    avatar_url,
    created_at,
    updated_at;

-- name: UpsertUserIdentityTokens :exec
-- Persists encrypted OAuth tokens for the given identity.
-- access_token MUST be AES-256-GCM encrypted (enc: prefix) before being passed here.
-- refresh_token_provider MUST also be encrypted if non-NULL.
-- chk_uit_access_token_encrypted and chk_uit_refresh_token_encrypted enforce this at the DB layer.
-- Must be called in the same transaction as UpsertUserIdentity, using the returned identity_id.
INSERT INTO user_identity_tokens (
    identity_id,
    access_token,
    access_token_expires_at,
    refresh_token_provider
)
VALUES (
    @identity_id::uuid,
    @access_token,
    sqlc.narg('access_token_expires_at')::timestamptz,
    sqlc.narg('refresh_token_provider')
)
ON CONFLICT (identity_id) DO UPDATE
    SET access_token            = EXCLUDED.access_token,
        access_token_expires_at = EXCLUDED.access_token_expires_at,
        refresh_token_provider  = EXCLUDED.refresh_token_provider,
        updated_at              = NOW();

-- name: DeleteUserIdentity :execrows
-- Removes the linked identity for the given (user, provider) pair.
-- Returns rows affected: 0 means the identity did not exist (idempotent).
-- trg_prevent_orphan_on_identity_delete (002_core_functions.sql) will raise an
-- exception if this would leave a password-less user with no remaining identity.
DELETE FROM user_identities
WHERE user_id = @user_id::uuid
  AND provider = @provider;

/* ── OAuth User ── */

-- name: GetUserAuthMethods :one
-- Returns whether the user has a password set and how many OAuth identities are linked.
-- Used by the account-settings page to determine which auth methods are available
-- and whether it is safe to unlink an identity or remove a password.
-- password_hash lives in user_secrets; a LEFT JOIN on user_identities counts linked providers.
SELECT
    (us.password_hash IS NOT NULL) AS has_password,
    COUNT(ui.id)                    AS identity_count
FROM users u
JOIN user_secrets us ON us.user_id = u.id
LEFT JOIN user_identities ui ON ui.user_id = u.id
WHERE u.id = @user_id::uuid
  AND u.deleted_at IS NULL
GROUP BY us.password_hash;

-- name: CreateOAuthUser :one
-- Creates a new user account for a first-time OAuth sign-in.
-- Atomically inserts both the users row and its companion user_secrets row (NULL
-- password_hash) using a CTE so trg_require_auth_method (DEFERRABLE INITIALLY
-- DEFERRED) sees both rows. The caller must INSERT into user_identities in the
-- same transaction before commit to satisfy the auth-method requirement.
-- email_verified = TRUE and is_active = TRUE because the provider has already
-- confirmed the email address.
-- avatar_url is seeded from the provider's profile picture so the user's profile
-- shows an avatar immediately without a separate update step.
WITH new_user AS (
    INSERT INTO users (email, display_name, avatar_url, email_verified, is_active)
    VALUES (
        sqlc.narg('email')::text,
        sqlc.narg('display_name')::text,
        sqlc.narg('avatar_url')::text,
        TRUE,
        TRUE
    )
    RETURNING id
),
_secrets AS (
    INSERT INTO user_secrets (user_id)
    SELECT id FROM new_user
)
SELECT id FROM new_user;

-- name: UpdateUserAvatarIfNull :exec
-- Backfills avatar_url from an OAuth provider only when the user has no avatar set.
-- Called during OAuth login to sync the provider's picture without overwriting
-- a profile picture the user has explicitly set via PATCH /profile/me.
UPDATE users
SET avatar_url = @avatar_url
WHERE id = @user_id::uuid
  AND avatar_url IS NULL;

-- name: GetUserByEmailForOAuth :one
-- Looks up an existing user by email during OAuth callback processing.
-- Used when a provider returns an email that matches an existing account
-- (e.g. signing in with Google using an email already registered via password).
-- Returns is_locked and admin_locked so the OAuth callback handler can gate the
-- flow with the same lockout checks as the password login path.
SELECT u.id, u.is_active, us.is_locked, us.admin_locked
FROM users u
JOIN user_secrets us ON us.user_id = u.id
WHERE u.email = @email
  AND u.deleted_at IS NULL;

-- name: GetUserForOAuthCallback :one
-- Fetches the account state for a user identified by id during OAuth callback processing.
-- Used after GetIdentityByProviderUID returns an existing linked identity to verify the
-- account is still active before issuing tokens.
SELECT u.id, u.is_active, us.is_locked, us.admin_locked
FROM users u
JOIN user_secrets us ON us.user_id = u.id
WHERE u.id = @user_id::uuid
  AND u.deleted_at IS NULL;

-- name: GetUserIdentities :many
-- Returns all linked OAuth identities for the given user, oldest first.
-- Used by the account-settings page to display connected providers.
-- access_token and refresh_token_provider are intentionally excluded —
-- they are provider secrets stored in user_identity_tokens and must never
-- be returned to clients.
SELECT
    provider,
    provider_uid,
    provider_email,
    display_name,
    avatar_url,
    created_at
FROM user_identities
WHERE user_id = @user_id::uuid
ORDER BY created_at ASC;
