package session

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/google/uuid"
)

// ── JWT claim types ───────────────────────────────────────────────────────────

// refreshClaims is the handler-layer representation of a parsed refresh JWT.
// All UUIDs are pre-parsed to [16]byte so downstream code never operates on raw strings.
type refreshClaims struct {
	JTI       [16]byte
	UserID    [16]byte
	SessionID [16]byte
	FamilyID  [16]byte
}

// accessClaims is the handler-layer representation of a parsed access JWT.
type accessClaims struct {
	JTI       [16]byte
	UserID    [16]byte
	SessionID [16]byte
	ExpiresAt time.Time // populated from the JWT exp claim; used for blocklist TTL
}

// ── Recorder ──────────────────────────────────────────────────────────────────

// Recorder is the narrow observability interface for the session handler.
// *telemetry.Registry satisfies this interface structurally.
type Recorder interface {
	OnTokenRefreshed(clientType string)
	OnLogout()
	OnSessionRevoked()
}

// ── Servicer interface ────────────────────────────────────────────────────────

// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
	RotateRefreshToken(ctx context.Context, jti [16]byte, ip, ua string) (RotatedSession, error)
	Logout(ctx context.Context, in LogoutTxInput) error
}

// ── Handler ───────────────────────────────────────────────────────────────────

// Handler is the HTTP layer for the session feature. It parses requests,
// calls the service, maps sentinel errors to HTTP status codes, and signs JWTs.
// blocklist may be nil; logout proceeds without access-token blocklisting when omitted.
type Handler struct {
	svc       Servicer
	recorder  Recorder
	blocklist kvstore.TokenBlocklist // may be nil
	token.JWTConfig
}

// NewHandler constructs a Handler.
func NewHandler(svc Servicer, cfg token.JWTConfig, blocklist kvstore.TokenBlocklist, recorder Recorder) *Handler {
	return &Handler{svc: svc, JWTConfig: cfg, blocklist: blocklist, recorder: recorder}
}

// ── Refresh ───────────────────────────────────────────────────────────────────

