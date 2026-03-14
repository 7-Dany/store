// Package rbacsharedtest provides test-only helpers shared across all rbac
// feature sub-packages. It must never be imported by production code.
package rbacsharedtest

import (
	"context"
	"time"

	"github.com/7-Dany/store/backend/internal/domain/rbac/owner"
	"github.com/7-Dany/store/backend/internal/domain/rbac/permissions"
	"github.com/7-Dany/store/backend/internal/domain/rbac/roles"
	"github.com/7-Dany/store/backend/internal/domain/rbac/userlock"
	"github.com/7-Dany/store/backend/internal/domain/rbac/userpermissions"
	"github.com/7-Dany/store/backend/internal/domain/rbac/userroles"
)

// ─────────────────────────────────────────────────────────────────────────────
// OwnerFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// OwnerFakeStorer is a hand-written implementation of owner.Storer for
// service unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns a safe default so tests only configure the fields they need.
//
// Defaults are chosen so that the assign-owner and transfer happy paths succeed
// without any configuration:
//   - CountActiveOwners        → (0, nil):   no owner exists, service proceeds.
//   - GetOwnerRoleID           → ([16]byte{}, nil): zero UUID, sufficient for unit tests.
//   - GetActiveUserByID        → (AssignOwnerUser{IsActive: true, EmailVerified: true}, nil)
//   - AssignOwnerTx            → (AssignOwnerResult{}, nil)
//   - GetTransferTargetUser    → (TransferTargetUser{IsActive: true, EmailVerified: true}, nil)
//   - HasPendingTransferToken  → (false, nil): no pending transfer, may proceed.
//   - InsertTransferToken      → ([16]byte{}, time.Now(), nil)
//   - GetPendingTransferToken  → (PendingTransferInfo{}, nil)
//   - DeletePendingTransferToken → nil
//   - WriteInitiateAuditLog   → nil  (non-fatal in production; safe to no-op in tests)
//   - WriteCancelAuditLog     → nil  (non-fatal in production; safe to no-op in tests)
//   - AcceptTransferTx        → (time.Now(), nil)
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

// compile-time interface check.
var _ owner.Storer = (*OwnerFakeStorer)(nil)

// CountActiveOwners delegates to CountActiveOwnersFn if set.
// Default: returns (0, nil) — no active owner, service proceeds.
func (f *OwnerFakeStorer) CountActiveOwners(ctx context.Context) (int64, error) {
	if f.CountActiveOwnersFn != nil {
		return f.CountActiveOwnersFn(ctx)
	}
	return 0, nil
}

// GetOwnerRoleID delegates to GetOwnerRoleIDFn if set.
// Default: returns a zero [16]byte and nil error.
func (f *OwnerFakeStorer) GetOwnerRoleID(ctx context.Context) ([16]byte, error) {
	if f.GetOwnerRoleIDFn != nil {
		return f.GetOwnerRoleIDFn(ctx)
	}
	return [16]byte{}, nil
}

// GetActiveUserByID delegates to GetActiveUserByIDFn if set.
// Default: returns a fully-active, email-verified AssignOwnerUser so tests that
// do not configure this field never trip the is_active or email_verified guard.
func (f *OwnerFakeStorer) GetActiveUserByID(ctx context.Context, userID [16]byte) (owner.AssignOwnerUser, error) {
	if f.GetActiveUserByIDFn != nil {
		return f.GetActiveUserByIDFn(ctx, userID)
	}
	return owner.AssignOwnerUser{IsActive: true, EmailVerified: true}, nil
}

// AssignOwnerTx delegates to AssignOwnerTxFn if set.
// Default: returns a zero AssignOwnerResult and nil error.
func (f *OwnerFakeStorer) AssignOwnerTx(ctx context.Context, in owner.AssignOwnerTxInput) (owner.AssignOwnerResult, error) {
	if f.AssignOwnerTxFn != nil {
		return f.AssignOwnerTxFn(ctx, in)
	}
	return owner.AssignOwnerResult{}, nil
}

// GetTransferTargetUser delegates to GetTransferTargetUserFn if set.
// Default: returns a fully-active, email-verified, non-owner TransferTargetUser.
func (f *OwnerFakeStorer) GetTransferTargetUser(ctx context.Context, userID [16]byte) (owner.TransferTargetUser, error) {
	if f.GetTransferTargetUserFn != nil {
		return f.GetTransferTargetUserFn(ctx, userID)
	}
	return owner.TransferTargetUser{IsActive: true, EmailVerified: true, IsOwner: false}, nil
}

// HasPendingTransferToken delegates to HasPendingTransferTokenFn if set.
// Default: returns (false, nil) — no pending transfer, service may proceed.
func (f *OwnerFakeStorer) HasPendingTransferToken(ctx context.Context) (bool, error) {
	if f.HasPendingTransferTokenFn != nil {
		return f.HasPendingTransferTokenFn(ctx)
	}
	return false, nil
}

