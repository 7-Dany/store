//go:build integration_test

package userroles_test

import (
	"context"
	"errors"
	"testing"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/domain/rbac/userroles"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	rbacsharedtest.RunTestMain(m, &testPool, 20)
}

// txStores begins a rolled-back transaction and returns a Store bound to it
// alongside *db.Queries for direct assertion queries. Skips when testPool is nil.
func txStores(t *testing.T) (*userroles.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured")
	}
	_, q := rbacsharedtest.MustBeginTx(t, testPool)
	return userroles.NewStore(testPool).WithQuerier(q), q
}

// withProxy wires q into proxy.Querier and returns a Store bound to it.
func withProxy(q db.Querier, proxy *rbacsharedtest.QuerierProxy) *userroles.Store {
	proxy.Querier = q
	return userroles.NewStore(testPool).WithQuerier(proxy)
}

// createTestUser creates an active user with a unique email for FK references.
func createTestUser(t *testing.T, q *db.Queries) pgtype.UUID {
	t.Helper()
	userID, err := q.CreateActiveUnverifiedUserForTest(context.Background(), db.CreateActiveUnverifiedUserForTestParams{
		Email:        pgtype.Text{String: rbacsharedtest.NewEmail(t), Valid: true},
		PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "test-password"), Valid: true},
	})
	require.NoError(t, err)
	return pgtype.UUID{Bytes: [16]byte(userID), Valid: true}
}

// ── T-R34: AssignUserRole assigns role; GetUserRole returns it ────────────────

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

// ── T-R34b: AssignUserRole replaces existing role ─────────────────────────────

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

		// First assignment
		_, err = s.AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
			UserID:        userID,
			RoleID:        [16]byte(adminRole.ID),
			GrantedBy:     [16]byte(grantedBy.Bytes),
			GrantedReason: "initial",
		})
		require.NoError(t, err)

		// Replace with vendor role
		result, err := s.AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
			UserID:        userID,
			RoleID:        [16]byte(vendorRole.ID),
			GrantedBy:     [16]byte(grantedBy.Bytes),
			GrantedReason: "replacement",
		})
		require.NoError(t, err)
		require.Equal(t, "vendor", result.RoleName)
	})
}

// ── T-R35: AssignUserRole returns ErrRoleNotFound for unknown role_id ─────────

func TestAssignUserRole_RoleNotFound_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown role_id returns ErrRoleNotFound", func(t *testing.T) {
		s, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		userID := [16]byte(userPgtypeID.Bytes)
		grantedBy := createTestUser(t, q)

		_, err := s.AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
			UserID:        userID,
			RoleID:        rbacsharedtest.MustUUID("aaaaaaaa-bbbb-cccc-dddd-ffffffffffff"),
			GrantedBy:     [16]byte(grantedBy.Bytes),
			GrantedReason: "test",
		})
		require.ErrorIs(t, err, userroles.ErrRoleNotFound)
	})
}

// ── T-R36: RemoveUserRole removes assignment; GetUserRole returns NotFound ─────

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
			UserID:        userID,
			RoleID:        [16]byte(adminRole.ID),
			GrantedBy:     [16]byte(grantedBy.Bytes),
			GrantedReason: "to be removed",
		})
		require.NoError(t, err)

		err = s.RemoveUserRole(ctx, userID, uuid.UUID(grantedBy.Bytes).String())
		require.NoError(t, err)

		_, err = s.GetUserRole(ctx, userID)
		require.ErrorIs(t, err, userroles.ErrUserRoleNotFound)
	})
}

// ── T-R36b: RemoveUserRole returns ErrUserRoleNotFound when no assignment ──────

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

// ── T-R36c: RemoveUserRole returns ErrLastOwnerRemoval for last owner ─────────

func TestRemoveUserRole_LastOwner_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("returns ErrLastOwnerRemoval when removing the last owner", func(t *testing.T) {
		if testPool == nil {
			t.Skip("no test database configured")
		}
		// Prerequisite: this test requires no bootstrapped owner to exist, because
		// the fn_prevent_orphaned_owner trigger only fires when removing the LAST
		// active owner. If a seed owner already exists, we cannot create an isolated
		// last-owner scenario within a rolled-back transaction.
		// Check for existing active owners and skip if found.
		q0 := db.New(testPool)
		ownerCount, err := q0.CountActiveOwners(context.Background())
		require.NoError(t, err)
		if ownerCount > 0 {
			t.Skip("bootstrapped owner exists; last-owner trigger test requires an empty owner set")
		}
		// This test needs its own connection outside the rolled-back tx because
		// the orphan-owner trigger fires at commit time. We use the pool directly
		// and rely on setup/teardown within the test.
		//
		// Find the existing owner user from the bootstrap seed.
		q := db.New(testPool)
		ownerRoleID, err := q.GetOwnerRoleID(ctx)
		require.NoError(t, err)

		// Find the user who holds the owner role.
		// We query user_roles directly via raw SQL fallback through GetUserRole
		// approach: create a new test user, assign them the owner role in a
		// sub-transaction, then attempt to delete. The trigger blocks the delete.
		//
		// We need to find existing owner assignment. Use a separate store
		// with its own transaction that will be rolled back.
		_, q2 := rbacsharedtest.MustBeginTx(t, testPool)
		s2 := userroles.NewStore(testPool).WithQuerier(q2)

		// Create a user and grant them the owner role directly through
		// raw query to bypass service-level guards.
		ownerUserID := createTestUser(t, q2)
		granterID := createTestUser(t, q2)

		_, err = q2.AssignUserRole(ctx, db.AssignUserRoleParams{
			UserID:        ownerUserID,
			RoleID:        pgtype.UUID{Bytes: [16]byte(ownerRoleID), Valid: true},
			GrantedBy:     granterID,
			GrantedReason: "last owner test",
		})
		require.NoError(t, err)

		// Attempt to remove: trigger should fire ErrLastOwnerRemoval.
		err = s2.RemoveUserRole(ctx, [16]byte(ownerUserID.Bytes), uuid.UUID(granterID.Bytes).String())
		require.ErrorIs(t, err, userroles.ErrLastOwnerRemoval)
	})
}

