# Phase 11 — User Lock Management

**Target package:** `internal/domain/rbac/userlock/`
**Routes:** 18 (POST lock), 19 (DELETE unlock), 20 (GET lock status)
**Analogue:** `internal/domain/rbac/userpermissions/` — follow every pattern exactly

Load `docs/prompts/rbac/context.md` for full resolved context before starting.

---

## What to build

Three HTTP endpoints that allow admins (with owner approval) to admin-lock and
admin-unlock user accounts, and allow any user with `user:read` to check lock
status.

```
POST   /admin/users/{user_id}/lock    JWT + user:lock   → 204 (locked)
DELETE /admin/users/{user_id}/lock    JWT + user:lock   → 204 (unlocked)
GET    /admin/users/{user_id}/lock    JWT + user:read   → 200 (lock status JSON)
```

Lock data lives in `user_secrets` (not `users`). The three generated DB methods
are already available — **do not add SQL or run `sqlc generate`**.

---

## Files to create

```
internal/domain/rbac/userlock/
    models.go
    errors.go
    validators.go
    requests.go
    service.go
    store.go
    handler.go
    routes.go
    export_test.go
    handler_test.go
    service_test.go
    store_test.go
    validators_test.go
```

## Files to modify

```
internal/domain/rbac/routes.go
    → add userlock import + userlock.Routes(ctx, r, deps) in adminRoutes()

internal/domain/rbac/shared/testutil/fake_storer.go
    → append UserLockFakeStorer

internal/domain/rbac/shared/testutil/fake_servicer.go
    → append UserLockFakeServicer

internal/domain/rbac/shared/testutil/querier_proxy.go
    → add Fail* flags + method overrides for LockUser, UnlockUser, GetUserLockStatus
```

---

## 1. models.go

```go
package userlock

import "time"

// LockUserInput is the service-layer input for POST /lock.
type LockUserInput struct {
    Reason string
}

// LockUserTxInput is the store-layer input with parsed [16]byte IDs.
type LockUserTxInput struct {
    UserID   [16]byte
    LockedBy [16]byte
    Reason   string
}

// UserLockStatus is the service-layer representation of a user's lock state.
type UserLockStatus struct {
    UserID           string
    AdminLocked      bool
    LockedBy         *string    // nil when not locked
    LockedReason     *string    // nil when not locked
    LockedAt         *time.Time // nil when not locked
    IsLocked         bool       // OTP lock — separate from admin lock
    LoginLockedUntil *time.Time // nil when OTP lock not active
}
```

---

## 2. errors.go

```go
package userlock

import "errors"

// ErrUserNotFound is returned when the target user_id does not exist or is
// deleted. Applies to all three operations (lock, unlock, get status).
var ErrUserNotFound = errors.New("user not found")

// ErrReasonRequired is returned when LockUser receives an empty reason string.
var ErrReasonRequired = errors.New("reason is required")
```

Note: `ErrCannotLockOwner` and `ErrCannotLockSelf` live in
`internal/platform/rbac/errors.go` — import and reuse them.

---

## 3. validators.go

```go
package userlock

import "strings"

func validateLockUser(in LockUserInput) error {
    if strings.TrimSpace(in.Reason) == "" {
        return ErrReasonRequired
    }
    return nil
}
```

---

## 4. requests.go

HTTP request / response structs.

```go
package userlock

import "time"

// ── HTTP request ──────────────────────────────────────────────────────────────

type lockUserRequest struct {
    Reason string `json:"reason"`
}

// ── HTTP response ─────────────────────────────────────────────────────────────

type userLockStatusResponse struct {
    UserID           string     `json:"user_id"`
    AdminLocked      bool       `json:"admin_locked"`
    LockedBy         *string    `json:"locked_by,omitempty"`
    LockedReason     *string    `json:"locked_reason,omitempty"`
    LockedAt         *time.Time `json:"locked_at,omitempty"`
    IsLocked         bool       `json:"is_locked"`
    LoginLockedUntil *time.Time `json:"login_locked_until,omitempty"`
}

// ── Mapper ────────────────────────────────────────────────────────────────────

func toLockStatusResponse(s UserLockStatus) userLockStatusResponse {
    return userLockStatusResponse{
        UserID:           s.UserID,
        AdminLocked:      s.AdminLocked,
        LockedBy:         s.LockedBy,
        LockedReason:     s.LockedReason,
        LockedAt:         s.LockedAt,
        IsLocked:         s.IsLocked,
        LoginLockedUntil: s.LoginLockedUntil,
    }
}
```

