-- +goose Up
-- +goose StatementBegin

-- 001_core.sql — core auth schema: users, OAuth identities, one-time tokens,
-- sessions, refresh tokens, and the security audit log.
-- Depends on: nothing (first migration).


-- ------------------------------------------------------------
-- ENUMS
-- ------------------------------------------------------------

CREATE TYPE auth_provider AS ENUM (
    'email',
    'magic_link',
    'google',
    'telegram'
);

COMMENT ON TYPE auth_provider IS
    'Supported authentication providers. Add values with ALTER TYPE … ADD VALUE; never remove a value referenced by existing rows.';

CREATE TYPE one_time_token_type AS ENUM (
    'email_verification',
    'password_reset',
    'magic_link',
    'account_unlock'
);

COMMENT ON TYPE one_time_token_type IS
    'Discriminator for one_time_tokens. Add values with ALTER TYPE … ADD VALUE.';


-- ------------------------------------------------------------
-- USERS
-- ------------------------------------------------------------

CREATE TABLE users (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),

    email         TEXT,
    username      TEXT,
    display_name  TEXT,
    avatar_url    TEXT,

    password_hash TEXT,

    is_active      BOOLEAN     NOT NULL DEFAULT FALSE,
    email_verified BOOLEAN     NOT NULL DEFAULT FALSE,
    is_locked      BOOLEAN     NOT NULL DEFAULT FALSE,
    admin_locked   BOOLEAN     NOT NULL DEFAULT FALSE,

    failed_login_attempts          SMALLINT    NOT NULL DEFAULT 0,
    login_locked_until              TIMESTAMPTZ,

    -- Tracks consecutive wrong old-password submissions on POST /change-password.
    -- Once failed_change_password_attempts >= 5 the service returns ErrTooManyAttempts
    -- and redirects the user to the forgot-password flow.
    -- Reset to 0 after every successful password change.
    -- Independent of failed_login_attempts (different endpoint, different threat model).
    failed_change_password_attempts SMALLINT    NOT NULL DEFAULT 0,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ,

    -- DB-layer email guard catches writes that bypass the application (admin scripts, imports).
    CONSTRAINT chk_users_email_format
        CHECK (email IS NULL OR email ~* '^[^@\s]+@[^@\s]+\.[^@\s]+$'),

    CONSTRAINT chk_users_username_nonempty
        CHECK (username IS NULL OR length(trim(username)) > 0),

    -- Both '$2a' and '$2b' prefixes are valid bcrypt output from golang.org/x/crypto/bcrypt.
    -- Length 60 is bcrypt's fixed output length.
    CONSTRAINT chk_users_password_hash_format
        CHECK (password_hash IS NULL
               OR (length(password_hash) = 60 AND left(password_hash, 3) IN ('$2a', '$2b'))),

    CONSTRAINT chk_users_display_name_len
        CHECK (display_name IS NULL OR length(display_name) <= 200),
    CONSTRAINT chk_users_avatar_url_len
        CHECK (avatar_url IS NULL OR length(avatar_url) <= 2048),
    CONSTRAINT chk_users_email_length
        CHECK (email IS NULL OR length(email) <= 254),

    CONSTRAINT chk_users_login_attempts_non_negative
        CHECK (failed_login_attempts >= 0),

    CONSTRAINT chk_users_change_pw_attempts_non_negative
        CHECK (failed_change_password_attempts >= 0)

    -- Lock field semantics:
    --   is_locked    — set by OTP brute-force exhaustion (IncrementAttemptsTx / LockAccount).
    --                  Cleared by the user-facing account-unlock OTP flow or by admin action.
    --   admin_locked — set and cleared exclusively by admin action (RBAC).
    --                  The user-facing OTP unlock flow must never clear this field.
    -- Both fields are checked independently wherever an account-locked guard fires.
    -- No CHECK constraint excludes (is_active=TRUE, is_locked=TRUE) because LockAccount
    -- can fire on active accounts (e.g. during password-reset OTP exhaustion), and an
    -- admin may lock an active account intentionally via admin_locked.
);

CREATE UNIQUE INDEX idx_users_email    ON users(email)    WHERE email    IS NOT NULL;
CREATE UNIQUE INDEX idx_users_username ON users(username) WHERE username IS NOT NULL;

-- Covering index for the password-login lookup; password_hash excluded from INCLUDE
-- because bcrypt verification always fetches the full row anyway.
CREATE INDEX idx_users_email_pw ON users(email)
    INCLUDE (is_active, email_verified)
    WHERE email IS NOT NULL AND password_hash IS NOT NULL;

