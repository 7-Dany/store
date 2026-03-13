# Package Review Prompt

Use this prompt to get a structured 4-part review of any Go package in this
codebase. Paste it, then replace `{PACKAGE_PATH}` with the actual path.

---

## Instructions for the reviewer

You are performing a deep review of the Go package at:

```
{PACKAGE_PATH}
```

**Before writing anything, read these files in order:**

1. `docs/RULES.md` — the authoritative source for every naming, layering,
   import, testing, and commenting convention in this codebase.
2. Every `.go` file in `{PACKAGE_PATH}` (production and test files).
3. Domain `shared/errors.go` — shared sentinel errors (path depends on domain; see table below).
4. Domain `shared/store.go` — BaseStore helpers.
5. Domain `shared/testutil/fake_storer.go` — FakeStorer catalogue.
6. Domain `shared/testutil/querier_proxy.go` — QuerierProxy catalogue.
7. Domain `shared/testutil/builders.go` — test helpers.

**Domain shared paths:**

| Package path prefix | Shared path | Testutil package name |
|---|---|---|
| `internal/domain/auth/{feature}/` | `internal/domain/auth/shared/` | `authsharedtest` |
| `internal/domain/profile/{feature}/` | `internal/domain/auth/shared/` (auth shared — profile has no own testutil) | `authsharedtest` |
| `internal/domain/oauth/{provider}/` | `internal/domain/oauth/shared/` | `oauthsharedtest` |
| `internal/domain/rbac/{feature}/` | `internal/domain/rbac/shared/` | `rbacsharedtest` |

Produce **exactly four parts**, in order, with no extra sections.

---

## Part 1 — Rules Conformance

Check every file against RULES.md. For each violation or ambiguity, produce one
entry in this format:

```
FILE        handler.go
RULE        RULES.md §3.10
SEVERITY    Error | Warning | Info
FINDING     <one-sentence description>
EVIDENCE    <quoted line(s) from the source file>
FIX         <what to change and why>
```

Severity levels:
- **Error** — breaks a hard rule; code review must reject it.
- **Warning** — violates a soft convention; should be fixed before merge.
- **Info** — style note; acceptable to defer.

After listing violations, add a subsection:

### Rule Contradictions or Ambiguities Found

If any rule in RULES.md contradicts another rule, contradicts the existing
code across *multiple* packages (suggesting the rule is stale), or is
underspecified, list each contradiction here so the rules can be refined.
Format each as:

```
RULE A      §X.Y <quote>
RULE B / CODE   §X.Z <quote> — OR — existing pattern in {other package}
CONFLICT    <what is contradictory>
RECOMMENDATION  <proposed resolution>
```

If no contradictions are found, write `None found.`

---

## Part 2 — Logic, Flow, and Code Quality

Review the package for correctness and quality issues **independent of rules**.
Cover every item in this checklist and report findings:

### 2.1 Bugs and Correctness
- Off-by-one errors, wrong comparisons, nil dereferences.
- Incorrect error wrapping (`%v` where `%w` is needed, swallowed errors).
- Guard ordering errors (wrong priority between sentinel returns).
- Transaction correctness: all error paths rollback; commit only after all steps succeed.
- Missing `context.WithoutCancel` on security-critical writes.

### 2.2 Security
- Anti-enumeration timing invariants: dummy hash/OTP runs on no-rows path.
- Cookie attributes: `HttpOnly`, `SameSite=Strict`, `Secure` driven by config.
- Audit log written for every failed authentication event.
- Client-disconnect can abort security writes (missing `WithoutCancel`).
- Information leakage in error messages or HTTP response bodies.

### 2.3 Performance
- Crypto (bcrypt, HMAC) inside a database transaction.
- N+1 query patterns.
- Unnecessary allocations inside hot paths.

### 2.4 Dead Code and Unreachable Paths
- Exported identifiers (types, methods, functions) unreachable from any callsite
  given the import rules (ADR-010: domain packages never import each other).
- Unexported helpers that are never called.
- Branches that can never be reached given the type system or invariants.
  These should carry an `// Unreachable:` comment in the source (§3.8).

