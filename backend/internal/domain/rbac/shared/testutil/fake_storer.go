// Package rbacsharedtest provides test-only helpers shared across all rbac
// feature sub-packages. It must never be imported by production code.
package rbacsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/domain/rbac/bootstrap"
	"github.com/7-Dany/store/backend/internal/domain/rbac/permissions"
	"github.com/7-Dany/store/backend/internal/domain/rbac/roles"
	"github.com/7-Dany/store/backend/internal/domain/rbac/userpermissions"
	"github.com/7-Dany/store/backend/internal/domain/rbac/userroles"
)

// ─────────────────────────────────────────────────────────────────────────────
// BootstrapFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// BootstrapFakeStorer is a hand-written implementation of bootstrap.Storer for
// service unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns a safe default so tests only configure the fields they need.
//
// Defaults are chosen so that the happy path succeeds without any configuration:
//   - CountActiveOwners → (0, nil): no owner exists, service may proceed.
//   - GetOwnerRoleID    → ([16]byte{}, nil): zero UUID, sufficient for unit tests.
//   - GetActiveUserByID → (BootstrapUser{IsActive: true, EmailVerified: true}, nil):
//     a valid, fully-verified user; avoids false guard failures in tests that do
//     not care about user-state checks.
//   - BootstrapOwnerTx  → (BootstrapResult{}, nil): zero result, nil error.
type BootstrapFakeStorer struct {
	CountActiveOwnersFn func(ctx context.Context) (int64, error)
	GetOwnerRoleIDFn    func(ctx context.Context) ([16]byte, error)
	GetActiveUserByIDFn func(ctx context.Context, userID [16]byte) (bootstrap.BootstrapUser, error)
	BootstrapOwnerTxFn  func(ctx context.Context, in bootstrap.BootstrapTxInput) (bootstrap.BootstrapResult, error)
}

// compile-time interface check.
var _ bootstrap.Storer = (*BootstrapFakeStorer)(nil)

// CountActiveOwners delegates to CountActiveOwnersFn if set.
// Default: returns (0, nil) — no active owner, service proceeds to the next step.
func (f *BootstrapFakeStorer) CountActiveOwners(ctx context.Context) (int64, error) {
	if f.CountActiveOwnersFn != nil {
		return f.CountActiveOwnersFn(ctx)
	}
	return 0, nil
}

// GetOwnerRoleID delegates to GetOwnerRoleIDFn if set.
// Default: returns a zero [16]byte and nil error.
func (f *BootstrapFakeStorer) GetOwnerRoleID(ctx context.Context) ([16]byte, error) {
	if f.GetOwnerRoleIDFn != nil {
		return f.GetOwnerRoleIDFn(ctx)
	}
	return [16]byte{}, nil
}

// GetActiveUserByID delegates to GetActiveUserByIDFn if set.
// Default: returns a fully-active, email-verified BootstrapUser so tests that
// do not configure this field never trip the is_active or email_verified guard.
func (f *BootstrapFakeStorer) GetActiveUserByID(ctx context.Context, userID [16]byte) (bootstrap.BootstrapUser, error) {
	if f.GetActiveUserByIDFn != nil {
		return f.GetActiveUserByIDFn(ctx, userID)
	}
	return bootstrap.BootstrapUser{IsActive: true, EmailVerified: true}, nil
}

