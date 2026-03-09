# §D-2 Telegram OAuth — Stage 4: HTTP Layer

**Depends on:** Stage 3 complete — `Servicer` interface declared in
`internal/domain/oauth/telegram/handler.go`, `Service` with `HandleCallback`,
`LinkTelegram`, and `UnlinkTelegram` implemented in `service.go`,
`TelegramFakeServicer` in `internal/domain/oauth/shared/testutil/fake_servicer.go`,
and all Stage 3 service unit tests passing.

**Stage goal:** `Handler` struct implemented (`HandleCallback`, `HandleLink`,
`HandleUnlink`), `routes.go` written, and all H-layer test cases from Stage 0 §12
written and passing.

---

## Read first (no modifications)

| File | Why |
|---|---|
| `docs/prompts/telegram/0-design.md` | Source of truth — HTTP contract (§3), guard ordering (§4), error mapping (§10), rate limits (§6), test cases (§12) |
| `internal/domain/oauth/telegram/handler.go` | Servicer interface — the Handler struct and methods go here |
| `internal/domain/oauth/telegram/service.go` | Storer + Service — understand what HandleCallback returns (CallbackResult.NewUser) |
| `internal/domain/oauth/telegram/models.go` | CallbackResult, LinkInput, TelegramUser — understand what service returns |
| `internal/domain/oauth/telegram/requests.go` | telegramCallbackRequest — the shared JSON body for callback and link |
| `internal/domain/oauth/telegram/validators.go` | VerifyHMAC, CheckAuthDate — called by handler before service |
| `internal/domain/oauth/telegram/errors.go` | All package sentinel errors |
| `internal/domain/oauth/shared/errors.go` | ErrAccountLocked, ErrLastAuthMethod, ErrIdentityNotFound |
| `internal/domain/oauth/shared/testutil/fake_servicer.go` | TelegramFakeServicer — used in all handler tests |
| `internal/domain/oauth/google/handler.go` | Canonical pattern: NewHandler, MintTokens call, error switch, cookie setup |
| `internal/domain/oauth/google/routes.go` | Canonical pattern: rate limiter construction, route registration |
| `internal/domain/oauth/google/handler_test.go` | Canonical test layout: newHandler helper, assertJSONCode, t.Parallel |
| `internal/platform/token/` | MintTokens, UserIDFromContext, JWTConfig, InjectUserIDForTest |
| `internal/platform/respond/respond.go` | DecodeJSON, ClientIP, JSON, NoContent, Error, MaxBodyBytes |
| `internal/app/deps.go` | *app.Deps — confirm OAuth.TelegramBotToken field name |
| `docs/RULES.md §3.10, §3.13` | HTTP conventions, sub-package checklist |

---

## Pre-flight

1. Confirm all Stage 3 service tests pass:
   ```
   go test ./internal/domain/oauth/telegram/... -run TestService_ -v
   ```
2. Confirm `Servicer` interface is declared in `handler.go` with all three methods.
3. Confirm `TelegramFakeServicer` exists in `fake_servicer.go` with `HandleCallbackFn`,
   `LinkTelegramFn`, and `UnlinkTelegramFn`.
4. Confirm `telegramCallbackRequest`, `VerifyHMAC`, and `CheckAuthDate` exist
   (Stage 1 deliverables).
5. Confirm `deps.OAuth.TelegramBotToken` (or the equivalent field path on `*app.Deps`)
   exists — read `internal/app/deps.go` to get the exact field name before wiring
   `routes.go`.

---

## Deliverables

### 1. `internal/domain/oauth/telegram/handler.go` — Handler struct + three methods

Replace the current file (which contains only the `Servicer` interface) with the
full handler implementation. Keep the `Servicer` interface at the top of the file.

#### Handler struct and constructor

```go
// Handler is the HTTP layer for Telegram Login Widget authentication:
// callback, link, and unlink.
type Handler struct {
    svc           Servicer
    botToken      string
    cfg           token.JWTConfig
    secureCookies bool
}

// NewHandler constructs a Handler with the given dependencies.
func NewHandler(svc Servicer, botToken string, cfg token.JWTConfig, secureCookies bool) *Handler {
    return &Handler{svc: svc, botToken: botToken, cfg: cfg, secureCookies: secureCookies}
}
```

#### HandleCallback — POST /oauth/telegram/callback (public, no JWT)

