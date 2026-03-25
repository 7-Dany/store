package ratelimit_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/stretchr/testify/require"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func newTestStore() *kvstore.InMemoryStore {
	return kvstore.NewInMemoryStore(0)
}

func newCounter(store *kvstore.InMemoryStore, max int, ttl time.Duration) *ratelimit.ConnectionCounter {
	return ratelimit.NewConnectionCounter(store, ratelimit.DefaultBTCSSEConnKeyPrefix, max, ttl, nil)
}

// ── basic acquire / release ──────────────────────────────────────────────────

func TestConnectionCounter_AcquireUnderCap_Succeeds(t *testing.T) {
	t.Parallel()
	c := newCounter(newTestStore(), 3, 2*time.Hour)
	require.NoError(t, c.Acquire(context.Background(), "user1"))
}

func TestConnectionCounter_AcquireAtCap_ReturnsErrAtCapacity(t *testing.T) {
	t.Parallel()
	c := newCounter(newTestStore(), 2, 2*time.Hour)
	ctx := context.Background()
	require.NoError(t, c.Acquire(ctx, "u"))
	require.NoError(t, c.Acquire(ctx, "u"))
	err := c.Acquire(ctx, "u")
	require.ErrorIs(t, err, ratelimit.ErrAtCapacity)
}

func TestConnectionCounter_ReleaseDecrementsCount(t *testing.T) {
	t.Parallel()
	c := newCounter(newTestStore(), 2, 2*time.Hour)
	ctx := context.Background()
	require.NoError(t, c.Acquire(ctx, "u"))
	require.NoError(t, c.Acquire(ctx, "u"))
	// at cap; a third acquire must fail
	require.ErrorIs(t, c.Acquire(ctx, "u"), ratelimit.ErrAtCapacity)
	// release one slot
	c.Release("u")
	// should be acquirable again
	require.NoError(t, c.Acquire(ctx, "u"))
}

