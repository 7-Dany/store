package token_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestMintTokens_Success(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	in := token.MintTokensInput{
		UserID:        [16]byte(uuid.New()),
		SessionID:     [16]byte(uuid.New()),
		RefreshJTI:    [16]byte(uuid.New()),
		FamilyID:      [16]byte(uuid.New()),
		RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
	}
	cfg := token.JWTConfig{
		JWTAccessSecret:  "test-access-secret-for-mint-tests-ok",
		JWTRefreshSecret: "test-refresh-secret-for-mint-tests-ok",
		AccessTTL:        15 * time.Minute,
		SecureCookies:    false,
	}

	result, err := token.MintTokens(w, in, cfg)

	require.NoError(t, err)
	require.NotEmpty(t, result.AccessToken)
	require.NotEmpty(t, result.RefreshToken)
	require.Equal(t, in.RefreshExpiry, result.RefreshExpiry)
	require.Equal(t, 900, result.ExpiresIn) // 15 * 60

	// Verify the refresh cookie was set.
	resp := w.Result()
	cookies := resp.Cookies()
	require.NotEmpty(t, cookies, "expected refresh cookie to be set")
	var found bool
	for _, c := range cookies {
		if c.Name == token.RefreshTokenCookie {
			found = true
			require.Equal(t, result.RefreshToken, c.Value)
		}
	}
	require.True(t, found, "refresh token cookie not found")
}

func TestMintTokens_EmptyAccessSecret(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	in := token.MintTokensInput{
		UserID:        [16]byte(uuid.New()),
		SessionID:     [16]byte(uuid.New()),
		RefreshJTI:    [16]byte(uuid.New()),
		FamilyID:      [16]byte(uuid.New()),
		RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
	}
	cfg := token.JWTConfig{
		JWTAccessSecret:  "", // empty → should fail
		JWTRefreshSecret: "test-refresh-secret-for-mint-tests-ok",
		AccessTTL:        15 * time.Minute,
	}

	_, err := token.MintTokens(w, in, cfg)

	require.Error(t, err)
	require.ErrorContains(t, err, "token.MintTokens: sign access token")

	// No cookie should have been set.
	resp := w.Result()
	require.Empty(t, resp.Cookies())
}

// TestMintTokens_EmptyRefreshSecret asserts that MintTokens fails when
// JWTRefreshSecret is empty, and that no cookie is set.
func TestMintTokens_EmptyRefreshSecret(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	in := token.MintTokensInput{
		UserID:        [16]byte(uuid.New()),
		SessionID:     [16]byte(uuid.New()),
		RefreshJTI:    [16]byte(uuid.New()),
		FamilyID:      [16]byte(uuid.New()),
		RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
	}
	cfg := token.JWTConfig{
		JWTAccessSecret:  "test-access-secret-for-mint-tests-ok",
		JWTRefreshSecret: "", // empty → GenerateRefreshToken must fail
		AccessTTL:        15 * time.Minute,
	}
	_, err := token.MintTokens(w, in, cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "token.MintTokens: sign refresh token")
	require.Empty(t, w.Result().Cookies(), "no cookie must be set when refresh signing fails")
}

// TestMintTokens_PastRefreshExpiry asserts that MintTokens fails when
// RefreshExpiry is in the past.
func TestMintTokens_PastRefreshExpiry(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	in := token.MintTokensInput{
		UserID:        [16]byte(uuid.New()),
		SessionID:     [16]byte(uuid.New()),
		RefreshJTI:    [16]byte(uuid.New()),
		FamilyID:      [16]byte(uuid.New()),
		RefreshExpiry: time.Now().Add(-time.Second), // in the past
	}
	cfg := token.JWTConfig{
		JWTAccessSecret:  "test-access-secret-for-mint-tests-ok",
		JWTRefreshSecret: "test-refresh-secret-for-mint-tests-ok",
		AccessTTL:        15 * time.Minute,
	}
	_, err := token.MintTokens(w, in, cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "token.MintTokens: sign refresh token")
}

