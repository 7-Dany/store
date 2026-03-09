package me

import (
	"context"
	"fmt"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// Storer is the data-access contract that Service depends on.
// *Store satisfies this interface; tests may supply a fake implementation.
type Storer interface {
	GetUserProfile(ctx context.Context, userID [16]byte) (UserProfile, error)
	UpdateProfileTx(ctx context.Context, in UpdateProfileInput) error
	GetUserIdentities(ctx context.Context, userID [16]byte) ([]LinkedIdentity, error) // §E-1
}

// Service holds pure business logic for the me sub-package.
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

// UpdateProfile updates the authenticated user's display_name and/or avatar_url.
// in.DisplayName and in.AvatarURL use nil to mean "do not change this field".
// All validation has been performed by the caller before this method is invoked.
func (s *Service) UpdateProfile(ctx context.Context, in UpdateProfileInput) error {
	if err := s.store.UpdateProfileTx(ctx, in); err != nil {
		return fmt.Errorf("profile.UpdateProfile: update profile: %w", err)
	}
	return nil
}

// GetUserIdentities returns all linked OAuth identities for the authenticated
// user. Returns an empty (non-nil) slice when the user has no linked
// identities. userID is the standard UUID string form.
func (s *Service) GetUserIdentities(ctx context.Context, userID string) ([]LinkedIdentity, error) {
	uid, err := authshared.ParseUserID("profile.GetUserIdentities", userID)
	if err != nil {
		return nil, err
	}
	identities, err := s.store.GetUserIdentities(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("profile.GetUserIdentities: get identities: %w", err)
	}
	return identities, nil
}
