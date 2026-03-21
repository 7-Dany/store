// Tests in package token (internal) so they can call sign() directly and
// access unexported constants such as maxAccessTTL.
package token

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// mintTestSecret is a 32-byte secret for use in this file.
// Defined here because this file is package token (not token_test) and
// cannot share testSecret from jwt_test.go (package token_test).
const mintTestSecret = "super-secret-key-for-tests-only1"

// ── test helpers ─────────────────────────────────────────────────────────────

// testCustomClaims embeds jwt.RegisteredClaims — used by sign() tests that
// need a concrete claims type with the standard expiry field.
type testCustomClaims struct {
	Foo string `json:"foo"`
	jwt.RegisteredClaims
}

// testBareCustomClaims satisfies jwt.Claims without embedding RegisteredClaims.
// Used to verify that sign() now correctly rejects missing exp via
// GetExpirationTime() — closing the gap the previous reflect implementation had.
type testBareCustomClaims struct {
	Foo  string
	base jwt.MapClaims
}

func (c testBareCustomClaims) GetExpirationTime() (*jwt.NumericDate, error) {
	return c.base.GetExpirationTime()
}
func (c testBareCustomClaims) GetIssuedAt() (*jwt.NumericDate, error)  { return c.base.GetIssuedAt() }
func (c testBareCustomClaims) GetNotBefore() (*jwt.NumericDate, error) { return c.base.GetNotBefore() }
func (c testBareCustomClaims) GetIssuer() (string, error)              { return c.base.GetIssuer() }
func (c testBareCustomClaims) GetSubject() (string, error)             { return c.base.GetSubject() }
func (c testBareCustomClaims) GetAudience() (jwt.ClaimStrings, error)  { return c.base.GetAudience() }

// ── sign ─────────────────────────────────────────────────────────────────────

func TestSign_ValidClaims_ReturnsSigned(t *testing.T) {
	t.Parallel()
	claims := &testCustomClaims{
		Foo: "bar",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	signed, err := sign(claims, mintTestSecret)
	require.NoError(t, err)
	require.NotEmpty(t, signed)
}

func TestSign_NilClaims_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := sign(nil, mintTestSecret)
	require.Error(t, err)
	require.ErrorContains(t, err, "nil")
}

// TestSign_NilPointerClaims_ReturnsError covers F-09: a non-nil interface
// wrapping a nil pointer must be caught before any method is invoked (which
// would otherwise panic inside GetExpirationTime).
func TestSign_NilPointerClaims_ReturnsError(t *testing.T) {
	t.Parallel()
	var c *AccessClaims // typed nil — non-nil interface, nil concrete value
	_, err := sign(c, mintTestSecret)
	require.Error(t, err)
	require.ErrorContains(t, err, "nil")
}

func TestSign_ShortSecret_ReturnsError(t *testing.T) {
	t.Parallel()
	claims := &testCustomClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	_, err := sign(claims, "tooshort")
	require.Error(t, err)
	require.ErrorContains(t, err, "32 bytes")
}

func TestSign_CanBeVerifiedWithParseClaims(t *testing.T) {
	t.Parallel()
	claims := &testCustomClaims{
		Foo: "verified",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	signed, err := sign(claims, mintTestSecret)
	require.NoError(t, err)

	parsed := &testCustomClaims{}
	_, parseErr := jwt.ParseWithClaims(signed, parsed, func(*jwt.Token) (any, error) {
		return []byte(mintTestSecret), nil
	})
	require.NoError(t, parseErr)
	require.Equal(t, "verified", parsed.Foo)
}

func TestSign_MissingExp_ReturnsError(t *testing.T) {
	t.Parallel()
	claims := &testCustomClaims{
		Foo:              "no-exp",
		RegisteredClaims: jwt.RegisteredClaims{},
	}
	_, err := sign(claims, mintTestSecret)
	require.Error(t, err)
	require.ErrorContains(t, err, "ExpiresAt")
}

func TestSign_NilExp_ReturnsError(t *testing.T) {
	t.Parallel()
	claims := &testCustomClaims{
		Foo:              "nil-exp",
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: nil},
	}
	_, err := sign(claims, mintTestSecret)
	require.Error(t, err)
	require.ErrorContains(t, err, "ExpiresAt")
}

