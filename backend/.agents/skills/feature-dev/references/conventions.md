# Conventions — File Layout, Naming, SQL, HTTP, Testing

---

## File layout

### Required files (every feature sub-package)

| File | Purpose |
|---|---|
| `handler.go` | HTTP handlers + `Servicer` interface |
| `service.go` | Business logic + `Storer` interface |
| `store.go` | Database access + `Store` struct |
| `routes.go` | Dependency wiring + route registration. No logic. |
| `models.go` | Service-layer I/O types. No `json:` tags. No pgtype. |

### Conditional files (create only when needed)

| File | Create when |
|---|---|
| `requests.go` | Feature has at least one endpoint that reads a JSON body |
| `errors.go` | Feature has feature-exclusive sentinel errors |
| `validators.go` | Feature has feature-exclusive validation functions |

**Banned file names:** `helpers.go`, `utils.go`, `common.go`.

---

## Naming conventions

| Thing | Pattern | Example |
|---|---|---|
| Service input struct | `{Operation}Input` | `RegisterInput` |
| Service result struct | `{Operation}Result` | `RegisterResult` |
| Store mutating method | `{Action}Tx` | `CreateUserTx` |
| Store read method | `Get{Thing}` or `List{Things}` | `GetUserForLogin` |
| Sentinel error | `Err{Condition}` | `ErrEmailTaken` |
| Typed error | `{Condition}Error` | `LoginLockedError` |
| Audit event constant | `Event{PastTense}` | `EventEmailVerified` |
| Rate limiter var | `{scope}{action}Limiter` | `loginLimiter` |
| Feature `Storer` interface | `Storer` | same in every feature |
| Feature `Servicer` interface | `Servicer` | same in every feature |
| Auth testutil fake struct | `{Feature}FakeStorer` | `LoginFakeStorer` |
| Auth testutil proxy | `QuerierProxy` | single struct for all features |
| Auth testutil proxy sentinel | `ErrProxy` | defined once |
| RBAC permission constant | `Perm{Resource}{Action}` | `PermRBACRead`, `PermUserLock` |

---

## Layer type rules

### Store method shapes

Every feature `store.go` must have exactly this structure — no deviations:

```go
var _ Storer = (*Store)(nil)

type Store struct {
    {domain}shared.BaseStore  // authshared, oauthshared, or rbacshared depending on domain
}

func NewStore(pool *pgxpool.Pool) *Store {
    return &Store{BaseStore: {domain}shared.NewBaseStore(pool)}
}

func (s *Store) WithQuerier(q db.Querier) *Store {
    c := *s
    c.BaseStore = s.BaseStore.WithQuerier(q)
    return &c
}
```

Missing `WithQuerier` = cannot be used in integration tests.

### Error handling

**Sentinel errors** — `var` declarations in `errors.go`:
```go
var ErrEmailTaken = errors.New("email already registered")
```

**Typed errors** — struct implementing `error` and `Unwrap`:
```go
type LoginLockedError struct{ RetryAfter time.Duration }
func (e *LoginLockedError) Error() string { return ErrLoginLocked.Error() }
func (e *LoginLockedError) Unwrap() error { return ErrLoginLocked }
```

**DB error mapping** (in store):
```go
if isNoRows(err) { return LoginUser{}, ErrUserNotFound }
if isDuplicateEmail(err) { return CreatedUser{}, ErrEmailTaken }
```

**Handler error mapping** (in handler):
```go
// var log = telemetry.New("{feature}") at package level — not inside the function.
switch {
case errors.Is(err, ErrInvalidCredentials):
    respond.Error(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
default:
    log.Error(r.Context(), "{domain}.{Method}: service error", "error", err)
    respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
}
```

Never use `slog.ErrorContext` directly in domain handlers. `log.Error` routes
through `TelemetryHandler` which auto-increments `app_errors_total` and writes
the fault into the request carrier for `http_errors_total`.

**Error wrapping — use the layer constructors, never `fmt.Errorf`:**

