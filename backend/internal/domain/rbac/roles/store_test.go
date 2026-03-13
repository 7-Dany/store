//go:build integration_test

package roles_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/domain/rbac/roles"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
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
func txStores(t *testing.T) (*roles.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured")
	}
	_, q := rbacsharedtest.MustBeginTx(t, testPool)
	return roles.NewStore(testPool).WithQuerier(q), q
}

// withProxy wires q into proxy.Querier and returns a Store bound to it.
func withProxy(q db.Querier, proxy *rbacsharedtest.QuerierProxy) *roles.Store {
	proxy.Querier = q
	return roles.NewStore(testPool).WithQuerier(proxy)
}

// ptr returns a pointer to s (helper for update inputs).
func ptr(s string) *string { return &s }

// ── T-R23: TestGetRoles_Integration ──────────────────────────────────────────

func TestGetRoles_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("returns seeded roles", func(t *testing.T) {
		s, _ := txStores(t)
		rolesList, err := s.GetRoles(ctx)
		require.NoError(t, err)
		// Seeds create owner, admin, vendor, customer — at least 4 active roles.
		require.GreaterOrEqual(t, len(rolesList), 4)
		var found bool
		for _, r := range rolesList {
			if r.Name == "admin" {
				require.True(t, r.IsSystemRole)
				require.False(t, r.IsOwnerRole)
				found = true
			}
		}
		require.True(t, found, "admin role must be present in seeded roles")
	})

	t.Run("result is never nil", func(t *testing.T) {
		s, _ := txStores(t)
		rolesList, err := s.GetRoles(ctx)
		require.NoError(t, err)
		require.NotNil(t, rolesList)
	})

	t.Run("FailGetRoles returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		_, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetRoles: true}).GetRoles(ctx)
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// ── TestGetRoleByID_Integration ─────────────────────────────────────────────

func TestGetRoleByID_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("known ID returns role with correct fields", func(t *testing.T) {
		s, q := txStores(t)
		adminRole, err := q.GetRoleByName(ctx, "admin")
		require.NoError(t, err)
		got, err := s.GetRoleByID(ctx, [16]byte(adminRole.ID))
		require.NoError(t, err)
		require.Equal(t, "admin", got.Name)
		require.True(t, got.IsSystemRole)
	})

	t.Run("unknown ID returns ErrRoleNotFound", func(t *testing.T) {
		s, _ := txStores(t)
		_, err := s.GetRoleByID(ctx, rbacsharedtest.MustUUID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"))
		require.ErrorIs(t, err, roles.ErrRoleNotFound)
	})

	t.Run("FailGetRoleByID returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		adminRole, err := q.GetRoleByName(ctx, "admin")
		require.NoError(t, err)
		_, err = withProxy(q, &rbacsharedtest.QuerierProxy{FailGetRoleByID: true}).
			GetRoleByID(ctx, [16]byte(adminRole.ID))
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// ── T-R24: TestCreateRole_Integration ────────────────────────────────────────

func TestCreateRole_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("creates non-system role and returns it", func(t *testing.T) {
		s, _ := txStores(t)
		role, err := s.CreateRole(ctx, roles.CreateRoleInput{Name: "test_vendor_plus", Description: "test"})
		require.NoError(t, err)
		require.Equal(t, "test_vendor_plus", role.Name)
		require.False(t, role.IsSystemRole)
		require.False(t, role.IsOwnerRole)
		require.NotEmpty(t, role.ID)
	})

	t.Run("FailCreateRole returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		_, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailCreateRole: true}).
			CreateRole(ctx, roles.CreateRoleInput{Name: "x"})
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})

	t.Run("duplicate name returns a DB error", func(t *testing.T) {
		s, _ := txStores(t)
		_, err := s.CreateRole(ctx, roles.CreateRoleInput{Name: "duplicate_name_role"})
		require.NoError(t, err)
		_, err = s.CreateRole(ctx, roles.CreateRoleInput{Name: "duplicate_name_role"})
		require.Error(t, err, "second insert with the same name must fail")
	})
}

// ── T-R25: TestUpdateRole_Integration ────────────────────────────────────────

func TestUpdateRole_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("updates name for non-system role", func(t *testing.T) {
		s, q := txStores(t)
		created, err := q.CreateRole(ctx, db.CreateRoleParams{Name: "mutable_role"})
		require.NoError(t, err)
		updated, err := s.UpdateRole(ctx, [16]byte(created.ID), roles.UpdateRoleInput{Name: ptr("mutable_role_v2")})
		require.NoError(t, err)
		require.Equal(t, "mutable_role_v2", updated.Name)
	})

	t.Run("FailUpdateRole returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		created, err := q.CreateRole(ctx, db.CreateRoleParams{Name: "proxy_update_role"})
		require.NoError(t, err)
		_, err = withProxy(q, &rbacsharedtest.QuerierProxy{FailUpdateRole: true}).
			UpdateRole(ctx, [16]byte(created.ID), roles.UpdateRoleInput{Name: ptr("x")})
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// ── TestUpdateRole_NotFound_Integration ────────────────────────────────────────

func TestUpdateRole_NotFound_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("non-existent role ID returns ErrRoleNotFound", func(t *testing.T) {
		s, _ := txStores(t)
		newName := "ghost"
		_, err := s.UpdateRole(ctx, rbacsharedtest.MustUUID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"), roles.UpdateRoleInput{Name: &newName})
		require.ErrorIs(t, err, roles.ErrRoleNotFound)
	})
}

// ── T-R26: TestUpdateRole_SystemRole_Integration ──────────────────────────────

func TestUpdateRole_SystemRole_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("returns ErrSystemRoleImmutable for system role", func(t *testing.T) {
		s, q := txStores(t)
		adminRole, err := q.GetRoleByName(ctx, "admin")
		require.NoError(t, err)
		newName := "hacked"
		_, err = s.UpdateRole(ctx, [16]byte(adminRole.ID), roles.UpdateRoleInput{Name: &newName})
		require.ErrorIs(t, err, rbac.ErrSystemRoleImmutable)
	})
}

// ── T-R27: TestDeactivateRole_Integration ────────────────────────────────────

func TestDeactivateRole_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("soft-deletes non-system role; GetRoleByID confirms is_active = FALSE", func(t *testing.T) {
		s, q := txStores(t)
		created, err := q.CreateRole(ctx, db.CreateRoleParams{Name: "deletable_role"})
		require.NoError(t, err)
		err = s.DeactivateRole(ctx, [16]byte(created.ID))
		require.NoError(t, err)
		row, err := q.GetRoleByID(ctx, pgtype.UUID{Bytes: [16]byte(created.ID), Valid: true})
		require.NoError(t, err)
		require.False(t, row.IsActive)
	})

	t.Run("FailDeactivateRole returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		created, err := q.CreateRole(ctx, db.CreateRoleParams{Name: "proxy_deactivate_role"})
		require.NoError(t, err)
		err = withProxy(q, &rbacsharedtest.QuerierProxy{FailDeactivateRole: true}).
			DeactivateRole(ctx, [16]byte(created.ID))
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// ── TestDeactivateRole_NotFound_Integration ─────────────────────────────────────

func TestDeactivateRole_NotFound_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("non-existent role ID returns ErrRoleNotFound", func(t *testing.T) {
		s, _ := txStores(t)
		err := s.DeactivateRole(ctx, rbacsharedtest.MustUUID("aaaaaaaa-bbbb-cccc-dddd-111111111111"))
		require.ErrorIs(t, err, roles.ErrRoleNotFound)
	})
}

// ── T-R28: TestDeactivateRole_SystemRole_Integration ─────────────────────────

func TestDeactivateRole_SystemRole_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("returns ErrSystemRoleImmutable for system role", func(t *testing.T) {
		s, q := txStores(t)
		adminRole, err := q.GetRoleByName(ctx, "admin")
		require.NoError(t, err)
		err = s.DeactivateRole(ctx, [16]byte(adminRole.ID))
		require.ErrorIs(t, err, rbac.ErrSystemRoleImmutable)
	})
}

