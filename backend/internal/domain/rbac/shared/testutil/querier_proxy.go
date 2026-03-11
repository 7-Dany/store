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
