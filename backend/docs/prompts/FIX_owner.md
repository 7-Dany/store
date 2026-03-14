# Fix Prompt — `internal/domain/rbac/owner`

You are implementing a set of concrete fixes for the Go package at:

```
internal/domain/rbac/owner
```

**Before touching any file, read these files in order:**

1. `docs/RULES.md` — all naming, layering, import, testing, and comment conventions.
2. Every `.go` file currently in `internal/domain/rbac/owner/`.
3. `internal/domain/rbac/shared/errors.go`
4. `internal/domain/rbac/shared/store.go`
5. `internal/domain/rbac/shared/testutil/fake_storer.go`
6. `internal/domain/rbac/shared/testutil/fake_servicer.go`
7. `internal/domain/rbac/shared/testutil/querier_proxy.go`
8. `internal/domain/rbac/shared/testutil/builders.go`
9. `internal/platform/rbac/errors.go`
10. `internal/platform/rbac/checker.go`
11. `internal/platform/mailer/mailer.go`
12. `internal/platform/mailer/templates/owner_transfer.go`
13. `internal/audit/audit.go`
14. `internal/app/deps.go`
15. `internal/domain/rbac/routes.go`

Implement **every fix listed below in order**. For each fix the affected file,
the location, and the exact change are stated. Do not skip any item. Do not
introduce changes beyond what is described.

After completing all fixes, run:

```
go build ./internal/domain/rbac/owner/...
go vet  ./internal/domain/rbac/owner/...
go test ./internal/domain/rbac/owner/...
```

All three must pass with zero errors before you are done.

---

## Fix 1 — Add package doc comment

**File:** `handler.go` (the primary file for this package — it defines the
exported `Handler` type and all route methods)

At the very top of the file, before the `package owner` line, add:

```go
// Package owner provides HTTP handlers, service logic, and database access for
// the initial owner-role assignment and ownership-transfer operations.
```

Remove any stray doc-comment attempt from other files in the package.

---

## Fix 2 — Resolve the dual-boundary type violations in models.go / requests.go

The rules require: `models.go` — service-layer I/O structs, **no `json:` tags**;
`requests.go` — HTTP request/response structs, **all `json:` tags** (ADR-012,
RULES.md §3.1, §3.3).

Currently three types violate this:

- `AssignOwnerResult` lives in `requests.go` but is returned by `Service.AssignOwner` and `Store.AssignOwnerTx`, making it a service-layer type with json tags.
- `InitiateResult` lives in `models.go` but carries `json:` tags.
- `AcceptResult` lives in `models.go` but carries `json:` tags.

### 2a — Fix `AssignOwnerResult`

Move the declaration from `requests.go` to `models.go`, and **strip all `json:` tags**:

```go
// models.go

// AssignOwnerResult is the service-layer output for a successful owner assignment.
type AssignOwnerResult struct {
    UserID    string
    RoleName  string
    GrantedAt time.Time
}
```

In `requests.go`, add a new HTTP-response type that the handler writes:

```go
// assignOwnerResponse is the JSON body written on a successful POST /owner/assign.
type assignOwnerResponse struct {
    UserID    string    `json:"user_id"`
    RoleName  string    `json:"role_name"`
    GrantedAt time.Time `json:"granted_at"`
}
```

In `handler.go`, update `AssignOwner` to map the service result to the response type:

```go
result, err := h.svc.AssignOwner(r.Context(), AssignOwnerInput{…})
if err == nil {
    respond.JSON(w, http.StatusCreated, assignOwnerResponse{
        UserID:    result.UserID,
        RoleName:  result.RoleName,
        GrantedAt: result.GrantedAt,
    })
    return
}
```

### 2b — Fix `InitiateResult`

Remove `json:` tags from `InitiateResult` in `models.go`:

```go
// InitiateResult is the service-layer output returned on successful transfer initiation.
type InitiateResult struct {
    TransferID   string
    TargetUserID string
    TargetEmail  string   // ← add this field; needed by the handler to send the invite email
    ExpiresAt    time.Time
}
```

Note the new `TargetEmail` field — this is required by Fix 5 (mailer wiring).
It must be populated in `service.go`'s `InitiateTransfer` from `target.Email`
before calling `InsertTransferToken`. Update the return statement:

```go
return InitiateResult{
    TransferID:   uuid.UUID(tokenID).String(),
    TargetUserID: targetIDStr,
    TargetEmail:  target.Email,
    ExpiresAt:    expiresAt,
}, rawToken, nil
```

In `requests.go`, add a new HTTP-response type:

```go
// initiateResponse is the JSON body written on a successful POST /owner/transfer.
type initiateResponse struct {
    TransferID   string    `json:"transfer_id"`
    TargetUserID string    `json:"target_user_id"`
    ExpiresAt    time.Time `json:"expires_at"`
}
```

In `handler.go`, map the result to the response type before passing to `respond.JSON`.
`TargetEmail` must **not** appear in the HTTP response.

### 2c — Fix `AcceptResult`

Remove `json:` tags from `AcceptResult` in `models.go`:

```go
// AcceptResult is the service-layer output returned on successful transfer acceptance.
type AcceptResult struct {
    NewOwnerID      string
    PreviousOwnerID string
    TransferredAt   time.Time
}
```

In `requests.go`, add:

```go
// acceptResponse is the JSON body written on a successful POST /owner/transfer/accept.
type acceptResponse struct {
    NewOwnerID      string    `json:"new_owner_id"`
    PreviousOwnerID string    `json:"previous_owner_id"`
    TransferredAt   time.Time `json:"transferred_at"`
}
```

In `handler.go`, map accordingly in `AcceptTransfer`.

---

## Fix 3 — Canonical `WithQuerier` form in store.go

**File:** `store.go`

Replace the current struct-literal `WithQuerier` with the copy-then-reassign
canonical form (RULES.md §3.3 "Feature store struct"):

```go
// WithQuerier returns a copy of the store with its querier replaced by q and
// TxBound set to true. Used in integration tests to bind the store to a
// rolled-back test transaction.
func (s *Store) WithQuerier(q db.Querier) *Store {
    c := *s
    c.BaseStore = s.BaseStore.WithQuerier(q)
    return &c
}
```

---

## Fix 4 — Add `// Unreachable via QuerierProxy:` comments in store.go

**File:** `store.go`

### 4a — `AssignOwnerTx` — `BeginOrBind` error path

Add immediately above the `if err != nil` check after `BeginOrBind`:

```go
// Unreachable via QuerierProxy: BeginOrBind with TxBound=true always returns
// the injected querier with nil error. No test can trigger this branch.
```

### 4b — `AssignOwnerTx` — `Commit` error path

Add immediately above the `if err := h.Commit(); err != nil` line:

```go
// Unreachable via QuerierProxy: on the TxBound path Commit is a no-op
// returning nil; on the non-TxBound path Commit wraps pgx.Tx.Commit which
// QuerierProxy cannot intercept.
```

### 4c — `AcceptTransferTx` — `BeginOrBind` error path

Same `BeginOrBind` comment as 4a.

### 4d — `AcceptTransferTx` — `Commit` error path

Same `Commit` comment as 4b.

---

## Fix 5 — Wire the ownership-transfer email (critical functional fix)

The raw token returned by `svc.InitiateTransfer` is currently discarded with `_`.
This means the target user never receives their invitation and the transfer can
never be accepted. Fix this end-to-end.

### 5a — Extend `Handler` to hold the mailer deps

**File:** `handler.go`

Add two fields to the `Handler` struct:

```go
type Handler struct {
    svc       Servicer
    secret    string
    deps      handlerDeps
    mailer    *mailer.SMTPMailer  // for sending the transfer invitation email
    mailQueue *mailer.Queue       // async delivery queue
}
```

Add the `"github.com/7-Dany/store/backend/internal/platform/mailer"` import.

Update `NewHandler`:

```go
func NewHandler(
    svc Servicer,
    secret string,
    checker *rbac.Checker,
    m *mailer.SMTPMailer,
    queue *mailer.Queue,
) *Handler {
    return &Handler{
        svc:       svc,
        secret:    secret,
        deps:      &rbacDeps{checker: checker},
        mailer:    m,
        mailQueue: queue,
    }
}
```

### 5b — Send the email in `InitiateTransfer`

**File:** `handler.go` — `InitiateTransfer` method

Replace:

```go
result, _, err := h.svc.InitiateTransfer(r.Context(), InitiateInput{…})
```

With:

```go
result, rawToken, err := h.svc.InitiateTransfer(r.Context(), InitiateInput{…})
```

After the `if err == nil` success check and **before** `respond.JSON`, enqueue
the invitation email:

```go
if err == nil {
    // Enqueue the ownership-transfer invitation email to the target user.
    // Non-fatal: a queue failure is logged but does not abort the response;
    // the token is already persisted and the owner can initiate again.
    mailertemplates "github.com/7-Dany/store/backend/internal/platform/mailer/templates"
    token := rawToken
    email := result.TargetEmail
    h.mailQueue.Enqueue(r.Context(), func(ctx context.Context) error {
        return h.mailer.Send(mailertemplates.OwnerTransferKey)(ctx, email, token)
    })
    respond.JSON(w, http.StatusCreated, initiateResponse{
        TransferID:   result.TransferID,
        TargetUserID: result.TargetUserID,
        ExpiresAt:    result.ExpiresAt,
    })
    return
}
```

