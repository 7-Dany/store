package ratelimit_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────
// nonAtomicStore — wraps InMemoryStore but deliberately does NOT expose the
// AtomicBackoffStore or AtomicBucketStore interfaces so the non-atomic local
// paths inside BackoffLimiter and rateLimiter are exercised.
// ─────────────────────────────────────────────────────────────

type nonAtomicStore struct {
	inner *kvstore.InMemoryStore
}

func newNonAtomicStore() *nonAtomicStore {
	return &nonAtomicStore{inner: kvstore.NewInMemoryStore(0)}
}

func (s *nonAtomicStore) Get(ctx context.Context, key string) (string, error) {
	return s.inner.Get(ctx, key)
}
func (s *nonAtomicStore) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return s.inner.Set(ctx, key, value, ttl)
}
func (s *nonAtomicStore) Delete(ctx context.Context, key string) error {
	return s.inner.Delete(ctx, key)
}
func (s *nonAtomicStore) Exists(ctx context.Context, key string) (bool, error) {
	return s.inner.Exists(ctx, key)
}
func (s *nonAtomicStore) Keys(ctx context.Context, prefix string) ([]string, error) {
	return s.inner.Keys(ctx, prefix)
}
func (s *nonAtomicStore) StartCleanup(ctx context.Context) { s.inner.StartCleanup(ctx) }
func (s *nonAtomicStore) Close() error                     { return s.inner.Close() }
func (s *nonAtomicStore) RefreshTTL(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return s.inner.RefreshTTL(ctx, key, ttl)
}

// corruptJSONStore behaves like nonAtomicStore but seeds a corrupt JSON value
// for a specific key, exercising the json.Unmarshal error branch in loadEntry.
type corruptJSONStore struct {
	*nonAtomicStore
	corruptKey string
}

func (s *corruptJSONStore) Get(ctx context.Context, key string) (string, error) {
	if key == s.corruptKey {
		return "not-valid-json", nil
	}
	return s.nonAtomicStore.Get(ctx, key)
}

// ─────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────

const (
	testBaseDelay = 20 * time.Millisecond
	testMaxDelay  = 40 * time.Millisecond
	testIdleTTL   = 5 * time.Minute
)

func newBackoffLimiter() *ratelimit.BackoffLimiter {
	s := kvstore.NewInMemoryStore(0)
	return ratelimit.NewBackoffLimiterWithStore(s, "backoff:", testBaseDelay, testMaxDelay, testIdleTTL)
}

