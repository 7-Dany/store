-- +goose Up
-- +goose StatementBegin

/*
 * 001_core.sql — Core authentication schema
 *
 * Establishes the foundational identity and authentication tables:
 * users — public-facing identity record (no secrets)
 * user_secrets — security-sensitive fields split from users
 * user_identities — linked OAuth provider accounts
 * user_identity_tokens— encrypted OAuth credentials split from user_identities
 * one_time_tokens — OTP and magic-link tokens for all email flows
 * user_sessions — active login session tracking
 * refresh_tokens — JWT refresh token ledger with rotation family support
 * auth_audit_log — immutable security event history
 * account_purge_log — permanent record of hard-deleted accounts
 * active_users — convenience view filtering soft-deleted rows
 *
 * Depends on: nothing (first migration in the chain).
 */


/* ─────────────────────────────────────────────────────────────
 ENUMS
 ───────────────────────────────────────────────────────────── */

-- Discriminates which authentication provider was used for a given login, session,
-- or identity row. New providers must be added with ALTER TYPE … ADD VALUE.
-- Never remove a value that may be referenced by existing rows.
CREATE TYPE auth_provider AS ENUM (
 'email', -- traditional email + password
 'magic_link', -- passwordless one-click link sent to email
 'google', -- Google OAuth 2.0
 'telegram' -- Telegram OAuth
);

COMMENT ON TYPE auth_provider IS
 'Supported authentication providers. Add values with ALTER TYPE … ADD VALUE; never remove a value referenced by existing rows.';

-- Controls which one_time_tokens row is being operated on and governs
-- which constraint rules and metadata shapes apply to that row.
CREATE TYPE one_time_token_type AS ENUM (
 'email_verification', -- 6-digit OTP or magic link sent on registration
 'password_reset', -- 6-digit OTP sent via Forgot Password
 'magic_link', -- opaque random token for passwordless login
 'account_unlock', -- 6-digit OTP sent to lift an OTP brute-force lock
 'email_change_verify', -- step 1: OTP sent to the current address to confirm ownership
 'email_change_confirm',-- step 2: OTP sent to the new address to confirm the destination
 'account_deletion' -- OTP sent to confirm the user's intent to delete their account (§B-3)
);

COMMENT ON TYPE one_time_token_type IS
 'Discriminator for one_time_tokens. Add values with ALTER TYPE … ADD VALUE.';


/* ─────────────────────────────────────────────────────────────
 USERS
 ───────────────────────────────────────────────────────────── */

/*
 * Public identity record for every registered account.
 *
 * Security-sensitive fields (password_hash, lock state, brute-force counters)
 * live in user_secrets so SELECT * on this table never accidentally
 * exposes credentials or security state in API responses.
 *
 * Soft-delete pattern: deleted_at IS NOT NULL marks a deleted account; the row
 * is preserved for audit and forensics until a background purge job removes it
 * after the 30-day grace period. Always query through the active_users view or
 * add WHERE deleted_at IS NULL explicitly.
 */
CREATE TABLE users (
 -- Immutable surrogate primary key; generated on INSERT.
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Login identifier; nullable because OAuth-only accounts may have no email.
 -- Uniqueness among active accounts enforced by idx_users_email_active (partial index).
 email TEXT,

 -- Chosen handle; nullable for OAuth accounts that haven't set one yet.
 -- Max 50 chars enforced by chk_users_username_length; must not be blank if set.
 username TEXT,

 -- Human-readable name shown in the UI; nullable, max 200 chars.
 display_name TEXT,

 -- Profile picture URL; nullable, max 2048 chars (accommodates long CDN URLs).
 avatar_url TEXT,

 -- FALSE until the user completes email verification. Unverified accounts cannot
 -- authenticate; they exist only to hold pending verification tokens.
 is_active BOOLEAN NOT NULL DEFAULT FALSE,

 -- Tracks whether the registered email address has been confirmed via OTP or magic link.
 email_verified BOOLEAN NOT NULL DEFAULT FALSE,

 -- Set by fn_set_updated_at trigger; never write manually.
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Updated on every successful login. NULL for accounts that have never completed login.
 last_login_at TIMESTAMPTZ,

 -- Soft-delete timestamp. NULL = active. Set on user-initiated deletion request.
 -- All read/write paths MUST filter WHERE deleted_at IS NULL (or use active_users view).
 -- Cleared if the account is recovered before the grace period expires.
 -- Partial unique indexes (idx_users_email_active, idx_users_username_active) ensure
 -- that a deleted account's email/username can be re-registered by a new user.
 deleted_at TIMESTAMPTZ,

 -- Rejects syntactically invalid emails written directly to the DB (admin scripts, imports).
 -- The application layer performs stricter validation before hitting this constraint.
 CONSTRAINT chk_users_email_format
 CHECK (email IS NULL OR email ~* '^[^@\s]+@[^@\s]+\.[^@\s]+$'),

 -- Prevents storing an empty string for username; must be NULL or have visible content.
 CONSTRAINT chk_users_username_nonempty
 CHECK (username IS NULL OR length(trim(username)) > 0),

 -- Caps username at 50 characters to match the application-layer validation.
 CONSTRAINT chk_users_username_length
 CHECK (username IS NULL OR length(username) <= 50),

 -- Prevents blank display_name from being stored (NULL is fine; whitespace-only is not).
 CONSTRAINT chk_users_display_name_nonempty
 CHECK (display_name IS NULL OR length(trim(display_name)) > 0),

 -- Caps display_name at 200 characters.
 CONSTRAINT chk_users_display_name_len
 CHECK (display_name IS NULL OR length(display_name) <= 200),

 -- Caps avatar_url at 2048 characters (standard URL length limit).
 CONSTRAINT chk_users_avatar_url_len
 CHECK (avatar_url IS NULL OR length(avatar_url) <= 2048),

 -- RFC 5321 maximum email address length.
 CONSTRAINT chk_users_email_length
 CHECK (email IS NULL OR length(email) <= 254)
);

