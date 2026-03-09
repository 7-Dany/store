# Telegram OAuth — Stage 5: Audit Review

**Feature:** Telegram OAuth (§D-2)
**Package:** `internal/domain/auth/oauth/telegram/`
**Depends on:** Stage 4 complete — all production files compile.
`go build ./internal/domain/auth/oauth/telegram/...` passes. All H-layer unit tests green.

---

## Instructions for the reviewer

You are performing a structured multi-role audit of the Telegram OAuth HTTP layer.

**Before writing anything, read these files in full:**

1. `docs/prompts/telegram/context.md` — resolved paths, decisions, sentinel names, rate-limit prefixes, test case IDs
2. `docs/prompts/telegram/0-design.md` — full Stage 0 design spec (§4 data flows, §5 security decisions, §12 test cases)
3. `internal/domain/auth/oauth/telegram/handler.go`
4. `internal/domain/auth/oauth/telegram/routes.go`
5. `internal/domain/auth/routes.go` (domain assembler)
6. `internal/domain/auth/oauth/telegram/models.go`
7. `internal/domain/auth/oauth/telegram/errors.go`
8. `internal/domain/auth/oauth/telegram/service.go`
9. `internal/domain/auth/shared/errors.go`
10. `internal/audit/audit.go` — `const` block + `AllEvents()`
11. `internal/platform/token/cookie.go`
12. `internal/platform/token/jwt.go`
13. `internal/platform/kvstore/store.go`
14. `internal/platform/respond/respond.go`
15. `docs/RULES.md`

Produce **exactly four parts**, in order. Each part is written from the
perspective of a different reviewer role. No extra sections.

---

## Part 1 — Security Engineer

*Focus: HMAC integrity, replay-attack window, constant-time comparison, bot-token
sourcing, audit-log completeness, cookie flags, error information leakage.*

For each finding, produce one entry:

```
SEVERITY    Critical | High | Medium | Low | Info
LOCATION    <file>:<function or line>
FINDING     <one-sentence description>
IMPACT      <what an attacker or buggy client could do if unfixed>
FIX         <what to change and why>
```

Cover **every item** in this checklist — report ✓ (pass) or a finding for each:

### 1.1 HMAC Verification (D-07, D-08)

- [ ] `VerifyHMAC` is called before any DB read in all three handlers
- [ ] Hash comparison uses `hmac.Equal` — not `==`, `bytes.Equal`, or string comparison on hex
- [ ] `data_check_string` is built from alphabetically sorted `key=value` pairs with the `hash` field excluded
- [ ] The SHA-256 of the raw bot-token bytes is used as the HMAC secret key (not the token string directly)
- [ ] An invalid or missing hash returns `ErrInvalidTelegramSignature` → 401 immediately, with no DB side-effects

### 1.2 Replay-Attack Window (D-09)

- [ ] Rejected if `time.Now().Unix() - auth_date > 86400` (one day old)
- [ ] Rejected if `auth_date - time.Now().Unix() > 60` (more than 60 s in the future)
- [ ] Both checks return `ErrTelegramAuthDateExpired` → 401
- [ ] `time.Now()` is called inside `CheckAuthDate` (not captured once at request parse time)

### 1.3 Bot Token Sourcing (D-10, D-15)

- [ ] Bot token is read exclusively from `deps.Config.TelegramBotToken` — never from the request body or a query param
- [ ] Handler or service panics / returns an error at construction time if `TelegramBotToken` is empty (startup guard)
- [ ] Bot token is never logged, included in error responses, or written to audit metadata

### 1.4 Audit Logging (D-11)

- [ ] `EventOAuthLogin` is written in the callback handler (both new-user and returning-user paths) via `context.WithoutCancel(ctx)`
- [ ] `EventOAuthLinked` is written in the link handler via `context.WithoutCancel(ctx)`
- [ ] `EventOAuthUnlinked` is written in the unlink handler via `context.WithoutCancel(ctx)`
- [ ] A client disconnect (cancelled `ctx`) cannot abort any of the three audit writes
- [ ] Audit metadata shape: `{"provider": "telegram", "new_user": true/false}` for login; `{"provider": "telegram"}` for link/unlink

