//go:build integration_test

package session_test

import (
	"context"
	"testing"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/profile/session"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) { authsharedtest.RunTestMain(m, &testPool, 20) }

// txStores begins a rolled-back test transaction and returns a *session.Store
// and the raw *db.Queries bound to that transaction.
func txStores(t *testing.T) (*session.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("integration_test: testPool is nil — set TEST_DATABASE_URL")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	return session.NewStore(testPool).WithQuerier(q), q
}

// createUser inserts a test user scoped to the test transaction bound to q and
// returns its UUID.
func createUser(t *testing.T, q db.Querier, email string) uuid.UUID {
	t.Helper()
	return authsharedtest.CreateUserUUID(t, testPool, q, email)
}

// ── TestGetActiveSessions_Integration ────────────────────────────────────────

// TestGetActiveSessions_Integration covers store.GetActiveSessions.
func TestGetActiveSessions_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns sessions after LoginTx", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)

		authsharedtest.CreateSession(t, testPool, q, userID)

		sessions, err := ps.GetActiveSessions(ctx, userID)
		require.NoError(t, err)
		require.Len(t, sessions, 1)
		require.NotEmpty(t, sessions[0].IPAddress)
		require.False(t, sessions[0].StartedAt.IsZero())
		require.False(t, sessions[0].LastActiveAt.IsZero())
	})

	t.Run("returns empty slice for unknown user", func(t *testing.T) {
		t.Parallel()
		ps, _ := txStores(t)
		sessions, err := ps.GetActiveSessions(ctx, [16]byte(uuid.New()))
		require.NoError(t, err)
		require.Empty(t, sessions)
	})

	t.Run("db.Querier failure returns wrapped error", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetActiveSessions = true
		_, err := session.NewStore(testPool).WithQuerier(proxy).GetActiveSessions(ctx, [16]byte(uuid.New()))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("does not return sessions belonging to a different user", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)

		userA := [16]byte(createUser(t, q, authsharedtest.NewEmail(t)))
		userB := [16]byte(createUser(t, q, authsharedtest.NewEmail(t)))

		// Create a session for user A only.
		authsharedtest.CreateSession(t, testPool, q, userA)

		// User B must see zero sessions.
		sessions, err := ps.GetActiveSessions(ctx, userB)
		require.NoError(t, err)
		require.Empty(t, sessions, "GetActiveSessions must not return another user's sessions")
	})

	t.Run("multiple sessions are returned newest first", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)

		// Create two sessions in sequence.
		authsharedtest.CreateSession(t, testPool, q, userID)
		authsharedtest.CreateSession(t, testPool, q, userID)

		sessions, err := ps.GetActiveSessions(ctx, userID)
		require.NoError(t, err)
		require.Len(t, sessions, 2)
		assert.True(t, !sessions[0].StartedAt.Before(sessions[1].StartedAt),
			"sessions must be ordered newest first")
	})

	t.Run("IpAddress is empty string when DB row has NULL ip_address", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)

		authsharedtest.CreateSessionNullIP(t, testPool, q, userID)

		sessions, err := ps.GetActiveSessions(ctx, userID)
		require.NoError(t, err)
		require.Len(t, sessions, 1)
		assert.Equal(t, "", sessions[0].IPAddress, "NULL ip_address must map to empty string")
	})

	t.Run("UserAgent populated correctly from DB row", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)

		authsharedtest.CreateSession(t, testPool, q, userID)

		sessions, err := ps.GetActiveSessions(ctx, userID)
		require.NoError(t, err)
		require.Len(t, sessions, 1)
		assert.Equal(t, "authsharedtest/CreateSession", sessions[0].UserAgent,
			"user_agent must be returned verbatim from the DB row")
	})
}

// ── TestRevokeSessionTx_Integration ──────────────────────────────────────────

