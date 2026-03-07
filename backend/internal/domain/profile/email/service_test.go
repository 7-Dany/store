package email_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/profile/email"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
)

// testPool is the integration-test database pool. It is nil when TEST_DATABASE_URL
// is not set. All tests in this file are unit tests (no DB required).
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	authsharedtest.RunTestMain(m, &testPool, 0)
}

// ── fixtures ──────────────────────────────────────────────────────────────────

var (
	testUserID    = [16]byte(uuid.MustParse("00000000-0000-0000-0000-000000000001"))
	altUserID     = [16]byte(uuid.MustParse("00000000-0000-0000-0000-000000000002"))
	currentEmail  = "current@example.com"
	newEmail      = "new@example.com"
	validCode     = "123456"
	invalidCode   = "000000"
	testIPAddress = "127.0.0.1"
	testUserAgent = "test-agent/1.0"
)

// newService constructs a Service with the given storer and a fresh in-memory KV store.
func newService(store email.Storer) (*email.Service, kvstore.Store) {
	kv := kvstore.NewInMemoryStore(0)
	svc := email.NewService(store, kv, nil, 15*time.Minute, time.Hour)
	return svc, kv
}

// newServiceWithKV constructs a Service using a caller-supplied KV store.
func newServiceWithKV(store email.Storer, kv kvstore.Store) *email.Service {
	return email.NewService(store, kv, nil, 15*time.Minute, time.Hour)
}

// makeValidVerifyToken returns a VerificationToken whose CodeHash matches validCode.
// Bcrypt cost is lowered by TestMain → RunTestMain → SetBcryptCostForTest(MinCost).
func makeValidVerifyToken(t *testing.T) authshared.VerificationToken {
	t.Helper()
	hash := authsharedtest.MustHashOTPCode(t, validCode)
	return authshared.NewVerificationToken(
		[16]byte(uuid.New()),
		testUserID,
		currentEmail,
		hash,
		0, // attempts
		5, // maxAttempts
		time.Now().Add(time.Hour),
	)
}

// makeExhaustedToken returns a token whose Attempts == MaxAttempts.
func makeExhaustedToken(_ *testing.T) authshared.VerificationToken {
	return authshared.NewVerificationToken(
		[16]byte(uuid.New()),
		testUserID,
		currentEmail,
		"bad-hash", // irrelevant: attempt check fires before hash comparison
		5,          // attempts == maxAttempts
		5,
		time.Now().Add(time.Hour),
	)
}

// ── TestService_RequestEmailChange ────────────────────────────────────────────

