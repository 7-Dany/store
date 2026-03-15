//go:build integration_test

package userpermissions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/db"
	adminshared "github.com/7-Dany/store/backend/internal/domain/admin/shared"
	adminsharedtest "github.com/7-Dany/store/backend/internal/domain/admin/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/admin/userpermissions"
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

func txStores(t *testing.T) (*userpermissions.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured")
	}
	_, q := adminsharedtest.MustBeginTx(t, testPool)
	return userpermissions.NewStore(testPool).WithQuerier(q), q
}

func withProxy(q db.Querier, proxy *adminsharedtest.QuerierProxy) *userpermissions.Store {
	proxy.Querier = q
	return userpermissions.NewStore(testPool).WithQuerier(proxy)
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

func getAnyPermissionID(t *testing.T, q *db.Queries) pgtype.UUID {
	t.Helper()
	perms, err := q.GetPermissions(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, perms, "database must have at least one seeded permission")
	return pgtype.UUID{Bytes: [16]byte(perms[0].ID), Valid: true}
}

func TestGrantPermissionTx_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("inserts and GetUserPermissions returns it", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		granterPgtypeID := createTestUser(t, q)
		permID := getAnyPermissionID(t, q)
		userID := [16]byte(userPgtypeID.Bytes)
		granterID := [16]byte(granterPgtypeID.Bytes)
		ownerRoleID, err := q.GetOwnerRoleID(ctx)
		require.NoError(t, err)
		_, err = q.AssignUserRole(ctx, db.AssignUserRoleParams{
			UserID: granterPgtypeID, RoleID: pgtype.UUID{Bytes: [16]byte(ownerRoleID), Valid: true},
			GrantedBy: granterPgtypeID, GrantedReason: "test setup",
		})
		require.NoError(t, err)
		result, err := s.GrantPermissionTx(ctx, userpermissions.GrantPermissionTxInput{
			UserID: userID, PermissionID: [16]byte(permID.Bytes), GrantedBy: granterID,
			GrantedReason: "integration test", Scope: "own", ExpiresAt: time.Now().Add(time.Hour),
		})
		require.NoError(t, err)
		require.NotEmpty(t, result.ID)
		perms, err := s.GetUserPermissions(ctx, userID)
		require.NoError(t, err)
		require.Len(t, perms, 1)
		require.Equal(t, result.ID, perms[0].ID)
	})
}

func TestGrantPermissionTx_ScopeAll_AnyPolicy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("scope=all on any-policy permission succeeds", func(t *testing.T) {
		s, q := txStores(t)
		allPerms, err := q.GetPermissions(ctx)
		require.NoError(t, err)
		var anyPolicyPermID pgtype.UUID
		for _, p := range allPerms {
			if p.ScopePolicy == db.PermissionScopePolicyAny {
				anyPolicyPermID = pgtype.UUID{Bytes: [16]byte(p.ID), Valid: true}
				break
			}
		}
		if !anyPolicyPermID.Valid {
			t.Skip("no permission with scope_policy=any found in seed data")
		}
		userPgtypeID := createTestUser(t, q)
		granterPgtypeID := createTestUser(t, q)
		ownerRoleID, err := q.GetOwnerRoleID(ctx)
		require.NoError(t, err)
		_, err = q.AssignUserRole(ctx, db.AssignUserRoleParams{
			UserID: granterPgtypeID, RoleID: pgtype.UUID{Bytes: [16]byte(ownerRoleID), Valid: true},
			GrantedBy: granterPgtypeID, GrantedReason: "test setup",
		})
		require.NoError(t, err)
		result, err := s.GrantPermissionTx(ctx, userpermissions.GrantPermissionTxInput{
			UserID: [16]byte(userPgtypeID.Bytes), PermissionID: [16]byte(anyPolicyPermID.Bytes),
			GrantedBy: [16]byte(granterPgtypeID.Bytes), GrantedReason: "scope all test",
			Scope: "all", ExpiresAt: time.Now().Add(time.Hour),
		})
		require.NoError(t, err)
		require.Equal(t, "all", result.Scope)
	})
}