// TestRevokeSessionTx_Integration covers store.RevokeSessionTx.
func TestRevokeSessionTx_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	loginSetup := func(t *testing.T) (*db.Queries, [16]byte, [16]byte) {
		t.Helper()
		_, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)
		sess := authsharedtest.CreateSession(t, testPool, q, userID)
		return q, sess.SessionID, userID
	}

	t.Run("success revokes session, tokens, and writes session_revoked audit row", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)
		sess := authsharedtest.CreateSession(t, testPool, q, userID)

		require.NoError(t, ps.RevokeSessionTx(ctx, sess.SessionID, userID, "127.0.0.1", "test"))

		row, err := q.GetLatestSessionByUser(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.True(t, row.EndedAt.Valid)

		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID: authsharedtest.ToPgtypeUUID(userID), EventType: string(audit.EventSessionRevoked),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})

	t.Run("session not found returns ErrSessionNotFound", func(t *testing.T) {
		t.Parallel()
		ps, _ := txStores(t)
		err := ps.RevokeSessionTx(ctx, [16]byte(uuid.New()), [16]byte(uuid.New()), "ip", "ua")
		require.ErrorIs(t, err, authshared.ErrSessionNotFound)
	})

	t.Run("wrong owner returns ErrSessionNotFound (IDOR protection)", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uidA := createUser(t, q, authsharedtest.NewEmail(t))
		uidB := createUser(t, q, authsharedtest.NewEmail(t))

		sess := authsharedtest.CreateSession(t, testPool, q, [16]byte(uidA))

		err := ps.RevokeSessionTx(ctx, sess.SessionID, [16]byte(uidB), "ip", "ua")
		require.ErrorIs(t, err, authshared.ErrSessionNotFound)
	})

	t.Run("GetSessionByID error", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetSessionByID = true
		err := session.NewStore(testPool).WithQuerier(proxy).RevokeSessionTx(ctx, [16]byte(uuid.New()), [16]byte(uuid.New()), "ip", "ua")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("EndUserSession error", func(t *testing.T) {
		t.Parallel()
		q, sessionID, userID := loginSetup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailEndUserSession = true
		err := session.NewStore(testPool).WithQuerier(proxy).RevokeSessionTx(ctx, sessionID, userID, "ip", "ua")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("RevokeSessionRefreshTokens error", func(t *testing.T) {
		t.Parallel()
		q, sessionID, userID := loginSetup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailRevokeSessionRefreshTokens = true
		err := session.NewStore(testPool).WithQuerier(proxy).RevokeSessionTx(ctx, sessionID, userID, "ip", "ua")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("InsertAuditLog error", func(t *testing.T) {
		t.Parallel()
		q, sessionID, userID := loginSetup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		err := session.NewStore(testPool).WithQuerier(proxy).RevokeSessionTx(ctx, sessionID, userID, "ip", "ua")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("refresh tokens for the revoked session are revoked", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)
		sess := authsharedtest.CreateSession(t, testPool, q, userID)

		require.NoError(t, ps.RevokeSessionTx(ctx, sess.SessionID, userID, "127.0.0.1", "test"))

		count, err := q.CountActiveRefreshTokensBySession(ctx, authsharedtest.ToPgtypeUUID(sess.SessionID))
		require.NoError(t, err)
		require.EqualValues(t, 0, count,
			"all refresh tokens for the revoked session must be revoked")
	})

	t.Run("only the targeted session is revoked; sibling sessions are untouched", func(t *testing.T) {
		t.Parallel()
		ps, q := txStores(t)
		uid := createUser(t, q, authsharedtest.NewEmail(t))
		userID := [16]byte(uid)

		sessionA := authsharedtest.CreateSession(t, testPool, q, userID)
		sessionB := authsharedtest.CreateSession(t, testPool, q, userID)

		require.NoError(t, ps.RevokeSessionTx(ctx, sessionA.SessionID, userID, "127.0.0.1", "test"))

		countA, err := q.CountActiveRefreshTokensBySession(ctx, authsharedtest.ToPgtypeUUID(sessionA.SessionID))
		require.NoError(t, err)
		assert.EqualValues(t, 0, countA,
			"all refresh tokens for the revoked session must be cleared")

		countB, err := q.CountActiveRefreshTokensBySession(ctx, authsharedtest.ToPgtypeUUID(sessionB.SessionID))
		require.NoError(t, err)
		assert.Greater(t, int(countB), 0,
			"surviving session's refresh tokens must remain active after sibling revocation")

		openCount, err := q.CountOpenSessionsByUser(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		assert.EqualValues(t, 1, openCount,
			"exactly one session must remain open after revoking one sibling session")
	})
}