COMMENT ON TABLE  users IS
    'Core identity record. trg_require_auth_method enforces at least one auth method per row.';
COMMENT ON COLUMN users.password_hash IS
    'bcrypt hash ($2a/$2b, length 60). NULL for OAuth-only users. Never store plaintext.';
COMMENT ON COLUMN users.is_active IS
    'FALSE until email verification completes.';
COMMENT ON COLUMN users.is_locked IS
    'Set by OTP brute-force exhaustion (LockAccount). Cleared by the account-unlock OTP flow or by admin action. Never self-clears.';
COMMENT ON COLUMN users.admin_locked IS
    'Set and cleared exclusively by admin action (RBAC). The user-facing OTP unlock flow must never touch this field. Independent of is_locked.';


-- ------------------------------------------------------------
-- USER IDENTITIES (OAUTH / EXTERNAL AUTH)
-- ------------------------------------------------------------

CREATE TABLE user_identities (
    id               UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID          NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    provider         auth_provider NOT NULL,
    provider_uid     TEXT          NOT NULL,

    provider_email   TEXT,
    display_name     TEXT,
    avatar_url       TEXT,

    access_token             TEXT,
    access_token_expires_at  TIMESTAMPTZ,
    refresh_token_provider   TEXT,

    raw_profile      JSONB,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_identity_user_provider UNIQUE (user_id, provider),

    -- UNIQUE constraint auto-creates the backing index; a separate CREATE INDEX
    -- on (provider, provider_uid) would duplicate it.
    CONSTRAINT uq_identity_provider_uid  UNIQUE (provider, provider_uid),

    -- access_token is AES-256-GCM encrypted at rest; 'enc:' is the canonical prefix.
    CONSTRAINT chk_ui_access_token_encrypted
        CHECK (access_token IS NULL OR access_token LIKE 'enc:%')
);

-- uq_identity_user_provider already covers user_id lookups; no separate index needed.
-- uq_identity_provider_uid covers (provider, provider_uid) lookups automatically.
CREATE INDEX idx_ui_provider_email ON user_identities(provider, provider_email)
    WHERE provider_email IS NOT NULL;

COMMENT ON TABLE  user_identities IS
    'OAuth / external auth identities. One row per (provider, external account).';
COMMENT ON COLUMN user_identities.access_token IS
    'AES-256-GCM ciphertext with enc: prefix. chk_ui_access_token_encrypted rejects unencrypted values.';


-- ------------------------------------------------------------
-- ONE-TIME TOKENS
-- -------------------------------------------------------chk_ott_ev_ttl_max -----

