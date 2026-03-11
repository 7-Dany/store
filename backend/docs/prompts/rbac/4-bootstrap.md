# RBAC — Phase 4: Bootstrap Domain (`internal/domain/rbac/bootstrap/`)

**Feature:** RBAC
**Phase:** 4 of 10
**Depends on:** Phases 0–3 (schema ✅, queries ✅, seeds ✅, platform/rbac ✅)
**Gate:** `go test -tags integration_test ./internal/domain/rbac/bootstrap/...` green — T-R18 through T-R22
**Design doc:** `docs/prompts/rbac/0-design.md`
**Go version:** 1.25 — use modern idioms throughout (`any`, `min`/`max`, range-over-int, etc.)

---

## What this phase builds

```
internal/domain/rbac/
    routes.go                        NEW — assembles /owner and /admin sub-routers
    bootstrap/
        handler.go                   NEW
        service.go                   NEW
        store.go                     NEW
        models.go                    NEW
        routes.go                    NEW
        validators.go                NEW
        handler_test.go              NEW — T-R18 through T-R22
```

`internal/server/routes.go` is also modified to mount the new sub-routers.

---

## Read before writing any code

1. `docs/prompts/rbac/0-design.md` — §8 (API), §10 (Bootstrap flow), §11 (decisions D-R4)
2. `internal/platform/rbac/errors.go` — `ErrOwnerAlreadyExists` sentinel
3. `internal/db/rbac.sql.go` — exact signatures for `CountActiveOwners`, `GetOwnerRoleID`, `GetActiveUserByID`, `AssignUserRole`
4. `internal/domain/auth/login/handler.go` — follow handler/service/store structural pattern
5. `internal/domain/auth/login/routes.go` — follow Routes() constructor pattern
6. `internal/domain/auth/shared/testutil/builders.go` — `RunTestMain`, `MustBeginTx`, `CreateUser` helpers used in integration tests
7. `internal/platform/ratelimit/ip_limiter.go` — `NewIPRateLimiter` constructor + `StartCleanup` pattern
8. `internal/server/routes.go` — where to mount the new routers

---

## Generated DB types (reference)

```go
// CountActiveOwners returns int64 — number of active owner assignments.
func (q *Queries) CountActiveOwners(ctx context.Context) (int64, error)

// GetOwnerRoleID returns uuid.UUID — the owner role's primary key.
func (q *Queries) GetOwnerRoleID(ctx context.Context) (uuid.UUID, error)

// GetActiveUserByIDRow — returned by GetActiveUserByID.
type GetActiveUserByIDRow struct {
    ID            uuid.UUID   `db:"id"`
    Email         pgtype.Text `db:"email"`
    IsActive      bool        `db:"is_active"`
    EmailVerified bool        `db:"email_verified"`
}

// AssignUserRoleParams — input to AssignUserRole.
type AssignUserRoleParams struct {
    UserID        pgtype.UUID
    RoleID        pgtype.UUID
    GrantedBy     pgtype.UUID
    GrantedReason string
    ExpiresAt     pgtype.Timestamptz  // set Valid=false for permanent grant
}

// AssignUserRoleRow — returned by AssignUserRole.
type AssignUserRoleRow struct {
    UserID    pgtype.UUID
    RoleID    pgtype.UUID
    ExpiresAt pgtype.Timestamptz
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

---

## `models.go`

```go
// BootstrapInput carries the validated user_id from the request body.
type BootstrapInput struct {
    UserID string
}

// BootstrapResult is returned on success and written as the JSON response body.
type BootstrapResult struct {
    UserID    string    `json:"user_id"`
    RoleName  string    `json:"role_name"`
    GrantedAt time.Time `json:"granted_at"`
}
```

---

## `validators.go`

One function, validates the request body struct:

```go
// validateBootstrapRequest validates the decoded request body in-place.
// Returns an error if user_id is absent or not a valid UUID string.
func validateBootstrapRequest(req *bootstrapRequest) error
```

`bootstrapRequest` is an unexported struct with a single `UserID string \`json:"user_id"\`` field.