| Layer | Constructor | Op format | Example |
|---|---|---|---|
| Store query | `telemetry.Store` | `"TypeName.query"` | `"GetUserForLogin.query"` |
| Store TX step | `telemetry.Store` | `"TxName.step"` | `"LoginTx.create_session"` |
| Store TX begin | `telemetry.Store` | `"TxName.begin_tx"` | |
| Service op | `telemetry.Service` | `"MethodName.step"` | `"Login.login_tx"` |
| Mailer | `telemetry.Mailer` | `"FnName.smtp_send"` | `"sendOTPEmail.smtp_send"` |
| Redis / KV | `telemetry.KVStore` | `"FnName.redis_op"` | `"Get.redis_get"` |
| JWT | `telemetry.Token` | `"FnName.step"` | `"MintTokens.sign_access"` |
| Crypto | `telemetry.Crypto` | `"FnName.step"` | `"Decrypt.authenticate"` |
| RBAC | `telemetry.RBAC` | `"FnName.step"` | `"Require.check_user_access"` |
| OAuth outbound | `telemetry.OAuth` | `"FnName.endpoint"` | `"ExchangeCode.token_endpoint"` |
| Worker/dispatch | `telemetry.Worker` | `"FnName.step"` | `"ClaimJob.query"` |

```go
// service layer
return RegisterResult{}, telemetry.Service("Register.hash_password", err)
// store layer
return CreatedUser{}, telemetry.Store("CreateUserTx.create_user", err)
// platform layers
return telemetry.Mailer("sendOTPEmail.smtp_send", err)
return telemetry.KVStore("Get.redis_get", err)
```

All constructors return nil when err is nil — safe to use directly in return statements.
Op strings are for logs only — they never become Prometheus label values.

---

## `AllowContentType` convention

Applied at the **root assembler** level, not at feature level:

| Domain | `AllowContentType` applied? | Reason |
|---|---|---|
| `auth/` | Yes — in `auth.Routes` | All endpoints consume `application/json` |
| `profile/` | Yes — in `profile.Routes` | All endpoints consume `application/json` |
| `oauth/` | **No** — intentionally omitted | OAuth endpoints are browser redirects or GET/DELETE; no JSON body |
| `rbac/` | Yes — in `ownerRoutes` and `adminRoutes` separately | Routes within each sub-router consume `application/json` |

---

## SQL conventions

- All SQL in `sql/queries/{domain}.sql` (production) or `sql/queries_test/{domain}_test.sql` (test-only).
- Every query has a `-- name:` directive in PascalCase.
- Groups within file use `/* ── {Section} ── */` separators.
- **No raw SQL strings in any `.go` file — production or test.** (`docs/rules/RULES.md §3.9`)
- Run `make sqlc` after any SQL change.

**Banned in `.go` files:**
```go
pool.Exec(ctx, "UPDATE users SET ...")     // banned
tx.QueryRow(ctx, "SELECT id FROM ...")     // banned
```

**Test-only queries** go in `sql/queries_test/auth_test.sql`. To call them:
```go
// Wrong — db.Querier does not expose test-only methods
q.CreateVerifiedUser(...)       // compile error

// Correct — use *db.Queries
cq := db.New(tx)
cq.CreateVerifiedUser(...)
```

---

## HTTP conventions

Every POST/PUT/PATCH handler must begin with:
```go
r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
```

Validation runs in the handler before any service call. Service never validates raw HTTP input.

### URL path naming

Paths identify **resources and sub-resources**. The HTTP method carries the verb. Path segments are always lowercase nouns or noun-phrases — never verb phrases.

**The core rule:** if a path segment reads like an imperative command (`forgot-password`, `verify-reset-code`, `force-password-reset`), it is wrong. Reorganise around the noun.

| ❌ Banned | ✓ Correct | Reason |
|---|---|---|
| `POST /auth/forgot-password` | `POST /auth/password/reset` | resource `password`, step `reset` |
| `POST /auth/verify-reset-code` | `POST /auth/password/reset/verify` | step `verify` as sub-path |
| `POST /auth/change-password` | `PATCH /auth/password` | PATCH implies change |
| `POST /auth/resend-verification` | `POST /auth/verification/resend` | resource first |
| `POST /auth/request-unlock` | `POST /auth/unlock` | POST implies request |
| `POST /auth/confirm-unlock` | `PUT /auth/unlock` | PUT implies confirm/replace |
| `POST /admin/users/{id}/force-password-reset` | `POST /admin/users/{id}/password/reset` | same hierarchy as auth domain |
| `POST /profile/me/cancel-deletion` | `DELETE /profile/me/deletion` | DELETE implies cancel |

