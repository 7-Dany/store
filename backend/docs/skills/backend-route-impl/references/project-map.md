# Project Map — Fast Lookup Reference

Last updated: 2026-03. Update this file when new packages, errors, or SQL queries are added.

---

## 1. Domain → Package → Files

### `internal/domain/auth/`  (package `auth`)

| Package | Endpoint(s) | Key files |
|---|---|---|
| `auth/login` | POST /auth/login | handler, service, store, models, requests, validators |
| `auth/register` | POST /auth/register | handler, service, store, models, requests, validators |
| `auth/verification` | POST /auth/verify-email, POST /auth/resend-verification | handler, service, store, models, requests, validators |
| `auth/unlock` | POST /auth/request-unlock, POST /auth/confirm-unlock | handler, service, store, models, requests, validators |
| `auth/password` | POST /auth/forgot-password, POST /auth/verify-reset-code, POST /auth/reset-password, POST /auth/change-password | handler, service, store, models, requests, validators, errors |
| `auth/session` | POST /auth/refresh, POST /auth/logout | handler, service, store, models |
| `auth/shared` | — shared primitives | errors, models, otp, password, store, validators |
| `auth/shared/testutil` | — test helpers | fake_storer, fake_servicer, querier_proxy, builders, backoff |

### `internal/domain/profile/`  (package `profile`)

| Package | Endpoint(s) | Key files |
|---|---|---|
| `profile/me` | GET /profile/me, PATCH /profile/me | handler, service, store, models, requests, validators, errors |
| `profile/session` | GET /profile/sessions, DELETE /profile/sessions/{id} | handler, service, store, models, requests |
| `profile/set-password` | POST /profile/set-password | handler, service, store, models, requests, validators, errors |
| `profile/username` | GET /profile/username/available, PATCH /profile/me/username | handler, service, store, models, requests, validators, errors |
| `profile/email` | *(not yet implemented — §B-2)* | — |
| `profile/shared` | — shared primitives | errors (ErrUserNotFound alias), store (BaseStore) |

**Note:** `profile/shared/testutil` does NOT exist. Profile domain uses `auth/shared/testutil` (package `authsharedtest`).

---

## 2. `authshared` — Exported API

**Import path:** `github.com/7-Dany/store/backend/internal/domain/auth/shared`

### Sentinel errors (all in `errors.go`)

```
ErrUserNotFound          — user row not found
ErrTokenNotFound         — one_time_tokens no-rows
ErrTokenExpired          — token.ExpiresAt in past
ErrTokenAlreadyUsed      — consume query affected 0 rows
ErrTooManyAttempts       — attempts >= max_attempts
ErrInvalidCode           — bcrypt mismatch
ErrAccountLocked         — admin_locked / is_locked = TRUE
ErrEmailTaken            — unique violation on users.email
ErrUsernameTaken         — unique violation on users.username
ErrResetTokenCooldown    — partial-unique-index on active reset token
ErrAlreadyVerified       — email_verified already TRUE
ErrInvalidToken          — refresh token unknown/revoked/expired
ErrTokenReuseDetected    — token family reuse detected
ErrSessionNotFound       — session row not found
ErrInvalidCredentials    — email/password mismatch
ErrEmailNotVerified      — login before email confirmed
ErrAccountInactive       — suspended account
ErrLoginLocked           — time-based login lockout (see LoginLockedError typed error)
```

Validation sentinels (also in `errors.go`):
```
ErrDisplayNameEmpty/TooLong/Invalid
ErrEmailEmpty / ErrEmailTooLong / ErrEmailInvalid
ErrIdentifierEmpty / ErrIdentifierTooLong
ErrPasswordEmpty/TooShort/TooLong/NoUpper/NoLower/NoDigit/NoSymbol
ErrCodeEmpty / ErrCodeInvalidFormat
ErrUsernameEmpty/TooShort/TooLong/InvalidChars/InvalidFormat
```

Typed error:
```
LoginLockedError{RetryAfter time.Duration}  — wraps ErrLoginLocked
```

### Models (`models.go`)

```
VerificationToken{ID, UserID, Email, CodeHash, Attempts, MaxAttempts, ExpiresAt}
OTPIssuanceResult{UserID string, Email string, RawCode string}
OTPTokenInput{UserID, Email, IPAddress, UserAgent, CodeHash string, TTL time.Duration}
IncrementInput{TokenID, UserID [16]byte, Attempts, MaxAttempts int16, IPAddress, UserAgent string, AttemptEvent audit.EventType}
NewVerificationToken(...)  — constructor
NewOTPIssuanceResult(...)  — constructor
```