Validation rules:
- `user_id` must be non-empty after trimming.
- `user_id` must parse as a valid UUID (`uuid.Parse`).

Return `authshared.ErrUserIDEmpty` when blank. Return a plain `fmt.Errorf("user_id must be a valid UUID")` when not parseable.

---

## `store.go`

### Interface

```go
// Storer is the data-access contract for the bootstrap service.
type Storer interface {
    CountActiveOwners(ctx context.Context) (int64, error)
    GetOwnerRoleID(ctx context.Context) (pgtype.UUID, error)
    GetActiveUserByID(ctx context.Context, userID pgtype.UUID) (BootstrapUser, error)
    BootstrapOwnerTx(ctx context.Context, in BootstrapTxInput) (BootstrapResult, error)
}
```

`BootstrapUser` is an unexported intermediate type:

```go
type BootstrapUser struct {
    ID            string
    Email         string
    IsActive      bool
    EmailVerified bool
}
```

`BootstrapTxInput`:

```go
type BootstrapTxInput struct {
    UserID  pgtype.UUID
    RoleID  pgtype.UUID
}
```

### `Store` implementation

```go
type Store struct {
    authshared.BaseStore
}

func NewStore(pool *pgxpool.Pool) *Store

func (s *Store) WithQuerier(q db.Querier) *Store
```

**`CountActiveOwners`** — delegates to `s.Queries.CountActiveOwners`.

**`GetOwnerRoleID`** — delegates to `s.Queries.GetOwnerRoleID`. Wraps the returned `uuid.UUID` into a `pgtype.UUID{Bytes: id, Valid: true}` before returning.

**`GetActiveUserByID`** — delegates to `s.Queries.GetActiveUserByID`. Maps `GetActiveUserByIDRow` to `BootstrapUser`. On no-rows returns `authshared.ErrUserNotFound`.

**`BootstrapOwnerTx`** — runs in a single transaction via `s.BeginOrBind`:
1. `AssignUserRole` with `GrantedBy = in.UserID` (self-grant — bootstrap only), `GrantedReason = "system bootstrap"`, `ExpiresAt = pgtype.Timestamptz{Valid: false}` (permanent).
2. Returns `BootstrapResult{UserID: …, RoleName: "owner", GrantedAt: row.CreatedAt}`.

On any error: rollback and wrap with `fmt.Errorf("store.BootstrapOwnerTx: …: %w", err)`.

---

## `service.go`

### Interface (for handler)

```go
type Servicer interface {
    Bootstrap(ctx context.Context, in BootstrapInput) (BootstrapResult, error)
}
```

### `Service` struct

```go
type Service struct {
    store Storer
}

func NewService(store Storer) *Service
```

### `Bootstrap` logic

Perform these steps **in order**:

1. Parse `in.UserID` as `pgtype.UUID`. On failure return `rbac.ErrOwnerAlreadyExists` — wait, no. The handler already validated the UUID in the validator. The service should still guard: parse it, return a wrapped `fmt.Errorf` if invalid (should be unreachable from the handler).

2. `store.CountActiveOwners` — if `count > 0`, return `rbac.ErrOwnerAlreadyExists`.

3. `store.GetOwnerRoleID` — on error wrap and return.

4. `store.GetActiveUserByID(ctx, userPgUUID)`:
   - On `authshared.ErrUserNotFound` → return `ErrUserNotFound` (sentinel from authshared).
   - On other error → wrap and return.
   - If `!user.IsActive` → return `ErrUserNotActive`.
   - If `!user.EmailVerified` → return `ErrUserNotVerified`.

5. `store.BootstrapOwnerTx` — on error wrap and return.

6. Return the result.

Add two new sentinel errors to this package's `errors.go` (not to `platform/rbac`):

