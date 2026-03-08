// Package oauthsharedtest provides test-only helpers shared across all oauth
// feature sub-packages. It must never be imported by production code.
package oauthsharedtest

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
)

// MustNewTestPool creates a *pgxpool.Pool for integration tests.
// Always pass maxConns = 20 (required by ADR-003).
func MustNewTestPool(dsn string, maxConns int32) *pgxpool.Pool {
	return authsharedtest.MustNewTestPool(dsn, maxConns)
}

// RunTestMain initialises the test pool when TEST_DATABASE_URL is set, lowers
// the bcrypt cost for fast unit tests, runs the suite, and exits. Call from
// TestMain in every store_test.go:
//
//	func TestMain(m *testing.M) { oauthsharedtest.RunTestMain(m, &testPool, 20) }
func RunTestMain(m *testing.M, pool **pgxpool.Pool, maxConns int32) {
	authsharedtest.RunTestMain(m, pool, maxConns)
}
