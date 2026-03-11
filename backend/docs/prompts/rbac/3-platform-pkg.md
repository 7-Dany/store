# RBAC — Phase 3: `internal/platform/rbac/` (Checker + Middleware)

**Feature:** RBAC
**Phase:** 3 of 10
**Depends on:** Phases 0–2 (schema ✅, queries ✅, seeds ✅)
**Gate:** ✅ `go test -tags integration ./internal/platform/rbac/...` green — T-R01 through T-R17 + T-RAG01 through T-RAG04 all pass
**Status:** ✅ Implemented
**Design doc:** `docs/prompts/rbac/0-design.md`
**Context:** `docs/prompts/rbac/context.md`
**Go version:** 1.25 — use modern idioms throughout (`any`, `min`/`max`, range-over-int, etc.)

---

## Architectural principle — keep platform/rbac minimal and decoupled

`Require` is a **pure permission gate**. Its only job is to check whether the caller
has a given permission and inject the result into context. It has zero knowledge of
what happens next — including approval submission.

### Access type semantics

| `access_type` | Meaning | Conditions not met |
|---|---|---|
| `direct` | Granted unconditionally | N/A |
| `conditional` | Granted but ABAC-scoped — handler evaluates constraints | **Handler calls `ConditionalEscalator` → 202** (or 403 if domain chooses not to escalate) |
| `request` | Requires approval before acting | **202** — `ApprovalGate` intercepts, always |
| `denied` | Explicitly blocked | **403** |

`conditional` means "granted, but within constraints." `Require` injects the full
`AccessResult` (scope + conditions) and always calls `next` — it never evaluates
the conditions itself. The handler reads `scope` and `conditions` from context,
evaluates them against the incoming request, and then makes one of two choices:

- **Conditions met** → execute the action immediately (the common path).
- **Conditions not met** → call `ConditionalEscalator.EscalateConditional`, which
  submits an approval request via the requests domain and returns the `request_id`.
  The handler returns 202. Once approved, the requests processor replays the action.

The `ConditionalEscalator` interface is defined in `checker.go` (alongside
`ApprovalSubmitter`) and implemented by the requests domain. This keeps the
escalation path discoverable and consistent across every domain that has conditional
permissions — no domain rolls its own 202 logic.

`ApprovalGate` is a **separate, optional, composable middleware** that reads the
injected `AccessResult` from context and, when `AccessType == "request"`, delegates
to an `ApprovalSubmitter` interface. The requests domain implements that interface.
`platform/rbac` never imports the requests domain.

```
token.Auth  →  rbac.Require("perm")  →  rbac.ApprovalGate(submitter)  →  handler
                      │                          │
                 pure DB check             domain bridge
                 injects AccessResult      calls submitter if needed
                 always calls next         returns 202 or calls next
```

This lets the approval workflow be built, tested, and swapped independently of the
RBAC middleware. Routes that will never have `access_type = 'request'` permissions
can omit `ApprovalGate` entirely.

---

## What you are building

```
internal/platform/rbac/
    errors.go       — sentinel errors
    context.go      — context key type, AccessResult, inject/read helpers
    checker.go      — Checker, ConditionalEscalator interface, ApprovalSubmitter interface,
                      IsOwner, HasPermission, Require middleware, ApprovalGate middleware
    checker_test.go — T-R01 through T-R17 (Require/Checker) + T-RAG01 through T-RAG04 (ApprovalGate)
```

No other files are created in this phase.

---

## ✅ Implemented — reference only

This phase is complete. The files below exist and pass all gate tests.
Do not modify them unless fixing a confirmed bug. Treat this document as the
combination of spec + implementation record for future phases.

---

## Read before writing any code (historical)

