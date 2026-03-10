# Checklist for routes we did

Track e2e test coverage across every exposed API endpoint. Check a box once the
scenario is covered by a real e2e test running against the live stack.

**Legend**
- `[ ]` — not yet written
- `[x]` — covered
- `[~]` — partially covered / smoke only

---

## Infrastructure

- [x] `GET /health` → 200 `{"status":"ok"}`
- [x] Any route with non-`application/json` Content-Type header → 415
- [x] Security headers present on every response (`X-Content-Type-Options`, `X-Frame-Options`)

---

## POST /api/v1/auth/register

### Happy path
- [x] Valid payload → 201, `message` field in body
- [x] Registered user exists in DB with `email_verified=false`, `is_locked=false`
- [x] Email is lowercased and trimmed before storage
- [x] `display_name` is whitespace-trimmed before storage
- [x] Unicode domain is stored as punycode ACE form (e.g. `xn--mnchen-3ya.de`)
- [x] Verification token row exists in `email_verification_tokens` with `code_hash` set and `used_at = NULL`
- [x] Verification token `expires_at` is within `[now + OTPTokenTTL - 5s, now + OTPTokenTTL + 5s]`
- [x] A `register` audit row is written for the new user in `auth_audit_log`

### Conflict
- [x] Register same email twice → second call returns 409, `code == "email_taken"`
- [x] A `register_failed` audit row is written in `auth_audit_log` with a NULL `user_id` on the duplicate attempt
- [x] Original user row is unaffected (still exists, `email_verified=false`)

### Validation → 422
- [x] Missing / empty `display_name` → 422 `validation_error`
- [x] `display_name` > 100 **runes** (rune-count, not byte-count) → 422 `validation_error`
- [x] `display_name` exactly 100 runes → **201** (accepted at boundary)
- [x] `display_name` contains ASCII control char in range `[0x00, 0x1F]` (e.g. NUL `\x00`, SOH `\x01`) → 422 `validation_error`
- [x] Missing / empty `email` → 422 `validation_error`
- [x] `email` invalid format (no `@`) → 422 `validation_error`
- [x] `email` in RFC 5322 display-name form (`Bob <bob@example.com>`) → 422 `validation_error`
- [x] `email` > 254 bytes before IDNA conversion → 422 `validation_error`
- [x] `email` ≤ 254 bytes before IDNA but > 254 bytes **after** punycode expansion → 422 `validation_error` (expansion guard fires post-normalisation)
- [x] `email` domain label > 63 chars (RFC 1035 DNS label limit) → 422 `validation_error`
- [x] `email` domain label starting with a hyphen (IDNA Lookup profile rejects it) → 422 `validation_error`
- [x] `password` empty → 422 `validation_error`
- [x] `password` < 8 bytes → 422 `validation_error`
- [x] `password` > 72 bytes (bcrypt hard truncation boundary) → 422 `validation_error`
- [x] `password` missing uppercase letter → 422 `validation_error`
- [x] `password` missing lowercase letter → 422 `validation_error`
- [x] `password` missing digit → 422 `validation_error`
- [x] `password` missing symbol → 422 `validation_error`
- [x] Body > 1 MiB → **413** (`http.MaxBytesReader` causes `json.Decoder` to return `*http.MaxBytesError`; `respond.DecodeJSON` maps it to `http.StatusRequestEntityTooLarge`)
- [x] Malformed JSON → **422** (`respond.DecodeJSON` maps all other decode errors to 422)

### Rate limiting
- [x] 6th request from the same IP within 10 min → 429 (limit: 5 req / 10 min, burst 5, key prefix `reg:ip:`)
- [x] Rate limit resets after the 10-min window: 6th request after window expiry → 201

---

## POST /api/v1/auth/verify-email

### Happy path
- [x] Valid `email` + correct OTP → **200**, body is `{"message":"email verified successfully"}`
- [x] `email_verified = true` in DB after success
- [x] Verification token row is consumed (`used_at` is set) — replaying the same OTP returns 422 `validation_error` (`ErrTokenAlreadyUsed`)

### Anti-enumeration
- [x] Unknown email (no token row) → **422** `validation_error` (`ErrTokenNotFound`) — identical status and body to a wrong-code submission; no 404 is ever returned

### Token / OTP failures → 4xx
- [x] Wrong OTP code (below max-attempts threshold) → **422** `validation_error` (`ErrInvalidCode`); backoff penalty recorded for the IP
- [x] Token already consumed (replay after a successful verification) → **422** `validation_error` (`ErrTokenAlreadyUsed`); **no** backoff penalty
- [x] No token row exists for the email → **422** `validation_error` (`ErrTokenNotFound`); **no** backoff penalty

