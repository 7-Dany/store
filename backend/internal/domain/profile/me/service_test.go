package me_test

import (
	"context"
	"errors"
	"testing"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/profile/me"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── Service unit tests (MeFakeStorer) ──────────────────────────────────────────

// TestProfile_GetUserProfile covers service.GetUserProfile.
func TestProfile_GetUserProfile(t *testing.T) {
	t.Parallel()

	t.Run("found returns profile with correct fields", func(t *testing.T) {
		t.Parallel()
		want := me.UserProfile{ID: authsharedtest.RandomUUID(), Email: "a@example.com", DisplayName: "Alice"}
		store := &authsharedtest.MeFakeStorer{
			GetUserProfileFn: func(_ context.Context, _ [16]byte) (me.UserProfile, error) {
				return want, nil
			},
		}
		svc := me.NewService(store)
		got, err := svc.GetUserProfile(context.Background(), uuid.UUID(want.ID).String())
		require.NoError(t, err)
		require.Equal(t, want.Email, got.Email)
		require.Equal(t, want.DisplayName, got.DisplayName)
	})

	t.Run("not found returns ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.MeFakeStorer{
			GetUserProfileFn: func(_ context.Context, _ [16]byte) (me.UserProfile, error) {
				return me.UserProfile{}, authshared.ErrUserNotFound
			},
		}
		svc := me.NewService(store)
		_, err := svc.GetUserProfile(context.Background(), uuid.NewString())
		require.ErrorIs(t, err, authshared.ErrUserNotFound)
	})

	t.Run("store error returns wrapped error", func(t *testing.T) {
		t.Parallel()
		storeErr := errors.New("connection reset")
		store := &authsharedtest.MeFakeStorer{
			GetUserProfileFn: func(_ context.Context, _ [16]byte) (me.UserProfile, error) {
				return me.UserProfile{}, storeErr
			},
		}
		svc := me.NewService(store)
		_, err := svc.GetUserProfile(context.Background(), uuid.NewString())
		require.Error(t, err)
		require.ErrorIs(t, err, storeErr)
		require.NotErrorIs(t, err, authshared.ErrUserNotFound)
	})

	t.Run("invalid UUID returns parse error", func(t *testing.T) {
		t.Parallel()
		svc := me.NewService(&authsharedtest.MeFakeStorer{})
		_, err := svc.GetUserProfile(context.Background(), "not-a-uuid")
		require.Error(t, err)
		require.NotErrorIs(t, err, authshared.ErrUserNotFound)
	})
}

// TestService_GetUserProfile_UUIDBytesForwardedToStore verifies that the service
// correctly parses a UUID string and forwards the matching [16]byte to the store.
func TestService_GetUserProfile_UUIDBytesForwardedToStore(t *testing.T) {
	t.Parallel()
	knownUID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	var got [16]byte
	store := &authsharedtest.MeFakeStorer{
		GetUserProfileFn: func(_ context.Context, userID [16]byte) (me.UserProfile, error) {
			got = userID
			return me.UserProfile{}, nil
		},
	}
	svc := me.NewService(store)
	_, _ = svc.GetUserProfile(context.Background(), knownUID.String())
	require.Equal(t, [16]byte(knownUID), got,
		"the [16]byte forwarded to the store must equal the parsed UUID")
}

// TestService_UpdateProfile covers service.UpdateProfile.
func TestService_UpdateProfile(t *testing.T) {
	t.Parallel()

	displayName := "Alice"
	avatarURL := "https://example.com/avatar.png"

	t.Run("T-01 happy path both fields", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.MeFakeStorer{
			UpdateProfileTxFn: func(_ context.Context, _ me.UpdateProfileInput) error {
				return nil
			},
		}
		svc := me.NewService(store)
		err := svc.UpdateProfile(context.Background(), me.UpdateProfileInput{
			UserID:      authsharedtest.RandomUUID(),
			DisplayName: &displayName,
			AvatarURL:   &avatarURL,
			IPAddress:   "1.2.3.4",
			UserAgent:   "test-agent",
		})
		require.NoError(t, err)
	})

	t.Run("T-02 happy path display_name only", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.MeFakeStorer{
			UpdateProfileTxFn: func(_ context.Context, _ me.UpdateProfileInput) error {
				return nil
			},
		}
		svc := me.NewService(store)
		err := svc.UpdateProfile(context.Background(), me.UpdateProfileInput{
			UserID:      authsharedtest.RandomUUID(),
			DisplayName: &displayName,
			AvatarURL:   nil,
			IPAddress:   "1.2.3.4",
			UserAgent:   "test-agent",
		})
		require.NoError(t, err)
	})

	t.Run("T-03 happy path avatar_url only", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.MeFakeStorer{
			UpdateProfileTxFn: func(_ context.Context, _ me.UpdateProfileInput) error {
				return nil
			},
		}
		svc := me.NewService(store)
		err := svc.UpdateProfile(context.Background(), me.UpdateProfileInput{
			UserID:    authsharedtest.RandomUUID(),
			AvatarURL: &avatarURL,
			IPAddress: "1.2.3.4",
			UserAgent: "test-agent",
		})
		require.NoError(t, err)
	})

	t.Run("T-18 store error wraps with correct prefix", func(t *testing.T) {
		t.Parallel()
		storeErr := errors.New("db down")
		store := &authsharedtest.MeFakeStorer{
			UpdateProfileTxFn: func(_ context.Context, _ me.UpdateProfileInput) error {
				return storeErr
			},
		}
		svc := me.NewService(store)
		err := svc.UpdateProfile(context.Background(), me.UpdateProfileInput{
			UserID:      authsharedtest.RandomUUID(),
			DisplayName: &displayName,
			IPAddress:   "1.2.3.4",
			UserAgent:   "test-agent",
		})
		require.Error(t, err)
		require.ErrorIs(t, err, storeErr)
		require.Contains(t, err.Error(), "profile.UpdateProfile:")
	})

	t.Run("store receives correct UpdateProfileInput", func(t *testing.T) {
		t.Parallel()
		uid := authsharedtest.RandomUUID()
		var gotIn me.UpdateProfileInput
		store := &authsharedtest.MeFakeStorer{
			UpdateProfileTxFn: func(_ context.Context, in me.UpdateProfileInput) error {
				gotIn = in
				return nil
			},
		}
		svc := me.NewService(store)
		in := me.UpdateProfileInput{
			UserID:      uid,
			DisplayName: &displayName,
			AvatarURL:   &avatarURL,
			IPAddress:   "10.0.0.1",
			UserAgent:   "go-test/1.0",
		}
		_ = svc.UpdateProfile(context.Background(), in)
		require.Equal(t, uid, gotIn.UserID)
		require.Equal(t, &displayName, gotIn.DisplayName)
		require.Equal(t, &avatarURL, gotIn.AvatarURL)
	})
}