Guard ordering (Stage 0 §4.1):
```
1. MaxBytesReader
2. DecodeJSON → telegramCallbackRequest
3. Validate req.ID != 0 → 422 validation_error ("id is required")
4. VerifyHMAC(req, botToken) → 401 invalid_signature
5. CheckAuthDate(req.AuthDate) → 401 auth_date_expired
6. svc.HandleCallback(ctx, CallbackInput{...})
   Error switch:
     ErrInvalidTelegramSignature → 401 invalid_signature  (defence-in-depth; service should not return this)
     ErrTelegramAuthDateExpired  → 401 auth_date_expired
     ErrProviderUIDTaken         → 409 provider_uid_taken
     ErrAccountLocked            → 423 account_locked
     default                     → slog.ErrorContext + 500 internal_error
7. token.MintTokens(w, ..., h.cfg) — error → 500 internal_error
8. result.NewUser == true → respond.JSON 201; else respond.JSON 200
   Body: {"access_token": mintResult.AccessToken, "token_type": "Bearer",
          "expires_in": int(cfg.AccessTTL.Seconds())}
```

**Note on MintTokens:** `token.MintTokens` sets the `refresh_token` HttpOnly cookie
internally (mirrors Google handler). The handler only needs to write the JSON response
body with the access token. Mirror the Google handler's `MintTokens` call exactly.

**Note on `account_inactive`:** The design doc §10 maps `ErrAccountInactive → 403`.
Add this case to the switch. The service currently does not return this sentinel but
the handler must be wired for it to keep the contract complete.

#### HandleLink — POST /oauth/telegram/link (JWT auth required)

Guard ordering (Stage 0 §4.2):
```
1. token.UserIDFromContext → missing/empty → 401 unauthorized
2. uuid.Parse(userIDStr) → error → 401 unauthorized
3. MaxBytesReader
4. DecodeJSON → telegramCallbackRequest
5. Validate req.ID != 0 → 422 validation_error
6. VerifyHMAC(req, botToken) → 401 invalid_signature
7. CheckAuthDate(req.AuthDate) → 401 auth_date_expired
8. svc.LinkTelegram(ctx, LinkInput{UserID: [16]byte(parsed), User: ..., IPAddress, UserAgent})
   Error switch:
     ErrInvalidTelegramSignature → 401 invalid_signature
     ErrTelegramAuthDateExpired  → 401 auth_date_expired
     ErrProviderAlreadyLinked    → 409 provider_already_linked
     ErrProviderUIDTaken         → 409 provider_uid_taken
     ErrAccountLocked            → 423 account_locked
     default                     → slog.ErrorContext + 500 internal_error
9. respond.NoContent(w) — 204
```

#### HandleUnlink — DELETE /oauth/telegram/unlink (JWT auth required)

Guard ordering (Stage 0 §4.3):
```
1. token.UserIDFromContext → missing/empty → 401 unauthorized
2. uuid.Parse(userIDStr) → error → 401 unauthorized
3. No body
4. svc.UnlinkTelegram(ctx, [16]byte(parsed), respond.ClientIP(r), r.UserAgent())
   Error switch:
     ErrProviderNotLinked → 404 provider_not_linked
     ErrLastAuthMethod    → 409 last_auth_method
     default              → slog.ErrorContext + 500 internal_error
5. respond.NoContent(w) — 204
```

---

### 2. `internal/domain/oauth/telegram/routes.go`

Mirror `internal/domain/oauth/google/routes.go` exactly for naming style,
section separators, and cleanup goroutine pattern.

```go
// Routes registers all Telegram OAuth endpoints on r.
// Call from the oauth root assembler:
//
//  telegram.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST   /telegram/callback — 10 req / 1 min per IP
//   - POST   /telegram/link     — 5 req / 15 min per authenticated user
//   - DELETE /telegram/unlink   — 5 req / 15 min per authenticated user
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
    // ── Rate limiters ──────────────────────────────────────────────────────

    // 10 req / 1 min per IP — deters widget replay abuse.
    cbLimiter := ratelimit.NewIPRateLimiter(
        deps.KVStore, "tgcb:ip:",
        10.0/(1*60), 10, 1*time.Minute,
    )
    go cbLimiter.StartCleanup(ctx)

    // 5 req / 15 min per user — deters repeated link attempts.
    linkLimiter := ratelimit.NewUserRateLimiter(
        deps.KVStore, "tglnk:usr:",
        5.0/(15*60), 5, 15*time.Minute,
    )
    go linkLimiter.StartCleanup(ctx)

    // 5 req / 15 min per user — deters unlink cycling.
    unlinkLimiter := ratelimit.NewUserRateLimiter(
        deps.KVStore, "tgunlk:usr:",
        5.0/(15*60), 5, 15*time.Minute,
    )
    go unlinkLimiter.StartCleanup(ctx)

    // ── Dependency wiring ──────────────────────────────────────────────────
    store := NewStore(deps.Pool)
    svc   := NewService(store)
    h     := NewHandler(svc, deps.OAuth.TelegramBotToken, deps.JWTConfig, deps.SecureCookies)

    // ── Route registration ─────────────────────────────────────────────────

    // POST /telegram/callback — Telegram Login Widget callback (IP-rate-limited; public)
    r.With(cbLimiter.Limit).Post("/telegram/callback", h.HandleCallback)

    // POST /telegram/link — link Telegram identity (JWT auth + user-rate-limited)
    r.With(deps.JWTAuth, linkLimiter.Limit).Post("/telegram/link", h.HandleLink)

    // DELETE /telegram/unlink — remove Telegram identity (JWT auth + user-rate-limited)
    r.With(deps.JWTAuth, unlinkLimiter.Limit).Delete("/telegram/unlink", h.HandleUnlink)
}
```

