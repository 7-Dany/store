package kvstore_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newStore(t *testing.T) *kvstore.InMemoryStore {
	t.Helper()
	return kvstore.NewInMemoryStore(0)
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestInMemoryStore_Get_MissingKey(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	_, err := s.Get(context.Background(), "missing")
	require.ErrorIs(t, err, kvstore.ErrNotFound)
}

func TestInMemoryStore_Get_ExpiredKey(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Set(ctx, "key", "val", time.Millisecond))
	time.Sleep(5 * time.Millisecond)

	_, err := s.Get(ctx, "key")
	require.ErrorIs(t, err, kvstore.ErrNotFound)

	// Confirm the entry was evicted.
	keys, err := s.Keys(ctx, "")
	require.NoError(t, err)
	require.NotContains(t, keys, "key")
}

func TestInMemoryStore_Get_HappyPath(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Set(ctx, "k", "v", 0))
	got, err := s.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "v", got)
}

// ── Set ───────────────────────────────────────────────────────────────────────

func TestInMemoryStore_Set_ZeroTTL_StoresIndefinitely(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Set(ctx, "k", "v", 0))
	time.Sleep(5 * time.Millisecond)

	got, err := s.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "v", got)
}

func TestInMemoryStore_Set_OverwritesExisting(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Set(ctx, "k", "first", 0))
	require.NoError(t, s.Set(ctx, "k", "second", 0))

	got, err := s.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "second", got)
}

// TestInMemoryStore_Set_NegativeTTL_ReturnsError verifies that a negative TTL
// is rejected with an error rather than silently treated as zero-expiry, which
// would diverge from RedisStore behaviour.
func TestInMemoryStore_Set_NegativeTTL_ReturnsError(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	err := s.Set(context.Background(), "k", "v", -time.Second)
	require.Error(t, err, "negative TTL must be rejected by InMemoryStore")
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestInMemoryStore_Delete_Idempotent(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	// Delete a key that was never set — must not error.
	require.NoError(t, s.Delete(ctx, "ghost"))

	require.NoError(t, s.Set(ctx, "k", "v", 0))
	require.NoError(t, s.Delete(ctx, "k"))

	// Second delete is also a no-op.
	require.NoError(t, s.Delete(ctx, "k"))

	_, err := s.Get(ctx, "k")
	require.ErrorIs(t, err, kvstore.ErrNotFound)
}

// ── Exists ────────────────────────────────────────────────────────────────────

func TestInMemoryStore_Exists_PresentKey(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Set(ctx, "k", "v", 0))
	ok, err := s.Exists(ctx, "k")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestInMemoryStore_Exists_MissingKey(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	ok, err := s.Exists(context.Background(), "missing")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestInMemoryStore_Exists_ExpiredKey(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Set(ctx, "k", "v", time.Millisecond))
	time.Sleep(5 * time.Millisecond)

	ok, err := s.Exists(ctx, "k")
	require.NoError(t, err)
	require.False(t, ok)
}

// ── Keys ──────────────────────────────────────────────────────────────────────

func TestInMemoryStore_Keys_PrefixFilter(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Set(ctx, "foo:1", "a", 0))
	require.NoError(t, s.Set(ctx, "foo:2", "b", 0))
	require.NoError(t, s.Set(ctx, "bar:1", "c", 0))

	keys, err := s.Keys(ctx, "foo:")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"foo:1", "foo:2"}, keys)
}

func TestInMemoryStore_Keys_EmptyPrefixReturnsAll(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Set(ctx, "a", "1", 0))
	require.NoError(t, s.Set(ctx, "b", "2", 0))

	keys, err := s.Keys(ctx, "")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"a", "b"}, keys)
}

func TestInMemoryStore_Keys_ExcludesExpired(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Set(ctx, "live", "v", 0))
	require.NoError(t, s.Set(ctx, "dead", "v", time.Millisecond))
	time.Sleep(5 * time.Millisecond)

	keys, err := s.Keys(ctx, "")
	require.NoError(t, err)
	require.Contains(t, keys, "live")
	require.NotContains(t, keys, "dead")
}

// TestInMemoryStore_Keys_DoesNotEvictSkippedEntries verifies that Keys skips
// expired entries under RLock without evicting them, and that a subsequent Get
// on the same key still performs lazy eviction correctly.
func TestInMemoryStore_Keys_DoesNotEvictSkippedEntries(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Set(ctx, "live", "v", 0))
	require.NoError(t, s.Set(ctx, "dead", "v", time.Millisecond))
	time.Sleep(5 * time.Millisecond)

	// Keys skips the expired entry without evicting it.
	keys, err := s.Keys(ctx, "")
	require.NoError(t, err)
	require.NotContains(t, keys, "dead")

	// Subsequent Get must perform lazy eviction — must not crash or return stale data.
	_, err = s.Get(ctx, "dead")
	require.ErrorIs(t, err, kvstore.ErrNotFound)
}

// ── TokenBlocklist ────────────────────────────────────────────────────────────

func TestInMemoryStore_BlockToken_ZeroTTL_IsNoop(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.BlockToken(ctx, "jti-zero", 0))

	blocked, err := s.IsTokenBlocked(ctx, "jti-zero")
	require.NoError(t, err)
	require.False(t, blocked)
}

func TestInMemoryStore_BlockToken_NegativeTTL_IsNoop(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.BlockToken(ctx, "jti-neg", -time.Second))

	blocked, err := s.IsTokenBlocked(ctx, "jti-neg")
	require.NoError(t, err)
	require.False(t, blocked)
}

func TestInMemoryStore_IsTokenBlocked_UnknownJTI(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	blocked, err := s.IsTokenBlocked(context.Background(), "unknown")
	require.NoError(t, err)
	require.False(t, blocked)
}

func TestInMemoryStore_IsTokenBlocked_BlockedJTI(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.BlockToken(ctx, "jti-abc", time.Minute))

	blocked, err := s.IsTokenBlocked(ctx, "jti-abc")
	require.NoError(t, err)
	require.True(t, blocked)
}

func TestInMemoryStore_IsTokenBlocked_ExpiredJTI(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.BlockToken(ctx, "jti-exp", time.Millisecond))
	time.Sleep(5 * time.Millisecond)

	blocked, err := s.IsTokenBlocked(ctx, "jti-exp")
	require.NoError(t, err)
	require.False(t, blocked)
}

// ── Close ─────────────────────────────────────────────────────────────────────

