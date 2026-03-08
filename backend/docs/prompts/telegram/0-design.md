# Stage 0 — Design: §D-2 Telegram OAuth

**Package:** `internal/domain/auth/oauth/telegram/`  
**Routes:**
- `POST /api/v1/auth/oauth/telegram/callback` — public (no JWT)
- `POST /api/v1/auth/oauth/telegram/link` — authenticated (requires JWT)
- `DELETE /api/v1/auth/oauth/telegram/unlink` — authenticated (requires JWT)

---

## 1. Feature Overview

Telegram's Login Widget does not use PKCE or redirect URLs. Instead, the frontend
receives a signed data bundle directly from Telegram's widget JS and posts it to
our backend for server-side HMAC verification.

Three endpoints share the same HMAC verification logic:
- **Callback**: unauthenticated login/register via Telegram
- **Link**: add Telegram as a second auth method to an existing account
- **Unlink**: remove the Telegram identity from an account

The Google OAuth flow (§D-1) will follow the same package layout established here.

---

## 2. Telegram Widget Payload

The Telegram Login Widget sends a JSON body with these fields:

```json
{
  "id":         12345678,
  "first_name": "John",
  "last_name":  "Doe",      // optional
  "username":   "johndoe",  // optional
  "photo_url":  "https://t.me/i/userpic/...",  // optional
  "auth_date":  1700000000,
  "hash":       "abcdef1234567890..."
}
```

`provider_uid` = string form of `id` (Telegram user ID, int64).

### HMAC Verification

```
data_check_string = alphabetically sorted "key=value" pairs
                    (all received fields EXCEPT "hash"), joined by "\n"
secret_key        = SHA256(raw_bytes(BOT_TOKEN))
expected_hash     = hex(HMAC_SHA256(secret_key, data_check_string))
valid             = expected_hash == received_hash (constant-time compare)
```

The bot token is read from `deps.Config.TelegramBotToken` (see §7 — Config).

### Replay Protection

Reject if `time.Now().Unix() - auth_date > 86400` (one day).

---

## 3. Request Bodies

### POST /callback and POST /link

```json
{
  "id":         12345678,
  "first_name": "John",
  "last_name":  "Doe",
  "username":   "johndoe",
  "photo_url":  "https://...",
  "auth_date":  1700000000,
  "hash":       "abcdef..."
}
```

Both endpoints share the same `telegramCallbackRequest` struct and
`validateTelegramRequest` function.

### DELETE /unlink

No request body.

---

## 4. Data Flows

### 4.1 POST /callback (new user vs returning user)

```
1. MaxBytesReader
2. DecodeJSON → telegramCallbackRequest
3. VerifyHMAC(req, botToken) → err=ErrInvalidTelegramSignature
4. CheckAuthDate(req.AuthDate) → err=ErrTelegramAuthDateExpired
5. store.GetIdentityByProviderUID(ctx, "telegram", providerUID) →
     found    → ExistingUserPath
     not found → NewUserPath

ExistingUserPath:
  6. store.GetUserForOAuth(ctx, userID)
       → ErrUserNotFound   → 404 (should never happen; identity references a live user)
       → ErrAccountLocked  → 423
       → ErrAccountInactive → 403
  7. store.CallbackTx(ctx, CallbackTxInput{...}) → creates session + refresh token
     + updates identity (name/avatar) if changed
  8. audit: EventOAuthLogin (provider=telegram, new_user=false) via WithoutCancel
  9. token.MintTokens → respond.JSON 200

NewUserPath:
  6. store.CreateUserWithTelegramTx(ctx, ...) →
       creates user row (email=NULL, password_hash=NULL, is_active=true,
       email_verified=false, display_name from first+last, avatar_url from photo_url)
       + user_identities row
       + session row
       + refresh_token row
       in a single transaction
  7. audit: EventOAuthLogin (provider=telegram, new_user=true) via WithoutCancel
  8. token.MintTokens → respond.JSON 201
```

**Decision D-01:** New Telegram users are created with `email=NULL`, `password_hash=NULL`,
`is_active=true`, `email_verified=false`. They have no email to verify; the
`user_identities` row is their auth method. The `trg_require_auth_method` trigger must
allow this (the identity row is inserted in the same TX before the trigger fires on
the user row — see §8 Migration Notes).

**Decision D-02:** `display_name` is set to `first_name + " " + last_name` (or just
`first_name` if last_name is absent). This may be NULL if Telegram returns no name
fields, in which case the DB column remains NULL.

**Decision D-03:** We do NOT store the Telegram bot token or user token in
`user_identities.access_token`. Telegram does not issue OAuth access tokens in the
widget flow. The `access_token` column will be NULL for all Telegram identities.

**Decision D-04:** `provider_email` will be NULL for Telegram identities.

### 4.2 POST /link

