# Auth System — Remaining Routes to Implement

Routes **not yet in** `E2E_CHECKLIST.md`. Everything here needs to be designed,
implemented, and then get its own E2E section before being marked production-ready.

**Legend**
- `[ ]` — not yet started
- `[~]` — in progress
- `[x]` — implemented (move to E2E_CHECKLIST.md when done)

---

## Implementation Order

Routes are sequenced so that every item builds only on what is already
working. Auth domain routes come before the admin domain. Within auth,
simpler additions to existing packages come before new packages. Schema
migrations are called out explicitly at the point they become required.

```
internal/domain/
│
├── auth/
│   │
│   └── oauth/
│     ├── google/    ← GET     /oauth/google              §D-1  (new package, external OIDC)
│       │              ← GET     /oauth/google/callback
│       │              ← POST    /oauth/google/link
│       │              ← DELETE  /oauth/google/unlink
│       └── telegram/  ← POST    /oauth/telegram/callback   §D-2  (new package, HMAC-only)
│                      ← POST    /oauth/telegram/link
│                      ← DELETE  /oauth/telegram/unlink
│
│   ├── profile/       
│   │   ├── username/   ← GET    /username/available        §B-1  (new package, no auth)
│   │   │               ← PATCH  /me/username
│   │   ├── me/         ← GET    /me/identities             §E-1  (extends existing, requires OAuth)
│   │   ├── email/      ← POST   /me/email/request-change   §B-2  (new package)
│   │   │               ← POST   /me/email/confirm-change
│   │   ├── profile/    ← DELETE /me                        §B-3  (extends existing, schema decision)
│   │   └── magiclink/  ← GET    /magic-link/verify         §F-5  (new package, paired with admin recovery)
│   │
├── owner/              ← all owner routes                  §F-*  (new top-level domain)
│   ├── bootstrap/      ← POST   /owner/bootstrap           §C-1  (new package, needs roles seed)
│   │
└── admin/              ← all admin routes                  §F-*  (new top-level domain)
    ├── users/
    ├── audit/
    ├── sessions/
    ├── lock/
    └── recovery/
```

---

## Group A — Profile Mangement

### §B-2 — Email Change Flow

New package: `internal/domain/profile/email/`

Three-step flow: prove ownership of the **current** address first, then prove
ownership of the **new** address before the change is applied.

#### Step 1 — Request change (requires auth)
`POST /api/v1/profile/me/email/request-change`
- [ ] Requires valid JWT
- [ ] Body: `{ "new_email": "..." }`
- [ ] Validate `new_email` format + uniqueness against `users`
- [ ] Sends OTP to the **current email** (proves the requester controls the account)
- [ ] Cooldown guard: suppress duplicate OTPs within 2 min
- [ ] Stores `new_email` in token `metadata` (token_type `email_change_verify`)
- [ ] Audit row: `email_change_requested`
- [ ] Rate-limit: 3 req / 10 min per user (key `echg:usr:`)

#### Step 2 — Verify current email (requires auth)
`POST /api/v1/profile/me/email/verify-current`
- [ ] Requires valid JWT
- [ ] Body: `{ "code": "123456" }`
- [ ] Validates OTP against the active `email_change_verify` token for this user
- [ ] Marks token consumed
- [ ] Issues a short-lived grant token (KV, 10 min TTL) encoding `new_email`
- [ ] Response: `{ "grant_token": "...", "expires_in": 600 }` — client holds this for step 3
- [ ] Sends OTP to the **new email** (proves ownership of the destination)
- [ ] Audit row: `email_change_current_verified`
- [ ] Rate-limit: 5 req / 15 min per user (key `echg:usr:vfy:`)

#### Step 3 — Confirm new email (requires auth)
`POST /api/v1/profile/me/email/confirm-change`
- [ ] Requires valid JWT
- [ ] Body: `{ "grant_token": "...", "code": "123456" }`
- [ ] Validates `grant_token` (must not be expired or already used)
- [ ] Validates OTP sent to the new email in step 2
- [ ] Atomically: updates `email` on `users`, marks OTP consumed, deletes grant token
- [ ] Re-check uniqueness inside the DB transaction
- [ ] Revokes all active refresh tokens (email is primary identifier)
- [ ] Blocklists current access token
- [ ] Sends confirmation notice to the **old email**
- [ ] Audit row: `email_changed` (old + new email in `metadata`)
- [ ] Rate-limit: 5 req / 15 min per user (key `echg:usr:cnf:`)

