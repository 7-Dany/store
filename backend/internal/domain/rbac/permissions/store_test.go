//go:build integration_test

package permissions_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/domain/rbac/permissions"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	rbacsharedtest.RunTestMain(m, &testPool, 20)
}

// txStores begins a rolled-back transaction and returns a Store bound to it
// alongside *db.Queries for direct assertion queries. Skips when testPool is nil.
func txStores(t *testing.T) (*permissions.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured")
	}
	_, q := rbacsharedtest.MustBeginTx(t, testPool)
	return permissions.NewStore(testPool).WithQuerier(q), q
}

// withProxy wires q into proxy.Querier and returns a Store bound to it.
func withProxy(q db.Querier, proxy *rbacsharedtest.QuerierProxy) *permissions.Store {
	proxy.Querier = q
	return permissions.NewStore(testPool).WithQuerier(proxy)
}

// ── T-R32: TestGetPermissions_Integration ────────────────────────────────────

func TestGetPermissions_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("returns all seeded permissions", func(t *testing.T) {
		s, _ := txStores(t)
		perms, err := s.GetPermissions(ctx)
		require.NoError(t, err)
		require.Len(t, perms, 13)

		// Spot-check: rbac:read must be present with correct resource_type.
		var found bool
		for _, p := range perms {
			if p.CanonicalName == "rbac:read" {
				require.Equal(t, "rbac", p.ResourceType)
				require.NotEmpty(t, p.ID)
				found = true
			}
		}
		require.True(t, found, "rbac:read must be present in seeded permissions")
	})

	t.Run("result slice is never nil", func(t *testing.T) {
		s, _ := txStores(t)
		perms, err := s.GetPermissions(ctx)
		require.NoError(t, err)
		require.NotNil(t, perms)
	})

	t.Run("FailGetPermissions returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		_, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetPermissions: true}).
			GetPermissions(ctx)
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// ── T-R33: TestGetPermissionGroups_Integration ───────────────────────────────

func TestGetPermissionGroups_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("returns all 5 seeded groups with members", func(t *testing.T) {
		s, _ := txStores(t)
		groups, err := s.GetPermissionGroups(ctx)
		require.NoError(t, err)
		require.Len(t, groups, 5)

		// Spot-check: system_administration group (name column, not display_label)
		// must have 3 members: rbac:read, rbac:manage, rbac:grant_user_permission.
		var sysAdmin *permissions.PermissionGroup
		for i := range groups {
			if groups[i].Name == "system_administration" {
				sysAdmin = &groups[i]
			}
		}
		require.NotNil(t, sysAdmin, "system_administration group must be present")
		require.Len(t, sysAdmin.Members, 3)

		// Total members across all groups must equal 13
		// (3 + 3 + 3 + 3 + 1 matching the 13 seeded permissions).
		total := 0
		for _, g := range groups {
			total += len(g.Members)
		}
		require.Equal(t, 13, total)
	})

	t.Run("members slice is never nil for any group", func(t *testing.T) {
		s, _ := txStores(t)
		groups, err := s.GetPermissionGroups(ctx)
		require.NoError(t, err)
		for _, g := range groups {
			require.NotNil(t, g.Members,
				"Members must not be nil for group %q", g.Name)
		}
	})

	t.Run("result slice is never nil", func(t *testing.T) {
		s, _ := txStores(t)
		groups, err := s.GetPermissionGroups(ctx)
		require.NoError(t, err)
		require.NotNil(t, groups)
	})

	t.Run("FailGetPermissionGroups returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		_, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetPermissionGroups: true}).
			GetPermissionGroups(ctx)
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})

	t.Run("FailGetPermissionGroupMembers returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		_, err := withProxy(q, &rbacsharedtest.QuerierProxy{FailGetPermissionGroupMembers: true}).
			GetPermissionGroups(ctx)
		require.ErrorIs(t, err, rbacsharedtest.ErrProxy)
	})
}

// ── T-R32b: TestGetPermissions_Empty_Integration ────────────────────────────

func TestGetPermissions_Empty_Integration(t *testing.T) {
	ctx := context.Background()
	s, q := txStores(t)

	// Soft-deactivate every permission row inside the transaction so the store
	// sees an empty active-permissions set. The transaction rolls back on cleanup.
	require.NoError(t, q.DeactivateAllPermissionsForTest(ctx))

	perms, err := s.GetPermissions(ctx)
	require.NoError(t, err)
	require.NotNil(t, perms, "GetPermissions must return a non-nil slice even when no rows match")
	require.Len(t, perms, 0)
}

// ── T-R33b: TestGetPermissionGroups_ZeroMemberGroup_Integration ──────────────

func TestGetPermissionGroups_ZeroMemberGroup_Integration(t *testing.T) {
	ctx := context.Background()
	s, q := txStores(t)

	// Insert a group with no permission_group_members rows.
	// The unique name is stable within this rolled-back transaction and does
	// not conflict with any seeded group name.
	groupID, err := q.CreatePermissionGroupForTest(ctx, "test_zero_member_group")
	require.NoError(t, err)

	groups, err := s.GetPermissionGroups(ctx)
	require.NoError(t, err)

	// Locate the newly inserted group by its returned UUID.
	wantID := groupID.String()
	var found *permissions.PermissionGroup
	for i := range groups {
		if groups[i].ID == wantID {
			found = &groups[i]
			break
		}
	}
	require.NotNil(t, found, "newly inserted group must appear in GetPermissionGroups result")
	require.NotNil(t, found.Members,
		"Members must not be nil for a group with no permission_group_members rows")
	require.Empty(t, found.Members)
}
