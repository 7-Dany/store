package profile_test

import (
	"context"
	"errors"
	"testing"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/auth/profile"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── Service unit tests (FakeStorer) ───────────────────────────────────────────

// TestProfile_GetUserProfile covers service.GetUserProfile.
func TestProfile_GetUserProfile(t *testing.T) {
	t.Parallel()

	t.Run("found returns profile with correct fields", func(t *testing.T) {
		t.Parallel()
		want := profile.UserProfile{ID: authsharedtest.RandomUUID(), Email: "a@example.com", DisplayName: "Alice"}
		store := &authsharedtest.ProfileFakeStorer{
			GetUserProfileFn: func(_ context.Context, _ [16]byte) (profile.UserProfile, error) {
				return want, nil
			},
		}
		svc := profile.NewService(store)
		got, err := svc.GetUserProfile(context.Background(), uuid.UUID(want.ID).String())
		require.NoError(t, err)
		require.Equal(t, want.Email, got.Email)
		require.Equal(t, want.DisplayName, got.DisplayName)
	})

	t.Run("not found returns ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.ProfileFakeStorer{
			GetUserProfileFn: func(_ context.Context, _ [16]byte) (profile.UserProfile, error) {
				return profile.UserProfile{}, authshared.ErrUserNotFound
			},
		}
		svc := profile.NewService(store)
		_, err := svc.GetUserProfile(context.Background(), uuid.NewString())
		require.ErrorIs(t, err, authshared.ErrUserNotFound)
	})

	t.Run("store error returns wrapped error", func(t *testing.T) {
		t.Parallel()
		storeErr := errors.New("connection reset")
		store := &authsharedtest.ProfileFakeStorer{
			GetUserProfileFn: func(_ context.Context, _ [16]byte) (profile.UserProfile, error) {
				return profile.UserProfile{}, storeErr
			},
		}
		svc := profile.NewService(store)
		_, err := svc.GetUserProfile(context.Background(), uuid.NewString())
		require.Error(t, err)
		require.ErrorIs(t, err, storeErr)
		require.NotErrorIs(t, err, authshared.ErrUserNotFound)
	})

	t.Run("invalid UUID returns parse error", func(t *testing.T) {
		t.Parallel()
		svc := profile.NewService(&authsharedtest.ProfileFakeStorer{})
		_, err := svc.GetUserProfile(context.Background(), "not-a-uuid")
		require.Error(t, err)
		require.NotErrorIs(t, err, authshared.ErrUserNotFound)
	})
}

// TestProfile_GetActiveSessions covers service.GetActiveSessions.
func TestProfile_GetActiveSessions(t *testing.T) {
	t.Parallel()

	t.Run("returns slice from store", func(t *testing.T) {
		t.Parallel()
		want := []profile.ActiveSession{{ID: authsharedtest.RandomUUID(), IPAddress: "1.2.3.4"}}
		store := &authsharedtest.ProfileFakeStorer{
			GetActiveSessionsFn: func(_ context.Context, _ [16]byte) ([]profile.ActiveSession, error) {
				return want, nil
			},
		}
		svc := profile.NewService(store)
		got, err := svc.GetActiveSessions(context.Background(), uuid.NewString())
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, want[0].IPAddress, got[0].IPAddress)
	})

	t.Run("returns empty slice when store returns nil", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.ProfileFakeStorer{
			GetActiveSessionsFn: func(_ context.Context, _ [16]byte) ([]profile.ActiveSession, error) {
				return nil, nil
			},
		}
		svc := profile.NewService(store)
		got, err := svc.GetActiveSessions(context.Background(), uuid.NewString())
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("store error returns wrapped error", func(t *testing.T) {
		t.Parallel()
		storeErr := errors.New("db timeout")
		store := &authsharedtest.ProfileFakeStorer{
			GetActiveSessionsFn: func(_ context.Context, _ [16]byte) ([]profile.ActiveSession, error) {
				return nil, storeErr
			},
		}
		svc := profile.NewService(store)
		_, err := svc.GetActiveSessions(context.Background(), uuid.NewString())
		require.Error(t, err)
		require.ErrorIs(t, err, storeErr)
	})

	t.Run("invalid UUID returns parse error", func(t *testing.T) {
		t.Parallel()
		svc := profile.NewService(&authsharedtest.ProfileFakeStorer{})
		_, err := svc.GetActiveSessions(context.Background(), "not-a-uuid")
		require.Error(t, err)
	})
}

// TestService_GetUserProfile_UUIDBytesForwardedToStore verifies that the service
// correctly parses a UUID string and forwards the matching [16]byte to the store.
func TestService_GetUserProfile_UUIDBytesForwardedToStore(t *testing.T) {
	t.Parallel()
	knownUID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	var got [16]byte
	store := &authsharedtest.ProfileFakeStorer{
		GetUserProfileFn: func(_ context.Context, userID [16]byte) (profile.UserProfile, error) {
			got = userID
			return profile.UserProfile{}, nil
		},
	}
	svc := profile.NewService(store)
	_, _ = svc.GetUserProfile(context.Background(), knownUID.String())
	require.Equal(t, [16]byte(knownUID), got,
		"the [16]byte forwarded to the store must equal the parsed UUID")
}