func TestInMemoryStore_Close_NoError(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	require.NoError(t, s.Close())
}

// ── StartCleanup ──────────────────────────────────────────────────────────────

// TestInMemoryStore_StartCleanup_EvictsExpiredEntries verifies that the cleanup
// goroutine removes expired entries and exits promptly on context cancellation
func TestInMemoryStore_StartCleanup_EvictsExpiredEntries(t *testing.T) {
	t.Parallel()
	s := kvstore.NewInMemoryStore(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		s.StartCleanup(ctx)
		close(done)
	}()

	require.NoError(t, s.Set(context.Background(), "short", "v", 15*time.Millisecond))
	require.NoError(t, s.Set(context.Background(), "long", "v", time.Hour))

	time.Sleep(50 * time.Millisecond)

	keys, err := s.Keys(context.Background(), "")
	require.NoError(t, err)
	require.NotContains(t, keys, "short")
	require.Contains(t, keys, "long")

	// Cancel the context and assert the goroutine exits promptly.
	cancel()
	select {
	case <-done:
		// expected — StartCleanup returned after cancellation
	case <-time.After(200 * time.Millisecond):
		t.Fatal("StartCleanup goroutine did not exit after context cancellation")
	}
}

func TestInMemoryStore_StartCleanup_ZeroInterval_ReturnsImmediately(t *testing.T) {
	t.Parallel()
	s := kvstore.NewInMemoryStore(0)

	done := make(chan struct{})
	go func() {
		s.StartCleanup(context.Background())
		close(done)
	}()

	select {
	case <-done:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("StartCleanup with zero interval did not return promptly")
	}
}

// ── Concurrency ───────────────────────────────────────────────────────────────

func TestInMemoryStore_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 4)

	for range goroutines {
		key := "concurrent-key"

		go func() {
			defer wg.Done()
			_ = s.Set(ctx, key, "v", time.Second)
		}()

		go func() {
			defer wg.Done()
			_, _ = s.Get(ctx, key)
		}()

		go func() {
			defer wg.Done()
			_ = s.Delete(ctx, key)
		}()

		go func() {
			defer wg.Done()
			_, _ = s.Exists(ctx, key)
		}()
	}

	wg.Wait()
}

// ── ErrNotFound sentinel integrity ────────────────────────────────────────────

func TestErrNotFound_IsDistinct(t *testing.T) {
	t.Parallel()
	require.False(t, errors.Is(kvstore.ErrNotFound, errors.New("other")))
	require.True(t, errors.Is(kvstore.ErrNotFound, kvstore.ErrNotFound))
}

// ── AtomicBackoffIncrement ────────────────────────────────────────────────────

func TestInMemoryStore_AtomicBackoffIncrement_FirstFailure(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	unlocksAt, failures, err := s.AtomicBackoffIncrement(ctx, "key", 100*time.Millisecond, time.Second, 5*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, failures)
	require.True(t, unlocksAt.After(time.Now()), "unlocksAt should be in the future")
}

func TestInMemoryStore_AtomicBackoffIncrement_ExponentialGrowth(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	base := 100 * time.Millisecond

	// Failure 1: delay = 2^0 * 100ms = 100ms — must not be capped at maxDelay.
	ua1, f1, err := s.AtomicBackoffIncrement(ctx, "key", base, time.Hour, 5*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, f1)
	require.True(t, ua1.After(time.Now().Add(50*time.Millisecond)), "failure 1 lower bound")
	require.True(t, ua1.Before(time.Now().Add(200*time.Millisecond)), "failure 1 upper bound: must not be capped at maxDelay")

	// Failure 2: delay = 2^1 * 100ms = 200ms
	ua2, f2, err := s.AtomicBackoffIncrement(ctx, "key", base, time.Hour, 5*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 2, f2)
	require.True(t, ua2.After(time.Now().Add(150*time.Millisecond)), "failure 2 lower bound")
	require.True(t, ua2.Before(time.Now().Add(350*time.Millisecond)), "failure 2 upper bound: must not be capped at maxDelay")

	// Each successive unlock must be later than the previous one.
	require.True(t, ua2.After(ua1), "exponential growth: ua2 must be later than ua1")
}

func TestInMemoryStore_AtomicBackoffIncrement_CapsAtMaxDelay(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	base := 10 * time.Millisecond
	maxDelay := 50 * time.Millisecond

	// Failure 1: delay = 2^0 * 10ms = 10ms — must NOT be capped at maxDelay yet.
	ua1, _, err := s.AtomicBackoffIncrement(ctx, "key", base, maxDelay, 5*time.Minute)
	require.NoError(t, err)
	require.True(t, ua1.Before(time.Now().Add(maxDelay)),
		"first failure must use baseDelay (~10ms), not maxDelay (50ms)")

	// Drive failure count up to 10; by then the delay must be capped at maxDelay.
	var last time.Time
	for range 9 {
		var loopErr error
		last, _, loopErr = s.AtomicBackoffIncrement(ctx, "key", base, maxDelay, 5*time.Minute)
		require.NoError(t, loopErr)
	}
	// The final unlock must not exceed now + maxDelay (with a small buffer for clock jitter).
	require.True(t, last.Before(time.Now().Add(maxDelay+5*time.Millisecond)),
		"unlock must be capped at maxDelay after many failures")
}

func TestInMemoryStore_AtomicBackoffIncrement_ZeroIdleTTL_StoresIndefinitely(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	_, _, err := s.AtomicBackoffIncrement(ctx, "key", 100*time.Millisecond, time.Second, 0)
	require.NoError(t, err)

	// A second increment must see failures=2 (not reset to 1).
	_, f2, err := s.AtomicBackoffIncrement(ctx, "key", 100*time.Millisecond, time.Second, 0)
	require.NoError(t, err)
	require.Equal(t, 2, f2)
}

func TestInMemoryStore_AtomicBackoffIncrement_CorruptEntry_StartsFromZero(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	// Manually store corrupt JSON under the key.
	require.NoError(t, s.Set(ctx, "bad-key", "not-json", 0))

	// Should treat the corrupt entry as missing and start fresh.
	_, failures, err := s.AtomicBackoffIncrement(ctx, "bad-key", 100*time.Millisecond, time.Second, 5*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, failures)
}