1. `docs/prompts/rbac/0-design.md` — §7 (type contracts), §9 (middleware sketch), §11 (decisions), §12 (test cases)
2. `docs/prompts/rbac/context.md` — resolved paths and decisions D-R1 through D-R11
3. `internal/db/rbac.sql.go` — **critical**: read `CheckUserAccessRow` field types before writing any type assertions
4. `internal/platform/token/middleware.go` — follow structural pattern for chi middleware
5. `internal/platform/respond/respond.go` — functions to call for JSON error/success responses
6. `sql/schema/005_requests.sql` — understand `requests` + `request_required_approvers` shape so `ApprovalSubmitter` carries the right data
7. One analogous platform package for doc-comment style (e.g. `internal/platform/kvstore/store.go`)

Do **not** read domain files. Do not import any `internal/domain/` package.

---

## Generated DB types (reference)

`CheckUserAccessRow` (from `internal/db/rbac.sql.go`) uses `any` for nullable/ENUM
columns because sqlc cannot infer the concrete type from `COALESCE` expressions:

```go
type CheckUserAccessRow struct {
    IsOwner            any         // bool when non-nil; nil when user has no role row
    IsExplicitlyDenied bool
    HasPermission      pgtype.Bool
    AccessType         any         // string (ENUM value) when non-nil
    Scope              any         // string (ENUM value) when non-nil
    Conditions         any         // []byte when non-nil
}
```

Write these three unexported helpers in `checker.go`:

```go
func asBool(v any) bool {
    b, _ := v.(bool)
    return b
}

func asString(v any, fallback string) string {
    if s, ok := v.(string); ok {
        return s
    }
    return fallback
}

func asBytes(v any, fallback []byte) []byte {
    if b, ok := v.([]byte); ok {
        return b
    }
    return fallback
}
```

pgx decodes custom ENUMs as plain `string` at runtime. Compare `AccessType` to raw
string literals `"direct"`, `"conditional"`, `"request"`, `"denied"` — not to
`db.PermissionAccessTypeDirect` etc.

`pgtype.Bool` carries `.Bool` and `.Valid`. Treat `!HasPermission.Valid` as false.

---

## `errors.go`

Exactly the sentinels from `0-design.md §7`. No extras.

```go
package rbac

import "errors"

var (
    ErrForbidden           = errors.New("insufficient permissions")
    ErrUnauthenticated     = errors.New("authentication required")
    ErrApprovalRequired    = errors.New("action requires approval — request submitted")
    ErrSystemRoleImmutable = errors.New("system roles cannot be modified")
    ErrCannotReassignOwner = errors.New("owner role cannot be reassigned via this route")
    ErrCannotModifyOwnRole = errors.New("you cannot modify your own role assignment")
    ErrOwnerAlreadyExists  = errors.New("an active owner already exists")
    ErrCannotLockOwner     = errors.New("owner accounts cannot be admin-locked")
    ErrCannotLockSelf      = errors.New("you cannot lock your own account")
)
```

---

## `context.go`

### Unexported context key type

```go
type contextKey int

const (
    accessResultKey contextKey = iota
    testPermissionsKey
)
```

### `AccessResult` struct

```go
// AccessResult is the full access context injected by Require into every request
// that passes the permission check, including those with access_type = "request".
// Downstream middleware (ApprovalGate) and handlers read from this — never from the DB.
type AccessResult struct {
    Permission    string          // canonical permission that was checked, e.g. "job_queue:configure"
    IsOwner       bool
    HasPermission bool
    AccessType    string          // "direct" | "conditional" | "request" | "denied"
    Scope         string          // "own" | "all"
    Conditions    json.RawMessage // '{}' when no conditions apply
}
```

`Permission` is populated by `Require` from its own argument — it lets downstream
middleware such as `ApprovalGate` know which permission triggered the check without
needing it passed as a separate parameter.

### Exported functions

