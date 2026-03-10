# §B-3 Delete Account — Stage 5: Audit

**Feature:** Delete Account (§B-3)
**Package:** `internal/domain/profile/delete-account/` (package `deleteaccount`)
**Depends on:** Stage 4 complete — `handler.go`, `routes.go`, `handler_test.go` exist and compile.

---

## Read first (before reviewing)

| File | Role |
|---|---|
| `docs/prompts/delete/context.md` | All |
| `docs/prompts/delete/0-design.md §5` (guard ordering) | Security + Go Engineer |
| `docs/prompts/delete/0-design.md §9` (test inventory) | Test Coverage |
| `internal/domain/profile/delete-account/handler.go` | All |
| `internal/domain/profile/delete-account/routes.go` | Go Engineer + Platform |
| `internal/domain/profile/delete-account/service.go` | Security + Go Engineer |
| `internal/domain/profile/delete-account/errors.go` | Security + Go Engineer |
| `internal/domain/profile/delete-account/handler_test.go` | Test Coverage |
| `internal/domain/profile/routes.go` | Platform |
| `internal/audit/audit.go` | Security + Platform |
| `internal/platform/respond/respond.go` | Platform |
| `internal/platform/ratelimit/ratelimit.go` | Platform |
| `internal/domain/auth/shared/errors.go` | Security + Go Engineer |

---

## Part 1 — Security Engineer

*Focus: soft-delete integrity, audit write protection against client disconnect,
Telegram HMAC re-auth, OTP attempt budget enforcement, session non-revocation
intent (D-02), error information leakage.*

For each finding:

```
SEVERITY    Critical | High | Medium | Low | Info
LOCATION    <file>:<function or line>
FINDING     <one-sentence description>
IMPACT      <what an attacker or buggy client could do if unfixed>
FIX         <what to change and why>
```

### 1.1 Audit Write Protection (ADR-004 / context.md D-02)

All three audit events (`account_deletion_requested`, `account_deletion_otp_requested`,
`account_deletion_cancelled`) must be written using `context.WithoutCancel` so a
mid-request client disconnect cannot abort the DB write and leave the account in an
inconsistent state.

- [ ] `ScheduleDeletionTx` (store.go): audit write uses `context.WithoutCancel`
- [ ] `SendDeletionOTPTx` (store.go): audit write uses `context.WithoutCancel`
- [ ] `CancelDeletionTx` (store.go): audit write uses `context.WithoutCancel`
- [ ] `IncrementAttemptsTx` call in `ConfirmEmailDeletion` (service.go): uses `context.WithoutCancel` — verify the call site passes a detached context

### 1.2 Telegram HMAC Re-Auth (context.md D-08, D-09)

- [ ] `ConfirmTelegramDeletion` (service.go): replay guard fires before HMAC check (`auth_date` > `time.Now().Unix() - 86400`)
- [ ] `ConfirmTelegramDeletion` (service.go): `verifyTelegramHMAC` returns false → `ErrInvalidTelegramAuth` (not a wrapped internal error that could leak stack info)
- [ ] `ConfirmTelegramDeletion` (service.go): `GetIdentityByUserAndProvider` not-found path returns `ErrInvalidCredentials` (not `ErrTelegramIdentityMismatch` — the latter is reserved for a found-but-mismatched `provider_uid`)
- [ ] `ConfirmTelegramDeletion` (service.go): ownership check compares `providerUID == strconv.FormatInt(telegramAuth.ID, 10)` — mismatch → `ErrTelegramIdentityMismatch`
- [ ] Handler (handler.go): `ErrInvalidTelegramAuth` maps to **401** `invalid_telegram_auth` (not 422 or 400)
- [ ] Handler (handler.go): `ErrTelegramIdentityMismatch` maps to **401** `telegram_identity_mismatch`

### 1.3 OTP Attempt Budget (context.md D-19)

- [ ] `ConfirmEmailDeletion` (service.go): `ErrTooManyAttempts` check fires *before* `VerifyCodeHash` (via `CheckOTPToken`) — so a budget-exhausted token never reaches hash comparison
- [ ] `IncrementAttemptsTx` is called only on `ErrInvalidCode` — not on `ErrTooManyAttempts`, `ErrTokenExpired`, or `ErrTokenNotFound`
- [ ] `ErrTokenExpired` is mapped to `ErrTokenNotFound` (per authshared convention) — not leaked as a separate error code

### 1.4 409 Guard Position (context.md D-18)

