package password

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// ── userBlocklist ─────────────────────────────────────────────────────────────────────────────

// userBlocklist implements the handler's Blocklist interface by writing a
// per-user block marker to the KV store. The TTL matches the access-token
// lifetime so the entry expires automatically once no valid tokens remain.
//
// The auth middleware must check for the "pr_blocked_user:" key and
// reject the request if it is present.
type userBlocklist struct {
	store kvstore.Store
	ttl   time.Duration
}

func (b *userBlocklist) Block(ctx context.Context, userID string) error {
	if b.store == nil {
		return nil
	}
	// Store the block Unix timestamp instead of a static "1" so the middleware
	// can compare it against the token's iat claim. Tokens minted after the
	// reset (fresh logins) will have iat > blockTime and are allowed through;
	// tokens issued before the reset remain blocked for the full TTL window.
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	return b.store.Set(ctx, "pr_blocked_user:"+userID, ts, b.ttl)
}

// ── Recorder ──────────────────────────────────────────────────────────────────

// Recorder is the narrow observability interface for the password handler.
// *telemetry.Registry satisfies this interface structurally.
type Recorder interface {
	OnPasswordResetRequested()
	OnPasswordResetCompleted()
	OnPasswordChanged()
}

// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
	RequestPasswordReset(ctx context.Context, in ForgotPasswordInput) (authshared.OTPIssuanceResult, error)
	// VerifyResetCode validates the OTP without consuming it and returns the email
	// on success so the handler can issue a grant token.
	VerifyResetCode(ctx context.Context, in VerifyResetCodeInput) (string, error)
	// ConsumePasswordResetToken returns the affected user's ID on success so the
	// handler can immediately invalidate outstanding access tokens.
	ConsumePasswordResetToken(ctx context.Context, in ResetPasswordInput) ([16]byte, error)
	// UpdatePasswordHash verifies the caller's current password and replaces it.
	UpdatePasswordHash(ctx context.Context, in ChangePasswordInput) error
}

// Blocklist immediately revokes outstanding access tokens for a user after a
// password reset. The concrete implementation writes a per-user block marker
// to the KV store that the auth middleware checks on every authenticated request.
//
// A nil Blocklist is treated as a no-op: revocation is best-effort and must
// never prevent the user from learning that their password was changed.
type Blocklist interface {
	Block(ctx context.Context, userID string) error
}

// JTIBlocklist immediately revokes a single access token by JTI after a
// password change. A nil JTIBlocklist silently skips invalidation — revocation
// is best-effort and must never prevent the response from being sent.
type JTIBlocklist interface {
	BlockToken(ctx context.Context, jti string, ttl time.Duration) error
}

// Handler is the HTTP layer for the password reset flow. It parses requests,
// calls the service, maps sentinel errors to HTTP status codes, and enqueues
// or synchronously delivers OTP emails.
type Handler struct {
	svc           Servicer
	recorder      Recorder
	blocklist     Blocklist    // per-user block for reset-password (may be nil)
	jtiBlocklist  JTIBlocklist // per-JTI block for change-password (may be nil)
	accessTTL     time.Duration
	secureCookies bool
	grantStore    kvstore.Store // KV store for reset grant tokens (may be nil)
	grantTTL      time.Duration // TTL for grant tokens; default 10 minutes
	mailer.OTPHandlerBase
}

// NewHandler constructs a Handler with the given dependencies.
//
// When base.Queue is non-nil, mail jobs are enqueued asynchronously via the queue.
// When base.Queue is nil, base.Send is called synchronously —
// the preferred path in tests.
//
// blocklist may be nil; token revocation is best-effort and a nil value simply
// skips the block call without affecting the HTTP response.
//
// jtiBlocklist may be nil; access-token invalidation on change-password is
// best-effort and a nil value silently skips the BlockToken call.
func NewHandler(
	svc           Servicer,
	base          mailer.OTPHandlerBase,
	blocklist     Blocklist,
	jtiBlocklist  JTIBlocklist,
	accessTTL     time.Duration,
	secureCookies bool,
	grantStore    kvstore.Store,
	grantTTL      time.Duration,
	recorder      Recorder,
) *Handler {
	return &Handler{
		svc:           svc,
		OTPHandlerBase: base,
		blocklist:     blocklist,
		jtiBlocklist:  jtiBlocklist,
		accessTTL:     accessTTL,
		secureCookies: secureCookies,
		grantStore:    grantStore,
		grantTTL:      grantTTL,
		recorder:      recorder,
	}
}

// ── ForgotPassword ────────────────────────────────────────────────────────────

