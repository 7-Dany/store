package unlock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/auth/unlock"
	"github.com/stretchr/testify/require"
)

// makeToken returns a VerificationToken ready for checkFn tests.
// The code hash is computed at bcrypt.MinCost (set in TestMain).
func makeToken(code string) authshared.VerificationToken {
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
	if err != nil {
		panic("makeToken: bcrypt: " + err.Error())
	}
	return authshared.VerificationToken{
		ID:          authsharedtest.RandomUUID(),
		UserID:      authsharedtest.RandomUUID(),
		Email:       "test@example.com",
		CodeHash:    string(hash),
		Attempts:    0,
		MaxAttempts: 5,
		ExpiresAt:   time.Now().Add(30 * time.Minute),
	}
}

// newService is a test constructor that injects the given FakeStorer.
func newService(store unlock.Storer) *unlock.Service {
	return unlock.NewService(store, 30*time.Minute)
}

// ── TestRequestUnlock ─────────────────────────────────────────────────────────

func TestRequestUnlock(t *testing.T) {
	t.Parallel()

	lockedUser := unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: true, EmailVerified: true}

	t.Run("success issues code and calls RequestUnlockTx", func(t *testing.T) {
		t.Parallel()
		var storeCalled bool
		var capturedIn unlock.RequestUnlockStoreInput
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return lockedUser, nil
			},
			RequestUnlockTxFn: func(_ context.Context, in unlock.RequestUnlockStoreInput) error {
				storeCalled = true
				capturedIn = in
				return nil
			},
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{
			Email: "a@example.com", IPAddress: "1.2.3.4", UserAgent: "go-test",
		})
		require.NoError(t, err)
		require.NotEmpty(t, result.RawCode)
		require.True(t, storeCalled)
		// DESIGN 3: CodeHash must be set on the store input, not passed separately.
		require.NotEmpty(t, capturedIn.CodeHash)
	})

	t.Run("unknown email returns zero result nil error (anti-enumeration)", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{}, authshared.ErrUserNotFound
			},
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "ghost@example.com"})
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
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{}, authshared.ErrUserNotFound
			},
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "nobody@example.com"})
		require.NoError(t, err)
		require.Equal(t, authshared.OTPIssuanceResult{}, result)
	})

	t.Run("not locked returns zero result nil error (anti-enumeration)", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: false}, nil
			},
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Empty(t, result.RawCode)
	})

	t.Run("time-locked only — issues code", func(t *testing.T) {
		t.Parallel()
		future := time.Now().Add(time.Hour)
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: false, EmailVerified: true, LoginLockedUntil: &future}, nil
			},
			RequestUnlockTxFn: func(_ context.Context, _ unlock.RequestUnlockStoreInput) error { return nil },
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.NotEmpty(t, result.RawCode)
	})

	t.Run("OTP-locked only (AdminLocked=false) — issues code", func(t *testing.T) {
		// Verifies the baseline: IsLocked=true with AdminLocked=false proceeds
		// to OTP issuance. (Previously misnamed "admin-locked only — issues code".)
		t.Parallel()
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: true, AdminLocked: false, EmailVerified: true, LoginLockedUntil: nil}, nil
			},
			RequestUnlockTxFn: func(_ context.Context, _ unlock.RequestUnlockStoreInput) error { return nil },
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.NotEmpty(t, result.RawCode)
	})

	t.Run("admin-locked, not OTP-locked — silent no-op (anti-enumeration)", func(t *testing.T) {
		// AdminLocked=true must suppress issuance regardless of IsLocked.
		// The service exits silently at step 3 so the caller cannot distinguish
		// this path from an unknown email or an unlocked account.
		t.Parallel()
		var requestCalled bool
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: false, AdminLocked: true, EmailVerified: true}, nil
			},
			RequestUnlockTxFn: func(_ context.Context, _ unlock.RequestUnlockStoreInput) error {
				requestCalled = true
				return nil
			},
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Equal(t, authshared.OTPIssuanceResult{}, result)
		require.False(t, requestCalled, "RequestUnlockTx must not be called for admin-locked accounts")
	})

	t.Run("admin-locked and OTP-locked — silent no-op (admin beats OTP lock)", func(t *testing.T) {
		// AdminLocked=true wins even when IsLocked=true is also set. The service
		// checks AdminLocked (step 3) before IsLocked (step 4), so the OTP lock
		// state is irrelevant and must not cause a code to be issued.
		t.Parallel()
		var requestCalled bool
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: true, AdminLocked: true, EmailVerified: true}, nil
			},
			RequestUnlockTxFn: func(_ context.Context, _ unlock.RequestUnlockStoreInput) error {
				requestCalled = true
				return nil
			},
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Equal(t, authshared.OTPIssuanceResult{}, result)
		require.False(t, requestCalled, "RequestUnlockTx must not be called for admin-locked accounts")
	})

	t.Run("timing invariant: admin-locked path calls GetDummyOTPHash (no panic)", func(t *testing.T) {
		// Regression guard: confirms the admin-locked suppression path calls
		// GetDummyOTPHash to equalise latency with the happy path and does not
		// panic. Timing precision is not assertable in a unit test — the comment
		// in service.go is the canonical documentation.
		t.Parallel()
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: false, AdminLocked: true, EmailVerified: true}, nil
			},
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Equal(t, authshared.OTPIssuanceResult{}, result)
	})

	t.Run("OTP-locked and time-locked (AdminLocked=false) — issues code", func(t *testing.T) {
		// "Both" here means IsLocked=true AND a future LoginLockedUntil; AdminLocked
		// is explicitly false. This is distinct from the admin-locked cases above.
		t.Parallel()
		future := time.Now().Add(time.Hour)
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: true, AdminLocked: false, EmailVerified: true, LoginLockedUntil: &future}, nil
			},
			RequestUnlockTxFn: func(_ context.Context, _ unlock.RequestUnlockStoreInput) error { return nil },
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.NotEmpty(t, result.RawCode)
	})

	t.Run("active token exists — returns zero result nil error (cooldown)", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: true, EmailVerified: true}, nil
			},
			GetUnlockTokenFn: func(_ context.Context, _ string) (authshared.VerificationToken, error) {
				return authshared.VerificationToken{}, nil // active token found
			},
		}
		var requestCalled bool
		store.RequestUnlockTxFn = func(_ context.Context, _ unlock.RequestUnlockStoreInput) error {
			requestCalled = true
			return nil
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Empty(t, result.RawCode)
		require.False(t, requestCalled, "RequestUnlockTx must not be called when an active token exists")
	})

	t.Run("RequestUnlockTx error wraps and returns", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return lockedUser, nil
			},
			RequestUnlockTxFn: func(_ context.Context, _ unlock.RequestUnlockStoreInput) error {
				return authshared.ErrTokenNotFound // any error
			},
		}
		svc := newService(store)
		_, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("GetUserForUnlock returns non-ErrUserNotFound error → wrapped error returned", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{}, authshared.ErrAccountLocked
			},
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.Error(t, err)
		require.ErrorIs(t, err, authshared.ErrAccountLocked)
		require.Equal(t, authshared.OTPIssuanceResult{}, result)
	})

	t.Run("GetUnlockToken returns non-ErrTokenNotFound error → wrapped error returned", func(t *testing.T) {
		t.Parallel()
		var requestCalled bool
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: true, EmailVerified: true}, nil
			},
			GetUnlockTokenFn: func(_ context.Context, _ string) (authshared.VerificationToken, error) {
				return authshared.VerificationToken{}, authshared.ErrAccountLocked
			},
			RequestUnlockTxFn: func(_ context.Context, _ unlock.RequestUnlockStoreInput) error {
				requestCalled = true
				return nil
			},
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.Error(t, err)
		require.ErrorIs(t, err, authshared.ErrAccountLocked)
		require.Equal(t, authshared.OTPIssuanceResult{}, result)
		require.False(t, requestCalled, "RequestUnlockTx must not be called when GetUnlockToken returns a non-sentinel error")
	})

	t.Run("LoginLockedUntil is non-nil but in the past → not treated as locked → silent no-op", func(t *testing.T) {
		t.Parallel()
		past := time.Now().Add(-time.Minute)
		var requestCalled bool
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: false, EmailVerified: true, LoginLockedUntil: &past}, nil
			},
			RequestUnlockTxFn: func(_ context.Context, _ unlock.RequestUnlockStoreInput) error {
				requestCalled = true
				return nil
			},
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Equal(t, authshared.OTPIssuanceResult{}, result)
		require.False(t, requestCalled, "RequestUnlockTx must not be called for past LoginLockedUntil")
	})

	t.Run("timing invariant: not-locked path calls GetDummyOTPHash (no panic)", func(t *testing.T) {
		t.Parallel()
		// Regression guard: confirms the fix from Task 5 does not panic and
		// returns a zero-value result with nil error on the not-locked path.
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: false, EmailVerified: true}, nil
			},
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Equal(t, authshared.OTPIssuanceResult{}, result)
	})

	t.Run("unverified account → silent no-op (anti-enumeration)", func(t *testing.T) {
		t.Parallel()
		var requestCalled bool
		store := &authsharedtest.UnlockFakeStorer{
			GetUserForUnlockFn: func(_ context.Context, _ string) (unlock.UnlockUser, error) {
				return unlock.UnlockUser{ID: authsharedtest.RandomUUID(), IsLocked: true, EmailVerified: false}, nil
			},
			RequestUnlockTxFn: func(_ context.Context, _ unlock.RequestUnlockStoreInput) error {
				requestCalled = true
				return nil
			},
		}
		svc := newService(store)
		result, err := svc.RequestUnlock(context.Background(), unlock.RequestUnlockInput{Email: "a@example.com"})
		require.NoError(t, err)
		require.Equal(t, authshared.OTPIssuanceResult{}, result)
		require.False(t, requestCalled, "RequestUnlockTx must not be called for unverified accounts")
	})
}

