//go:build integration_test

package unlock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/auth/unlock"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	// ADR-003: IncrementAttemptsTx opens a fresh pool transaction concurrently
	// with the outer test transaction — 20 connections required.
	authsharedtest.RunTestMain(m, &testPool, 20)
}

// ── GetUserForUnlock_Integration ─────────────────────────────────────────────

func TestGetUserForUnlock_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	t.Run("found returns UnlockUser", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := unlock.NewStore(testPool).WithQuerier(q)
		email := authsharedtest.NewEmail(t)
		_ = [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
		user, err := s.GetUserForUnlock(ctx, email)
		require.NoError(t, err)
		require.NotEqual(t, [16]byte{}, user.ID)
	})

	t.Run("not found returns ErrUserNotFound", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := unlock.NewStore(testPool).WithQuerier(q)
		_, err := s.GetUserForUnlock(ctx, "nobody@example.com")
		require.ErrorIs(t, err, authshared.ErrUserNotFound)
	})

	t.Run("query error returns ErrProxy", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailGetUserForUnlock: true}
		_, err := unlock.NewStore(testPool).WithQuerier(proxy).GetUserForUnlock(ctx, "x@example.com")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── GetUnlockToken_Integration ───────────────────────────────────────────────

func TestGetUnlockToken_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	t.Run("token found after RequestUnlockTx returns VerificationToken", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := unlock.NewStore(testPool).WithQuerier(q)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
		require.NoError(t, s.RequestUnlockTx(ctx, unlock.RequestUnlockStoreInput{
			UserID:    userID,
			Email:     email,
			IPAddress: "127.0.0.1",
			UserAgent: "test",
			CodeHash:  authsharedtest.MustOTPHash(t),
			TTL:       15 * time.Second,
		}))
		tok, err := s.GetUnlockToken(ctx, email)
		require.NoError(t, err)
		require.NotEqual(t, [16]byte{}, tok.ID)
	})

	t.Run("no token for email returns ErrTokenNotFound", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := unlock.NewStore(testPool).WithQuerier(q)
		_, err := s.GetUnlockToken(ctx, "nobody@example.com")
		require.ErrorIs(t, err, authshared.ErrTokenNotFound)
	})

	t.Run("query error returns ErrProxy", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailGetUnlockToken: true}
		_, err := unlock.NewStore(testPool).WithQuerier(proxy).GetUnlockToken(ctx, "x@example.com")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── RequestUnlockTx_Integration ───────────────────────────────────────────────

func TestRequestUnlockTx_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	t.Run("creates token and writes audit row", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := unlock.NewStore(testPool).WithQuerier(q)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
		require.NoError(t, s.RequestUnlockTx(ctx, unlock.RequestUnlockStoreInput{
			UserID:    userID,
			Email:     email,
			IPAddress: "127.0.0.1",
			UserAgent: "test",
			CodeHash:  authsharedtest.MustOTPHash(t),
			TTL:       15 * time.Second,
		}))
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventUnlockRequested),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})

	fakeInput := unlock.RequestUnlockStoreInput{
		UserID: [16]byte(uuid.New()), Email: "x@example.com",
		IPAddress: "ip", UserAgent: "ua", CodeHash: "hash",
	}

	t.Run("CreateUnlockToken error returns ErrProxy", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailCreateUnlockToken: true}
		require.ErrorIs(t, unlock.NewStore(testPool).WithQuerier(proxy).RequestUnlockTx(ctx, fakeInput), authsharedtest.ErrProxy)
	})

	t.Run("InsertAuditLog error returns ErrProxy", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
		proxy := &authsharedtest.QuerierProxy{Base: q, FailInsertAuditLog: true}
		require.ErrorIs(t, unlock.NewStore(testPool).WithQuerier(proxy).RequestUnlockTx(ctx, unlock.RequestUnlockStoreInput{
			UserID:    userID,
			Email:     email,
			IPAddress: "ip",
			UserAgent: "ua",
			CodeHash:  authsharedtest.MustOTPHash(t),
			TTL:       15 * time.Second,
		}), authsharedtest.ErrProxy)
	})
}

// ── ConsumeUnlockTokenTx_Integration ──────────────────────────────────────────