-- Enforces email uniqueness only among non-deleted accounts.
-- A full-table UNIQUE index is intentionally absent: it would permanently reserve
-- a deleted account's email address, blocking re-registration after soft-delete.
-- Application maps 23505 violations on this index to ErrEmailTaken.
CREATE UNIQUE INDEX idx_users_email_active
 ON users (email)
 WHERE email IS NOT NULL AND deleted_at IS NULL;

-- Same rationale as idx_users_email_active but for username.
-- Application maps 23505 violations to ErrUsernameTaken.
CREATE UNIQUE INDEX idx_users_username_active
 ON users (username)
 WHERE username IS NOT NULL AND deleted_at IS NULL;

-- Supports the background purge worker's scan: find accounts whose grace period
-- has expired (deleted_at < NOW() - INTERVAL '30 days').
CREATE INDEX idx_users_pending_deletion
 ON users (deleted_at)
 WHERE deleted_at IS NOT NULL;

COMMENT ON TABLE users IS
 'Core identity record. Sensitive security fields (password_hash, lock state, attempt counters)
 are split into user_secrets so SELECT * on users never returns ciphertext or
 brute-force counters. trg_require_auth_method enforces at least one auth method per row.';
COMMENT ON COLUMN users.is_active IS
 'FALSE until email verification completes.';
COMMENT ON COLUMN users.deleted_at IS
 'Soft-delete timestamp. NULL = active account. Cleared by account recovery.
 Hard-purged by background job after 30-day grace period.
 Partial unique indexes (idx_users_email_active, idx_users_username_active) enforce
 uniqueness for active rows only, allowing re-registration after deletion.';
COMMENT ON COLUMN users.last_login_at IS
 'Timestamp of the most recent successful login. NULL = never logged in.';


/* ─────────────────────────────────────────────────────────────
 USER SECRETS
 ───────────────────────────────────────────────────────────── */

/*
 * Holds all security-sensitive fields that must never appear in a routine
 * SELECT * on users. Exactly one row per user (user_id is the PRIMARY KEY),
 * created atomically in the same transaction as the parent users row.
 *
 * Two independent lock mechanisms coexist:
 * is_locked — set after OTP brute-force exhaustion; cleared by OTP unlock flow or admin.
 * admin_locked — set/cleared exclusively by admin action (RBAC); user flows cannot touch it.
 */
CREATE TABLE user_secrets (
 -- 1:1 with users; cascades on parent deletion so no orphan rows accumulate.
 user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,

 -- bcrypt hash ($2a/$2b, fixed length 60). NULL for OAuth-only users who have no password.
 -- The application must never store a plaintext password or an unrecognised hash format.
 password_hash TEXT,

 -- Incremented atomically (attempts = attempts + 1) on each failed login.
 -- Never read-then-write under concurrency or increments will silently be lost.
 failed_login_attempts SMALLINT NOT NULL DEFAULT 0,

 -- Same atomic-increment requirement; reset to 0 on every successful password change.
 failed_change_password_attempts SMALLINT NOT NULL DEFAULT 0,

 -- Timestamp until which login is rate-limited after excessive failed attempts.
 -- NULL = not currently locked by failed attempts. Auto-clears when NOW() passes the value.
 login_locked_until TIMESTAMPTZ,

 -- TRUE after brute-force OTP exhaustion. Cleared by the account-unlock OTP flow or by admin.
 -- Independent of admin_locked — both can be TRUE simultaneously.
 is_locked BOOLEAN NOT NULL DEFAULT FALSE,

 -- TRUE when an administrator has explicitly locked this account via RBAC.
 -- Cleared only by admin action; the user-facing OTP unlock flow must never modify this field.
 admin_locked BOOLEAN NOT NULL DEFAULT FALSE,

 -- UUID of the admin who set admin_locked. RESTRICT prevents that admin from being deleted
 -- while the lock reference exists. NULL when admin_locked = FALSE.
 admin_locked_by UUID REFERENCES users(id) ON DELETE SET NULL,

 -- Human-readable reason provided by the admin at lock time. Required when admin_locked = TRUE.
 admin_locked_reason TEXT,

 -- Timestamp when admin_locked was last set. Required when admin_locked = TRUE.
 admin_locked_at TIMESTAMPTZ,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Validates bcrypt hash format: both Go's $2a and $2b prefixes are accepted;
 -- length 60 is bcrypt's fixed output length. Rejects any non-bcrypt value.
 CONSTRAINT chk_us_password_hash_format
 CHECK (password_hash IS NULL
 OR (length(password_hash) = 60 AND left(password_hash, 3) IN ('$2a', '$2b'))),

 CONSTRAINT chk_us_login_attempts_non_negative
 CHECK (failed_login_attempts >= 0),

 CONSTRAINT chk_us_change_pw_attempts_non_negative
 CHECK (failed_change_password_attempts >= 0),

 -- When admin_locked = TRUE, reason and timestamp are required metadata.
 -- When admin_locked = FALSE, all three metadata fields must be NULL to avoid stale data.
 CONSTRAINT chk_us_admin_lock_coherent CHECK (
 (admin_locked = FALSE AND admin_locked_reason IS NULL AND admin_locked_by IS NULL)
 OR
 (admin_locked = TRUE AND admin_locked_reason IS NOT NULL AND admin_locked_at IS NOT NULL)
 ),

 -- An admin cannot lock their own account via the admin_locked flag (self-lock prevention).
 CONSTRAINT chk_us_no_self_lock
 CHECK (admin_locked_by IS NULL OR admin_locked_by != user_id),

 -- Caps admin_locked_reason at 1000 characters to bound storage and prevent abuse.
 CONSTRAINT chk_us_admin_locked_reason_length
 CHECK (admin_locked_reason IS NULL OR length(admin_locked_reason) <= 1000)
);

