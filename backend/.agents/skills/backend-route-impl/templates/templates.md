# Templates — All Stage Prompt Templates

Copy the relevant template, fill every `{placeholder}`, and save to
`docs/prompts/{feature}/{N}-{stage-name}.md`. Leave no placeholder unfilled.

---

## Stage 0 — Design template

```markdown
# {Feature} — Stage 0: Design

**Section:** INCOMING.md §{section}
**Package:** `internal/domain/{domain}/{route}/`

---

## §1 — Requirements summary

{1–3 sentence plain-English description of what this endpoint does and why.}

---

## §2 — HTTP contract

### {METHOD} {path}

**Auth:** {None | Bearer JWT — authenticated user | Bearer JWT — admin only}
**Content-Type:** {application/json | none}
**Body size cap:** 1 MiB (`http.MaxBytesReader`)

#### Request body

| Field | Type | Required | Constraints |
|---|---|---|---|
| `{field}` | string | yes | max 255 chars, trimmed |

#### Success responses

| Status | Code | Body |
|---|---|---|
| 200 | — | `{"field": "value"}` |
| 202 | — | `{"message": "..."}` |
| 204 | — | *(empty)* |

#### Error responses

| Status | Code string | Condition |
|---|---|---|
| 400 | `{snake_case_code}` | {condition} |
| 401 | `unauthorized` | Missing or invalid JWT |
| 413 | `request_entity_too_large` | Body > 1 MiB |
| 422 | `invalid_request` | Malformed JSON |
| 429 | `rate_limit_exceeded` | IP/user rate limit hit |
| 500 | `internal_error` | Unexpected server error |

---

## §3 — Decisions

| ID | Question | Decision | Rationale |
|---|---|---|---|
| D-01 | {question} | {answer} | {why} |

*(For RBAC-gated routes add rows for:)*
*(D-xx: Which `rbac.Perm*` constant guards this route?)*
*(D-xx: Does this route need `ApprovalGate`? Only if `access_type = 'request'` is possible.)*
*(D-xx: Is there a `ConditionalEscalator` path? Only if `access_type = 'conditional'` is possible.)*

---

## §4 — New audit events

| Constant | Value string | Emitted when |
|---|---|---|
| `Event{PastTense}` | `"{snake_case}"` | {condition} |

---

## §5 — Guard ordering

### {HandlerMethodName}

```
1. Cap body: http.MaxBytesReader
2. Decode body: respond.DecodeJSON[{Request}]
3. Validate: validateAndNormalise(&req)
4. {Auth guard if needed}: token.UserIDFromContext
5. {Rate limit if user-scoped}: userLimiter.Allow
6. {Business guard 1}: svc.{Method}
   → {ErrSentinel} if {condition}
7. {Business guard 2}
   → {ErrSentinel} if {condition}
8. {Mutate / success}
9. {Side effect: audit, email, cookie}
```

---

## §6 — Rate limiting

| Limiter | Scope | KV prefix | Rate | Burst | TTL |
|---|---|---|---|---|---|
| `{name}Limiter` | IP | `{xx:ip:}` | {N}/{duration} | {N} | {duration} |

**Collision check:** confirm prefix is not in `references/e2e-status.md §KV prefixes`.

---

## §7 — Test case inventory

### S-layer (service unit tests) — `service_test.go`

| ID | Scenario | Assert |
|---|---|---|
| T-01 | Happy path | Returns success result, nil error |
| T-02 | {Sentinel}: {guard description} | `errors.Is(err, {ErrSentinel})` |
| T-03 | Timing invariant: dummy {hash/OTP} on not-found | Dummy called exactly once |
| T-04 | `context.WithoutCancel` on {write} | Captured ctx has nil `Done()` channel |

### H-layer (handler unit tests) — `handler_test.go`

| ID | Scenario | Expected status | Expected code |
|---|---|---|---|
| T-{N} | {ErrSentinel} from service | {status} | `{code_string}` |
| T-{N} | Missing Authorization header | 401 | `unauthorized` |
| T-{N} | Malformed JSON body | 422 | `invalid_request` |
| T-{N} | Body > 1 MiB | 413 | `request_entity_too_large` |
| T-{N} | {field} empty | 400 | `{code_string}` |

### I-layer (store integration tests) — `store_test.go`

| ID | Scenario | Assert |
|---|---|---|
| T-{N} | {Method}: found + valid | Returns populated struct |
| T-{N} | {Method}: not found | `errors.Is(err, Err{Sentinel})` |
| T-{N} | `QuerierProxy.Fail{Query}` | `errors.Is(err, {domain}sharedtest.ErrProxy)` |

---

## §8 — Open questions

| # | Question | Blocked by |
|---|---|---|
| 1 | {question} | {what needs to be decided first} |

*All questions must be answered before Stage 1 begins.*
```