**Multi-step flows** share the base path; method and optional `/step` sub-path distinguish phases:
```
POST  /password/reset          ← step 1: request OTP
POST  /password/reset/verify   ← step 2: verify OTP, receive grant token
PUT   /password/reset          ← step 3: apply new password

POST  /me/email                ← step 1: request change OTP
POST  /me/email/verify         ← step 2: verify current-email OTP
PUT   /me/email                ← step 3: confirm
```

**Permitted single-word step nouns** as the final segment: `verify`, `resend`, `assign`, `transfer`. These are nouns in context — they name a lifecycle phase, not an action.

**Hyphens in path segments are banned.** Either split into two segments (`password-reset` ❌ → `password/reset` ✓) or drop the redundant word when the shorter form is unambiguous (`magic-link` ❌ → `magic` ✓).

### Always use `platform/*` — never hand-roll equivalents

| Concern | Required | Banned |
|---|---|---|
| JSON success | `respond.JSON(w, status, v)` | `json.NewEncoder(w).Encode(...)` |
| JSON error | `respond.Error(w, status, code, msg)` | raw `w.Write(...)` |
| 204 | `respond.NoContent(w)` | `w.WriteHeader(http.StatusNoContent)` |
| Body decode | `respond.DecodeJSON[T](w, r)` | `json.NewDecoder(r.Body).Decode(...)` |
| Client IP | `respond.ClientIP(r)` | `r.RemoteAddr` |
| JWT sign/parse | `platform/token` helpers | direct jwt library calls |
| Rate limiting | `platform/ratelimit` limiters | ad-hoc counters or sync.Map |

Refresh token cookie flags (mandatory):
- `HttpOnly: true`
- `SameSite: http.SameSiteStrictMode`
- `Secure: h.secureCookies`
- `Path: "/api/v1/auth"`
- `MaxAge` driven by DB row — never a hardcoded duration

Authenticated routes always extract identity via `token.UserIDFromContext(r.Context())`.
Never read the `Authorization` header directly.

---

## Three-file mandatory syncs

### §S-1 — Audit event triad

When adding a new audit event, update all three atomically:

| Location | What to add |
|---|---|
| `internal/audit/audit.go` const block | `EventXxx EventType = "value_string"` |
| `internal/audit/audit.go` `AllEvents()` | `EventXxx,` |
| `audit_test.go` cases table | `{audit.EventXxx, "value_string"},` |

The test enforces `len(AllEvents()) == len(cases)`. A count mismatch fails the whole package.

### §S-2 — Querier / QuerierProxy / nopQuerier triad

When `make sqlc` adds a new method to `db.Querier`, update all three:

| File | What to add |
|---|---|
| Domain testutil `querier_proxy.go` | Forwarding method + `Fail{MethodName} bool` field |
| `querier_proxy_test.go` | Stub on `nopQuerier` returning zero + nil |
| Any other `*_test.go` with a local `db.Querier` impl | Same stub |

Run `go build ./internal/domain/{name}/shared/testutil/...` after every `make sqlc`.

### §S-3 — DecodeJSON 413 path

`respond.DecodeJSON` drains the remaining body after a JSON syntax error and
re-checks for `*http.MaxBytesError`. Do not remove `io.Copy(io.Discard, r.Body)`.
Handler tests asserting 413 must send a raw byte slice (not valid JSON) as the
oversized body.

---

## Testing conventions

### Fake location

Domain fakes (`Storer`, `Servicer` implementations) live exclusively in
`internal/domain/{name}/shared/testutil/`. No per-feature `testutil/` folder.
Each domain (`auth`, `oauth`, `rbac`) has its own `shared/testutil/` — do NOT add fakes for one domain into another domain's testutil.

Platform interface fakes may be defined locally in `handler_test.go` or in
the platform package's own `testutil/` — NOT in the domain testutil.