### Validation → 422
- [x] Missing / empty `email` → 422 `validation_error`
- [x] `email` invalid format (no `@`, missing TLD) → 422 `validation_error`
- [x] `email` > 254 bytes → 422 `validation_error`
- [x] Missing / empty `code` → 422 `validation_error`
- [x] `code` is not exactly 6 digits (too short, too long, or contains non-digit characters) → 422 `validation_error`
- [x] Body > 1 MiB → **413** (`http.MaxBytesReader` fires; `respond.DecodeJSON` maps `*http.MaxBytesError` to 413)
- [x] Malformed JSON → **422**

### Rate limiting
- [x] 6th request from same IP within 10 min → **429** (IP limiter: 5 req / 10 min, burst 5, key prefix `vfy:ip:`)

---

## POST /api/v1/auth/resend-verification

### Happy path
- [x] Unverified, unlocked account → **202 Accepted**, body is `{"message":"if that email is registered and unverified, a new code has been sent"}`

### Anti-enumeration — all suppressed paths must return identical 202 + body
- [x] Unknown email → **202**, same body as happy path; no token row created; no audit row written
- [x] Email exists, account is already verified (`email_verified = true`) → **202**, same body; no token row created

### Validation → 422
- [x] Missing / empty `email` → 422 `validation_error`
- [x] `email` invalid format (no `@`, missing TLD) → 422 `validation_error`
- [x] `email` > 254 bytes → 422 `validation_error`
- [x] Body > 1 MiB → **413**
- [x] Malformed JSON → **422**

### Rate limiting
- [x] 4th request from same IP within 10 min → **429** (IP limiter: 3 req / 10 min, burst 3, key prefix `rsnd:ip:`)

---

## POST /api/v1/auth/login

### Happy path
- [x] Valid email + password, verified account → 200, `access_token` in JSON body, `refresh_token` HttpOnly cookie set
- [x] Refresh token cookie attributes: `HttpOnly`, `SameSite=Strict`, `Path=/api/v1/auth`, `Secure` when served over HTTPS
- [~] Cookie `MaxAge` is derived from the DB refresh token `expires_at`, not a hardcoded duration — *positive Max-Age asserted in e2e; exact derivation covered by handler_test.go*
- [x] Identifier is case-insensitive for email addresses (UPPER@CASE.COM resolves to same account)
- [x] Identifier whitespace is trimmed before lookup

### Failures → 4xx
- [x] Wrong password → 401 `invalid_credentials` (no user enumeration — identical response to unknown identifier)
- [x] Unknown email → 401 `invalid_credentials` (same body and timing as wrong password)
- [x] Unverified account → 403 `email_not_verified`
- [x] Time-locked account (`login_locked_until` in the future) → 429 `login_locked`, `Retry-After` header set in seconds
- [x] Body > 1 MiB → 413
- [x] Malformed JSON → 422
- [x] Missing / empty `identifier` → 422 `validation_error`
- [x] `identifier` > 254 bytes → 422 `validation_error`
- [x] Missing / empty `password` → 422 `validation_error`

### Rate limiting
- [x] 6th request from same IP within 15 min → 429 (limit: 5 req / 15 min per IP, burst 5, key prefix `lgn:ip:`)

---

## POST /api/v1/auth/refresh

### Happy path
- [x] Valid refresh token cookie → 200, new `access_token` in JSON body, new `refresh_token` cookie issued
- [x] New refresh cookie attributes: `HttpOnly`, `SameSite=Strict`, `Path=/api/v1/auth`, positive `MaxAge`

### Token reuse detection (RFC 6819 / ADR-011)
- [x] Presenting a **revoked** refresh token → 401 `token_reuse_detected`, refresh cookie cleared
- [x] After reuse detection the **entire token family** is revoked: the legitimately-rotated successor token is also rejected → 401 `token_reuse_detected`

### Rate limiting
- [x] 6th request from same IP within 15 min → 429 (limit: 5 req / 15 min, burst 5, key prefix `rfsh:ip:`)

---

## POST /api/v1/auth/logout

### Happy path
- [x] Valid refresh token cookie + valid `Authorization: Bearer <access>` header → **204 No Content**, empty body
- [x] `refresh_token` cookie is cleared (`Max-Age=0`) in the response
- [x] Subsequent `GET /me` with the same access token → 401 (blocklisted by KV store, enforced by `/me` auth middleware)
- [x] **Other sessions for the same user are unaffected**: a sibling session's refresh token still rotates successfully after this device logs out

### Rate limiting
- [x] 6th request from same IP within 1 min → 429 (limit: 5 req / 1 min, burst 5, key prefix `lgout:ip:`)

---

## POST /api/v1/auth/request-unlock

### Happy path
- [x] OTP-locked account (`login_locked_until` in the future, triggered by 10 consecutive failed logins) → **202 Accepted**, body is `{"message":"if that email is registered and locked, an unlock code has been sent"}`