---

## Stage 1 — Foundations template

```markdown
# {Feature} — Stage 1: Foundations

**Depends on:** Stage 0 approved.
**Goal:** SQL queries, audit event constant, models, request/response types.

---

## Read first (no modifications)

| File | Why |
|---|---|
| Domain SQL file (see Stage 0 table) — tail 60 lines | Append position; confirm section style |
| `internal/audit/audit.go` | Full const + AllEvents() — needed for sync triad |
| Analogous `models.go`, `requests.go`, `validators.go`, `errors.go` | Pattern reference |
| `docs/RULES.md §3.9` (SQL) + `§3.11` (naming) | Conventions |

---

## Deliverables

### 1. SQL — Domain SQL file

Append a new section:

```sql
/* ── {Feature} ── */

-- name: {QueryName} :one/:exec/:execrows
{SQL body}
```

Run `make sqlc` after adding queries.

### 2. Audit event — `internal/audit/audit.go`

Three-file sync §S-1: add to `const` block, `AllEvents()`, and test cases table.

```go
Event{PastTense} EventType = "{snake_case_value}"
```

### 3. Models — `{feature}/models.go`

```go
// {Operation}Input is the service-layer input for {description}.
type {Operation}Input struct {
    // No json: tags. No pgtype. Plain Go types only.
}

// {Operation}Result is the service-layer result for {description}.
type {Operation}Result struct {
    // No json: tags. No pgtype. Plain Go types only.
}
```

### 4. Request/response — `{feature}/requests.go`

```go
// {Feature}Request is the HTTP request body for {endpoint}.
type {Feature}Request struct {
    Field string `json:"field"`
}

// {Feature}Response is the HTTP response body for {endpoint}.
type {Feature}Response struct {
    Field string `json:"field"`
}
```

### 5. Sentinel errors — `{feature}/errors.go` (if feature-exclusive)

```go
var Err{Condition} = errors.New("{human readable}")
```

### 6. Validators — `{feature}/validators.go` (if feature-exclusive)

```go
func validateAndNormalise(req *{Feature}Request) error {
    // ...
}
```

---

## Done when

- [ ] `make sqlc` runs cleanly (new queries in `internal/db/`)
- [ ] Audit triad (const + AllEvents + test case) all updated
- [ ] `go build ./internal/domain/{domain}/{route}/...` passes
```

---

## Stage 2 — Data layer template

