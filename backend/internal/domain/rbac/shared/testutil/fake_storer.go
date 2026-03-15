// Package rbacsharedtest provides test-only helpers shared across all rbac
// feature sub-packages. It must never be imported by production code.
package rbacsharedtest

import (
	"context"
	"time"

	"github.com/7-Dany/store/backend/internal/domain/rbac/owner"
	"github.com/7-Dany/store/backend/internal/domain/rbac/permissions"
	"github.com/7-Dany/store/backend/internal/domain/rbac/roles"
)

// ─────────────────────────────────────────────────────────────────────────────
// OwnerFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

type OwnerFakeStorer struct {
	CountActiveOwnersFn          func(ctx context.Context) (int64, error)
	GetOwnerRoleIDFn             func(ctx context.Context) ([16]byte, error)
	GetActiveUserByIDFn          func(ctx context.Context, userID [16]byte) (owner.AssignOwnerUser, error)
	AssignOwnerTxFn              func(ctx context.Context, in owner.AssignOwnerTxInput) (owner.AssignOwnerResult, error)
	GetTransferTargetUserFn      func(ctx context.Context, userID [16]byte) (owner.TransferTargetUser, error)
	HasPendingTransferTokenFn    func(ctx context.Context) (bool, error)
	InsertTransferTokenFn        func(ctx context.Context, targetUserID [16]byte, targetEmail, codeHash, initiatedBy string) ([16]byte, time.Time, error)
	GetPendingTransferTokenFn    func(ctx context.Context) (owner.PendingTransferInfo, error)
	DeletePendingTransferTokenFn func(ctx context.Context, initiatedBy string) error
	WriteInitiateAuditLogFn      func(ctx context.Context, actingOwnerID [16]byte, targetUserID, ipAddress, userAgent string) error
	WriteCancelAuditLogFn        func(ctx context.Context, actingOwnerID [16]byte, ipAddress, userAgent string) error
	AcceptTransferTxFn           func(ctx context.Context, in owner.AcceptTransferTxInput) (time.Time, error)
}

var _ owner.Storer = (*OwnerFakeStorer)(nil)

func (f *OwnerFakeStorer) CountActiveOwners(ctx context.Context) (int64, error) {
	if f.CountActiveOwnersFn != nil {
		return f.CountActiveOwnersFn(ctx)
	}
	return 0, nil
}

func (f *OwnerFakeStorer) GetOwnerRoleID(ctx context.Context) ([16]byte, error) {
	if f.GetOwnerRoleIDFn != nil {
		return f.GetOwnerRoleIDFn(ctx)
	}
	return [16]byte{}, nil
}

func (f *OwnerFakeStorer) GetActiveUserByID(ctx context.Context, userID [16]byte) (owner.AssignOwnerUser, error) {
	if f.GetActiveUserByIDFn != nil {
		return f.GetActiveUserByIDFn(ctx, userID)
	}
	return owner.AssignOwnerUser{IsActive: true, EmailVerified: true}, nil
}

func (f *OwnerFakeStorer) AssignOwnerTx(ctx context.Context, in owner.AssignOwnerTxInput) (owner.AssignOwnerResult, error) {
	if f.AssignOwnerTxFn != nil {
		return f.AssignOwnerTxFn(ctx, in)
	}
	return owner.AssignOwnerResult{}, nil
}

func (f *OwnerFakeStorer) GetTransferTargetUser(ctx context.Context, userID [16]byte) (owner.TransferTargetUser, error) {
	if f.GetTransferTargetUserFn != nil {
		return f.GetTransferTargetUserFn(ctx, userID)
	}
	return owner.TransferTargetUser{IsActive: true, EmailVerified: true, IsOwner: false}, nil
}

func (f *OwnerFakeStorer) HasPendingTransferToken(ctx context.Context) (bool, error) {
	if f.HasPendingTransferTokenFn != nil {
		return f.HasPendingTransferTokenFn(ctx)
	}
	return false, nil
}

func (f *OwnerFakeStorer) InsertTransferToken(ctx context.Context, targetUserID [16]byte, targetEmail, codeHash, initiatedBy string) ([16]byte, time.Time, error) {
	if f.InsertTransferTokenFn != nil {
		return f.InsertTransferTokenFn(ctx, targetUserID, targetEmail, codeHash, initiatedBy)
	}
	return [16]byte{}, time.Now(), nil
}

func (f *OwnerFakeStorer) GetPendingTransferToken(ctx context.Context) (owner.PendingTransferInfo, error) {
	if f.GetPendingTransferTokenFn != nil {
		return f.GetPendingTransferTokenFn(ctx)
	}
	return owner.PendingTransferInfo{}, nil
}

