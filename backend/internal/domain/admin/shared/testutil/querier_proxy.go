package adminsharedtest

import (
	"context"
	"errors"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrProxy is the sentinel error returned by any QuerierProxy when a Fail* flag is set.
var ErrProxy = errors.New("querier_proxy: injected error")

// compile-time check that *QuerierProxy satisfies db.Querier.
var _ db.Querier = (*QuerierProxy)(nil)

// QuerierProxy wraps db.Querier with per-method failure injection for the admin
// domain. Set Fail* flags to force a specific method to return ErrProxy.
type QuerierProxy struct {
	db.Querier // embedded — auto-forwards any method not explicitly overridden below

	// ── owner / bootstrap ────────────────────────────────────────────────────
	FailCountActiveOwners bool
	FailGetOwnerRoleID    bool
	FailGetActiveUserByID bool
	FailAssignUserRole    bool
	FailInsertAuditLog    bool

	// ── owner transfer ────────────────────────────────────────────────────────
	FailGetPendingOwnershipTransferToken    bool
	FailInsertOwnershipTransferToken        bool
	FailDeletePendingOwnershipTransferToken bool
	FailConsumeOwnershipTransferToken       bool
	FailCheckUserAccess                     bool
	FailSetSkipEscalationCheck              bool
	FailRevokeAllUserRefreshTokens          bool

	// ── permissions ───────────────────────────────────────────────────────────
	FailGetPermissions            bool
	FailGetPermissionGroups       bool
	FailGetPermissionGroupMembers bool

	// ── user roles ────────────────────────────────────────────────────────────
	FailGetUserRole    bool
	FailRemoveUserRole bool

	// ── user permissions ──────────────────────────────────────────────────────
	FailGetPermissionByID    bool
	FailGetUserPermissions   bool
	FailGrantUserPermission  bool
	FailRevokeUserPermission bool

	// ── roles ─────────────────────────────────────────────────────────────────
	FailGetRoles             bool
	FailGetRoleByID          bool
	FailGetRoleByName        bool
	FailCreateRole           bool
	FailUpdateRole           bool
	FailDeactivateRole       bool
	FailGetRolePermissions   bool
	FailAddRolePermission    bool
	FailRemoveRolePermission bool

	// ── user lock ─────────────────────────────────────────────────────────────
	FailLockUser          bool
	FailUnlockUser        bool
	FailGetUserLockStatus bool
}

// NewQuerierProxy constructs a QuerierProxy backed by base.
func NewQuerierProxy(base db.Querier) *QuerierProxy {
	return &QuerierProxy{Querier: base}
}

func (p *QuerierProxy) CountActiveOwners(ctx context.Context) (int64, error) {
	if p.FailCountActiveOwners {
		return 0, ErrProxy
	}
	return p.Querier.CountActiveOwners(ctx)
}

func (p *QuerierProxy) GetOwnerRoleID(ctx context.Context) (uuid.UUID, error) {
	if p.FailGetOwnerRoleID {
		return uuid.UUID{}, ErrProxy
	}
	return p.Querier.GetOwnerRoleID(ctx)
}

func (p *QuerierProxy) GetActiveUserByID(ctx context.Context, userID pgtype.UUID) (db.GetActiveUserByIDRow, error) {
	if p.FailGetActiveUserByID {
		return db.GetActiveUserByIDRow{}, ErrProxy
	}
	return p.Querier.GetActiveUserByID(ctx, userID)
}

func (p *QuerierProxy) AssignUserRole(ctx context.Context, arg db.AssignUserRoleParams) (db.AssignUserRoleRow, error) {
	if p.FailAssignUserRole {
		return db.AssignUserRoleRow{}, ErrProxy
	}
	return p.Querier.AssignUserRole(ctx, arg)
}

func (p *QuerierProxy) InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) error {
	if p.FailInsertAuditLog {
		return ErrProxy
	}
	return p.Querier.InsertAuditLog(ctx, arg)
}