// ForgotPassword handles POST /forgot-password.
//
// Anti-enumeration invariant: this handler always returns 202 Accepted on
// every exit path — including service errors and mail delivery failures.
// A non-202 response would reveal whether the email is registered.
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	req, ok := respond.DecodeJSON[forgotPasswordRequest](w, r)
	if !ok {
		return
	}

	if err := validateForgotPasswordRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	result, err := h.svc.RequestPasswordReset(r.Context(), ForgotPasswordInput{
		Email:     req.Email,
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		// Do not expose service errors — anti-enumeration requires uniform 202.
		log.Error(r.Context(), "ForgotPassword: service error", "error", err)
		respond.JSON(w, http.StatusAccepted, map[string]string{
			"message": "if that email is registered and verified, a password reset code has been sent",
		})
		return
	}

	// SendOTPEmail is a no-op when result.RawCode is empty (anti-enumeration path).
	if result.RawCode == "" {
		log.Debug(r.Context(), "ForgotPassword: suppressed — service returned empty code (anti-enumeration)")
	} else {
		log.Debug(r.Context(), "ForgotPassword: dispatching OTP email", "email", result.Email)
	}
	if err := mailer.SendOTPEmail(r.Context(), h.OTPHandlerBase, result.UserID, result.Email, result.RawCode, "password"); err != nil {
		// Security: do not surface mail failure — any non-202 response here
		// reveals that the email is registered and verified (anti-enumeration).
		log.Warn(r.Context(), "ForgotPassword: mail delivery failed", "error", err, "email", result.Email)
	} else if result.RawCode != "" {
		h.recorder.OnPasswordResetRequested()
		log.Info(r.Context(), "ForgotPassword: OTP email dispatched", "email", result.Email)
	}

	respond.JSON(w, http.StatusAccepted, map[string]string{
		"message": "if that email is registered and verified, a password reset code has been sent",
	})
}

// ── VerifyResetCode ─────────────────────────────────────────────────────────────────

