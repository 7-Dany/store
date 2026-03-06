package ratelimit_test

import (
	"context"
	"errors"
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
// helpers
// ─────────────────────────────────────────────────────────────

func newIPLimiter(rate, burst float64) *ratelimit.IPRateLimiter {
	s := kvstore.NewInMemoryStore(0)
	return ratelimit.NewIPRateLimiter(s, "ip:", rate, burst, 10*time.Minute)
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func requestWithRemoteAddr(addr string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = addr
	return r
}

// ─────────────────────────────────────────────────────────────
// Allow
// ─────────────────────────────────────────────────────────────

func TestIPRateLimiter_Allow_PermitsWhenTokensAvailable(t *testing.T) {
	t.Parallel()
	l := newIPLimiter(10, 5)
	require.True(t, l.Allow(context.Background(), "1.2.3.4"))
}

func TestIPRateLimiter_Allow_BlocksWhenBucketEmpty(t *testing.T) {
	t.Parallel()
	// burst=1: only one request allowed before the bucket is empty.
	l := newIPLimiter(0, 1)
	require.True(t, l.Allow(context.Background(), "1.2.3.4"))
	require.False(t, l.Allow(context.Background(), "1.2.3.4"))
}

func TestIPRateLimiter_Allow_DifferentIPsHaveIndependentBuckets(t *testing.T) {
	t.Parallel()
	l := newIPLimiter(0, 1)
	require.True(t, l.Allow(context.Background(), "1.2.3.4"))
	require.False(t, l.Allow(context.Background(), "1.2.3.4"))
	// A different IP still has a full bucket.
	require.True(t, l.Allow(context.Background(), "5.6.7.8"))
}

func TestIPRateLimiter_Allow_KeyPrefixIsolatesNamespaces(t *testing.T) {
	t.Parallel()
	s := kvstore.NewInMemoryStore(0)
	// Two limiters sharing a store but with different prefixes must not
	// consume each other's tokens.
	l1 := ratelimit.NewIPRateLimiter(s, "ns1:", 0, 1, 10*time.Minute)
	l2 := ratelimit.NewIPRateLimiter(s, "ns2:", 0, 1, 10*time.Minute)
	require.True(t, l1.Allow(context.Background(), "1.2.3.4"))
	require.False(t, l1.Allow(context.Background(), "1.2.3.4"))
	// l2 has its own bucket and must still allow the IP.
	require.True(t, l2.Allow(context.Background(), "1.2.3.4"))
}

// ─────────────────────────────────────────────────────────────
// Limit middleware
// ─────────────────────────────────────────────────────────────

func TestIPRateLimiter_Limit_Allows200WhenTokenAvailable(t *testing.T) {
	t.Parallel()
	l := newIPLimiter(10, 5)
	mw := l.Limit(okHandler())
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, requestWithRemoteAddr("10.0.0.1:1234"))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestIPRateLimiter_Limit_Returns429WhenExhausted(t *testing.T) {
	t.Parallel()
	l := newIPLimiter(0, 1)
	mw := l.Limit(okHandler())

	// First request consumes the single token.
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, requestWithRemoteAddr("10.0.0.1:1234"))
	require.Equal(t, http.StatusOK, w1.Code)

	// Second request should be rejected.
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, requestWithRemoteAddr("10.0.0.1:1234"))
	require.Equal(t, http.StatusTooManyRequests, w2.Code)
	require.Equal(t, "1", w2.Header().Get("Retry-After"))
}

func TestIPRateLimiter_Limit_ExtractsIPFromRemoteAddr(t *testing.T) {
	t.Parallel()
	// burst=1: first request from each distinct IP is allowed.
	l := newIPLimiter(0, 1)
	mw := l.Limit(okHandler())

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, requestWithRemoteAddr("192.168.1.1:9999"))
	require.Equal(t, http.StatusOK, w.Code)

	// Same IP: second request must be blocked.
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, requestWithRemoteAddr("192.168.1.1:9999"))
	require.Equal(t, http.StatusTooManyRequests, w2.Code)
}

// ─────────────────────────────────────────────────────────────
// StartCleanup respects ctx.Done()
// ─────────────────────────────────────────────────────────────

func TestIPRateLimiter_StartCleanup_StopsOnContextCancel(t *testing.T) {
	t.Parallel()
	s := kvstore.NewInMemoryStore(10 * time.Millisecond)
	l := ratelimit.NewIPRateLimiter(s, "ip:", 10, 10, 1*time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.StartCleanup(ctx)
	}()

	cancel()
	select {
	case <-done:
		// Goroutine exited after cancel.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("StartCleanup did not stop after context was cancelled")
	}
}

// ─────────────────────────────────────────────────────────────
// Concurrent access
// ─────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────
// remoteIP bare-IP branch (RemoteAddr with no port)
// ─────────────────────────────────────────────────────────────