func (p *QuerierProxy) GetPendingOwnershipTransferToken(ctx context.Context) (db.GetPendingOwnershipTransferTokenRow, error) {
	if p.FailGetPendingOwnershipTransferToken {
		return db.GetPendingOwnershipTransferTokenRow{}, ErrProxy
	}
	return p.Querier.GetPendingOwnershipTransferToken(ctx)
}

func (p *QuerierProxy) InsertOwnershipTransferToken(ctx context.Context, arg db.InsertOwnershipTransferTokenParams) (db.InsertOwnershipTransferTokenRow, error) {
	if p.FailInsertOwnershipTransferToken {
		return db.InsertOwnershipTransferTokenRow{}, ErrProxy
	}
	return p.Querier.InsertOwnershipTransferToken(ctx, arg)
}

func (p *QuerierProxy) DeletePendingOwnershipTransferToken(ctx context.Context, initiatedBy string) (int64, error) {
	if p.FailDeletePendingOwnershipTransferToken {
		return 0, ErrProxy
	}
	return p.Querier.DeletePendingOwnershipTransferToken(ctx, initiatedBy)
}

func (p *QuerierProxy) ConsumeOwnershipTransferToken(ctx context.Context, id pgtype.UUID) (int64, error) {
	if p.FailConsumeOwnershipTransferToken {
		return 0, ErrProxy
	}
	return p.Querier.ConsumeOwnershipTransferToken(ctx, id)
}

func (p *QuerierProxy) CheckUserAccess(ctx context.Context, arg db.CheckUserAccessParams) (db.CheckUserAccessRow, error) {
	if p.FailCheckUserAccess {
		return db.CheckUserAccessRow{}, ErrProxy
	}
	return p.Querier.CheckUserAccess(ctx, arg)
}

func (p *QuerierProxy) SetSkipEscalationCheck(ctx context.Context) error {
	if p.FailSetSkipEscalationCheck {
		return ErrProxy
	}
	return p.Querier.SetSkipEscalationCheck(ctx)
}

func (p *QuerierProxy) RevokeAllUserRefreshTokens(ctx context.Context, arg db.RevokeAllUserRefreshTokensParams) error {
	if p.FailRevokeAllUserRefreshTokens {
		return ErrProxy
	}
	return p.Querier.RevokeAllUserRefreshTokens(ctx, arg)
}

func (p *QuerierProxy) GetPermissions(ctx context.Context) ([]db.GetPermissionsRow, error) {
	if p.FailGetPermissions {
		return nil, ErrProxy
	}
	return p.Querier.GetPermissions(ctx)
}

func (p *QuerierProxy) GetPermissionGroups(ctx context.Context) ([]db.GetPermissionGroupsRow, error) {
	if p.FailGetPermissionGroups {
		return nil, ErrProxy
	}
	return p.Querier.GetPermissionGroups(ctx)
}

func (p *QuerierProxy) GetPermissionGroupMembers(ctx context.Context, groupID pgtype.UUID) ([]db.GetPermissionGroupMembersRow, error) {
	if p.FailGetPermissionGroupMembers {
		return nil, ErrProxy
	}
	return p.Querier.GetPermissionGroupMembers(ctx, groupID)
}

func (p *QuerierProxy) GetPermissionByID(ctx context.Context, id pgtype.UUID) (db.GetPermissionByIDRow, error) {
	if p.FailGetPermissionByID {
		return db.GetPermissionByIDRow{}, ErrProxy
	}
	return p.Querier.GetPermissionByID(ctx, id)
}

func (p *QuerierProxy) GetRoles(ctx context.Context) ([]db.Role, error) {
	if p.FailGetRoles {
		return nil, ErrProxy
	}
	return p.Querier.GetRoles(ctx)
}

func (p *QuerierProxy) GetRoleByID(ctx context.Context, id pgtype.UUID) (db.Role, error) {
	if p.FailGetRoleByID {
		return db.Role{}, ErrProxy
	}
	return p.Querier.GetRoleByID(ctx, id)
}

func (p *QuerierProxy) GetRoleByName(ctx context.Context, name string) (db.Role, error) {
	if p.FailGetRoleByName {
		return db.Role{}, ErrProxy
	}
	return p.Querier.GetRoleByName(ctx, name)
}

