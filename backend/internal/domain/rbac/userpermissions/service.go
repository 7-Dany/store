package userpermissions

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Storer is the data-access contract for the userpermissions service.
type Storer interface {
	// GetUserPermissions returns all active (non-expired) direct grants for userID.
	GetUserPermissions(ctx context.Context, userID [16]byte) ([]UserPermission, error)

	// GrantPermissionTx validates caps, handles re-grant idempotency, and inserts.
	// Returns ErrPermissionNotFound when permissionID is unknown/inactive.
	// Returns ErrPermissionAlreadyGranted when an active (non-expired) grant exists.
	// Returns ErrPrivilegeEscalation when the granter lacks the permission.
	// Returns ErrScopeNotAllowed when scope violates scope_policy.
	GrantPermissionTx(ctx context.Context, in GrantPermissionTxInput) (UserPermission, error)

	// RevokePermission hard-deletes the grant identified by (grantID, userID).
	// Returns ErrGrantNotFound when no matching row exists.
	RevokePermission(ctx context.Context, grantID, userID [16]byte, actingUserID string) error
}

// Service implements business logic for user permission management.
type Service struct {
	store Storer
}

// NewService constructs a Service with the given store.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// ListPermissions returns all active direct permission grants for targetUserID.
//
// Guards (in order):
//  1. Parse targetUserID → ErrGrantNotFound on bad UUID (consistent with Revoke)
//  2. store.GetUserPermissions
func (s *Service) ListPermissions(ctx context.Context, targetUserID string) ([]UserPermission, error) {
	userID, err := parseID(targetUserID)
	if err != nil {
		return nil, ErrGrantNotFound
	}
	perms, err := s.store.GetUserPermissions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("userpermissions.ListPermissions: get permissions: %w", err)
	}
	return perms, nil
}

// GrantPermission grants a direct permission to targetUserID.
//
// Guards (in order):
//  1. Parse targetUserID → ErrGrantNotFound on bad UUID
//  2. Parse actingUserID (from JWT) — 500 on failure
//  3. Validate input: permission_id + granted_reason + expires_at required
//  4. Normalise scope (default "own")
//  5. Parse permissionID → ErrPermissionNotFound on bad UUID
//  6. store.GrantPermissionTx — handles cap validation + re-grant idempotency internally
func (s *Service) GrantPermission(ctx context.Context, targetUserID, actingUserID string, in GrantPermissionInput) (UserPermission, error) {
	// 1. Parse targetUserID
	targetID, err := parseID(targetUserID)
	if err != nil {
		return UserPermission{}, ErrGrantNotFound
	}
	// 2. Parse actingUserID — actingUserID comes from the JWT subject; a parse
	//    failure here indicates a token signing misconfiguration, not user input.
	actorID, err := parseID(actingUserID)
	if err != nil {
		return UserPermission{}, fmt.Errorf("userpermissions.GrantPermission: invalid acting user id: %w", err)
	}
	// 3. Validate input
	if err := validateGrantPermission(in); err != nil {
		return UserPermission{}, err
	}
	// 4. Normalise scope
	in.Scope = normaliseScope(in.Scope)
	// 5. Parse permissionID
	permID, err := parseID(in.PermissionID)
	if err != nil {
		return UserPermission{}, ErrPermissionNotFound
	}
	// 6. Delegate to store
	result, err := s.store.GrantPermissionTx(ctx, GrantPermissionTxInput{
		UserID:        targetID,
		PermissionID:  permID,
		GrantedBy:     actorID,
		GrantedReason: in.GrantedReason,
		Scope:         in.Scope,
		Conditions:    in.Conditions,
		ExpiresAt:     in.ExpiresAt,
	})
	if err != nil {
		return UserPermission{}, fmt.Errorf("userpermissions.GrantPermission: grant tx: %w", err)
	}
	return result, nil
}

// RevokePermission revokes a direct permission grant from targetUserID.
//
// Guards (in order):
//  1. Parse targetUserID → ErrGrantNotFound on bad UUID
//  2. Parse grantID → ErrGrantNotFound on bad UUID
//  3. Parse actingUserID (from JWT) — 500 on failure
//  4. store.RevokePermission
func (s *Service) RevokePermission(ctx context.Context, targetUserID, grantID, actingUserID string) error {
	// 1. Parse targetUserID
	userID, err := parseID(targetUserID)
	if err != nil {
		return ErrGrantNotFound
	}
	// 2. Parse grantID
	gID, err := parseID(grantID)
	if err != nil {
		return ErrGrantNotFound
	}
	// 3. Parse actingUserID — actingUserID comes from the JWT subject; a parse
	//    failure here indicates a token signing misconfiguration, not user input.
	if _, err := parseID(actingUserID); err != nil {
		return fmt.Errorf("userpermissions.RevokePermission: invalid acting user id: %w", err)
	}
	// 4. Delegate to store
	// Security: detach from the request context so a client-timed disconnect
	// cannot abort the revocation mid-flight, leaving the grant active.
	if err := s.store.RevokePermission(context.WithoutCancel(ctx), gID, userID, actingUserID); err != nil {
		return fmt.Errorf("userpermissions.RevokePermission: revoke: %w", err)
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