CREATE TABLE one_time_tokens (
    id           UUID                 PRIMARY KEY DEFAULT gen_random_uuid(),
    token_type   one_time_token_type  NOT NULL,
    user_id      UUID                 NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email        TEXT                 NOT NULL,

    -- token_hash: opaque random value hashed with SHA-256, used by magic_link tokens.
    -- code_hash:  6-digit OTP hashed with bcrypt, used by email_verification /
    --             password_reset / account_unlock tokens.
    -- Both are UNIQUE: a hash collision would allow a foreign credential to consume this row.
    token_hash   TEXT        UNIQUE,
    code_hash    TEXT        UNIQUE,

    -- max_attempts = 0 means no attempt limit (non-OTP token types).
    -- OTP token types (email_verification, password_reset, account_unlock) use 3.
    attempts         SMALLINT    NOT NULL DEFAULT 0,
    max_attempts     SMALLINT    NOT NULL DEFAULT 0,
    last_attempt_at  TIMESTAMPTZ,

    redirect_to  TEXT,

    expires_at   TIMESTAMPTZ NOT NULL,
    used_at      TIMESTAMPTZ,
    ip_address   INET,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- magic_link is the only type that requires token_hash.
    CONSTRAINT chk_ott_magic_link_requires_token_hash
        CHECK (token_type IN ('email_verification', 'account_unlock', 'password_reset') OR token_hash IS NOT NULL),

    -- email_verification can be delivered via OTP (code_hash) or magic link (token_hash);
    -- at least one must be set so the token can actually be consumed.
    CONSTRAINT chk_ott_ev_at_least_one_path
        CHECK (token_type != 'email_verification' OR (token_hash IS NOT NULL OR code_hash IS NOT NULL)),

    -- code_hash is only valid for OTP token types.
    CONSTRAINT chk_ott_otp_fields_scoped
        CHECK (code_hash IS NULL OR token_type IN ('email_verification', 'account_unlock', 'password_reset')),

    -- max_attempts is meaningless without a code_hash to count against.
    CONSTRAINT chk_ott_max_attempts_scoped
        CHECK (code_hash IS NOT NULL OR max_attempts = 0),

    -- redirect_to is only meaningful for magic_link flows.
    CONSTRAINT chk_ott_redirect_scoped
        CHECK (redirect_to IS NULL OR token_type = 'magic_link'),

    CONSTRAINT chk_ott_expires_future
        CHECK (expires_at > created_at),

    CONSTRAINT chk_ott_used_after_created
        CHECK (used_at IS NULL OR used_at >= created_at),

    -- Hard TTL caps guard against misconfigured application-layer TTL values.
    CONSTRAINT chk_ott_ev_ttl_max
        CHECK (token_type != 'email_verification'
               OR expires_at <= created_at + INTERVAL '15 minutes'),

    CONSTRAINT chk_ott_magic_link_ttl_max
        CHECK (token_type != 'magic_link' OR expires_at <= created_at + INTERVAL '1 hour'),

    CONSTRAINT chk_ott_au_ttl_max
        CHECK (token_type != 'account_unlock'
               OR expires_at <= created_at + INTERVAL '15 minutes'),

    CONSTRAINT chk_ott_attempts_non_negative
        CHECK (attempts >= 0),

    -- max_attempts = 0 bypasses this check for non-OTP tokens.
    CONSTRAINT chk_ott_attempts_not_exceed_max
        CHECK (attempts <= max_attempts OR max_attempts = 0),

    CONSTRAINT chk_ott_attempts_require_code
        CHECK (attempts = 0 OR code_hash IS NOT NULL),

    CONSTRAINT chk_ott_redirect_length
        CHECK (redirect_to IS NULL OR length(redirect_to) <= 2048),

    CONSTRAINT chk_ott_redirect_scheme
        CHECK (redirect_to IS NULL OR redirect_to ~ '^https?://'),

    CONSTRAINT chk_ott_email_format
        CHECK (length(email) <= 254 AND email LIKE '%@%'),

    -- All code_hash values are bcrypt hashes, which always contain '$'.
    -- This rejects accidentally stored bare (unsalted) hashes.
    CONSTRAINT chk_ott_code_hash_salted
        CHECK (code_hash IS NULL OR code_hash LIKE '%$%')
);

-- Covers active-token lookups by (user_id, token_type) and expiry-based queries.
CREATE INDEX idx_ott_active ON one_time_tokens(user_id, token_type, expires_at)
    WHERE used_at IS NULL;

-- Used by the cleanup job to sweep expired unused tokens.
CREATE INDEX idx_ott_expires_at ON one_time_tokens(expires_at)
    WHERE used_at IS NULL;

-- Covers email-based OTP lookups (GetEmailVerificationToken, GetPasswordResetToken,
-- GetUnlockToken) which all filter on (email, token_type) and order by created_at DESC.
CREATE INDEX idx_ott_email_active
    ON one_time_tokens(email, token_type, created_at DESC)
    WHERE used_at IS NULL;

-- Prevents concurrent ForgotPassword calls from issuing two active tokens for the
-- same user. The application layer catches the 23505 / idx_password_reset_tokens_user_active
-- violation and treats it as a cooldown sentinel (ErrResetTokenCooldown).
CREATE UNIQUE INDEX idx_password_reset_tokens_user_active
    ON one_time_tokens (user_id)
    WHERE token_type = 'password_reset' AND used_at IS NULL;

COMMENT ON TABLE  one_time_tokens IS
    'Single-table token store for email_verification, password_reset, magic_link, and account_unlock flows.';
COMMENT ON COLUMN one_time_tokens.max_attempts IS
    '0 = no attempt limit (non-OTP types). OTP types use 3.';
COMMENT ON COLUMN one_time_tokens.last_attempt_at IS
    'Timestamp of the most recent failed OTP attempt.';


-- ------------------------------------------------------------
-- USER SESSIONS
-- ------------------------------------------------------------

CREATE TABLE user_sessions (
    id             UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID          NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    auth_provider  auth_provider,

    started_at     TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    last_active_at TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    ended_at       TIMESTAMPTZ,

    user_agent     TEXT,
    ip_address     INET,
    device_name    TEXT,

    CONSTRAINT chk_us_ended_after_started
        CHECK (ended_at IS NULL OR ended_at >= started_at),
    CONSTRAINT chk_us_active_after_started
        CHECK (last_active_at >= started_at)
);

