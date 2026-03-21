package token_test

import (
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/stretchr/testify/require"
)

// TestJWTConfig_StructLiteral verifies that JWTConfig can be constructed and
// its fields read back correctly.
func TestJWTConfig_StructLiteral(t *testing.T) {
	t.Parallel()

	cfg := token.JWTConfig{
		JWTAccessSecret:  "access-secret",
		JWTRefreshSecret: "refresh-secret",
		AccessTTL:        15 * time.Minute,
		SecureCookies:    true,
	}

	require.Equal(t, "access-secret", cfg.JWTAccessSecret)
	require.Equal(t, "refresh-secret", cfg.JWTRefreshSecret)
	require.Equal(t, 15*time.Minute, cfg.AccessTTL)
	require.True(t, cfg.SecureCookies)
}

// TestJWTConfig_ZeroValue verifies the zero value is safe to inspect.
func TestJWTConfig_ZeroValue(t *testing.T) {
	t.Parallel()

	var cfg token.JWTConfig

	require.Empty(t, cfg.JWTAccessSecret)
	require.Empty(t, cfg.JWTRefreshSecret)
	require.Zero(t, cfg.AccessTTL)
	require.False(t, cfg.SecureCookies)
}

// ── ValidateJWTConfig ─────────────────────────────────────────────────────────

// validCfgProd returns a fully valid production JWTConfig for use in
// ValidateJWTConfig subtests.
func validCfgProd() token.JWTConfig {
	return token.JWTConfig{
		JWTAccessSecret:  "access-secret-32-bytes-long-ok!!",
		JWTRefreshSecret: "refresh-secret-32-bytes-long-ok!",
		AccessTTL:        15 * time.Minute,
		SecureCookies:    true,
	}
}

func TestValidateJWTConfig_ValidProduction(t *testing.T) {
	t.Parallel()
	require.NoError(t, token.ValidateJWTConfig(validCfgProd(), false))
}

func TestValidateJWTConfig_ValidDev(t *testing.T) {
	t.Parallel()
	cfg := validCfgProd()
	cfg.SecureCookies = false
	require.NoError(t, token.ValidateJWTConfig(cfg, true))
}

// TestValidateJWTConfig_ShortAccessSecret verifies F-05 / secret length guard.
func TestValidateJWTConfig_ShortAccessSecret(t *testing.T) {
	t.Parallel()
	cfg := validCfgProd()
	cfg.JWTAccessSecret = "tooshort"
	require.ErrorContains(t, token.ValidateJWTConfig(cfg, false), "JWTAccessSecret")
}

// TestValidateJWTConfig_ShortRefreshSecret verifies secret length guard.
func TestValidateJWTConfig_ShortRefreshSecret(t *testing.T) {
	t.Parallel()
	cfg := validCfgProd()
	cfg.JWTRefreshSecret = "tooshort"
	require.ErrorContains(t, token.ValidateJWTConfig(cfg, false), "JWTRefreshSecret")
}

// TestValidateJWTConfig_SameSecrets verifies F-13: identical keys are rejected.
func TestValidateJWTConfig_SameSecrets(t *testing.T) {
	t.Parallel()
	cfg := validCfgProd()
	cfg.JWTRefreshSecret = cfg.JWTAccessSecret
	err := token.ValidateJWTConfig(cfg, false)
	require.Error(t, err)
	require.ErrorContains(t, err, "different keys")
}

// TestValidateJWTConfig_ZeroTTL verifies TTL guard.
func TestValidateJWTConfig_ZeroTTL(t *testing.T) {
	t.Parallel()
	cfg := validCfgProd()
	cfg.AccessTTL = 0
	require.Error(t, token.ValidateJWTConfig(cfg, false))
}

// TestValidateJWTConfig_NegativeTTL verifies TTL guard.
func TestValidateJWTConfig_NegativeTTL(t *testing.T) {
	t.Parallel()
	cfg := validCfgProd()
	cfg.AccessTTL = -time.Second
	require.Error(t, token.ValidateJWTConfig(cfg, false))
}

// TestValidateJWTConfig_TTLExceedsCeiling verifies F-12 ceiling.
func TestValidateJWTConfig_TTLExceedsCeiling(t *testing.T) {
	t.Parallel()
	cfg := validCfgProd()
	cfg.AccessTTL = 25 * time.Hour // above maxAccessTTL (24h)
	err := token.ValidateJWTConfig(cfg, false)
	require.Error(t, err)
	require.ErrorContains(t, err, "exceeds maximum")
}

// TestValidateJWTConfig_InsecureCookiesProd verifies F-05: SecureCookies=false
// is rejected in production (isDev=false).
func TestValidateJWTConfig_InsecureCookiesProd(t *testing.T) {
	t.Parallel()
	cfg := validCfgProd()
	cfg.SecureCookies = false
	err := token.ValidateJWTConfig(cfg, false)
	require.Error(t, err)
	require.ErrorContains(t, err, "SecureCookies")
}

// TestValidateJWTConfig_InsecureCookiesDev verifies that SecureCookies=false
// is allowed in dev environments (isDev=true).
func TestValidateJWTConfig_InsecureCookiesDev(t *testing.T) {
	t.Parallel()
	cfg := validCfgProd()
	cfg.SecureCookies = false
	require.NoError(t, token.ValidateJWTConfig(cfg, true))
}