---

### §B-3 — Delete Account

`DELETE /api/v1/profile/me` — extends `internal/domain/profile/me`

**Schema decision required before implementing** (choose one and document it):
- **Soft-delete** (recommended): `deleted_at TIMESTAMPTZ` on `users`; filter in
  all queries; 30-day grace period.
- **Hard-delete**: CASCADE handles most child rows, but RBAC audit tables
  (`user_roles_audit`, `user_permissions_audit`, `role_permissions_audit`)
  use ON DELETE RESTRICT — ensure those constraints are handled first.

- [ ] Requires valid JWT
- [ ] Confirmation strategy (choose and document):
  - Password users: body `{ "password": "..." }` — verify before proceeding
  - OAuth-only users: OTP to their address as confirmation
- [ ] Revokes all refresh tokens, ends all sessions
- [ ] Blocklists current access token
- [ ] Clears the refresh cookie
- [ ] Audit row: `account_deleted` (written before the row is removed)
- [ ] Rate-limit: 3 req / 1 hour per user (key `del:usr:`)

---

## Group D — OAuth (external OIDC / widget integrations)

Both OAuth providers share the same `user_identities` table but have different
auth mechanisms (PKCE + ID token vs HMAC widget). Implement Google first because
it is more conventional and will establish the shared patterns for the `oauth/`
sub-package structure.

---

### §D-1 — OAuth — Google

New package: `internal/domain/auth/oauth/google/`

`GET /api/v1/auth/oauth/google`
- [ ] Generates `state` (CSRF token, short-lived KV entry)
- [ ] Generates PKCE `code_verifier` / `code_challenge`
- [ ] Redirects to Google authorization endpoint with `state`, `code_challenge`,
      scopes (`openid email profile`)

`GET /api/v1/auth/oauth/google/callback`
- [ ] Validates `state` (CSRF check)
- [ ] Exchanges `code` for tokens; verifies ID token (signature, `aud`, `exp`)
- [ ] **New user**: creates `users` row (`email_verified = TRUE`), creates
      `user_identities` row, issues session + token pair
- [ ] **Existing user**: refreshes identity data, issues new session
- [ ] Stores encrypted `access_token` in `user_identities` (`enc:` prefix required)
- [ ] Audit row: `oauth_login` with `provider = 'google'`
- [ ] Failure: redirect to frontend error page; never expose raw Google errors

`POST /api/v1/auth/oauth/google/link`
- [ ] Requires valid JWT
- [ ] Guard: `provider_uid` must not already be linked to a different user (409)
- [ ] Inserts/upserts `user_identities` row for the authenticated user
- [ ] Audit row: `oauth_linked` with `provider = 'google'`

`DELETE /api/v1/auth/oauth/google/unlink`
- [ ] Requires valid JWT
- [ ] Guard: user must have at least one other auth method (422 `last_auth_method`)
- [ ] Deletes `user_identities` row for `(user_id, 'google')`
- [ ] Audit row: `oauth_unlinked` with `provider = 'google'`
- [ ] Rate-limit: 5 req / 15 min per user (key `unl:usr:`)

---

### §D-2 — OAuth — Telegram

New package: `internal/domain/auth/oauth/telegram/`

`POST /api/v1/auth/oauth/telegram/callback`
- [ ] Verifies HMAC-SHA256: `HMAC_SHA256(SHA256(BOT_TOKEN), data_check_string)`
- [ ] Rejects if `auth_date` > 86400 seconds old (replay protection)
- [ ] Same new-user / existing-user paths as Google callback
- [ ] `provider_email` will be NULL (Telegram does not provide email)
- [ ] Audit row: `oauth_login` with `provider = 'telegram'`

`POST /api/v1/auth/oauth/telegram/link`
- [ ] Requires valid JWT
- [ ] Same HMAC + `auth_date` checks as callback
- [ ] Same duplicate-provider guard as Google link (409)
- [ ] Audit row: `oauth_linked` with `provider = 'telegram'`

