package permissions_test

import (
	"context"
	"errors"
	"testing"

	"github.com/7-Dany/store/backend/internal/domain/rbac/permissions"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/stretchr/testify/require"
)

// ── TestService_ListPermissions ───────────────────────────────────────────────

func TestService_ListPermissions(t *testing.T) {
	t.Parallel()

	t.Run("delegates to store and returns result", func(t *testing.T) {
		t.Parallel()
		want := []permissions.Permission{
			{ID: "id-1", CanonicalName: "rbac:read", ResourceType: "rbac", Name: "RBAC Read"},
			{ID: "id-2", CanonicalName: "rbac:manage", ResourceType: "rbac", Name: "RBAC Manage"},
			{ID: "id-3", CanonicalName: "user:read", ResourceType: "user", Name: "User Read"},
		}
		store := &rbacsharedtest.PermissionsFakeStorer{
			GetPermissionsFn: func(_ context.Context) ([]permissions.Permission, error) {
				return want, nil
			},
		}
		got, err := permissions.NewService(store).ListPermissions(context.Background())
		require.NoError(t, err)
		require.Len(t, got, 3)
		require.Equal(t, want, got)
	})

	t.Run("store error is wrapped and returned", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db down")
		store := &rbacsharedtest.PermissionsFakeStorer{
			GetPermissionsFn: func(_ context.Context) ([]permissions.Permission, error) {
				return nil, dbErr
			},
		}
		_, err := permissions.NewService(store).ListPermissions(context.Background())
		require.ErrorIs(t, err, dbErr)
		require.Contains(t, err.Error(), "permissions.ListPermissions:")
	})
}

// ── TestService_ListPermissionGroups ─────────────────────────────────────────

func TestService_ListPermissionGroups(t *testing.T) {
	t.Parallel()

	t.Run("delegates to store and returns result", func(t *testing.T) {
		t.Parallel()
		want := []permissions.PermissionGroup{
			{ID: "g-1", Name: "System Administration", Members: []permissions.PermissionGroupMember{}},
			{ID: "g-2", Name: "User Management", Members: []permissions.PermissionGroupMember{}},
		}
		store := &rbacsharedtest.PermissionsFakeStorer{
			GetPermissionGroupsFn: func(_ context.Context) ([]permissions.PermissionGroup, error) {
				return want, nil
			},
		}
		got, err := permissions.NewService(store).ListPermissionGroups(context.Background())
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.Equal(t, want, got)
	})

	t.Run("store error is wrapped and returned", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db down")
		store := &rbacsharedtest.PermissionsFakeStorer{
			GetPermissionGroupsFn: func(_ context.Context) ([]permissions.PermissionGroup, error) {
				return nil, dbErr
			},
		}
		_, err := permissions.NewService(store).ListPermissionGroups(context.Background())
		require.ErrorIs(t, err, dbErr)
		require.Contains(t, err.Error(), "permissions.ListPermissionGroups:")
	})
}