COMMENT ON TABLE user_secrets IS
 'Security-sensitive fields for users. 1:1 with users (user_id PK).
 Kept in a separate table so SELECT * on users never accidentally includes
 password_hash, lock state, or brute-force counters in API responses.
 Must be created in the same transaction as the parent users row.';
COMMENT ON COLUMN user_secrets.password_hash IS
 'bcrypt hash ($2a/$2b, length 60). NULL for OAuth-only users. Never store plaintext.';
COMMENT ON COLUMN user_secrets.is_locked IS
 'Set by OTP brute-force exhaustion (LockAccount). Cleared by the account-unlock OTP flow or by admin action. Never self-clears.';
COMMENT ON COLUMN user_secrets.admin_locked IS
 'Set and cleared exclusively by admin action (RBAC). The user-facing OTP unlock flow must never touch this field. Independent of is_locked.';
COMMENT ON COLUMN user_secrets.admin_locked_by IS
 'UUID of the admin who set admin_locked.';
COMMENT ON COLUMN user_secrets.admin_locked_reason IS
 'Human-readable reason for the lock. Required when admin_locked = TRUE.';
COMMENT ON COLUMN user_secrets.admin_locked_at IS
 'Timestamp when admin_locked was set.';
COMMENT ON COLUMN user_secrets.login_locked_until IS
 'NULL = not locked by failed attempts. Account unlocks automatically on next login when NOW() > this value — no background job required.';
COMMENT ON COLUMN user_secrets.failed_login_attempts IS
 'Increment atomically via SQL arithmetic — never read-then-write or concurrent increments will be silently lost.';
COMMENT ON COLUMN user_secrets.failed_change_password_attempts IS
 'Increment atomically via SQL arithmetic — never read-then-write. Reset to 0 after every successful password change.';


/* ─────────────────────────────────────────────────────────────
 USER IDENTITIES (OAuth / External Auth)
 ───────────────────────────────────────────────────────────── */

/*
 * One row per linked external OAuth provider account.
 * A user may have at most one identity per provider (uq_identity_user_provider),
 * and a given provider UID maps to at most one internal user (uq_identity_provider_uid).
 *
 * Encrypted credentials (access_token, refresh_token_provider) are split into
 * user_identity_tokens so SELECT * here never returns ciphertext.
 */
CREATE TABLE user_identities (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Foreign key to users; CASCADE means all identities are removed when the user is purged.
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

 -- Which external provider this identity belongs to (google, telegram, etc.).
 provider auth_provider NOT NULL,

 -- The user's stable identifier within that provider (e.g. Google's 'sub' claim).
 provider_uid TEXT NOT NULL,

 -- Email address returned by the provider's userinfo endpoint. May differ from users.email.
 -- NULL if the provider did not return an email. Max 254 chars.
 provider_email TEXT,

 -- Display name from the provider's userinfo. Nullable, max 200 chars.
 display_name TEXT,

 -- Avatar URL from the provider. Nullable, max 2048 chars.
 avatar_url TEXT,

 -- Raw JSON blob from the provider's userinfo endpoint. Stored for debugging only;
 -- do not treat as authoritative profile data. May be NULL if the provider returned nothing.
 raw_profile JSONB,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- One identity per provider per user; prevents linking the same provider twice.
 CONSTRAINT uq_identity_user_provider UNIQUE (user_id, provider),

 -- Prevents two users from claiming the same external provider account.
 -- This UNIQUE constraint also creates the backing index for (provider, provider_uid) lookups.
 CONSTRAINT uq_identity_provider_uid UNIQUE (provider, provider_uid),

 CONSTRAINT chk_ui_provider_uid_length
 CHECK (length(provider_uid) <= 255),
 CONSTRAINT chk_ui_provider_email_length
 CHECK (provider_email IS NULL OR length(provider_email) <= 254),
 CONSTRAINT chk_ui_display_name_length
 CHECK (display_name IS NULL OR length(display_name) <= 200),
 CONSTRAINT chk_ui_avatar_url_length
 CHECK (avatar_url IS NULL OR length(avatar_url) <= 2048)
);

