# Project Map — Fast Lookup Reference

Last updated: 2026-03-16. Update this file when new packages, errors, or SQL queries are added.

---

## 1. Domain → Package → Files

### `internal/domain/auth/`  (package `auth`)

| Package | Endpoint(s) | Key files |
|---|---|---|
| `auth/login` | POST /auth/login | handler, service, store, models, requests, validators |
| `auth/register` | POST /auth/register | handler, service, store, models, requests, validators |
| `auth/verification` | POST /auth/verification, POST /auth/verification/resend | handler, service, store, models, requests, validators |
| `auth/unlock` | POST /auth/unlock, PUT /auth/unlock | handler, service, store, models, requests, validators |
| `auth/password` | POST /auth/password/reset, POST /auth/password/reset/verify, PUT /auth/password/reset, PATCH /auth/password | handler, service, store, models, requests, validators, errors |
| `auth/session` | POST /auth/refresh, POST /auth/logout | handler, service, store, models |
| `auth/shared` | — shared primitives | errors, models, otp, password, store, validators |
| `auth/shared/testutil` | — test helpers | fake_storer, fake_servicer, querier_proxy, builders, backoff |

### `internal/domain/profile/`  (package `profile`)

| Package | Endpoint(s) | Key files |
|---|---|---|
| `profile/me` | GET /profile/me, PATCH /profile/me, GET /profile/me/identities | handler, service, store, models, requests, validators, errors |
| `profile/session` | GET /profile/me/sessions, DELETE /profile/me/sessions/{id} | handler, service, store, models, requests |
| `profile/set-password` | POST /profile/me/password | handler, service, store, models, requests, validators, errors |
| `profile/username` | GET /profile/me/username/available, PATCH /profile/me/username | handler, service, store, models, requests, validators, errors |
| `profile/email` | POST /profile/me/email, POST /profile/me/email/verify, PUT /profile/me/email | handler, service, store, models, requests, routes, validators, validators_test |
| `profile/delete-account` | DELETE /profile/me, DELETE /profile/me/deletion, GET /profile/me/deletion | handler, service, store, models, requests, routes, validators, errors |
| `profile/shared` | — shared primitives | errors (ErrUserNotFound alias), store (BaseStore) |

**Note:** `profile/shared/testutil` does NOT exist. Profile domain uses `auth/shared/testutil` (package `authsharedtest`).

---

### `internal/domain/oauth/`  (package `oauth`)

| Package | Endpoint(s) | Key files |
|---|---|---|
| `oauth/google` | GET /oauth/google, GET /oauth/google/callback, DELETE /oauth/google | handler, service, store, models, routes, errors, provider |
| `oauth/telegram` | POST /oauth/telegram/callback, PUT /oauth/telegram, DELETE /oauth/telegram | handler, service, store, models, requests, routes, validators, validators_test, errors |
| `oauth/shared` | — shared primitives | models (LoggedInSession, LinkedIdentity), errors, store |
| `oauth/shared/testutil` | — test helpers | builders, fake_storer, fake_servicer, querier_proxy |

---

### `internal/domain/rbac/`  (package `rbac`)

| Package | Endpoint(s) | Key files |
|---|---|---|
| `rbac/owner` | PUT /rbac/owner/assign, POST/PUT/DELETE /rbac/owner/transfer | handler, service, store, models, requests, routes, validators, validators_test, errors, export_test |
| `rbac/permissions` | GET /rbac/permissions, GET /rbac/permissions/groups | handler, service, store, models, requests, routes |
| `rbac/roles` | GET/POST /rbac/roles, GET/PATCH/DELETE /rbac/roles/{id}, GET/POST /rbac/roles/{id}/permissions, DELETE /rbac/roles/{id}/permissions/{perm_id} | handler, service, store, models, requests, routes, validators, validators_test, errors, export_test |
| `rbac/shared` | — shared primitives | errors (ErrUserNotFound), store, validators |
| `rbac/shared/testutil` | — test helpers | builders, fake_storer, fake_servicer, querier_proxy |

---

### `internal/domain/admin/`  (package `admin`)

