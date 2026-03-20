package roles

import (
	"context"

	"github.com/7-Dany/store/backend/internal/db"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store is the data-access implementation for the roles package.
type Store struct {
	rbacshared.BaseStore
}

// NewStore constructs a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{BaseStore: rbacshared.NewBaseStore(pool)}
}

// WithQuerier returns a copy of the Store with its querier replaced by q and
// TxBound set to true. Used in integration tests to bind writes to a
// rolled-back test transaction.
func (s *Store) WithQuerier(q db.Querier) *Store {
	c := *s
	c.BaseStore = s.BaseStore.WithQuerier(q)
	return &c
}

// GetRoles returns all active roles ordered by name.
func (s *Store) GetRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.Queries.GetRoles(ctx)
	if err != nil {
		return nil, telemetry.Store("GetRoles.query", err)
	}
	roles := make([]Role, 0, len(rows))
	for _, row := range rows {
		roles = append(roles, mapDBRole(row))
	}
	return roles, nil
}

// GetRoleByID returns a single role by its primary key.
// Returns ErrRoleNotFound on no-rows.
func (s *Store) GetRoleByID(ctx context.Context, roleID [16]byte) (Role, error) {
	row, err := s.Queries.GetRoleByID(ctx, s.ToPgtypeUUID(roleID))
	if err != nil {
		if s.IsNoRows(err) {
			return Role{}, ErrRoleNotFound
		}
		return Role{}, telemetry.Store("GetRoleByID.query", err)
	}
	return mapDBRole(row), nil
}

// CreateRole inserts a new non-system role.
func (s *Store) CreateRole(ctx context.Context, in CreateRoleInput) (Role, error) {
	row, err := s.Queries.CreateRole(ctx, db.CreateRoleParams{
		Name:        in.Name,
		Description: pgtype.Text{String: in.Description, Valid: in.Description != ""},
	})
	if err != nil {
		if s.IsUniqueViolation(err, "roles_name_key") {
			return Role{}, ErrRoleNameConflict
		}
		return Role{}, telemetry.Store("CreateRole.insert", err)
	}
	return mapDBRole(row), nil
}

// UpdateRole updates name/description for a non-system role.
// Returns ErrRoleNotFound when the role does not exist, and
// rbac.ErrSystemRoleImmutable when the role is a system role.
func (s *Store) UpdateRole(ctx context.Context, roleID [16]byte, in UpdateRoleInput) (Role, error) {
	var namePgt pgtype.Text
	if in.Name != nil {
		namePgt = pgtype.Text{String: *in.Name, Valid: true}
	}
	var descPgt pgtype.Text
	if in.Description != nil {
		descPgt = pgtype.Text{String: *in.Description, Valid: true}
	}
	row, err := s.Queries.UpdateRole(ctx, db.UpdateRoleParams{
		Name:        namePgt,
		Description: descPgt,
		ID:          s.ToPgtypeUUID(roleID),
	})
	if err != nil {
		if s.IsNoRows(err) {
			// Zero rows: either the role does not exist or it is a system role.
			// Distinguish the two by checking whether the ID is present at all.
			if _, lookupErr := s.Queries.GetRoleByID(ctx, s.ToPgtypeUUID(roleID)); s.IsNoRows(lookupErr) {
				return Role{}, ErrRoleNotFound
			}
			return Role{}, rbac.ErrSystemRoleImmutable
		}
		if s.IsUniqueViolation(err, "roles_name_key") {
			return Role{}, ErrRoleNameConflict
		}
		return Role{}, telemetry.Store("UpdateRole.exec", err)
	}
	return mapDBRole(row), nil
}

// DeactivateRole soft-deletes a non-system role.
// Returns ErrRoleNotFound when the role does not exist, and
// rbac.ErrSystemRoleImmutable when the role is a system role or already inactive.
func (s *Store) DeactivateRole(ctx context.Context, roleID [16]byte) error {
	rows, err := s.Queries.DeactivateRole(ctx, s.ToPgtypeUUID(roleID))
	if err != nil {
		return telemetry.Store("DeactivateRole.exec", err)
	}
	if rows == 0 {
		// Zero rows: the role does not exist, is a system role, or is already inactive.
		// Distinguish not-found from immutable so the service can return the correct error.
		if _, lookupErr := s.Queries.GetRoleByID(ctx, s.ToPgtypeUUID(roleID)); s.IsNoRows(lookupErr) {
			return ErrRoleNotFound
		}
		return rbac.ErrSystemRoleImmutable
	}
	return nil
}