### Anti-enumeration — all suppressed paths must return identical 202 + body
- [x] Unknown email → **202**, same body as happy path (no hint the email is absent)
- [x] Email exists, account is **not locked** (`is_locked=false`, no future `login_locked_until`) → **202**, same body (no hint)

### Validation → 422
- [x] Missing / empty `email` → 422 `validation_error`
- [x] `email` invalid format (no `@`, missing TLD) → 422 `validation_error`
- [x] `email` > 254 bytes → 422 `validation_error`
- [x] Body > 1 MiB → 413
- [x] Malformed JSON → 422

### Rate limiting
- [x] 4th request from same IP within 10 min → 429 (limit: 3 req / 10 min per IP, burst 3, key prefix `unlk:ip:`, **shared with `/confirm-unlock`**)

---

## POST /api/v1/auth/confirm-unlock

### Happy path
- [x] Correct OTP for a time-locked account → **200 OK**, body is `{"message":"account unlocked successfully"}`
- [x] User can log in immediately after unlock (login → 200 with correct credentials); this indirectly confirms `is_locked` and `login_locked_until` were cleared in DB
- [x] OTP replay (resubmit the same consumed code) → **422** `validation_error` (`ErrTokenAlreadyUsed`)

### OTP / token failures → 4xx
- [x] Wrong OTP code (1 attempt, well below max-attempts threshold) → **422** `validation_error`
- [x] No active token for the email (account unlocked, token consumed) → **422** `validation_error` (`ErrTokenNotFound`)
- [x] Unknown email (no token row exists) → **422** `validation_error` (`ErrTokenNotFound`, same response as no-token case — no enumeration)

### Validation → 422
- [x] Missing / empty `email` → 422 `validation_error`
- [x] `email` invalid format (no `@`, missing TLD) → 422 `validation_error`
- [x] `email` > 254 bytes → 422 `validation_error`
- [x] Missing / empty `code` → 422 `validation_error`
- [x] `code` shorter than 6 digits → 422 `validation_error`
- [x] `code` longer than 6 digits → 422 `validation_error`
- [x] `code` contains non-digit characters → 422 `validation_error`
- [x] Body > 1 MiB → 413
- [x] Malformed JSON → 422

### Rate limiting
- [x] 4th request from same IP within 10 min → 429 (limit: 3 req / 10 min per IP, burst 3, key prefix `unlk:ip:`, **shared with `/request-unlock`**)
- [x] Combined cross-endpoint exhaustion: 2 × `POST /request-unlock` + 1 × `POST /confirm-unlock` from same IP → 4th request (either endpoint) → 429

---

## POST /api/v1/auth/forgot-password

### Happy path
- [x] Known, verified, active, unlocked account → 202, body is `{"message":"if that email is registered and verified, a password reset code has been sent"}`

### Anti-enumeration — all suppressed paths must return identical 202 + body
- [x] Unknown email → 202, same body as happy path (no hint the email is absent)
- [x] Email exists but account is **unverified** → 202, same body (no hint)

### Validation → 422 / 413
- [x] Missing / empty `email` → 422 `validation_error`
- [x] `email` invalid format (no `@`, missing TLD) → 422 `validation_error`
- [x] `email` > 254 bytes → 422 `validation_error`
- [x] Body > 1 MiB → **413** (valid JSON with `_pad` field; `http.MaxBytesReader` fires during decode)
- [x] Malformed JSON → 422

### Rate limiting
- [x] 4th request from same IP within 10 min → 429 (limit: 3 req / 10 min, burst 3, key prefix `fpw:ip:`)

---

## POST /api/v1/auth/verify-reset-code

### Happy path
- [x] Valid `email` + correct OTP → 200, body contains `reset_token` (UUID) and `expires_in: 600` (10 min TTL)
- [x] OTP is **not consumed** — the same OTP can still be used in step 3 (`POST /reset-password`)
- [x] Attempting a wrong code (below max-attempts threshold) → 422 `validation_error` (`ErrInvalidCode`); attempt counter incremented
- [x] Correct code after a prior wrong attempt → 200 (token still active, counter < max)

### OTP / token failures → 4xx
- [x] Wrong OTP code → 422 `validation_error` (`ErrInvalidCode`)
- [x] OTP already consumed (used by a subsequent `POST /reset-password`) → 422 `validation_error` (`ErrTokenNotFound`)
- [x] No active token for the email (never requested, or token expired/consumed) → 422 `validation_error` (`ErrTokenNotFound`)

### Validation → 422 / 413
- [x] Missing / empty `email` → 422 `validation_error`
- [x] `email` invalid format → 422 `validation_error`
- [x] `code` empty → 422 `validation_error`
- [x] `code` wrong format (not exactly 6 digits, contains non-digit chars) → 422 `validation_error`
- [x] Body > 1 MiB → **413**
- [x] Malformed JSON → 422

