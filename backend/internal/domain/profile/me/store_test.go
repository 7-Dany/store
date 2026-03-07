//go:build integration_test

package me_test

import (
	"context"
	"testing"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/profile/me"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) { authsharedtest.RunTestMain(m, &testPool, 20) }

// txStores begins a rolled-back test transaction and returns a *me.Store
// and the raw *db.Queries bound to that transaction.
func txStores(t *testing.T) (*me.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("integration_test: testPool is nil — set TEST_DATABASE_URL")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	return me.NewStore(testPool).WithQuerier(q), q
}

// createUser inserts a test user scoped to the test transaction bound to q and
// returns its UUID.
func createUser(t *testing.T, q db.Querier, email string) uuid.UUID {
	t.Helper()
	return authsharedtest.CreateUserUUID(t, testPool, q, email)
}

// ── TestGetUserProfile_Integration ───────────────────────────────────────────

// TestGetUserProfile_Integration covers store.GetUserProfile.
func TestGetUserProfile_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("found returns correct fields", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		uid := createUser(t, q, email)
		userID := [16]byte(uid)

		p, err := ps.GetUserProfile(ctx, userID)
		require.NoError(t, err)
		require.Equal(t, userID, p.ID)
		require.Equal(t, email, p.Email)
		require.False(t, p.EmailVerified)
		require.False(t, p.IsActive)
		require.Nil(t, p.LastLoginAt)
		require.False(t, p.CreatedAt.IsZero())
	})

	t.Run("LastLoginAt is non-nil after LoginTx", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)

		authsharedtest.CreateSession(t, testPool, q, userID)

		p, err := ps.GetUserProfile(ctx, userID)
		require.NoError(t, err)
		require.NotNil(t, p.LastLoginAt)
	})

	t.Run("not found returns ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		ps, _ := txStores(t)
		_, err := ps.GetUserProfile(ctx, [16]byte(uuid.New()))
		require.ErrorIs(t, err, authshared.ErrUserNotFound)
	})

	t.Run("db.Querier failure returns wrapped error", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetUserProfile = true
		_, err := me.NewStore(testPool).WithQuerier(proxy).GetUserProfile(ctx, [16]byte(uuid.New()))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("DisplayName and AvatarURL are empty strings when NULL in DB", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := authsharedtest.CreateUserWithNullDisplayName(t, testPool, q, authsharedtest.NewEmail(t))
		p, err := ps.GetUserProfile(ctx, [16]byte(uid))
		require.NoError(t, err)
		assert.Equal(t, "", p.DisplayName, "NULL display_name must map to empty string")
		assert.Equal(t, "", p.AvatarURL, "NULL avatar_url must map to empty string")
	})

	t.Run("DisplayName populated correctly when non-NULL", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		p, err := ps.GetUserProfile(ctx, [16]byte(uid))
		require.NoError(t, err)
		assert.Equal(t, "Test User", p.DisplayName, "non-NULL display_name must be returned verbatim")
	})

	t.Run("AvatarURL populated correctly when non-NULL", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)

		const wantURL = "https://cdn.example.com/avatar.png"
		require.NoError(t, q.SetAvatarURLForTest(ctx, db.SetAvatarURLForTestParams{
			AvatarURL: wantURL,
			UserID:    authsharedtest.ToPgtypeUUID(userID),
		}))

		p, err := ps.GetUserProfile(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, wantURL, p.AvatarURL, "non-NULL avatar_url must be returned verbatim")
	})

	t.Run("IsLocked reflects DB state when is_locked = TRUE", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		uid := createUser(t, q, email)

		require.NoError(t, q.LockUserForTest(ctx, email))

		p, err := ps.GetUserProfile(ctx, [16]byte(uid))
		require.NoError(t, err)
		assert.True(t, p.IsLocked, "is_locked = TRUE in DB must be reflected in UserProfile.IsLocked")
	})

	t.Run("AdminLocked reflects DB state when admin_locked = TRUE", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		uid := createUser(t, q, email)

		require.NoError(t, q.AdminLockUserForTest(ctx, email))

		p, err := ps.GetUserProfile(ctx, [16]byte(uid))
		require.NoError(t, err)
		assert.True(t, p.AdminLocked, "admin_locked = TRUE in DB must be reflected in UserProfile.AdminLocked")
	})
}

// ── TestUpdateProfileTx_Integration ──────────────────────────────────────────

