// Package rbacsharedtest provides test-only helpers shared across all rbac
// feature sub-packages. It must never be imported by production code.
package rbacsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/domain/rbac/bootstrap"
	"github.com/7-Dany/store/backend/internal/domain/rbac/permissions"
	"github.com/7-Dany/store/backend/internal/domain/rbac/roles"
)

// ─────────────────────────────────────────────────────────────────────────────
// BootstrapFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// BootstrapFakeServicer is a hand-written implementation of bootstrap.Servicer
// for handler unit tests. Set BootstrapFn to control the response; leave it nil
// to return a zero BootstrapResult and nil error.
type BootstrapFakeServicer struct {
	BootstrapFn func(ctx context.Context, in bootstrap.BootstrapInput) (bootstrap.BootstrapResult, error)
}

// compile-time interface check.
var _ bootstrap.Servicer = (*BootstrapFakeServicer)(nil)

// Bootstrap delegates to BootstrapFn if set.
// Default: returns (BootstrapResult{}, nil).
func (f *BootstrapFakeServicer) Bootstrap(ctx context.Context, in bootstrap.BootstrapInput) (bootstrap.BootstrapResult, error) {
	if f.BootstrapFn != nil {
		return f.BootstrapFn(ctx, in)
	}
	return bootstrap.BootstrapResult{}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PermissionsFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// PermissionsFakeServicer is a hand-written implementation of permissions.Servicer
// for handler unit tests. Set the Fn fields to control responses; leave nil
// to return an empty slice and nil error.
type PermissionsFakeServicer struct {
	ListPermissionsFn      func(ctx context.Context) ([]permissions.Permission, error)
	ListPermissionGroupsFn func(ctx context.Context) ([]permissions.PermissionGroup, error)
}

// compile-time interface check.
var _ permissions.Servicer = (*PermissionsFakeServicer)(nil)

// ListPermissions delegates to ListPermissionsFn if set.
// Default: returns ([]permissions.Permission{}, nil).
func (f *PermissionsFakeServicer) ListPermissions(ctx context.Context) ([]permissions.Permission, error) {
	if f.ListPermissionsFn != nil {
		return f.ListPermissionsFn(ctx)
	}
	return []permissions.Permission{}, nil
}

// ListPermissionGroups delegates to ListPermissionGroupsFn if set.
// Default: returns ([]permissions.PermissionGroup{}, nil).
func (f *PermissionsFakeServicer) ListPermissionGroups(ctx context.Context) ([]permissions.PermissionGroup, error) {
	if f.ListPermissionGroupsFn != nil {
		return f.ListPermissionGroupsFn(ctx)
	}
	return []permissions.PermissionGroup{}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// RolesFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// RolesFakeServicer is a hand-written implementation of roles.Servicer for
// handler unit tests. Nil Fn fields return safe defaults.
//
// Defaults:
//
//	ListRolesFn            → ([]roles.Role{}, nil)
//	GetRoleFn              → (roles.Role{}, nil)
//	CreateRoleFn           → (roles.Role{}, nil)
//	UpdateRoleFn           → (roles.Role{}, nil)
//	DeleteRoleFn           → nil
//	ListRolePermissionsFn  → ([]roles.RolePermission{}, nil)
//	AddRolePermissionFn    → nil
//	RemoveRolePermissionFn → nil
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

// compile-time interface check.
var _ roles.Servicer = (*RolesFakeServicer)(nil)

// ListRoles delegates to ListRolesFn if set.
// Default: returns ([]roles.Role{}, nil).
func (f *RolesFakeServicer) ListRoles(ctx context.Context) ([]roles.Role, error) {
	if f.ListRolesFn != nil {
		return f.ListRolesFn(ctx)
	}
	return []roles.Role{}, nil
}

// GetRole delegates to GetRoleFn if set.
// Default: returns (roles.Role{}, nil).
func (f *RolesFakeServicer) GetRole(ctx context.Context, roleID string) (roles.Role, error) {
	if f.GetRoleFn != nil {
		return f.GetRoleFn(ctx, roleID)
	}
	return roles.Role{}, nil
}

// CreateRole delegates to CreateRoleFn if set.
// Default: returns (roles.Role{}, nil).
func (f *RolesFakeServicer) CreateRole(ctx context.Context, in roles.CreateRoleInput) (roles.Role, error) {
	if f.CreateRoleFn != nil {
		return f.CreateRoleFn(ctx, in)
	}
	return roles.Role{}, nil
}

// UpdateRole delegates to UpdateRoleFn if set.
// Default: returns (roles.Role{}, nil).
func (f *RolesFakeServicer) UpdateRole(ctx context.Context, roleID string, in roles.UpdateRoleInput) (roles.Role, error) {
	if f.UpdateRoleFn != nil {
		return f.UpdateRoleFn(ctx, roleID, in)
	}
	return roles.Role{}, nil
}

// DeleteRole delegates to DeleteRoleFn if set.
// Default: returns nil.
func (f *RolesFakeServicer) DeleteRole(ctx context.Context, roleID string) error {
	if f.DeleteRoleFn != nil {
		return f.DeleteRoleFn(ctx, roleID)
	}
	return nil
}

// ListRolePermissions delegates to ListRolePermissionsFn if set.
// Default: returns ([]roles.RolePermission{}, nil).
func (f *RolesFakeServicer) ListRolePermissions(ctx context.Context, roleID string) ([]roles.RolePermission, error) {
	if f.ListRolePermissionsFn != nil {
		return f.ListRolePermissionsFn(ctx, roleID)
	}
	return []roles.RolePermission{}, nil
}

// AddRolePermission delegates to AddRolePermissionFn if set.
// Default: returns nil.
func (f *RolesFakeServicer) AddRolePermission(ctx context.Context, roleID string, in roles.AddRolePermissionInput) error {
	if f.AddRolePermissionFn != nil {
		return f.AddRolePermissionFn(ctx, roleID, in)
	}
	return nil
}

// RemoveRolePermission delegates to RemoveRolePermissionFn if set.
// Default: returns nil.
func (f *RolesFakeServicer) RemoveRolePermission(ctx context.Context, roleID, permID, actingUserID string) error {
	if f.RemoveRolePermissionFn != nil {
		return f.RemoveRolePermissionFn(ctx, roleID, permID, actingUserID)
	}
	return nil
}