func TestService_RequestEmailChange(t *testing.T) {
	t.Parallel()

	baseInput := email.EmailChangeRequestInput{
		UserID:    testUserID,
		NewEmail:  newEmail,
		IPAddress: testIPAddress,
		UserAgent: testUserAgent,
	}

	// ── T-01: Happy path ──────────────────────────────────────────────────────

	t.Run("happy path returns CurrentEmail and 6-digit RawCode", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
			CheckEmailAvailableForChangeFn: func(_ context.Context, _ string, _ [16]byte) (bool, error) {
				return true, nil
			},
		}
		svc, _ := newService(store)
		result, err := svc.RequestEmailChange(context.Background(), baseInput)
		require.NoError(t, err)
		require.Equal(t, currentEmail, result.CurrentEmail)
		require.Len(t, result.RawCode, 6, "RawCode must be exactly 6 digits")
	})

	// ── ErrInvalidEmailFormat ─────────────────────────────────────────────────

	t.Run("invalid email format returns ErrInvalidEmailFormat", func(t *testing.T) {
		t.Parallel()
		svc, _ := newService(&authsharedtest.EmailChangeFakeStorer{})
		in := baseInput
		in.NewEmail = "not-an-email"
		_, err := svc.RequestEmailChange(context.Background(), in)
		require.ErrorIs(t, err, email.ErrInvalidEmailFormat)
	})

	// ── ErrEmailTooLong ───────────────────────────────────────────────────────

	t.Run("email too long returns ErrEmailTooLong", func(t *testing.T) {
		t.Parallel()
		svc, _ := newService(&authsharedtest.EmailChangeFakeStorer{})
		in := baseInput
		// 246 'a' chars + "@example.com" = 258 bytes — exceeds the 254-byte limit.
		in.NewEmail = strings.Repeat("a", 246) + "@example.com"
		_, err := svc.RequestEmailChange(context.Background(), in)
		require.ErrorIs(t, err, email.ErrEmailTooLong)
	})

	// ── Validation short-circuits before store ────────────────────────────────

	t.Run("validation error short-circuits before any store call", func(t *testing.T) {
		t.Parallel()
		storeCalled := false
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				storeCalled = true
				return currentEmail, nil
			},
		}
		svc, _ := newService(store)
		in := baseInput
		in.NewEmail = "bad"
		_, err := svc.RequestEmailChange(context.Background(), in)
		require.Error(t, err)
		require.False(t, storeCalled, "store must not be called when validation fails")
	})

	// ── ErrSameEmail ──────────────────────────────────────────────────────────

	t.Run("new email same as current returns ErrSameEmail", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
		}
		svc, _ := newService(store)
		in := baseInput
		in.NewEmail = currentEmail
		_, err := svc.RequestEmailChange(context.Background(), in)
		require.ErrorIs(t, err, email.ErrSameEmail)
	})

	// ── ErrSameEmail after normalisation ─────────────────────────────────────

	t.Run("new email same as current after normalisation returns ErrSameEmail", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
		}
		svc, _ := newService(store)
		in := baseInput
		in.NewEmail = "  CURRENT@EXAMPLE.COM  "
		_, err := svc.RequestEmailChange(context.Background(), in)
		require.ErrorIs(t, err, email.ErrSameEmail)
	})

	// ── ErrEmailTaken ─────────────────────────────────────────────────────────

	t.Run("unavailable new email returns ErrEmailTaken", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
			CheckEmailAvailableForChangeFn: func(_ context.Context, _ string, _ [16]byte) (bool, error) {
				return false, nil
			},
		}
		svc, _ := newService(store)
		_, err := svc.RequestEmailChange(context.Background(), baseInput)
		require.ErrorIs(t, err, email.ErrEmailTaken)
	})

	// ── ErrCooldownActive ─────────────────────────────────────────────────────

	t.Run("token issued < 2 min ago returns ErrCooldownActive", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
			CheckEmailAvailableForChangeFn: func(_ context.Context, _ string, _ [16]byte) (bool, error) {
				return true, nil
			},
			GetLatestEmailChangeVerifyTokenCreatedAtFn: func(_ context.Context, _ [16]byte) (time.Time, error) {
				return time.Now().Add(-30 * time.Second), nil // 30 s ago — cooldown active
			},
		}
		svc, _ := newService(store)
		_, err := svc.RequestEmailChange(context.Background(), baseInput)
		require.ErrorIs(t, err, email.ErrCooldownActive)
	})

	// ── ErrTokenNotFound from cooldown query ──────────────────────────────────

	t.Run("ErrTokenNotFound from cooldown query is treated as no prior token", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
			CheckEmailAvailableForChangeFn: func(_ context.Context, _ string, _ [16]byte) (bool, error) {
				return true, nil
			},
			// Default returns ErrTokenNotFound → no cooldown applies.
		}
		svc, _ := newService(store)
		result, err := svc.RequestEmailChange(context.Background(), baseInput)
		require.NoError(t, err)
		require.NotEmpty(t, result.RawCode)
	})

	// ── ErrUserNotFound ───────────────────────────────────────────────────────

	t.Run("ErrUserNotFound from GetCurrentUserEmail is propagated", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return "", profileshared.ErrUserNotFound
			},
		}
		svc, _ := newService(store)
		_, err := svc.RequestEmailChange(context.Background(), baseInput)
		require.ErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	// ── T-09: context.WithoutCancel in RequestEmailChangeTx ──────────────────

	t.Run("T-09: RequestEmailChangeTx receives context.WithoutCancel (Done==nil)", func(t *testing.T) {
		t.Parallel()
		var capturedCtx context.Context
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
			CheckEmailAvailableForChangeFn: func(_ context.Context, _ string, _ [16]byte) (bool, error) {
				return true, nil
			},
			RequestEmailChangeTxFn: func(ctx context.Context, _ email.RequestEmailChangeTxInput) error {
				capturedCtx = ctx
				return nil
			},
		}
		svc, _ := newService(store)
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, err := svc.RequestEmailChange(parent, baseInput)
		require.NoError(t, err)
		require.NotNil(t, capturedCtx)
		require.Nil(t, capturedCtx.Done(),
			"context passed to RequestEmailChangeTx must have no Done channel (context.WithoutCancel)")
	})

	// ── Unexpected store error is wrapped ─────────────────────────────────────

	t.Run("unexpected store error from GetCurrentUserEmail is wrapped", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return "", errors.New("db gone")
			},
		}
		svc, _ := newService(store)
		_, err := svc.RequestEmailChange(context.Background(), baseInput)
		require.ErrorContains(t, err, "email.RequestEmailChange:")
	})
}

