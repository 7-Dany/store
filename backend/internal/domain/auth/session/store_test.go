//go:build integration_test

// Package session_test contains integration tests for the session store.
//
// Every test function ends with _Integration and requires a real PostgreSQL
// instance configured via TEST_DATABASE_URL. Tests that run without a DB
// are skipped via the nil-guard at the top of each Test* function.
//
// Run with:
//
//	TEST_DATABASE_URL=postgres://... go test -tags integration_test ./internal/domain/auth/session/...
package session_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	login "github.com/7-Dany/store/backend/internal/domain/auth/login"
	"github.com/7-Dany/store/backend/internal/domain/auth/session"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	// ADR-003: session rotation opens independent pool transactions —
	// 20 connections required to avoid deadlocks in integration tests.
	authsharedtest.RunTestMain(m, &testPool, 20)
}

// ── TestStore_GetRefreshTokenByJTI_Integration ─────────────────────────────────

func TestStore_GetRefreshTokenByJTI_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	t.Run("found returns StoredRefreshToken", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := session.NewStore(testPool).WithQuerier(q)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "test",
		})
		require.NoError(t, err)
		rt, err := s.GetRefreshTokenByJTI(ctx, sess.RefreshJTI)
		require.NoError(t, err)
		require.Equal(t, sess.RefreshJTI, rt.JTI)
		require.False(t, rt.IsRevoked)
	})

	t.Run("not found returns ErrTokenNotFound", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := session.NewStore(testPool).WithQuerier(q)
		_, err := s.GetRefreshTokenByJTI(ctx, [16]byte(uuid.New()))
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("query error returns ErrProxy", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailGetRefreshTokenByJTI: true}
		_, err := session.NewStore(testPool).WithQuerier(proxy).GetRefreshTokenByJTI(ctx, [16]byte(uuid.New()))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestStore_RotateRefreshTokenTx_Integration ─────────────────────────────────

func TestStore_RotateRefreshTokenTx_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	makeInput := func(sess login.LoggedInSession, userID [16]byte) session.RotateTxInput {
		return session.RotateTxInput{
			CurrentJTI: sess.RefreshJTI,
			UserID:     userID,
			SessionID:  sess.SessionID,
			FamilyID:   sess.FamilyID,
			IPAddress:  "127.0.0.1",
			UserAgent:  "go-test/1.0",
		}
	}

	t.Run("new token minted last_active_at stamped audit written", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := session.NewStore(testPool).WithQuerier(q)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "test",
		})
		require.NoError(t, err)
		before := time.Now()
		rotated, err := s.RotateRefreshTokenTx(ctx, makeInput(sess, userID))
		require.NoError(t, err)
		require.NotEqual(t, [16]byte{}, rotated.NewRefreshJTI)
		require.True(t, rotated.RefreshExpiry.After(before.Add(29*24*time.Hour)))
		// New token row exists.
		expiry, err := q.GetRefreshTokenExpiry(ctx, authsharedtest.ToPgtypeUUID(rotated.NewRefreshJTI))
		require.NoError(t, err)
		require.True(t, expiry.Valid)
		// Session last_active_at updated.
		sessions, err := q.GetActiveSessions(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.Len(t, sessions, 1)
		require.True(t, sessions[0].LastActiveAt.Time.UTC().After(before.Add(-time.Second)))
		// Audit row written.
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID: authsharedtest.ToPgtypeUUID(userID), EventType: string(audit.EventTokenRefreshed),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})

	t.Run("RevokeRefreshTokenByJTI error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "go-test/1.0",
		})
		require.NoError(t, err)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailRevokeRefreshTokenByJTI: true}
		_, err = session.NewStore(testPool).WithQuerier(proxy).RotateRefreshTokenTx(ctx, makeInput(sess, userID))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("CreateRotatedRefreshToken error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "go-test/1.0",
		})
		require.NoError(t, err)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailCreateRotatedRefreshToken: true}
		_, err = session.NewStore(testPool).WithQuerier(proxy).RotateRefreshTokenTx(ctx, makeInput(sess, userID))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("UpdateSessionLastActive error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "go-test/1.0",
		})
		require.NoError(t, err)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailUpdateSessionLastActive: true}
		_, err = session.NewStore(testPool).WithQuerier(proxy).RotateRefreshTokenTx(ctx, makeInput(sess, userID))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("UpdateLastLoginAt error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "go-test/1.0",
		})
		require.NoError(t, err)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailUpdateLastLoginAt: true}
		_, err = session.NewStore(testPool).WithQuerier(proxy).RotateRefreshTokenTx(ctx, makeInput(sess, userID))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("InsertAuditLog error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "go-test/1.0",
		})
		require.NoError(t, err)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailInsertAuditLog: true}
		_, err = session.NewStore(testPool).WithQuerier(proxy).RotateRefreshTokenTx(ctx, makeInput(sess, userID))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestStore_RevokeFamilyTokens_Integration ───────────────────────────────────

