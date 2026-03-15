// Package adminsharedtest provides test-only helpers shared across all admin
// feature sub-packages. It must never be imported by production code.
package adminsharedtest

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/7-Dany/store/backend/internal/config"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// ─── UUID helpers ─────────────────────────────────────────────────────────────

// MustUUID parses a UUID string and panics if it is invalid.
func MustUUID(s string) [16]byte {
	id, err := uuid.Parse(s)
	if err != nil {
		panic("adminsharedtest.MustUUID: " + err.Error())
	}
	return [16]byte(id)
}

// RandomUUID returns a fresh random [16]byte UUID.
func RandomUUID() [16]byte {
	return [16]byte(uuid.New())
}

// ShortID returns the first 8 hex characters of a new random UUID.
// Useful for generating unique name suffixes in integration tests.
func ShortID() string {
	id := uuid.New()
	return fmt.Sprintf("%x", id[0:4])
}

// ─── Email helpers ────────────────────────────────────────────────────────────

// NewEmail returns a unique email address for the current test run.
func NewEmail(t *testing.T) string {
	t.Helper()
	id := uuid.New()
	return fmt.Sprintf("test+%x@example.com", id[0:4])
}

// ─── Password helpers ─────────────────────────────────────────────────────────

// MustHashPassword returns a bcrypt hash of pw at MinCost. Fails the test on error.
func MustHashPassword(t *testing.T, pw string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("adminsharedtest.MustHashPassword: %v", err)
	}
	return string(h)
}

// ─── Pool helpers ─────────────────────────────────────────────────────────────

// MustNewTestPool creates a pgxpool.Pool for integration tests using dsn.
func MustNewTestPool(dsn string, maxConns int32) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		panic("adminsharedtest.MustNewTestPool: parse config: " + err.Error())
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		panic("adminsharedtest.MustNewTestPool: connect: " + err.Error())
	}
	return pool
}

// RunTestMain is the canonical TestMain body for admin integration test suites.
// It initialises the pool when TEST_DATABASE_URL is set, runs the suite, and exits.
func RunTestMain(m *testing.M, pool **pgxpool.Pool, maxConns int32) {
	if dsn := config.TestDatabaseURL(); dsn != "" {
		*pool = MustNewTestPool(dsn, maxConns)
	}
	code := m.Run()
	if *pool != nil {
		(*pool).Close()
	}
	os.Exit(code)
}

// MustBeginTx begins a transaction on pool, registers a t.Cleanup that rolls it
// back, and returns the raw pgx.Tx together with a *db.Queries bound to that
// transaction.
func MustBeginTx(t *testing.T, pool *pgxpool.Pool) (pgx.Tx, *db.Queries) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx, db.New(tx)
}
