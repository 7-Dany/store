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
| `docs/map/backlog.md §{section}` | Required | **Target section only** — use head/tail or search; includes Go package path per section |
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
| `docs/rules/RULES.md §3.9` (SQL) + `§3.11` (naming) | Required |

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
| `docs/rules/RULES.md §3.3` (store shapes) + `§3.4` (error wrapping) | Required |
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
| `docs/rules/RULES.md §3.4, §3.6, §3.7` | Required |

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
| `docs/rules/RULES.md §3.10` (HTTP) + `§3.13` (checklist) | Required |

---

## Stage 5 — Audit

**Goal:** Multi-role audit of every file produced in Stages 1–4. Each pass is
conducted from a distinct reviewer's perspective so no class of issue escapes
through a gap between roles.

### Read before starting

Load `references/review.md` — it is the audit framework for all four passes.
Also read these feature-specific files:

| File | What to extract |
|---|---|
| `context/{feature}/context.md` | Resolved paths, decisions, KV prefixes, test case IDs |
| `context/{feature}/0-design.md §5` | Guard ordering — source of truth for every pass |
| `context/{feature}/0-design.md §7` | Full test-case inventory — used by Pass 4 |

Then read the implementation files:

| File | Passes that need it |
|---|---|
| `{feature}/handler.go` | 1, 2, 3, 6 |
| `{feature}/service.go` | 1, 2, 6 |
| `{feature}/store.go` | 2, 4, 6 |
| `{feature}/routes.go` | 2, 3, 5 |
| `{feature}/models.go` | 2, 4, 5 |
| `{feature}/requests.go` + `validators.go` | 2, 6 |
| `{feature}/errors.go` + `shared/errors.go` | 1, 2, 4 |
| `{domain}/routes.go` (domain assembler) | 3, 5 |
| `internal/db/{domain}.sql.go` — generated methods for this feature | 4 |
| `internal/audit/audit.go` — const + AllEvents() | 1, 3, 4 |
| `internal/platform/token/cookie.go` | 1, 3 |
| `internal/platform/token/jwt.go` | 1, 3 |
| `internal/platform/kvstore/store.go` | 3 |
| `internal/platform/respond/respond.go` | 3 |
| `internal/app/deps.go` | 5 |
| `docs/rules/RULES.md §3.10` + `§3.13` | 2, 3 |
| `{feature}/handler_test.go` + `service_test.go` + `store_test.go` | 6 |
| Analogous `handler.go` from another domain (head 60 lines) | 2 |

### Pass structure (6 passes, in order)

Each pass uses `references/review.md` as its checklist, applied through the
lens of that role. Passes are **independent** — do not collapse them.

---

**Pass 1 — Security Engineer**

Focus: auth flows, secrets, cookies, timing invariants, audit logging,
information leakage, PKCE/CSRF/OIDC correctness.

Run `references/review.md Part 2 §2.2` (Security) as the primary checklist.
Additionally verify against `0-design.md §5`:
- Every guard from the spec fires in the documented order.
- Every `context.WithoutCancel` call is present for writes marked in §5.
- Timing-invariant dummy calls are never bypassable by an early return.
- No audit event is emitted before the operation it records has succeeded.
- Error messages do not expose internal state beyond what §2 allows.

For RBAC-gated routes also check:
- `ApprovalGate` present only if permission can have `access_type = 'request'`.
- `ConditionalEscalator` nil-check present if `access_type = 'conditional'` is possible.

Finding format (from `references/review.md`):
```
SEVERITY    Critical | High | Medium | Low | Info
LOCATION    <file>:<function or line>
FINDING     <one-sentence description>
IMPACT      <what an attacker or system could do if unfixed>
FIX         <what to change and why>
```

---

**Pass 2 — Go Engineer**

Focus: correctness, idiomatic Go, guard ordering vs spec, error wrapping,
concurrency, dead code, interface satisfaction.

