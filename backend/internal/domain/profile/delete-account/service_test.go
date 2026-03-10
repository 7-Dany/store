package deleteaccount_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	deleteaccount "github.com/7-Dany/store/backend/internal/domain/profile/delete-account"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
)

// ── constants & shared fixtures ───────────────────────────────────────────────

const (
	svcPassword = "Str0ng!Pass"
	svcBotToken = "test-bot-token"
)

var (
	svcUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001").String()
	svcIP     = "127.0.0.1"
	svcUA     = "go-test/1.0"
	svcEmail  = "user@example.com"
	svcOTPTTL = 15 * time.Minute
)

// newSvc returns a Service backed by the given fake storer.
func newSvc(store deleteaccount.Storer) *deleteaccount.Service {
	return deleteaccount.NewService(store, svcOTPTTL, svcBotToken)
}

// userWithPassword returns a DeletionUser whose PasswordHash is a valid bcrypt
// hash of svcPassword (at MinCost, set by TestMain → RunTestMain).
func userWithPassword(t *testing.T) deleteaccount.DeletionUser {
	t.Helper()
	h := authsharedtest.MustHashPassword(t, svcPassword)
	return deleteaccount.DeletionUser{
		ID:           [16]byte(uuid.MustParse(svcUserID)),
		Email:        strPtr(svcEmail),
		PasswordHash: strPtr(h),
	}
}

// userEmailOAuth returns a DeletionUser with an email but no password.
func userEmailOAuth() deleteaccount.DeletionUser {
	return deleteaccount.DeletionUser{
		ID:    [16]byte(uuid.MustParse(svcUserID)),
		Email: strPtr(svcEmail),
	}
}

// userTelegramOnly returns a DeletionUser with no email and no password.
func userTelegramOnly() deleteaccount.DeletionUser {
	return deleteaccount.DeletionUser{
		ID: [16]byte(uuid.MustParse(svcUserID)),
	}
}

// userPendingDeletion returns a DeletionUser whose DeletedAt is already set.
func userPendingDeletion() deleteaccount.DeletionUser {
	ts := time.Now().Add(-time.Hour)
	return deleteaccount.DeletionUser{
		ID:        [16]byte(uuid.MustParse(svcUserID)),
		Email:     strPtr(svcEmail),
		DeletedAt: &ts,
	}
}

// makeValidOTPToken returns a VerificationToken whose CodeHash matches OTPPlaintext.
func makeValidOTPToken(t *testing.T) authshared.VerificationToken {
	t.Helper()
	hash := authsharedtest.MustHashOTPCode(t, authsharedtest.OTPPlaintext)
	return authshared.NewVerificationToken(
		authsharedtest.RandomUUID(),
		[16]byte(uuid.MustParse(svcUserID)),
		svcEmail,
		hash,
		0, // attempts
		3, // maxAttempts (D-19)
		time.Now().Add(time.Hour),
	)
}

// makeExpiredToken returns a VerificationToken past its expiry.
func makeExpiredToken() authshared.VerificationToken {
	return authshared.NewVerificationToken(
		authsharedtest.RandomUUID(),
		[16]byte(uuid.MustParse(svcUserID)),
		svcEmail,
		"bh",
		0, 3,
		time.Now().Add(-time.Hour),
	)
}

// makeExhaustedToken returns a token whose Attempts == MaxAttempts.
func makeExhaustedToken() authshared.VerificationToken {
	return authshared.NewVerificationToken(
		authsharedtest.RandomUUID(),
		[16]byte(uuid.MustParse(svcUserID)),
		svcEmail,
		"bh",
		3, 3, // attempts == maxAttempts
		time.Now().Add(time.Hour),
	)
}

func strPtr(s string) *string { return &s }

func deletionInput() deleteaccount.ScheduleDeletionInput {
	return deleteaccount.ScheduleDeletionInput{UserID: svcUserID, IPAddress: svcIP, UserAgent: svcUA}
}

func cancelInput() deleteaccount.CancelDeletionInput {
	return deleteaccount.CancelDeletionInput{UserID: svcUserID, IPAddress: svcIP, UserAgent: svcUA}
}

// ── TestService_ResolveUserForDeletion ────────────────────────────────────────

