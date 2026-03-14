package userlock

import (
	"context"
	"fmt"
	"log/slog"

	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/google/uuid"
)

// adminLockKeyPrefix is the KV key prefix written when a user is admin-locked.
// Token middleware checks this key to invalidate all outstanding JWTs.
// Key format: "admin_lock:<user_uuid>"
const adminLockKeyPrefix = "admin_lock:"

// Storer is the data-access contract for the userlock service.
type Storer interface {
	// IsOwnerUser returns true when userID holds a role with is_owner_role = TRUE.
	// Returns false (not true + error) when the user has no role assignment.
	IsOwnerUser(ctx context.Context, userID [16]byte) (bool, error)

	// GetLockStatus returns the full lock state for userID.
	// Returns ErrUserNotFound when the user does not exist or is deleted.
	GetLockStatus(ctx context.Context, userID [16]byte) (UserLockStatus, error)

	// LockUserTx sets admin_locked = TRUE with metadata in user_secrets.
	// Must be called after IsOwnerUser and self-lock guards pass.
	LockUserTx(ctx context.Context, in LockUserTxInput) error

	// UnlockUserTx clears admin_locked and all metadata in user_secrets.
	UnlockUserTx(ctx context.Context, userID [16]byte, actingUserID string) error
}

// Service implements business logic for user lock management.
type Service struct {
	store   Storer
	kvStore kvstore.Store
}

// NewService constructs a Service with the given store and optional KV store.
// When kvStore is non-nil, LockUser writes an "admin_lock:<uid>" key that
// token.Auth uses to immediately reject all outstanding JWTs for the locked
// user. When nil, the KV step is skipped (safe for unit tests).
func NewService(store Storer, kvStore kvstore.Store) *Service {
	return &Service{store: store, kvStore: kvStore}
}

// LockUser admin-locks the target user account.
// validateLockUser must be called by the handler before this method is invoked.
//
// Guards (in order):
//  1. Parse targetUserID → ErrUserNotFound on bad UUID
//  2. Parse actingUserID (from JWT) → wrapped 500 error on bad UUID
//  3. Check targetUserID == actingUserID → ErrCannotLockSelf
//  4. store.GetLockStatus → ErrUserNotFound (existence gate, before owner check)
//  5. store.IsOwnerUser → if true → ErrCannotLockOwner
//  6. store.LockUserTx
func (s *Service) LockUser(ctx context.Context, targetUserID, actingUserID string, in LockUserInput) error {
	// 1. Parse targetUserID
	targetID, err := parseID(targetUserID)
	if err != nil {
		return ErrUserNotFound
	}
	// 2. Parse actingUserID — comes from JWT; parse failure indicates misconfiguration
	actorID, err := parseID(actingUserID)
	if err != nil {
		return fmt.Errorf("userlock.LockUser: invalid acting user id: %w", err)
	}
	// 3. Self-lock guard
	if targetID == actorID {
		return platformrbac.ErrCannotLockSelf
	}
	// 4. Existence gate — confirm user exists before the owner check to avoid
	// a wasted IsOwnerUser SELECT for non-existent target UUIDs.
	if _, err := s.store.GetLockStatus(ctx, targetID); err != nil {
		return err // ErrUserNotFound propagates unwrapped
	}
	// 5. Owner check
	isOwner, err := s.store.IsOwnerUser(ctx, targetID)
	if err != nil {
		return fmt.Errorf("userlock.LockUser: is owner check: %w", err)
	}
	if isOwner {
		return platformrbac.ErrCannotLockOwner
	}
	// 6. Lock
	if err := s.store.LockUserTx(ctx, LockUserTxInput{
		UserID:   targetID,
		LockedBy: actorID,
		Reason:   in.Reason,
	}); err != nil {
		return fmt.Errorf("userlock.LockUser: lock tx: %w", err)
	}
	// 7. Invalidate all outstanding JWTs for the user by writing the admin-lock
	// key to KV. token.Auth reads this key on every authenticated request and
	// returns 401 when present. No TTL — the key persists until UnlockUser
	// deletes it. Best-effort: a KV write failure is logged but does not abort
	// the lock (the DB row is the authoritative source of truth).
	// kvStore is nil in unit tests; skip silently.
	if s.kvStore != nil {
		kvKey := adminLockKeyPrefix + uuid.UUID(targetID).String()
		if kvErr := s.kvStore.Set(context.WithoutCancel(ctx), kvKey, "1", 0); kvErr != nil {
			slog.ErrorContext(ctx, "userlock.LockUser: set admin_lock KV key", "user_id", uuid.UUID(targetID).String(), "error", kvErr)
		}
	}
	return nil
}

// UnlockUser clears the admin lock on the target user account.
//
// Guards (in order):
//  1. Parse targetUserID → ErrUserNotFound on bad UUID
//  2. Parse actingUserID (from JWT) → wrapped 500 error on bad UUID
//  3. store.GetLockStatus → ErrUserNotFound (existence gate)
//  4. store.UnlockUser
func (s *Service) UnlockUser(ctx context.Context, targetUserID, actingUserID string) error {
	// 1. Parse targetUserID
	targetID, err := parseID(targetUserID)
	if err != nil {
		return ErrUserNotFound
	}
	// 2. Parse actingUserID — validate only; the string form is passed to the
	// store's WithActingUser for the audit trigger, so the parsed value is unused.
	if _, err := parseID(actingUserID); err != nil {
		return fmt.Errorf("userlock.UnlockUser: invalid acting user id: %w", err)
	}
	// 3. Existence gate
	if _, err := s.store.GetLockStatus(ctx, targetID); err != nil {
		return err // ErrUserNotFound propagates unwrapped
	}
	// 4. Unlock
	if err := s.store.UnlockUserTx(ctx, targetID, actingUserID); err != nil {
		return fmt.Errorf("userlock.UnlockUser: unlock: %w", err)
	}
	// 5. Delete the admin-lock KV key so previously rejected tokens can flow
	// through again (assuming they are not expired or JTI-blocked).
	// Best-effort: a KV delete failure is logged but does not abort the unlock.
	// kvStore is nil in unit tests; skip silently.
	if s.kvStore != nil {
		kvKey := adminLockKeyPrefix + uuid.UUID(targetID).String()
		if kvErr := s.kvStore.Delete(context.WithoutCancel(ctx), kvKey); kvErr != nil {
			slog.ErrorContext(ctx, "userlock.UnlockUser: delete admin_lock KV key", "user_id", uuid.UUID(targetID).String(), "error", kvErr)
		}
	}
	return nil
}

// GetLockStatus returns the lock state for the target user.
//
// Guards (in order):
//  1. Parse targetUserID → ErrUserNotFound on bad UUID
//  2. store.GetLockStatus → ErrUserNotFound
func (s *Service) GetLockStatus(ctx context.Context, targetUserID string) (UserLockStatus, error) {
	// 1. Parse targetUserID
	targetID, err := parseID(targetUserID)
	if err != nil {
		return UserLockStatus{}, ErrUserNotFound
	}
	// 2. Fetch
	status, err := s.store.GetLockStatus(ctx, targetID)
	if err != nil {
		return UserLockStatus{}, err // ErrUserNotFound propagates unwrapped
	}
	return status, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func parseID(s string) ([16]byte, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return [16]byte{}, err
	}
	return [16]byte(id), nil
}
