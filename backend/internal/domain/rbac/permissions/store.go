package permissions

import (
	"context"

	"github.com/7-Dany/store/backend/internal/db"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check: *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store is the data-access implementation for the permissions package.
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

// GetPermissions returns all active RBAC permissions ordered by canonical_name.
func (s *Store) GetPermissions(ctx context.Context) ([]Permission, error) {
	rows, err := s.Queries.GetPermissions(ctx)
	if err != nil {
		return nil, telemetry.Store("GetPermissions.query", err)
	}
	perms := make([]Permission, 0, len(rows))
	for _, row := range rows {
		perms = append(perms, Permission{
			ID:            uuid.UUID(row.ID).String(),
			CanonicalName: row.CanonicalName.String,
			Name:          row.Name,
			ResourceType:  row.ResourceType,
			Description:   row.Description.String,
			Capabilities:  buildCapabilities(string(row.ScopePolicy), row.AllowConditional, row.AllowRequest),
		})
	}
	return perms, nil
}

// GetPermissionGroups returns all active permission groups with their members embedded.
// This uses an intentional N+1 pattern: at most ~30 groups in practice.
func (s *Store) GetPermissionGroups(ctx context.Context) ([]PermissionGroup, error) {
	groupRows, err := s.Queries.GetPermissionGroups(ctx)
	if err != nil {
		return nil, telemetry.Store("GetPermissionGroups.query_groups", err)
	}

	groups := make([]PermissionGroup, 0, len(groupRows))
	for _, g := range groupRows {
		memberRows, err := s.Queries.GetPermissionGroupMembers(ctx, s.UUIDToPgtypeUUID(g.ID))
		if err != nil {
			return nil, telemetry.Store("GetPermissionGroups.query_members", err)
		}

		members := make([]PermissionGroupMember, 0, len(memberRows))
		for _, m := range memberRows {
			members = append(members, PermissionGroupMember{
				ID:            uuid.UUID(m.ID).String(),
				CanonicalName: m.CanonicalName.String,
				Name:          m.Name,
				ResourceType:  m.ResourceType,
				Description:   m.Description.String,
				Capabilities:  buildCapabilities(string(m.ScopePolicy), m.AllowConditional, m.AllowRequest),
			})
		}

		groups = append(groups, PermissionGroup{
			ID:           uuid.UUID(g.ID).String(),
			Name:         g.Name,
			DisplayLabel: g.DisplayLabel.String,
			Icon:         g.Icon.String,
			ColorHex:     g.ColorHex.String,
			DisplayOrder: g.DisplayOrder,
			IsVisible:    g.IsVisible,
			Members:      members,
		})
	}
	return groups, nil
}