```
1. JWTAuth middleware → token.UserIDFromContext
2. MaxBytesReader
3. DecodeJSON → telegramCallbackRequest
4. VerifyHMAC(req, botToken)
5. CheckAuthDate(req.AuthDate)
6. Check if user already has a telegram identity:
     store.GetIdentityByUserAndProvider(ctx, userID, "telegram")
       found → ErrProviderAlreadyLinked (409)
7. Check if this Telegram account is already linked to ANOTHER user:
     store.GetIdentityByProviderUID(ctx, "telegram", providerUID)
       found with different userID → ErrProviderUIDTaken (409)
8. store.LinkIdentityTx(ctx, ...) → inserts user_identities row atomically
9. audit: EventOAuthLinked (provider=telegram) via WithoutCancel
10. respond.NoContent 204
```

**Decision D-05:** Step 7 prevents a single Telegram account from being linked to
multiple platform accounts. This check must be inside a transaction with step 8
(SELECT FOR UPDATE) to avoid TOCTOU races.

### 4.3 DELETE /unlink

```
1. JWTAuth middleware → token.UserIDFromContext
2. No body
3. store.GetIdentitiesByUser(ctx, userID) → list of user_identities
4. Last-auth-method guard:
     hasTelegramIdentity = any row where provider = "telegram"
     if !hasTelegramIdentity → ErrProviderNotLinked (404)
     hasOtherAuthMethod   = password_hash IS NOT NULL
                           OR any other user_identity with provider ≠ "telegram"
     if !hasOtherAuthMethod → ErrLastAuthMethod (409)
5. store.DeleteIdentityTx(ctx, userID, "telegram") → deletes identity row
6. audit: EventOAuthUnlinked (provider=telegram) via WithoutCancel
7. respond.NoContent 204
```

**Decision D-06:** The last-auth-method guard (step 4) runs at the service layer
using data already fetched (no extra DB round trip). The `hasPassword` flag is
returned alongside the identity list from a single query.

---

## 5. Security Decisions

**D-07 — HMAC is non-negotiable.** Missing or invalid HMAC = 401 immediately.
No fallback. The handler calls `VerifyHMAC` before any DB read.

**D-08 — Constant-time hash comparison.** `hmac.Equal(expected, received)` is
used; never `==` or `bytes.Equal` on the hex strings.