func TestService_ResolveUserForDeletion(t *testing.T) {
	t.Parallel()

	t.Run("happy path returns user and auth methods", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (deleteaccount.UserAuthMethods, error) {
				return deleteaccount.UserAuthMethods{HasPassword: false, IdentityCount: 1}, nil
			},
		}
		user, methods, err := newSvc(store).ResolveUserForDeletion(context.Background(), svcUserID)
		require.NoError(t, err)
		require.NotNil(t, user.Email)
		require.False(t, methods.HasPassword)
	})

	// T-06: already pending
	t.Run("T-06: user.DeletedAt set returns ErrAlreadyPendingDeletion", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userPendingDeletion(), nil
			},
		}
		_, _, err := newSvc(store).ResolveUserForDeletion(context.Background(), svcUserID)
		require.ErrorIs(t, err, deleteaccount.ErrAlreadyPendingDeletion)
	})

	t.Run("GetUserForDeletion returns ErrUserNotFound — wrapped as internal error", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return deleteaccount.DeletionUser{}, profileshared.ErrUserNotFound
			},
		}
		_, _, err := newSvc(store).ResolveUserForDeletion(context.Background(), svcUserID)
		require.Error(t, err)
		require.ErrorContains(t, err, "deleteaccount.ResolveUserForDeletion")
	})

	t.Run("GetUserForDeletion DB error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return deleteaccount.DeletionUser{}, errors.New("connection lost")
			},
		}
		_, _, err := newSvc(store).ResolveUserForDeletion(context.Background(), svcUserID)
		require.ErrorContains(t, err, "deleteaccount.ResolveUserForDeletion: get user:")
	})

	t.Run("GetUserAuthMethods error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		authErr := errors.New("db: auth methods query failed")
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (deleteaccount.UserAuthMethods, error) {
				return deleteaccount.UserAuthMethods{}, authErr
			},
		}
		_, _, err := newSvc(store).ResolveUserForDeletion(context.Background(), svcUserID)
		require.ErrorIs(t, err, authErr)
		require.ErrorContains(t, err, "deleteaccount.ResolveUserForDeletion: get auth methods:")
	})

	t.Run("invalid userID returns parse error before any store call", func(t *testing.T) {
		t.Parallel()
		called := false
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				called = true
				return deleteaccount.DeletionUser{}, nil
			},
		}
		_, _, err := newSvc(store).ResolveUserForDeletion(context.Background(), "not-a-uuid")
		require.Error(t, err)
		require.False(t, called)
	})
}

// ── TestService_DeleteWithPassword ────────────────────────────────────────────