// VerifyResetCode handles POST /verify-reset-code.
// Validates the OTP without consuming it and issues a short-lived grant token
// that the client presents to POST /reset-password.
func (h *Handler) VerifyResetCode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	req, ok := respond.DecodeJSON[verifyResetCodeRequest](w, r)
	if !ok {
		return
	}

	if err := validateVerifyResetCodeRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	email, err := h.svc.VerifyResetCode(r.Context(), VerifyResetCodeInput{
		Email:     req.Email,
		Code:      req.Code,
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		switch {
		case errors.Is(err, authshared.ErrTokenExpired):
			respond.Error(w, http.StatusGone, "token_expired", err.Error())
		case errors.Is(err, authshared.ErrTooManyAttempts):
			respond.Error(w, http.StatusTooManyRequests, "too_many_attempts", err.Error())
		case errors.Is(err, authshared.ErrTokenNotFound),
			errors.Is(err, authshared.ErrTokenAlreadyUsed),
			errors.Is(err, authshared.ErrInvalidCode):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "invalid reset code")
		default:
			log.Error(r.Context(), "VerifyResetCode: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	if h.grantStore == nil {
		log.Error(r.Context(), "VerifyResetCode: grantStore is nil")
		respond.Error(w, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
		return
	}

	grantToken := uuid.New().String()
	// Security: WithoutCancel so a client disconnect cannot skip writing the grant
	// token — without it, the OTP would be verified but no token issued.
	if err := h.grantStore.Set(
		context.WithoutCancel(r.Context()),
		"prc:"+grantToken,
		email+"\n"+req.Code,
		h.grantTTL,
	); err != nil {
		log.Error(r.Context(), "VerifyResetCode: store grant token failed", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	respond.JSON(w, http.StatusOK, verifyResetCodeResponse{
		ResetToken: grantToken,
		ExpiresIn:  int(h.grantTTL.Seconds()),
	})
}

// ── ResetPassword ─────────────────────────────────────────────────────────────

// ResetPassword handles POST /reset-password.
//
// Security: outstanding access tokens are immediately invalidated via the
// blocklist after a successful reset.
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	req, ok := respond.DecodeJSON[resetPasswordRequest](w, r)
	if !ok {
		return
	}

	if err := validateResetPasswordRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	if h.grantStore == nil {
		log.Error(r.Context(), "ResetPassword: grantStore is nil")
		respond.Error(w, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
		return
	}

	// Look up grant token (single-use, issued by POST /verify-reset-code).
	val, grantErr := h.grantStore.Get(r.Context(), "prc:"+req.ResetToken)
	if grantErr != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "invalid or expired reset token")
		return
	}

	// Parse value: "<email>\n<rawCode>".
	parts := strings.SplitN(val, "\n", 2)
	if len(parts) != 2 {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "invalid or expired reset token")
		return
	}
	grantEmail, rawCode := parts[0], parts[1]

	// Security: WithoutCancel so a client disconnect cannot skip deletion and
	// leave a single-use grant token reusable.
	_ = h.grantStore.Delete(context.WithoutCancel(r.Context()), "prc:"+req.ResetToken)

	userID, err := h.svc.ConsumePasswordResetToken(r.Context(), ResetPasswordInput{
		Email:       grantEmail,
		Code:        rawCode,
		NewPassword: req.NewPassword,
		IPAddress:   respond.ClientIP(r),
		UserAgent:   r.UserAgent(),
	})
	if err == nil {
		if h.blocklist != nil {
			uid := uuid.UUID(userID).String()
			// Security: WithoutCancel so a client disconnect cannot skip blocklist
			// revocation and leave outstanding access tokens valid after a reset.
			if blErr := h.blocklist.Block(context.WithoutCancel(r.Context()), uid); blErr != nil {
				log.Warn(r.Context(), "ResetPassword: blocklist.Block failed", "error", blErr, "user_id", uid)
			}
		}
		h.recorder.OnPasswordResetCompleted()
		respond.JSON(w, http.StatusOK, map[string]string{"message": "password reset successfully"})
		return
	}

	switch {
	case authshared.IsPasswordStrengthError(err):
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
	case errors.Is(err, authshared.ErrTokenExpired):
		respond.Error(w, http.StatusGone, "token_expired", err.Error())
	case errors.Is(err, authshared.ErrTokenNotFound),
		errors.Is(err, authshared.ErrTokenAlreadyUsed),
		errors.Is(err, authshared.ErrInvalidCode):
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
	case errors.Is(err, authshared.ErrTooManyAttempts):
		respond.Error(w, http.StatusTooManyRequests, "too_many_attempts", err.Error())
	case errors.Is(err, ErrSamePassword):
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
	default:
		log.Error(r.Context(), "ResetPassword: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// ── ChangePassword ────────────────────────────────────────────────────────────

// ChangePassword handles POST /change-password.
// On success, clears the refresh cookie — all sessions are revoked by the service.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	req, ok := respond.DecodeJSON[changePasswordRequest](w, r)
	if !ok {
		return
	}

	if err := validateChangePasswordRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	err := h.svc.UpdatePasswordHash(r.Context(), ChangePasswordInput{
		UserID:      userID,
		OldPassword: req.OldPassword,
		NewPassword: req.NewPassword,
		IPAddress:   respond.ClientIP(r),
		UserAgent:   r.UserAgent(),
	})
	if err != nil {
		switch {
		case errors.Is(err, authshared.ErrInvalidCredentials):
			respond.Error(w, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect")
		case errors.Is(err, authshared.ErrTooManyAttempts):
			respond.Error(w, http.StatusTooManyRequests, "too_many_attempts",
				"too many incorrect password attempts — use forgot-password to reset your password")
		case errors.Is(err, ErrSamePassword):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		case authshared.IsPasswordStrengthError(err):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		case errors.Is(err, authshared.ErrUserNotFound):
			respond.Error(w, http.StatusNotFound, "not_found", "user not found")
		default:
			log.Error(r.Context(), "ChangePassword: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	// Clear the refresh token cookie — UpdatePasswordHashTx ended all sessions.
	token.ClearRefreshCookie(w, h.secureCookies)

	// Security: invalidate the current access token so it cannot be reused
	// for the full TTL after the password has changed.
	if h.jtiBlocklist != nil {
		if jti, ok := token.JTIFromContext(r.Context()); ok && jti != "" {
			if err := h.jtiBlocklist.BlockToken(context.WithoutCancel(r.Context()), jti, h.accessTTL); err != nil {
				log.Error(r.Context(), "ChangePassword: blocklist current access token failed", "error", err)
				// Non-fatal: the refresh cookie was cleared and all sessions are revoked.
			}
		}
	}

	h.recorder.OnPasswordChanged()
	respond.JSON(w, http.StatusOK, map[string]string{"message": "password changed successfully"})
}

// ── private helpers ───────────────────────────────────────────────────────────

// mustUserID extracts the authenticated user ID from the request context.
// If absent or empty it writes a 401 and returns ("", false) so the caller
// can return immediately.
func (h *Handler) mustUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "missing user id in context")
		return "", false
	}
	return userID, true
}
