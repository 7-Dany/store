// Tests in package token (internal) so they share mintTestSecret and can
// call sign() directly for edge-case assertions if needed.
package token

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// btcTestSecret is a 32-byte secret used by bitcoin SSE token tests.
// Intentionally different from mintTestSecret to mirror the production
// requirement that BTC_SSE_SIGNING_SECRET != JWT access/refresh secrets.
const btcTestSecret = "btc-sse-signing-secret-for-tests1"

// ── helpers ──────────────────────────────────────────────────────────────────

// validBTCInput returns a fully-populated BitcoinSSETokenInput for use in
// tests that only want to vary one field.
func validBTCInput() BitcoinSSETokenInput {
	return BitcoinSSETokenInput{
		UserID:        uuid.NewString(),
		JTI:           uuid.NewString(),
		SID:           "hmac-of-session-id-and-jti-placeholder",
		IPClaim:       "203.0.113.0/24",
		TTL:           60 * time.Second,
		SigningSecret: btcTestSecret,
	}
}

// ── GenerateBitcoinSSEToken ───────────────────────────────────────────────────

func TestGenerateBitcoinSSEToken_HappyPath(t *testing.T) {
	t.Parallel()
	in := validBTCInput()

	tok, err := GenerateBitcoinSSEToken(in)
	require.NoError(t, err)
	require.NotEmpty(t, tok)

	claims, err := ParseBitcoinSSEToken(tok, in.SigningSecret)
	require.NoError(t, err)
	require.Equal(t, in.UserID, claims.Subject)
	require.Equal(t, in.JTI, claims.ID)
	require.Equal(t, in.SID, claims.SID)
	require.Equal(t, in.IPClaim, claims.IPClaim)
	require.Equal(t, Issuer, claims.Issuer)
	require.Contains(t, []string(claims.Audience), AudienceBitcoinSSE)
	require.NotNil(t, claims.ExpiresAt)
	require.NotNil(t, claims.IssuedAt)
}

// TestGenerateBitcoinSSEToken_EmptyIPClaim verifies that IPClaim is optional.
// IPv6 clients and deployments with BTC_SSE_TOKEN_BIND_IP=false pass "".
func TestGenerateBitcoinSSEToken_EmptyIPClaim(t *testing.T) {
	t.Parallel()
	in := validBTCInput()
	in.IPClaim = ""

	tok, err := GenerateBitcoinSSEToken(in)
	require.NoError(t, err)

	claims, err := ParseBitcoinSSEToken(tok, in.SigningSecret)
	require.NoError(t, err)
	require.Empty(t, claims.IPClaim)
}

func TestGenerateBitcoinSSEToken_ShortSecret_ReturnsError(t *testing.T) {
	t.Parallel()
	in := validBTCInput()
	in.SigningSecret = "tooshort"

	_, err := GenerateBitcoinSSEToken(in)
	require.Error(t, err)
	require.ErrorContains(t, err, "32 bytes")
}

func TestGenerateBitcoinSSEToken_ZeroTTL_ReturnsError(t *testing.T) {
	t.Parallel()
	in := validBTCInput()
	in.TTL = 0

	_, err := GenerateBitcoinSSEToken(in)
	require.Error(t, err)
	require.ErrorContains(t, err, "TTL")
}

func TestGenerateBitcoinSSEToken_NegativeTTL_ReturnsError(t *testing.T) {
	t.Parallel()
	in := validBTCInput()
	in.TTL = -time.Second

	_, err := GenerateBitcoinSSEToken(in)
	require.Error(t, err)
	require.ErrorContains(t, err, "TTL")
}

func TestGenerateBitcoinSSEToken_EmptyUserID_ReturnsError(t *testing.T) {
	t.Parallel()
	in := validBTCInput()
	in.UserID = ""

	_, err := GenerateBitcoinSSEToken(in)
	require.Error(t, err)
	require.ErrorContains(t, err, "UserID")
}

func TestGenerateBitcoinSSEToken_EmptyJTI_ReturnsError(t *testing.T) {
	t.Parallel()
	in := validBTCInput()
	in.JTI = ""

	_, err := GenerateBitcoinSSEToken(in)
	require.Error(t, err)
	require.ErrorContains(t, err, "JTI")
}

func TestGenerateBitcoinSSEToken_EmptySID_ReturnsError(t *testing.T) {
	t.Parallel()
	in := validBTCInput()
	in.SID = ""

	_, err := GenerateBitcoinSSEToken(in)
	require.Error(t, err)
	require.ErrorContains(t, err, "SID")
}

