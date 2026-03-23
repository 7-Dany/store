package bitcoinsharedtest

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/7-Dany/store/backend/internal/config"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
)

// ErrProxy is the sentinel error returned by any fake or proxy injection in
// the bitcoin domain tests.
var ErrProxy = errors.New("bitcoinsharedtest: injected error")

// MustNewTestRedis connects to the test Redis instance and returns a
// *kvstore.RedisStore. Calls t.Skip when TEST_REDIS_URL is unset.
func MustNewTestRedis(t *testing.T) *kvstore.RedisStore {
	t.Helper()
	url := config.TestRedisURL()
	if url == "" {
		t.Skip("no test Redis configured (set TEST_REDIS_URL or REDIS_URL)")
	}
	s, err := kvstore.NewRedisStore(url)
	if err != nil {
		t.Fatalf("MustNewTestRedis: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// FlushTestRedis flushes all keys from the Redis test instance.
// Call at the start of each integration test that writes to Redis to ensure
// a clean slate. Only works correctly when TEST_REDIS_URL points to a
// dedicated test instance — never run against a shared or production Redis.
func FlushTestRedis(t *testing.T, s *kvstore.RedisStore) {
	t.Helper()
	// Use the underlying client flush via a raw command. Since RedisStore does
	// not expose FLUSHDB directly, we delete the keys we created via Keys().
	ctx := context.Background()
	keys, err := s.Keys(ctx, "")
	if err != nil {
		t.Fatalf("FlushTestRedis: list keys: %v", err)
	}
	for _, k := range keys {
		if err := s.Delete(ctx, k); err != nil {
			t.Logf("FlushTestRedis: delete %q: %v", k, err)
		}
	}
}

// MustNewTestPool creates a pgxpool.Pool for integration tests using dsn.
// maxConns sets cfg.MaxConns; pass 0 to keep the driver default.
// Panics on parse or connection error — TestMain should not mask pool failures.
//
// Mirror of authsharedtest.MustNewTestPool for the bitcoin domain.
// Callers that accept *testing.T should use RunTestMainWithDB to initialise the
// pool once in TestMain rather than calling MustNewTestPool per-test.
func MustNewTestPool(dsn string, maxConns int32) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		panic("bitcoinsharedtest.MustNewTestPool: parse config: " + err.Error())
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		panic("bitcoinsharedtest.MustNewTestPool: connect: " + err.Error())
	}
	return pool
}

// RunTestMain is the canonical TestMain for bitcoin domain integration tests
// that require only Redis. Call from TestMain in store_test.go:
//
//	func TestMain(m *testing.M) { bitcoinsharedtest.RunTestMain(m) }
func RunTestMain(m *testing.M) {
	os.Exit(m.Run())
}

// RunTestMainWithDB is like RunTestMain but also initialises a pgxpool.Pool
// when TEST_DATABASE_URL is set. Use this in test suites that exercise both
// Redis and PostgreSQL (e.g. WriteAuditLog integration tests):
//
//	var testPool *pgxpool.Pool
//	func TestMain(m *testing.M) { bitcoinsharedtest.RunTestMainWithDB(m, &testPool, 5) }
//
// maxConns is passed directly to MustNewTestPool; 5 is sufficient for bitcoin
// domain store tests which have no concurrent pool transactions.
func RunTestMainWithDB(m *testing.M, pool **pgxpool.Pool, maxConns int32) {
	if dsn := config.TestDatabaseURL(); dsn != "" {
		*pool = MustNewTestPool(dsn, maxConns)
	}
	code := m.Run()
	if *pool != nil {
		(*pool).Close()
	}
	os.Exit(code)
}