`DELETE /api/v1/auth/oauth/telegram/unlink`
- [ ] Requires valid JWT
- [ ] Same last-auth-method guard
- [ ] Audit row: `oauth_unlinked` with `provider = 'telegram'`

---

## Group E — Post-OAuth user routes

These routes depend on OAuth infrastructure being live. Do not start until at
least one OAuth provider (§D-1) is implemented and `user_identities` is populated.

---

### §E-1 — Linked Accounts

`GET /api/v1/profile/me/identities` — extends `internal/domain/profile/me`

- [ ] Requires valid JWT
- [ ] Returns all rows in `user_identities` for the authenticated user
- [ ] Response shape per identity: `provider`, `provider_email`, `display_name`,
      `avatar_url`, `created_at` — **never** return `access_token` or `refresh_token_provider`
- [ ] Rate-limit: 20 req / 1 min per IP (key `ident:ip:`)

---

## Group F — Admin Domain (new top-level domain)

`internal/domain/admin/` follows the identical three-layer layout as auth.
The owner middleware lives in `admin/shared/` and is imported only by
`admin/*/routes.go` — never by any `auth/` package.

Until the RBAC system is live, protect all admin routes with middleware that
checks `user_roles.role_id` resolves to a role where `is_owner_role = TRUE`.

___

New package: `internal/domain/admin/bootstrap/`

`POST /api/v1/admin/bootstrap`
- [ ] **Environment-gated**: only callable when `OWNER_BOOTSTRAP_SECRET` is set;
      returns 404 when absent
- [ ] Body: `{ "secret": "...", "email": "...", "password": "...", "display_name": "..." }`
- [ ] Guard: fails with 409 if any user with `is_owner_role = TRUE` already exists
- [ ] Creates user row + verifies email immediately (`email_verified = TRUE`, `is_active = TRUE`)
- [ ] Looks up the role where `is_owner_role = TRUE` in `roles`; inserts into `user_roles`
      with `granted_by` = the newly created owner's own UUID (self-granting — document this)
- [ ] Audit row: `owner_bootstrapped`
- [ ] No rate-limit (env-var gate is sufficient)

---

### §F-1 — User Listing and Detail

`GET /api/v1/admin/users`
- [ ] Paginated (cursor-based)
- [ ] Filters: `is_locked`, `admin_locked`, `is_active`, `email_verified`,
      `created_after`, `search` (partial email or username match)
- [ ] Never return `password_hash`

`GET /api/v1/admin/users/{id}`
- [ ] Full profile: all `users` columns except `password_hash`
- [ ] Includes current role from `user_roles`
- [ ] Includes counts: `session_count`, `recent_failed_logins`

---

### §F-2 — User Audit Log

`GET /api/v1/admin/users/{id}/audit`
- [ ] Paginated rows from `auth_audit_log` filtered by `user_id`
- [ ] Query params: `limit` (default 50, max 200), `cursor`, `event_type`, `from`, `to`
- [ ] Masks sensitive `metadata` fields before returning
- [ ] Rate-limit: 30 req / 1 min per admin (key `aaud:usr:`)

`GET /api/v1/admin/audit`
- [ ] Global audit log — same schema, no `user_id` filter
- [ ] Additional filters: `ip_address`, `provider`
- [ ] Rate-limit: 10 req / 1 min per admin (key `gaud:usr:`)

---

### §F-3 — Session Administration

`GET /api/v1/admin/users/{id}/sessions`
- [ ] Returns all active sessions for any user

`DELETE /api/v1/admin/users/{id}/sessions`
- [ ] Force-revokes all active sessions; revokes all refresh tokens (`forced_logout`)
- [ ] Audit row: `forced_logout` on target user

`DELETE /api/v1/admin/users/{id}/sessions/{session_id}`
- [ ] Force-revoke a single session
- [ ] Audit row: `session_force_revoked`

---

### §F-4 — Lock / Unlock (admin_locked field)

> **Doc TODO when implemented:** Update
> `mint/api-reference/auth/unlock/request-unlock.mdx` and
> `mint/api-reference/auth/unlock/confirm-unlock.mdx` — both reference this
> admin endpoint as "planned". Remove the qualifier and confirm behaviour is accurate.

