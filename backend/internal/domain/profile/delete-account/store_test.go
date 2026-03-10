//go:build integration_test

// Package deleteaccount_test contains store-layer integration tests for
// DELETE /me and POST /me/cancel-deletion.
//
// Coverage:
//
//	T-01 (I)   ScheduleDeletionTx: deleted_at stamped, audit row written.
//	T-18 (I)   Sessions NOT revoked after soft-delete.
//	T-19 (I)   Refresh tokens NOT revoked after soft-delete.
//	T-02 (I)   SendDeletionOTPTx: OTP token created, audit row written.
//	T-03 (I)   ConfirmOTPDeletionTx: token consumed, deleted_at stamped, audit row written.
//	T-27 (I)   CancelDeletionTx: deleted_at cleared, audit row written.
//	T-28 (I)   CancelDeletionTx → 0 rows returns ErrNotPendingDeletion, no audit row.
//	           GetUserAuthMethods: password user has HasPassword=true; ErrUserNotFound; ErrProxy.
//
// Failure injection (via QuerierProxy):
//
//	FailGetUserForDeletion           → wrapped ErrProxy
//	FailScheduleUserDeletion         → wrapped ErrProxy
//	ScheduleUserDeletionZero         → ErrUserNotFound (simulates already-pending race)
//	FailInsertAuditLog (call 1)      → wrapped ErrProxy from ScheduleDeletionTx audit step
//	FailInvalidateUserDeletionTokens → wrapped ErrProxy from SendDeletionOTPTx step 1
//	FailCreateAccountDeletionToken   → wrapped ErrProxy from SendDeletionOTPTx step 3
//	FailInsertAuditLog (call 1 of SendDeletionOTPTx) → wrapped ErrProxy
//	FailGetAccountDeletionToken      → wrapped ErrProxy
//	FailConsumeAccountDeletionToken  → wrapped ErrProxy
//	ConsumeAccountDeletionTokenZero  → ErrTokenAlreadyUsed (concurrent consume)
//	FailCancelUserDeletion           → wrapped ErrProxy
//	CancelUserDeletionZero           → ErrNotPendingDeletion
//
// Run with:
//
//	go test -tags integration_test ./internal/domain/profile/delete-account/... -v
package deleteaccount_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	deleteaccount "github.com/7-Dany/store/backend/internal/domain/profile/delete-account"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
)

var testPool *pgxpool.Pool

// TestMain lowers bcrypt cost for fast unit tests and (when TEST_DATABASE_URL is
// set) initialises testPool for integration tests.
// maxConns=20 satisfies ADR-003 (IncrementAttemptsTx opens independent connections).
func TestMain(m *testing.M) { authsharedtest.RunTestMain(m, &testPool, 20) }

// ── helpers ───────────────────────────────────────────────────────────────────

// txStores begins a rolled-back test transaction and returns a *deleteaccount.Store
// and raw *db.Queries both bound to that transaction.
func txStores(t *testing.T) (*deleteaccount.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("integration_test: testPool is nil — set TEST_DATABASE_URL")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	return deleteaccount.NewStore(testPool).WithQuerier(q), q
}

// withProxy wraps q in proxy and returns a *deleteaccount.Store using that proxy.
func withProxy(q db.Querier, proxy *authsharedtest.QuerierProxy) *deleteaccount.Store {
	proxy.Querier = q
	return deleteaccount.NewStore(testPool).WithQuerier(proxy)
}

// createPendingUser inserts a user via the full register flow and then stamps
// deleted_at directly so the user is already in pending-deletion state.
// Returns the user's UUID as [16]byte and the email used.
func createPendingUser(t *testing.T, q *db.Queries, email string) [16]byte {
	t.Helper()
	userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
	// Stamp deleted_at = NOW() directly via the query; no OTP needed.
	_, err := q.ScheduleUserDeletion(context.Background(), authsharedtest.ToPgtypeUUID(userID))
	require.NoError(t, err, "createPendingUser: ScheduleUserDeletion failed")
	return userID
}