func TestService_DeleteWithPassword(t *testing.T) {
	t.Parallel()

	baseInput := deleteaccount.DeleteWithPasswordInput{
		UserID:    svcUserID,
		Password:  svcPassword,
		IPAddress: svcIP,
		UserAgent: svcUA,
	}

	// T-01: happy path
	t.Run("T-01: happy path schedules deletion and returns ScheduledDeletionAt", func(t *testing.T) {
		t.Parallel()
		scheduledAt := time.Now().Add(30 * 24 * time.Hour)
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userWithPassword(t), nil
			},
			ScheduleDeletionTxFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{ScheduledDeletionAt: scheduledAt}, nil
			},
		}
		result, err := newSvc(store).DeleteWithPassword(context.Background(), baseInput)
		require.NoError(t, err)
		require.WithinDuration(t, scheduledAt, result.ScheduledDeletionAt, time.Second)
	})

	// T-06: already pending
	t.Run("T-06: already pending deletion returns ErrAlreadyPendingDeletion", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userPendingDeletion(), nil
			},
		}
		_, err := newSvc(store).DeleteWithPassword(context.Background(), baseInput)
		require.ErrorIs(t, err, deleteaccount.ErrAlreadyPendingDeletion)
	})

	// T-08: wrong password — bcrypt mismatch
	t.Run("T-08: wrong password returns ErrInvalidCredentials", func(t *testing.T) {
		t.Parallel()
		scheduleTxCalled := false
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userWithPassword(t), nil
			},
			ScheduleDeletionTxFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput) (deleteaccount.DeletionScheduled, error) {
				scheduleTxCalled = true
				return deleteaccount.DeletionScheduled{}, nil
			},
		}
		in := baseInput
		in.Password = "WrongP@ss1"
		_, err := newSvc(store).DeleteWithPassword(context.Background(), in)
		require.ErrorIs(t, err, authshared.ErrInvalidCredentials)
		require.False(t, scheduleTxCalled, "ScheduleDeletionTx must not be called on wrong password")
	})

	t.Run("nil PasswordHash (non-password account) returns ErrInvalidCredentials", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil // PasswordHash == nil
			},
		}
		_, err := newSvc(store).DeleteWithPassword(context.Background(), baseInput)
		require.ErrorIs(t, err, authshared.ErrInvalidCredentials)
	})

	// T-24: store error on ScheduleDeletionTx
	t.Run("T-24: ScheduleDeletionTx error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userWithPassword(t), nil
			},
			ScheduleDeletionTxFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, errors.New("db: pool closed")
			},
		}
		_, err := newSvc(store).DeleteWithPassword(context.Background(), baseInput)
		require.Error(t, err)
		require.ErrorContains(t, err, "deleteaccount.DeleteWithPassword")
	})

	t.Run("invalid userID returns parse error before store call", func(t *testing.T) {
		t.Parallel()
		called := false
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				called = true
				return deleteaccount.DeletionUser{}, nil
			},
		}
		in := baseInput
		in.UserID = "bad-uuid"
		_, err := newSvc(store).DeleteWithPassword(context.Background(), in)
		require.Error(t, err)
		require.False(t, called)
	})

	// T-26: error wrapping prefix
	t.Run("T-26: wrapped store error carries deleteaccount.DeleteWithPassword prefix", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return deleteaccount.DeletionUser{}, errors.New("raw db error")
			},
		}
		_, err := newSvc(store).DeleteWithPassword(context.Background(), baseInput)
		require.ErrorContains(t, err, "deleteaccount.DeleteWithPassword:")
	})

	t.Run("ScheduleDeletionTx ErrUserNotFound wraps as internal error", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userWithPassword(t), nil
			},
			ScheduleDeletionTxFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, profileshared.ErrUserNotFound
			},
		}
		in := deleteaccount.DeleteWithPasswordInput{
			UserID: svcUserID, Password: svcPassword, IPAddress: svcIP, UserAgent: svcUA,
		}
		_, err := newSvc(store).DeleteWithPassword(context.Background(), in)
		require.Error(t, err)
		require.ErrorContains(t, err, "deleteaccount.DeleteWithPassword: schedule deletion: user not found:")
	})
}

// ── TestService_InitiateEmailDeletion ─────────────────────────────────────────

func TestService_InitiateEmailDeletion(t *testing.T) {
	t.Parallel()

	// T-02: happy path step 1
	t.Run("T-02: happy path returns OTPIssuanceResult with email and 6-char RawCode", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			SendDeletionOTPTxFn: func(_ context.Context, in deleteaccount.SendDeletionOTPInput) (deleteaccount.SendDeletionOTPResult, error) {
				require.Equal(t, svcEmail, in.Email)
				return deleteaccount.SendDeletionOTPResult{RawCode: "654321"}, nil
			},
		}
		result, err := newSvc(store).InitiateEmailDeletion(context.Background(), deletionInput())
		require.NoError(t, err)
		require.Equal(t, svcEmail, result.Email)
		require.Len(t, result.RawCode, 6)
	})

	// T-06: already pending
	t.Run("T-06: already pending returns ErrAlreadyPendingDeletion", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userPendingDeletion(), nil
			},
		}
		_, err := newSvc(store).InitiateEmailDeletion(context.Background(), deletionInput())
		require.ErrorIs(t, err, deleteaccount.ErrAlreadyPendingDeletion)
	})

	// T-25: store error
	t.Run("T-25: SendDeletionOTPTx error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			SendDeletionOTPTxFn: func(_ context.Context, _ deleteaccount.SendDeletionOTPInput) (deleteaccount.SendDeletionOTPResult, error) {
				return deleteaccount.SendDeletionOTPResult{}, errors.New("tx timeout")
			},
		}
		_, err := newSvc(store).InitiateEmailDeletion(context.Background(), deletionInput())
		require.Error(t, err)
		require.ErrorContains(t, err, "deleteaccount.InitiateEmailDeletion")
	})

	t.Run("user has no email returns internal routing error", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userTelegramOnly(), nil // Email == nil
			},
		}
		_, err := newSvc(store).InitiateEmailDeletion(context.Background(), deletionInput())
		require.Error(t, err)
		require.ErrorContains(t, err, "no email")
	})

	t.Run("TTL from constructor is forwarded to SendDeletionOTPTx", func(t *testing.T) {
		t.Parallel()
		var capturedTTL float64
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			SendDeletionOTPTxFn: func(_ context.Context, in deleteaccount.SendDeletionOTPInput) (deleteaccount.SendDeletionOTPResult, error) {
				capturedTTL = in.TTLSeconds
				return deleteaccount.SendDeletionOTPResult{RawCode: "123456"}, nil
			},
		}
		_, _ = newSvc(store).InitiateEmailDeletion(context.Background(), deletionInput())
		require.InDelta(t, svcOTPTTL.Seconds(), capturedTTL, 1.0)
	})
}

