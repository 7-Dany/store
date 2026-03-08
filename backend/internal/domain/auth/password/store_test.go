//go:build integration_test

// Package password_test contains store-layer integration tests for the password
// sub-package.
//
// Two strategies cover every branch in store.go:
//
//  1. QuerierProxy — wraps a real db.Querier and intercepts individual methods
//     to return injected errors. Scoped to the same test transaction via
//     withProxy, so failures roll back cleanly without leaking state.
//
//  2. Integration tests — drive the DB into real states (consumed tokens,
//     threshold breaches) and assert the correct domain error.
//
// Run with: go test -tags integration_test ./internal/domain/auth/password/...
package password_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/domain/auth/password"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/google/uuid"
)

// testPool is shared by all test files in package password_test.
// RunTestMain initialises it only when TEST_DATABASE_URL is set; it is
// nil during unit-only test runs, causing integration test functions to
// t.Skip automatically via txStores.
var testPool *pgxpool.Pool

// TestMain lowers the bcrypt cost for fast unit tests, and initialises
// testPool when TEST_DATABASE_URL is set (integration tests only).
// ADR-003: MaxConns=20 is required because IncrementAttemptsTx opens a
// fresh pool connection concurrently with the outer test transaction.
func TestMain(m *testing.M) { authsharedtest.RunTestMain(m, &testPool, 20) }

// txStores begins a transaction and returns a *password.Store bound to it
// and the raw *db.Queries. Skips when testPool is nil.
func txStores(t *testing.T) (*password.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured — set TEST_DATABASE_URL to run integration tests")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	return password.NewStore(testPool).WithQuerier(q), q
}

// createUser inserts a test user scoped to the test transaction bound to q
// and returns the new user's UUID.
func createUser(t *testing.T, q db.Querier, email string) uuid.UUID {
	t.Helper()
	return authsharedtest.CreateUserUUID(t, testPool, q, email)
}

// withProxy sets proxy.Querier = q and returns a Store bound to the proxy.
func withProxy(q db.Querier, proxy *authsharedtest.QuerierProxy) *password.Store {
	proxy.Querier = q
	return password.NewStore(testPool).WithQuerier(proxy)
}

// checkFn is a default checkFn that accepts token "123456" (matches mustOTPHash).
func checkFn(tok authshared.VerificationToken) error {
	return authshared.CheckOTPToken(tok, "123456", time.Now())
}

// ── TestGetUserForPasswordReset_Integration ───────────────────────────────────