func TestConsumeUnlockTokenTx_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()
	checkFn := func(tok authshared.VerificationToken) error {
		return authshared.CheckOTPToken(tok, "123456", time.Now())
	}

	// setup creates a transaction-scoped user and an active unlock token,
	// returning the store, querier, email, and userID for use in sub-tests.
	setup := func(t *testing.T) (*unlock.Store, *db.Queries, string, [16]byte) {
		t.Helper()
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := unlock.NewStore(testPool).WithQuerier(q)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
		require.NoError(t, s.RequestUnlockTx(ctx, unlock.RequestUnlockStoreInput{
			UserID: userID, Email: email, IPAddress: "ip", UserAgent: "ua", CodeHash: authsharedtest.MustOTPHash(t),
			TTL: 15 * time.Second,
		}))
		return s, q, email, userID
	}

	t.Run("success consumes token; account remains locked until UnlockAccountTx runs", func(t *testing.T) {
		s, q, email, userID := setup(t)
		// Lock the account so we can verify ConsumeUnlockTokenTx does NOT clear it.
		_, err := q.LockAccount(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		// ConsumeUnlockTokenTx must only consume the token — no unlock side-effect.
		require.NoError(t, s.ConsumeUnlockTokenTx(ctx, email, checkFn))
		// Read the user back; is_locked must still be true because only
		// UnlockAccountTx (called by the service onSuccess) clears the flag.
		user, err := s.GetUserForUnlock(ctx, email)
		require.NoError(t, err)
		require.True(t, user.IsLocked, "ConsumeUnlockTokenTx must not clear is_locked")
	})

	t.Run("token not found returns ErrTokenNotFound", func(t *testing.T) {
		s, _, _, _ := setup(t)
		require.ErrorIs(t, s.ConsumeUnlockTokenTx(ctx, "nobody@example.com", checkFn), authshared.ErrTokenNotFound)
	})

	t.Run("checkFn error propagates", func(t *testing.T) {
		s, _, email, _ := setup(t)
		badFn := func(_ authshared.VerificationToken) error { return errors.New("bad code") }
		err := s.ConsumeUnlockTokenTx(ctx, email, badFn)
		require.ErrorContains(t, err, "bad code")
	})

	t.Run("GetUnlockToken error returns ErrProxy", func(t *testing.T) {
		_, q, _, _ := setup(t)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailGetUnlockToken: true}
		require.ErrorIs(t, unlock.NewStore(testPool).WithQuerier(proxy).ConsumeUnlockTokenTx(ctx, "x@example.com", checkFn), authsharedtest.ErrProxy)
	})

	t.Run("ConsumeUnlockToken error returns ErrProxy", func(t *testing.T) {
		_, q, email, _ := setup(t)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailConsumeUnlockToken: true}
		require.ErrorIs(t, unlock.NewStore(testPool).WithQuerier(proxy).ConsumeUnlockTokenTx(ctx, email, checkFn), authsharedtest.ErrProxy)
	})

	t.Run("ConsumeUnlockToken returns 0 rows → ErrTokenAlreadyUsed", func(t *testing.T) {
		_, q, email, _ := setup(t)
		proxy := &authsharedtest.QuerierProxy{Base: q, ConsumeUnlockTokenZero: true}
		require.ErrorIs(t, unlock.NewStore(testPool).WithQuerier(proxy).ConsumeUnlockTokenTx(ctx, email, checkFn), authshared.ErrTokenAlreadyUsed)
	})

	t.Run("second consume on same token returns ErrTokenAlreadyUsed (idempotency)", func(t *testing.T) {
		s, _, email, _ := setup(t)
		// First consume — must succeed.
		require.NoError(t, s.ConsumeUnlockTokenTx(ctx, email, checkFn))
		// Second consume — token already used: ConsumeUnlockToken returns 0 rows.
		require.ErrorIs(t, s.ConsumeUnlockTokenTx(ctx, email, checkFn), authshared.ErrTokenAlreadyUsed)
	})
}

// ── UnlockAccountTx_Integration ───────────────────────────────────────────────

func TestUnlockAccountTx_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	t.Run("clears lock and writes audit row", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		s := unlock.NewStore(testPool).WithQuerier(q)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))
		// Lock the account so we can assert UnlockAccountTx actually clears it.
		_, err := q.LockAccount(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.NoError(t, s.UnlockAccountTx(ctx, userID, "127.0.0.1", "test"))
		// Verify is_locked is now false.
		user, err := s.GetUserForUnlock(ctx, email)
		require.NoError(t, err)
		require.False(t, user.IsLocked, "UnlockAccountTx must clear is_locked")
		// Verify the audit row was written.
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventAccountUnlocked),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})

	userID := [16]byte(uuid.New())

	t.Run("UnlockAccount error returns ErrProxy", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailUnlockAccount: true}
		require.ErrorIs(t, unlock.NewStore(testPool).WithQuerier(proxy).UnlockAccountTx(ctx, userID, "ip", "ua"), authsharedtest.ErrProxy)
	})

	t.Run("InsertAuditLog error returns ErrProxy", func(t *testing.T) {
		_, q := authsharedtest.MustBeginTx(t, testPool)
		proxy := &authsharedtest.QuerierProxy{Base: q, FailInsertAuditLog: true}
		require.ErrorIs(t, unlock.NewStore(testPool).WithQuerier(proxy).UnlockAccountTx(ctx, userID, "ip", "ua"), authsharedtest.ErrProxy)
	})
}

// ── IncrementAttemptsTx_Integration ──────────────────────────────────────────
// IncrementAttemptsTx opens its own fresh pool transaction (bypasses txBound).
// Tests for it must commit real rows and clean up with t.Cleanup + DELETE.

