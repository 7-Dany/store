package rbacsharedtest

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

// QuerierProxy wraps db.Querier with per-method failure injection for the rbac
// domain. The embedded db.Querier (accessed as proxy.Querier) auto-forwards any
// method not explicitly overridden below; set it via NewQuerierProxy or directly
// in tests with proxy.Querier = q.
//
// Fail* flags are grouped by feature. Add new flags when a store test needs
// to inject a failure for a specific query.
type QuerierProxy struct {
	db.Querier // embedded — auto-forwards any method not explicitly overridden below

	// ── bootstrap ────────────────────────────────────────────────────────────
	FailCountActiveOwners bool
	FailGetOwnerRoleID    bool
	FailGetActiveUserByID bool
	FailAssignUserRole    bool
	FailInsertAuditLog    bool

	// ── permissions ───────────────────────────────────────────────────────────
	FailGetPermissions            bool
	FailGetPermissionGroups       bool
	FailGetPermissionGroupMembers bool

	// ── user roles ────────────────────────────────────────────────────────────
	FailGetUserRole    bool
	FailRemoveUserRole bool

	// ── user permissions ──────────────────────────────────────────────────────
	FailGetPermissionByID    bool // used by GrantPermissionTx step 1
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