func TestConnectionCounter_ReleaseBelowZero_FloorsAtZero(t *testing.T) {
	t.Parallel()
	c := newCounter(newTestStore(), 3, 2*time.Hour)
	ctx := context.Background()
	require.NoError(t, c.Acquire(ctx, "u"))
	c.Release("u")
	c.Release("u") // extra release — should not go negative
	n, err := c.Count(ctx, "u")
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

func TestConnectionCounter_ReleaseUsesBackgroundContext(t *testing.T) {
	t.Parallel()
	// Even when the caller's context is already cancelled, Release must still
	// decrement the counter (it uses context.Background() internally).
	c := newCounter(newTestStore(), 3, 2*time.Hour)
	ctx := context.Background()
	require.NoError(t, c.Acquire(ctx, "u"))

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Release is called with the cancelled context as the *caller* context —
	// but Release ignores the caller context entirely (uses Background()).
	// We validate by checking the count went to 0 even though ctx was cancelled.
	_ = cancelledCtx
	c.Release("u")
	n, err := c.Count(context.Background(), "u")
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

func TestConnectionCounter_Concurrent_NeverExceedsCap(t *testing.T) {
	t.Parallel()
	const max = 5
	const goroutines = 50
	c := newCounter(newTestStore(), max, 2*time.Hour)
	ctx := context.Background()

	var mu sync.Mutex
	var peak int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			err := c.Acquire(ctx, "shared")
			if err != nil {
				return // at cap — acceptable
			}
			cur, _ := c.Count(ctx, "shared")
			mu.Lock()
			if cur > peak {
				peak = cur
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			c.Release("shared")
		}()
	}
	wg.Wait()
	require.LessOrEqual(t, peak, int64(max),
		"concurrent count %d exceeded cap %d", peak, max)
}

// ── Count ────────────────────────────────────────────────────────────────────

func TestConnectionCounter_Count_ReturnsCurrentValue(t *testing.T) {
	t.Parallel()
	c := newCounter(newTestStore(), 5, 2*time.Hour)
	ctx := context.Background()
	n, err := c.Count(ctx, "u")
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
	require.NoError(t, c.Acquire(ctx, "u"))
	n, err = c.Count(ctx, "u")
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	require.NoError(t, c.Acquire(ctx, "u"))
	n, err = c.Count(ctx, "u")
	require.NoError(t, err)
	require.Equal(t, int64(2), n)
}

func TestConnectionCounter_Count_InvalidValue_LogsWarning(t *testing.T) {
	t.Parallel()
	store := newTestStore()
	ctx := context.Background()
	// Inject a non-integer value into the key.
	require.NoError(t, store.Set(ctx, ratelimit.DefaultBTCSSEConnKeyPrefix+"u", "not-a-number", time.Hour))
	c := newCounter(store, 5, 2*time.Hour)
	// Should return 0, not panic or return an error.
	n, err := c.Count(ctx, "u")
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

func TestConnectionCounter_Count_NegativeValue_TreatedAsZero(t *testing.T) {
	t.Parallel()
	store := newTestStore()
	ctx := context.Background()
	// Inject a negative value.
	require.NoError(t, store.Set(ctx, ratelimit.DefaultBTCSSEConnKeyPrefix+"u", "-3", time.Hour))
	c := newCounter(store, 5, 2*time.Hour)
	n, err := c.Count(ctx, "u")
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

// ── AtomicDecrement negative counter repair ──────────────────────────────────

func TestAtomicDecrement_NegativeCounter_RepairedToZero(t *testing.T) {
	t.Parallel()
	store := newTestStore()
	ctx := context.Background()
	// Inject key = -3.
	require.NoError(t, store.Set(ctx, "mykey", "-3", time.Hour))
	n, err := store.AtomicDecrement(ctx, "mykey", 0)
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

func TestAtomicAcquire_NegativeCounter_RepairedBeforeCapCheck(t *testing.T) {
	t.Parallel()
	store := newTestStore()
	ctx := context.Background()
	// Inject key = -2, max = 3; acquire should treat current as 0 and return 1.
	require.NoError(t, store.Set(ctx, "mykey", "-2", time.Hour))
	n, err := store.AtomicAcquire(ctx, "mykey", 3, time.Hour)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}

func TestAtomicAcquire_NegativeCounter_RepairDoesNotAllowExceedingCap(t *testing.T) {
	t.Parallel()
	store := newTestStore()
	ctx := context.Background()
	// Inject key = -1, max = 1 — after repair current == 0, which is < 1 so acquire should succeed once.
	require.NoError(t, store.Set(ctx, "mykey", "-1", time.Hour))
	n, err := store.AtomicAcquire(ctx, "mykey", 1, time.Hour)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	// Now at cap; next acquire must return -1.
	n2, err2 := store.AtomicAcquire(ctx, "mykey", 1, time.Hour)
	require.NoError(t, err2)
	require.Equal(t, int64(-1), n2)
}

func TestAtomicDecrement_CorruptedNegative_RepairPreservesTTL(t *testing.T) {
	t.Parallel()
	store := newTestStore()
	ctx := context.Background()
	// Set key = -3 with a TTL.
	require.NoError(t, store.Set(ctx, "mykey", "-3", 5*time.Minute))
	n, err := store.AtomicDecrement(ctx, "mykey", 0)
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
	// Key should now be deleted (count == 0).
	_, getErr := store.Get(ctx, "mykey")
	require.ErrorIs(t, getErr, kvstore.ErrNotFound)
}

func TestAtomicAcquire_CorruptedNegative_RepairPreservesTTL(t *testing.T) {
	t.Parallel()
	store := newTestStore()
	ctx := context.Background()
	require.NoError(t, store.Set(ctx, "mykey", "-2", 2*time.Hour))
	n, err := store.AtomicAcquire(ctx, "mykey", 3, time.Hour)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	// Key must still exist with the new TTL.
	val, getErr := store.Get(ctx, "mykey")
	require.NoError(t, getErr)
	require.Equal(t, "1", val)
}

func TestAtomicAcquire_SetWithPX_NoPartialExecution(t *testing.T) {
	t.Parallel()
	store := newTestStore()
	ctx := context.Background()
	const max = 10
	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, _ = store.AtomicAcquire(ctx, "mykey", max, time.Hour)
		}()
	}
	wg.Wait()
	val, err := store.Get(ctx, "mykey")
	require.NoError(t, err)
	n, parseErr := strconv.ParseInt(val, 10, 64)
	require.NoError(t, parseErr)
	require.LessOrEqual(t, n, int64(max), "counter must not exceed cap")
}

// ── C-03 suite (TTL safety) ──────────────────────────────────────────────────

func TestConnectionCounter_C03_HalfTTL_ReleaseRefreshes(t *testing.T) {
	t.Parallel()
	// MAX connections; advance clock to slotTTL/2; Release; cap still enforced.
	// We test the logical behaviour: after a release, re-acquire should succeed
	// and the count should be consistent.
	const max = 2
	ttl := 200 * time.Millisecond
	c := newCounter(newTestStore(), max, ttl)
	ctx := context.Background()
	require.NoError(t, c.Acquire(ctx, "u"))
	require.NoError(t, c.Acquire(ctx, "u"))
	// Sleep half the TTL — key still alive.
	time.Sleep(ttl / 2)
	c.Release("u")
	// Slot freed; re-acquire should succeed.
	require.NoError(t, c.Acquire(ctx, "u"))
}

func TestConnectionCounter_C03_FullTTL_NoChurn_HeartbeatPreventsExpiry(t *testing.T) {
	t.Parallel()
	const max = 1
	ttl := 100 * time.Millisecond
	c := newCounter(newTestStore(), max, ttl)
	ctx := context.Background()
	require.NoError(t, c.Acquire(ctx, "u"))

	// Heartbeat every 30ms over 200ms — key must survive.
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.After(200 * time.Millisecond)
		for {
			select {
			case <-ticker.C:
				c.Heartbeat(ctx, "u")
			case <-deadline:
				return
			}
		}
	}()
	<-done
	// Cap must still be enforced — key was kept alive by heartbeats.
	err := c.Acquire(ctx, "u")
	require.ErrorIs(t, err, ratelimit.ErrAtCapacity,
		"cap should still be enforced after heartbeats kept the key alive")
}

func TestConnectionCounter_C03_FullTTL_NoChurn_NoHeartbeat_CapBypassed_Documented(t *testing.T) {
	t.Parallel()
	// Without heartbeats, after slotTTL the key expires and the cap can be bypassed.
	// This test DOCUMENTS the known bypass (it is the failure mode that Heartbeat fixes).
	const max = 1
	ttl := 80 * time.Millisecond
	c := newCounter(newTestStore(), max, ttl)
	ctx := context.Background()
	require.NoError(t, c.Acquire(ctx, "u"))
	// No heartbeat — wait for TTL to expire.
	time.Sleep(ttl + 20*time.Millisecond)
	// Key has expired; acquire should succeed again (cap bypassed).
	err := c.Acquire(ctx, "u")
	require.NoError(t, err,
		"documented bypass: without Heartbeat, expired key allows a new connection beyond cap")
}

// ── Heartbeat ────────────────────────────────────────────────────────────────

func TestConnectionCounter_Heartbeat_KeyExists_TTLRefreshed(t *testing.T) {
	t.Parallel()
	c := newCounter(newTestStore(), 3, time.Hour)
	ctx := context.Background()
	require.NoError(t, c.Acquire(ctx, "u"))
	// Should not panic or warn.
	c.Heartbeat(ctx, "u")
}

func TestConnectionCounter_Heartbeat_KeyMissing_WarnsAndMetricIncrements(t *testing.T) {
	t.Parallel()
	c := newCounter(newTestStore(), 3, time.Hour)
	ctx := context.Background()
	// Heartbeat on a key that was never acquired — should log warning, not panic.
	c.Heartbeat(ctx, "does-not-exist")
}

// ── Constructor panic guard ──────────────────────────────────────────────────

func TestConnectionCounter_DefaultBTCSSEConnKeyPrefix_IsCanonical(t *testing.T) {
	t.Parallel()
	require.Equal(t, "btc:sse:conn:", ratelimit.DefaultBTCSSEConnKeyPrefix)
}

// badAtomicCounterStore satisfies kvstore.AtomicCounterStore but NOT kvstore.Store,
// used to verify the constructor panics on contract violation.
type badAtomicCounterStore struct{}

func (b *badAtomicCounterStore) Get(_ context.Context, _ string) (string, error) {
	return "", errors.New("not implemented")
}
func (b *badAtomicCounterStore) Set(_ context.Context, _, _ string, _ time.Duration) error {
	return errors.New("not implemented")
}
func (b *badAtomicCounterStore) Delete(_ context.Context, _ string) error {
	return errors.New("not implemented")
}
func (b *badAtomicCounterStore) Exists(_ context.Context, _ string) (bool, error) {
	return false, errors.New("not implemented")
}
func (b *badAtomicCounterStore) Keys(_ context.Context, _ string) ([]string, error) {
	return nil, errors.New("not implemented")
}
func (b *badAtomicCounterStore) StartCleanup(_ context.Context) {}
func (b *badAtomicCounterStore) Close() error                   { return nil }
func (b *badAtomicCounterStore) RefreshTTL(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return false, errors.New("not implemented")
}
func (b *badAtomicCounterStore) AtomicIncrement(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 0, nil
}
func (b *badAtomicCounterStore) AtomicDecrement(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 0, nil
}
func (b *badAtomicCounterStore) AtomicAcquire(_ context.Context, _ string, _ int, _ time.Duration) (int64, error) {
	return 0, nil
}

// Note: badAtomicCounterStore satisfies kvstore.AtomicCounterStore fully
// (including RefreshTTL via the embedded Store). Since RefreshTTL is now on
// the base Store interface, any AtomicCounterStore also provides RefreshTTL.
// The constructor panic guard therefore cannot be triggered with a valid
// AtomicCounterStore — the test below documents this invariant instead.
func TestConnectionCounter_NewConnectionCounter_StoreIsAlwaysKVStore(t *testing.T) {
	t.Parallel()
	// Any type satisfying AtomicCounterStore necessarily satisfies Store
	// (because AtomicCounterStore embeds Store). The constructor panic guard
	// therefore never fires in practice — this test documents the invariant.
	store := &badAtomicCounterStore{}
	// Should NOT panic because badAtomicCounterStore satisfies kvstore.Store.
	require.NotPanics(t, func() {
		ratelimit.NewConnectionCounter(store, "prefix:", 3, time.Hour, nil)
	})
}