### 1.5 Cookie Security

- [ ] `token.MintTokens` (or `token.SetRefreshCookie`) is used in callback — not a hand-rolled `Set-Cookie` header
- [ ] Refresh-token cookie has `HttpOnly` and `SameSite=Strict` (or Lax) enforced by the platform helper
- [ ] No token material is appended as a query parameter to any redirect URL

### 1.6 Error Information Leakage

- [ ] Internal DB errors and Go sentinel strings are not surfaced in HTTP 5xx responses
- [ ] 401 responses for HMAC failure and expired auth_date use `invalid_signature` / `auth_date_expired` code strings — not Go error text
- [ ] `ErrProviderUIDTaken` and `ErrProviderAlreadyLinked` 409 responses do not reveal the other user's identity

---

## Part 2 — Go Senior Engineer

*Focus: idiomatic Go, error handling discipline, guard ordering correctness,
concurrency and shutdown, interface satisfaction, import hygiene, code clarity.*

Use the same finding format as Part 1.

Cover **every item** in this checklist:

### 2.1 Error Handling

- [ ] All `fmt.Errorf` wrapping uses `%w` (not `%v`) so `errors.Is` chains work
- [ ] No sentinel defined in wrong package (Telegram-specific sentinels are in `telegram/errors.go`, not `authshared`)
- [ ] `errors.Is` used for all sentinel comparisons — no `==` on error values
- [ ] No service error silently swallowed in a default branch
- [ ] Default branch logs via `slog.ErrorContext` before responding 500

### 2.2 Guard Ordering

Compare each handler method against `0-design.md §4` line-by-line.
Create one sub-section per handler method:

**Callback (§4.1):**
- [ ] Step 1: `MaxBytesReader` applied before body decode
- [ ] Step 2: `DecodeJSON → telegramCallbackRequest`
- [ ] Step 3: `VerifyHMAC(req, botToken)` → 401 on failure (before any DB read)
- [ ] Step 4: `CheckAuthDate(req.AuthDate)` → 401 on failure
- [ ] Step 5: `store.GetIdentityByProviderUID` → branch on found/not-found
- [ ] ExistingUserPath Step 6: `store.GetUserForOAuth` → 404 / 423 / 403 as appropriate
- [ ] ExistingUserPath Step 7: `store.CallbackTx` (create session + refresh token, update identity profile if changed)
- [ ] ExistingUserPath Step 8: audit `EventOAuthLogin` (`new_user=false`) via `WithoutCancel`
- [ ] ExistingUserPath Step 9: `token.MintTokens` → `respond.JSON` 200
- [ ] NewUserPath Step 6: `store.CreateUserWithTelegramTx` (user + identity + session + refresh_token in one TX)
- [ ] NewUserPath Step 7: audit `EventOAuthLogin` (`new_user=true`) via `WithoutCancel`
- [ ] NewUserPath Step 8: `token.MintTokens` → `respond.JSON` 201

**Link (§4.2):**
- [ ] Step 1: JWT middleware → `token.UserIDFromContext`
- [ ] Step 2: `MaxBytesReader`
- [ ] Step 3: `DecodeJSON → telegramCallbackRequest`
- [ ] Step 4: `VerifyHMAC` → 401
- [ ] Step 5: `CheckAuthDate` → 401
- [ ] Step 6: `store.GetIdentityByUserAndProvider` → 409 `provider_already_linked` if found
- [ ] Step 7: `store.GetIdentityByProviderUID` → 409 `provider_uid_taken` if found with different userID
- [ ] Step 8: `store.LinkIdentityTx` (atomically inserts identity with SELECT FOR UPDATE)
- [ ] Step 9: audit `EventOAuthLinked` via `WithoutCancel`
- [ ] Step 10: `respond.NoContent` 204

