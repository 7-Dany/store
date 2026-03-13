package roles_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/7-Dany/store/backend/internal/domain/rbac/roles"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── TestService_ListRoles ─────────────────────────────────────────────────────

func TestService_ListRoles(t *testing.T) {
	t.Parallel()

	t.Run("delegates to store and returns slice", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			GetRolesFn: func(_ context.Context) ([]roles.Role, error) {
				return []roles.Role{{ID: "id-1"}, {ID: "id-2"}}, nil
			},
		}
		svc := roles.NewService(store)
		got, err := svc.ListRoles(context.Background())
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("store error is wrapped", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db down")
		store := &rbacsharedtest.RolesFakeStorer{
			GetRolesFn: func(_ context.Context) ([]roles.Role, error) { return nil, dbErr },
		}
		svc := roles.NewService(store)
		_, err := svc.ListRoles(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, dbErr)
		assert.Contains(t, err.Error(), "roles.ListRoles:")
	})
}

// ── TestService_GetRole ───────────────────────────────────────────────────────

func TestService_GetRole(t *testing.T) {
	t.Parallel()

	t.Run("valid UUID delegates to store", func(t *testing.T) {
		t.Parallel()
		want := roles.Role{ID: testRoleID, Name: "test"}
		store := &rbacsharedtest.RolesFakeStorer{
			GetRoleByIDFn: func(_ context.Context, _ [16]byte) (roles.Role, error) { return want, nil },
		}
		svc := roles.NewService(store)
		got, err := svc.GetRole(context.Background(), testRoleID)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("invalid UUID string returns ErrRoleNotFound", func(t *testing.T) {
		t.Parallel()
		svc := roles.NewService(&rbacsharedtest.RolesFakeStorer{})
		_, err := svc.GetRole(context.Background(), "not-a-uuid")
		require.ErrorIs(t, err, roles.ErrRoleNotFound)
	})

	t.Run("store returns ErrRoleNotFound → propagated", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			GetRoleByIDFn: func(_ context.Context, _ [16]byte) (roles.Role, error) {
				return roles.Role{}, roles.ErrRoleNotFound
			},
		}
		svc := roles.NewService(store)
		_, err := svc.GetRole(context.Background(), testRoleID)
		require.ErrorIs(t, err, roles.ErrRoleNotFound)
	})

	t.Run("other store error is wrapped", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db error")
		store := &rbacsharedtest.RolesFakeStorer{
			GetRoleByIDFn: func(_ context.Context, _ [16]byte) (roles.Role, error) { return roles.Role{}, dbErr },
		}
		svc := roles.NewService(store)
		_, err := svc.GetRole(context.Background(), testRoleID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "roles.GetRole:")
	})
}

// ── TestService_CreateRole ────────────────────────────────────────────────────

func TestService_CreateRole(t *testing.T) {
	t.Parallel()

	t.Run("delegates and returns role", func(t *testing.T) {
		t.Parallel()
		want := roles.Role{ID: testRoleID, Name: "new_role"}
		store := &rbacsharedtest.RolesFakeStorer{
			CreateRoleFn: func(_ context.Context, in roles.CreateRoleInput) (roles.Role, error) {
				want.Name = in.Name
				return want, nil
			},
		}
		svc := roles.NewService(store)
		got, err := svc.CreateRole(context.Background(), roles.CreateRoleInput{Name: "new_role"})
		require.NoError(t, err)
		assert.Equal(t, "new_role", got.Name)
	})

	t.Run("store error is wrapped", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			CreateRoleFn: func(_ context.Context, _ roles.CreateRoleInput) (roles.Role, error) {
				return roles.Role{}, errors.New("db error")
			},
		}
		svc := roles.NewService(store)
		_, err := svc.CreateRole(context.Background(), roles.CreateRoleInput{Name: "x"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "roles.CreateRole:")
	})
}

// ── TestService_UpdateRole ────────────────────────────────────────────────────

func TestService_UpdateRole(t *testing.T) {
	t.Parallel()

	newName := "updated"

	t.Run("valid UUID delegates to store", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			UpdateRoleFn: func(_ context.Context, _ [16]byte, in roles.UpdateRoleInput) (roles.Role, error) {
				return roles.Role{ID: testRoleID, Name: *in.Name}, nil
			},
		}
		svc := roles.NewService(store)
		got, err := svc.UpdateRole(context.Background(), testRoleID, roles.UpdateRoleInput{Name: &newName})
		require.NoError(t, err)
		assert.Equal(t, "updated", got.Name)
	})

	t.Run("invalid UUID string returns ErrRoleNotFound", func(t *testing.T) {
		t.Parallel()
		svc := roles.NewService(&rbacsharedtest.RolesFakeStorer{})
		_, err := svc.UpdateRole(context.Background(), "bad-uuid", roles.UpdateRoleInput{Name: &newName})
		require.ErrorIs(t, err, roles.ErrRoleNotFound)
	})

	t.Run("store ErrSystemRoleImmutable propagates", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			UpdateRoleFn: func(_ context.Context, _ [16]byte, _ roles.UpdateRoleInput) (roles.Role, error) {
				return roles.Role{}, fmt.Errorf("store.UpdateRole: %w", rbac.ErrSystemRoleImmutable)
			},
		}
		svc := roles.NewService(store)
		_, err := svc.UpdateRole(context.Background(), testRoleID, roles.UpdateRoleInput{Name: &newName})
		require.ErrorIs(t, err, rbac.ErrSystemRoleImmutable)
	})

	t.Run("other store error is wrapped", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			UpdateRoleFn: func(_ context.Context, _ [16]byte, _ roles.UpdateRoleInput) (roles.Role, error) {
				return roles.Role{}, errors.New("db error")
			},
		}
		svc := roles.NewService(store)
		_, err := svc.UpdateRole(context.Background(), testRoleID, roles.UpdateRoleInput{Name: &newName})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "roles.UpdateRole:")
	})
}

