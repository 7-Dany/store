package kvstore_test

import (
	"context"
	"errors"
	"fmt"
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