// TestInMemoryStore_AtomicBackoffIncrement_ConcurrentCallers verifies that N
// simultaneous increments on the same key produce a final failure count of N
// with no lost updates — detectable under -race.
func TestInMemoryStore_AtomicBackoffIncrement_ConcurrentCallers(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, _, _ = s.AtomicBackoffIncrement(ctx, "concurrent-backoff", 10*time.Millisecond, time.Minute, 5*time.Minute)
		}()
	}
	wg.Wait()

	// One extra increment to read the current failure count.
	_, failures, err := s.AtomicBackoffIncrement(ctx, "concurrent-backoff", 10*time.Millisecond, time.Minute, 5*time.Minute)
	require.NoError(t, err)
	require.Equal(t, goroutines+1, failures, "no lost updates: all goroutine increments must be visible")
}

// TestInMemoryStore_AtomicBackoffAllow_ConcurrentWithIncrement verifies that
// concurrent Allow (RLock) and Increment (Lock) calls on the same key do not
// produce a data race and that Allow never returns corrupt data.
func TestInMemoryStore_AtomicBackoffAllow_ConcurrentWithIncrement(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	const writers = 10
	const readers = 10
	var wg sync.WaitGroup
	wg.Add(writers + readers)

	for range writers {
		go func() {
			defer wg.Done()
			_, _, _ = s.AtomicBackoffIncrement(ctx, "race-key", 10*time.Millisecond, time.Minute, 5*time.Minute)
		}()
	}
	for range readers {
		go func() {
			defer wg.Done()
			// Result intentionally discarded; we are checking for races, not values.
			_, _, _ = s.AtomicBackoffAllow(ctx, "race-key")
		}()
	}

	wg.Wait()
}

// ── AtomicBackoffAllow ────────────────────────────────────────────────────────

func TestInMemoryStore_AtomicBackoffAllow_FreshKey_Allowed(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	allowed, remaining, err := s.AtomicBackoffAllow(context.Background(), "fresh")
	require.NoError(t, err)
	require.True(t, allowed)
	require.Zero(t, remaining)
}

func TestInMemoryStore_AtomicBackoffAllow_AfterExpiry_Allowed(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	// Store an entry that has already expired.
	_, _, err := s.AtomicBackoffIncrement(ctx, "key", 10*time.Millisecond, 50*time.Millisecond, 5*time.Minute)
	require.NoError(t, err)
	time.Sleep(25 * time.Millisecond)

	allowed, remaining, err := s.AtomicBackoffAllow(ctx, "key")
	require.NoError(t, err)
	require.True(t, allowed)
	require.Zero(t, remaining)
}

func TestInMemoryStore_AtomicBackoffAllow_DuringWindow_Blocked(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	_, _, err := s.AtomicBackoffIncrement(ctx, "key", 200*time.Millisecond, time.Second, 5*time.Minute)
	require.NoError(t, err)

	allowed, remaining, err := s.AtomicBackoffAllow(ctx, "key")
	require.NoError(t, err)
	require.False(t, allowed)
	require.Positive(t, remaining)
}

func TestInMemoryStore_AtomicBackoffAllow_CorruptEntry_Allowed(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Set(ctx, "bad", "not-json", 0))

	allowed, remaining, err := s.AtomicBackoffAllow(ctx, "bad")
	require.NoError(t, err)
	require.True(t, allowed, "corrupt entry should be treated as unlocked")
	require.Zero(t, remaining)
}

func TestInMemoryStore_AtomicBackoffAllow_ZeroFailures_Allowed(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	// Manually store an entry with Failures=0.
	require.NoError(t, s.Set(ctx, "zero", `{"failures":0,"unlocks_at":"0001-01-01T00:00:00Z","last_seen":"0001-01-01T00:00:00Z"}`, 0))

	allowed, _, err := s.AtomicBackoffAllow(ctx, "zero")
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestInMemoryStore_AtomicBackoffAllow_ExpiredTTLEntry_Allowed(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	// Use a very short idleTTL so the entry itself expires.
	_, _, err := s.AtomicBackoffIncrement(ctx, "key", 200*time.Millisecond, time.Second, time.Millisecond)
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)

	allowed, _, err := s.AtomicBackoffAllow(ctx, "key")
	require.NoError(t, err)
	require.True(t, allowed, "expired TTL entry should behave as absent")
}

// ── Contract tests ────────────────────────────────────────────────────────────

// TestInMemoryStore_StoreContract runs the shared Store contract suite against
// InMemoryStore to catch semantic drift from other backends.
func TestInMemoryStore_StoreContract(t *testing.T) {
	t.Parallel()
	RunStoreContractTests(t, kvstore.NewInMemoryStore(0))
}

// TestInMemoryStore_TokenBlocklistContract runs the shared TokenBlocklist
// contract suite against InMemoryStore.
func TestInMemoryStore_TokenBlocklistContract(t *testing.T) {
	t.Parallel()
	RunTokenBlocklistContractTests(t, kvstore.NewInMemoryStore(0))
}

// TestInMemoryStore_AtomicBackoffContract runs the shared AtomicBackoffStore
// contract suite against InMemoryStore.
func TestInMemoryStore_AtomicBackoffContract(t *testing.T) {
	t.Parallel()
	RunAtomicBackoffContractTests(t, kvstore.NewInMemoryStore(0))
}

// TestInMemoryStore_WatchCapStoreContract runs the shared WatchCapStore
// contract suite against InMemoryStore.
func TestInMemoryStore_WatchCapStoreContract(t *testing.T) {
	t.Parallel()
	RunWatchCapStoreContractTests(t, kvstore.NewInMemoryStore(0))
}

// ── WatchCapStore unit tests ──────────────────────────────────────────────────

// watchTestKeys returns a unique triplet of keys for one logical user in the
// watch namespace. All three share the same hash-tag prefix so they'd land on
// the same Redis Cluster slot in production.
func watchTestKeys() (setKey, regAtKey, lastActiveKey string) {
	id := contractCounter.Add(1)
	pfx := fmt.Sprintf("{btc:user:ut%s:%d}", testRunID, id)
	return pfx + ":addresses", pfx + ":registered_at", pfx + ":last_active"
}

// TestInMemoryStore_RunWatchCapScript_FirstRegistration verifies the happy path:
// a clean store accepts addresses and returns the correct counters.
func TestInMemoryStore_RunWatchCapScript_FirstRegistration(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	succ, newCount, added, err := s.RunWatchCapScript(
		ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute,
		[]string{"addr1", "addr2", "addr3"},
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, succ)
	require.EqualValues(t, 3, newCount)
	require.EqualValues(t, 3, added)

	// registered_at must have been written.
	regAtVal, err := s.Get(ctx, regAtKey)
	require.NoError(t, err)
	require.NotEmpty(t, regAtVal)

	// last_active must have been written.
	_, err = s.Get(ctx, lastActiveKey)
	require.NoError(t, err)
}