// ── T-R29: TestGetRolePermissions_Integration ─────────────────────────────────

func TestGetRolePermissions_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("returns permissions for admin role with access_type and scope", func(t *testing.T) {
		s, q := txStores(t)
		adminRole, err := q.GetRoleByName(ctx, "admin")
		require.NoError(t, err)
		perms, err := s.GetRolePermissions(ctx, [16]byte(adminRole.ID))
		require.NoError(t, err)
		require.NotEmpty(t, perms)
		var found bool
		for _, p := range perms {
			if p.CanonicalName == "rbac:read" {
				require.NotEmpty(t, p.AccessType)
				require.NotEmpty(t, p.Scope)
				require.NotEmpty(t, p.PermissionID)
				found = true
			}
		}
		require.True(t, found, "rbac:read must be in admin role permissions")
	})

	t.Run("result is never nil", func(t *testing.T) {
		s, q := txStores(t)
		ownerID, err := q.GetOwnerRoleID(ctx)
		require.NoError(t, err)
		perms, err := s.GetRolePermissions(ctx, [16]byte(ownerID))
		require.NoError(t, err)
		require.NotNil(t, perms)
	})

	t.Run("FailGetRolePermissions returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		adminRole, err := q.GetRoleByName(ctx, "admin")
		require.NoError(t, err)
		_, err = withProxy(q, &rbacsharedtest.QuerierProxy{FailGetRolePermissions: true}).
			GetRolePermissions(ctx, [16]byte(adminRole.ID))
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})

	t.Run("role with no permissions returns empty non-nil slice", func(t *testing.T) {
		s, q := txStores(t)
		created, err := q.CreateRole(ctx, db.CreateRoleParams{Name: "empty_perms_role"})
		require.NoError(t, err)
		perms, err := s.GetRolePermissions(ctx, [16]byte(created.ID))
		require.NoError(t, err)
		require.NotNil(t, perms)
		require.Empty(t, perms)
	})
}