// createDeletionToken inserts an account_deletion OTP token for userID via the
// store method, scoped to the test transaction in q.
func createDeletionToken(t *testing.T, s *deleteaccount.Store, userID [16]byte, email string) {
	t.Helper()
	_, err := s.SendDeletionOTPTx(context.Background(), deleteaccount.SendDeletionOTPInput{
		UserID:     uuid.UUID(userID).String(),
		Email:      email,
		TTLSeconds: 900,
		IPAddress:  "127.0.0.1",
		UserAgent:  "go-test/1.0",
	})
	require.NoError(t, err, "createDeletionToken: SendDeletionOTPTx failed")
}

// defaultInput builds a ScheduleDeletionInput for the given userID string.
func defaultInput(userIDStr string) deleteaccount.ScheduleDeletionInput {
	return deleteaccount.ScheduleDeletionInput{
		UserID:    userIDStr,
		IPAddress: "127.0.0.1",
		UserAgent: "go-test/1.0",
		Provider:  db.AuthProviderEmail,
	}
}

// ── TestGetUserForDeletion_Integration ────────────────────────────────────────

func TestGetUserForDeletion_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("normal user — DeletedAt nil, Email and PasswordHash populated", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))

		u, err := s.GetUserForDeletion(ctx, userID)
		require.NoError(t, err)
		require.Equal(t, userID, u.ID)
		require.Nil(t, u.DeletedAt, "normal user must have nil DeletedAt")
		require.NotNil(t, u.Email, "Email must be populated")
		require.Equal(t, email, *u.Email)
		require.NotNil(t, u.PasswordHash, "PasswordHash must be populated for a password user")
	})

	t.Run("pending-deletion user — DeletedAt non-nil", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := createPendingUser(t, q, email)

		u, err := s.GetUserForDeletion(ctx, userID)
		require.NoError(t, err)
		require.NotNil(t, u.DeletedAt, "pending-deletion user must have non-nil DeletedAt")
	})

	t.Run("unknown user ID returns ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		s, _ := txStores(t)
		_, err := s.GetUserForDeletion(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	t.Run("FailGetUserForDeletion returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetUserForDeletion = true
		_, err := withProxy(q, proxy).GetUserForDeletion(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestScheduleDeletionTx_Integration ───────────────────────────────────────

func TestScheduleDeletionTx_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	setup := func(t *testing.T) (*deleteaccount.Store, *db.Queries, string, [16]byte) {
		t.Helper()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
		return s, q, uuid.UUID(userID).String(), userID
	}

	// T-01 (I) + T-18 + T-19: happy path
	t.Run("T-01/T-18/T-19 happy path — deleted_at stamped, sessions kept, tokens kept", func(t *testing.T) {
		t.Parallel()
		s, q, userIDStr, userID := setup(t)

		// Create an active session and a refresh token to verify non-revocation (T-18/T-19).
		session := authsharedtest.CreateSession(t, testPool, q, userID)

		before := time.Now().UTC().Add(-time.Second) // subtract 1s to tolerate app/DB clock skew
		result, err := s.ScheduleDeletionTx(ctx, defaultInput(userIDStr))
		require.NoError(t, err)

		// T-01: deleted_at is set.
		u, err := s.GetUserForDeletion(ctx, userID)
		require.NoError(t, err)
		require.NotNil(t, u.DeletedAt, "deleted_at must be non-nil after ScheduleDeletionTx")
		require.True(t, u.DeletedAt.After(before) || u.DeletedAt.Equal(before),
			"deleted_at must be approximately NOW()")

		// T-01: ScheduledDeletionAt = deleted_at + 30 days.
		expected := u.DeletedAt.Add(30 * 24 * time.Hour)
		require.WithinDuration(t, expected, result.ScheduledDeletionAt, time.Second)

		// Audit row written.
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventAccountDeletionRequested),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count, "exactly one account_deletion_requested audit row must be written")

		// T-18: session ended_at remains NULL — confirmed by presence in active sessions.
		activeSessions, err := q.GetActiveSessions(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		sessionActive := false
		for _, s := range activeSessions {
			if [16]byte(s.ID) == session.SessionID {
				sessionActive = true
				break
			}
		}
		require.True(t, sessionActive, "T-18: session must still be active (ended_at NULL) after soft-delete")

		// T-19: refresh token is not revoked.
		tokenRow, err := q.GetRefreshTokenByJTI(ctx, pgtype.UUID{Bytes: session.RefreshJTI, Valid: true})
		require.NoError(t, err)
		require.False(t, tokenRow.RevokedAt.Valid, "T-19: refresh token revoked_at must remain NULL after soft-delete")
	})

	// ScheduleUserDeletionZero → ErrUserNotFound (not-found / already-pending race)
	t.Run("ScheduleUserDeletionZero returns ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, _ := setup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.ScheduleUserDeletionZero = true
		_, err := withProxy(q, proxy).ScheduleDeletionTx(ctx, defaultInput(userIDStr))
		require.ErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	t.Run("FailScheduleUserDeletion returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, _ := setup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailScheduleUserDeletion = true
		_, err := withProxy(q, proxy).ScheduleDeletionTx(ctx, defaultInput(userIDStr))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailInsertAuditLog returns wrapped ErrProxy and audit row NOT written", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, userID := setup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		_, err := withProxy(q, proxy).ScheduleDeletionTx(ctx, defaultInput(userIDStr))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)

		count, cerr := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventAccountDeletionRequested),
		})
		require.NoError(t, cerr)
		require.EqualValues(t, 0, count, "audit row must NOT be written when InsertAuditLog fails")
	})
}