```go
// InjectPermissionsForTest writes a set of allowed permission strings into ctx.
// Require checks this set before hitting the DB.
// Call only from test code — never from production paths.
func InjectPermissionsForTest(ctx context.Context, perms ...string) context.Context

// HasPermissionInContext checks test-injected permissions.
// Returns (false, false) when no test set is present — Require falls through to DB.
// Returns (true,  true)  when the permission is in the test set.
// Returns (false, true)  when a test set exists but this permission is not in it.
func HasPermissionInContext(ctx context.Context, permission string) (allowed, found bool)

// AccessResultFromContext returns the AccessResult injected by Require.
// Returns nil when called outside a Require-guarded route.
func AccessResultFromContext(ctx context.Context) *AccessResult

// ScopeFromContext returns the scope ("own"|"all") from the current AccessResult.
// Returns "own" — the safe default — when not set.
func ScopeFromContext(ctx context.Context) string

// ConditionsFromContext returns the conditions JSONB from the current AccessResult.
// Returns json.RawMessage("{}") when not set.
func ConditionsFromContext(ctx context.Context) json.RawMessage
```

### Unexported helper (used only by checker.go)

```go
func injectAccessResult(ctx context.Context, r *AccessResult) context.Context
```

---

## `checker.go`

### `ConditionalEscalator` interface ✅

Defined in `checker.go` above `ApprovalSubmitter`. Implemented by the requests domain
in Phase 10. Domain handlers receive it via `deps.ConditionalEscalator`.

```go
type ConditionalEscalator interface {
    EscalateConditional(ctx context.Context, userID, permission string, r *http.Request) (requestID string, err error)
}
```

**Handler pattern for conditional escalation:**

```go
// In the domain handler/service:
scope      := rbac.ScopeFromContext(r.Context())
conditions := rbac.ConditionsFromContext(r.Context())

if !meetsConditions(input, conditions, scope) {
    userID, _ := token.UserIDFromContext(r.Context())
    requestID, err := h.conditionalEscalator.EscalateConditional(
        r.Context(), userID, rbac.PermProductManage, r,
    )
    if err != nil {
        slog.ErrorContext(r.Context(), "handler: conditional escalation failed", "error", err)
        respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
        return
    }
    respond.JSON(w, http.StatusAccepted, map[string]any{
        "code":       "approval_required",
        "request_id": requestID,
        "message":    "this action exceeds your permission limits — a request has been submitted",
    })
    return
}
// conditions met — proceed with the action normally
```

The 202 response shape is intentionally identical to `ApprovalGate`'s response so
the client only needs one code path for both approval scenarios.

---

### `ApprovalSubmitter` interface

Define this interface **in `checker.go`** (not in a separate file) immediately before
the `Checker` struct. It is the only coupling point between `platform/rbac` and the
requests domain — the requests domain implements it; `platform/rbac` defines it.

```go
// ApprovalSubmitter is implemented by the requests domain.
// ApprovalGate calls it when a permission has access_type = "request".
// Defining the interface here keeps platform/rbac free of any import dependency
// on the requests domain — the requests domain depends on platform/rbac, not vice versa.
type ApprovalSubmitter interface {
    // SubmitPermissionApproval creates a pending approval request for the given
    // user and permission. The full *http.Request is passed so the implementation
    // can capture everything needed to replay the action once approved:
    // chi path params, query params, and the request body.
    //
    // Reading the body is safe here because ApprovalGate never calls next on the
    // approval path — the guarded handler is not invoked, so the body stream is
    // unconsumed. If the implementation reads the body it must also close it.
    //
    // The implementation is responsible for:
    //   — building request_data JSONB (see canonical shape below)
    //   — inserting a requests row (request_type = "permission_action")
    //   — populating request_required_approvers from permission_request_approvers
    //     for this permission
    // Returns the new request's UUID string on success.
    SubmitPermissionApproval(ctx context.Context, userID, permission string, r *http.Request) (requestID string, err error)
}
```

**Canonical `request_data` shape for `request_type = "permission_action"`:**

The requests domain implementation must build and validate this JSON against the
`request_type_schemas` row for `"permission_action"` before inserting into `requests`:

```jsonc
{
    "permission":   "job_queue:configure",        // always present — canonical name
    "method":       "POST",                       // r.Method
    "path":         "/api/v1/admin/queues/email/pause",  // r.URL.Path
    "path_params":  { "kind": "email" },          // chi.RouteContext(r.Context()).URLParams — omit if empty
    "query_params": { "force": "true" },          // r.URL.Query() — omit if empty
    "body":         { ... }                       // parsed JSON body — omit if no body or non-JSON
}
```

