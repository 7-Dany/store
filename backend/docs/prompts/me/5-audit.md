# §E-1 — Linked Accounts — Stage 5: Audit

**Requirement source:** `docs/map/INCOMING.md §E-1`
**Target package:** `internal/domain/profile/me/`
**Depends on:** Stage 4 complete — `Handler.Identities` implemented, routes wired, H-layer tests green.

---

## Read first (no modifications)

| File | What to extract |
|---|---|
| `docs/prompts/me/context.md` | Decisions D-01–D-06, KV prefix `ident:ip:`, test IDs |
| `docs/prompts/me/0-design.md` | §5 guard ordering, §7 test inventory |
| `internal/domain/profile/me/handler.go` | `Identities` method, `mustUserID` helper |
| `internal/domain/profile/me/routes.go` | `identitiesLimiter` wiring, `StartCleanup` call, route registration |
| `internal/domain/profile/me/requests.go` | `identityItem`, `identitiesResponse` struct tags |
| `internal/domain/profile/me/handler_test.go` | `TestHandler_Identities` — existing test cases |
| `internal/domain/profile/me/service.go` | `Servicer` interface — `GetUserIdentities` signature |
| `internal/platform/respond/respond.go` | `JSON`, `Error`, `ClientIP`, `DecodeJSON`, `MaxBodyBytes` |
| `internal/platform/ratelimit/ratelimit.go` | `NewIPRateLimiter`, `StartCleanup` signature |
| `internal/platform/token/jwt.go` | `UserIDFromContext` |
| `docs/RULES.md` | §3.13 audit checklist + §3.9 (error handling) |

Produce **exactly four parts**, in order. No extra sections.

---

## Part 1 — Security Engineer

*Focus: sensitive field exclusion (D-03), rate-limit prefix and parameters (D-06),
no-audit-row rationale (D-05), error information leakage, client-disconnect safety.*

For each finding, produce one entry:

```
SEVERITY    Critical | High | Medium | Low | Info
LOCATION    <file>:<function or line>
FINDING     <one-sentence description>
IMPACT      <what an attacker or buggy client could do if unfixed>
FIX         <what to change and why>
```

Cover **every item** in this checklist — report ✓ (pass) or a finding for each:

### 1.1 Sensitive Field Exclusion (D-03)

- [ ] `access_token` is absent from all columns selected by `GetUserIdentities` SQL query
- [ ] `refresh_token_provider` is absent from all columns selected by `GetUserIdentities` SQL query
- [ ] `identityItem` struct in `requests.go` has no `AccessToken` or `RefreshTokenProvider` field
- [ ] `LinkedIdentity` model has no `AccessToken` or `RefreshTokenProvider` field
- [ ] Handler mapping loop (`for _, id := range identities`) copies no token field to `identityItem`

### 1.2 Rate Limiting (D-06)

- [ ] `identitiesLimiter` uses prefix `ident:ip:` exactly — no typo, no extra colon
- [ ] Burst cap is 20, matching INCOMING.md specification
- [ ] Rate is `20.0/(1*60)` tokens/sec (≈ 0.333) — not inverted
- [ ] `r.With(identitiesLimiter.Limit)` applied before `.Get("/me/identities", h.Identities)` — limiter cannot be bypassed
- [ ] `go identitiesLimiter.StartCleanup(ctx)` passes application root `ctx` — not `context.Background()`

### 1.3 Audit Row Omission (D-05)

- [ ] No `audit.Log` / audit write of any kind in `Identities` handler — read-only endpoint requires none
- [ ] No `context.WithoutCancel` usage in `Identities` — correct, because no security-critical write occurs

### 1.4 Error Information Leakage

- [ ] 500 response body is `"internal_error"` / `"internal server error"` — no DB detail, no stack trace
- [ ] `slog.ErrorContext` logs the real error server-side before responding 500
- [ ] No sentinel errors are defined or used for this endpoint (only middleware 401/429 and handler 500)

---

## Part 2 — Go Senior Engineer

*Focus: idiomatic Go, error handling discipline, guard ordering correctness,
concurrency and shutdown, interface satisfaction, import hygiene, code clarity.*

Use the same finding format as Part 1.

### 2.1 Error Handling

- [ ] No `fmt.Errorf` wrapping in this handler (single error path maps to 500 — no chain needed)
- [ ] No sentinel defined in `me/` package for this endpoint — correct, only `internal_error` applies
- [ ] Default / unexpected service error logs via `slog.ErrorContext` before `respond.Error`
- [ ] No accidental `==` comparison on error values anywhere in the `Identities` call path

### 2.2 Guard Ordering

Compare `Handler.Identities` line-by-line against `0-design.md §5`:

**Identities (§5 — 4 guard steps):**

- [ ] Step 1: `mustUserID(w, r)` — fires first; returns on `!ok`
- [ ] Step 2: `svc.GetUserIdentities(r.Context(), userID)` — called only after userID confirmed present; error → 500
- [ ] Step 3: mapping loop `make([]identityItem, 0, len(identities))` — executed only after successful service call
- [ ] Step 4: `respond.JSON(w, http.StatusOK, identitiesResponse{Identities: items})` — terminal response, nothing follows

### 2.3 Concurrency and Shutdown

- [ ] `go identitiesLimiter.StartCleanup(ctx)` passes the application root `ctx` — shutdown will stop the goroutine
- [ ] No other goroutines launched in `Identities` handler or its service/store call path
- [ ] No shared mutable state accessed in the handler method