// ipKeyFn is a BackoffLimiter keyFn that extracts the host from RemoteAddr.
func ipKeyFn(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ─────────────────────────────────────────────────────────────
// Allow — happy path
// ─────────────────────────────────────────────────────────────

func TestBackoffLimiter_Allow_PermitsOnFreshKey(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	ok, remaining := l.Allow(context.Background(), "1.2.3.4")
	require.True(t, ok)
	require.Zero(t, remaining)
}

// ─────────────────────────────────────────────────────────────
// RecordFailure
// ─────────────────────────────────────────────────────────────

func TestBackoffLimiter_RecordFailure_ReturnsPositiveDelay(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	delay := l.RecordFailure(context.Background(), "1.2.3.4")
	require.Positive(t, delay)
}

func TestBackoffLimiter_RecordFailure_IncreasesDelayExponentially(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	d1 := l.RecordFailure(context.Background(), "key")
	d2 := l.RecordFailure(context.Background(), "key")
	require.Greater(t, d2, d1)
}

func TestBackoffLimiter_RecordFailure_CapsAtMaxDelay(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	var last time.Duration
	for range 10 {
		last = l.RecordFailure(context.Background(), "key")
	}
	require.LessOrEqual(t, last, testMaxDelay)
}

// ─────────────────────────────────────────────────────────────
// Allow — after failure
// ─────────────────────────────────────────────────────────────

func TestBackoffLimiter_Allow_BlocksDuringBackoffWindow(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	l.RecordFailure(context.Background(), "1.2.3.4")

	ok, remaining := l.Allow(context.Background(), "1.2.3.4")
	require.False(t, ok)
	require.Positive(t, remaining)
}

func TestBackoffLimiter_Allow_PermitsAfterWindowExpires(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	l.RecordFailure(context.Background(), "1.2.3.4")

	time.Sleep(testBaseDelay + 5*time.Millisecond)

	ok, remaining := l.Allow(context.Background(), "1.2.3.4")
	require.True(t, ok)
	require.Zero(t, remaining)
}

func TestBackoffLimiter_Allow_DifferentKeysAreIndependent(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	l.RecordFailure(context.Background(), "1.2.3.4")

	ok, _ := l.Allow(context.Background(), "5.6.7.8")
	require.True(t, ok)
}

// ─────────────────────────────────────────────────────────────
// Reset
// ─────────────────────────────────────────────────────────────

func TestBackoffLimiter_Reset_ClearsBackoffWindow(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	l.RecordFailure(context.Background(), "1.2.3.4")

	ok, _ := l.Allow(context.Background(), "1.2.3.4")
	require.False(t, ok, "expected block before reset")

	l.Reset(context.Background(), "1.2.3.4")

	ok, remaining := l.Allow(context.Background(), "1.2.3.4")
	require.True(t, ok)
	require.Zero(t, remaining)
}

func TestBackoffLimiter_Reset_IsNoOpForUnknownKey(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	l.Reset(context.Background(), "unknown")
	ok, _ := l.Allow(context.Background(), "unknown")
	require.True(t, ok)
}

// ─────────────────────────────────────────────────────────────
// Key prefix isolation
// ─────────────────────────────────────────────────────────────

func TestBackoffLimiter_KeyPrefixIsolatesNamespaces(t *testing.T) {
	t.Parallel()
	s := kvstore.NewInMemoryStore(0)
	l1 := ratelimit.NewBackoffLimiterWithStore(s, "ns1:", testBaseDelay, testMaxDelay, testIdleTTL)
	l2 := ratelimit.NewBackoffLimiterWithStore(s, "ns2:", testBaseDelay, testMaxDelay, testIdleTTL)

	l1.RecordFailure(context.Background(), "key")

	ok, _ := l1.Allow(context.Background(), "key")
	require.False(t, ok)

	ok2, _ := l2.Allow(context.Background(), "key")
	require.True(t, ok2)
}

// ─────────────────────────────────────────────────────────────
// Middleware
// ─────────────────────────────────────────────────────────────

func TestBackoffLimiter_Middleware_AllowsWhenNoFailures(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	mw := l.Middleware(ipKeyFn)

	w := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(w, requestWithRemoteAddr("10.0.0.1:1234"))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestBackoffLimiter_Middleware_BlocksDuringWindow(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	l.RecordFailure(context.Background(), "10.0.0.1")

	mw := l.Middleware(ipKeyFn)
	w := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(w, requestWithRemoteAddr("10.0.0.1:1234"))
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.NotEmpty(t, w.Header().Get("Retry-After"))
}

func TestBackoffLimiter_Middleware_SetsRetryAfterHeader(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	l.RecordFailure(context.Background(), "10.0.0.1")

	mw := l.Middleware(ipKeyFn)
	w := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(w, requestWithRemoteAddr("10.0.0.1:1234"))

	require.NotEmpty(t, w.Header().Get("Retry-After"))
}

// ─────────────────────────────────────────────────────────────
// StartCleanup respects ctx.Done()
// ─────────────────────────────────────────────────────────────

func TestBackoffLimiter_StartCleanup_StopsOnContextCancel(t *testing.T) {
	t.Parallel()
	l := ratelimit.NewBackoffLimiter("backoff:", testBaseDelay, testMaxDelay, testIdleTTL, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.StartCleanup(ctx)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("StartCleanup did not stop after context was cancelled")
	}
}

// ─────────────────────────────────────────────────────────────
// Concurrent access
// ─────────────────────────────────────────────────────────────

func TestBackoffLimiter_RecordFailure_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()

	// Verify no panic, deadlock, or data race under concurrent RecordFailure
	// calls. Run with -race to catch data races. State-correctness after a
	// single failure is already covered by TestBackoffLimiter_Allow_BlocksDuringBackoffWindow.
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			l.RecordFailure(context.Background(), "shared-key")
		}()
	}
	wg.Wait() // reaching here without panic/deadlock is the assertion
}

