//go:build integration_test

// Package setpassword_test contains store-layer integration tests for
// POST /set-password.
//
// Coverage:
//
//	T-01 (I)   Happy path: password_hash persisted, audit row written.
//	T-03 (I)   Concurrency: second SetPasswordHashTx returns ErrPasswordAlreadySet
//	           because WHERE password_hash IS NULL matches 0 rows after the first write.
//	T-18       Audit row with event_type = "password_set" is written on success.
//	T-19       password_hash column is updated to a valid bcrypt hash on success.
//
// Failure injection (via QuerierProxy):
//
//	FailGetUserForSetPassword  → wrapped ErrProxy from GetUserForSetPassword
//	FailSetPasswordHash        → wrapped ErrProxy from SetPasswordHash (step 1 of TX)
//	FailInsertAuditLog         → wrapped ErrProxy from InsertAuditLog (step 2 of TX)
//
// Run with:
//
//	go test -tags integration_test ./internal/domain/profile/set-password/... -v
package setpassword_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	setpassword "github.com/7-Dany/store/backend/internal/domain/profile/set-password"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
)

// testPool is the integration-test connection pool. Nil during unit-only runs;
// integration tests skip automatically via txStores when nil.
var testPool *pgxpool.Pool

// TestMain lowers bcrypt cost and (when TEST_DATABASE_URL is set) initialises
// testPool. All unit tests in this package also benefit from the lower cost.
func TestMain(m *testing.M) { authsharedtest.RunTestMain(m, &testPool, 20) }

// txStores begins a rolled-back test transaction and returns a *setpassword.Store
// and the raw *db.Queries both bound to that transaction.
func txStores(t *testing.T) (*setpassword.Store, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("integration_test: testPool is nil — set TEST_DATABASE_URL")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	return setpassword.NewStore(testPool).WithQuerier(q), q
}

// createOAuthUser inserts a user row with password_hash = NULL (OAuth-only account)
// scoped to the test transaction bound to q. Returns the user's UUID as [16]byte.
func createOAuthUser(t *testing.T, q *db.Queries, email string) [16]byte {
	t.Helper()
	row, err := q.CreateUser(context.Background(), db.CreateUserParams{
		Email:        pgtype.Text{String: email, Valid: true},
		DisplayName:  pgtype.Text{String: "OAuth User", Valid: true},
		PasswordHash: pgtype.Text{}, // Valid: false → NULL; simulates an OAuth-only account
	})
	require.NoError(t, err, "createOAuthUser: CreateUser failed")
	return [16]byte(row.ID)
}

// withProxy wraps q in proxy and returns a *setpassword.Store that uses the proxy.
func withProxy(q db.Querier, proxy *authsharedtest.QuerierProxy) *setpassword.Store {
	proxy.Querier = q
	return setpassword.NewStore(testPool).WithQuerier(proxy)
}

// newPassword is the plaintext used across tests; authsharedtest.MustHashPassword
// produces the corresponding bcrypt hash at the cost set by RunTestMain (MinCost).
const newPassword = "Str0ng!Pass"

// ── TestGetUserForSetPassword_Integration ─────────────────────────────────────