// ── TestService_DeleteRole ────────────────────────────────────────────────────

func TestService_DeleteRole(t *testing.T) {
	t.Parallel()

	t.Run("valid UUID delegates to store", func(t *testing.T) {
		t.Parallel()
		svc := roles.NewService(&rbacsharedtest.RolesFakeStorer{})
		err := svc.DeleteRole(context.Background(), testRoleID)
		require.NoError(t, err)
	})

	t.Run("invalid UUID string returns ErrRoleNotFound", func(t *testing.T) {
		t.Parallel()
		svc := roles.NewService(&rbacsharedtest.RolesFakeStorer{})
		err := svc.DeleteRole(context.Background(), "bad-uuid")
		require.ErrorIs(t, err, roles.ErrRoleNotFound)
	})

	t.Run("store ErrSystemRoleImmutable propagates", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			DeactivateRoleFn: func(_ context.Context, _ [16]byte) error {
				return fmt.Errorf("store.DeactivateRole: %w", rbac.ErrSystemRoleImmutable)
			},
		}
		svc := roles.NewService(store)
		err := svc.DeleteRole(context.Background(), testRoleID)
		require.ErrorIs(t, err, rbac.ErrSystemRoleImmutable)
	})

	t.Run("other store error is wrapped", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			DeactivateRoleFn: func(_ context.Context, _ [16]byte) error {
				return errors.New("db error")
			},
		}
		svc := roles.NewService(store)
		err := svc.DeleteRole(context.Background(), testRoleID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "roles.DeleteRole:")
	})
}

// ── TestService_ListRolePermissions ──────────────────────────────────────────

func TestService_ListRolePermissions(t *testing.T) {
	t.Parallel()

	t.Run("valid UUID delegates to store", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			GetRoleByIDFn: func(_ context.Context, _ [16]byte) (roles.Role, error) {
				return roles.Role{ID: testRoleID}, nil
			},
			GetRolePermissionsFn: func(_ context.Context, _ [16]byte) ([]roles.RolePermission, error) {
				return []roles.RolePermission{{PermissionID: testPermID}}, nil
			},
		}
		svc := roles.NewService(store)
		got, err := svc.ListRolePermissions(context.Background(), testRoleID)
		require.NoError(t, err)
		assert.Len(t, got, 1)
	})

	t.Run("invalid UUID string returns ErrRoleNotFound", func(t *testing.T) {
		t.Parallel()
		svc := roles.NewService(&rbacsharedtest.RolesFakeStorer{})
		_, err := svc.ListRolePermissions(context.Background(), "bad-uuid")
		require.ErrorIs(t, err, roles.ErrRoleNotFound)
	})

	t.Run("role not found returns ErrRoleNotFound", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			GetRoleByIDFn: func(_ context.Context, _ [16]byte) (roles.Role, error) {
				return roles.Role{}, roles.ErrRoleNotFound
			},
		}
		svc := roles.NewService(store)
		_, err := svc.ListRolePermissions(context.Background(), testRoleID)
		require.ErrorIs(t, err, roles.ErrRoleNotFound)
	})

	t.Run("GetRoleByID other error is wrapped", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			GetRoleByIDFn: func(_ context.Context, _ [16]byte) (roles.Role, error) {
				return roles.Role{}, errors.New("db error")
			},
		}
		svc := roles.NewService(store)
		_, err := svc.ListRolePermissions(context.Background(), testRoleID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "roles.ListRolePermissions:")
	})

	t.Run("GetRolePermissions error is wrapped", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			GetRoleByIDFn: func(_ context.Context, _ [16]byte) (roles.Role, error) {
				return roles.Role{ID: testRoleID}, nil
			},
			GetRolePermissionsFn: func(_ context.Context, _ [16]byte) ([]roles.RolePermission, error) {
				return nil, errors.New("db error")
			},
		}
		svc := roles.NewService(store)
		_, err := svc.ListRolePermissions(context.Background(), testRoleID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "roles.ListRolePermissions:")
	})
}

// ── TestService_AddRolePermission ─────────────────────────────────────────────