Add the `mailertemplates` import at the top of the file (use the alias
`mailertemplates "github.com/7-Dany/store/backend/internal/platform/mailer/templates"`).

### 5c — Update `routes.go` to pass mailer deps to `NewHandler`

**File:** `routes.go`

Change:

```go
h := NewHandler(svc, secret, deps.RBAC)
```

To:

```go
h := NewHandler(svc, secret, deps.RBAC, deps.Mailer, deps.MailQueue)
```

### 5d — Update `export_test.go` to match the new `NewHandlerForTest` signature

**File:** `export_test.go`

`NewHandlerForTest` is used by unit tests that do not need real mailer wiring.
Pass `nil` for both mailer fields — no email is sent in unit tests:

```go
func NewHandlerForTest(svc Servicer, secret string, deps *FakeOwnerDeps) *Handler {
    return &Handler{svc: svc, secret: secret, deps: deps, mailer: nil, mailQueue: nil}
}
```

Add a nil-guard in the `InitiateTransfer` handler so it does not panic in unit
tests that exercise the success path:

```go
if h.mailQueue != nil && h.mailer != nil {
    token := rawToken
    email := result.TargetEmail
    h.mailQueue.Enqueue(r.Context(), func(ctx context.Context) error {
        return h.mailer.Send(mailertemplates.OwnerTransferKey)(ctx, email, token)
    })
}
```

---

## Fix 6 — Add `context.WithoutCancel` to `AcceptTransferTx` call in service.go

**File:** `service.go` — `AcceptTransfer` method

The `AcceptTransferTx` call performs an irreversible, atomic role swap. Per
ADR-004 and RULES.md §3.6, security-critical writes that must not be aborted
by a client disconnect must use `context.WithoutCancel`.

Replace:

```go
transferredAt, err := s.store.AcceptTransferTx(ctx, AcceptTransferTxInput{…})
```

With:

```go
// Security: detach from the request context so a client-timed disconnect cannot
// abort this irreversible role swap mid-transaction (ADR-004).
transferredAt, err := s.store.AcceptTransferTx(context.WithoutCancel(ctx), AcceptTransferTxInput{…})
```

---

## Fix 7 — Log (don't swallow) audit-write errors in service.go

**File:** `service.go`

### 7a — `InitiateTransfer`

Replace:

```go
_ = s.store.WriteInitiateAuditLog(ctx, in.ActingOwnerID, targetIDStr, in.IPAddress, in.UserAgent)
```

With:

```go
if err := s.store.WriteInitiateAuditLog(ctx, in.ActingOwnerID, targetIDStr, in.IPAddress, in.UserAgent); err != nil {
    slog.ErrorContext(ctx, "owner.InitiateTransfer: audit log", "error", err)
}
```

Add `"log/slog"` to the imports if not already present.

### 7b — `CancelTransfer`

Replace:

```go
_ = s.store.WriteCancelAuditLog(ctx, actingOwnerID, ipAddress, userAgent)
```

With:

```go
if err := s.store.WriteCancelAuditLog(ctx, actingOwnerID, ipAddress, userAgent); err != nil {
    slog.ErrorContext(ctx, "owner.CancelTransfer: audit log", "error", err)
}
```

---

## Fix 8 — Add `WithActingUser` to `AcceptTransferTx` step 5 in store.go

**File:** `store.go` — `AcceptTransferTx`, step 5

Per the `BaseStore.WithActingUser` doc comment, a `WithActingUser` call is
required for every `DELETE` on `user_roles` so audit triggers record the
correct actor.

Add `ActingUserID [16]byte` to `AcceptTransferTxInput` in `models.go`:

```go
type AcceptTransferTxInput struct {
    TokenID         [16]byte
    NewOwnerID      [16]byte
    PreviousOwnerID [16]byte
    RoleID          [16]byte
    ActingUserID    [16]byte  // ← add: the new owner, who authorised the transfer
    IPAddress       string
    UserAgent       string
}
```

In `service.go`, populate the field (use `info.NewOwnerID` as the acting user
because the new owner is the one accepting and triggering the role revocation):

```go
s.store.AcceptTransferTx(context.WithoutCancel(ctx), AcceptTransferTxInput{
    TokenID:         info.TokenID,
    NewOwnerID:      info.NewOwnerID,
    PreviousOwnerID: previousOwnerID,
    RoleID:          roleID,
    ActingUserID:    info.NewOwnerID,
    IPAddress:       in.IPAddress,
    UserAgent:       in.UserAgent,
})
```

