//go:build integration_test

// Package username_test contains store-layer integration tests for the username
// availability check and username mutation endpoints.
//
// Coverage:
//
//	T-12 (I)   CheckUsernameAvailable: no matching row → available = true.
//	T-13 (I)   CheckUsernameAvailable: matching row exists → available = false.
//	           FailCheckUsernameAvailable → ErrProxy.
//	T-33 (I)   UpdateUsernameTx: happy path — username column updated in DB.
//	T-34 (I)   UpdateUsernameTx: unique violation → ErrUsernameTaken.
//	T-35 (I)   UpdateUsernameTx: same username → ErrSameUsername.
//	T-36 (I)   UpdateUsernameTx: audit row written with event_type = "username_changed".
//	T-37 (I)   UpdateUsernameTx: FailGetUserForUsernameUpdate → ErrProxy.
//	T-38 (I)   UpdateUsernameTx: FailSetUsername → ErrProxy.
//	           FailInsertAuditLog → ErrProxy.
//	           Unknown user ID → ErrUserNotFound.
//	           Audit row NOT written when SetUsername proxy fails.
//
// Run with:
//
//	go test -tags integration_test ./internal/domain/profile/username/... -v
package username_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/7-Dany/store/backend/internal/domain/profile/username"
)

// testPool is the integration-test connection pool. Nil during unit-only runs;
// integration tests skip automatically via txStores when nil.
var testPool *pgxpool.Pool

// TestMain lowers bcrypt cost and (when TEST_DATABASE_URL is set) initialises
// testPool. All unit tests in this package also benefit from the lower cost.
func TestMain(m *testing.M) { authsharedtest.RunTestMain(m, &testPool, 20) }

// txStores begins a rolled-back test transaction and returns a *username.Store
// and the raw *db.Queries both bound to that transaction.
func txStores(t *testing.T) (*username.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("integration_test: testPool is nil — set TEST_DATABASE_URL")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	return username.NewStore(testPool).WithQuerier(q), q
}

// withProxy wraps q in proxy and returns a *username.Store that uses the proxy.
func withProxy(q db.Querier, proxy *authsharedtest.QuerierProxy) *username.Store {
	proxy.Base = q
	return username.NewStore(testPool).WithQuerier(proxy)
}

// createUserWithUsername inserts a user via the standard helper, then sets
// their username column to uname using the querier bound to the test transaction.
func createUserWithUsername(t *testing.T, q *db.Queries, email, uname string) [16]byte {
	t.Helper()
	uid := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
	_, err := q.SetUsername(context.Background(), db.SetUsernameParams{
		Username: pgtype.Text{String: uname, Valid: true},
		UserID:   authsharedtest.ToPgtypeUUID(uid),
	})
	require.NoError(t, err, "createUserWithUsername: SetUsername failed")
	return uid
}

// buildUpdateInput returns an UpdateUsernameInput for the given user and new username.
func buildUpdateInput(uid [16]byte, newUsername string) username.UpdateUsernameInput {
	return username.UpdateUsernameInput{
		UserID:    uid,
		Username:  newUsername,
		IPAddress: "127.0.0.1",
		UserAgent: "go-test/1.0",
	}
}

// ── TestCheckUsernameAvailable_Integration ────────────────────────────────────