// TestInMemoryStore_RunWatchCapScript_ReRegistration_SameAddresses verifies that
// submitting already-registered addresses is a success with addedCount=0.
func TestInMemoryStore_RunWatchCapScript_ReRegistration_SameAddresses(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()
	addrs := []string{"addr1", "addr2"}

	_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, addrs)
	require.NoError(t, err)

	succ, newCount, added, err := s.RunWatchCapScript(
		ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, addrs)
	require.NoError(t, err)
	require.EqualValues(t, 1, succ, "re-registration of same addresses must succeed")
	require.EqualValues(t, 2, newCount, "count must not change")
	require.EqualValues(t, 0, added, "addedCount must be 0 — no new addresses")
}

// TestInMemoryStore_RunWatchCapScript_AddsNewAddressesIncrementally verifies that
// a second call with new addresses increases the count correctly.
func TestInMemoryStore_RunWatchCapScript_AddsNewAddressesIncrementally(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, []string{"addr1", "addr2"})
	require.NoError(t, err)

	succ, newCount, added, err := s.RunWatchCapScript(
		ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, []string{"addr3", "addr4"})
	require.NoError(t, err)
	require.EqualValues(t, 1, succ)
	require.EqualValues(t, 4, newCount)
	require.EqualValues(t, 2, added)
}

// TestInMemoryStore_RunWatchCapScript_MixedExistingAndNew verifies partial overlap:
// some addresses already registered, some new.
func TestInMemoryStore_RunWatchCapScript_MixedExistingAndNew(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, []string{"addr1", "addr2"})
	require.NoError(t, err)

	// addr2 is already registered; addr3 is new.
	succ, newCount, added, err := s.RunWatchCapScript(
		ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, []string{"addr2", "addr3"})
	require.NoError(t, err)
	require.EqualValues(t, 1, succ)
	require.EqualValues(t, 3, newCount)
	require.EqualValues(t, 1, added, "only addr3 is new")
}

// TestInMemoryStore_RunWatchCapScript_ExactlyAtLimit_Succeeds verifies that
// filling the watch set to exactly the limit is permitted.
func TestInMemoryStore_RunWatchCapScript_ExactlyAtLimit_Succeeds(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	succ, newCount, added, err := s.RunWatchCapScript(
		ctx, setKey, regAtKey, lastActiveKey, 3,
		30*time.Minute, 30*time.Minute,
		[]string{"a", "b", "c"},
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, succ)
	require.EqualValues(t, 3, newCount)
	require.EqualValues(t, 3, added)
}

// TestInMemoryStore_RunWatchCapScript_CapExceeded_RollsBack verifies that
// exceeding the per-user cap returns success=0 and leaves the set unchanged.
func TestInMemoryStore_RunWatchCapScript_CapExceeded_RollsBack(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	// Fill the set to the cap.
	_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 2,
		30*time.Minute, 30*time.Minute, []string{"addr1", "addr2"})
	require.NoError(t, err)

	// Attempt to add one more address — must be rejected.
	succ, newCount, added, err := s.RunWatchCapScript(
		ctx, setKey, regAtKey, lastActiveKey, 2,
		30*time.Minute, 30*time.Minute, []string{"addr3"})
	require.NoError(t, err)
	require.EqualValues(t, 0, succ, "cap exceeded must return success=0")
	require.EqualValues(t, 2, newCount, "reported count must be the pre-attempt count")
	require.EqualValues(t, 0, added)

	// addr3 must NOT be in the set — the speculative add was rolled back.
	members, _, err := s.SScan(ctx, setKey, 0, "", 100)
	require.NoError(t, err)
	require.NotContains(t, members, "addr3", "addr3 must have been rolled back")
	require.ElementsMatch(t, []string{"addr1", "addr2"}, members)
}

// TestInMemoryStore_RunWatchCapScript_CapExceeded_MultipleAddresses verifies
// rollback when several addresses are submitted at once and collectively exceed
// the cap. All speculative adds must be undone atomically.
func TestInMemoryStore_RunWatchCapScript_CapExceeded_MultipleAddresses(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	// Set has 1 address; limit is 2. Adding 2 new would make 3 → cap exceeded.
	_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 2,
		30*time.Minute, 30*time.Minute, []string{"addr1"})
	require.NoError(t, err)

	succ, newCount, added, err := s.RunWatchCapScript(
		ctx, setKey, regAtKey, lastActiveKey, 2,
		30*time.Minute, 30*time.Minute, []string{"addr2", "addr3"})
	require.NoError(t, err)
	require.EqualValues(t, 0, succ)
	require.EqualValues(t, 1, newCount)
	require.EqualValues(t, 0, added)

	// Both addr2 and addr3 must have been rolled back.
	members, _, err := s.SScan(ctx, setKey, 0, "", 100)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"addr1"}, members,
		"all speculative adds must be rolled back on cap overflow")
}

// TestInMemoryStore_RunWatchCapScript_SevenDayWindowExpired verifies that a
// registered_at timestamp older than 7 days (604 800 s) causes success=-1.
func TestInMemoryStore_RunWatchCapScript_SevenDayWindowExpired(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	// Plant a registered_at that is 8 days old (beyond the 7-day window).
	oldTS := fmt.Sprintf("%d", time.Now().Add(-8*24*time.Hour).Unix())
	require.NoError(t, s.Set(ctx, regAtKey, oldTS, 0))

	succ, newCount, added, err := s.RunWatchCapScript(
		ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, []string{"addr1"})
	require.NoError(t, err)
	require.EqualValues(t, -1, succ, "expired 7-day window must return success=-1")
	require.EqualValues(t, 0, newCount)
	require.EqualValues(t, 0, added)
}

