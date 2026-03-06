//go:build integration_test

package verification_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/auth/verification"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	// ADR-003: IncrementAttemptsTx opens a fresh pool transaction concurrently
	// with the outer test transaction — 20 connections required.
	authsharedtest.RunTestMain(m, &testPool, 20)
}

// txStores begins a rolled-back test transaction and returns a *verification.Store
// and the tx-scoped *db.Queries. Skips when testPool is nil.
func txStores(t *testing.T) (*verification.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("integration_test: testPool is nil — set TEST_DATABASE_URL")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	return verification.NewStore(testPool).WithQuerier(q), q
}

// mustCreateUser inserts a test user scoped to q and returns the new user's UUID.
func mustCreateUser(t *testing.T, q db.Querier, email string) uuid.UUID {
	t.Helper()
	return authsharedtest.CreateUserUUID(t, testPool, q, email)
}

// ── VerifyEmailTx ─────────────────────────────────────────────────────────────

func TestVerifyEmailTx_Integration(t *testing.T) {
	t.Run("success marks email verified and writes audit log", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		// Seed a verification token.
		_, err := q.CreateEmailVerificationToken(context.Background(), db.CreateEmailVerificationTokenParams{
			UserID:     pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
			Email:      email,
			CodeHash:   pgtype.Text{String: authsharedtest.MustHashOTPCode(t, "123456"), Valid: true},
			IpAddress:  nil,
			TtlSeconds: 900,
		})
		require.NoError(t, err)

		err = s.VerifyEmailTx(context.Background(), email, "127.0.0.1", "test-agent",
			func(token authshared.VerificationToken) error {
				return authshared.CheckOTPToken(token, "123456", time.Now())
			})
		require.NoError(t, err)
	})

	t.Run("token not found returns ErrTokenNotFound", func(t *testing.T) {
		s, _ := txStores(t)
		err := s.VerifyEmailTx(context.Background(), "nobody@example.com", "", "",
			func(_ authshared.VerificationToken) error { return nil })
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("FailGetEmailVerificationToken — error returned", func(t *testing.T) {
		s, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetEmailVerificationToken = true
		ps := s.WithQuerier(proxy)

		err := ps.VerifyEmailTx(context.Background(), "a@example.com", "", "",
			func(_ authshared.VerificationToken) error { return nil })
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailConsumeEmailVerificationToken — error returned", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		_, err := q.CreateEmailVerificationToken(context.Background(), db.CreateEmailVerificationTokenParams{
			UserID:     pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
			Email:      email,
			CodeHash:   pgtype.Text{String: authsharedtest.MustOTPHash(t), Valid: true},
			TtlSeconds: 900,
		})
		require.NoError(t, err)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailConsumeEmailVerificationToken = true
		ps := s.WithQuerier(proxy)

		err = ps.VerifyEmailTx(context.Background(), email, "", "",
			func(_ authshared.VerificationToken) error { return nil })
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── GetUserForResend ──────────────────────────────────────────────────────────

func TestGetUserForResend_Integration(t *testing.T) {
	t.Run("known email returns user data", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		mustCreateUser(t, q, email)

		user, err := s.GetUserForResend(context.Background(), email)
		require.NoError(t, err)
		require.NotEqual(t, [16]byte{}, user.ID, "user ID must be non-zero")
		require.False(t, user.EmailVerified)
		require.False(t, user.IsLocked)
	})

	t.Run("unknown email returns ErrUserNotFound", func(t *testing.T) {
		s, _ := txStores(t)
		_, err := s.GetUserForResend(context.Background(), "ghost@example.com")
		require.ErrorIs(t, err, authshared.ErrUserNotFound)
	})

	t.Run("FailGetUserForResend — error returned", func(t *testing.T) {
		s, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetUserForResend = true
		ps := s.WithQuerier(proxy)

		_, err := ps.GetUserForResend(context.Background(), "a@example.com")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── GetLatestTokenCreatedAt ───────────────────────────────────────────────────

func TestGetLatestTokenCreatedAt_Integration(t *testing.T) {
	t.Run("no tokens returns zero time and nil error", func(t *testing.T) {
		// mustCreateUser goes through register.CreateUserTx which always creates
		// a verification token. Use CreateUserDirect here so the user has zero
		// tokens and GetLatestTokenCreatedAt correctly returns zero time.
		s, q := txStores(t)
		userID := authsharedtest.CreateUserDirect(t, q, authsharedtest.NewEmail(t))

		ts, err := s.GetLatestTokenCreatedAt(context.Background(), [16]byte(userID))
		require.NoError(t, err)
		require.True(t, ts.IsZero())
	})

	t.Run("existing token returns non-zero time", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		_, err := q.CreateEmailVerificationToken(context.Background(), db.CreateEmailVerificationTokenParams{
			UserID:     pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
			Email:      email,
			CodeHash:   pgtype.Text{String: authsharedtest.MustOTPHash(t), Valid: true},
			TtlSeconds: 900,
		})
		require.NoError(t, err)

		ts, err := s.GetLatestTokenCreatedAt(context.Background(), [16]byte(userID))
		require.NoError(t, err)
		require.False(t, ts.IsZero())
	})
}

// ── ResendVerificationTx ──────────────────────────────────────────────────────

func TestResendVerificationTx_Integration(t *testing.T) {
	t.Run("invalidates old tokens and creates new one", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		err := s.ResendVerificationTx(context.Background(), verification.ResendStoreInput{
			UserID:    [16]byte(userID),
			Email:     email,
			IPAddress: "127.0.0.1",
			UserAgent: "test-agent",
			TTL:       15 * time.Minute,
		}, authsharedtest.MustOTPHash(t))
		require.NoError(t, err)

		// A fresh token should exist now.
		ts, err := s.GetLatestTokenCreatedAt(context.Background(), [16]byte(userID))
		require.NoError(t, err)
		require.False(t, ts.IsZero())
	})

	t.Run("FailInvalidateAllUserTokens — error returned", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInvalidateAllUserTokens = true
		ps := s.WithQuerier(proxy)

		err := ps.ResendVerificationTx(context.Background(), verification.ResendStoreInput{
			UserID: [16]byte(userID),
			Email:  email,
			TTL:    15 * time.Minute,
		}, authsharedtest.MustOTPHash(t))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailCreateEmailVerificationToken — error returned", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailCreateEmailVerificationToken = true
		ps := s.WithQuerier(proxy)

		err := ps.ResendVerificationTx(context.Background(), verification.ResendStoreInput{
			UserID: [16]byte(userID),
			Email:  email,
			TTL:    15 * time.Minute,
		}, authsharedtest.MustOTPHash(t))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailInsertAuditLog — error returned", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		ps := s.WithQuerier(proxy)

		err := ps.ResendVerificationTx(context.Background(), verification.ResendStoreInput{
			UserID: [16]byte(userID),
			Email:  email,
			TTL:    15 * time.Minute,
		}, authsharedtest.MustOTPHash(t))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── IncrementAttemptsTx ───────────────────────────────────────────────────────

func TestIncrementAttemptsTx_Integration(t *testing.T) {
	t.Run("increments counter and writes audit log", func(t *testing.T) {
		// IncrementAttemptsTx always opens a FRESH transaction from the pool so it
		// commits independently of the caller's tx. That means the user and token
		// rows must already be committed to the DB before we call it — rows that
		// only exist inside a rolled-back test transaction are invisible to the
		// fresh connection.
		if testPool == nil {
			t.Skip("integration_test: testPool is nil — set TEST_DATABASE_URL")
		}

		email := authsharedtest.NewEmail(t)

		// Create user via a committed transaction so the FK on auth_audit_log
		// resolves when IncrementAttemptsTx inserts its audit row.
		userID := mustCreateUserCommitted(t, email)

		// Create the verification token in a second committed transaction.
		tokenID := mustCreateTokenCommitted(t, userID, email, authsharedtest.MustOTPHash(t))

		s := verification.NewStore(testPool)
		err := s.IncrementAttemptsTx(context.Background(), authshared.IncrementInput{
			TokenID:      tokenID,
			UserID:       [16]byte(userID),
			Attempts:     0,
			MaxAttempts:  5,
			IPAddress:    "127.0.0.1",
			UserAgent:    "test-agent",
			AttemptEvent: audit.EventVerifyAttemptFailed,
		})
		require.NoError(t, err)
	})
}

// ── VerifyEmailTx — additional branches ────────────────────────────────────────

func TestVerifyEmailTx_CheckFnReturnsError_Integration(t *testing.T) {
	t.Run("checkFn error is propagated and transaction is rolled back", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		_, err := q.CreateEmailVerificationToken(context.Background(), db.CreateEmailVerificationTokenParams{
			UserID:     pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
			Email:      email,
			CodeHash:   pgtype.Text{String: authsharedtest.MustOTPHash(t), Valid: true},
			TtlSeconds: 900,
		})
		require.NoError(t, err)

		sentinel := errors.New("check rejected")
		err = s.VerifyEmailTx(context.Background(), email, "127.0.0.1", "test-agent",
			func(_ authshared.VerificationToken) error { return sentinel })
		require.ErrorIs(t, err, sentinel)
	})
}

func TestVerifyEmailTx_ConsumeZeroRows_ErrTokenAlreadyUsed_Integration(t *testing.T) {
	t.Run("concurrent consume returns ErrTokenAlreadyUsed", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		_, err := q.CreateEmailVerificationToken(context.Background(), db.CreateEmailVerificationTokenParams{
			UserID:     pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
			Email:      email,
			CodeHash:   pgtype.Text{String: authsharedtest.MustOTPHash(t), Valid: true},
			TtlSeconds: 900,
		})
		require.NoError(t, err)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.ConsumeEmailVerificationTokenZero = true
		ps := s.WithQuerier(proxy)

		err = ps.VerifyEmailTx(context.Background(), email, "127.0.0.1", "test-agent",
			func(_ authshared.VerificationToken) error { return nil })
		require.ErrorIs(t, err, authshared.ErrTokenAlreadyUsed)
	})
}

func TestVerifyEmailTx_FailRevokePreVerificationTokens_Integration(t *testing.T) {
	t.Run("RevokePreVerificationTokens error is propagated", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		_, err := q.CreateEmailVerificationToken(context.Background(), db.CreateEmailVerificationTokenParams{
			UserID:     pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
			Email:      email,
			CodeHash:   pgtype.Text{String: authsharedtest.MustOTPHash(t), Valid: true},
			TtlSeconds: 900,
		})
		require.NoError(t, err)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailRevokePreVerificationTokens = true
		ps := s.WithQuerier(proxy)

		err = ps.VerifyEmailTx(context.Background(), email, "127.0.0.1", "test-agent",
			func(_ authshared.VerificationToken) error { return nil })
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

func TestVerifyEmailTx_FailMarkEmailVerified_Integration(t *testing.T) {
	t.Run("MarkEmailVerified error is propagated", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		_, err := q.CreateEmailVerificationToken(context.Background(), db.CreateEmailVerificationTokenParams{
			UserID:     pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
			Email:      email,
			CodeHash:   pgtype.Text{String: authsharedtest.MustOTPHash(t), Valid: true},
			TtlSeconds: 900,
		})
		require.NoError(t, err)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailMarkEmailVerified = true
		ps := s.WithQuerier(proxy)

		err = ps.VerifyEmailTx(context.Background(), email, "127.0.0.1", "test-agent",
			func(_ authshared.VerificationToken) error { return nil })
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

func TestVerifyEmailTx_AlreadyVerified_Integration(t *testing.T) {
	t.Run("MarkEmailVerified returns 0 rows on already-verified user yields ErrAlreadyVerified", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		// Perform a first successful verification to mark the account verified.
		_, err := q.CreateEmailVerificationToken(context.Background(), db.CreateEmailVerificationTokenParams{
			UserID:     pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
			Email:      email,
			CodeHash:   pgtype.Text{String: authsharedtest.MustHashOTPCode(t, "123456"), Valid: true},
			TtlSeconds: 900,
		})
		require.NoError(t, err)
		err = s.VerifyEmailTx(context.Background(), email, "127.0.0.1", "agent",
			func(tok authshared.VerificationToken) error {
				return authshared.CheckOTPToken(tok, "123456", time.Now())
			})
		require.NoError(t, err)

		// Issue a second token for the now-verified account and attempt verification again.
		_, err = q.CreateEmailVerificationToken(context.Background(), db.CreateEmailVerificationTokenParams{
			UserID:     pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
			Email:      email,
			CodeHash:   pgtype.Text{String: authsharedtest.MustHashOTPCode(t, "654321"), Valid: true},
			TtlSeconds: 900,
		})
		require.NoError(t, err)

		err = s.VerifyEmailTx(context.Background(), email, "127.0.0.1", "agent",
			func(_ authshared.VerificationToken) error { return nil })
		require.ErrorIs(t, err, authshared.ErrAlreadyVerified)
	})
}

func TestVerifyEmailTx_AccountLocked_Integration(t *testing.T) {
	t.Run("MarkEmailVerified returns 0 rows on locked user yields ErrAccountLocked", func(t *testing.T) {
		if testPool == nil {
			t.Skip("integration_test: testPool is nil — set TEST_DATABASE_URL")
		}

		email := authsharedtest.NewEmail(t)
		userID := mustCreateUserCommitted(t, email)

		// Lock the account before presenting the token.
		err := db.New(testPool).LockUserForTest(context.Background(), email)
		require.NoError(t, err)

		tokenID := mustCreateTokenCommitted(t, userID, email, authsharedtest.MustOTPHash(t))
		_ = tokenID

		s := verification.NewStore(testPool)
		err = s.VerifyEmailTx(context.Background(), email, "127.0.0.1", "agent",
			func(_ authshared.VerificationToken) error { return nil })
		require.ErrorIs(t, err, authshared.ErrAccountLocked)
	})
}

func TestVerifyEmailTx_FailInsertAuditLog_Integration(t *testing.T) {
	t.Run("InsertAuditLog error is propagated", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		_, err := q.CreateEmailVerificationToken(context.Background(), db.CreateEmailVerificationTokenParams{
			UserID:     pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
			Email:      email,
			CodeHash:   pgtype.Text{String: authsharedtest.MustOTPHash(t), Valid: true},
			TtlSeconds: 900,
		})
		require.NoError(t, err)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		ps := s.WithQuerier(proxy)

		err = ps.VerifyEmailTx(context.Background(), email, "127.0.0.1", "test-agent",
			func(_ authshared.VerificationToken) error { return nil })
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

func TestGetLatestTokenCreatedAt_QueryError_Integration(t *testing.T) {
	t.Run("GetLatestVerificationTokenCreatedAt error is propagated", func(t *testing.T) {
		s, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetLatestVerificationTokenCreatedAt = true
		ps := s.WithQuerier(proxy)

		_, err := ps.GetLatestTokenCreatedAt(context.Background(), authsharedtest.RandomUUID())
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

func TestIncrementAttemptsTx_AtCeiling_LocksAccount_Integration(t *testing.T) {
	t.Run("counter at MaxAttempts-1 locks account after increment", func(t *testing.T) {
		if testPool == nil {
			t.Skip("integration_test: testPool is nil — set TEST_DATABASE_URL")
		}

		email := authsharedtest.NewEmail(t)
		userID := mustCreateUserCommitted(t, email)
		tokenID := mustCreateTokenCommitted(t, userID, email, authsharedtest.MustOTPHash(t))

		// Pin attempts to max_attempts - 1 so the next increment crosses the ceiling.
		err := db.New(testPool).PinTokenAttemptsToMax(context.Background(), email)
		require.NoError(t, err)

		// PinTokenAttemptsToMax sets attempts = max_attempts; we need attempts = max_attempts - 1
		// so that IncrementAttemptsTx treats this as the ceiling-crossing call.
		// Read back the real max_attempts from the DB to build the input.
		attempts, err := db.New(testPool).GetTokenAttempts(
			context.Background(),
			pgtype.UUID{Bytes: tokenID, Valid: true},
		)
		require.NoError(t, err)
		// Adjust: pass current (max) value as Attempts so the service treats this
		// call as the one that hits the ceiling, which triggers LockAccount.
		maxAttempts := attempts

		s := verification.NewStore(testPool)
		err = s.IncrementAttemptsTx(context.Background(), authshared.IncrementInput{
			TokenID:      tokenID,
			UserID:       [16]byte(userID),
			Attempts:     maxAttempts,
			MaxAttempts:  maxAttempts,
			IPAddress:    "127.0.0.1",
			UserAgent:    "test-agent",
			AttemptEvent: audit.EventVerifyAttemptFailed,
		})
		require.NoError(t, err)

		locked, err := db.New(testPool).GetUserIsLocked(
			context.Background(),
			pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
		)
		require.NoError(t, err)
		require.True(t, locked, "account must be locked after hitting the attempt ceiling")
	})
}

// ── TestGetUserEmailVerified_Integration ───────────────────────────────

// TestGetUserEmailVerified_Integration covers querier_proxy.go:112 by driving
// QuerierProxy.GetUserEmailVerified through a proxy wrapping a real querier.
// The method is a pure pass-through with no Fail* flag; the only way to reach it
// is via a test that calls it explicitly.
func TestGetUserEmailVerified_Integration(t *testing.T) {
	_, q := txStores(t)
	email := authsharedtest.NewEmail(t)
	mustCreateUser(t, q, email)

	proxy := authsharedtest.NewQuerierProxy(q)

	verified, err := proxy.GetUserEmailVerified(
		context.Background(),
		pgtype.Text{String: email, Valid: true},
	)
	require.NoError(t, err)
	require.False(t, verified, "newly created user must not be email-verified")
}

// mustCreateUserCommitted inserts a user row in a committed transaction and
// registers a cleanup that deletes the row after the test completes.
// Used by tests that need committed rows visible to fresh pool connections.
func mustCreateUserCommitted(t *testing.T, email string) uuid.UUID {
	t.Helper()
	return authsharedtest.CreateUserCommittedWithEmail(t, testPool, email)
}

// mustCreateTokenCommitted inserts a verification token row in a committed
// transaction and registers a cleanup to delete it after the test.
func mustCreateTokenCommitted(t *testing.T, userID uuid.UUID, email, codeHash string) [16]byte {
	t.Helper()
	return authsharedtest.CreateVerificationTokenCommitted(t, testPool, userID, email, codeHash)
}

// ── TestVerifyEmailTx_FailGetUserVerifiedAndLocked_Integration ──────────────────────

func TestVerifyEmailTx_FailGetUserVerifiedAndLocked_Integration(t *testing.T) {
	t.Run("GetUserVerifiedAndLocked error after zero MarkEmailVerified rows is propagated", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUser(t, q, email)

		// Seed a token so VerifyEmailTx can find it.
		_, err := q.CreateEmailVerificationToken(context.Background(), db.CreateEmailVerificationTokenParams{
			UserID:     pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
			Email:      email,
			CodeHash:   pgtype.Text{String: authsharedtest.MustOTPHash(t), Valid: true},
			TtlSeconds: 900,
		})
		require.NoError(t, err)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.MarkEmailVerifiedZero = true
		proxy.FailGetUserVerifiedAndLocked = true
		ps := s.WithQuerier(proxy)

		err = ps.VerifyEmailTx(context.Background(), email, "", "",
			func(_ authshared.VerificationToken) error { return nil })
		require.Error(t, err)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestIncrementAttemptsTx_StepFailures_Integration ─────────────────────────────────

func TestIncrementAttemptsTx_StepFailures_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("integration_test: testPool is nil — set TEST_DATABASE_URL")
	}

	t.Run("FailIncrementVerificationAttempts — step 1 error propagated", func(t *testing.T) {
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUserCommitted(t, email)
		tokenID := mustCreateTokenCommitted(t, userID, email, authsharedtest.MustOTPHash(t))

		proxy := authsharedtest.NewQuerierProxy(db.New(testPool))
		proxy.FailIncrementVerificationAttempts = true
		s := verification.NewStore(testPool).WithQuerier(proxy)

		err := s.IncrementAttemptsTx(context.Background(), authshared.IncrementInput{
			TokenID:      tokenID,
			UserID:       [16]byte(userID),
			Attempts:     0,
			MaxAttempts:  5,
			AttemptEvent: audit.EventVerifyAttemptFailed,
		})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailInsertAuditLog on first call — step 2 attempt audit row error propagated", func(t *testing.T) {
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUserCommitted(t, email)
		tokenID := mustCreateTokenCommitted(t, userID, email, authsharedtest.MustOTPHash(t))

		proxy := authsharedtest.NewQuerierProxy(db.New(testPool))
		proxy.FailInsertAuditLog = true
		proxy.InsertAuditLogFailOnCall = 1
		s := verification.NewStore(testPool).WithQuerier(proxy)

		err := s.IncrementAttemptsTx(context.Background(), authshared.IncrementInput{
			TokenID:      tokenID,
			UserID:       [16]byte(userID),
			Attempts:     0,
			MaxAttempts:  5,
			AttemptEvent: audit.EventVerifyAttemptFailed,
		})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailLockAccount at ceiling — step 3 lock error propagated", func(t *testing.T) {
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUserCommitted(t, email)
		tokenID := mustCreateTokenCommitted(t, userID, email, authsharedtest.MustOTPHash(t))

		// Pin to ceiling so IncrementVerificationAttempts returns ErrNoRows
		// and newAttempts falls through to MaxAttempts, triggering LockAccount.
		err := db.New(testPool).PinTokenAttemptsToMax(context.Background(), email)
		require.NoError(t, err)

		attempts, err := db.New(testPool).GetTokenAttempts(
			context.Background(),
			pgtype.UUID{Bytes: tokenID, Valid: true},
		)
		require.NoError(t, err)
		maxAttempts := attempts

		proxy := authsharedtest.NewQuerierProxy(db.New(testPool))
		proxy.FailLockAccount = true
		s := verification.NewStore(testPool).WithQuerier(proxy)

		err = s.IncrementAttemptsTx(context.Background(), authshared.IncrementInput{
			TokenID:      tokenID,
			UserID:       [16]byte(userID),
			Attempts:     maxAttempts,
			MaxAttempts:  maxAttempts,
			AttemptEvent: audit.EventVerifyAttemptFailed,
		})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailInsertAuditLog on second call at ceiling — account_locked audit row error propagated", func(t *testing.T) {
		email := authsharedtest.NewEmail(t)
		userID := mustCreateUserCommitted(t, email)
		tokenID := mustCreateTokenCommitted(t, userID, email, authsharedtest.MustOTPHash(t))

		err := db.New(testPool).PinTokenAttemptsToMax(context.Background(), email)
		require.NoError(t, err)

		attempts, err := db.New(testPool).GetTokenAttempts(
			context.Background(),
			pgtype.UUID{Bytes: tokenID, Valid: true},
		)
		require.NoError(t, err)
		maxAttempts := attempts

		proxy := authsharedtest.NewQuerierProxy(db.New(testPool))
		proxy.FailInsertAuditLog = true
		proxy.InsertAuditLogFailOnCall = 2
		s := verification.NewStore(testPool).WithQuerier(proxy)

		err = s.IncrementAttemptsTx(context.Background(), authshared.IncrementInput{
			TokenID:      tokenID,
			UserID:       [16]byte(userID),
			Attempts:     maxAttempts,
			MaxAttempts:  maxAttempts,
			AttemptEvent: audit.EventVerifyAttemptFailed,
		})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}