// TestSign_BareCustomClaimsWithoutExp_ReturnsError verifies F-03 is fixed.
// The previous reflect-based implementation had a known gap: bare custom claims
// (not embedding jwt.RegisteredClaims) bypassed the exp check entirely.
// The new GetExpirationTime() call works for ALL jwt.Claims types.
func TestSign_BareCustomClaimsWithoutExp_ReturnsError(t *testing.T) {
	t.Parallel()
	claims := testBareCustomClaims{Foo: "no-exp-bare"} // base is nil MapClaims → no exp
	_, err := sign(claims, mintTestSecret)
	require.Error(t, err, "bare custom claims without exp must now be rejected (F-03 closed)")
	require.ErrorContains(t, err, "ExpiresAt")
}

// TestSign_BareCustomClaimsWithExp_Succeeds verifies that bare custom claims
// WITH a valid exp are accepted — the fix must not over-reject.
func TestSign_BareCustomClaimsWithExp_Succeeds(t *testing.T) {
	t.Parallel()
	// jwt.MapClaims.GetExpirationTime() expects exp as a float64 Unix timestamp
	// (the JSON number format). Passing a *jwt.NumericDate struct instead would
	// cause "invalid type for claim" — use float64(unix) as MapClaims requires.
	claims := testBareCustomClaims{
		Foo:  "has-exp",
		base: jwt.MapClaims{"exp": float64(time.Now().Add(time.Hour).Unix())},
	}
	signed, err := sign(claims, mintTestSecret)
	require.NoError(t, err)
	require.NotEmpty(t, signed)
}

// ── GenerateAccessToken ───────────────────────────────────────────────────────

func TestGenerateAccessToken_HappyPath(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	sessionID := uuid.NewString()

	tok, err := GenerateAccessToken(userID, sessionID, 15*time.Minute, mintTestSecret)
	require.NoError(t, err)
	require.NotEmpty(t, tok)

	claims, err := ParseAccessToken(tok, mintTestSecret)
	require.NoError(t, err)
	require.Equal(t, userID, claims.Subject)
	require.Equal(t, sessionID, claims.SessionID)
	require.Contains(t, []string(claims.Audience), AudienceAccess)
	require.Equal(t, Issuer, claims.Issuer)
	require.NotEmpty(t, claims.ID)
}

func TestGenerateAccessToken_EmptySecret(t *testing.T) {
	t.Parallel()
	_, err := GenerateAccessToken(uuid.NewString(), uuid.NewString(), time.Minute, "")
	require.Error(t, err)
}

func TestGenerateAccessToken_ShortSecret(t *testing.T) {
	t.Parallel()
	_, err := GenerateAccessToken(uuid.NewString(), uuid.NewString(), time.Minute, "shortkey")
	require.Error(t, err)
	require.ErrorContains(t, err, "32 bytes")
}

func TestGenerateAccessToken_ZeroTTL(t *testing.T) {
	t.Parallel()
	_, err := GenerateAccessToken(uuid.NewString(), uuid.NewString(), 0, mintTestSecret)
	require.Error(t, err)
}

func TestGenerateAccessToken_NegativeTTL(t *testing.T) {
	t.Parallel()
	_, err := GenerateAccessToken(uuid.NewString(), uuid.NewString(), -time.Second, mintTestSecret)
	require.Error(t, err)
}

// TestGenerateAccessToken_TTLAtCeiling verifies that maxAccessTTL exactly is accepted.
func TestGenerateAccessToken_TTLAtCeiling(t *testing.T) {
	t.Parallel()
	tok, err := GenerateAccessToken(uuid.NewString(), uuid.NewString(), maxAccessTTL, mintTestSecret)
	require.NoError(t, err)
	require.NotEmpty(t, tok)
}

// TestGenerateAccessToken_TTLExceedsCeiling verifies F-12: a TTL above maxAccessTTL is rejected.
func TestGenerateAccessToken_TTLExceedsCeiling(t *testing.T) {
	t.Parallel()
	_, err := GenerateAccessToken(uuid.NewString(), uuid.NewString(), maxAccessTTL+time.Second, mintTestSecret)
	require.Error(t, err)
	require.ErrorContains(t, err, "exceeds maximum")
}