func TestGetUserForPasswordReset_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("found returns result", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		_ = createUser(t, q, email)
		got, err := s.GetUserForPasswordReset(ctx, email)
		require.NoError(t, err)
		require.NotEqual(t, [16]byte{}, got.ID)
	})

	t.Run("not found returns ErrUserNotFound", func(t *testing.T) {
		s, _ := txStores(t)
		_, err := s.GetUserForPasswordReset(ctx, "nobody@example.com")
		require.ErrorIs(t, err, authshared.ErrUserNotFound)
	})

	t.Run("FailGetUserForPasswordReset returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailGetUserForPasswordReset: true}).GetUserForPasswordReset(ctx, "x@example.com")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestRequestPasswordResetTx_Integration ────────────────────────────────────

func TestRequestPasswordResetTx_Integration(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T) (*password.Store, *db.Queries, string, [16]byte) {
		t.Helper()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		return s, q, email, [16]byte(createUser(t, q, email))
	}

	t.Run("success creates token writes audit", func(t *testing.T) {
		s, q, email, userID := setup(t)
		require.NoError(t, s.RequestPasswordResetTx(ctx, password.RequestPasswordResetStoreInput{
			UserID: userID, Email: email, IPAddress: "127.0.0.1", UserAgent: "test",
			CodeHash: authsharedtest.MustOTPHash(t), TTL: 15 * time.Minute,
		}))
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID: authsharedtest.ToPgtypeUUID(userID), EventType: string(audit.EventPasswordResetRequested),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})

	fakeInput := password.RequestPasswordResetStoreInput{
		UserID: [16]byte(uuid.New()), Email: "x@example.com", IPAddress: "ip", UserAgent: "ua",
		CodeHash: "somehash", TTL: 15 * time.Minute,
	}

	t.Run("FailCreatePasswordResetToken returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		err := withProxy(q, &authsharedtest.QuerierProxy{FailCreatePasswordResetToken: true}).
			RequestPasswordResetTx(ctx, fakeInput)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailInsertAuditLog returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(createUser(t, q, email))
		err := withProxy(q, &authsharedtest.QuerierProxy{FailInsertAuditLog: true}).
			RequestPasswordResetTx(ctx, password.RequestPasswordResetStoreInput{
				UserID: userID, Email: email, IPAddress: "ip", UserAgent: "ua",
				CodeHash: authsharedtest.MustOTPHash(t), TTL: 15 * time.Minute,
			})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailGetPasswordResetTokenCreatedAt inside TX returns wrapped error", func(t *testing.T) {
		_, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(createUser(t, q, email))
		err := withProxy(q, &authsharedtest.QuerierProxy{
			FailGetPasswordResetTokenCreatedAt: true,
		}).RequestPasswordResetTx(ctx, password.RequestPasswordResetStoreInput{
			UserID: userID, Email: email, IPAddress: "ip", UserAgent: "ua",
			CodeHash: authsharedtest.MustOTPHash(t), TTL: 15 * time.Minute,
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "check existing token")
	})
}

// ── TestGetPasswordResetTokenCreatedAt_Integration ───────────────────────────