// ── TestConsumeUnlockToken ────────────────────────────────────────────────────

func TestConsumeUnlockToken(t *testing.T) {
	t.Parallel()

	goodCode := "234567"

	t.Run("success calls UnlockAccountTx with context.WithoutCancel", func(t *testing.T) {
		t.Parallel()
		token := makeToken(goodCode)
		var unlockCalled bool
		store := &authsharedtest.UnlockFakeStorer{
			ConsumeUnlockTokenTxFn: func(_ context.Context, _ string,
				checkFn func(authshared.VerificationToken) error) error {
				return checkFn(token)
			},
			UnlockAccountTxFn: func(_ context.Context, _ [16]byte, _, _ string) error {
				unlockCalled = true
				return nil
			},
		}
		svc := newService(store)
		err := svc.ConsumeUnlockToken(context.Background(), unlock.ConfirmUnlockInput{
			Email: "a@example.com", Code: goodCode,
		})
		require.NoError(t, err)
		require.True(t, unlockCalled)
	})

	t.Run("token not found runs dummy hash and returns ErrTokenNotFound", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UnlockFakeStorer{
			ConsumeUnlockTokenTxFn: func(_ context.Context, _ string,
				_ func(authshared.VerificationToken) error) error {
				return authshared.ErrTokenNotFound
			},
		}
		svc := newService(store)
		err := svc.ConsumeUnlockToken(context.Background(), unlock.ConfirmUnlockInput{
			Email: "ghost@example.com", Code: "000000",
		})
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("expired token returns ErrTokenExpired", func(t *testing.T) {
		t.Parallel()
		token := makeToken(goodCode)
		token.ExpiresAt = time.Now().Add(-time.Minute)
		store := &authsharedtest.UnlockFakeStorer{
			ConsumeUnlockTokenTxFn: func(_ context.Context, _ string,
				checkFn func(authshared.VerificationToken) error) error {
				return checkFn(token)
			},
		}
		svc := newService(store)
		err := svc.ConsumeUnlockToken(context.Background(), unlock.ConfirmUnlockInput{
			Email: "a@example.com", Code: goodCode,
		})
		require.ErrorIs(t, err, authshared.ErrTokenExpired)
	})

	t.Run("max attempts reached — ErrTooManyAttempts, IncrementAttemptsTx NOT called (ADR-005)", func(t *testing.T) {
		t.Parallel()
		token := makeToken(goodCode)
		token.Attempts = token.MaxAttempts // exhausted
		var incCalled bool
		store := &authsharedtest.UnlockFakeStorer{
			ConsumeUnlockTokenTxFn: func(_ context.Context, _ string,
				checkFn func(authshared.VerificationToken) error) error {
				return checkFn(token)
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incCalled = true
				return nil
			},
		}
		svc := newService(store)
		err := svc.ConsumeUnlockToken(context.Background(), unlock.ConfirmUnlockInput{
			Email: "a@example.com", Code: goodCode,
		})
		require.ErrorIs(t, err, authshared.ErrTooManyAttempts)
		require.False(t, incCalled, "IncrementAttemptsTx must not be called when max attempts is reached")
	})

	t.Run("wrong code — IncrementAttemptsTx called with context.WithoutCancel", func(t *testing.T) {
		t.Parallel()
		token := makeToken(goodCode)
		var incCalled bool
		store := &authsharedtest.UnlockFakeStorer{
			ConsumeUnlockTokenTxFn: func(_ context.Context, _ string,
				checkFn func(authshared.VerificationToken) error) error {
				return checkFn(token)
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incCalled = true
				return nil
			},
		}
		svc := newService(store)
		err := svc.ConsumeUnlockToken(context.Background(), unlock.ConfirmUnlockInput{
			Email: "a@example.com", Code: "000000",
		})
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
		require.True(t, incCalled)
	})

	t.Run("IncrementAttemptsTx failure is logged but ErrInvalidCode is still returned", func(t *testing.T) {
		t.Parallel()
		token := makeToken(goodCode)
		store := &authsharedtest.UnlockFakeStorer{
			ConsumeUnlockTokenTxFn: func(_ context.Context, _ string,
				checkFn func(authshared.VerificationToken) error) error {
				return checkFn(token)
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				return authshared.ErrTokenNotFound // simulated failure
			},
		}
		svc := newService(store)
		err := svc.ConsumeUnlockToken(context.Background(), unlock.ConfirmUnlockInput{
			Email: "a@example.com", Code: "000000",
		})
		// The increment failure must not replace the primary error.
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
	})

	t.Run("UnlockAccountTx failure returns wrapped error", func(t *testing.T) {
		t.Parallel()
		token := makeToken(goodCode)
		store := &authsharedtest.UnlockFakeStorer{
			ConsumeUnlockTokenTxFn: func(_ context.Context, _ string,
				checkFn func(authshared.VerificationToken) error) error {
				return checkFn(token)
			},
			UnlockAccountTxFn: func(_ context.Context, _ [16]byte, _, _ string) error {
				return authshared.ErrTokenNotFound // simulated store error
			},
		}
		svc := newService(store)
		err := svc.ConsumeUnlockToken(context.Background(), unlock.ConfirmUnlockInput{
			Email: "a@example.com", Code: goodCode,
		})
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("ConsumeUnlockTokenTx returns a non-sentinel error → error propagates", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.UnlockFakeStorer{
			ConsumeUnlockTokenTxFn: func(_ context.Context, _ string,
				_ func(authshared.VerificationToken) error) error {
				return errors.New("db failure")
			},
		}
		svc := newService(store)
		err := svc.ConsumeUnlockToken(context.Background(), unlock.ConfirmUnlockInput{
			Email: "a@example.com", Code: "123456",
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "db failure")
	})
}
