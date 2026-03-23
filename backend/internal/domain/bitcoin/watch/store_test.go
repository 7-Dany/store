//go:build integration_test

package watch_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	"github.com/7-Dany/store/backend/internal/domain/bitcoin/watch"
	bitcoinsharedtest "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
)

// testPool is initialised by TestMain when TEST_DATABASE_URL is set.
// Tests that require PostgreSQL skip themselves when testPool is nil.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	// 5 connections is sufficient — bitcoin watch store tests have no concurrent
	// pool transactions (all writes are independent audit log inserts).
	bitcoinsharedtest.RunTestMainWithDB(m, &testPool, 5)
}

// txStore returns a Store backed by a fresh Redis instance with no PostgreSQL
// pool — suitable for all Redis-only integration tests.
//
// Tests in this file are intentionally NOT parallel. They share a single Redis
// instance; running t.Parallel() alongside per-test FlushTestRedis calls means
// one test's flush can destroy another test's data mid-run (TEST-1 fix).
func txStore(t *testing.T) (*watch.Store, *kvstore.RedisStore) {
	t.Helper()
	rs := bitcoinsharedtest.MustNewTestRedis(t)
	bitcoinsharedtest.FlushTestRedis(t, rs)
	// Pass nil for the pool — these tests do not exercise WriteAuditLog.
	store := watch.NewStore(rs, rs, rs, rs, nil, "testnet4")
	return store, rs
}

// txStoreWithDB returns a Store backed by both Redis and testPool.
// Skips the test when testPool is nil (TEST_DATABASE_URL not set).
func txStoreWithDB(t *testing.T) *watch.Store {
	t.Helper()
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping WriteAuditLog integration test")
	}
	rs := bitcoinsharedtest.MustNewTestRedis(t)
	bitcoinsharedtest.FlushTestRedis(t, rs)
	return watch.NewStore(rs, rs, rs, rs, testPool, "testnet4")
}

// createTestUser inserts a minimal user row directly and registers a t.Cleanup
// that deletes it. Returns the new user's UUID as a string.
//
// We create a real user because auth_audit_log.user_id has a FK reference to
// users(id) — inserting a non-existent UUID causes a FK violation.
// The password hash is a bcrypt hash at MinCost so the insert satisfies the
// chk_users_password_hash_format constraint without being slow.
func createTestUser(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	q := db.New(testPool)

	hash, err := bcrypt.GenerateFromPassword([]byte("test-watch-store"), bcrypt.MinCost)
	require.NoError(t, err)

	email := fmt.Sprintf("btc-watch-test+%s@example.com", uuid.New().String())
	row, err := q.CreateUser(ctx, db.CreateUserParams{
		Email:        pgtype.Text{String: email, Valid: true},
		DisplayName:  pgtype.Text{String: "Watch Test User", Valid: true},
		PasswordHash: pgtype.Text{String: string(hash), Valid: true},
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		// DeleteUserByEmail is generated from auth_test.sql.
		_ = db.New(testPool).DeleteUserByEmail(context.Background(), email)
	})
	return row.ID.String()
}

// ── T-28: RunWatchCap — under limit ──────────────────────────────────────────

func TestRunWatchCap_UnderLimit_Integration(t *testing.T) {
	store, _ := txStore(t)

	success, newCount, added, err := store.RunWatchCap(context.Background(), "user-t28", 100,
		[]string{"tb1qaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0001"})

	require.NoError(t, err)
	assert.Equal(t, int64(1), success)
	assert.Equal(t, int64(1), newCount)
	assert.Equal(t, int64(1), added)
}

// ── T-29: RunWatchCap — exactly at limit ──────────────────────────────────────