`PATCH /api/v1/admin/users/{id}/lock`
- [ ] Sets `admin_locked = TRUE`
- [ ] Body: `{ "reason": "..." }`
- [ ] Immediately force-revokes all sessions and refresh tokens
- [ ] Cannot lock another owner (check target role)
- [ ] Audit row: `admin_lock_applied` on target; `admin_action` on acting admin

`PATCH /api/v1/admin/users/{id}/unlock`
- [ ] Clears `admin_locked = FALSE` (does NOT touch `is_locked` or
      `login_locked_until` — those are the user-facing OTP unlock flow)
- [ ] Body: `{ "reason": "..." }`
- [ ] Audit row: `admin_lock_removed`

---

### §F-5 — CS-Assisted Account Recovery

These three admin routes and the user-facing magic-link verify endpoint form a
single recovery workflow. The admin issues the magic link or forces a reset; the
user-facing route completes the handshake. Implement them together.

`PATCH /api/v1/admin/users/{id}/email`
- [ ] Body: `{ "new_email": "...", "reason": "ticket:#1234" }` — reason required
- [ ] Validates `new_email` format + uniqueness
- [ ] Notifies old email; confirms to new email
- [ ] Revokes all refresh tokens (`email_changed_by_admin`); blocklists access tokens
- [ ] Cannot no-op (guard if email already matches)
- [ ] Cannot target another owner unless actor is also an owner
- [ ] Audit row: `admin_email_changed` (old + new email, `admin_id` in `metadata`)
- [ ] Rate-limit: 10 req / 1 min per admin (key `adm:echg:usr:`)

`POST /api/v1/admin/users/{id}/magic-link`
- [ ] Body: `{ "send_to": "...", "redirect_to": "...", "reason": "ticket:#1234" }`
- [ ] `send_to` validated but need not match user's registered email (recovery scenario)
- [ ] `redirect_to` validated against internal allowlist (not bypassed for admins)
- [ ] One active magic-link token per user at a time (new one invalidates existing)
- [ ] TTL: 1 hour
- [ ] Sends email to `send_to`; never to `users.email`
- [ ] Audit row: `admin_magic_link_issued` (`admin_id`, `send_to`, `reason` in `metadata`)
- [ ] Rate-limit: 5 req / 15 min per admin (key `adm:ml:usr:`)

`POST /api/v1/admin/users/{id}/force-password-reset`
- [ ] Body: `{ "reason": "ticket:#1234" }` — required
- [ ] Sets `password_hash = NULL`
- [ ] Revokes all refresh tokens (`force_password_reset`); blocklists access tokens
- [ ] Immediately triggers `forgot-password` OTP flow to `users.email`
- [ ] Cannot target another owner unless actor is also an owner
- [ ] Audit row: `admin_force_password_reset` (`admin_id`, `reason` in `metadata`)
- [ ] Rate-limit: 5 req / 15 min per admin (key `adm:fpr:usr:`)

#### Magic Link — user-facing verify

New package: `internal/domain/auth/magiclink/`

> Magic links are admin-controlled recovery tools only. Self-service issuance
> is intentionally omitted. All issuance goes through the admin routes above.

`GET /api/v1/auth/magic-link/verify?token=<token_hash>`
- [ ] Public — the token is the credential
- [ ] Validates `token_hash` against `one_time_tokens` where
      `token_type = 'magic_link'` and `used_at IS NULL` and `expires_at > NOW()`
- [ ] Checks linked user is not `admin_locked` or `is_locked` at verify-time
- [ ] Marks token consumed (`used_at`)
- [ ] Creates session + issues refresh/access token pair (same as POST /login)
- [ ] Redirects to `redirect_to` URL stored on the token row (not caller-supplied)
- [ ] Unknown / expired / used token → redirect to generic frontend error page
- [ ] Audit row: `magic_link_verified` (includes `admin_id` from token `metadata`)

---

## Cross-cutting Reminders

- Every mutation must write an audit row to `auth_audit_log`
- Every credential change (email, username) must check last-auth-method where applicable
- OAuth callbacks use `state` + signed cookie for CSRF; never store raw tokens in
  plaintext (`chk_ui_access_token_encrypted` constraint will reject them)
- Telegram HMAC check is non-negotiable — missing check is a critical auth bypass
- Admin routes verify the role check **before** any DB read
- Rate-limit key prefixes must not reuse any prefix defined in `E2E_CHECKLIST.md`