**D-09 — auth_date replay window.** Reject if `now - auth_date > 86400s`. Clock
skew tolerance is 0 (we trust Telegram's servers). If auth_date is in the future,
reject as well (e.g. `auth_date - now > 60s`).

**D-10 — Bot token in config, never in request.** The bot token is never accepted
from the client. It comes exclusively from `deps.Config.TelegramBotToken`.

**D-11 — Audit writes use context.WithoutCancel.** All three endpoints write audit
rows under `context.WithoutCancel(ctx)` so a client disconnect cannot suppress the
audit trail.

**D-12 — No access_token stored.** Telegram widget flow issues no access token.
`user_identities.access_token` remains NULL. The DB constraint
`chk_ui_access_token_encrypted` only triggers when the column is non-NULL, so NULL
is safe.

**D-13 — Rate limits apply per IP for public callback; per user for link/unlink.**
See §6.

**D-14 — provider_uid uniqueness enforced by DB.** The `user_identities` table has
a unique index on `(provider, provider_uid)`. A race between two concurrent new-user
Telegram callbacks with the same ID will produce a unique-violation, which the store
maps to `ErrProviderUIDTaken`.

---

## 6. Rate Limits and KV Prefixes

| Handler | Limiter type | Key prefix | Rate | Burst |
|---|---|---|---|---|
| POST /callback | IP | `tgcb:ip:` | 10 / 1 min | 10 |
| POST /link | User | `tglnk:usr:` | 5 / 15 min | 5 |
| DELETE /unlink | User | `tgunlk:usr:` | 5 / 15 min | 5 |

All three prefixes are new and do not conflict with any existing prefixes.

---

## 7. Config / Dependencies

A new field is needed on `app.Deps` (or a sub-config struct):

```go
// In app/deps.go or app/config.go
TelegramBotToken string  // read from TELEGRAM_BOT_TOKEN env var
```

**Decision D-15:** The bot token is validated at startup (non-empty string check in
the same pattern as JWT secrets). If absent, the telegram routes are either skipped
or the handler panics at construction time — document the choice during Stage 1.

---

## 8. New SQL Queries Required

All go in `sql/queries/auth.sql` under a new `/* ── Telegram OAuth ── */` section.

| Query name | Type | Description |
|---|---|---|
| `GetIdentityByProviderUID` | `:one` | look up `user_identities` by `(provider, provider_uid)` — returns identity + user_id |
| `GetIdentityByUserAndProvider` | `:one` | look up identity by `(user_id, provider)` — for duplicate-link guard |
| `GetUserForOAuth` | `:one` | get user (id, is_locked, admin_locked, is_active, email_verified, password_hash) — FOR UPDATE |
| `CreateUserWithTelegramTx` | manual TX | user + user_identity + session + refresh_token in one TX |
| `CreateOAuthSessionTx` | manual TX | session + refresh_token for existing user path (callback) |
| `UpdateIdentityProfile` | `:exec` | update display_name, avatar_url on user_identity when they change |
| `InsertIdentity` | `:one` | insert a user_identity row (for link); returns id |
| `GetUserIdentitiesWithPassword` | `:many` | returns all identities for a user + password_hash IS NOT NULL flag (for unlink guard) |
| `DeleteIdentity` | `:execrows` | delete user_identity by (user_id, provider); returns rows affected |

**Note on `CreateUserWithTelegramTx` and `CreateOAuthSessionTx`:** These are manual
transaction functions (not generated by sqlc) because they span multiple tables.
They follow the same pattern as `login.LoginTx`.

---

## 9. New Sentinel Errors

Defined in `oauth/telegram/errors.go` (not in authshared — these are telegram-specific):

```go
var (
    ErrInvalidTelegramSignature = errors.New("invalid telegram signature")
    ErrTelegramAuthDateExpired  = errors.New("telegram auth_date too old or in future")
    ErrProviderAlreadyLinked    = errors.New("telegram account already linked to this user")
    ErrProviderUIDTaken         = errors.New("telegram account already linked to another user")
    ErrProviderNotLinked        = errors.New("no telegram identity linked to this account")
    ErrLastAuthMethod           = errors.New("cannot unlink last authentication method")
)
```

---

## 10. HTTP Error Mapping

| Sentinel | Status | Code |
|---|---|---|
| `ErrInvalidTelegramSignature` | 401 | `invalid_signature` |
| `ErrTelegramAuthDateExpired` | 401 | `auth_date_expired` |
| `ErrProviderAlreadyLinked` | 409 | `provider_already_linked` |
| `ErrProviderUIDTaken` | 409 | `provider_uid_taken` |
| `ErrProviderNotLinked` | 404 | `provider_not_linked` |
| `ErrLastAuthMethod` | 409 | `last_auth_method` |
| `authshared.ErrAccountLocked` | 423 | `account_locked` |
| `authshared.ErrAccountInactive` | 403 | `account_inactive` |
| Unexpected | 500 | `internal_error` |

---

## 11. Package Layout

```
internal/domain/auth/oauth/telegram/
├── handler.go          — HTTP layer (Handler struct + 3 methods)
├── service.go          — business logic (Service struct + Storer interface)
├── store.go            — store implementation (wraps db.Queries + pgxpool)
├── models.go           — domain models (TelegramUser, CallbackResult, etc.)
├── requests.go         — request structs (telegramCallbackRequest)
├── validators.go       — VerifyHMAC, CheckAuthDate, validateRequest
├── errors.go           — sentinel errors
└── routes.go           — func Routes(ctx, r chi.Router, deps *app.Deps)
```

---

## 12. Test Cases

### H-layer (handler_test.go, no build tag)

**Callback:**
- T-01: valid new user → 201, tokens in body + cookie
- T-02: valid returning user → 200, tokens in body + cookie
- T-03: invalid HMAC → 401 `invalid_signature`
- T-04: auth_date > 86400s old → 401 `auth_date_expired`
- T-05: auth_date in future → 401 `auth_date_expired`
- T-06: missing required field (id) → 422 `validation_error`
- T-07: returning user, account locked → 423 `account_locked`
- T-08: returning user, account inactive → 403 `account_inactive`
- T-09: decode failure (malformed JSON) → 400
- T-10: provider_uid race → 409 `provider_uid_taken` (store returns ErrProviderUIDTaken)
- T-11: rate limited → 429

**Link:**
- T-12: valid link, success → 204
- T-13: already linked to this user → 409 `provider_already_linked`
- T-14: Telegram account linked to another user → 409 `provider_uid_taken`
- T-15: invalid HMAC → 401
- T-16: auth_date expired → 401
- T-17: no JWT → 401 (handled by JWTAuth middleware, not unit-tested here)
- T-18: rate limited → 429

**Unlink:**
- T-19: valid unlink, user has password → 204
- T-20: valid unlink, user has another OAuth identity → 204
- T-21: provider not linked → 404 `provider_not_linked`
- T-22: last auth method → 409 `last_auth_method`
- T-23: no JWT → 401 (middleware)
- T-24: rate limited → 429

---

## 13. Migration Notes

**D-16:** The `users` table has `trg_require_auth_method`. In the new-user path, we
insert the `user` and `user_identity` in the same transaction. The trigger must fire
**after** the identity is inserted, or the trigger must check identities as part of
its condition.

Action required at Stage 1: read the migration that defines `trg_require_auth_method`
and confirm it accounts for `user_identities`. If not, a migration to update the
trigger is required before this feature can be implemented.

**D-17:** No new DB enum values are needed — `db.AuthProviderTelegram` already exists.

**D-18:** No new `one_time_tokens.token_type` values needed.

---

## 14. Audit Events Used

All three audit events already exist in `internal/audit/audit.go`:
- `audit.EventOAuthLogin` — callback handler (new user and returning user)
- `audit.EventOAuthLinked` — link handler
- `audit.EventOAuthUnlinked` — unlink handler

Metadata shape:
- `EventOAuthLogin`: `{"provider": "telegram", "new_user": true/false}`
- `EventOAuthLinked`: `{"provider": "telegram"}`
- `audit.EventOAuthUnlinked`: `{"provider": "telegram"}`
