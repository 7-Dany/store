// Package tokentest provides JWT-related test helpers for domain test suites.
package tokentest

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/google/uuid"
)

// InjectUserIDForTest writes userID into ctx using the same key that the Auth
// middleware uses, bypassing JWT validation in handler tests.
func InjectUserIDForTest(ctx context.Context, userID string) context.Context {
	return token.InjectUserIDForTest(ctx, userID)
}

// InjectSessionIDForTest writes sessionID into ctx using the same key that the
// Auth middleware uses.
func InjectSessionIDForTest(ctx context.Context, sessionID string) context.Context {
	return token.InjectSessionIDForTest(ctx, sessionID)
}

// MakeAccessToken generates a signed access token for use in tests.
// It fails the test immediately if generation fails.
func MakeAccessToken(t testing.TB, userID, sessionID, secret string, ttl time.Duration) string {
	t.Helper()
	tok, err := token.GenerateAccessToken(userID, sessionID, ttl, secret)
	if err != nil {
		t.Fatalf("tokentest.MakeAccessToken: %v", err)
	}
	return tok
}

// MakeRefreshToken generates a signed refresh token for use in tests.
// It fails the test immediately if generation fails.
func MakeRefreshToken(t testing.TB, userID, sessionID, secret string, ttl time.Duration) string {
	t.Helper()
	jti := uuid.NewString()
	familyID := uuid.NewString()
	tok, err := token.GenerateRefreshToken(userID, sessionID, jti, familyID, time.Now().Add(ttl), secret)
	if err != nil {
		t.Fatalf("tokentest.MakeRefreshToken: %v", err)
	}
	return tok
}

// MakeExpiredAccessToken generates a signed access token whose expiry is one
// minute in the past. Useful for testing rejection of stale tokens.
//
// The token is built directly with the JWT library rather than via
// GenerateAccessToken because GenerateAccessToken rejects non-positive TTLs
// as a defence-in-depth guard; here we intentionally want a past expiry for
// test purposes.
func MakeExpiredAccessToken(t testing.TB, userID, sessionID, secret string) string {
	t.Helper()
	if secret == "" {
		t.Fatalf("tokentest.MakeExpiredAccessToken: secret must not be empty")
		return ""
	}
	now := time.Now()
	claims := token.AccessClaims{
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    token.Issuer,
			Subject:   userID,
			Audience:  jwt.ClaimStrings{token.AudienceAccess},
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Minute)),
		},
	}
	raw := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := raw.SignedString([]byte(secret)) // HMAC-SHA256 with a []byte key never errors
	return signed
}

// MakeExpiredRefreshToken generates a signed refresh token whose expiry is one
// minute in the past. Useful for testing rejection of stale refresh cookies.
//
// The token is built directly with the JWT library rather than via
// GenerateRefreshToken because GenerateRefreshToken rejects past expiresAt
// values; here we intentionally want a past expiry for test purposes.
func MakeExpiredRefreshToken(t testing.TB, userID, sessionID, secret string) string {
	t.Helper()
	if secret == "" {
		t.Fatalf("tokentest.MakeExpiredRefreshToken: secret must not be empty")
		return ""
	}
	now := time.Now()
	jti := uuid.NewString()
	familyID := uuid.NewString()
	claims := token.RefreshClaims{
		FamilyID:  familyID,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    token.Issuer,
			Subject:   userID,
			Audience:  jwt.ClaimStrings{token.AudienceRefresh},
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Minute)),
		},
	}
	raw := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := raw.SignedString([]byte(secret)) // HMAC-SHA256 with a []byte key never errors
	return signed
}
