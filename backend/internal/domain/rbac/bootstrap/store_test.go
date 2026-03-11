//go:build integration_test

package bootstrap_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/domain/rbac/bootstrap"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/jackc/pgx/v5/pgtype"
)

// testPool is the shared integration-test pool. Declared here so both
// store_test.go and integration_handler_test.go (same package, same build tag)
// can reference it.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	rbacsharedtest.RunTestMain(m, &testPool, 20)
}

// txStores begins a rolled-back transaction and returns a Store bound to it
// alongside the underlying *db.Queries for assertion queries and seed calls.
// Skips when testPool is nil (no database configured).
func txStores(t *testing.T) (*bootstrap.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured")
	}
	_, q := rbacsharedtest.MustBeginTx(t, testPool)
	return bootstrap.NewStore(testPool).WithQuerier(q), q
}

// withProxy sets the proxy's embedded Querier to q and returns a Store bound
// to the proxy. Used to inject per-method failures into individual store calls.
func withProxy(q db.Querier, proxy *rbacsharedtest.QuerierProxy) *bootstrap.Store {
	proxy.Querier = q
	return bootstrap.NewStore(testPool).WithQuerier(proxy)
}

// seedVerifiedUser inserts an active, email-verified user inside the test
// transaction and returns its UUID as [16]byte.
func seedVerifiedUser(t *testing.T, q *db.Queries) [16]byte {
	t.Helper()
	id, err := q.CreateVerifiedUserWithUsername(context.Background(), db.CreateVerifiedUserWithUsernameParams{
		Email:        pgtype.Text{String: rbacsharedtest.NewEmail(t), Valid: true},
		Username:     pgtype.Text{Valid: false},
		PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "S3cure!Pass"), Valid: true},
	})
	require.NoError(t, err)
	return [16]byte(id)
}

// seedOwnerRole assigns the owner role to userID directly through the querier,
// simulating a pre-existing bootstrapped system.
func seedOwnerRole(t *testing.T, q *db.Queries, userID [16]byte) {
	t.Helper()
	ownerRoleID, err := q.GetOwnerRoleID(context.Background())
	require.NoError(t, err)
	_, err = q.AssignUserRole(context.Background(), db.AssignUserRoleParams{
		UserID:        pgtype.UUID{Bytes: userID, Valid: true},
		RoleID:        pgtype.UUID{Bytes: [16]byte(ownerRoleID), Valid: true},
		GrantedBy:     pgtype.UUID{Bytes: userID, Valid: true},
		GrantedReason: "store test seed",
		ExpiresAt:     pgtype.Timestamptz{Valid: false},
	})
	require.NoError(t, err)
}

// ── TestCountActiveOwners_Integration ────────────────────────────────────────

func TestCountActiveOwners_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("returns 0 when no owner exists", func(t *testing.T) {
		s, _ := txStores(t)
		count, err := s.CountActiveOwners(ctx)
		require.NoError(t, err)
		require.EqualValues(t, 0, count)
	})

	t.Run("returns 1 after owner role is assigned", func(t *testing.T) {
		s, q := txStores(t)
		userID := seedVerifiedUser(t, q)
		seedOwnerRole(t, q, userID)
		count, err := s.CountActiveOwners(ctx)
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})

	t.Run("query error returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		_, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailCountActiveOwners: true}).
			CountActiveOwners(ctx)
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// ── TestGetOwnerRoleID_Integration ───────────────────────────────────────────

func TestGetOwnerRoleID_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("returns a valid non-zero UUID for the seeded owner role", func(t *testing.T) {
		s, _ := txStores(t)
		roleID, err := s.GetOwnerRoleID(ctx)
		require.NoError(t, err)
		require.NotEqual(t, [16]byte{}, roleID, "role UUID must not be the zero UUID")
	})

	t.Run("query error returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		_, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetOwnerRoleID: true}).
			GetOwnerRoleID(ctx)
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// ── TestGetActiveUserByID_Integration ────────────────────────────────────────

func TestGetActiveUserByID_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("found: returns BootstrapUser with correct fields", func(t *testing.T) {
		s, q := txStores(t)
		userID := seedVerifiedUser(t, q)

		user, err := s.GetActiveUserByID(ctx, userID)
		require.NoError(t, err)
		require.True(t, user.IsActive, "user must be active")
		require.True(t, user.EmailVerified, "user must be email-verified")
	})

	t.Run("not found returns ErrUserNotFound", func(t *testing.T) {
		s, _ := txStores(t)
		_, err := s.GetActiveUserByID(ctx, rbacsharedtest.RandomUUID())
		require.ErrorIs(t, err, rbacshared.ErrUserNotFound)
	})

	t.Run("query error returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		_, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetActiveUserByID: true}).
			GetActiveUserByID(ctx, rbacsharedtest.RandomUUID())
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// ── TestBootstrapOwnerTx_Integration ─────────────────────────────────────────

func TestBootstrapOwnerTx_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("assigns owner role and returns correct result", func(t *testing.T) {
		s, q := txStores(t)
		userID := seedVerifiedUser(t, q)

		roleID, err := s.GetOwnerRoleID(ctx)
		require.NoError(t, err)

		result, err := s.BootstrapOwnerTx(ctx, bootstrap.BootstrapTxInput{
			UserID: userID,
			RoleID: roleID,
		})
		require.NoError(t, err)
		require.Equal(t, "owner", result.RoleName)
		require.False(t, result.GrantedAt.IsZero(), "GrantedAt must be set")
	})

	t.Run("result UserID matches the input UserID", func(t *testing.T) {
		s, q := txStores(t)
		userID := seedVerifiedUser(t, q)
		roleID, err := s.GetOwnerRoleID(ctx)
		require.NoError(t, err)

		result, err := s.BootstrapOwnerTx(ctx, bootstrap.BootstrapTxInput{
			UserID: userID,
			RoleID: roleID,
		})
		require.NoError(t, err)
		// result.UserID is the UUID string form of userID
		require.NotEmpty(t, result.UserID)
	})

	t.Run("owner is visible via CountActiveOwners after tx", func(t *testing.T) {
		s, q := txStores(t)
		userID := seedVerifiedUser(t, q)
		roleID, err := s.GetOwnerRoleID(ctx)
		require.NoError(t, err)

		_, err = s.BootstrapOwnerTx(ctx, bootstrap.BootstrapTxInput{
			UserID: userID,
			RoleID: roleID,
		})
		require.NoError(t, err)

		count, err := s.CountActiveOwners(ctx)
		require.NoError(t, err)
		require.EqualValues(t, 1, count, "owner must be visible to CountActiveOwners after BootstrapOwnerTx")
	})

	t.Run("AssignUserRole error returns wrapped ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		s := withProxy(q, &rbacsharedtest.QuerierProxy{FailAssignUserRole: true})

		roleID, err := bootstrap.NewStore(testPool).WithQuerier(q).GetOwnerRoleID(ctx)
		require.NoError(t, err)

		uid := rbacsharedtest.RandomUUID()
		_, err = s.BootstrapOwnerTx(ctx, bootstrap.BootstrapTxInput{
			UserID: uid,
			RoleID: roleID,
		})
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}