// ── TestService_VerifyCurrentEmail ────────────────────────────────────────────

func TestService_VerifyCurrentEmail(t *testing.T) {
	t.Parallel()

	baseInput := email.EmailChangeVerifyCurrentInput{
		UserID:    testUserID,
		Code:      validCode,
		IPAddress: testIPAddress,
		UserAgent: testUserAgent,
	}

	// ── T-13: Happy path ──────────────────────────────────────────────────────

	t.Run("happy path returns GrantToken, ExpiresIn=600, NewEmail, NewEmailRawCode", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			VerifyCurrentEmailTxFn: func(_ context.Context, _ email.VerifyCurrentEmailTxInput, checkFn func(authshared.VerificationToken) error) (email.VerifyCurrentEmailStoreResult, error) {
				tok := makeValidVerifyToken(t)
				if err := checkFn(tok); err != nil {
					return email.VerifyCurrentEmailStoreResult{}, err
				}
				return email.VerifyCurrentEmailStoreResult{NewEmail: newEmail}, nil
			},
		}
		svc, _ := newService(store)
		result, err := svc.VerifyCurrentEmail(context.Background(), baseInput)
		require.NoError(t, err)
		require.NotEmpty(t, result.GrantToken, "GrantToken must be a non-empty UUID string")
		require.Equal(t, 600, result.ExpiresIn)
		require.Equal(t, newEmail, result.NewEmail)
		require.Len(t, result.NewEmailRawCode, 6, "NewEmailRawCode must be a 6-digit OTP")
	})

	// ── Grant token is stored in KV ───────────────────────────────────────────

	t.Run("grant token is stored in KV under echg:gt: prefix with correct value", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			VerifyCurrentEmailTxFn: func(_ context.Context, _ email.VerifyCurrentEmailTxInput, checkFn func(authshared.VerificationToken) error) (email.VerifyCurrentEmailStoreResult, error) {
				tok := makeValidVerifyToken(t)
				if err := checkFn(tok); err != nil {
					return email.VerifyCurrentEmailStoreResult{}, err
				}
				return email.VerifyCurrentEmailStoreResult{NewEmail: newEmail}, nil
			},
		}
		kv := kvstore.NewInMemoryStore(0)
		svc := newServiceWithKV(store, kv)
		result, err := svc.VerifyCurrentEmail(context.Background(), baseInput)
		require.NoError(t, err)

		val, kvErr := kv.Get(context.Background(), "echg:gt:"+result.GrantToken)
		require.NoError(t, kvErr, "grant token must be findable in KV")
		require.Contains(t, val, uuid.UUID(testUserID).String())
		require.Contains(t, val, newEmail)
	})

	// ── T-16: ErrInvalidCodeFormat ────────────────────────────────────────────

	t.Run("invalid code format returns ErrInvalidCodeFormat", func(t *testing.T) {
		t.Parallel()
		svc, _ := newService(&authsharedtest.EmailChangeFakeStorer{})
		in := baseInput
		in.Code = "abc"
		_, err := svc.VerifyCurrentEmail(context.Background(), in)
		require.ErrorIs(t, err, email.ErrInvalidCodeFormat)
	})

	// ── T-17: ErrTokenNotFound ────────────────────────────────────────────────

	t.Run("store returns ErrTokenNotFound — propagated", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			VerifyCurrentEmailTxFn: func(_ context.Context, _ email.VerifyCurrentEmailTxInput, _ func(authshared.VerificationToken) error) (email.VerifyCurrentEmailStoreResult, error) {
				return email.VerifyCurrentEmailStoreResult{}, authshared.ErrTokenNotFound
			},
		}
		svc, _ := newService(store)
		_, err := svc.VerifyCurrentEmail(context.Background(), baseInput)
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	// ── ErrTokenExpired ───────────────────────────────────────────────────────

	t.Run("expired token in checkFn returns ErrTokenExpired", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			VerifyCurrentEmailTxFn: func(_ context.Context, _ email.VerifyCurrentEmailTxInput, checkFn func(authshared.VerificationToken) error) (email.VerifyCurrentEmailStoreResult, error) {
				expired := authshared.NewVerificationToken(
					[16]byte(uuid.New()), testUserID, currentEmail,
					"hash", 0, 5,
					time.Now().Add(-time.Hour),
				)
				return email.VerifyCurrentEmailStoreResult{}, checkFn(expired)
			},
		}
		svc, _ := newService(store)
		_, err := svc.VerifyCurrentEmail(context.Background(), baseInput)
		require.ErrorIs(t, err, authshared.ErrTokenExpired)
	})

	// ── T-19: ErrInvalidCode → IncrementAttemptsTx called ────────────────────

	t.Run("T-19: wrong code triggers IncrementAttemptsTx when attempts < max", func(t *testing.T) {
		t.Parallel()
		incrementCalled := false
		store := &authsharedtest.EmailChangeFakeStorer{
			VerifyCurrentEmailTxFn: func(_ context.Context, _ email.VerifyCurrentEmailTxInput, checkFn func(authshared.VerificationToken) error) (email.VerifyCurrentEmailStoreResult, error) {
				// Token with bad hash so checkFn returns ErrInvalidCode.
				tok := authshared.NewVerificationToken(
					[16]byte(uuid.New()), testUserID, currentEmail,
					"not-a-valid-bcrypt-hash",
					0, 5,
					time.Now().Add(time.Hour),
				)
				return email.VerifyCurrentEmailStoreResult{}, checkFn(tok)
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incrementCalled = true
				return nil
			},
		}
		svc, _ := newService(store)
		in := baseInput
		in.Code = invalidCode
		_, err := svc.VerifyCurrentEmail(context.Background(), in)
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
		require.True(t, incrementCalled, "IncrementAttemptsTx must be called when code is wrong and budget remains")
	})

	// ── T-20: ErrTooManyAttempts → no increment ───────────────────────────────

	t.Run("T-20: exhausted token returns ErrTooManyAttempts without calling increment", func(t *testing.T) {
		t.Parallel()
		incrementCalled := false
		store := &authsharedtest.EmailChangeFakeStorer{
			VerifyCurrentEmailTxFn: func(_ context.Context, _ email.VerifyCurrentEmailTxInput, checkFn func(authshared.VerificationToken) error) (email.VerifyCurrentEmailStoreResult, error) {
				tok := makeExhaustedToken(t)
				return email.VerifyCurrentEmailStoreResult{}, checkFn(tok)
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incrementCalled = true
				return nil
			},
		}
		svc, _ := newService(store)
		_, err := svc.VerifyCurrentEmail(context.Background(), baseInput)
		require.ErrorIs(t, err, authshared.ErrTooManyAttempts)
		require.False(t, incrementCalled, "IncrementAttemptsTx must NOT be called when attempts already exhausted")
	})

	// ── T-21a: context.WithoutCancel in VerifyCurrentEmailTx ─────────────────

	t.Run("T-21a: VerifyCurrentEmailTx receives context.WithoutCancel (Done==nil)", func(t *testing.T) {
		t.Parallel()
		var capturedCtx context.Context
		store := &authsharedtest.EmailChangeFakeStorer{
			VerifyCurrentEmailTxFn: func(ctx context.Context, _ email.VerifyCurrentEmailTxInput, checkFn func(authshared.VerificationToken) error) (email.VerifyCurrentEmailStoreResult, error) {
				capturedCtx = ctx
				tok := makeValidVerifyToken(t)
				if err := checkFn(tok); err != nil {
					return email.VerifyCurrentEmailStoreResult{}, err
				}
				return email.VerifyCurrentEmailStoreResult{NewEmail: newEmail}, nil
			},
		}
		svc, _ := newService(store)
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, err := svc.VerifyCurrentEmail(parent, baseInput)
		require.NoError(t, err)
		require.NotNil(t, capturedCtx)
		require.Nil(t, capturedCtx.Done(),
			"context passed to VerifyCurrentEmailTx must be context.WithoutCancel")
	})

	// ── T-21b: context.WithoutCancel in IncrementAttemptsTx ──────────────────

	t.Run("T-21b: IncrementAttemptsTx receives context.WithoutCancel (Done==nil)", func(t *testing.T) {
		t.Parallel()
		var capturedIncCtx context.Context
		store := &authsharedtest.EmailChangeFakeStorer{
			VerifyCurrentEmailTxFn: func(_ context.Context, _ email.VerifyCurrentEmailTxInput, checkFn func(authshared.VerificationToken) error) (email.VerifyCurrentEmailStoreResult, error) {
				tok := authshared.NewVerificationToken(
					[16]byte(uuid.New()), testUserID, currentEmail,
					"not-a-valid-bcrypt-hash", 0, 5, time.Now().Add(time.Hour),
				)
				return email.VerifyCurrentEmailStoreResult{}, checkFn(tok)
			},
			IncrementAttemptsTxFn: func(ctx context.Context, _ authshared.IncrementInput) error {
				capturedIncCtx = ctx
				return nil
			},
		}
		svc, _ := newService(store)
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		in := baseInput
		in.Code = invalidCode
		_, err := svc.VerifyCurrentEmail(parent, in)
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
		require.NotNil(t, capturedIncCtx)
		require.Nil(t, capturedIncCtx.Done(),
			"context passed to IncrementAttemptsTx must be context.WithoutCancel")
	})

	// ── KV Set failure → wrapped error ────────────────────────────────────────

	t.Run("KV Set failure returns wrapped error", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.EmailChangeFakeStorer{
			VerifyCurrentEmailTxFn: func(_ context.Context, _ email.VerifyCurrentEmailTxInput, checkFn func(authshared.VerificationToken) error) (email.VerifyCurrentEmailStoreResult, error) {
				tok := makeValidVerifyToken(t)
				if err := checkFn(tok); err != nil {
					return email.VerifyCurrentEmailStoreResult{}, err
				}
				return email.VerifyCurrentEmailStoreResult{NewEmail: newEmail}, nil
			},
		}
		kv := &failingKV{err: errors.New("redis: connection reset")}
		svc := newServiceWithKV(store, kv)
		_, err := svc.VerifyCurrentEmail(context.Background(), baseInput)
		require.ErrorContains(t, err, "email.VerifyCurrentEmail:")
	})
}