The `ErrAlreadyPendingDeletion` guard must fire as the *first service check* after
`mustUserID`, before any auth-method dispatch or OTP verification:

- [ ] In `DeleteWithPassword`: `user.DeletedAt != nil` check appears before password verification
- [ ] In `ConfirmEmailDeletion`: `user.DeletedAt != nil` check appears before OTP lookup
- [ ] In `ConfirmTelegramDeletion`: `user.DeletedAt != nil` check appears before HMAC verification
- [ ] In `ResolveUserForDeletion`: `user.DeletedAt != nil` check appears before `GetUserAuthMethods`

### 1.5 Session Non-Revocation Intent (context.md D-02)

- [ ] `ScheduleDeletionTx` (store.go): does NOT call `RevokeAllSessions`, `InvalidateRefreshTokens`, or any token-revocation method
- [ ] Handler `Delete` (handler.go): does NOT clear the refresh cookie after a successful soft-delete
- [ ] Handler `Delete` (handler.go): does NOT call `token.ClearRefreshCookie` or set a zero-value `Set-Cookie` header

### 1.6 Error Information Leakage

- [ ] Default branch in `mapDeleteError` calls `slog.ErrorContext` with the raw `err` (server-side logging only) and responds with the opaque string `"internal server error"` — not `err.Error()`
- [ ] Default branch in `CancelDeletion` error switch does the same
- [ ] `ErrInvalidCredentials` response message does not reveal whether the account lacks a password vs supplied a wrong password

---

## Part 2 — Go Senior Engineer

*Focus: idiomatic Go, error handling discipline, guard ordering vs spec,
concurrency and shutdown, interface satisfaction, import hygiene.*

### 2.1 Error Handling

- [ ] All `fmt.Errorf` wrapping in `service.go` uses `%w` (not `%v`) so `errors.Is` chains work
- [ ] No service sentinel leaked from the wrong package (e.g. `profileshared.ErrUserNotFound` is not returned raw to the handler — it is wrapped as a 500)
- [ ] `errors.Is` used for all sentinel comparisons in `mapDeleteError` — no `==` on error values
- [ ] `ErrTokenExpired` is converted to `ErrTokenNotFound` in `ConfirmEmailDeletion` before returning (per authshared convention — expired = absent)
- [ ] Default branch in `mapDeleteError` logs via `slog.ErrorContext` before responding 500
- [ ] Default branch in `CancelDeletion` error switch logs via `slog.ErrorContext` before responding 500

### 2.2 Guard Ordering

Verify each handler method in `handler.go` matches `0-design.md §5` line-by-line.

**Delete — Path A (password present):**
- [ ] Step 1: `MaxBytesReader` set before `mustUserID`
- [ ] Step 2: `mustUserID` → 401 if absent
- [ ] Step 3: `DecodeJSON[deleteAccountRequest]` → 400/422 on malformed body
- [ ] Step 4: `req.Password != ""` guard triggers `DeleteWithPassword`; returns before any other path

**Delete — Path B-2 (code present):**
- [ ] Step 5: `req.Code != ""` guard triggers `ConfirmEmailDeletion`; returns before Telegram/empty-body paths

**Delete — Path C-2 (telegram_auth present):**
- [ ] Step 6: `req.TelegramAuth != nil` guard triggers `ConfirmTelegramDeletion`; returns before empty-body path

**Delete — Empty body (dispatch step 1):**
- [ ] Step 7: `ResolveUserForDeletion` called only when all three field-present guards fail
- [ ] Step 8: `authMethods.HasPassword == true` → 400 `validation_error` (D-11)
- [ ] Step 9: `user.Email != nil` → Path B-1 (`InitiateEmailDeletion` + enqueue email + 202)
- [ ] Step 10: `user.Email == nil` → Path C-1 (202 `auth_method: telegram`)

**CancelDeletion:**
- [ ] Step 1: `MaxBytesReader` set before `mustUserID`
- [ ] Step 2: `mustUserID` → 401 if absent
- [ ] Step 3: `DecodeJSON[cancelDeletionRequest]` — body parsed (even though struct is empty)
- [ ] Step 4: `svc.CancelDeletion` called; `ErrNotPendingDeletion` → 409; default → 500

### 2.3 Concurrency and Shutdown

- [ ] `deleteLimiter.StartCleanup(ctx)` and `cancelLimiter.StartCleanup(ctx)` both receive the application root `ctx` passed into `Routes(ctx, ...)` — not `context.Background()`
- [ ] Neither goroutine ignores `ctx.Done()` (the `StartCleanup` implementation must honour context cancellation — verify in `ratelimit.go`)
- [ ] No shared mutable state in `Handler` accessed across goroutines

