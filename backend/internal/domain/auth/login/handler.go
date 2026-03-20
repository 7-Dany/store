package login

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// Recorder is the narrow observability interface for the login handler.
// *telemetry.Registry satisfies this interface structurally.
type Recorder interface {
	OnLoginSuccess(provider string)
	OnLoginFailed(provider string, reason string)
}

// Servicer is the subset of the service that the handler requires.
type Servicer interface {
	Login(ctx context.Context, in LoginInput) (LoggedInSession, error)
}

// Handler is the HTTP layer for the login feature.
type Handler struct {
	svc      Servicer
	recorder Recorder
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
func NewHandler(svc Servicer, cfg token.JWTConfig, recorder Recorder) *Handler {
	if len(cfg.JWTAccessSecret) < 32 {
		panic("login.NewHandler: JWTAccessSecret must be at least 32 bytes")
	}
	if len(cfg.JWTRefreshSecret) < 32 {
		panic("login.NewHandler: JWTRefreshSecret must be at least 32 bytes")
	}
	return &Handler{svc: svc, JWTConfig: cfg, recorder: recorder}
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
			log.Error(r.Context(), "Login: sign tokens failed", "error", signErr)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		h.recorder.OnLoginSuccess("email")
		respond.JSON(w, http.StatusOK, loginResponse{
			TokenResult:         result,
			ScheduledDeletionAt: session.ScheduledDeletionAt,
		})
		return
	}

	switch {
	case errors.Is(err, authshared.ErrInvalidCredentials):
		h.recorder.OnLoginFailed("email", authshared.LoginReasonInvalidCredentials)
		respond.Error(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
	case errors.Is(err, authshared.ErrEmailNotVerified):
		h.recorder.OnLoginFailed("email", authshared.LoginReasonEmailUnverified)
		respond.Error(w, http.StatusForbidden, "email_not_verified", err.Error())
	case errors.Is(err, authshared.ErrAccountInactive):
		h.recorder.OnLoginFailed("email", authshared.LoginReasonAccountInactive)
		respond.Error(w, http.StatusForbidden, "account_inactive", err.Error())
	case errors.Is(err, authshared.ErrAdminLocked):
		// Admin-imposed lock: 423 with a distinct code so clients know the OTP
		// self-unlock flow will not work and the user must contact support.
		h.recorder.OnLoginFailed("email", authshared.LoginReasonAccountLocked)
		respond.Error(w, http.StatusLocked, "admin_locked", err.Error())
	case errors.Is(err, authshared.ErrAccountLocked):
		// OTP brute-force lock: 423 so clients prompt the user to use the
		// self-unlock flow.
		h.recorder.OnLoginFailed("email", authshared.LoginReasonAccountLocked)
		respond.Error(w, http.StatusLocked, "account_locked", err.Error())
	case errors.Is(err, authshared.ErrLoginLocked):
		// Security: only *LoginLockedError carries RetryAfter. The plain
		// ErrLoginLocked sentinel (e.g. from a test double) emits 429
		// without Retry-After — callers should always return the typed error.
		h.recorder.OnLoginFailed("email", authshared.LoginReasonRateLimit)
		var lle *authshared.LoginLockedError
		if errors.As(err, &lle) {
			secs := max(int(lle.RetryAfter.Seconds()), 1)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
		}
		respond.Error(w, http.StatusTooManyRequests, "login_locked", err.Error())
	default:
		log.Error(r.Context(), "Login: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
