# §E-1 — Linked Accounts — Stage 1: Foundations

**Requirement source:** `docs/map/INCOMING.md §E-1`
**Target package:** `internal/domain/profile/me/`
**Depends on:** Stage 0 approved (`docs/prompts/me/0-design.md`)

---

## Read first (no modifications)

| File | What to verify |
|---|---|
| `docs/prompts/me/context.md` | Resolved paths, decisions, KV prefix |
| `sql/queries/oauth.sql` | Append position and existing section style |
| `internal/domain/profile/me/models.go` | Existing types (add LinkedIdentity below existing) |
| `internal/domain/profile/me/requests.go` | Existing response types (add identityItem + identitiesResponse) |
| `internal/domain/profile/me/service.go` | Storer interface (add GetUserIdentities method) |
| `internal/domain/profile/me/handler.go` | Servicer interface (add GetUserIdentities method) |
| `internal/domain/auth/shared/testutil/fake_storer.go` | MeFakeStorer struct (add GetUserIdentitiesFn) |
| `internal/domain/auth/shared/testutil/fake_servicer.go` | MeFakeServicer struct (add GetUserIdentitiesFn) |

**Do NOT run `make sqlc` yet** — that comes after this file is saved and reviewed.

---

## Changes by file

### 1. `sql/queries/oauth.sql` — append new query

Append at the end of the file, after `GetUserForOAuthCallback`:

```sql
-- name: GetUserIdentities :many
-- Returns all linked OAuth identities for the given user, oldest first.
-- access_token and refresh_token_provider are intentionally excluded —
-- they are provider secrets and must never be returned to clients.
SELECT
    provider,
    provider_email,
    display_name,
    avatar_url,
    created_at
FROM user_identities
WHERE user_id = @user_id::uuid
ORDER BY created_at ASC;
```

After saving, run:
```
make sqlc
```

Confirm `internal/db/oauth.sql.go` now contains a `GetUserIdentities` function returning
`[]GetUserIdentitiesRow`. The generated row type will have:
- `Provider      AuthProvider`
- `ProviderEmail pgtype.Text`
- `DisplayName   pgtype.Text`
- `AvatarURL     pgtype.Text`
- `CreatedAt     pgtype.Timestamptz`

---

### 2. `internal/domain/profile/me/models.go` — add LinkedIdentity

Append after the `UpdateProfileInput` type:

```go
// LinkedIdentity is the service-layer representation of a single linked OAuth
// identity. access_token and refresh_token_provider are intentionally absent —
// they are provider secrets and must never be returned to API clients.
type LinkedIdentity struct {
	Provider      string
	ProviderEmail *string
	DisplayName   *string
	AvatarURL     *string
	CreatedAt     time.Time
}
```

`time.Time` is already imported. No new imports needed.

---

### 3. `internal/domain/profile/me/requests.go` — add response types

Append after the existing `meResponse` type:

```go
// identityItem is a single entry in the GET /me/identities response.
// Nullable fields use omitempty — they are omitted entirely when nil.
type identityItem struct {
	Provider      string     `json:"provider"`
	ProviderEmail *string    `json:"provider_email,omitempty"`
	DisplayName   *string    `json:"display_name,omitempty"`
	AvatarURL     *string    `json:"avatar_url,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// identitiesResponse is the JSON body for GET /me/identities.