// TestService_GetActiveSessions_UUIDBytesForwardedToStore verifies that the
// service correctly parses a UUID string and forwards the matching [16]byte to
// the store.
func TestService_GetActiveSessions_UUIDBytesForwardedToStore(t *testing.T) {
	t.Parallel()
	knownUID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	var got [16]byte
	store := &authsharedtest.ProfileFakeStorer{
		GetActiveSessionsFn: func(_ context.Context, userID [16]byte) ([]profile.ActiveSession, error) {
			got = userID
			return nil, nil
		},
	}
	svc := profile.NewService(store)
	_, _ = svc.GetActiveSessions(context.Background(), knownUID.String())
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
		store := &authsharedtest.ProfileFakeStorer{
			UpdateProfileTxFn: func(_ context.Context, _ profile.UpdateProfileInput) error {
				return nil
			},
		}
		svc := profile.NewService(store)
		err := svc.UpdateProfile(context.Background(), profile.UpdateProfileInput{
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
		store := &authsharedtest.ProfileFakeStorer{
			UpdateProfileTxFn: func(_ context.Context, _ profile.UpdateProfileInput) error {
				return nil
			},
		}
		svc := profile.NewService(store)
		err := svc.UpdateProfile(context.Background(), profile.UpdateProfileInput{
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
		store := &authsharedtest.ProfileFakeStorer{
			UpdateProfileTxFn: func(_ context.Context, _ profile.UpdateProfileInput) error {
				return nil
			},
		}
		svc := profile.NewService(store)
		err := svc.UpdateProfile(context.Background(), profile.UpdateProfileInput{
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
		store := &authsharedtest.ProfileFakeStorer{
			UpdateProfileTxFn: func(_ context.Context, _ profile.UpdateProfileInput) error {
				return storeErr
			},
		}
		svc := profile.NewService(store)
		err := svc.UpdateProfile(context.Background(), profile.UpdateProfileInput{
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
		var gotIn profile.UpdateProfileInput
		store := &authsharedtest.ProfileFakeStorer{
			UpdateProfileTxFn: func(_ context.Context, in profile.UpdateProfileInput) error {
				gotIn = in
				return nil
			},
		}
		svc := profile.NewService(store)
		in := profile.UpdateProfileInput{
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

// TestProfile_RevokeSession covers service.RevokeSession.
func TestProfile_RevokeSession(t *testing.T) {
	t.Parallel()

	t.Run("success delegates to store with correct parsed UUIDs", func(t *testing.T) {
		t.Parallel()
		var gotSessionID, gotOwnerID [16]byte
		store := &authsharedtest.ProfileFakeStorer{
			RevokeSessionTxFn: func(_ context.Context, sessionID, ownerUserID [16]byte, _, _ string) error {
				gotSessionID = sessionID
				gotOwnerID = ownerUserID
				return nil
			},
		}
		svc := profile.NewService(store)
		uid := uuid.New()
		sid := uuid.New()
		err := svc.RevokeSession(context.Background(), uid.String(), sid.String(), "1.2.3.4", "ua")
		require.NoError(t, err)
		require.Equal(t, [16]byte(uid), gotOwnerID)
		require.Equal(t, [16]byte(sid), gotSessionID)
	})

	t.Run("ErrSessionNotFound propagates through wrap", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.ProfileFakeStorer{
			RevokeSessionTxFn: func(_ context.Context, _, _ [16]byte, _, _ string) error {
				return authshared.ErrSessionNotFound
			},
		}
		svc := profile.NewService(store)
		err := svc.RevokeSession(context.Background(), uuid.NewString(), uuid.NewString(), "", "")
		require.ErrorIs(t, err, authshared.ErrSessionNotFound)
	})

	t.Run("invalid userID UUID returns error", func(t *testing.T) {
		t.Parallel()
		svc := profile.NewService(&authsharedtest.ProfileFakeStorer{})
		err := svc.RevokeSession(context.Background(), "bad-uuid", uuid.NewString(), "", "")
		require.Error(t, err)
		require.NotErrorIs(t, err, authshared.ErrSessionNotFound)
	})

	t.Run("invalid sessionID UUID returns error", func(t *testing.T) {
		t.Parallel()
		svc := profile.NewService(&authsharedtest.ProfileFakeStorer{})
		err := svc.RevokeSession(context.Background(), uuid.NewString(), "bad-uuid", "", "")
		require.Error(t, err)
		require.NotErrorIs(t, err, authshared.ErrSessionNotFound)
	})

	t.Run("unexpected store error returns wrapped error", func(t *testing.T) {
		t.Parallel()
		storeErr := errors.New("db gone")
		store := &authsharedtest.ProfileFakeStorer{
			RevokeSessionTxFn: func(_ context.Context, _, _ [16]byte, _, _ string) error {
				return storeErr
			},
		}
		svc := profile.NewService(store)
		err := svc.RevokeSession(context.Background(), uuid.NewString(), uuid.NewString(), "ip", "ua")
		require.Error(t, err)
		require.ErrorIs(t, err, storeErr)
		require.NotErrorIs(t, err, authshared.ErrSessionNotFound)
	})
}


