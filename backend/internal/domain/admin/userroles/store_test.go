//go:build integration_test

package userroles_test

import (
	"context"
	"errors"
	"testing"

	"github.com/7-Dany/store/backend/internal/db"
	adminsharedtest "github.com/7-Dany/store/backend/internal/domain/admin/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/admin/userroles"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	adminsharedtest.RunTestMain(m, &testPool, 20)
}

func txStores(t *testing.T) (*userroles.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured")
	}
	_, q := adminsharedtest.MustBeginTx(t, testPool)
	return userroles.NewStore(testPool).WithQuerier(q), q
}

func withProxy(q db.Querier, proxy *adminsharedtest.QuerierProxy) *userroles.Store {
	proxy.Querier = q
	return userroles.NewStore(testPool).WithQuerier(proxy)
}

func createTestUser(t *testing.T, q *db.Queries) pgtype.UUID {
	t.Helper()
	userID, err := q.CreateActiveUnverifiedUserForTest(context.Background(), db.CreateActiveUnverifiedUserForTestParams{
		Email:        pgtype.Text{String: adminsharedtest.NewEmail(t), Valid: true},
		PasswordHash: pgtype.Text{String: adminsharedtest.MustHashPassword(t, "test-password"), Valid: true},
	})
	require.NoError(t, err)
	return pgtype.UUID{Bytes: [16]byte(userID), Valid: true}
}

func TestAssignAndGetUserRole_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("assigns role and GetUserRole returns it", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		userID := [16]byte(userPgtypeID.Bytes)
		adminRole, err := q.GetRoleByName(ctx, "admin")
		require.NoError(t, err)
		grantedBy := createTestUser(t, q)
		in := userroles.AssignRoleTxInput{
			UserID:        userID,
			RoleID:        [16]byte(adminRole.ID),
			GrantedBy:     [16]byte(grantedBy.Bytes),
			GrantedReason: "integration test",
		}
		result, err := s.AssignUserRoleTx(ctx, in)
		require.NoError(t, err)
		require.Equal(t, "admin", result.RoleName)
		require.False(t, result.IsOwnerRole)
		got, err := s.GetUserRole(ctx, userID)
		require.NoError(t, err)
		require.Equal(t, "admin", got.RoleName)
		require.Equal(t, result.RoleID, got.RoleID)
	})
}

func TestAssignUserRole_Replaces_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("replaces an existing role assignment", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		userID := [16]byte(userPgtypeID.Bytes)
		grantedBy := createTestUser(t, q)
		adminRole, err := q.GetRoleByName(ctx, "admin")
		require.NoError(t, err)
		vendorRole, err := q.GetRoleByName(ctx, "vendor")
		require.NoError(t, err)
		_, err = s.AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
			UserID: userID, RoleID: [16]byte(adminRole.ID),
			GrantedBy: [16]byte(grantedBy.Bytes), GrantedReason: "initial",
		})
		require.NoError(t, err)
		result, err := s.AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
			UserID: userID, RoleID: [16]byte(vendorRole.ID),
			GrantedBy: [16]byte(grantedBy.Bytes), GrantedReason: "replacement",
		})
		require.NoError(t, err)
		require.Equal(t, "vendor", result.RoleName)
	})
}

func TestAssignUserRole_RoleNotFound_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("unknown role_id returns ErrRoleNotFound", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		userID := [16]byte(userPgtypeID.Bytes)
		grantedBy := createTestUser(t, q)
		_, err := s.AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
			UserID:        userID,
			RoleID:        adminsharedtest.MustUUID("aaaaaaaa-bbbb-cccc-dddd-ffffffffffff"),
			GrantedBy:     [16]byte(grantedBy.Bytes),
			GrantedReason: "test",
		})
		require.ErrorIs(t, err, userroles.ErrRoleNotFound)
	})
}

func TestRemoveUserRole_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("removes assignment; subsequent GetUserRole returns ErrUserRoleNotFound", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		userID := [16]byte(userPgtypeID.Bytes)
		grantedBy := createTestUser(t, q)
		adminRole, err := q.GetRoleByName(ctx, "admin")
		require.NoError(t, err)
		_, err = s.AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
			UserID: userID, RoleID: [16]byte(adminRole.ID),
			GrantedBy: [16]byte(grantedBy.Bytes), GrantedReason: "to be removed",
		})
		require.NoError(t, err)
		err = s.RemoveUserRole(ctx, userID, uuid.UUID(grantedBy.Bytes).String())
		require.NoError(t, err)
		_, err = s.GetUserRole(ctx, userID)
		require.ErrorIs(t, err, userroles.ErrUserRoleNotFound)
	})
}

func TestRemoveUserRole_NoAssignment_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("returns ErrUserRoleNotFound when no assignment exists", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		userID := [16]byte(userPgtypeID.Bytes)
		grantedBy := createTestUser(t, q)
		err := s.RemoveUserRole(ctx, userID, uuid.UUID(grantedBy.Bytes).String())
		require.ErrorIs(t, err, userroles.ErrUserRoleNotFound)
	})
}