func TestService_AddRolePermission(t *testing.T) {
	t.Parallel()

	validInput := roles.AddRolePermissionInput{
		GrantedReason: "test",
		AccessType:    "direct",
		Scope:         "all",
	}

	t.Run("valid role UUID delegates to store", func(t *testing.T) {
		t.Parallel()
		svc := roles.NewService(&rbacsharedtest.RolesFakeStorer{})
		err := svc.AddRolePermission(context.Background(), testRoleID, validInput)
		require.NoError(t, err)
	})

	t.Run("invalid role UUID string returns ErrRoleNotFound", func(t *testing.T) {
		t.Parallel()
		svc := roles.NewService(&rbacsharedtest.RolesFakeStorer{})
		err := svc.AddRolePermission(context.Background(), "bad-uuid", validInput)
		require.ErrorIs(t, err, roles.ErrRoleNotFound)
	})

	t.Run("empty Conditions is defaulted to {}", func(t *testing.T) {
		t.Parallel()
		var captured roles.AddRolePermissionInput
		store := &rbacsharedtest.RolesFakeStorer{
			AddRolePermissionFn: func(_ context.Context, _ [16]byte, in roles.AddRolePermissionInput) error {
				captured = in
				return nil
			},
		}
		svc := roles.NewService(store)
		in := validInput
		in.Conditions = nil
		require.NoError(t, svc.AddRolePermission(context.Background(), testRoleID, in))
		assert.Equal(t, "{}", string(captured.Conditions))
	})

	t.Run("non-empty Conditions is passed through unchanged", func(t *testing.T) {
		t.Parallel()
		var captured roles.AddRolePermissionInput
		store := &rbacsharedtest.RolesFakeStorer{
			AddRolePermissionFn: func(_ context.Context, _ [16]byte, in roles.AddRolePermissionInput) error {
				captured = in
				return nil
			},
		}
		svc := roles.NewService(store)
		in := validInput
		in.Conditions = []byte(`{"key":"val"}`)
		require.NoError(t, svc.AddRolePermission(context.Background(), testRoleID, in))
		assert.Equal(t, `{"key":"val"}`, string(captured.Conditions))
	})

	t.Run("store ErrPermissionNotFound propagates", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			AddRolePermissionFn: func(_ context.Context, _ [16]byte, _ roles.AddRolePermissionInput) error {
				return roles.ErrPermissionNotFound
			},
		}
		svc := roles.NewService(store)
		err := svc.AddRolePermission(context.Background(), testRoleID, validInput)
		require.ErrorIs(t, err, roles.ErrPermissionNotFound)
	})

	t.Run("other store error is wrapped", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			AddRolePermissionFn: func(_ context.Context, _ [16]byte, _ roles.AddRolePermissionInput) error {
				return errors.New("db error")
			},
		}
		svc := roles.NewService(store)
		err := svc.AddRolePermission(context.Background(), testRoleID, validInput)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "roles.AddRolePermission:")
	})
}

// ── TestService_RemoveRolePermission ──────────────────────────────────────────

func TestService_RemoveRolePermission(t *testing.T) {
	t.Parallel()

	t.Run("valid IDs delegate to store", func(t *testing.T) {
		t.Parallel()
		svc := roles.NewService(&rbacsharedtest.RolesFakeStorer{})
		err := svc.RemoveRolePermission(context.Background(), testRoleID, testPermID, testUserID)
		require.NoError(t, err)
	})

	t.Run("invalid roleID string returns ErrRolePermissionNotFound", func(t *testing.T) {
		t.Parallel()
		svc := roles.NewService(&rbacsharedtest.RolesFakeStorer{})
		err := svc.RemoveRolePermission(context.Background(), "bad-uuid", testPermID, testUserID)
		require.ErrorIs(t, err, roles.ErrRolePermissionNotFound)
	})

	t.Run("invalid permID string returns ErrRolePermissionNotFound", func(t *testing.T) {
		t.Parallel()
		svc := roles.NewService(&rbacsharedtest.RolesFakeStorer{})
		err := svc.RemoveRolePermission(context.Background(), testRoleID, "bad-uuid", testUserID)
		require.ErrorIs(t, err, roles.ErrRolePermissionNotFound)
	})

	t.Run("store ErrRolePermissionNotFound propagates", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			RemoveRolePermissionFn: func(_ context.Context, _, _ [16]byte, _ string) error {
				return fmt.Errorf("store.RemoveRolePermission: %w", roles.ErrRolePermissionNotFound)
			},
		}
		svc := roles.NewService(store)
		err := svc.RemoveRolePermission(context.Background(), testRoleID, testPermID, testUserID)
		require.ErrorIs(t, err, roles.ErrRolePermissionNotFound)
	})

	t.Run("other store error is wrapped", func(t *testing.T) {
		t.Parallel()
		store := &rbacsharedtest.RolesFakeStorer{
			RemoveRolePermissionFn: func(_ context.Context, _, _ [16]byte, _ string) error {
				return errors.New("db error")
			},
		}
		svc := roles.NewService(store)
		err := svc.RemoveRolePermission(context.Background(), testRoleID, testPermID, testUserID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "roles.RemoveRolePermission:")
	})
}
