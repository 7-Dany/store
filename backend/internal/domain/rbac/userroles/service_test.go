package userroles_test

import (
	"context"
	"errors"
	"testing"

	"github.com/7-Dany/store/backend/internal/domain/rbac/userroles"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/stretchr/testify/require"
)

const (
	targetID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	actorID  = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	roleID   = "cccccccc-dddd-eeee-ffff-aaaaaaaaaaaa"
)

// ── T-R37: self-assignment guard ──────────────────────────────────────────────

func TestService_AssignRole_SelfAssignment(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{}
	svc := userroles.NewService(store)
	_, err := svc.AssignRole(context.Background(), targetID, targetID, userroles.AssignRoleInput{
		RoleID: roleID, GrantedReason: "test",
	})
	require.ErrorIs(t, err, platformrbac.ErrCannotModifyOwnRole)
}

// ── T-R37b: owner guard ───────────────────────────────────────────────────────

func TestService_AssignRole_OwnerGuard(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{
		GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
			return userroles.UserRole{IsOwnerRole: true, RoleName: "owner"}, nil
		},
	}
	svc := userroles.NewService(store)
	_, err := svc.AssignRole(context.Background(), targetID, actorID, userroles.AssignRoleInput{
		RoleID: roleID, GrantedReason: "test",
	})
	require.ErrorIs(t, err, platformrbac.ErrCannotReassignOwner)
}

// ── T-R37c: no existing role (safe to proceed) ────────────────────────────────

func TestService_AssignRole_NoExistingRole(t *testing.T) {
	t.Parallel()
	expected := userroles.UserRole{RoleName: "admin", RoleID: roleID}
	store := &rbacsharedtest.UserRolesFakeStorer{
		GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
			return userroles.UserRole{}, userroles.ErrUserRoleNotFound
		},
		AssignUserRoleTxFn: func(_ context.Context, _ userroles.AssignRoleTxInput) (userroles.UserRole, error) {
			return expected, nil
		},
	}
	svc := userroles.NewService(store)
	got, err := svc.AssignRole(context.Background(), targetID, actorID, userroles.AssignRoleInput{
		RoleID: roleID, GrantedReason: "test",
	})
	require.NoError(t, err)
	require.Equal(t, expected.RoleName, got.RoleName)
}

// ── validation guard: role_id required ───────────────────────────────────────

func TestService_AssignRole_RoleIDRequired(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{}
	svc := userroles.NewService(store)
	_, err := svc.AssignRole(context.Background(), targetID, actorID, userroles.AssignRoleInput{
		GrantedReason: "test",
	})
	require.ErrorIs(t, err, userroles.ErrRoleIDEmpty)
}

// ── validation guard: granted_reason required ─────────────────────────────────

func TestService_AssignRole_GrantedReasonRequired(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{}
	svc := userroles.NewService(store)
	_, err := svc.AssignRole(context.Background(), targetID, actorID, userroles.AssignRoleInput{
		RoleID: roleID,
	})
	require.ErrorIs(t, err, userroles.ErrGrantedReasonEmpty)
}

// ── RemoveRole: self-assignment guard ─────────────────────────────────────────

func TestService_RemoveRole_SelfAssignment(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{}
	svc := userroles.NewService(store)
	err := svc.RemoveRole(context.Background(), targetID, targetID)
	require.ErrorIs(t, err, platformrbac.ErrCannotModifyOwnRole)
}

// ── RemoveRole: owner guard ───────────────────────────────────────────────────

func TestService_RemoveRole_OwnerGuard(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{
		GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
			return userroles.UserRole{IsOwnerRole: true, RoleName: "owner"}, nil
		},
	}
	svc := userroles.NewService(store)
	err := svc.RemoveRole(context.Background(), targetID, actorID)
	require.ErrorIs(t, err, platformrbac.ErrCannotReassignOwner)
}

// ── RemoveRole: no existing role ──────────────────────────────────────────────

func TestService_RemoveRole_NotFound(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{
		GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
			return userroles.UserRole{}, userroles.ErrUserRoleNotFound
		},
	}
	svc := userroles.NewService(store)
	err := svc.RemoveRole(context.Background(), targetID, actorID)
	require.ErrorIs(t, err, userroles.ErrUserRoleNotFound)
}

// ── RemoveRole: propagates store errors ──────────────────────────────────────

func TestService_RemoveRole_StoreError(t *testing.T) {
	t.Parallel()
	storeErr := errors.New("db down")
	store := &rbacsharedtest.UserRolesFakeStorer{
		GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
			return userroles.UserRole{RoleName: "admin"}, nil
		},
		RemoveUserRoleFn: func(_ context.Context, _ [16]byte, _ string) error {
			return storeErr
		},
	}
	svc := userroles.NewService(store)
	err := svc.RemoveRole(context.Background(), targetID, actorID)
	require.ErrorIs(t, err, storeErr)
}

// ── GetUserRole: invalid UUID maps to ErrUserRoleNotFound ─────────────────────

func TestService_GetUserRole_InvalidUUID(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{}
	svc := userroles.NewService(store)
	_, err := svc.GetUserRole(context.Background(), "not-a-uuid")
	require.ErrorIs(t, err, userroles.ErrUserRoleNotFound)
}

// ── FIX 14a: GetUserRole success ─────────────────────────────────────────────────