### Test file layout

| File | Build tag | Contents |
|---|---|---|
| `handler_test.go` | none | Handler unit tests using `httptest.NewRecorder` + FakeServicer |
| `service_test.go` | none | Service unit tests using FakeStorer. All `t.Parallel()`. |
| `store_test.go` | `//go:build integration_test` | `TestMain`, `testPool`, `txStores`, seed helpers, all store integration tests |

No `main_test.go`. No non-build-tagged `{feature}_test.go`. `TestMain` lives
exclusively in `store_test.go` behind `integration_test`.

### Canonical TestMain

```go
func TestMain(m *testing.M) { authsharedtest.RunTestMain(m, &testPool, 20) }
```

For `oauth/` and `rbac/` domain tests use the domain's own testutil package (`oauthsharedtest.RunTestMain` or `rbacsharedtest.RunTestMain`). Never write pool-creation or bcrypt-cost boilerplate by hand.

### txStores convention

```go
func txStores(t *testing.T) (*{feature}.Store, *db.Queries) {
    t.Helper()
    if testPool == nil {
        t.Skip("no test database configured")
    }
    // Use the domain testutil package: authsharedtest, oauthsharedtest, or rbacsharedtest.
    _, q := {domain}sharedtest.MustBeginTx(t, testPool)
    return {feature}.NewStore(testPool).WithQuerier(q), q
}
```

### Integration test naming

All integration test functions carry the suffix `_Integration`:
```go
func TestCreateUserTx_Integration(t *testing.T) { ... }
```

### bcrypt cost in tests

`SetBcryptCostForTest` controls bcrypt cost for both `HashPassword` and
`GenerateCodeHash`. `RunTestMain` lowers it for the entire binary. Use
`authsharedtest.MustOTPHash` and `MustHashPassword` in tests — never call
`bcrypt.GenerateFromPassword` directly.

### MaxConns = 20 (required)

`IncrementAttemptsTx` and `IncrementLoginFailuresTx` always open a **fresh pool
transaction** that must run concurrently with the outer test transaction.
With pgxpool's default of 4 connections this deadlocks. Always pass `maxConns = 20`.

### Structurally unreachable branches

Place `// Unreachable:` comment in the **source file** above the dead branch.
Do **not** create `t.Skip` stubs — they signal "runnable under right conditions"
but these branches can never be reached.

```go
// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin.
if err != nil {
    return fmt.Errorf("store.CreateUserTx: begin tx: %w", err)
}
```

---

## Audit log

All event names are typed constants in `internal/audit/audit.go`. No string
literal event name may appear in any store file.

```go
// Correct
h.q.InsertAuditLog(ctx, db.InsertAuditLogParams{
    EventType: audit.EventRegister,
})
```

---

## `context.WithoutCancel` rule

Security-critical writes that must not be aborted by a client disconnect:
- OTP attempt counter increments
- Login failure counter increments
- Failed login audit rows
- Token family revocation on reuse detection
- Session/cookie clearing on logout

```go
s.store.IncrementAttemptsTx(context.WithoutCancel(ctx), ...)
```

---

## Anti-enumeration timing

Endpoints that may reveal email existence must equalise latency:

1. **Dummy hash on no-rows:** always call `CheckPassword`/`VerifyCodeHash` even
   when user not found — against a precomputed dummy hash.
2. **Uniform 202:** resend/forgot-password/unlock always return `202 Accepted`
   with the same body regardless of whether the email exists.

---

## Configuration

`internal/config/config.go` is the **only** file that may call `os.Getenv`.
Use the dedicated helpers in tests:
```go
dsn := config.TestDatabaseURL()   // reads TEST_DATABASE_URL
url := config.TestRedisURL()      // reads TEST_REDIS_URL
```

---

## Mailer template convention

Never add methods to `SMTPMailer`. Use the registry pattern:
```go
deps.MailQueue.Enqueue(r.Context(), func(ctx context.Context) error {
    return deps.Mailer.Send(mailertemplates.{Name}Key)(ctx, toAddr, code)
})
```