// ── T-R30: TestAddRolePermission_Integration ──────────────────────────────────

func TestAddRolePermission_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("adds permission to role; subsequent duplicate is no-op", func(t *testing.T) {
		s, q := txStores(t)
		created, err := q.CreateRole(ctx, db.CreateRoleParams{Name: "perm_test_role"})
		require.NoError(t, err)
		perm, err := q.GetPermissionByCanonicalName(ctx, pgtype.Text{String: "rbac:read", Valid: true})
		require.NoError(t, err)
		// granted_by references users(id), not roles — create a real user to satisfy the FK.
		grantedByUserID, err := q.CreateActiveUnverifiedUserForTest(ctx, db.CreateActiveUnverifiedUserForTestParams{
			Email:        pgtype.Text{String: rbacsharedtest.NewEmail(t), Valid: true},
			PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "test-password"), Valid: true},
		})
		require.NoError(t, err)
		in := roles.AddRolePermissionInput{
			PermissionID:  [16]byte(perm.ID),
			GrantedBy:     [16]byte(grantedByUserID),
			GrantedReason: "integration test",
			AccessType:    "direct",
			Scope:         "all",
			Conditions:    json.RawMessage("{}"),
		}
		err = s.AddRolePermission(ctx, [16]byte(created.ID), in)
		require.NoError(t, err)
		// Second call returns ErrGrantAlreadyExists — ON CONFLICT DO NOTHING fires (0 rows affected).
		err = s.AddRolePermission(ctx, [16]byte(created.ID), in)
		require.ErrorIs(t, err, roles.ErrGrantAlreadyExists)
		// Confirm the permission is on the role
		perms, err := s.GetRolePermissions(ctx, [16]byte(created.ID))
		require.NoError(t, err)
		require.Len(t, perms, 1)
		require.Equal(t, "direct", perms[0].AccessType)
	})

	t.Run("non-existent permission_id returns ErrPermissionNotFound", func(t *testing.T) {
		s, q := txStores(t)
		created, err := q.CreateRole(ctx, db.CreateRoleParams{Name: "fk_perm_role"})
		require.NoError(t, err)
		grantedByUserID, err := q.CreateActiveUnverifiedUserForTest(ctx, db.CreateActiveUnverifiedUserForTestParams{
			Email:        pgtype.Text{String: rbacsharedtest.NewEmail(t), Valid: true},
			PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "test-password"), Valid: true},
		})
		require.NoError(t, err)
		in := roles.AddRolePermissionInput{
			PermissionID:  rbacsharedtest.MustUUID("aaaaaaaa-bbbb-cccc-dddd-ffffffffffff"), // does not exist
			GrantedBy:     [16]byte(grantedByUserID),
			GrantedReason: "fk test",
			AccessType:    "direct",
			Scope:         "all",
			Conditions:    json.RawMessage("{}"),
		}
		err = s.AddRolePermission(ctx, [16]byte(created.ID), in)
		require.ErrorIs(t, err, roles.ErrPermissionNotFound)
	})

	t.Run("non-existent role_id returns ErrRoleNotFound", func(t *testing.T) {
		s, q := txStores(t)
		perm, err := q.GetPermissionByCanonicalName(ctx, pgtype.Text{String: "rbac:read", Valid: true})
		require.NoError(t, err)
		grantedByUserID, err := q.CreateActiveUnverifiedUserForTest(ctx, db.CreateActiveUnverifiedUserForTestParams{
			Email:        pgtype.Text{String: rbacsharedtest.NewEmail(t), Valid: true},
			PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "test-password"), Valid: true},
		})
		require.NoError(t, err)
		in := roles.AddRolePermissionInput{
			PermissionID:  [16]byte(perm.ID),
			GrantedBy:     [16]byte(grantedByUserID),
			GrantedReason: "fk test",
			AccessType:    "direct",
			Scope:         "all",
			Conditions:    json.RawMessage("{}"),
		}
		err = s.AddRolePermission(ctx, rbacsharedtest.MustUUID("aaaaaaaa-bbbb-cccc-dddd-000000000000"), in) // role does not exist
		require.ErrorIs(t, err, roles.ErrRoleNotFound)
	})

	t.Run("FailAddRolePermission returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		created, err := q.CreateRole(ctx, db.CreateRoleParams{Name: "proxy_perm_role"})
		require.NoError(t, err)
		perm, err := q.GetPermissionByCanonicalName(ctx, pgtype.Text{String: "rbac:read", Valid: true})
		require.NoError(t, err)
		// granted_by references users(id), not roles — create a real user to satisfy the FK.
		grantedByUserID, err := q.CreateActiveUnverifiedUserForTest(ctx, db.CreateActiveUnverifiedUserForTestParams{
			Email:        pgtype.Text{String: rbacsharedtest.NewEmail(t), Valid: true},
			PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "test-password"), Valid: true},
		})
		require.NoError(t, err)
		in := roles.AddRolePermissionInput{
			PermissionID:  [16]byte(perm.ID),
			GrantedBy:     [16]byte(grantedByUserID),
			GrantedReason: "proxy test",
			AccessType:    "direct",
			Scope:         "all",
			Conditions:    json.RawMessage("{}"),
		}
		err = withProxy(q, &rbacsharedtest.QuerierProxy{FailAddRolePermission: true}).
			AddRolePermission(ctx, [16]byte(created.ID), in)
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// ── T-R31: TestRemoveRolePermission_Integration ───────────────────────────────