// ── TestService_ConfirmEmailChange ────────────────────────────────────────────

func TestService_ConfirmEmailChange(t *testing.T) {
	t.Parallel()

	const testGrantToken = "550e8400-e29b-41d4-a716-446655440000"
	const testJTI = "test-jti-value"

	baseInput := email.EmailChangeConfirmInput{
		UserID:     testUserID,
		GrantToken: testGrantToken,
		Code:       validCode,
		IPAddress:  testIPAddress,
		UserAgent:  testUserAgent,
		AccessJTI:  testJTI,
	}

	seedGrantToken := func(t *testing.T, kv kvstore.Store, grantToken string, ownerID [16]byte, newEmailAddr string) {
		t.Helper()
		err := kv.Set(context.Background(),
			"echg:gt:"+grantToken,
			uuid.UUID(ownerID).String()+":"+newEmailAddr,
			time.Minute,
		)
		require.NoError(t, err)
	}

	// ── T-25: Happy path ──────────────────────────────────────────────────────

	t.Run("happy path returns OldEmail and deletes grant token from KV", func(t *testing.T) {
		t.Parallel()
		kv := kvstore.NewInMemoryStore(0)
		seedGrantToken(t, kv, testGrantToken, testUserID, newEmail)

		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
			ConfirmEmailChangeTxFn: func(_ context.Context, _ email.ConfirmEmailChangeTxInput, checkFn func(authshared.VerificationToken) error) error {
				tok := makeValidVerifyToken(t)
				return checkFn(tok)
			},
		}
		svc := newServiceWithKV(store, kv)
		result, err := svc.ConfirmEmailChange(context.Background(), baseInput)
		require.NoError(t, err)
		require.Equal(t, currentEmail, result.OldEmail)

		// Grant token must be deleted (best-effort).
		_, kvErr := kv.Get(context.Background(), "echg:gt:"+testGrantToken)
		require.ErrorIs(t, kvErr, kvstore.ErrNotFound, "grant token must be deleted after successful confirm")
	})

	// ── ErrGrantTokenEmpty ────────────────────────────────────────────────────

	t.Run("blank grant_token returns ErrGrantTokenEmpty", func(t *testing.T) {
		t.Parallel()
		svc, _ := newService(&authsharedtest.EmailChangeFakeStorer{})
		in := baseInput
		in.GrantToken = "   "
		_, err := svc.ConfirmEmailChange(context.Background(), in)
		require.ErrorIs(t, err, email.ErrGrantTokenEmpty)
	})

	// ── ErrInvalidCodeFormat ──────────────────────────────────────────────────

	t.Run("invalid code format returns ErrInvalidCodeFormat", func(t *testing.T) {
		t.Parallel()
		svc, _ := newService(&authsharedtest.EmailChangeFakeStorer{})
		in := baseInput
		in.Code = "abc"
		_, err := svc.ConfirmEmailChange(context.Background(), in)
		require.ErrorIs(t, err, email.ErrInvalidCodeFormat)
	})

	// ── ErrGrantTokenInvalid — KV miss ────────────────────────────────────────

	t.Run("grant token absent from KV returns ErrGrantTokenInvalid", func(t *testing.T) {
		t.Parallel()
		svc, _ := newService(&authsharedtest.EmailChangeFakeStorer{})
		_, err := svc.ConfirmEmailChange(context.Background(), baseInput)
		require.ErrorIs(t, err, email.ErrGrantTokenInvalid)
	})

	// ── ErrGrantTokenInvalid — userID mismatch ────────────────────────────────

	t.Run("grant token owned by different user returns ErrGrantTokenInvalid", func(t *testing.T) {
		t.Parallel()
		kv := kvstore.NewInMemoryStore(0)
		seedGrantToken(t, kv, testGrantToken, altUserID, newEmail) // altUserID ≠ testUserID
		svc := newServiceWithKV(&authsharedtest.EmailChangeFakeStorer{}, kv)
		_, err := svc.ConfirmEmailChange(context.Background(), baseInput)
		require.ErrorIs(t, err, email.ErrGrantTokenInvalid)
	})

	// ── ErrUserNotFound ───────────────────────────────────────────────────────

	t.Run("ErrUserNotFound from GetCurrentUserEmail is propagated", func(t *testing.T) {
		t.Parallel()
		kv := kvstore.NewInMemoryStore(0)
		seedGrantToken(t, kv, testGrantToken, testUserID, newEmail)
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return "", profileshared.ErrUserNotFound
			},
		}
		svc := newServiceWithKV(store, kv)
		_, err := svc.ConfirmEmailChange(context.Background(), baseInput)
		require.ErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	// ── ErrTokenNotFound from ConfirmEmailChangeTx ────────────────────────────

	t.Run("ErrTokenNotFound from ConfirmEmailChangeTx is propagated", func(t *testing.T) {
		t.Parallel()
		kv := kvstore.NewInMemoryStore(0)
		seedGrantToken(t, kv, testGrantToken, testUserID, newEmail)
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
			ConfirmEmailChangeTxFn: func(_ context.Context, _ email.ConfirmEmailChangeTxInput, _ func(authshared.VerificationToken) error) error {
				return authshared.ErrTokenNotFound
			},
		}
		svc := newServiceWithKV(store, kv)
		_, err := svc.ConfirmEmailChange(context.Background(), baseInput)
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	// ── ErrTokenExpired ───────────────────────────────────────────────────────

	t.Run("expired confirm token in checkFn returns ErrTokenExpired", func(t *testing.T) {
		t.Parallel()
		kv := kvstore.NewInMemoryStore(0)
		seedGrantToken(t, kv, testGrantToken, testUserID, newEmail)
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
			ConfirmEmailChangeTxFn: func(_ context.Context, _ email.ConfirmEmailChangeTxInput, checkFn func(authshared.VerificationToken) error) error {
				expired := authshared.NewVerificationToken(
					[16]byte(uuid.New()), testUserID, newEmail,
					"hash", 0, 5, time.Now().Add(-time.Hour),
				)
				return checkFn(expired)
			},
		}
		svc := newServiceWithKV(store, kv)
		_, err := svc.ConfirmEmailChange(context.Background(), baseInput)
		require.ErrorIs(t, err, authshared.ErrTokenExpired)
	})

	// ── T-34: ErrInvalidCode → IncrementAttemptsTx called ────────────────────

	t.Run("T-34: wrong code triggers IncrementAttemptsTx when attempts < max", func(t *testing.T) {
		t.Parallel()
		incrementCalled := false
		kv := kvstore.NewInMemoryStore(0)
		seedGrantToken(t, kv, testGrantToken, testUserID, newEmail)
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
			ConfirmEmailChangeTxFn: func(_ context.Context, _ email.ConfirmEmailChangeTxInput, checkFn func(authshared.VerificationToken) error) error {
				tok := authshared.NewVerificationToken(
					[16]byte(uuid.New()), testUserID, newEmail,
					"not-a-valid-bcrypt-hash", 0, 5, time.Now().Add(time.Hour),
				)
				return checkFn(tok)
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incrementCalled = true
				return nil
			},
		}
		svc := newServiceWithKV(store, kv)
		in := baseInput
		in.Code = invalidCode
		_, err := svc.ConfirmEmailChange(context.Background(), in)
		require.ErrorIs(t, err, authshared.ErrInvalidCode)
		require.True(t, incrementCalled, "IncrementAttemptsTx must be called on wrong code with budget remaining")
	})

	// ── T-35: ErrTooManyAttempts → no increment ───────────────────────────────

	t.Run("T-35: exhausted confirm token returns ErrTooManyAttempts without calling increment", func(t *testing.T) {
		t.Parallel()
		incrementCalled := false
		kv := kvstore.NewInMemoryStore(0)
		seedGrantToken(t, kv, testGrantToken, testUserID, newEmail)
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
			ConfirmEmailChangeTxFn: func(_ context.Context, _ email.ConfirmEmailChangeTxInput, checkFn func(authshared.VerificationToken) error) error {
				tok := makeExhaustedToken(t)
				return checkFn(tok)
			},
			IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
				incrementCalled = true
				return nil
			},
		}
		svc := newServiceWithKV(store, kv)
		_, err := svc.ConfirmEmailChange(context.Background(), baseInput)
		require.ErrorIs(t, err, authshared.ErrTooManyAttempts)
		require.False(t, incrementCalled, "IncrementAttemptsTx must NOT be called when attempts are exhausted")
	})

	// ── ErrEmailTaken from ConfirmEmailChangeTx ───────────────────────────────

	t.Run("ErrEmailTaken from ConfirmEmailChangeTx is propagated", func(t *testing.T) {
		t.Parallel()
		kv := kvstore.NewInMemoryStore(0)
		seedGrantToken(t, kv, testGrantToken, testUserID, newEmail)
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
			ConfirmEmailChangeTxFn: func(_ context.Context, _ email.ConfirmEmailChangeTxInput, _ func(authshared.VerificationToken) error) error {
				return email.ErrEmailTaken
			},
		}
		svc := newServiceWithKV(store, kv)
		_, err := svc.ConfirmEmailChange(context.Background(), baseInput)
		require.ErrorIs(t, err, email.ErrEmailTaken)
	})

	// ── T-38: context.WithoutCancel in ConfirmEmailChangeTx ──────────────────

	t.Run("T-38: ConfirmEmailChangeTx receives context.WithoutCancel (Done==nil)", func(t *testing.T) {
		t.Parallel()
		var capturedCtx context.Context
		kv := kvstore.NewInMemoryStore(0)
		seedGrantToken(t, kv, testGrantToken, testUserID, newEmail)
		store := &authsharedtest.EmailChangeFakeStorer{
			GetCurrentUserEmailFn: func(_ context.Context, _ [16]byte) (string, error) {
				return currentEmail, nil
			},
			ConfirmEmailChangeTxFn: func(ctx context.Context, _ email.ConfirmEmailChangeTxInput, checkFn func(authshared.VerificationToken) error) error {
				capturedCtx = ctx
				tok := makeValidVerifyToken(t)
				return checkFn(tok)
			},
		}
		svc := newServiceWithKV(store, kv)
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, err := svc.ConfirmEmailChange(parent, baseInput)
		require.NoError(t, err)
		require.NotNil(t, capturedCtx)
		require.Nil(t, capturedCtx.Done(),
			"context passed to ConfirmEmailChangeTx must be context.WithoutCancel")
	})
}

// ── failingKV ─────────────────────────────────────────────────────────────────

// failingKV is a minimal kvstore.Store stub whose Set always returns err.
// All other methods return safe zero values.
type failingKV struct{ err error }

func (f *failingKV) Get(_ context.Context, _ string) (string, error) {
	return "", kvstore.ErrNotFound
}
func (f *failingKV) Set(_ context.Context, _, _ string, _ time.Duration) error { return f.err }
func (f *failingKV) Delete(_ context.Context, _ string) error                  { return nil }
func (f *failingKV) Exists(_ context.Context, _ string) (bool, error)          { return false, nil }
func (f *failingKV) Keys(_ context.Context, _ string) ([]string, error)        { return nil, nil }
func (f *failingKV) StartCleanup(_ context.Context)                            {}
func (f *failingKV) Close() error                                              { return nil }
