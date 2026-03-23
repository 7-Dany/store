# API Endpoint Reference

All routes are prefixed with `/api/v1`. JWT = `Authorization: Bearer <token>` required.

---

## Health

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Returns `{"status":"ok"}`. Validates DB + Redis connectivity. |

---

## Auth — Registration & Verification

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/auth/register` | Create account. Sends email verification OTP. Returns 201. |
| `POST` | `/auth/verification` | Consume email OTP to verify account. Returns 200. |
| `POST` | `/auth/verification/resend` | Re-send verification OTP. Anti-enumeration: always 202. |

---

## Auth — Session

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/auth/login` | Authenticate with email + password. Returns `access_token` + sets `refresh_token` HttpOnly cookie. |
| `POST` | `/auth/refresh` | Rotate refresh token. Returns new `access_token` + new cookie. Token-reuse detection kills the entire family. |
| `POST` | `/auth/logout` | Blocklist access token + clear refresh cookie. JWT required. |

---

## Auth — Account Unlock (OTP lockout)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/auth/unlock` | Request OTP to unlock a login-locked account. Anti-enumeration: always 202. |
| `PUT` | `/auth/unlock` | Submit OTP to clear login lockout. Returns 200. |

---

## Auth — Password Reset

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/auth/password/reset` | Request password-reset OTP. Anti-enumeration: always 202. |
| `POST` | `/auth/password/reset/verify` | Submit OTP to receive a one-time `reset_token`. Returns 200 + `reset_token`. |
| `PUT` | `/auth/password/reset` | Apply new password using `reset_token`. Blocklists all sessions. Returns 200. |

---

## Auth — Change Password *(JWT)*

| Method | Path | Description |
|--------|------|-------------|
| `PATCH` | `/auth/password` | Change password by supplying `old_password` + `new_password`. Blocklists current token + clears refresh cookie. |

---

## Profile — Account

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/profile/me` | Return authenticated user's profile. Optional fields: `username`, `avatar_url`, `last_login_at`, `scheduled_deletion_at`. |
| `PATCH` | `/profile/me` | Update `display_name` and/or `avatar_url`. Partial update — omitted fields unchanged. |

---

## Profile — Sessions *(JWT)*

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/profile/me/sessions` | List all active sessions. Each entry has `is_current` flag. |
| `DELETE` | `/profile/me/sessions/{id}` | Revoke a specific session (own or other device). Kills associated refresh token. |

---

## Profile — Identities *(JWT)*

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/profile/me/identities` | List all linked OAuth providers (`google`, `telegram`). Empty array when none linked. |

---

## Profile — Password (OAuth accounts) *(JWT)*

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/profile/me/password` | Add a password to an OAuth-only account (no existing password). One-time operation. |

---

## Profile — Username

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/profile/me/username/available?username=X` | Check if a username is available. Public, no auth. Always 200 `{"available": true/false}`. |
| `PATCH` | `/profile/me/username` | Set or update username. 409 if taken, 422 if same as current. *(JWT)* |

---

## Profile — Email Change *(JWT, 3-step flow)*

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/profile/me/email` | Step 1: request change. Sends OTP to **current** email. Cooldown: 2 min between requests. |
| `POST` | `/profile/me/email/verify` | Step 2: verify current email. Submit OTP → receive `grant_token` + OTP sent to **new** email. |
| `PUT` | `/profile/me/email` | Step 3: confirm change. Submit `grant_token` + new-email OTP. Swaps email, revokes all sessions. |

---

## Profile — Account Deletion *(JWT)*

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/profile/me/deletion` | Returns required confirmation method: `password`, `email_otp`, or `telegram`. No side effects. |
| `DELETE` | `/profile/me` | Schedule soft-delete (30-day grace). Path A: `{password}`. Path B: empty body → OTP → `{code}`. Path C: empty body → Telegram widget → `{telegram_auth}`. |
| `DELETE` | `/profile/me/deletion` | Cancel pending deletion. Restores account immediately. 409 if no deletion pending. |

---