func TestBackoffLimiter_AllowAndReset_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	l := newBackoffLimiter()
	l.RecordFailure(context.Background(), "key")

	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for range goroutines {
		go func() {
			defer wg.Done()
			l.Allow(context.Background(), "key")
		}()
		go func() {
			defer wg.Done()
			l.Reset(context.Background(), "key")
		}()
	}
	wg.Wait()
}

// ─────────────────────────────────────────────────────────────
// Non-atomic local paths
// These tests force BackoffLimiter onto its local mutex path by backing it
// with a nonAtomicStore that does NOT implement AtomicBackoffStore.
// ─────────────────────────────────────────────────────────────

func newNonAtomicBackoffLimiter() *ratelimit.BackoffLimiter {
	s := newNonAtomicStore()
	return ratelimit.NewBackoffLimiterWithStore(s, "backoff:", testBaseDelay, testMaxDelay, testIdleTTL)
}

// recordFailureLocal: first call on fresh key — loadEntry returns ErrNotFound,
// starts at failures=0, increments to 1.
func TestBackoffLimiter_NonAtomic_RecordFailure_FirstFailure(t *testing.T) {
	t.Parallel()
	l := newNonAtomicBackoffLimiter()
	delay := l.RecordFailure(context.Background(), "1.2.3.4")
	require.Positive(t, delay)
}

func TestBackoffLimiter_NonAtomic_RecordFailure_IncreasesExponentially(t *testing.T) {
	t.Parallel()
	l := newNonAtomicBackoffLimiter()
	d1 := l.RecordFailure(context.Background(), "key")
	d2 := l.RecordFailure(context.Background(), "key")
	require.Greater(t, d2, d1)
}

func TestBackoffLimiter_NonAtomic_RecordFailure_CapsAtMaxDelay(t *testing.T) {
	t.Parallel()
	l := newNonAtomicBackoffLimiter()
	var last time.Duration
	for range 10 {
		last = l.RecordFailure(context.Background(), "key")
	}
	require.LessOrEqual(t, last, testMaxDelay)
}

// allowLocal: failures == 0 (fresh key) → allowed.
func TestBackoffLimiter_NonAtomic_Allow_FreshKey_Allowed(t *testing.T) {
	t.Parallel()
	l := newNonAtomicBackoffLimiter()
	ok, rem := l.Allow(context.Background(), "1.2.3.4")
	require.True(t, ok)
	require.Zero(t, rem)
}

// allowLocal: within backoff window → blocked.
func TestBackoffLimiter_NonAtomic_Allow_BlocksDuringWindow(t *testing.T) {
	t.Parallel()
	l := newNonAtomicBackoffLimiter()
	l.RecordFailure(context.Background(), "key")

	ok, rem := l.Allow(context.Background(), "key")
	require.False(t, ok)
	require.Positive(t, rem)
}

// allowLocal: after window expires → allowed.
func TestBackoffLimiter_NonAtomic_Allow_PermitsAfterWindowExpires(t *testing.T) {
	t.Parallel()
	l := newNonAtomicBackoffLimiter()
	l.RecordFailure(context.Background(), "key")

	time.Sleep(testBaseDelay + 5*time.Millisecond)

	ok, rem := l.Allow(context.Background(), "key")
	require.True(t, ok)
	require.Zero(t, rem)
}

// Reset non-atomic path: hold mu.Lock before deleting.
func TestBackoffLimiter_NonAtomic_Reset_ClearsWindow(t *testing.T) {
	t.Parallel()
	l := newNonAtomicBackoffLimiter()
	l.RecordFailure(context.Background(), "key")

	ok, _ := l.Allow(context.Background(), "key")
	require.False(t, ok, "expected block before reset")

	l.Reset(context.Background(), "key")

	ok, rem := l.Allow(context.Background(), "key")
	require.True(t, ok)
	require.Zero(t, rem)
}

// loadEntry: corrupt JSON stored in the backing store → unmarshal error branch.
func TestBackoffLimiter_NonAtomic_LoadEntry_CorruptJSON_StartsFromZero(t *testing.T) {
	t.Parallel()
	corruptKey := "backoff:corrupt-ip"
	cs := &corruptJSONStore{
		nonAtomicStore: newNonAtomicStore(),
		corruptKey:     corruptKey,
	}
	l := ratelimit.NewBackoffLimiterWithStore(cs, "backoff:", testBaseDelay, testMaxDelay, testIdleTTL)

	// RecordFailure calls loadEntry which will see corrupt JSON.
	// It must treat the entry as fresh (failures=0) and return a positive delay.
	delay := l.RecordFailure(context.Background(), "corrupt-ip")
	require.Positive(t, delay)
}

