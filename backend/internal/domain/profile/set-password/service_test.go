package setpassword_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	setpassword "github.com/7-Dany/store/backend/internal/domain/profile/set-password"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
)

// validUserID is a well-formed UUID string used across test cases.
var validUserID = uuid.New().String()

// validInput is a ready-to-use happy-path input.
var validInput = setpassword.SetPasswordInput{
	UserID:      validUserID,
	NewPassword: "Str0ng!Pass",
	IPAddress:   "127.0.0.1",
	UserAgent:   "test-agent",
}

// oauthUser is a SetPasswordUser whose account has no password (OAuth-only).
var oauthUser = setpassword.SetPasswordUser{HasNoPassword: true}

// ── TestService_SetPassword ───────────────────────────────────────────────────

func TestService_SetPassword(t *testing.T) {
	t.Parallel()

	// ── T-01: Happy path ──────────────────────────────────────────────────────

	t.Run("happy path — hash stored via SetPasswordHashTx", func(t *testing.T) {
		t.Parallel()
		var capturedHash string
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, _ [16]byte) (setpassword.SetPasswordUser, error) {
				return oauthUser, nil
			},
			SetPasswordHashTxFn: func(_ context.Context, _ setpassword.SetPasswordInput, newHash string) error {
				capturedHash = newHash
				return nil
			},
		}
		svc := setpassword.NewService(store)
		err := svc.SetPassword(context.Background(), validInput)
		require.NoError(t, err)
		require.NotEmpty(t, capturedHash)
		// Verify the hash stored is a valid bcrypt hash of the supplied password.
		require.NoError(t, bcrypt.CompareHashAndPassword([]byte(capturedHash), []byte(validInput.NewPassword)),
			"stored hash must be a valid bcrypt hash of the supplied password")
	})

	// ── T-02: User already has a password (service-level guard) ───────────────

	t.Run("user already has password returns ErrPasswordAlreadySet", func(t *testing.T) {
		t.Parallel()
		setTxCalled := false
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, _ [16]byte) (setpassword.SetPasswordUser, error) {
				// HasNoPassword == false — the account already has a password.
				return setpassword.SetPasswordUser{HasNoPassword: false}, nil
			},
			SetPasswordHashTxFn: func(_ context.Context, _ setpassword.SetPasswordInput, _ string) error {
				setTxCalled = true
				return nil
			},
		}
		svc := setpassword.NewService(store)
		err := svc.SetPassword(context.Background(), validInput)
		require.ErrorIs(t, err, setpassword.ErrPasswordAlreadySet)
		require.False(t, setTxCalled, "SetPasswordHashTx must not be called when account already has a password")
	})

	// ── T-03: Concurrency race — SetPasswordHashTx returns ErrPasswordAlreadySet ─

	t.Run("concurrency race — SetPasswordHashTx returns ErrPasswordAlreadySet", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, _ [16]byte) (setpassword.SetPasswordUser, error) {
				// GET sees no password (HasNoPassword: true), but concurrent write wins.
				return oauthUser, nil
			},
			SetPasswordHashTxFn: func(_ context.Context, _ setpassword.SetPasswordInput, _ string) error {
				// WHERE password_hash IS NULL affected 0 rows — concurrent set won.
				return setpassword.ErrPasswordAlreadySet
			},
		}
		svc := setpassword.NewService(store)
		err := svc.SetPassword(context.Background(), validInput)
		require.ErrorIs(t, err, setpassword.ErrPasswordAlreadySet)
	})

	// ── T-04: Ghost user on GET ───────────────────────────────────────────────

	t.Run("GetUserForSetPassword returns ErrUserNotFound — propagated", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, _ [16]byte) (setpassword.SetPasswordUser, error) {
				return setpassword.SetPasswordUser{}, profileshared.ErrUserNotFound
			},
		}
		svc := setpassword.NewService(store)
		err := svc.SetPassword(context.Background(), validInput)
		require.ErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	// ── T-05: Ghost user in TX ────────────────────────────────────────────────

	t.Run("SetPasswordHashTx returns ErrUserNotFound — propagated", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, _ [16]byte) (setpassword.SetPasswordUser, error) {
				return oauthUser, nil
			},
			SetPasswordHashTxFn: func(_ context.Context, _ setpassword.SetPasswordInput, _ string) error {
				// User row deleted between GET and SET (ghost-user path).
				return profileshared.ErrUserNotFound
			},
		}
		svc := setpassword.NewService(store)
		err := svc.SetPassword(context.Background(), validInput)
		require.ErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	// ── T-16: Store error on GetUserForSetPassword ────────────────────────────

	t.Run("GetUserForSetPassword DB error wraps and returns", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, _ [16]byte) (setpassword.SetPasswordUser, error) {
				return setpassword.SetPasswordUser{}, errors.New("connection lost")
			},
		}
		svc := setpassword.NewService(store)
		err := svc.SetPassword(context.Background(), validInput)
		require.Error(t, err)
		require.ErrorContains(t, err, "setpassword.SetPassword: get user:")
	})

	// ── T-17: Store error on SetPasswordHashTx ────────────────────────────────

	t.Run("SetPasswordHashTx DB error wraps and returns", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, _ [16]byte) (setpassword.SetPasswordUser, error) {
				return oauthUser, nil
			},
			SetPasswordHashTxFn: func(_ context.Context, _ setpassword.SetPasswordInput, _ string) error {
				return errors.New("tx aborted")
			},
		}
		svc := setpassword.NewService(store)
		err := svc.SetPassword(context.Background(), validInput)
		require.Error(t, err)
		require.ErrorContains(t, err, "setpassword.SetPassword: set password hash tx:")
	})

	// ── T-21: Error wrapping prefix ───────────────────────────────────────────

	t.Run("wrapped store error contains setpassword.SetPassword prefix", func(t *testing.T) {
		t.Parallel()
		rawErr := errors.New("db gone")
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, _ [16]byte) (setpassword.SetPasswordUser, error) {
				return setpassword.SetPasswordUser{}, rawErr
			},
		}
		svc := setpassword.NewService(store)
		err := svc.SetPassword(context.Background(), validInput)
		require.ErrorContains(t, err, "setpassword.SetPassword:",
			"wrapped error must carry the qualified method name prefix")
	})

	// ── Validation: weak password ─────────────────────────────────────────────
	// These are S-layer cases because ValidatePassword is called by the service
	// (step 4). The handler also validates, but these exercise the service path.

	t.Run("weak password — empty — returns ErrPasswordEmpty without calling store", func(t *testing.T) {
		t.Parallel()
		setTxCalled := false
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, _ [16]byte) (setpassword.SetPasswordUser, error) {
				return oauthUser, nil
			},
			SetPasswordHashTxFn: func(_ context.Context, _ setpassword.SetPasswordInput, _ string) error {
				setTxCalled = true
				return nil
			},
		}
		svc := setpassword.NewService(store)
		in := validInput
		in.NewPassword = ""
		err := svc.SetPassword(context.Background(), in)
		require.ErrorIs(t, err, authshared.ErrPasswordEmpty)
		require.False(t, setTxCalled)
	})

	t.Run("weak password — fails strength check — IsPasswordStrengthError is true", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, _ [16]byte) (setpassword.SetPasswordUser, error) {
				return oauthUser, nil
			},
		}
		svc := setpassword.NewService(store)
		in := validInput
		in.NewPassword = "alllower1!" // no uppercase
		err := svc.SetPassword(context.Background(), in)
		require.True(t, authshared.IsPasswordStrengthError(err),
			"weak password must return a password-strength sentinel error")
	})

	// ── Invalid userID ────────────────────────────────────────────────────────

	t.Run("invalid userID returns parse error without calling store", func(t *testing.T) {
		t.Parallel()
		getCalled := false
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, _ [16]byte) (setpassword.SetPasswordUser, error) {
				getCalled = true
				return setpassword.SetPasswordUser{}, nil
			},
		}
		svc := setpassword.NewService(store)
		in := validInput
		in.UserID = "not-a-uuid"
		err := svc.SetPassword(context.Background(), in)
		require.Error(t, err)
		require.False(t, getCalled, "store must not be called for a malformed user ID")
	})

	// ── SetPasswordHashTx receives the correct input ──────────────────────────

	t.Run("SetPasswordHashTx receives the original input struct", func(t *testing.T) {
		t.Parallel()
		var capturedIn setpassword.SetPasswordInput
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, _ [16]byte) (setpassword.SetPasswordUser, error) {
				return oauthUser, nil
			},
			SetPasswordHashTxFn: func(_ context.Context, in setpassword.SetPasswordInput, _ string) error {
				capturedIn = in
				return nil
			},
		}
		svc := setpassword.NewService(store)
		require.NoError(t, svc.SetPassword(context.Background(), validInput))
		require.Equal(t, validInput.UserID, capturedIn.UserID)
		require.Equal(t, validInput.IPAddress, capturedIn.IPAddress)
		require.Equal(t, validInput.UserAgent, capturedIn.UserAgent)
	})

	// ── GetUserForSetPassword receives the parsed UUID ────────────────────────

	t.Run("GetUserForSetPassword receives the parsed UUID bytes", func(t *testing.T) {
		t.Parallel()
		wantUID := authsharedtest.RandomUUID()
		wantUIDStr := uuid.UUID(wantUID).String()
		var capturedUID [16]byte
		store := &authsharedtest.SetPasswordFakeStorer{
			GetUserForSetPasswordFn: func(_ context.Context, userID [16]byte) (setpassword.SetPasswordUser, error) {
				capturedUID = userID
				return oauthUser, nil
			},
		}
		svc := setpassword.NewService(store)
		in := validInput
		in.UserID = wantUIDStr
		_ = svc.SetPassword(context.Background(), in)
		require.Equal(t, wantUID, capturedUID, "store must receive the parsed UUID bytes")
	})
}
