package token

import (
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// GenerateAccessToken signs a short-lived HS256 access JWT.
//
// Claims embedded: sub=userID, sid=sessionID, jti=new UUID, iss="store",
// aud=["store:access"], iat=now, exp=now+ttl.
//
// Returns an error if secret is shorter than 32 bytes, if ttl is zero or
// negative, or if signing fails.
func GenerateAccessToken(userID, sessionID string, ttl time.Duration, secret string) (string, error) {
	if len(secret) < 32 {
		return "", fmt.Errorf("token.GenerateAccessToken: signing secret must be at least 32 bytes (got %d)", len(secret))
	}
	if ttl <= 0 {
		return "", fmt.Errorf("token.GenerateAccessToken: ttl must be positive (got %s)", ttl)
	}
	now := time.Now()
	claims := AccessClaims{
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   Issuer,
			Subject:  userID,
			Audience: jwt.ClaimStrings{AudienceAccess},
			// JTI is a raw UUID v4 with no type prefix. Access and refresh tokens share
			// the same blocklist namespace; UUID v4 collision probability (~10^-37 at
			// 10^9 tokens/s) makes cross-type collisions negligible. If prefixes are
			// ever added here, BlockToken and IsTokenBlocked call sites must be updated
			// simultaneously.
			ID:       uuid.NewString(),
			IssuedAt: jwt.NewNumericDate(now),
			// NotBefore is intentionally omitted: tokens are valid immediately on
			// issuance and the access TTL is short enough (default 15 min) that
			// clock-skew tolerance from nbf would provide no practical benefit.
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := t.SignedString([]byte(secret)) // HMAC-SHA256 with a []byte key never errors
	return signed, nil
}

// GenerateRefreshToken signs a long-lived HS256 refresh JWT.
//
// Claims embedded: sub=userID, jti=refreshJTI (the DB row primary key),
// fid=familyID, sid=sessionID, iss="store", aud=["store:refresh"],
// iat=now, exp=expiresAt (copied from the DB row so they stay in sync).
//
// Returns an error if secret is shorter than 32 bytes or if signing fails.
// Returns an error if expiresAt is not in the future or if signing fails.
func GenerateRefreshToken(userID, sessionID, refreshJTI, familyID string, expiresAt time.Time, secret string) (string, error) {
	if len(secret) < 32 {
		return "", fmt.Errorf("token.GenerateRefreshToken: signing secret must be at least 32 bytes (got %d)", len(secret))
	}
	if !expiresAt.After(time.Now()) {
		return "", fmt.Errorf("token.GenerateRefreshToken: expiresAt must be in the future")
	}
	now := time.Now()
	claims := RefreshClaims{
		FamilyID:  familyID,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   Issuer,
			Subject:  userID,
			Audience: jwt.ClaimStrings{AudienceRefresh},
			ID:       refreshJTI,
			IssuedAt: jwt.NewNumericDate(now),
			// NotBefore is intentionally omitted: tokens are valid immediately on
			// issuance and the access TTL is short enough (default 15 min) that
			// clock-skew tolerance from nbf would provide no practical benefit.
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := t.SignedString([]byte(secret)) // HMAC-SHA256 with a []byte key never errors
	return signed, nil
}

// MintTokensInput carries the identifiers needed to sign a new access + refresh
// token pair. Produced by login.LoggedInSession or session.RotatedSession at
// the handler layer.
type MintTokensInput struct {
	UserID        [16]byte
	SessionID     [16]byte
	RefreshJTI    [16]byte
	FamilyID      [16]byte
	RefreshExpiry time.Time
}

// TokenResult is returned by any handler method that mints a new access/refresh
// token pair. Used by the login and session handlers.
type TokenResult struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"refresh_token"`
	RefreshExpiry time.Time `json:"refresh_expiry"`
	ExpiresIn     int       `json:"expires_in"`
}

// MintTokens generates a new access token and rotated refresh token, sets the
// refresh token as an HttpOnly cookie, and returns a TokenResult ready to be
// written to the JSON response body.
//
// This is the handler-layer helper shared by login.Handler and session.Handler.
// JWT signing remains the handler's exclusive responsibility (ADR-001); the
// secrets are passed in via cfg and never reach the service layer.
func MintTokens(
	w http.ResponseWriter,
	in MintTokensInput,
	cfg JWTConfig,
) (TokenResult, error) {
	accessToken, err := GenerateAccessToken(
		uuid.UUID(in.UserID).String(),
		uuid.UUID(in.SessionID).String(),
		cfg.AccessTTL,
		cfg.JWTAccessSecret,
	)
	if err != nil {
		return TokenResult{}, fmt.Errorf("token.MintTokens: sign access token: %w", err)
	}

	refreshToken, err := GenerateRefreshToken(
		uuid.UUID(in.UserID).String(),
		uuid.UUID(in.SessionID).String(),
		uuid.UUID(in.RefreshJTI).String(),
		uuid.UUID(in.FamilyID).String(),
		in.RefreshExpiry,
		cfg.JWTRefreshSecret,
	)
	if err != nil {
		return TokenResult{}, fmt.Errorf("token.MintTokens: sign refresh token: %w", err)
	}

	SetRefreshCookie(w, refreshToken, in.RefreshExpiry, cfg.SecureCookies)

	return TokenResult{
		AccessToken:   accessToken,
		RefreshToken:  refreshToken,
		RefreshExpiry: in.RefreshExpiry,
		ExpiresIn:     int(cfg.AccessTTL.Seconds()),
	}, nil
}