// ── Proxy tests ───────────────────────────────────────────────────────────────

// ── FIX 15a: AssignUserRoleTx returns ErrRoleNotFound for inactive role ──────────────

func TestAssignUserRoleTx_InactiveRole_Integration(t *testing.T) {
	ctx := context.Background()
	s, q := txStores(t)
	userPgtypeID := createTestUser(t, q)
	userID := [16]byte(userPgtypeID.Bytes)
	grantedBy := createTestUser(t, q)

	// Create a fresh non-system role (DeactivateRole guards is_system_role=FALSE,
	// so seeded roles like "admin" cannot be deactivated via that query).
	newRole, err := q.CreateRole(ctx, db.CreateRoleParams{
		Name: "test-inactive-" + rbacsharedtest.ShortID(),
	})
	require.NoError(t, err)

	// Deactivate it — this succeeds because it is not a system role.
	rows, err := q.DeactivateRole(ctx, pgtype.UUID{Bytes: [16]byte(newRole.ID), Valid: true})
	require.NoError(t, err)
	require.EqualValues(t, 1, rows, "expected exactly 1 row deactivated")

	_, err = s.AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
		UserID:        userID,
		RoleID:        [16]byte(newRole.ID),
		GrantedBy:     [16]byte(grantedBy.Bytes),
		GrantedReason: "should fail",
	})
	require.ErrorIs(t, err, userroles.ErrRoleNotFound)
}

// ── FIX 15b: AssignUserRoleTx — FailGetRoleByID proxy ───────────────────────────

func TestAssignUserRoleTx_FailGetRoleByID_Integration(t *testing.T) {
	ctx := context.Background()
	_, q := txStores(t)
	userPgtypeID := createTestUser(t, q)
	userID := [16]byte(userPgtypeID.Bytes)
	grantedBy := createTestUser(t, q)

	adminRole, err := q.GetRoleByName(ctx, "admin")
	require.NoError(t, err)

	_, err = withProxy(q, &rbacsharedtest.QuerierProxy{FailGetRoleByID: true}).
		AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
			UserID:        userID,
			RoleID:        [16]byte(adminRole.ID),
			GrantedBy:     [16]byte(grantedBy.Bytes),
			GrantedReason: "proxy test",
		})
	require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
}

// ── FIX 15c: AssignUserRoleTx — FailAssignUserRole proxy ────────────────────────

func TestAssignUserRoleTx_FailAssignUserRole_Integration(t *testing.T) {
	ctx := context.Background()
	_, q := txStores(t)
	userPgtypeID := createTestUser(t, q)
	userID := [16]byte(userPgtypeID.Bytes)
	grantedBy := createTestUser(t, q)

	adminRole, err := q.GetRoleByName(ctx, "admin")
	require.NoError(t, err)

	_, err = withProxy(q, &rbacsharedtest.QuerierProxy{FailAssignUserRole: true}).
		AssignUserRoleTx(ctx, userroles.AssignRoleTxInput{
			UserID:        userID,
			RoleID:        [16]byte(adminRole.ID),
			GrantedBy:     [16]byte(grantedBy.Bytes),
			GrantedReason: "proxy test",
		})
	require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
}

// ── FIX 15d: isOrphanedOwnerViolation unit test (no build tag) ──────────────────
// This lives here rather than a separate file to keep all store tests together.
// The build tag on this file is integration_test, but this sub-test itself
// needs no DB — it is a pure in-process unit test of a helper function.

func TestIsOrphanedOwnerViolation_Unit(t *testing.T) {
	if testPool == nil {
		// Skip DB setup but run the pure unit assertions regardless.
		// Actually this function needs no pool at all — call it directly.
	}
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
		_, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetUserRole: true}).
			GetUserRole(ctx, [16]byte(userPgtypeID.Bytes))
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

func TestRemoveUserRole_FailProxy_Integration(t *testing.T) {
	ctx := context.Background()
	t.Run("FailRemoveUserRole returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		userPgtypeID := createTestUser(t, q)
		err := withProxy(q, &rbacsharedtest.QuerierProxy{FailRemoveUserRole: true}).
			RemoveUserRole(ctx, [16]byte(userPgtypeID.Bytes), "ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb")
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}
