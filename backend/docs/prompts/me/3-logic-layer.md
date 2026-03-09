# §E-1 — Linked Accounts — Stage 3: Logic Layer

**Requirement source:** `docs/map/INCOMING.md §E-1`
**Target package:** `internal/domain/profile/me/`
**Depends on:** Stage 2 complete — `store.GetUserIdentities` implemented, `go build ./...` passes.

---

## Read first (no modifications)

| File | What to extract |
|---|---|
| `docs/prompts/me/context.md` | Resolved paths, decisions, test case IDs |
| `internal/domain/profile/me/service.go` | Constructor, existing methods, Storer interface |
| `internal/domain/profile/me/handler.go` | Servicer interface (confirm `GetUserIdentities` present) |
| `internal/domain/profile/me/models.go` | `LinkedIdentity` type |
| `internal/domain/auth/shared/testutil/fake_servicer.go` | `MeFakeServicer` (confirm Stage 1 addition present) |

---

## Changes by file

### 1. `internal/domain/profile/me/service.go` — implement GetUserIdentities

Add after `UpdateProfile`. No new imports needed — `authshared` and `fmt` are
already imported.

```go
// GetUserIdentities returns all linked OAuth identities for the authenticated
// user. Returns an empty (non-nil) slice when the user has no linked
// identities. userID is the standard UUID string form.
func (s *Service) GetUserIdentities(ctx context.Context, userID string) ([]LinkedIdentity, error) {
	uid, err := authshared.ParseUserID("profile.GetUserIdentities", userID)
	if err != nil {
		return nil, err
	}
	identities, err := s.store.GetUserIdentities(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("profile.GetUserIdentities: get identities: %w", err)
	}
	return identities, nil
}
```

**Notes:**
- Uses `authshared.ParseUserID` — the existing UUID-parsing helper used by all
  other service methods in this package. Consistent with `GetUserProfile`.
- No sentinel is returned from the store on success: an empty slice is a valid
  result (D-01, D-04). The handler maps a non-nil error to 500.
- No `context.WithoutCancel` — this is a pure read with no writes (D-05).

---

## Service unit tests (S-layer)

Add to `service_test.go` (or `identities_service_test.go` in the same package):

### T-01 — Happy path: user has identities

```go
func TestService_GetUserIdentities_HappyPath(t *testing.T) {
    now := time.Now().UTC().Truncate(time.Second)
    email := "alice@gmail.com"
    name := "Alice Smith"
    avatar := "https://example.com/avatar.png"

    fake := &authsharedtest.MeFakeStorer{
        GetUserIdentitiesFn: func(_ context.Context, _ [16]byte) ([]me.LinkedIdentity, error) {
            return []me.LinkedIdentity{
                {
                    Provider:      "google",
                    ProviderEmail: &email,
                    DisplayName:   &name,
                    AvatarURL:     &avatar,
                    CreatedAt:     now,
                },
            }, nil
        },
    }
    svc := me.NewService(fake)
    got, err := svc.GetUserIdentities(context.Background(), "00000000-0000-0000-0000-000000000001")
    require.NoError(t, err)
    require.Len(t, got, 1)
    assert.Equal(t, "google", got[0].Provider)
    assert.Equal(t, &email, got[0].ProviderEmail)
    assert.Equal(t, &name, got[0].DisplayName)
    assert.Equal(t, &avatar, got[0].AvatarURL)
    assert.Equal(t, now, got[0].CreatedAt)
}
```

### T-02 — Happy path: user has no identities → empty slice (non-nil)

```go
func TestService_GetUserIdentities_Empty(t *testing.T) {
    fake := &authsharedtest.MeFakeStorer{} // default returns []me.LinkedIdentity{}
    svc := me.NewService(fake)
    got, err := svc.GetUserIdentities(context.Background(), "00000000-0000-0000-0000-000000000001")
    require.NoError(t, err)
    assert.NotNil(t, got)
    assert.Empty(t, got)
}
```

### T-08 — Store error → service wraps and returns error

```go
func TestService_GetUserIdentities_StoreError(t *testing.T) {
    storeErr := errors.New("db failure")
    fake := &authsharedtest.MeFakeStorer{
        GetUserIdentitiesFn: func(_ context.Context, _ [16]byte) ([]me.LinkedIdentity, error) {
            return nil, storeErr
        },
    }
    svc := me.NewService(fake)
    _, err := svc.GetUserIdentities(context.Background(), "00000000-0000-0000-0000-000000000001")
    require.Error(t, err)
    assert.ErrorIs(t, err, storeErr)
}
```

---

## Checklist before closing Stage 3

- [ ] `Service.GetUserIdentities` implemented in `service.go`
- [ ] Uses `authshared.ParseUserID` — consistent with all other service methods
- [ ] Returns `(nil, err)` on store error; wraps with `fmt.Errorf(...%w...)`
- [ ] No `context.WithoutCancel` (read-only, no writes — D-05)
- [ ] No new audit event (D-05)
- [ ] `go build ./...` passes
- [ ] `go vet ./...` clean
- [ ] S-layer unit tests (T-01, T-02, T-08) written and green

Once all items are checked → proceed to Stage 4.
