package token

import (
	"errors"
	"fmt"
	"time"
)

// maxAccessTTL is the absolute ceiling for access token lifetime enforced by
// both ValidateJWTConfig (startup) and GenerateAccessToken (runtime).
// Access tokens are not server-side revocable, so allowing arbitrarily long
// TTLs via a misconfigured env var would produce near-eternal tokens with no
// revocation path. 24h is a generous upper bound for any legitimate use case.
const maxAccessTTL = 24 * time.Hour

// JWTConfig holds the four JWT configuration values shared by the login and
// session route configs and handlers. Embed this in RouteConfig to eliminate
// the repeated JWTAccessSecret / JWTRefreshSecret / AccessTTL / SecureCookies
// quartet.
type JWTConfig struct {
	// JWTAccessSecret is the HMAC-SHA256 signing key for access tokens.
	// Must be at least 32 bytes of high-entropy random data.
	// Must differ from JWTRefreshSecret; validated by ValidateJWTConfig.
	JWTAccessSecret string
	// JWTRefreshSecret is the HMAC-SHA256 signing key for refresh tokens.
	// Must be at least 32 bytes of high-entropy random data.
	// Must differ from JWTAccessSecret; validated by ValidateJWTConfig.
	JWTRefreshSecret string
	// AccessTTL is the lifetime of a signed access token.
	// Keep short (≤15m) — access tokens are not server-side revocable.
	// Maximum allowed value is maxAccessTTL (24h); validated by ValidateJWTConfig.
	AccessTTL time.Duration
	// SecureCookies controls the Secure attribute on the refresh-token cookie.
	// Must be true in production (HTTPS); false only in known HTTP dev environments.
	SecureCookies bool
}

// ValidateJWTConfig validates a JWTConfig at application startup and returns
// an error for any field that would produce an insecure configuration.
// Call this once during server initialisation, before the first token is minted.
//
// isDev relaxes the SecureCookies requirement for known HTTP-only local
// development environments. Never pass isDev=true in production.
//
// Enforces (F-05, F-12, F-13):
//   - Both secrets are at least 32 bytes.
//   - Access and refresh secrets are different keys (cross-audience replay risk).
//   - AccessTTL is positive and does not exceed maxAccessTTL (24h).
//   - SecureCookies is true outside of dev (HTTPS required for refresh cookie).
//
// Note on secret entropy (F-06): length alone does not guarantee security.
// Secrets must be generated with a CSPRNG, e.g.:
//
//	openssl rand -hex 32   # produces 64 hex chars (256 bits)
func ValidateJWTConfig(cfg JWTConfig, isDev bool) error {
	if len(cfg.JWTAccessSecret) < 32 {
		return fmt.Errorf("token: JWTAccessSecret must be at least 32 bytes (got %d)", len(cfg.JWTAccessSecret))
	}
	if len(cfg.JWTRefreshSecret) < 32 {
		return fmt.Errorf("token: JWTRefreshSecret must be at least 32 bytes (got %d)", len(cfg.JWTRefreshSecret))
	}
	// F-13: identical secrets allow cross-audience token replay. A refresh token
	// becomes a structurally valid access token (and vice versa) whenever audience
	// checking is inadvertently skipped at any endpoint.
	if cfg.JWTAccessSecret == cfg.JWTRefreshSecret {
		return errors.New("token: JWTAccessSecret and JWTRefreshSecret must be different keys (cross-audience replay risk)")
	}
	if cfg.AccessTTL <= 0 {
		return fmt.Errorf("token: AccessTTL must be positive (got %s)", cfg.AccessTTL)
	}
	// F-12: maxAccessTTL ceiling is also enforced inside GenerateAccessToken.
	// Checking here provides an earlier, clearer startup error.
	if cfg.AccessTTL > maxAccessTTL {
		return fmt.Errorf("token: AccessTTL %s exceeds maximum allowed %s (access tokens are not revocable)", cfg.AccessTTL, maxAccessTTL)
	}
	// F-05: a false SecureCookies in production means the refresh token is sent
	// over plain HTTP and is trivially interceptable via network sniff or MITM.
	if !isDev && !cfg.SecureCookies {
		return errors.New("token: SecureCookies must be true in production (HTTPS required for refresh cookie)")
	}
	return nil
}