// TestGenerateAccessToken_EmptyUserID verifies F-14.
func TestGenerateAccessToken_EmptyUserID(t *testing.T) {
	t.Parallel()
	_, err := GenerateAccessToken("", uuid.NewString(), time.Minute, mintTestSecret)
	require.Error(t, err)
	require.ErrorContains(t, err, "userID")
}

// TestGenerateAccessToken_EmptySessionID verifies F-14.
func TestGenerateAccessToken_EmptySessionID(t *testing.T) {
	t.Parallel()
	_, err := GenerateAccessToken(uuid.NewString(), "", time.Minute, mintTestSecret)
	require.Error(t, err)
	require.ErrorContains(t, err, "sessionID")
}

func TestGenerateAccessToken_UniqueJTI(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	sessionID := uuid.NewString()

	tok1, err := GenerateAccessToken(userID, sessionID, time.Minute, mintTestSecret)
	require.NoError(t, err)
	tok2, err := GenerateAccessToken(userID, sessionID, time.Minute, mintTestSecret)
	require.NoError(t, err)

	c1, err := ParseAccessToken(tok1, mintTestSecret)
	require.NoError(t, err)
	c2, err := ParseAccessToken(tok2, mintTestSecret)
	require.NoError(t, err)
	require.NotEqual(t, c1.ID, c2.ID)
}

func TestGenerateAccessToken_ExpClaim(t *testing.T) {
	t.Parallel()
	ttl := 15 * time.Minute
	tok, err := GenerateAccessToken(uuid.NewString(), uuid.NewString(), ttl, mintTestSecret)
	require.NoError(t, err)

	claims, err := ParseAccessToken(tok, mintTestSecret)
	require.NoError(t, err)

	want := time.Now().Add(ttl)
	diff := claims.ExpiresAt.Time.Sub(want)
	if diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("ExpiresAt %v not within ±2s of expected %v", claims.ExpiresAt.Time, want)
	}
}

// ── GenerateRefreshToken ──────────────────────────────────────────────────────

func TestGenerateRefreshToken_HappyPath(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	sessionID := uuid.NewString()
	jti := uuid.NewString()
	familyID := uuid.NewString()
	expiresAt := time.Now().Add(7 * 24 * time.Hour)

	tok, err := GenerateRefreshToken(userID, sessionID, jti, familyID, expiresAt, mintTestSecret)
	require.NoError(t, err)
	require.NotEmpty(t, tok)

	claims, err := ParseRefreshToken(tok, mintTestSecret)
	require.NoError(t, err)
	require.Equal(t, userID, claims.Subject)
	require.Equal(t, sessionID, claims.SessionID)
	require.Equal(t, familyID, claims.FamilyID)
	require.Equal(t, jti, claims.ID)
	require.Contains(t, []string(claims.Audience), AudienceRefresh)
	require.Equal(t, Issuer, claims.Issuer)
}

func TestGenerateRefreshToken_EmptySecret(t *testing.T) {
	t.Parallel()
	_, err := GenerateRefreshToken(
		uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(),
		time.Now().Add(time.Hour), "",
	)
	require.Error(t, err)
}

func TestGenerateRefreshToken_ExpiresAtInPast(t *testing.T) {
	t.Parallel()
	_, err := GenerateRefreshToken(
		uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(),
		time.Now().Add(-time.Second), mintTestSecret,
	)
	require.Error(t, err)
}

// TestGenerateRefreshToken_EmptyUserID verifies F-14.
func TestGenerateRefreshToken_EmptyUserID(t *testing.T) {
	t.Parallel()
	_, err := GenerateRefreshToken("", uuid.NewString(), uuid.NewString(), uuid.NewString(), time.Now().Add(time.Hour), mintTestSecret)
	require.Error(t, err)
	require.ErrorContains(t, err, "userID")
}

// TestGenerateRefreshToken_EmptySessionID verifies F-14.
func TestGenerateRefreshToken_EmptySessionID(t *testing.T) {
	t.Parallel()
	_, err := GenerateRefreshToken(uuid.NewString(), "", uuid.NewString(), uuid.NewString(), time.Now().Add(time.Hour), mintTestSecret)
	require.Error(t, err)
	require.ErrorContains(t, err, "sessionID")
}

