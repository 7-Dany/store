# Auth Domain Rules

**Reference implementation:** `internal/domain/auth`  
**Last updated:** 2026-03

Read `docs/RULES.md` first. This file documents only what is specific to the
auth domain: its feature set, concrete code flows, security decisions, and
patterns derived from the reference implementation.

---

## Table of Contents

1. [Conflicts and Clarifications](#1-conflicts-and-clarifications)
2. [Auth Domain Structure](#2-auth-domain-structure)
   - 2.1 [Feature Sub-Packages](#21-feature-sub-packages)
   - 2.2 [Shared Package (`authshared`)](#22-shared-package-authshared)
   - 2.3 [Testutil Package (`authsharedtest`)](#23-testutil-package-authsharedtest)
3. [Code Flow Traces](#3-code-flow-traces)
   - 3.1 [Login Flow (POST /login)](#31-login-flow-post-login)
   - 3.2 [OTP Verification Flow (POST /verify-email)](#32-otp-verification-flow-post-verify-email)
   - 3.3 [Token Rotation Flow (POST /refresh)](#33-token-rotation-flow-post-refresh)
4. [JWT Token Flow](#4-jwt-token-flow)
5. [Auth-Specific Conventions](#5-auth-specific-conventions)
   - 5.1 [Global Middleware on the Auth Router](#51-global-middleware-on-the-auth-router)
   - 5.2 [Login Guard Ordering](#52-login-guard-ordering)
   - 5.3 [Anti-Enumeration Timing](#53-anti-enumeration-timing)
   - 5.4 [Session Package: No `requests.go`](#54-session-package-no-requestsgo)
   - 5.5 [`RouteWithIP` Pattern](#55-routewithip-pattern)
   - 5.6 [`backoff.go` and `NopBackoffChecker`](#56-backoffgo-and-nopbackoffchecker)
   - 5.7 [`export_test.go` Pattern](#57-export_testgo-pattern)
   - 5.8 [FakeStorer Non-Nil Default Values](#58-fakestorer-non-nil-default-values)
   - 5.9 [Section Separator Style in Testutil](#59-section-separator-style-in-testutil)
   - 5.10 [Audit Event Casts](#510-audit-event-casts)
6. [Auth-Specific ADRs](#6-auth-specific-adrs)

---

## 1. Conflicts and Clarifications

This section records every place where the reference implementation diverges
from, extends, or clarifies a rule in `docs/RULES.md`. The implementation wins
in all cases — the rules in this file take precedence over any conflicting
statement in RULES.md for the auth domain.

| # | RULES.md says | Auth actually does | Resolution |
|---|---|---|---|
| C-01 | §3.1 "Every domain package contains **exactly** these files" listing `requests.go` and `validators.go` | `session` has neither; `profile` has no `validators.go` | These files are **conditional**: create them only when the feature needs a JSON request body (`requests.go`) or feature-exclusive validators (`validators.go`). The §3.13 checklist already treats `errors.go` and `validators.go` as conditional — apply the same logic universally. |
| C-02 | §3.8 testutil table lists `fake_storer.go`, `querier_proxy.go`, `builders.go` | `authsharedtest` also contains `fake_servicer.go` and `backoff.go` | Both files belong in `authsharedtest`. The rule text says "implementations of Storer **and Servicer** interfaces" — the table was simply incomplete. See §2.3 for the complete file inventory. |
| C-03 | §3.8 "Platform test doubles are **not** placed in `authsharedtest`" | `authsharedtest/backoff.go` implements `verification.BackoffChecker`, which is defined inside the auth domain (not under `internal/platform/`) | `BackoffChecker` is an auth-domain interface. The rule's intent is to keep platform-package fakes (e.g. `mailer.Mailer`, `kvstore.Store`) out of `authsharedtest`. Fakes for interfaces defined inside `auth/` belong in `authsharedtest`. |
| C-04 | §3.8 test file layout lists only `handler_test.go`, `service_test.go`, `store_test.go` | `login`, `register`, and `verification` also have `export_test.go` | `export_test.go` is a valid Go pattern for exposing unexported identifiers to external `_test` packages. See §5.7. |
| C-05 | §1.4 code flow for `Login` does not mention audit writes for guard failures | Service calls `WriteLoginFailedAuditTx` for `is_locked`, `email_not_verified`, and `account_inactive` paths | The full guard sequence in §3.1 of this file is the authoritative reference. |
| C-06 | §4.11 "section separators use `// ──` + title" | `fake_storer.go` and `fake_servicer.go` use title-only separators: `// ─────────────────────────────────────────────────────────────────────────────` followed by a comment line for the struct name | This is an intentional deviation for testutil files only. The full-width rule applies to all other files. See §5.9. |
| C-07 | §2.5 JWT Token Flow is presented as a general concern | JWT handling is exclusively an auth-domain concern in this codebase | §2.5 of RULES.md serves as the general rule; this file (§4) provides the auth-specific details. |

---

## 2. Auth Domain Structure

### 2.1 Feature Sub-Packages

```
internal/domain/auth/
├── routes.go            # package auth — root assembler only; returns *chi.Mux
├── shared/              # package authshared
└── {feature}/           # one sub-package per feature
```

**Currently implemented features:**

| Package | HTTP Endpoints | Notes |
|---|---|---|
| `register` | `POST /register` | Creates user + issues verification OTP |
| `verification` | `POST /verify-email`, `POST /resend-verification` | Consumes OTP; exponential backoff on wrong codes |
| `login` | `POST /login` | bcrypt check; time-based lockout at 10 failures |
| `session` | `POST /refresh`, `POST /logout` | Refresh token rotation; access-token blocklist on logout |
| `unlock` | `POST /request-unlock`, `POST /confirm-unlock` | OTP-gated admin-lock removal |
| `password` | `POST /forgot-password`, `POST /reset-password`, `POST /change-password` | OTP-gated reset; inline change-password from `profile` handler |
| `profile` | `GET /me`, `GET /sessions`, `DELETE /sessions/{id}` | Authenticated reads; `DELETE` revokes a specific session |

All `POST /auth/*` endpoints require `Content-Type: application/json` (enforced
by `chimiddleware.AllowContentType` in the root assembler — see §5.1).

Authenticated endpoints (`profile.*`) are guarded by `deps.JWTAuth` middleware
applied inside each feature's `routes.go`.

---

### 2.2 Shared Package (`authshared`)

`internal/domain/auth/shared/` (package `authshared`) holds everything that
more than one feature sub-package needs.

```
shared/
├── errors.go       # Cross-feature sentinels: ErrUserNotFound, ErrInvalidCredentials,
│                   #   ErrTokenNotFound, ErrTokenExpired, ErrTokenInvalidCode,
│                   #   ErrTokenReuseDetected, ErrInvalidToken, ErrAccountLocked,
│                   #   ErrAccountInactive, ErrEmailNotVerified, ErrLoginLocked,
│                   #   LoginLockedError (typed), IncrementInput (shared store param)
├── models.go       # Shared types: VerificationToken, TokenResult, OTPIssuanceResult
├── otp.go          # ConsumeOTP closure helper; GenerateCodeHash; GetDummyCodeHash
├── password.go     # HashPassword, CheckPassword, GetDummyPasswordHash;
│                   #   SetBcryptCostForTest (controls cost for both password and OTP)
├── store.go        # BaseStore: pool, BeginOrBind, TxBound, Queries,
│                   #   conversion helpers (ToPgtypeUUID, UUIDToBytes, ToText, etc.)
│                   #   LogRollback helper
├── validators.go   # ValidatePassword (shared strength rules)
└── testutil/       # package authsharedtest
```

**Import rule for `authshared`:**

`authshared` may be imported by any auth feature sub-package. It must never
import any feature sub-package. Alias: `authshared "github.com/.../domain/auth/shared"`.

---

### 2.3 Testutil Package (`authsharedtest`)

`internal/domain/auth/shared/testutil/` (package `authsharedtest`) contains
every test-only helper shared across auth features. It must never be imported
by production code.

**Complete file inventory:**

| File | Contents |
|---|---|
| `fake_storer.go` | One `{Feature}FakeStorer` per feature implementing that feature's `Storer`. All in this file. |
| `fake_servicer.go` | One `{Feature}FakeServicer` per feature implementing that feature's `Servicer`. All in this file. |
| `querier_proxy.go` | `QuerierProxy` (single struct covering all features) + `ErrProxy` sentinel. |
| `builders.go` | Pool creation (`MustNewTestPool`), `MustBeginTx`, `CreateUser`, `CreateUserUUID`, `CreateUserDirect`, `RunTestMain`, HTTP request builders, `MustOTPHash`, `MustHashPassword`. |
| `backoff.go` | `NopBackoffChecker` — implements `verification.BackoffChecker` for handler tests that do not exercise backoff. See §5.6. |

**Why `fake_servicer.go` lives here:**  
The `Servicer` interface is analogous to `Storer` — it is defined per-feature
and satisfied by the feature's `*Service`. Handler unit tests need a fake
`Servicer` just as service unit tests need a fake `Storer`. Centralising both
in `authsharedtest` keeps all fakes in one place and prevents scattered
per-feature `testutil/` folders.

---

## 3. Code Flow Traces

### 3.1 Login Flow (POST /login)

This is the most complete example of the auth pattern. Every other feature
follows the same structure with different names.

```
HTTP Client
    │
    │  POST /api/v1/auth/login
    ▼
server/router.go
    │  r.Mount("/api/v1/auth", auth.Routes(...))
    ▼
auth/routes.go  (package auth — root assembler)
    │  r.Use(chimiddleware.AllowContentType("application/json"))
    │  login.Routes(ctx, r, deps)
    ▼
auth/login/routes.go
    │  ipLimiter := ratelimit.NewIPRateLimiter(..., "lgn:ip:", 12.0/(15*60), 12, ...)
    │  r.With(ipLimiter.Limit).Post("/login", h.Login)
    ▼
auth/login/handler.go  h.Login()
    │  1. r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
    │  2. req, ok := respond.DecodeJSON[loginRequest](w, r)
    │  3. validateLoginRequest(&req)  — normalises identifier (trim/lower)
    │  4. svc.Login(ctx, LoginInput{Identifier, Password, IPAddress, UserAgent})
    │  5a. success: token.MintTokens(w, ...) → set cookie + JSON body
    │  5b. error: switch on sentinel → HTTP status code
    ▼
auth/login/service.go  s.Login()
    │  1. store.GetUserForLogin(ctx, identifier)     → LoginUser or ErrUserNotFound
    │  2. authshared.CheckPassword(hash, password)  — ALWAYS runs (timing invariant)
    │  3. ErrUserNotFound → return ErrInvalidCredentials  (anti-enumeration)
    │  4. wrong password  → store.IncrementLoginFailuresTx(WithoutCancel)
    │                        return ErrInvalidCredentials
    │  5. login_locked_until in future → return LoginLockedError{RetryAfter}
    │  6a. is_locked       → store.WriteLoginFailedAuditTx(WithoutCancel, "account_locked")
    │                         return ErrAccountLocked
    │  6b. !email_verified → store.WriteLoginFailedAuditTx(WithoutCancel, "email_not_verified")
    │                         return ErrEmailNotVerified
    │  6c. !is_active      → store.WriteLoginFailedAuditTx(WithoutCancel, "account_inactive")
    │                         return ErrAccountInactive
    │  7. store.LoginTx(ctx, input)              → LoggedInSession
    │  8. store.ResetLoginFailuresTx(WithoutCancel)
    │  return LoggedInSession
    ▼
auth/login/store.go
    │  GetUserForLogin:
    │    q.GetUserForLogin(ctx, ToText(identifier))
    │    → map pgtype.* → LoginUser{ID [16]byte, ...}
    │    → ErrUserNotFound on pgx.ErrNoRows
    │
    │  LoginTx (single transaction):
    │    1. q.CreateUserSession   → sessionRow
    │    2. q.CreateRefreshToken  → tokenRow
    │    3. q.UpdateLastLoginAt
    │    4. q.InsertAuditLog(audit.EventLogin)
    │    → commit → return LoggedInSession{SessionID, RefreshJTI, FamilyID, RefreshExpiry}
    │
    │  IncrementLoginFailuresTx (bypasses BeginOrBind — uses s.Pool.Begin directly):
    │    1. q.IncrementLoginFailures  → row with LoginLockedUntil if threshold reached
    │    2. q.InsertAuditLog(audit.EventLoginFailed, reason=wrong_password)
    │    3. if locked: q.InsertAuditLog(audit.EventLoginLockout)
    │
    │  WriteLoginFailedAuditTx (uses BeginOrBind):
    │    q.InsertAuditLog(audit.EventLoginFailed, metadata={"reason": reason})
    ▼
internal/db/  (sqlc-generated)
    ▼
PostgreSQL
```

**Handler error mapping for login:**

| Sentinel | HTTP Status | Code string |
|---|---|---|
| `ErrInvalidCredentials` | 401 | `invalid_credentials` |
| `ErrEmailNotVerified` | 403 | `email_not_verified` |
| `ErrAccountInactive` | 403 | `account_inactive` |
| `ErrAccountLocked` | 423 | `account_locked` |
| `ErrLoginLocked` / `*LoginLockedError` | 429 + `Retry-After` header | `login_locked` |
| anything else | 500 (logged) | `internal_error` |

---

### 3.2 OTP Verification Flow (POST /verify-email)

OTP flows (verify-email, resend-verification, forgot-password, reset-password,
request-unlock, confirm-unlock) all follow the same pattern.

```
handler.go  h.VerifyEmail()
    │  DecodeJSON → VerifyEmailInput{Email, Code}
    │  svc.VerifyEmail(ctx, in)
    ▼
service.go  s.VerifyEmail()
    │  1. store.VerifyEmailTx(ctx, email, ip, ua, checkFn)
    │     checkFn is a closure that calls authshared.ConsumeOTP(token, code)
    │  2. on authshared.ErrInvalidCode:
    │       store.IncrementAttemptsTx(context.WithoutCancel(ctx), IncrementInput{...})
    │       return ErrTokenInvalidCode
    ▼
store.go  s.VerifyEmailTx(ctx, email, ip, ua, checkFn)
    │  h, _ := s.BeginOrBind(ctx)
    │  1. q.GetVerificationTokenForUpdate(FOR UPDATE)  → VerificationToken
    │  2. checkFn(token)  — runs ConsumeOTP inside the lock
    │  3. if nil: q.DeleteVerificationToken, q.SetEmailVerified, q.InsertAuditLog
    │  4. commit
    │  (on checkFn error: rollback, return checkFn error)
```

**Why `checkFn` is a closure (ADR-005):**  
`IncrementAttemptsTx` opens a fresh pool transaction and issues an UPDATE on
the same row that `VerifyEmailTx` holds with `FOR UPDATE`. Calling it from
inside `checkFn` (while the row is still locked) would deadlock. The `checkFn`
closure runs inside the lock; `IncrementAttemptsTx` runs after the lock is
released.

---

### 3.3 Token Rotation Flow (POST /refresh)

```
handler.go  h.Refresh()
    │  1. r.Cookie("refresh_token")
    │  2. parseRefreshToken(cookie.Value) → refreshClaims{JTI, UserID, SessionID, FamilyID}
    │  3. svc.RotateRefreshToken(ctx, claims.JTI, ip, ua)
    │  4. token.MintTokens(w, {UserID, SessionID, NewRefreshJTI, FamilyID, ...})
    ▼
service.go  s.RotateRefreshToken()
    │  1. store.GetRefreshTokenByJTI(jti) → StoredRefreshToken
    │  2. if revoked: store.RevokeFamilyTokensTx(WithoutCancel, userID, familyID, "reuse")
    │                  return ErrTokenReuseDetected
    │  3. store.GetUserVerifiedAndLocked(userID)
    │  4. guards: !email_verified, is_locked, !is_active
    │  5. store.RotateRefreshTokenTx(ctx, RotateTxInput{...}) → RotatedSession
    ▼
store.go  s.RotateRefreshTokenTx()
    │  single transaction:
    │    1. q.RevokeRefreshToken (old JTI)
    │    2. q.CreateRefreshToken (new token, same family_id)
    │    3. q.InsertAuditLog(audit.EventRefresh)
    │    → commit → return RotatedSession{NewRefreshJTI, RefreshExpiry}
```

---

## 4. JWT Token Flow

JWT signing and parsing are exclusively handler-layer concerns.

**Login and Refresh:**
1. Service returns raw metadata: `SessionID [16]byte`, `RefreshJTI [16]byte`, `FamilyID [16]byte`, `RefreshExpiry time.Time`. No tokens, no secrets.
2. Handler calls `token.MintTokens(w, input, cfg)` which internally calls `token.GenerateAccessToken` and `token.GenerateRefreshToken`.
3. `MintTokens` sets the refresh token as an `HttpOnly` cookie and returns a `tokenResponse` with the access token for the JSON body.

**Refresh cookie attributes (set by `platform/token`):**

| Attribute | Value |
|---|---|
| `HttpOnly` | `true` |
| `SameSite` | `http.SameSiteStrictMode` |
| `Secure` | `h.SecureCookies` (from `deps.JWTConfig`) |
| `Path` | `"/api/v1/auth"` |
| `MaxAge` | Derived from DB row `expires_at` |

Never hardcode a cookie MaxAge duration. The value must come from the token
row's actual expiry.

**Logout blocklist:**
1. Handler parses the `Authorization: Bearer <token>` header (best-effort; logout
   always succeeds even if absent).
2. On valid access token: `h.blocklist.BlockToken(context.WithoutCancel(ctx), jti, ttl)`.
3. All subsequent requests with that JTI are rejected by `deps.JWTAuth` middleware.

**Identity extraction on authenticated routes:**  
Always use `token.UserIDFromContext(r.Context())`. Never read `Authorization` header directly.

**Handler-local claim types:**  
`refreshClaims` and `accessClaims` are unexported structs defined at the top of
`session/handler.go`. They hold all UUID fields as `[16]byte` so no string
parsing happens in the route logic.

---

## 5. Auth-Specific Conventions

### 5.1 Global Middleware on the Auth Router

The auth root assembler (`auth/routes.go`) applies one global middleware before
mounting any feature:

```go
r.Use(chimiddleware.AllowContentType("application/json"))
```

This rejects any request without `Content-Type: application/json` with a 415
before it reaches any handler. This is auth-specific because every auth
endpoint consumes JSON.

When building a new domain router, decide whether the same middleware is
appropriate. Do not copy it blindly — a domain that serves mixed content types
must not apply `AllowContentType` globally.

---

### 5.2 Login Guard Ordering

After the password check passes, guards run in this exact order:

1. `login_locked_until in future` → `LoginLockedError{RetryAfter}` (HTTP 429)
2. `is_locked` → `ErrAccountLocked` (HTTP 423) + audit write
3. `!email_verified` → `ErrEmailNotVerified` (HTTP 403) + audit write
4. `!is_active` → `ErrAccountInactive` (HTTP 403) + audit write

**Why time-based lockout (step 1) runs after the password check (not before):**

This is Option A. A user whose account is time-locked can confirm their
password is correct by observing a 429 (LoginLockedError) rather than a 401
(ErrInvalidCredentials). This leak is intentional. The lockout window itself
limits exploitation, and the simpler guard order is easier to audit. The
service doc comment documents this trade-off explicitly so future contributors
cannot accidentally "fix" it.

**Every guard failure writes a `login_failed` audit row:**

The audit row's `metadata` JSON contains the `reason` field
(`"account_locked"`, `"email_not_verified"`, `"account_inactive"`) so analysts
can distinguish guard-failure types from wrong-password failures without
scanning application logs.

---

### 5.3 Anti-Enumeration Timing

Any endpoint that looks up a user by email and may reveal whether the email
exists must equalise response latency between "found" and "not found" paths.
Two techniques are always used together:

**1. Dummy hash compare on no-rows:**  
When `GetUserForLogin` returns `ErrUserNotFound`, `CheckPassword` is called
against `authshared.GetDummyPasswordHash()`. The result is discarded. This
equalises bcrypt latency between "user not found" and "wrong password".

The same technique applies to OTP endpoints: when the token lookup returns
`ErrTokenNotFound`, `authshared.VerifyCodeHash(code, GetDummyCodeHash())` is
called and the result discarded.

**2. Uniform response on ambiguous outcomes:**  
Resend-verification, forgot-password, and request-unlock endpoints always
return `202 Accepted` with the same body regardless of whether the email
exists, whether the account is already in the correct state, etc. The service
returns `nil, zero-result` — never a sentinel — for these suppressed paths.
The handler always writes the same response.

**Annotation convention:**  
Inline comment at every dummy-hash call site:

```go
// Timing invariant: always run CheckPassword, even on no-rows, to prevent
// timing-based email enumeration (§3.7).
```

Service method doc comment:

```go
// Timing invariant: CheckPassword always runs, even if the user is not found.
```

---

### 5.4 Session Package: No `requests.go`

The `session` package (`POST /refresh`, `POST /logout`) has no `requests.go`
and no `validators.go`. This is intentional:

- `POST /refresh` reads the refresh token from an HttpOnly cookie. There is no
  JSON request body.
- `POST /logout` reads the refresh token from an HttpOnly cookie and the access
  token from the `Authorization` header (best-effort). No JSON body.

Since neither endpoint reads a JSON body, there is nothing to validate and no
request struct to define. `requests.go` and `validators.go` are not created.

**General rule (supersedes RULES.md §3.1 for conditional files):**

| File | Create when |
|---|---|
| `requests.go` | The feature has at least one endpoint that reads a JSON request body |
| `validators.go` | The feature has feature-exclusive validation functions |
| `errors.go` | The feature has feature-exclusive sentinel errors |

All three are omitted when the feature doesn't need them.

---

### 5.5 `RouteWithIP` Pattern

The `session` package uses a different route registration helper:

```go
// Standard pattern (used by most features):
r.With(limiter.Limit).Post("/login", h.Login)

// session uses RouteWithIP:
ratelimit.RouteWithIP(r, http.MethodPost, "/refresh", h.Refresh, refreshLimiter)
```

`ratelimit.RouteWithIP` exists because the `session` handler needs to call
`respond.ClientIP(r)` before any service call. It wires the IP into the request
context so the handler can read it without inspecting `r.RemoteAddr` directly.

Use `RouteWithIP` whenever the handler needs the client IP from context (not
just from `respond.ClientIP(r)` in the handler body). Prefer the standard
`r.With(limiter.Limit).Post(...)` pattern everywhere else.

---

### 5.6 `backoff.go` and `NopBackoffChecker`

`internal/domain/auth/shared/testutil/backoff.go` defines:

```go
// NopBackoffChecker is a verification.BackoffChecker that always allows
// requests and records no state. Used by handler tests that do not exercise
// backoff behaviour.
type NopBackoffChecker struct{}

var _ verification.BackoffChecker = (*NopBackoffChecker)(nil)
```

This fake is in `authsharedtest` because `verification.BackoffChecker` is an
interface defined inside the auth domain. It is not a platform package interface.

**Why a separate file instead of inline in handler_test.go:**  
`NopBackoffChecker` is used by multiple handler test files across the
verification package. Moving it to a shared location prevents duplication.

**When to add more fakes to backoff.go:**  
If the verification package grows to need a `RecordingBackoffChecker` (one that
records calls for assertion), add it to `backoff.go`. Do not create a new file
in `authsharedtest` for each individual fake — group related fakes in one
logical file.

---

### 5.7 `export_test.go` Pattern

Some feature packages (`login`, `register`, `verification`) have an
`export_test.go` file in the production package (not the `_test` package). This
file exposes unexported identifiers to external test packages.

```go
// register/export_test.go — package register (not register_test)
package register

func (s *Service) SetGenerateCodeHashForTest(fn func() (string, string, error)) {
    s.generateCodeHash = fn
}

func ExportedValidateAndNormalise(req *registerRequest) error {
    return validateAndNormalise(req)
}

func ExportedRegisterRequest(...) registerRequest { ... }
```

**Rules for `export_test.go`:**

- Only ever needed when `{feature}_test.go` tests live in package
  `{feature}_test` (external package) but need access to unexported types or
  functions.
- Contains only shim functions and type aliases — never real logic.
- Never imported by production code. The Go toolchain excludes it from
  non-test builds automatically.
- Create it when a feature's handler or validator tests need to construct
  unexported request structs or call unexported validation functions directly.
- Omit it if all tests live in the same package (e.g. `package register` with
  `package register_test` in separate files but sharing unexported access via
  same package declaration).

---

### 5.8 FakeStorer Non-Nil Default Values

Most `{Feature}FakeStorer` methods return `(zero, nil)` when the `Fn` field is
not set. Two methods deliberately return a non-nil default error:

| Method | Default return | Why |
|---|---|---|
| `PasswordFakeStorer.GetPasswordResetTokenForVerify` | `(zero, ErrTokenNotFound)` | The service expects no-token to be the starting state; a nil error would signal an unexpected found-token |
| `PasswordFakeStorer.GetPasswordResetTokenCreatedAt` | `(zero, ErrTokenNotFound)` | Same rationale |
| `UnlockFakeStorer.GetUnlockToken` | `(zero, ErrTokenNotFound)` | Service reads this to guard against issuing a second token |
| `VerificationFakeStorer` (token lookups) | Default is nil — these are consumed via closure | N/A |

When adding a new `FakeStorer` method, choose the default that makes tests that
*don't configure the field* produce the expected behaviour. If no-rows is the
"happy path starting state", return `ErrTokenNotFound`. Otherwise return `nil`.

**`PasswordFakeStorer.IncrementChangePasswordFailuresTx` default:**  
Returns `(0, nil)` so the failure counter never reaches `changePasswordMaxAttempts`
(currently 5) in tests that don't explicitly configure it.

---

### 5.9 Section Separator Style in Testutil

`fake_storer.go` and `fake_servicer.go` use a modified separator style that
deviates from RULES.md §4.11. Each feature's block is separated by a
full-width title bar followed by a blank comment line:

```go
// ─────────────────────────────────────────────────────────────────────────────
// LoginFakeStorer
// ─────────────────────────────────────────────────────────────────────────────
```

This style is used **only in testutil files** where the repeating structure
(one block per feature) benefits from a prominent visual boundary. All other
files use the documented `// ── Title ──────────────` style from RULES.md §4.11.

Do not introduce the full-width style outside of `fake_storer.go`,
`fake_servicer.go`, and `querier_proxy.go`.

---

### 5.10 Audit Event Casts

`db.InsertAuditLogParams.EventType` is a `string` field (not `audit.EventType`).
Store files cast the typed constant:

```go
EventType: string(audit.EventLogin),
```

This cast is correct and required. The typed `audit.EventType` prevents
arbitrary strings from being passed to any function that accepts `EventType`,
but `db.InsertAuditLogParams` is sqlc-generated and uses `string`. The cast at
this single call site is the correct adaptation layer. Do not skip the cast by
using a string literal directly.

---

## 6. Auth-Specific ADRs

These ADRs supplement the general ADRs in RULES.md §5. Read the general ADRs
first — they provide the foundational reasoning that the auth-specific decisions
build on.

---

### ADR-001 — JWT signing belongs in the handler, not the service

**Context:** The service performs login and token rotation. It calls `LoginTx`
which returns session metadata. Somewhere between that DB write and the HTTP
response, a signed JWT must be produced.

**Decision:** The handler is responsible for JWT signing. The service returns
raw `[16]byte` and `time.Time` values. The handler calls
`token.MintTokens(w, input, cfg)`.

**Why not in the service:**
- A service that holds JWT secrets cannot be tested without real secrets.
- JWT format is a presentation-layer decision. Changing to PASETO should not
  touch business logic.
- Services must be importable by future background jobs or CLIs without pulling
  in the JWT library.

**Consequence:** A signing failure after a committed `LoginTx` leaves an
orphaned session row. This is logged at ERROR. It is acceptable because
HMAC-SHA256 signing cannot fail unless the secret is empty, which is caught at
startup by `NewHandler` panicking on a short secret.

---

### ADR-003 — The `txBound` / `WithQuerier` pattern for test isolation

**Context:** Store integration tests need to scope all DB writes to a single
transaction that rolls back at test cleanup. But `*Tx` methods open their own
transactions internally.

**Decision:** `BaseStore` has a `txBound bool` flag. When true, `BeginOrBind`
returns the injected `db.Querier` with no-op commit and rollback. When false,
it opens a real transaction from the pool.

**Exception:** `IncrementLoginFailuresTx` always opens a fresh pool transaction,
ignoring `txBound`. These must commit independently even if the caller's
transaction rolls back.

**Consequence:** Tests require `MaxConns >= 20`. `VerifyEmailTx` holds a
`FOR UPDATE` lock while `checkFn` runs. `IncrementAttemptsTx` needs a
separate pool connection to UPDATE the same row after the lock is released.
With the default pgxpool maximum of 4, this deadlocks in tests.

---

### ADR-005 — OTP consumption uses a `checkFn` closure to avoid deadlocks

**Context:** The OTP verification flow needs to: (1) lock the token row,
(2) check the code, (3) consume the token, and (4) on wrong code, increment the
attempt counter. Steps 1-3 must be atomic. Step 4 must happen after the lock is
released.

**Decision:** `VerifyEmailTx` (and all equivalent token consumption methods)
accept a `checkFn func(VerificationToken) error` closure. The store opens a
transaction, locks the row, calls `checkFn`, and if it returns nil proceeds to
consume the token. The service calls `IncrementAttemptsTx` after the `*Tx`
method returns.

**Why not call `IncrementAttemptsTx` inside `checkFn`:** It opens an
independent transaction and issues an UPDATE on the same row currently held
with `FOR UPDATE`. This creates a PostgreSQL row-level lock deadlock.

**Consequence:** There is a narrow window where a client sees `ErrInvalidCode`
but the counter has not yet incremented (process crash between the two
transactions). An attacker gets at most one extra free attempt during a crash,
which is not exploitable in practice.

---

### ADR-006 — Anti-enumeration: uniform 202 + timing equalization

**Context:** Endpoints that accept an email address must not reveal whether the
email exists in the system.

**Decision:** Two techniques are always used together: (1) resend/unlock/
forgot-password endpoints always return `202 Accepted` with the same body
regardless of whether the email exists; (2) endpoints that compare a code or
password run the comparison even on the no-rows path, against a precomputed
dummy hash.

**Why timing matters:** If the no-rows path returns in 1ms and the wrong-code
path returns in 300ms, an attacker can determine which emails are registered
by measuring response times.

**Consequence:** Legitimate users cannot distinguish "email not found" from
"email found but code was wrong" on resend and unlock endpoints. This is
acceptable — it prevents enumeration without affecting flow completion.

---

### ADR-009 — Shared KV store instance for all rate limiters in a domain

**Context:** Each domain routes file creates multiple rate limiters. Each could
have its own Redis connection pool.

**Decision:** One `kvstore.Store` instance (from `deps.KVStore`) is shared
across all rate limiters and the token blocklist in the auth domain.

**Why:** One Redis pool per limiter with 6 limiters plus a blocklist would open
7 pools. Limiters use distinct key prefixes (`lgn:ip:`, `rfsh:ip:`, `lgout:ip:`,
`blocklist:`) to avoid key collisions.

**Rate limiter key prefixes in use:**

| Prefix | Limiter | Feature |
|---|---|---|
| `lgn:ip:` | IP rate limiter | login |
| `rfsh:ip:` | IP rate limiter | session/refresh |
| `lgout:ip:` | IP rate limiter | session/logout |
| `rgstr:ip:` | IP rate limiter | register |
| `vrf:ip:` | IP rate limiter | verification |
| `vrf:uid:` | User rate limiter | verification |
| `vrf:backoff:` | Backoff limiter | verification |
| `unlock:ip:` | IP rate limiter | unlock |
| `unlock:uid:` | User rate limiter | unlock |
| `pwd:ip:` | IP rate limiter | password |
| `blocklist:` | Token blocklist | session/logout |

**Consequence:** A transient Redis error affects all rate limiters
simultaneously. Each falls back to local in-memory state — single-instance rate
limiting rather than failing open.

---

### ADR-011 — Token family revocation on reuse detection

**Context:** Refresh tokens rotate on every use. If a client presents a token
that has already been rotated, it may indicate replay or theft.

**Decision:** When a revoked refresh token is presented to `/auth/refresh`, the
entire token family (all tokens sharing `family_id`) is revoked atomically.
The user is forced to re-authenticate.

**Why revoke the whole family:** Revoking only generation N accomplishes nothing
if the attacker has already obtained generation N+1.

**Consequence:** Legitimate clients that retry a rotation after a network
timeout will find their family revoked. They must re-login. This is acceptable
— the alternative (allowing retry within a window) requires tracking rotation
timestamps and introduces race conditions.

---

### ADR-A01 — `AllowContentType` applied at the domain router level

**Context:** Every auth endpoint consumes `application/json`. The check could
go in each handler or once at the router level.

**Decision:** Applied once in `auth/routes.go` via
`r.Use(chimiddleware.AllowContentType("application/json"))`.

**Why:** Handler code should assume the content type is correct. Centralising
the check eliminates a missing check on any newly added endpoint.

**Consequence:** Any future auth endpoint that does NOT consume JSON (e.g. an
OAuth callback that receives a form-encoded payload) must explicitly override or
skip this middleware at the feature router level.

---

### ADR-A02 — Login failure audit writes even for non-credential guard failures

**Context:** The original design audit-logs only wrong-password failures. Guard
failures (locked, inactive, unverified) could silently succeed from an audit
perspective.

**Decision:** Every guard failure in `login.Login` calls
`WriteLoginFailedAuditTx(context.WithoutCancel(ctx), userID, reason, ip, ua)`
with a `reason` string identifying the guard that fired.

**Why:** Credential-stuffing tools frequently probe whether accounts are locked
or unverified. Logging these paths makes such probes visible in the audit trail.

**Consequence:** A locked or inactive account generates one `login_failed` row
per authentication attempt. Analysts must filter by `reason` to distinguish
wrong-password from guard-failure events.