// TestMintTokens_AccessTokenParseable asserts that the access token in the
// result can be parsed with the access secret and carries the correct sub and
// sid claims.
func TestMintTokens_AccessTokenParseable(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	userID := uuid.New()
	sessionID := uuid.New()
	in := token.MintTokensInput{
		UserID:        [16]byte(userID),
		SessionID:     [16]byte(sessionID),
		RefreshJTI:    [16]byte(uuid.New()),
		FamilyID:      [16]byte(uuid.New()),
		RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
	}
	cfg := token.JWTConfig{
		JWTAccessSecret:  "test-access-secret-for-mint-tests-ok",
		JWTRefreshSecret: "test-refresh-secret-for-mint-tests-ok",
		AccessTTL:        15 * time.Minute,
	}
	result, err := token.MintTokens(w, in, cfg)
	require.NoError(t, err)

	claims, err := token.ParseAccessToken(result.AccessToken, cfg.JWTAccessSecret)
	require.NoError(t, err)
	require.Equal(t, userID.String(), claims.Subject)
	require.Equal(t, sessionID.String(), claims.SessionID)
}

// TestMintTokens_RefreshTokenParseable asserts that the refresh token in the
// result can be parsed with the refresh secret and carries the correct sub,
// jti, fid, and sid claims.
func TestMintTokens_RefreshTokenParseable(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	userID := uuid.New()
	sessionID := uuid.New()
	refreshJTI := uuid.New()
	familyID := uuid.New()
	in := token.MintTokensInput{
		UserID:        [16]byte(userID),
		SessionID:     [16]byte(sessionID),
		RefreshJTI:    [16]byte(refreshJTI),
		FamilyID:      [16]byte(familyID),
		RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
	}
	cfg := token.JWTConfig{
		JWTAccessSecret:  "test-access-secret-for-mint-tests-ok",
		JWTRefreshSecret: "test-refresh-secret-for-mint-tests-ok",
		AccessTTL:        15 * time.Minute,
	}
	result, err := token.MintTokens(w, in, cfg)
	require.NoError(t, err)

	claims, err := token.ParseRefreshToken(result.RefreshToken, cfg.JWTRefreshSecret)
	require.NoError(t, err)
	require.Equal(t, userID.String(), claims.Subject)
	require.Equal(t, sessionID.String(), claims.SessionID)
	require.Equal(t, refreshJTI.String(), claims.ID)
	require.Equal(t, familyID.String(), claims.FamilyID)
}

// TestMintTokens_ExpiresInMatchesTTL asserts that TokenResult.ExpiresIn equals
// the AccessTTL expressed in whole seconds.
func TestMintTokens_ExpiresInMatchesTTL(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	ttl := 30 * time.Minute
	in := token.MintTokensInput{
		UserID:        [16]byte(uuid.New()),
		SessionID:     [16]byte(uuid.New()),
		RefreshJTI:    [16]byte(uuid.New()),
		FamilyID:      [16]byte(uuid.New()),
		RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
	}
	cfg := token.JWTConfig{
		JWTAccessSecret:  "test-access-secret-for-mint-tests-ok",
		JWTRefreshSecret: "test-refresh-secret-for-mint-tests-ok",
		AccessTTL:        ttl,
	}
	result, err := token.MintTokens(w, in, cfg)
	require.NoError(t, err)
	require.Equal(t, int(ttl.Seconds()), result.ExpiresIn)
}

// ── GenerateAccessToken ────────────────────────────────────────────────────────────────────

func TestGenerateAccessToken_HappyPath(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	sessionID := uuid.NewString()

	tok, err := token.GenerateAccessToken(userID, sessionID, 15*time.Minute, testSecret)
	require.NoError(t, err)
	require.NotEmpty(t, tok)

	claims, err := token.ParseAccessToken(tok, testSecret)
	require.NoError(t, err)
	require.Equal(t, userID, claims.Subject)
	require.Equal(t, sessionID, claims.SessionID)
	require.Contains(t, []string(claims.Audience), token.AudienceAccess)
	require.Equal(t, token.Issuer, claims.Issuer)
	require.NotEmpty(t, claims.ID)
}

func TestGenerateAccessToken_EmptySecret(t *testing.T) {
	t.Parallel()
	_, err := token.GenerateAccessToken(uuid.NewString(), uuid.NewString(), time.Minute, "")
	require.Error(t, err)
}

func TestGenerateAccessToken_UniqueJTI(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	sessionID := uuid.NewString()

	tok1, err := token.GenerateAccessToken(userID, sessionID, time.Minute, testSecret)
	require.NoError(t, err)
	tok2, err := token.GenerateAccessToken(userID, sessionID, time.Minute, testSecret)
	require.NoError(t, err)

	c1, err := token.ParseAccessToken(tok1, testSecret)
	require.NoError(t, err)
	c2, err := token.ParseAccessToken(tok2, testSecret)
	require.NoError(t, err)
	require.NotEqual(t, c1.ID, c2.ID)
}

