//go:build integration_test

package userlock_test

import (
	"context"
	"testing"

	"github.com/7-Dany/store/backend/internal/db"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/admin/userlock"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	rbacsharedtest.RunTestMain(m, &testPool, 20)
}

// txStores begins a rolled-back transaction and returns a Store bound to it
// alongside *db.Queries for direct assertion queries.
func txStores(t *testing.T) (*userlock.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured")
	}
	_, q := rbacsharedtest.MustBeginTx(t, testPool)
	return userlock.NewStore(testPool).WithQuerier(q), q
}

// withProxy wires q into proxy.Querier and returns a Store bound to it.
func withProxy(q db.Querier, proxy *rbacsharedtest.QuerierProxy) *userlock.Store {
	proxy.Querier = q
	return userlock.NewStore(testPool).WithQuerier(proxy)
}

// createTestUser inserts an active user and returns its pgtype.UUID.
func createTestUser(t *testing.T, q *db.Queries) pgtype.UUID {
	t.Helper()
	userID, err := q.CreateActiveUnverifiedUserForTest(context.Background(), db.CreateActiveUnverifiedUserForTestParams{
		Email:        pgtype.Text{String: rbacsharedtest.NewEmail(t), Valid: true},
		PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "test-password"), Valid: true},
	})
	require.NoError(t, err)
	return pgtype.UUID{Bytes: [16]byte(userID), Valid: true}
}

// assignOwnerRole assigns the owner role to userPgtypeID using the seeded owner role.
func assignOwnerRole(t *testing.T, ctx context.Context, q *db.Queries, userPgtypeID pgtype.UUID) {
	t.Helper()
	ownerRoleID, err := q.GetOwnerRoleID(ctx)
	require.NoError(t, err)
	_, err = q.AssignUserRole(ctx, db.AssignUserRoleParams{
		UserID:        userPgtypeID,
		RoleID:        pgtype.UUID{Bytes: [16]byte(ownerRoleID), Valid: true},
		GrantedBy:     userPgtypeID,
		GrantedReason: "test setup",
	})
	require.NoError(t, err)
}

// ── T-R45i: LockUserTx sets admin_locked=TRUE; GetLockStatus reflects it ─────

func TestLockUserTx_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("LockUserTx sets admin_locked; GetLockStatus reflects it", func(t *testing.T) {
		s, q := txStores(t)
		targetPgID := createTestUser(t, q)
		actorPgID := createTestUser(t, q)
		targetID := [16]byte(targetPgID.Bytes)
		actorID := [16]byte(actorPgID.Bytes)

		err := s.LockUserTx(ctx, userlock.LockUserTxInput{
			UserID:   targetID,
			LockedBy: actorID,
			Reason:   "integration test",
		})
		require.NoError(t, err)

		status, err := s.GetLockStatus(ctx, targetID)
		require.NoError(t, err)
		require.True(t, status.AdminLocked)
		require.NotNil(t, status.LockedBy)
		require.NotNil(t, status.LockedReason)
		require.Equal(t, "integration test", *status.LockedReason)
	})
}

// ── T-R46i: UnlockUser clears admin_locked; GetLockStatus reflects it ─────────

func TestUnlockUser_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("UnlockUser clears admin_locked; GetLockStatus reflects it", func(t *testing.T) {
		s, q := txStores(t)
		targetPgID := createTestUser(t, q)
		actorPgID := createTestUser(t, q)
		targetID := [16]byte(targetPgID.Bytes)
		actorID := [16]byte(actorPgID.Bytes)
		actorIDStr := uuid.UUID(actorID).String()

		// Lock first
		err := s.LockUserTx(ctx, userlock.LockUserTxInput{
			UserID:   targetID,
			LockedBy: actorID,
			Reason:   "to be unlocked",
		})
		require.NoError(t, err)

		// Unlock
		err = s.UnlockUserTx(ctx, targetID, actorIDStr)
		require.NoError(t, err)

		status, err := s.GetLockStatus(ctx, targetID)
		require.NoError(t, err)
		require.False(t, status.AdminLocked)
		require.Nil(t, status.LockedBy)
	})
}

// ── T-R47i: GetLockStatus returns ErrUserNotFound for non-existent user ───────

func TestGetLockStatus_UserNotFound_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("non-existent user returns ErrUserNotFound", func(t *testing.T) {
		s, _ := txStores(t)
		nonExistentID := rbacsharedtest.RandomUUID()
		_, err := s.GetLockStatus(ctx, nonExistentID)
		require.ErrorIs(t, err, userlock.ErrUserNotFound)
	})
}

// ── T-R49i: IsOwnerUser returns true for user with owner role ─────────────────

func TestIsOwnerUser_OwnerRole_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("IsOwnerUser returns true for user with owner role", func(t *testing.T) {
		s, q := txStores(t)
		userPgID := createTestUser(t, q)
		assignOwnerRole(t, ctx, q, userPgID)

		isOwner, err := s.IsOwnerUser(ctx, [16]byte(userPgID.Bytes))
		require.NoError(t, err)
		require.True(t, isOwner)
	})
}

// ── T-R49j: IsOwnerUser returns false for user with no role ───────────────────

func TestIsOwnerUser_NoRole_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("IsOwnerUser returns false for user with no role", func(t *testing.T) {
		s, q := txStores(t)
		userPgID := createTestUser(t, q)

		isOwner, err := s.IsOwnerUser(ctx, [16]byte(userPgID.Bytes))
		require.NoError(t, err)
		require.False(t, isOwner)
	})
}

// ── Proxy tests ───────────────────────────────────────────────────────────────

// T-R45p: FailLockUser proxy → ErrProxy
func TestLockUser_FailProxy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("FailLockUser returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		targetPgID := createTestUser(t, q)
		actorPgID := createTestUser(t, q)
		err := withProxy(q, &rbacsharedtest.QuerierProxy{FailLockUser: true}).
			LockUserTx(ctx, userlock.LockUserTxInput{
				UserID:   [16]byte(targetPgID.Bytes),
				LockedBy: [16]byte(actorPgID.Bytes),
				Reason:   "proxy test",
			})
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// T-R46p: FailUnlockUser proxy → ErrProxy
func TestUnlockUser_FailProxy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("FailUnlockUser returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		targetPgID := createTestUser(t, q)
		actorIDStr := uuid.New().String()
		err := withProxy(q, &rbacsharedtest.QuerierProxy{FailUnlockUser: true}).
			UnlockUserTx(ctx, [16]byte(targetPgID.Bytes), actorIDStr)
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// T-R47p: FailGetUserLockStatus proxy → ErrProxy
func TestGetLockStatus_FailProxy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("FailGetUserLockStatus returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		targetPgID := createTestUser(t, q)
		_, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetUserLockStatus: true}).
			GetLockStatus(ctx, [16]byte(targetPgID.Bytes))
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// T-R49p: FailGetUserRole proxy → ErrProxy in IsOwnerUser
func TestIsOwnerUser_FailProxy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("FailGetUserRole returns ErrProxy in IsOwnerUser", func(t *testing.T) {
		_, q := txStores(t)
		targetPgID := createTestUser(t, q)
		_, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetUserRole: true}).
			IsOwnerUser(ctx, [16]byte(targetPgID.Bytes))
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}
