package password_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/google/uuid"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/domain/auth/password"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/stretchr/testify/require"
)

// makeServiceToken returns a VerificationToken valid for checkFn tests.
// The code hash is computed via authsharedtest.MustHashOTPCode, which uses
// the package-level bcrypt cost set by RunTestMain.
func makeServiceToken(t *testing.T, code string) authshared.VerificationToken {
	t.Helper()
	return authshared.VerificationToken{
		ID:          authsharedtest.RandomUUID(),
		UserID:      authsharedtest.RandomUUID(),
		Email:       "test@example.com",
		CodeHash:    authsharedtest.MustHashOTPCode(t, code),
		Attempts:    0,
		MaxAttempts: 5,
		ExpiresAt:   time.Now().Add(30 * time.Minute),
	}
}

// ── TestService_RequestPasswordReset ─────────────────────────────────────────

func TestService_RequestPasswordReset(t *testing.T) {
	t.Parallel()

	activeUser := password.GetUserForPasswordResetResult{ID: authsharedtest.RandomUUID(), EmailVerified: true, IsActive: true}

	t.Run("success returns raw code with CodeHash populated (DESIGN 1)", func(t *testing.T) {
		t.Parallel()
		var capturedIn password.RequestPasswordResetStoreInput
		store := &authsharedtest.PasswordFakeStorer{
			GetUserForPasswordResetFn: func(_ context.Context, _ string) (password.GetUserForPasswordResetResult, error) {
				return activeUser, nil
			},
			RequestPasswordResetTxFn: func(_ context.Context, in password.RequestPasswordResetStoreInput) error {
				capturedIn = in
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		result, err := svc.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.NotEmpty(t, result.RawCode)
		// DESIGN 1: CodeHash must be folded into the input struct (not a separate param).
		require.NotEmpty(t, capturedIn.CodeHash, "CodeHash must be set in RequestPasswordResetStoreInput")
	})

	t.Run("unknown email returns zero result and nil error (timing invariant)", func(t *testing.T) {
		t.Parallel()
		before := authshared.GetDummyOTPHashCallCount()
		resetTxCalled := false
		store := &authsharedtest.PasswordFakeStorer{
			GetUserForPasswordResetFn: func(_ context.Context, _ string) (password.GetUserForPasswordResetResult, error) {
				return password.GetUserForPasswordResetResult{}, authshared.ErrUserNotFound
			},
			RequestPasswordResetTxFn: func(_ context.Context, _ password.RequestPasswordResetStoreInput) error {
				resetTxCalled = true
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		result, err := svc.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{Email: "ghost@example.com"})
		require.NoError(t, err)
		require.Empty(t, result.RawCode)
		require.False(t, resetTxCalled, "RequestPasswordResetTx must not be called on unknown email path")
		require.Equal(t, before+1, authshared.GetDummyOTPHashCallCount(),
			"GetDummyOTPHash must be called exactly once on the unknown-email path (timing invariant)")
	})

	t.Run("unverified account returns zero result", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			GetUserForPasswordResetFn: func(_ context.Context, _ string) (password.GetUserForPasswordResetResult, error) {
				return password.GetUserForPasswordResetResult{ID: authsharedtest.RandomUUID(), EmailVerified: false, IsActive: true}, nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		result, err := svc.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Empty(t, result.RawCode)
	})

	t.Run("locked account returns zero result", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			GetUserForPasswordResetFn: func(_ context.Context, _ string) (password.GetUserForPasswordResetResult, error) {
				return password.GetUserForPasswordResetResult{ID: authsharedtest.RandomUUID(), EmailVerified: true, IsLocked: true, IsActive: true}, nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		result, err := svc.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Empty(t, result.RawCode)
	})

	t.Run("inactive account returns zero result", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			GetUserForPasswordResetFn: func(_ context.Context, _ string) (password.GetUserForPasswordResetResult, error) {
				return password.GetUserForPasswordResetResult{ID: authsharedtest.RandomUUID(), EmailVerified: true, IsActive: false}, nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		result, err := svc.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Empty(t, result.RawCode)
	})

	t.Run("cooldown active returns zero result", func(t *testing.T) {
		t.Parallel()
		resetTxCalled := false
		store := &authsharedtest.PasswordFakeStorer{
			GetUserForPasswordResetFn: func(_ context.Context, _ string) (password.GetUserForPasswordResetResult, error) {
				return activeUser, nil
			},
			GetPasswordResetTokenCreatedAtFn: func(_ context.Context, _ string) (time.Time, error) {
				return time.Now(), nil // within cooldown window
			},
			RequestPasswordResetTxFn: func(_ context.Context, _ password.RequestPasswordResetStoreInput) error {
				resetTxCalled = true
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		result, err := svc.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Empty(t, result.RawCode)
		require.False(t, resetTxCalled, "RequestPasswordResetTx must not be called during cooldown")
	})

	t.Run("cooldown sentinel returns zero result", func(t *testing.T) {
		t.Parallel()
		resetTxCalled := false
		store := &authsharedtest.PasswordFakeStorer{
			GetUserForPasswordResetFn: func(_ context.Context, _ string) (password.GetUserForPasswordResetResult, error) {
				return activeUser, nil
			},
			RequestPasswordResetTxFn: func(_ context.Context, _ password.RequestPasswordResetStoreInput) error {
				resetTxCalled = true
				return authshared.ErrResetTokenCooldown
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		result, err := svc.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Empty(t, result.RawCode)
		require.True(t, resetTxCalled)
	})

	t.Run("store error is wrapped and returned", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			GetUserForPasswordResetFn: func(_ context.Context, _ string) (password.GetUserForPasswordResetResult, error) {
				return activeUser, nil
			},
			RequestPasswordResetTxFn: func(_ context.Context, _ password.RequestPasswordResetStoreInput) error {
				return authshared.ErrUserNotFound // any error
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{Email: "a@example.com"})
		require.Error(t, err)
	})

	t.Run("GetUserForPasswordReset DB error returns wrapped error", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			GetUserForPasswordResetFn: func(_ context.Context, _ string) (password.GetUserForPasswordResetResult, error) {
				return password.GetUserForPasswordResetResult{}, errors.New("db connection lost")
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{Email: "a@example.com"})
		require.Error(t, err)
		require.ErrorContains(t, err, "password.RequestPasswordReset")
	})

	t.Run("GetPasswordResetTokenCreatedAt DB error returns wrapped cooldown error", func(t *testing.T) {
		t.Parallel()
		activeUser := password.GetUserForPasswordResetResult{
			ID: authsharedtest.RandomUUID(), EmailVerified: true, IsActive: true,
		}
		store := &authsharedtest.PasswordFakeStorer{
			GetUserForPasswordResetFn: func(_ context.Context, _ string) (password.GetUserForPasswordResetResult, error) {
				return activeUser, nil
			},
			GetPasswordResetTokenCreatedAtFn: func(_ context.Context, _ string) (time.Time, error) {
				return time.Time{}, errors.New("redis timeout")
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{Email: "a@example.com"})
		require.Error(t, err)
		require.ErrorContains(t, err, "cooldown check")
	})

	t.Run("RequestPasswordResetTx input TTL equals the service tokenTTL", func(t *testing.T) {
		t.Parallel()
		const wantTTL = 30 * time.Minute
		var capturedIn password.RequestPasswordResetStoreInput
		store := &authsharedtest.PasswordFakeStorer{
			GetUserForPasswordResetFn: func(_ context.Context, _ string) (password.GetUserForPasswordResetResult, error) {
				return password.GetUserForPasswordResetResult{
					ID: authsharedtest.RandomUUID(), EmailVerified: true, IsActive: true,
				}, nil
			},
			RequestPasswordResetTxFn: func(_ context.Context, in password.RequestPasswordResetStoreInput) error {
				capturedIn = in
				return nil
			},
		}
		svc := password.NewService(store, wantTTL)
		_, err := svc.RequestPasswordReset(context.Background(), password.ForgotPasswordInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Equal(t, wantTTL, capturedIn.TTL, "store input TTL must match the service's tokenTTL")
	})
}

// ── TestService_ConsumePasswordResetToken ─────────────────────────────────────

func TestService_ConsumePasswordResetToken(t *testing.T) {
	t.Parallel()

	goodCode := "345678"

	t.Run("success updates password hash with bcrypt hash", func(t *testing.T) {
		t.Parallel()
		token := makeServiceToken(t, goodCode)
		var updatedHash string
		var updateCalled bool
		store := &authsharedtest.PasswordFakeStorer{
			ConsumeAndUpdatePasswordTxFn: func(_ context.Context, in password.ConsumeAndUpdateInput, checkFn func(authshared.VerificationToken) error) ([16]byte, error) {
				if err := checkFn(token); err != nil {
					return [16]byte{}, err
				}
				updatedHash = in.NewHash
				updateCalled = true
				return authsharedtest.RandomUUID(), nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{
			Email:       "a@example.com",
			Code:        goodCode,
			NewPassword: "NewPassw0rd!1",
		})
		require.NoError(t, err)
		require.True(t, updateCalled)
		require.NotEmpty(t, updatedHash)
		// Verify the stored hash is a valid bcrypt hash of the new password.
		require.NoError(t, bcrypt.CompareHashAndPassword([]byte(updatedHash), []byte("NewPassw0rd!1")))
	})

	t.Run("token not found returns ErrTokenNotFound", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			ConsumeAndUpdatePasswordTxFn: func(_ context.Context, _ password.ConsumeAndUpdateInput, _ func(authshared.VerificationToken) error) ([16]byte, error) {
				return [16]byte{}, authshared.ErrTokenNotFound
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{
			Email: "ghost@example.com", Code: "000000", NewPassword: "NewPassw0rd!1",
		})
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("token already used returns ErrTokenAlreadyUsed", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			ConsumeAndUpdatePasswordTxFn: func(_ context.Context, _ password.ConsumeAndUpdateInput, _ func(authshared.VerificationToken) error) ([16]byte, error) {
				return [16]byte{}, authshared.ErrTokenAlreadyUsed
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{
			Email: "a@example.com", Code: goodCode, NewPassword: "NewPassw0rd!1",
		})
		require.ErrorIs(t, err, authshared.ErrTokenAlreadyUsed)
	})

	t.Run("token expired returns ErrTokenExpired", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			ConsumeAndUpdatePasswordTxFn: func(_ context.Context, _ password.ConsumeAndUpdateInput, _ func(authshared.VerificationToken) error) ([16]byte, error) {
				return [16]byte{}, authshared.ErrTokenExpired
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{
			Email: "a@example.com", Code: goodCode, NewPassword: "NewPassw0rd!1",
		})
		require.ErrorIs(t, err, authshared.ErrTokenExpired)
	})

	t.Run("max attempts reached does NOT call IncrementAttemptsTx (BUG 3 regression)", func(t *testing.T) {
		t.Parallel()
		incrementCalled := false
		store := &authsharedtest.PasswordFakeStorer{
			ConsumeAndUpdatePasswordTxFn: func(_ context.Context, _ password.ConsumeAndUpdateInput, _ func(authshared.VerificationToken) error) ([16]byte, error) {
				return [16]byte{}, authshared.ErrTooManyAttempts
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incrementCalled = true
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{
			Email: "a@example.com", Code: "000000", NewPassword: "NewPassw0rd!1",
		})
		require.ErrorIs(t, err, authshared.ErrTooManyAttempts)
		require.False(t, incrementCalled, "IncrementAttemptsTx must NOT be called on ErrTooManyAttempts")
	})

	t.Run("wrong code calls IncrementAttemptsTx and returns ErrInvalidCode (BUG 3 fix)", func(t *testing.T) {
		t.Parallel()
		token := makeServiceToken(t, goodCode)
		incrementCalled := false
		store := &authsharedtest.PasswordFakeStorer{
			ConsumeAndUpdatePasswordTxFn: func(_ context.Context, _ password.ConsumeAndUpdateInput, checkFn func(authshared.VerificationToken) error) ([16]byte, error) {
				return [16]byte{}, checkFn(token) // returns ErrInvalidCode because wrong code below
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incrementCalled = true
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{
			Email: "a@example.com", Code: "000000", NewPassword: "NewPassw0rd!1",
		})
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
		require.True(t, incrementCalled, "IncrementAttemptsTx must be called on wrong code")
	})

	t.Run("weak password after correct code returns validation error without calling store", func(t *testing.T) {
		t.Parallel()
		storeCalled := false
		store := &authsharedtest.PasswordFakeStorer{
			ConsumeAndUpdatePasswordTxFn: func(_ context.Context, _ password.ConsumeAndUpdateInput, _ func(authshared.VerificationToken) error) ([16]byte, error) {
				storeCalled = true
				return [16]byte{}, nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{
			Email: "a@example.com", Code: goodCode, NewPassword: "weak",
		})
		require.Error(t, err)
		require.False(t, storeCalled)
	})

	t.Run("store error is wrapped and returned", func(t *testing.T) {
		t.Parallel()
		token := makeServiceToken(t, goodCode)
		store := &authsharedtest.PasswordFakeStorer{
			ConsumeAndUpdatePasswordTxFn: func(_ context.Context, _ password.ConsumeAndUpdateInput, checkFn func(authshared.VerificationToken) error) ([16]byte, error) {
				if err := checkFn(token); err != nil {
					return [16]byte{}, err
				}
				return [16]byte{}, authshared.ErrUserNotFound
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{
			Email: "a@example.com", Code: goodCode, NewPassword: "NewPassw0rd!1",
		})
		require.Error(t, err)
	})

	t.Run("same password returns ErrSamePassword", func(t *testing.T) {
		t.Parallel()
		token := makeServiceToken(t, goodCode)
		store := &authsharedtest.PasswordFakeStorer{
			ConsumeAndUpdatePasswordTxFn: func(_ context.Context, _ password.ConsumeAndUpdateInput, checkFn func(authshared.VerificationToken) error) ([16]byte, error) {
				if err := checkFn(token); err != nil {
					return [16]byte{}, err
				}
				// Same-password check is now atomic inside the combined TX.
				return [16]byte{}, password.ErrSamePassword
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{
			Email: "a@example.com", Code: goodCode, NewPassword: "NewPassw0rd!1",
		})
		require.ErrorIs(t, err, password.ErrSamePassword)
	})

	t.Run("wrong code IncrementAttemptsTx receives WithoutCancel context", func(t *testing.T) {
		t.Parallel()

		token := makeServiceToken(t, "345678")
		var capturedCtx context.Context

		store := &authsharedtest.PasswordFakeStorer{
			ConsumeAndUpdatePasswordTxFn: func(_ context.Context, _ password.ConsumeAndUpdateInput, checkFn func(authshared.VerificationToken) error) ([16]byte, error) {
				return [16]byte{}, checkFn(token) // wrong code → ErrInvalidCode
			},
			IncrementAttemptsTxFn: func(ctx context.Context, _ authshared.IncrementInput) error {
				capturedCtx = ctx
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{
			Email:       "a@example.com",
			Code:        "000000", // wrong
			NewPassword: "NewPassw0rd!1",
		})
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
		require.NotNil(t, capturedCtx, "IncrementAttemptsTx must be called")
		// context.WithoutCancel produces a context whose Done() is always nil.
		require.Nil(t, capturedCtx.Done(),
			"IncrementAttemptsTx must receive a context.WithoutCancel-derived context (Done() == nil)")
	})

	t.Run("IncrementAttemptsTx failure on wrong code still returns ErrInvalidCode", func(t *testing.T) {
		t.Parallel()
		token := makeServiceToken(t, goodCode)
		store := &authsharedtest.PasswordFakeStorer{
			ConsumeAndUpdatePasswordTxFn: func(_ context.Context, _ password.ConsumeAndUpdateInput, checkFn func(authshared.VerificationToken) error) ([16]byte, error) {
				return [16]byte{}, checkFn(token)
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				return authshared.ErrUserNotFound // simulate failure
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{
			Email: "a@example.com", Code: "000000", NewPassword: "NewPassw0rd!1",
		})
		// The increment failure must be logged but ErrInvalidCode is still returned.
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
	})

	t.Run("IncrementAttemptsTx receives EventPasswordResetAttemptFailed as AttemptEvent", func(t *testing.T) {
		t.Parallel()
		tok := makeServiceToken(t, goodCode)
		var capturedIn authshared.IncrementInput
		store := &authsharedtest.PasswordFakeStorer{
			ConsumeAndUpdatePasswordTxFn: func(_ context.Context, _ password.ConsumeAndUpdateInput, checkFn func(authshared.VerificationToken) error) ([16]byte, error) {
				return [16]byte{}, checkFn(tok) // wrong code → ErrInvalidCode
			},
			IncrementAttemptsTxFn: func(_ context.Context, in authshared.IncrementInput) error {
				capturedIn = in
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, _ = svc.ConsumePasswordResetToken(context.Background(), password.ResetPasswordInput{
			Email: "a@example.com", Code: "000000", NewPassword: "NewPassw0rd!1",
		})
		require.Equal(t, audit.EventPasswordResetAttemptFailed, capturedIn.AttemptEvent,
			"IncrementAttemptsTx must receive EventPasswordResetAttemptFailed")
	})
}

// ── TestService_UpdatePasswordHash ─────────────────────────────────────────

func TestService_UpdatePasswordHash(t *testing.T) {
	t.Parallel()

	uid := uuid.UUID(authsharedtest.RandomUUID()).String()
	validInput := password.ChangePasswordInput{
		UserID:      uid,
		OldPassword: "OldPassw0rd!",
		NewPassword: "NewPassw0rd!1",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	}

	t.Run("success calls UpdatePasswordHashTx with a valid new hash", func(t *testing.T) {
		t.Parallel()
		var capturedHash string
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{PasswordHash: authsharedtest.MustHashPassword(t, "OldPassw0rd!")}, nil
			},
			UpdatePasswordHashTxFn: func(_ context.Context, _ [16]byte, newHash, _, _ string) error {
				capturedHash = newHash
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		err := svc.UpdatePasswordHash(context.Background(), validInput)
		require.NoError(t, err)
		require.NotEmpty(t, capturedHash)
		require.NoError(t, bcrypt.CompareHashAndPassword([]byte(capturedHash), []byte("NewPassw0rd!1")))
	})

	t.Run("wrong old password increments counter and returns ErrInvalidCredentials", func(t *testing.T) {
		t.Parallel()
		incrementCalled := false
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{PasswordHash: authsharedtest.MustHashPassword(t, "DifferentPassword!")}, nil
			},
			IncrementChangePasswordFailuresTxFn: func(_ context.Context, _ [16]byte, _, _ string) (int16, error) {
				incrementCalled = true
				return 1, nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		err := svc.UpdatePasswordHash(context.Background(), validInput)
		require.ErrorIs(t, err, authshared.ErrInvalidCredentials)
		require.True(t, incrementCalled, "IncrementChangePasswordFailuresTx must be called on wrong password")
	})

	t.Run("wrong password IncrementChangePasswordFailuresTx receives WithoutCancel context", func(t *testing.T) {
		t.Parallel()
		var capturedCtx context.Context
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{PasswordHash: authsharedtest.MustHashPassword(t, "WrongOld!")}, nil
			},
			IncrementChangePasswordFailuresTxFn: func(ctx context.Context, _ [16]byte, _, _ string) (int16, error) {
				capturedCtx = ctx
				return 1, nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_ = svc.UpdatePasswordHash(context.Background(), validInput)
		require.NotNil(t, capturedCtx)
		require.Nil(t, capturedCtx.Done(),
			"IncrementChangePasswordFailuresTx must receive a WithoutCancel context")
	})

	t.Run("user not found runs CheckPassword anyway and returns ErrUserNotFound (timing invariant)", func(t *testing.T) {
		t.Parallel()
		auditCalled := false
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{}, authshared.ErrUserNotFound
			},
			WritePasswordChangeFailedAuditTxFn: func(_ context.Context, _ [16]byte, _, _ string) error {
				auditCalled = true
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		err := svc.UpdatePasswordHash(context.Background(), validInput)
		require.ErrorIs(t, err, authshared.ErrUserNotFound)
		require.False(t, auditCalled, "audit must not be written when user is not found")
	})

	t.Run("GetUserPasswordHash DB error returns wrapped error", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{}, errors.New("db gone")
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		err := svc.UpdatePasswordHash(context.Background(), validInput)
		require.Error(t, err)
		require.ErrorContains(t, err, "get password hash")
	})

	t.Run("weak new password returns validation error without calling UpdatePasswordHashTx", func(t *testing.T) {
		t.Parallel()
		updateCalled := false
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{PasswordHash: authsharedtest.MustHashPassword(t, "OldPassw0rd!")}, nil
			},
			UpdatePasswordHashTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error {
				updateCalled = true
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		err := svc.UpdatePasswordHash(context.Background(), password.ChangePasswordInput{
			UserID: uid, OldPassword: "OldPassw0rd!", NewPassword: "weak",
		})
		require.Error(t, err)
		require.False(t, updateCalled)
	})

	t.Run("UpdatePasswordHashTx receives WithoutCancel context", func(t *testing.T) {
		t.Parallel()
		var capturedCtx context.Context
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{PasswordHash: authsharedtest.MustHashPassword(t, "OldPassw0rd!")}, nil
			},
			UpdatePasswordHashTxFn: func(ctx context.Context, _ [16]byte, _, _, _ string) error {
				capturedCtx = ctx
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		err := svc.UpdatePasswordHash(context.Background(), validInput)
		require.NoError(t, err)
		require.NotNil(t, capturedCtx)
		require.Nil(t, capturedCtx.Done(),
			"UpdatePasswordHashTx must receive a WithoutCancel context (Done() == nil)")
	})

	t.Run("UpdatePasswordHashTx error returns wrapped error", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{PasswordHash: authsharedtest.MustHashPassword(t, "OldPassw0rd!")}, nil
			},
			UpdatePasswordHashTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error {
				return errors.New("db gone")
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		err := svc.UpdatePasswordHash(context.Background(), validInput)
		require.Error(t, err)
		require.ErrorContains(t, err, "update password")
	})

	t.Run("invalid userID returns parse error without calling store", func(t *testing.T) {
		t.Parallel()
		called := false
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				called = true
				return password.CurrentCredentials{}, nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		err := svc.UpdatePasswordHash(context.Background(), password.ChangePasswordInput{UserID: "not-a-uuid"})
		require.Error(t, err)
		require.False(t, called)
	})

	t.Run("IncrementChangePasswordFailuresTx error is logged but ErrInvalidCredentials is still returned", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{PasswordHash: authsharedtest.MustHashPassword(t, "WrongOld!")}, nil
			},
			IncrementChangePasswordFailuresTxFn: func(_ context.Context, _ [16]byte, _, _ string) (int16, error) {
				return 0, errors.New("db gone") // must be swallowed
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		err := svc.UpdatePasswordHash(context.Background(), password.ChangePasswordInput{
			UserID:      uid,
			OldPassword: "OldPassw0rd!",
			NewPassword: "NewPassw0rd!1",
		})
		require.ErrorIs(t, err, authshared.ErrInvalidCredentials,
			"increment failure must not replace ErrInvalidCredentials")
	})

	t.Run("5 wrong old_password attempts returns ErrTooManyAttempts", func(t *testing.T) {
		t.Parallel()
		const threshold = 5
		var callCount int
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{PasswordHash: authsharedtest.MustHashPassword(t, "DifferentPassword!")}, nil
			},
			IncrementChangePasswordFailuresTxFn: func(_ context.Context, _ [16]byte, _, _ string) (int16, error) {
				callCount++
				return int16(callCount), nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		input := password.ChangePasswordInput{
			UserID: uid, OldPassword: "OldPassw0rd!", NewPassword: "NewPassw0rd!1",
		}
		// First four attempts must return ErrInvalidCredentials.
		for i := range threshold - 1 {
			err := svc.UpdatePasswordHash(context.Background(), input)
			require.ErrorIs(t, err, authshared.ErrInvalidCredentials,
				"attempt %d must return ErrInvalidCredentials, not ErrTooManyAttempts", i+1)
		}
		// Fifth attempt must return ErrTooManyAttempts.
		err := svc.UpdatePasswordHash(context.Background(), input)
		require.ErrorIs(t, err, authshared.ErrTooManyAttempts,
			"5th wrong attempt must return ErrTooManyAttempts")
	})

	t.Run("same new password returns ErrSamePassword without calling UpdatePasswordHashTx", func(t *testing.T) {
		t.Parallel()
		updateCalled := false
		const samePassword = "OldPassw0rd!"
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{PasswordHash: authsharedtest.MustHashPassword(t, samePassword)}, nil
			},
			UpdatePasswordHashTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error {
				updateCalled = true
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		err := svc.UpdatePasswordHash(context.Background(), password.ChangePasswordInput{
			UserID:      uid,
			OldPassword: samePassword,
			NewPassword: samePassword, // same as current
		})
		require.ErrorIs(t, err, password.ErrSamePassword)
		require.False(t, updateCalled, "UpdatePasswordHashTx must not be called when new == old")
	})

	t.Run("successful change resets failure counter", func(t *testing.T) {
		t.Parallel()
		resetCalled := false
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{PasswordHash: authsharedtest.MustHashPassword(t, "OldPassw0rd!")}, nil
			},
			UpdatePasswordHashTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error {
				return nil
			},
			ResetChangePasswordFailuresTxFn: func(_ context.Context, _ [16]byte) error {
				resetCalled = true
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		err := svc.UpdatePasswordHash(context.Background(), validInput)
		require.NoError(t, err)
		require.True(t, resetCalled, "ResetChangePasswordFailuresTx must be called on success")
	})

	t.Run("ResetChangePasswordFailuresTx error is logged but success is still returned", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			GetUserPasswordHashFn: func(_ context.Context, _ [16]byte) (password.CurrentCredentials, error) {
				return password.CurrentCredentials{PasswordHash: authsharedtest.MustHashPassword(t, "OldPassw0rd!")}, nil
			},
			UpdatePasswordHashTxFn: func(_ context.Context, _ [16]byte, _, _, _ string) error {
				return nil
			},
			ResetChangePasswordFailuresTxFn: func(_ context.Context, _ [16]byte) error {
				return errors.New("db gone") // must be swallowed
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		err := svc.UpdatePasswordHash(context.Background(), validInput)
		require.NoError(t, err, "reset failure must not surface as an error")
	})
}

// ── TestService_VerifyResetCode ─────────────────────────────────────────

func TestService_VerifyResetCode(t *testing.T) {
	t.Parallel()

	const goodCode = "654321"

	t.Run("success returns email", func(t *testing.T) {
		t.Parallel()
		tok := makeServiceToken(t, goodCode)
		store := &authsharedtest.PasswordFakeStorer{
			GetPasswordResetTokenForVerifyFn: func(_ context.Context, _ string) (authshared.VerificationToken, error) {
				return tok, nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		got, err := svc.VerifyResetCode(context.Background(), password.VerifyResetCodeInput{
			Email: tok.Email,
			Code:  goodCode,
		})
		require.NoError(t, err)
		require.Equal(t, tok.Email, got, "success must return the email bound to the token")
	})

	t.Run("token not found calls dummy hash and returns ErrTokenNotFound", func(t *testing.T) {
		t.Parallel()
		before := authshared.GetDummyOTPHashCallCount()
		store := &authsharedtest.PasswordFakeStorer{
			GetPasswordResetTokenForVerifyFn: func(_ context.Context, _ string) (authshared.VerificationToken, error) {
				return authshared.VerificationToken{}, authshared.ErrTokenNotFound
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.VerifyResetCode(context.Background(), password.VerifyResetCodeInput{
			Email: "ghost@example.com",
			Code:  goodCode,
		})
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
		require.Equal(t, before+1, authshared.GetDummyOTPHashCallCount(),
			"GetDummyOTPHash must be called exactly once on the not-found path (timing invariant)")
	})

	t.Run("token expired returns ErrTokenExpired", func(t *testing.T) {
		t.Parallel()
		expiredTok := authshared.VerificationToken{
			ID:          authsharedtest.RandomUUID(),
			UserID:      authsharedtest.RandomUUID(),
			Email:       "test@example.com",
			CodeHash:    authsharedtest.MustHashOTPCode(t, goodCode),
			Attempts:    0,
			MaxAttempts: 5,
			ExpiresAt:   time.Now().Add(-1 * time.Minute), // already expired
		}
		store := &authsharedtest.PasswordFakeStorer{
			GetPasswordResetTokenForVerifyFn: func(_ context.Context, _ string) (authshared.VerificationToken, error) {
				return expiredTok, nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.VerifyResetCode(context.Background(), password.VerifyResetCodeInput{
			Email: expiredTok.Email,
			Code:  goodCode,
		})
		require.ErrorIs(t, err, authshared.ErrTokenExpired)
	})

	t.Run("too many attempts returns ErrTooManyAttempts and does not call IncrementAttemptsTx", func(t *testing.T) {
		t.Parallel()
		exhaustedTok := authshared.VerificationToken{
			ID:          authsharedtest.RandomUUID(),
			UserID:      authsharedtest.RandomUUID(),
			Email:       "test@example.com",
			CodeHash:    authsharedtest.MustHashOTPCode(t, goodCode),
			Attempts:    5,
			MaxAttempts: 5, // budget exhausted
			ExpiresAt:   time.Now().Add(30 * time.Minute),
		}
		incrementCalled := false
		store := &authsharedtest.PasswordFakeStorer{
			GetPasswordResetTokenForVerifyFn: func(_ context.Context, _ string) (authshared.VerificationToken, error) {
				return exhaustedTok, nil
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incrementCalled = true
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.VerifyResetCode(context.Background(), password.VerifyResetCodeInput{
			Email: exhaustedTok.Email,
			Code:  "000000",
		})
		require.ErrorIs(t, err, authshared.ErrTooManyAttempts)
		require.False(t, incrementCalled, "IncrementAttemptsTx must NOT be called when attempt budget is exhausted")
	})

	t.Run("wrong code calls IncrementAttemptsTx and returns ErrInvalidCode", func(t *testing.T) {
		t.Parallel()
		tok := makeServiceToken(t, goodCode)
		incrementCalled := false
		store := &authsharedtest.PasswordFakeStorer{
			GetPasswordResetTokenForVerifyFn: func(_ context.Context, _ string) (authshared.VerificationToken, error) {
				return tok, nil
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incrementCalled = true
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.VerifyResetCode(context.Background(), password.VerifyResetCodeInput{
			Email: tok.Email,
			Code:  "000000", // wrong
		})
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
		require.True(t, incrementCalled, "IncrementAttemptsTx must be called on a wrong code with budget remaining")
	})

	t.Run("wrong code increment uses context.WithoutCancel", func(t *testing.T) {
		t.Parallel()
		tok := makeServiceToken(t, goodCode)
		var capturedCtx context.Context
		store := &authsharedtest.PasswordFakeStorer{
			GetPasswordResetTokenForVerifyFn: func(_ context.Context, _ string) (authshared.VerificationToken, error) {
				return tok, nil
			},
			IncrementAttemptsTxFn: func(ctx context.Context, _ authshared.IncrementInput) error {
				capturedCtx = ctx
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, _ = svc.VerifyResetCode(context.Background(), password.VerifyResetCodeInput{
			Email: tok.Email,
			Code:  "000000", // wrong
		})
		require.NotNil(t, capturedCtx, "IncrementAttemptsTx must be called")
		// context.WithoutCancel returns a context whose Done() channel is always nil.
		require.Nil(t, capturedCtx.Done(),
			"IncrementAttemptsTx must receive a context.WithoutCancel-derived context (Done() == nil)")
	})

	t.Run("wrong code increment failure still returns ErrInvalidCode", func(t *testing.T) {
		t.Parallel()
		tok := makeServiceToken(t, goodCode)
		store := &authsharedtest.PasswordFakeStorer{
			GetPasswordResetTokenForVerifyFn: func(_ context.Context, _ string) (authshared.VerificationToken, error) {
				return tok, nil
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				return errors.New("db timeout")
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.VerifyResetCode(context.Background(), password.VerifyResetCodeInput{
			Email: tok.Email,
			Code:  "000000", // wrong
		})
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
	})

	t.Run("IncrementAttemptsTx receives EventPasswordResetAttemptFailed", func(t *testing.T) {
		t.Parallel()
		tok := makeServiceToken(t, goodCode)
		var capturedIn authshared.IncrementInput
		store := &authsharedtest.PasswordFakeStorer{
			GetPasswordResetTokenForVerifyFn: func(_ context.Context, _ string) (authshared.VerificationToken, error) {
				return tok, nil
			},
			IncrementAttemptsTxFn: func(_ context.Context, in authshared.IncrementInput) error {
				capturedIn = in
				return nil
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, _ = svc.VerifyResetCode(context.Background(), password.VerifyResetCodeInput{
			Email: tok.Email,
			Code:  "000000", // wrong
		})
		require.Equal(t, audit.EventPasswordResetAttemptFailed, capturedIn.AttemptEvent,
			"IncrementAttemptsTx must receive EventPasswordResetAttemptFailed")
	})

	t.Run("store error wraps and returns", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.PasswordFakeStorer{
			GetPasswordResetTokenForVerifyFn: func(_ context.Context, _ string) (authshared.VerificationToken, error) {
				return authshared.VerificationToken{}, errors.New("connection refused")
			},
		}
		svc := password.NewService(store, 15*time.Minute)
		_, err := svc.VerifyResetCode(context.Background(), password.VerifyResetCodeInput{
			Email: "a@example.com",
			Code:  goodCode,
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "password.VerifyResetCode",
			"store errors must be wrapped with the function name")
	})
}
