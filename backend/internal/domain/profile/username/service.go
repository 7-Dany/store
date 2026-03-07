package username

import (
	"context"
	"errors"
	"fmt"

	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
)

// Storer is the data-access contract for the username feature.
// The concrete implementation is *Store; tests use UsernameFakeStorer from
// internal/domain/auth/shared/testutil.
type Storer interface {
	// CheckUsernameAvailable returns true when no user row with username = username
	// exists in the database. The result is a point-in-time read; the mutation path
	// enforces uniqueness via the idx_users_username unique index.
	CheckUsernameAvailable(ctx context.Context, username string) (bool, error)

	// UpdateUsernameTx atomically fetches the current username, validates it is not
	// identical to the requested username, updates the users row, and writes an audit
	// log row — all within a single database transaction.
	UpdateUsernameTx(ctx context.Context, in UpdateUsernameInput) error
}

// Service holds the business logic for the username availability check and
// username mutation endpoints. It has no knowledge of HTTP, pgtype, or JWT.
type Service struct {
	store Storer
}

// NewService constructs a Service backed by the given Storer.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// CheckUsernameAvailable normalises and validates the candidate username, then
// queries the store for a point-in-time availability check.
// Returns (false, err) on any validation or store failure; (true, nil) when the
// username is unclaimed; (false, nil) when the username is already taken.
//
// Guard ordering (Stage 0 §5a):
//  1. Normalise + validate username — on failure: return false + validation sentinel.
//  2. store.CheckUsernameAvailable — on store error: return false + wrapped error.
func (s *Service) CheckUsernameAvailable(ctx context.Context, username string) (bool, error) {
	// 1. Normalise and validate before hitting the store.
	normalised, err := NormaliseAndValidateUsername(username)
	if err != nil {
		return false, err
	}

	// 2. Point-in-time availability query.
	available, err := s.store.CheckUsernameAvailable(ctx, normalised)
	if err != nil {
		return false, fmt.Errorf("username.CheckUsernameAvailable: query: %w", err)
	}
	return available, nil
}

// UpdateUsername normalises and validates the new username, then atomically
// updates the authenticated user's username via the store transaction.
// Returns a sentinel error on validation failure, same-username, uniqueness
// conflict, or an unexpected store error.
//
// Guard ordering (Stage 0 §5b):
//  1. Normalise + validate new username — on failure: validation sentinel.
//  2. store.UpdateUsernameTx — handles same-username check, unique constraint,
//     FOR UPDATE lock, and audit write inside a single transaction.
//
// Note: UserID is received as [16]byte from the handler (already parsed from the
// JWT claim), so no UUID parsing step is required in the service.
func (s *Service) UpdateUsername(ctx context.Context, in UpdateUsernameInput) error {
	// 1. Normalise and validate the new username before any store call.
	normalised, err := NormaliseAndValidateUsername(in.Username)
	if err != nil {
		return err
	}

	// 2. Delegate to the store transaction which enforces uniqueness, detects
	// the same-username case, writes the audit row, and commits atomically.
	if err := s.store.UpdateUsernameTx(ctx, UpdateUsernameInput{
		UserID:    in.UserID,
		Username:  normalised,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
	}); err != nil {
		switch {
		case errors.Is(err, ErrSameUsername):
			return ErrSameUsername
		case errors.Is(err, ErrUsernameTaken):
			return ErrUsernameTaken
		case errors.Is(err, profileshared.ErrUserNotFound):
			return profileshared.ErrUserNotFound
		default:
			return fmt.Errorf("username.UpdateUsername: update username tx: %w", err)
		}
	}
	return nil
}