func TestRemoveRolePermission_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("removes existing grant; GetRolePermissions returns empty after removal", func(t *testing.T) {
		s, q := txStores(t)
		created, err := q.CreateRole(ctx, db.CreateRoleParams{Name: "remove_perm_role"})
		require.NoError(t, err)
		perm, err := q.GetPermissionByCanonicalName(ctx, pgtype.Text{String: "rbac:read", Valid: true})
		require.NoError(t, err)
		// granted_by references users(id), not roles — create a real user to satisfy the FK.
		grantedByUserID, err := q.CreateActiveUnverifiedUserForTest(ctx, db.CreateActiveUnverifiedUserForTestParams{
			Email:        pgtype.Text{String: rbacsharedtest.NewEmail(t), Valid: true},
			PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "test-password"), Valid: true},
		})
		require.NoError(t, err)
		_, _ = q.AddRolePermission(ctx, db.AddRolePermissionParams{
			RoleID:        pgtype.UUID{Bytes: [16]byte(created.ID), Valid: true},
			PermissionID:  pgtype.UUID{Bytes: [16]byte(perm.ID), Valid: true},
			GrantedBy:     pgtype.UUID{Bytes: [16]byte(grantedByUserID), Valid: true},
			GrantedReason: "test",
			AccessType:    db.PermissionAccessTypeDirect,
			Scope:         db.PermissionScopeAll,
			Conditions:    []byte("{}"),
		})
		err = s.RemoveRolePermission(ctx, [16]byte(created.ID), [16]byte(perm.ID), grantedByUserID.String())
		require.NoError(t, err)
		perms, err := s.GetRolePermissions(ctx, [16]byte(created.ID))
		require.NoError(t, err)
		require.Empty(t, perms)
	})

	t.Run("returns ErrRolePermissionNotFound when grant does not exist", func(t *testing.T) {
		s, _ := txStores(t)
		err := s.RemoveRolePermission(ctx,
			rbacsharedtest.MustUUID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
			rbacsharedtest.MustUUID("11111111-2222-3333-4444-555555555555"),
			"ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb")
		require.ErrorIs(t, err, roles.ErrRolePermissionNotFound)
	})

	t.Run("FailRemoveRolePermission returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		err := withProxy(q, &rbacsharedtest.QuerierProxy{FailRemoveRolePermission: true}).
			RemoveRolePermission(ctx,
				rbacsharedtest.MustUUID(testRoleID),
				rbacsharedtest.MustUUID(testPermID),
				testUserID)
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// ── T-R31d: TestAddRolePermission_Duplicate_Integration ───────────────────────

// TestAddRolePermission_Duplicate_Integration verifies that a second AddRolePermission
// call for the same (role_id, permission_id) pair returns ErrGrantAlreadyExists.
// The ON CONFLICT DO NOTHING path fires; `:execrows` returns 0 rows, which the store
// maps to ErrGrantAlreadyExists instead of nil.
func TestAddRolePermission_Duplicate_Integration(t *testing.T) {
	ctx := context.Background()
	s, q := txStores(t)

	role, err := q.CreateRole(ctx, db.CreateRoleParams{Name: "dup_test_role_" + rbacsharedtest.ShortID()})
	require.NoError(t, err)
	perm, err := q.GetPermissionByCanonicalName(ctx, pgtype.Text{String: "rbac:read", Valid: true})
	require.NoError(t, err)
	grantedByUserID, err := q.CreateActiveUnverifiedUserForTest(ctx, db.CreateActiveUnverifiedUserForTestParams{
		Email:        pgtype.Text{String: rbacsharedtest.NewEmail(t), Valid: true},
		PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "test-password"), Valid: true},
	})
	require.NoError(t, err)

	in := roles.AddRolePermissionInput{
		PermissionID:  [16]byte(perm.ID),
		GrantedBy:     [16]byte(grantedByUserID),
		GrantedReason: "integration test",
		AccessType:    "direct",
		Scope:         "all",
		Conditions:    json.RawMessage("{}"),
	}

	// First insert succeeds.
	err = s.AddRolePermission(ctx, [16]byte(role.ID), in)
	require.NoError(t, err)

	// Second insert with identical (role_id, permission_id) must return ErrGrantAlreadyExists.
	err = s.AddRolePermission(ctx, [16]byte(role.ID), in)
	require.ErrorIs(t, err, roles.ErrGrantAlreadyExists,
		"second AddRolePermission on same (role_id, permission_id) must return ErrGrantAlreadyExists")
}

