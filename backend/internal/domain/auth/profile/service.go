package profile

import (
	"context"
	"fmt"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/google/uuid"
)

// Storer is the data-access contract that Service depends on.
// *Store satisfies this interface; tests may supply a fake implementation.
type Storer interface {
	GetUserProfile(ctx context.Context, userID [16]byte) (UserProfile, error)
	GetActiveSessions(ctx context.Context, userID [16]byte) ([]ActiveSession, error)
	RevokeSessionTx(ctx context.Context, sessionID, ownerUserID [16]byte, ipAddress, userAgent string) error
	UpdateProfileTx(ctx context.Context, in UpdateProfileInput) error
}

// Service holds pure business logic for the profile sub-package.
// It has no knowledge of HTTP, pgtype, pgxpool, or JWT signing.
type Service struct {
	store Storer
}

// NewService constructs a Service with the given store.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// GetUserProfile returns the public profile for the given user.
// Returns authshared.ErrUserNotFound on no-rows. userID is the standard UUID string form.
func (s *Service) GetUserProfile(ctx context.Context, userID string) (UserProfile, error) {
	uid, err := authshared.ParseUserID("profile.GetUserProfile", userID)
	if err != nil {
		return UserProfile{}, err
	}
	profile, err := s.store.GetUserProfile(ctx, uid)
	if err != nil {
		return UserProfile{}, fmt.Errorf("profile.GetUserProfile: get profile: %w", err)
	}
	return profile, nil
}

// GetActiveSessions returns all open sessions for the given user, newest first.
// Returns an empty slice (not an error) when no sessions exist.
// userID is the standard UUID string form.
func (s *Service) GetActiveSessions(ctx context.Context, userID string) ([]ActiveSession, error) {
	uid, err := authshared.ParseUserID("profile.GetActiveSessions", userID)
	if err != nil {
		return nil, err
	}
	sessions, err := s.store.GetActiveSessions(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("profile.GetActiveSessions: get sessions: %w", err)
	}
	return sessions, nil
}

// UpdateProfile updates the authenticated user's display_name and/or avatar_url.
// in.DisplayName and in.AvatarURL use nil to mean "do not change this field".
// All validation has been performed by the caller before this method is invoked.
func (s *Service) UpdateProfile(ctx context.Context, in UpdateProfileInput) error {
	if err := s.store.UpdateProfileTx(ctx, in); err != nil {
		return fmt.Errorf("profile.UpdateProfile: update profile: %w", err)
	}
	return nil
}

// RevokeSession revokes a specific session, verifying it belongs to the caller.
// Returns authshared.ErrSessionNotFound if the session does not exist or belongs to a
// different user. Both userID and sessionID are standard UUID string form.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID, ipAddress, userAgent string) error {
	uid, err := authshared.ParseUserID("profile.RevokeSession", userID)
	if err != nil {
		return err
	}
	sid, err := uuid.Parse(sessionID)
	if err != nil {
		return fmt.Errorf("profile.RevokeSession: parse session id: %w", err)
	}
	// Security: use WithoutCancel so a client disconnect cannot abort the session revocation commit.
	if err := s.store.RevokeSessionTx(context.WithoutCancel(ctx), [16]byte(sid), uid, ipAddress, userAgent); err != nil {
		return fmt.Errorf("profile.RevokeSession: revoke session: %w", err)
	}
	return nil
}