**Unlink (§4.3):**
- [ ] Step 1: JWT middleware → `token.UserIDFromContext`
- [ ] Step 2: No body decode
- [ ] Step 3: `store.GetUserIdentitiesWithPassword` (fetches all identities + hasPassword flag)
- [ ] Step 4a: `hasTelegramIdentity` check → 404 `provider_not_linked` if false
- [ ] Step 4b: `hasOtherAuthMethod` check (password OR another identity) → 409 `last_auth_method` if false
- [ ] Step 5: `store.DeleteIdentityTx`
- [ ] Step 6: audit `EventOAuthUnlinked` via `WithoutCancel`
- [ ] Step 7: `respond.NoContent` 204

### 2.3 Concurrency and Shutdown

- [ ] Every `go limiter.StartCleanup(ctx)` passes the application root `ctx` — not `context.Background()`
- [ ] No goroutines ignore `ctx.Done()` (shutdown bug per RULES §2.6)
- [ ] No shared mutable state accessed across goroutines without synchronisation

### 2.4 Interface Satisfaction

- [ ] `*Store` satisfies the `Storer` interface declared in `service.go` (compile-time check or direct registration)
- [ ] `*Service` satisfies the `Servicer` interface declared in `handler.go`
- [ ] Handler's injected `Servicer` is the package-local interface, not the concrete `*Service`

### 2.5 Package and Import Hygiene

- [ ] No production file imports a `testutil` / `_test` package
- [ ] No circular domain imports (`telegram` package never imports another domain package)
- [ ] `authshared` is not imported for Telegram-specific sentinels (they live in `telegram/errors.go`)

### 2.6 Code Clarity and Idioms

- [ ] Helper functions (`VerifyHMAC`, `CheckAuthDate`, `buildDataCheckString`) are pure — no hidden side-effects
- [ ] Package-level constants used for magic values (replay window `86400`, future-skew `60`, KV prefixes)
- [ ] No `TODO`, `FIXME`, or `HACK` comments without an issue reference

---

## Part 3 — Platform Compliance Reviewer

*Focus: correct and consistent use of `internal/platform/` abstractions.
Every concern in the table must use the canonical platform helper.*

For each row, state **✓ Correct**, **✗ Violation**, or **N/A**.
For violations, add a finding entry in the same format as Part 1.

| Concern | Required platform helper | Status |
|---|---|---|
| JSON success response | `respond.JSON` | |
| JSON error response | `respond.Error` | |
| 204 No Content | `respond.NoContent` | |
| Request body decode | `respond.DecodeJSON[T]` | |
| Client IP extraction | `respond.ClientIP(r)` | |
| Body size cap | `http.MaxBytesReader` + `respond.MaxBodyBytes` | |
| Refresh token cookie | `token.MintTokens` (sets it internally) | |
| Access token signing | `token.MintTokens` — not hand-rolled | |
| User ID from context | `token.UserIDFromContext` | |
| JWT parsing (best-effort) | `token.ParseAccessToken` — not `token.JWTSubjectExtractor` | |
| KV get / set / delete | `kvstore.Store` interface methods | |
| IP rate limiting (callback) | `ratelimit.NewIPRateLimiter` with prefix `tgcb:ip:` | |
| User rate limiting (link) | `ratelimit.NewUserRateLimiter` with prefix `tglnk:usr:` | |
| User rate limiting (unlink) | `ratelimit.NewUserRateLimiter` with prefix `tgunlk:usr:` | |
| Encryption at rest | N/A — `access_token` is NULL for Telegram (D-03, D-12) | |

Additionally verify:

- [ ] Domain assembler `auth/routes.go` returns `*chi.Mux` — matches `auth.Routes` pattern
- [ ] Feature `routes.go` has signature `func Routes(ctx context.Context, r chi.Router, deps *app.Deps)` — no return value
- [ ] `AllowContentType("application/json")` is applied to POST /callback and POST /link; DELETE /unlink correctly omits it (no body)
- [ ] All three audit event constants (`EventOAuthLogin`, `EventOAuthLinked`, `EventOAuthUnlinked`) appear in `AllEvents()` in `internal/audit/audit.go`
- [ ] All KV prefix strings match `context.md` exactly: `tgcb:ip:`, `tglnk:usr:`, `tgunlk:usr:`

---

## Part 4 — Test Coverage Reviewer