## Bitcoin — Watch *(JWT)*

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/bitcoin/watch` | Register 1–20 Bitcoin addresses for transaction monitoring. Idempotent — re-submitting an existing address is a no-op. Returns `watching` array of normalised addresses from the current request (not the full watch list). |

---

## OAuth — Google

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/oauth/google` | Initiate Google OAuth. Generates PKCE + state, redirects to Google. |
| `GET` | `/oauth/google/callback` | Handle Google callback. Creates or updates user + identity, issues session, redirects to frontend. |
| `DELETE` | `/oauth/google` | Remove Google identity from account. 422 if it's the last auth method. *(JWT)* |

---

## OAuth — Telegram

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/oauth/telegram/callback` | Handle Telegram Login Widget payload. Verifies HMAC, creates or updates user + identity, issues session. |
| `PUT` | `/oauth/telegram` | Link Telegram to an existing account. 409 if already linked or identity belongs to another user. *(JWT)* |
| `DELETE` | `/oauth/telegram` | Remove Telegram identity from account. 409 if it's the last auth method. *(JWT)* |

---

## RBAC — Owner

| Method | Path | Description |
|--------|------|-------------|
| `PUT` | `/rbac/owner/assign` | Bootstrap: grant owner role to caller using `bootstrap_secret`. One-time — 409 once an owner exists. *(JWT)* |
| `POST` | `/rbac/owner/transfer` | Initiate ownership transfer to a target user. Emails one-time token to target. *(JWT, owner only)* |
| `PUT` | `/rbac/owner/transfer` | Accept a pending transfer using the one-time token from email. No JWT required — token is the credential. |
| `DELETE` | `/rbac/owner/transfer` | Cancel a pending transfer, invalidating the emailed token. *(JWT, owner only)* |

---

## RBAC — Permissions *(JWT, `rbac:read`)*

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/rbac/permissions` | List all active permissions with `capabilities` (valid `access_type` and `scope` values per permission). |
| `GET` | `/rbac/permissions/groups` | List permission groups with member permissions embedded. Ordered by `display_order`. |

---

## RBAC — Roles *(JWT, `rbac:read` / `rbac:manage`)*

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/rbac/roles` | List all active roles. |
| `POST` | `/rbac/roles` | Create a custom role. New roles have no permissions. *(`rbac:manage`)* |
| `GET` | `/rbac/roles/{id}` | Get a single role by ID. |
| `PATCH` | `/rbac/roles/{id}` | Update name/description of a custom role. 409 if system/owner role. *(`rbac:manage`)* |
| `DELETE` | `/rbac/roles/{id}` | Soft-delete a custom role. 409 if system/owner role. *(`rbac:manage`)* |
| `GET` | `/rbac/roles/{id}/permissions` | List all permission grants on a role. |
| `POST` | `/rbac/roles/{id}/permissions` | Add a permission grant to a role. 409 if already granted. *(`rbac:manage`)* |
| `DELETE` | `/rbac/roles/{id}/permissions/{perm_id}` | Remove a permission grant from a role. *(`rbac:manage`)* |

---

## Admin — User Role *(JWT, `rbac:read` / `rbac:manage`)*

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/admin/users/{user_id}/role` | Get a user's current active role assignment. 404 if none. |
| `PUT` | `/admin/users/{user_id}/role` | Assign or replace a user's role. 409 if target is owner or self. *(`rbac:manage`)* |
| `DELETE` | `/admin/users/{user_id}/role` | Remove a user's role assignment. 404 if none. 409 if owner role. *(`rbac:manage`)* |

---

## Admin — User Permissions *(JWT, `rbac:grant_user_permission`)*

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/admin/users/{user_id}/permissions` | List active direct permission grants for a user. Empty array if none. |
| `POST` | `/admin/users/{user_id}/permissions` | Grant a direct permission. Always temporary (`expires_at` required). 409 if already granted. |
| `DELETE` | `/admin/users/{user_id}/permissions/{grant_id}` | Revoke a direct permission grant immediately. |

---

## Admin — User Lock *(JWT, `user:lock` / `user:read`)*

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/admin/users/{user_id}/lock` | Get lock status: `admin_locked` + `is_locked` (OTP lockout) + metadata. *(`user:read`)* |
| `POST` | `/admin/users/{user_id}/lock` | Admin-lock a user. Requires `reason`. 409 if owner or self. *(`user:lock`)* |
| `DELETE` | `/admin/users/{user_id}/lock` | Clear admin lock. Idempotent — 204 even if not currently locked. *(`user:lock`)* |