This is what the requests processor uses to replay the action once the approval is
granted. It reads `path_params` to reconstruct route variables, `body` to reconstruct
the payload, and dispatches to the appropriate service method directly — it does
**not** re-issue an HTTP call. The processor is independent from the RBAC middleware.

### Struct and constructor

```go
// Checker performs RBAC permission checks against the database.
// All methods are safe for concurrent use from multiple goroutines.
// Construct once at server startup via NewChecker and store in app.Deps.
type Checker struct {
    q db.Querier
}

// NewChecker constructs a Checker backed by the given Querier.
// Panics if q is nil — misconfiguration must be caught at startup.
func NewChecker(q db.Querier) *Checker {
    if q == nil {
        panic("rbac.NewChecker: querier must not be nil")
    }
    return &Checker{q: q}
}
```

### `IsOwner`

```go
// IsOwner reports whether userID holds the active owner role.
// Returns (false, nil) for any non-owner user, unknown user IDs, and parse failures.
func (c *Checker) IsOwner(ctx context.Context, userID string) (bool, error)
```

Parse `userID` → `pgtype.UUID` (return false, nil on failure). Call
`c.q.CheckUserAccess` with `Permission: ""`. Safe to call with empty permission —
the owner check reads `user_role_ctx.is_owner_role` which is independent of the
permission argument; the permission-specific CTEs produce empty sets, which is harmless.

### `HasPermission`

```go
// HasPermission reports whether userID holds the given canonical permission
// via role or direct grant, and is not explicitly denied.
// Returns (false, nil) for unknown user IDs and expired grants.
func (c *Checker) HasPermission(ctx context.Context, userID, permission string) (bool, error)
```

Call `CheckUserAccess`. Short-circuit on `asBool(row.IsOwner)` → true.
Otherwise: `row.HasPermission.Bool && row.HasPermission.Valid && !row.IsExplicitlyDenied`.

### `Require` middleware — minimal, always calls next on non-error paths

```go
// Require returns chi-compatible middleware that enforces the named permission.
// It is intentionally minimal: its only job is to check permissions and inject
// the AccessResult into context. It does NOT handle approval submission —
// compose ApprovalGate after Require for routes that need that behaviour.
//
// Prerequisites:
//   token.Auth must run before Require — it injects the userID.
//
// Guard order (implement exactly in this sequence):
//   1. token.UserIDFromContext — empty/missing → 401 authentication_required
//   2. HasPermissionInContext  — test hook; short-circuits DB if a test set is present
//   3. c.q.CheckUserAccess    — DB error → slog.ErrorContext + 500; fails closed (D-R11)
//   4. asBool(row.IsOwner)    → inject AccessResult{IsOwner:true, Scope:"all"}; call next
//   5. row.IsExplicitlyDenied → 403 forbidden
//   6. !row.HasPermission     → 403 forbidden
//   7. switch asString(row.AccessType, "direct"):
//        "denied"                      → 403 forbidden
//        "request"                     → inject AccessResult{AccessType:"request"}; call next
//        "conditional"                 → inject full AccessResult (scope+conditions populated); call next
//                                        Require never evaluates the conditions — that is the handler's
//                                        job. If conditions are not met the handler calls
//                                        ConditionalEscalator.EscalateConditional and returns 202.
//        "direct" | _                  → inject full AccessResult; call next
//
// access_type = "request" does NOT produce a 202 here. Require injects the
// AccessResult and calls next — ApprovalGate (chained after) intercepts and returns 202.
//
// Require never reads r.Body — it only inspects the Authorization header via
// token.UserIDFromContext. The body stream is left intact for ApprovalGate's
// submitter to read if the approval path is taken.
func (c *Checker) Require(permission string) func(http.Handler) http.Handler
```