// ── TestService_ConfirmEmailDeletion ──────────────────────────────────────────

func TestService_ConfirmEmailDeletion(t *testing.T) {
	t.Parallel()

	baseInput := deleteaccount.ConfirmOTPDeletionInput{
		UserID:    svcUserID,
		Code:      authsharedtest.OTPPlaintext,
		IPAddress: svcIP,
		UserAgent: svcUA,
	}

	// T-03: happy path step 2
	t.Run("T-03: correct code schedules deletion", func(t *testing.T) {
		t.Parallel()
		scheduledAt := time.Now().Add(30 * 24 * time.Hour)
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
				return makeValidOTPToken(t), nil
			},
			ConfirmOTPDeletionTxFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput, _ [16]byte) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{ScheduledDeletionAt: scheduledAt}, nil
			},
		}
		result, err := newSvc(store).ConfirmEmailDeletion(context.Background(), baseInput)
		require.NoError(t, err)
		require.WithinDuration(t, scheduledAt, result.ScheduledDeletionAt, time.Second)
	})

	// T-06: already pending
	t.Run("T-06: already pending returns ErrAlreadyPendingDeletion", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userPendingDeletion(), nil
			},
		}
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), baseInput)
		require.ErrorIs(t, err, deleteaccount.ErrAlreadyPendingDeletion)
	})

	// T-09: code wrong format — fires before token lookup
	t.Run("T-09: code wrong format returns ErrCodeInvalidFormat without token lookup", func(t *testing.T) {
		t.Parallel()
		tokenLookupCalled := false
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
				tokenLookupCalled = true
				return authshared.VerificationToken{}, nil
			},
		}
		in := baseInput
		in.Code = "abc"
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), in)
		require.ErrorIs(t, err, authshared.ErrCodeInvalidFormat)
		require.False(t, tokenLookupCalled, "GetAccountDeletionToken must not be called on invalid code format")
	})

	// T-10: no active token
	t.Run("T-10: no active token returns ErrTokenNotFound", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			// default GetAccountDeletionTokenFn returns ErrTokenNotFound
		}
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), baseInput)
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	// T-11: expired token → maps to ErrTokenNotFound (authshared convention)
	t.Run("T-11: expired token maps to ErrTokenNotFound", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
				return makeExpiredToken(), nil
			},
		}
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), baseInput)
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	// T-13: attempt ceiling → ErrTooManyAttempts; IncrementAttemptsTx NOT called
	t.Run("T-13: attempt ceiling returns ErrTooManyAttempts without calling IncrementAttemptsTx", func(t *testing.T) {
		t.Parallel()
		incrementCalled := false
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
				return makeExhaustedToken(), nil
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incrementCalled = true
				return nil
			},
		}
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), baseInput)
		require.ErrorIs(t, err, authshared.ErrTooManyAttempts)
		require.False(t, incrementCalled, "IncrementAttemptsTx must NOT be called when attempt ceiling is reached")
	})

	// T-12: wrong code → IncrementAttemptsTx called exactly once
	t.Run("T-12: wrong code calls IncrementAttemptsTx once and returns ErrInvalidCode", func(t *testing.T) {
		t.Parallel()
		incrementCount := 0
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
				return makeValidOTPToken(t), nil
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incrementCount++
				return nil
			},
		}
		in := baseInput
		in.Code = "000000" // wrong but valid 6-digit format
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), in)
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
		require.Equal(t, 1, incrementCount, "IncrementAttemptsTx must be called exactly once on wrong code")
	})

	// T-23: context.WithoutCancel on IncrementAttemptsTx
	t.Run("T-23: IncrementAttemptsTx receives context.WithoutCancel (Done() == nil)", func(t *testing.T) {
		t.Parallel()
		var capturedCtx context.Context
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
				return makeValidOTPToken(t), nil
			},
			IncrementAttemptsTxFn: func(ctx context.Context, _ authshared.IncrementInput) error {
				capturedCtx = ctx
				return nil
			},
		}
		in := baseInput
		in.Code = "000000" // trigger wrong-code path
		newSvc(store).ConfirmEmailDeletion(context.Background(), in) //nolint:errcheck
		require.NotNil(t, capturedCtx)
		require.Nil(t, capturedCtx.Done(),
			"IncrementAttemptsTx must receive a context.WithoutCancel context (Done() == nil)")
	})

	t.Run("GetUserForDeletion raw DB error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db: connection reset")
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return deleteaccount.DeletionUser{}, dbErr
			},
		}
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), deleteaccount.ConfirmOTPDeletionInput{
			UserID: svcUserID, Code: authsharedtest.OTPPlaintext, IPAddress: svcIP, UserAgent: svcUA,
		})
		require.ErrorIs(t, err, dbErr)
		require.ErrorContains(t, err, "deleteaccount.ConfirmEmailDeletion: get user:")
	})

	t.Run("GetAccountDeletionToken DB error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db: deadlock")
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
				return authshared.VerificationToken{}, dbErr
			},
		}
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), deleteaccount.ConfirmOTPDeletionInput{
			UserID: svcUserID, Code: authsharedtest.OTPPlaintext, IPAddress: svcIP, UserAgent: svcUA,
		})
		require.ErrorIs(t, err, dbErr)
		require.ErrorContains(t, err, "deleteaccount.ConfirmEmailDeletion: get token:")
	})

	t.Run("IncrementAttemptsTx error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		incErr := errors.New("db: increment failed")
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
				return makeValidOTPToken(t), nil
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				return incErr
			},
		}
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), deleteaccount.ConfirmOTPDeletionInput{
			UserID: svcUserID, Code: "000000", IPAddress: svcIP, UserAgent: svcUA,
		})
		require.ErrorIs(t, err, incErr)
		require.ErrorContains(t, err, "deleteaccount.ConfirmEmailDeletion: increment attempts:")
	})

	t.Run("ConfirmOTPDeletionTx ErrTokenAlreadyUsed returns ErrTokenAlreadyUsed", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
				return makeValidOTPToken(t), nil
			},
			ConfirmOTPDeletionTxFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput, _ [16]byte) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, authshared.ErrTokenAlreadyUsed
			},
		}
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), deleteaccount.ConfirmOTPDeletionInput{
			UserID: svcUserID, Code: authsharedtest.OTPPlaintext, IPAddress: svcIP, UserAgent: svcUA,
		})
		require.ErrorIs(t, err, authshared.ErrTokenAlreadyUsed)
	})

	t.Run("ConfirmOTPDeletionTx ErrUserNotFound wraps as internal error", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
				return makeValidOTPToken(t), nil
			},
			ConfirmOTPDeletionTxFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput, _ [16]byte) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, profileshared.ErrUserNotFound
			},
		}
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), deleteaccount.ConfirmOTPDeletionInput{
			UserID: svcUserID, Code: authsharedtest.OTPPlaintext, IPAddress: svcIP, UserAgent: svcUA,
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "deleteaccount.ConfirmEmailDeletion: confirm deletion: user not found:")
	})

	t.Run("ConfirmOTPDeletionTx DB error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db: tx aborted")
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
				return makeValidOTPToken(t), nil
			},
			ConfirmOTPDeletionTxFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput, _ [16]byte) (deleteaccount.DeletionScheduled, error) {
				return deleteaccount.DeletionScheduled{}, dbErr
			},
		}
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), deleteaccount.ConfirmOTPDeletionInput{
			UserID: svcUserID, Code: authsharedtest.OTPPlaintext, IPAddress: svcIP, UserAgent: svcUA,
		})
		require.ErrorIs(t, err, dbErr)
		require.ErrorContains(t, err, "deleteaccount.ConfirmEmailDeletion: confirm deletion:")
	})

	t.Run("invalid userID returns parse error before any store call", func(t *testing.T) {
		t.Parallel()
		called := false
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				called = true
				return deleteaccount.DeletionUser{}, nil
			},
		}
		in := baseInput
		in.UserID = "not-a-uuid"
		_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), in)
		require.Error(t, err)
		require.False(t, called)
	})
}

