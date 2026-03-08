# Google OAuth — Stage 1: Foundations

**Feature:** Google OAuth (§D-1)
**Package:** `internal/domain/oauth/google/` + `internal/domain/oauth/shared/`
**Depends on:** Stage 0 approved (`docs/prompts/oauth/0-design.md`)

---

## Read first

| File | What to extract |
|---|---|
| `docs/prompts/oauth/context.md` | Resolved paths, decisions, query names, sentinels |
| `docs/RULES.md §3.9` | SQL conventions, no-raw-SQL rule |
| `docs/RULES.md §3.11` | Naming conventions |
| `docs/RULES.md §3.14` | Mandatory three-file sync rules (S-1, S-2) |
| `internal/audit/audit.go` | Full file — const block + AllEvents() — to append new events and keep sync |
| `internal/domain/auth/shared/testutil/querier_proxy.go` | Existing structure — to add 8 forwarding stubs for new oauth queries (S-2) |
| `internal/domain/auth/login/models.go` | Reference for service-layer model style |
| `sql/queries/auth.sql` tail 60 lines | Query style to match in `oauth.sql` |
| `sql/schema/001_core.sql` | `user_identities` columns and constraints |
| `internal/db/querier.go` | After `make sqlc` — verify 8 new method signatures |

---

## Deliverables (in implementation order)

### 1. `sql/queries/oauth.sql` — new file

Create `sql/queries/oauth.sql`. Follow the same comment and formatting style as `auth.sql`. Use `@param_name::type` for named parameters. Use `pgtype`-friendly nullable columns by selecting them directly (sqlc emits `pgtype.Text` for nullable `TEXT`). All queries share one section header at the top of the file.

```sql
-- oauth.sql — queries for the OAuth domain (Google provider).
-- Appended to by future providers (Telegram). Do not merge into auth.sql.
```

**Query 1 — GetIdentityByProviderUID `:one`**

Looks up a `user_identities` row by `(provider, provider_uid)`.
Returns: `id, user_id, provider_email, display_name, avatar_url, access_token`.
Filter: `WHERE provider = @provider AND provider_uid = @provider_uid`.
No `deleted_at` check needed — cascade DELETE on users already removes these rows.

**Query 2 — GetIdentityByUserAndProvider `:one`**

Looks up a `user_identities` row by `(user_id, provider)`.
Returns: `id, user_id`.
Filter: `WHERE user_id = @user_id::uuid AND provider = @provider`.
Used for unlink guard and link-mode duplicate check.

**Query 3 — UpsertUserIdentity `:one`**

INSERT into `user_identities` with conflict target `ON CONFLICT ON CONSTRAINT uq_identity_user_provider DO UPDATE`.
Named params: `user_id, provider, provider_uid, provider_email, display_name, avatar_url, access_token`.
On conflict, update: `provider_email`, `display_name`, `avatar_url`, `access_token`, `updated_at = NOW()`.
Returns the full row (all columns).
Note: `access_token` already carries the `enc:` prefix; the DB constraint `chk_ui_access_token_encrypted` enforces this.

**Query 4 — DeleteUserIdentity `:execrows`**

`DELETE FROM user_identities WHERE user_id = @user_id::uuid AND provider = @provider`.
Returns rows affected — the store checks for 0 to detect a lost-race.

**Query 5 — GetUserAuthMethods `:one`**

Returns whether the user has a password and how many OAuth identities they have.
Use a LEFT JOIN:

```sql
SELECT
    (u.password_hash IS NOT NULL) AS has_password,
    COUNT(ui.id)                   AS identity_count
FROM users u
LEFT JOIN user_identities ui ON ui.user_id = u.id
WHERE u.id = @user_id::uuid
  AND u.deleted_at IS NULL
GROUP BY u.password_hash;
```

sqlc will emit `GetUserAuthMethodsRow{HasPassword bool, IdentityCount int64}`.

**Query 6 — CreateOAuthUser `:one`**

Inserts a new user row for a Google-only registration.
Named params: `email` (nullable TEXT), `display_name` (nullable TEXT).
Hard-coded in the query body: `email_verified = TRUE`, `is_active = TRUE`, `password_hash = NULL`.
Returns only `id`.