func TestRemoveUserRole_LastOwner_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("returns ErrLastOwnerRemoval when removing the last owner", func(t *testing.T) {
		if testPool == nil {
			t.Skip("no test database configured")
		}
		q0 := db.New(testPool)
		ownerCount, err := q0.CountActiveOwners(context.Background())
		require.NoError(t, err)
		if ownerCount > 0 {
			t.Skip("bootstrapped owner exists; last-owner trigger test requires an empty owner set")
		}
		q := db.New(testPool)
		ownerRoleID, err := q.GetOwnerRoleID(ctx)
		require.NoError(t, err)
		_, q2 := adminsharedtest.MustBeginTx(t, testPool)
		s2 := userroles.NewStore(testPool).WithQuerier(q2)
		ownerUserID := createTestUser(t, q2)
		granterID := createTestUser(t, q2)
		_, err = q2.AssignUserRole(ctx, db.AssignUserRoleParams{
			UserID:        ownerUserID,
			RoleID:        pgtype.UUID{Bytes: [16]byte(ownerRoleID), Valid: true},
			GrantedBy:     granterID,
			GrantedReason: "last owner test",
		})
		require.NoError(t, err)
		err = s2.RemoveUserRole(ctx, [16]byte(ownerUserID.Bytes), uuid.UUID(granterID.Bytes).String())
		require.ErrorIs(t, err, userroles.ErrLastOwnerRemoval)
	})
}

func TestAssignUserRoleTx_InactiveRole_Integration(t *testing.T) {
	ctx := context.Background()
	s, q := txStores(t)
	userPgtypeID := createTestUser(t, q)
	userID := [16]byte(userPgtypeID.Bytes)
	grantedBy := createTestUser(t, q)
	newRole, err := q.CreateRole(ctx, db.CreateRoleParams{
		Name: "test-inactive-" + adminsharedtest.ShortID(),
	})
	require.NoError(t, err)
	rows, err := q.DeactivateRole(ctx, pgtype.UUID{Bytes: [16]byte(newRole.ID), Valid: true})
	require.NoError(t, err)
	require.EqualValues(t, 1, rows)
	_, err = s.AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
		UserID: userID, RoleID: [16]byte(newRole.ID),
		GrantedBy: [16]byte(grantedBy.Bytes), GrantedReason: "should fail",
	})
	require.ErrorIs(t, err, userroles.ErrRoleNotFound)
}

func TestAssignUserRoleTx_FailGetRoleByID_Integration(t *testing.T) {
	ctx := context.Background()
	_, q := txStores(t)
	userPgtypeID := createTestUser(t, q)
	userID := [16]byte(userPgtypeID.Bytes)
	grantedBy := createTestUser(t, q)
	adminRole, err := q.GetRoleByName(ctx, "admin")
	require.NoError(t, err)
	_, err = withProxy(q, &adminsharedtest.QuerierProxy{FailGetRoleByID: true}).
		AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
			UserID: userID, RoleID: [16]byte(adminRole.ID),
			GrantedBy: [16]byte(grantedBy.Bytes), GrantedReason: "proxy test",
		})
	require.ErrorIs(t, err, adminsharedtest.ErrProxy)
}

func TestAssignUserRoleTx_FailAssignUserRole_Integration(t *testing.T) {
	ctx := context.Background()
	_, q := txStores(t)
	userPgtypeID := createTestUser(t, q)
	userID := [16]byte(userPgtypeID.Bytes)
	grantedBy := createTestUser(t, q)
	adminRole, err := q.GetRoleByName(ctx, "admin")
	require.NoError(t, err)
	_, err = withProxy(q, &adminsharedtest.QuerierProxy{FailAssignUserRole: true}).
		AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
			UserID: userID, RoleID: [16]byte(adminRole.ID),
			GrantedBy: [16]byte(grantedBy.Bytes), GrantedReason: "proxy test",
		})
	require.ErrorIs(t, err, adminsharedtest.ErrProxy)
}

func TestIsOrphanedOwnerViolation_Unit(t *testing.T) {
	fn := userroles.IsOrphanedOwnerViolation
	require.False(t, fn(nil), "nil error")
	require.False(t, fn(errors.New("foo")), "plain error")
	require.False(t, fn(&pgconn.PgError{Code: "23000", Message: "something else"}), "23000 wrong message")
	require.False(t, fn(&pgconn.PgError{Code: "23001", Message: "last active owner"}), "wrong code")
	require.True(t, fn(&pgconn.PgError{Code: "23000", Message: "cannot remove last active owner"}), "match")
}

func TestGetUserRole_FailProxy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("FailGetUserRole returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		_, err := withProxy(q, &adminsharedtest.QuerierProxy{FailGetUserRole: true}).
			GetUserRole(ctx, [16]byte(userPgtypeID.Bytes))
		require.ErrorIs(t, err, adminsharedtest.ErrProxy)
	})
}

func TestRemoveUserRole_FailProxy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("FailRemoveUserRole returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		err := withProxy(q, &adminsharedtest.QuerierProxy{FailRemoveUserRole: true}).
			RemoveUserRole(ctx, [16]byte(userPgtypeID.Bytes), "ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb")
		require.ErrorIs(t, err, adminsharedtest.ErrProxy)
	})
}