---

## 5. service.go

### Storer interface

```go
type Storer interface {
    // IsOwnerUser returns true when userID holds a role with is_owner_role = TRUE.
    // Returns false (not true + error) when the user has no role assignment.
    IsOwnerUser(ctx context.Context, userID [16]byte) (bool, error)

    // GetLockStatus returns the full lock state for userID.
    // Returns ErrUserNotFound when the user does not exist or is deleted.
    GetLockStatus(ctx context.Context, userID [16]byte) (UserLockStatus, error)

    // LockUserTx sets admin_locked = TRUE with metadata in user_secrets.
    // Must be called after IsOwnerUser and self-lock guards pass.
    LockUserTx(ctx context.Context, in LockUserTxInput) error

    // UnlockUser clears admin_locked and all metadata in user_secrets.
    UnlockUser(ctx context.Context, userID [16]byte, actingUserID string) error
}
```

### Service type

```go
type Service struct { store Storer }
func NewService(store Storer) *Service { return &Service{store: store} }
```

### LockUser guards (in order)

1. Parse `targetUserID` → `ErrUserNotFound` on bad UUID
2. Parse `actingUserID` (from JWT) → wrapped 500 error on bad UUID
3. Check `targetUserID == actingUserID` → `platformrbac.ErrCannotLockSelf`
4. `validateLockUser(in)` → `ErrReasonRequired`
5. `store.IsOwnerUser(ctx, targetID)` → if true → `platformrbac.ErrCannotLockOwner`
6. `store.GetLockStatus(ctx, targetID)` → `ErrUserNotFound` (existence gate)
7. `store.LockUserTx(ctx, LockUserTxInput{...})`

### UnlockUser guards (in order)

1. Parse `targetUserID` → `ErrUserNotFound` on bad UUID
2. Parse `actingUserID` (from JWT) → wrapped 500 error on bad UUID
3. `store.GetLockStatus(ctx, targetID)` → `ErrUserNotFound` (existence gate)
4. `store.UnlockUser(ctx, targetID, actingUserID)`

### GetLockStatus guards (in order)

1. Parse `targetUserID` → `ErrUserNotFound` on bad UUID
2. `store.GetLockStatus(ctx, targetID)` → `ErrUserNotFound`

---

## 6. store.go

### Store type

```go
// Store is the data-access implementation for the userlock package.
type Store struct {
    rbacshared.BaseStore
}

func NewStore(pool *pgxpool.Pool) *Store {
    return &Store{BaseStore: rbacshared.NewBaseStore(pool)}
}

// WithQuerier returns a copy of the Store with its querier replaced by q.
// Used in integration tests to bind writes to a rolled-back test transaction.
func (s *Store) WithQuerier(q db.Querier) *Store {
    c := *s
    c.BaseStore = s.BaseStore.WithQuerier(q)
    return &c
}
```

### IsOwnerUser

Calls `s.Queries.GetUserRole(ctx, s.ToPgtypeUUID(userID))`.
- Returns `false, nil` on `pgx.ErrNoRows` (no role = not owner).
- Returns `row.IsOwnerRole, nil` otherwise.
- Wraps any other error.

### GetLockStatus