```sql
INSERT INTO users (email, display_name, email_verified, is_active)
VALUES (
    sqlc.narg('email')::text,
    sqlc.narg('display_name')::text,
    TRUE,
    TRUE
)
RETURNING id;
```

**Query 7 — GetUserByEmailForOAuth `:one`**

Fetches `id, is_active, is_locked, admin_locked` by email, for the email-match path in callback.
Filter: `WHERE email = @email AND deleted_at IS NULL`.

**Query 8 — GetUserForOAuthCallback `:one`**

Fetches `id, is_active, is_locked, admin_locked` by user ID, for the lock guard in link mode and the existing-identity login path.
Filter: `WHERE id = @user_id::uuid AND deleted_at IS NULL`.

---

### 2. Run `make sqlc`

After writing `oauth.sql`, run `make sqlc`. This regenerates `internal/db/` and adds 8 new methods to `db.Querier` in `internal/db/querier.go`. Verify the generated signatures match the query definitions above before proceeding.

The generated param/row struct names will be (sqlc derives these from the query name):
- `GetIdentityByProviderUIDRow`
- `GetIdentityByUserAndProviderRow`
- `UpsertUserIdentityParams` / `UpsertUserIdentityRow` (full `UserIdentity` row)
- `DeleteUserIdentityParams`
- `GetUserAuthMethodsRow` (`HasPassword bool`, `IdentityCount int64`)
- `CreateOAuthUserParams` / returns `uuid.UUID`
- `GetUserByEmailForOAuthRow`
- `GetUserForOAuthCallbackRow`

---

### 3. `internal/audit/audit.go` — add 3 constants (Sync S-1)

All three updates must be made in the same commit. Missing any one causes a compile-time test failure in `internal/audit/`.

**3a. Add to the `const` block** (after the last existing constant, before the closing parenthesis):

```go
// EventOAuthLogin is emitted when a user successfully authenticates or registers
// via an OAuth provider. The metadata field contains provider and new_user (bool).
EventOAuthLogin EventType = "oauth_login"

// EventOAuthLinked is emitted when an OAuth identity is linked to an existing
// authenticated account (link mode). The metadata field contains provider.
EventOAuthLinked EventType = "oauth_linked"

// EventOAuthUnlinked is emitted when an OAuth identity is removed from a user
// account via DELETE /oauth/{provider}/unlink.
EventOAuthUnlinked EventType = "oauth_unlinked"
```

**3b. Add to `AllEvents()` return slice:**

```go
EventOAuthLogin,
EventOAuthLinked,
EventOAuthUnlinked,
```

**3c. Add to `audit_test.go` `TestEventConstants_ExactValues` cases table:**

```go
{audit.EventOAuthLogin,    "oauth_login"},
{audit.EventOAuthLinked,   "oauth_linked"},
{audit.EventOAuthUnlinked, "oauth_unlinked"},
```

---

### 4. `internal/domain/auth/shared/testutil/querier_proxy.go` — add 8 stubs (Sync S-2)

The auth `QuerierProxy` implements `db.Querier` via a compile-time check. The 8 new methods added by `make sqlc` will break the build unless plain forwarding stubs are added here.

Add a new section at the end of `QuerierProxy`, after the last existing feature section. These stubs have **no `Fail*` flags** — that is the oauth domain's responsibility:

```go
// ── oauth ────────────────────────────────────────────────────────────────────

func (p *QuerierProxy) GetIdentityByProviderUID(ctx context.Context, arg db.GetIdentityByProviderUIDParams) (db.GetIdentityByProviderUIDRow, error) {
	return p.Base.GetIdentityByProviderUID(ctx, arg)
}

func (p *QuerierProxy) GetIdentityByUserAndProvider(ctx context.Context, arg db.GetIdentityByUserAndProviderParams) (db.GetIdentityByUserAndProviderRow, error) {
	return p.Base.GetIdentityByUserAndProvider(ctx, arg)
}

func (p *QuerierProxy) UpsertUserIdentity(ctx context.Context, arg db.UpsertUserIdentityParams) (db.UserIdentity, error) {
	return p.Base.UpsertUserIdentity(ctx, arg)
}

func (p *QuerierProxy) DeleteUserIdentity(ctx context.Context, arg db.DeleteUserIdentityParams) (int64, error) {
	return p.Base.DeleteUserIdentity(ctx, arg)
}

func (p *QuerierProxy) GetUserAuthMethods(ctx context.Context, userID uuid.UUID) (db.GetUserAuthMethodsRow, error) {
	return p.Base.GetUserAuthMethods(ctx, userID)
}

func (p *QuerierProxy) CreateOAuthUser(ctx context.Context, arg db.CreateOAuthUserParams) (uuid.UUID, error) {
	return p.Base.CreateOAuthUser(ctx, arg)
}

func (p *QuerierProxy) GetUserByEmailForOAuth(ctx context.Context, email pgtype.Text) (db.GetUserByEmailForOAuthRow, error) {
	return p.Base.GetUserByEmailForOAuth(ctx, email)
}

func (p *QuerierProxy) GetUserForOAuthCallback(ctx context.Context, userID uuid.UUID) (db.GetUserForOAuthCallbackRow, error) {
	return p.Base.GetUserForOAuthCallback(ctx, userID)
}
```

Use the exact generated parameter and return types from `internal/db/querier.go` — the names above are expected but verify them after `make sqlc`.

Also add the same 8 plain-forwarding stubs to the `nopQuerier` struct inside `querier_proxy_test.go`, and to any other `*_test.go` file in the auth domain that has a local `db.Querier` implementation.

---

### 5. `internal/domain/oauth/shared/errors.go`

Create the new file:

```go
// Package oauthshared holds primitives shared by all oauth feature sub-packages.
package oauthshared

import "errors"

// ── Cross-feature sentinel errors ─────────────────────────────────────────────

// ErrIdentityNotFound is returned when a user_identities row cannot be located
// for the given (user_id, provider) or (provider, provider_uid) pair.
var ErrIdentityNotFound = errors.New("oauth identity not found")

// ErrProviderAlreadyLinked is returned when a Google UID is already linked to
// a different user account in link mode (link_user_id != row.UserID).
var ErrProviderAlreadyLinked = errors.New("provider already linked to another account")

// ErrLastAuthMethod is returned by UnlinkGoogle when removing the identity
// would leave the user with no remaining authentication method.
var ErrLastAuthMethod = errors.New("cannot remove the last authentication method")

// ErrAccountLocked is returned when the matched user is locked (is_locked or
// admin_locked) during an OAuth callback or link operation.
var ErrAccountLocked = errors.New("account is locked")
```

---

### 6. `internal/domain/oauth/google/errors.go`

Create the new file:

```go
// Package google handles Google OAuth authentication: initiate, callback, and unlink.
package google

import "errors"

// ErrTokenExchangeFailed is returned when the code-exchange request to Google's
// token endpoint fails (network error, invalid code, or non-2xx response).
var ErrTokenExchangeFailed = errors.New("google token exchange failed")

// ErrInvalidIDToken is returned when the ID token returned by Google fails
// oidc verification — invalid signature, wrong audience, or expired.
var ErrInvalidIDToken = errors.New("google id token verification failed")
```

---

### 7. `internal/domain/oauth/shared/models.go`

Create the new file. These types are shared across all current and future OAuth providers (Telegram, etc.):

```go
// Package oauthshared holds primitives shared by all oauth feature sub-packages.
package oauthshared

import "time"

// LoggedInSession is the session metadata returned by a store Tx method after
// a successful OAuth login or registration. All UUIDs are raw [16]byte; the
// handler converts them to strings for JWT claims.
type LoggedInSession struct {
	UserID        [16]byte
	SessionID     [16]byte
	RefreshJTI    [16]byte
	FamilyID      [16]byte
	RefreshExpiry time.Time
}

// LinkedIdentity is a summary of one OAuth identity linked to a user account.
// Used by the future GET /profile/me/identities endpoint (§E-1).
type LinkedIdentity struct {
	Provider    string
	DisplayName string
	AvatarURL   string
}
```