### 2.4 Interface Satisfaction

- [ ] `handler.go` does NOT redeclare `Servicer` — it is already declared in `service.go` (same package); importing from `service.go` is the only `Servicer` definition
- [ ] `var _ Servicer = (*Service)(nil)` compile-time check exists in `service.go`
- [ ] `var _ deleteaccount.Servicer = (*DeleteAccountFakeServicer)(nil)` compile-time check exists in `fake_servicer.go`

### 2.5 Package and Import Hygiene

- [ ] `handler.go` does not import any `testutil` / `_test` package
- [ ] `handler.go` does not import `internal/domain/auth/login` or any other domain package (cross-domain import)
- [ ] `routes.go` does not import `service.go` types directly — uses only the package-level constructors `NewStore`, `NewService`, `NewHandler`
- [ ] `handler_test.go` uses `package deleteaccount_test` (external test package), not `package deleteaccount`

### 2.6 Code Clarity and Idioms

- [ ] `newDeletionScheduledResponse` is a pure helper with no side effects — called identically by all three success paths
- [ ] Response type `deletionInitiatedResponse` is reused for both the 202 email-OTP response and the 202 Telegram response (the `AuthMethod` field is omitempty)
- [ ] Response type `deletionInitiatedResponse` is also reused for the 200 cancel-deletion response — or a separate `messageResponse` struct is used; verify consistency with the email/handler.go convention
- [ ] `mapDeleteError` is a single shared method — not copy-pasted into each code path
- [ ] Package-level constants used for TTLs and rate-limit rates in `routes.go` (or inline float literals are acceptable per project conventions — verify against analogous `routes.go` files)

---

## Part 3 — Platform Compliance Reviewer

*Focus: correct and consistent use of `internal/platform/` abstractions.*

For each row, state **✓ Correct**, **✗ Violation**, or **N/A**.
For violations, add a finding entry in the same format as Part 1.

| Concern | Required platform helper | Status |
|---|---|---|
| JSON success response | `respond.JSON` | |
| JSON error response | `respond.Error` | |
| 204 No Content | `respond.NoContent` | N/A — no 204 in this feature |
| Request body decode | `respond.DecodeJSON[T]` | |
| Client IP extraction | `respond.ClientIP(r)` | |
| Body size cap | `http.MaxBytesReader` + `respond.MaxBodyBytes` | |
| Refresh token cookie | N/A — D-02: sessions not revoked; no cookie changes | N/A |
| Access token signing | N/A — no new tokens issued | N/A |
| User ID from context | `token.UserIDFromContext` | |
| JWT parsing (best-effort) | N/A — handler relies on JWTAuth middleware; no manual parsing | N/A |
| KV get / set / delete | `kvstore.Store` interface via `ratelimit.NewUserRateLimiter` | |
| IP rate limiting | N/A — both routes use user-rate limiting, not IP | N/A |
| User rate limiting | `ratelimit.NewUserRateLimiter` | |
| Encryption at rest | N/A — no OAuth tokens stored in this flow | N/A |

Additionally verify:

- [ ] `internal/domain/profile/routes.go` returns `*chi.Mux` — matches `profile.Routes` pattern
- [ ] `deleteaccount/routes.go` has signature `func Routes(ctx context.Context, r chi.Router, deps *app.Deps)` — no return value (consistent with `email.Routes`, `setpassword.Routes`)
- [ ] `AllowContentType("application/json")` is inherited from the profile domain assembler — `deleteaccount/routes.go` does NOT re-add it (it would double-wrap)
- [ ] All three audit event constants appear in `AllEvents()` in `internal/audit/audit.go`:
  - [ ] `EventAccountDeletionRequested`
  - [ ] `EventAccountDeletionOTPRequested`
  - [ ] `EventAccountDeletionCancelled`
- [ ] KV prefix strings match `context.md` exactly:
  - [ ] `"del:usr:"` for `DELETE /me` limiter
  - [ ] `"delc:usr:"` for `POST /me/cancel-deletion` limiter
- [ ] `mailer/templates/account_deletion.go` defines `AccountDeletionOTPKey` and `AccountDeletionOTPTemplate`
- [ ] `mailer/templates/registry.go` has an entry for `AccountDeletionOTPKey`

---

## Part 4 — Test Coverage Reviewer

*Focus: identify every untested H-layer path in `handler.go`. Mark existing tests,
flag missing tests, and explain structurally unreachable branches.*