func TestGetUserPermissions_ExpiredExcluded_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("expired grants are excluded", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		granterPgtypeID := createTestUser(t, q)
		permID := getAnyPermissionID(t, q)
		userID := [16]byte(userPgtypeID.Bytes)
		granterID := [16]byte(granterPgtypeID.Bytes)
		_, err := q.GrantUserPermission(ctx, db.GrantUserPermissionParams{
			UserID: userPgtypeID, PermissionID: permID, GrantedBy: granterPgtypeID,
			GrantedReason: "expired grant",
			ExpiresAt:     pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true},
			Scope:         db.PermissionScopeOwn, Conditions: []byte(`{}`),
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23514" {
				t.Skip("privilege escalation trigger prevents direct insert without granter role")
			}
			t.Skipf("direct insert failed (trigger): %v", err)
		}
		perms, err := s.GetUserPermissions(ctx, userID)
		require.NoError(t, err)
		for _, p := range perms {
			_ = granterID
			require.True(t, p.ExpiresAt.After(time.Now()), "found expired grant %s", p.ID)
		}
	})
}

func TestRevokePermission_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("removes grant; subsequent GetUserPermissions is empty", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		granterPgtypeID := createTestUser(t, q)
		permID := getAnyPermissionID(t, q)
		userID := [16]byte(userPgtypeID.Bytes)
		granterID := [16]byte(granterPgtypeID.Bytes)
		ownerRoleID, err := q.GetOwnerRoleID(ctx)
		require.NoError(t, err)
		_, err = q.AssignUserRole(ctx, db.AssignUserRoleParams{
			UserID: granterPgtypeID, RoleID: pgtype.UUID{Bytes: [16]byte(ownerRoleID), Valid: true},
			GrantedBy: granterPgtypeID, GrantedReason: "test setup",
		})
		require.NoError(t, err)
		grant, err := s.GrantPermissionTx(ctx, userpermissions.GrantPermissionTxInput{
			UserID: userID, PermissionID: [16]byte(permID.Bytes), GrantedBy: granterID,
			GrantedReason: "to be revoked", Scope: "own", ExpiresAt: time.Now().Add(time.Hour),
		})
		require.NoError(t, err)
		grantID := adminsharedtest.MustUUID(grant.ID)
		err = s.RevokePermission(ctx, grantID, userID, uuid.UUID(granterID).String())
		require.NoError(t, err)
		perms, err := s.GetUserPermissions(ctx, userID)
		require.NoError(t, err)
		require.Empty(t, perms)
	})
}

func TestRevokePermission_NotFound_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("returns ErrGrantNotFound when no matching row", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		userID := [16]byte(userPgtypeID.Bytes)
		actorID := uuid.New().String()
		nonExistentGrantID := adminsharedtest.RandomUUID()
		err := s.RevokePermission(ctx, nonExistentGrantID, userID, actorID)
		require.ErrorIs(t, err, userpermissions.ErrGrantNotFound)
	})
}

func TestGrantPermissionTx_PermissionNotFound_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("unknown permission_id returns ErrPermissionNotFound", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		granterPgtypeID := createTestUser(t, q)
		_, err := s.GrantPermissionTx(ctx, userpermissions.GrantPermissionTxInput{
			UserID: [16]byte(userPgtypeID.Bytes),
			PermissionID: adminsharedtest.MustUUID("aaaaaaaa-bbbb-cccc-dddd-ffffffffffff"),
			GrantedBy: [16]byte(granterPgtypeID.Bytes), GrantedReason: "test",
			Scope: "own", ExpiresAt: time.Now().Add(time.Hour),
		})
		require.ErrorIs(t, err, userpermissions.ErrPermissionNotFound)
	})
}