// ── GenerateRefreshToken ────────────────────────────────────────────────────────────────────

func TestGenerateRefreshToken_HappyPath(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	sessionID := uuid.NewString()
	jti := uuid.NewString()
	familyID := uuid.NewString()
	expiresAt := time.Now().Add(7 * 24 * time.Hour)

	tok, err := token.GenerateRefreshToken(userID, sessionID, jti, familyID, expiresAt, testSecret)
	require.NoError(t, err)
	require.NotEmpty(t, tok)

	claims, err := token.ParseRefreshToken(tok, testSecret)
	require.NoError(t, err)
	require.Equal(t, userID, claims.Subject)
	require.Equal(t, sessionID, claims.SessionID)
	require.Equal(t, familyID, claims.FamilyID)
	require.Equal(t, jti, claims.ID)
	require.Contains(t, []string(claims.Audience), token.AudienceRefresh)
	require.Equal(t, token.Issuer, claims.Issuer)
}

func TestGenerateRefreshToken_EmptySecret(t *testing.T) {
	t.Parallel()
	_, err := token.GenerateRefreshToken(
		uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(),
		time.Now().Add(time.Hour), "",
	)
	require.Error(t, err)
}

// TestGenerateAccessToken_ExpClaim asserts the exp claim is set correctly.
func TestGenerateAccessToken_ExpClaim(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	sessionID := uuid.NewString()
	ttl := 15 * time.Minute

	tok, err := token.GenerateAccessToken(userID, sessionID, ttl, testSecret)
	require.NoError(t, err)

	claims, err := token.ParseAccessToken(tok, testSecret)
	require.NoError(t, err)

	want := time.Now().Add(ttl)
	diff := claims.ExpiresAt.Time.Sub(want)
	if diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("ExpiresAt %v not within ±2s of expected %v", claims.ExpiresAt.Time, want)
	}
}

// TestGenerateRefreshToken_ExpClaim asserts the exp claim matches expiresAt.
func TestGenerateRefreshToken_ExpClaim(t *testing.T) {
	t.Parallel()
	expiresAt := time.Now().Add(7 * 24 * time.Hour).Truncate(time.Second)

	tok, err := token.GenerateRefreshToken(
		uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(),
		expiresAt, testSecret,
	)
	require.NoError(t, err)

	claims, err := token.ParseRefreshToken(tok, testSecret)
	require.NoError(t, err)

	diff := claims.ExpiresAt.Time.Sub(expiresAt)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("ExpiresAt %v not within ±1s of expected %v", claims.ExpiresAt.Time, expiresAt)
	}
}

// TestGenerateAccessToken_ZeroTTL asserts that a zero TTL is rejected.
func TestGenerateAccessToken_ZeroTTL(t *testing.T) {
	t.Parallel()
	_, err := token.GenerateAccessToken(uuid.NewString(), uuid.NewString(), 0, testSecret)
	require.Error(t, err)
}

// TestGenerateAccessToken_NegativeTTL asserts that a negative TTL is rejected.
func TestGenerateAccessToken_NegativeTTL(t *testing.T) {
	t.Parallel()
	_, err := token.GenerateAccessToken(uuid.NewString(), uuid.NewString(), -time.Second, testSecret)
	require.Error(t, err)
}

// TestGenerateRefreshToken_ExpiresAtInPast asserts that an expiresAt in the past is rejected.
func TestGenerateRefreshToken_ExpiresAtInPast(t *testing.T) {
	t.Parallel()
	_, err := token.GenerateRefreshToken(
		uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(),
		time.Now().Add(-time.Second), testSecret,
	)
	require.Error(t, err)
}

// TestGenerateAccessToken_ShortSecret_31Bytes asserts that a secret shorter than 32 bytes is rejected.
func TestGenerateAccessToken_ShortSecret_31Bytes(t *testing.T) {
	t.Parallel()
	_, err := token.GenerateAccessToken(uuid.NewString(), uuid.NewString(), time.Minute, "shortkey")
	require.Error(t, err)
	require.ErrorContains(t, err, "32 bytes")
}