Adding a new email type = three-file change:
1. `internal/platform/mailer/templates/{name}.go` — key const, exported `*string` var
2. `internal/platform/mailer/templates/registry.go` — one `Entry` in `Registry()` map
3. Handler call site — `deps.Mailer.Send(mailertemplates.{Name}Key)(...)`

---

## Telemetry & Observability

Every feature must satisfy the 9-gate decision process from
`docs/design/monitoring/telemetry-checklist.md`. The summary for each stage
of feature implementation is below.

### One import rule

Any package needing logging, error wrapping, or metrics imports **only**
`internal/platform/telemetry`. Never import `log/slog`,
`prometheus/client_golang`, or any other observability library directly.

### Gate 1 — Logger (always required)

Declare one package-level logger per feature file set:

```go
// At the top of service.go (or handler.go if it has error logging too)
var log = telemetry.New("{feature}")          // e.g. telemetry.New("login")
var log = telemetry.New("worker.{feature}")   // e.g. telemetry.New("worker.purge")
```

### Gate 2 — Error wrapping (always required)

Use the layer constructors in the table above. These are free — `TelemetryHandler`
automatically fires `app_errors_total` on every `log.Error` call. No Prometheus
registration needed.

**WARN vs ERROR discipline:**
- `log.Warn` — best-effort secondary ops that don’t affect the primary response
  (audit write failures, counter resets). Does NOT fire `app_errors_total`.
- `log.Error` — primary-operation failures or dependency outages. Fires
  `app_errors_total` and writes the fault into the HTTP carrier.

### Gate 3 — Does this event need its own metric?

Ask: **"Would I page someone specifically because of this signal, or investigate
it independently of the HTTP error rate?"**

| Answer | Action |
|---|---|
| No — a 500 error spike already captures it | Stop at Gate 2. Nothing else needed. |
| Yes — it needs its own counter/gauge | Continue to Gates 4–9. |

**Stops at Gate 2 (no new metric needed):**
- A DB query fails → `app_errors_total` covers it.
- A handler returns 422 → `http_requests_total{status="422"}` covers it.
- A background job crashes → `app_errors_total{component="worker.*"}` covers it.

**Needs a new metric:**
- User places an order → business throughput, not an error.
- Login fails with wrong password → security signal (already in Family 3 auth).
- A new business domain event → see Gate 4.

### Gate 4 — Which family?

| Event is about… | Family | Already automatic? |
|---|---|---|
| HTTP request shape | 1 — HTTP | ✅ auto (RequestMiddleware) |
| Any error in any package | 2 — App Errors | ✅ auto (TelemetryHandler) |
| Auth / account / session | 3 — Auth | Explicit — add recorder method |
| DB pool / Redis / goroutines | 4 — Infra | ✅ auto (InfraPoller) |
| Job queue lifecycle | 5 — Job Queue | ✅ auto (MetricsRecorder) |
| Bitcoin / payments | 6 — Bitcoin | Explicit — add recorder method |
| New domain (e.g. orders) | New family | Justify it; add `{domain}_hooks.go` |

If the family is auto-covered and no independent signal is needed — **stop**.

### Gate 5–6 — Instrument + label safety

| Event… | Instrument |
|---|---|
| Counts occurrences (never goes down) | `CounterVec`, name ends in `_total` |
| Measures current state | `Gauge` |
| Measures duration / distribution | `HistogramVec`, name ends in `_seconds` |

**Label safety — mandatory:**
- Values must come from a **bounded constant set** — never from user input,
  URL path segments, error messages, UUIDs, or email addresses.
- Document allowed values as Go constants next to the interface method.
- Cardinality = product of all label value counts. Keep under 100 per metric.

```go
// Good — bounded constant set
const (
    OrderMethodCard   = "card"
    OrderMethodBTC    = "bitcoin"
)

// Bad — unbounded; never do this
r.orders.WithLabelValues(req.UserID).Inc()   // one series per user — cardinality bomb
```

### Gate 7 — Where does the code live?

