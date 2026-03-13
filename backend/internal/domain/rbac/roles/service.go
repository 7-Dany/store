package roles

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// Storer is the data-access contract for the roles service.
// Defined here per ADR-007 — the service owns its dependencies.
type Storer interface {
	GetRoles(ctx context.Context) ([]Role, error)
	GetRoleByID(ctx context.Context, roleID [16]byte) (Role, error)
	CreateRole(ctx context.Context, in CreateRoleInput) (Role, error)
	UpdateRole(ctx context.Context, roleID [16]byte, in UpdateRoleInput) (Role, error)
	DeactivateRole(ctx context.Context, roleID [16]byte) error
	GetRolePermissions(ctx context.Context, roleID [16]byte) ([]RolePermission, error)
	AddRolePermission(ctx context.Context, roleID [16]byte, in AddRolePermissionInput) error
	RemoveRolePermission(ctx context.Context, roleID, permID [16]byte, actingUserID string) error
	// GetPermissionCaps returns the capability flags for a permission by ID.
	// Used by AddRolePermission to validate access_type and scope before inserting.
	// Returns ErrPermissionNotFound when the permission does not exist or is inactive.
	GetPermissionCaps(ctx context.Context, permissionID [16]byte) (PermissionCaps, error)
}

// Service implements Servicer for the roles package.
type Service struct {
	store Storer
}

// NewService constructs a Service with the given store.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// ListRoles returns all active roles.
func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	roles, err := s.store.GetRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("roles.ListRoles: %w", err)
	}
	return roles, nil
}

// GetRole returns a single role by ID string.
// Returns ErrRoleNotFound for invalid or unknown UUIDs.
func (s *Service) GetRole(ctx context.Context, roleID string) (Role, error) {
	id, err := parseID(roleID)
	if err != nil {
		return Role{}, ErrRoleNotFound
	}
	role, err := s.store.GetRoleByID(ctx, id)
	if err != nil {
		return Role{}, fmt.Errorf("roles.GetRole: %w", err)
	}
	return role, nil
}

// CreateRole inserts a new non-system role.
func (s *Service) CreateRole(ctx context.Context, in CreateRoleInput) (Role, error) {
	role, err := s.store.CreateRole(ctx, in)
	if err != nil {
		return Role{}, fmt.Errorf("roles.CreateRole: %w", err)
	}
	return role, nil
}

// UpdateRole patches name/description of a non-system role.
// Returns ErrRoleNotFound for invalid or unknown UUIDs; propagates rbac.ErrSystemRoleImmutable.
func (s *Service) UpdateRole(ctx context.Context, roleID string, in UpdateRoleInput) (Role, error) {
	id, err := parseID(roleID)
	if err != nil {
		return Role{}, ErrRoleNotFound
	}
	role, err := s.store.UpdateRole(ctx, id, in)
	if err != nil {
		return Role{}, fmt.Errorf("roles.UpdateRole: %w", err)
	}
	return role, nil
}

// DeleteRole soft-deletes a non-system role.
// Returns ErrRoleNotFound for invalid or unknown UUIDs; propagates rbac.ErrSystemRoleImmutable.
func (s *Service) DeleteRole(ctx context.Context, roleID string) error {
	id, err := parseID(roleID)
	if err != nil {
		return ErrRoleNotFound
	}
	if err := s.store.DeactivateRole(ctx, id); err != nil {
		return fmt.Errorf("roles.DeleteRole: %w", err)
	}
	return nil
}

// ListRolePermissions returns the permissions assigned to a role.
// Returns ErrRoleNotFound for invalid UUIDs or when the role does not exist.
func (s *Service) ListRolePermissions(ctx context.Context, roleID string) ([]RolePermission, error) {
	id, err := parseID(roleID)
	if err != nil {
		return nil, ErrRoleNotFound
	}
	// Verify the role exists before querying its permissions so that a valid
	// UUID referencing a non-existent role returns 404 instead of an empty list.
	if _, err := s.store.GetRoleByID(ctx, id); err != nil {
		return nil, fmt.Errorf("roles.ListRolePermissions: %w", err)
	}
	perms, err := s.store.GetRolePermissions(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("roles.ListRolePermissions: %w", err)
	}
	return perms, nil
}

// AddRolePermission adds a permission grant to a role.
// Returns ErrRoleNotFound for invalid role UUID strings.
// Validates access_type and scope against the permission's capability flags.
// Applies a "{}" default to Conditions when the caller omits them.
func (s *Service) AddRolePermission(ctx context.Context, roleID string, in AddRolePermissionInput) error {
	rid, err := parseID(roleID)
	if err != nil {
		return ErrRoleNotFound
	}

	// Fetch capability flags — validates the permission exists and is active.
	caps, err := s.store.GetPermissionCaps(ctx, in.PermissionID)
	if err != nil {
		return fmt.Errorf("roles.AddRolePermission: %w", err)
	}

	// Validate access_type against capability flags.
	// 'direct' and 'denied' are always allowed — no flag needed.
	switch in.AccessType {
	case "conditional":
		if !caps.AllowConditional {
			return ErrAccessTypeNotAllowed
		}
	case "request":
		if !caps.AllowRequest {
			return ErrAccessTypeNotAllowed
		}
	}

	// Validate and normalise scope against scope_policy.
	switch caps.ScopePolicy {
	case "none":
		// Scope is not meaningful — silently normalise to 'all' so the DB
		// holds a consistent value rather than whatever the caller passed in.
		in.Scope = "all"
	case "own":
		if in.Scope != "own" {
			return ErrScopeNotAllowed
		}
	case "all":
		if in.Scope != "all" {
			return ErrScopeNotAllowed
		}
	case "any":
		// Both 'own' and 'all' are valid — pass through.
	}

	// Apply the conditions default once, at the service boundary.
	if len(in.Conditions) == 0 {
		in.Conditions = json.RawMessage("{}")
	}

	if err := s.store.AddRolePermission(ctx, rid, in); err != nil {
		return fmt.Errorf("roles.AddRolePermission: %w", err)
	}
	return nil
}

// RemoveRolePermission removes a permission grant from a role.
// actingUserID is forwarded to the store so the audit trigger records the
// correct deletion actor. Returns ErrRolePermissionNotFound for invalid UUID
// strings or missing grants.
func (s *Service) RemoveRolePermission(ctx context.Context, roleID, permID, actingUserID string) error {
	rid, err := parseID(roleID)
	if err != nil {
		return ErrRolePermissionNotFound
	}
	pid, err := parseID(permID)
	if err != nil {
		return ErrRolePermissionNotFound
	}
	if err := s.store.RemoveRolePermission(ctx, rid, pid, actingUserID); err != nil {
		return fmt.Errorf("roles.RemoveRolePermission: %w", err)
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// parseID parses a UUID string into a [16]byte.
func parseID(s string) ([16]byte, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return [16]byte{}, err
	}
	return [16]byte(id), nil
}