```go
// package-local sentinels (bootstrap/errors.go or inline in service.go)
var ErrUserNotActive   = errors.New("user account is not active")
var ErrUserNotVerified = errors.New("user email address is not verified")
```

These are domain-level guards, not platform-level sentinels.

---

## `handler.go`

```go
type Handler struct {
    svc Servicer
}

func NewHandler(svc Servicer) *Handler
```

**`Bootstrap`** handles `POST /owner/bootstrap`.

Steps:
1. `r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)`
2. `respond.DecodeJSON[bootstrapRequest](w, r)` — returns on failure.
3. `validateBootstrapRequest(&req)` — on error: `respond.Error(w, 422, "validation_error", err.Error())`.
4. Call `h.svc.Bootstrap(r.Context(), BootstrapInput{UserID: req.UserID})`.
5. Map errors to responses (see table below).
6. On success: `respond.JSON(w, 201, result)`.

Error mapping:

| Sentinel | Status | Code | Message |
|---|---|---|---|
| `rbac.ErrOwnerAlreadyExists` | 409 | `"owner_already_exists"` | `"an active owner already exists"` |
| `authshared.ErrUserNotFound` | 422 | `"user_not_found"` | `"no active user found with the given user_id"` |
| `ErrUserNotActive` | 422 | `"user_not_active"` | `"user account is not active"` |
| `ErrUserNotVerified` | 422 | `"email_not_verified"` | `"user email address must be verified before bootstrapping"` |
| any other | 500 | `"internal_error"` | `"internal server error"` |

Log 500 errors before responding:
```go
slog.ErrorContext(r.Context(), "bootstrap.Bootstrap: service error", "error", err)
```

---

## `routes.go`

```go
// Routes registers POST /bootstrap on r.
// Call from the owner sub-router assembler:
//
//   bootstrap.Routes(ctx, r, deps)
//
// Rate limit: 3 req / 15 min per IP.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps)
```

Rate limiter setup:
```go
ipLimiter := ratelimit.NewIPRateLimiter(
    deps.KVStore, "bstrp:ip:",
    3.0/(15*60), 3,
    15*time.Minute,
)
go ipLimiter.StartCleanup(ctx)
```

Wiring:
```go
store := NewStore(deps.Pool)
svc   := NewService(store)
h     := NewHandler(svc)

r.With(ipLimiter.Limit).Post("/bootstrap", h.Bootstrap)
```

No `JWTAuth` — this route is intentionally unauthenticated.

---

## `internal/domain/rbac/routes.go`

The top-level domain assembler. Exports two functions:

```go
// OwnerRoutes returns the /owner sub-router (unauthenticated).
func OwnerRoutes(ctx context.Context, deps *app.Deps) *chi.Mux

// AdminRoutes returns the /admin sub-router (JWT-auth required on all routes).
// Phases 5–9 will mount sub-routers here.
func AdminRoutes(ctx context.Context, deps *app.Deps) *chi.Mux
```

`OwnerRoutes`:
```go
r := chi.NewRouter()
r.Use(chimiddleware.AllowContentType("application/json"))
bootstrap.Routes(ctx, r, deps)
return r
```

`AdminRoutes`:
```go
r := chi.NewRouter()
r.Use(chimiddleware.AllowContentType("application/json"))
// Phases 5–9 will mount here.
return r
```

---

## `internal/server/routes.go` — modification

Add the import and mount the new sub-routers inside `/api/v1`:

```go
import rbacdomain "github.com/7-Dany/store/backend/internal/domain/rbac"

// inside r.Route("/api/v1", ...) :
r.Mount("/owner", rbacdomain.OwnerRoutes(ctx, deps))
r.Mount("/admin", rbacdomain.AdminRoutes(ctx, deps))
```

---

## `handler_test.go`

### Build tag and package

```go
//go:build integration_test

package bootstrap_test
```

### TestMain

```go
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
    authsharedtest.RunTestMain(m, &testPool, 20)
}
```

### Test setup pattern