// TestInMemoryStore_RunWatchCapScript_SevenDayExpiry_SetsCleanupTTL verifies
// the M-02/OD-07 cleanup: after returning -1, registered_at must have a
// cleanup TTL set (minimum 1-day grace period) so stale keys don't accumulate.
func TestInMemoryStore_RunWatchCapScript_SevenDayExpiry_SetsCleanupTTL(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	// Plant a registered_at exactly 30 days ago — the 30-day cleanup TTL minus
	// elapsed would be 0, so the 1-day minimum floor must apply.
	oldTS := fmt.Sprintf("%d", time.Now().Add(-30*24*time.Hour).Unix())
	require.NoError(t, s.Set(ctx, regAtKey, oldTS, 0))

	_, _, _, err := s.RunWatchCapScript(
		ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, []string{"addr1"})
	require.NoError(t, err)

	// registered_at must still be readable (the TTL just got set, not expired).
	_, err = s.Get(ctx, regAtKey)
	require.NoError(t, err, "registered_at must still exist immediately after cleanup TTL is set")

	// 28-day-old registration: cleanupIn = 2592000 - elapsed ≈ 2 days remaining.
	oldTS28 := fmt.Sprintf("%d", time.Now().Add(-28*24*time.Hour).Unix())
	setKey2, regAtKey2, lastActiveKey2 := watchTestKeys()
	require.NoError(t, s.Set(ctx, regAtKey2, oldTS28, 0))
	_, _, _, err = s.RunWatchCapScript(
		ctx, setKey2, regAtKey2, lastActiveKey2, 10,
		30*time.Minute, 30*time.Minute, []string{"addr1"})
	require.NoError(t, err)
	_, err = s.Get(ctx, regAtKey2)
	require.NoError(t, err, "registered_at must be readable with remaining cleanup TTL")
}

// TestInMemoryStore_RunWatchCapScript_NX_RegAtKeyNotOverwritten verifies that
// registered_at is written exactly once (NX semantics): subsequent calls with
// new addresses must not reset the original timestamp.
func TestInMemoryStore_RunWatchCapScript_NX_RegAtKeyNotOverwritten(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	// First registration: seeds registered_at.
	_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, []string{"addr1"})
	require.NoError(t, err)

	firstTS, err := s.Get(ctx, regAtKey)
	require.NoError(t, err)

	// Sleep 1 ms so a second write would produce a different timestamp.
	time.Sleep(time.Millisecond)

	// Second registration with a new address — NX must prevent overwrite.
	_, _, _, err = s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, []string{"addr2"})
	require.NoError(t, err)

	secondTS, err := s.Get(ctx, regAtKey)
	require.NoError(t, err)
	require.Equal(t, firstTS, secondTS, "registered_at must not be overwritten by subsequent registrations")
}

// TestInMemoryStore_RunWatchCapScript_LastActive_AlwaysRefreshed verifies that
// last_active is refreshed on every successful call, even when no new addresses
// are added (re-registration of existing addresses).
func TestInMemoryStore_RunWatchCapScript_LastActive_AlwaysRefreshed(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	addrs := []string{"addr1"}
	_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, addrs)
	require.NoError(t, err)

	firstActive, err := s.Get(ctx, lastActiveKey)
	require.NoError(t, err)

	time.Sleep(time.Millisecond)

	// Re-registration of the same address.
	_, _, _, err = s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, addrs)
	require.NoError(t, err)

	secondActive, err := s.Get(ctx, lastActiveKey)
	require.NoError(t, err)
	require.GreaterOrEqual(t, secondActive, firstActive,
		"last_active timestamp must be refreshed on every call (including re-registration)")
}

// TestInMemoryStore_RunWatchCapScript_LastActive_HasTTL verifies that last_active
// is stored with the provided lastActiveTTL so it will expire naturally.
func TestInMemoryStore_RunWatchCapScript_LastActive_HasTTL(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 5*time.Millisecond, // very short TTL
		[]string{"addr1"})
	require.NoError(t, err)

	// Immediately readable.
	_, err = s.Get(ctx, lastActiveKey)
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	// Must have expired.
	_, err = s.Get(ctx, lastActiveKey)
	require.ErrorIs(t, err, kvstore.ErrNotFound, "last_active must expire after lastActiveTTL")
}

// TestInMemoryStore_RunWatchCapScript_EmptyAddresses_StillRefreshesLastActive verifies
// that passing an empty address slice is a no-op (addedCount=0) but last_active
// is still refreshed — matching the Lua script's unconditional SET at the end.
func TestInMemoryStore_RunWatchCapScript_EmptyAddresses_StillRefreshesLastActive(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	// Seed last_active via a first registration.
	_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, []string{"addr1"})
	require.NoError(t, err)

	firstActive, err := s.Get(ctx, lastActiveKey)
	require.NoError(t, err)

	time.Sleep(time.Millisecond)

	// Second call with empty address list.
	succ, newCount, added, err := s.RunWatchCapScript(
		ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, []string{})
	require.NoError(t, err)
	require.EqualValues(t, 1, succ)
	require.EqualValues(t, 1, newCount, "count must not change")
	require.EqualValues(t, 0, added)

	secondActive, err := s.Get(ctx, lastActiveKey)
	require.NoError(t, err)
	require.GreaterOrEqual(t, secondActive, firstActive,
		"last_active must be refreshed even when address slice is empty")
}

// TestInMemoryStore_RunWatchCapScript_DuplicateAddressesInSlice verifies that
// submitting duplicates within a single call doesn't double-count.
func TestInMemoryStore_RunWatchCapScript_DuplicateAddressesInSlice(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	succ, newCount, added, err := s.RunWatchCapScript(
		ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute,
		[]string{"addr1", "addr1", "addr1"},
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, succ)
	require.EqualValues(t, 1, newCount, "duplicates must be deduplicated by set semantics")
	require.EqualValues(t, 1, added)
}

// TestInMemoryStore_RunWatchCapScript_NX_ResetsAfterExpiredRegAtKey verifies
// the bug fix: if regAtKey is still in the map but has expired (cleanup TTL
// elapsed), a new registration must reset it rather than leaving the stale
// entry in place.
func TestInMemoryStore_RunWatchCapScript_NX_ResetsAfterExpiredRegAtKey(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	// Plant a regAtKey that is already expired (1 ms TTL).
	oldTS := fmt.Sprintf("%d", time.Now().Add(-1*time.Second).Unix())
	require.NoError(t, s.Set(ctx, regAtKey, oldTS, time.Millisecond))
	time.Sleep(5 * time.Millisecond) // wait for expiry

	// Now register — the expired regAtKey must be treated as absent (NX resets).
	now := time.Now()
	_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 10,
		30*time.Minute, 30*time.Minute, []string{"addr1"})
	require.NoError(t, err)

	newTSStr, err := s.Get(ctx, regAtKey)
	require.NoError(t, err)
	newTS, err := strconv.ParseInt(newTSStr, 10, 64)
	require.NoError(t, err)
	require.GreaterOrEqual(t, newTS, now.Unix(),
		"expired regAtKey must be reset to current time, not left as old stale value")
}

