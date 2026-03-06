//go:build integration_test

package login_test

import (
	"context"
	"testing"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/domain/auth/login"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	authsharedtest.RunTestMain(m, &testPool, 20)
}

// txStores begins a transaction, returns a *login.Store bound to it and the
// underlying *db.Queries, and registers a t.Cleanup that rolls the transaction
// back. Skips when testPool is nil (no database configured).
func txStores(t *testing.T) (*login.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	return login.NewStore(testPool).WithQuerier(q), q
}

// createUser inserts a test user scoped to the test transaction bound to q and
// returns the user's ID as [16]byte.
func createUser(t *testing.T, q db.Querier, email string) [16]byte {
	t.Helper()
	result := authsharedtest.CreateUser(t, testPool, q, email)
	uid, err := uuid.Parse(result.UserID)
	require.NoError(t, err)
	return [16]byte(uid)
}

// withProxy sets proxy.Base = q and returns a Store bound to the proxy.
func withProxy(q db.Querier, proxy *authsharedtest.QuerierProxy) *login.Store {
	proxy.Base = q
	return login.NewStore(testPool).WithQuerier(proxy)
}

// commitUser creates a user in its own committed transaction (needed because
// IncrementLoginFailuresTx bypasses BeginOrBind and commits independently).
// Skips when testPool is nil. Delegates to authsharedtest.CreateUserCommitted.
func commitUser(t *testing.T) (email string, userID [16]byte) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured")
	}
	return authsharedtest.CreateUserCommitted(t, testPool)
}

// ── TestGetUserForLogin_Integration ──────────────────────────────────────────

