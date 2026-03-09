# §D-2 Telegram OAuth — Stage 2: Data Layer

**Depends on:** Stage 1 complete — `internal/domain/oauth/telegram/` package compiles and
vets cleanly.
**Stage goal:** Store methods implemented, Storer interface declared (in `service.go`),
`TelegramFakeStorer` added to `internal/domain/oauth/shared/testutil/fake_storer.go`,
and `QuerierProxy` updated with any new Fail fields needed. No service logic, no handler.

---

## Read first (no modifications)

| File | Why |
|---|---|
| `docs/prompts/telegram/0-design.md` | Source of truth — guard ordering, error table, decisions |
| `internal/domain/oauth/telegram/models.go` | Input/result types from Stage 1 |
| `internal/domain/oauth/telegram/errors.go` | Package sentinel errors |
| `internal/domain/oauth/google/store.go` | Canonical store method pattern to follow |
| `internal/domain/oauth/google/service.go` | Storer interface layout to mirror for telegram |
| `internal/domain/oauth/shared/base_store.go` | BaseStore helpers: IsNoRows, ToPgtypeUUID, IPToNullable, MustJSON, ToText, TruncateUserAgent, UUIDToBytes, UUIDToPgtypeUUID |
| `internal/domain/oauth/shared/errors.go` | ErrIdentityNotFound, ErrAccountLocked, ErrLastAuthMethod |
| `internal/domain/auth/shared/errors.go` | ErrUserNotFound — used for no-rows on GetUserForOAuthCallback |
| `internal/domain/oauth/shared/testutil/fake_storer.go` | GoogleFakeStorer layout to mirror for TelegramFakeStorer |
| `internal/domain/oauth/shared/testutil/querier_proxy.go` | Proxy pattern, existing Fail fields |
| `internal/db/oauth.sql.go` | Confirm generated query signatures: GetIdentityByProviderUID, GetIdentityByUserAndProvider, GetUserForOAuthCallback, GetUserAuthMethods, CreateOAuthUser, UpsertUserIdentity, DeleteUserIdentity, InsertAuditLog, CreateUserSession, CreateRefreshToken, UpdateLastLoginAt |
| `docs/RULES.md` | Error wrapping: `fmt.Errorf("store.Method: step: %w", err)` |

---

## Pre-flight

1. Confirm `internal/domain/oauth/telegram/` compiles: `go build ./internal/domain/oauth/telegram/...`
2. Confirm all required queries exist in `internal/db/oauth.sql.go`:
   `GetIdentityByProviderUID`, `GetIdentityByUserAndProvider`, `GetUserForOAuthCallback`,
   `GetUserAuthMethods`, `CreateOAuthUser`, `UpsertUserIdentity`, `DeleteUserIdentity`,
   `InsertAuditLog`, `CreateUserSession`, `CreateRefreshToken`, `UpdateLastLoginAt`.
3. Confirm `internal/domain/oauth/telegram/service.go` contains only the package
   declaration (Storer interface goes here — Stage 2 adds it).
4. Confirm `internal/domain/oauth/telegram/store.go` contains only the package
   declaration (Store implementation goes here — Stage 2 adds it).
5. Confirm `internal/domain/oauth/shared/testutil/fake_storer.go` does NOT yet
   contain a `TelegramFakeStorer`.

---

## Deliverables

### 1. `internal/domain/oauth/telegram/service.go` — Storer interface

Replace the package-declaration-only stub with the full Storer interface. Mirror
the google Storer pattern exactly, adapting for Telegram specifics (no
`GetUserByEmailForOAuth`, no `UpsertUserIdentity` — use `InsertUserIdentity` for
the link flow since there is no update path; no access token).

```go
// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram

import "context"

// Storer is the persistence interface consumed by Service.
// All methods are implemented by *Store. FakeStorer in shared/testutil implements
// this interface for service unit tests.
type Storer interface {
	// GetIdentityByProviderUID looks up user_identities by (provider=telegram, provider_uid).
	// Returns oauthshared.ErrIdentityNotFound on no-rows.
	GetIdentityByProviderUID(ctx context.Context, providerUID string) (ProviderIdentity, error)

	// GetIdentityByUserAndProvider looks up user_identities by (user_id, provider=telegram).
	// Returns oauthshared.ErrIdentityNotFound on no-rows.
	GetIdentityByUserAndProvider(ctx context.Context, userID [16]byte) (ProviderIdentity, error)

	// GetUserForOAuthCallback fetches a user by ID for the lock guard.
	// Returns authshared.ErrUserNotFound on no-rows.
	GetUserForOAuthCallback(ctx context.Context, userID [16]byte) (OAuthUserRecord, error)

	// GetUserAuthMethods returns HasPassword and IdentityCount for the unlink guard.
	// Returns authshared.ErrUserNotFound on no-rows.
	GetUserAuthMethods(ctx context.Context, userID [16]byte) (UserAuthMethods, error)

	// InsertUserIdentity inserts a new user_identities row for the Telegram provider.
	// Used exclusively by the link flow. Unlike UpsertUserIdentity (used by Google),
	// this is a plain INSERT — the duplicate-provider guard runs before this call.
	// Returns error only.
	InsertUserIdentity(ctx context.Context, in InsertIdentityInput) error

	// DeleteUserIdentity deletes a user_identities row by (user_id, provider=telegram).
	// Returns (rowsAffected, error); the service maps 0 rows → ErrProviderNotLinked.
	DeleteUserIdentity(ctx context.Context, userID [16]byte) (int64, error)

	// OAuthLoginTx creates a session + refresh token + stamps last_login_at +
	// writes an oauth_login audit row — all in one transaction.
	OAuthLoginTx(ctx context.Context, in OAuthLoginTxInput) (oauthshared.LoggedInSession, error)

	// OAuthRegisterTx creates a new user + identity + session + refresh token +
	// last_login_at + audit row — all in one transaction.
	// Email is always empty for Telegram (D-04).
	OAuthRegisterTx(ctx context.Context, in OAuthRegisterTxInput) (oauthshared.LoggedInSession, error)

	// InsertAuditLogTx writes a standalone audit row for link and unlink flows.
	// Caller must pass a context.WithoutCancel ctx.
	InsertAuditLogTx(ctx context.Context, in OAuthAuditInput) error
}
```