### OTP functions (`otp.go`)

```
GenerateCodeHash() (raw, hash string, err error)      — random 6-digit OTP
VerifyCodeHash(code, stored string) bool              — bcrypt compare
CheckOTPToken(token, code, now) error                 — expiry + attempts + hash
ConsumeOTPToken(ctx, code, consumeFn, onSuccess, incrementFn) error  — orchestration
GetDummyOTPHash() string                              — anti-enumeration placeholder
GetDummyOTPHashCallCount() int64                      — test assertion helper
```

### Password functions (`password.go`)

```
HashPassword(plaintext string) (string, error)
CheckPassword(hash, plaintext string) error           — returns ErrInvalidCredentials on mismatch
GetDummyPasswordHash() string                         — anti-enumeration placeholder
SetBcryptCostForTest(cost int)                        — call from TestMain only
```

### Validators (`validators.go`)

```
ValidatePassword(p string) error                      — strength rules
ValidateEmail(raw string) (normalised string, err error)
```

---

## 3. `authsharedtest` — Test Helpers

**Import path:** `github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil`

```
MustNewTestPool(dsn string, maxConns int) *pgxpool.Pool
MustBeginTx(t, pool) (pgx.Tx, *db.Queries)
RunTestMain(m *testing.M, pool **pgxpool.Pool, maxConns int)
CreateUser(t, q, params) [16]byte                     — inserts verified active user
ErrProxy                                              — sentinel for QuerierProxy injections
QuerierProxy{Base db.Querier, Fail* bool}             — one Fail* per query, grouped by feature
{Feature}FakeStorer  — one per feature, all in fake_storer.go
{Feature}FakeServicer — one per feature, all in fake_servicer.go
```

---

## 4. `app.Deps` — Available Dependencies

```go
Pool           *pgxpool.Pool        — shared DB pool
KVStore        kvstore.Store        — shared KV (rate limiters + blocklist backend)
Blocklist      kvstore.TokenBlocklist — may be nil if in-memory backend; nil-check before use
Mailer         *mailer.SMTPMailer   — synchronous; prefer MailQueue in handlers
MailQueue      *mailer.Queue        — async; handlers call Queue.Enqueue(...)
MailDeliveryTimeout time.Duration
JWTConfig      token.JWTConfig
JWTAuth        func(http.Handler) http.Handler  — JWT middleware; apply as r.With(deps.JWTAuth)
SecureCookies  bool
OTPTokenTTL    time.Duration        — single source for OTP token lifetimes
Encryptor      *crypto.Encryptor    — AES-256-GCM; OAuth tokens only
```

---

## 5. Platform Interfaces — Quick Reference

### `kvstore.Store` (interface, `internal/platform/kvstore/store.go`)
```
Get(ctx, key) (string, error)            — ErrNotFound if absent/expired
Set(ctx, key, value string, ttl) error   — ttl=0 → no expiry; negative → error
Delete(ctx, key) error                   — no-op if absent
Exists(ctx, key) (bool, error)
Keys(ctx, prefix) ([]string, error)
StartCleanup(ctx)                        — call in goroutine
Close() error
```
Optional extensions: `AtomicBucketStore` (Redis atomic bucket), `AtomicBackoffStore` (Redis atomic backoff).

`kvstore.TokenBlocklist` interface:
```
BlockToken(ctx, jti string, ttl) error
IsTokenBlocked(ctx, jti string) (bool, error)
```

### `ratelimit.IPRateLimiter`
```
NewIPRateLimiter(s kvstore.Store, keyPrefix string, rate, burst float64, idleTTL time.Duration) *IPRateLimiter
limiter.Limit  — chi middleware (reads r.RemoteAddr after proxy trust)
limiter.Allow(ctx, ip string) bool
limiter.StartCleanup(ctx)     — goroutine
```

### `ratelimit.UserRateLimiter`
```
NewUserRateLimiter(s kvstore.Store, keyPrefix string, rate, burst float64, idleTTL time.Duration) *UserRateLimiter
limiter.Limit  — chi middleware (reads user ID from JWT context; MUST be after JWTAuth)
limiter.Allow(ctx, userID string) bool
limiter.StartCleanup(ctx)     — goroutine
```