In `store.go`, wrap step 5 with `WithActingUser`:

```go
// Step 5 — Remove owner role from previous owner.
// WithActingUser is required so fn_audit_role_permissions records the new
// owner (the accepting party) as the actor on the DELETE, not the original granter.
actingStr := uuid.UUID(in.ActingUserID).String()
if err := s.WithActingUser(context.WithoutCancel(ctx), actingStr, func() error {
    _, err := h.Q.RemoveUserRole(ctx, s.ToPgtypeUUID(in.PreviousOwnerID))
    return err
}); err != nil {
    return rb("remove_prev_owner",
        fmt.Errorf("store.AcceptTransferTx: remove prev owner role: %w", err))
}
```

Note: `WithActingUser` uses `b.Queries` internally (not `h.Q`). In the
`TxBound=true` test path both refer to the same querier, so this is safe.

---

## Fix 9 — Add `// Unreachable:` comment on `uuid.Parse` ignores in handler.go

**File:** `handler.go`

In `AssignOwner`, `InitiateTransfer`, and `CancelTransfer`, each `uuid.Parse`
call ignores the error with `_`. Add a one-line comment immediately above each:

```go
// Unreachable: userID is the JWT sub claim, already validated as a valid UUID
// by the token middleware before this handler is reached.
parsed, _ := uuid.Parse(userID)
```

In `InitiateTransfer`, do the same for `targetParsed`:

```go
// Unreachable: req.TargetUserID was validated as a valid UUID by
// validateInitiateRequest above.
targetParsed, _ := uuid.Parse(req.TargetUserID)
```

---

## Fix 10 — Move `ErrOwnerAlreadyExists` sentinel check to owner's errors.go

**File:** `errors.go`, `service.go`, `handler.go`

The service currently imports `platform/rbac` solely to return and the handler
to check `rbac.ErrOwnerAlreadyExists`. The service layer must not import
`platform/rbac` for a sentinel error (RULES.md §2.2 — service may not import
platform packages that are not mailer, token-enum-types, or audit).

### 10a

In `errors.go`, add:

```go
// ErrOwnerAlreadyExists is returned by AssignOwner when an active owner role
// assignment already exists. Wraps platform/rbac.ErrOwnerAlreadyExists so that
// callers in the handler can use errors.Is against either sentinel.
var ErrOwnerAlreadyExists = rbac.ErrOwnerAlreadyExists
```

Add `"github.com/7-Dany/store/backend/internal/platform/rbac"` to the imports
of `errors.go`. (The errors package may import platform packages.)

### 10b

In `service.go`, remove the `platform/rbac` import and replace:

```go
return AssignOwnerResult{}, rbac.ErrOwnerAlreadyExists
```

With:

```go
return AssignOwnerResult{}, ErrOwnerAlreadyExists
```

### 10c

In `handler.go`, the switch case already uses `errors.Is(err, rbac.ErrOwnerAlreadyExists)`.
Change it to use the local sentinel:

```go
case errors.Is(err, ErrOwnerAlreadyExists):
```

Remove the `platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"` import
from `handler.go` if it is only used for this sentinel.

---

## Fix 11 — Add bcrypt cost control for unit tests

The service's `generateTransferToken` calls `bcrypt.GenerateFromPassword` at
`bcrypt.DefaultCost` (cost 12). Every unit test that reaches `InitiateTransfer`
success pays ~300 ms. Follow the auth domain pattern (RULES.md §3.8).

**File:** `service.go`

Add a package-level cost variable and expose a test setter:

```go
// transferTokenBcryptCost is the bcrypt work factor used by generateTransferToken.
// Lowered to bcrypt.MinCost in tests via SetTransferTokenBcryptCostForTest.
var transferTokenBcryptCost = bcrypt.DefaultCost
```

In `generateTransferToken`, replace the hardcoded cost:

```go
h, err := bcrypt.GenerateFromPassword([]byte(raw), transferTokenBcryptCost)
```

**File:** `export_test.go`

Add the test-only setter:

```go
// SetTransferTokenBcryptCostForTest lowers the bcrypt cost used by
// generateTransferToken for fast unit tests.
func SetTransferTokenBcryptCostForTest(cost int) {
    transferTokenBcryptCost = cost
}
```

**File:** `service_test.go`

Add a `TestMain` that lowers the cost for all unit tests in this file.
Because `store_test.go` (with its `//go:build integration_test` tag) owns the
canonical `TestMain`, the unit-test file needs a separate one only when there
is no integration build. Use `rbacsharedtest.RunTestMain` with the bcrypt
lowering:

Actually — do NOT add a second `TestMain` in `service_test.go`. Instead,
lower the cost at the top of every test that reaches `generateTransferToken`
(the `InitiateTransfer_Success` and related tests) by calling
`owner.SetTransferTokenBcryptCostForTest(bcrypt.MinCost)` inside a `TestMain`
in `service_test.go` that is NOT behind a build tag:

```go
func TestMain(m *testing.M) {
    owner.SetTransferTokenBcryptCostForTest(bcrypt.MinCost)
    os.Exit(m.Run())
}
```

Add `"os"` and `"golang.org/x/crypto/bcrypt"` imports to `service_test.go`.

When `store_test.go` is created (Fix 12), its `TestMain` (behind the
`integration_test` tag) will call `rbacsharedtest.RunTestMain`. At that point
there will be two `TestMain` functions — one per build tag, which Go allows
because only one is ever compiled into a given test binary.

---

## Fix 12 — Replace `mustBcrypt` with an `rbacsharedtest` builder helper

**File:** `internal/domain/rbac/shared/testutil/builders.go`

Add a new exported helper (following the pattern of `MustHashPassword`):

```go
// MustTokenHash returns a bcrypt hash of raw at bcrypt.MinCost.
// Used in service unit tests that need a pre-hashed transfer token stored in
// PendingTransferInfo.CodeHash without calling bcrypt at DefaultCost.
func MustTokenHash(raw string) string {
    h, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.MinCost)
    if err != nil {
        panic("rbacsharedtest.MustTokenHash: " + err.Error())
    }
    return string(h)
}
```

**File:** `service_test.go`

Remove the local `mustBcrypt` function entirely. Replace every call site with
`rbacsharedtest.MustTokenHash(…)`.

---

## Fix 13 — Create `store_test.go` with full integration coverage

**File:** `internal/domain/rbac/owner/store_test.go` ← create this file

The file must begin with `//go:build integration_test` and be in package
`owner_test`. It must contain **all** of the following, in order:

### Preamble

```go
//go:build integration_test

package owner_test

import (
    "context"
    "encoding/json"
    "testing"
    "time"

    "github.com/7-Dany/store/backend/internal/db"
    "github.com/7-Dany/store/backend/internal/domain/rbac/owner"
    rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
    rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
    rbacsharedtest.RunTestMain(m, &testPool, 20)
}
```

### `txStores` helper

```go
func txStores(t *testing.T) (*owner.Store, *db.Queries) {
    t.Helper()
    if testPool == nil {
        t.Skip("no test database configured")
    }
    tx, q := rbacsharedtest.MustBeginTx(t, testPool)
    _ = tx
    return owner.NewStore(testPool).WithQuerier(q), db.New(tx)
}
```

### `withProxy` helper

```go
func withProxy(t *testing.T, mutate func(*rbacsharedtest.QuerierProxy)) (*owner.Store, *rbacsharedtest.QuerierProxy) {
    t.Helper()
    if testPool == nil {
        t.Skip("no test database configured")
    }
    _, q := rbacsharedtest.MustBeginTx(t, testPool)
    proxy := rbacsharedtest.NewQuerierProxy(q)
    mutate(proxy)
    return owner.NewStore(testPool).WithQuerier(proxy), proxy
}
```

### Integration tests

Write one `_Integration` test function per scenario listed below. Every test
calls `t.Parallel()` immediately after `t.Helper()` (if applicable) or as the
first statement. Every test that needs a store calls `txStores(t)` or
`withProxy(t, …)`. No raw SQL — every seed and read-back query uses generated
`*db.Queries` methods.

#### `CountActiveOwners`

- `TestCountActiveOwners_Zero_Integration` — no owner row seeded → returns 0, nil
- `TestCountActiveOwners_One_Integration` — one active owner seeded → returns 1, nil

#### `GetOwnerRoleID`

- `TestGetOwnerRoleID_ReturnsNonZeroUUID_Integration` — owner role exists in seed → returned UUID is non-zero
- `TestGetOwnerRoleID_Fail_Integration` — `withProxy` sets `FailGetOwnerRoleID=true` → `rbacsharedtest.ErrProxy` returned

#### `GetActiveUserByID`

- `TestGetActiveUserByID_UserNotFound_Integration` — random UUID → `rbacshared.ErrUserNotFound`
- `TestGetActiveUserByID_Success_Integration` — seeded active+verified user → `IsActive=true, EmailVerified=true`
- `TestGetActiveUserByID_Fail_Integration` — `FailGetActiveUserByID=true` → `ErrProxy`

#### `AssignOwnerTx`