// GetRolePermissions returns active permissions for a role ordered by canonical_name.
func (s *Store) GetRolePermissions(ctx context.Context, roleID [16]byte) ([]RolePermission, error) {
	rows, err := s.Queries.GetRolePermissions(ctx, s.ToPgtypeUUID(roleID))
	if err != nil {
		return nil, telemetry.Store("GetRolePermissions.query", err)
	}
	perms := make([]RolePermission, 0, len(rows))
	for _, row := range rows {
		perms = append(perms, RolePermission{
			PermissionID:  uuid.UUID(row.ID).String(),
			CanonicalName: row.CanonicalName.String,
			Name:          row.Name,
			ResourceType:  row.ResourceType,
			AccessType:    string(row.AccessType),
			Scope:         string(row.Scope),
			Conditions:    row.Conditions,
			GrantedAt:     row.GrantedAt,
		})
	}
	return perms, nil
}

// GetPermissionCaps returns capability flags for a permission by ID.
// Returns ErrPermissionNotFound when the permission does not exist or is inactive.
func (s *Store) GetPermissionCaps(ctx context.Context, permissionID [16]byte) (PermissionCaps, error) {
	row, err := s.Queries.GetPermissionByID(ctx, s.ToPgtypeUUID(permissionID))
	if err != nil {
		if s.IsNoRows(err) {
			return PermissionCaps{}, ErrPermissionNotFound
		}
		return PermissionCaps{}, telemetry.Store("GetPermissionCaps.query", err)
	}
	return PermissionCaps{
		ID:               [16]byte(row.ID),
		CanonicalName:    row.CanonicalName.String,
		ScopePolicy:      string(row.ScopePolicy),
		AllowConditional: row.AllowConditional,
		AllowRequest:     row.AllowRequest,
	}, nil
}

// AddRolePermission inserts a role-permission grant.
// Returns ErrGrantAlreadyExists when ON CONFLICT DO NOTHING fires (0 rows affected).
// Returns ErrRoleNotFound when the role_id FK is violated and
// ErrPermissionNotFound when the permission_id FK is violated.
func (s *Store) AddRolePermission(ctx context.Context, roleID [16]byte, in AddRolePermissionInput) error {
	rows, err := s.Queries.AddRolePermission(ctx, db.AddRolePermissionParams{
		RoleID:        s.ToPgtypeUUID(roleID),
		PermissionID:  s.ToPgtypeUUID(in.PermissionID),
		GrantedBy:     s.ToPgtypeUUID(in.GrantedBy),
		GrantedReason: in.GrantedReason,
		AccessType:    db.PermissionAccessType(in.AccessType),
		Scope:         db.PermissionScope(in.Scope),
		Conditions:    in.Conditions,
	})
	if err != nil {
		if s.IsForeignKeyViolation(err, "role_permissions_permission_id_fkey") {
			return ErrPermissionNotFound
		}
		if s.IsForeignKeyViolation(err, "role_permissions_role_id_fkey") {
			return ErrRoleNotFound
		}
		return telemetry.Store("AddRolePermission.insert", err)
	}
	if rows == 0 {
		return ErrGrantAlreadyExists
	}
	return nil
}

// RemoveRolePermission hard-deletes by (role_id, permission_id).
// actingUserID is set as rbac.acting_user before the DELETE so the audit trigger
// records the correct deletion actor rather than the original granter.
// Returns ErrRolePermissionNotFound when the grant did not exist.
func (s *Store) RemoveRolePermission(ctx context.Context, roleID, permID [16]byte, actingUserID string) error {
	return s.WithActingUser(ctx, actingUserID, func() error {
		rows, err := s.Queries.RemoveRolePermission(ctx, db.RemoveRolePermissionParams{
			RoleID:       s.ToPgtypeUUID(roleID),
			PermissionID: s.ToPgtypeUUID(permID),
		})
		if err != nil {
			return telemetry.Store("RemoveRolePermission.delete", err)
		}
		if rows == 0 {
			return ErrRolePermissionNotFound
		}
		return nil
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func mapDBRole(row db.Role) Role {
	return Role{
		ID:           uuid.UUID(row.ID).String(),
		Name:         row.Name,
		Description:  row.Description.String,
		IsSystemRole: row.IsSystemRole,
		IsOwnerRole:  row.IsOwnerRole,
		IsActive:     row.IsActive,
		CreatedAt:    row.CreatedAt,
	}
}