### 2.5 Race Conditions and Concurrency
- Shared mutable state accessed from goroutines without synchronisation.
- Goroutines that ignore `ctx.Done()` (shutdown bug per §2.6).
- `Pool.Begin` vs `BeginOrBind` misuse (ADR-003 requirement for independent commits).

### 2.6 Redundancy and Clarity
- Double `context.WithoutCancel` wrapping (redundant but harmless — note it).
- Duplicate logic already covered by a platform package (flag for Part 3).

Report each issue as:

```
SEVERITY    Critical | High | Medium | Low | Info
LOCATION    <file>:<function or line>
FINDING     <description>
IMPACT      <what goes wrong if unfixed>
FIX         <proposed change>
```

---

## Part 3 — Platform Package Compliance

`internal/platform/` packages exist to centralise cross-cutting concerns.
Using them everywhere is what keeps those concerns consistent (RULES.md §3.10).

For each concern below, state whether the package uses the correct platform
abstraction or hand-rolls an alternative:

| Concern | Required | Status |
|---|---|---|
| JSON success response | `respond.JSON` | |
| JSON error response | `respond.Error` | |
| 204 No Content | `respond.NoContent` | |
| Request body decode | `respond.DecodeJSON[T]` | |
| Client IP extraction | `respond.ClientIP` | |
| Body size cap | `respond.MaxBodyBytes` with `http.MaxBytesReader` | |
| JWT signing / parsing | `platform/token` helpers | |
| Cookie setting | `token.MintTokens` (not hand-rolled `http.SetCookie`) | |
| Rate limiting | `platform/ratelimit` limiters | |
| Key-value / blocklist | `platform/kvstore` | |
| Email delivery | `platform/mailer` | |
| RBAC permission check | `deps.RBAC.Require(rbac.Perm*)` — never raw `db.CheckUserAccess` call | |
| RBAC approval gate | `deps.RBAC.ApprovalGate(deps.ApprovalSubmitter)` when `access_type=request` possible | |
| RBAC permission constant | `rbac.Perm*` constant — never a raw string literal | |

**RBAC-specific checks (apply only to `internal/domain/rbac/` packages):**
- `deps.JWTAuth` must come **before** `deps.RBAC.Require(...)` in every `r.With(...)` chain.
- `ApprovalGate` must only be present on routes where the permission can realistically have `access_type = 'request'`.
- `ConditionalEscalator` nil-check must be present in handler code if the `conditional` access path is possible.
- No IP rate limiter on pure admin routes unless the design doc explicitly specifies one.
- Routes must be mounted under `/admin/` sub-router (not at the `/api/v1/` root).

For any cell marked as a violation, provide a finding entry in the same format
as Part 2.

---

## Part 4 — Complete Test Checklist

For **every source file** in the package (excluding `routes.go` and
`export_test.go`), produce a checklist of every testable path.

Structure:

```
### {filename}.go

#### Unit tests  (no build tag, FakeStorer / FakeServicer)
- [ ] {function}: {scenario} → expected outcome

#### Integration tests  (//go:build integration_test)
- [ ] {function}: {scenario} → expected outcome

#### Structurally unreachable (document in source with // Unreachable:, no test stub)
- {function}: {branch} — reason it cannot be reached
```

Rules for this checklist:
- List **every path through every function**, including happy paths, error paths,
  boundary values, guard-ordering interactions, and timing invariants.
- Mark `[x]` for cases that already exist in the current test files.
- Mark `[ ]` for cases that are missing and must be added.
- For integration tests, specify whether `txStores(t)` or `commitUser(t)` is
  needed (per ADR-003: use `commitUser` only when independent commit is required).
- Do not list tests for `// Unreachable:` branches — explain why instead.
- Apply all conventions from RULES.md §3.8 and §3.13 test checklist to every
  generated test name (suffix `_Integration`, `t.Parallel()`, no raw SQL, etc.).

### Avoiding Redundant Tests: What Shared Packages Already Guarantee

Before adding a test in any **feature sub-package**, check whether the behaviour
under test is already enforced by a function in that domain's `shared/` package.
Redundant tests slow the suite and create false confidence.

---

#### `authshared` — `internal/domain/auth/shared/`