func TestGrantPermissionTx_AlreadyGranted_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("active grant exists returns ErrPermissionAlreadyGranted", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		granterPgtypeID := createTestUser(t, q)
		permID := getAnyPermissionID(t, q)
		userID := [16]byte(userPgtypeID.Bytes)
		granterID := [16]byte(granterPgtypeID.Bytes)
		ownerRoleID, err := q.GetOwnerRoleID(ctx)
		require.NoError(t, err)
		_, err = q.AssignUserRole(ctx, db.AssignUserRoleParams{
			UserID: granterPgtypeID, RoleID: pgtype.UUID{Bytes: [16]byte(ownerRoleID), Valid: true},
			GrantedBy: granterPgtypeID, GrantedReason: "test setup",
		})
		require.NoError(t, err)
		in := userpermissions.GrantPermissionTxInput{
			UserID: userID, PermissionID: [16]byte(permID.Bytes), GrantedBy: granterID,
			GrantedReason: "first grant", Scope: "own", ExpiresAt: time.Now().Add(time.Hour),
		}
		_, err = s.GrantPermissionTx(ctx, in)
		require.NoError(t, err)
		in.GrantedReason = "second grant attempt"
		_, err = s.GrantPermissionTx(ctx, in)
		require.ErrorIs(t, err, userpermissions.ErrPermissionAlreadyGranted)
	})
}

func TestGrantPermissionTx_PrivilegeEscalation_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("granter without the permission triggers ErrPrivilegeEscalation", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		granterPgtypeID := createTestUser(t, q)
		permID := getAnyPermissionID(t, q)
		_, err := s.GrantPermissionTx(ctx, userpermissions.GrantPermissionTxInput{
			UserID: [16]byte(userPgtypeID.Bytes), PermissionID: [16]byte(permID.Bytes),
			GrantedBy: [16]byte(granterPgtypeID.Bytes), GrantedReason: "escalation test",
			Scope: "own", ExpiresAt: time.Now().Add(time.Hour),
		})
		require.ErrorIs(t, err, userpermissions.ErrPrivilegeEscalation)
	})
}

func TestGetUserPermissions_FailProxy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("FailGetUserPermissions returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		_, err := withProxy(q, &adminsharedtest.QuerierProxy{FailGetUserPermissions: true}).
			GetUserPermissions(ctx, [16]byte(userPgtypeID.Bytes))
		require.ErrorIs(t, err, adminsharedtest.ErrProxy)
	})
}

func TestGrantUserPermission_FailProxy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("FailGrantUserPermission returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		granterPgtypeID := createTestUser(t, q)
		permID := getAnyPermissionID(t, q)
		_, err := withProxy(q, &adminsharedtest.QuerierProxy{FailGrantUserPermission: true}).
			GrantPermissionTx(ctx, userpermissions.GrantPermissionTxInput{
				UserID: [16]byte(userPgtypeID.Bytes), PermissionID: [16]byte(permID.Bytes),
				GrantedBy: [16]byte(granterPgtypeID.Bytes), GrantedReason: "proxy test",
				Scope: "own", ExpiresAt: time.Now().Add(time.Hour),
			})
		require.ErrorIs(t, err, adminsharedtest.ErrProxy)
	})
}

func TestRevokeUserPermission_FailProxy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("FailRevokeUserPermission returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		actorID := uuid.New().String()
		grantID := adminsharedtest.RandomUUID()
		err := withProxy(q, &adminsharedtest.QuerierProxy{FailRevokeUserPermission: true}).
			RevokePermission(ctx, grantID, [16]byte(userPgtypeID.Bytes), actorID)
		require.ErrorIs(t, err, adminsharedtest.ErrProxy)
	})
}

func TestIsPrivilegeEscalation_Unit(t *testing.T) {
	fn := userpermissions.IsPrivilegeEscalation
	require.False(t, fn(nil))
	require.False(t, fn(errors.New("foo")))
	require.False(t, fn(&pgconn.PgError{Code: "23514", Message: "something else"}))
	require.False(t, fn(&pgconn.PgError{Code: "42501", Message: "something else"}))
	require.False(t, fn(&pgconn.PgError{Code: "23515", Message: "privilege escalation"}))
	require.True(t, fn(&pgconn.PgError{Code: "23514", Message: "privilege escalation detected"}))
	require.True(t, fn(&pgconn.PgError{Code: "42501", Message: "Privilege escalation denied: granter has no role"}))
	require.True(t, fn(&pgconn.PgError{Code: "42501", Message: "PRIVILEGE ESCALATION DENIED"}))
}