`respond.Error` exact strings:

| Condition | status | code | message |
|-----------|--------|------|---------|
| No userID in context | 401 | `"authentication_required"` | `"authentication is required"` |
| Forbidden (any branch) | 403 | `"forbidden"` | `"insufficient permissions"` |
| DB error | 500 | `"internal_error"` | `"internal server error"` |

Log DB errors before responding 500:
```go
slog.ErrorContext(r.Context(), "rbac.Require: db check failed", "error", err)
```

For the **test hook path** (step 2): when `HasPermissionInContext` finds a test set,
inject a synthetic `AccessResult{Permission: permission, HasPermission: true, AccessType: "direct", Scope: "all", Conditions: json.RawMessage("{}")}` and call next.
If the test set exists but the permission is absent, return 403 directly.

### `ApprovalGate` middleware — separate, composable, domain-agnostic

```go
// ApprovalGate returns middleware that intercepts requests where the AccessResult
// injected by Require has AccessType == "request", calls the submitter to create
// an approval request, then returns 202 Accepted. The guarded handler is NOT called —
// it must not run until the approval is granted and the requests processor executes it.
//
// For all other access types (direct, conditional, owner) it is a no-op passthrough.
// Chain ApprovalGate immediately after Require on routes where approval may apply:
//
//   r.With(
//       deps.JWTAuth,
//       deps.RBAC.Require(rbac.PermJobQueueConfigure),
//       deps.RBAC.ApprovalGate(deps.ApprovalSubmitter),
//   ).Post("/queues/{kind}/pause", h.PauseKind)
//
// Routes without any 'request'-type permissions may omit ApprovalGate entirely.
//
// Behaviour:
//   AccessType != "request"  → call next unchanged (body untouched)
//   submitter is nil         → 503 approval_unavailable (safe until requests domain is wired)
//   submitter returns error  → slog.ErrorContext + 500 internal_error
//   submitter succeeds       → 202 {"code":"approval_required","request_id":"<uuid>","message":"..."}
//
// ApprovalGate passes the original *http.Request to SubmitPermissionApproval so the
// requests domain can capture path params, query params, and body for later replay.
// The body is safe to read because next is never called on the approval path —
// the guarded handler is not invoked and the body stream is unconsumed.
//
// ApprovalGate does not read or buffer the body itself — that is the submitter's
// responsibility. If the submitter reads the body it must also close it.
func (c *Checker) ApprovalGate(submitter ApprovalSubmitter) func(http.Handler) http.Handler
```

**503 response when submitter is nil:**
```go
respond.Error(w, http.StatusServiceUnavailable, "approval_unavailable",
    "approval submission is not available yet")
```

**500 response when submitter errors:**
```go
slog.ErrorContext(r.Context(), "rbac.ApprovalGate: submit approval failed", "error", err)
respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
```

**202 response on success:**
```go
respond.JSON(w, http.StatusAccepted, map[string]any{
    "code":       "approval_required",
    "request_id": requestID,
    "message":    "this action requires approval — a request has been submitted",
})
```

`ApprovalGate` reads `AccessResultFromContext(r.Context()).Permission` for the
permission name to pass to the submitter — this is why `Require` stamps `Permission`
onto `AccessResult` for every code path including the `"request"` branch.

### UUID parsing helper (unexported)

```go
// parseUUID converts a string userID to pgtype.UUID.
// Returns pgtype.UUID{} (Valid=false) on failure — CheckUserAccess returns no rows,
// yielding is_owner=false, has_permission=false.
func parseUUID(s string) pgtype.UUID {
    id, err := uuid.Parse(s)
    if err != nil {
        return pgtype.UUID{}
    }
    return pgtype.UUID{Bytes: id, Valid: true}
}
```

---

## Route wiring examples (reference — implement in Phase 10)

