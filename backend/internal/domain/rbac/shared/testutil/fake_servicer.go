// Package rbacsharedtest provides test-only helpers shared across all rbac
// feature sub-packages. It must never be imported by production code.
package rbacsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/domain/rbac/owner"
	"github.com/7-Dany/store/backend/internal/domain/rbac/permissions"
	"github.com/7-Dany/store/backend/internal/domain/rbac/roles"
)

// ─────────────────────────────────────────────────────────────────────────────
// OwnerFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

type OwnerFakeServicer struct {
	AssignOwnerFn        func(ctx context.Context, in owner.AssignOwnerInput) (owner.AssignOwnerResult, error)
	HasPendingTransferFn func(ctx context.Context) (bool, error)
	InitiateTransferFn   func(ctx context.Context, in owner.InitiateInput) (owner.InitiateResult, string, error)
	AcceptTransferFn     func(ctx context.Context, in owner.AcceptInput) (owner.AcceptResult, error)
	CancelTransferFn     func(ctx context.Context, actingOwnerID [16]byte, ipAddress, userAgent string) error
}

var _ owner.Servicer = (*OwnerFakeServicer)(nil)

func (f *OwnerFakeServicer) AssignOwner(ctx context.Context, in owner.AssignOwnerInput) (owner.AssignOwnerResult, error) {
	if f.AssignOwnerFn != nil {
		return f.AssignOwnerFn(ctx, in)
	}
	return owner.AssignOwnerResult{}, nil
}

func (f *OwnerFakeServicer) HasPendingTransfer(ctx context.Context) (bool, error) {
	if f.HasPendingTransferFn != nil {
		return f.HasPendingTransferFn(ctx)
	}
	return false, nil
}

func (f *OwnerFakeServicer) InitiateTransfer(ctx context.Context, in owner.InitiateInput) (owner.InitiateResult, string, error) {
	if f.InitiateTransferFn != nil {
		return f.InitiateTransferFn(ctx, in)
	}
	return owner.InitiateResult{}, "", nil
}

func (f *OwnerFakeServicer) AcceptTransfer(ctx context.Context, in owner.AcceptInput) (owner.AcceptResult, error) {
	if f.AcceptTransferFn != nil {
		return f.AcceptTransferFn(ctx, in)
	}
	return owner.AcceptResult{}, nil
}

func (f *OwnerFakeServicer) CancelTransfer(ctx context.Context, actingOwnerID [16]byte, ipAddress, userAgent string) error {
	if f.CancelTransferFn != nil {
		return f.CancelTransferFn(ctx, actingOwnerID, ipAddress, userAgent)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PermissionsFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

type PermissionsFakeServicer struct {
	ListPermissionsFn      func(ctx context.Context) ([]permissions.Permission, error)
	ListPermissionGroupsFn func(ctx context.Context) ([]permissions.PermissionGroup, error)
}

var _ permissions.Servicer = (*PermissionsFakeServicer)(nil)

func (f *PermissionsFakeServicer) ListPermissions(ctx context.Context) ([]permissions.Permission, error) {
	if f.ListPermissionsFn != nil {
		return f.ListPermissionsFn(ctx)
	}
	return []permissions.Permission{}, nil
}

func (f *PermissionsFakeServicer) ListPermissionGroups(ctx context.Context) ([]permissions.PermissionGroup, error) {
	if f.ListPermissionGroupsFn != nil {
		return f.ListPermissionGroupsFn(ctx)
	}
	return []permissions.PermissionGroup{}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// RolesFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

type RolesFakeServicer struct {
	ListRolesFn            func(ctx context.Context) ([]roles.Role, error)
	GetRoleFn              func(ctx context.Context, roleID string) (roles.Role, error)
	CreateRoleFn           func(ctx context.Context, in roles.CreateRoleInput) (roles.Role, error)
	UpdateRoleFn           func(ctx context.Context, roleID string, in roles.UpdateRoleInput) (roles.Role, error)
	DeleteRoleFn           func(ctx context.Context, roleID string) error
	ListRolePermissionsFn  func(ctx context.Context, roleID string) ([]roles.RolePermission, error)
	AddRolePermissionFn    func(ctx context.Context, roleID string, in roles.AddRolePermissionInput) error
	RemoveRolePermissionFn func(ctx context.Context, roleID, permID, actingUserID string) error
}

var _ roles.Servicer = (*RolesFakeServicer)(nil)

func (f *RolesFakeServicer) ListRoles(ctx context.Context) ([]roles.Role, error) {
	if f.ListRolesFn != nil {
		return f.ListRolesFn(ctx)
	}
	return []roles.Role{}, nil
}

func (f *RolesFakeServicer) GetRole(ctx context.Context, roleID string) (roles.Role, error) {
	if f.GetRoleFn != nil {
		return f.GetRoleFn(ctx, roleID)
	}
	return roles.Role{}, nil
}

func (f *RolesFakeServicer) CreateRole(ctx context.Context, in roles.CreateRoleInput) (roles.Role, error) {
	if f.CreateRoleFn != nil {
		return f.CreateRoleFn(ctx, in)
	}
	return roles.Role{}, nil
}

func (f *RolesFakeServicer) UpdateRole(ctx context.Context, roleID string, in roles.UpdateRoleInput) (roles.Role, error) {
	if f.UpdateRoleFn != nil {
		return f.UpdateRoleFn(ctx, roleID, in)
	}
	return roles.Role{}, nil
}

func (f *RolesFakeServicer) DeleteRole(ctx context.Context, roleID string) error {
	if f.DeleteRoleFn != nil {
		return f.DeleteRoleFn(ctx, roleID)
	}
	return nil
}

func (f *RolesFakeServicer) ListRolePermissions(ctx context.Context, roleID string) ([]roles.RolePermission, error) {
	if f.ListRolePermissionsFn != nil {
		return f.ListRolePermissionsFn(ctx, roleID)
	}
	return []roles.RolePermission{}, nil
}

func (f *RolesFakeServicer) AddRolePermission(ctx context.Context, roleID string, in roles.AddRolePermissionInput) error {
	if f.AddRolePermissionFn != nil {
		return f.AddRolePermissionFn(ctx, roleID, in)
	}
	return nil
}

func (f *RolesFakeServicer) RemoveRolePermission(ctx context.Context, roleID, permID, actingUserID string) error {
	if f.RemoveRolePermissionFn != nil {
		return f.RemoveRolePermissionFn(ctx, roleID, permID, actingUserID)
	}
	return nil
}