Calls `s.Queries.GetUserLockStatus(ctx, s.ToPgtypeUUID(userID))`.
- Returns `ErrUserNotFound` on `IsNoRows(err)`.
- Maps `GetUserLockStatusRow` → `UserLockStatus`:
  - `AdminLockedBy pgtype.UUID` → `*string` (nil when not valid)
  - `AdminLockedReason pgtype.Text` → `*string` (nil when not valid)
  - `AdminLockedAt pgtype.Timestamptz` → `*time.Time` (nil when not valid)
  - `LoginLockedUntil pgtype.Timestamptz` → `*time.Time` (nil when not valid)
  - `ID uuid.UUID` → `string` via `uuid.UUID.String()`

### LockUserTx

Uses `s.WithActingUser(ctx, actingUserIDString, func() error { ... })` to set
`rbac.acting_user` for the audit trigger.

Calls `s.Queries.LockUser(ctx, db.LockUserParams{
    LockedBy: s.ToPgtypeUUID(in.LockedBy),
    Reason:   pgtype.Text{String: in.Reason, Valid: true},
    UserID:   s.ToPgtypeUUID(in.UserID),
})`.

### UnlockUser

Uses `s.WithActingUser(ctx, actingUserID, func() error { ... })`.
Calls `s.Queries.UnlockUser(ctx, s.ToPgtypeUUID(userID))`.
Uses `context.WithoutCancel(ctx)` inside the acting-user closure so a
client disconnect cannot abort the unlock mid-flight.

---

## 7. handler.go

### Servicer interface

```go
type Servicer interface {
    LockUser(ctx context.Context, targetUserID, actingUserID string, in LockUserInput) error
    UnlockUser(ctx context.Context, targetUserID, actingUserID string) error
    GetLockStatus(ctx context.Context, targetUserID string) (UserLockStatus, error)
}
```

### Handler type

```go
type Handler struct { svc Servicer }
func NewHandler(svc Servicer) *Handler { return &Handler{svc: svc} }
```

### LockUser — POST /admin/users/{user_id}/lock → 204

1. `http.MaxBytesReader` on body.
2. `chi.URLParam(r, "user_id")`.
3. `mustUserID(w, r)` → actingUserID.
4. `respond.DecodeJSON[lockUserRequest](w, r)`.
5. `svc.LockUser(ctx, userID, actingUserID, LockUserInput{Reason: req.Reason})`.
6. On success → `respond.NoContent(w)`.
7. On error → `writeLockError(w, r, err)`.

### UnlockUser — DELETE /admin/users/{user_id}/lock → 204

1. `chi.URLParam(r, "user_id")`.
2. `mustUserID(w, r)` → actingUserID.
3. `svc.UnlockUser(ctx, userID, actingUserID)`.
4. On success → `respond.NoContent(w)`.
5. On error → `writeLockError(w, r, err)`.

### GetLockStatus — GET /admin/users/{user_id}/lock → 200

1. `chi.URLParam(r, "user_id")`.
2. `svc.GetLockStatus(ctx, userID)`.
3. On success → `respond.JSON(w, http.StatusOK, toLockStatusResponse(status))`.
4. On error → `writeLockError(w, r, err)`.

### writeLockError — single error switch

```
ErrUserNotFound          → 404 "user_not_found"
ErrReasonRequired        → 422 "reason_required"
ErrCannotLockSelf        → 409 "cannot_lock_self"
ErrCannotLockOwner       → 409 "cannot_lock_owner"
default                  → 500 "internal_error" + slog.ErrorContext
```

Import `ErrCannotLockSelf` and `ErrCannotLockOwner` from `internal/platform/rbac`.

### mustUserID (private)

Same pattern as `userpermissions`: extracts acting userID from JWT context via
`token.UserIDFromContext`; writes 401 and returns `("", false)` if absent.

---

## 8. routes.go