-- Supports "find all users with a Google account registered to email X" lookups,
-- used during OAuth login to detect existing accounts for linking.
-- Partial index: excludes rows without a provider_email to save space.
CREATE INDEX idx_ui_provider_email ON user_identities(provider, provider_email)
 WHERE provider_email IS NOT NULL;

COMMENT ON TABLE user_identities IS
 'OAuth / external auth identities. One row per (provider, external account).
 Encrypted credentials (access_token, refresh_token_provider) are split into
 user_identity_tokens so SELECT * never returns ciphertext.';
COMMENT ON COLUMN user_identities.raw_profile IS
 'Raw JSON blob from the provider''s userinfo endpoint. Stored for debugging only — do not treat
 as authoritative profile data. May be NULL if the provider returned no userinfo.';


/* ─────────────────────────────────────────────────────────────
 USER IDENTITY TOKENS
 ───────────────────────────────────────────────────────────── */

/*
 * Stores AES-256-GCM encrypted OAuth credentials for each identity.
 * Split from user_identities so SELECT * on that table never
 * returns ciphertext. Exactly 1:1 with user_identities (identity_id is PK).
 *
 * Both token columns MUST carry the 'enc:' prefix enforced by the CHECK constraints.
 * Any write that omits encryption will be rejected at the DB layer.
 */
CREATE TABLE user_identity_tokens (
 -- 1:1 with user_identities; cascades on identity deletion.
 identity_id UUID PRIMARY KEY REFERENCES user_identities(id) ON DELETE CASCADE,

 -- AES-256-GCM encrypted access token with 'enc:' prefix. NULL if not yet stored.
 access_token TEXT,

 -- Expiry time of the access_token. NULL when access_token is NULL or the provider
 -- does not return an expiry. Required whenever access_token is non-NULL.
 access_token_expires_at TIMESTAMPTZ,

 -- AES-256-GCM encrypted provider refresh token with 'enc:' prefix.
 -- NULL if the provider does not issue refresh tokens or the user has disconnected.
 refresh_token_provider TEXT,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Rejects any access_token that hasn't been encrypted (missing enc: prefix).
 CONSTRAINT chk_uit_access_token_encrypted
 CHECK (access_token IS NULL OR access_token LIKE 'enc:%'),

 -- Same guard for the provider refresh token.
 CONSTRAINT chk_uit_refresh_token_encrypted
 CHECK (refresh_token_provider IS NULL OR refresh_token_provider LIKE 'enc:%'),

 -- access_token and access_token_expires_at must be stored together or not at all.
 CONSTRAINT chk_uit_access_token_expiry_coherent
 CHECK (access_token IS NULL OR access_token_expires_at IS NOT NULL)
);

COMMENT ON TABLE user_identity_tokens IS
 'Encrypted OAuth credentials split from user_identities so SELECT * on
 user_identities never returns ciphertext. 1:1 with user_identities.';
COMMENT ON COLUMN user_identity_tokens.access_token IS
 'AES-256-GCM ciphertext with enc: prefix. chk_uit_access_token_encrypted rejects unencrypted values.';
COMMENT ON COLUMN user_identity_tokens.access_token_expires_at IS
 'NULL if the provider does not return an expiry or the token has not been stored yet.';
COMMENT ON COLUMN user_identity_tokens.refresh_token_provider IS
 'AES-256-GCM ciphertext with enc: prefix. NULL if the provider does not issue refresh tokens or the user disconnected.';


/* ─────────────────────────────────────────────────────────────
 ONE-TIME TOKENS
 ───────────────────────────────────────────────────────────── */

/*
 * Single-table store for all short-lived verification tokens across every email flow.
 *
 * Two credential shapes are supported, controlled by token_type:
 * token_hash — opaque random value hashed with SHA-256; used by magic_link.
 * code_hash — 6-digit OTP hashed with bcrypt; used by all other types.
 *
 * Both columns carry UNIQUE constraints so a hash collision (however unlikely)
 * cannot allow a foreign credential to consume this row.
 *
 * The metadata JSONB column carries type-specific auxiliary data; its expected
 * shape varies by token_type (see column comment below).
 *
 * Attempt tracking: max_attempts = 0 means no limit (non-OTP types).
 * OTP types (email_verification, password_reset, account_unlock, etc.) use 3.
 */