### `mailer.Queue`
```
queue.Enqueue(ctx, fn func(context.Context) error)  — submits async mail task
queue.Shutdown()  — called by server cleanup only; never call from domain code
```

---

## 6. SQL File Map

```
sql/queries/auth.sql            — ALL user-row queries (auth + profile domain combined)
sql/queries_test/auth_test.sql  — test-only helper queries (integration_test build tag)
internal/db/auth.sql.go         — sqlc-generated (read-only; run make sqlc to regenerate)
internal/db/auth_test.sql.go    — sqlc-generated test queries
internal/db/models.go           — sqlc-generated row types
internal/db/querier.go          — db.Querier interface (all methods)
```

Sections in `auth.sql` (in order):
```
Registration → Email verification → Resend verification → Login →
Login lockout & account unlock → Refresh token lifecycle → Sessions →
Mass revocation → Forgot / reset password → Change password →
Profile → Set password → Username
```
New email-change queries go in a new `/* ── Email change ── */` section appended last.

---

## 7. KV Key Prefixes — All In Use

Collision check: new prefixes must not appear in this table.

| Prefix | Feature | Limiter type | Limit |
|---|---|---|---|
| `reg:ip:` | register | IP | 5 / 10 min |
| `vfy:ip:` | verify-email | IP | 5 / 10 min |
| `rsnd:ip:` | resend-verification | IP | 3 / 10 min |
| `lgn:ip:` | login | IP | 5 / 15 min |
| `rfsh:ip:` | refresh | IP | 5 / 15 min |
| `lgout:ip:` | logout | IP | 5 / 1 min |
| `unlk:ip:` | request-unlock + confirm-unlock (shared) | IP | 3 / 10 min |
| `fpw:ip:` | forgot-password | IP | 3 / 10 min |
| `vpc:ip:` | verify-reset-code | IP | 5 / 10 min |
| `rpw:ip:` | reset-password | IP | 5 / 10 min |
| `cpw:ip:` | change-password | IP | 5 / 15 min |
| `pme:ip:` | GET /me | IP | 10 / 1 min |
| `psess:ip:` | GET /sessions | IP | 10 / 1 min |
| `rsess:ip:` | DELETE /sessions/{id} | IP | 3 / 15 min |
| `prof:ip:` | PATCH /me (profile) | IP | 10 / 1 min |
| `spw:usr:` | set-password | User | 5 / 15 min |
| `unav:ip:` | GET /username/available | IP | 20 / 1 min |
| `uchg:usr:` | PATCH /me/username | User | 5 / 10 min |
| `blocklist:jti:` | token blocklist | KV direct | — |
| `echg:usr:` | email request-change *(planned)* | User | 3 / 10 min |
| `echg:usr:vfy:` | email verify-current *(planned)* | User | 5 / 15 min |
| `echg:usr:cnf:` | email confirm-change *(planned)* | User | 5 / 15 min |
| `echg:pending:` | email change KV carry *(planned)* | KV direct | — |
| `echg:gt:` | email grant token *(planned)* | KV direct | — |

---

## 8. `internal/db` Models — Key Types

Generated by sqlc; never edit by hand.

```
db.Querier          — interface; all queries as methods
db.AuthProvider     — typed enum: AuthProviderEmail, AuthProviderGoogle, AuthProviderTelegram
```

---

## 9. `token` Platform — Key Functions

**Import path:** `github.com/7-Dany/store/backend/internal/platform/token`

```
token.UserIDFromContext(ctx) (string, bool)           — extract JWT user ID
token.SessionIDFromContext(ctx) (string, bool)        — extract JWT session ID
token.JWTIDFromContext(ctx) (string, bool)            — extract JWT jti (for blocklisting)
token.GenerateAccessToken(config, claims) (string, error)
token.GenerateRefreshToken(config, claims) (string, error)
```

---

## 10. `respond` Platform — Key Functions

**Import path:** `github.com/7-Dany/store/backend/internal/platform/respond`

```
respond.JSON(w, status, v)                — success JSON response
respond.Error(w, status, code, msg)       — error JSON response; log before calling on 5xx
respond.NoContent(w)                      — 204
respond.DecodeJSON[T](w, r) (T, bool)     — decode body; writes error and returns false on failure
respond.ClientIP(r) string               — trusted real IP (respects TrustedProxyCIDRs)
respond.MaxBodyBytes int64               — 1 MiB; use with http.MaxBytesReader
```