func (f *OwnerFakeStorer) DeletePendingTransferToken(ctx context.Context, initiatedBy string) error {
	if f.DeletePendingTransferTokenFn != nil {
		return f.DeletePendingTransferTokenFn(ctx, initiatedBy)
	}
	return nil
}

func (f *OwnerFakeStorer) WriteInitiateAuditLog(ctx context.Context, actingOwnerID [16]byte, targetUserID, ipAddress, userAgent string) error {
	if f.WriteInitiateAuditLogFn != nil {
		return f.WriteInitiateAuditLogFn(ctx, actingOwnerID, targetUserID, ipAddress, userAgent)
	}
	return nil
}

func (f *OwnerFakeStorer) WriteCancelAuditLog(ctx context.Context, actingOwnerID [16]byte, ipAddress, userAgent string) error {
	if f.WriteCancelAuditLogFn != nil {
		return f.WriteCancelAuditLogFn(ctx, actingOwnerID, ipAddress, userAgent)
	}
	return nil
}

func (f *OwnerFakeStorer) AcceptTransferTx(ctx context.Context, in owner.AcceptTransferTxInput) (time.Time, error) {
	if f.AcceptTransferTxFn != nil {
		return f.AcceptTransferTxFn(ctx, in)
	}
	return time.Now(), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PermissionsFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

type PermissionsFakeStorer struct {
	GetPermissionsFn      func(ctx context.Context) ([]permissions.Permission, error)
	GetPermissionGroupsFn func(ctx context.Context) ([]permissions.PermissionGroup, error)
}

var _ permissions.Storer = (*PermissionsFakeStorer)(nil)

func (f *PermissionsFakeStorer) GetPermissions(ctx context.Context) ([]permissions.Permission, error) {
	if f.GetPermissionsFn != nil {
		return f.GetPermissionsFn(ctx)
	}
	return []permissions.Permission{}, nil
}

func (f *PermissionsFakeStorer) GetPermissionGroups(ctx context.Context) ([]permissions.PermissionGroup, error) {
	if f.GetPermissionGroupsFn != nil {
		return f.GetPermissionGroupsFn(ctx)
	}
	return []permissions.PermissionGroup{}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// RolesFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

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

var _ roles.Storer = (*RolesFakeStorer)(nil)

func (f *RolesFakeStorer) GetRoles(ctx context.Context) ([]roles.Role, error) {
	if f.GetRolesFn != nil {
		return f.GetRolesFn(ctx)
	}
	return []roles.Role{}, nil
}

func (f *RolesFakeStorer) GetRoleByID(ctx context.Context, roleID [16]byte) (roles.Role, error) {
	if f.GetRoleByIDFn != nil {
		return f.GetRoleByIDFn(ctx, roleID)
	}
	return roles.Role{}, nil
}

func (f *RolesFakeStorer) CreateRole(ctx context.Context, in roles.CreateRoleInput) (roles.Role, error) {
	if f.CreateRoleFn != nil {
		return f.CreateRoleFn(ctx, in)
	}
	return roles.Role{}, nil
}

func (f *RolesFakeStorer) UpdateRole(ctx context.Context, roleID [16]byte, in roles.UpdateRoleInput) (roles.Role, error) {
	if f.UpdateRoleFn != nil {
		return f.UpdateRoleFn(ctx, roleID, in)
	}
	return roles.Role{}, nil
}

func (f *RolesFakeStorer) DeactivateRole(ctx context.Context, roleID [16]byte) error {
	if f.DeactivateRoleFn != nil {
		return f.DeactivateRoleFn(ctx, roleID)
	}
	return nil
}

func (f *RolesFakeStorer) GetRolePermissions(ctx context.Context, roleID [16]byte) ([]roles.RolePermission, error) {
	if f.GetRolePermissionsFn != nil {
		return f.GetRolePermissionsFn(ctx, roleID)
	}
	return []roles.RolePermission{}, nil
}

func (f *RolesFakeStorer) AddRolePermission(ctx context.Context, roleID [16]byte, in roles.AddRolePermissionInput) error {
	if f.AddRolePermissionFn != nil {
		return f.AddRolePermissionFn(ctx, roleID, in)
	}
	return nil
}

func (f *RolesFakeStorer) RemoveRolePermission(ctx context.Context, roleID, permID [16]byte, actingUserID string) error {
	if f.RemoveRolePermissionFn != nil {
		return f.RemoveRolePermissionFn(ctx, roleID, permID, actingUserID)
	}
	return nil
}

func (f *RolesFakeStorer) GetPermissionCaps(ctx context.Context, permissionID [16]byte) (roles.PermissionCaps, error) {
	if f.GetPermissionCapsFn != nil {
		return f.GetPermissionCapsFn(ctx, permissionID)
	}
	return roles.PermissionCaps{}, nil
}
