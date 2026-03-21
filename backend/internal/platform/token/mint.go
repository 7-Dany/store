package token

import (
	"fmt"
	"net/http"
	"reflect"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// sign signs any jwt.Claims implementation with HS256 using the given secret.
// It is intentionally unexported — all signing must go through the named
// constructors (GenerateAccessToken, GenerateRefreshToken) which enforce the
// correct claims shape and field values.
//
// sign returns plain fmt errors; callers are responsible for wrapping with
// telemetry.Token so the fault layer and op label are correct.
//
// Returns an error if:
//   - claims is nil (nil interface or non-nil interface wrapping a nil pointer)
//   - secret is shorter than 32 bytes
//   - claims.GetExpirationTime() returns nil or zero (eternal tokens are forbidden)
func sign(claims jwt.Claims, secret string) (string, error) {
	if claims == nil {
		return "", fmt.Errorf("claims must not be nil")
	}
	if len(secret) < 32 {
		return "", fmt.Errorf("signing secret must be at least 32 bytes (got %d)", len(secret))
	}

	// F-09: a non-nil interface value may wrap a nil pointer. Calling any method
	// on such a value (including GetExpirationTime below) panics. Detect and
	// reject it before any method is invoked.
	rv := reflect.ValueOf(claims)
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return "", fmt.Errorf("claims pointer must not be nil")
	}

	// F-03 / F-15: use the jwt.Claims interface method instead of reflect field
	// traversal. Works for ALL claims types, eliminates the mutable package-level
	// registeredClaimsType var, and is immune to depth > 1 embedding.
	exp, err := claims.GetExpirationTime()
	if err != nil {
		return "", fmt.Errorf("could not read ExpiresAt: %w", err)
	}
	if exp == nil || exp.IsZero() {
		return "", fmt.Errorf("ExpiresAt must be set and non-zero (eternal tokens are forbidden)")
	}

	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// F-07: handle the error explicitly. For HS256 + []byte key this path is
	// unreachable, but a future algorithm change must not silently emit "".
	signed, err := t.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("SignedString failed: %w", err)
	}
	return signed, nil
}