- `TestAssignOwnerTx_Success_Integration` — valid `UserID`, valid `RoleID`; after commit, `user_roles` row exists, audit row with `EventOwnerAssigned` present; result has correct `UserID`, `RoleName="owner"`, non-zero `GrantedAt`
- `TestAssignOwnerTx_FailAssignUserRole_Integration` — `FailAssignUserRole=true` → error returned; no `user_roles` row
- `TestAssignOwnerTx_FailInsertAuditLog_Integration` — `FailInsertAuditLog=true` → error returned; no `user_roles` row (rollback)

#### `GetTransferTargetUser`

- `TestGetTransferTargetUser_UserNotFound_Integration` — random UUID → `rbacshared.ErrUserNotFound`
- `TestGetTransferTargetUser_NotOwner_Integration` — seeded non-owner user → `IsOwner=false`, `IsActive`, `EmailVerified`, `Email` correct
- `TestGetTransferTargetUser_IsOwner_Integration` — seeded owner user → `IsOwner=true`
- `TestGetTransferTargetUser_FailGetActiveUserByID_Integration` — `FailGetActiveUserByID=true` → `ErrProxy`
- `TestGetTransferTargetUser_FailCheckUserAccess_Integration` — `FailCheckUserAccess=true` → `ErrProxy`

#### `HasPendingTransferToken`

- `TestHasPendingTransferToken_NoToken_Integration` — no token row → `false, nil`
- `TestHasPendingTransferToken_ActiveToken_Integration` — active (unexpired) token seeded → `true, nil`
- `TestHasPendingTransferToken_Fail_Integration` — `FailGetPendingOwnershipTransferToken=true` → `ErrProxy`

#### `InsertTransferToken`

- `TestInsertTransferToken_Success_Integration` — correct params; returned `tokenID` is non-zero UUID; `ExpiresAt` is in the future; DB row has correct `target_user_id` and non-null `code_hash`; metadata JSON contains `"initiated_by"` key
- `TestInsertTransferToken_Fail_Integration` — `FailInsertOwnershipTransferToken=true` → `ErrProxy`

#### `GetPendingTransferToken`

- `TestGetPendingTransferToken_NoToken_Integration` — no row → `ErrTransferTokenInvalid`
- `TestGetPendingTransferToken_Success_Integration` — seeded active token with known `initiated_by` UUID and `code_hash` → `TokenID`, `NewOwnerID`, `CodeHash`, `InitiatedBy` all correct
- `TestGetPendingTransferToken_Fail_Integration` — `FailGetPendingOwnershipTransferToken=true` → `ErrProxy`

#### `DeletePendingTransferToken`

- `TestDeletePendingTransferToken_NoMatch_Integration` — no row for `initiatedBy` → `ErrNoPendingTransfer`
- `TestDeletePendingTransferToken_Success_Integration` — existing token with matching `initiated_by` → nil, row absent
- `TestDeletePendingTransferToken_Fail_Integration` — `FailDeletePendingOwnershipTransferToken=true` → `ErrProxy`

#### `WriteInitiateAuditLog`

- `TestWriteInitiateAuditLog_WritesRow_Integration` — after call, audit row exists with `event_type = "owner_transfer_initiated"` and `metadata->>'target_user_id'` matching the passed UUID

#### `WriteCancelAuditLog`

- `TestWriteCancelAuditLog_WritesRow_Integration` — after call, audit row exists with `event_type = "owner_transfer_cancelled"`

#### `AcceptTransferTx`

- `TestAcceptTransferTx_FailSetSkipEscalationCheck_Integration` — `FailSetSkipEscalationCheck=true` → error; no role changes
- `TestAcceptTransferTx_FailConsumeToken_Integration` — `FailConsumeOwnershipTransferToken=true` → error; no role changes
- `TestAcceptTransferTx_TokenAlreadyConsumed_Integration` — `ConsumeOwnershipTransferToken` returns 0 rows (seeded consumed token) → `ErrTransferTokenInvalid`
- `TestAcceptTransferTx_FailCheckUserAccess_Integration` — `FailCheckUserAccess=true` → error
- `TestAcceptTransferTx_InitiatorNotOwner_Integration` — `CheckUserAccess` for `PreviousOwnerID` returns `IsOwner=false` → `ErrInitiatorNotOwner`
- `TestAcceptTransferTx_FailAssignNewOwner_Integration` — `FailAssignUserRole=true` → error; `PreviousOwnerID` still has owner role
- `TestAcceptTransferTx_FailRemoveOldOwner_Integration` — `FailRemoveUserRole=true` → error; `NewOwnerID` does not have owner role
- `TestAcceptTransferTx_FailAuditLog_Integration` — `FailInsertAuditLog=true` → error; neither user's role changed
- `TestAcceptTransferTx_FailRevokeTokens_Integration` — `FailRevokeAllUserRefreshTokens=true` → error
- `TestAcceptTransferTx_Success_Integration` — all steps succeed; new owner has owner role; previous owner's role revoked; audit row with `EventOwnerTransferAccepted`; refresh tokens revoked; `transferredAt` is recent UTC timestamp