// ── TestService_ConfirmTelegramDeletion ───────────────────────────────────────

func TestService_ConfirmTelegramDeletion(t *testing.T) {
	t.Parallel()

	// Base payload — auth_date within 86400 s window but hash is fake.
	baseInput := deleteaccount.ConfirmTelegramDeletionInput{
		UserID:    svcUserID,
		IPAddress: svcIP,
		UserAgent: svcUA,
		TelegramAuth: deleteaccount.TelegramAuthPayload{
			ID:       12345678,
			AuthDate: time.Now().Unix() - 60,
			Hash:     "fakehash",
		},
	}

	// T-06: already pending — fires before HMAC
	t.Run("T-06: already pending returns ErrAlreadyPendingDeletion before HMAC", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userPendingDeletion(), nil
			},
		}
		_, err := newSvc(store).ConfirmTelegramDeletion(context.Background(), baseInput)
		require.ErrorIs(t, err, deleteaccount.ErrAlreadyPendingDeletion)
	})

	// T-14: HMAC fails — fake hash will never verify against the bot token
	t.Run("T-14: invalid HMAC returns ErrInvalidTelegramAuth", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userTelegramOnly(), nil
			},
		}
		_, err := newSvc(store).ConfirmTelegramDeletion(context.Background(), baseInput)
		require.ErrorIs(t, err, deleteaccount.ErrInvalidTelegramAuth)
	})

	// T-15: auth_date too old — replay protection fires before HMAC
	t.Run("T-15: auth_date > 86400s old returns ErrInvalidTelegramAuth", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userTelegramOnly(), nil
			},
		}
		in := baseInput
		in.TelegramAuth.AuthDate = time.Now().Unix() - 90000 // > 86400 s
		_, err := newSvc(store).ConfirmTelegramDeletion(context.Background(), in)
		require.ErrorIs(t, err, deleteaccount.ErrInvalidTelegramAuth)
	})

	t.Run("replay protection fires before HMAC check", func(t *testing.T) {
		t.Parallel()
		// This test is identical to T-15 but asserts ordering: the old auth_date
		// must return ErrInvalidTelegramAuth even if the hash were somehow valid.
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userTelegramOnly(), nil
			},
		}
		in := baseInput
		in.TelegramAuth.AuthDate = 1 // epoch — always too old
		_, err := newSvc(store).ConfirmTelegramDeletion(context.Background(), in)
		require.ErrorIs(t, err, deleteaccount.ErrInvalidTelegramAuth)
	})

	t.Run("invalid userID returns parse error before store call", func(t *testing.T) {
		t.Parallel()
		called := false
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				called = true
				return deleteaccount.DeletionUser{}, nil
			},
		}
		in := baseInput
		in.UserID = "not-a-uuid"
		_, err := newSvc(store).ConfirmTelegramDeletion(context.Background(), in)
		require.Error(t, err)
		require.False(t, called)
	})
}