---

### 8. `internal/domain/oauth/google/models.go`

Create the new file. These types are the service/store boundary for the Google package:

```go
// Package google handles Google OAuth authentication: initiate, callback, and unlink.
package google

import (
	"github.com/7-Dany/store/backend/internal/domain/oauth/shared"
)

// CallbackInput is the input to Service.HandleCallback.
// All strings are pre-validated by the handler's guard sequence.
type CallbackInput struct {
	Code         string // authorization code from Google
	CodeVerifier string // PKCE code_verifier from KV state entry
	LinkUserID   string // non-empty when the initiate request carried a valid JWT
	IPAddress    string
	UserAgent    string
}

// CallbackResult is returned by Service.HandleCallback on success.
// Exactly one of Linked or (Session + NewUser) is meaningful:
//   - Linked == true → link mode succeeded; no session is issued
//   - Linked == false → login/register mode succeeded; Session carries token metadata
type CallbackResult struct {
	Session oauthshared.LoggedInSession
	NewUser bool // true when a new users row was created
	Linked  bool // true when link mode ran successfully
}

// GoogleClaims contains the verified claims extracted from a Google OIDC ID token.
type GoogleClaims struct {
	Sub     string // Google subject (stable provider UID)
	Email   string // may be empty for accounts without a verified email
	Name    string // display name
	Picture string // avatar URL
}

// ProviderIdentity is the store-layer view of a user_identities row, returned
// by Store.GetIdentityByProviderUID and Store.GetIdentityByUserAndProvider.
type ProviderIdentity struct {
	ID            [16]byte
	UserID        [16]byte
	ProviderEmail string
	DisplayName   string
	AvatarURL     string
	AccessToken   string // encrypted; carries "enc:" prefix
}

// OAuthUserRecord is the minimal user view returned by Store.GetUserByEmailForOAuth
// and Store.GetUserForOAuthCallback. Carries only the fields needed for lock guards.
type OAuthUserRecord struct {
	ID          [16]byte
	IsActive    bool
	IsLocked    bool
	AdminLocked bool
}

// UserAuthMethods is returned by Store.GetUserAuthMethods for the unlink
// last-auth-method guard.
type UserAuthMethods struct {
	HasPassword   bool
	IdentityCount int64
}

// UpsertIdentityInput carries the fields written by Store.UpsertUserIdentity.
type UpsertIdentityInput struct {
	UserID        [16]byte
	Provider      string // "google"
	ProviderUID   string // Google subject
	ProviderEmail string
	DisplayName   string
	AvatarURL     string
	AccessToken   string // must already carry "enc:" prefix
}
```

---

### 9. `internal/domain/oauth/shared/store.go`

Create the new file:

```go
// Package oauthshared holds primitives shared by all oauth feature sub-packages.
package oauthshared

import authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"

// BaseStore is a type alias for authshared.BaseStore, providing OAuth packages
// with the same pool, BeginOrBind, IsNoRows, and pgtype conversion helpers
// without duplicating the implementation.
type BaseStore = authshared.BaseStore

// NewBaseStore returns an oauthshared.BaseStore backed by the given pool.
// OAuth feature stores embed this as their base.
func NewBaseStore(pool interface{ /* *pgxpool.Pool */ }) BaseStore {
	return authshared.NewBaseStore(pool.(*pgxpool.Pool))
}
```

**Note:** The signature above uses the real pgxpool type. Import `pgxpool` at the top and use the real type:

```go
import (
	"github.com/jackc/pgx/v5/pgxpool"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// NewBaseStore returns an oauthshared.BaseStore backed by the given pool.
func NewBaseStore(pool *pgxpool.Pool) BaseStore {
	return authshared.NewBaseStore(pool)
}
```

---

### 10. `internal/domain/oauth/shared/testutil/` — skeleton package

Create the directory and four skeleton files. FakeStorer and FakeServicer will be populated during Stages 2–4 as the Storer and Servicer interfaces are defined. Skeleton files must compile now.