#### Structurally unreachable — do NOT add test stubs for:

- `AssignOwnerTx: begin tx error` — BeginOrBind with TxBound=true always returns nil
- `AssignOwnerTx: commit error` — Commit is a no-op on TxBound path; non-TxBound Commit is not interceptable by QuerierProxy
- `AcceptTransferTx: begin tx error` — same as AssignOwnerTx
- `AcceptTransferTx: commit error` — same as AssignOwnerTx

These already have `// Unreachable via QuerierProxy:` comments added by Fix 4. No test stubs needed.

---

## Fix 14 — Fill missing unit-test cases in `service_test.go`

**File:** `service_test.go`

Add the following test functions. All use `rbacsharedtest.OwnerFakeStorer`.
All call `t.Parallel()`.

### AssignOwner

```
TestService_AssignOwner_UserNotActiveAndNotVerified
    store returns IsActive=false, EmailVerified=false
    → must error with ErrUserNotActive (IsActive guard fires before EmailVerified guard)
```

### InitiateTransfer

```
TestService_InitiateTransfer_TargetIsOwnerBeforeActiveCheck
    store returns IsOwner=true, IsActive=false
    → must error with ErrUserIsAlreadyOwner (IsOwner guard fires before IsActive guard)

TestService_InitiateTransfer_WriteAuditLogError_NonFatal
    InsertTransferToken succeeds; WriteInitiateAuditLog returns an error
    → InitiateTransfer must still return a non-nil result and nil error
    (audit write is non-fatal per Fix 7)
```

### AcceptTransfer

```
TestService_AcceptTransfer_TargetNotActiveAtAcceptTime
    GetTransferTargetUser (re-check) returns IsActive=false, EmailVerified=true
    → ErrUserNotEligible

TestService_AcceptTransfer_TargetNotVerifiedAtAcceptTime
    GetTransferTargetUser (re-check) returns IsActive=true, EmailVerified=false
    → ErrUserNotEligible

TestService_AcceptTransfer_TargetNeitherActiveNorVerifiedAtAcceptTime
    GetTransferTargetUser (re-check) returns IsActive=false, EmailVerified=false
    → ErrUserNotEligible

TestService_AcceptTransfer_TxReturnsTransferTokenInvalid
    AcceptTransferTx returns ErrTransferTokenInvalid (token already consumed race)
    → errors.Is(err, ErrTransferTokenInvalid) must be true

TestService_AcceptTransfer_TxReturnsInitiatorNotOwner
    AcceptTransferTx returns ErrInitiatorNotOwner
    → errors.Is(err, ErrInitiatorNotOwner) must be true
```

### CancelTransfer

```
TestService_CancelTransfer_AuditLogError_NonFatal
    DeletePendingTransferToken succeeds; WriteCancelAuditLog returns an error
    → CancelTransfer must still return nil error
    (audit write is non-fatal per Fix 7)
```

---

## Fix 15 — Fill missing unit-test cases in `handler_test.go`

**File:** `handler_test.go`

### Oversized body tests (3 tests)

Per RULES.md §3.14 S-3, oversized-body tests must send **raw bytes** (not valid
JSON) so the `respond.DecodeJSON` drain path triggers a 413:

```go
func TestHandler_AssignOwner_OversizedBody(t *testing.T) {
    t.Parallel()
    h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
    w := httptest.NewRecorder()
    body := bytes.Repeat([]byte("x"), int(respond.MaxBodyBytes)+1)
    r := authedOwnerReq(t, http.MethodPost, "/owner/assign", bytes.NewReader(body))
    h.AssignOwner(w, r)
    require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestHandler_InitiateTransfer_OversizedBody(t *testing.T) { … }   // same pattern

func TestHandler_AcceptTransfer_OversizedBody(t *testing.T) { … }     // same pattern, no auth
```

### Malformed-body status should be 400, not any 4xx

Fix `TestHandler_AssignOwner_MalformedBody` and
`TestHandler_AcceptTransfer_MalformedBody` to assert `http.StatusBadRequest`
(400) instead of `w.Code >= 400 && w.Code < 500`.

### Whitespace-only secret