func TestGetUserForSetPassword_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("OAuth-only user — HasNoPassword is true", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		userID := createOAuthUser(t, q, authsharedtest.NewEmail(t))

		user, err := setpassword.NewStore(testPool).WithQuerier(q).GetUserForSetPassword(ctx, userID)
		require.NoError(t, err)
		require.True(t, user.HasNoPassword, "OAuth-only account must report HasNoPassword = true")
	})

	t.Run("user with password — HasNoPassword is false", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		// CreateUserUUID creates a user with a bcrypt password_hash (not NULL).
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, authsharedtest.NewEmail(t)))

		user, err := setpassword.NewStore(testPool).WithQuerier(q).GetUserForSetPassword(ctx, userID)
		require.NoError(t, err)
		require.False(t, user.HasNoPassword, "account with password must report HasNoPassword = false")
	})

	t.Run("unknown user ID returns ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		s, _ := txStores(t)
		_, err := s.GetUserForSetPassword(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	t.Run("FailGetUserForSetPassword returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetUserForSetPassword = true
		_, err := withProxy(q, proxy).GetUserForSetPassword(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}

// ── TestSetPasswordHashTx_Integration ─────────────────────────────────────────

func TestSetPasswordHashTx_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// setup creates an OAuth-only user and returns the store, raw queries, userID
	// string (for SetPasswordInput.UserID), and a pre-hashed password.
	setup := func(t *testing.T) (*setpassword.Store, *db.Queries, string, string) {
		t.Helper()
		s, q := txStores(t)
		userID := createOAuthUser(t, q, authsharedtest.NewEmail(t))
		return s, q, uuid.UUID(userID).String(), authsharedtest.MustHashPassword(t, newPassword)
	}

	// buildInput constructs a SetPasswordInput for use with SetPasswordHashTx.
	buildInput := func(userIDStr string) setpassword.SetPasswordInput {
		return setpassword.SetPasswordInput{
			UserID:    userIDStr,
			IPAddress: "127.0.0.1",
			UserAgent: "go-test/1.0",
		}
	}

	// ── T-01 (I) + T-18 + T-19: Happy path ───────────────────────────────────

	t.Run("T-01/T-18/T-19 happy path — hash written, audit row recorded", func(t *testing.T) {
		t.Parallel()
		s, q, userIDStr, newHash := setup(t)
		uid, _ := uuid.Parse(userIDStr)
		userID := [16]byte(uid)

		require.NoError(t, s.SetPasswordHashTx(ctx, buildInput(userIDStr), newHash))

		// T-19: password_hash column must contain a valid bcrypt hash of the new password.
		creds, err := q.GetUserPasswordHash(ctx, authsharedtest.ToPgtypeUUID(userID))
		require.NoError(t, err)
		require.True(t, creds.PasswordHash.Valid, "password_hash must not be NULL after SetPasswordHashTx")
		require.NoError(t,
			bcrypt.CompareHashAndPassword([]byte(creds.PasswordHash.String), []byte(newPassword)),
			"stored hash must be a valid bcrypt hash of the supplied password",
		)

		// T-18: exactly one password_set audit row must be written.
		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventPasswordSet),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, count, "exactly one password_set audit row must be written on success")
	})

	// ── T-03 (I): Concurrency race — second TX returns ErrPasswordAlreadySet ──

	t.Run("T-03 concurrency race — second SetPasswordHashTx returns ErrPasswordAlreadySet", func(t *testing.T) {
		t.Parallel()
		s, _, userIDStr, newHash := setup(t)
		in := buildInput(userIDStr)

		// First call succeeds — sets password_hash, so WHERE password_hash IS NULL
		// will match 0 rows on the next call.
		require.NoError(t, s.SetPasswordHashTx(ctx, in, newHash))

		// Second call must return ErrPasswordAlreadySet (0 rows affected).
		err := s.SetPasswordHashTx(ctx, in, newHash)
		require.ErrorIs(t, err, setpassword.ErrPasswordAlreadySet,
			"second SetPasswordHashTx must return ErrPasswordAlreadySet when hash is already set")
	})

	// ── Failure injection via QuerierProxy ────────────────────────────────────

	t.Run("FailSetPasswordHash returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, newHash := setup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailSetPasswordHash = true
		err := withProxy(q, proxy).SetPasswordHashTx(ctx, buildInput(userIDStr), newHash)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("FailInsertAuditLog returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, newHash := setup(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailInsertAuditLog = true
		err := withProxy(q, proxy).SetPasswordHashTx(ctx, buildInput(userIDStr), newHash)
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})

	t.Run("audit row is NOT written when SetPasswordHash UPDATE fails", func(t *testing.T) {
		t.Parallel()
		_, q, userIDStr, newHash := setup(t)
		uid, _ := uuid.Parse(userIDStr)
		userID := [16]byte(uid)

		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailSetPasswordHash = true
		_ = withProxy(q, proxy).SetPasswordHashTx(ctx, buildInput(userIDStr), newHash)

		count, err := q.CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
			UserID:    authsharedtest.ToPgtypeUUID(userID),
			EventType: string(audit.EventPasswordSet),
		})
		require.NoError(t, err)
		require.EqualValues(t, 0, count, "audit row must NOT be written when the UPDATE fails")
	})
}