### Rate limiting
- [x] 6th request from same IP within 10 min → 429 (limit: 5 req / 10 min, burst 5, key prefix `vpc:ip:`)

---

## POST /api/v1/auth/reset-password

### Happy path
- [x] Valid `reset_token` (grant token from `POST /verify-reset-code`) + strong new password → 200 `{"message":"password reset successfully"}`
- [x] Password hash is updated: can log in with new password immediately
- [x] Old password is rejected after reset
- [x] Grant token is **single-use**: re-submitting the same `reset_token` after a successful reset → 422 `validation_error` (key deleted from KV store)
- [x] **Outstanding access tokens are blocklisted**: a `/me` request with a pre-reset access token → 401
- [x] New password is the **same** as the current password → 422 `validation_error` (`ErrSamePassword`)

### Grant token / OTP failures → 4xx
- [x] Unknown / expired `reset_token` (not in KV store) → 422 `validation_error` ("invalid or expired reset token")
- [x] Grant token already used (single-use, deleted after first `reset-password` call) → 422 `validation_error`

### Password strength failures → 422
- [x] `new_password` empty → 422 `validation_error` (caught at handler-level before service is called)
- [x] `new_password` < 8 chars → 422 `validation_error` (representative strength case)

### Validation → 422 / 413 (handler-level, service never called)
- [x] Missing / empty `reset_token` → 422 `validation_error`
- [x] Body > 1 MiB → **413** (valid JSON with `_pad` field)
- [x] Malformed JSON → 422

### Rate limiting
- [x] 6th request from same IP within 10 min → 429 (limit: 5 req / 10 min, burst 5, key prefix `rpw:ip:`)

---

## POST /api/v1/auth/change-password  *(requires JWT)*

### Happy path
- [x] Correct `old_password` + valid `new_password` → 200 `{"message":"password changed successfully"}`
- [x] Can log in with new password immediately after change
- [x] Old password is rejected after change (login → 401 `invalid_credentials`)
- [x] `refresh_token` cookie is cleared in the response (`MaxAge: -1`, negative Max-Age)
- [x] Current access token is blocklisted: a `/me` request with the pre-change token → 401

### Failures → 4xx
- [x] Wrong `old_password` → 401 `invalid_credentials`
- [x] `new_password` == `old_password` (same password reuse) → 422 `validation_error` (`ErrSamePassword`)
- [x] 5 consecutive wrong `old_password` attempts → 429 `too_many_attempts`, body contains `forgot-password` hint
- [x] Successful change after prior failures resets the counter: subsequent wrong attempt returns 401, not 429
- [x] `new_password` fails strength rules → 422 `validation_error` (one representative case: too short)
- [x] Missing `old_password` field → 422 `validation_error`
- [x] Missing `new_password` field → 422 `validation_error`
- [x] Missing `Authorization` header → 401
- [x] Tampered token signature → 401
- [x] Body > 1 MiB → 413
- [x] Malformed JSON → 422

### Rate limiting
- [x] 6th request from same IP within 15 min → 429 (limit: 5 req / 15 min, burst 5, key prefix `cpw:ip:`)

---

## GET /api/v1/profile/me  *(requires JWT)*

### Happy path
- [x] Valid access token → 200, response body contains `id`, `email`, `display_name`, `email_verified`, `is_active`, `is_locked`, `created_at`
- [x] `last_login_at` is present and non-null after at least one prior login; absent (`omitempty`) for a freshly-registered never-logged-in user
- [x] `avatar_url` is absent from the JSON body when not set (field is `omitempty`)
- [x] `id` field in response is the standard UUID string form (`xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`)

### Auth failures
- [x] Blocklisted access token → 401 — *covered by session.json happy-path (logout → `/me` with old token → 401) and change-password.json happy-path*
- *Missing header / expired / tampered → 401: pure middleware behaviour; covered by `handler_test.go` and exercised in change-password.json `auth-failures` folder — not duplicated here*

### Rate limiting
- [x] 11th request from same IP within 1 min → 429 (limit: 10 req / 1 min, burst 10, key prefix `pme:ip:`)

---

## GET /api/v1/profile/sessions  *(requires JWT)*

### Happy path
- [x] Active sessions returned → 200, response body is `{"sessions": [...]}`
- [x] Each entry has `id` (UUID string), `ip_address`, `user_agent`, `started_at`, `last_active_at`, `is_current`
- [x] The session corresponding to the JWT used in the request has `is_current: true`; all other sessions have `is_current: false`
- [x] Sessions are ordered newest-first (`started_at` descending)
- [x] No active sessions after all are revoked → 200 with `{"sessions": []}` (empty array, not null) — *covered by revoke-session.json: after
### Rate limiting
- [x] 11th request from same IP within 1 min → 429 (limit: 10 req / 1 min, burst 10, key prefix `psess:ip:`)

---



---