func TestStore_RevokeFamilyTokens_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	t.Run("revokes all family tokens idempotent", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := session.NewStore(testPool).WithQuerier(q)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "test",
		})
		require.NoError(t, err)
		require.NoError(t, s.RevokeFamilyTokensTx(ctx, userID, sess.FamilyID, "reuse_detected"))
		rt, err := q.GetLatestRefreshTokenByUser(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.True(t, rt.RevokedAt.Valid)
		// Idempotent: calling again succeeds.
		require.NoError(t, s.RevokeFamilyTokensTx(ctx, userID, sess.FamilyID, "reuse_detected"))
	})

	t.Run("RevokeFamilyRefreshTokens error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailRevokeFamilyRefreshTokens: true}
		err := session.NewStore(testPool).WithQuerier(proxy).RevokeFamilyTokensTx(ctx, [16]byte(uuid.New()), [16]byte(uuid.New()), "reuse_detected")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("InsertAuditLog error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{
			Base:                     q,
			FailInsertAuditLog:       true,
			InsertAuditLogFailOnCall: 1,
		}
		err := session.NewStore(testPool).WithQuerier(proxy).RevokeFamilyTokensTx(ctx, [16]byte(uuid.New()), [16]byte(uuid.New()), "reuse_detected")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestStore_RevokeAllUserTokens_Integration ──────────────────────────────────

func TestStore_RevokeAllUserTokens_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	t.Run("revokes tokens and ends sessions idempotent", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := session.NewStore(testPool).WithQuerier(q)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		_, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "test",
		})
		require.NoError(t, err)
		require.NoError(t, s.RevokeAllUserTokensTx(ctx, userID, "forced_logout", "127.0.0.1", "go-test/1.0"))
		rt, err := q.GetLatestRefreshTokenByUser(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.True(t, rt.RevokedAt.Valid)
		require.Equal(t, "forced_logout", rt.RevokeReason.String)
		// Idempotent: calling again succeeds.
		require.NoError(t, s.RevokeAllUserTokensTx(ctx, userID, "forced_logout", "127.0.0.1", "go-test/1.0"))
	})

	t.Run("RevokeAllUserRefreshTokens error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailRevokeAllUserRefreshTokens: true}
		err := session.NewStore(testPool).WithQuerier(proxy).RevokeAllUserTokensTx(ctx, [16]byte(uuid.New()), "forced_logout", "", "")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("EndAllUserSessions error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailEndAllUserSessions: true}
		err := session.NewStore(testPool).WithQuerier(proxy).RevokeAllUserTokensTx(ctx, [16]byte(uuid.New()), "forced_logout", "", "")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("InsertAuditLog error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{
			Base:                     q,
			FailInsertAuditLog:       true,
			InsertAuditLogFailOnCall: 1,
		}
		err := session.NewStore(testPool).WithQuerier(proxy).RevokeAllUserTokensTx(ctx, [16]byte(uuid.New()), "forced_logout", "", "")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestStore_LogoutTx_Integration ─────────────────────────────────────────────

func TestStore_LogoutTx_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	makeInput := func(sess login.LoggedInSession, userID [16]byte) session.LogoutTxInput {
		return session.LogoutTxInput{
			JTI:       sess.RefreshJTI,
			SessionID: sess.SessionID,
			UserID:    userID,
			IPAddress: "127.0.0.1",
			UserAgent: "go-test/1.0",
		}
	}

	t.Run("revokes token ends session writes audit", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := session.NewStore(testPool).WithQuerier(q)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "test",
		})
		require.NoError(t, err)
		require.NoError(t, s.LogoutTx(ctx, makeInput(sess, userID)))
		// Token revoked with reason='logout'.
		rt, err := q.GetLatestRefreshTokenByUser(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.True(t, rt.RevokedAt.Valid)
		require.Equal(t, "logout", rt.RevokeReason.String)
		// Session ended.
		dbSess, err := q.GetLatestSessionByUser(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.True(t, dbSess.EndedAt.Valid)
		// Audit row written.
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID: authsharedtest.ToPgtypeUUID(userID), EventType: string(audit.EventLogout),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})

	t.Run("RevokeRefreshTokenByJTI error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "go-test/1.0",
		})
		require.NoError(t, err)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailRevokeRefreshTokenByJTI: true}
		err = session.NewStore(testPool).WithQuerier(proxy).LogoutTx(ctx, makeInput(sess, userID))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("EndUserSession error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "go-test/1.0",
		})
		require.NoError(t, err)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailEndUserSession: true}
		err = session.NewStore(testPool).WithQuerier(proxy).LogoutTx(ctx, makeInput(sess, userID))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("InsertAuditLog error", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "go-test/1.0",
		})
		require.NoError(t, err)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailInsertAuditLog: true}
		err = session.NewStore(testPool).WithQuerier(proxy).LogoutTx(ctx, makeInput(sess, userID))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("idempotent: second call on same JTI succeeds", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := session.NewStore(testPool).WithQuerier(q)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
			UserID: userID, IPAddress: "127.0.0.1", UserAgent: "test",
		})
		require.NoError(t, err)
		input := makeInput(sess, userID)
		require.NoError(t, s.LogoutTx(ctx, input), "first LogoutTx must succeed")
		require.NoError(t, s.LogoutTx(ctx, input), "second LogoutTx on same JTI must also succeed (idempotent)")
	})
}

