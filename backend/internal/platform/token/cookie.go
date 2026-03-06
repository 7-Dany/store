package token

import (
	"net/http"
	"time"
)

// RefreshTokenCookie is the name of the HttpOnly cookie that carries the
// refresh token. Shared by all auth handlers that read or clear this cookie.
const RefreshTokenCookie = "refresh_token"

// SetRefreshCookie writes the refresh token as an HttpOnly, SameSite=Strict
// cookie scoped to the auth path.
//
// Security: HttpOnly + SameSiteStrictMode prevent XSS/CSRF token theft.
// The Secure flag is set in production (secure=true); disabled for local HTTP dev.
// Path is scoped to /api/v1/auth so the cookie is only sent to auth endpoints.
func SetRefreshCookie(w http.ResponseWriter, tok string, expiry time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshTokenCookie,
		Value:    tok,
		Path:     "/api/v1/auth",
		Expires:  expiry,
		MaxAge:   int(time.Until(expiry).Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearRefreshCookie immediately expires the refresh-token cookie.
//
// Security: cookie flags must match SetRefreshCookie exactly so the browser
// clears the cookie that was set. A Path mismatch would leave the cookie in place.
func ClearRefreshCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshTokenCookie,
		Value:    "",
		Path:     "/api/v1/auth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}