Run `references/review.md Part 1` (Rules Conformance) and `Part 2 §2.1,
§2.3, §2.4, §2.5, §2.6`.
Additionally verify against `0-design.md §5`:
- Produce a guard-ordering table per handler method: spec step vs actual code.
  Flag any deviation — even "harmless" reorderings.
- All `fmt.Errorf` uses `%w`, not `%v`.
- No sentinel defined in the wrong package.
- Default branch in every error switch logs via `slog.ErrorContext`.
- Every `go limiter.StartCleanup(ctx)` passes the application root `ctx`.

For RBAC-gated routes also check:
- `rbac.Perm*` constant used — never a raw string literal.
- `JWTAuth` comes before `RBAC.Require` in every `r.With(...)` chain.

Finding format: same as Pass 1.

After listing violations, add:
```
### Rule Contradictions or Ambiguities Found
<list per references/review.md format, or "None found.">
```

---

**Pass 3 — Platform Compliance Reviewer**

Focus: correct use of every `internal/platform/` abstraction; route shape;
no hand-rolled alternatives.

Run `references/review.md Part 3` (Platform Package Compliance) in full.
Produce the compliance table with Status filled for every row.
Additionally verify:
- Domain assembler returns `*chi.Mux`; feature `routes.go` has no return value.
- All audit event constants appear in `AllEvents()`.
- All KV prefix strings match `context.md` exactly — no typos.
- Rate-limit config matches `0-design.md §6` exactly.

For RBAC-gated routes also check:
- No IP rate limiter unless §6 explicitly specifies one.
- Route mounted under `/admin/` sub-router, not at `/api/v1/` root.

Finding format: same as Pass 1.

---

**Pass 4 — Data Layer Reviewer**

Focus: store implementation correctness, type boundaries, three-file syncs.
This pass does NOT re-evaluate SQL design (decided in Stage 0) — it only
verifies the implementation matches what was designed.

Verify against `context.md` and `0-design.md §4` (new SQL queries / audit events):
- `store.go` has the required `var _ Storer = (*Store)(nil)` compile-time check.
- `Store` embeds the correct `BaseStore` for this domain — no wrong-domain embedding.
- `WithQuerier` is present and follows the exact copy-and-replace pattern.
- Every store method signature matches the generated `internal/db/{domain}.sql.go` method exactly — no drift.
- No `pgtype.*` type appears in any public `Store` method signature or returned struct field.
- UUID conversions follow the canonical form: `pgtype.UUID → [16]byte` in store, never leaking out.
- Error wrapping prefix follows `"{feature}.{Method}: {step}: %w"` — check every `fmt.Errorf` in `store.go`.
- `isNoRows` / `isUniqueViolation` used where appropriate — not raw `pgx.ErrNoRows` comparisons.

**§S-1 Audit event triad** — for every new `EventXxx` in `0-design.md §4`:
- [ ] Constant declared in `internal/audit/audit.go` const block
- [ ] Constant present in `AllEvents()` return slice
- [ ] Corresponding row present in `audit_test.go` cases table

**§S-2 Querier / QuerierProxy triad** — for every new SQL query from `0-design.md §4`:
- [ ] Forwarding method added to domain testutil `querier_proxy.go`
- [ ] `Fail{MethodName} bool` field added to `QuerierProxy` struct
- [ ] Zero-value stub added to `querier_proxy_test.go` `nopQuerier`

Finding format: same as Pass 1.

---

**Pass 5 — Architecture & Import Reviewer**

Focus: import direction, layer contracts, type boundaries between layers,
wiring model, one-route-one-folder rule.

Read `references/architecture.md` before this pass.