func TestGetUserPermissions_NoGrants_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("user with no grants returns empty slice", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		perms, err := s.GetUserPermissions(ctx, [16]byte(userPgtypeID.Bytes))
		require.NoError(t, err)
		require.NotNil(t, perms)
		require.Empty(t, perms)
	})
}

func TestGrantPermissionTx_ScopeNotAllowed_OwnPolicy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("scope=all on own-policy permission returns ErrScopeNotAllowed", func(t *testing.T) {
		s, q := txStores(t)
		allPerms, err := q.GetPermissions(ctx)
		require.NoError(t, err)
		var ownPolicyPermID pgtype.UUID
		for _, p := range allPerms {
			if p.ScopePolicy == db.PermissionScopePolicyOwn {
				ownPolicyPermID = pgtype.UUID{Bytes: [16]byte(p.ID), Valid: true}
				break
			}
		}
		if !ownPolicyPermID.Valid {
			t.Skip("no permission with scope_policy=own in seed data")
		}
		userPgtypeID := createTestUser(t, q)
		granterPgtypeID := createTestUser(t, q)
		_, err = s.GrantPermissionTx(ctx, userpermissions.GrantPermissionTxInput{
			UserID: [16]byte(userPgtypeID.Bytes), PermissionID: [16]byte(ownPolicyPermID.Bytes),
			GrantedBy: [16]byte(granterPgtypeID.Bytes), GrantedReason: "scope test",
			Scope: "all", ExpiresAt: time.Now().Add(time.Hour),
		})
		require.ErrorIs(t, err, adminshared.ErrScopeNotAllowed)
	})
}

func TestGrantPermissionTx_ScopeNotAllowed_AllPolicy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("scope=own on all-policy permission returns ErrScopeNotAllowed", func(t *testing.T) {
		s, q := txStores(t)
		allPerms, err := q.GetPermissions(ctx)
		require.NoError(t, err)
		var allPolicyPermID pgtype.UUID
		for _, p := range allPerms {
			if p.ScopePolicy == db.PermissionScopePolicyAll {
				allPolicyPermID = pgtype.UUID{Bytes: [16]byte(p.ID), Valid: true}
				break
			}
		}
		if !allPolicyPermID.Valid {
			t.Skip("no permission with scope_policy=all in seed data")
		}
		userPgtypeID := createTestUser(t, q)
		granterPgtypeID := createTestUser(t, q)
		_, err = s.GrantPermissionTx(ctx, userpermissions.GrantPermissionTxInput{
			UserID: [16]byte(userPgtypeID.Bytes), PermissionID: [16]byte(allPolicyPermID.Bytes),
			GrantedBy: [16]byte(granterPgtypeID.Bytes), GrantedReason: "scope test",
			Scope: "own", ExpiresAt: time.Now().Add(time.Hour),
		})
		require.ErrorIs(t, err, adminshared.ErrScopeNotAllowed)
	})
}

func TestGrantPermissionTx_FailGetPermissionByID_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("FailGetPermissionByID propagates ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		granterPgtypeID := createTestUser(t, q)
		permID := getAnyPermissionID(t, q)
		_, err := withProxy(q, &adminsharedtest.QuerierProxy{FailGetPermissionByID: true}).
			GrantPermissionTx(ctx, userpermissions.GrantPermissionTxInput{
				UserID: [16]byte(userPgtypeID.Bytes), PermissionID: [16]byte(permID.Bytes),
				GrantedBy: [16]byte(granterPgtypeID.Bytes), GrantedReason: "proxy test",
				Scope: "own", ExpiresAt: time.Now().Add(time.Hour),
			})
		require.ErrorIs(t, err, adminsharedtest.ErrProxy)
	})
}
