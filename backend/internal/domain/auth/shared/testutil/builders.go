// Package authsharedtest provides test-only helpers shared across all auth
// feature sub-packages. It must never be imported by production code.
package authsharedtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/config"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/domain/auth/login"
	"github.com/7-Dany/store/backend/internal/domain/auth/register"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// ─── UUID helpers ────────────────────────────────────────────────────────────

// MustUUID parses a UUID string and panics if it is invalid. Useful for
// package-level test constants.
func MustUUID(s string) [16]byte {
	id, err := uuid.Parse(s)
	if err != nil {
		panic("authsharedtest.MustUUID: " + err.Error())
	}
	return [16]byte(id)
}

// RandomUUID returns a fresh random [16]byte UUID. Panics on CSPRNG failure.
func RandomUUID() [16]byte {
	return [16]byte(uuid.New())
}

// ToPgtypeUUID converts a raw [16]byte to pgtype.UUID.
func ToPgtypeUUID(b [16]byte) pgtype.UUID {
	return pgtype.UUID{Bytes: b, Valid: true}
}

// ─── Password helpers ────────────────────────────────────────────────────────

// MustHashPassword returns a bcrypt hash of pw using authshared.HashPassword.
// Fails the test on error.
func MustHashPassword(t *testing.T, pw string) string {
	t.Helper()
	h, err := authshared.HashPassword(pw)
	// Unreachable: t.Fatalf branches in helpers that accept *testing.T cannot
	// be exercised without a mock T, which Go's testing model does not support.
	if err != nil {
		t.Fatalf("authsharedtest.MustHashPassword: %v", err)
	}
	return h
}

// OTPPlaintext is the canonical OTP code used by MustOTPHash. Export it so
// callers can compare against the plaintext without duplicating the magic string.
const OTPPlaintext = "123456"

// MustOTPHash returns a bcrypt hash of OTPPlaintext at bcrypt.MinCost.
// Fails the test on error.
func MustOTPHash(t *testing.T) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(OTPPlaintext), bcrypt.MinCost)
	// Unreachable: t.Fatalf branches in helpers that accept *testing.T cannot
	// be exercised without a mock T, which Go's testing model does not support.
	if err != nil {
		t.Fatalf("authsharedtest.MustOTPHash: %v", err)
	}
	return string(h)
}

// ─── Email helpers ───────────────────────────────────────────────────────────

// NewEmail returns a unique email address for the current test run.
// It uses the first four raw bytes of a UUID (encoded as hex) to avoid
// relying on the string representation's hyphen positions.
func NewEmail(t *testing.T) string {
	t.Helper()
	id := uuid.New()
	return fmt.Sprintf("test+%x@example.com", id[0:4])
}

// ─── Time helpers ────────────────────────────────────────────────────────────

// Future returns a time.Time one hour in the future from now.
func Future() time.Time {
	return time.Now().Add(time.Hour)
}

// Past returns a time.Time one hour in the past from now.
func Past() time.Time {
	return time.Now().Add(-time.Hour)
}

// ─── HTTP request builders ───────────────────────────────────────────────────