CREATE TABLE one_time_tokens (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Identifies the flow this token belongs to; governs which constraints apply.
 token_type one_time_token_type NOT NULL,

 -- Owner of this token; cascades on user deletion to prevent orphan tokens.
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

 -- The email address this token is addressed to. Always set; may differ from users.email
 -- during an in-progress email-change flow.
 email TEXT NOT NULL,

 -- Opaque random value hashed with SHA-256. Required for magic_link; optional for
 -- email_verification (which supports both magic-link and OTP paths).
 token_hash TEXT UNIQUE,

 -- bcrypt hash of the 6-digit OTP code. Required for all non-magic-link types.
 -- All bcrypt hashes contain '$', enforced by chk_ott_code_hash_salted.
 code_hash TEXT UNIQUE,

 -- Auxiliary JSONB payload whose structure depends on token_type.
 -- email_change_verify: {"new_email": "..."} so the confirm step knows the destination
 -- without a separate KV lookup. NULL for all other types.
 metadata JSONB,

 -- Current number of failed OTP attempts for this token.
 -- max_attempts = 0 disables the ceiling (non-OTP token types).
 -- Incremented atomically; never read-then-write.
 attempts SMALLINT NOT NULL DEFAULT 0,
 max_attempts SMALLINT NOT NULL DEFAULT 0,

 -- Timestamp of the most recent failed OTP attempt. Used for rate-limiting diagnostics.
 last_attempt_at TIMESTAMPTZ,

 -- For magic_link only: where to redirect the browser after successful verification.
 -- Must be an http:// or https:// URL; max 2048 chars.
 redirect_to TEXT,

 -- Hard expiry timestamp. The DB constraint caps the TTL per token type (15 min for OTPs).
 expires_at TIMESTAMPTZ NOT NULL,

 -- Set when the token is successfully consumed. Used_at IS NOT NULL = already used.
 used_at TIMESTAMPTZ,

 -- Client IP address recorded at token-creation time for audit purposes.
 ip_address INET,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- magic_link is the only type that requires token_hash. Written as != 'magic_link' so
 -- adding a new OTP type never requires updating this constraint.
 CONSTRAINT chk_ott_magic_link_requires_token_hash
 CHECK (token_type != 'magic_link' OR token_hash IS NOT NULL),

 -- email_verification supports both OTP (code_hash) and magic-link (token_hash) paths;
 -- at least one must be present so the token can be consumed.
 CONSTRAINT chk_ott_ev_at_least_one_path
 CHECK (token_type != 'email_verification' OR (token_hash IS NOT NULL OR code_hash IS NOT NULL)),

 -- code_hash is only valid for non-magic-link types.
 CONSTRAINT chk_ott_otp_fields_scoped
 CHECK (code_hash IS NULL OR token_type != 'magic_link'),

 -- All OTP-only types (everything except magic_link and email_verification) require code_hash.
 CONSTRAINT chk_ott_otp_types_require_code
 CHECK (
 token_type IN ('magic_link', 'email_verification')
 OR code_hash IS NOT NULL
 ),

 -- max_attempts is meaningless without a code_hash to count attempts against.
 CONSTRAINT chk_ott_max_attempts_scoped
 CHECK (code_hash IS NOT NULL OR max_attempts = 0),

 -- redirect_to is only meaningful in the magic_link flow.
 CONSTRAINT chk_ott_redirect_scoped
 CHECK (redirect_to IS NULL OR token_type = 'magic_link'),

 -- expires_at must be strictly after created_at to avoid immediately-expired tokens.
 CONSTRAINT chk_ott_expires_future
 CHECK (expires_at > created_at),

 -- A token cannot be marked as used before it was created.
 CONSTRAINT chk_ott_used_after_created
 CHECK (used_at IS NULL OR used_at >= created_at),

 -- Hard TTL caps prevent misconfigured application-layer TTL values from creating
 -- extremely long-lived tokens. Each type has an independently capped maximum.
 CONSTRAINT chk_ott_ev_ttl_max
 CHECK (token_type != 'email_verification'
 OR expires_at <= created_at + INTERVAL '15 minutes'),

 CONSTRAINT chk_ott_magic_link_ttl_max
 CHECK (token_type != 'magic_link' OR expires_at <= created_at + INTERVAL '1 hour'),

 CONSTRAINT chk_ott_au_ttl_max
 CHECK (token_type != 'account_unlock'
 OR expires_at <= created_at + INTERVAL '15 minutes'),

 CONSTRAINT chk_ott_ecv_ttl_max
 CHECK (token_type != 'email_change_verify'
 OR expires_at <= created_at + INTERVAL '15 minutes'),

 CONSTRAINT chk_ott_ecc_ttl_max
 CHECK (token_type != 'email_change_confirm'
 OR expires_at <= created_at + INTERVAL '15 minutes'),

 CONSTRAINT chk_ott_ad_ttl_max
 CHECK (token_type != 'account_deletion'
 OR expires_at <= created_at + INTERVAL '15 minutes'),

 CONSTRAINT chk_ott_attempts_non_negative
 CHECK (attempts >= 0),

 -- max_attempts = 0 disables this ceiling check for non-OTP tokens.
 CONSTRAINT chk_ott_attempts_not_exceed_max
 CHECK (attempts <= max_attempts OR max_attempts = 0),

 -- attempts can only be non-zero when there is a code_hash to count against.
 CONSTRAINT chk_ott_attempts_require_code
 CHECK (attempts = 0 OR code_hash IS NOT NULL),

 CONSTRAINT chk_ott_redirect_length
 CHECK (redirect_to IS NULL OR length(redirect_to) <= 2048),

 -- Rejects relative and non-HTTP redirect targets.
 CONSTRAINT chk_ott_redirect_scheme
 CHECK (redirect_to IS NULL OR redirect_to ~ '^https?://'),

 CONSTRAINT chk_ott_email_format
 CHECK (length(email) <= 254 AND email LIKE '%@%'),

 -- All bcrypt hashes contain '$'; this rejects accidentally bare or unsalted hashes.
 CONSTRAINT chk_ott_code_hash_salted
 CHECK (code_hash IS NULL OR code_hash LIKE '%$%')
);