func TestGetPasswordResetTokenCreatedAt_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("found returns time", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(createUser(t, q, email))
		require.NoError(t, s.RequestPasswordResetTx(ctx, password.RequestPasswordResetStoreInput{
			UserID: userID, Email: email, IPAddress: "ip", UserAgent: "ua",
			CodeHash: authsharedtest.MustOTPHash(t), TTL: 15 * time.Minute,
		}))
		got, err := s.GetPasswordResetTokenCreatedAt(ctx, email)
		require.NoError(t, err)
		require.False(t, got.IsZero())
	})

	t.Run("no active token returns ErrTokenNotFound", func(t *testing.T) {
		s, _ := txStores(t)
		_, err := s.GetPasswordResetTokenCreatedAt(ctx, "nobody@example.com")
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("FailGetPasswordResetTokenCreatedAt returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{
			FailGetPasswordResetTokenCreatedAt: true,
		}).GetPasswordResetTokenCreatedAt(ctx, email)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestRequestPasswordResetTx_DuplicateActiveToken_Integration ───────────────

func TestRequestPasswordResetTx_DuplicateActiveToken_Integration(t *testing.T) {
	ctx := context.Background()
	s, q := txStores(t)
	email := authsharedtest.NewEmail(t)
	userID := [16]byte(createUser(t, q, email))
	in := password.RequestPasswordResetStoreInput{
		UserID: userID, Email: email, IPAddress: "ip", UserAgent: "ua",
		CodeHash: authsharedtest.MustOTPHash(t), TTL: 15 * time.Minute,
	}
	require.NoError(t, s.RequestPasswordResetTx(ctx, in))
	// Second call without consuming the first token must hit the unique index.
	err := s.RequestPasswordResetTx(ctx, password.RequestPasswordResetStoreInput{
		UserID: userID, Email: email, IPAddress: "ip", UserAgent: "ua",
		CodeHash: authsharedtest.MustOTPHash(t), TTL: 15 * time.Minute,
	})
	require.ErrorIs(t, err, authshared.ErrResetTokenCooldown)
}

// ── TestConsumeAndUpdatePasswordTx_Integration ───────────────────────────────

func TestConsumeAndUpdatePasswordTx_Integration(t *testing.T) {
	ctx := context.Background()

	const newPassword = "N3w!Passw0rd#9"

	// setupWithToken creates a verified user with a pending password-reset token
	// and returns the store, raw querier, email, and the user's ID.
	setupWithToken := func(t *testing.T) (*password.Store, *db.Queries, string, [16]byte) {
		t.Helper()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(createUser(t, q, email))
		require.NoError(t, s.RequestPasswordResetTx(ctx, password.RequestPasswordResetStoreInput{
			UserID: userID, Email: email, IPAddress: "ip", UserAgent: "ua",
			CodeHash: authsharedtest.MustOTPHash(t), TTL: 15 * time.Minute,
		}))
		return s, q, email, userID
	}

	// validInput builds a ConsumeAndUpdateInput for the given email. newPassword
	// differs from the test user's initial password ("S3cure!Pass") so the
	// same-password reuse guard is not triggered on the happy path.
	validInput := func(t *testing.T, email string) password.ConsumeAndUpdateInput {
		t.Helper()
		return password.ConsumeAndUpdateInput{
			Email:       email,
			NewPassword: newPassword,
			NewHash:     authsharedtest.MustHashPassword(t, newPassword),
			IPAddress:   "ip",
			UserAgent:   "ua",
		}
	}

	t.Run("success consumes token updates hash revokes sessions writes both audit rows", func(t *testing.T) {
		s, q, email, userID := setupWithToken(t)
		_ = authsharedtest.CreateSession(t, testPool, q, userID)

		gotID, err := s.ConsumeAndUpdatePasswordTx(ctx, validInput(t, email), checkFn)
		require.NoError(t, err)
		require.Equal(t, userID, gotID)

		// Refresh token must be revoked with reason "password_changed".
		rt, err := q.GetLatestRefreshTokenByUser(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.True(t, rt.RevokedAt.Valid)
		require.Equal(t, "password_changed", rt.RevokeReason.String)

		// Session must be ended.
		sess, err := q.GetLatestSessionByUser(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.True(t, sess.EndedAt.Valid)

		// Both audit rows must exist.
		for _, event := range []string{string(audit.EventPasswordResetConfirmed), string(audit.EventPasswordChanged)} {
			count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
				UserID: authsharedtest.ToPgtypeUUID(userID), EventType: event,
			})
			require.NoError(t, err)
			require.EqualValues(t, 1, count, "expected exactly one %s audit row", event)
		}
	})

	t.Run("token not found returns ErrTokenNotFound", func(t *testing.T) {
		s, _, _, _ := setupWithToken(t)
		_, err := s.ConsumeAndUpdatePasswordTx(ctx, password.ConsumeAndUpdateInput{
			Email: "nobody@example.com", NewPassword: newPassword,
			NewHash: authsharedtest.MustHashPassword(t, newPassword),
		}, checkFn)
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("checkFn error propagates", func(t *testing.T) {
		s, _, email, _ := setupWithToken(t)
		badFn := func(_ authshared.VerificationToken) error { return errors.New("bad code") }
		_, err := s.ConsumeAndUpdatePasswordTx(ctx, validInput(t, email), badFn)
		require.ErrorContains(t, err, "bad code")
	})

	t.Run("same password reuse returns ErrSamePassword", func(t *testing.T) {
		// The test user is created with password "S3cure!Pass" (authsharedtest.CreateUser).
		// Attempting to reset to the same password must be rejected.
		s, _, email, _ := setupWithToken(t)
		sameIn := password.ConsumeAndUpdateInput{
			Email:       email,
			NewPassword: "S3cure!Pass",
			NewHash:     authsharedtest.MustHashPassword(t, "S3cure!Pass"),
			IPAddress:   "ip", UserAgent: "ua",
		}
		_, err := s.ConsumeAndUpdatePasswordTx(ctx, sameIn, checkFn)
		require.ErrorIs(t, err, password.ErrSamePassword)
	})

	t.Run("FailGetPasswordResetToken returns ErrProxy", func(t *testing.T) {
		_, q, email, _ := setupWithToken(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailGetPasswordResetToken: true}).
			ConsumeAndUpdatePasswordTx(ctx, validInput(t, email), checkFn)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailConsumePasswordResetToken returns ErrProxy", func(t *testing.T) {
		_, q, email, _ := setupWithToken(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailConsumePasswordResetToken: true}).
			ConsumeAndUpdatePasswordTx(ctx, validInput(t, email), checkFn)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("ConsumePasswordResetTokenZero returns ErrTokenAlreadyUsed", func(t *testing.T) {
		_, q, email, _ := setupWithToken(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{ConsumePasswordResetTokenZero: true}).
			ConsumeAndUpdatePasswordTx(ctx, validInput(t, email), checkFn)
		require.ErrorIs(t, err, authshared.ErrTokenAlreadyUsed)
	})

	t.Run("FailUpdatePasswordHash returns ErrProxy", func(t *testing.T) {
		_, q, email, _ := setupWithToken(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailUpdatePasswordHash: true}).
			ConsumeAndUpdatePasswordTx(ctx, validInput(t, email), checkFn)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailRevokeAllUserRefreshTokens returns ErrProxy", func(t *testing.T) {
		_, q, email, _ := setupWithToken(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailRevokeAllUserRefreshTokens: true}).
			ConsumeAndUpdatePasswordTx(ctx, validInput(t, email), checkFn)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailEndAllUserSessions returns ErrProxy", func(t *testing.T) {
		_, q, email, _ := setupWithToken(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailEndAllUserSessions: true}).
			ConsumeAndUpdatePasswordTx(ctx, validInput(t, email), checkFn)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailInsertAuditLog first call returns ErrProxy", func(t *testing.T) {
		// First InsertAuditLog call is the password_reset_confirmed row (step 9).
		_, q, email, _ := setupWithToken(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailInsertAuditLog: true, InsertAuditLogFailOnCall: 1}).
			ConsumeAndUpdatePasswordTx(ctx, validInput(t, email), checkFn)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailInsertAuditLog second call returns ErrProxy", func(t *testing.T) {
		// Second InsertAuditLog call is the password_changed row (step 10).
		_, q, email, _ := setupWithToken(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailInsertAuditLog: true, InsertAuditLogFailOnCall: 2}).
			ConsumeAndUpdatePasswordTx(ctx, validInput(t, email), checkFn)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("expired token returns ErrTokenExpired_Integration", func(t *testing.T) {
		s, q, email, _ := setupWithToken(t)

		// Back-date the token so CheckOTPToken sees it as already expired.
		require.NoError(t, q.ExpirePasswordResetToken(ctx, email))

		_, err := s.ConsumeAndUpdatePasswordTx(ctx, password.ConsumeAndUpdateInput{
			Email:       email,
			NewPassword: newPassword,
			NewHash:     authsharedtest.MustHashPassword(t, newPassword),
			IPAddress:   "ip", UserAgent: "ua",
		}, func(tok authshared.VerificationToken) error {
			return authshared.CheckOTPToken(tok, authsharedtest.OTPPlaintext, time.Now())
		})
		require.ErrorIs(t, err, authshared.ErrTokenExpired)
	})
}