// ── TestService_CancelDeletion ────────────────────────────────────────────────

func TestService_CancelDeletion(t *testing.T) {
	t.Parallel()

	// T-27: happy path
	t.Run("T-27: happy path returns nil", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			CancelDeletionTxFn: func(_ context.Context, _ deleteaccount.CancelDeletionInput) error {
				return nil
			},
		}
		require.NoError(t, newSvc(store).CancelDeletion(context.Background(), cancelInput()))
	})

	// T-28: not pending
	t.Run("T-28: not pending returns ErrNotPendingDeletion", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			CancelDeletionTxFn: func(_ context.Context, _ deleteaccount.CancelDeletionInput) error {
				return deleteaccount.ErrNotPendingDeletion
			},
		}
		err := newSvc(store).CancelDeletion(context.Background(), cancelInput())
		require.ErrorIs(t, err, deleteaccount.ErrNotPendingDeletion)
	})

	// T-31: store error wraps
	t.Run("T-31: CancelDeletionTx error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			CancelDeletionTxFn: func(_ context.Context, _ deleteaccount.CancelDeletionInput) error {
				return errors.New("connection reset")
			},
		}
		err := newSvc(store).CancelDeletion(context.Background(), cancelInput())
		require.Error(t, err)
		require.ErrorContains(t, err, "deleteaccount.CancelDeletion")
	})

	t.Run("invalid userID returns parse error without calling store", func(t *testing.T) {
		t.Parallel()
		called := false
		store := &authsharedtest.DeleteAccountFakeStorer{
			CancelDeletionTxFn: func(_ context.Context, _ deleteaccount.CancelDeletionInput) error {
				called = true
				return nil
			},
		}
		in := cancelInput()
		in.UserID = "bad"
		err := newSvc(store).CancelDeletion(context.Background(), in)
		require.Error(t, err)
		require.False(t, called)
	})

	// T-30: service passes original context to CancelDeletionTx unchanged
	// (the store is responsible for context.WithoutCancel on audit writes).
	t.Run("T-30: service passes original context to CancelDeletionTx", func(t *testing.T) {
		t.Parallel()
		type ctxKey struct{}
		sentinelCtx := context.WithValue(context.Background(), ctxKey{}, "marker")
		var receivedCtx context.Context
		store := &authsharedtest.DeleteAccountFakeStorer{
			CancelDeletionTxFn: func(ctx context.Context, _ deleteaccount.CancelDeletionInput) error {
				receivedCtx = ctx
				return nil
			},
		}
		require.NoError(t, newSvc(store).CancelDeletion(sentinelCtx, cancelInput()))
		require.Equal(t, "marker", receivedCtx.Value(ctxKey{}))
	})
}

