package token

import "time"

// JWTConfig holds the four JWT configuration values shared by the login and
// session route configs and handlers. Embed this in RouteConfig to eliminate
// the repeated JWTAccessSecret / JWTRefreshSecret / AccessTTL / SecureCookies
// quartet.
type JWTConfig struct {
	// JWTAccessSecret is the HMAC-SHA256 signing key for access tokens.
	// Must be at least 32 bytes; validated by config.validate() at startup.
	JWTAccessSecret string
	// JWTRefreshSecret is the HMAC-SHA256 signing key for refresh tokens.
	// Must differ from JWTAccessSecret; validated by config.validate() at startup.
	JWTRefreshSecret string
	// AccessTTL is the lifetime of a signed access token.
	// Keep short (≤15m) — access tokens are not server-side revocable.
	AccessTTL time.Duration
	// SecureCookies controls the Secure attribute on the refresh-token cookie.
	// Must be true in production (HTTPS); false only in known HTTP dev environments.
	SecureCookies bool
}