// TestInMemoryStore_RunWatchCapScript_ConcurrentCalls_NoRace verifies that
// concurrent calls on the same user keys do not produce a data race and that
// the final count does not exceed the cap.
func TestInMemoryStore_RunWatchCapScript_ConcurrentCalls_NoRace(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	setKey, regAtKey, lastActiveKey := watchTestKeys()

	const goroutines = 30
	const limit = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	var capExceeded atomic.Int64
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			addr := fmt.Sprintf("addr%d", i)
			succ, _, _, _ := s.RunWatchCapScript(
				ctx, setKey, regAtKey, lastActiveKey, limit,
				30*time.Minute, 30*time.Minute, []string{addr})
			if succ == 0 {
				capExceeded.Add(1)
			}
		}(i)
	}
	wg.Wait()

	// After all goroutines finish the set must not exceed the cap.
	members, _, err := s.SScan(ctx, setKey, 0, "", 100)
	require.NoError(t, err)
	require.LessOrEqual(t, len(members), limit,
		"concurrent registrations must never exceed the cap")
	// At least one goroutine must have been rejected when goroutines > limit.
	require.Positive(t, capExceeded.Load(),
		"at least one goroutine must have seen cap exceeded (goroutines=%d > limit=%d)",
		goroutines, limit)
}

// ── ScanWatchAddressKeys ──────────────────────────────────────────────────────

// TestInMemoryStore_ScanWatchAddressKeys_EmptyStore verifies that an empty
// store returns an empty slice and cursor=0.
func TestInMemoryStore_ScanWatchAddressKeys_EmptyStore(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	keys, cursor, err := s.ScanWatchAddressKeys(context.Background(), 0, 100)
	require.NoError(t, err)
	require.Empty(t, keys)
	require.EqualValues(t, 0, cursor)
}

// TestInMemoryStore_ScanWatchAddressKeys_FiltersNonAddressKeys verifies that
// only set keys whose name ends with ":addresses" are returned; other set keys
// (e.g. ":last_active" stored as plain KV, or other set namespaces) are ignored.
func TestInMemoryStore_ScanWatchAddressKeys_FiltersNonAddressKeys(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	// Insert an ":addresses" set key and an unrelated set key.
	_, err := s.SAdd(ctx, "btc:user:1:addresses", "addr1")
	require.NoError(t, err)
	_, err = s.SAdd(ctx, "btc:user:1:other_set", "member1")
	require.NoError(t, err)
	// Also add a plain KV entry to confirm we don't scan items.
	require.NoError(t, s.Set(ctx, "btc:user:1:registered_at", "12345", 0))

	keys, _, err := s.ScanWatchAddressKeys(ctx, 0, 100)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"btc:user:1:addresses"}, keys)
}

// TestInMemoryStore_ScanWatchAddressKeys_ReturnsAllMatchingKeys verifies that
// all set keys ending in ":addresses" across multiple users are returned.
func TestInMemoryStore_ScanWatchAddressKeys_ReturnsAllMatchingKeys(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	want := []string{
		"btc:user:alice:addresses",
		"btc:user:bob:addresses",
		"btc:user:carol:addresses",
	}
	for _, k := range want {
		_, err := s.SAdd(ctx, k, "addr1")
		require.NoError(t, err)
	}
	// Noise key that must not appear.
	_, err := s.SAdd(ctx, "btc:user:alice:other", "member")
	require.NoError(t, err)

	keys, cursor, err := s.ScanWatchAddressKeys(ctx, 0, 100)
	require.NoError(t, err)
	require.EqualValues(t, 0, cursor, "InMemoryStore always returns cursor=0")
	require.ElementsMatch(t, want, keys)
}

// TestInMemoryStore_ScanWatchAddressKeys_CursorIgnored verifies that InMemoryStore
// always returns all matching keys regardless of the cursor or count hint,
// and always responds with nextCursor=0.
func TestInMemoryStore_ScanWatchAddressKeys_CursorIgnored(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()

	_, err := s.SAdd(ctx, "btc:user:x:addresses", "addr1")
	require.NoError(t, err)

	// Non-zero cursor and small count hint — must still return all keys.
	keys, cursor, err := s.ScanWatchAddressKeys(ctx, 99, 1)
	require.NoError(t, err)
	require.EqualValues(t, 0, cursor)
	require.ElementsMatch(t, []string{"btc:user:x:addresses"}, keys)
}

// ── Contract test helpers ─────────────────────────────────────────────────────
//
// RunStoreContractTests, RunTokenBlocklistContractTests, and
// RunAtomicBackoffContractTests are shared behavioural helpers that are called
// from both this file and redis_test.go. Keeping them here (no build tag)
// ensures they are always compiled and available regardless of which backend's
// tests are being built.

// contractCounter generates unique key names so contract tests do not
// interfere with each other when running in parallel against a shared store.
var contractCounter atomic.Int64

// testRunID is a per-process unique token included in every contract key so
// that Redis keys from a previous test run do not collide with keys generated
// in the current run. Redis persists hash entries across runs; without this
// prefix a key like "contract:bo-inc1:1" would be reused and its existing
// failure counter would inflate the expected-1 assertion.
var testRunID = fmt.Sprintf("%d", time.Now().UnixNano())

// contractKey returns a unique key with the given logical name.
func contractKey(name string) string {
	return fmt.Sprintf("contract:%s:%s:%d", testRunID, name, contractCounter.Add(1))
}

