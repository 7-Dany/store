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
// UserRolesFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// UserRolesFakeServicer implements userroles.Servicer for handler unit tests.
//
// Defaults:
//
//	GetUserRoleFn → (UserRole{RoleName: "admin"}, nil)
//	AssignRoleFn  → (UserRole{}, nil)
//	RemoveRoleFn  → nil
type UserRolesFakeServicer struct {
	GetUserRoleFn func(ctx context.Context, targetUserID string) (userroles.UserRole, error)
	AssignRoleFn  func(ctx context.Context, targetUserID, actingUserID string, in userroles.AssignRoleInput) (userroles.UserRole, error)
	RemoveRoleFn  func(ctx context.Context, targetUserID, actingUserID string) error
}

var _ userroles.Servicer = (*UserRolesFakeServicer)(nil)

func (f *UserRolesFakeServicer) GetUserRole(ctx context.Context, targetUserID string) (userroles.UserRole, error) {
	if f.GetUserRoleFn != nil {
		return f.GetUserRoleFn(ctx, targetUserID)
	}
	return userroles.UserRole{RoleName: "admin"}, nil
}

func (f *UserRolesFakeServicer) AssignRole(ctx context.Context, targetUserID, actingUserID string, in userroles.AssignRoleInput) (userroles.UserRole, error) {
	if f.AssignRoleFn != nil {
		return f.AssignRoleFn(ctx, targetUserID, actingUserID, in)
	}
	return userroles.UserRole{}, nil
}

func (f *UserRolesFakeServicer) RemoveRole(ctx context.Context, targetUserID, actingUserID string) error {
	if f.RemoveRoleFn != nil {
		return f.RemoveRoleFn(ctx, targetUserID, actingUserID)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UserPermissionsFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// UserPermissionsFakeServicer implements userpermissions.Servicer for handler unit tests.
//
// Defaults:
//
//	ListPermissionsFn  → ([]userpermissions.UserPermission{}, nil)
//	GrantPermissionFn  → (userpermissions.UserPermission{}, nil)
//	RevokePermissionFn → nil
type UserPermissionsFakeServicer struct {
	ListPermissionsFn  func(ctx context.Context, targetUserID string) ([]userpermissions.UserPermission, error)
	GrantPermissionFn  func(ctx context.Context, targetUserID, actingUserID string, in userpermissions.GrantPermissionInput) (userpermissions.UserPermission, error)
	RevokePermissionFn func(ctx context.Context, targetUserID, grantID, actingUserID string) error
}

var _ userpermissions.Servicer = (*UserPermissionsFakeServicer)(nil)

func (f *UserPermissionsFakeServicer) ListPermissions(ctx context.Context, targetUserID string) ([]userpermissions.UserPermission, error) {
	if f.ListPermissionsFn != nil {
		return f.ListPermissionsFn(ctx, targetUserID)
	}
	return []userpermissions.UserPermission{}, nil
}

func (f *UserPermissionsFakeServicer) GrantPermission(ctx context.Context, targetUserID, actingUserID string, in userpermissions.GrantPermissionInput) (userpermissions.UserPermission, error) {
	if f.GrantPermissionFn != nil {
		return f.GrantPermissionFn(ctx, targetUserID, actingUserID, in)
	}
	return userpermissions.UserPermission{}, nil
}

func (f *UserPermissionsFakeServicer) RevokePermission(ctx context.Context, targetUserID, grantID, actingUserID string) error {
	if f.RevokePermissionFn != nil {
		return f.RevokePermissionFn(ctx, targetUserID, grantID, actingUserID)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UserLockFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// UserLockFakeServicer implements userlock.Servicer for handler unit tests.
//
// Defaults:
//
//	LockUserFn      → nil
//	UnlockUserFn    → nil
//	GetLockStatusFn → (UserLockStatus{}, nil)
type UserLockFakeServicer struct {
	LockUserFn      func(ctx context.Context, targetUserID, actingUserID string, in userlock.LockUserInput) error
	UnlockUserFn    func(ctx context.Context, targetUserID, actingUserID string) error
	GetLockStatusFn func(ctx context.Context, targetUserID string) (userlock.UserLockStatus, error)
}

var _ userlock.Servicer = (*UserLockFakeServicer)(nil)

func (f *UserLockFakeServicer) LockUser(ctx context.Context, targetUserID, actingUserID string, in userlock.LockUserInput) error {
	if f.LockUserFn != nil {
		return f.LockUserFn(ctx, targetUserID, actingUserID, in)
	}
	return nil
}

func (f *UserLockFakeServicer) UnlockUser(ctx context.Context, targetUserID, actingUserID string) error {
	if f.UnlockUserFn != nil {
		return f.UnlockUserFn(ctx, targetUserID, actingUserID)
	}
	return nil
}

func (f *UserLockFakeServicer) GetLockStatus(ctx context.Context, targetUserID string) (userlock.UserLockStatus, error) {
	if f.GetLockStatusFn != nil {
		return f.GetLockStatusFn(ctx, targetUserID)
	}
	return userlock.UserLockStatus{}, nil
}