## DELETE /api/v1/profile/sessions/{id}  *(requires JWT)*

### Happy path
- [x] Revoke a different (non-current) session → **204 No Content** — *revoke-other scenario*
- [x] After revoking device B's session, `GET /sessions` no longer includes it — *revoke-other: second GET /sessions asserts absence*
- [x] `POST /refresh` with the revoked session's token → 401 `token_reuse_detected` — *revoke-other: confirms refresh token is dead after session revocation*
- [x] Revoke current session → 204 — *revoke-self scenario*
- [x] `POST /refresh` after self-revoke → 401 `token_reuse_detected` — *revoke-self: confirms own refresh token is dead*

### Failures → 4xx
- *Non-existent session → 404, IDOR (other user) → 404, invalid UUID → 422, empty id → 422, auth failures → 401: all covered by `handler_test.go` `TestHandler_RevokeSession` and `store_test.go` `wrong_owner_returns_ErrSessionNotFound` — not duplicated in E2E (IDOR E2E would require two independently verified Gmail accounts)*

### Rate limiting
- [x] 4th request from same IP within 15 min → 429 (limit: 3 req / 15 min, burst 3, key prefix `rsess:ip:`, Retry-After=300)

---

## PATCH /api/v1/profile/me  *(requires JWT)*

### Happy path
- [x] Valid JWT + both fields provided → 200 `{"message":"profile updated successfully"}`
- [x] Only `display_name` provided → 200; `avatar_url` unchanged in DB
- [x] Only `avatar_url` provided → 200; `display_name` unchanged in DB
- [x] Same values as currently stored → 200 (no no-op detection)
- [x] A `profile_updated` audit row is written with changed fields in `metadata`

### Validation → 400 / 422 / 413
- [x] Both fields absent / null → 400 `validation_error` (empty patch rejected)
- [x] `display_name` empty after trim → 422 `validation_error`
- [x] `display_name` > 100 runes → 422 `validation_error`
- [x] `display_name` exactly 100 runes → 200 (accepted at boundary)
- [x] `display_name` contains ASCII control character → 422 `validation_error`
- [x] `avatar_url` empty string → 422 `validation_error` (clearing not supported)
- [x] `avatar_url` not a valid absolute URL → 422 `validation_error`
- [x] `avatar_url` uses a non-permitted scheme → 422 `validation_error`
- [x] `avatar_url` > 2048 bytes → 422 `validation_error`
- [x] Body > 1 MiB → 413
- [x] Malformed JSON → 422

### Auth failures
- [x] Missing `Authorization` header → 401
- [x] Tampered token signature → 401

### Rate limiting
- [x] 11th request from same IP within 1 min → 429 (limit: 10 req / 1 min, burst 10, key prefix `prof:ip:`)

---

## POST /api/v1/profile/set-password  *(requires JWT)*

### Happy path
- *OAuth-only user successfully sets a password → 200 `{"message":"password set successfully"}`: requires a JWT for a user with `password_hash IS NULL`, which cannot be obtained via the standard email+password flow. Covered by `service_test.go TestService_SetPassword/happy_path` and `store_test.go TestSetPasswordHashTx_Integration/T-01/T-18/T-19`.*

### Failures → 422
- [x] Registered (non-OAuth) user already has a password → 422 `password_already_set`
- [x] `new_password` empty → 422 `validation_error`
- [x] `new_password` too short (< 8 chars, representative strength case) → 422 `validation_error`
- *Individual strength rule variants (no-upper, no-lower, no-digit, no-symbol, > 72 bytes): covered by `handler_test.go`*

### Auth failures
- [x] Missing `Authorization` header → 401
- [x] Tampered token signature → 401

### Validation → 413 / 422
- [x] Body > 1 MiB → 413
- [x] Malformed JSON → 422 `validation_error`

### Rate limiting
- [x] 6th request from same user within 15 min → 429 (limit: 5 req / 15 min, burst 5, key prefix `spw:usr:`, Retry-After=180)

---


### §B-1 — Username Management

New package: `internal/domain/profile/username/`

#### Availability check
`GET /api/v1/profile/username/available?username=X`
- [X] Public (no auth required) — used by frontend live-validation
- [X] Returns `{"available": true|false}` — always 200; never reveal account existence
      through different response shapes
- [X] Normalise input (trim, lowercase) the same way the DB stores it before checking
- [X] Validate: not empty, same length/charset rules as registration
- [X] Rate-limit: 20 req / 1 min per IP (key `unav:ip:`)

#### Update username (requires auth)
`PATCH /api/v1/profile/me/username`
- [X] Requires valid JWT
- [X] Body: `{ "username": "..." }`
- [X] Validate format (same length/charset rules as registration)
- [X] Check availability; return 409 `username_taken` if already in use
- [X] Atomically update `username` on `users` with a re-check inside the DB transaction
- [X] Guard: no-op if the new username is identical to the current one (422 `same_username`)
- [X] Audit row: `username_changed` (old + new username in `metadata`)
- [X] No session/token revocation (username is not in the JWT payload)
- [X] Rate-limit: 5 req / 10 min per user (key `uchg:usr:`)

