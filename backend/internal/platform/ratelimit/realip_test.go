package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────
// ParseTrustedProxies
// ─────────────────────────────────────────────────────────────

func TestParseTrustedProxies_EmptyStringReturnsNil(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("")
	require.NoError(t, err)
	require.Nil(t, nets)
}

func TestParseTrustedProxies_WhitespaceOnlyReturnsNil(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("   ")
	require.NoError(t, err)
	require.Nil(t, nets)
}

func TestParseTrustedProxies_SingleCIDR(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("10.0.0.0/8")
	require.NoError(t, err)
	require.Len(t, nets, 1)
}

func TestParseTrustedProxies_MultipleCIDRs(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("10.0.0.0/8,172.16.0.0/12,192.168.0.0/16")
	require.NoError(t, err)
	require.Len(t, nets, 3)
}

func TestParseTrustedProxies_IgnoresEmptySegments(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("10.0.0.0/8,,192.168.0.0/16")
	require.NoError(t, err)
	require.Len(t, nets, 2)
}

func TestParseTrustedProxies_MalformedCIDRReturnsError(t *testing.T) {
	t.Parallel()
	_, err := ratelimit.ParseTrustedProxies("not-a-cidr")
	require.Error(t, err)
}

func TestParseTrustedProxies_MalformedCIDRInListReturnsError(t *testing.T) {
	t.Parallel()
	_, err := ratelimit.ParseTrustedProxies("10.0.0.0/8,bad")
	require.Error(t, err)
}

// ─────────────────────────────────────────────────────────────
// TrustedProxyRealIP
// ─────────────────────────────────────────────────────────────

func TestTrustedProxyRealIP_RewritesRemoteAddrFromXFF(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("10.0.0.0/8")
	require.NoError(t, err)
	mw := ratelimit.TrustedProxyRealIP(nets)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:9000" // trusted proxy
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	require.Contains(t, captured, "1.2.3.4")
}

func TestTrustedProxyRealIP_RewritesRemoteAddrFromXRealIP(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("10.0.0.0/8")
	require.NoError(t, err)
	mw := ratelimit.TrustedProxyRealIP(nets)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:9000"
	r.Header.Set("X-Real-IP", "5.6.7.8")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	require.Contains(t, captured, "5.6.7.8")
}

func TestTrustedProxyRealIP_DoesNotRewriteFromUntrustedPeer(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("10.0.0.0/8")
	require.NoError(t, err)
	mw := ratelimit.TrustedProxyRealIP(nets)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.1:9000" // not in trusted range
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	// RemoteAddr must remain unchanged.
	require.Equal(t, "203.0.113.1:9000", captured)
}

func TestTrustedProxyRealIP_EmptyCIDRListDisablesRewrite(t *testing.T) {
	t.Parallel()
	mw := ratelimit.TrustedProxyRealIP(nil)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:9000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	require.Equal(t, "10.0.0.1:9000", captured)
}

func TestTrustedProxyRealIP_XFFTakesLeftmostEntry(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("10.0.0.0/8")
	require.NoError(t, err)
	mw := ratelimit.TrustedProxyRealIP(nets)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:9000"
	// Left-most entry is the originating client.
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.2, 10.0.0.3")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	require.Contains(t, captured, "1.2.3.4")
}

func TestTrustedProxyRealIP_XFFPreferredOverXRealIP(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("10.0.0.0/8")
	require.NoError(t, err)
	mw := ratelimit.TrustedProxyRealIP(nets)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:9000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Real-IP", "9.9.9.9")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	require.Contains(t, captured, "1.2.3.4")
}

// TestTrustedProxyRealIP_BareIPRemoteAddr exercises the peerIP error branch:
// when r.RemoteAddr contains no port, net.SplitHostPort returns an error and
// peerIP falls back to net.ParseIP on the whole string. If the resulting IP is
// in a trusted CIDR the X-Forwarded-For header must still be used.
func TestTrustedProxyRealIP_BareIPRemoteAddr_TrustedProxy(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("10.0.0.0/8")
	require.NoError(t, err)
	mw := ratelimit.TrustedProxyRealIP(nets)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1" // no port — triggers SplitHostPort error branch
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	require.Contains(t, captured, "1.2.3.4")
}

// TestTrustedProxyRealIP_BareIPRemoteAddr_UntrustedProxy verifies that a bare
// IP that does NOT fall in a trusted CIDR still leaves RemoteAddr unchanged.
func TestTrustedProxyRealIP_BareIPRemoteAddr_UntrustedProxy(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("10.0.0.0/8")
	require.NoError(t, err)
	mw := ratelimit.TrustedProxyRealIP(nets)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.5" // bare IP, not in trusted range
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	// RemoteAddr must remain unchanged.
	require.Equal(t, "203.0.113.5", captured)
}

func TestTrustedProxyRealIP_NoProxyHeadersLeavesRemoteAddrUnchanged(t *testing.T) {
	t.Parallel()
	nets, err := ratelimit.ParseTrustedProxies("10.0.0.0/8")
	require.NoError(t, err)
	mw := ratelimit.TrustedProxyRealIP(nets)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:9000"
	// No X-Forwarded-For or X-Real-IP headers.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	// No client IP to extract — RemoteAddr stays as the proxy address.
	require.Equal(t, "10.0.0.1:9000", captured)
}