// ── TestStore_GetUserVerifiedAndLocked_Integration ─────────────────────────────

func TestStore_GetUserVerifiedAndLocked_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	t.Run("found active unlocked user", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := session.NewStore(testPool).WithQuerier(q)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		// Mark email verified so EmailVerified comes back true.
		_, err := q.MarkEmailVerified(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		status, err := s.GetUserVerifiedAndLocked(ctx, userID)
		require.NoError(t, err)
		require.True(t, status.EmailVerified)
		require.False(t, status.IsLocked)
		require.True(t, status.IsActive)
	})

	t.Run("found locked user", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := session.NewStore(testPool).WithQuerier(q)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
		_, err := q.LockAccount(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		status, err := s.GetUserVerifiedAndLocked(ctx, userID)
		require.NoError(t, err)
		require.True(t, status.IsLocked)
	})

	t.Run("not found returns ErrUserNotFound", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := session.NewStore(testPool).WithQuerier(q)
		_, err := s.GetUserVerifiedAndLocked(ctx, [16]byte(uuid.New()))
		require.ErrorIs(t, err, authshared.ErrUserNotFound)
	})

	t.Run("query error returns ErrProxy", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailGetUserVerifiedAndLocked: true}
		_, err := session.NewStore(testPool).WithQuerier(proxy).GetUserVerifiedAndLocked(ctx, [16]byte(uuid.New()))
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestStore_WriteRefreshFailedAuditTx_Integration ───────────────────────────