func (p *QuerierProxy) CreateRole(ctx context.Context, arg db.CreateRoleParams) (db.Role, error) {
	if p.FailCreateRole {
		return db.Role{}, ErrProxy
	}
	return p.Querier.CreateRole(ctx, arg)
}

func (p *QuerierProxy) UpdateRole(ctx context.Context, arg db.UpdateRoleParams) (db.Role, error) {
	if p.FailUpdateRole {
		return db.Role{}, ErrProxy
	}
	return p.Querier.UpdateRole(ctx, arg)
}

func (p *QuerierProxy) DeactivateRole(ctx context.Context, id pgtype.UUID) (int64, error) {
	if p.FailDeactivateRole {
		return 0, ErrProxy
	}
	return p.Querier.DeactivateRole(ctx, id)
}

func (p *QuerierProxy) GetRolePermissions(ctx context.Context, roleID pgtype.UUID) ([]db.GetRolePermissionsRow, error) {
	if p.FailGetRolePermissions {
		return nil, ErrProxy
	}
	return p.Querier.GetRolePermissions(ctx, roleID)
}

func (p *QuerierProxy) AddRolePermission(ctx context.Context, arg db.AddRolePermissionParams) (int64, error) {
	if p.FailAddRolePermission {
		return 0, ErrProxy
	}
	return p.Querier.AddRolePermission(ctx, arg)
}

func (p *QuerierProxy) RemoveRolePermission(ctx context.Context, arg db.RemoveRolePermissionParams) (int64, error) {
	if p.FailRemoveRolePermission {
		return 0, ErrProxy
	}
	return p.Querier.RemoveRolePermission(ctx, arg)
}

func (p *QuerierProxy) GetUserRole(ctx context.Context, userID pgtype.UUID) (db.GetUserRoleRow, error) {
	if p.FailGetUserRole {
		return db.GetUserRoleRow{}, ErrProxy
	}
	return p.Querier.GetUserRole(ctx, userID)
}

func (p *QuerierProxy) RemoveUserRole(ctx context.Context, userID pgtype.UUID) (int64, error) {
	if p.FailRemoveUserRole {
		return 0, ErrProxy
	}
	return p.Querier.RemoveUserRole(ctx, userID)
}

func (p *QuerierProxy) GetUserPermissions(ctx context.Context, userID pgtype.UUID) ([]db.GetUserPermissionsRow, error) {
	if p.FailGetUserPermissions {
		return nil, ErrProxy
	}
	return p.Querier.GetUserPermissions(ctx, userID)
}

func (p *QuerierProxy) GrantUserPermission(ctx context.Context, arg db.GrantUserPermissionParams) (db.GrantUserPermissionRow, error) {
	if p.FailGrantUserPermission {
		return db.GrantUserPermissionRow{}, ErrProxy
	}
	return p.Querier.GrantUserPermission(ctx, arg)
}

func (p *QuerierProxy) RevokeUserPermission(ctx context.Context, arg db.RevokeUserPermissionParams) (int64, error) {
	if p.FailRevokeUserPermission {
		return 0, ErrProxy
	}
	return p.Querier.RevokeUserPermission(ctx, arg)
}

func (p *QuerierProxy) SetActingUser(ctx context.Context, userID string) error {
	return p.Querier.SetActingUser(ctx, userID)
}

func (p *QuerierProxy) LockUser(ctx context.Context, arg db.LockUserParams) error {
	if p.FailLockUser {
		return ErrProxy
	}
	return p.Querier.LockUser(ctx, arg)
}

func (p *QuerierProxy) UnlockUser(ctx context.Context, userID pgtype.UUID) error {
	if p.FailUnlockUser {
		return ErrProxy
	}
	return p.Querier.UnlockUser(ctx, userID)
}

func (p *QuerierProxy) GetUserLockStatus(ctx context.Context, userID pgtype.UUID) (db.GetUserLockStatusRow, error) {
	if p.FailGetUserLockStatus {
		return db.GetUserLockStatusRow{}, ErrProxy
	}
	return p.Querier.GetUserLockStatus(ctx, userID)
}