```go
package userlock

import (
    "context"

    "github.com/7-Dany/store/backend/internal/app"
    platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
    "github.com/go-chi/chi/v5"
)

func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
    store := NewStore(deps.Pool)
    svc   := NewService(store)
    h     := NewHandler(svc)

    r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermUserLock)).
        Post("/users/{user_id}/lock", h.LockUser)

    r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermUserLock)).
        Delete("/users/{user_id}/lock", h.UnlockUser)

    r.With(deps.JWTAuth, deps.RBAC.Require(platformrbac.PermUserRead)).
        Get("/users/{user_id}/lock", h.GetLockStatus)
}
```

---

## 9. export_test.go

Export unexported helpers for white-box unit tests:

```go
package userlock

var ValidateLockUser = validateLockUser
```

---

## 10. Modifications to shared/testutil

### fake_storer.go — append UserLockFakeStorer

```go
// UserLockFakeStorer is a hand-written implementation of userlock.Storer
// for service unit tests.
//
// Defaults:
//   IsOwnerUserFn  → (false, nil)  — non-owner; lock guards pass
//   GetLockStatusFn → (UserLockStatus{}, nil)
//   LockUserTxFn   → nil
//   UnlockUserFn   → nil
type UserLockFakeStorer struct {
    IsOwnerUserFn   func(ctx context.Context, userID [16]byte) (bool, error)
    GetLockStatusFn func(ctx context.Context, userID [16]byte) (userlock.UserLockStatus, error)
    LockUserTxFn    func(ctx context.Context, in userlock.LockUserTxInput) error
    UnlockUserFn    func(ctx context.Context, userID [16]byte, actingUserID string) error
}

var _ userlock.Storer = (*UserLockFakeStorer)(nil)

func (f *UserLockFakeStorer) IsOwnerUser(ctx context.Context, userID [16]byte) (bool, error) {
    if f.IsOwnerUserFn != nil { return f.IsOwnerUserFn(ctx, userID) }
    return false, nil
}
func (f *UserLockFakeStorer) GetLockStatus(ctx context.Context, userID [16]byte) (userlock.UserLockStatus, error) {
    if f.GetLockStatusFn != nil { return f.GetLockStatusFn(ctx, userID) }
    return userlock.UserLockStatus{}, nil
}
func (f *UserLockFakeStorer) LockUserTx(ctx context.Context, in userlock.LockUserTxInput) error {
    if f.LockUserTxFn != nil { return f.LockUserTxFn(ctx, in) }
    return nil
}
func (f *UserLockFakeStorer) UnlockUser(ctx context.Context, userID [16]byte, actingUserID string) error {
    if f.UnlockUserFn != nil { return f.UnlockUserFn(ctx, userID, actingUserID) }
    return nil
}
```

### fake_servicer.go — append UserLockFakeServicer

```go
// UserLockFakeServicer implements userlock.Servicer for handler unit tests.
//
// Defaults:
//   LockUserFn      → nil
//   UnlockUserFn    → nil
//   GetLockStatusFn → (UserLockStatus{}, nil)
type UserLockFakeServicer struct {
    LockUserFn      func(ctx context.Context, targetUserID, actingUserID string, in userlock.LockUserInput) error
    UnlockUserFn    func(ctx context.Context, targetUserID, actingUserID string) error
    GetLockStatusFn func(ctx context.Context, targetUserID string) (userlock.UserLockStatus, error)
}

var _ userlock.Servicer = (*UserLockFakeServicer)(nil)

func (f *UserLockFakeServicer) LockUser(ctx context.Context, targetUserID, actingUserID string, in userlock.LockUserInput) error {
    if f.LockUserFn != nil { return f.LockUserFn(ctx, targetUserID, actingUserID, in) }
    return nil
}
func (f *UserLockFakeServicer) UnlockUser(ctx context.Context, targetUserID, actingUserID string) error {
    if f.UnlockUserFn != nil { return f.UnlockUserFn(ctx, targetUserID, actingUserID) }
    return nil
}
func (f *UserLockFakeServicer) GetLockStatus(ctx context.Context, targetUserID string) (userlock.UserLockStatus, error) {
    if f.GetLockStatusFn != nil { return f.GetLockStatusFn(ctx, targetUserID) }
    return userlock.UserLockStatus{}, nil
}
```

