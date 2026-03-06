package verification_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/auth/verification"
	"github.com/stretchr/testify/require"
)

// makeVerificationToken returns a VerificationToken valid for use in service
// unit tests (no DB required). The code hash is computed at bcrypt.MinCost,
// which is safe because RunTestMain calls SetBcryptCostForTest(MinCost).
func makeVerificationToken(t *testing.T, code string) authshared.VerificationToken {
	t.Helper()
	return authshared.VerificationToken{
		ID:          authsharedtest.RandomUUID(),
		UserID:      authsharedtest.RandomUUID(),
		Email:       "test@example.com",
		CodeHash:    authsharedtest.MustHashOTPCode(t, code),
		Attempts:    0,
		MaxAttempts: 5,
		ExpiresAt:   time.Now().Add(time.Hour),
	}
}

// ── TestVerifyEmail ─────────────────────────────────────────────────────────────

func TestVerifyEmail(t *testing.T) {
	t.Parallel()

	goodCode := "123456"

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		token := makeVerificationToken(t, goodCode)
		store := &authsharedtest.VerificationFakeStorer{
			VerifyEmailTxFn: func(_ context.Context, _, _, _ string, checkFn func(authshared.VerificationToken) error) error {
				return checkFn(token)
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		err := svc.VerifyEmail(context.Background(), verification.VerifyEmailInput{
			Email: "a@example.com", Code: goodCode,
		})
		require.NoError(t, err)
	})

	t.Run("token not found runs dummy hash and returns ErrTokenNotFound", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.VerificationFakeStorer{
			VerifyEmailTxFn: func(_ context.Context, _, _, _ string, _ func(authshared.VerificationToken) error) error {
				return authshared.ErrTokenNotFound
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		err := svc.VerifyEmail(context.Background(), verification.VerifyEmailInput{
			Email: "x@example.com", Code: "000000",
		})
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("expired token returns ErrTokenExpired", func(t *testing.T) {
		t.Parallel()
		token := makeVerificationToken(t, goodCode)
		token.ExpiresAt = time.Now().Add(-time.Minute)
		store := &authsharedtest.VerificationFakeStorer{
			VerifyEmailTxFn: func(_ context.Context, _, _, _ string, checkFn func(authshared.VerificationToken) error) error {
				return checkFn(token)
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		err := svc.VerifyEmail(context.Background(), verification.VerifyEmailInput{
			Email: "a@example.com", Code: goodCode,
		})
		require.ErrorIs(t, err, authshared.ErrTokenExpired)
	})

	t.Run("max attempts already reached returns ErrTooManyAttempts without incrementing", func(t *testing.T) {
		t.Parallel()
		token := makeVerificationToken(t, goodCode)
		token.Attempts = token.MaxAttempts // already exhausted
		incrementCalled := false
		store := &authsharedtest.VerificationFakeStorer{
			VerifyEmailTxFn: func(_ context.Context, _, _, _ string, checkFn func(authshared.VerificationToken) error) error {
				return checkFn(token)
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incrementCalled = true
				return nil
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		err := svc.VerifyEmail(context.Background(), verification.VerifyEmailInput{
			Email: "a@example.com", Code: goodCode,
		})
		require.ErrorIs(t, err, authshared.ErrTooManyAttempts)
		require.False(t, incrementCalled, "IncrementAttemptsTx must not be called when Guard 2 fires before hash comparison")
	})

	t.Run("wrong code increments attempts and returns ErrInvalidCode", func(t *testing.T) {
		t.Parallel()
		token := makeVerificationToken(t, goodCode)
		var incrementInput authshared.IncrementInput
		store := &authsharedtest.VerificationFakeStorer{
			VerifyEmailTxFn: func(_ context.Context, _, _, _ string, checkFn func(authshared.VerificationToken) error) error {
				return checkFn(token)
			},
			IncrementAttemptsTxFn: func(_ context.Context, in authshared.IncrementInput) error {
				incrementInput = in
				return nil
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		err := svc.VerifyEmail(context.Background(), verification.VerifyEmailInput{
			Email: "a@example.com", Code: "999999",
		})
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
		require.Equal(t, token.ID, incrementInput.TokenID)
		require.Equal(t, token.UserID, incrementInput.UserID, "increment must carry the token's UserID")
		require.Equal(t, audit.EventVerifyAttemptFailed, incrementInput.AttemptEvent, "increment must record the correct audit event")
	})

	t.Run("ErrAccountLocked is forwarded", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.VerificationFakeStorer{
			VerifyEmailTxFn: func(_ context.Context, _, _, _ string, _ func(authshared.VerificationToken) error) error {
				return authshared.ErrAccountLocked
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		err := svc.VerifyEmail(context.Background(), verification.VerifyEmailInput{
			Email: "a@example.com", Code: goodCode,
		})
		require.ErrorIs(t, err, authshared.ErrAccountLocked)
	})

	t.Run("increment failure is logged but ErrInvalidCode is still returned", func(t *testing.T) {
		t.Parallel()
		token := makeVerificationToken(t, goodCode)
		sentinel := errors.New("db write failed")
		store := &authsharedtest.VerificationFakeStorer{
			VerifyEmailTxFn: func(_ context.Context, _, _, _ string, checkFn func(authshared.VerificationToken) error) error {
				return checkFn(token)
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				return sentinel
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		err := svc.VerifyEmail(context.Background(), verification.VerifyEmailInput{
			Email: "a@example.com", Code: "wrong!",
		})
		// The service must still return ErrInvalidCode; the increment error is
		// logged only (ADR-005: never surface increment failure to the caller).
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
	})

	t.Run("already verified returns ErrAlreadyVerified", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.VerificationFakeStorer{
			VerifyEmailTxFn: func(_ context.Context, _, _, _ string, _ func(authshared.VerificationToken) error) error {
				return authshared.ErrAlreadyVerified
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		err := svc.VerifyEmail(context.Background(), verification.VerifyEmailInput{
			Email: "a@example.com", Code: goodCode,
		})
		require.ErrorIs(t, err, authshared.ErrAlreadyVerified)
	})
}

// ── TestResendVerification ──────────────────────────────────────────────────────

func TestResendVerification(t *testing.T) {
	t.Parallel()

	baseUser := verification.ResendUser{ID: authsharedtest.RandomUUID(), EmailVerified: false, IsLocked: false}

	t.Run("success issues new token and returns raw code", func(t *testing.T) {
		t.Parallel()
		var savedHash string
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn:        func(_ context.Context, _ string) (verification.ResendUser, error) { return baseUser, nil },
			GetLatestTokenCreatedAtFn: func(_ context.Context, _ [16]byte) (time.Time, error) { return time.Time{}, nil },
			ResendVerificationTxFn: func(_ context.Context, _ verification.ResendStoreInput, codeHash string) error {
				savedHash = codeHash
				return nil
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		result, err := svc.ResendVerification(context.Background(), verification.ResendInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.NotEmpty(t, result.RawCode)
		require.NotEmpty(t, savedHash)
		require.NotEqual(t, result.RawCode, savedHash, "raw code must not equal its hash")
	})

	t.Run("unknown email returns zero result and nil error", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn: func(_ context.Context, _ string) (verification.ResendUser, error) {
				return verification.ResendUser{}, authshared.ErrUserNotFound
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		result, err := svc.ResendVerification(context.Background(), verification.ResendInput{Email: "ghost@example.com"})
		require.NoError(t, err)
		require.Empty(t, result.RawCode)
		// Timing: the dummy hash is called on this path so response latency
		// matches the happy path (bcrypt at cost bcrypt.MinCost in tests).
	})

	t.Run("timing invariant: ErrUserNotFound path calls GetDummyOTPHash (no panic)", func(t *testing.T) {
		t.Parallel()
		// This test verifies that the dummy hash call on the unknown-email path
		// does not panic and returns without error (timing assertion is not
		// feasible in a unit test — the comment in service.go is the canonical doc).
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn: func(_ context.Context, _ string) (verification.ResendUser, error) {
				return verification.ResendUser{}, authshared.ErrUserNotFound
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		result, err := svc.ResendVerification(context.Background(), verification.ResendInput{Email: "nobody@example.com"})
		require.NoError(t, err)
		require.Equal(t, authshared.OTPIssuanceResult{}, result)
	})

	t.Run("already verified account returns zero result", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn: func(_ context.Context, _ string) (verification.ResendUser, error) {
				return verification.ResendUser{ID: authsharedtest.RandomUUID(), EmailVerified: true}, nil
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		result, err := svc.ResendVerification(context.Background(), verification.ResendInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Empty(t, result.RawCode)
	})

	t.Run("locked account returns zero result (anti-enumeration)", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn: func(_ context.Context, _ string) (verification.ResendUser, error) {
				return verification.ResendUser{ID: authsharedtest.RandomUUID(), IsLocked: true}, nil
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		result, err := svc.ResendVerification(context.Background(), verification.ResendInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Empty(t, result.RawCode)
	})

	t.Run("cooldown not elapsed returns zero result (anti-enumeration)", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn:        func(_ context.Context, _ string) (verification.ResendUser, error) { return baseUser, nil },
			GetLatestTokenCreatedAtFn: func(_ context.Context, _ [16]byte) (time.Time, error) { return time.Now(), nil },
		}
		svc := verification.NewService(store, 15*time.Minute)
		result, err := svc.ResendVerification(context.Background(), verification.ResendInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Empty(t, result.RawCode)
	})

	t.Run("timing invariant: raw code and hash are distinct", func(t *testing.T) {
		t.Parallel()
		var savedHash string
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn:        func(_ context.Context, _ string) (verification.ResendUser, error) { return baseUser, nil },
			GetLatestTokenCreatedAtFn: func(_ context.Context, _ [16]byte) (time.Time, error) { return time.Time{}, nil },
			ResendVerificationTxFn: func(_ context.Context, _ verification.ResendStoreInput, codeHash string) error {
				savedHash = codeHash
				return nil
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		result, err := svc.ResendVerification(context.Background(), verification.ResendInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.NotEqual(t, result.RawCode, savedHash, "raw code must not equal its bcrypt hash")
	})

	t.Run("ResendVerificationTx called with correct user ID", func(t *testing.T) {
		t.Parallel()
		var calledWith verification.ResendStoreInput
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn:        func(_ context.Context, _ string) (verification.ResendUser, error) { return baseUser, nil },
			GetLatestTokenCreatedAtFn: func(_ context.Context, _ [16]byte) (time.Time, error) { return time.Time{}, nil },
			ResendVerificationTxFn: func(_ context.Context, in verification.ResendStoreInput, _ string) error {
				calledWith = in
				return nil
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		_, err := svc.ResendVerification(context.Background(), verification.ResendInput{
			Email:     "a@example.com",
			IPAddress: "1.2.3.4",
			UserAgent: "test-agent",
		})
		require.NoError(t, err)
		require.Equal(t, baseUser.ID, calledWith.UserID)
		require.Equal(t, "a@example.com", calledWith.Email)
		require.Equal(t, "1.2.3.4", calledWith.IPAddress, "IPAddress must be forwarded to the store")
		require.Equal(t, "test-agent", calledWith.UserAgent, "UserAgent must be forwarded to the store")
	})

	t.Run("GetUserForResend non-ErrUserNotFound error is wrapped and returned", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("connection reset")
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn: func(_ context.Context, _ string) (verification.ResendUser, error) {
				return verification.ResendUser{}, dbErr
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		_, err := svc.ResendVerification(context.Background(), verification.ResendInput{Email: "a@example.com"})
		require.ErrorIs(t, err, dbErr)
	})

	t.Run("GetLatestTokenCreatedAt error is wrapped and returned", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("timeout reading token row")
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn: func(_ context.Context, _ string) (verification.ResendUser, error) {
				return baseUser, nil
			},
			GetLatestTokenCreatedAtFn: func(_ context.Context, _ [16]byte) (time.Time, error) {
				return time.Time{}, dbErr
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		_, err := svc.ResendVerification(context.Background(), verification.ResendInput{Email: "a@example.com"})
		require.ErrorIs(t, err, dbErr)
	})

	t.Run("ResendVerificationTx error is wrapped and returned", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("tx commit failed")
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn: func(_ context.Context, _ string) (verification.ResendUser, error) {
				return baseUser, nil
			},
			GetLatestTokenCreatedAtFn: func(_ context.Context, _ [16]byte) (time.Time, error) {
				return time.Time{}, nil
			},
			ResendVerificationTxFn: func(_ context.Context, _ verification.ResendStoreInput, _ string) error {
				return dbErr
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		_, err := svc.ResendVerification(context.Background(), verification.ResendInput{Email: "a@example.com"})
		require.ErrorIs(t, err, dbErr)
	})

	t.Run("TTL forwarded to ResendVerificationTx", func(t *testing.T) {
		t.Parallel()
		var calledWith verification.ResendStoreInput
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn:        func(_ context.Context, _ string) (verification.ResendUser, error) { return baseUser, nil },
			GetLatestTokenCreatedAtFn: func(_ context.Context, _ [16]byte) (time.Time, error) { return time.Time{}, nil },
			ResendVerificationTxFn: func(_ context.Context, in verification.ResendStoreInput, _ string) error {
				calledWith = in
				return nil
			},
		}
		svc := verification.NewService(store, 30*time.Minute)
		_, err := svc.ResendVerification(context.Background(), verification.ResendInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Equal(t, 30*time.Minute, calledWith.TTL)
	})

	t.Run("GetLatestTokenCreatedAt zero time allows immediate resend", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.VerificationFakeStorer{
			GetUserForResendFn: func(_ context.Context, _ string) (verification.ResendUser, error) { return baseUser, nil },
			GetLatestTokenCreatedAtFn: func(_ context.Context, _ [16]byte) (time.Time, error) {
				return time.Time{}, nil
			},
			ResendVerificationTxFn: func(_ context.Context, _ verification.ResendStoreInput, _ string) error {
				return nil
			},
		}
		svc := verification.NewService(store, 15*time.Minute)
		result, err := svc.ResendVerification(context.Background(), verification.ResendInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.NotEmpty(t, result.RawCode)
	})
}

// ── TestVerifyEmail — additional sub-tests (Phase 2-E) ─────────────────────────

func TestVerifyEmail_ErrTokenAlreadyUsed(t *testing.T) {
	t.Parallel()
	store := &authsharedtest.VerificationFakeStorer{
		VerifyEmailTxFn: func(_ context.Context, _, _, _ string, _ func(authshared.VerificationToken) error) error {
			return authshared.ErrTokenAlreadyUsed
		},
	}
	svc := verification.NewService(store, 15*time.Minute)
	err := svc.VerifyEmail(context.Background(), verification.VerifyEmailInput{
		Email: "a@example.com", Code: "123456",
	})
	require.ErrorIs(t, err, authshared.ErrTokenAlreadyUsed)
}

func TestVerifyEmail_IncrementFnReceivesDetachedContext(t *testing.T) {
	t.Parallel()
	goodCode := "123456"
	token := makeVerificationToken(t, goodCode)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling service so the request context is already done

	var capturedCtx context.Context
	store := &authsharedtest.VerificationFakeStorer{
		VerifyEmailTxFn: func(_ context.Context, _, _, _ string, checkFn func(authshared.VerificationToken) error) error {
			return checkFn(token)
		},
		IncrementAttemptsTxFn: func(incCtx context.Context, _ authshared.IncrementInput) error {
			capturedCtx = incCtx
			return nil
		},
	}
	svc := verification.NewService(store, 15*time.Minute)
	_ = svc.VerifyEmail(ctx, verification.VerifyEmailInput{
		Email: "a@example.com", Code: "wrong",
	})
	require.NotNil(t, capturedCtx)
	// The context passed to incrementFn must not be the cancelled request context.
	require.Nil(t, context.Cause(capturedCtx))
}
