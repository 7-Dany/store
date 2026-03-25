//go:build integration_test

package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/domain/bitcoin/events"
	bitcoinsharedtest "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
)

// errKVDown is the sentinel returned by failingKVStore on every operation.
var errKVDown = errors.New("simulated redis connection failure")

// failingKVStore is a minimal implementation of kvstore.OnceStore and
// kvstore.SetStore that returns errKVDown for every mutating/reading
// operation. Used by T-28 to simulate Redis unavailability without spinning
// up a real (unreachable) server.
//
// NewRedisStore pings eagerly, so passing an unreachable address causes the
// constructor itself to fail — not the first command. failingKVStore sidesteps
// that by injecting the failure directly at the interface level.
type failingKVStore struct{}

// kvstore.Store ──────────────────────────────────────────────────────────────
func (*failingKVStore) Get(_ context.Context, _ string) (string, error)           { return "", errKVDown }
func (*failingKVStore) Set(_ context.Context, _, _ string, _ time.Duration) error { return errKVDown }
func (*failingKVStore) Delete(_ context.Context, _ string) error                  { return errKVDown }
func (*failingKVStore) Exists(_ context.Context, _ string) (bool, error)          { return false, errKVDown }
func (*failingKVStore) Keys(_ context.Context, _ string) ([]string, error)        { return nil, errKVDown }
func (*failingKVStore) StartCleanup(_ context.Context)                            {}
func (*failingKVStore) Close() error                                              { return nil }
func (*failingKVStore) RefreshTTL(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return false, errKVDown
}

// kvstore.OnceStore ──────────────────────────────────────────────────────────
func (*failingKVStore) ConsumeOnce(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return false, errKVDown
}

// kvstore.SetStore ───────────────────────────────────────────────────────────
func (*failingKVStore) SAdd(_ context.Context, _ string, _ ...string) (int64, error) {
	return 0, errKVDown
}
func (*failingKVStore) SRem(_ context.Context, _ string, _ ...string) (int64, error) {
	return 0, errKVDown
}
func (*failingKVStore) SCard(_ context.Context, _ string) (int64, error) { return 0, errKVDown }
func (*failingKVStore) SScan(_ context.Context, _ string, _ uint64, _ string, _ int64) ([]string, uint64, error) {
	return nil, 0, errKVDown
}

// compile-time checks that *failingKVStore satisfies both required interfaces.
var _ kvstore.OnceStore = (*failingKVStore)(nil)
var _ kvstore.SetStore = (*failingKVStore)(nil)

// testPool is initialised by TestMain when TEST_DATABASE_URL is set.
// Tests that require PostgreSQL skip themselves when testPool is nil.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	bitcoinsharedtest.RunTestMainWithDB(m, &testPool, 5)
}

// txStore returns a Store backed by a fresh Redis instance with no PostgreSQL
// pool — suitable for all Redis-only integration tests.
func txStore(t *testing.T) (*events.Store, *kvstore.RedisStore) {
	t.Helper()
	rs := bitcoinsharedtest.MustNewTestRedis(t)
	bitcoinsharedtest.FlushTestRedis(t, rs)
	store := events.NewStore(rs, rs, nil)
	return store, rs
}

// ── T-21: StoreSessionSID — written to Redis ──────────────────────────────────

func TestStore_StoreSessionSID_WrittenToRedis(t *testing.T) {
	store, rs := txStore(t)
	ctx := context.Background()

	jti := "test-jti-t21"
	sessionID := "test-session-id-t21"
	ttl := 5 * time.Minute

	err := store.StoreSessionSID(ctx, jti, sessionID, ttl)
	require.NoError(t, err)

	// Verify the value was written under the expected key.
	val, err := rs.Get(ctx, "btc:token:sid:"+jti)
	require.NoError(t, err)
	assert.Equal(t, sessionID, val, "session ID must be stored at btc:token:sid:{jti}")

	// TTL is validated implicitly: Set with ttl > 0 would fail if the value
	// were rejected. Key existence after the call confirms the write succeeded.
}

// ── T-22: ConsumeJTI — first call returns true ────────────────────────────────

func TestStore_ConsumeJTI_FirstCall_ReturnsTrue(t *testing.T) {
	store, _ := txStore(t)
	ctx := context.Background()

	consumed, err := store.ConsumeJTI(ctx, "test-jti-t22", 5*time.Minute)
	require.NoError(t, err)
	assert.True(t, consumed, "first ConsumeJTI call must return true (key created)")
}

// ── T-23: ConsumeJTI — second call returns false ──────────────────────────────

func TestStore_ConsumeJTI_SecondCall_ReturnsFalse(t *testing.T) {
	store, _ := txStore(t)
	ctx := context.Background()

	jti := "test-jti-t23"
	first, err := store.ConsumeJTI(ctx, jti, 5*time.Minute)
	require.NoError(t, err)
	require.True(t, first, "first call must return true")

	second, err := store.ConsumeJTI(ctx, jti, 5*time.Minute)
	require.NoError(t, err)
	assert.False(t, second, "second ConsumeJTI call must return false (already consumed)")
}

// ── T-24: GetDelSessionSID — deletes key ─────────────────────────────────────

func TestStore_GetDelSessionSID_DeletesKey(t *testing.T) {
	store, rs := txStore(t)
	ctx := context.Background()

	jti := "test-jti-t24"
	sessionID := "test-session-id-t24"

	err := store.StoreSessionSID(ctx, jti, sessionID, 5*time.Minute)
	require.NoError(t, err)

	got, err := store.GetDelSessionSID(ctx, jti)
	require.NoError(t, err)
	assert.Equal(t, sessionID, got, "GetDelSessionSID must return the stored session ID")

	// After GetDelSessionSID, the key must be gone.
	_, err = rs.Get(ctx, "btc:token:sid:"+jti)
	assert.ErrorIs(t, err, kvstore.ErrNotFound, "key must be deleted after GetDelSessionSID")
}

// ── T-28: StoreSessionSID — Redis down returns error ─────────────────────────

func TestStore_StoreSessionSID_RedisDown_ReturnsError(t *testing.T) {
	// failingKVStore returns errKVDown on every operation, simulating a Redis
	// instance that is unreachable. We cannot use NewRedisStore with a bad
	// address here because NewRedisStore pings eagerly and would fail at
	// construction time rather than at the first command.
	fkv := &failingKVStore{}
	store := events.NewStore(fkv, fkv, nil)

	err := store.StoreSessionSID(context.Background(), "jti", "sid", 5*time.Minute)
	require.Error(t, err)
	// telemetry.KVStore wraps with LayerKVStore = "kvstore" (lowercase).
	assert.Contains(t, err.Error(), "kvstore", "error must be wrapped with kvstore layer prefix")
}