// TestIPRateLimiter_Limit_BareIPRemoteAddr verifies that the Limit middleware
// correctly handles a RemoteAddr that contains no port (e.g. a bare IP string),
// which triggers the net.SplitHostPort error branch inside remoteIP. The full
// RemoteAddr is used as the bucket key in that case, so rate-limiting still
// works correctly.
func TestIPRateLimiter_Limit_BareIPRemoteAddr(t *testing.T) {
	t.Parallel()
	l := newIPLimiter(10, 5)
	mw := l.Limit(okHandler())

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1" // no port — SplitHostPort returns an error
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

// ─────────────────────────────────────────────────────────────
// Atomic-bucket-store error fallback path
// ip_limiter.go:70-78 — slog.WarnContext + local-mutex fallback in allow()
// when the AtomicBucketStore returns an error.
// ─────────────────────────────────────────────────────────────

// errorAtomicBucketStore implements AtomicBucketStore but always returns an
// error from AtomicBucketAllow, forcing rateLimiter.allow to fall back to the
// local-mutex path.
type errorAtomicBucketStore struct {
	*nonAtomicStore
}

func (s *errorAtomicBucketStore) AtomicBucketAllow(_ context.Context, _ string, _, _ float64, _ time.Duration) (bool, error) {
	return false, errors.New("simulated atomic bucket error")
}

// TestIPRateLimiter_AtomicBucketError_FallsBackToLocal verifies that when the
// AtomicBucketStore.AtomicBucketAllow returns an error the limiter logs a
// warning (slog.WarnContext, line 70-72) and continues with the local-mutex
// token-bucket path (lines 72-78), correctly allowing the first request and
// blocking the second.
func TestIPRateLimiter_AtomicBucketError_FallsBackToLocal(t *testing.T) {
	t.Parallel()
	s := &errorAtomicBucketStore{nonAtomicStore: newNonAtomicStore()}
	l := ratelimit.NewIPRateLimiter(s, "ip:", 0, 1, 10*time.Minute)

	// First request: bucket is full (burst=1), local path should allow it.
	require.True(t, l.Allow(context.Background(), "1.2.3.4"))
	// Second request: bucket empty after first consume, local path should block.
	require.False(t, l.Allow(context.Background(), "1.2.3.4"))
}

// TestIPRateLimiter_NonAtomic_Allow exercises the local mutex path inside the
// shared rateLimiter.allow when backed by a nonAtomicStore (no AtomicBucketStore).
func TestIPRateLimiter_NonAtomic_Allow_PermitsAndBlocks(t *testing.T) {
	t.Parallel()
	s := newNonAtomicStore()
	l := ratelimit.NewIPRateLimiter(s, "ip:", 0, 1, 10*time.Minute)

	require.True(t, l.Allow(context.Background(), "1.2.3.4"))
	require.False(t, l.Allow(context.Background(), "1.2.3.4"))
}

func TestIPRateLimiter_Allow_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	// burst=100 so not every goroutine is blocked; we care that there is no
	// data race, not about the exact count.
	l := newIPLimiter(0, 100)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			l.Allow(context.Background(), "1.2.3.4")
		}(i)
	}
	wg.Wait()
}

// runTokenBucketContractTests exercises the fundamental allow → block → refill
// contract of the token-bucket engine against the provided store. Call this
// with every store backend to catch semantic drift between implementations.
func runTokenBucketContractTests(t *testing.T, s kvstore.Store) {
	t.Helper()

	t.Run("permits_when_tokens_available", func(t *testing.T) {
		t.Parallel()
		l := ratelimit.NewIPRateLimiter(s, "contract:permits:", 10, 5, 10*time.Minute)
		require.True(t, l.Allow(context.Background(), "1.2.3.4"))
	})

	t.Run("blocks_when_bucket_empty", func(t *testing.T) {
		t.Parallel()
		l := ratelimit.NewIPRateLimiter(s, "contract:blocks:", 0, 1, 10*time.Minute)
		require.True(t, l.Allow(context.Background(), "2.2.2.2"))
		require.False(t, l.Allow(context.Background(), "2.2.2.2"))
	})

	t.Run("independent_buckets_per_ip", func(t *testing.T) {
		t.Parallel()
		l := ratelimit.NewIPRateLimiter(s, "contract:independent:", 0, 1, 10*time.Minute)
		require.True(t, l.Allow(context.Background(), "3.3.3.3"))
		require.False(t, l.Allow(context.Background(), "3.3.3.3"))
		require.True(t, l.Allow(context.Background(), "4.4.4.4"))
	})

	t.Run("token_refill_after_delay", func(t *testing.T) {
		t.Parallel()
		// rate=200 tokens/s means one token every 5ms.
		l := ratelimit.NewIPRateLimiter(s, "contract:refill:", 200, 1, 10*time.Minute)
		require.True(t, l.Allow(context.Background(), "5.5.5.5"))
		require.False(t, l.Allow(context.Background(), "5.5.5.5"))
		time.Sleep(15 * time.Millisecond)
		require.True(t, l.Allow(context.Background(), "5.5.5.5"))
	})
}

