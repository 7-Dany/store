# Bitcoin Review Playbook

> **What this file is:** A reusable review script for the Bitcoin backend.
> It is designed for Codex, Claude, or any other scoped reviewer to audit one
> bounded target at a time while still covering all relevant review dimensions:
> rules, architecture, correctness, performance, concurrency, comments, tests,
> and docs drift.
>
> **What this file is not:** It is not an implementation plan and not a fix list.
> Each pass is review-only unless the prompt explicitly switches to fix mode.
>
> **Primary baselines:** `docs/rules/RULES.md`, `docs/design/btc/context.md`, any
> package-specific feature or technical docs that exist, and the current code under
> `internal/platform/bitcoin/`, `internal/domain/bitcoin/`, `sql/schema/`, and
> `sql/queries/btc.sql`.

---

## Table of Contents

1. [Core Review Rules](#1-core-review-rules)
2. [Feature-Dev Rules To Inherit](#2-feature-dev-rules-to-inherit)
3. [Preflight](#3-preflight)
4. [Current Review Targets](#4-current-review-targets)
5. [Review Dimensions](#5-review-dimensions)
6. [Output Contract](#6-output-contract)
7. [Review Sequence](#7-review-sequence)
8. [Prompt Templates](#8-prompt-templates)
9. [Ready-to-Paste Review Prompts](#9-ready-to-paste-review-prompts)
10. [Fix Mode Prompts](#10-fix-mode-prompts)

---

## 1. Core Review Rules

Every Bitcoin review pass should follow these constraints:

- Review one bounded target at a time.
- Read the full target inventory before writing findings. Do not sample one or two files and generalize.
- Review production files, tests, routes, ports, and package-local helpers inside the target scope.
- Review first. Do not edit code unless the prompt explicitly allows edits.
- Compare against `docs/rules/RULES.md` and the relevant Bitcoin docs.
- If a package does not yet have dedicated Bitcoin docs, use `docs/design/btc/context.md`
  plus adjacent implemented packages as the comparison baseline.
- If package-specific docs are missing, call that out explicitly and decide whether the absence is itself a finding.
- Prefer findings over summaries.
- Focus on concrete defects, not style churn.
- If no findings exist, say so explicitly.
- Treat docs drift, missing tests, and rule violations as real findings when they
  create maintenance or correctness risk.
- Keep open questions separate from confirmed findings.

Use this base instruction at the end of every review prompt:

```text
Findings first, ordered by severity, with file references.
Focus on bugs, rule violations, performance risks, concurrency risks, comment quality, and missing tests.
Read the whole target first and keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

---

## 2. Feature-Dev Rules To Inherit

The shared reviewer should also apply the conventions from
`backend/.agents/skills/feature-dev/` when auditing Bitcoin code. These are not
separate from `RULES.md`; they are the practical review checks that the feature
workflow already expects.

### 2.1 Process and reading discipline

From `SKILL.md`:

- Read files directly. Never guess at contents.
- Read leanly, but do not sample so narrowly that package-level findings become guesses.
- Read one strong analogue instead of many weak analogues when comparing package structure.
- Work bottom-up when evaluating implementation flow: SQL, then store, then service, then handler, then routes.
- Treat Stage 0 design and resolved context as authoritative when they exist.

### 2.2 Architecture and layer rules

From `references/architecture.md`:

- `routes.go` is the only dependency-construction site.
- Every domain has a root `routes.go` and a `shared/` package.
- Feature packages import their domain `shared/` package, never sibling features.
- `db` is store-only.
- `app` is routes-only.
- `platform` packages never import domain packages.
- Handler, service, and store boundaries are strict:
  - handler must not import DB or pgx types
  - service must not import `net/http`, pgx, or config
  - store must not leak `pgtype.*`, `db.*`, or `pgx.*` through public methods
- Type boundaries are explicit:
  - store internal: `pgtype.UUID`
  - store/service: `[16]byte`
  - service/handler: `string`

### 2.3 File and naming conventions

From `references/conventions.md`:

- Required feature files: `handler.go`, `service.go`, `store.go`, `routes.go`, `models.go`.
- Conditional files only when needed: `requests.go`, `errors.go`, `validators.go`.
- Banned file names: `helpers.go`, `utils.go`, `common.go`.
- `Store` shape should follow the repo pattern, including compile-time check, embedded base store, `NewStore`, and `WithQuerier`.
- Naming should stay consistent:
  - `{Operation}Input`, `{Operation}Result`
  - `Err{Condition}`
  - `{Action}Tx`
  - `Get{Thing}` / `List{Things}`

### 2.4 Platform and HTTP conventions

Also from `references/conventions.md` and `references/review.md`:

- No raw SQL in Go, including tests.
- Handlers that read JSON bodies must cap body size with `http.MaxBytesReader`.
- Use `respond.DecodeJSON`, `respond.JSON`, `respond.Error`, and `respond.NoContent`.
- Use `respond.ClientIP` instead of hand-rolled IP extraction.
- Use `platform/token` helpers instead of direct JWT handling.
- Use `platform/ratelimit` and `platform/kvstore` instead of ad hoc equivalents.
- Review URL paths for noun-based resource naming and banned hyphenated verb-style paths.

### 2.5 Telemetry and observability conventions

From `references/conventions.md` and `references/review.md`:

- Domain packages should use package-level `telemetry.New("{feature}")` loggers.
- Error wrapping should use `telemetry.Store`, `telemetry.Service`, and other layer constructors, not `fmt.Errorf`.
- Domain handlers should use `log.Error(...)`, not bare `slog.ErrorContext`.
- If a feature introduces a real business metric, the review should check the full telemetry chain:
  - domain recorder contract
  - telemetry hook
  - metrics registration
  - frontend telemetry integration when needed

### 2.6 Security and correctness conventions

From `references/conventions.md` and `references/review.md`:

- Review for missing `context.WithoutCancel` on security-critical writes.
- Review anti-enumeration timing invariants.
- Review cookie attribute correctness where cookies are issued.
- Review audit event constant usage; no inline string event names in stores.
- Review transaction sequencing and rollback behavior explicitly.

### 2.7 Testing and artifact conventions

From `references/conventions.md`:

- Domain fakes belong in `shared/testutil/`, not feature-local `testutil/`.
- Expected test layout:
  - `handler_test.go`
  - `service_test.go`
  - `store_test.go` with `//go:build integration_test`
- `WithQuerier` support is required for integration-testable stores.
- No raw SQL in test Go files.
- Artifact hygiene matters: backup files, duplicate test files, or mixed test responsibilities are review findings.

### 2.8 Comment and review conventions

From `references/comments.md` and `references/review.md`:

- Package comments and exported identifier comments are mandatory where required.
- Inline comments should explain why, not what.
- Security-sensitive lines should carry `// Security:` comments.
- Timing invariants should be documented at both method and call-site level when applicable.
- Multi-step transaction methods should use numbered step comments.
- Reviews should call out rule contradictions or stale rule text when the observed repo pattern differs across multiple packages.

These inherited checks should be treated as part of the Bitcoin review baseline,
not as optional add-ons.

---

## 3. Preflight

Before each review pass, do this discovery step first:

1. List every file in the target scope.
2. Identify which docs actually exist for that target.
3. Identify neighboring packages or stronger reference packages to compare against.
4. Note any obvious scope hazards before reviewing:
   - stray generated files
   - duplicated test files
   - backup files
   - missing package docs
   - mixed production and test responsibilities

Use this preflight prompt fragment when needed:

```text
Before listing findings, enumerate the files in scope and identify which relevant docs actually exist.
Review the full target, including tests and routes, not just the main service files.
If docs are missing for the target package, say that explicitly and use backend/docs/design/btc/context.md plus neighboring implemented packages as the fallback baseline.
```

---

## 4. Current Review Targets

This is the current Bitcoin implementation inventory in this repo. Use it to keep
the review bounded and grounded in the code that actually exists.

### 4.1 Platform packages

| Target | Path | Notes |
|---|---|---|
| RPC | `backend/internal/platform/bitcoin/rpc` | `client.go`, `types.go`, `recorder.go`, `client_test.go` |
| ZMQ | `backend/internal/platform/bitcoin/zmq` | transport, runtime, subscriber, events, and tests |

### 4.2 Domain packages

| Target | Path | Notes |
|---|---|---|
| Root domain router | `backend/internal/domain/bitcoin/routes.go` | review wiring and mount ownership separately when needed |
| Shared | `backend/internal/domain/bitcoin/shared` | includes `testutil/` and validators |
| Watch | `backend/internal/domain/bitcoin/watch` | handler, service, store, routes, tests |
| Events | `backend/internal/domain/bitcoin/events` | handler, service, store, broker, mempool, tracking, tests |
| Txstatus | `backend/internal/domain/bitcoin/txstatus` | handler, service, store, routes, tests |
| Block | `backend/internal/domain/bitcoin/block` | handler, service, routes, ports, tests |

### 4.3 Current doc coverage

Currently present under `backend/docs/design/btc/`:

- `context.md`
- package docs for `audit`, `compliance`, `dispute`, `invoice`, `kyc`, `payment`,
  `rate`, `reconciliation`, `resilience`, `settlement`, `sweep`, `vendor`,
  `wallet-backup`, and `webhook`

Currently not present as dedicated package docs:

- `watch`
- `events`
- `txstatus`
- `block`
- platform package docs for `rpc` and `zmq`

Implication for review:

- Missing docs should not block the review.
- Missing docs may still be a finding if the package is implemented enough that
  undocumented behavior creates drift or maintenance risk.

---

## 5. Review Dimensions

Each scoped pass should cover the full set of dimensions below. The exact weight
changes by target, but the checklist stays fixed so no review turns into a vague
"look around" pass.

### 5.1 Architecture and rules

- Package layout follows `RULES.md`.
- Correct handler/service/store/routes separation.
- Imports point in the allowed direction only.
- Domain packages do not grow cross-domain coupling.
- Files match repo responsibilities; no `helpers.go` or mixed-layer leakage.

### 5.2 API and type boundaries

- Handler/request types stay at the HTTP boundary.
- Service models stay plain Go types.
- Store owns DB and driver-specific conversions.
- Interface shape is minimal but sufficient for the package.
- Endpoint naming, status mapping, and response shape are consistent.

### 5.3 Correctness and state transitions

- Business rules match the package feature/technical docs.
- Status transitions are valid, explicit, and `RowsAffected` sensitive where required.
- Fallback logic is correct and does not mask stale or contradictory state.
- Idempotency, deduplication, and partial-failure handling are explicit.

### 5.4 Performance and hot paths

- No avoidable allocations or repeated heavy work in hot paths.
- DB queries have sane ownership and index support.
- Polling, reconnect, and reload loops have bounded work.
- RPC and ZMQ paths do not introduce avoidable latency or repeated I/O.

### 5.5 Concurrency and shutdown

- Context use is correct.
- Goroutine ownership is clear.
- Shutdown paths do not leak workers or hang.
- Reconnect and recovery paths do not race with steady-state processing.
- Shared in-memory state is synchronized correctly.

### 5.6 Error handling and observability

- Sentinel errors are used consistently.
- RPC and DB errors are not silently collapsed into the wrong behavior.
- Logging and recorder behavior are coherent and non-duplicative.
- Failure paths preserve enough context for operators.

### 5.7 Comments and docs

- Package comments and exported comments meet `RULES.md`.
- Inline comments explain why, not what.
- Docs match the implementation and do not promise behavior that code does not enforce.
- Docs do not leak internal details that should remain implementation-private.

### 5.8 Tests

- Happy path and failure path coverage exist.
- Concurrency, reconnect, recovery, and shutdown cases are tested where relevant.
- Fallback paths are tested, not just the primary path.
- Missing tests are called out specifically, not as a generic complaint.

### 5.9 Artifact hygiene

- No stray temporary or backup files sit inside production packages.
- Export-test helpers are justified and scoped.
- Test layout still matches repo conventions.
- Review prompts should call out files like `*.go.*` when they exist in package directories.

---

## 6. Output Contract

Use this review output shape for every pass:

1. Findings only, highest severity first.
2. Each finding should include:
   - severity (`P0` to `P3`)
   - file reference
   - line or symbol when possible
   - concrete issue
   - why it matters
   - what test is missing if the gap is test-related
3. After findings, optionally list:
   - open questions
   - assumptions
   - baseline gaps, such as missing docs
   - a short change summary only if needed

Evidence rules:

- Do not report a finding unless the code or docs in scope support it.
- If behavior is uncertain, put it under open questions, not findings.
- For package-level issues, cite at least one concrete file or flow in the package.
- Prefer exact file references over package-only references.

Recommended severity meanings:

- `P0`: fund loss, broken accounting, invalid security or safety boundary
- `P1`: likely correctness bug, invalid state transition, broken recovery path
- `P2`: meaningful performance, maintainability, or test gap with operational risk
- `P3`: lower-risk rule, comment, or consistency issue that should still be fixed

---

## 7. Review Sequence

Run the audit in this order so foundational issues are caught before higher-layer
package reviews depend on them.

1. Schema and query ownership
2. Platform RPC
3. Platform ZMQ
4. Domain root router and shared contracts
5. `watch`
6. `events`
7. `txstatus`
8. `block`
9. Artifact hygiene and tests as a dedicated pass
10. Docs drift as a dedicated pass

Why this order:

- SQL and query ownership shape every store and service review.
- `rpc` and `zmq` are platform boundaries used by the domain packages.
- Domain root wiring and shared contracts influence every feature package.
- `watch`, `events`, `txstatus`, and `block` should be reviewed after the lower
  layers they rely on.
- Tests and docs deserve explicit passes because they are easy to under-review if
  mixed into a broad code pass.

---

## 8. Prompt Templates

### 8.1 Universal scoped review template

```text
Review only {target_path}.
Compare it against backend/docs/rules/RULES.md, backend/docs/design/btc/context.md, and any directly relevant Bitcoin design docs.
Focus on {focus_areas}.
Before listing findings, enumerate the files in scope and identify which relevant docs actually exist.
Review the full target, including tests and routes, not just the main service files.
Check architecture, type boundaries, correctness, performance, concurrency, comments, tests, and docs drift.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 8.2 Schema and query template

```text
Review only backend/sql/schema and backend/sql/queries/btc.sql.
Compare them against backend/docs/rules/RULES.md and backend/docs/design/btc/context.md.
Before listing findings, enumerate the files in scope and identify the BTC migrations and query sections actually used by implemented packages.
Focus on constraints, indexes, migration safety, query ownership, state-transition safety, duplicate or conflicting queries, and missing query-level test coverage.
Call out any rule violations, bad constraints, bad indexes, ownership leaks, or query/API mismatches.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 8.3 Platform package template

```text
Review only {platform_package_path}.
Compare it against backend/docs/rules/RULES.md, backend/docs/design/btc/context.md, and the relevant Bitcoin technical docs.
Before listing findings, enumerate the files in scope and note whether dedicated package docs exist.
Focus on interface shape, boundary ownership, retry/reconnect behavior, shutdown correctness, error mapping, hot-path performance, comments, and missing tests.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 8.4 Domain package template

```text
Review only {domain_package_path}.
Compare it against backend/docs/rules/RULES.md, backend/docs/design/btc/context.md, and any package feature/technical docs that exist.
Before listing findings, enumerate the files in scope and note whether dedicated package docs exist.
Review the package's handler, service, store, routes, models, requests/responses, ports, and tests when present.
Focus on handler/service/store/routes separation, endpoint design, fallback logic, status transitions, persistence boundaries, comments, performance, concurrency, and missing tests.
Call out any divergence from stronger mature domain packages in this repo.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 8.5 Docs-only template

```text
Review only the Bitcoin docs for {package_or_scope}.
Compare the docs against the current implementation and backend/docs/rules/RULES.md.
Focus on behavioral drift, technical drift, undocumented constraints, wrong guarantees, and internal implementation details that should not be documented.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

---

## 9. Ready-to-Paste Review Prompts

These are the concrete prompts to run one by one.

### 9.1 Schema and queries

```text
Review only backend/sql/schema and backend/sql/queries/btc.sql.
Compare them against backend/docs/rules/RULES.md and backend/docs/design/btc/context.md.
Before listing findings, enumerate the files in scope and identify the BTC migrations and query sections actually used by implemented packages.
Focus on constraints, indexes, migration safety, query ownership, state-transition safety, duplicate or conflicting queries, and missing query-level test coverage.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 9.2 RPC

```text
Review only backend/internal/platform/bitcoin/rpc.
Compare it against backend/docs/rules/RULES.md, backend/docs/design/btc/context.md, and the relevant Bitcoin docs.
Before listing findings, enumerate the files in scope and note that dedicated package docs may not exist yet.
Focus on interface shape, wallet-only assumptions, error mapping, timeout behavior, retry policy, comment quality, and missing tests.
Check architecture, type boundaries, correctness, performance, concurrency, comments, tests, and docs drift.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 9.3 ZMQ

```text
Review only backend/internal/platform/bitcoin/zmq.
Compare it against backend/docs/rules/RULES.md, backend/docs/design/btc/context.md, and the relevant Bitcoin docs.
Before listing findings, enumerate the files in scope and note that dedicated package docs may not exist yet.
Focus on hot-path performance, reconnect behavior, sequence-gap handling, shutdown correctness, goroutine ownership, recorder behavior, and missing tests.
Check architecture, type boundaries, correctness, performance, concurrency, comments, tests, and docs drift.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 9.4 Domain root router and shared contracts

```text
Review only backend/internal/domain/bitcoin/routes.go and backend/internal/domain/bitcoin/shared.
Compare them against backend/docs/rules/RULES.md and backend/docs/design/btc/context.md.
Before listing findings, enumerate the files in scope and note whether dedicated package docs exist.
Focus on root wiring ownership, shared contract quality, sentinel error ownership, validator ownership, testutil boundaries, cross-package leakage, comment quality, and missing tests.
Check architecture, type boundaries, correctness, performance, concurrency, comments, tests, and docs drift.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 9.5 Watch

```text
Review only backend/internal/domain/bitcoin/watch.
Compare it against backend/docs/rules/RULES.md, backend/docs/design/btc/context.md, and any package docs that exist.
Before listing findings, enumerate the files in scope and note whether dedicated package docs exist.
Focus on handler/service/store/routes separation, watch registration rules, Redis ownership, input validation, endpoint design, comments, and missing tests.
Call out any divergence from stronger mature domain packages in this repo.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 9.6 Events

```text
Review only backend/internal/domain/bitcoin/events.
Compare it against backend/docs/rules/RULES.md, backend/docs/design/btc/context.md, and any package docs that exist.
Before listing findings, enumerate the files in scope and note whether dedicated package docs exist.
Focus on handler/service/store/routes separation, SSE lifecycle, persistence boundaries, fan-out correctness, backpressure risks, comment quality, performance, and missing tests.
Call out any divergence from stronger mature domain packages in this repo.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 9.7 Txstatus

```text
Review only backend/internal/domain/bitcoin/txstatus.
Compare it against backend/docs/rules/RULES.md, backend/docs/design/btc/context.md, and any package docs that exist.
Before listing findings, enumerate the files in scope and note whether dedicated package docs exist.
Focus on consistency, fallback logic, endpoint design, persisted read-model ownership, comment quality, performance, and missing tests.
Call out artifact hygiene issues too, including temporary or backup files inside the package directory.
Call out any divergence from stronger mature domain packages in this repo.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 9.8 Block

```text
Review only backend/internal/domain/bitcoin/block.
Compare it against backend/docs/rules/RULES.md, backend/docs/design/btc/context.md, and any package docs that exist.
Before listing findings, enumerate the files in scope and note whether dedicated package docs exist.
Focus on package layout, endpoint design, fallback logic, RPC/store separation, comments, performance, and missing tests.
Call out any divergence from stronger mature domain packages in this repo.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 9.9 Artifact hygiene and tests-only pass

```text
Review only the Bitcoin test files under backend/internal/platform/bitcoin and backend/internal/domain/bitcoin.
Compare them against backend/docs/rules/RULES.md, backend/docs/design/btc/context.md, and the Bitcoin package docs.
Before listing findings, enumerate the files in scope and call out any stray backup or generated files inside package directories.
Focus on missing coverage, weak assertions, missing race/reconnect/recovery/shutdown cases, duplicated helpers, artifact hygiene, and divergence from repo test layout conventions.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

### 9.10 Docs drift pass

```text
Review only backend/docs/design/btc and any Bitcoin README or Mint docs that describe the implementation.
Compare the docs against the current code in backend/internal/platform/bitcoin, backend/internal/domain/bitcoin, backend/sql/schema, and backend/sql/queries/btc.sql.
Focus on drift from implementation, missing documented constraints, guarantees the code does not enforce, and internal details that should not be documented.
Findings first, ordered by severity, with file references.
Keep open questions separate from confirmed findings.
If no findings, say that explicitly.
Do not change code yet.
```

---

## 10. Fix Mode Prompts

Only use these after a review pass has produced findings you agree with.

### 10.1 Package fix prompt

```text
Fix only the confirmed findings in {package_path}.
Keep package structure aligned with backend/docs/rules/RULES.md.
Do not broaden scope beyond the confirmed findings.
Keep tests consolidated according to repo conventions.
Sync docs and tests after code changes.
```

### 10.2 Docs fix prompt

```text
Fix the confirmed Bitcoin docs drift findings only.
Update the relevant backend/docs/design/btc files and any affected README or Mint docs.
Do not add speculative design text; make the docs match the current implementation and enforced rules.
```

### 10.3 Review-after-fix prompt

```text
Re-review only the files changed by the fix pass.
Focus on regressions, incomplete fixes, rule violations, and test/doc gaps introduced by the patch.
Findings first, ordered by severity, with file references.
If no findings, say that explicitly.
Do not make further edits yet.
```