```go
// direct — omit ApprovalGate
r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermJobQueueRead)).
    Get("/stats", h.Stats)

// conditional — omit ApprovalGate; handler reads scope+conditions from context
r.With(deps.JWTAuth, deps.RBAC.Require(rbac.PermProductManage)).
    Post("/products", h.CreateProduct)

// request — ApprovalGate intercepts for admin (access_type="request");
// owner bypasses (access_type resolved as owner, ApprovalGate no-ops)
r.With(
    deps.JWTAuth,
    deps.RBAC.Require(rbac.PermJobQueueConfigure),
    deps.RBAC.ApprovalGate(deps.ApprovalSubmitter),
).Post("/queues/{kind}/pause", h.PauseKind)
```

---

## `checker_test.go`

### Build tag and package

```go
//go:build integration

package rbac_test
```

Unit tests (T-R01, T-R08, T-R09, T-R10, T-RAG01..T-RAG04) use a fake Querier — no pool.
Integration tests use a real pool from `authsharedtest.MustNewTestPool`. Both live in the
same file; `TestMain` guards pool construction with the `integration` build tag.

### Fake Querier

```go
type fakeQuerier struct {
    db.Querier // embedded to satisfy remaining interface methods (panic on unexpected calls)
    row db.CheckUserAccessRow
    err error
}

func (f *fakeQuerier) CheckUserAccess(_ context.Context, _ db.CheckUserAccessParams) (db.CheckUserAccessRow, error) {
    return f.row, f.err
}
```

### Fake ApprovalSubmitter

```go
type fakeSubmitter struct {
    requestID string
    err       error
    // populated on call — assert in tests
    called        bool
    gotUserID     string
    gotPermission string
    gotRequest    *http.Request
}

func (f *fakeSubmitter) SubmitPermissionApproval(_ context.Context, userID, permission string, r *http.Request) (string, error) {
    f.called = true
    f.gotUserID = userID
    f.gotPermission = permission
    f.gotRequest = r
    return f.requestID, f.err
}
```

---

### Require tests (T-R01 through T-R17)

| ID | Scenario | Layer | Key assertion |
|----|----------|-------|---------------|
| T-R01 | `Require` passes for owner regardless of permission | U | HTTP 200; next called; `AccessResult.IsOwner == true` |
| T-R02 | `Require` passes + injects scope for `direct` | I | HTTP 200; `ScopeFromContext` == seeded scope |
| T-R03 | `Require` passes + injects scope+conditions for `conditional` | I | HTTP 200; `ConditionsFromContext` ≠ `{}` |
| T-R04 | `Require` injects `AccessResult` and calls next for `access_type = "request"` | I | HTTP 200; next called; `AccessResultFromContext.AccessType == "request"` |
| T-R05 | `Require` returns 403 for `denied` | I | HTTP 403; next NOT called |
| T-R06 | `Require` returns 403 when user has no role and no direct grant | I | HTTP 403; next NOT called |
| T-R07 | `Require` returns 403 when direct grant is expired | I | HTTP 403; next NOT called |
| T-R08 | `Require` returns 401 when no userID in context | U | HTTP 401; `code=="authentication_required"` |
| T-R09 | `Require` uses test-injected permissions; no DB hit | U | HTTP 200; fake querier `CheckUserAccess` never called |
| T-R10 | `Require` returns 500 + fails closed on DB error | U | HTTP 500; next NOT called |
| T-R11 | `IsOwner` returns true for owner-role user | I | `true, nil` |
| T-R12 | `IsOwner` returns false for non-owner user | I | `false, nil` |
| T-R13 | `HasPermission` returns true via role path | I | `true, nil` |
| T-R14 | `HasPermission` returns true via direct-grant path | I | `true, nil` |
| T-R15 | `HasPermission` returns false after role permission removed | I | `false, nil` |
| T-R16 | `ScopeFromContext` returns `"all"` for admin, `"own"` for vendor | I | scope matches seeded value |
| T-R17 | `ConditionsFromContext` returns non-`{}` conditions for conditional grant | I | raw JSON ≠ `{}` |