```markdown
# {Feature} — Stage 2: Data layer

**Depends on:** Stage 1 complete — `make sqlc` green.
**Goal:** Store struct, Storer interface, FakeStorer entry, QuerierProxy entries.

---

## Read first (no modifications)

| File | Why |
|---|---|
| Domain `db/*.sql.go` file (see Stage 0 table) | Confirm generated method signatures from Stage 1 |
| `{feature}/service.go` | Storer interface definition location |
| Domain testutil `fake_storer.go` (see Stage 0 table) | Existing FakeStorer layout |
| Domain testutil `querier_proxy.go` (see Stage 0 table) | Existing QuerierProxy layout |
| Analogous `store.go` | Implementation pattern |
| `docs/RULES.md §3.3` + `§3.4` | Store shape + error wrapping rules |

---

## Deliverables

### 1. `{feature}/store.go`

Required shape — never deviate:

```go
// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store implements Storer for the {feature} feature.
type Store struct {
    {domain}shared.BaseStore  // authshared, oauthshared, or rbacshared — see Stage 0 table
}

// NewStore constructs a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
    return &Store{BaseStore: {domain}shared.NewBaseStore(pool)}
}

// WithQuerier returns a copy of s that uses q for all database calls.
func (s *Store) WithQuerier(q db.Querier) *Store {
    c := *s
    c.BaseStore = s.BaseStore.WithQuerier(q)
    return &c
}

// {MethodName} {description}. Returns {ErrSentinel} on {condition}.
func (s *Store) {MethodName}(ctx context.Context, {params}) ({Result}, error) {
    // pgtype conversions here — never leak pgtype past this boundary
}
```

### 2. `Storer` interface — `{feature}/service.go`

Add the new method to the existing `Storer` interface.

### 3. `{Feature}FakeStorer` — Domain testutil `fake_storer.go`

Add new struct + compile-time check:

```go
// {Feature}FakeStorer implements {feature}.Storer for service unit tests.
type {Feature}FakeStorer struct {
    {MethodName}Fn func(ctx context.Context, {params}) ({Result}, error)
}

var _ {feature}.Storer = (*{Feature}FakeStorer)(nil)

func (f *{Feature}FakeStorer) {MethodName}(ctx context.Context, {params}) ({Result}, error) {
    if f.{MethodName}Fn != nil {
        return f.{MethodName}Fn(ctx, {params})
    }
    return {Result}{}, nil
}
```

### 4. `QuerierProxy` — Domain testutil `querier_proxy.go`

Add under the `// ── {feature} ──` section separator:

```go
// ── {feature} ────────────────────────────────────────────────────────────────
Fail{QueryName} bool
```

Add the forwarding method:

```go
func (p *QuerierProxy) {QueryName}(ctx context.Context, {params}) ({result}, error) {
    if p.Fail{QueryName} {
        return {zero}, ErrProxy
    }
    return p.Base.{QueryName}(ctx, {params})
}
```

---

## Done when

- [ ] `go build ./...` passes
- [ ] `var _ Storer = (*Store)(nil)` compile-time check present in `store.go`
- [ ] `var _ db.Querier = (*QuerierProxy)(nil)` still passes
- [ ] No `pgtype.*` in public `Store` method signatures
```

---

## Stage 3 — Logic layer template

```markdown
# {Feature} — Stage 3: Logic layer

**Depends on:** Stage 2 complete — `go build ./...` passes.
**Goal:** Service method(s), Servicer interface update, FakeServicer, service unit tests.

---

## Read first (no modifications)

| File | Why |
|---|---|
| `{feature}/service.go` | Constructor + Storer interface |
| `{feature}/handler.go` | Servicer interface location |
| `{feature}/models.go` | I/O types |
| Domain testutil `fake_servicer.go` (see Stage 0 table) | Existing FakeServicer layout |
| `docs/prompts/{feature}/0-design.md §5` | Guard ordering (source of truth) |
| `docs/prompts/{feature}/0-design.md §7` | S-layer test cases to implement |
| `docs/RULES.md §3.4, §3.6, §3.7` | Error wrapping, WithoutCancel, timing |

---

## Deliverables

### 1. Service method — `{feature}/service.go`

Follow guard ordering from `0-design.md §5` exactly. No deviations.

```go
// {MethodName} {description}.
//
// Timing invariant: {dummy call description if applicable}.
func (s *Service) {MethodName}(ctx context.Context, in {Input}) ({Result}, error) {
    // 1. {Guard description}
    // Security: context.WithoutCancel so a client disconnect cannot skip {write}.
}
```

### 2. Servicer interface — `{feature}/handler.go`

Add the new method to the existing `Servicer` interface.

### 3. `{Feature}FakeServicer` — Domain testutil `fake_servicer.go`

```go
// {Feature}FakeServicer implements {feature}.Servicer for handler unit tests.
type {Feature}FakeServicer struct {
    {MethodName}Fn func(ctx context.Context, in {feature}.{Input}) ({feature}.{Result}, error)
}

var _ {feature}.Servicer = (*{Feature}FakeServicer)(nil)

func (f *{Feature}FakeServicer) {MethodName}(ctx context.Context, in {feature}.{Input}) ({feature}.{Result}, error) {
    if f.{MethodName}Fn != nil {
        return f.{MethodName}Fn(ctx, in)
    }
    return {feature}.{Result}{}, nil
}
```

**Note:** the package name is `{domain}sharedtest` — `authsharedtest`, `oauthsharedtest`, or `rbacsharedtest`
depending on domain. See Stage 0 testutil table.

### 4. Service unit tests — `{feature}/service_test.go`

One sub-test per S-layer row in `0-design.md §7`. All tests parallel.

```go
func Test{MethodName}(t *testing.T) {
    t.Parallel()

    t.Run("T-01 happy path", func(t *testing.T) { ... })
    t.Run("T-02 {ErrSentinel}", func(t *testing.T) { ... })
    t.Run("T-03 timing invariant dummy hash", func(t *testing.T) { ... })
    t.Run("T-04 context.WithoutCancel on increment", func(t *testing.T) { ... })
}
```

---

## Done when

- [ ] `go test ./internal/domain/{domain}/{route}/...` (unit only) passes
- [ ] Every S-layer T-NN case from `0-design.md §7` has a sub-test
- [ ] Timing invariant dummy calls explicitly asserted
- [ ] `context.WithoutCancel` paths explicitly asserted
```

---

## Stage 4 — HTTP layer template

```markdown
# {Feature} — Stage 4: HTTP layer

**Depends on:** Stage 3 complete — service unit tests green.
**Goal:** Handler methods, routes wiring, handler unit tests, store integration tests.

---

## Read first (no modifications)

| File | Why |
|---|---|
| `{feature}/handler.go` | Handler struct + Servicer interface |
| `{feature}/routes.go` | Route registration |
| `{feature}/requests.go` + `validators.go` | Request/validation types |
| Domain testutil `fake_servicer.go` (see Stage 0 table) | FakeServicer for handler tests |
| `docs/prompts/{feature}/0-design.md §2` | HTTP contract |
| `docs/prompts/{feature}/0-design.md §7` | H-layer and I-layer test cases |
| `docs/RULES.md §3.10` + `§3.13` | HTTP conventions + sub-package checklist |

---

## Deliverables

### 1. Handler method — `{feature}/handler.go`

```go
// {MethodName} handles {METHOD} {path}.
func (h *Handler) {MethodName}(w http.ResponseWriter, r *http.Request) {
    r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

    req, ok := respond.DecodeJSON[{Request}](w, r)
    if !ok {
        return
    }

    if err := validateAndNormalise(&req); err != nil {
        respond.Error(w, http.StatusBadRequest, codeFor(err), err.Error())
        return
    }

    result, err := h.svc.{MethodName}(r.Context(), {feature}.{Input}{...})
    if err != nil {
        switch {
        case errors.Is(err, {feature}.Err{Sentinel}):
            respond.Error(w, http.Status{Code}, "{code_string}", err.Error())
        // ... all sentinels
        default:
            slog.ErrorContext(r.Context(), "{domain}.{MethodName}: service error", "error", err)
            respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
        }
        return
    }

    respond.JSON(w, http.Status{Code}, {Response}{...})
}
```

### 2. Routes — `{feature}/routes.go`

**Standard rate-limited route (auth/profile domain):**

```go
// Routes registers the {feature} endpoint on r.
// Call from the {domain} root assembler:
//
//	{feature}.Routes(ctx, r, deps)
//
// Rate limits:
//   - {METHOD} {path}: {N} req / {duration} per {IP|user}
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
    store := NewStore(deps.Pool)
    svc   := NewService(store)
    h     := NewHandler(svc, deps.JWTConfig, deps.SecureCookies)

    // {N} req / {duration} per IP — {attack description}.
    ipLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "{prefix}:ip:", {rate}, {burst}, {ttl})
    go ipLimiter.StartCleanup(ctx)

    r.With(ipLimiter.Limit).{Method}("/{path}", h.{MethodName})
}
```

**RBAC-gated admin route (no rate limiter; access controlled by permission):**

```go
// Routes registers the {feature} admin endpoints on r.
// Called from adminRoutes in internal/domain/rbac/routes.go:
//
//	{feature}.Routes(ctx, r, deps)
//
// All routes require a valid JWT and the {rbac.Perm*} permission.
// No additional rate limiter — admin routes are RBAC-gated.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
    store := NewStore(deps.Pool)
    svc   := NewService(store)
    h     := NewHandler(svc)

    r.With(deps.JWTAuth, deps.RBAC.Require(rbac.{PermConst})).
        {Method}("/{path}", h.{MethodName})
}
```

**RBAC-gated route with ApprovalGate (access_type = 'request' possible):**

```go
    r.With(
        deps.JWTAuth,
        deps.RBAC.Require(rbac.{PermConst}),
        deps.RBAC.ApprovalGate(deps.ApprovalSubmitter),
    ).{Method}("/{path}", h.{MethodName})
```

### 3. Handler unit tests — `{feature}/handler_test.go`

One sub-test per H-layer row in `0-design.md §7`.

```go
func Test{MethodName}Handler(t *testing.T) {
    t.Parallel()

    t.Run("T-{N} {ErrSentinel} → {status}", func(t *testing.T) {
        svc := &{domain}sharedtest.{Feature}FakeServicer{
            {MethodName}Fn: func(...) (..., error) {
                return ..., {feature}.Err{Sentinel}
            },
        }
        // For auth/profile: NewHandler(svc, testJWTConfig, false)
        // For rbac admin:   NewHandler(svc)  — no JWTConfig; JWT auth is middleware
        h := NewHandler(svc, ...)
        // httptest setup + assert
    })
}
```

### 4. Store integration tests — `{feature}/store_test.go`

Add behind `//go:build integration_test`. One sub-test per I-layer row.

```go
func Test{StoreName}_Integration(t *testing.T) {
    store, q := txStores(t)
    // seed + call + assert DB state
}
```

---

## Done when

- [ ] `go build ./internal/domain/{domain}/{route}/...` passes
- [ ] `go test ./internal/domain/{domain}/{route}/...` (unit) passes
- [ ] `go test -tags integration_test ./internal/domain/{domain}/{route}/...` passes
- [ ] `§3.13` sub-package split checklist fully satisfied
- [ ] KV prefix added to `references/e2e-status.md` collision table
```

---

## Stage 5 — Audit prompt template

```markdown
# {Feature} — Stage 5: Audit Review

**Feature:** {Feature} (§{section})
**Package:** `{package path}`
**Depends on:** Stage 4 complete — all production files compile.
`go build ./{package path}/...` passes. All H-layer unit tests green.

---

## Instructions for the reviewer

Perform a structured multi-role audit of the {Feature} HTTP layer.

**Before writing anything, read these files in full:**

1. `docs/prompts/{feature}/context.md`
2. `docs/prompts/{feature}/0-design.md` (§5 guard ordering + §7 test cases)
3. `{feature}/handler.go`
4. `{feature}/routes.go`
5. `{domain}/routes.go` (domain assembler)
6. `{feature}/models.go`
7. `{feature}/errors.go`
8. `{feature}/service.go`
9. `{shared errors file}`
10. `internal/audit/audit.go` — const block + AllEvents()
11. `internal/platform/token/cookie.go`
12. `internal/platform/token/jwt.go`
13. `internal/platform/kvstore/store.go`
14. `internal/platform/respond/respond.go`
15. `docs/RULES.md`

Produce **exactly four parts**, in order. No extra sections.

---

## Part 1 — Security Engineer

*Focus: {feature-specific security concerns}*

Finding format:
```
SEVERITY    Critical | High | Medium | Low | Info
LOCATION    <file>:<function or line>
FINDING     <one-sentence description>
IMPACT      <what an attacker could do if unfixed>
FIX         <what to change and why>
```

Checklist (report ✓ pass or a finding for each):

### 1.1 {Security area — e.g. Token integrity / PKCE / Cookie flags}
- [ ] {specific cryptographic or auth requirement from spec}
- [ ] {specific secret handling requirement}

### 1.2 {Security area — e.g. Audit logging}
- [ ] Every {action} writes {EventName} via `context.WithoutCancel`
- [ ] A client disconnect cannot abort any of the {N} audit writes

### 1.3 {Security area — e.g. Error information leakage}
- [ ] Error responses do not reveal internal state beyond what the spec allows

---

## Part 2 — Go Senior Engineer

*Focus: idiomatic Go, error handling, guard ordering correctness, concurrency.*

Use the same finding format as Part 1.

### 2.1 Error handling
- [ ] All `fmt.Errorf` wrapping uses `%w` (not `%v`)
- [ ] No sentinel defined in wrong package
- [ ] `errors.Is` used for all sentinel comparisons — no `==` on error values
- [ ] Default branch logs via `slog.ErrorContext` before responding

### 2.2 Guard ordering (one sub-section per handler method)

**{MethodName}:**
{Copy guard steps verbatim from 0-design.md §5, one checkbox per step}
- [ ] Step 1: {guard} — verify it fires at this position

### 2.3 Concurrency and shutdown
- [ ] Every `go limiter.StartCleanup(ctx)` passes the application root `ctx`
- [ ] No goroutines ignore `ctx.Done()`

### 2.4 Interface satisfaction
- [ ] All compile-time interface checks present

### 2.5 Package and import hygiene
- [ ] No production file imports a testutil package
- [ ] No circular domain imports

---

## Part 3 — Platform Compliance Reviewer

*Focus: correct use of `internal/platform/` abstractions.*

| Concern | Required helper | Status |
|---|---|---|
| JSON success response | `respond.JSON` | |
| JSON error response | `respond.Error` | |
| 204 No Content | `respond.NoContent` | |
| Request body decode | `respond.DecodeJSON[T]` | |
| Client IP extraction | `respond.ClientIP(r)` | |
| Body size cap | `http.MaxBytesReader` + `respond.MaxBodyBytes` | |
| Refresh cookie | `token.SetRefreshCookie` or `token.MintTokens` | |
| Access token signing | `token.GenerateAccessToken` via `token.MintTokens` | |
| User ID from context | `token.UserIDFromContext` | |
| KV get / set / delete | `kvstore.Store` interface | |
| IP rate limiting | `ratelimit.NewIPRateLimiter` | |
| User rate limiting | `ratelimit.NewUserRateLimiter` | |
| RBAC permission check | `deps.RBAC.Require(rbac.Perm*)` — never raw string | N/A or |
| RBAC approval gate | `deps.RBAC.ApprovalGate(deps.ApprovalSubmitter)` when `access_type=request` possible | N/A or |
| RBAC permission constant | `rbac.Perm*` constant — never a raw string literal | N/A or |

**RBAC-only checks (skip for auth/profile/oauth):**
- [ ] `deps.JWTAuth` comes **before** `deps.RBAC.Require(...)` in every `r.With(...)` chain
- [ ] `ApprovalGate` present only on routes where permission can have `access_type = 'request'`
- [ ] `ConditionalEscalator` nil-check in handler if conditional path is possible
- [ ] No IP rate limiter on pure admin routes (unless spec explicitly requires one)
- [ ] Route mounted under `/admin/` sub-router, not at `/api/v1/` root

Additionally verify:
- [ ] Domain assembler returns `*chi.Mux`
- [ ] Feature `routes.go` has no return value
- [ ] All audit event constants appear in `AllEvents()`
- [ ] All KV prefix strings match `context.md` exactly

---

## Part 4 — Test Coverage Reviewer

*Focus: identify every untested path.*

```
### handler.go

#### {MethodName} — unit tests
- [x/❌] T-NN: {scenario} → {expected outcome}

#### Structurally unreachable paths (no test stub needed)
- {function}:{branch} — {reason}
```

**Required test cases from Stage 0 §7 (H-layer):**

| ID | Handler method | Scenario |
|---|---|---|
{Copy every H-layer row from 0-design.md §7 verbatim.}

---

## Sync checklist before closing this stage

- [ ] All Part 1 Critical and High findings resolved
- [ ] All Part 2 guard-ordering deviations corrected
- [ ] All Part 3 platform violations corrected
- [ ] All Part 4 `[❌]` missing tests added to `handler_test.go`
- [ ] `go build ./{package path}/...` passes
- [ ] `go vet ./{package path}/...` passes
- [ ] `go test ./{package path}/...` green

Once all items checked, run unit tests manually and proceed to Stage 6.
```

---

## Appendix A — context.md template

Write immediately after Stage 0. Keep under 80 lines.

```markdown
# {Feature} — Resolved Context

**Section:** INCOMING.md §{section}
**Package:** `internal/domain/{domain}/{route}/`
**Status:** Stage 0 approved / Stage N complete

## Resolved paths
- SQL file: `sql/queries/{auth|oauth|rbac}.sql` (new section: `/* ── {Feature} ── */`)
- Models: `internal/domain/{domain}/{route}/models.go`
- Store: `internal/domain/{domain}/{route}/store.go`
- Service: `internal/domain/{domain}/{route}/service.go`
- Handler: `internal/domain/{domain}/{route}/handler.go`
- Routes: `internal/domain/{domain}/{route}/routes.go`
- FakeStorer: `internal/domain/{domain}/shared/testutil/fake_storer.go`
- FakeServicer: `internal/domain/{domain}/shared/testutil/fake_servicer.go`
- QuerierProxy: `internal/domain/{domain}/shared/testutil/querier_proxy.go`

## Key decisions (from Stage 0 §3)
- D-01: {summary}
- D-02: {summary}

## New SQL queries
{query names, one per line}

## New audit events
{EventName = "value_string", one per line}

## New sentinel errors
{ErrName — package location}

## Rate-limit prefixes
{prefix: endpoint mapping}

## Test case IDs (from Stage 0 §7)
- S-layer: T-01 to T-{N}
- H-layer: T-{N+1} to T-{M}
- I-layer: T-{M+1} to T-{K}
```