// TestUpdateProfileTx_Integration covers store.UpdateProfileTx.
func TestUpdateProfileTx_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// T-01 / T-19: both fields updated; read-back confirms both columns changed.
	t.Run("T-01/T-19 both fields updated in DB", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)

		newDN := "Alice Updated"
		newURL := "https://cdn.example.com/alice.png"
		err := ps.UpdateProfileTx(ctx, me.UpdateProfileInput{
			UserID:      userID,
			DisplayName: &newDN,
			AvatarURL:   &newURL,
			IPAddress:   "127.0.0.1",
			UserAgent:   "go-test/1.0",
		})
		require.NoError(t, err)

		p, err := ps.GetUserProfile(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, newDN, p.DisplayName, "display_name must be updated")
		assert.Equal(t, newURL, p.AvatarURL, "avatar_url must be updated")
	})

	// T-02 / T-20: display_name only — avatar_url must remain unchanged.
	t.Run("T-02/T-20 display_name only, avatar_url unchanged", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)

		const originalURL = "https://original.example.com/img.png"
		require.NoError(t, q.SetAvatarURLForTest(ctx, db.SetAvatarURLForTestParams{
			AvatarURL: originalURL,
			UserID:    authsharedtest.ToPgtypeUUID(userID),
		}))

		newDN := "Bob"
		err := ps.UpdateProfileTx(ctx, me.UpdateProfileInput{
			UserID:      userID,
			DisplayName: &newDN,
			AvatarURL:   nil,
			IPAddress:   "127.0.0.1",
			UserAgent:   "go-test/1.0",
		})
		require.NoError(t, err)

		p, err := ps.GetUserProfile(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, newDN, p.DisplayName, "display_name must be updated")
		assert.Equal(t, originalURL, p.AvatarURL, "avatar_url must remain unchanged")
	})

	// T-03 / T-21: avatar_url only — display_name must remain unchanged.
	t.Run("T-03/T-21 avatar_url only, display_name unchanged", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)

		newURL := "https://new.example.com/avatar.png"
		err := ps.UpdateProfileTx(ctx, me.UpdateProfileInput{
			UserID:      userID,
			DisplayName: nil,
			AvatarURL:   &newURL,
			IPAddress:   "127.0.0.1",
			UserAgent:   "go-test/1.0",
		})
		require.NoError(t, err)

		p, err := ps.GetUserProfile(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, "Test User", p.DisplayName, "display_name must remain unchanged")
		assert.Equal(t, newURL, p.AvatarURL, "avatar_url must be updated")
	})

	// T-22: FailUpdateUserProfile — UpdateUserProfile query failure returns error.
	t.Run("T-22 FailUpdateUserProfile returns wrapped error", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailUpdateUserProfile = true

		newDN := "Charlie"
		err := me.NewStore(testPool).WithQuerier(proxy).UpdateProfileTx(ctx, me.UpdateProfileInput{
			UserID:      [16]byte(authsharedtest.RandomUUID()),
			DisplayName: &newDN,
			IPAddress:   "127.0.0.1",
			UserAgent:   "go-test/1.0",
		})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	// T-23: FailInsertAuditLog — InsertAuditLog query failure returns error.
	t.Run("T-23 FailInsertAuditLog returns wrapped error", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true

		newDN := "Dave"
		err := me.NewStore(testPool).WithQuerier(proxy).UpdateProfileTx(ctx, me.UpdateProfileInput{
			UserID:      [16]byte(authsharedtest.RandomUUID()),
			DisplayName: &newDN,
			IPAddress:   "127.0.0.1",
			UserAgent:   "go-test/1.0",
		})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	// T-24: audit row written with event_type = "profile_updated" after successful update.
	t.Run("T-24 audit row written with profile_updated event", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)

		newDN := "Eve"
		newURL := "https://cdn.example.com/eve.png"
		err := ps.UpdateProfileTx(ctx, me.UpdateProfileInput{
			UserID:      userID,
			DisplayName: &newDN,
			AvatarURL:   &newURL,
			IPAddress:   "127.0.0.1",
			UserAgent:   "go-test/1.0",
		})
		require.NoError(t, err)

		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventProfileUpdated),
		})
		require.NoError(t, err)
		assert.EqualValues(t, 1, count,
			"exactly one profile_updated audit row must be written per UpdateProfileTx call")
	})
}