// TestGenerateRefreshToken_EmptyRefreshJTI verifies F-14.
func TestGenerateRefreshToken_EmptyRefreshJTI(t *testing.T) {
	t.Parallel()
	_, err := GenerateRefreshToken(uuid.NewString(), uuid.NewString(), "", uuid.NewString(), time.Now().Add(time.Hour), mintTestSecret)
	require.Error(t, err)
	require.ErrorContains(t, err, "refreshJTI")
}

// TestGenerateRefreshToken_EmptyFamilyID verifies F-14.
func TestGenerateRefreshToken_EmptyFamilyID(t *testing.T) {
	t.Parallel()
	_, err := GenerateRefreshToken(uuid.NewString(), uuid.NewString(), uuid.NewString(), "", time.Now().Add(time.Hour), mintTestSecret)
	require.Error(t, err)
	require.ErrorContains(t, err, "familyID")
}

func TestGenerateRefreshToken_ExpClaim(t *testing.T) {
	t.Parallel()
	expiresAt := time.Now().Add(7 * 24 * time.Hour).Truncate(time.Second)

	tok, err := GenerateRefreshToken(
		uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(),
		expiresAt, mintTestSecret,
	)
	require.NoError(t, err)

	claims, err := ParseRefreshToken(tok, mintTestSecret)
	require.NoError(t, err)

	diff := claims.ExpiresAt.Time.Sub(expiresAt)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("ExpiresAt %v not within ±1s of expected %v", claims.ExpiresAt.Time, expiresAt)
	}
}

// ── MintTokens ────────────────────────────────────────────────────────────────

func validMintInput() MintTokensInput {
	return MintTokensInput{
		UserID:        [16]byte(uuid.New()),
		SessionID:     [16]byte(uuid.New()),
		RefreshJTI:    [16]byte(uuid.New()),
		FamilyID:      [16]byte(uuid.New()),
		RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
	}
}

func validMintCfg() JWTConfig {
	return JWTConfig{
		JWTAccessSecret:  "test-access-secret-for-mint-tests-ok",
		JWTRefreshSecret: "test-refresh-secret-for-mint-tests-ok",
		AccessTTL:        15 * time.Minute,
		SecureCookies:    false,
	}
}

func TestMintTokens_Success(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	in := validMintInput()

	result, err := MintTokens(w, in, validMintCfg())

	require.NoError(t, err)
	require.NotEmpty(t, result.AccessToken)
	require.NotEmpty(t, result.RefreshToken) // internal field — still populated
	require.Equal(t, in.RefreshExpiry, result.RefreshExpiry)
	require.Equal(t, 900, result.ExpiresIn) // 15 min * 60 s

	resp := w.Result()
	cookies := resp.Cookies()
	require.NotEmpty(t, cookies, "expected refresh cookie to be set")
	var found bool
	for _, c := range cookies {
		if c.Name == RefreshTokenCookie {
			found = true
			require.Equal(t, result.RefreshToken, c.Value)
		}
	}
	require.True(t, found, "refresh token cookie not found")
}

// TestMintTokens_RefreshTokenNotInJSON verifies F-02: the refresh token must
// not be present in the JSON-serialised TokenResult, only in the cookie.
func TestMintTokens_RefreshTokenNotInJSON(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()

	result, err := MintTokens(w, validMintInput(), validMintCfg())
	require.NoError(t, err)
	require.NotEmpty(t, result.RefreshToken, "internal field must still be populated")

	b, err := json.Marshal(result)
	require.NoError(t, err)
	body := string(b)
	require.NotContains(t, body, result.RefreshToken, "refresh token value must not appear in JSON")
	require.NotContains(t, body, "refresh_token", "refresh_token key must not appear in JSON")
}

// TestMintTokens_ZeroUserID verifies F-11: a zero-value UserID is rejected.
func TestMintTokens_ZeroUserID(t *testing.T) {
	t.Parallel()
	in := validMintInput()
	in.UserID = [16]byte{}
	_, err := MintTokens(httptest.NewRecorder(), in, validMintCfg())
	require.Error(t, err)
	require.ErrorContains(t, err, "UserID")
}

// TestMintTokens_ZeroSessionID verifies F-11.
func TestMintTokens_ZeroSessionID(t *testing.T) {
	t.Parallel()
	in := validMintInput()
	in.SessionID = [16]byte{}
	_, err := MintTokens(httptest.NewRecorder(), in, validMintCfg())
	require.Error(t, err)
	require.ErrorContains(t, err, "SessionID")
}