// ── TestIncrementAttemptsTx_Integration ──────────────────────────────────────

func TestIncrementAttemptsTx_Integration(t *testing.T) {
	ctx := context.Background()

	// setupToken creates a user and a password reset token.
	// The DB hardcodes max_attempts=3 for password_reset tokens.
	setupToken := func(t *testing.T) (s *password.Store, q *db.Queries, userID [16]byte, email string) {
		t.Helper()
		s, q = txStores(t)
		email = authsharedtest.NewEmail(t)
		userID = [16]byte(createUser(t, q, email))
		require.NoError(t, s.RequestPasswordResetTx(ctx, password.RequestPasswordResetStoreInput{
			UserID: userID, Email: email, IPAddress: "ip", UserAgent: "ua",
			CodeHash: authsharedtest.MustOTPHash(t), TTL: 15 * time.Minute,
		}))
		return
	}

	// getTokenID returns the token UUID for the given email.
	getTokenID := func(t *testing.T, q *db.Queries, email string) [16]byte {
		t.Helper()
		row, err := q.GetPasswordResetToken(ctx, email)
		require.NoError(t, err)
		return [16]byte(row.ID)
	}

	// preSetAttempts brings a token's DB attempts counter to n by calling
	// s.IncrementAttemptsTx n times with a large MaxAttempts so the lock
	// threshold is never reached during pre-fill.
	preSetAttempts := func(t *testing.T, s *password.Store, userID [16]byte, tokenID [16]byte, n int16) {
		t.Helper()
		for i := int16(0); i < n; i++ {
			pre := authshared.IncrementInput{
				TokenID:      tokenID,
				UserID:       userID,
				Attempts:     i,
				MaxAttempts:  100,
				IPAddress:    "ip",
				UserAgent:    "ua",
				AttemptEvent: audit.EventPasswordResetAttemptFailed,
			}
			require.NoError(t, s.IncrementAttemptsTx(ctx, pre))
		}
	}

	t.Run("below threshold increments counter writes attempt audit no lock", func(t *testing.T) {
		s, q, userID, email := setupToken(t)
		tokenID := getTokenID(t, q, email)
		in := authshared.IncrementInput{
			TokenID: tokenID, UserID: userID,
			Attempts: 0, MaxAttempts: 5,
			IPAddress: "ip", UserAgent: "ua",
			AttemptEvent: audit.EventPasswordResetAttemptFailed,
		}
		require.NoError(t, s.IncrementAttemptsTx(ctx, in))
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID: authsharedtest.ToPgtypeUUID(userID), EventType: string(audit.EventPasswordResetAttemptFailed),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})

	t.Run("at threshold increments writes attempt audit locks account writes locked audit", func(t *testing.T) {
		s, q, userID, email := setupToken(t)
		tokenID := getTokenID(t, q, email)
		preSetAttempts(t, s, userID, tokenID, 2)

		in := authshared.IncrementInput{
			TokenID: tokenID, UserID: userID,
			Attempts: 2, MaxAttempts: 3,
			IPAddress: "ip", UserAgent: "ua",
			AttemptEvent: audit.EventPasswordResetAttemptFailed,
		}
		require.NoError(t, s.IncrementAttemptsTx(ctx, in))

		attemptCount, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID: authsharedtest.ToPgtypeUUID(userID), EventType: string(audit.EventPasswordResetAttemptFailed),
		})
		require.NoError(t, err)
		require.EqualValues(t, 3, attemptCount)

		lockedCount, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID: authsharedtest.ToPgtypeUUID(userID), EventType: string(audit.EventAccountLocked),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, lockedCount)
	})

	t.Run("FailIncrementVerificationAttempts returns ErrProxy", func(t *testing.T) {
		_, q, userID, email := setupToken(t)
		tokenID := getTokenID(t, q, email)
		in := authshared.IncrementInput{TokenID: tokenID, UserID: userID, Attempts: 0, MaxAttempts: 5, AttemptEvent: audit.EventPasswordResetAttemptFailed}
		proxy := &authsharedtest.QuerierProxy{FailIncrementVerificationAttempts: true}
		require.ErrorIs(t,
			withProxy(q, proxy).IncrementAttemptsTx(ctx, in),
			authsharedtest.ErrProxy,
		)
	})

	t.Run("FailInsertAuditLog first call returns ErrProxy", func(t *testing.T) {
		_, q, userID, email := setupToken(t)
		tokenID := getTokenID(t, q, email)
		in := authshared.IncrementInput{TokenID: tokenID, UserID: userID, Attempts: 0, MaxAttempts: 5, AttemptEvent: audit.EventPasswordResetAttemptFailed}
		proxy := &authsharedtest.QuerierProxy{FailInsertAuditLog: true, InsertAuditLogFailOnCall: 1}
		require.ErrorIs(t,
			withProxy(q, proxy).IncrementAttemptsTx(ctx, in),
			authsharedtest.ErrProxy,
		)
	})

	t.Run("FailLockAccount returns ErrProxy at threshold", func(t *testing.T) {
		s, q, userID, email := setupToken(t)
		tokenID := getTokenID(t, q, email)
		preSetAttempts(t, s, userID, tokenID, 2)
		in := authshared.IncrementInput{
			TokenID: tokenID, UserID: userID,
			Attempts: 2, MaxAttempts: 3,
			IPAddress: "ip", UserAgent: "ua",
			AttemptEvent: audit.EventPasswordResetAttemptFailed,
		}
		proxy := &authsharedtest.QuerierProxy{FailLockAccount: true}
		require.ErrorIs(t,
			withProxy(q, proxy).IncrementAttemptsTx(ctx, in),
			authsharedtest.ErrProxy,
		)
	})

	t.Run("FailInsertAuditLog second call returns ErrProxy", func(t *testing.T) {
		s, q, userID, email := setupToken(t)
		tokenID := getTokenID(t, q, email)
		preSetAttempts(t, s, userID, tokenID, 2)
		in := authshared.IncrementInput{
			TokenID: tokenID, UserID: userID,
			Attempts: 2, MaxAttempts: 3,
			IPAddress: "ip", UserAgent: "ua",
			AttemptEvent: audit.EventPasswordResetAttemptFailed,
		}
		proxy := &authsharedtest.QuerierProxy{FailInsertAuditLog: true, InsertAuditLogFailOnCall: 2}
		require.ErrorIs(t,
			withProxy(q, proxy).IncrementAttemptsTx(ctx, in),
			authsharedtest.ErrProxy,
		)
	})
}