func TestRunWatchCap_ExactlyAtLimit_Integration(t *testing.T) {
	store, _ := txStore(t)

	ctx := context.Background()
	addrs := make([]string, 4)
	for i := range addrs {
		addrs[i] = "tb1qaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + string(rune('a'+i))
	}
	_, _, _, err := store.RunWatchCap(ctx, "user-t29", 5, addrs)
	require.NoError(t, err)

	success, newCount, added, err := store.RunWatchCap(ctx, "user-t29", 5,
		[]string{"tb1qbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), success)
	assert.Equal(t, int64(5), newCount)
	assert.Equal(t, int64(1), added)
}

// ── T-30: RunWatchCap — one over limit → cap exceeded ─────────────────────────

func TestRunWatchCap_OverLimit_Integration(t *testing.T) {
	store, _ := txStore(t)

	ctx := context.Background()
	addrs := make([]string, 5)
	for i := range addrs {
		addrs[i] = "tb1qaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + string(rune('a'+i))
	}
	_, _, _, err := store.RunWatchCap(ctx, "user-t30", 5, addrs)
	require.NoError(t, err)

	success, _, added, err := store.RunWatchCap(ctx, "user-t30", 5,
		[]string{"tb1qbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), success, "should return cap exceeded")
	assert.Equal(t, int64(0), added, "no addresses should have been added")
}

// ── T-31: RunWatchCap — re-registration (added_count == 0) ───────────────────

func TestRunWatchCap_Reregistration_Integration(t *testing.T) {
	store, _ := txStore(t)

	ctx := context.Background()
	addr := "tb1q0000000000000000000000000000000000000001"

	_, _, firstAdded, err := store.RunWatchCap(ctx, "user-t31", 100, []string{addr})
	require.NoError(t, err)
	assert.Equal(t, int64(1), firstAdded)

	success, _, reAdded, err := store.RunWatchCap(ctx, "user-t31", 100, []string{addr})
	require.NoError(t, err)
	assert.Equal(t, int64(1), success, "re-registration should return success=1")
	assert.Equal(t, int64(0), reAdded, "re-registration should return added_count=0")
}

// ── T-32: RunWatchCap — 7-day registration window expired ────────────────────
//
// Seeds registered_at to 8 days in the past so the Lua script returns -1.
// Uses watch.RegAtKey (exported via export_test.go) to construct the Redis key.

func TestRunWatchCap_RegistrationWindowExpired_Integration(t *testing.T) {
	store, rs := txStore(t)
	ctx := context.Background()

	eightDaysAgo := strconv.FormatInt(time.Now().Add(-8*24*time.Hour).Unix(), 10)
	err := rs.Set(ctx, watch.RegAtKey("user-t32"), eightDaysAgo, 0)
	require.NoError(t, err, "failed to seed registered_at for expiry test")

	success, _, _, err := store.RunWatchCap(ctx, "user-t32", 100,
		[]string{"tb1q0000000000000000000000000000000000000001"})
	require.NoError(t, err)
	assert.Equal(t, int64(-1), success,
		"Lua script must return -1 when registered_at is older than 7 days")
}

// ── T-36: IncrGlobalWatchCount ────────────────────────────────────────────────

func TestIncrGlobalWatchCount_Integration(t *testing.T) {
	store, rs := txStore(t)

	ctx := context.Background()
	err := store.IncrGlobalWatchCount(ctx)
	require.NoError(t, err)

	val, err := rs.Get(ctx, "btc:global:watch_count")
	require.NoError(t, err)
	assert.Equal(t, "1", val)
}

// ── T-37: PublishCacheInvalidation ────────────────────────────────────────────
//
// Subscribes before publishing and waits 50 ms for the subscription to be
// acknowledged server-side. Redis pub/sub is at-most-once with no buffering —
// a PUBLISH that arrives before the server has processed the SUBSCRIBE is lost.

func TestPublishCacheInvalidation_Integration(t *testing.T) {
	store, rs := txStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, unsub := rs.Subscribe(ctx, "btc:watch:invalidate:user-t37")
	defer unsub()

	// Ensure the server-side subscription is active before publishing (TEST-4 fix).
	time.Sleep(50 * time.Millisecond)

	err := store.PublishCacheInvalidation(context.Background(), "user-t37")
	require.NoError(t, err)

	select {
	case msg := <-ch:
		assert.Equal(t, "btc:watch:invalidate:user-t37", msg.Channel)
	case <-time.After(2 * time.Second):
		t.Fatal("no pub/sub message received within 2s")
	}
}

// ── T-38: ListWatchAddressKeys ────────────────────────────────────────────────

func TestListWatchAddressKeys_Integration(t *testing.T) {
	store, _ := txStore(t)

	ctx := context.Background()
	_, _, _, err := store.RunWatchCap(ctx, "user-t38a", 100,
		[]string{"tb1q0000000000000000000000000000000000000001"})
	require.NoError(t, err)
	_, _, _, err = store.RunWatchCap(ctx, "user-t38b", 100,
		[]string{"tb1q0000000000000000000000000000000000000002"})
	require.NoError(t, err)

	keys, nextCursor, err := store.ListWatchAddressKeys(ctx, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), nextCursor, "single page scan should return cursor=0")
	assert.GreaterOrEqual(t, len(keys), 2, "should find at least 2 watch address keys")
}

// ── T-39: GetWatchSetSize ─────────────────────────────────────────────────────

func TestGetWatchSetSize_Integration(t *testing.T) {
	store, _ := txStore(t)

	ctx := context.Background()
	_, _, _, err := store.RunWatchCap(ctx, "user-t39", 100,
		[]string{
			"tb1q0000000000000000000000000000000000000001",
			"tb1q0000000000000000000000000000000000000002",
		})
	require.NoError(t, err)

	count, err := store.GetWatchSetSize(ctx, watch.SetKey("user-t39"))
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

// ── T-40: WriteAuditLog — valid UUID user ID ──────────────────────────────────
//
// Verifies that WriteAuditLog inserts an auth_audit_log row with the correct
// non-NULL user_id when a parseable UUID is supplied.
//
// auth_audit_log.user_id has a FK reference to users(id), so a real user row
// must exist before the audit log insert. createTestUser handles setup and
// cleanup.
// CountAuditEventsByUser is already defined in auth_test.sql — reused here.

func TestWriteAuditLog_ValidUserID_Integration(t *testing.T) {
	store := txStoreWithDB(t)
	ctx := context.Background()

	userID := createTestUser(t)

	err := store.WriteAuditLog(ctx,
		audit.EventBitcoinAddressWatched,
		userID,
		"1.2.3.4",
		map[string]string{"added_count": "1"},
	)
	require.NoError(t, err)

	uid, err := uuid.Parse(userID)
	require.NoError(t, err)

	// CountAuditEventsByUser returns COUNT(*)::int which sqlc maps to int32.
	count, err := db.New(testPool).CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
		UserID:    pgtype.UUID{Bytes: [16]byte(uid), Valid: true},
		EventType: string(audit.EventBitcoinAddressWatched),
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), count, "one audit row must be inserted for the valid user ID")
}

// ── T-41: WriteAuditLog — empty user ID (anonymous/rate-limit path) ──────────
//
// Verifies that WriteAuditLog accepts an empty userID (the pre-JWT rate-limit
// path in the middleware) and stores NULL in auth_audit_log.user_id.
// Confirms auth_audit_log.user_id is nullable (SEC-3 fix).
//
// Uses GetLatestAuditEvent from auth_test.sql (ordered by UUID v7 PK so the
// most recently inserted row is always returned) to read back user_id.
// EventBitcoinWatchRateLimitHit is only written by this test in the test DB,
// so the query reliably returns the right row.

func TestWriteAuditLog_EmptyUserID_Integration(t *testing.T) {
	store := txStoreWithDB(t)
	ctx := context.Background()

	err := store.WriteAuditLog(ctx,
		audit.EventBitcoinWatchRateLimitHit,
		"",        // anonymous — no JWT has been validated yet
		"10.0.0.1",
		nil,
	)
	require.NoError(t, err,
		"empty userID must produce a NULL user_id row — auth_audit_log.user_id must be nullable")

	row, err := db.New(testPool).GetLatestAuditEvent(ctx,
		string(audit.EventBitcoinWatchRateLimitHit))
	require.NoError(t, err)
	assert.False(t, row.UserID.Valid,
		"user_id must be NULL for the anonymous rate-limit path")
}

// ── T-42: WriteAuditLog — invalid IP address falls back to NULL ───────────────
//
// Verifies that WriteAuditLog silently ignores an unparseable sourceIP and
// stores NULL in auth_audit_log.ip_address rather than returning an error.
// Uses GetLatestAuditEvent from auth_test.sql to read back ip_address.
// EventBitcoinWatchInvalidAddress is only written by this test in the test DB.

func TestWriteAuditLog_InvalidIP_Integration(t *testing.T) {
	store := txStoreWithDB(t)
	ctx := context.Background()

	err := store.WriteAuditLog(ctx,
		audit.EventBitcoinWatchInvalidAddress,
		"",
		"not-an-ip-address", // netip.ParseAddr will fail; ip_address should be NULL
		map[string]string{"invalid_address_hmac": "abc123"},
	)
	require.NoError(t, err,
		"unparseable IP must result in NULL ip_address, not an error")

	row, err := db.New(testPool).GetLatestAuditEvent(ctx,
		string(audit.EventBitcoinWatchInvalidAddress))
	require.NoError(t, err)
	assert.Nil(t, row.IpAddress,
		"ip_address must be NULL when the source IP string cannot be parsed")
}