Applies to: `auth/{feature}/`, `profile/{feature}/`

**Do NOT re-test these — fully covered in `authshared`:**

| Shared function | What is already tested |
|---|---|
| `NormaliseEmail` | blank/whitespace → `ErrEmailEmpty`; >254 bytes → `ErrEmailTooLong`; missing `@`, empty local/domain, no TLD dot → `ErrEmailInvalid`; trims + lowercases. |
| `ValidatePassword` | empty → `ErrPasswordEmpty`; <8 → `ErrPasswordTooShort`; >72 → `ErrPasswordTooLong`; missing upper/lower/digit/symbol (including space-not-a-symbol edge case) → respective sentinels. |
| `ValidateOTPCode` | empty → `ErrCodeEmpty`; wrong length or non-digits → `ErrCodeInvalidFormat`; all valid 6-digit forms pass. |
| `ParseUserID` | invalid/empty UUID string → wrapped error; valid UUID → `[16]byte` roundtrip. |
| `CheckOTPToken` | expiry-before-attempts guard order; attempts-before-code guard order; exact-at-expiry boundary; `Attempts >= MaxAttempts` → `ErrTooManyAttempts`; wrong code → `ErrInvalidCode`; correct code → nil. |
| `ConsumeOTPToken` | `ErrTokenNotFound` → dummy hash runs + sentinel returned; `ErrTokenExpired` → returned as-is; `ErrTooManyAttempts` → returned as-is; `ErrInvalidCode` (below max) → `incrementFn` called; `ErrInvalidCode` (at max) → `incrementFn` skipped; `incrementFn` receives `WithoutCancel` context; `incrementFn` error does not change return value. |
| `HashPassword` / `CheckPassword` | hash uniqueness; empty/too-short/too-long cost guards; wrong-password → `ErrInvalidCredentials`. |
| `VerifyCodeHash` | non-6-byte codes → false; correct/wrong 6-byte codes. |
| `IsPasswordStrengthError` | true for all 7 strength sentinels; false for non-strength errors. |
| `ErrTokenAlreadyConsumed` | same pointer as `ErrTokenAlreadyUsed` (`errors.Is` both ways). |
| `ErrIdentifierTooLong` | used by `login/validators.go` to guard the identifier byte length; do not remove or re-declare it in the login package. |

---

#### `oauthshared` — `internal/domain/oauth/shared/`

Applies to: `oauth/google/`, `oauth/telegram/`

`oauthshared` is a thin struct-only package (`LoggedInSession`, `LinkedIdentity`).
There are no shared business-logic functions to avoid re-testing here.

**What to test in OAuth feature packages:**
- Provider-specific HMAC / OIDC token verification logic.
- Store methods: identity upsert, unlink, OAuth session creation.
- Handler: correct response shape, cookie issuance, redirect URL construction.
- Error mapping: provider errors → correct HTTP status + code string.

---

#### `rbacshared` — `internal/domain/rbac/shared/`

Applies to: `rbac/bootstrap/`, `rbac/permissions/`, `rbac/roles/`, `rbac/userroles/`

`rbacshared` exposes only `ErrUserNotFound`. There are no shared validation or
crypto functions to avoid re-testing here.

**What to test in RBAC feature packages:**
- Permission constant routing: correct `rbac.Perm*` constant used per route.
- Handler RBAC guard: missing permission → 403 (use `HasPermissionInContext` test hook to inject).
- Handler owner bypass: `IsOwner=true` injected → expected success status.
- Store methods: query results mapped to correct models; `pgtype` does not leak past store boundary.
- Service guard ordering matches the design doc exactly.
- `ApprovalGate` path: `access_type=request` AccessResult in context → 202 with `request_id`.

---

**What to test in any feature package (applies to all domains):**
- That the feature's *handler* or *service* calls shared validators and maps the returned sentinel to the correct HTTP status + error code. One test per sentinel is sufficient; do not repeat boundary-value sub-cases.
- That the feature's *store* correctly passes validated inputs to the DB and handles DB-level errors (unique violations, no-rows, etc.).
- Business rules that live exclusively in the feature (rate-limiting cooldowns, token issuance TTL, specific audit event types).
