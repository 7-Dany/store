package token_test

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/platform/token"
)

const testSecret = "super-secret-key-for-tests-only1"

// ── ParseAccessToken ──────────────────────────────────────────────────────────

func TestParseAccessToken_EmptySecret(t *testing.T) {
	t.Parallel()
	_, err := token.ParseAccessToken("anything", "")
	require.Error(t, err)
}

func TestParseAccessToken_WrongSecret(t *testing.T) {
	t.Parallel()
	tok, err := token.GenerateAccessToken(uuid.NewString(), uuid.NewString(), time.Minute, testSecret)
	require.NoError(t, err)
	_, err = token.ParseAccessToken(tok, "wrong-secret")
	require.Error(t, err)
}

func TestParseAccessToken_MalformedToken(t *testing.T) {
	t.Parallel()
	_, err := token.ParseAccessToken("not.a.jwt", testSecret)
	require.Error(t, err)
}

func TestParseAccessToken_ExpiredToken(t *testing.T) {
	t.Parallel()
	now := time.Now()
	claims := token.AccessClaims{
		SessionID: uuid.NewString(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    token.Issuer,
			Subject:   uuid.NewString(),
			Audience:  jwt.ClaimStrings{token.AudienceAccess},
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Minute)),
		},
	}
	raw := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := raw.SignedString([]byte(testSecret))
	require.NoError(t, err)

	_, err = token.ParseAccessToken(signed, testSecret)
	require.Error(t, err)
}

func TestParseAccessToken_AlgorithmRS256Rejected(t *testing.T) {
	t.Parallel()
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    token.Issuer,
		Audience:  jwt.ClaimStrings{token.AudienceAccess},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		IssuedAt:  jwt.NewNumericDate(now),
	}
	raw := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := raw.SignedString(privKey)
	require.NoError(t, err)

	_, err = token.ParseAccessToken(signed, testSecret)
	require.Error(t, err)
}

func TestParseAccessToken_AlgorithmNoneRejected(t *testing.T) {
	t.Parallel()
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    token.Issuer,
		Audience:  jwt.ClaimStrings{token.AudienceAccess},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		IssuedAt:  jwt.NewNumericDate(now),
	}
	raw := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := raw.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = token.ParseAccessToken(signed, testSecret)
	require.Error(t, err)
}

func TestParseAccessToken_AudienceEnforced_RefreshTokenRejected(t *testing.T) {
	t.Parallel()
	tok, err := token.GenerateRefreshToken(
		uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(),
		time.Now().Add(time.Minute), testSecret,
	)
	require.NoError(t, err)

	_, err = token.ParseAccessToken(tok, testSecret)
	require.Error(t, err)
}

// ── ParseRefreshToken ─────────────────────────────────────────────────────────

func TestParseRefreshToken_EmptySecret(t *testing.T) {
	t.Parallel()
	_, err := token.ParseRefreshToken("anything", "")
	require.Error(t, err)
}

func TestParseRefreshToken_WrongSecret(t *testing.T) {
	t.Parallel()
	tok, err := token.GenerateRefreshToken(
		uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(),
		time.Now().Add(time.Hour), testSecret,
	)
	require.NoError(t, err)
	_, err = token.ParseRefreshToken(tok, "wrong-secret")
	require.Error(t, err)
}

func TestParseRefreshToken_AlgorithmRS256Rejected(t *testing.T) {
	t.Parallel()
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    token.Issuer,
		Audience:  jwt.ClaimStrings{token.AudienceRefresh},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		IssuedAt:  jwt.NewNumericDate(now),
	}
	raw := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := raw.SignedString(privKey)
	require.NoError(t, err)

	_, err = token.ParseRefreshToken(signed, testSecret)
	require.Error(t, err)
}

func TestParseRefreshToken_AlgorithmNoneRejected(t *testing.T) {
	t.Parallel()
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    token.Issuer,
		Audience:  jwt.ClaimStrings{token.AudienceRefresh},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		IssuedAt:  jwt.NewNumericDate(now),
	}
	raw := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := raw.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = token.ParseRefreshToken(signed, testSecret)
	require.Error(t, err)
}

func TestParseRefreshToken_AudienceEnforced_AccessTokenRejected(t *testing.T) {
	t.Parallel()
	tok, err := token.GenerateAccessToken(uuid.NewString(), uuid.NewString(), time.Minute, testSecret)
	require.NoError(t, err)

	_, err = token.ParseRefreshToken(tok, testSecret)
	require.Error(t, err)
}

// TestParseRefreshToken_MalformedToken asserts that a non-JWT string is rejected.
func TestParseRefreshToken_MalformedToken(t *testing.T) {
	t.Parallel()
	_, err := token.ParseRefreshToken("not.a.jwt", testSecret)
	require.Error(t, err)
}

// TestParseRefreshToken_ExpiredToken asserts that a refresh token whose exp is
// in the past is rejected.
func TestParseRefreshToken_ExpiredToken(t *testing.T) {
	t.Parallel()
	now := time.Now()
	claims := token.RefreshClaims{
		FamilyID:  uuid.NewString(),
		SessionID: uuid.NewString(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    token.Issuer,
			Subject:   uuid.NewString(),
			Audience:  jwt.ClaimStrings{token.AudienceRefresh},
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Minute)),
		},
	}
	raw := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := raw.SignedString([]byte(testSecret))
	require.NoError(t, err)

	_, err = token.ParseRefreshToken(signed, testSecret)
	require.Error(t, err)
}