// JSONRequest builds an httptest.Request whose body is the JSON encoding of v.
// Panics if v cannot be marshalled.
func JSONRequest(method, target string, v any) *http.Request {
	b, err := json.Marshal(v)
	if err != nil {
		panic("authsharedtest.JSONRequest: " + err.Error())
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// RegisterBody returns a map suitable for encoding as a register request.
func RegisterBody(displayName, email, password string) map[string]string {
	return map[string]string{
		"display_name": displayName,
		"email":        email,
		"password":     password,
	}
}

// LoginBody returns a map suitable for encoding as a login request.
func LoginBody(identifier, password string) map[string]string {
	return map[string]string{
		"identifier": identifier,
		"password":   password,
	}
}

// VerifyEmailBody returns a map suitable for encoding as a verify-email request.
func VerifyEmailBody(email, code string) map[string]string {
	return map[string]string{"email": email, "code": code}
}

// ResendBody returns a map suitable for encoding as a resend-verification request.
func ResendBody(email string) map[string]string {
	return map[string]string{"email": email}
}

// ConfirmUnlockBody returns a map suitable for encoding as a confirm-unlock request.
func ConfirmUnlockBody(email, code string) map[string]string {
	return map[string]string{"email": email, "code": code}
}

// ResetPasswordBody returns a map suitable for encoding as a reset-password request.
func ResetPasswordBody(email, code, newPassword string) map[string]string {
	return map[string]string{
		"email":        email,
		"code":         code,
		"new_password": newPassword,
	}
}

// ─── Integration-test pool helpers ───────────────────────────────────────────

// MustNewTestPool creates a pgxpool.Pool for integration tests using dsn.
// maxConns sets cfg.MaxConns; pass 0 to keep the driver default.
// Panics on parse or connection error — TestMain should not mask pool failures.
//
// ADR-003 requires at least 20 connections per feature test suite because
// IncrementAttemptsTx and IncrementLoginFailuresTx always open a fresh pool
// transaction that must run concurrently with the outer test transaction.
// Always pass maxConns = 20 unless a specific feature test has a documented
// reason to use a different value.
func MustNewTestPool(dsn string, maxConns int32) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		panic("authsharedtest.MustNewTestPool: parse config: " + err.Error())
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		panic("authsharedtest.MustNewTestPool: connect: " + err.Error())
	}
	return pool
}

// ─── User creation helpers ──────────────────────────────────────────────────

// CreateUser inserts a user via register.Store.CreateUserTx and returns the
// CreatedUser result. q must be bound to the test transaction so the row is
// scoped to the rollback-on-cleanup transaction. pool is used to construct the
// register.Store (it only performs queries through q).
func CreateUser(t *testing.T, pool *pgxpool.Pool, q db.Querier, email string) register.CreatedUser {
	t.Helper()
	result, err := register.NewStore(pool).WithQuerier(q).CreateUserTx(context.Background(), register.CreateUserInput{
		DisplayName:  "Test User",
		Email:        email,
		PasswordHash: MustHashPassword(t, "S3cure!Pass"),
		CodeHash:     MustOTPHash(t),
		TTL:          15 * time.Minute,
		IPAddress:    "127.0.0.1",
		UserAgent:    "go-test/1.0",
	})
	// Unreachable: t.Fatalf branches in helpers that accept *testing.T cannot
	// be exercised without a mock T, which Go's testing model does not support.
	if err != nil {
		t.Fatalf("authsharedtest.CreateUser: %v", err)
	}
	return result
}

// CreateUserUUID is like CreateUser but parses and returns the new user's UUID.
// Useful when callers only need the ID and not the full CreatedUser value.
func CreateUserUUID(t *testing.T, pool *pgxpool.Pool, q db.Querier, email string) uuid.UUID {
	t.Helper()
	result := CreateUser(t, pool, q, email)
	id, err := uuid.Parse(result.UserID)
	// Unreachable: t.Fatalf branches in helpers that accept *testing.T cannot
	// be exercised without a mock T, which Go's testing model does not support.
	if err != nil {
		t.Fatalf("authsharedtest.CreateUserUUID: parse uuid %q: %v", result.UserID, err)
	}
	return id
}

// CreateUserDirect inserts only the user row (no verification token, no audit
// log) by calling q.CreateUser directly. Use this when the test specifically
// needs a user that has zero verification tokens — for example to assert that
// GetLatestTokenCreatedAt returns zero time. For all other cases prefer
// CreateUser / CreateUserUUID, which go through the full register flow.
func CreateUserDirect(t *testing.T, q db.Querier, email string) uuid.UUID {
	t.Helper()
	row, err := q.CreateUser(context.Background(), db.CreateUserParams{
		Email:        pgtype.Text{String: email, Valid: true},
		DisplayName:  pgtype.Text{String: "Test User", Valid: true},
		PasswordHash: pgtype.Text{String: MustHashPassword(t, "S3cure!Pass"), Valid: true},
	})
	// Unreachable: t.Fatalf branches in helpers that accept *testing.T cannot
	// be exercised without a mock T, which Go's testing model does not support.
	if err != nil {
		t.Fatalf("authsharedtest.CreateUserDirect: %v", err)
	}
	return row.ID
}