func TestStore_WriteRefreshFailedAuditTx_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	t.Run("writes audit row with NULL userID and correct event type", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := session.NewStore(testPool).WithQuerier(q)
		// WriteRefreshFailedAuditTx uses a NULL user_id (no trusted identity).
		// CountAuditEventsByUser uses WHERE user_id = $1 which never matches NULL,
		// so we verify correctness by asserting the insert itself succeeds
		// (a FK violation or wrong event_type would cause a non-nil error).
		require.NoError(t, s.WriteRefreshFailedAuditTx(ctx, "1.2.3.4", "go-test/1.0"))
	})

	t.Run("InsertAuditLog error returns wrapped ErrProxy", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailInsertAuditLog: true}
		err := session.NewStore(testPool).WithQuerier(proxy).WriteRefreshFailedAuditTx(ctx, "1.2.3.4", "go-test")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestStore_RotateRefreshTokenTx_AlreadyRevoked_Integration ─────────────────

func TestStore_RotateRefreshTokenTx_AlreadyRevoked_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	_, q := authsharedtest.MustBeginTx(t, testPool)
	s := session.NewStore(testPool).WithQuerier(q)
	userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))
	sess, err := login.NewStore(testPool).WithQuerier(q).LoginTx(ctx, login.LoginTxInput{
		UserID: userID, IPAddress: "127.0.0.1", UserAgent: "test",
	})
	require.NoError(t, err)

	// Pre-revoke the token directly so RotateRefreshTokenTx sees 0 rows affected.
	_, err = q.RevokeRefreshTokenByJTI(ctx, db.RevokeRefreshTokenByJTIParams{
		Reason: "pre_revoked",
		Jti:    authsharedtest.ToPgtypeUUID(sess.RefreshJTI),
	})
	require.NoError(t, err)

	_, err = s.RotateRefreshTokenTx(ctx, session.RotateTxInput{
		CurrentJTI: sess.RefreshJTI,
		UserID:     userID,
		SessionID:  sess.SessionID,
		FamilyID:   sess.FamilyID,
		IPAddress:  "127.0.0.1",
		UserAgent:  "go-test/1.0",
	})
	require.ErrorIs(t, err, authshared.ErrTokenAlreadyConsumed)
}

// ── TestStore_RotateRefreshTokenTx_ConcurrentRotation_Integration ──────────────────────

func TestStore_RotateRefreshTokenTx_ConcurrentRotation_Integration(t *testing.T) {
	// This test verifies that two goroutines racing to rotate the same refresh
	// token produce exactly one success and one error. Before the rowcount-check
	// fix this test will non-deterministically allow both goroutines to succeed,
	// creating two child tokens (diverged family).
	if testPool == nil {
		t.Skip("no test database configured")
	}
	ctx := context.Background()

	// Use real (non-test-tx) querier for setup so rows are visible to both goroutines.
	q := db.New(testPool)
	userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))

	// Mark the user as email-verified so the status guard passes.
	if _, err := q.MarkEmailVerified(ctx, authsharedtest.ToPgtypeUUID(userID)); err != nil {
		t.Fatalf("mark email verified: %v", err)
	}

	realStore := session.NewStore(testPool)
	sess, err := login.NewStore(testPool).LoginTx(ctx, login.LoginTxInput{
		UserID: userID, IPAddress: "127.0.0.1", UserAgent: "go-test/concurrent",
	})
	if err != nil {
		t.Fatalf("loginTx: %v", err)
	}

	t.Cleanup(func() {
		_ = realStore.RevokeAllUserTokensTx(ctx, userID, "test_cleanup", "", "")
	})

	input := session.RotateTxInput{
		CurrentJTI: sess.RefreshJTI,
		UserID:     userID,
		SessionID:  sess.SessionID,
		FamilyID:   sess.FamilyID,
		IPAddress:  "127.0.0.1",
		UserAgent:  "go-test/concurrent",
	}

	type rotateResult struct {
		rotated session.RotatedSession
		err     error
	}
	ch := make(chan rotateResult, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	for range 2 {
		go func() {
			defer wg.Done()
			r, e := realStore.RotateRefreshTokenTx(ctx, input)
			ch <- rotateResult{r, e}
		}()
	}
	wg.Wait()
	close(ch)

	var successes, failures int
	for r := range ch {
		if r.err == nil {
			successes++
		} else {
			failures++
		}
	}

	require.Equal(t, 1, successes, "exactly one goroutine must succeed")
	require.Equal(t, 1, failures, "exactly one goroutine must fail")
}