Each test:
- Uses `authsharedtest.MustBeginTx(t, testPool)` → `tx, q`.
- Creates a real user via `authsharedtest.CreateUser(t, testPool, q)` (email-verified, active).
- Constructs `store := bootstrap.NewStore(testPool).WithQuerier(q)`.
- Constructs `svc := bootstrap.NewService(store)`.
- Constructs `h := bootstrap.NewHandler(svc)`.
- Fires requests through `httptest.NewRecorder` + `httptest.NewRequest`.
- All writes roll back via `t.Cleanup` registered by `MustBeginTx`.

### Tests (T-R18 through T-R22)

| ID | Name | Scenario | Key assertions |
|----|------|----------|---------------|
| T-R18 | `TestBootstrap_Success` | No owner exists, valid active+verified user | HTTP 201; body contains `user_id`, `role_name == "owner"`, `granted_at` non-zero |
| T-R19 | `TestBootstrap_OwnerAlreadyExists` | Seed an owner first, then call bootstrap again | HTTP 409; `code == "owner_already_exists"` |
| T-R20 | `TestBootstrap_UserNotFound` | Non-existent UUID in `user_id` | HTTP 422; `code == "user_not_found"` |
| T-R21 | `TestBootstrap_EmailNotVerified` | Create user with `email_verified = false`, then call bootstrap | HTTP 422; `code == "email_not_verified"` |
| T-R22 | `TestBootstrap_RateLimitIsRegistered` | Verify rate limiter is wired (call 4 times, expect 429 on 4th) | HTTP 429 on 4th request; `code == "too_many_requests"` |

**T-R19 setup** — seed the owner directly:
```go
_, err = q.AssignUserRole(ctx, db.AssignUserRoleParams{
    UserID:        toPgtypeUUID(userID),
    RoleID:        toPgtypeUUID(ownerRoleID), // from q.GetOwnerRoleID
    GrantedBy:     toPgtypeUUID(userID),
    GrantedReason: "test seed",
    ExpiresAt:     pgtype.Timestamptz{Valid: false},
})
```
Note: `fn_prevent_owner_role_escalation` bootstrap exception fires (no existing owner → allow). Set `SET LOCAL rbac.skip_escalation_check = '1'` is **not** required here because the bootstrap exception in the trigger already handles no-owner inserts. If tests are running with an existing owner from a prior seed you must clean it up or use a transaction rollback.

**T-R22** — route-level test:
Wire the full `Routes(ctx, r, deps)` with a real chi router, then fire 4 `POST /bootstrap` requests from the same IP. The first 3 should pass (or 409 if already bootstrapped — both are non-429), the 4th must be HTTP 429.

---

## What NOT to do in this phase

- Do not add any admin routes yet — those start in Phase 5.
- Do not import `internal/platform/rbac` from the bootstrap service — it has no permission check (unauthenticated route). The `rbac.ErrOwnerAlreadyExists` sentinel IS imported by the handler for error mapping.
- Do not add audit events — RBAC mutations are audited at DB trigger level (`fn_audit_user_roles`).
- Do not create a separate `errors.go` unless you have more than 2 sentinels; inline in `service.go` is fine.
- Do not implement token revocation or session management — this endpoint creates an assignment only.
- Do not add `DocsEnabled` docs route handling — that's a server-level concern.

---

## Gate checklist

- [ ] `go build ./internal/domain/rbac/...` — zero errors
- [ ] `go vet ./internal/domain/rbac/...` — zero warnings
- [ ] `go build ./internal/server/...` — routes compile after mount additions
- [ ] `go test -tags integration_test ./internal/domain/rbac/bootstrap/...` — T-R18 through T-R22 green
- [ ] `POST /owner/bootstrap` returns 201 on a clean DB; returns 409 on repeat call
- [ ] No circular imports — bootstrap imports platform/rbac (for ErrOwnerAlreadyExists) and authshared (for ErrUserNotFound); platform/rbac does not import domain/rbac