// TestGenerateBitcoinSSEToken_ExpClaimMatchesTTL verifies exp = iat + TTL within 2s.
func TestGenerateBitcoinSSEToken_ExpClaimMatchesTTL(t *testing.T) {
	t.Parallel()
	in := validBTCInput()
	in.TTL = 90 * time.Second
	before := time.Now()

	tok, err := GenerateBitcoinSSEToken(in)
	require.NoError(t, err)

	claims, err := ParseBitcoinSSEToken(tok, in.SigningSecret)
	require.NoError(t, err)

	want := before.Add(in.TTL)
	diff := claims.ExpiresAt.Time.Sub(want)
	require.LessOrEqualf(t, diff.Abs(), 2*time.Second,
		"ExpiresAt %v not within ±2s of expected %v", claims.ExpiresAt.Time, want)
}

// ── ParseBitcoinSSEToken ──────────────────────────────────────────────────────

func TestParseBitcoinSSEToken_EmptySecret_ReturnsError(t *testing.T) {
	t.Parallel()
	in := validBTCInput()
	tok, err := GenerateBitcoinSSEToken(in)
	require.NoError(t, err)

	_, err = ParseBitcoinSSEToken(tok, "")
	require.Error(t, err)
	require.ErrorContains(t, err, "empty")
}

func TestParseBitcoinSSEToken_WrongSecret_ReturnsError(t *testing.T) {
	t.Parallel()
	tok, err := GenerateBitcoinSSEToken(validBTCInput())
	require.NoError(t, err)

	_, err = ParseBitcoinSSEToken(tok, "completely-different-secret-32byt")
	require.Error(t, err)
}

func TestParseBitcoinSSEToken_ExpiredToken_ReturnsError(t *testing.T) {
	t.Parallel()
	in := validBTCInput()
	// Sign a claims struct directly with a past expiry, bypassing the TTL>0 check
	// in GenerateBitcoinSSEToken, to produce a legitimately expired token.
	claims := &BitcoinSSEClaims{
		SID:     in.SID,
		IPClaim: in.IPClaim,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Subject:   in.UserID,
			Audience:  jwt.ClaimStrings{AudienceBitcoinSSE},
			ID:        in.JTI,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	}
	tok, err := sign(claims, in.SigningSecret)
	require.NoError(t, err)

	_, err = ParseBitcoinSSEToken(tok, in.SigningSecret)
	require.Error(t, err)
}

// TestParseBitcoinSSEToken_AccessTokenRejected verifies that a valid access
// token is rejected by ParseBitcoinSSEToken — wrong audience.
func TestParseBitcoinSSEToken_AccessTokenRejected(t *testing.T) {
	t.Parallel()
	accessTok, err := GenerateAccessToken(
		uuid.NewString(), uuid.NewString(),
		15*time.Minute, mintTestSecret,
	)
	require.NoError(t, err)

	// Even with the same secret, audience mismatch must cause rejection.
	_, err = ParseBitcoinSSEToken(accessTok, mintTestSecret)
	require.Error(t, err)
}

// TestParseBitcoinSSEToken_SSETokenRejectedByAccessParser verifies the reverse:
// a bitcoin SSE token must be rejected by ParseAccessToken — wrong audience.
func TestParseBitcoinSSEToken_SSETokenRejectedByAccessParser(t *testing.T) {
	t.Parallel()
	in := validBTCInput()
	// Use the same secret to isolate the audience check.
	in.SigningSecret = mintTestSecret
	tok, err := GenerateBitcoinSSEToken(in)
	require.NoError(t, err)

	_, err = ParseAccessToken(tok, mintTestSecret)
	require.Error(t, err)
}

// TestGenerateBitcoinSSEToken_AudienceIsBitcoinSSE verifies the aud claim
// is exactly AudienceBitcoinSSE and nothing else.
func TestGenerateBitcoinSSEToken_AudienceIsBitcoinSSE(t *testing.T) {
	t.Parallel()
	tok, err := GenerateBitcoinSSEToken(validBTCInput())
	require.NoError(t, err)

	claims, err := ParseBitcoinSSEToken(tok, btcTestSecret)
	require.NoError(t, err)
	require.Len(t, claims.Audience, 1)
	require.Equal(t, AudienceBitcoinSSE, claims.Audience[0])
}

// TestGenerateBitcoinSSEToken_IssuerIsStore verifies the iss claim.
func TestGenerateBitcoinSSEToken_IssuerIsStore(t *testing.T) {
	t.Parallel()
	tok, err := GenerateBitcoinSSEToken(validBTCInput())
	require.NoError(t, err)

	claims, err := ParseBitcoinSSEToken(tok, btcTestSecret)
	require.NoError(t, err)
	require.Equal(t, Issuer, claims.Issuer)
}