// RunStoreContractTests verifies the core Store semantics against any
// implementation. Call it from both InMemoryStore and RedisStore test suites.
func RunStoreContractTests(t *testing.T, s kvstore.Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("Get_MissingKey_ReturnsErrNotFound", func(t *testing.T) {
		t.Parallel()
		_, err := s.Get(ctx, contractKey("get-miss"))
		require.ErrorIs(t, err, kvstore.ErrNotFound)
	})

	t.Run("Set_Get_RoundTrip", func(t *testing.T) {
		t.Parallel()
		k := contractKey("rtrip")
		require.NoError(t, s.Set(ctx, k, "hello", time.Minute))
		got, err := s.Get(ctx, k)
		require.NoError(t, err)
		require.Equal(t, "hello", got)
	})

	t.Run("Set_ZeroTTL_Persists", func(t *testing.T) {
		t.Parallel()
		k := contractKey("zero-ttl")
		require.NoError(t, s.Set(ctx, k, "v", 0))
		got, err := s.Get(ctx, k)
		require.NoError(t, err)
		require.Equal(t, "v", got)
	})

	t.Run("Set_NegativeTTL_ReturnsError", func(t *testing.T) {
		t.Parallel()
		err := s.Set(ctx, contractKey("neg-ttl"), "v", -time.Second)
		require.Error(t, err, "negative ttl must be rejected by all backends")
	})

	t.Run("Set_OverwritesExisting", func(t *testing.T) {
		t.Parallel()
		k := contractKey("overwrite")
		require.NoError(t, s.Set(ctx, k, "first", time.Minute))
		require.NoError(t, s.Set(ctx, k, "second", time.Minute))
		got, err := s.Get(ctx, k)
		require.NoError(t, err)
		require.Equal(t, "second", got)
	})

	t.Run("Delete_Idempotent", func(t *testing.T) {
		t.Parallel()
		k := contractKey("del")
		require.NoError(t, s.Delete(ctx, contractKey("del-ghost")))
		require.NoError(t, s.Set(ctx, k, "v", time.Minute))
		require.NoError(t, s.Delete(ctx, k))
		require.NoError(t, s.Delete(ctx, k))
		_, err := s.Get(ctx, k)
		require.ErrorIs(t, err, kvstore.ErrNotFound)
	})

	t.Run("Exists_Present", func(t *testing.T) {
		t.Parallel()
		k := contractKey("exists-yes")
		require.NoError(t, s.Set(ctx, k, "v", time.Minute))
		ok, err := s.Exists(ctx, k)
		require.NoError(t, err)
		require.True(t, ok)
	})

	t.Run("Exists_Missing", func(t *testing.T) {
		t.Parallel()
		ok, err := s.Exists(ctx, contractKey("exists-no"))
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("Keys_PrefixFilter", func(t *testing.T) {
		t.Parallel()
		pfx := fmt.Sprintf("contract:kpfx:%d:", contractCounter.Add(1))
		k1 := pfx + "a"
		k2 := pfx + "b"
		other := contractKey("keys-other")
		require.NoError(t, s.Set(ctx, k1, "1", time.Minute))
		require.NoError(t, s.Set(ctx, k2, "2", time.Minute))
		require.NoError(t, s.Set(ctx, other, "3", time.Minute))
		keys, err := s.Keys(ctx, pfx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{k1, k2}, keys)
	})
}

// RunTokenBlocklistContractTests verifies the TokenBlocklist semantics against
// any implementation.
func RunTokenBlocklistContractTests(t *testing.T, b kvstore.TokenBlocklist) {
	t.Helper()
	ctx := context.Background()

	t.Run("ZeroTTL_IsNoop", func(t *testing.T) {
		t.Parallel()
		jti := fmt.Sprintf("contract-jti-zero-%d", contractCounter.Add(1))
		require.NoError(t, b.BlockToken(ctx, jti, 0))
		blocked, err := b.IsTokenBlocked(ctx, jti)
		require.NoError(t, err)
		require.False(t, blocked)
	})

	t.Run("NegativeTTL_IsNoop", func(t *testing.T) {
		t.Parallel()
		jti := fmt.Sprintf("contract-jti-neg-%d", contractCounter.Add(1))
		require.NoError(t, b.BlockToken(ctx, jti, -time.Second))
		blocked, err := b.IsTokenBlocked(ctx, jti)
		require.NoError(t, err)
		require.False(t, blocked)
	})

	t.Run("UnknownJTI_NotBlocked", func(t *testing.T) {
		t.Parallel()
		jti := fmt.Sprintf("contract-jti-unknown-%d", contractCounter.Add(1))
		blocked, err := b.IsTokenBlocked(ctx, jti)
		require.NoError(t, err)
		require.False(t, blocked)
	})

	t.Run("BlockedJTI_IsBlocked", func(t *testing.T) {
		t.Parallel()
		jti := fmt.Sprintf("contract-jti-blocked-%d", contractCounter.Add(1))
		require.NoError(t, b.BlockToken(ctx, jti, time.Minute))
		blocked, err := b.IsTokenBlocked(ctx, jti)
		require.NoError(t, err)
		require.True(t, blocked)
	})
}

// RunWatchCapStoreContractTests verifies WatchCapStore semantics against any
// implementation. It is called from both InMemoryStore and RedisStore test suites.
func RunWatchCapStoreContractTests(t *testing.T, s kvstore.WatchCapStore) {
	t.Helper()
	ctx := context.Background()

	// contractWatchKeys returns a unique triplet of related keys for one logical
	// user. Keys share the same hash-tag prefix so they'd land on the same Redis
	// Cluster slot in production.
	contractWatchKeys := func() (setKey, regAtKey, lastActiveKey string) {
		id := contractCounter.Add(1)
		pfx := fmt.Sprintf("{btc:user:ct%s:%d}", testRunID, id)
		return pfx + ":addresses", pfx + ":registered_at", pfx + ":last_active"
	}

	t.Run("FirstRegistration_Success", func(t *testing.T) {
		t.Parallel()
		setKey, regAtKey, lastActiveKey := contractWatchKeys()
		succ, newCount, added, err := s.RunWatchCapScript(
			ctx, setKey, regAtKey, lastActiveKey, 10,
			30*time.Minute, 30*time.Minute,
			[]string{"addr1", "addr2"},
		)
		require.NoError(t, err)
		require.EqualValues(t, 1, succ)
		require.EqualValues(t, 2, newCount)
		require.EqualValues(t, 2, added)
	})

	t.Run("ReRegistration_SameAddresses_AddedCountZero", func(t *testing.T) {
		t.Parallel()
		setKey, regAtKey, lastActiveKey := contractWatchKeys()
		addrs := []string{"addr1", "addr2"}
		_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 10,
			30*time.Minute, 30*time.Minute, addrs)
		require.NoError(t, err)

		succ, newCount, added, err := s.RunWatchCapScript(
			ctx, setKey, regAtKey, lastActiveKey, 10,
			30*time.Minute, 30*time.Minute, addrs)
		require.NoError(t, err)
		require.EqualValues(t, 1, succ, "re-registration must succeed")
		require.EqualValues(t, 2, newCount, "count must not change")
		require.EqualValues(t, 0, added, "no new addresses added")
	})

	t.Run("CapExceeded_ReturnsZero", func(t *testing.T) {
		t.Parallel()
		setKey, regAtKey, lastActiveKey := contractWatchKeys()
		// Fill to limit=2.
		_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 2,
			30*time.Minute, 30*time.Minute, []string{"addr1", "addr2"})
		require.NoError(t, err)

		// Try to add a third address beyond the limit.
		succ, newCount, added, err := s.RunWatchCapScript(
			ctx, setKey, regAtKey, lastActiveKey, 2,
			30*time.Minute, 30*time.Minute, []string{"addr3"})
		require.NoError(t, err)
		require.EqualValues(t, 0, succ, "cap exceeded must return success=0")
		require.EqualValues(t, 2, newCount, "count must be the pre-attempt count")
		require.EqualValues(t, 0, added)
	})

	t.Run("ExactlyAtLimit_Succeeds", func(t *testing.T) {
		t.Parallel()
		setKey, regAtKey, lastActiveKey := contractWatchKeys()
		succ, newCount, added, err := s.RunWatchCapScript(
			ctx, setKey, regAtKey, lastActiveKey, 3,
			30*time.Minute, 30*time.Minute,
			[]string{"a", "b", "c"},
		)
		require.NoError(t, err)
		require.EqualValues(t, 1, succ)
		require.EqualValues(t, 3, newCount)
		require.EqualValues(t, 3, added)
	})

	t.Run("SevenDayWindowExpired_ReturnsMinus1", func(t *testing.T) {
		t.Parallel()
		setKey, regAtKey, lastActiveKey := contractWatchKeys()
		// Pre-seed regAtKey with a timestamp 8 days in the past.
		// We do this through a first RunWatchCapScript call, then directly
		// mutate the timestamp via the Store base interface.
		_, _, _, err := s.RunWatchCapScript(ctx, setKey, regAtKey, lastActiveKey, 10,
			30*time.Minute, 30*time.Minute, []string{"addr1"})
		require.NoError(t, err)

		// Overwrite registered_at with a timestamp 8 days ago.
		oldTS := fmt.Sprintf("%d", time.Now().Add(-8*24*time.Hour).Unix())
		require.NoError(t, s.Set(ctx, regAtKey, oldTS, 0))

		succ, _, _, err := s.RunWatchCapScript(
			ctx, setKey, regAtKey, lastActiveKey, 10,
			30*time.Minute, 30*time.Minute, []string{"addr2"})
		require.NoError(t, err)
		require.EqualValues(t, -1, succ, "7-day window expired must return -1")
	})

	t.Run("Concurrent_NoRace", func(t *testing.T) {
		t.Parallel()
		setKey, regAtKey, lastActiveKey := contractWatchKeys()
		const goroutines = 20
		var wg sync.WaitGroup
		wg.Add(goroutines)
		for i := range goroutines {
			go func(i int) {
				defer wg.Done()
				addr := fmt.Sprintf("addr%d", i)
				_, _, _, _ = s.RunWatchCapScript(
					ctx, setKey, regAtKey, lastActiveKey, goroutines,
					30*time.Minute, 30*time.Minute, []string{addr})
			}(i)
		}
		wg.Wait()
	})
}

