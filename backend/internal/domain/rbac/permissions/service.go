package permissions

import (
	"context"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// log is the package-level structured logger for the permissions feature.
// One logger per package — shared across all files in the package.
var log = telemetry.New("permissions")

// Storer is the data-access contract for the permissions service.
type Storer interface {
	GetPermissions(ctx context.Context) ([]Permission, error)
	GetPermissionGroups(ctx context.Context) ([]PermissionGroup, error)
}

// Service implements Servicer for the permissions package.
type Service struct {
	store Storer
}

// NewService constructs a Service with the given store.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// ListPermissions returns all active RBAC permissions ordered by canonical_name.
func (s *Service) ListPermissions(ctx context.Context) ([]Permission, error) {
	perms, err := s.store.GetPermissions(ctx)
	if err != nil {
		return nil, telemetry.Service("permissions.ListPermissions: get", err)
	}
	return perms, nil
}

// ListPermissionGroups returns all active permission groups with their members embedded.
func (s *Service) ListPermissionGroups(ctx context.Context) ([]PermissionGroup, error) {
	groups, err := s.store.GetPermissionGroups(ctx)
	if err != nil {
		return nil, telemetry.Service("permissions.ListPermissionGroups: get", err)
	}
	return groups, nil
}
