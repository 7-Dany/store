//go:build integration_test

package register_test

import (
	"context"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/domain/auth/register"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	authsharedtest.RunTestMain(m, &testPool, 20)
}

// txStores begins a rolled-back test transaction and returns a *register.Store,
// the tx-scoped *db.Queries, and the raw pgx.Tx for within-transaction assertions.
func txStores(t *testing.T) (*register.Store, *db.Queries, pgx.Tx) {
	t.Helper()
	if testPool == nil {
		t.Skip("integration_test: testPool is nil — set TEST_DATABASE_URL")
	}
	tx, q := authsharedtest.MustBeginTx(t, testPool)
	return register.NewStore(testPool).WithQuerier(q), q, tx
}

// newCreateUserInput returns a CreateUserInput with a unique email and sensible defaults.
func newCreateUserInput(t *testing.T) register.CreateUserInput {
	t.Helper()
	return register.CreateUserInput{
		DisplayName:  "Test User",
		Email:        authsharedtest.NewEmail(t),
		PasswordHash: authsharedtest.MustHashPassword(t, "P@ssw0rd!1"),
		CodeHash:     authsharedtest.MustOTPHash(t),
		TTL:          15 * time.Minute,
		IPAddress:    "127.0.0.1",
		UserAgent:    "test-agent/1.0",
	}
}

func TestCreateUserTx_Integration(t *testing.T) {
	t.Parallel()

	t.Run("creates user, verification token, and audit row", func(t *testing.T) {
		t.Parallel()
		s, _, tx := txStores(t)

		in := newCreateUserInput(t)
		result, err := s.CreateUserTx(context.Background(), in)
		require.NoError(t, err)
		require.NotEmpty(t, result.UserID)
		require.Equal(t, in.Email, result.Email)

		// directly query auth_audit_log within the same transaction to verify
		// the register event row was written. Using the querier bound to tx keeps the
		// assertion inside the rolled-back transaction — no data leaks and no
		// false positives from concurrent tests.
		q := db.New(tx)
		userUUID, err := uuid.Parse(result.UserID)
		require.NoError(t, err)
		events, err := q.GetAuditEventsByUser(context.Background(), pgtype.UUID{Bytes: [16]byte(userUUID), Valid: true})
		require.NoError(t, err)
		var found bool
		for _, eventType := range events {
			if eventType == "register" {
				found = true
				break
			}
		}
		require.True(t, found, "auth_audit_log must contain a 'register' row for the new user")
	})

	t.Run("duplicate email returns ErrEmailTaken", func(t *testing.T) {
		t.Parallel()
		s, _, _ := txStores(t)

		in := newCreateUserInput(t)
		_, err := s.CreateUserTx(context.Background(), in)
		require.NoError(t, err)

		_, err = s.CreateUserTx(context.Background(), in)
		require.ErrorIs(t, err, authshared.ErrEmailTaken)
	})

	t.Run("FailCreateUser — error returned", func(t *testing.T) {
		t.Parallel()
		s, q, _ := txStores(t)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailCreateUser = true
		ps := s.WithQuerier(proxy)

		in := newCreateUserInput(t)
		_, err := ps.CreateUserTx(context.Background(), in)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailCreateEmailVerificationToken — error returned, no user row committed", func(t *testing.T) {
		t.Parallel()
		s, q, _ := txStores(t)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailCreateEmailVerificationToken = true
		ps := s.WithQuerier(proxy)

		in := newCreateUserInput(t)
		_, err := ps.CreateUserTx(context.Background(), in)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailInsertAuditLog — error returned, no user or token row committed", func(t *testing.T) {
		t.Parallel()
		s, q, _ := txStores(t)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		ps := s.WithQuerier(proxy)

		in := newCreateUserInput(t)
		_, err := ps.CreateUserTx(context.Background(), in)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("user row: email_verified=false after register", func(t *testing.T) {
		t.Parallel()
		s, q, _ := txStores(t)

		in := newCreateUserInput(t)
		result, err := s.CreateUserTx(context.Background(), in)
		require.NoError(t, err)
		require.NotEmpty(t, result.UserID)

		row, err := q.GetUserForResend(context.Background(), pgtype.Text{String: in.Email, Valid: true})
		require.NoError(t, err)
		require.False(t, row.EmailVerified, "email_verified must be false after register")
		require.False(t, row.IsLocked, "is_locked must be false after register")
	})

	t.Run("token row: code_hash set, used_at is null", func(t *testing.T) {
		t.Parallel()
		s, q, _ := txStores(t)

		in := newCreateUserInput(t)
		_, err := s.CreateUserTx(context.Background(), in)
		require.NoError(t, err)

		row, err := q.GetEmailVerificationToken(context.Background(), in.Email)
		require.NoError(t, err)
		require.True(t, row.CodeHash.Valid && row.CodeHash.String != "",
			"code_hash must be set on the verification token")
		require.False(t, row.UsedAt.Valid, "used_at must be NULL after register")
	})

	t.Run("token row: expires_at is within expected TTL window", func(t *testing.T) {
		t.Parallel()
		s, q, _ := txStores(t)

		ttl := 15 * time.Minute
		before := time.Now()
		in := newCreateUserInput(t)
		in.TTL = ttl

		_, err := s.CreateUserTx(context.Background(), in)
		require.NoError(t, err)

		after := time.Now()

		row, err := q.GetEmailVerificationToken(context.Background(), in.Email)
		require.NoError(t, err)

		// expires_at must fall within [before+TTL-1s, after+TTL+1s] to account for
		// clock skew between the test process and the PostgreSQL server. The 1-second
		// cushion on each side covers sub-second differences in wall-clock readings.
		lowerBound := before.Add(ttl).Add(-time.Second)
		upperBound := after.Add(ttl).Add(time.Second)
		require.True(t,
			!row.ExpiresAt.Time.Before(lowerBound) && !row.ExpiresAt.Time.After(upperBound),
			"expires_at=%v must be within [%v, %v]",
			row.ExpiresAt.Time, lowerBound, upperBound,
		)
	})
}

// TestWriteRegisterFailedAuditTx_Integration covers the store method that writes
// a register_failed audit row when account creation fails (e.g. duplicate email).
func TestWriteRegisterFailedAuditTx_Integration(t *testing.T) {
	t.Parallel()

	t.Run("writes register_failed audit row", func(t *testing.T) {
		t.Parallel()
		// Use a zero userID — the accepted convention for pre-creation audit rows
		// (no user row was committed at this point).
		s, _, _ := txStores(t)
		err := s.WriteRegisterFailedAuditTx(context.Background(), [16]byte{}, "127.0.0.1", "test-agent/1.0")
		require.NoError(t, err)
	})

	t.Run("FailInsertAuditLog — error returned", func(t *testing.T) {
		t.Parallel()
		s, q, _ := txStores(t)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		ps := s.WithQuerier(proxy)

		err := ps.WriteRegisterFailedAuditTx(context.Background(), [16]byte{}, "127.0.0.1", "test-agent/1.0")
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}