func TestIPRateLimiter_Contract_InMemoryStore(t *testing.T) {
	t.Parallel()
	s := kvstore.NewInMemoryStore(0)
	runTokenBucketContractTests(t, s)
}

func TestIPRateLimiter_Contract_NonAtomicStore(t *testing.T) {
	t.Parallel()
	s := newNonAtomicStore()
	runTokenBucketContractTests(t, s)
}

func TestIPRateLimiter_Contract_AtomicErrorFallbackStore(t *testing.T) {
	t.Parallel()
	s := &errorAtomicBucketStore{nonAtomicStore: newNonAtomicStore()}
	runTokenBucketContractTests(t, s)
}

// ─────────────────────────────────────────────────────────────
// remoteIP — uncovered branches
// ─────────────────────────────────────────────────────────────

// ip_limiter.go:201.25,205.4 — slog.Warn + fallback when RemoteAddr is empty.
// Every request with an empty RemoteAddr shares the prefix-only bucket key.
func TestIPRateLimiter_Limit_EmptyRemoteAddr_SharesPrefixBucket(t *testing.T) {
	t.Parallel()
	l := newIPLimiter(10, 5)
	mw := l.Limit(okHandler())

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "" // triggers slog.Warn and prefix-only bucket
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

// ip_limiter.go:210.4,210.22 — bare IPv4 string (no port) normalised via
// net.ParseIP → To4 → v4.String(). SplitHostPort returns an error.
func TestIPRateLimiter_Limit_BareIPv4RemoteAddr_NormalisedCorrectly(t *testing.T) {
	t.Parallel()
	// burst=1 so the first request is allowed and the second from the same IP
	// is blocked — proving the key is extracted and used consistently.
	l := newIPLimiter(0, 1)
	mw := l.Limit(okHandler())

	r1 := httptest.NewRequest(http.MethodGet, "/", nil)
	r1.RemoteAddr = "10.0.0.1" // bare IPv4, no port
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code)

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "10.0.0.1"
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, r2)
	require.Equal(t, http.StatusTooManyRequests, w2.Code)
}

// ip_limiter.go:212.3,212.22 — bare IPv6 string (no port/brackets) normalised
// via net.ParseIP → To4 returns nil → ip.String().
func TestIPRateLimiter_Limit_BareIPv6RemoteAddr_NormalisedCorrectly(t *testing.T) {
	t.Parallel()
	l := newIPLimiter(0, 1)
	mw := l.Limit(okHandler())

	r1 := httptest.NewRequest(http.MethodGet, "/", nil)
	r1.RemoteAddr = "2001:db8::1" // bare IPv6, no brackets/port
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code)

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "2001:db8::1"
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, r2)
	require.Equal(t, http.StatusTooManyRequests, w2.Code)
}

// ip_limiter.go:220.3,220.21 — after successful SplitHostPort, host is an
// IPv4 address: net.ParseIP → To4 → v4.String().
func TestIPRateLimiter_Limit_IPv4WithPort_NormalisedCorrectly(t *testing.T) {
	t.Parallel()
	l := newIPLimiter(0, 1)
	mw := l.Limit(okHandler())

	r1 := httptest.NewRequest(http.MethodGet, "/", nil)
	r1.RemoteAddr = "192.168.0.1:54321" // host part is a parseable IPv4
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code)

	// Same normalised IP on a different source port must share the bucket.
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "192.168.0.1:11111"
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, r2)
	require.Equal(t, http.StatusTooManyRequests, w2.Code)
}

// ip_limiter.go:222.2,222.13 — after successful SplitHostPort, host is an
// IPv6 address: net.ParseIP → To4 returns nil → ip.String().
func TestIPRateLimiter_Limit_IPv6WithPort_NormalisedCorrectly(t *testing.T) {
	t.Parallel()
	l := newIPLimiter(0, 1)
	mw := l.Limit(okHandler())

	r1 := httptest.NewRequest(http.MethodGet, "/", nil)
	r1.RemoteAddr = "[2001:db8::1]:8080" // standard bracket-port IPv6
	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code)

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "[2001:db8::1]:9090"
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, r2)
	require.Equal(t, http.StatusTooManyRequests, w2.Code)
}

// ip_limiter.go:74.17,76.4 — slog.WarnContext inside allow() when the
// AtomicBucketStore returns an error. Verified by checking that the fallback
// local path still enforces the burst limit correctly.
func TestIPRateLimiter_AtomicBucketWarnPath_LocalFallbackEnforcesBurst(t *testing.T) {
	t.Parallel()
	s := &errorAtomicBucketStore{nonAtomicStore: newNonAtomicStore()}
	l := ratelimit.NewIPRateLimiter(s, "warn:", 0, 2, 10*time.Minute)

	// Two requests must be allowed (burst=2).
	require.True(t, l.Allow(context.Background(), "10.0.0.1"))
	require.True(t, l.Allow(context.Background(), "10.0.0.1"))
	// Third must be blocked.
	require.False(t, l.Allow(context.Background(), "10.0.0.1"))
}