### querier_proxy.go — add lock Fail* fields and overrides

In the `QuerierProxy` struct, add under a new `// ── user lock ──` comment:

```go
// ── user lock ─────────────────────────────────────────────────────────────────
FailLockUser          bool
FailUnlockUser        bool
FailGetUserLockStatus bool
```

Add the three method overrides:

```go
func (p *QuerierProxy) LockUser(ctx context.Context, arg db.LockUserParams) error {
    if p.FailLockUser { return ErrProxy }
    return p.Querier.LockUser(ctx, arg)
}

func (p *QuerierProxy) UnlockUser(ctx context.Context, userID pgtype.UUID) error {
    if p.FailUnlockUser { return ErrProxy }
    return p.Querier.UnlockUser(ctx, userID)
}

func (p *QuerierProxy) GetUserLockStatus(ctx context.Context, userID pgtype.UUID) (db.GetUserLockStatusRow, error) {
    if p.FailGetUserLockStatus { return db.GetUserLockStatusRow{}, ErrProxy }
    return p.Querier.GetUserLockStatus(ctx, userID)
}
```

---

## 11. Modification to routes.go (domain assembler)

In `internal/domain/rbac/routes.go`, import `userlock` and add the mount in `adminRoutes`:

```go
import "github.com/7-Dany/store/backend/internal/domain/rbac/userlock"

// in adminRoutes():
userlock.Routes(ctx, r, deps)
```

---

## 12. Test cases

### handler_test.go (unit, no build tag)

Use `rbacsharedtest.UserLockFakeServicer`. Inject chi URL params and JWT user ID
exactly as done in `userpermissions/handler_test.go`.

| ID | Method | Scenario | Expected |
|----|--------|----------|----------|
| T-R45h | POST /lock | success | 204 |
| T-R45i | POST /lock | reason empty | 422 reason_required |
| T-R45j | POST /lock | svc returns ErrCannotLockSelf | 409 cannot_lock_self |
| T-R45k | POST /lock | svc returns ErrCannotLockOwner | 409 cannot_lock_owner |
| T-R45l | POST /lock | svc returns ErrUserNotFound | 404 user_not_found |
| T-R45m | POST /lock | no JWT auth | 401 unauthorized |
| T-R45n | POST /lock | svc returns generic error | 500 internal_error |
| T-R45o | POST /lock | malformed JSON body | 4xx |
| T-R46h | DELETE /lock | success | 204 |
| T-R46i | DELETE /lock | svc returns ErrUserNotFound | 404 user_not_found |
| T-R46j | DELETE /lock | no JWT auth | 401 unauthorized |
| T-R46k | DELETE /lock | svc returns generic error | 500 internal_error |
| T-R47h | GET /lock | success — returns JSON body with user_id | 200 |
| T-R47i | GET /lock | svc returns ErrUserNotFound | 404 user_not_found |
| T-R47j | GET /lock | svc returns generic error | 500 internal_error |

### service_test.go (unit, no build tag)

Use `rbacsharedtest.UserLockFakeStorer`.

| ID | Method | Scenario | Expected |
|----|--------|----------|----------|
| T-R45s | LockUser | invalid targetUserID UUID | ErrUserNotFound |
| T-R45t | LockUser | target == actor (self-lock) | ErrCannotLockSelf |
| T-R45u | LockUser | reason empty | ErrReasonRequired |
| T-R45v | LockUser | IsOwnerUser returns true | ErrCannotLockOwner |
| T-R45w | LockUser | GetLockStatus returns ErrUserNotFound | ErrUserNotFound |
| T-R45x | LockUser | store.LockUserTx propagates error | wrapped error |
| T-R45y | LockUser | success path — LockUserTx receives correct input | nil |
| T-R45z | LockUser | invalid actingUserID UUID | non-nil wrapped error (not ErrUserNotFound) |
| T-R46s | UnlockUser | invalid targetUserID UUID | ErrUserNotFound |
| T-R46t | UnlockUser | GetLockStatus returns ErrUserNotFound | ErrUserNotFound |
| T-R46u | UnlockUser | store.UnlockUser propagates error | wrapped error |
| T-R46v | UnlockUser | success path | nil |
| T-R47s | GetLockStatus | invalid targetUserID UUID | ErrUserNotFound |
| T-R47t | GetLockStatus | store returns ErrUserNotFound | ErrUserNotFound |
| T-R47u | GetLockStatus | success — returns status | nil + correct UserLockStatus |