// loadEntry: key doesn't exist (ErrNotFound) → error branch returns zero entry.
func TestBackoffLimiter_NonAtomic_LoadEntry_MissingKey_ReturnsZeroEntry(t *testing.T) {
	t.Parallel()
	l := newNonAtomicBackoffLimiter()
	// First Allow on a non-existent key should always pass.
	ok, _ := l.Allow(context.Background(), "brand-new")
	require.True(t, ok)
}

// ─────────────────────────────────────────────────────────────
// Atomic-store error fallback paths
// These tests back the limiter with a store that implements AtomicBackoffStore
// but always returns an error, forcing both RecordFailure and Allow to fall
// through to their local-mutex path (the slog.Warn + fallback branches).
// ─────────────────────────────────────────────────────────────

// errorAtomicBackoffStore implements AtomicBackoffStore but always errors,
// delegating the underlying storage to a nonAtomicStore so the fallback local
// path can still read/write entries correctly.
type errorAtomicBackoffStore struct {
	*nonAtomicStore
}

func (s *errorAtomicBackoffStore) AtomicBackoffIncrement(_ context.Context, _ string, _, _, _ time.Duration) (time.Time, int, error) {
	return time.Time{}, 0, errors.New("simulated atomic backoff increment error")
}

func (s *errorAtomicBackoffStore) AtomicBackoffAllow(_ context.Context, _ string) (bool, time.Duration, error) {
	return false, 0, errors.New("simulated atomic backoff allow error")
}

func newErrorAtomicBackoffLimiter() *ratelimit.BackoffLimiter {
	s := &errorAtomicBackoffStore{nonAtomicStore: newNonAtomicStore()}
	return ratelimit.NewBackoffLimiterWithStore(s, "backoff:", testBaseDelay, testMaxDelay, testIdleTTL)
}

// backoff.go:90 — slog.WarnContext + local fallback inside RecordFailure when
// the AtomicBackoffStore returns an error.
func TestBackoffLimiter_AtomicError_RecordFailure_FallsBackToLocal(t *testing.T) {
	t.Parallel()
	l := newErrorAtomicBackoffLimiter()
	// AtomicBackoffIncrement will error; the limiter must fall back to the
	// local path and still return a positive delay.
	delay := l.RecordFailure(context.Background(), "1.2.3.4")
	require.Positive(t, delay)
}

// backoff.go:152 — slog.WarnContext + local fallback inside Allow when the
// AtomicBackoffStore returns an error.
func TestBackoffLimiter_AtomicError_Allow_FallsBackToLocal(t *testing.T) {
	t.Parallel()
	l := newErrorAtomicBackoffLimiter()
	// Record a failure through the local path (atomic increment will error and
	// fall back), then check Allow also uses the local path.
	l.RecordFailure(context.Background(), "1.2.3.4")

	// AtomicBackoffAllow will error; the limiter must fall back to the local
	// path. The key is now in a backoff window, so Allow should return false.
	ok, remaining := l.Allow(context.Background(), "1.2.3.4")
	require.False(t, ok)
	require.Positive(t, remaining)
}

// saveEntry: json.Marshal is called with _ to discard the error because
// backoffEntry contains only int and time.Time fields, both always
// JSON-serialisable. The unreachable error branch was removed for coverage clarity.

// Middleware uses non-atomic limiter.
func TestBackoffLimiter_NonAtomic_Middleware_BlocksDuringWindow(t *testing.T) {
	t.Parallel()
	l := newNonAtomicBackoffLimiter()
	l.RecordFailure(context.Background(), "10.0.0.1")

	mw := l.Middleware(ipKeyFn)
	w := httptest.NewRecorder()
	mw(okHandler()).ServeHTTP(w, requestWithRemoteAddr("10.0.0.1:1234"))
	require.Equal(t, http.StatusTooManyRequests, w.Code)
}