---

### §B-2 — Email Change Flow

New package: `internal/domain/profile/email/`

Three-step flow: prove ownership of the **current** address first, then prove
ownership of the **new** address before the change is applied.

#### Step 1 — Request change (requires auth)
`POST /api/v1/profile/me/email/request-change`
- [X] Requires valid JWT
- [X] Body: `{ "new_email": "..." }`
- [X] Validate `new_email` format + uniqueness against `users`
- [X] Sends OTP to the **current email** (proves the requester controls the account)
- [X] Cooldown guard: suppress duplicate OTPs within 2 min
- [X] Stores `new_email` in token `metadata` (token_type `email_change_verify`)
- [X] Audit row: `email_change_requested`
- [X] Rate-limit: 3 req / 10 min per user (key `echg:usr:`)

#### Step 2 — Verify current email (requires auth)
`POST /api/v1/profile/me/email/verify-current`
- [X] Requires valid JWT
- [X] Body: `{ "code": "123456" }`
- [X] Validates OTP against the active `email_change_verify` token for this user
- [X] Marks token consumed
- [X] Issues a short-lived grant token (KV, 10 min TTL) encoding `new_email`
- [X] Response: `{ "grant_token": "...", "expires_in": 600 }` — client holds this for step 3
- [X] Sends OTP to the **new email** (proves ownership of the destination)
- [X] Audit row: `email_change_current_verified`
- [X] Rate-limit: 5 req / 15 min per user (key `echg:usr:vfy:`)

#### Step 3 — Confirm new email (requires auth)
`POST /api/v1/profile/me/email/confirm-change`
- [X] Requires valid JWT
- [X] Body: `{ "grant_token": "...", "code": "123456" }`
- [X] Validates `grant_token` (must not be expired or already used)
- [X] Validates OTP sent to the new email in step 2
- [X] Atomically: updates `email` on `users`, marks OTP consumed, deletes grant token
- [X] Re-check uniqueness inside the DB transaction
- [X] Revokes all active refresh tokens (email is primary identifier)
- [X] Blocklists current access token
- [X] Sends confirmation notice to the **old email**
- [X] Audit row: `email_changed` (old + new email in `metadata`)
- [X] Rate-limit: 5 req / 15 min per user (key `echg:usr:cnf:`)

---

## §D-1 — OAuth — Google

Package: `internal/domain/auth/oauth/google/`

### GET /api/v1/oauth/google (initiate)
- [x] Generates `state` (CSRF token, short-lived KV entry)
- [x] Generates PKCE `code_verifier` / `code_challenge`
- [x] Redirects to Google authorization endpoint with `state`, `code_challenge`, scopes (`openid email profile`)

### GET /api/v1/oauth/google/callback
- [x] Validates `state` (CSRF check)
- [x] Exchanges `code` for tokens; verifies ID token (signature, `aud`, `exp`)
- [x] **New user**: creates `users` row (`email_verified = TRUE`), creates `user_identities` row, issues session + token pair
- [x] **Existing user**: refreshes identity data, issues new session
- [x] Stores encrypted `access_token` in `user_identities` (`enc:` prefix required)
- [x] Audit row: `oauth_login` with `provider = 'google'`
- [x] Failure: redirect to frontend error page; never expose raw Google errors
- [x] Sets `oauth_access_token` cookie (`SameSite=Lax`, non-HttpOnly, 30s TTL, `path=/`) for frontend pickup
- [x] Sets `refresh_token` cookie (`HttpOnly`, `SameSite=Strict`, `path=/api/v1/auth`)
- [x] Redirects to frontend success URL (`OAUTH_SUCCESS_URL`) with `?provider=google&new_user=<bool>`

### DELETE /api/v1/oauth/google/unlink
- [x] Requires valid JWT
- [x] Guard: user must have at least one other auth method (422 `last_auth_method`)
- [x] Deletes `user_identities` row for `(user_id, 'google')`
- [x] Audit row: `oauth_unlinked` with `provider = 'google'`
- [x] Rate-limit: 5 req / 15 min per user (key `unl:usr:`)

### Failures
- [x] Missing or invalid `state` → redirect to frontend error page
- [x] Google token exchange failure → redirect to frontend error page
- [x] Unlink with no other auth method → 422 `last_auth_method`
- [x] Unlink when identity not linked → 401 `unauthorized`
- [x] Unauthenticated unlink attempt (missing token) → 401 `missing_token`
- [x] Unauthenticated unlink attempt (invalid token) → 401 `invalid_token`

---