Cross-reference `handler_test.go` against `handler.go` and the complete T-NN inventory
from `0-design.md §9`.

### handler.go

#### Delete — required H-layer cases (from 0-design.md §9)

| ID | Handler path | Scenario | Status |
|---|---|---|---|
| T-01 | Path A | Password user, correct password → 200 with scheduled_deletion_at | |
| T-02 | Path B-1 | Email-OTP user, empty body → 202 "verification code sent" | |
| T-03 | Path B-2 | Email-OTP user, correct 6-digit code → 200 with scheduled_deletion_at | |
| T-04 | Path C-1 | Telegram-only user, empty body → 202 with `auth_method: telegram` | |
| T-05 | Path C-2 | Telegram user, valid HMAC payload → 200 with scheduled_deletion_at | |
| T-06 | All paths | Any service fn returns `ErrAlreadyPendingDeletion` → 409 `already_pending_deletion` | |
| T-07 | Empty body | `authMethods.HasPassword == true` → 400 `validation_error` | |
| T-09 | Path B-2 | `ConfirmEmailDeletionFn` returns `ErrCodeInvalidFormat` → 422 `validation_error` | |
| T-14 | Path C-2 | `ConfirmTelegramDeletionFn` returns `ErrInvalidTelegramAuth` → 401 `invalid_telegram_auth` | |
| T-15 | Path C-2 | `ConfirmTelegramDeletionFn` returns `ErrInvalidTelegramAuth` (old auth_date) → 401 | |
| T-17 | Delete + Cancel | No JWT in context → 401 `unauthorized` | |
| T-20 | Path A | `scheduled_deletion_at` field present and correct in response JSON | |

#### CancelDeletion — required H-layer cases

| ID | Scenario | Status |
|---|---|---|
| T-27 | `CancelDeletionFn` returns nil → 200 "account deletion cancelled" | |
| T-28 | `CancelDeletionFn` returns `ErrNotPendingDeletion` → 409 `not_pending_deletion` | |
| T-29 | No JWT on cancel → 401 `unauthorized` | |

#### Additional cases to check for (full branch coverage beyond T-NN list)

Use `[x]` for cases already in `handler_test.go`; `[❌]` for missing cases.

```
### Delete handler
- [ ] Malformed JSON body → 400
- [ ] Body > respond.MaxBodyBytes → 413
- [ ] Path A: `DeleteWithPasswordFn` returns unexpected error → 500 `internal_error`
- [ ] Path B-2: `ConfirmEmailDeletionFn` returns `ErrTokenNotFound` → 422 `token_not_found`
- [ ] Path B-2: `ConfirmEmailDeletionFn` returns `ErrTokenAlreadyUsed` → 422 `token_already_used`
- [ ] Path B-2: `ConfirmEmailDeletionFn` returns `ErrInvalidCode` → 422 `invalid_code`
- [ ] Path B-2: `ConfirmEmailDeletionFn` returns `ErrTooManyAttempts` → 429 `too_many_attempts`
- [ ] Path C-2: `ConfirmTelegramDeletionFn` returns `ErrTelegramIdentityMismatch` → 401 `telegram_identity_mismatch`
- [ ] Empty body: `ResolveUserForDeletionFn` returns error → mapDeleteError
- [ ] Path B-1: `InitiateEmailDeletionFn` returns error → mapDeleteError

### CancelDeletion handler
- [ ] Malformed JSON body → 400
- [ ] Unexpected service error → 500 `internal_error`
```

#### Structurally unreachable paths (no test stub needed)

- `enqueueEmail`: mail queue failure path is non-fatal (best-effort); handler returns 202 regardless — not testable without a real queue instance in unit tests. Covered by integration/e2e tests only.

---

## Sync checklist before closing Stage 5

- [ ] All Part 1 Critical and High findings resolved
- [ ] All Part 2 guard-ordering deviations corrected
- [ ] All Part 3 platform violations corrected
- [ ] All Part 4 `[❌]` missing handler tests added to `handler_test.go`
- [ ] `go build ./internal/platform/mailer/templates/...` passes
- [ ] `go build ./internal/domain/profile/delete-account/...` passes
- [ ] `go build ./internal/domain/profile/...` passes
- [ ] `go vet ./internal/domain/profile/delete-account/...` passes
- [ ] `go test ./internal/domain/profile/delete-account/... -run TestHandler -v` green

Once all items are checked and unit tests pass, move to Stage 6 (unit tests — manual) then Stage 7 (E2E).