-- Hot-path index for active-token lookups filtered by user and type.
-- Also covers expiry-based queries (e.g. "are there any live tokens for this user?").
CREATE INDEX idx_ott_active ON one_time_tokens(user_id, token_type, expires_at)
 WHERE used_at IS NULL;

-- Used by the periodic cleanup job to sweep expired but unconsumed tokens.
CREATE INDEX idx_ott_expires_at ON one_time_tokens(expires_at)
 WHERE used_at IS NULL;

-- Covers email-based OTP lookups (GetEmailVerificationToken, GetPasswordResetToken,
-- GetUnlockToken) which all filter on (email, token_type) and order by recency.
CREATE INDEX idx_ott_email_active
 ON one_time_tokens(email, token_type, created_at DESC)
 WHERE used_at IS NULL;

-- Prevents two concurrent ForgotPassword calls for the same user from issuing
-- two active reset tokens. Application maps the 23505 violation to ErrResetTokenCooldown.
CREATE UNIQUE INDEX idx_ott_password_reset_active
 ON one_time_tokens (user_id)
 WHERE token_type = 'password_reset' AND used_at IS NULL;

-- Prevents two concurrent email-change requests for the same user.
-- Application maps the 23505 violation to ErrEmailChangeCooldown.
CREATE UNIQUE INDEX idx_ott_email_change_verify_active
 ON one_time_tokens (user_id)
 WHERE token_type = 'email_change_verify' AND used_at IS NULL;

-- Prevents issuing a second confirm token before the first is consumed.
CREATE UNIQUE INDEX idx_ott_email_change_confirm_active
 ON one_time_tokens (user_id)
 WHERE token_type = 'email_change_confirm' AND used_at IS NULL;

-- Prevents issuing a duplicate account-deletion confirmation token.
CREATE UNIQUE INDEX idx_ott_account_deletion_active
 ON one_time_tokens (user_id)
 WHERE token_type = 'account_deletion' AND used_at IS NULL;

COMMENT ON TABLE one_time_tokens IS
 'Single-table token store for email_verification, password_reset, magic_link, account_unlock, email_change_verify, and email_change_confirm flows.';
COMMENT ON COLUMN one_time_tokens.max_attempts IS
 '0 = no attempt limit (non-OTP types). OTP types use 3.';
COMMENT ON COLUMN one_time_tokens.last_attempt_at IS
 'Timestamp of the last failed OTP attempt.';
COMMENT ON COLUMN one_time_tokens.metadata IS
 'Auxiliary payload keyed by token_type. email_change_verify stores {"new_email": "..."} so the confirm step can retrieve the destination address without a separate KV lookup.';


/* ─────────────────────────────────────────────────────────────
 USER SESSIONS
 ───────────────────────────────────────────────────────────── */

/*
 * Tracks individual login sessions for device-management UI and security audit.
 * Not used for token validation — that goes through refresh_tokens.
 *
 * A session is opened on login and closed (ended_at set) on logout, token revocation,
 * or admin forced-logout. Sessions may also be left open if the user simply stops
 * using the app without logging out.
 */
CREATE TABLE user_sessions (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Which user owns this session; cascades on user deletion.
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

 -- Provider used to initiate this session (email, google, etc.).
 -- NULL only for sessions migrated before provider tracking was introduced.
 auth_provider auth_provider,

 started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 last_active_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- NULL = still active. Set on logout, forced revocation, or session expiry.
 ended_at TIMESTAMPTZ,

 -- Browser/client identification for the active-devices list in account settings.
 user_agent TEXT,

 -- IP address at session start; used for security review.
 ip_address INET,

 -- Human-readable device label (e.g. "Chrome on MacBook"). Populated by the client.
 device_name TEXT,

 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- ended_at must be on or after the session start time.
 CONSTRAINT chk_us_ended_after_started
 CHECK (ended_at IS NULL OR ended_at >= started_at),

 -- Activity timestamp cannot precede the session's creation.
 CONSTRAINT chk_us_active_after_started
 CHECK (last_active_at >= started_at)
);

-- Speeds up "list all sessions for user X" queries (device management, forced-logout).
CREATE INDEX idx_us_user_id ON user_sessions(user_id);

-- Hot path for "list active sessions for user X, most recent first" (device management UI).
CREATE INDEX idx_us_active_recent ON user_sessions(user_id, last_active_at DESC)
 WHERE ended_at IS NULL;

-- Supports the cleanup job that removes very old ended sessions.
CREATE INDEX idx_us_ended_at ON user_sessions(ended_at)
 WHERE ended_at IS NOT NULL;

COMMENT ON TABLE user_sessions IS
 'Login sessions for active-device visibility and audit. Not used for token validation.';
COMMENT ON COLUMN user_sessions.auth_provider IS
 'Provider used to initiate this session. NULL only when the session was created
 before provider tracking was added (backfill path). New rows must always supply
 a non-NULL value. CreateUserSession enforces this via a non-nullable parameter.';


/* ─────────────────────────────────────────────────────────────
 REFRESH TOKENS
 ───────────────────────────────────────────────────────────── */

/*
 * Server-side ledger for JWT refresh tokens, implementing RFC 6819 §5.2.2.3
 * token rotation with family-based reuse detection.
 *
 * Every issued refresh token gets a row here. When a token is rotated, the old
 * row is revoked and a new one is inserted sharing the same family_id. If a
 * previously rotated (and thus already revoked) token is presented again, the
 * entire family is revoked — a signal that the original token was stolen.
 *
 * fn_revoke_token_family (trigger) performs the cascade revocation automatically.
 */