// proxyStore wraps testPool store with proxy for IncrementAttemptsTx error paths.
// Cannot use txStores because IncrementAttemptsTx bypasses TxBound.
func proxyStore(proxy *authsharedtest.QuerierProxy) *unlock.Store {
	proxy.Base = db.New(testPool)
	return unlock.NewStore(testPool).WithQuerier(proxy)
}

func TestIncrementAttemptsTx_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("no test database configured (set TEST_DATABASE_URL)")
	}
	ctx := context.Background()

	t.Run("increments counter and writes audit row (below threshold)", func(t *testing.T) {
		email, userID := authsharedtest.CreateUserCommitted(t, testPool)
		_ = email // only userID is needed by these tests

		in := authshared.IncrementInput{
			TokenID:      authsharedtest.RandomUUID(),
			UserID:       userID,
			Attempts:     0,
			MaxAttempts:  5,
			IPAddress:    "1.2.3.4",
			UserAgent:    "go-test",
			AttemptEvent: audit.EventUnlockAttemptFailed,
		}
		require.NoError(t, unlock.NewStore(testPool).IncrementAttemptsTx(ctx, in))
	})

	t.Run("locks account and writes account_locked audit row at threshold", func(t *testing.T) {
		email, userID := authsharedtest.CreateUserCommitted(t, testPool)
		_ = email // only userID is needed by these tests

		in := authshared.IncrementInput{
			TokenID:      authsharedtest.RandomUUID(),
			UserID:       userID,
			Attempts:     4, // next attempt reaches max (5)
			MaxAttempts:  5,
			IPAddress:    "1.2.3.4",
			UserAgent:    "go-test",
			AttemptEvent: audit.EventUnlockAttemptFailed,
		}
		require.NoError(t, unlock.NewStore(testPool).IncrementAttemptsTx(ctx, in))
		// Verify account_locked audit row was written.
		count, err := db.New(testPool).CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventAccountLocked),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
	})

	t.Run("IncrementVerificationAttempts error returns wrapped error", func(t *testing.T) {
		_, userID := authsharedtest.CreateUserCommitted(t, testPool)
		in := authshared.IncrementInput{
			TokenID:      authsharedtest.RandomUUID(),
			UserID:       userID,
			Attempts:     0,
			MaxAttempts:  5,
			IPAddress:    "1.2.3.4",
			UserAgent:    "go-test",
			AttemptEvent: audit.EventUnlockAttemptFailed,
		}
		err := proxyStore(&authsharedtest.QuerierProxy{FailIncrementVerificationAttempts: true}).IncrementAttemptsTx(ctx, in)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("InsertAuditLog for attempt-failed event returns wrapped error", func(t *testing.T) {
		_, userID := authsharedtest.CreateUserCommitted(t, testPool)
		in := authshared.IncrementInput{
			TokenID:      authsharedtest.RandomUUID(),
			UserID:       userID,
			Attempts:     0,
			MaxAttempts:  5,
			IPAddress:    "1.2.3.4",
			UserAgent:    "go-test",
			AttemptEvent: audit.EventUnlockAttemptFailed,
		}
		// InsertAuditLogFailOnCall: 1 — fails on the first InsertAuditLog call
		// (the attempt-failed row); the second call (account_locked) is not reached.
		err := proxyStore(&authsharedtest.QuerierProxy{InsertAuditLogFailOnCall: 1, FailInsertAuditLog: true}).IncrementAttemptsTx(ctx, in)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("LockAccount error at threshold returns wrapped error", func(t *testing.T) {
		_, userID := authsharedtest.CreateUserCommitted(t, testPool)
		in := authshared.IncrementInput{
			TokenID:      authsharedtest.RandomUUID(),
			UserID:       userID,
			Attempts:     4, // MaxAttempts - 1 → threshold triggered
			MaxAttempts:  5,
			IPAddress:    "1.2.3.4",
			UserAgent:    "go-test",
			AttemptEvent: audit.EventUnlockAttemptFailed,
		}
		err := proxyStore(&authsharedtest.QuerierProxy{FailLockAccount: true}).IncrementAttemptsTx(ctx, in)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("InsertAuditLog for account_locked event returns wrapped error", func(t *testing.T) {
		_, userID := authsharedtest.CreateUserCommitted(t, testPool)
		in := authshared.IncrementInput{
			TokenID:      authsharedtest.RandomUUID(),
			UserID:       userID,
			Attempts:     4, // MaxAttempts - 1 → threshold triggered
			MaxAttempts:  5,
			IPAddress:    "1.2.3.4",
			UserAgent:    "go-test",
			AttemptEvent: audit.EventUnlockAttemptFailed,
		}
		// InsertAuditLogFailOnCall: 2 — fails on the second InsertAuditLog call
		// (the account_locked row); the first call (attempt-failed) succeeds.
		err := proxyStore(&authsharedtest.QuerierProxy{InsertAuditLogFailOnCall: 2, FailInsertAuditLog: true}).IncrementAttemptsTx(ctx, in)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}