// BootstrapOwnerTx delegates to BootstrapOwnerTxFn if set.
// Default: returns a zero BootstrapResult and nil error.
func (f *BootstrapFakeStorer) BootstrapOwnerTx(ctx context.Context, in bootstrap.BootstrapTxInput) (bootstrap.BootstrapResult, error) {
	if f.BootstrapOwnerTxFn != nil {
		return f.BootstrapOwnerTxFn(ctx, in)
	}
	return bootstrap.BootstrapResult{}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PermissionsFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// PermissionsFakeStorer is a hand-written implementation of permissions.Storer
// for service unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns a safe default so tests only configure the fields they need.
//
// Defaults are chosen so that the happy path succeeds without any configuration:
//   - GetPermissions      → ([]permissions.Permission{}, nil)
//   - GetPermissionGroups → ([]permissions.PermissionGroup{}, nil)
type PermissionsFakeStorer struct {
	GetPermissionsFn      func(ctx context.Context) ([]permissions.Permission, error)
	GetPermissionGroupsFn func(ctx context.Context) ([]permissions.PermissionGroup, error)
}

// compile-time interface check.
var _ permissions.Storer = (*PermissionsFakeStorer)(nil)

// GetPermissions delegates to GetPermissionsFn if set.
// Default: returns ([]permissions.Permission{}, nil).
func (f *PermissionsFakeStorer) GetPermissions(ctx context.Context) ([]permissions.Permission, error) {
	if f.GetPermissionsFn != nil {
		return f.GetPermissionsFn(ctx)
	}
	return []permissions.Permission{}, nil
}

// GetPermissionGroups delegates to GetPermissionGroupsFn if set.
// Default: returns ([]permissions.PermissionGroup{}, nil).
func (f *PermissionsFakeStorer) GetPermissionGroups(ctx context.Context) ([]permissions.PermissionGroup, error) {
	if f.GetPermissionGroupsFn != nil {
		return f.GetPermissionGroupsFn(ctx)
	}
	return []permissions.PermissionGroup{}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// RolesFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// RolesFakeStorer is a hand-written implementation of roles.Storer for service
// unit tests. Nil Fn fields return safe defaults so tests only configure what
// they need.
//
// Defaults:
//
//	GetRoles              → ([]roles.Role{}, nil)
//	GetRoleByID           → (roles.Role{}, nil)
//	CreateRole            → (roles.Role{}, nil)
//	UpdateRole            → (roles.Role{}, nil)
//	DeactivateRole        → nil
//	GetRolePermissions    → ([]roles.RolePermission{}, nil)
//	AddRolePermission     → nil
//	RemoveRolePermission  → nil
type RolesFakeStorer struct {
	GetRolesFn             func(ctx context.Context) ([]roles.Role, error)
	GetRoleByIDFn          func(ctx context.Context, roleID [16]byte) (roles.Role, error)
	CreateRoleFn           func(ctx context.Context, in roles.CreateRoleInput) (roles.Role, error)
	UpdateRoleFn           func(ctx context.Context, roleID [16]byte, in roles.UpdateRoleInput) (roles.Role, error)
	DeactivateRoleFn       func(ctx context.Context, roleID [16]byte) error
	GetRolePermissionsFn   func(ctx context.Context, roleID [16]byte) ([]roles.RolePermission, error)
	AddRolePermissionFn    func(ctx context.Context, roleID [16]byte, in roles.AddRolePermissionInput) error
	RemoveRolePermissionFn func(ctx context.Context, roleID, permID [16]byte, actingUserID string) error
	GetPermissionCapsFn    func(ctx context.Context, permissionID [16]byte) (roles.PermissionCaps, error)
}

// compile-time interface check.
var _ roles.Storer = (*RolesFakeStorer)(nil)

// GetRoles delegates to GetRolesFn if set.
// Default: returns ([]roles.Role{}, nil).
func (f *RolesFakeStorer) GetRoles(ctx context.Context) ([]roles.Role, error) {
	if f.GetRolesFn != nil {
		return f.GetRolesFn(ctx)
	}
	return []roles.Role{}, nil
}

// GetRoleByID delegates to GetRoleByIDFn if set.
// Default: returns (roles.Role{}, nil).
func (f *RolesFakeStorer) GetRoleByID(ctx context.Context, roleID [16]byte) (roles.Role, error) {
	if f.GetRoleByIDFn != nil {
		return f.GetRoleByIDFn(ctx, roleID)
	}
	return roles.Role{}, nil
}

// CreateRole delegates to CreateRoleFn if set.
// Default: returns (roles.Role{}, nil).
func (f *RolesFakeStorer) CreateRole(ctx context.Context, in roles.CreateRoleInput) (roles.Role, error) {
	if f.CreateRoleFn != nil {
		return f.CreateRoleFn(ctx, in)
	}
	return roles.Role{}, nil
}

// UpdateRole delegates to UpdateRoleFn if set.
// Default: returns (roles.Role{}, nil).
func (f *RolesFakeStorer) UpdateRole(ctx context.Context, roleID [16]byte, in roles.UpdateRoleInput) (roles.Role, error) {
	if f.UpdateRoleFn != nil {
		return f.UpdateRoleFn(ctx, roleID, in)
	}
	return roles.Role{}, nil
}

// DeactivateRole delegates to DeactivateRoleFn if set.
// Default: returns nil.
func (f *RolesFakeStorer) DeactivateRole(ctx context.Context, roleID [16]byte) error {
	if f.DeactivateRoleFn != nil {
		return f.DeactivateRoleFn(ctx, roleID)
	}
	return nil
}

// GetRolePermissions delegates to GetRolePermissionsFn if set.
// Default: returns ([]roles.RolePermission{}, nil).
func (f *RolesFakeStorer) GetRolePermissions(ctx context.Context, roleID [16]byte) ([]roles.RolePermission, error) {
	if f.GetRolePermissionsFn != nil {
		return f.GetRolePermissionsFn(ctx, roleID)
	}
	return []roles.RolePermission{}, nil
}

// AddRolePermission delegates to AddRolePermissionFn if set.
// Default: returns nil.
func (f *RolesFakeStorer) AddRolePermission(ctx context.Context, roleID [16]byte, in roles.AddRolePermissionInput) error {
	if f.AddRolePermissionFn != nil {
		return f.AddRolePermissionFn(ctx, roleID, in)
	}
	return nil
}

// RemoveRolePermission delegates to RemoveRolePermissionFn if set.
// Default: returns nil.
func (f *RolesFakeStorer) RemoveRolePermission(ctx context.Context, roleID, permID [16]byte, actingUserID string) error {
	if f.RemoveRolePermissionFn != nil {
		return f.RemoveRolePermissionFn(ctx, roleID, permID, actingUserID)
	}
	return nil
}

// GetPermissionCaps delegates to GetPermissionCapsFn if set.
// Default: returns (roles.PermissionCaps{}, nil).
func (f *RolesFakeStorer) GetPermissionCaps(ctx context.Context, permissionID [16]byte) (roles.PermissionCaps, error) {
	if f.GetPermissionCapsFn != nil {
		return f.GetPermissionCapsFn(ctx, permissionID)
	}
	return roles.PermissionCaps{}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UserRolesFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// UserRolesFakeStorer is a hand-written implementation of userroles.Storer for
// service unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns a safe default so tests only configure the fields they need.
//
// Defaults:
//
//	GetUserRoleFn      → (UserRole{RoleName: "admin"}, nil) — a non-owner role so guards pass by default
//	AssignUserRoleTxFn → (UserRole{}, nil)
//	RemoveUserRoleFn   → nil
type UserRolesFakeStorer struct {
	GetUserRoleFn      func(ctx context.Context, userID [16]byte) (userroles.UserRole, error)
	AssignUserRoleTxFn func(ctx context.Context, in userroles.AssignRoleTxInput) (userroles.UserRole, error)
	RemoveUserRoleFn   func(ctx context.Context, userID [16]byte, actingUserID string) error
}

// compile-time interface check.
var _ userroles.Storer = (*UserRolesFakeStorer)(nil)

// GetUserRole delegates to GetUserRoleFn if set.
// Default: returns (UserRole{RoleName: "admin"}, nil) — a non-owner role.
func (f *UserRolesFakeStorer) GetUserRole(ctx context.Context, userID [16]byte) (userroles.UserRole, error) {
	if f.GetUserRoleFn != nil {
		return f.GetUserRoleFn(ctx, userID)
	}
	return userroles.UserRole{RoleName: "admin"}, nil
}

// AssignUserRoleTx delegates to AssignUserRoleTxFn if set.
// Default: returns (UserRole{}, nil).
func (f *UserRolesFakeStorer) AssignUserRoleTx(ctx context.Context, in userroles.AssignRoleTxInput) (userroles.UserRole, error) {
	if f.AssignUserRoleTxFn != nil {
		return f.AssignUserRoleTxFn(ctx, in)
	}
	return userroles.UserRole{}, nil
}

// RemoveUserRole delegates to RemoveUserRoleFn if set.
// Default: returns nil.
func (f *UserRolesFakeStorer) RemoveUserRole(ctx context.Context, userID [16]byte, actingUserID string) error {
	if f.RemoveUserRoleFn != nil {
		return f.RemoveUserRoleFn(ctx, userID, actingUserID)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UserPermissionsFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// UserPermissionsFakeStorer is a hand-written implementation of userpermissions.Storer
// for service unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns a safe default so tests only configure the fields they need.
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

// compile-time interface check.
var _ userpermissions.Storer = (*UserPermissionsFakeStorer)(nil)

// GetUserPermissions delegates to GetUserPermissionsFn if set.
// Default: returns ([]userpermissions.UserPermission{}, nil).
func (f *UserPermissionsFakeStorer) GetUserPermissions(ctx context.Context, userID [16]byte) ([]userpermissions.UserPermission, error) {
	if f.GetUserPermissionsFn != nil {
		return f.GetUserPermissionsFn(ctx, userID)
	}
	return []userpermissions.UserPermission{}, nil
}

// GrantPermissionTx delegates to GrantPermissionTxFn if set.
// Default: returns (userpermissions.UserPermission{}, nil).
func (f *UserPermissionsFakeStorer) GrantPermissionTx(ctx context.Context, in userpermissions.GrantPermissionTxInput) (userpermissions.UserPermission, error) {
	if f.GrantPermissionTxFn != nil {
		return f.GrantPermissionTxFn(ctx, in)
	}
	return userpermissions.UserPermission{}, nil
}

// RevokePermission delegates to RevokePermissionFn if set.
// Default: returns nil.
func (f *UserPermissionsFakeStorer) RevokePermission(ctx context.Context, grantID, userID [16]byte, actingUserID string) error {
	if f.RevokePermissionFn != nil {
		return f.RevokePermissionFn(ctx, grantID, userID, actingUserID)
	}
	return nil
}