**`builders.go`**

```go
// Package oauthsharedtest provides test-only helpers shared across all oauth
// feature sub-packages. It must never be imported by production code.
package oauthsharedtest

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
)

// MustNewTestPool creates a *pgxpool.Pool for integration tests.
// Always pass maxConns = 20 (required by ADR-003).
func MustNewTestPool(dsn string, maxConns int) *pgxpool.Pool {
	return authsharedtest.MustNewTestPool(dsn, maxConns)
}

// RunTestMain initialises the test pool when TEST_DATABASE_URL is set, lowers
// the bcrypt cost for fast unit tests, runs the suite, and exits. Call from
// TestMain in every store_test.go:
//
//	func TestMain(m *testing.M) { oauthsharedtest.RunTestMain(m, &testPool, 20) }
func RunTestMain(m *testing.M, pool **pgxpool.Pool, maxConns int) {
	authsharedtest.RunTestMain(m, pool, maxConns)
}
```

**`querier_proxy.go`**

Create with Fail* flags for all 8 oauth queries. Use the actual generated types after `make sqlc`:

```go
// Package oauthsharedtest provides test-only helpers shared across all oauth
// feature sub-packages. It must never be imported by production code.
package oauthsharedtest

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/7-Dany/store/backend/internal/db"
)

// ErrProxy is the sentinel error returned by QuerierProxy when a Fail* flag is set.
var ErrProxy = errors.New("querier_proxy: injected error")

// QuerierProxy implements db.Querier, delegating each call to Base unless the
// corresponding Fail* flag is set — in that case ErrProxy is returned immediately.
// Fail* flags are grouped by feature with section separator comments.
//
// Note: this proxy covers only the oauth-domain queries. Auth-domain queries are
// forwarded to Base without Fail* flags, since those are tested via authsharedtest.QuerierProxy.
type QuerierProxy struct {
	Base db.Querier

	// ── google ───────────────────────────────────────────────────────────────
	FailGetIdentityByProviderUID      bool
	FailGetIdentityByUserAndProvider  bool
	FailUpsertUserIdentity            bool
	FailDeleteUserIdentity            bool
	FailGetUserAuthMethods            bool
	FailCreateOAuthUser               bool
	FailGetUserByEmailForOAuth        bool
	FailGetUserForOAuthCallback       bool
}

// compile-time interface check.
var _ db.Querier = (*QuerierProxy)(nil)

// NewQuerierProxy wraps base with a zero-valued QuerierProxy.
func NewQuerierProxy(base db.Querier) *QuerierProxy {
	return &QuerierProxy{Base: base}
}

func (p *QuerierProxy) GetIdentityByProviderUID(ctx context.Context, arg db.GetIdentityByProviderUIDParams) (db.GetIdentityByProviderUIDRow, error) {
	if p.FailGetIdentityByProviderUID {
		return db.GetIdentityByProviderUIDRow{}, ErrProxy
	}
	return p.Base.GetIdentityByProviderUID(ctx, arg)
}

func (p *QuerierProxy) GetIdentityByUserAndProvider(ctx context.Context, arg db.GetIdentityByUserAndProviderParams) (db.GetIdentityByUserAndProviderRow, error) {
	if p.FailGetIdentityByUserAndProvider {
		return db.GetIdentityByUserAndProviderRow{}, ErrProxy
	}
	return p.Base.GetIdentityByUserAndProvider(ctx, arg)
}

func (p *QuerierProxy) UpsertUserIdentity(ctx context.Context, arg db.UpsertUserIdentityParams) (db.UserIdentity, error) {
	if p.FailUpsertUserIdentity {
		return db.UserIdentity{}, ErrProxy
	}
	return p.Base.UpsertUserIdentity(ctx, arg)
}

func (p *QuerierProxy) DeleteUserIdentity(ctx context.Context, arg db.DeleteUserIdentityParams) (int64, error) {
	if p.FailDeleteUserIdentity {
		return 0, ErrProxy
	}
	return p.Base.DeleteUserIdentity(ctx, arg)
}

func (p *QuerierProxy) GetUserAuthMethods(ctx context.Context, userID uuid.UUID) (db.GetUserAuthMethodsRow, error) {
	if p.FailGetUserAuthMethods {
		return db.GetUserAuthMethodsRow{}, ErrProxy
	}
	return p.Base.GetUserAuthMethods(ctx, userID)
}

func (p *QuerierProxy) CreateOAuthUser(ctx context.Context, arg db.CreateOAuthUserParams) (uuid.UUID, error) {
	if p.FailCreateOAuthUser {
		return uuid.UUID{}, ErrProxy
	}
	return p.Base.CreateOAuthUser(ctx, arg)
}

func (p *QuerierProxy) GetUserByEmailForOAuth(ctx context.Context, email pgtype.Text) (db.GetUserByEmailForOAuthRow, error) {
	if p.FailGetUserByEmailForOAuth {
		return db.GetUserByEmailForOAuthRow{}, ErrProxy
	}
	return p.Base.GetUserByEmailForOAuth(ctx, email)
}

func (p *QuerierProxy) GetUserForOAuthCallback(ctx context.Context, userID uuid.UUID) (db.GetUserForOAuthCallbackRow, error) {
	if p.FailGetUserForOAuthCallback {
		return db.GetUserForOAuthCallbackRow{}, ErrProxy
	}
	return p.Base.GetUserForOAuthCallback(ctx, userID)
}
```