### store_test.go (integration, `//go:build integration_test`)

Follow the same `TestMain` + `txStores` + `withProxy` pattern as
`userpermissions/store_test.go`.

| ID | Scenario | Expected |
|----|----------|----------|
| T-R45i | LockUserTx sets admin_locked = TRUE; GetLockStatus reflects it | AdminLocked = true |
| T-R46i | UnlockUser clears admin_locked; GetLockStatus reflects it | AdminLocked = false, LockedBy = nil |
| T-R47i | GetLockStatus returns ErrUserNotFound for non-existent user | ErrUserNotFound |
| T-R48i | LockUser on owner-role user triggers guard at service level (unit only) | — |
| T-R49i | IsOwnerUser returns true for user with owner role | true |
| T-R49j | IsOwnerUser returns false for user with no role | false |
| T-R45p | FailLockUser proxy → ErrProxy | ErrProxy |
| T-R46p | FailUnlockUser proxy → ErrProxy | ErrProxy |
| T-R47p | FailGetUserLockStatus proxy → ErrProxy | ErrProxy |
| T-R49p | FailGetUserRole proxy → ErrProxy in IsOwnerUser | ErrProxy |

**Important:** `LockUser` DB query has `chk_us_no_self_lock` constraint. In
integration tests, ensure `LockedBy != UserID` — use two separate test users.

### validators_test.go (unit, no build tag)

```go
TestValidateLockUser_ReasonEmpty     → ErrReasonRequired
TestValidateLockUser_ReasonWhitespace → ErrReasonRequired
TestValidateLockUser_Valid            → nil
```

---

## 13. Conventions checklist

- [ ] `var _ Storer = (*Store)(nil)` compile-time check in `store.go`
- [ ] `var _ Servicer = (*Service)(nil)` — not required (handler holds the interface)
- [ ] Handler never imports `store.go` types directly — only `models.go` types
- [ ] `context.WithoutCancel(ctx)` inside `UnlockUser` store method (client-disconnect safety)
- [ ] `WithActingUser` used in both `LockUserTx` and `UnlockUser`
- [ ] `slog.ErrorContext` in every `default:` branch in `writeLockError`
- [ ] No raw string permission literals — always `platformrbac.PermUserLock` etc.
- [ ] `http.MaxBytesReader` on POST body before `DecodeJSON`
- [ ] `store_test.go` has `//go:build integration_test` tag on first line
- [ ] All `fake_storer.go` / `fake_servicer.go` additions have compile-time `var _ Interface = (*Fake)(nil)` checks
- [ ] `querier_proxy.go` `GetUserRole` override must already exist (used by `IsOwnerUser`) — verify before adding new ones

---

## 14. Do not

- Do not modify `sql/queries/rbac.sql` — all three lock queries (`LockUser`,
  `UnlockUser`, `GetUserLockStatus`) are already generated.
- Do not add `is_admin_locked` guards to `auth/login` or `auth/unlock` — that is
  Phase 12's responsibility.
- Do not add a Redis invalidation call for admin-locked users — Phase 12.
- Do not add `user_secrets` directly to `db.Querier` — it is already there via
  `LockUser`, `UnlockUser`, `GetUserLockStatus`.
- Do not import `userroles` from `userlock` — call `GetUserRole` directly via
  `s.Queries.GetUserRole(ctx, ...)` in `IsOwnerUser` store method.