**Field name caveat:** Read `internal/app/deps.go` before writing this file.
Use the exact field path for the bot token. If the field does not yet exist on
`*app.Deps`, add it following the existing `OAuth` sub-struct pattern and
update `deps.go` accordingly.

---

### 3. `internal/domain/oauth/telegram/handler_test.go` — Handler unit tests

Package: `telegram_test`. No build tag.

#### Test infrastructure

**`newTestHandler` helper** (mirrors Google's `newHandler`):
```go
func newTestHandler(svc telegram.Servicer) *telegram.Handler {
    return telegram.NewHandler(
        svc,
        "test-bot-token",
        token.JWTConfig{
            JWTAccessSecret:  "test-access-secret-must-be-32bytes!!",
            JWTRefreshSecret: "test-refresh-secret-must-be-32bytes!",
            AccessTTL:        15 * time.Minute,
            SecureCookies:    false,
        },
        false, // secureCookies=false for tests
    )
}
```

**`validCallbackBody` helper** — builds a valid `telegramCallbackRequest` JSON body
with a correct HMAC for `"test-bot-token"`. Compute the HMAC at test time using
the same `VerifyHMAC` algorithm so the handler does not reject it.

**`newCallbackRequest(body string) *http.Request`** — POST with JSON body.
**`newLinkRequest(body, userIDStr string) *http.Request`** — POST with JSON body
and `token.InjectUserIDForTest` for the user ID.
**`newUnlinkRequest(userIDStr string) *http.Request`** — DELETE, no body.

**`assertJSONCode(t, w, wantCode)`** — decodes `{"code": "..."}` from response,
same pattern as Google handler test.

---

#### H-layer test cases from Stage 0 §12

All test functions are `t.Parallel()`.

**Callback (POST /telegram/callback):**

| Test ID | Test function name | Setup | Assert |
|---|---|---|---|
| T-01 | `TestHandleCallback_NewUser_Returns201` | FakeServicer returns `CallbackResult{NewUser: true, Session: ...}` | 201; access_token in body; refresh_token cookie set, HttpOnly=true |
| T-02 | `TestHandleCallback_ReturningUser_Returns200` | FakeServicer returns `CallbackResult{NewUser: false, Session: ...}` | 200; access_token in body; refresh_token cookie set |
| T-03 | `TestHandleCallback_InvalidHMAC_Returns401` | Body has wrong `hash` field | 401; code=`invalid_signature` |
| T-04 | `TestHandleCallback_AuthDateTooOld_Returns401` | `auth_date` = `time.Now().Unix() - 90000` (>86400s old); valid HMAC for this payload | 401; code=`auth_date_expired` |
| T-05 | `TestHandleCallback_AuthDateInFuture_Returns401` | `auth_date` = `time.Now().Unix() + 120` (>60s future); valid HMAC | 401; code=`auth_date_expired` |
| T-06 | `TestHandleCallback_MissingID_Returns422` | JSON body `{"id":0,"auth_date":..., "hash":"..."}` (zero ID) | 422; code=`validation_error` |
| T-07 | `TestHandleCallback_AccountLocked_Returns423` | FakeServicer returns `oauthshared.ErrAccountLocked` | 423; code=`account_locked` |
| T-08 | `TestHandleCallback_AccountInactive_Returns403` | FakeServicer returns `oauthshared.ErrAccountInactive` (if it exists, else skip this case with a note) | 403; code=`account_inactive` |
| T-09 | `TestHandleCallback_MalformedJSON_Returns400` | Body = `{not valid json` | 400 |
| T-10 | `TestHandleCallback_ProviderUIDTaken_Returns409` | FakeServicer returns `telegram.ErrProviderUIDTaken` | 409; code=`provider_uid_taken` |
| T-11 | `TestHandleCallback_InternalError_Returns500` | FakeServicer returns `errors.New("db down")` | 500; code=`internal_error` |
| — | `TestHandleCallback_MintTokensFailure_Returns500` | FakeServicer returns valid `CallbackResult` but `RefreshExpiry` is zero (forces MintTokens error) | 500; code=`internal_error` |
| — | `TestHandleCallback_NewUser_SetsRefreshTokenCookieHttpOnly` | FakeServicer returns new-user result | refresh_token cookie has `HttpOnly=true`, `Path="/api/v1/auth"` |

**Link (POST /telegram/link):**

| Test ID | Test function name | Setup | Assert |
|---|---|---|---|
| T-12 | `TestHandleLink_HappyPath_Returns204` | Valid body; valid HMAC; FakeServicer returns nil | 204 |
| T-13 | `TestHandleLink_AlreadyLinked_Returns409` | FakeServicer returns `telegram.ErrProviderAlreadyLinked` | 409; code=`provider_already_linked` |
| T-14 | `TestHandleLink_ProviderUIDTaken_Returns409` | FakeServicer returns `telegram.ErrProviderUIDTaken` | 409; code=`provider_uid_taken` |
| T-15 | `TestHandleLink_InvalidHMAC_Returns401` | Body with wrong hash | 401; code=`invalid_signature` |
| T-16 | `TestHandleLink_AuthDateExpired_Returns401` | auth_date too old; valid HMAC for this payload | 401; code=`auth_date_expired` |
| — | `TestHandleLink_MissingJWT_Returns401` | No user ID in context | 401; code=`unauthorized` |
| — | `TestHandleLink_MalformedUserID_Returns401` | `InjectUserIDForTest` with `"not-a-uuid"` | 401; code=`unauthorized` |
| — | `TestHandleLink_MissingID_Returns422` | id=0 in body | 422; code=`validation_error` |
| — | `TestHandleLink_InternalError_Returns500` | FakeServicer returns `errors.New("db down")` | 500; code=`internal_error` |

**Unlink (DELETE /telegram/unlink):**

| Test ID | Test function name | Setup | Assert |
|---|---|---|---|
| T-19 | `TestHandleUnlink_HappyPath_Returns204` | FakeServicer returns nil | 204 |
| T-21 | `TestHandleUnlink_ProviderNotLinked_Returns404` | FakeServicer returns `telegram.ErrProviderNotLinked` | 404; code=`provider_not_linked` |
| T-22 | `TestHandleUnlink_LastAuthMethod_Returns409` | FakeServicer returns `oauthshared.ErrLastAuthMethod` | 409; code=`last_auth_method` |
| T-23 | `TestHandleUnlink_MissingJWT_Returns401` | No user ID in context | 401; code=`unauthorized` |
| — | `TestHandleUnlink_MalformedUserID_Returns401` | `InjectUserIDForTest` with `"not-a-uuid"` | 401; code=`unauthorized` |
| — | `TestHandleUnlink_InternalError_Returns500` | FakeServicer returns `errors.New("db down")` | 500; code=`internal_error` |

> **T-08 note:** If `oauthshared.ErrAccountInactive` does not exist in
> `internal/domain/oauth/shared/errors.go`, skip T-08 and add a comment:
> `// T-08: ErrAccountInactive not yet defined in oauthshared — add when wired.`
> Do not invent the sentinel; check the file first.

> **T-11 / T-18 / T-24 (rate limit) note:** Rate limiter tests require a real
> KV store and are E2E concerns. Handler unit tests for the rate-limit path are
> not written here — they are covered by the E2E collection in Stage 7.

---

## Done when

```bash
# Package compiles with Handler + routes
go build ./internal/domain/oauth/telegram/...

# Full project still builds
go build ./internal/...

# All handler unit tests pass
go test ./internal/domain/oauth/telegram/... -run TestHandle -v

# All tests in the package pass
go test ./internal/domain/oauth/telegram/... -v
```

Run the §3.13 checklist against `handler.go` and `routes.go` before submitting.

**Stop here. Do not write integration tests or modify store files. Stage 5 (audit) starts in a separate session.**