> **Note:** Add `import oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"` to
> the import block (needed for `oauthshared.LoggedInSession` in the interface).

### 2. `internal/domain/oauth/telegram/store.go` — Store implementation

Full implementation following the google/store.go pattern. Key differences from
Google:
- All queries pass `db.AuthProviderTelegram` instead of `db.AuthProviderGoogle`.
- No `GetUserByEmailForOAuth` method (Telegram has no email path).
- `InsertUserIdentity` uses `UpsertUserIdentity` under the hood (same underlying
  SQL query — the duplicate-provider guard ensures the row is always new at this
  point, so ON CONFLICT is effectively a no-op; using Upsert avoids a new SQL query).
- No `access_token` or `provider_email` fields — pass empty `s.ToText("")` for both.
- `OAuthLoginTx` and `OAuthRegisterTx` set `AuthProvider: db.AuthProviderTelegram`.
- `InsertAuditLogTx` sets `Provider: db.AuthProviderTelegram`.

**Error wrapping prefix:** `"store.{MethodName}: ..."` — same convention as Google.

**Compile-time interface check:** `var _ Storer = (*Store)(nil)` at the top of the file.

### 3. `internal/domain/oauth/shared/testutil/fake_storer.go` — TelegramFakeStorer

Add `TelegramFakeStorer` below `GoogleFakeStorer`. Mirror the GoogleFakeStorer
structure exactly, substituting telegram types. Default behaviours:

| Method | Default return |
|---|---|
| `GetIdentityByProviderUID` | `(ProviderIdentity{}, oauthshared.ErrIdentityNotFound)` |
| `GetIdentityByUserAndProvider` | `(ProviderIdentity{}, oauthshared.ErrIdentityNotFound)` |
| `GetUserForOAuthCallback` | `(OAuthUserRecord{IsActive: true}, nil)` |
| `GetUserAuthMethods` | `(UserAuthMethods{HasPassword: false, IdentityCount: 2}, nil)` |
| `InsertUserIdentity` | `nil` |
| `DeleteUserIdentity` | `(1, nil)` |
| `OAuthLoginTx` | `(oauthshared.LoggedInSession{}, nil)` |
| `OAuthRegisterTx` | `(oauthshared.LoggedInSession{}, nil)` |
| `InsertAuditLogTx` | `nil` |

Add a compile-time check: `var _ telegram.Storer = (*TelegramFakeStorer)(nil)`

> **Import note:** You will need to add `"github.com/7-Dany/store/backend/internal/domain/oauth/telegram"`
> to the import block in `fake_storer.go`.

### 4. `internal/domain/oauth/shared/testutil/querier_proxy.go` — no new fields needed

All queries used by the telegram store (`GetIdentityByProviderUID`,
`GetIdentityByUserAndProvider`, `GetUserForOAuthCallback`, `GetUserAuthMethods`,
`CreateOAuthUser`, `UpsertUserIdentity`, `DeleteUserIdentity`, `InsertAuditLog`,
`CreateUserSession`, `CreateRefreshToken`, `UpdateLastLoginAt`) already have Fail
fields in `QuerierProxy` from the Google implementation.

**No changes to querier_proxy.go are required.**

---

## Done when

```bash
# Telegram package compiles and vets cleanly
go build ./internal/domain/oauth/telegram/...
go vet ./internal/domain/oauth/telegram/...

# Testutil compiles (new TelegramFakeStorer added)
go build ./internal/domain/oauth/shared/testutil/...
go vet ./internal/domain/oauth/shared/testutil/...

# Full build still passes
go build ./internal/...
```

No test failures should result — no tests exist yet for this package.

**Stop here. Do not implement service logic, handlers, or routes.**
**Stage 3 starts in a separate session.**
