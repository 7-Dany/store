package login

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// Servicer is the subset of the service that the handler requires.
type Servicer interface {
	Login(ctx context.Context, in LoginInput) (LoggedInSession, error)
}

// Handler is the HTTP layer for the login feature.
type Handler struct {
	svc Servicer
	token.JWTConfig
}

// NewHandler constructs a Handler.
//
// Panics if cfg.JWTAccessSecret or cfg.JWTRefreshSecret is shorter than 32 bytes.
// Both token.GenerateAccessToken and token.GenerateRefreshToken enforce this
// minimum, so a short secret would cause every post-login token mint to fail
// after LoginTx has already committed (ADR-001). Catching it at construction
// time surfaces the misconfiguration at startup rather than at first login.
//
// cfg.SecureCookies should be true in production (HTTPS) and false for local HTTP
// development. The refresh cookie's Secure flag is set accordingly.
func NewHandler(svc Servicer, cfg token.JWTConfig) *Handler {
	if len(cfg.JWTAccessSecret) < 32 {
		panic("login.NewHandler: JWTAccessSecret must be at least 32 bytes")
	}
	if len(cfg.JWTRefreshSecret) < 32 {
		panic("login.NewHandler: JWTRefreshSecret must be at least 32 bytes")
	}
	return &Handler{svc: svc, JWTConfig: cfg}
}

// Login handles POST /login.
//
// On success it mints an access token and a refresh token. The access token is
// returned in the JSON body so JS clients can store it in memory. The refresh
// token is set as an HttpOnly cookie so it is not accessible from JavaScript.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	req, ok := respond.DecodeJSON[loginRequest](w, r)
	if !ok {
		return
	}

	if err := validateLoginRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	session, err := h.svc.Login(r.Context(), LoginInput{
		Identifier: req.Identifier,
		Password:   req.Password,
		IPAddress:  respond.ClientIP(r),
		UserAgent:  r.UserAgent(),
	})
	if err == nil {
		result, signErr := token.MintTokens(w, token.MintTokensInput{
			UserID:        session.UserID,
			SessionID:     session.SessionID,
			RefreshJTI:    session.RefreshJTI,
			FamilyID:      session.FamilyID,
			RefreshExpiry: session.RefreshExpiry,
		}, h.JWTConfig)
		if signErr != nil {
			slog.ErrorContext(r.Context(), "login.Login: sign tokens", "error", signErr)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		respond.JSON(w, http.StatusOK, result)
		return
	}

	switch {
	case errors.Is(err, authshared.ErrInvalidCredentials):
		respond.Error(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
	case errors.Is(err, authshared.ErrEmailNotVerified):
		respond.Error(w, http.StatusForbidden, "email_not_verified", err.Error())
	case errors.Is(err, authshared.ErrAccountInactive):
		respond.Error(w, http.StatusForbidden, "account_inactive", err.Error())
	case errors.Is(err, authshared.ErrAccountLocked):
		// Security: 423 Locked distinguishes admin-enforced locks from
		// time-based lockouts (429), enabling clients to show the correct UI.
		respond.Error(w, http.StatusLocked, "account_locked", err.Error())
	case errors.Is(err, authshared.ErrLoginLocked):
		// Security: only *LoginLockedError carries RetryAfter. The plain
		// ErrLoginLocked sentinel (e.g. from a test double) emits 429
		// without Retry-After — callers should always return the typed error.
		var lle *authshared.LoginLockedError
		if errors.As(err, &lle) {
			secs := max(int(lle.RetryAfter.Seconds()), 1)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
		}
		respond.Error(w, http.StatusTooManyRequests, "login_locked", err.Error())
	default:
		slog.ErrorContext(r.Context(), "login.Login: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