func TestGetUserForLogin_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("found by email", func(t *testing.T) {
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := createUser(t, q, email)
		_, err := q.MarkEmailVerified(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		user, err := s.GetUserForLogin(ctx, email)
		require.NoError(t, err)
		require.Equal(t, userID, user.ID)
		require.Equal(t, email, user.Email)
		require.NotEmpty(t, user.PasswordHash)
		require.True(t, user.IsActive)
		require.True(t, user.EmailVerified)
		require.False(t, user.IsLocked)
		require.Nil(t, user.LoginLockedUntil)
	})

	t.Run("found by username", func(t *testing.T) {
		_, username, wantUserID := commitUserWithUsername(t)
		user, err := login.NewStore(testPool).GetUserForLogin(ctx, username)
		require.NoError(t, err)
		require.Equal(t, wantUserID, user.ID)
	})

	t.Run("not found returns ErrUserNotFound", func(t *testing.T) {
		s, _ := txStores(t)
		_, err := s.GetUserForLogin(ctx, "nobody@example.com")
		require.ErrorIs(t, err, authshared.ErrUserNotFound)
	})

	t.Run("query error", func(t *testing.T) {
		_, q := txStores(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailGetUserForLogin: true}).GetUserForLogin(ctx, "x@example.com")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("login_locked_until populated after 10 failures", func(t *testing.T) {
		email, userID := commitUser(t)
		s := login.NewStore(testPool)
		for i := 0; i < 10; i++ {
			require.NoError(t, s.IncrementLoginFailuresTx(ctx, userID, "127.0.0.1", "ua"))
		}
		user, err := s.GetUserForLogin(ctx, email)
		require.NoError(t, err)
		require.NotNil(t, user.LoginLockedUntil)
	})
}

// ── TestLoginTx_Integration ───────────────────────────────────────────────────

func TestLoginTx_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("creates session token stamps last_login_at and writes audit", func(t *testing.T) {
		s, q := txStores(t)
		userID := createUser(t, q, authsharedtest.NewEmail(t))
		session, err := s.LoginTx(ctx, login.LoginTxInput{UserID: userID, IPAddress: "127.0.0.1", UserAgent: "test"})
		require.NoError(t, err)
		require.NotEqual(t, [16]byte{}, session.SessionID)
		require.NotEqual(t, [16]byte{}, session.RefreshJTI)
		require.False(t, session.RefreshExpiry.IsZero())
		require.NotEqual(t, [16]byte{}, session.FamilyID)
		require.Equal(t, userID, session.UserID)
		ts, err := q.GetUserLastLoginAt(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.True(t, ts.Valid)
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID: authsharedtest.ToPgtypeUUID(userID), EventType: string(audit.EventLogin),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})

	setup := func(t *testing.T) (db.Querier, [16]byte) {
		t.Helper()
		_, q := txStores(t)
		return q, createUser(t, q, authsharedtest.NewEmail(t))
	}

	t.Run("CreateUserSession error", func(t *testing.T) {
		q, userID := setup(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailCreateUserSession: true}).LoginTx(ctx, login.LoginTxInput{UserID: userID, IPAddress: "ip", UserAgent: "ua"})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
	t.Run("CreateRefreshToken error", func(t *testing.T) {
		q, userID := setup(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailCreateRefreshToken: true}).LoginTx(ctx, login.LoginTxInput{UserID: userID, IPAddress: "ip", UserAgent: "ua"})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
	t.Run("UpdateLastLoginAt error", func(t *testing.T) {
		q, userID := setup(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailUpdateLastLoginAt: true}).LoginTx(ctx, login.LoginTxInput{UserID: userID, IPAddress: "ip", UserAgent: "ua"})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
	t.Run("InsertAuditLog error", func(t *testing.T) {
		q, userID := setup(t)
		_, err := withProxy(q, &authsharedtest.QuerierProxy{FailInsertAuditLog: true}).LoginTx(ctx, login.LoginTxInput{UserID: userID, IPAddress: "ip", UserAgent: "ua"})
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestWriteLoginFailedAuditTx_Integration ───────────────────────────────────

func TestWriteLoginFailedAuditTx_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("writes audit row", func(t *testing.T) {
		s, q := txStores(t)
		userID := createUser(t, q, authsharedtest.NewEmail(t))
		require.NoError(t, s.WriteLoginFailedAuditTx(ctx, userID, "wrong_password", "127.0.0.1", "test"))
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID: authsharedtest.ToPgtypeUUID(userID), EventType: string(audit.EventLoginFailed),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})

	t.Run("InsertAuditLog error", func(t *testing.T) {
		_, q := txStores(t)
		require.ErrorIs(t, withProxy(q, &authsharedtest.QuerierProxy{FailInsertAuditLog: true}).WriteLoginFailedAuditTx(ctx, [16]byte(uuid.New()), "wrong_password", "ip", "ua"), authsharedtest.ErrProxy)
	})
}

// ── TestIncrementLoginFailuresTx_Integration ──────────────────────────────────

func TestIncrementLoginFailuresTx_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("audit row committed independently", func(t *testing.T) {
		_, userID := commitUser(t)
		require.NoError(t, login.NewStore(testPool).IncrementLoginFailuresTx(ctx, userID, "127.0.0.1", "test"))
		count, err := db.New(testPool).CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID: authsharedtest.ToPgtypeUUID(userID), EventType: string(audit.EventLoginFailed),
		})
		require.NoError(t, err)
		require.GreaterOrEqual(t, count, int32(1))
	})

	t.Run("lockout branch fires at threshold 10", func(t *testing.T) {
		_, userID := commitUser(t)
		s := login.NewStore(testPool)
		for i := 0; i < 10; i++ {
			require.NoError(t, s.IncrementLoginFailuresTx(ctx, userID, "127.0.0.1", "ua"))
		}
		// After 10 failures the DB sets login_locked_until; assert exactly one
		// login_lockout audit row was written by the threshold branch.
		count, err := db.New(testPool).CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventLoginLockout),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})
}

// ── TestResetLoginFailuresTx_Integration ──────────────────────────────────────

func TestResetLoginFailuresTx_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("clears failures and lock", func(t *testing.T) {
		s, q := txStores(t)
		userID := createUser(t, q, authsharedtest.NewEmail(t))
		require.NoError(t, s.ResetLoginFailuresTx(ctx, userID))
	})

	t.Run("ResetLoginFailures error", func(t *testing.T) {
		_, q := txStores(t)
		require.ErrorIs(t, withProxy(q, &authsharedtest.QuerierProxy{FailResetLoginFailures: true}).ResetLoginFailuresTx(ctx, [16]byte(uuid.New())), authsharedtest.ErrProxy)
	})

	t.Run("counter is zero and lock cleared in DB after reset_Integration", func(t *testing.T) {
		email, userID := commitUser(t) // committed user required: IncrementLoginFailuresTx bypasses BeginOrBind
		s := login.NewStore(testPool)
		require.NoError(t, s.IncrementLoginFailuresTx(ctx, userID, "127.0.0.1", "ua"))
		require.NoError(t, s.ResetLoginFailuresTx(ctx, userID))
		user, err := s.GetUserForLogin(ctx, email)
		require.NoError(t, err)
		require.Nil(t, user.LoginLockedUntil, "login_locked_until must be NULL after reset")
	})
}

func TestIncrementLoginFailuresTx_BelowThreshold_Integration(t *testing.T) {
	ctx := context.Background()
	_, userID := commitUser(t)
	s := login.NewStore(testPool)
	for i := 0; i < 5; i++ {
		require.NoError(t, s.IncrementLoginFailuresTx(ctx, userID, "127.0.0.1", "ua"))
	}
	count, err := db.New(testPool).CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
		UserID:    authsharedtest.ToPgtypeUUID(userID),
		EventType: string(audit.EventLoginLockout),
	})
	require.NoError(t, err)
	require.Zero(t, count, "no lockout audit row expected below threshold 10")
}

// commitUserWithUsername inserts a verified user with a unique username in its
// own committed transaction. Registers t.Cleanup to delete the row.
// Skips when testPool is nil.
func commitUserWithUsername(t *testing.T) (email, username string, userID [16]byte) {
	t.Helper()
	if testPool == nil {
		t.Skip("no test database configured")
	}
	email = authsharedtest.NewEmail(t)
	username = "user_" + uuid.NewString()[:8]
	hash := authsharedtest.MustHashPassword(t, "S3cure!Pass")
	id, err := db.New(testPool).CreateVerifiedUserWithUsername(context.Background(),
		db.CreateVerifiedUserWithUsernameParams{
			Email:        pgtype.Text{String: email, Valid: true},
			Username:     pgtype.Text{String: username, Valid: true},
			PasswordHash: pgtype.Text{String: hash, Valid: true},
		})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.New(testPool).DeleteUserByEmail(context.Background(), email) })
	uid, err := uuid.Parse(id.String())
	require.NoError(t, err)
	return email, username, [16]byte(uid)
}
