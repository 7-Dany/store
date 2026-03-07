package username_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/7-Dany/store/backend/internal/domain/profile/username"
)

// validUID is a fixed UUID used as the authenticated user's ID across tests.
var validUID = [16]byte(uuid.MustParse("00000000-0000-0000-0000-000000000001"))

// validUpdateInput is a ready-to-use happy-path UpdateUsername input.
var validUpdateInput = username.UpdateUsernameInput{
	UserID:    validUID,
	Username:  "alice_wonder",
	IPAddress: "127.0.0.1",
	UserAgent: "test-agent",
}

// ── TestService_CheckUsernameAvailable ────────────────────────────────────────

func TestService_CheckUsernameAvailable(t *testing.T) {
	t.Parallel()

	// ── T-01: Happy path — username available ─────────────────────────────────

	t.Run("available username returns (true, nil)", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UsernameFakeStorer{
			CheckUsernameAvailableFn: func(_ context.Context, _ string) (bool, error) {
				return true, nil
			},
		}
		svc := username.NewService(store)
		available, err := svc.CheckUsernameAvailable(context.Background(), "alice_wonder")
		require.NoError(t, err)
		require.True(t, available)
	})

	// ── T-02: Happy path — username taken ────────────────────────────────────

	t.Run("taken username returns (false, nil)", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UsernameFakeStorer{
			CheckUsernameAvailableFn: func(_ context.Context, _ string) (bool, error) {
				return false, nil
			},
		}
		svc := username.NewService(store)
		available, err := svc.CheckUsernameAvailable(context.Background(), "alice_wonder")
		require.NoError(t, err)
		require.False(t, available)
	})

	// ── T-10: Store error → (false, wrapped error) ────────────────────────────

	t.Run("store error returns (false, wrapped error)", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UsernameFakeStorer{
			CheckUsernameAvailableFn: func(_ context.Context, _ string) (bool, error) {
				return false, errors.New("connection reset")
			},
		}
		svc := username.NewService(store)
		available, err := svc.CheckUsernameAvailable(context.Background(), "alice_wonder")
		require.Error(t, err)
		require.False(t, available, "must return false on store error")
	})

	// ── T-11: Error wrapping prefix ───────────────────────────────────────────

	t.Run("store error wraps with username.CheckUsernameAvailable prefix", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UsernameFakeStorer{
			CheckUsernameAvailableFn: func(_ context.Context, _ string) (bool, error) {
				return false, errors.New("db gone")
			},
		}
		svc := username.NewService(store)
		_, err := svc.CheckUsernameAvailable(context.Background(), "alice_wonder")
		require.ErrorContains(t, err, "username.CheckUsernameAvailable:",
			"wrapped error must carry the qualified method name prefix")
	})

	// ── Normalisation passthrough ─────────────────────────────────────────────

	t.Run("store receives normalised (lowercased, trimmed) username", func(t *testing.T) {
		t.Parallel()
		var capturedUsername string
		store := &authsharedtest.UsernameFakeStorer{
			CheckUsernameAvailableFn: func(_ context.Context, uname string) (bool, error) {
				capturedUsername = uname
				return true, nil
			},
		}
		svc := username.NewService(store)
		_, err := svc.CheckUsernameAvailable(context.Background(), "  Alice_Wonder  ")
		require.NoError(t, err)
		require.Equal(t, "alice_wonder", capturedUsername,
			"service must normalise before passing to store")
	})

	// ── Validation short-circuits before store ────────────────────────────────

	t.Run("validation error short-circuits before store call", func(t *testing.T) {
		t.Parallel()
		storeCalled := false
		store := &authsharedtest.UsernameFakeStorer{
			CheckUsernameAvailableFn: func(_ context.Context, _ string) (bool, error) {
				storeCalled = true
				return false, nil
			},
		}
		svc := username.NewService(store)
		_, err := svc.CheckUsernameAvailable(context.Background(), "al") // too short
		require.Error(t, err, "short username must produce a validation error")
		require.False(t, storeCalled, "store must not be called when validation fails")
	})

	// ── Validation: empty username ────────────────────────────────────────────

	t.Run("empty username returns ErrUsernameEmpty", func(t *testing.T) {
		t.Parallel()
		svc := username.NewService(&authsharedtest.UsernameFakeStorer{})
		_, err := svc.CheckUsernameAvailable(context.Background(), "")
		require.ErrorIs(t, err, username.ErrUsernameEmpty)
	})

	// ── Validation: username too short ────────────────────────────────────────

	t.Run("username too short returns ErrUsernameTooShort", func(t *testing.T) {
		t.Parallel()
		svc := username.NewService(&authsharedtest.UsernameFakeStorer{})
		_, err := svc.CheckUsernameAvailable(context.Background(), "ab")
		require.ErrorIs(t, err, username.ErrUsernameTooShort)
	})

	// ── Validation: username too long ─────────────────────────────────────────

	t.Run("username too long returns ErrUsernameTooLong", func(t *testing.T) {
		t.Parallel()
		svc := username.NewService(&authsharedtest.UsernameFakeStorer{})
		_, err := svc.CheckUsernameAvailable(context.Background(), "a_very_long_username_that_exceeds_30")
		require.ErrorIs(t, err, username.ErrUsernameTooLong)
	})

	// ── Validation: invalid charset ───────────────────────────────────────────

	t.Run("username with invalid chars returns ErrUsernameInvalidChars", func(t *testing.T) {
		t.Parallel()
		svc := username.NewService(&authsharedtest.UsernameFakeStorer{})
		_, err := svc.CheckUsernameAvailable(context.Background(), "al!ce")
		require.ErrorIs(t, err, username.ErrUsernameInvalidChars)
	})

	// ── Validation: leading underscore ───────────────────────────────────────

	t.Run("leading underscore returns ErrUsernameInvalidFormat", func(t *testing.T) {
		t.Parallel()
		svc := username.NewService(&authsharedtest.UsernameFakeStorer{})
		_, err := svc.CheckUsernameAvailable(context.Background(), "_alice")
		require.ErrorIs(t, err, username.ErrUsernameInvalidFormat)
	})

	// ── Validation: trailing underscore ──────────────────────────────────────

	t.Run("trailing underscore returns ErrUsernameInvalidFormat", func(t *testing.T) {
		t.Parallel()
		svc := username.NewService(&authsharedtest.UsernameFakeStorer{})
		_, err := svc.CheckUsernameAvailable(context.Background(), "alice_")
		require.ErrorIs(t, err, username.ErrUsernameInvalidFormat)
	})

	// ── Validation: consecutive underscores ──────────────────────────────────

	t.Run("consecutive underscores returns ErrUsernameInvalidFormat", func(t *testing.T) {
		t.Parallel()
		svc := username.NewService(&authsharedtest.UsernameFakeStorer{})
		_, err := svc.CheckUsernameAvailable(context.Background(), "al__ice")
		require.ErrorIs(t, err, username.ErrUsernameInvalidFormat)
	})
}

