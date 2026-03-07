package session_test

import (
	"context"
	"errors"
	"testing"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/profile/session"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── Service unit tests (FakeStorer) ───────────────────────────────────────────

// TestProfile_GetActiveSessions covers service.GetActiveSessions.
func TestProfile_GetActiveSessions(t *testing.T) {
	t.Parallel()

	t.Run("returns slice from store", func(t *testing.T) {
		t.Parallel()
		want := []session.ActiveSession{{ID: authsharedtest.RandomUUID(), IPAddress: "1.2.3.4"}}
		store := &authsharedtest.ProfileSessionFakeStorer{
			GetActiveSessionsFn: func(_ context.Context, _ [16]byte) ([]session.ActiveSession, error) {
				return want, nil
			},
		}
		svc := session.NewService(store)
		got, err := svc.GetActiveSessions(context.Background(), uuid.NewString())
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, want[0].IPAddress, got[0].IPAddress)
	})

	t.Run("returns empty slice when store returns nil", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.ProfileSessionFakeStorer{
			GetActiveSessionsFn: func(_ context.Context, _ [16]byte) ([]session.ActiveSession, error) {
				return nil, nil
			},
		}
		svc := session.NewService(store)
		got, err := svc.GetActiveSessions(context.Background(), uuid.NewString())
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("store error returns wrapped error", func(t *testing.T) {
		t.Parallel()
		storeErr := errors.New("db timeout")
		store := &authsharedtest.ProfileSessionFakeStorer{
			GetActiveSessionsFn: func(_ context.Context, _ [16]byte) ([]session.ActiveSession, error) {
				return nil, storeErr
			},
		}
		svc := session.NewService(store)
		_, err := svc.GetActiveSessions(context.Background(), uuid.NewString())
		require.Error(t, err)
		require.ErrorIs(t, err, storeErr)
	})

	t.Run("invalid UUID returns parse error", func(t *testing.T) {
		t.Parallel()
		svc := session.NewService(&authsharedtest.ProfileSessionFakeStorer{})
		_, err := svc.GetActiveSessions(context.Background(), "not-a-uuid")
		require.Error(t, err)
	})
}

// TestService_GetActiveSessions_UUIDBytesForwardedToStore verifies that the
// service correctly parses a UUID string and forwards the matching [16]byte to
// the store.
func TestService_GetActiveSessions_UUIDBytesForwardedToStore(t *testing.T) {
	t.Parallel()
	knownUID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	var got [16]byte
	store := &authsharedtest.ProfileSessionFakeStorer{
		GetActiveSessionsFn: func(_ context.Context, userID [16]byte) ([]session.ActiveSession, error) {
			got = userID
			return nil, nil
		},
	}
	svc := session.NewService(store)
	_, _ = svc.GetActiveSessions(context.Background(), knownUID.String())
	require.Equal(t, [16]byte(knownUID), got,
		"the [16]byte forwarded to the store must equal the parsed UUID")
}

// TestProfile_RevokeSession covers service.RevokeSession.
func TestProfile_RevokeSession(t *testing.T) {
	t.Parallel()

	t.Run("success delegates to store with correct parsed UUIDs", func(t *testing.T) {
		t.Parallel()
		var gotSessionID, gotOwnerID [16]byte
		store := &authsharedtest.ProfileSessionFakeStorer{
			RevokeSessionTxFn: func(_ context.Context, sessionID, ownerUserID [16]byte, _, _ string) error {
				gotSessionID = sessionID
				gotOwnerID = ownerUserID
				return nil
			},
		}
		svc := session.NewService(store)
		uid := uuid.New()
		sid := uuid.New()
		err := svc.RevokeSession(context.Background(), uid.String(), sid.String(), "1.2.3.4", "ua")
		require.NoError(t, err)
		require.Equal(t, [16]byte(uid), gotOwnerID)
		require.Equal(t, [16]byte(sid), gotSessionID)
	})

	t.Run("ErrSessionNotFound propagates through wrap", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.ProfileSessionFakeStorer{
			RevokeSessionTxFn: func(_ context.Context, _, _ [16]byte, _, _ string) error {
				return authshared.ErrSessionNotFound
			},
		}
		svc := session.NewService(store)
		err := svc.RevokeSession(context.Background(), uuid.NewString(), uuid.NewString(), "", "")
		require.ErrorIs(t, err, authshared.ErrSessionNotFound)
	})

	t.Run("invalid userID UUID returns error", func(t *testing.T) {
		t.Parallel()
		svc := session.NewService(&authsharedtest.ProfileSessionFakeStorer{})
		err := svc.RevokeSession(context.Background(), "bad-uuid", uuid.NewString(), "", "")
		require.Error(t, err)
		require.NotErrorIs(t, err, authshared.ErrSessionNotFound)
	})

	t.Run("invalid sessionID UUID returns error", func(t *testing.T) {
		t.Parallel()
		svc := session.NewService(&authsharedtest.ProfileSessionFakeStorer{})
		err := svc.RevokeSession(context.Background(), uuid.NewString(), "bad-uuid", "", "")
		require.Error(t, err)
		require.NotErrorIs(t, err, authshared.ErrSessionNotFound)
	})

	t.Run("unexpected store error returns wrapped error", func(t *testing.T) {
		t.Parallel()
		storeErr := errors.New("db gone")
		store := &authsharedtest.ProfileSessionFakeStorer{
			RevokeSessionTxFn: func(_ context.Context, _, _ [16]byte, _, _ string) error {
				return storeErr
			},
		}
		svc := session.NewService(store)
		err := svc.RevokeSession(context.Background(), uuid.NewString(), uuid.NewString(), "ip", "ua")
		require.Error(t, err)
		require.ErrorIs(t, err, storeErr)
		require.NotErrorIs(t, err, authshared.ErrSessionNotFound)
	})
}