---

## Appendix B — Stage 7 (E2E) template

```markdown
# {Feature} — Stage 7: E2E Tests

**Depends on:** Stage 6 (unit tests) all passing.
**Goal:** `e2e/{domain}/{feature}.json` + `make e2e-{feature}` target.

---

## Read first

| File | Why |
|---|---|
| Closest existing collection in `e2e/{domain}/` | Canonical format for this domain (see note below) |
| `make/e2e.mk` | Existing make target pattern |
| `docs/prompts/{feature}/0-design.md §2, §6, §7` | HTTP contract, rate limits, test inventory |

**Domain collection reference:**
- `auth/` or `profile/`: use `e2e/auth/change-password.json` — has OTP flow and failure folders
- `oauth/`: use the closest existing `e2e/oauth/*.json` — no OTP or register setup needed
- `rbac/`: use the closest existing `e2e/rbac/*.json` — no OTP or register setup; setup folder bootstraps an admin user instead

---

## Collection folder structure (in order)

| Folder | Contents |
|---|---|
| `setup` | Register (gmail), verify-email (OTP fetch), login — stores `{prefix}_access_token` |
| `happy-path` | One request per happy-path variant from §7 |
| `failures` | One request per §2 error code needing wire-check |
| `auth-failures` | Missing Authorization → 401; tampered token → 401 |
| `validation` | Body > 1 MiB → 413; malformed JSON → 422 |
| `rate-limiting-{prefix}` | Warmup to burst, then N+1 → 429 with `Retry-After` |

**Mandatory conventions:**
- Global prerequest: auto-increment `_req_seq`, derive unique `10.x.x.x` XFF IP.
- Warmup requests use empty/invalid bodies (rejected by validation, still consume rate-limit slot).
- Variable names prefixed with 2–4-char feature prefix (e.g. `echg_`, `del_`).
- Rate-limiting folder prerequest overrides `_xff` to `127.0.0.1`.
- `// NOT covered here (why)` comments for intentionally omitted cases.

---

## Done when

- [ ] Collection loads cleanly in Postman/Newman
- [ ] `make e2e-{feature}` target added to `make/e2e.mk`
- [ ] Target registered in `e2e-profile` or `e2e-auth`
- [ ] Human has run `make e2e-{feature}` — all tests pass
```

---

## Appendix C — Stage 8 (Docs) template

```markdown
# {Feature} — Stage 8: Docs

**Depends on:** Stage 7 (E2E) passing.
**Goal:** `.mdx` file(s) in `mint/api-reference/{domain}/{route}/`.

---

## Read first

| File | Why |
|---|---|
| `mint/api-reference/{domain}/{closest-existing}.mdx` | Format to follow exactly |
| `mint/docs.json` | Navigation tree — where to add the new page |
| `docs/prompts/{feature}/0-design.md §2, §3, §6` | HTTP contract, behaviour, rate limits |

---

## Deliverable — `mint/api-reference/{domain}/{route}/{endpoint}.mdx`

Required sections:
- `title`, `description`, `api` frontmatter
- `<ParamField>` blocks for every request field (with `<Expandable>` constraints)
- `## Behaviour` — what the endpoint does, what is NOT done, ordering, side effects
- `## Rate Limiting` — exact limit + KV strategy from §6
- `<RequestExample>` — realistic example body
- `<ResponseExample>` — one block per status code from §2
- `## Responses` — `<AccordionGroup>` + one `<Accordion>` per status code

---

## Done when

- [ ] Every new endpoint has a `.mdx` file
- [ ] `docs.json` references every new `.mdx` path
- [ ] All error codes from §2 present in Responses section
- [ ] Rate limit matches §6 exactly
- [ ] Human review passed
- [ ] Route marked `[x]` in `docs/map/INCOMING.md`
- [ ] `docs/rules/{domain}.md` updated with new endpoint + KV prefixes
```