// ── TestService_UpdateUsername ────────────────────────────────────────────────

func TestService_UpdateUsername(t *testing.T) {
	t.Parallel()

	// ── T-15: Happy path ──────────────────────────────────────────────────────

	t.Run("happy path — UpdateUsernameTx called with normalised input", func(t *testing.T) {
		t.Parallel()
		var capturedIn username.UpdateUsernameInput
		store := &authsharedtest.UsernameFakeStorer{
			UpdateUsernameTxFn: func(_ context.Context, in username.UpdateUsernameInput) error {
				capturedIn = in
				return nil
			},
		}
		svc := username.NewService(store)
		err := svc.UpdateUsername(context.Background(), validUpdateInput)
		require.NoError(t, err)
		require.Equal(t, "alice_wonder", capturedIn.Username,
			"store must receive the normalised username")
		require.Equal(t, validUpdateInput.UserID, capturedIn.UserID)
		require.Equal(t, validUpdateInput.IPAddress, capturedIn.IPAddress)
		require.Equal(t, validUpdateInput.UserAgent, capturedIn.UserAgent)
	})

	// ── T-26: Store error wraps ───────────────────────────────────────────────

	t.Run("unexpected store error wraps with username.UpdateUsername prefix", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UsernameFakeStorer{
			UpdateUsernameTxFn: func(_ context.Context, _ username.UpdateUsernameInput) error {
				return errors.New("tx aborted")
			},
		}
		svc := username.NewService(store)
		err := svc.UpdateUsername(context.Background(), validUpdateInput)
		require.ErrorContains(t, err, "username.UpdateUsername:",
			"wrapped store error must carry the qualified method name prefix")
	})

	// ── T-27: ErrSameUsername propagated unwrapped ────────────────────────────

	t.Run("store returns ErrSameUsername — propagated unwrapped", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UsernameFakeStorer{
			UpdateUsernameTxFn: func(_ context.Context, _ username.UpdateUsernameInput) error {
				return username.ErrSameUsername
			},
		}
		svc := username.NewService(store)
		err := svc.UpdateUsername(context.Background(), validUpdateInput)
		require.ErrorIs(t, err, username.ErrSameUsername)
	})

	// ── T-28: ErrUsernameTaken propagated unwrapped ───────────────────────────

	t.Run("store returns ErrUsernameTaken — propagated unwrapped", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UsernameFakeStorer{
			UpdateUsernameTxFn: func(_ context.Context, _ username.UpdateUsernameInput) error {
				return username.ErrUsernameTaken
			},
		}
		svc := username.NewService(store)
		err := svc.UpdateUsername(context.Background(), validUpdateInput)
		require.ErrorIs(t, err, username.ErrUsernameTaken)
	})

	// ── ErrUserNotFound propagated unwrapped ──────────────────────────────────

	t.Run("store returns ErrUserNotFound — propagated unwrapped", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UsernameFakeStorer{
			UpdateUsernameTxFn: func(_ context.Context, _ username.UpdateUsernameInput) error {
				return profileshared.ErrUserNotFound
			},
		}
		svc := username.NewService(store)
		err := svc.UpdateUsername(context.Background(), validUpdateInput)
		require.ErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	// ── Normalisation passthrough ─────────────────────────────────────────────

	t.Run("store receives normalised (lowercased, trimmed) username", func(t *testing.T) {
		t.Parallel()
		var capturedUsername string
		store := &authsharedtest.UsernameFakeStorer{
			UpdateUsernameTxFn: func(_ context.Context, in username.UpdateUsernameInput) error {
				capturedUsername = in.Username
				return nil
			},
		}
		svc := username.NewService(store)
		in := validUpdateInput
		in.Username = "  Alice_Wonder  "
		err := svc.UpdateUsername(context.Background(), in)
		require.NoError(t, err)
		require.Equal(t, "alice_wonder", capturedUsername)
	})

	// ── Validation short-circuits before store ────────────────────────────────

	t.Run("validation error short-circuits before store call", func(t *testing.T) {
		t.Parallel()
		storeCalled := false
		store := &authsharedtest.UsernameFakeStorer{
			UpdateUsernameTxFn: func(_ context.Context, _ username.UpdateUsernameInput) error {
				storeCalled = true
				return nil
			},
		}
		svc := username.NewService(store)
		in := validUpdateInput
		in.Username = "al" // too short
		err := svc.UpdateUsername(context.Background(), in)
		require.Error(t, err, "short username must produce a validation error")
		require.False(t, storeCalled, "store must not be called when validation fails")
	})

	// ── Validation: empty username ────────────────────────────────────────────

	t.Run("empty username returns ErrUsernameEmpty without calling store", func(t *testing.T) {
		t.Parallel()
		storeCalled := false
		store := &authsharedtest.UsernameFakeStorer{
			UpdateUsernameTxFn: func(_ context.Context, _ username.UpdateUsernameInput) error {
				storeCalled = true
				return nil
			},
		}
		svc := username.NewService(store)
		in := validUpdateInput
		in.Username = ""
		err := svc.UpdateUsername(context.Background(), in)
		require.ErrorIs(t, err, username.ErrUsernameEmpty)
		require.False(t, storeCalled)
	})

	// ── Validation: leading underscore ───────────────────────────────────────

	t.Run("leading underscore returns ErrUsernameInvalidFormat", func(t *testing.T) {
		t.Parallel()
		svc := username.NewService(&authsharedtest.UsernameFakeStorer{})
		in := validUpdateInput
		in.Username = "_alice"
		err := svc.UpdateUsername(context.Background(), in)
		require.ErrorIs(t, err, username.ErrUsernameInvalidFormat)
	})

	// ── Validation: trailing underscore ──────────────────────────────────────

	t.Run("trailing underscore returns ErrUsernameInvalidFormat", func(t *testing.T) {
		t.Parallel()
		svc := username.NewService(&authsharedtest.UsernameFakeStorer{})
		in := validUpdateInput
		in.Username = "alice_"
		err := svc.UpdateUsername(context.Background(), in)
		require.ErrorIs(t, err, username.ErrUsernameInvalidFormat)
	})

	// ── Validation: consecutive underscores ──────────────────────────────────

	t.Run("consecutive underscores returns ErrUsernameInvalidFormat", func(t *testing.T) {
		t.Parallel()
		svc := username.NewService(&authsharedtest.UsernameFakeStorer{})
		in := validUpdateInput
		in.Username = "al__ice"
		err := svc.UpdateUsername(context.Background(), in)
		require.ErrorIs(t, err, username.ErrUsernameInvalidFormat)
	})

	// ── UserID bytes passed through unchanged ─────────────────────────────────

	t.Run("UserID bytes are passed through to store unchanged", func(t *testing.T) {
		t.Parallel()
		wantUID := [16]byte(uuid.New())
		var capturedUID [16]byte
		store := &authsharedtest.UsernameFakeStorer{
			UpdateUsernameTxFn: func(_ context.Context, in username.UpdateUsernameInput) error {
				capturedUID = in.UserID
				return nil
			},
		}
		svc := username.NewService(store)
		in := validUpdateInput
		in.UserID = wantUID
		require.NoError(t, svc.UpdateUsername(context.Background(), in))
		require.Equal(t, wantUID, capturedUID, "store must receive the exact UserID bytes")
	})
}
