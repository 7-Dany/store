-- oauth.sql — queries for the OAuth domain (Google provider).
-- Appended to by future providers (Telegram). Do not merge into auth.sql.

/* ── OAuth Identity ── */

-- name: GetIdentityByProviderUID :one
SELECT id, user_id, provider_email, display_name, avatar_url, access_token
FROM user_identities
WHERE provider = @provider
  AND provider_uid = @provider_uid;

-- name: GetIdentityByUserAndProvider :one
SELECT id, user_id
FROM user_identities
WHERE user_id = @user_id::uuid
  AND provider = @provider;

-- name: UpsertUserIdentity :one
INSERT INTO user_identities (
    user_id, provider, provider_uid,
    provider_email, display_name, avatar_url, access_token
)
VALUES (
    @user_id::uuid,
    @provider,
    @provider_uid,
    @provider_email,
    @display_name,
    @avatar_url,
    @access_token
)
ON CONFLICT ON CONSTRAINT uq_identity_user_provider DO UPDATE
    SET provider_email = EXCLUDED.provider_email,
        display_name   = EXCLUDED.display_name,
        avatar_url     = EXCLUDED.avatar_url,
        access_token   = EXCLUDED.access_token,
        updated_at     = NOW()
RETURNING *;

-- name: DeleteUserIdentity :execrows
DELETE FROM user_identities
WHERE user_id = @user_id::uuid
  AND provider = @provider;

/* ── OAuth User ── */

-- name: GetUserAuthMethods :one
SELECT
    (u.password_hash IS NOT NULL) AS has_password,
    COUNT(ui.id)                   AS identity_count
FROM users u
LEFT JOIN user_identities ui ON ui.user_id = u.id
WHERE u.id = @user_id::uuid
  AND u.deleted_at IS NULL
GROUP BY u.password_hash;

-- name: CreateOAuthUser :one
INSERT INTO users (email, display_name, email_verified, is_active)
VALUES (
    sqlc.narg('email')::text,
    sqlc.narg('display_name')::text,
    TRUE,
    TRUE
)
RETURNING id;

-- name: GetUserByEmailForOAuth :one
SELECT id, is_active, is_locked, admin_locked
FROM users
WHERE email = @email
  AND deleted_at IS NULL;

-- name: GetUserForOAuthCallback :one
SELECT id, is_active, is_locked, admin_locked
FROM users
WHERE id = @user_id::uuid
  AND deleted_at IS NULL;

-- name: GetUserIdentities :many
-- Returns all linked OAuth identities for the given user, oldest first.
-- access_token and refresh_token_provider are intentionally excluded —
-- they are provider secrets and must never be returned to clients.
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
