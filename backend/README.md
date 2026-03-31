# Store Backend

Go REST API for an e-commerce store. Provides a complete authentication and identity layer — email/password auth, OAuth (Google and Telegram), session management with refresh token rotation, full account lifecycle, profile management, and role-based access control.

📖 **[API Reference & Guides](https://store-e032ba24.mintlify.app/guides/overview)**

---

## Features

### Authentication

Full email/password auth with a security-first implementation:

- **Registration** — creates user + email verification token in a single transaction. Bcrypt is computed before the duplicate-email check to equalise timing between the known-email and unknown-email paths (anti-enumeration).
- **Email verification** — OTP-based verification with exponential backoff on wrong codes, a 2-minute resend cooldown, and idempotent silent no-ops for unknown/already-verified accounts.
- **Login** — identifier lookup + bcrypt verify, always in that order regardless of outcome so timing is indistinguishable. Guards check: time-locked → admin-locked → unverified → inactive. Consecutive wrong passwords increment a per-user failure counter; at 10 failures `login_locked_until` is set.
- **Account unlock** — two-step OTP flow (request → confirm) to self-service clear a login lockout. Admin-locked accounts cannot self-unlock. Suppresses silently for unknown, unverified, and already-unlocked accounts (anti-enumeration).
- **Password reset** — three-step flow: request OTP → verify code (receive grant token) → submit new password with grant token. The OTP is *not* consumed at the verify step — only at the reset step. A 60-second issuance cooldown prevents OTP flooding. bcrypt is pre-computed before opening any transaction to avoid holding a DB lock during key derivation.
- **Change password** — requires current password, rejects same-password reuse, revokes all sessions on success. A per-user attempt counter (backed by the IP rate limiter) protects against brute-force of the old password.
- **Session refresh** — refresh token rotation with reuse detection: presenting a revoked token immediately invalidates the entire token *family*, killing any sibling tokens an attacker may hold. Account lock state is re-checked on every rotation.
- **Logout** — idempotent; revokes the token and ends the session even if the client disconnects mid-request (`context.WithoutCancel`).

### OAuth

- **Google OAuth 2.0** — server-side PKCE flow using OIDC discovery. Supports login, registration, and account linking. Access tokens stored AES-256-GCM encrypted at rest. Unlink removes the identity record.
- **Telegram Login Widget** — HMAC-SHA256 verification of the widget payload (bot token as key). Supports login/register via the callback and post-auth account linking. Replays older than 24 hours are rejected.

### Profile Management

- **Get / update profile** — display name and avatar URL via `GET /me` and `PATCH /me`.
- **Username** — set or update a unique username (`PATCH /me/username`), with a public availability check endpoint (`GET /username/available`) for live frontend type-ahead.
- **Linked identities** — `GET /me/identities` lists all OAuth providers connected to the account.
- **Session management** — `GET /sessions` lists active sessions with device/IP metadata; `DELETE /sessions/{id}` revokes any single session.
- **Set password** — lets OAuth-only accounts add a password without going through the reset flow.
- **Email change** — three-step OTP flow: request (OTP to current email) → verify current (receive grant token) → confirm new (OTP to new address, commit change). The grant token prevents step 3 from being reached without completing step 2. The access token is invalidated after the change to force re-authentication.
- **Account deletion** — soft-delete with a 30-day cancellation window. The confirmation method is derived from the account's auth state: password accounts confirm with their password; email-only accounts use an OTP; Telegram-only accounts re-authenticate with a fresh widget payload. Deletion can be cancelled during the window.

### RBAC (Role-Based Access Control)

A full role/permission system with fine-grained access control:

- **Bootstrap** — one-time secret-gated endpoint to assign the first owner role. Enforces a single-owner invariant.
- **Permissions** — list all system permissions and their groups. Currently defined: `rbac:read`, `rbac:manage`, `rbac:grant_user_permission`, `job_queue:read/manage/configure`, `user:read/manage/lock`, `request:read/manage/approve`, `product:manage`.
- **Roles** — full CRUD: create, read, update, delete, list. Roles have a name, slug, and description.
- **Role permissions** — assign and remove permissions from roles.
- **User roles** — assign, remove, and query a user's role.
- **Access types** — permissions can have access type `direct` (immediate), `conditional` (subject to constraint checks), or `request` (requires an approval workflow via the `ApprovalGate` middleware).
- **Owner bypass** — the owner role has implicit access to all permissions, bypassing all checks.
- **Explicit deny** — a role grant can carry an explicit deny that overrides any allow.

### Bitcoin Monitoring

- **Address watch registration** — `POST /bitcoin/watch` stores the authenticated user's active watch set in Redis with a 30-minute inactivity window. Re-registering an existing address is idempotent and refreshes the TTL.
- **Live events** — `POST /bitcoin/events/token` + `GET /bitcoin/events` open an SSE stream for `new_block`, `pending_mempool`, `confirmed_tx`, and `mempool_replaced` events.
- **Durable tx-status read model** — `txstatus` owns `btc_tracked_transactions`, a SQL-backed read model for Bitcoin transaction tracking. Users create explicit tx watches through `/bitcoin/tx`, read them through `/bitcoin/tx/{id}`, and `events` upserts watch-discovered rows automatically when watched addresses receive transactions.
- **Durable confirmed fallback** — once a tracked transaction is confirmed, `events` saves its block hash and block height. Later `txstatus` lookups can reuse that saved block anchor to keep returning confirmed status even after the node no longer exposes the transaction through wallet RPC.

### Security Model

Security is treated as a first-class concern throughout:

- **Constant-time password checks** — bcrypt is always computed on both the found and not-found paths. Timing differences between "wrong password" and "unknown user" are eliminated.
- **Anti-enumeration** — all OTP issuance endpoints (`register`, `resend-verification`, `forgot-password`, `request-unlock`) return a uniform 202 for unknown/ineligible accounts. `GetDummyOTPHash()` is called on suppressed paths to equalise response latency.
- **Token rotation + reuse detection** — every refresh uses a new token (the old one is revoked atomically). A revoked token presented a second time triggers full family revocation (RFC 6819).
- **`context.WithoutCancel` for security writes** — failure counters, session revocations, audit rows, and token invalidations are always written with a detached context so a client-timed disconnect cannot abort them.
- **Audit log** — 40+ typed event constants covering every security-relevant action: register, login, logout, token refresh, OTP attempts, lock/unlock, password changes, OAuth events, session revocations, email changes, account deletion, and owner bootstrap. Events are written in the same transaction as the action that triggers them.
- **Rate limiting** — token-bucket rate limiting (IP-based and user-based) on every auth-adjacent endpoint. Backed by Redis (atomic Lua script for multi-instance correctness) with an in-memory fallback. Limits are chosen to allow legitimate use while preventing brute-force and flooding.
- **Trusted-proxy real IP** — `X-Forwarded-For` is only trusted from CIDR ranges configured in `TRUSTED_PROXIES`. Requests from untrusted peers use the TCP `RemoteAddr` so clients cannot spoof IPs to bypass rate limiting.
- **JWT separation** — access and refresh tokens use distinct HMAC-SHA256 secrets so a leaked access-token key cannot be used to forge refresh tokens.
- **OAuth token encryption** — provider access tokens are stored AES-256-GCM encrypted with a 32-byte key (`TOKEN_ENCRYPTION_KEY`). Losing the key makes all stored tokens unreadable.
- **Security headers** — `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and `Strict-Transport-Security` (when `HTTPS_ENABLED=true`) on all responses.
- **CORS** — restricted to `ALLOWED_ORIGINS` with `AllowCredentials: true` for the refresh cookie.

### Platform Packages

Shared infrastructure used across all domain packages:

| Package | Description |
|---|---|
| `platform/token` | JWT mint (access + refresh), validation middleware, cookie management, context helpers |
| `platform/ratelimit` | Token-bucket IP and user rate limiters; Redis atomic Lua path + in-memory fallback; `TrustedProxyRealIP` middleware; exponential-backoff limiter |
| `platform/mailer` | Async SMTP mail queue with worker goroutines; STARTTLS; HTML OTP templates (verification, password reset, unlock, email change, account deletion) |
| `platform/kvstore` | KV store abstraction over Redis and an in-memory store; `AtomicBucketStore` interface for the Redis atomic path |
| `platform/rbac` | `Checker` for DB-backed permission queries; `Require` and `ApprovalGate` middleware; permission constants; `AccessResult` context helpers |
| `platform/crypto` | AES-256-GCM encryption/decryption for OAuth access tokens |
| `platform/respond` | JSON response + error helpers |
| `audit` | Typed `EventType` constants for every audit log event |
| `worker` | Background job queue for purge tasks |

---

## Stack

- **Go** · chi router, pgx v5, JWT (golang-jwt), bcrypt, go-oidc v3, go-redis v9
- **PostgreSQL** · Goose migrations (8 files), SQLC-generated queries, pgTAP schema tests
- **Redis** · token-bucket rate limiting (atomic Lua), OTP/KV store, token blocklist, OAuth state
- **Docker** · Postgres + Redis via `docker-compose.yml`

---

## Quick Start

```bash
# 1. Copy env and fill in your values
make env-create

# 2. Start Docker services (Postgres + Redis)
make docker-up

# 3. Install tools, migrate, and generate code
make setup

# 4. Run the server
make run
```

Server starts on `ADDR` from `.env` (default `:8080`). For full environment setup see **[docs/SETUP.md](docs/SETUP.md)**.

---

## Common Commands

| Command | Description |
|---|---|
| `make run` | Build and run the API |
| `make test` | Unit tests (no DB) |
| `make test-integration` | Integration tests (requires DB) |
| `make test-all` | Unit + integration |
| `make test-coverage` | HTML coverage report |
| `make migrate-up` | Apply pending migrations |
| `make sqlc-generate` | Regenerate DB code from SQL |
| `make lint` | Run golangci-lint |
| `make e2e` | Run Newman e2e suites |

Run `make help` for the full list.

---

## Project Structure

```
cmd/api/              # Entrypoint (main.go)
internal/
  app/                # Dependency wiring (Deps struct)
  audit/              # Typed audit event constants (40+ events)
  config/             # Env-based config loading + validation
  db/                 # SQLC-generated queries and models
  domain/
    auth/
      login/          # POST /auth/login
      register/       # POST /auth/register
      session/        # POST /auth/refresh, POST /auth/logout
      verification/   # POST /auth/verify-email, /resend-verification
      unlock/         # POST /auth/request-unlock, /confirm-unlock
      password/       # POST /auth/forgot-password, /verify-reset-code, /reset-password, /change-password
      shared/         # OTP helpers, bcrypt, error sentinels, test utilities
    oauth/
      google/         # GET /oauth/google, /callback; DELETE /oauth/google/unlink
      telegram/       # POST /oauth/telegram/callback, /link; DELETE /unlink
      shared/         # OAuth models, store interface, test utilities
    profile/
      me/             # GET/PATCH /me, GET /me/identities
      username/       # GET /username/available, PATCH /me/username
      session/        # GET /sessions, DELETE /sessions/{id}
      set-password/   # POST /set-password
      email/          # POST /me/email/request-change, /verify-current, /confirm-change
      delete-account/ # GET /me/deletion-method, DELETE /me, POST /me/cancel-deletion
      shared/         # Profile error sentinels, shared store
    rbac/
      bootstrap/      # POST /owner/bootstrap
      permissions/    # GET /admin/permissions, /permissions/groups
      roles/          # CRUD + permission assignment on /admin/rbac/roles
      userroles/      # GET/PUT/DELETE /admin/rbac/users/{id}/role
      shared/         # RBAC error sentinels, store, test utilities
    bitcoin/
      block/          # GET /bitcoin/block/{hash}
      watch/          # POST /bitcoin/watch
      events/         # POST /bitcoin/events/token, GET /bitcoin/events, GET /bitcoin/status
      txstatus/       # GET /bitcoin/tx/*/status + CRUD on /bitcoin/tx and /bitcoin/tx/{id}
  platform/
    crypto/           # AES-256-GCM encrypt/decrypt for OAuth tokens
    kvstore/          # Redis + in-memory KV store; AtomicBucketStore interface
    mailer/           # Async SMTP queue, STARTTLS, OTP HTML templates
    ratelimit/        # Token-bucket IP/user limiters, TrustedProxyRealIP
    rbac/             # Checker, Require/ApprovalGate middleware, permission constants
    respond/          # JSON response + error helpers
    token/            # JWT mint/parse, cookie management, Auth middleware, context
  server/             # HTTP server + global middleware + route assembly
  worker/             # Background purge job queue
sql/
  schema/             # 8 Goose migration files (core, RBAC, requests, job queue)
  queries/            # SQLC SQL source files
  queries_test/       # Test-only SQL fixtures
  seeds/              # Seed data (roles, permissions, role-permission assignments)
make/                 # Modular Makefile includes
e2e/                  # Newman (Postman) collections per domain
docs/
  SETUP.md            # Full environment setup guide
  RULES.md            # Architecture rules and coding conventions
  rules/              # Domain-specific rules (auth, rbac)
  map/                # Implementation checklist and backlog (INCOMING.md)
mint/                 # Mintlify API reference docs and guides
  api-reference/      # Per-endpoint MDX files
  guides/             # Flow guides (registration, session lifecycle, OAuth, etc.)
```

---

## API Routes

Base path: `/api/v1`. All request bodies require `Content-Type: application/json` unless noted.

**Legend:** 🔒 requires JWT access token · 🛡️ requires RBAC permission

### Health

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Server liveness check |

### Auth — `/api/v1/auth`

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/auth/register` | — | Create account; sends verification OTP |
| `POST` | `/auth/verify-email` | — | Verify email address with OTP |
| `POST` | `/auth/resend-verification` | — | Re-issue email verification OTP |
| `POST` | `/auth/login` | — | Authenticate; returns access token + sets refresh cookie |
| `POST` | `/auth/refresh` | — | Rotate refresh token; issue new access token |
| `POST` | `/auth/logout` | — | Revoke refresh token + end session |
| `POST` | `/auth/request-unlock` | — | Request account-unlock OTP (locked accounts only) |
| `POST` | `/auth/confirm-unlock` | — | Confirm unlock OTP; clears login lockout |
| `POST` | `/auth/forgot-password` | — | Request password-reset OTP |
| `POST` | `/auth/verify-reset-code` | — | Verify reset OTP; receive short-lived grant token |
| `POST` | `/auth/reset-password` | — | Reset password using grant token |
| `POST` | `/auth/change-password` | 🔒 | Change password (requires current password) |

### OAuth — `/api/v1/oauth`

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/oauth/google` | — | Initiate Google OAuth 2.0 flow (browser redirect) |
| `GET` | `/oauth/google/callback` | — | Google OAuth callback; redirects to frontend |
| `DELETE` | `/oauth/google/unlink` | 🔒 | Remove linked Google identity |
| `POST` | `/oauth/telegram/callback` | — | Telegram Login Widget callback; login or register |
| `POST` | `/oauth/telegram/link` | 🔒 | Link Telegram identity to authenticated account |
| `DELETE` | `/oauth/telegram/unlink` | 🔒 | Remove linked Telegram identity |

### Profile — `/api/v1/profile`

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/profile/me` | 🔒 | Get authenticated user's profile |
| `PATCH` | `/profile/me` | 🔒 | Update display name and/or avatar URL |
| `GET` | `/profile/me/identities` | 🔒 | List linked OAuth identities |
| `GET` | `/profile/sessions` | 🔒 | List active sessions |
| `DELETE` | `/profile/sessions/{id}` | 🔒 | Revoke a specific session |
| `GET` | `/profile/username/available` | — | Check if a username is unclaimed |
| `PATCH` | `/profile/me/username` | 🔒 | Set or update username |
| `POST` | `/profile/set-password` | 🔒 | Add a password to an OAuth-only account |
| `POST` | `/profile/me/email/request-change` | 🔒 | Step 1 — send OTP to current email address |
| `POST` | `/profile/me/email/verify-current` | 🔒 | Step 2 — verify current-email OTP; receive grant token |
| `POST` | `/profile/me/email/confirm-change` | 🔒 | Step 3 — verify new-email OTP; commit change |
| `GET` | `/profile/me/deletion-method` | 🔒 | Get required confirmation method for account deletion |
| `DELETE` | `/profile/me` | 🔒 | Schedule account deletion (30-day soft-delete) |
| `POST` | `/profile/me/cancel-deletion` | 🔒 | Cancel a pending account deletion |

### RBAC — `/api/v1`

#### Owner

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/owner/bootstrap` | 🔒 | Assign owner role (one-time, requires `BOOTSTRAP_SECRET`) |

#### Admin

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/admin/permissions` | 🔒🛡️ `rbac:read` | List all system permissions |
| `GET` | `/admin/permissions/groups` | 🔒🛡️ `rbac:read` | List permissions grouped by category |
| `GET` | `/admin/rbac/roles` | 🔒🛡️ `rbac:read` | List all roles |
| `POST` | `/admin/rbac/roles` | 🔒🛡️ `rbac:manage` | Create a role |
| `GET` | `/admin/rbac/roles/{id}` | 🔒🛡️ `rbac:read` | Get a role by ID |
| `PATCH` | `/admin/rbac/roles/{id}` | 🔒🛡️ `rbac:manage` | Update a role |
| `DELETE` | `/admin/rbac/roles/{id}` | 🔒🛡️ `rbac:manage` | Delete a role |
| `GET` | `/admin/rbac/roles/{id}/permissions` | 🔒🛡️ `rbac:read` | List permissions on a role |
| `POST` | `/admin/rbac/roles/{id}/permissions` | 🔒🛡️ `rbac:manage` | Add a permission to a role |
| `DELETE` | `/admin/rbac/roles/{id}/permissions/{perm_id}` | 🔒🛡️ `rbac:manage` | Remove a permission from a role |
| `GET` | `/admin/rbac/users/{user_id}/role` | 🔒🛡️ `rbac:read` | Get a user's assigned role |
| `PUT` | `/admin/rbac/users/{user_id}/role` | 🔒🛡️ `rbac:manage` | Assign a role to a user |
| `DELETE` | `/admin/rbac/users/{user_id}/role` | 🔒🛡️ `rbac:manage` | Remove a user's role |

### Bitcoin — `/api/v1/bitcoin`

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/bitcoin/watch` | 🔒 | Register addresses for watch/event tracking |
| `POST` | `/bitcoin/events/token` | 🔒 | Mint a short-lived SSE token for the current session |
| `GET` | `/bitcoin/events` | 🔒 | Open the Bitcoin SSE stream |
| `GET` | `/bitcoin/status` | 🔒 | Read Bitcoin event-stream health |
| `GET` | `/bitcoin/block/{hash}` | 🔒 | Fetch one block by hash |
| `POST` | `/bitcoin/tx` | 🔒 | Create a durable tx-status tracking row |
| `GET` | `/bitcoin/tx` | 🔒 | List durable tx-status rows for the caller |
| `GET` | `/bitcoin/tx/{id}` | 🔒 | Get one durable tx-status row |
| `PUT` | `/bitcoin/tx/{id}` | 🔒 | Update one explicit txid-tracking row |
| `DELETE` | `/bitcoin/tx/{id}` | 🔒 | Delete one durable tx-status row |

---

## Rate Limits

Every auth-adjacent route is individually rate-limited. Key limits at a glance:

| Route | Limit |
|---|---|
| `POST /auth/register` | 5 req / 10 min per IP |
| `POST /auth/login` | 12 req / 15 min per IP |
| `POST /auth/refresh` | 5 req / 15 min per IP |
| `POST /auth/logout` | 5 req / 1 min per IP |
| `POST /auth/verify-email` | 5 req / 10 min per IP |
| `POST /auth/resend-verification` | 3 req / 10 min per IP |
| `POST /auth/forgot-password` | 3 req / 10 min per IP |
| `POST /auth/reset-password` | 5 req / 10 min per IP |
| `POST /auth/change-password` | 5 req / 15 min per IP |
| `POST /auth/request-unlock` | 3 req / 10 min per IP (shared with confirm) |
| `GET /oauth/google` (initiate) | 20 req / 5 min per IP |
| `POST /oauth/telegram/callback` | 10 req / 1 min per IP |
| `PATCH /me/username` | 5 req / 10 min per user |
| `POST /me/email/request-change` | 3 req / 10 min per user |
| `DELETE /me` (account deletion) | 10 req / 1 hr per user |
| `GET /health` | 3 req / 1 min per IP |

Responses to rate-limited requests include a `Retry-After` header (seconds until the next token is available).

---

## Environment

See **[docs/SETUP.md](docs/SETUP.md)** for the complete environment setup guide covering PostgreSQL, Redis, JWT secrets, SMTP, Google OAuth, and Telegram OAuth.

Minimum variables to boot:

| Variable | Purpose |
|---|---|
| `DATABASE_URL` / `TEST_DATABASE_URL` | PostgreSQL connection strings |
| `REDIS_URL` / `TEST_REDIS_URL` | Redis connection strings |
| `JWT_ACCESS_SECRET` | HMAC-SHA256 signing key for access tokens (`openssl rand -hex 32`) |
| `JWT_REFRESH_SECRET` | HMAC-SHA256 signing key for refresh tokens (separate key) |
| `TOKEN_ENCRYPTION_KEY` | 32-byte AES-256 key for OAuth token storage |
| `SMTP_*` | Transactional email (host, port, username, password, from) |
| `GOOGLE_CLIENT_ID` + `GOOGLE_CLIENT_SECRET` + `GOOGLE_REDIRECT_URI` | Google OAuth |
| `TELEGRAM_BOT_TOKEN` | Telegram Login Widget HMAC verification |
| `ALLOWED_ORIGINS` | CORS allowlist |
| `BOOTSTRAP_SECRET` | One-time RBAC owner bootstrap secret |
