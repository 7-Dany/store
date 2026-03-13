package userroles

import (
	"context"
	"errors"
	"fmt"

	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/google/uuid"
)

// Storer is the data-access contract for the userroles service.
type Storer interface {
	// GetUserRole returns the user's current active role assignment.
	// Returns ErrUserRoleNotFound when no active assignment exists.
	GetUserRole(ctx context.Context, userID [16]byte) (UserRole, error)

	// AssignUserRoleTx upserts the user's role and returns the full UserRole.
	// Returns ErrRoleNotFound when roleID does not correspond to an active role.
	AssignUserRoleTx(ctx context.Context, in AssignRoleTxInput) (UserRole, error)

	// RemoveUserRole deletes the user's active role assignment.
	// actingUserID is written to rbac.acting_user so the audit trigger records
	// the correct deletion actor.
	// Returns ErrUserRoleNotFound when the user has no active assignment.
	// Returns ErrLastOwnerRemoval when fn_prevent_orphaned_owner fires (23000).
	RemoveUserRole(ctx context.Context, userID [16]byte, actingUserID string) error
}

// Service implements business logic for user role management.
type Service struct {
	store Storer
}

// NewService constructs a Service with the given store.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// GetUserRole returns the active role for targetUserID.
func (s *Service) GetUserRole(ctx context.Context, targetUserID string) (UserRole, error) {
	id, err := parseID(targetUserID)
	if err != nil {
		return UserRole{}, ErrUserRoleNotFound
	}
	ur, err := s.store.GetUserRole(ctx, id)
	if err != nil {
		return UserRole{}, fmt.Errorf("userroles.GetUserRole: %w", err)
	}
	return ur, nil
}

// AssignRole assigns (or replaces) a role for targetUserID.
//
// Guards (in order):
//  1. Parse targetUserID → ErrUserRoleNotFound on bad UUID
//  2. Parse actingUserID (from JWT) — actingUserID comes from the JWT subject;
//     a parse failure here indicates a token signing misconfiguration, not user input.
//  3. Self-assignment: targetUserID == actingUserID → rbac.ErrCannotModifyOwnRole
//  4. Validate input: role_id + granted_reason required (defence-in-depth; handler validates first)
//  5. Parse roleID → ErrRoleNotFound on bad UUID
//  6. GetUserRole for target: if row found and IsOwnerRole = true → rbac.ErrCannotReassignOwner
//  7. AssignUserRoleTx in store (upsert)
func (s *Service) AssignRole(ctx context.Context, targetUserID, actingUserID string, in AssignRoleInput) (UserRole, error) {
	// 1. Parse targetUserID
	targetID, err := parseID(targetUserID)
	if err != nil {
		return UserRole{}, ErrUserRoleNotFound
	}
	// 2. Parse actingUserID — actingUserID comes from the JWT subject; a parse
	//    failure here indicates a token signing misconfiguration, not user input.
	actorID, err := parseID(actingUserID)
	if err != nil {
		return UserRole{}, fmt.Errorf("userroles.AssignRole: invalid acting user id: %w", err)
	}
	// 3. Self-assignment guard
	if targetID == actorID {
		return UserRole{}, platformrbac.ErrCannotModifyOwnRole
	}
	// 4. Defence-in-depth: handler always validates first; this guard protects
	//    callers that bypass the HTTP layer (e.g. tests, future CLI tools).
	if err := validateAssignRole(in); err != nil {
		return UserRole{}, err
	}
	// 5. Parse roleID
	roleID, err := parseID(in.RoleID)
	if err != nil {
		return UserRole{}, ErrRoleNotFound
	}
	// 6. Owner guard: check if target already has an owner role
	existing, err := s.store.GetUserRole(ctx, targetID)
	if err != nil && !errors.Is(err, ErrUserRoleNotFound) {
		return UserRole{}, fmt.Errorf("userroles.AssignRole: check target role: %w", err)
	}
	if err == nil && existing.IsOwnerRole {
		return UserRole{}, platformrbac.ErrCannotReassignOwner
	}
	// 7. Upsert.
	// Unreachable: the owner guard at step 6 returns ErrCannotReassignOwner
	// before this point for any target that already holds an owner role.
	// fn_prevent_orphaned_owner cannot fire on an upsert that replaces a
	// non-owner role, so ErrLastOwnerRemoval never propagates from here.
	result, err := s.store.AssignUserRoleTx(ctx, AssignRoleTxInput{
		UserID:        targetID,
		RoleID:        roleID,
		GrantedBy:     actorID,
		GrantedReason: in.GrantedReason,
		ExpiresAt:     in.ExpiresAt,
	})
	if err != nil {
		return UserRole{}, fmt.Errorf("userroles.AssignRole: %w", err)
	}
	return result, nil
}

// RemoveRole removes the active role for targetUserID.
//
// Guards (in order):
//  1. Parse targetUserID → ErrUserRoleNotFound on bad UUID
//  2. Parse actingUserID — actingUserID comes from the JWT subject; a parse
//     failure here indicates a token signing misconfiguration, not user input.
//  3. Self-assignment: targetUserID == actingUserID → rbac.ErrCannotModifyOwnRole
//  4. GetUserRole for target: if not found → ErrUserRoleNotFound
//  5. If IsOwnerRole = true → rbac.ErrCannotReassignOwner
//  6. RemoveUserRole in store
func (s *Service) RemoveRole(ctx context.Context, targetUserID, actingUserID string) error {
	// 1. Parse targetUserID
	targetID, err := parseID(targetUserID)
	if err != nil {
		return ErrUserRoleNotFound
	}
	// 2. Parse actingUserID — actingUserID comes from the JWT subject; a parse
	//    failure here indicates a token signing misconfiguration, not user input.
	actorID, err := parseID(actingUserID)
	if err != nil {
		return fmt.Errorf("userroles.RemoveRole: invalid acting user id: %w", err)
	}
	// 3. Self-assignment guard
	if targetID == actorID {
		return platformrbac.ErrCannotModifyOwnRole
	}
	// 4. Check if target has a role
	existing, err := s.store.GetUserRole(ctx, targetID)
	if err != nil {
		return fmt.Errorf("userroles.RemoveRole: %w", err) // propagates ErrUserRoleNotFound
	}
	// 5. Owner guard
	if existing.IsOwnerRole {
		return platformrbac.ErrCannotReassignOwner
	}
	// 6. Remove
	if err := s.store.RemoveUserRole(ctx, targetID, actingUserID); err != nil {
		return fmt.Errorf("userroles.RemoveRole: %w", err)
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