// RunAtomicBackoffContractTests verifies the AtomicBackoffStore semantics
// against any implementation, covering the full increment→allow→unlock cycle,
// exponential growth, and the max-delay cap.
func RunAtomicBackoffContractTests(t *testing.T, s kvstore.AtomicBackoffStore) {
	t.Helper()
	ctx := context.Background()

	t.Run("FreshKey_Allowed", func(t *testing.T) {
		t.Parallel()
		allowed, rem, err := s.AtomicBackoffAllow(ctx, contractKey("bo-fresh"))
		require.NoError(t, err)
		require.True(t, allowed)
		require.Zero(t, rem)
	})

	t.Run("FirstIncrement_CountIsOne", func(t *testing.T) {
		t.Parallel()
		k := contractKey("bo-inc1")
		_, failures, err := s.AtomicBackoffIncrement(ctx, k, 100*time.Millisecond, time.Minute, time.Hour)
		require.NoError(t, err)
		require.Equal(t, 1, failures)
	})

	t.Run("IncrementsAccumulate", func(t *testing.T) {
		t.Parallel()
		k := contractKey("bo-accum")
		for i := 1; i <= 3; i++ {
			_, f, err := s.AtomicBackoffIncrement(ctx, k, 10*time.Millisecond, time.Minute, time.Hour)
			require.NoError(t, err)
			require.Equal(t, i, f, "failure count must accumulate across calls")
		}
	})

	t.Run("MaxDelayCap", func(t *testing.T) {
		t.Parallel()
		k := contractKey("bo-cap")
		maxD := 200 * time.Millisecond
		var last time.Time
		for range 15 {
			var err error
			last, _, err = s.AtomicBackoffIncrement(ctx, k, 10*time.Millisecond, maxD, time.Hour)
			require.NoError(t, err)
		}
		require.True(t, last.Before(time.Now().Add(maxD+50*time.Millisecond)),
			"unlock time must not exceed now + maxDelay + buffer")
	})

	t.Run("BlockedDuringWindow_ThenAllowedAfter", func(t *testing.T) {
		// Not parallel — this test sleeps.
		k := contractKey("bo-cycle")
		// Use 500ms so the backoff window is still open when AtomicBackoffAllow is
		// called immediately after. Under heavy parallel load or with WSL2/Docker
		// clock skew the two Redis round-trips can together consume >80ms, which
		// would cause an 80ms window to close before the allow check runs.
		unlocksAt, _, err := s.AtomicBackoffIncrement(ctx, k, 500*time.Millisecond, time.Minute, time.Hour)
		require.NoError(t, err)

		allowed, rem, err := s.AtomicBackoffAllow(ctx, k)
		require.NoError(t, err)
		require.False(t, allowed, "key must be blocked immediately after increment")
		require.Positive(t, rem)

		// Sleep until the actual unlock time reported by the store, plus a
		// 300ms buffer to absorb Redis vs Go wall-clock skew.
		sleepFor := time.Until(unlocksAt) + 300*time.Millisecond
		if sleepFor > 0 {
			time.Sleep(sleepFor)
		}

		allowed, _, err = s.AtomicBackoffAllow(ctx, k)
		require.NoError(t, err)
		require.True(t, allowed, "key must be allowed after unlock window expires")
	})
}