// InsertTransferToken delegates to InsertTransferTokenFn if set.
// Default: returns a zero token ID, a non-zero time.Now(), and nil error.
func (f *OwnerFakeStorer) InsertTransferToken(ctx context.Context, targetUserID [16]byte, targetEmail, codeHash, initiatedBy string) ([16]byte, time.Time, error) {
	if f.InsertTransferTokenFn != nil {
		return f.InsertTransferTokenFn(ctx, targetUserID, targetEmail, codeHash, initiatedBy)
	}
	return [16]byte{}, time.Now(), nil
}

// GetPendingTransferToken delegates to GetPendingTransferTokenFn if set.
// Default: returns a zero PendingTransferInfo and nil error.
func (f *OwnerFakeStorer) GetPendingTransferToken(ctx context.Context) (owner.PendingTransferInfo, error) {
	if f.GetPendingTransferTokenFn != nil {
		return f.GetPendingTransferTokenFn(ctx)
	}
	return owner.PendingTransferInfo{}, nil
}

// DeletePendingTransferToken delegates to DeletePendingTransferTokenFn if set.
// Default: returns nil.
func (f *OwnerFakeStorer) DeletePendingTransferToken(ctx context.Context, initiatedBy string) error {
	if f.DeletePendingTransferTokenFn != nil {
		return f.DeletePendingTransferTokenFn(ctx, initiatedBy)
	}
	return nil
}

// WriteInitiateAuditLog delegates to WriteInitiateAuditLogFn if set.
// Default: returns nil (non-fatal in production; safe to no-op in tests).
func (f *OwnerFakeStorer) WriteInitiateAuditLog(ctx context.Context, actingOwnerID [16]byte, targetUserID, ipAddress, userAgent string) error {
	if f.WriteInitiateAuditLogFn != nil {
		return f.WriteInitiateAuditLogFn(ctx, actingOwnerID, targetUserID, ipAddress, userAgent)
	}
	return nil
}

// WriteCancelAuditLog delegates to WriteCancelAuditLogFn if set.
// Default: returns nil (non-fatal in production; safe to no-op in tests).
func (f *OwnerFakeStorer) WriteCancelAuditLog(ctx context.Context, actingOwnerID [16]byte, ipAddress, userAgent string) error {
	if f.WriteCancelAuditLogFn != nil {
		return f.WriteCancelAuditLogFn(ctx, actingOwnerID, ipAddress, userAgent)
	}
	return nil
}

// AcceptTransferTx delegates to AcceptTransferTxFn if set.
// Default: returns time.Now() and nil error.
func (f *OwnerFakeStorer) AcceptTransferTx(ctx context.Context, in owner.AcceptTransferTxInput) (time.Time, error) {
	if f.AcceptTransferTxFn != nil {
		return f.AcceptTransferTxFn(ctx, in)
	}
	return time.Now(), nil
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

// ─────────────────────────────────────────────────────────────────────────────
// UserLockFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// UserLockFakeStorer is a hand-written implementation of userlock.Storer
// for service unit tests.
//
// Defaults:
//
//	IsOwnerUserFn   → (false, nil)  — non-owner; lock guards pass
//	GetLockStatusFn → (UserLockStatus{}, nil)
//	LockUserTxFn    → nil
//	UnlockUserFn    → nil
type UserLockFakeStorer struct {
	IsOwnerUserFn   func(ctx context.Context, userID [16]byte) (bool, error)
	GetLockStatusFn func(ctx context.Context, userID [16]byte) (userlock.UserLockStatus, error)
	LockUserTxFn    func(ctx context.Context, in userlock.LockUserTxInput) error
	UnlockUserTxFn  func(ctx context.Context, userID [16]byte, actingUserID string) error
}

// compile-time interface check.
var _ userlock.Storer = (*UserLockFakeStorer)(nil)

// IsOwnerUser delegates to IsOwnerUserFn if set.
// Default: returns (false, nil) — non-owner.
func (f *UserLockFakeStorer) IsOwnerUser(ctx context.Context, userID [16]byte) (bool, error) {
	if f.IsOwnerUserFn != nil {
		return f.IsOwnerUserFn(ctx, userID)
	}
	return false, nil
}

// GetLockStatus delegates to GetLockStatusFn if set.
// Default: returns (UserLockStatus{}, nil).
func (f *UserLockFakeStorer) GetLockStatus(ctx context.Context, userID [16]byte) (userlock.UserLockStatus, error) {
	if f.GetLockStatusFn != nil {
		return f.GetLockStatusFn(ctx, userID)
	}
	return userlock.UserLockStatus{}, nil
}

// LockUserTx delegates to LockUserTxFn if set.
// Default: returns nil.
func (f *UserLockFakeStorer) LockUserTx(ctx context.Context, in userlock.LockUserTxInput) error {
	if f.LockUserTxFn != nil {
		return f.LockUserTxFn(ctx, in)
	}
	return nil
}

// UnlockUserTx delegates to UnlockUserTxFn if set.
// Default: returns nil.
func (f *UserLockFakeStorer) UnlockUserTx(ctx context.Context, userID [16]byte, actingUserID string) error {
	if f.UnlockUserTxFn != nil {
		return f.UnlockUserTxFn(ctx, userID, actingUserID)
	}
	return nil
}