**Add all remaining `db.Querier` methods as plain forwarding stubs.** After `make sqlc`, `db.Querier` has all auth-domain methods plus the 8 new ones. The proxy must implement every method. Copy the existing methods from `authsharedtest.QuerierProxy` (all the auth-domain ones) as plain forwarding stubs with no Fail* flags. The Fail* flags only appear for the 8 oauth queries above.

**`fake_storer.go`** — skeleton:

```go
// Package oauthsharedtest provides test-only helpers shared across all oauth
// feature sub-packages. It must never be imported by production code.
package oauthsharedtest

// GoogleFakeStorer and GoogleFakeServicer are populated in Stage 2 (Data Layer)
// and Stage 4 (HTTP Layer) respectively, once the Storer and Servicer interfaces
// are defined in google/service.go and google/handler.go.
```

**`fake_servicer.go`** — skeleton:

```go
// Package oauthsharedtest provides test-only helpers shared across all oauth
// feature sub-packages. It must never be imported by production code.
package oauthsharedtest
```

---

## Sync checklist for this stage

- [ ] S-1: `audit.go` const block, `AllEvents()`, and `audit_test.go` cases table all updated with the same 3 events
- [ ] S-2: `auth/shared/testutil/querier_proxy.go` has 8 new plain-forwarding stubs; `nopQuerier` in `querier_proxy_test.go` has the same 8 stubs; any other local `db.Querier` implementations in auth test files are updated
- [ ] S-2: `oauth/shared/testutil/querier_proxy.go` compiles with `var _ db.Querier = (*QuerierProxy)(nil)` (includes all auth-domain forwarding stubs + 8 oauth Fail* methods)
- [ ] `make sqlc` passes with no errors after writing `oauth.sql`
- [ ] `go build ./internal/...` passes
- [ ] `go vet ./internal/...` produces no output
- [ ] `go test ./internal/audit/...` passes (S-1 count check)
- [ ] `go test ./internal/domain/auth/shared/testutil/...` passes (S-2 compile check)
- [ ] `go test ./internal/domain/oauth/shared/testutil/...` passes (S-2 compile check)

---

## What Stage 2 will implement

Stage 2 (Data Layer) implements `google/store.go`, populates `GoogleFakeStorer` in testutil, adds `Fail*` flags to `authsharedtest.QuerierProxy` for shared queries called by the oauth store (e.g. `CreateUserSession`, `CreateRefreshToken`, `UpdateLastLoginAt`, `InsertAuditLog`), and writes `store_test.go` integration tests for all 8 new queries plus the two Tx methods (`OAuthLoginTx`, `OAuthRegisterTx`).