// ── TestGetUserPasswordHash_Integration ──────────────────────────────────

func TestGetUserPasswordHash_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("found returns hash", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(createUser(t, q, email))
		creds, err := s.GetUserPasswordHash(ctx, userID)
		require.NoError(t, err)
		require.NotEmpty(t, creds.PasswordHash)
	})

	t.Run("not found returns ErrUserNotFound", func(t *testing.T) {
		s, _ := txStores(t)
		_, err := s.GetUserPasswordHash(ctx, [16]byte(uuid.New()))
		require.ErrorIs(t, err, authshared.ErrUserNotFound)
	})

	t.Run("FailGetUserPasswordHash returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailGetUserPasswordHash: true}).
			GetUserPasswordHash(ctx, [16]byte(uuid.New()))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestUpdatePasswordHashTx_Integration ────────────────────────────────────

func TestUpdatePasswordHashTx_Integration(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T) (*password.Store, *db.Queries, [16]byte) {
		t.Helper()
		s, q := txStores(t)
		userID := [16]byte(createUser(t, q, authsharedtest.NewEmail(t)))
		authsharedtest.CreateSession(t, testPool, q, userID)
		return s, q, userID
	}

	t.Run("success updates hash revokes tokens ends sessions writes audit", func(t *testing.T) {
		s, q, userID := setup(t)
		newHash := authsharedtest.MustHashPassword(t, "NewPassw0rd!1")
		require.NoError(t, s.UpdatePasswordHashTx(ctx, userID, newHash, "127.0.0.1", "test"))

		// Hash updated.
		creds, err := q.GetUserPasswordHash(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.Equal(t, newHash, creds.PasswordHash.String)

		// Session ended.
		sess, err := q.GetLatestSessionByUser(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.True(t, sess.EndedAt.Valid)

		// Refresh token revoked.
		rt, err := q.GetLatestRefreshTokenByUser(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.True(t, rt.RevokedAt.Valid)
		require.Equal(t, "password_changed", rt.RevokeReason.String)

		// Audit row written.
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID: authsharedtest.ToPgtypeUUID(userID), EventType: string(audit.EventPasswordChanged),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})

	t.Run("FailUpdatePasswordHash returns ErrProxy", func(t *testing.T) {
		_, q, _ := setup(t)
		err := withProxy(q, &authsharedtest.QuerierProxy{FailUpdatePasswordHash: true}).
			UpdatePasswordHashTx(ctx, [16]byte(uuid.New()), "hash", "ip", "ua")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailRevokeAllUserRefreshTokens returns ErrProxy", func(t *testing.T) {
		_, q, userID := setup(t)
		err := withProxy(q, &authsharedtest.QuerierProxy{FailRevokeAllUserRefreshTokens: true}).
			UpdatePasswordHashTx(ctx, userID, authsharedtest.MustHashPassword(t, "NewPassw0rd!1"), "ip", "ua")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailEndAllUserSessions returns ErrProxy", func(t *testing.T) {
		_, q, userID := setup(t)
		err := withProxy(q, &authsharedtest.QuerierProxy{FailEndAllUserSessions: true}).
			UpdatePasswordHashTx(ctx, userID, authsharedtest.MustHashPassword(t, "NewPassw0rd!1"), "ip", "ua")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailInsertAuditLog returns ErrProxy", func(t *testing.T) {
		_, q, userID := setup(t)
		err := withProxy(q, &authsharedtest.QuerierProxy{FailInsertAuditLog: true}).
			UpdatePasswordHashTx(ctx, userID, authsharedtest.MustHashPassword(t, "NewPassw0rd!1"), "ip", "ua")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("all active refresh tokens are revoked after update", func(t *testing.T) {
		s, q, userID := setup(t)
		require.NoError(t, s.UpdatePasswordHashTx(ctx, userID, authsharedtest.MustHashPassword(t, "NewPassw0rd!1"), "ip", "ua"))
		count, err := q.CountActiveRefreshTokensByUser(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.EqualValues(t, 0, count)
	})
}

// ── TestWritePasswordChangeFailedAuditTx_Integration ─────────────────────────

func TestIncrementChangePasswordFailuresTx_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("increments counter writes audit and returns new count", func(t *testing.T) {
		s, q := txStores(t)
		userID := [16]byte(createUser(t, q, authsharedtest.NewEmail(t)))

		count, err := s.IncrementChangePasswordFailuresTx(ctx, userID, "127.0.0.1", "test")
		require.NoError(t, err)
		require.EqualValues(t, 1, count, "first increment must return 1")

		auditCount, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventPasswordChangeFailed),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, auditCount)
	})

	t.Run("five increments return counts 1 through 5", func(t *testing.T) {
		s, q := txStores(t)
		userID := [16]byte(createUser(t, q, authsharedtest.NewEmail(t)))

		for want := int16(1); want <= 5; want++ {
			got, err := s.IncrementChangePasswordFailuresTx(ctx, userID, "127.0.0.1", "test")
			require.NoError(t, err)
			require.Equal(t, want, got, "increment %d must return %d", want, want)
		}
	})

	t.Run("ghost user returns 0 count without error", func(t *testing.T) {
		s, _ := txStores(t)
		count, err := s.IncrementChangePasswordFailuresTx(ctx, [16]byte(uuid.New()), "ip", "ua")
		require.NoError(t, err)
		require.EqualValues(t, 0, count, "deleted/missing user must return count=0, not an error")
	})

	t.Run("FailInsertAuditLog returns ErrProxy", func(t *testing.T) {
		// Must use a real user: IncrementChangePasswordFailures now runs first, so
		// a ghost UUID would return no-rows and short-circuit before the audit INSERT
		// is ever attempted — the proxy flag would never fire.
		_, q := txStores(t)
		userID := [16]byte(createUser(t, q, authsharedtest.NewEmail(t)))
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailInsertAuditLog: true}).
			IncrementChangePasswordFailuresTx(ctx, userID, "ip", "ua")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailIncrementChangePasswordFailures returns ErrProxy", func(t *testing.T) {
		// Ghost UUID is fine here: the proxy intercepts IncrementChangePasswordFailures
		// before the no-rows path is reached, so ErrProxy surfaces correctly.
		_, q := txStores(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailIncrementChangePasswordFailures: true}).
			IncrementChangePasswordFailuresTx(ctx, [16]byte(uuid.New()), "ip", "ua")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestResetChangePasswordFailuresTx_Integration ──────────────────────

func TestResetChangePasswordFailuresTx_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("resets counter to zero after increments", func(t *testing.T) {
		s, q := txStores(t)
		userID := [16]byte(createUser(t, q, authsharedtest.NewEmail(t)))

		for range 3 {
			_, err := s.IncrementChangePasswordFailuresTx(ctx, userID, "ip", "ua")
			require.NoError(t, err)
		}

		require.NoError(t, s.ResetChangePasswordFailuresTx(ctx, userID))

		count, err := s.IncrementChangePasswordFailuresTx(ctx, userID, "ip", "ua")
		require.NoError(t, err)
		require.EqualValues(t, 1, count, "counter must restart from 1 after reset")
	})

	t.Run("reset on zero counter is a no-op and returns nil", func(t *testing.T) {
		s, q := txStores(t)
		userID := [16]byte(createUser(t, q, authsharedtest.NewEmail(t)))
		require.NoError(t, s.ResetChangePasswordFailuresTx(ctx, userID),
			"resetting an already-zero counter must not return an error")
	})

	t.Run("FailResetChangePasswordFailures returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		err := withProxy(q, &authsharedtest.QuerierProxy{FailResetChangePasswordFailures: true}).
			ResetChangePasswordFailuresTx(ctx, [16]byte(uuid.New()))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestGetPasswordResetTokenForVerify_Integration ─────────────────────────

func TestGetPasswordResetTokenForVerify_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("found returns token with all fields populated", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(createUser(t, q, email))
		require.NoError(t, s.RequestPasswordResetTx(ctx, password.RequestPasswordResetStoreInput{
			UserID: userID, Email: email, IPAddress: "ip", UserAgent: "ua",
			CodeHash: authsharedtest.MustOTPHash(t), TTL: 15 * time.Minute,
		}))
		tok, err := s.GetPasswordResetTokenForVerify(ctx, email)
		require.NoError(t, err)
		require.NotEqual(t, [16]byte{}, tok.ID, "token ID must be populated")
		require.Equal(t, email, tok.Email)
		require.NotEmpty(t, tok.CodeHash)
		require.False(t, tok.ExpiresAt.IsZero())
	})

	t.Run("no active token returns ErrTokenNotFound", func(t *testing.T) {
		s, _ := txStores(t)
		_, err := s.GetPasswordResetTokenForVerify(ctx, "nobody@example.com")
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("consumed token returns ErrTokenNotFound", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(createUser(t, q, email))
		require.NoError(t, s.RequestPasswordResetTx(ctx, password.RequestPasswordResetStoreInput{
			UserID: userID, Email: email, IPAddress: "ip", UserAgent: "ua",
			CodeHash: authsharedtest.MustOTPHash(t), TTL: 15 * time.Minute,
		}))
		// Consume the token via the full reset transaction.
		newHash := authsharedtest.MustHashPassword(t, "N3w!Passw0rd#9")
		_, err := s.ConsumeAndUpdatePasswordTx(ctx, password.ConsumeAndUpdateInput{
			Email: email, NewPassword: "N3w!Passw0rd#9", NewHash: newHash,
			IPAddress: "ip", UserAgent: "ua",
		}, checkFn)
		require.NoError(t, err)
		// After consumption the token is marked used; GetPasswordResetTokenForVerify must return ErrTokenNotFound.
		_, err = s.GetPasswordResetTokenForVerify(ctx, email)
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("FailGetPasswordResetTokenForVerify flag returns ErrProxy", func(t *testing.T) {
		_, q := txStores(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{
			FailGetPasswordResetTokenForVerify: true,
		}).GetPasswordResetTokenForVerify(ctx, "x@example.com")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}