func TestCheckUsernameAvailable_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// ── T-12: available — no row with that username ────────────────────────────

	t.Run("T-12: username with no matching row returns available = true", func(t *testing.T) {
		t.Parallel()
		s, _ := txStores(t)
		available, err := s.CheckUsernameAvailable(ctx, "ghost_user_xyz")
		require.NoError(t, err)
		require.True(t, available, "username not in DB must be available")
	})

	// ── T-13: taken — existing user owns the username ─────────────────────────

	t.Run("T-13: username belonging to an existing user returns available = false", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		// Use a short UUID fragment to keep the username within the 30-char limit.
		uname := "usr_" + uuid.New().String()[:8]
		createUserWithUsername(t, q, authsharedtest.NewEmail(t), uname)

		s := username.NewStore(testPool).WithQuerier(q)
		available, err := s.CheckUsernameAvailable(ctx, uname)
		require.NoError(t, err)
		require.False(t, available, "username owned by an existing user must not be available")
	})

	// ── FailCheckUsernameAvailable → ErrProxy ─────────────────────────────────

	t.Run("FailCheckUsernameAvailable returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailCheckUsernameAvailable = true
		_, err := withProxy(q, proxy).CheckUsernameAvailable(ctx, "alice_wonder")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestUpdateUsernameTx_Integration ─────────────────────────────────────────

func TestUpdateUsernameTx_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// ── T-33: happy path — username column updated ────────────────────────────

	t.Run("T-33: happy path — username column updated in DB", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		uid := createUserWithUsername(t, q, authsharedtest.NewEmail(t), "old_username")

		s := username.NewStore(testPool).WithQuerier(q)
		require.NoError(t, s.UpdateUsernameTx(ctx, buildUpdateInput(uid, "new_username")))

		// Read back via GetUserForUsernameUpdate to confirm the column changed.
		row, err := q.GetUserForUsernameUpdate(ctx, authsharedtest.ToPgtypeUUID(uid))
		require.NoError(t, err)
		require.Equal(t, "new_username", row.Username.String,
			"username column must be updated after a successful UpdateUsernameTx")
	})

	// ── T-34: unique violation → ErrUsernameTaken ─────────────────────────────

	t.Run("T-34: username already taken by another user returns ErrUsernameTaken", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		takenName := "taken_" + uuid.New().String()[:8]
		createUserWithUsername(t, q, authsharedtest.NewEmail(t), takenName)      // user 1 owns takenName
		uid2 := createUserWithUsername(t, q, authsharedtest.NewEmail(t), "other") // user 2

		s := username.NewStore(testPool).WithQuerier(q)
		err := s.UpdateUsernameTx(ctx, buildUpdateInput(uid2, takenName))
		require.ErrorIs(t, err, username.ErrUsernameTaken)
	})

	// ── T-35: same username → ErrSameUsername ────────────────────────────────

	t.Run("T-35: setting the same username returns ErrSameUsername", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		currentName := "same_" + uuid.New().String()[:8]
		uid := createUserWithUsername(t, q, authsharedtest.NewEmail(t), currentName)

		s := username.NewStore(testPool).WithQuerier(q)
		err := s.UpdateUsernameTx(ctx, buildUpdateInput(uid, currentName))
		require.ErrorIs(t, err, username.ErrSameUsername)
	})

	// ── T-36: audit row written ───────────────────────────────────────────────

	t.Run("T-36: audit row with event_type=username_changed is written on success", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		uid := createUserWithUsername(t, q, authsharedtest.NewEmail(t), "audit_old")

		s := username.NewStore(testPool).WithQuerier(q)
		require.NoError(t, s.UpdateUsernameTx(ctx, buildUpdateInput(uid, "audit_new")))

		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(uid),
			EventType: string(audit.EventUsernameChanged),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count,
			"exactly one username_changed audit row must be written on success")
	})

	// ── T-37: FailGetUserForUsernameUpdate → ErrProxy ─────────────────────────

	t.Run("T-37: FailGetUserForUsernameUpdate returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetUserForUsernameUpdate = true
		err := withProxy(q, proxy).UpdateUsernameTx(ctx,
			buildUpdateInput(authsharedtest.RandomUUID(), "any_name"))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	// ── T-38: FailSetUsername → ErrProxy ─────────────────────────────────────

	t.Run("T-38: FailSetUsername returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		uid := createUserWithUsername(t, q, authsharedtest.NewEmail(t), "proxy_old")
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailSetUsername = true
		err := withProxy(q, proxy).UpdateUsernameTx(ctx, buildUpdateInput(uid, "proxy_new"))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	// ── FailInsertAuditLog → ErrProxy ─────────────────────────────────────────

	t.Run("FailInsertAuditLog returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		uid := createUserWithUsername(t, q, authsharedtest.NewEmail(t), "auditfail_old")
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		err := withProxy(q, proxy).UpdateUsernameTx(ctx, buildUpdateInput(uid, "auditfail_new"))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	// ── Unknown user ID → ErrUserNotFound ─────────────────────────────────────

	t.Run("unknown user ID returns ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		s, _ := txStores(t)
		err := s.UpdateUsernameTx(ctx,
			buildUpdateInput(authsharedtest.RandomUUID(), "any_name"))
		require.ErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	// ── Audit row NOT written when SetUsername proxy fails ────────────────────

	t.Run("audit row is NOT written when SetUsername proxy fails", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		uid := createUserWithUsername(t, q, authsharedtest.NewEmail(t), "rollback_old")
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailSetUsername = true
		_ = withProxy(q, proxy).UpdateUsernameTx(ctx, buildUpdateInput(uid, "rollback_new"))

		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(uid),
			EventType: string(audit.EventUsernameChanged),
		})
		require.NoError(t, err)
		require.EqualValues(t, 0, count,
			"audit row must NOT be written when the UPDATE step fails")
	})
}