CREATE TABLE refresh_tokens (
 -- JWT ID; matches the 'jti' claim in the issued JWT.
 jti UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- Owner; cascades on user deletion so orphan tokens are never left behind.
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

 -- The session this token is bound to. SET NULL on session deletion (session is ended
 -- but the token record is preserved for the audit trail).
 session_id UUID REFERENCES user_sessions(id) ON DELETE SET NULL,

 -- UUID shared by all tokens in a rotation chain. fn_revoke_token_family uses this
 -- to cascade revocation across the entire chain when reuse is detected.
 family_id UUID NOT NULL DEFAULT gen_random_uuid(),

 -- jti of the token this one was rotated from. NULL for the root token of a new family.
 -- SET NULL on parent deletion (cleanup); the child token becomes a new root.
 parent_jti UUID REFERENCES refresh_tokens(jti) ON DELETE SET NULL,

 expires_at TIMESTAMPTZ NOT NULL,

 -- Non-NULL when this token has been voluntarily or forcibly revoked.
 revoked_at TIMESTAMPTZ,

 -- Human-readable revocation reason. Max 256 chars.
 -- Known values: logout, rotated, reuse_detected, pre_verification, session_revoked,
 -- session_expired, password_changed, forced_logout, family_revoked:<original_reason>.
 revoke_reason TEXT,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- expires_at must be strictly in the future relative to when the row was created.
 CONSTRAINT chk_rt_expires_future
 CHECK (expires_at > created_at),

 -- revoked_at temporal integrity is enforced by trigger trg_check_revoked_after_created
 -- rather than a CHECK constraint because CHECK would evaluate NOW() at INSERT time
 -- and could reject valid pre-revoked sibling tokens created in the same transaction.
 -- Max 256 chars; fn_revoke_token_family prepends 'family_revoked:' (15 chars).
 CONSTRAINT chk_rt_revoke_reason_length
 CHECK (revoke_reason IS NULL OR length(revoke_reason) <= 256),

 -- Prevents empty-string revoke_reason from silently bypassing IS NULL forensic queries.
 CONSTRAINT chk_rt_revoke_reason_nonempty
 CHECK (revoke_reason IS NULL OR length(trim(revoke_reason)) > 0)
);

-- Speeds up family cascade revocation: fn_revoke_token_family updates all non-revoked
-- siblings sharing the same family_id.
CREATE INDEX idx_rt_family_id ON refresh_tokens(family_id) WHERE revoked_at IS NULL;

-- Supports "list all tokens for user X" and mass-revocation on password change / logout.
CREATE INDEX idx_rt_user_id ON refresh_tokens(user_id);

-- Speeds up "end the session when all its tokens are gone" lookups.
CREATE INDEX idx_rt_session_id ON refresh_tokens(session_id) WHERE session_id IS NOT NULL;

-- Supports the cleanup job that removes expired non-revoked tokens from the ledger.
CREATE INDEX idx_rt_cleanup ON refresh_tokens(expires_at) WHERE revoked_at IS NULL;

COMMENT ON TABLE refresh_tokens IS
 'Server-side refresh token ledger with individual revocation and family-based reuse detection (RFC 6819 §5.2.2.3).';
COMMENT ON COLUMN refresh_tokens.revoke_reason IS
 'Max 256 chars. Known values: logout, rotated, reuse_detected, pre_verification, session_revoked, session_expired, password_changed, forced_logout, family_revoked:<reason>.';
COMMENT ON COLUMN refresh_tokens.family_id IS
 'UUID shared by all tokens in a rotation chain. Used by fn_revoke_token_family to cascade revocation on reuse detection (RFC 6819 §5.2.2.3).';
COMMENT ON COLUMN refresh_tokens.parent_jti IS
 'jti of the token this one was rotated from. NULL for the first token in a rotation family.';


/* ─────────────────────────────────────────────────────────────
 AUTH AUDIT LOG
 ───────────────────────────────────────────────────────────── */

/*
 * Immutable append-only log of all authentication security events.
 *
 * user_id references users(id) ON DELETE SET NULL — when a user is hard-purged
 * the audit trail is preserved with user_id = NULL rather than being cascaded away.
 * This satisfies compliance requirements that demand retention of security events
 * even after account deletion.
 *
 * Rows cannot be UPDATEd (trg_deny_audit_update enforces this).
 * Rows older than 90 days are swept by the retention job using idx_aal_cleanup (ASC).
 */
CREATE TABLE auth_audit_log (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

 -- NULL when the associated user has been hard-purged; the event is still retained.
 user_id UUID REFERENCES users(id) ON DELETE SET NULL,

 -- Free-text event type; TEXT rather than ENUM so new event codes need no migration
 -- on this large append-only table. Use constants from internal/audit/audit.go.
 -- Max 128 chars enforced by chk_aal_event_type_length.
 event_type TEXT NOT NULL,

 -- Which authentication provider the event relates to (may be NULL for system events).
 provider auth_provider,

 -- Client IP address at the time of the event.
 ip_address INET,

 -- Browser/client user-agent string for forensic analysis.
 user_agent TEXT,

 -- Arbitrary structured context for the event (e.g. failure reason, device fingerprint).
 metadata JSONB,

 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 CONSTRAINT chk_aal_event_type_length
 CHECK (length(event_type) <= 128)
);

