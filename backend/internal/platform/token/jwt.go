// Package token provides JWT generation, parsing, middleware, and context helpers for the store API.
package token

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Audience and issuer constants enforce per-token-type audience claims so that
// an access token cannot be accepted at the refresh endpoint and vice-versa,
// even when both share the same HMAC key.
const (
	// AudienceAccess is the audience claim embedded in every access token.
	AudienceAccess = "store:access"

	// AudienceRefresh is the audience claim embedded in every refresh token.
	AudienceRefresh = "store:refresh"

	// Issuer is the "iss" claim used in every token issued by this service.
	Issuer = "store"

	// maxExtractedKeyLen is the maximum number of bytes accepted from a field
	// extractor before the value is treated as absent. This bounds the size of
	// keys written to the store regardless of what user-controlled input contains.
	// 320 = 254 (max RFC 5321 email) + 66 bytes of headroom.
	maxExtractedKeyLen = 320
)

// AccessClaims are the payload fields embedded in every access token.
// Access tokens are short-lived (default 15 min) and are NOT server-side
// revokable — keep the TTL short and store no mutable user state here.
type AccessClaims struct {
	// SessionID (sid) is the user_sessions.id that this token was issued for.
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

// RefreshClaims are the payload fields embedded in every refresh token.
// The DB row is the source of truth for revocation; these claims are only
// used to locate and validate the row without exposing raw UUIDs in cookies.
type RefreshClaims struct {
	// FamilyID (fid) is the refresh_tokens.family_id used for reuse-detection revocation.
	FamilyID string `json:"fid"`
	// SessionID (sid) mirrors the session_id stored on the refresh_token row.
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

// ParseAccessToken validates and parses a signed access JWT.
//
// Validations performed:
//   - Algorithm: must be HS256; "none" and all non-HMAC methods are rejected.
//   - Signature: verified with secret.
//   - Issuer: must be "store".
//   - Audience: must contain "store:access" — refresh tokens are rejected here.
//   - Expiry: token must not be expired; ExpiresAt claim is required.
//
// Returns an error if secret is empty, the token is malformed, or any
// validation fails.
func ParseAccessToken(tokenString, secret string) (*AccessClaims, error) {
	if secret == "" {
		return nil, fmt.Errorf("token.ParseAccessToken: empty signing secret")
	}
	claims := &AccessClaims{}
	_, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(_ *jwt.Token) (any, error) { return []byte(secret), nil },
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(AudienceAccess),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}),
	)
	if err != nil {
		return nil, fmt.Errorf("token.ParseAccessToken: %w", err)
	}
	return claims, nil
}

// ParseRefreshToken validates and parses a signed refresh JWT.
//
// Validations performed:
//   - Algorithm: must be HS256; "none" and all non-HMAC methods are rejected.
//   - Signature: verified with secret.
//   - Issuer: must be "store".
//   - Audience: must contain "store:refresh" — access tokens are rejected here.
//   - Expiry: token must not be expired; ExpiresAt claim is required.
//
// Returns an error if secret is empty, the token is malformed, or any
// validation fails.
func ParseRefreshToken(tokenString, secret string) (*RefreshClaims, error) {
	if secret == "" {
		return nil, fmt.Errorf("token.ParseRefreshToken: empty signing secret")
	}
	claims := &RefreshClaims{}
	_, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(_ *jwt.Token) (any, error) { return []byte(secret), nil },
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(AudienceRefresh),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}),
	)
	if err != nil {
		return nil, fmt.Errorf("token.ParseRefreshToken: %w", err)
	}
	return claims, nil
}

// JWTSubjectExtractor returns a field extractor that reads the
// Authorization: Bearer <token> header, parses the access JWT with the
// provided secret, and returns the sub claim. Any parse failure returns ""
// so the IP limiter remains as the coarse guard.
//
// Pass the result to NewUserRateLimiter as the fieldExtractor argument.
func JWTSubjectExtractor(secret string) func(*http.Request) string {
	return func(r *http.Request) string {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			return ""
		}
		claims, err := ParseAccessToken(raw, secret)
		if err != nil || claims.Subject == "" {
			return ""
		}
		if len(claims.Subject) > maxExtractedKeyLen {
			return ""
		}
		return claims.Subject
	}
}