**T-R04 detail:** seed a role/permission with `access_type = 'request'`. Chain `Require`
only (no `ApprovalGate`). A `next` handler records that it was called. Assert next was
called and `AccessResultFromContext(ctx).AccessType == "request"`. This proves `Require`
itself does not intercept — `ApprovalGate` is the interceptor.

### ApprovalGate tests (T-RAG01 through T-RAG04)

All four are **unit tests** — use `fakeQuerier` and `fakeSubmitter`.
Chain: `Require → ApprovalGate(submitter) → next`.

| ID | Scenario | Key assertions |
|----|----------|---------------|
| T-RAG01 | `access_type = "request"` → submitter called with correct userID, permission, and `*http.Request`; 202 returned; next NOT called | HTTP 202; `code=="approval_required"`; `request_id` present; `fakeSubmitter.gotPermission == permission`; `fakeSubmitter.gotRequest != nil` |
| T-RAG02 | `access_type = "direct"` → submitter NOT called; next called | HTTP 200; `fakeSubmitter.called == false` |
| T-RAG03 | nil submitter + `access_type = "request"` → 503 | HTTP 503; `code=="approval_unavailable"` |
| T-RAG04 | submitter returns error + `access_type = "request"` → 500; next NOT called | HTTP 500; `code=="internal_error"` |

---

### Integration test setup pattern

Each integration test:
- Begins a transaction rolled back in `t.Cleanup` (`authsharedtest.MustBeginTx`).
- Seeds users via `authsharedtest.CreateUser`.
- Seeds roles/permissions directly via `q.*` methods (no service layer).
- Constructs `rbac.NewChecker(q)` with the transactional querier.

---

## Wiring into `app.Deps` ✅

`internal/app/deps.go` — **done**:

```go
RBAC                  *rbac.Checker
ApprovalSubmitter      rbac.ApprovalSubmitter    // nil until Phase 10
ConditionalEscalator   rbac.ConditionalEscalator // nil until Phase 10
```

`internal/server/server.go` — **done**:

```go
deps.RBAC = rbac.NewChecker(db.New(pool))
// deps.ApprovalSubmitter and deps.ConditionalEscalator assigned in Phase 10.
```

Domain handlers that need conditional escalation receive `deps.ConditionalEscalator`
via their constructor and must nil-check before calling (safe until Phase 10).

---

## What NOT to do in this phase

- Do not implement `SubmitPermissionApproval` — that belongs to the requests domain.
- Do not add rate limiters — bootstrap has its own (Phase 4).
- Do not import any `internal/domain/` package — platform packages never import domain.
- Do not create `store.go` or `service.go` — Checker IS the service+store here.
- Do not modify `internal/audit/audit.go` — RBAC mutations are audited at DB trigger level.
- Do not create `internal/domain/rbac/` — that starts Phase 4.
- Do not use `interface{}` anywhere — this is Go 1.25; use `any` throughout.
- Do not evaluate `conditional` permission constraints inside `Require` or `ApprovalGate`.
  Condition logic is domain-specific — the middleware has no knowledge of what `max_price`
  or any other condition field means. Inject `AccessResult` and call `next`; let the handler
  decide.
- Do not scatter the 202 escalation response across domain handlers. Use
  `ConditionalEscalator.EscalateConditional` (defined here, implemented by the requests
  domain) so all conditional escalations go through one code path and produce the same
  response shape as `ApprovalGate`.
- Do not implement `EscalateConditional` in this phase — that belongs to the requests domain.

---

## Gate checklist ✅

- [x] `go build ./internal/platform/rbac/...` — zero errors
- [x] `go vet ./internal/platform/rbac/...` — zero warnings
- [x] `go test -tags integration ./internal/platform/rbac/...` — T-R01..T-R17 + T-RAG01..T-RAG04 green
- [x] `go build ./internal/app/...` — `Deps.RBAC`, `Deps.ApprovalSubmitter`, `Deps.ConditionalEscalator` compile
- [x] `go build ./internal/server/...` — `rbac.NewChecker` construction compiles
- [x] No circular imports — nothing outside `platform/rbac` imports it yet