## §D-2 — OAuth — Telegram

Package: `internal/domain/auth/oauth/telegram/`

### POST /api/v1/oauth/telegram/callback
- [x] Verifies HMAC-SHA256: `HMAC_SHA256(SHA256(BOT_TOKEN), data_check_string)`
- [x] Rejects if `auth_date` > 86400 seconds old (replay protection)
- [x] **New user**: creates `users` row (`email_verified = TRUE`), creates `user_identities` row, issues session + token pair
- [x] **Existing user**: refreshes identity data, issues new session
- [x] `provider_email` is NULL (Telegram does not provide email)
- [x] Audit row: `oauth_login` with `provider = 'telegram'`
- [x] Failure (invalid HMAC / expired `auth_date`): redirect to frontend error page
- [x] Sets `oauth_access_token` cookie (`SameSite=Lax`, non-HttpOnly, 30s TTL, `path=/`) for frontend pickup
- [x] Sets `refresh_token` cookie (`HttpOnly`, `SameSite=Strict`, `path=/api/v1/auth`)
- [x] Redirects to frontend success URL with `?provider=telegram&new_user=<bool>`

### POST /api/v1/oauth/telegram/link
- [x] Requires valid JWT
- [x] Same HMAC + `auth_date` checks as callback
- [x] Guard: 409 `provider_already_linked` if Telegram already linked to this account
- [x] Guard: 409 `provider_taken` if the Telegram identity is linked to a different account
- [x] Audit row: `oauth_linked` with `provider = 'telegram'`
- [x] Rate-limit: 5 req / 15 min per user (key `lnk:usr:`)

### DELETE /api/v1/oauth/telegram/unlink
- [x] Requires valid JWT
- [x] Guard: 422 `last_auth_method` if Telegram is the only auth method
- [x] Deletes `user_identities` row for `(user_id, 'telegram')`
- [x] Audit row: `oauth_unlinked` with `provider = 'telegram'`
- [x] Rate-limit: 5 req / 15 min per user (key `unl:usr:`)

### Failures
- [x] Invalid HMAC or `auth_date` older than 24 h → redirect to frontend error page
- [x] Link with Telegram already linked to this account → 409 `provider_already_linked`
- [x] Link when identity is linked to a different account → 409 `provider_taken`
- [x] Unlink with no other auth method → 422 `last_auth_method`
- [x] Unlink when Telegram not linked → 401 `unauthorized`
- [x] Unauthenticated link/unlink attempt → 401

---

## §E-1 — Linked Accounts

Package: `internal/domain/profile/me/` (extended)

### GET /api/v1/profile/me/identities
- [x] Requires valid JWT
- [x] Returns all rows in `user_identities` for the authenticated user
- [x] Response shape per identity: `provider`, `provider_email`, `display_name`, `avatar_url`, `created_at`
- [x] `access_token` and `refresh_token_provider` are never returned
- [x] Empty array (not null) when no identities are linked
- [x] Rate-limit: 20 req / 1 min per IP (key `ident:ip:`)

### Auth failures
- [x] Missing `Authorization` header → 401
- [x] Tampered token signature → 401

---

## §B-3 — Delete Account

Package: `internal/domain/profile/delete-account/`

Soft-delete: `deleted_at TIMESTAMPTZ` on `users`; 30-day grace period before permanent removal.

### GET /api/v1/profile/me/deletion-method
- [x] Requires valid JWT
- [x] Returns `{ "deletion_method": "password" | "email_otp" | "telegram" }` — no side effects
- [x] Priority: password → email_otp → telegram
- [x] 409 `already_pending_deletion` when account is already scheduled for deletion
- [x] Rate-limit: 10 req / 1 min per user

### DELETE /api/v1/profile/me — Path A (password)
- [x] Requires valid JWT + `{ "password": "..." }` body
- [x] Verifies password before proceeding; 401 `invalid_credentials` on mismatch
- [x] Stamps `deleted_at = NOW()`; responds 200 `{ "message": "account scheduled for deletion", "scheduled_deletion_at": "..." }`
- [x] `GET /me` includes `scheduled_deletion_at` from this point until cancelled or expired
- [x] Audit row: `account_deletion_requested` with `provider = 'email'`
- [x] Rate-limit: 10 req / 1 hr per user

### DELETE /api/v1/profile/me — Path B (email OTP, step 1)
- [x] Requires valid JWT + empty body `{}`
- [x] Applies to accounts with no password hash and an email address
- [x] Issues 6-digit OTP to account email; 202 response with `expires_in`
- [x] Audit row: `account_deletion_otp_requested`

### DELETE /api/v1/profile/me — Path B (email OTP, step 2)
- [x] Body: `{ "code": "123456" }`
- [x] Validates OTP; 422 `invalid_code` on mismatch, `token_not_found` if expired
- [x] Stamps `deleted_at`; 200 with `scheduled_deletion_at`
- [x] Audit row: `account_deletion_requested` with `provider = 'email'`

