# Stages — Per-Stage File-Read Tables

Each table lists exactly what to read for that stage. **Required** = always
read. **If needed** = open only when an unknown arises. Never re-read files
already in context (E-4).

---

## Stage 0 — Design

**Goal:** Signed-off spec with HTTP contract, all decisions answered, and a
complete test-case inventory.

| File | Required / If needed | Note |
|---|---|---|
| `docs/map/INCOMING.md §{section}` | Required | **Target section only** — head/tail or search |
| Closest analogous package (`handler.go` + `service.go`) | Required | Use `project-map.md §1` to locate; files are small |
| `internal/audit/audit.go` | Required | `const` block only (first 80 lines) — event names |
| Domain SQL file (see table below) | Required | **Tail 60 lines** — existing query style at append point |
| `project-map.md §5` | Already loaded in Step 1 | kvstore / ratelimit / respond / token interfaces — **skip re-reading** |
| `project-map.md §2` | Already loaded in Step 1 | authshared errors + OTP functions — **skip re-reading** |
| `project-map.md §11` | Required for RBAC-gated routes | Permission constants + Checker API |

**Domain SQL file selection:**

| Domain | SQL file to read |
|---|---|
| `auth/` or `profile/` | `sql/queries/auth.sql` |
| `oauth/` | `sql/queries/oauth.sql` |
| `rbac/` | `sql/queries/rbac.sql` |

**Additional Stage 0 questions for RBAC-gated routes:**
- Which `rbac.Perm*` constant guards this route? (Use `project-map.md §11` — never raw strings.)
- Does this route need `ApprovalGate`? (Only if the permission can have `access_type = 'request'`.)
- Is there a `ConditionalEscalator` path? (Only if the permission can be `access_type = 'conditional'`.)

**Testutil package by domain:**

| Domain | Testutil import path |
|---|---|
| `auth/` or `profile/` | `auth/shared/testutil` (package `authsharedtest`) |
| `oauth/` | `oauth/shared/testutil` (package `oauthsharedtest`) |
| `rbac/` | `rbac/shared/testutil` (package `rbacsharedtest`) |

**Stage 0 must produce §7 (test case inventory).** See the derivation algorithm
at the bottom of this file.

---

## Stage 1 — Foundations

**Goal:** SQL queries, audit event constants, models, request/response types.

| File | Required / If needed |
|---|---|
| Domain SQL file (see Stage 0 table) | Required — **tail 60 lines** (append position; confirm section style) |
| `internal/audit/audit.go` | Required — full `const` + `AllEvents()` (needed to write the sync triad) |
| Analogous `models.go` + `requests.go` + `validators.go` + `errors.go` | Required — one analogous feature only; use `project-map.md §1` |
| `project-map.md §2` (authshared errors) | Already loaded — check here first before defining new sentinels (for rbac features also check §13 rbacshared errors) |
| `internal/db/rbac.sql.go` | Required for `rbac/` domain only — confirm generated method signatures |
| `docs/RULES.md §3.9` (SQL) + `§3.11` (naming) | Required |

---

## Stage 2 — Data layer

**Goal:** Store method(s), `Storer` interface, `FakeStorer`, `QuerierProxy` entries.

| File | Required / If needed |
|---|---|
| Domain `db/*.sql.go` file (see Stage 0 table) | Required — confirm generated method signatures from Stage 1 |
| `{feature}/service.go` | Required — `Storer` interface definition |
| Domain testutil `fake_storer.go` (see Stage 0 table) | Required — existing `FakeStorer` layout (add new entry) |
| Domain testutil `querier_proxy.go` (see Stage 0 table) | Required — existing `QuerierProxy` layout (add `Fail*` fields) |
| `project-map.md §3` (authsharedtest) | Already loaded — confirms exact FakeStorer/Proxy patterns (auth/profile domains only — for oauth see §12 testutil, for rbac see §13 testutil) |
| `docs/RULES.md §3.3` (store shapes) + `§3.4` (error wrapping) | Required |
| Analogous `store.go` | Required — one file; use `project-map.md §1` to pick the closest |

---

## Stage 3 — Logic layer

**Goal:** Service method(s), `Servicer` interface, `FakeServicer`, service unit tests.

| File | Required / If needed |
|---|---|
| `{feature}/service.go` | Required — constructor + `Storer` interface |
| `{feature}/handler.go` | Required — `Servicer` interface location |
| `{feature}/models.go` | Required |
| Domain testutil `fake_servicer.go` (see Stage 0 table) | Required — existing layout |
| `0-design.md §5` (guard ordering) + `§7` (S-layer test cases) | Required |
| `shared/otp.go` | If OTP used |
| `docs/RULES.md §3.4, §3.6, §3.7` | Required |

---

## Stage 4 — HTTP layer

**Goal:** Handler methods, routes wiring, handler unit tests, store integration tests.

| File | Required / If needed |
|---|---|
| `{feature}/handler.go` | Required |
| `{feature}/routes.go` | Required |
| `{feature}/requests.go` + `validators.go` | Required |
| `{feature}/handler_test.go` (if exists) | Required |
| Domain testutil `fake_servicer.go` (see Stage 0 table) | Required |
| `0-design.md §2` (HTTP contract) + `§7` (H+I test cases) | Required |
| `docs/RULES.md §3.10` (HTTP) + `§3.13` (checklist) | Required |

---

## Stage 5 — Audit

**Goal:** 4-part multi-role audit prompt. Each part maps to a distinct reviewer
role. See [templates/templates.md](../templates/templates.md) for the full template.

Read these files before writing — each maps to a specific reviewer role:

| File | Role | What to extract |
|---|---|---|
| `docs/prompts/{feature}/context.md` | All roles | Resolved paths, decisions, KV prefixes, test case IDs |
| `docs/prompts/{feature}/0-design.md` — §5 guard ordering + §7 test inventory | All roles | Guard order + every expected test case |
| `{feature}/handler.go` | Security + Go Engineer + Test Coverage | Guard ordering, error mapping, cookie logic |
| `{feature}/routes.go` | Go Engineer + Platform | Rate-limiter wiring, middleware order, route pattern |
| `{domain}/routes.go` (domain assembler) | Platform | Return type, AllowContentType, mount pattern |
| `{feature}/service.go` | Security + Go Engineer | `context.WithoutCancel` audit, error wrapping |
| `{feature}/models.go` | Go Engineer | Type correctness, KV state struct |
| `{feature}/errors.go` + `shared/errors.go` | Security + Go Engineer | Sentinel package ownership |
| `internal/audit/audit.go` — const + AllEvents() | Security + Platform | Verify all new events declared and exported |
| `internal/platform/token/cookie.go` | Security + Platform | `SetRefreshCookie` signature and flags |
| `internal/platform/token/jwt.go` | Security + Platform | `ParseAccessToken` vs `JWTSubjectExtractor` distinction |
| `internal/platform/kvstore/store.go` | Platform | Store interface methods used in handler |
| `internal/platform/respond/respond.go` | Platform | Canonical response helpers |
| `docs/RULES.md §3.10` + `§3.13` | Go Engineer + Platform | HTTP layer rules + test checklist |
| `{feature}/handler_test.go` | Test Coverage | Which T-NN cases present vs missing |
| Analogous `handler.go` from another domain | Go Engineer | Convention baseline (head 60 lines only) |

**Audit output structure (mandatory — 4 parts, in order):**

| Part | Role | Primary focus |
|---|---|---|
| 1 | Security Engineer | OAuth/OIDC, secrets, cookies, PKCE, CSRF, audit logs, info leakage |
| 2 | Go Senior Engineer | Error wrapping, guard ordering vs spec, concurrency, interface correctness |
| 3 | Platform Compliance Reviewer | Correct use of every `internal/platform/` abstraction; route shape |
| 4 | Test Coverage Reviewer | `[x]` / `[❌]` checklist of every T-NN case from Stage 0 §7 |

**Additional audit checks for RBAC-gated routes (add to Parts 2 and 3):**

| Check | Part |
|---|---|
| Permission constant used is from `rbac.Perm*` — no raw string literals | 2 |
| `deps.RBAC.Require(perm)` is chained **before** `deps.JWTAuth` only if route needs both — confirm ordering: `JWTAuth → Require` | 2 |
| `ApprovalGate` is present only on routes where permission can be `access_type = 'request'` | 2 |
| `ConditionalEscalator` nil-check present in handler if conditional path is possible | 2 |
| No IP rate limiter on pure admin routes — access control is RBAC only (unless spec explicitly adds one) | 3 |
| Route mounted under `/admin/` sub-router (not `/api/v1/` root) | 3 |

**How to fill each part:**
- Part 1 checklist — derive from feature's auth/security decisions in `context.md` D-xx entries and `0-design.md §5`.
- Part 2 guard tables — copy guard steps verbatim from `0-design.md §5`, one sub-section per handler method.
- Part 3 platform table — populate from what platform helpers the handler actually calls; mark N/A where not applicable.
- Part 4 T-NN table — copy every test case from `context.md §Test case IDs`; mark `[x]` only for cases already in `handler_test.go`; mark `[❌]` for all others.

---

## Stage 8 — Docs

**Goal:** `.mdx` files in `mint/` for every new or changed endpoint.

| File | Required / If needed |
|---|---|
| Closest existing `.mdx` in `mint/api-reference/{domain}/` | Required |
| `mint/docs.json` | Required — navigation tree |
| `0-design.md §2` (HTTP contract) + `§6` (rate limits) | Required |

---

## Test-case derivation algorithm (Stage 0 §7)

Derive mechanically from the guard ordering. Every guard that can fail → one
test case. Every timing dummy call → one test case. Every `context.WithoutCancel`
call → one test case.

### S-layer (service unit tests)

For each guard step that returns a sentinel error:
- One case per sentinel.
- One case asserting `ctx.Done() == nil` for every `context.WithoutCancel` call.
- One case counting invocations for every timing-invariant dummy call.
- One happy-path case (all guards pass → success result).

### H-layer (handler unit tests)

For every sentinel the service can return:
- One handler test case mapping sentinel → HTTP status + code string.

For every request field:
- One validation failure case (empty, wrong format, too long).

Always add:
- Missing auth → 401
- Malformed JSON → 422
- Body > 1 MiB → 413

For RBAC-gated routes, also add:
- Missing permission → 403 `forbidden` (inject `HasPermission=false` via `HasPermissionInContext` test hook)
- Owner bypass → 200 / expected status (inject `IsOwner=true`)

### I-layer (store integration tests)

For every store method that writes to DB:
- One test asserting DB state after the call.

For every store error path:
- One test using `QuerierProxy` `Fail*` flag.

**Rule:** A guard with no error sentinel (e.g. "extract user ID from JWT")
produces only an H-layer case (missing auth → 401), not an S-layer case.

---

## Stage 6 — Unit tests (manual, no AI output)

**Goal:** All unit tests green before E2E.

No AI output for this stage. Run:
```
go test ./internal/domain/{domain}/{route}/...
```
Fix any failures. Proceed to Stage 7 when `go test` is fully green.

---

## Stage 7 — E2E (manual trigger, AI produces collection)

**Goal:** Postman/Newman collection + `make e2e-{feature}` target.

AI produces the E2E collection prompt. The human runs it.
See [templates/templates.md](../templates/templates.md) Appendix B for the template.

Proceed to Stage 8 when `make e2e-{feature}` passes.