```go
func TestHandler_AssignOwner_WhitespaceSecret(t *testing.T) {
    t.Parallel()
    h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
    w := httptest.NewRecorder()
    r := authedOwnerReq(t, http.MethodPost, "/owner/assign",
        jsonOwnerBuf(t, map[string]any{"secret": "   "}))
    h.AssignOwner(w, r)
    require.Equal(t, http.StatusUnprocessableEntity, w.Code)
    assert.Equal(t, "validation_error", decodeOwnerBody(t, w)["code"])
}
```

### Whitespace-only token for AcceptTransfer

```go
func TestHandler_AcceptTransfer_WhitespaceToken(t *testing.T) {
    t.Parallel()
    h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
    w := httptest.NewRecorder()
    r := httptest.NewRequest(http.MethodPost, "/owner/transfer/accept",
        jsonOwnerBuf(t, map[string]any{"token": "   "}))
    h.AcceptTransfer(w, r)
    require.Equal(t, http.StatusUnprocessableEntity, w.Code)
    assert.Equal(t, "validation_error", decodeOwnerBody(t, w)["code"])
}
```

### Unauthenticated AcceptTransfer succeeds (route-level verification)

AcceptTransfer is documented as "no JWT required". Add a test that confirms a
request with **no JWT context** still reaches the service (or at minimum does
not return 401):

```go
func TestHandler_AcceptTransfer_NoAuthAllowed(t *testing.T) {
    t.Parallel()
    svc := &rbacsharedtest.OwnerFakeServicer{
        AcceptTransferFn: func(_ context.Context, _ owner.AcceptInput) (owner.AcceptResult, error) {
            return sampleAcceptResult(), nil
        },
    }
    h := newHandler(svc)
    w := httptest.NewRecorder()
    // Deliberately use httptest.NewRequest (no injected JWT context).
    r := httptest.NewRequest(http.MethodPost, "/owner/transfer/accept",
        jsonOwnerBuf(t, map[string]any{"token": "valid-token"}))
    h.AcceptTransfer(w, r)
    require.Equal(t, http.StatusOK, w.Code)
}
```

---

## Fix 16 — Verify InitiateTransfer success response shape uses the new response type

After Fix 2b, `TestHandler_InitiateTransfer_Success` currently asserts
`decodeOwnerBody(t, w)["target_user_id"]`. The field name in `initiateResponse`
is still `"target_user_id"`, so this test continues to pass unchanged.

However, ensure `sampleInitiateResult()` in `handler_test.go` is updated to
no longer set fields that no longer exist with json tags on `InitiateResult`.
Since `InitiateResult` is now a models.go type with no json tags and the handler
maps to `initiateResponse`, `sampleInitiateResult()` returns the service type —
no json-tag dependency. No change needed to `sampleInitiateResult()` itself; just
verify it still compiles.

---

## Summary of files changed

| File | Change |
|---|---|
| `handler.go` | Package doc comment; mailer fields + NewHandler signature; InitiateTransfer mailer enqueue; assignOwnerResponse + initiateResponse + acceptResponse mapping; Unreachable comments on uuid.Parse; platform/rbac import removed; mailertemplates import added |
| `service.go` | transferTokenBcryptCost var; generateTransferToken uses var; context.WithoutCancel on AcceptTransferTx; slog.ErrorContext on audit log writes; TargetEmail in InitiateResult return; platform/rbac import removed; ErrOwnerAlreadyExists from local errors.go |
| `store.go` | Canonical WithQuerier; Unreachable comments on BeginOrBind+Commit; WithActingUser on step 5 of AcceptTransferTx; ActingUserID consumed from input |
| `models.go` | json tags stripped from InitiateResult and AcceptResult; AssignOwnerResult moved here with tags stripped; TargetEmail added to InitiateResult; ActingUserID added to AcceptTransferTxInput |
| `requests.go` | assignOwnerResponse, initiateResponse, acceptResponse added; old AssignOwnerResult removed |
| `errors.go` | ErrOwnerAlreadyExists var added (wraps platform/rbac sentinel); platform/rbac import added |
| `export_test.go` | SetTransferTokenBcryptCostForTest added; NewHandlerForTest nil-mailer guard |
| `routes.go` | NewHandler call updated with deps.Mailer, deps.MailQueue |
| `service_test.go` | TestMain added; mustBcrypt removed → rbacsharedtest.MustTokenHash; new test cases from Fix 14 |
| `handler_test.go` | Oversized body tests; malformed-body status pinned to 400; whitespace secret; whitespace token; unauthenticated AcceptTransfer test |
| `store_test.go` | **New file** — full integration test suite per Fix 13 |
| `internal/domain/rbac/shared/testutil/builders.go` | MustTokenHash helper added |
