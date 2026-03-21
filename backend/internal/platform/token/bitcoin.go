package token

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// AudienceBitcoinSSE is the audience claim embedded in every bitcoin SSE token.
// It is distinct from AudienceAccess and AudienceRefresh — ParseAccessToken
// and ParseRefreshToken both reject tokens carrying this audience, and
// ParseBitcoinSSEToken rejects tokens carrying any other audience.
const AudienceBitcoinSSE = "bitcoin-sse"

// BitcoinSSEClaims are the payload fields embedded in every bitcoin SSE token.
//
// Design constraints:
//   - SID is an HMAC of the session ID, not the raw session ID. The raw session
//     ID is stored server-side in Redis at "btc:token:sid:<jti>" and compared
//     against the HMAC on GET /events. This prevents session ID leakage even
//     if a token is somehow intercepted.
//   - IPClaim is the /24 CIDR subnet of the issuing client's IPv4 address.
//     Empty string when the client is on IPv6 or BTC_SSE_TOKEN_BIND_IP=false.
//     ParseBitcoinSSEToken does NOT validate these fields — the handler does
//     that in its guard sequence so audit events can be emitted on failure.
type BitcoinSSEClaims struct {
	// SID is HMAC-SHA256(BTC_SESSION_SECRET, "<len(sessionID)>:<sessionID>:<jti>").
	// Length-prefixed encoding prevents second-preimage collisions when
	// sessionID contains ':'. Never the raw session ID.
	SID string `json:"sid"`

	// IPClaim is the /24 CIDR subnet of the issuing IPv4 client address,
	// e.g. "203.0.113.0/24". Empty for IPv6 clients or when binding is disabled.
	// omitempty keeps the token compact for the common IPv6/disabled case.
	IPClaim string `json:"ip,omitempty"`

	jwt.RegisteredClaims
	// aud = AudienceBitcoinSSE ("bitcoin-sse")
	// iss = Issuer ("store")
	// sub = userID
	// jti = caller-supplied UUID (stored in Redis before this token is issued)
	// iat = issuance time
	// exp = iat + BTC_SSE_TOKEN_TTL
}

// BitcoinSSETokenInput carries the values needed to mint a bitcoin SSE token.
// All string fields must be non-empty; TTL must be positive.
type BitcoinSSETokenInput struct {
	// UserID is the authenticated user's UUID string (sub claim).
	UserID string
	// JTI is the one-time token identifier (jti claim).
	// The caller MUST store the session ID in Redis at "btc:token:sid:<JTI>"
	// BEFORE calling GenerateBitcoinSSEToken. If the Redis write fails, the
	// token must not be issued.
	JTI string
	// SID is HMAC-SHA256(BTC_SESSION_SECRET, "<len(sessionID)>:<sessionID>:<jti>").
	// Computed by the handler before constructing this input.
	SID string
	// IPClaim is the /24 CIDR subnet for the issuing client; empty string is valid
	// (IPv6 clients or when BTC_SSE_TOKEN_BIND_IP=false).
	IPClaim string
	// TTL is the token lifetime. Must be positive. Sourced from BTC_SSE_TOKEN_TTL.
	TTL time.Duration
	// SigningSecret is the HMAC-SHA256 key. Must be ≥ 32 bytes.
	// Sourced from BTC_SSE_SIGNING_SECRET — NEVER the JWT access/refresh secrets.
	SigningSecret string
}

// GenerateBitcoinSSEToken signs a short-lived HS256 bitcoin SSE JWT.
//
// Claims embedded: sub=UserID, sid=SID, ip=IPClaim (omitted when empty),
// jti=JTI, iss="store", aud=["bitcoin-sse"], iat=now, exp=now+TTL.
//
// Security invariant: the caller MUST successfully write the session ID to
// Redis at "btc:token:sid:<JTI>" BEFORE calling this function. If that write
// fails the token must not be issued — return 503 without calling this.
//
// All errors are wrapped with telemetry.Token so the fault layer and op are
// set correctly for structured logging and Prometheus classification.
//
// Returns an error if:
//   - SigningSecret is shorter than 32 bytes
//   - TTL is zero or negative
//   - UserID, JTI, or SID is empty (IPClaim may be empty — see BitcoinSSEClaims)
func GenerateBitcoinSSEToken(in BitcoinSSETokenInput) (string, error) {
	if len(in.SigningSecret) < 32 {
		return "", telemetry.Token("GenerateBitcoinSSEToken.validate",
			fmt.Errorf("signing secret must be at least 32 bytes (got %d)", len(in.SigningSecret)))
	}
	if in.TTL <= 0 {
		return "", telemetry.Token("GenerateBitcoinSSEToken.validate",
			fmt.Errorf("TTL must be positive (got %s)", in.TTL))
	}
	if in.UserID == "" {
		return "", telemetry.Token("GenerateBitcoinSSEToken.validate",
			fmt.Errorf("UserID must not be empty"))
	}
	if in.JTI == "" {
		return "", telemetry.Token("GenerateBitcoinSSEToken.validate",
			fmt.Errorf("JTI must not be empty"))
	}
	if in.SID == "" {
		return "", telemetry.Token("GenerateBitcoinSSEToken.validate",
			fmt.Errorf("SID must not be empty"))
	}

	now := time.Now()
	claims := &BitcoinSSEClaims{
		SID:     in.SID,
		IPClaim: in.IPClaim,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   Issuer,
			Subject:  in.UserID,
			Audience: jwt.ClaimStrings{AudienceBitcoinSSE},
			ID:       in.JTI,
			IssuedAt: jwt.NewNumericDate(now),
			// NotBefore intentionally omitted — tokens are valid immediately and
			// the short TTL (≤300s) makes clock-skew tolerance impractical.
			ExpiresAt: jwt.NewNumericDate(now.Add(in.TTL)),
		},
	}

	// Security: sign() validates ExpiresAt is non-zero and secret length ≥ 32.
	// Both are already checked above, but sign() is the authoritative gate.
	tok, err := sign(claims, in.SigningSecret)
	if err != nil {
		return "", telemetry.Token("GenerateBitcoinSSEToken.sign", err)
	}
	return tok, nil
}

// ParseBitcoinSSEToken validates and parses a signed bitcoin SSE JWT.
//
// Validations performed:
//   - Algorithm: must be HS256; "none" and all non-HMAC methods are rejected.
//   - Signature: verified with secret.
//   - Issuer: must be "store".
//   - Audience: must contain "bitcoin-sse" — access and refresh tokens are rejected here.
//   - Expiry: token must not be expired; ExpiresAt claim is required.
//
// Does NOT validate SID or IPClaim — the handler validates those in its guard
// sequence (steps 4 and 5) so it can emit audit events on failure with the
// correct reason code. Validating them here would swallow that context.
//
// Returns an error if secret is empty, the token is malformed, or any
// validation fails. NEVER use ParseAccessToken for SSE tokens — wrong audience.
func ParseBitcoinSSEToken(tokenString, secret string) (*BitcoinSSEClaims, error) {
	if secret == "" {
		return nil, telemetry.Token("ParseBitcoinSSEToken.validate",
			fmt.Errorf("signing secret must not be empty"))
	}
	claims := &BitcoinSSEClaims{}
	_, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(_ *jwt.Token) (any, error) { return []byte(secret), nil },
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(AudienceBitcoinSSE),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}),
	)
	if err != nil {
		return nil, telemetry.Token("ParseBitcoinSSEToken.parse", err)
	}
	return claims, nil
}