// Identities is always an array — never null; empty array when none linked.
type identitiesResponse struct {
	Identities []identityItem `json:"identities"`
}
```

`time.Time` is already imported. No new imports needed.

---

### 4. `internal/domain/profile/me/service.go` — extend Storer interface

Add `GetUserIdentities` to the `Storer` interface:

```go
type Storer interface {
	GetUserProfile(ctx context.Context, userID [16]byte) (UserProfile, error)
	UpdateProfileTx(ctx context.Context, in UpdateProfileInput) error
	GetUserIdentities(ctx context.Context, userID [16]byte) ([]LinkedIdentity, error) // §E-1
}
```

---

### 5. `internal/domain/profile/me/handler.go` — extend Servicer interface

Add `GetUserIdentities` to the `Servicer` interface:

```go
type Servicer interface {
	GetUserProfile(ctx context.Context, userID string) (UserProfile, error)
	UpdateProfile(ctx context.Context, in UpdateProfileInput) error
	GetUserIdentities(ctx context.Context, userID string) ([]LinkedIdentity, error) // §E-1
}
```

---

### 6. `internal/domain/auth/shared/testutil/fake_storer.go` — extend MeFakeStorer

Add the new field to `MeFakeStorer`:

```go
type MeFakeStorer struct {
	GetUserProfileFn       func(ctx context.Context, userID [16]byte) (me.UserProfile, error)
	UpdateProfileTxFn      func(ctx context.Context, in me.UpdateProfileInput) error
	GetUserIdentitiesFn    func(ctx context.Context, userID [16]byte) ([]me.LinkedIdentity, error) // §E-1
}
```

Add the method implementation after `UpdateProfileTx`:

```go
// GetUserIdentities delegates to GetUserIdentitiesFn if set.
// Default: returns ([]me.LinkedIdentity{}, nil) — empty slice (never nil) so tests
// that don't configure it always receive a valid empty collection.
func (f *MeFakeStorer) GetUserIdentities(ctx context.Context, userID [16]byte) ([]me.LinkedIdentity, error) {
	if f.GetUserIdentitiesFn != nil {
		return f.GetUserIdentitiesFn(ctx, userID)
	}
	return []me.LinkedIdentity{}, nil
}
```

Verify the compile-time check still passes:
```go
var _ me.Storer = (*MeFakeStorer)(nil)
```

---

### 7. `internal/domain/auth/shared/testutil/fake_servicer.go` — extend MeFakeServicer

Add the new field to `MeFakeServicer`:

```go
type MeFakeServicer struct {
	GetUserProfileFn       func(ctx context.Context, userID string) (me.UserProfile, error)
	UpdateProfileFn        func(ctx context.Context, in me.UpdateProfileInput) error
	GetUserIdentitiesFn    func(ctx context.Context, userID string) ([]me.LinkedIdentity, error) // §E-1
}
```

Add the method implementation after `UpdateProfile`:

```go
// GetUserIdentities delegates to GetUserIdentitiesFn if set.
// Default: returns ([]me.LinkedIdentity{}, nil) — empty slice (never nil).
func (f *MeFakeServicer) GetUserIdentities(ctx context.Context, userID string) ([]me.LinkedIdentity, error) {
	if f.GetUserIdentitiesFn != nil {
		return f.GetUserIdentitiesFn(ctx, userID)
	}
	return []me.LinkedIdentity{}, nil
}
```

Verify the compile-time check still passes:
```go
var _ me.Servicer = (*MeFakeServicer)(nil)
```

---

## Checklist before closing Stage 1

- [ ] SQL query appended to `sql/queries/oauth.sql` exactly as written above
- [ ] `make sqlc` run — `internal/db/oauth.sql.go` contains `GetUserIdentities` + `GetUserIdentitiesRow`
- [ ] `LinkedIdentity` added to `models.go`
- [ ] `identityItem` + `identitiesResponse` added to `requests.go`
- [ ] `GetUserIdentities` added to `Storer` interface in `service.go`
- [ ] `GetUserIdentities` added to `Servicer` interface in `handler.go`
- [ ] `MeFakeStorer.GetUserIdentitiesFn` added + method implemented (default: empty slice)
- [ ] `MeFakeServicer.GetUserIdentitiesFn` added + method implemented (default: empty slice)
- [ ] `go build ./...` passes — no interface satisfaction errors
- [ ] `go vet ./...` clean

Once all items are checked → proceed to Stage 2.