// ── T-R31e: TestRemoveRolePermission_AuditActor_Integration ─────────────────

// TestRemoveRolePermission_AuditActor_Integration verifies that the audit row written
// by fn_audit_role_permissions on DELETE records the actingUserID passed to
// RemoveRolePermission — not the original granted_by (original granter).
// This is the F-4 regression: WithActingUser sets rbac.acting_user before the DELETE
// so the trigger reads the correct actor instead of falling back to OLD.granted_by.
func TestRemoveRolePermission_AuditActor_Integration(t *testing.T) {
	ctx := context.Background()
	s, q := txStores(t)

	role, err := q.CreateRole(ctx, db.CreateRoleParams{Name: "audit_actor_role_" + rbacsharedtest.ShortID()})
	require.NoError(t, err)
	perm, err := q.GetPermissionByCanonicalName(ctx, pgtype.Text{String: "rbac:read", Valid: true})
	require.NoError(t, err)

	// Original granter — used as granted_by when the grant is created.
	granterID, err := q.CreateActiveUnverifiedUserForTest(ctx, db.CreateActiveUnverifiedUserForTestParams{
		Email:        pgtype.Text{String: rbacsharedtest.NewEmail(t), Valid: true},
		PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "test-password"), Valid: true},
	})
	require.NoError(t, err)

	// Acting user — the one who will perform the DELETE. Must be a different user.
	actingUserID, err := q.CreateActiveUnverifiedUserForTest(ctx, db.CreateActiveUnverifiedUserForTestParams{
		Email:        pgtype.Text{String: rbacsharedtest.NewEmail(t), Valid: true},
		PasswordHash: pgtype.Text{String: rbacsharedtest.MustHashPassword(t, "test-password"), Valid: true},
	})
	require.NoError(t, err)

	// Seed the grant using granterID as granted_by.
	_, qErr := q.AddRolePermission(ctx, db.AddRolePermissionParams{
		RoleID:        pgtype.UUID{Bytes: [16]byte(role.ID), Valid: true},
		PermissionID:  pgtype.UUID{Bytes: [16]byte(perm.ID), Valid: true},
		GrantedBy:     pgtype.UUID{Bytes: [16]byte(granterID), Valid: true},
		GrantedReason: "audit actor test setup",
		AccessType:    db.PermissionAccessTypeDirect,
		Scope:         db.PermissionScopeAll,
		Conditions:    []byte("{}"),
	})
	require.NoError(t, qErr)

	// Delete the grant using actingUserID — the acting user must appear in the audit row.
	err = s.RemoveRolePermission(ctx, [16]byte(role.ID), [16]byte(perm.ID), actingUserID.String())
	require.NoError(t, err)

	// Assert the audit row records actingUserID, not granterID.
	// Queries through q (the transaction) so uncommitted rows are visible.
	auditRow, scanErr := q.GetLatestRolePermissionAuditEntry(ctx, db.GetLatestRolePermissionAuditEntryParams{
		RoleID:       pgtype.UUID{Bytes: [16]byte(role.ID), Valid: true},
		PermissionID: pgtype.UUID{Bytes: [16]byte(perm.ID), Valid: true},
		ChangeType:   db.AuditChangeTypeEnumDeleted,
	})
	require.NoError(t, scanErr)
	require.True(t, auditRow.ChangedBy.Valid, "changed_by must not be NULL")
	require.Equal(t, [16]byte(actingUserID), auditRow.ChangedBy.Bytes,
		"audit changed_by must be the acting user, not the original granter")
}
