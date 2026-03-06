// Package token_test contains tests for the token package.
package token_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/platform/token"
)

// findCookie locates the named cookie in a recorder response and fails the
// test immediately if the cookie is absent.
func findCookie(t *testing.T, w *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cookie %q not found in response", name)
	return nil
}

// ── SetRefreshCookie ──────────────────────────────────────────────────────────

// TestSetRefreshCookie_HttpOnly asserts that the cookie is flagged HttpOnly.
func TestSetRefreshCookie_HttpOnly(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	token.SetRefreshCookie(w, "tok", time.Now().Add(time.Hour), false)
	c := findCookie(t, w, token.RefreshTokenCookie)
	require.True(t, c.HttpOnly, "refresh cookie must be HttpOnly to prevent XSS token theft")
}

// TestSetRefreshCookie_SameSiteStrict asserts that the cookie uses SameSite=Strict.
func TestSetRefreshCookie_SameSiteStrict(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	token.SetRefreshCookie(w, "tok", time.Now().Add(time.Hour), false)
	c := findCookie(t, w, token.RefreshTokenCookie)
	require.Equal(t, http.SameSiteStrictMode, c.SameSite,
		"refresh cookie must use SameSite=Strict to prevent CSRF")
}

// TestSetRefreshCookie_SecureTrue asserts that Secure=true when the parameter is true.
func TestSetRefreshCookie_SecureTrue(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	token.SetRefreshCookie(w, "tok", time.Now().Add(time.Hour), true)
	c := findCookie(t, w, token.RefreshTokenCookie)
	require.True(t, c.Secure, "Secure must be true when secure=true is passed")
}

// TestSetRefreshCookie_SecureFalse asserts that Secure=false when the parameter is false.
func TestSetRefreshCookie_SecureFalse(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	token.SetRefreshCookie(w, "tok", time.Now().Add(time.Hour), false)
	c := findCookie(t, w, token.RefreshTokenCookie)
	require.False(t, c.Secure, "Secure must mirror the secure parameter")
}

// TestSetRefreshCookie_Path asserts the cookie is scoped to /api/v1/auth.
func TestSetRefreshCookie_Path(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	token.SetRefreshCookie(w, "tok", time.Now().Add(time.Hour), false)
	c := findCookie(t, w, token.RefreshTokenCookie)
	require.Equal(t, "/api/v1/auth", c.Path,
		"cookie path must be /api/v1/auth so it is only sent to auth endpoints")
}

// TestSetRefreshCookie_MaxAgeDerivedFromExpiry asserts that MaxAge is computed
// from the expiry parameter, not a hardcoded constant.
func TestSetRefreshCookie_MaxAgeDerivedFromExpiry(t *testing.T) {
	t.Parallel()
	expiry := time.Now().Add(7 * 24 * time.Hour)
	w := httptest.NewRecorder()
	token.SetRefreshCookie(w, "tok", expiry, false)
	c := findCookie(t, w, token.RefreshTokenCookie)
	wantSeconds := int(time.Until(expiry).Seconds())
	// Allow ±2 seconds of clock drift between the SetRefreshCookie call and
	// the time.Until evaluation here.
	diff := c.MaxAge - wantSeconds
	if diff < -2 || diff > 2 {
		t.Errorf("MaxAge %d is not within ±2s of expected %d", c.MaxAge, wantSeconds)
	}
}

// TestSetRefreshCookie_Value asserts the token value is stored in the cookie.
func TestSetRefreshCookie_Value(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	token.SetRefreshCookie(w, "my-token-value", time.Now().Add(time.Hour), false)
	c := findCookie(t, w, token.RefreshTokenCookie)
	require.Equal(t, "my-token-value", c.Value)
}

// ── ClearRefreshCookie ────────────────────────────────────────────────────────

// TestClearRefreshCookie_MaxAgeNegativeOne asserts MaxAge is -1 so the browser
// deletes the cookie immediately.
func TestClearRefreshCookie_MaxAgeNegativeOne(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	token.ClearRefreshCookie(w, false)
	c := findCookie(t, w, token.RefreshTokenCookie)
	require.Equal(t, -1, c.MaxAge,
		"MaxAge must be -1 to instruct the browser to delete the cookie on logout")
}

// TestClearRefreshCookie_ValueEmpty asserts the cookie value is cleared.
func TestClearRefreshCookie_ValueEmpty(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	token.ClearRefreshCookie(w, false)
	c := findCookie(t, w, token.RefreshTokenCookie)
	require.Empty(t, c.Value, "cookie value must be empty on logout")
}

// TestClearRefreshCookie_HttpOnly asserts the cookie retains HttpOnly on
// clear so the clear instruction cannot be intercepted by JavaScript.
func TestClearRefreshCookie_HttpOnly(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	token.ClearRefreshCookie(w, false)
	c := findCookie(t, w, token.RefreshTokenCookie)
	require.True(t, c.HttpOnly)
}

// TestClearRefreshCookie_Path asserts the path matches SetRefreshCookie so
// the browser can match and clear the original cookie.
func TestClearRefreshCookie_Path(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	token.ClearRefreshCookie(w, false)
	c := findCookie(t, w, token.RefreshTokenCookie)
	require.Equal(t, "/api/v1/auth", c.Path,
		"clear cookie path must match set cookie path or the browser will not clear it")
}

// TestClearRefreshCookie_SameSiteStrict asserts SameSite=Strict is preserved
// on the clear cookie.
func TestClearRefreshCookie_SameSiteStrict(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	token.ClearRefreshCookie(w, false)
	c := findCookie(t, w, token.RefreshTokenCookie)
	require.Equal(t, http.SameSiteStrictMode, c.SameSite)
}