// Refresh handles POST /refresh.
//
// It reads the refresh JWT from the HttpOnly cookie, validates it, and — when
// the token is still valid — issues a new access token and a rotated refresh
// token. The old refresh token is atomically revoked as part of the rotation.
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	cookie, err := r.Cookie(token.RefreshTokenCookie)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "missing_token", "refresh token cookie is missing")
		return
	}

	claims, err := h.parseRefreshToken(cookie.Value)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "invalid_token", "refresh token is invalid or expired")
		return
	}

	// Pass claims.JTI directly as [16]byte — no uuid.UUID(...).String() round-trip (DESIGN 6).
	session, err := h.svc.RotateRefreshToken(r.Context(), claims.JTI, respond.ClientIP(r), r.UserAgent())
	if err == nil {
		result, signErr := token.MintTokens(w, token.MintTokensInput{
			UserID:        claims.UserID,
			SessionID:     claims.SessionID,
			RefreshJTI:    session.NewRefreshJTI,
			FamilyID:      claims.FamilyID,
			RefreshExpiry: session.RefreshExpiry,
		}, h.JWTConfig)
		if signErr != nil {
			log.Error(r.Context(), "Refresh: sign tokens failed", "error", signErr)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		h.recorder.OnTokenRefreshed("web")
		respond.JSON(w, http.StatusOK, result)
		return
	}

	switch {
	case errors.Is(err, authshared.ErrTokenReuseDetected):
		h.recorder.OnSessionRevoked()
		h.clearRefreshCookie(w)
		respond.Error(w, http.StatusUnauthorized, "token_reuse_detected", err.Error())
	case errors.Is(err, authshared.ErrInvalidToken):
		respond.Error(w, http.StatusUnauthorized, "invalid_token", err.Error())
	case errors.Is(err, authshared.ErrAccountLocked):
		h.clearRefreshCookie(w)
		respond.Error(w, http.StatusLocked, "account_locked", err.Error())
	case errors.Is(err, authshared.ErrAccountInactive):
		h.clearRefreshCookie(w)
		respond.Error(w, http.StatusForbidden, "account_inactive", err.Error())
	default:
		log.Error(r.Context(), "Refresh: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// ── Logout ────────────────────────────────────────────────────────────────────

// Logout handles POST /logout.
//
// It revokes the caller's refresh token, ends the session, clears the HttpOnly
// cookie, and adds the access-token JTI to the blocklist so in-flight access
// tokens are immediately invalidated. Always responds 204 No Content with no
// body — even on missing cookie, malformed cookie, or DB error.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	// ── 1. Parse refresh token from cookie ────────────────────────────────────
	cookie, err := r.Cookie(token.RefreshTokenCookie)
	if err != nil {
		// No cookie — nothing to revoke; respond 204.
		respond.NoContent(w)
		return
	}

	claims, err := h.parseRefreshToken(cookie.Value)
	if err != nil {
		// Malformed / expired cookie — clear it and respond 204.
		h.clearRefreshCookie(w)
		respond.NoContent(w)
		return
	}

	// ── 2. Parse access token for blocklist ────────────────────────────────────
	// Best-effort: if absent or expired, logout still proceeds.
	var parsedAccessClaims *accessClaims
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		rawAccess := strings.TrimPrefix(authHeader, "Bearer ")
		if ac, parseErr := h.parseAccessToken(rawAccess); parseErr == nil {
			parsedAccessClaims = ac
		}
	}

	// ── 3. Revoke refresh token, end session, write audit log ─────────────────
	// Logout always returns nil; errors are swallowed internally and logged.
	h.svc.Logout(r.Context(), LogoutTxInput{ //nolint:errcheck — Logout always returns nil
		JTI:       claims.JTI,
		SessionID: claims.SessionID,
		UserID:    claims.UserID,
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})

	// ── 4. Add access token to blocklist ─────────────────────────────────────
	// Security: triple guard — JTI must be non-zero, blocklist must be wired,
	// and the token must not already be expired (remaining TTL > 0). WithoutCancel
	// so a client disconnect cannot abort the blocklist write.
	if parsedAccessClaims != nil && parsedAccessClaims.JTI != ([16]byte{}) && h.blocklist != nil && h.JWTAccessSecret != "" {
		ttl := time.Until(parsedAccessClaims.ExpiresAt)
		if ttl > 0 {
			if blErr := h.blocklist.BlockToken(
				context.WithoutCancel(r.Context()),
				uuid.UUID(parsedAccessClaims.JTI).String(),
				ttl,
			); blErr != nil {
				log.Warn(r.Context(), "Logout: blocklist.BlockToken failed", "error", blErr)
			}
		}
	}

	h.recorder.OnLogout()
	h.clearRefreshCookie(w)
	respond.NoContent(w)
}

// ── private helpers ───────────────────────────────────────────────────────────

// parseRefreshToken validates and parses a signed refresh JWT, returning
// handler-local claims with all UUIDs pre-converted to [16]byte.
func (h *Handler) parseRefreshToken(tokenString string) (*refreshClaims, error) {
	c, err := token.ParseRefreshToken(tokenString, h.JWTRefreshSecret)
	if err != nil {
		return nil, err
	}
	jti, err := uuid.Parse(c.ID)
	if err != nil {
		return nil, telemetry.Handler("parseRefreshToken.jti", err)
	}
	userID, err := uuid.Parse(c.Subject)
	if err != nil {
		return nil, telemetry.Handler("parseRefreshToken.sub", err)
	}
	sessionID, err := uuid.Parse(c.SessionID)
	if err != nil {
		return nil, telemetry.Handler("parseRefreshToken.sid", err)
	}
	familyID, err := uuid.Parse(c.FamilyID)
	if err != nil {
		return nil, telemetry.Handler("parseRefreshToken.fid", err)
	}
	return &refreshClaims{
		JTI:       [16]byte(jti),
		UserID:    [16]byte(userID),
		SessionID: [16]byte(sessionID),
		FamilyID:  [16]byte(familyID),
	}, nil
}

// parseAccessToken validates and parses a signed access JWT, returning
// handler-local claims with all UUIDs pre-converted to [16]byte.
func (h *Handler) parseAccessToken(tokenString string) (*accessClaims, error) {
	c, err := token.ParseAccessToken(tokenString, h.JWTAccessSecret)
	if err != nil {
		return nil, err
	}
	jti, err := uuid.Parse(c.ID)
	if err != nil {
		return nil, telemetry.Handler("parseAccessToken.jti", err)
	}
	userID, err := uuid.Parse(c.Subject)
	if err != nil {
		return nil, telemetry.Handler("parseAccessToken.sub", err)
	}
	sessionID, err := uuid.Parse(c.SessionID)
	if err != nil {
		return nil, telemetry.Handler("parseAccessToken.sid", err)
	}
	return &accessClaims{
		JTI:       [16]byte(jti),
		UserID:    [16]byte(userID),
		SessionID: [16]byte(sessionID),
		ExpiresAt: c.ExpiresAt.Time, // jwt.RegisteredClaims.ExpiresAt is *jwt.NumericDate; .Time is time.Time
	}, nil
}

// clearRefreshCookie immediately expires the refresh-token cookie.
func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	token.ClearRefreshCookie(w, h.SecureCookies)
}