| Package | Endpoint(s) | Key files |
|---|---|---|
| `admin/userroles` | GET /admin/users/{user_id}/role, PUT/DELETE /admin/users/{user_id}/role | handler, service, store, models, requests, routes, validators, validators_test, errors, export_test |
| `admin/userpermissions` | GET /admin/users/{user_id}/permissions, POST /admin/users/{user_id}/permissions, DELETE /admin/users/{user_id}/permissions/{grant_id} | handler, service, store, models, requests, routes, validators, validators_test, errors, export_test |
| `admin/userlock` | POST /admin/users/{user_id}/lock, DELETE /admin/users/{user_id}/lock, GET /admin/users/{user_id}/lock | handler, service, store, models, requests, routes, validators, validators_test, errors, export_test |
| `admin/shared` | — shared primitives | errors, handler (shared base handler) |
| `admin/shared/testutil` | — test helpers | builders, fake_storer, fake_servicer, querier_proxy |

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
RBAC           *rbac.Checker        — DB-backed permission checker; use Require(perm) as middleware
BootstrapSecret string              — value of BOOTSTRAP_SECRET env var
ApprovalSubmitter rbac.ApprovalSubmitter      — nil until requests domain wired (Phase 10)
ConditionalEscalator rbac.ConditionalEscalator — nil until requests domain wired (Phase 10)
OAuth          app.OAuthConfig      — {GoogleClientID, GoogleClientSecret, GoogleRedirectURI, SuccessURL, ErrorURL, TelegramBotToken}
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
sql/queries/oauth.sql           — OAuth identity queries (google, telegram)
sql/queries/rbac.sql            — RBAC queries (bootstrap, roles, permissions, userroles)
sql/queries_test/auth_test.sql  — test-only helper queries (integration_test build tag)
sql/queries_test/oauth_test.sql — test-only helper queries for oauth
sql/queries_test/rbac_test.sql  — test-only helper queries for rbac
internal/db/auth.sql.go         — sqlc-generated (read-only; run make sqlc to regenerate)
internal/db/oauth.sql.go        — sqlc-generated OAuth queries
internal/db/rbac.sql.go         — sqlc-generated RBAC queries
internal/db/auth_test.sql.go    — sqlc-generated test queries
internal/db/oauth_test.sql.go   — sqlc-generated OAuth test queries
internal/db/rbac_test.sql.go    — sqlc-generated RBAC test queries
internal/db/models.go           — sqlc-generated row types
internal/db/querier.go          — db.Querier interface (all methods)
```

Sections in `auth.sql` (in order):
```
Registration → Email verification → Resend verification → Login →
Login lockout & account unlock → Refresh token lifecycle → Sessions →
Mass revocation → Forgot / reset password → Change password →
Profile → Set password → Username → Email change → Delete account
```

---

## 7. KV Key Prefixes — All In Use

Collision check: new prefixes must not appear in this table.

| Prefix | Feature | Limiter type | Limit |
|---|---|---|---|
| `reg:ip:` | POST /auth/register | IP | 5 / 10 min |
| `vfy:ip:` | POST /auth/verification | IP | 5 / 10 min |
| `rsnd:ip:` | POST /auth/verification/resend | IP | 3 / 10 min |
| `lgn:ip:` | POST /auth/login | IP | 5 / 15 min |
| `rfsh:ip:` | POST /auth/refresh | IP | 5 / 15 min |
| `lgout:ip:` | POST /auth/logout | IP | 5 / 1 min |
| `unlk:ip:` | POST+PUT /auth/unlock (shared) | IP | 3 / 10 min |
| `fpw:ip:` | POST /auth/password/reset | IP | 3 / 10 min |
| `vpc:ip:` | POST /auth/password/reset/verify | IP | 5 / 10 min |
| `rpw:ip:` | PUT /auth/password/reset | IP | 5 / 10 min |
| `cpw:ip:` | PATCH /auth/password | IP | 5 / 15 min |
| `pme:ip:` | GET /profile/me | IP | 10 / 1 min |
| `psess:ip:` | GET /profile/me/sessions | IP | 10 / 1 min |
| `rsess:ip:` | DELETE /profile/me/sessions/{id} | IP | 3 / 15 min |
| `prof:ip:` | PATCH /profile/me | IP | 10 / 1 min |
| `ident:ip:` | GET /profile/me/identities | IP | 20 / 1 min |
| `spw:usr:` | POST /profile/me/password | User | 5 / 15 min |
| `unav:ip:` | GET /profile/me/username/available | IP | 20 / 1 min |
| `uchg:usr:` | PATCH /profile/me/username | User | 5 / 10 min |
| `echg:usr:` | POST /profile/me/email | User | 3 / 10 min |
| `echg:usr:vfy:` | POST /profile/me/email/verify | User | 5 / 15 min |
| `echg:usr:cnf:` | PUT /profile/me/email (confirm step) | User | 5 / 15 min |
| `echg:pending:` | email change pending KV carry | KV direct | — |
| `echg:gt:` | email change grant token | KV direct | — |
| `del:usr:` | DELETE /profile/me | User | 10 / 1 hr |
| `delc:usr:` | DELETE /profile/me/deletion | User | 10 / 10 min |
| `delm:usr:` | GET /profile/me/deletion | User | 10 / 1 min |
| `goauth:init:ip:` | GET /oauth/google | IP | 20 / 5 min |
| `goauth:cb:ip:` | GET /oauth/google/callback | IP | 20 / 5 min |
| `goauth:unl:usr:` | DELETE /oauth/google | User | 5 / 15 min |
| `tgcb:ip:` | POST /oauth/telegram/callback | IP | 10 / 1 min |
| `tglnk:usr:` | PUT /oauth/telegram | User | 5 / 15 min |
| `tgunlk:usr:` | DELETE /oauth/telegram | User | 5 / 15 min |
| `asgn:ip:` | PUT /rbac/owner/assign | IP | 3 / 15 min |
| `xfr:usr:` | POST /rbac/owner/transfer | User | 3 / 24 hr |
| `xfra:ip:` | PUT /rbac/owner/transfer | IP | 10 / 1 hr |
| `xfrc:usr:` | DELETE /rbac/owner/transfer | User | 10 / 1 hr |
| `health:ip:` | GET /health | IP | 3 / 1 min |
| `blocklist:jti:` | token blocklist (all domains) | KV direct | — |

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

---

## 11. `platform/rbac` — Key API

**Import path:** `github.com/7-Dany/store/backend/internal/platform/rbac`

```
NewChecker(q db.Querier) *Checker
checker.IsOwner(ctx, userID) (bool, error)
checker.HasPermission(ctx, userID, permission) (bool, error)
checker.Require(permission) func(http.Handler) http.Handler  — chi middleware; 401 if no JWT, 403 if no perm
checker.ApprovalGate(submitter) func(http.Handler) http.Handler  — intercepts access_type="request"; returns 202
```

Permission constants (use these, never raw strings):
```
PermRBACRead           = "rbac:read"
PermRBACManage         = "rbac:manage"
PermRBACGrantUserPerm  = "rbac:grant_user_permission"
PermJobQueueRead       = "job_queue:read"
PermJobQueueManage     = "job_queue:manage"
PermJobQueueConfigure  = "job_queue:configure"
PermUserRead           = "user:read"
PermUserManage         = "user:manage"
PermUserLock           = "user:lock"
PermRequestRead        = "request:read"
PermRequestManage      = "request:manage"
PermRequestApprove     = "request:approve"
PermProductManage      = "product:manage"
```

---

## 12. `oauthshared` — Exported API

**Import path:** `github.com/7-Dany/store/backend/internal/domain/oauth/shared`

```
LoggedInSession{UserID, SessionID, RefreshJTI, FamilyID [16]byte; RefreshExpiry time.Time}
LinkedIdentity{Provider, DisplayName, AvatarURL string}
```

---

## 13. `rbacshared` — Exported API

**Import path:** `github.com/7-Dany/store/backend/internal/domain/rbac/shared`

```
ErrUserNotFound  — user row not found
```

---

## 14. `adminsharedtest` — Test Helpers

**Import path:** `github.com/7-Dany/store/backend/internal/domain/admin/shared/testutil`

```
ErrProxy                                              — sentinel for QuerierProxy injections
QuerierProxy{Base db.Querier, Fail* bool}             — one Fail* per query, grouped by feature
{Feature}FakeStorer  — one per feature, all in fake_storer.go
{Feature}FakeServicer — one per feature, all in fake_servicer.go
```

**Note:** admin domain has its own testutil — do NOT import `authsharedtest` or `rbacsharedtest` for admin tests.