// TestParseRefreshToken_MissingExpClaim asserts that a refresh token with no
// exp claim is rejected.
func TestParseRefreshToken_MissingExpClaim(t *testing.T) {
	t.Parallel()
	now := time.Now()
	claims := token.RefreshClaims{
		FamilyID:  uuid.NewString(),
		SessionID: uuid.NewString(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   token.Issuer,
			Subject:  uuid.NewString(),
			Audience: jwt.ClaimStrings{token.AudienceRefresh},
			ID:       uuid.NewString(),
			IssuedAt: jwt.NewNumericDate(now),
			// ExpiresAt intentionally omitted
		},
	}
	raw := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := raw.SignedString([]byte(testSecret))
	require.NoError(t, err)

	_, err = token.ParseRefreshToken(signed, testSecret)
	require.Error(t, err, "refresh token with no exp claim must be rejected")
}

// TestParseAccessToken_MissingExpClaim asserts that a token with no exp claim is rejected.
func TestParseAccessToken_MissingExpClaim(t *testing.T) {
	t.Parallel()
	now := time.Now()
	claims := token.AccessClaims{
		SessionID: uuid.NewString(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   token.Issuer,
			Subject:  uuid.NewString(),
			Audience: jwt.ClaimStrings{token.AudienceAccess},
			ID:       uuid.NewString(),
			IssuedAt: jwt.NewNumericDate(now),
			// ExpiresAt intentionally omitted
		},
	}
	raw := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := raw.SignedString([]byte(testSecret))
	require.NoError(t, err)

	_, err = token.ParseAccessToken(signed, testSecret)
	require.Error(t, err, "token with no exp claim must be rejected")
}

// ── JWTSubjectExtractor ─────────────────────────────────────────────────────────────

// newReq is a helper that builds a GET *http.Request and optionally sets the
// Authorization header.
func newReq(authHeader string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if authHeader != "" {
		r.Header.Set("Authorization", authHeader)
	}
	return r
}

// mintCustomSubject signs an AccessClaims with an arbitrary Subject string
// using jwt directly, bypassing the 32-byte secret length check in
// GenerateAccessToken. Used for boundary tests on maxExtractedKeyLen.
func mintCustomSubject(t *testing.T, subject string) string {
	t.Helper()
	now := time.Now()
	claims := token.AccessClaims{
		SessionID: uuid.NewString(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    token.Issuer,
			Subject:   subject,
			Audience:  jwt.ClaimStrings{token.AudienceAccess},
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	require.NoError(t, err)
	return signed
}

func TestJWTSubjectExtractor_NoAuthorizationHeader(t *testing.T) {
	t.Parallel()
	extract := token.JWTSubjectExtractor(testSecret)
	require.Equal(t, "", extract(newReq("")))
}

func TestJWTSubjectExtractor_AuthHeaderMissingBearerPrefix(t *testing.T) {
	t.Parallel()
	extract := token.JWTSubjectExtractor(testSecret)
	require.Equal(t, "", extract(newReq("Token abc123")))
}

func TestJWTSubjectExtractor_EmptyBearerValue(t *testing.T) {
	t.Parallel()
	extract := token.JWTSubjectExtractor(testSecret)
	// "Bearer " with nothing after the space — raw == "" after CutPrefix.
	require.Equal(t, "", extract(newReq("Bearer ")))
}

func TestJWTSubjectExtractor_InvalidToken(t *testing.T) {
	t.Parallel()
	extract := token.JWTSubjectExtractor(testSecret)
	require.Equal(t, "", extract(newReq("Bearer not.a.jwt")))
}

func TestJWTSubjectExtractor_ValidToken_ReturnsSubject(t *testing.T) {
	t.Parallel()
	sub := uuid.NewString()
	tok, err := token.GenerateAccessToken(sub, uuid.NewString(), time.Minute, testSecret)
	require.NoError(t, err)
	extract := token.JWTSubjectExtractor(testSecret)
	require.Equal(t, sub, extract(newReq("Bearer "+tok)))
}

func TestJWTSubjectExtractor_EmptySubjectInValidToken(t *testing.T) {
	t.Parallel()
	// Craft a structurally valid token whose sub claim is empty.
	tok := mintCustomSubject(t, "")
	extract := token.JWTSubjectExtractor(testSecret)
	require.Equal(t, "", extract(newReq("Bearer "+tok)))
}

func TestJWTSubjectExtractor_SubjectExceedsMaxLen(t *testing.T) {
	t.Parallel()
	// 321 bytes > maxExtractedKeyLen (320) → extractor must return "".
	tok := mintCustomSubject(t, strings.Repeat("a", 321))
	extract := token.JWTSubjectExtractor(testSecret)
	require.Equal(t, "", extract(newReq("Bearer "+tok)))
}

func TestJWTSubjectExtractor_SubjectExactlyMaxLen(t *testing.T) {
	t.Parallel()
	// 320 bytes == maxExtractedKeyLen → extractor must return the subject unchanged.
	sub := strings.Repeat("a", 320)
	tok := mintCustomSubject(t, sub)
	extract := token.JWTSubjectExtractor(testSecret)
	require.Equal(t, sub, extract(newReq("Bearer "+tok)))
}