// GenerateAccessToken signs a short-lived HS256 access JWT.
//
// Claims embedded: sub=userID, sid=sessionID, jti=UUID v7, iss="store",
// aud=["store:access"], iat=now, exp=now+ttl.
//
// All errors are wrapped with telemetry.Token so the fault layer and op are
// set correctly for structured logging and Prometheus classification.
//
// Returns an error if:
//   - secret is shorter than 32 bytes
//   - ttl is zero, negative, or exceeds maxAccessTTL (24h)
//   - userID or sessionID is empty
//   - UUID v7 generation fails
func GenerateAccessToken(userID, sessionID string, ttl time.Duration, secret string) (string, error) {
	if len(secret) < 32 {
		return "", telemetry.Token("GenerateAccessToken.validate",
			fmt.Errorf("signing secret must be at least 32 bytes (got %d)", len(secret)))
	}
	if ttl <= 0 {
		return "", telemetry.Token("GenerateAccessToken.validate",
			fmt.Errorf("ttl must be positive (got %s)", ttl))
	}
	// F-12: enforce an upper bound. Access tokens are not server-side revocable;
	// a misconfigured env var (e.g. ACCESS_TTL=8760h) would produce near-eternal
	// tokens with no way to revoke them.
	if ttl > maxAccessTTL {
		return "", telemetry.Token("GenerateAccessToken.validate",
			fmt.Errorf("ttl %s exceeds maximum allowed %s", ttl, maxAccessTTL))
	}
	// F-14: reject empty identifiers. An empty sub produces a useless token;
	// an empty sid breaks session-scoped revocation.
	if userID == "" {
		return "", telemetry.Token("GenerateAccessToken.validate",
			fmt.Errorf("userID must not be empty"))
	}
	if sessionID == "" {
		return "", telemetry.Token("GenerateAccessToken.validate",
			fmt.Errorf("sessionID must not be empty"))
	}

	// F-04: replace uuid.Must (panics on entropy failure) with explicit error
	// handling. uuid.NewV7 calls crypto/rand internally; a failure here should
	// produce a 500, not crash the process.
	jti, err := uuid.NewV7()
	if err != nil {
		return "", telemetry.Token("GenerateAccessToken.jti",
			fmt.Errorf("failed to generate jti: %w", err))
	}

	now := time.Now()
	claims := AccessClaims{
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   Issuer,
			Subject:  userID,
			Audience: jwt.ClaimStrings{AudienceAccess},
			// JTI is UUID v7 (time-ordered). Access and refresh tokens share the
			// same blocklist namespace; the timestamp prefix keeps blocklist keys
			// naturally ordered by issuance time. If prefixes are ever added here,
			// BlockToken and IsTokenBlocked call sites must be updated simultaneously.
			ID:       jti.String(),
			IssuedAt: jwt.NewNumericDate(now),
			// NotBefore intentionally omitted: tokens are valid immediately on
			// issuance and the short TTL makes nbf clock-skew tolerance impractical.
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	// F-10: route through sign() so all guards apply consistently.
	tok, err := sign(claims, secret)
	if err != nil {
		return "", telemetry.Token("GenerateAccessToken.sign", err)
	}
	return tok, nil
}

// GenerateRefreshToken signs a long-lived HS256 refresh JWT.
//
// Claims embedded: sub=userID, jti=refreshJTI (DB row PK), fid=familyID,
// sid=sessionID, iss="store", aud=["store:refresh"], iat=now,
// exp=expiresAt (copied from the DB row so they stay in sync).
//
// All errors are wrapped with telemetry.Token so the fault layer and op are
// set correctly for structured logging and Prometheus classification.
//
// Returns an error if:
//   - secret is shorter than 32 bytes
//   - expiresAt is not in the future
//   - any identifier (userID, sessionID, refreshJTI, familyID) is empty
func GenerateRefreshToken(userID, sessionID, refreshJTI, familyID string, expiresAt time.Time, secret string) (string, error) {
	if len(secret) < 32 {
		return "", telemetry.Token("GenerateRefreshToken.validate",
			fmt.Errorf("signing secret must be at least 32 bytes (got %d)", len(secret)))
	}
	// F-08: capture time.Now() once so the expiry validation and iat claim use
	// the same instant. Previously two separate time.Now() calls produced a
	// subtle TOCTOU where iat could be marginally later than the validation instant.
	now := time.Now()
	if !expiresAt.After(now) {
		return "", telemetry.Token("GenerateRefreshToken.validate",
			fmt.Errorf("expiresAt must be in the future"))
	}
	// F-14: reject empty identifiers. An empty jti would break blocklist lookups
	// since empty string may collide or be silently skipped by the blocklist impl.
	if userID == "" {
		return "", telemetry.Token("GenerateRefreshToken.validate",
			fmt.Errorf("userID must not be empty"))
	}
	if sessionID == "" {
		return "", telemetry.Token("GenerateRefreshToken.validate",
			fmt.Errorf("sessionID must not be empty"))
	}
	if refreshJTI == "" {
		return "", telemetry.Token("GenerateRefreshToken.validate",
			fmt.Errorf("refreshJTI must not be empty"))
	}
	if familyID == "" {
		return "", telemetry.Token("GenerateRefreshToken.validate",
			fmt.Errorf("familyID must not be empty"))
	}

	claims := RefreshClaims{
		FamilyID:  familyID,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   Issuer,
			Subject:  userID,
			Audience: jwt.ClaimStrings{AudienceRefresh},
			ID:       refreshJTI,
			IssuedAt: jwt.NewNumericDate(now),
			// NotBefore intentionally omitted (same rationale as GenerateAccessToken).
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	// F-10: route through sign().
	tok, err := sign(claims, secret)
	if err != nil {
		return "", telemetry.Token("GenerateRefreshToken.sign", err)
	}
	return tok, nil
}

// MintTokensInput carries the identifiers needed to sign a new access + refresh
// token pair. Produced by login.LoggedInSession or session.RotatedSession at
// the handler layer.
//
// All [16]byte fields must be non-zero UUIDs; MintTokens rejects zero values
// (F-11) because a zero UserID would produce a token with
// sub="00000000-0000-0000-0000-000000000000" which could match a sentinel row
// in the database and grant unintended access.
type MintTokensInput struct {
	UserID        [16]byte
	SessionID     [16]byte
	RefreshJTI    [16]byte
	FamilyID      [16]byte
	RefreshExpiry time.Time
}

// TokenResult is returned by any handler method that mints a new access/refresh
// token pair. Used by the login and session handlers.
//
// RefreshToken is excluded from JSON serialisation (json:"-") — the refresh
// token travels only via the HttpOnly cookie set by MintTokens (F-02).
// Returning it in the response body would expose it to JavaScript running in
// the browser, defeating the purpose of the HttpOnly flag.
type TokenResult struct {
	AccessToken   string    `json:"access_token"`
	RefreshToken  string    `json:"-"` // F-02: cookie-only — never serialised to JSON
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
//
// All errors are wrapped with telemetry.Token so that fault attribution in
// structured logs and Prometheus metrics reflects the token layer, not the
// handler layer — MintTokens is a token-platform function, not a domain handler.
func MintTokens(
	w http.ResponseWriter,
	in MintTokensInput,
	cfg JWTConfig,
) (TokenResult, error) {
	// F-11: reject zero-value UUIDs before any signing attempt.
	zeroUUID := [16]byte{}
	if in.UserID == zeroUUID {
		return TokenResult{}, telemetry.Token("MintTokens.validate",
			fmt.Errorf("UserID must not be zero"))
	}
	if in.SessionID == zeroUUID {
		return TokenResult{}, telemetry.Token("MintTokens.validate",
			fmt.Errorf("SessionID must not be zero"))
	}
	if in.RefreshJTI == zeroUUID {
		return TokenResult{}, telemetry.Token("MintTokens.validate",
			fmt.Errorf("RefreshJTI must not be zero"))
	}
	if in.FamilyID == zeroUUID {
		return TokenResult{}, telemetry.Token("MintTokens.validate",
			fmt.Errorf("FamilyID must not be zero"))
	}

	accessToken, err := GenerateAccessToken(
		uuid.UUID(in.UserID).String(),
		uuid.UUID(in.SessionID).String(),
		cfg.AccessTTL,
		cfg.JWTAccessSecret,
	)
	if err != nil {
		// GenerateAccessToken already returns a telemetry.Token-wrapped error.
		// Add a MintTokens op label on top so the call site is traceable in logs.
		return TokenResult{}, telemetry.Token("MintTokens.sign_access_token", err)
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
		return TokenResult{}, telemetry.Token("MintTokens.sign_refresh_token", err)
	}

	SetRefreshCookie(w, refreshToken, in.RefreshExpiry, cfg.SecureCookies)

	return TokenResult{
		AccessToken:   accessToken,
		RefreshToken:  refreshToken, // populated for callers; excluded from JSON by json:"-"
		RefreshExpiry: in.RefreshExpiry,
		ExpiresIn:     int(cfg.AccessTTL.Seconds()),
	}, nil
}
