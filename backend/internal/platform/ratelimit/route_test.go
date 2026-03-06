package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	ratelimitest "github.com/7-Dany/store/backend/internal/platform/ratelimit/testutil"
)

// routeOKHandler is a minimal http.HandlerFunc that writes 200 OK,
// used exclusively by the RouteWithIP tests.
var routeOKHandler http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// ─────────────────────────────────────────────────────────────────────────────
// RouteWithIP
// ─────────────────────────────────────────────────────────────────────────────

func TestRouteWithIP_NilLimiter_RegistersWithoutMiddleware(t *testing.T) {
	t.Parallel()

	r := chi.NewRouter()
	ratelimit.RouteWithIP(r, http.MethodPost, "/test", routeOKHandler, nil)

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.RemoteAddr = "1.2.3.4:9999"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestRouteWithIP_WithPermissiveLimiter_RequestPasses(t *testing.T) {
	t.Parallel()

	limiter := ratelimitest.NewPermissiveIPRateLimiter()

	r := chi.NewRouter()
	ratelimit.RouteWithIP(r, http.MethodPost, "/test", routeOKHandler, limiter)

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.RemoteAddr = "1.2.3.4:9999"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestRouteWithIP_WithExhaustedLimiter_Returns429(t *testing.T) {
	t.Parallel()

	// rate=0 means no token replenishment; burst=1 means one request is
	// allowed, then exhausted.
	limiter := ratelimitest.NewTestIPRateLimiter(0, 1)

	r := chi.NewRouter()
	ratelimit.RouteWithIP(r, http.MethodPost, "/test", routeOKHandler, limiter)

	makeRequest := func() int {
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		req.RemoteAddr = "1.2.3.4:9999"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	require.Equal(t, http.StatusOK, makeRequest())             // first request consumes the only token
	require.Equal(t, http.StatusTooManyRequests, makeRequest()) // bucket now empty
}