*Focus: identify every untested path in `handler.go`. Mark existing tests,
flag missing tests, and explain structurally unreachable branches.*

Cross-reference `handler_test.go` against the complete handler source.

Structure your output as:

```
### handler.go

#### Callback — unit tests (no build tag)
- [x/❌] T-NN: {scenario} → {expected outcome}

#### Link — unit tests (no build tag)
- [x/❌] T-NN: {scenario} → {expected outcome}

#### Unlink — unit tests (no build tag)
- [x/❌] T-NN: {scenario} → {expected outcome}

#### Structurally unreachable paths (no test stub needed)
- {function}:{branch} — {reason}
```

Use `[x]` for cases already in `handler_test.go`.
Use `[❌]` for cases that are **missing and must be added** before Stage 6.

**Required test cases from Stage 0 §12 (H-layer):**

| ID | Handler method | Scenario |
|---|---|---|
| T-01 | Callback | valid new user → 201, tokens in body + cookie |
| T-02 | Callback | valid returning user → 200, tokens in body + cookie |
| T-03 | Callback | invalid HMAC → 401 `invalid_signature` |
| T-04 | Callback | auth_date > 86400s old → 401 `auth_date_expired` |
| T-05 | Callback | auth_date in future (> 60s) → 401 `auth_date_expired` |
| T-06 | Callback | missing required field (id=0) → 422 `validation_error` |
| T-07 | Callback | returning user, account locked → 423 `account_locked` |
| T-08 | Callback | returning user, account inactive → 403 `account_inactive` |
| T-09 | Callback | malformed JSON → 400 |
| T-10 | Callback | provider_uid race (store returns ErrProviderUIDTaken) → 409 `provider_uid_taken` |
| T-11 | Callback | IP rate limited → 429 |
| T-12 | Link | valid link, success → 204 |
| T-13 | Link | already linked to this user → 409 `provider_already_linked` |
| T-14 | Link | Telegram account linked to another user → 409 `provider_uid_taken` |
| T-15 | Link | invalid HMAC → 401 `invalid_signature` |
| T-16 | Link | auth_date expired → 401 `auth_date_expired` |
| T-17 | Link | no JWT → 401 (JWTAuth middleware — structurally unreachable in unit test) |
| T-18 | Link | user rate limited → 429 |
| T-19 | Unlink | valid unlink, user has password → 204 |
| T-20 | Unlink | valid unlink, user has another OAuth identity → 204 |
| T-21 | Unlink | provider not linked → 404 `provider_not_linked` |
| T-22 | Unlink | last auth method → 409 `last_auth_method` |
| T-23 | Unlink | no JWT → 401 (JWTAuth middleware — structurally unreachable in unit test) |
| T-24 | Unlink | user rate limited → 429 |

**Additional cases to check for (full coverage beyond T-NN list):**

- Callback: body exceeds `respond.MaxBodyBytes` → 413
- Callback: `store.GetUserForOAuth` returns unexpected error → 500 `internal_error`
- Callback: `store.CreateUserWithTelegramTx` returns unexpected error → 500 `internal_error`
- Callback: `store.CallbackTx` returns unexpected error → 500 `internal_error`
- Link: body exceeds `respond.MaxBodyBytes` → 413
- Link: `store.LinkIdentityTx` returns unexpected error → 500 `internal_error`
- Unlink: `store.GetUserIdentitiesWithPassword` returns unexpected error → 500 `internal_error`
- Unlink: `store.DeleteIdentityTx` returns unexpected error → 500 `internal_error`

---

## Sync checklist before closing this stage

- [ ] All Part 1 Critical and High findings resolved
- [ ] All Part 2 guard-ordering deviations corrected
- [ ] All Part 3 platform violations corrected
- [ ] All Part 4 `[❌]` missing tests added to `handler_test.go`
- [ ] `go build ./internal/domain/auth/oauth/telegram/...` passes
- [ ] `go vet ./internal/domain/auth/oauth/telegram/...` passes
- [ ] `go test ./internal/domain/auth/oauth/telegram/...` green — all T-01 through T-24 pass

Once all items are checked, run unit tests manually and proceed to Stage 6 when they pass.
