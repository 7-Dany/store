// Package adminsharedtest provides test-only helpers shared across all admin
// feature sub-packages. It must never be imported by production code.
package adminsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/domain/admin/userlock"
	"github.com/7-Dany/store/backend/internal/domain/admin/userpermissions"
	"github.com/7-Dany/store/backend/internal/domain/admin/userroles"
)

// ─────────────────────────────────────────────────────────────────────────────
// UserRolesFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// UserRolesFakeStorer implements userroles.Storer for service unit tests.
//
// Defaults:
//
//	GetUserRoleFn      → (UserRole{RoleName: "admin"}, nil)
//	AssignUserRoleTxFn → (UserRole{}, nil)
//	RemoveUserRoleFn   → nil
type UserRolesFakeStorer struct {
	GetUserRoleFn      func(ctx context.Context, userID [16]byte) (userroles.UserRole, error)
	AssignUserRoleTxFn func(ctx context.Context, in userroles.AssignRoleTxInput) (userroles.UserRole, error)
	RemoveUserRoleFn   func(ctx context.Context, userID [16]byte, actingUserID string) error
}

var _ userroles.Storer = (*UserRolesFakeStorer)(nil)

func (f *UserRolesFakeStorer) GetUserRole(ctx context.Context, userID [16]byte) (userroles.UserRole, error) {
	if f.GetUserRoleFn != nil {
		return f.GetUserRoleFn(ctx, userID)
	}
	return userroles.UserRole{RoleName: "admin"}, nil
}

func (f *UserRolesFakeStorer) AssignUserRoleTx(ctx context.Context, in userroles.AssignRoleTxInput) (userroles.UserRole, error) {
	if f.AssignUserRoleTxFn != nil {
		return f.AssignUserRoleTxFn(ctx, in)
	}
	return userroles.UserRole{}, nil
}

func (f *UserRolesFakeStorer) RemoveUserRole(ctx context.Context, userID [16]byte, actingUserID string) error {
	if f.RemoveUserRoleFn != nil {
		return f.RemoveUserRoleFn(ctx, userID, actingUserID)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UserPermissionsFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// UserPermissionsFakeStorer implements userpermissions.Storer for service unit tests.
//
// Defaults:
//
//	GetUserPermissionsFn → ([]userpermissions.UserPermission{}, nil)
//	GrantPermissionTxFn  → (userpermissions.UserPermission{}, nil)
//	RevokePermissionFn   → nil
type UserPermissionsFakeStorer struct {
	GetUserPermissionsFn func(ctx context.Context, userID [16]byte) ([]userpermissions.UserPermission, error)
	GrantPermissionTxFn  func(ctx context.Context, in userpermissions.GrantPermissionTxInput) (userpermissions.UserPermission, error)
	RevokePermissionFn   func(ctx context.Context, grantID, userID [16]byte, actingUserID string) error
}

var _ userpermissions.Storer = (*UserPermissionsFakeStorer)(nil)

func (f *UserPermissionsFakeStorer) GetUserPermissions(ctx context.Context, userID [16]byte) ([]userpermissions.UserPermission, error) {
	if f.GetUserPermissionsFn != nil {
		return f.GetUserPermissionsFn(ctx, userID)
	}
	return []userpermissions.UserPermission{}, nil
}

func (f *UserPermissionsFakeStorer) GrantPermissionTx(ctx context.Context, in userpermissions.GrantPermissionTxInput) (userpermissions.UserPermission, error) {
	if f.GrantPermissionTxFn != nil {
		return f.GrantPermissionTxFn(ctx, in)
	}
	return userpermissions.UserPermission{}, nil
}

func (f *UserPermissionsFakeStorer) RevokePermission(ctx context.Context, grantID, userID [16]byte, actingUserID string) error {
	if f.RevokePermissionFn != nil {
		return f.RevokePermissionFn(ctx, grantID, userID, actingUserID)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UserLockFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// UserLockFakeStorer implements userlock.Storer for service unit tests.
//
// Defaults:
//
//	IsOwnerUserFn   → (false, nil)
//	GetLockStatusFn → (UserLockStatus{}, nil)
//	LockUserTxFn    → nil
//	UnlockUserTxFn  → nil
type UserLockFakeStorer struct {
	IsOwnerUserFn   func(ctx context.Context, userID [16]byte) (bool, error)
	GetLockStatusFn func(ctx context.Context, userID [16]byte) (userlock.UserLockStatus, error)
	LockUserTxFn    func(ctx context.Context, in userlock.LockUserTxInput) error
	UnlockUserTxFn  func(ctx context.Context, userID [16]byte, actingUserID string) error
}

var _ userlock.Storer = (*UserLockFakeStorer)(nil)

func (f *UserLockFakeStorer) IsOwnerUser(ctx context.Context, userID [16]byte) (bool, error) {
	if f.IsOwnerUserFn != nil {
		return f.IsOwnerUserFn(ctx, userID)
	}
	return false, nil
}

func (f *UserLockFakeStorer) GetLockStatus(ctx context.Context, userID [16]byte) (userlock.UserLockStatus, error) {
	if f.GetLockStatusFn != nil {
		return f.GetLockStatusFn(ctx, userID)
	}
	return userlock.UserLockStatus{}, nil
}

func (f *UserLockFakeStorer) LockUserTx(ctx context.Context, in userlock.LockUserTxInput) error {
	if f.LockUserTxFn != nil {
		return f.LockUserTxFn(ctx, in)
	}
	return nil
}

func (f *UserLockFakeStorer) UnlockUserTx(ctx context.Context, userID [16]byte, actingUserID string) error {
	if f.UnlockUserTxFn != nil {
		return f.UnlockUserTxFn(ctx, userID, actingUserID)
	}
	return nil
}