// TestMintTokens_ZeroRefreshJTI verifies F-11.
func TestMintTokens_ZeroRefreshJTI(t *testing.T) {
	t.Parallel()
	in := validMintInput()
	in.RefreshJTI = [16]byte{}
	_, err := MintTokens(httptest.NewRecorder(), in, validMintCfg())
	require.Error(t, err)
	require.ErrorContains(t, err, "RefreshJTI")
}

// TestMintTokens_ZeroFamilyID verifies F-11.
func TestMintTokens_ZeroFamilyID(t *testing.T) {
	t.Parallel()
	in := validMintInput()
	in.FamilyID = [16]byte{}
	_, err := MintTokens(httptest.NewRecorder(), in, validMintCfg())
	require.Error(t, err)
	require.ErrorContains(t, err, "FamilyID")
}

func TestMintTokens_EmptyAccessSecret(t *testing.T) {
	t.Parallel()
	cfg := validMintCfg()
	cfg.JWTAccessSecret = ""
	_, err := MintTokens(httptest.NewRecorder(), validMintInput(), cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "token.MintTokens.sign_access_token")
	require.Empty(t, httptest.NewRecorder().Result().Cookies())
}

func TestMintTokens_EmptyRefreshSecret(t *testing.T) {
	t.Parallel()
	cfg := validMintCfg()
	cfg.JWTRefreshSecret = ""
	w := httptest.NewRecorder()
	_, err := MintTokens(w, validMintInput(), cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "token.MintTokens.sign_refresh_token")
	require.Empty(t, w.Result().Cookies(), "no cookie must be set when refresh signing fails")
}

func TestMintTokens_PastRefreshExpiry(t *testing.T) {
	t.Parallel()
	in := validMintInput()
	in.RefreshExpiry = time.Now().Add(-time.Second)
	_, err := MintTokens(httptest.NewRecorder(), in, validMintCfg())
	require.Error(t, err)
	require.ErrorContains(t, err, "token.MintTokens.sign_refresh_token")
}

func TestMintTokens_AccessTokenParseable(t *testing.T) {
	t.Parallel()
	userID := uuid.New()
	sessionID := uuid.New()
	in := validMintInput()
	in.UserID = [16]byte(userID)
	in.SessionID = [16]byte(sessionID)
	cfg := validMintCfg()

	result, err := MintTokens(httptest.NewRecorder(), in, cfg)
	require.NoError(t, err)

	claims, err := ParseAccessToken(result.AccessToken, cfg.JWTAccessSecret)
	require.NoError(t, err)
	require.Equal(t, userID.String(), claims.Subject)
	require.Equal(t, sessionID.String(), claims.SessionID)
}

func TestMintTokens_RefreshTokenParseable(t *testing.T) {
	t.Parallel()
	userID := uuid.New()
	sessionID := uuid.New()
	refreshJTI := uuid.New()
	familyID := uuid.New()
	in := MintTokensInput{
		UserID:        [16]byte(userID),
		SessionID:     [16]byte(sessionID),
		RefreshJTI:    [16]byte(refreshJTI),
		FamilyID:      [16]byte(familyID),
		RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
	}
	cfg := validMintCfg()

	result, err := MintTokens(httptest.NewRecorder(), in, cfg)
	require.NoError(t, err)

	claims, err := ParseRefreshToken(result.RefreshToken, cfg.JWTRefreshSecret)
	require.NoError(t, err)
	require.Equal(t, userID.String(), claims.Subject)
	require.Equal(t, sessionID.String(), claims.SessionID)
	require.Equal(t, refreshJTI.String(), claims.ID)
	require.Equal(t, familyID.String(), claims.FamilyID)
}

func TestMintTokens_ExpiresInMatchesTTL(t *testing.T) {
	t.Parallel()
	ttl := 30 * time.Minute
	cfg := validMintCfg()
	cfg.AccessTTL = ttl

	result, err := MintTokens(httptest.NewRecorder(), validMintInput(), cfg)
	require.NoError(t, err)
	require.Equal(t, int(ttl.Seconds()), result.ExpiresIn)
}