### 2.4 Interface Satisfaction

- [ ] `Servicer` interface in `handler.go` includes `GetUserIdentities(ctx context.Context, userID string) ([]LinkedIdentity, error)`
- [ ] `*Service` satisfies `Servicer` — method signature matches exactly (no pointer/value mismatch)
- [ ] `MeFakeServicer` in `testutil/fake_servicer.go` has `GetUserIdentitiesFn` field and delegates correctly

### 2.5 Non-nil Empty Slice Guarantee

- [ ] `make([]identityItem, 0, len(identities))` — non-nil slice constructed even when `identities` is empty
- [ ] `respond.JSON` serialises this as `[]` — never `null` (satisfies D-01)
- [ ] Confirm `MeFakeServicer` default for `GetUserIdentitiesFn` returns `([]LinkedIdentity{}, nil)` — not a nil slice

### 2.6 Package and Import Hygiene

- [ ] No production file in `me/` imports a `testutil` or `_test` package
- [ ] `handler.go` imports only: `context`, `errors`, `log/slog`, `net/http`, `authshared`, `respond`, `token`
- [ ] No circular domain imports (profile/me does not import any other domain package)

### 2.7 Code Clarity and Idioms

- [ ] `Identities` method has canonical godoc comment (`// Identities handles GET /me/identities.`)
- [ ] No `TODO`, `FIXME`, or `HACK` comments without an issue reference
- [ ] Rate values expressed as `20.0/(1*60)` inline with a comment explaining the burst cap rationale

---

## Part 3 — Platform Compliance Reviewer

*Focus: correct and consistent use of `internal/platform/` abstractions.*

For each row, state **✓ Correct**, **✗ Violation**, or **N/A**.
For violations, add a finding entry using the Part 1 format.

| Concern | Required platform helper | Status |
|---|---|---|
| JSON success response | `respond.JSON` | |
| JSON error response | `respond.Error` | |
| Request body decode | `respond.DecodeJSON[T]` | N/A — GET, no body |
| Body size cap | `http.MaxBytesReader` + `respond.MaxBodyBytes` | N/A — GET, no body |
| Client IP extraction | `respond.ClientIP(r)` | N/A — no audit, no IP forwarding needed |
| User ID from context | `token.UserIDFromContext` (via `mustUserID`) | |
| IP rate limiting | `ratelimit.NewIPRateLimiter` | |
| Encryption at rest | N/A — `access_token` never read or returned | N/A |
| Refresh token cookie | N/A — read-only, no token issuance | N/A |
| Access token signing | N/A — read-only | N/A |

Additionally verify:

- [ ] `Routes` function signature is `func Routes(ctx context.Context, r chi.Router, deps *app.Deps)` — no return value
- [ ] `identitiesLimiter` is constructed with `deps.KVStore` — not a raw Redis/Postgres client
- [ ] KV prefix is exactly `ident:ip:` — matches `context.md` and passes prefix collision check
- [ ] No `AllowContentType` middleware on the GET route — correct, GET has no request body
- [ ] No new audit event constants introduced — `audit.go` is unchanged by this feature (D-05)
- [ ] Package-level godoc on `routes.go` mentions `GET /me/identities` and the `ident:ip:` rate limit

---

## Part 4 — Test Coverage Reviewer

*Focus: identify every tested and untested path in `Handler.Identities`.*

Cross-reference `TestHandler_Identities` in `handler_test.go` against `handler.go`.

```
### handler.go

#### Identities — unit tests (no build tag)
- [x/❌] T-07: Missing auth → 401
- [x/❌] T-02: Empty identities → "identities":[] (never null)
- [x/❌] T-04: access_token absent from response body
- [x/❌] T-05: refresh_token_provider absent from response body
- [x/❌] T-06: Nullable fields (provider_email, display_name, avatar_url) omitted when nil
- [x/❌] T-08: Service error → 500

Additional cases to check (full coverage beyond T-NN list):
- Identities: happy path ≥ 1 identity → 200 with correct provider, provider_email, display_name, created_at fields
- Identities: mapping loop with multiple identities preserves order returned by service

#### Structurally unreachable paths (no test stub needed)
- mustUserID: the "ok && userID == empty-string" branch is only reachable if
  token.UserIDFromContext returns (empty-string, true) — verify this case is
  exercised or note if the helper makes it structurally impossible.
```

Use `[x]` for cases already in `handler_test.go`.
Use `[❌]` for cases that are **missing and must be added** before Stage 6.

Also verify:

- [ ] `doDirectIdentities` helper correctly exercises the no-userID path (T-07) and authenticated paths
- [ ] `MeFakeServicer.GetUserIdentitiesFn` nil-safe — default (nil fn) returns `([]LinkedIdentity{}, nil)`
- [ ] No test imports a real DB or network connection — all `TestHandler_Identities` cases are pure unit tests

---

## Sync checklist before closing Stage 5

- [ ] All Part 1 Critical and High findings resolved
- [ ] All Part 2 guard-ordering deviations corrected
- [ ] All Part 3 platform violations corrected
- [ ] All Part 4 `[❌]` missing tests added to `handler_test.go`
- [ ] `go build ./internal/domain/profile/me/...` passes
- [ ] `go vet ./internal/domain/profile/me/...` clean
- [ ] `go test ./internal/domain/profile/me/...` green — all T-02, T-04–T-08 pass

Once all items are checked → proceed to Stage 6 (unit tests, manual) then Stage 7 (E2E).
