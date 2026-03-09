# §E-1 — Linked Accounts — Stage 2: Data Layer

**Requirement source:** `docs/map/INCOMING.md §E-1`
**Target package:** `internal/domain/profile/me/`
**Depends on:** Stage 1 complete — `make sqlc` run, `GetUserIdentitiesRow` exists in `internal/db/oauth.sql.go`, interfaces updated, `go build ./...` passes.

---

## Read first (no modifications)

| File | What to extract |
|---|---|
| `docs/prompts/me/context.md` | Resolved paths, decisions |
| `internal/db/oauth.sql.go` | `GetUserIdentitiesRow` field types (confirm `pgtype.Text`, `pgtype.Timestamptz`) |
| `internal/domain/profile/me/service.go` | Current `Storer` interface (confirm `GetUserIdentities` is present) |
| `internal/domain/profile/me/store.go` | Existing store layout — helper methods (`ToPgtypeUUID`, `ToText`, etc.) |
| `internal/domain/auth/shared/testutil/fake_storer.go` | `MeFakeStorer` (confirm `GetUserIdentitiesFn` + method present from Stage 1) |

---

## Changes by file

### 1. `internal/domain/profile/me/store.go` — implement GetUserIdentities

Add after `UpdateProfileTx`. No new imports are needed — `db` and `fmt` are
already imported.

```go
// GetUserIdentities returns all linked OAuth identities for the given user,
// ordered by created_at ASC. Returns an empty (non-nil) slice when the user
// has no linked identities. access_token and refresh_token_provider are
// excluded by the SQL query and never appear in the returned slice.
func (s *Store) GetUserIdentities(ctx context.Context, userID [16]byte) ([]LinkedIdentity, error) {
	rows, err := s.Queries.GetUserIdentities(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		return nil, fmt.Errorf("store.GetUserIdentities: query: %w", err)
	}

	out := make([]LinkedIdentity, 0, len(rows))
	for _, r := range rows {
		id := LinkedIdentity{
			Provider:  string(r.Provider),
			CreatedAt: r.CreatedAt.Time.UTC(),
		}
		if r.ProviderEmail.Valid {
			v := r.ProviderEmail.String
			id.ProviderEmail = &v
		}
		if r.DisplayName.Valid {
			v := r.DisplayName.String
			id.DisplayName = &v
		}
		if r.AvatarURL.Valid {
			v := r.AvatarURL.String
			id.AvatarURL = &v
		}
		out = append(out, id)
	}
	return out, nil
}
```

**Notes:**
- `make([]LinkedIdentity, 0, len(rows))` ensures a non-nil empty slice is returned
  even when `rows` is empty — satisfies D-01 (always array, never null).
- `r.CreatedAt.Time.UTC()` normalises to UTC; matches all other timestamps in the
  codebase.
- `string(r.Provider)` converts the `db.AuthProvider` typed enum to the plain
  string that the service layer and JSON response expect.

---

## Compile-time check

`store.go` already has:
```go
var _ Storer = (*Store)(nil)
```

After adding `GetUserIdentities`, this assertion will fail to compile if the
method signature doesn't exactly match the `Storer` interface. Fix any mismatch
before proceeding.

---

## Integration test stubs (I-layer — add to `store_test.go` or `identities_integration_test.go`)

These test stubs should be written now and run as part of Stage 6 (E2E). They
require a live DB (build tag: `integration`). Add them now to avoid forgetting
which cases need coverage.

```go
// T-01: Happy path — user with ≥1 identity returns correct fields
// T-02: User with 0 identities returns empty (non-nil) slice
// T-03: Two identities inserted at different times → older appears first (created_at ASC)
// T-09: access_token column excluded — GetUserIdentitiesRow has no access_token field
//       (verified by compile-time field access; confirmed by make sqlc output)
```

Actual test bodies will be filled in during Stage 6.

---

## Checklist before closing Stage 2

- [ ] `GetUserIdentities` store method implemented in `store.go`
- [ ] Returns `[]LinkedIdentity{}` (non-nil empty slice) when DB returns zero rows
- [ ] `pgtype.Text` nullability handled — nil pointer when `Valid == false`
- [ ] `pgtype.Timestamptz.Time.UTC()` used for `CreatedAt`
- [ ] `string(r.Provider)` used — no raw cast that could break on enum change
- [ ] Error wrapped with `fmt.Errorf("store.GetUserIdentities: query: %w", err)`
- [ ] `var _ Storer = (*Store)(nil)` still compiles
- [ ] `go build ./...` passes
- [ ] `go vet ./...` clean

Once all items are checked → proceed to Stage 3.