| What | Where |
|---|---|
| Interface method declaration + Noop struct | `domain/{domain}/shared/recorder.go` |
| Counter/gauge field on Registry | `internal/platform/telemetry/metrics.go` |
| Hook method on `*Registry` | `internal/platform/telemetry/{domain}_hooks.go` |
| Registration in `NewRegistry()` | `internal/platform/telemetry/metrics.go` |
| Local narrow interface in sub-package | `domain/{domain}/{feature}/service.go` |
| Recorder call at the event site | same service file |

**Sub-packages never import `domain/{domain}/shared/recorder.go` directly** —
they define a local narrow interface covering only the methods they call.
`*telemetry.Registry` satisfies it structurally.

```go
// domain/auth/login/service.go — narrow local interface
type recorder interface {
    OnLoginSuccess(provider string)
    OnLoginFailed(provider, reason string)
    OnUserLocked(reason string)
}
```

**Provider / reason label safety for auth:** All `provider` values passed to
`OnOAuth*` methods must be normalised through a bounded allowlist before the
call. Never pass a raw URL path segment.

### Gate 8 — Does it need an alert?

| Severity | When |
|---|---|
| `critical` | SLO breached, funds at risk, any single occurrence unacceptable |
| `warning` | Investigate within the hour |
| `warning` + `team: security` | Auth / fraud anomaly — routes to `#security-alerts` |

Add alert YAML to `docs/design/monitoring/monitoring-technical.md §20`.

### Gate 9 — Frontend dashboard obligations

Always check against the existing panels in
`D:\Projects\store\frontend\lib\api\telemetry\prometheus.ts`.

**Already covered — no frontend change needed:**
- HTTP error rate, request rate, latency → existing area chart panels.
- App errors by component → existing bar chart.
- Service health (DB, Redis, mailer, jobs) → `deriveServices()` already covers
  the standard infrastructure services.
- Any anomaly with an alert rule → `computeAnomalies()` in `prometheus.ts`.

**Needs a frontend change:**

| Scenario | What to add to `prometheus.ts` | What to add to the dashboard |
|---|---|---|
| New security signal | New `instant()` query + field on `SecuritySnapshot` | Case in `computeAnomalies()` + stat card in `security-dashboard.tsx` |
| New business stat (e.g. orders/min) | New `instant()` query + field on a new `{Domain}Snapshot` type | `StatCard` + `MetricAreaChart` |
| New infrastructure dependency | New `instant()` query for error count; entry in `deriveServices()` | `ServiceHealth` row appears automatically |
| New anomaly detection rule (no new metric) | Case in `computeAnomalies()` only | `AnomalyFeed` picks it up automatically |

**Quick-reference by scenario:**

```
Scenario A: New API route, no special business event
  ✅ var log = telemetry.New("orders")           ← Gate 1
  ✅ return telemetry.Store("GetOrder.q", err)   ← Gate 2
  Nothing else. HTTP + error metrics are free.

Scenario B: New business event worth counting (e.g. order placed)
  ✅ Gates 1-2 as above
  ✅ domain/orders/shared/recorder.go           ← add OnOrderPlaced(method string)
  ✅ telemetry/metrics.go                       ← add ordersPlaced *prometheus.CounterVec
  ✅ telemetry/orders_hooks.go                  ← implement OnOrderPlaced on *Registry
  ✅ telemetry/metrics.go NewRegistry()         ← register the counter
  ✅ domain/orders/{feature}/service.go         ← narrow recorder interface + call site
  Optional: alert + frontend stat card / chart

Scenario C: New security signal (e.g. failed 2FA)
  ✅ Everything in Scenario B
  ✅ Alert with team: security in monitoring-technical.md §20
  ✅ Case in computeAnomalies() in prometheus.ts
  ✅ Stat card in security-dashboard.tsx

Scenario D: New infrastructure dependency (e.g. external payment API)
  ✅ Gates 1-2 as above
  ✅ New gauge/counter in metrics.go for connectivity / error rate
  ✅ Hook method or InfraPoller extension
  ✅ Entry in deriveServices() in prometheus.ts
  ✅ Warning alert if it can go down
```

### Tests

Use `telemetry.NewNoopRegistry()` in all tests — it returns a fully wired
`*Registry` on its own scoped Prometheus registry. Two test calls never
collide on metric names and neither affects the application registry.