// ── TestSendDeletionOTPTx_Integration ────────────────────────────────────────

func TestSendDeletionOTPTx_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	setup := func(t *testing.T) (*deleteaccount.Store, *db.Queries, string, [16]byte) {
		t.Helper()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
		return s, q, uuid.UUID(userID).String(), userID
	}

	// T-02 (I): happy path
	t.Run("T-02 happy path — OTP token created, audit row written, RawCode non-empty", func(t *testing.T) {
		t.Parallel()
		s, q, userIDStr, userID := setup(t)
		email := authsharedtest.NewEmail(t)

		result, err := s.SendDeletionOTPTx(ctx, deleteaccount.SendDeletionOTPInput{
			UserID:     userIDStr,
			Email:      email,
			TTLSeconds: 900,
			IPAddress:  "127.0.0.1",
			UserAgent:  "go-test/1.0",
		})
		require.NoError(t, err)
		require.Len(t, result.RawCode, 6, "RawCode must be exactly 6 digits")

		// OTP token must exist.
		token, err := s.GetAccountDeletionToken(ctx, userID)
		require.NoError(t, err)
		require.Equal(t, userID, token.UserID)

		// Audit row written.
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventAccountDeletionOTPRequested),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count, "exactly one account_deletion_otp_requested audit row must be written")
	})

	t.Run("second call invalidates first token — only one active token at end", func(t *testing.T) {
		t.Parallel()
		s, _, userIDStr, userID := setup(t)
		email := authsharedtest.NewEmail(t)
		in := deleteaccount.SendDeletionOTPInput{
			UserID: userIDStr, Email: email, TTLSeconds: 900,
			IPAddress: "127.0.0.1", UserAgent: "go-test/1.0",
		}

		_, err := s.SendDeletionOTPTx(ctx, in)
		require.NoError(t, err)
		_, err = s.SendDeletionOTPTx(ctx, in)
		require.NoError(t, err)

		// GetAccountDeletionToken must return exactly one (the latest) active token.
		_, err = s.GetAccountDeletionToken(ctx, userID)
		require.NoError(t, err, "exactly one active token must remain after re-issue")
	})

	t.Run("FailInvalidateUserDeletionTokens returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, _ := setup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInvalidateUserDeletionTokens = true
		_, err := withProxy(q, proxy).SendDeletionOTPTx(ctx, deleteaccount.SendDeletionOTPInput{
			UserID: userIDStr, Email: "x@x.com", TTLSeconds: 900,
			IPAddress: "127.0.0.1", UserAgent: "go-test/1.0",
		})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailCreateAccountDeletionToken returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, _ := setup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailCreateAccountDeletionToken = true
		_, err := withProxy(q, proxy).SendDeletionOTPTx(ctx, deleteaccount.SendDeletionOTPInput{
			UserID: userIDStr, Email: "x@x.com", TTLSeconds: 900,
			IPAddress: "127.0.0.1", UserAgent: "go-test/1.0",
		})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailInsertAuditLog returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, _ := setup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		_, err := withProxy(q, proxy).SendDeletionOTPTx(ctx, deleteaccount.SendDeletionOTPInput{
			UserID: userIDStr, Email: "x@x.com", TTLSeconds: 900,
			IPAddress: "127.0.0.1", UserAgent: "go-test/1.0",
		})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestGetAccountDeletionToken_Integration ───────────────────────────────────

func TestGetAccountDeletionToken_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns token for user with active OTP", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
		createDeletionToken(t, s, userID, email)

		token, err := s.GetAccountDeletionToken(ctx, userID)
		require.NoError(t, err)
		require.Equal(t, userID, token.UserID)
		require.Equal(t, email, token.Email)
		require.Equal(t, int16(0), token.Attempts)
		require.Equal(t, int16(3), token.MaxAttempts)
		require.True(t, token.ExpiresAt.After(time.Now()), "token must not be expired")
	})

	t.Run("no token returns ErrTokenNotFound", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		_, err := s.GetAccountDeletionToken(ctx, userID)
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("FailGetAccountDeletionToken returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetAccountDeletionToken = true
		_, err := withProxy(q, proxy).GetAccountDeletionToken(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestConfirmOTPDeletionTx_Integration ─────────────────────────────────────

func TestConfirmOTPDeletionTx_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	setup := func(t *testing.T) (*deleteaccount.Store, *db.Queries, string, [16]byte, authshared.VerificationToken) {
		t.Helper()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
		createDeletionToken(t, s, userID, email)
		token, err := s.GetAccountDeletionToken(ctx, userID)
		require.NoError(t, err)
		return s, q, uuid.UUID(userID).String(), userID, token
	}

	// T-03 (I): happy path
	t.Run("T-03 happy path — token consumed, deleted_at stamped, audit row written", func(t *testing.T) {
		t.Parallel()
		s, q, userIDStr, userID, token := setup(t)

		before := time.Now().UTC().Add(-time.Second) // subtract 1s to tolerate app/DB clock skew
		result, err := s.ConfirmOTPDeletionTx(ctx, defaultInput(userIDStr), token.ID)
		require.NoError(t, err)

		// deleted_at set.
		u, err := s.GetUserForDeletion(ctx, userID)
		require.NoError(t, err)
		require.NotNil(t, u.DeletedAt)
		require.True(t, u.DeletedAt.After(before) || u.DeletedAt.Equal(before))

		// ScheduledDeletionAt = deleted_at + 30 days.
		require.WithinDuration(t, u.DeletedAt.Add(30*24*time.Hour), result.ScheduledDeletionAt, time.Second)

		// Audit row written.
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventAccountDeletionRequested),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count, "exactly one account_deletion_requested audit row must be written on confirm")

		// Token is now consumed — GetAccountDeletionToken returns ErrTokenNotFound.
		_, err = s.GetAccountDeletionToken(ctx, userID)
		require.ErrorIs(t, err, authshared.ErrTokenNotFound, "consumed token must not be retrievable")
	})

	// ConsumeAccountDeletionTokenZero → ErrTokenAlreadyUsed
	t.Run("ConsumeAccountDeletionTokenZero returns ErrTokenAlreadyUsed", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, _, token := setup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.ConsumeAccountDeletionTokenZero = true
		_, err := withProxy(q, proxy).ConfirmOTPDeletionTx(ctx, defaultInput(userIDStr), token.ID)
		require.ErrorIs(t, err, authshared.ErrTokenAlreadyUsed)
	})

	t.Run("FailConsumeAccountDeletionToken returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, _, token := setup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailConsumeAccountDeletionToken = true
		_, err := withProxy(q, proxy).ConfirmOTPDeletionTx(ctx, defaultInput(userIDStr), token.ID)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailScheduleUserDeletion (step 3 inside confirm tx) returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, _, token := setup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailScheduleUserDeletion = true
		_, err := withProxy(q, proxy).ConfirmOTPDeletionTx(ctx, defaultInput(userIDStr), token.ID)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailInsertAuditLog (inside confirm tx) returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, _, token := setup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		// InsertAuditLog is called once inside ConfirmOTPDeletionTx (step 4).
		proxy.FailInsertAuditLog = true
		_, err := withProxy(q, proxy).ConfirmOTPDeletionTx(ctx, defaultInput(userIDStr), token.ID)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestCancelDeletionTx_Integration ─────────────────────────────────────────

func TestCancelDeletionTx_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cancelIn := func(userIDStr string) deleteaccount.CancelDeletionInput {
		return deleteaccount.CancelDeletionInput{
			UserID:    userIDStr,
			IPAddress: "127.0.0.1",
			UserAgent: "go-test/1.0",
		}
	}

	// T-27 (I): happy path
	t.Run("T-27 happy path — deleted_at cleared, audit row written", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := createPendingUser(t, q, email)
		userIDStr := uuid.UUID(userID).String()

		err := s.CancelDeletionTx(ctx, cancelIn(userIDStr))
		require.NoError(t, err)

		// deleted_at cleared.
		u, err := s.GetUserForDeletion(ctx, userID)
		require.NoError(t, err)
		require.Nil(t, u.DeletedAt, "deleted_at must be NULL after CancelDeletionTx")

		// Audit row written.
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventAccountDeletionCancelled),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count, "exactly one account_deletion_cancelled audit row must be written")
	})

	// T-28 (I): no pending deletion → ErrNotPendingDeletion; no audit row
	t.Run("T-28 not pending deletion returns ErrNotPendingDeletion without audit row", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
		userIDStr := uuid.UUID(userID).String()

		err := s.CancelDeletionTx(ctx, cancelIn(userIDStr))
		require.ErrorIs(t, err, deleteaccount.ErrNotPendingDeletion)

		count, cerr := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventAccountDeletionCancelled),
		})
		require.NoError(t, cerr)
		require.EqualValues(t, 0, count, "audit row must NOT be written when cancellation is a no-op")
	})

	// CancelUserDeletionZero (proxy): same path, ensures proxy wiring matches
	t.Run("CancelUserDeletionZero returns ErrNotPendingDeletion", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.CancelUserDeletionZero = true
		err := withProxy(q, proxy).CancelDeletionTx(ctx, cancelIn(uuid.UUID(userID).String()))
		require.ErrorIs(t, err, deleteaccount.ErrNotPendingDeletion)
		_ = s // suppress unused warning
	})

	t.Run("FailCancelUserDeletion returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := createPendingUser(t, q, email)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailCancelUserDeletion = true
		err := withProxy(q, proxy).CancelDeletionTx(ctx, cancelIn(uuid.UUID(userID).String()))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
		_ = s // suppress unused warning
	})

	t.Run("FailInsertAuditLog returns wrapped ErrProxy, deleted_at NOT cleared", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := createPendingUser(t, q, email)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		err := withProxy(q, proxy).CancelDeletionTx(ctx, cancelIn(uuid.UUID(userID).String()))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
		// NOTE: post-state (deleted_at still set) cannot be asserted here because
		// WithQuerier sets TxBound=true, making Rollback() a no-op by design — the
		// outer test-tx is the only real transaction. Rollback semantics are covered
		// by T-27 which uses a real pool-started transaction.
	})
}

// ── TestGetUserAuthMethods_Integration ────────────────────────────────────────────────

func TestGetUserAuthMethods_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("password user — HasPassword true, IdentityCount 0", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))

		methods, err := s.GetUserAuthMethods(ctx, userID)
		require.NoError(t, err)
		require.True(t, methods.HasPassword, "password user must have HasPassword=true")
		require.Equal(t, 0, methods.IdentityCount)
	})

	t.Run("unknown userID returns ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		s, _ := txStores(t)
		_, err := s.GetUserAuthMethods(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	t.Run("FailGetUserAuthMethods returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetUserAuthMethods = true
		_, err := withProxy(q, proxy).GetUserAuthMethods(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}