-- Supports time-range queries on the full audit log (compliance reports, incident response).
CREATE INDEX idx_aal_created_at ON auth_audit_log(created_at DESC);

-- Speeds up per-user event history queries (account security page, admin investigation).
-- Partial index excludes NULL user_id rows (hard-purged accounts) from user-scoped queries.
CREATE INDEX idx_aal_user_recent ON auth_audit_log(user_id, created_at DESC)
 WHERE user_id IS NOT NULL;

-- Supports filtering by event type within a time range (e.g. "all login failures in the last hour").
CREATE INDEX idx_aal_event_recent ON auth_audit_log(event_type, created_at DESC);

-- ASC index for the retention sweep job: efficiently deletes rows older than the cutoff
-- using a range scan without sorting (DESC index is suboptimal for range deletion).
CREATE INDEX idx_aal_cleanup ON auth_audit_log(created_at ASC);

COMMENT ON TABLE auth_audit_log IS
 'Security event log. user_id is NULLed (not deleted) when the owning user is purged,
 preserving the audit trail post-deletion.
 Retention: rows older than 90 days are swept by the retention job.
 Sweep uses idx_aal_cleanup (ASC) for efficient range deletion.
 trg_deny_audit_update enforces immutability — existing rows may not be edited.';


/* ─────────────────────────────────────────────────────────────
 ACCOUNT PURGE LOG
 ───────────────────────────────────────────────────────────── */

/*
 * Permanent compliance record written just before a users row is hard-deleted.
 * user_id intentionally has NO FK to users — by the time this row is written
 * (or immediately after, in the same transaction), the users row is gone.
 * Rows in this table are append-only: all UPDATEs are blocked by trg_deny_purge_log_update.
 */
CREATE TABLE account_purge_log (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id UUID NOT NULL, -- no FK: the users row is deleted in the same transaction
 purged_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

 -- Required JSONB audit payload. Expected keys: purged_by (UUID of acting admin or system job),
 -- reason (string), anonymized_email (one-way hash for re-registration dedup).
 -- Minimum required: {"deleted_at": "<RFC3339>"}.
 metadata JSONB NOT NULL DEFAULT '{}'
);

-- Supports compliance lookups: "show all purge records for user UUID X".
CREATE INDEX idx_purge_log_user_id ON account_purge_log(user_id);

COMMENT ON TABLE account_purge_log IS
 'Permanent record of hard-purged accounts. user_id has no FK constraint '
 'because the users row is deleted before this record is written.';

COMMENT ON COLUMN account_purge_log.metadata IS
 'JSONB audit payload. Expected keys: purged_by (UUID of acting admin or system job),
 reason (string), anonymized_email (one-way hash of purged email for re-registration dedup).
 Minimum required: {"deleted_at": "<RFC3339>"}.';


/* ─────────────────────────────────────────────────────────────
 ACTIVE-RECORD VIEWS
 ───────────────────────────────────────────────────────────── */

-- Pre-filtered view that excludes soft-deleted users.
-- All application read paths should use this view instead of the base table to avoid
-- accidentally surfacing deleted accounts in API responses.
-- Exception: queries with special logic (e.g. GetUserForLogin, GetUserForDeletion)
-- must query the base table directly with their own predicate.
CREATE VIEW active_users AS
 SELECT * FROM users WHERE deleted_at IS NULL;

COMMENT ON VIEW active_users IS
 'Pre-filtered view of non-deleted users. Use this in all application read paths
 to avoid accidentally returning soft-deleted accounts.
 Queries with grace-period or special logic (GetUserForLogin, GetUserForDeletion)
 must use the base table directly with their own filter.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP VIEW IF EXISTS active_users;
DROP INDEX IF EXISTS idx_aal_cleanup;
DROP INDEX IF EXISTS idx_purge_log_user_id;
DROP TABLE IF EXISTS account_purge_log CASCADE;
DROP TABLE IF EXISTS auth_audit_log CASCADE;
DROP TABLE IF EXISTS refresh_tokens CASCADE;
DROP TABLE IF EXISTS user_sessions CASCADE;
DROP INDEX IF EXISTS idx_ott_account_deletion_active;
DROP INDEX IF EXISTS idx_ott_email_change_confirm_active;
DROP INDEX IF EXISTS idx_ott_email_change_verify_active;
DROP INDEX IF EXISTS idx_ott_password_reset_active;
DROP TABLE IF EXISTS one_time_tokens CASCADE;
DROP TABLE IF EXISTS user_identity_tokens CASCADE;
DROP TABLE IF EXISTS user_identities CASCADE;
DROP TABLE IF EXISTS user_secrets CASCADE;
DROP INDEX IF EXISTS idx_users_pending_deletion;
DROP INDEX IF EXISTS idx_users_username_active;
DROP INDEX IF EXISTS idx_users_email_active;
DROP TABLE IF EXISTS users CASCADE;

DROP TYPE IF EXISTS one_time_token_type CASCADE;
DROP TYPE IF EXISTS auth_provider RESTRICT;

-- +goose StatementEnd