// CreateUserCommitted inserts a user row in an independent committed transaction,
// generates a unique email internally, and registers a t.Cleanup that deletes
// the row after the test. Returns (email, userID) as [16]byte.
//
// Used by login and unlock store tests whose helpers call
// authsharedtest.CreateUserCommitted(t, pool) and destructure the result as
// (email, userID).
func CreateUserCommitted(t *testing.T, pool *pgxpool.Pool) (string, [16]byte) {
	t.Helper()
	email := NewEmail(t)
	id := CreateUserCommittedWithEmail(t, pool, email)
	return email, [16]byte(id)
}

// CreateUserCommittedWithEmail inserts a user row in an independent committed
// transaction using the provided email and registers a t.Cleanup that deletes
// the row after the test. Use this (not CreateUserUUID) when IncrementAttemptsTx
// or other independent-tx methods need committed rows visible to fresh pool
// connections and the caller already has a specific email address.
func CreateUserCommittedWithEmail(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	q := db.New(pool)
	row, err := q.CreateUser(context.Background(), db.CreateUserParams{
		Email:        pgtype.Text{String: email, Valid: true},
		DisplayName:  pgtype.Text{String: "Test User", Valid: true},
		PasswordHash: pgtype.Text{String: MustHashPassword(t, "P@ssw0rd!1"), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateUserCommittedWithEmail: %v", err)
	}
	t.Cleanup(func() {
		_ = db.New(pool).DeleteUserByEmail(context.Background(), email)
	})
	return row.ID
}

// CreateVerificationTokenCommitted inserts a verification token row in an
// independent committed transaction and registers a t.Cleanup to delete it.
func CreateVerificationTokenCommitted(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, email, codeHash string) [16]byte {
	t.Helper()
	q := db.New(pool)
	row, err := q.CreateEmailVerificationToken(context.Background(), db.CreateEmailVerificationTokenParams{
		UserID:     pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
		Email:      email,
		CodeHash:   pgtype.Text{String: codeHash, Valid: true},
		TtlSeconds: 900,
	})
	if err != nil {
		t.Fatalf("CreateVerificationTokenCommitted: %v", err)
	}
	t.Cleanup(func() {
		_ = db.New(pool).DeleteOTPTokenByID(
			context.Background(),
			pgtype.UUID{Bytes: [16]byte(row.ID), Valid: true},
		)
	})
	return [16]byte(row.ID)
}

// CreateUserWithNullDisplayName inserts a user row with NULL display_name and
// NULL avatar_url and returns the new user's UUID. The NULL columns allow profile
// store tests to verify that the store maps NULL text columns to empty strings
// rather than to "Test User" (the value used by CreateUser / CreateUserUUID).
// For all other integration tests prefer CreateUser / CreateUserUUID.
func CreateUserWithNullDisplayName(t *testing.T, pool *pgxpool.Pool, q db.Querier, email string) uuid.UUID {
	t.Helper()
	row, err := q.CreateUser(context.Background(), db.CreateUserParams{
		Email:        pgtype.Text{String: email, Valid: true},
		DisplayName:  pgtype.Text{}, // NULL — exercises the store's NULL-to-empty-string mapping
		PasswordHash: pgtype.Text{String: MustHashPassword(t, "S3cure!Pass"), Valid: true},
	})
	// Unreachable: t.Fatalf branches in helpers that accept *testing.T cannot
	// be exercised without a mock T, which Go's testing model does not support.
	if err != nil {
		t.Fatalf("authsharedtest.CreateUserWithNullDisplayName: %v", err)
	}
	return row.ID
}

// CreateSessionNullIP creates an active login session for userID with a NULL
// ip_address. Use this in store integration tests that need to verify the store
// maps NULL ip_address to an empty string in the ActiveSession result.
func CreateSessionNullIP(t *testing.T, pool *pgxpool.Pool, q db.Querier, userID [16]byte) login.LoggedInSession {
	t.Helper()
	s, err := login.NewStore(pool).WithQuerier(q).LoginTx(context.Background(), login.LoginTxInput{
		UserID:    userID,
		IPAddress: "", // empty string maps to NULL ip_address via IPToNullable in the store
		UserAgent: "authsharedtest/CreateSessionNullIP",
	})
	// Unreachable: t.Fatalf branches in helpers that accept *testing.T cannot
	// be exercised without a mock T, which Go's testing model does not support.
	if err != nil {
		t.Fatalf("authsharedtest.CreateSessionNullIP: %v", err)
	}
	return s
}

// RunTestMain is the canonical TestMain body shared by every auth sub-package.
// It lowers the bcrypt cost for fast unit tests, initialises the
// integration-test pool when TEST_DATABASE_URL is set, runs the suite, closes
// the pool, and calls os.Exit. Each package's non-build-tagged suite file
// should call this and nothing else:
//
//	func TestMain(m *testing.M) { authsharedtest.RunTestMain(m, &testPool, 20) }
func RunTestMain(m *testing.M, pool **pgxpool.Pool, maxConns int32) {
	authshared.SetBcryptCostForTest(bcrypt.MinCost)
	if dsn := config.TestDatabaseURL(); dsn != "" {
		*pool = MustNewTestPool(dsn, maxConns)
	}
	code := m.Run()
	if *pool != nil {
		(*pool).Close()
	}
	os.Exit(code)
}

// MustHashOTPCode hashes code using bcrypt at the package-level cost variable
// (controlled by RunTestMain / SetBcryptCostForTest). Use in place of direct
// bcrypt.GenerateFromPassword calls in service unit tests.
func MustHashOTPCode(t *testing.T, code string) string {
	t.Helper()
	hash, err := authshared.GenerateCodeHashForTest(code)
	if err != nil {
		t.Fatalf("MustHashOTPCode: %v", err)
	}
	return hash
}

// MustBeginTx begins a transaction on pool, registers a t.Cleanup that rolls
// it back, and returns the raw pgx.Tx together with a *db.Queries bound to
// that transaction.
//
// Usage in txStores:
//
//	tx, q := authsharedtest.MustBeginTx(t, testPool)
//	s := myfeature.NewStore(testPool).WithQuerier(q)
//	return s, q
//
// Tests that need to issue assertion queries directly (e.g. verifying a row's
// state without going through the store) can use q or tx for those queries;
// the results are scoped to the same transaction and are rolled back on cleanup.
func MustBeginTx(t *testing.T, pool *pgxpool.Pool) (pgx.Tx, *db.Queries) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	// Unreachable: t.Fatalf branches in helpers that accept *testing.T cannot
	// be exercised without a mock T, which Go's testing model does not support.
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx, db.New(tx)
}

// CreateSession calls login.NewStore(pool).WithQuerier(q).LoginTx and returns
// the resulting LoggedInSession. It calls t.Fatalf on any error.
// Use this in store integration tests that need an active session without
// importing the login feature package.
func CreateSession(t *testing.T, pool *pgxpool.Pool, q db.Querier, userID [16]byte) login.LoggedInSession {
	t.Helper()
	s, err := login.NewStore(pool).WithQuerier(q).LoginTx(context.Background(), login.LoginTxInput{
		UserID:    userID,
		IPAddress: "127.0.0.1",
		UserAgent: "authsharedtest/CreateSession",
	})
	// Unreachable: t.Fatalf branches in helpers that accept *testing.T cannot
	// be exercised without a mock T, which Go's testing model does not support.
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return s
}
