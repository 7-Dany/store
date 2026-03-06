package ratelimitest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ratelimitest "github.com/7-Dany/store/backend/internal/platform/ratelimit/testutil"
	"github.com/stretchr/testify/require"
)

// okHandler is a trivial 200-OK handler used by several tests below.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// ─────────────────────────────────────────────────────────────
// BackoffLimiter helpers
// ─────────────────────────────────────────────────────────────

func TestNewTestBackoffLimiter_AllowsOnFreshKey(t *testing.T) {
	t.Parallel()
	l := ratelimitest.NewTestBackoffLimiter()

	ok, _ := l.Allow(context.Background(), "1.2.3.4")
	require.True(t, ok)
}

func TestNewTestBackoffLimiter_BlocksAfterFailure(t *testing.T) {
	t.Parallel()
	l := ratelimitest.NewTestBackoffLimiter()
	ctx := context.Background()

	l.RecordFailure(ctx, "1.2.3.4")

	ok, _ := l.Allow(ctx, "1.2.3.4")
	require.False(t, ok)
}

func TestNewTestBackoffLimiter_AllowsAfterWindowExpires(t *testing.T) {
	t.Parallel()
	l := ratelimitest.NewTestBackoffLimiter()
	ctx := context.Background()

	l.RecordFailure(ctx, "1.2.3.4")
	// maxDelay=12ms; sleep past it.
	time.Sleep(20 * time.Millisecond)

	ok, _ := l.Allow(ctx, "1.2.3.4")
	require.True(t, ok)
}

// ─────────────────────────────────────────────────────────────
// IPRateLimiter helpers
// ─────────────────────────────────────────────────────────────

func TestNewTestIPRateLimiter_ExhaustsAfterBurst(t *testing.T) {
	t.Parallel()
	l := ratelimitest.NewTestIPRateLimiter(0, 1) // burst=1: one request allowed

	ctx := context.Background()
	require.True(t, l.Allow(ctx, "1.2.3.4"))
	require.False(t, l.Allow(ctx, "1.2.3.4"))
}

func TestNewPermissiveIPRateLimiter_NeverBlocks(t *testing.T) {
	t.Parallel()
	l := ratelimitest.NewPermissiveIPRateLimiter()

	ctx := context.Background()
	for range 20 {
		require.True(t, l.Allow(ctx, "1.2.3.4"))
	}
}

func TestNewPermissiveIPRateLimiter_Middleware_Allows200(t *testing.T) {
	t.Parallel()
	l := ratelimitest.NewPermissiveIPRateLimiter()
	mw := l.Limit(okHandler())

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "1.2.3.4:5678"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

// ─────────────────────────────────────────────────────────────
// TrustedProxy helpers
// ─────────────────────────────────────────────────────────────

func TestMustParseTrustedProxies_ReturnsNetworks(t *testing.T) {
	t.Parallel()
	nets := ratelimitest.MustParseTrustedProxies("10.0.0.0/8", "172.16.0.0/12")
	require.Len(t, nets, 2)
}

func TestMustParseTrustedProxies_EmptyArgs_ReturnsNil(t *testing.T) {
	t.Parallel()
	nets := ratelimitest.MustParseTrustedProxies()
	require.Nil(t, nets)
}

func TestMustParseTrustedProxies_SingleEmptyString_ReturnsNil(t *testing.T) {
	t.Parallel()
	nets := ratelimitest.MustParseTrustedProxies("")
	require.Nil(t, nets)
}

func TestMustParseTrustedProxies_PanicsOnBadCIDR(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() {
		ratelimitest.MustParseTrustedProxies("not-a-cidr")
	})
}

func TestMustParseTrustedProxies_CIDRContainsExpectedAddress(t *testing.T) {
	t.Parallel()
	nets := ratelimitest.MustParseTrustedProxies("10.0.0.0/8")
	require.Len(t, nets, 1)
	require.True(t, nets[0].Contains([]byte{10, 0, 0, 1}))
	require.False(t, nets[0].Contains([]byte{192, 168, 1, 1}))
}