### DELETE /api/v1/profile/me — Path C (Telegram, step 1)
- [x] Requires valid JWT + empty body `{}`
- [x] Applies to Telegram-only accounts (no password, no email)
- [x] 202 response prompting client to render Telegram Login Widget

### DELETE /api/v1/profile/me — Path C (Telegram, step 2)
- [x] Body: `{ "telegram_auth": { ... } }`
- [x] Verifies HMAC + `auth_date` freshness; 401 `invalid_telegram_auth` on failure
- [x] Ownership check: `telegram_auth.id` must match linked identity; 401 `telegram_identity_mismatch` on mismatch
- [x] Stamps `deleted_at`; 200 with `scheduled_deletion_at`
- [x] Audit row: `account_deletion_requested` with `provider = 'telegram'`

### POST /api/v1/profile/me/cancel-deletion
- [x] Requires valid JWT
- [x] Clears `deleted_at`; account immediately restored to full active status
- [x] `GET /me` no longer returns `scheduled_deletion_at` after cancellation
- [x] 409 `not_pending_deletion` when no deletion is pending
- [x] Audit row: `account_deletion_cancelled`
- [x] Rate-limit: 5 req / 10 min per user

### Common failures
- [x] Any path: 409 `already_pending_deletion` when deletion is already scheduled
- [x] Any path: 400 `validation_error` empty body on a password account
- [x] Any path: 413 body > 1 MiB
- [x] Any path: 401 missing/invalid JWT

---

## Cross-cutting / flow scenarios

Items in this section require multiple endpoints, real HTTP cookies, or live infrastructure
wiring that unit tests cannot exercise. Scenarios that are impractical for E2E (need two
independent Gmail accounts, raw HttpOnly cookie access across parallel sessions, DB-query
steps, or long waits) are noted as internal-only with their unit/integration test references.

- [x] **Full happy-path flow**: register → verify email → login → call `/me` → logout → verify token is blocklisted — *covered collectively by session.json happy-path (`GET /me` cross-endpoint blocklist check) and change-password.json happy-path*
- [x] **Account lockout flow — time lock (failed login threshold)**: register → verify → login with wrong password until `login_locked_until` is set → `POST /request-unlock` (202, OTP sent) → `POST /confirm-unlock` (correct OTP, 200) → login succeeds
- [x] **Unlock — account not locked is a silent no-op**: unlocked account → `POST /request-unlock` → 202, same body; no hint given that no OTP was issued
- [x] **Unlock — shared rate limiter across both endpoints**: 2 × `POST /request-unlock` + 1 × `POST /confirm-unlock` from the same IP within 10 min → 4th request to either endpoint → 429
- [x] **Password reset flow — full 3-step success path**: register → verify-email → forgot-password (OTP issued) → verify-reset-code (wrong code → 422, correct code → 200 + `reset_token`) → reset-password (`reset_token` + new password → 200) → login with new password succeeds → login with old password → 401
- [x] **Password reset flow — access token blocklisted**: complete 3-step reset flow → confirm a pre-reset access token is rejected by `/me` (blocklist active)
- [x] **Password reset flow — grant token is single-use**: reset-password with a previously-used `reset_token` → 422 `validation_error` (key deleted from KV after first use)
- [x] **Password reset flow — OTP is consumed after reset**: after a successful reset, `POST /verify-reset-code` with the original OTP → 422 `validation_error` (`ErrTokenNotFound`)
- [x] **Password reset flow — same-password rejection**: forgot-password → verify-reset-code → reset-password with current password as `new_password` → 422 `validation_error`
- [x] **Session rotation**: login → `POST /refresh` (rotate) → present original token → 401 `token_reuse_detected` — *covered by session.json token-reuse folder*
- [x] **Token reuse detection — full family kill**: login → refresh once (get token B) → present original token A → 401 `token_reuse_detected` → immediately try token B → 401 (family revoked) — *covered by session.json token-reuse folder*
- [x] **Logout blocklists access token**: login → logout (with Bearer access token in header) → `GET /me` with old access token → 401 — *covered by session.json happy-path cross-endpoint blocklist check*
- [x] **Logout is per-device**: login on two "devices" (A and B) → logout device A → device B's refresh token still rotates successfully — *covered by session.json happy-path (sibling session refresh after logout)*
- [x] **Multi-session revocation**: login on two "devices" → revoke one session via `DELETE /sessions/{id}` → revoked device's `POST /refresh` → 401; other device unaffected — *covered by revoke-session.json happy-path revoke-other scenario*
- [x] **Change-password full flow**: login → call `/me` with access token (200) → `POST /change-password` (correct passwords) → `/me` with old access token → 401 (blocklisted) → login with old password → 401 → login with new password → 200