// ── TestService_GetDeletionMethod ──────────────────────────────────────────

func TestService_GetDeletionMethod(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("password user returns method:password", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (deleteaccount.UserAuthMethods, error) {
				return deleteaccount.UserAuthMethods{HasPassword: true}, nil
			},
		}
		result, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.NoError(t, err)
		require.Equal(t, "password", result.Method)
	})

	t.Run("email-only user returns method:email_otp", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (deleteaccount.UserAuthMethods, error) {
				return deleteaccount.UserAuthMethods{HasPassword: false}, nil
			},
		}
		result, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.NoError(t, err)
		require.Equal(t, "email_otp", result.Method)
	})

	t.Run("Telegram-only user returns method:telegram", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userTelegramOnly(), nil
			},
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (deleteaccount.UserAuthMethods, error) {
				return deleteaccount.UserAuthMethods{HasPassword: false}, nil
			},
		}
		result, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.NoError(t, err)
		require.Equal(t, "telegram", result.Method)
	})

	t.Run("already pending deletion returns ErrAlreadyPendingDeletion", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userPendingDeletion(), nil
			},
		}
		_, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.ErrorIs(t, err, deleteaccount.ErrAlreadyPendingDeletion)
	})

	t.Run("GetUserForDeletion ErrUserNotFound wraps as internal", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return deleteaccount.DeletionUser{}, profileshared.ErrUserNotFound
			},
		}
		_, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.Error(t, err)
		require.ErrorContains(t, err, "deleteaccount.GetDeletionMethod:")
		require.NotErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	t.Run("GetUserForDeletion DB error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db: connection reset")
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return deleteaccount.DeletionUser{}, dbErr
			},
		}
		_, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.ErrorIs(t, err, dbErr)
		require.ErrorContains(t, err, "deleteaccount.GetDeletionMethod: get user:")
	})

	t.Run("GetUserAuthMethods error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		authErr := errors.New("db: auth methods query failed")
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (deleteaccount.UserAuthMethods, error) {
				return deleteaccount.UserAuthMethods{}, authErr
			},
		}
		_, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.ErrorIs(t, err, authErr)
		require.ErrorContains(t, err, "deleteaccount.GetDeletionMethod: get auth methods:")
	})

	t.Run("invalid userID returns parse error before any store call", func(t *testing.T) {
		t.Parallel()
		called := false
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				called = true
				return deleteaccount.DeletionUser{}, nil
			},
		}
		_, err := newSvc(store).GetDeletionMethod(ctx, "not-a-uuid")
		require.Error(t, err)
		require.False(t, called, "store must not be called on invalid userID")
	})
}
