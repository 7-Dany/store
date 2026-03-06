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