func TestService_GetUserRole_Success(t *testing.T) {
	t.Parallel()
	expected := userroles.UserRole{RoleName: "admin", RoleID: roleID}
	store := &rbacsharedtest.UserRolesFakeStorer{
		GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
			return expected, nil
		},
	}
	svc := userroles.NewService(store)
	got, err := svc.GetUserRole(context.Background(), targetID)
	require.NoError(t, err)
	require.Equal(t, expected.RoleName, got.RoleName)
}

// ── FIX 14b: GetUserRole store error ─────────────────────────────────────────────

func TestService_GetUserRole_StoreError(t *testing.T) {
	t.Parallel()
	storeErr := errors.New("db down")
	store := &rbacsharedtest.UserRolesFakeStorer{
		GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
			return userroles.UserRole{}, storeErr
		},
	}
	svc := userroles.NewService(store)
	_, err := svc.GetUserRole(context.Background(), targetID)
	require.ErrorIs(t, err, storeErr)
}

// ── FIX 14c: AssignRole invalid target UUID ────────────────────────────────────

func TestService_AssignRole_InvalidTargetUUID(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{}
	svc := userroles.NewService(store)
	_, err := svc.AssignRole(context.Background(), "not-a-uuid", actorID, userroles.AssignRoleInput{
		RoleID: roleID, GrantedReason: "test",
	})
	require.ErrorIs(t, err, userroles.ErrUserRoleNotFound)
}

// ── FIX 14d: AssignRole invalid acting UUID (500 path) ─────────────────────────

func TestService_AssignRole_InvalidActingUUID(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{}
	svc := userroles.NewService(store)
	_, err := svc.AssignRole(context.Background(), targetID, "not-a-uuid", userroles.AssignRoleInput{
		RoleID: roleID, GrantedReason: "test",
	})
	require.NotNil(t, err)
	// Not a sentinel — exercises the 500 internal path.
	require.False(t, errors.Is(err, userroles.ErrUserRoleNotFound))
	require.False(t, errors.Is(err, userroles.ErrRoleNotFound))
}

// ── FIX 14e: AssignRole invalid role UUID ──────────────────────────────────────

func TestService_AssignRole_InvalidRoleUUID(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{
		GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
			return userroles.UserRole{}, userroles.ErrUserRoleNotFound
		},
	}
	svc := userroles.NewService(store)
	_, err := svc.AssignRole(context.Background(), targetID, actorID, userroles.AssignRoleInput{
		RoleID: "not-a-uuid", GrantedReason: "test",
	})
	require.ErrorIs(t, err, userroles.ErrRoleNotFound)
}

// ── FIX 14f: AssignRole — GetUserRole store error (non-sentinel) ────────────────

func TestService_AssignRole_GetUserRoleStoreError(t *testing.T) {
	t.Parallel()
	storeErr := errors.New("db down")
	store := &rbacsharedtest.UserRolesFakeStorer{
		GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
			return userroles.UserRole{}, storeErr
		},
	}
	svc := userroles.NewService(store)
	_, err := svc.AssignRole(context.Background(), targetID, actorID, userroles.AssignRoleInput{
		RoleID: roleID, GrantedReason: "test",
	})
	require.ErrorIs(t, err, storeErr)
}

// ── FIX 14g: AssignRole — store.AssignUserRoleTx error ────────────────────────

func TestService_AssignRole_StoreAssignError(t *testing.T) {
	t.Parallel()
	storeErr := errors.New("db down")
	store := &rbacsharedtest.UserRolesFakeStorer{
		GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
			return userroles.UserRole{}, userroles.ErrUserRoleNotFound
		},
		AssignUserRoleTxFn: func(_ context.Context, _ userroles.AssignRoleTxInput) (userroles.UserRole, error) {
			return userroles.UserRole{}, storeErr
		},
	}
	svc := userroles.NewService(store)
	_, err := svc.AssignRole(context.Background(), targetID, actorID, userroles.AssignRoleInput{
		RoleID: roleID, GrantedReason: "test",
	})
	require.ErrorIs(t, err, storeErr)
}

// ── FIX 14h: RemoveRole invalid target UUID ──────────────────────────────────

func TestService_RemoveRole_InvalidTargetUUID(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{}
	svc := userroles.NewService(store)
	err := svc.RemoveRole(context.Background(), "not-a-uuid", actorID)
	require.ErrorIs(t, err, userroles.ErrUserRoleNotFound)
}

// ── FIX 14i: RemoveRole invalid acting UUID ─────────────────────────────────

func TestService_RemoveRole_InvalidActingUUID(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{}
	svc := userroles.NewService(store)
	err := svc.RemoveRole(context.Background(), targetID, "not-a-uuid")
	require.NotNil(t, err)
}

// ── FIX 14j: RemoveRole success ───────────────────────────────────────────────

func TestService_RemoveRole_Success(t *testing.T) {
	t.Parallel()
	store := &rbacsharedtest.UserRolesFakeStorer{
		GetUserRoleFn: func(_ context.Context, _ [16]byte) (userroles.UserRole, error) {
			return userroles.UserRole{RoleName: "admin", IsOwnerRole: false}, nil
		},
	}
	svc := userroles.NewService(store)
	err := svc.RemoveRole(context.Background(), targetID, actorID)
	require.NoError(t, err)
}