- No domain package imports another domain package (ADR-010).
- `internal/db` imported only in store files — never in handler or service.
- `net/http` does not appear in `service.go` or `store.go`.
- `pgtype` / `pgxpool` / `pgx` do not appear in `handler.go` or `service.go`.
- `platform/token` not imported in `service.go` or `store.go`.
- `config.Config` not imported anywhere in the feature package.
- `app.Deps` only accessed in `routes.go` — not threaded into service or store.
- Handler only calls service via `Servicer` interface — never `*Service` directly.
- Service only calls store via `Storer` interface — never `*Store` directly.
- Feature sub-package `Routes` func has no return value.
- Domain root assembler `Routes` func returns `*chi.Mux`.
- All goroutines in `routes.go` propagate the passed `ctx` and respect `ctx.Done()`.

For RBAC domain:
- Feature routes mounted under the `adminRoutes` or `ownerRoutes` sub-router, not at root.

Finding format: same as Pass 1.

---

**Pass 6 — Test Coverage Reviewer**

Focus: every path through every file is tested; no T-NN case from Stage 0
§7 is missing.

Run `references/review.md Part 4` (Complete Test Checklist) for every
production file in the package.

Additionally produce the T-NN coverage table from `context.md §Test case IDs`:

```
| ID | Layer | Scenario | Present? |
|---|---|---|---|
| T-01 | S | ... | [x] / [❌] |
```

Mark `[x]` only if the sub-test exists in the test file. Mark `[❌]` if missing.
Do not mark `[x]` based on intent — only on actual code.

---

### Audit sign-off checklist

Before closing Stage 5, confirm:
- [ ] All Pass 1 Critical/High security findings resolved
- [ ] All Pass 2 guard-ordering deviations and rules violations corrected
- [ ] All Pass 3 platform compliance violations corrected
- [ ] All Pass 4 data layer findings resolved; §S-1 and §S-2 triads complete
- [ ] All Pass 5 import/architecture violations corrected
- [ ] All Pass 6 `[❌]` missing tests added to test files
- [ ] `go build ./{package}/...` passes
- [ ] `go vet ./{package}/...` passes
- [ ] `go test ./{package}/...` green

Once all items checked, proceed to Stage 6 (run unit tests manually).

---

## Stage 8 — Docs

**Goal:** `.mdx` files in `mint/` for every new or changed endpoint.

| File | Required / If needed |
|---|---|
| Closest existing `.mdx` in `mint/api-reference/{domain}/` | Required |
| `mint/docs.json` | Required — navigation tree |
| `.agents/skills/backend-route-impl/context/{feature}/0-design.md §2, §6` | Required — HTTP contract + rate limits |

---

## Stage 9 — Finalize

**Goal:** Update all project tracking and reference files to reflect the
completed feature. Run this after Stage 8 (docs) is reviewed and merged.
**Never skip — stale files break every future AI session for this codebase.**

This stage has no code deliverable. It is a structured update checklist.

### Project files (relative to project root)

| File | What to do |
|---|---|
| `docs/map/backlog.md §{section}` | Mark every route in this feature `[x]` |
| `docs/map/endpoints.md` | Add the new endpoint(s) under the correct domain section |
| `docs/map/project-map.md` | Add new package row under correct domain (§1); add new KV prefixes to §7 |
| `docs/rules/{domain}.md` | Add endpoint to feature table (§2.1); add new KV prefixes; add ADR if a non-obvious decision was made |
| `mint/api-reference/{domain}/` | Confirm `.mdx` committed and `mint/docs.json` updated |

### Skill files (relative to skill root)

| File | What to do |
|---|---|
| `context/{feature}/` | Delete the entire folder — scaffolding, not long-term docs |

### Verification checklist

- [ ] Every route from the feature is marked `[x]` in `backlog.md`
- [ ] `endpoints.md` has a row for each new endpoint
- [ ] `project-map.md §1` has the new package row
- [ ] `project-map.md §7` has every new KV prefix
- [ ] `docs/rules/{domain}.md` feature table is current
- [ ] `context/{feature}/` folder deleted
- [ ] No `context/{feature}/` path exists under the skill root

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
See [references/templates.md](templates.md) Appendix B for the template.

Proceed to Stage 8 when `make e2e-{feature}` passes.