CREATE INDEX idx_us_user_id       ON user_sessions(user_id);
CREATE INDEX idx_us_active_recent ON user_sessions(user_id, last_active_at DESC)
    WHERE ended_at IS NULL;
CREATE INDEX idx_us_ended_at      ON user_sessions(ended_at)
    WHERE ended_at IS NOT NULL;

COMMENT ON TABLE user_sessions IS
    'Login sessions for active-device visibility and audit. Not used for token validation.';


-- ------------------------------------------------------------
-- REFRESH TOKENS
-- ------------------------------------------------------------

CREATE TABLE refresh_tokens (
    jti        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),

    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id UUID        REFERENCES user_sessions(id) ON DELETE SET NULL,

    family_id  UUID        NOT NULL DEFAULT gen_random_uuid(),
    parent_jti UUID        REFERENCES refresh_tokens(jti) ON DELETE SET NULL,

    expires_at    TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ,
    revoke_reason TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_rt_expires_future
        CHECK (expires_at > created_at),

    -- revoked_at temporal integrity is a trigger (trg_check_revoked_after_created) rather
    -- than a CHECK constraint because a CHECK would reject valid INSERTs of pre-revoked
    -- sibling tokens where created_at defaults to NOW().
    -- revoke_reason length: 256 chars. fn_revoke_token_family prepends 'family_revoked:'
    -- (15 chars), leaving 241 chars for the original reason.
    CONSTRAINT chk_rt_revoke_reason_length
        CHECK (revoke_reason IS NULL OR length(revoke_reason) <= 256)
);

CREATE INDEX idx_rt_family_id  ON refresh_tokens(family_id)   WHERE revoked_at IS NULL;
CREATE INDEX idx_rt_user_id    ON refresh_tokens(user_id);
CREATE INDEX idx_rt_session_id ON refresh_tokens(session_id)  WHERE session_id IS NOT NULL;
CREATE INDEX idx_rt_cleanup    ON refresh_tokens(expires_at)  WHERE revoked_at IS NULL;

COMMENT ON TABLE  refresh_tokens IS
    'Server-side refresh token ledger with individual revocation and family-based reuse detection (RFC 6819 §5.2.2.3).';
COMMENT ON COLUMN refresh_tokens.revoke_reason IS
    'Max 256 chars. Known values: logout, rotated, reuse_detected, pre_verification, session_revoked, session_expired, password_changed, forced_logout, family_revoked:<reason>.';


-- ------------------------------------------------------------
-- AUTH AUDIT LOG
-- ------------------------------------------------------------

CREATE TABLE auth_audit_log (
    id         UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID          REFERENCES users(id) ON DELETE CASCADE,

    -- event_type is TEXT rather than an ENUM so new event types can be added without
    -- a table rewrite on this append-only table. Use constants from internal/audit/audit.go.
    event_type TEXT          NOT NULL,
    provider   auth_provider,
    ip_address INET,
    user_agent TEXT,
    metadata   JSONB,

    created_at TIMESTAMPTZ   NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_aal_event_type_length
        CHECK (length(event_type) <= 128)
);

-- idx_aal_user_recent covers both user_id filtering and recency ordering.
CREATE INDEX idx_aal_created_at  ON auth_audit_log(created_at DESC);
CREATE INDEX idx_aal_user_recent ON auth_audit_log(user_id, created_at DESC)
    WHERE user_id IS NOT NULL;
-- Composite index for incident response: filter by event type and time range.
CREATE INDEX idx_aal_event_recent ON auth_audit_log(event_type, created_at DESC);

COMMENT ON TABLE auth_audit_log IS
    'Security event log. Rows are deleted when the owning user is deleted (ON DELETE CASCADE) or by periodic retention sweeps. trg_deny_audit_update enforces immutability — existing rows may not be edited.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS auth_audit_log    CASCADE;
DROP TABLE IF EXISTS refresh_tokens    CASCADE;
DROP TABLE IF EXISTS user_sessions     CASCADE;
DROP INDEX IF EXISTS idx_password_reset_tokens_user_active;
DROP TABLE IF EXISTS one_time_tokens   CASCADE;
DROP TABLE IF EXISTS user_identities   CASCADE;
DROP TABLE IF EXISTS users             CASCADE;

DROP TYPE IF EXISTS one_time_token_type CASCADE;
DROP TYPE IF EXISTS auth_provider       RESTRICT;

-- +goose StatementEnd
