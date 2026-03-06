package verification

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	"github.com/7-Dany/store/backend/internal/platform/respond"
)

// BackoffChecker is the subset of ratelimit.BackoffLimiter used by Handler.
// Defined here so the verification package does not import internal/platform/ratelimit.
// All methods accept a context so callers can use context.WithoutCancel for
// security-critical failure recording.
type BackoffChecker interface {
	Allow(ctx context.Context, key string) (ok bool, remaining time.Duration)
	RecordFailure(ctx context.Context, key string) time.Duration
	Reset(ctx context.Context, key string)
}

// Servicer defines the service methods Handler needs.
type Servicer interface {
	VerifyEmail(ctx context.Context, in VerifyEmailInput) error
	ResendVerification(ctx context.Context, in ResendInput) (authshared.OTPIssuanceResult, error)
}

// Handler is the HTTP layer for verification operations.
type Handler struct {
	svc           Servicer
	verifyBackoff BackoffChecker
	mailer.OTPHandlerBase
}

// NewHandler constructs a Handler.
func NewHandler(svc Servicer, backoff BackoffChecker, base mailer.OTPHandlerBase) *Handler {
	return &Handler{svc: svc, verifyBackoff: backoff, OTPHandlerBase: base}
}

// ── VerifyEmail ───────────────────────────────────────────────────────────────

// VerifyEmail handles POST /verify-email.
func (h *Handler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	// Backoff gate: reject the request immediately if the IP is still in a
	// backoff window from prior failed attempts.
	ip := respond.ClientIP(r)
	if ok, remaining := h.verifyBackoff.Allow(r.Context(), ip); !ok {
		secs := int64(math.Ceil(remaining.Seconds()))
		w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
		respond.Error(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed attempts — please wait before retrying")
		return
	}

	req, ok := respond.DecodeJSON[verifyEmailRequest](w, r)
	if !ok {
		return
	}

	if err := validateVerifyEmailRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	err := h.svc.VerifyEmail(r.Context(), VerifyEmailInput{
		Email:     req.Email,
		Code:      req.Code,
		IPAddress: ip,
		UserAgent: r.UserAgent(),
	})

	if err == nil {
		// Success — clear any backoff state for this IP.
		h.verifyBackoff.Reset(r.Context(), ip)
		respond.JSON(w, http.StatusOK, map[string]string{
			"message": "email verified successfully",
		})
		return
	}

	switch {
	case errors.Is(err, authshared.ErrInvalidCode):
		// Proven wrong-code submission — record backoff penalty.
		// Security: WithoutCancel so a client disconnect cannot abort the write.
		h.verifyBackoff.RecordFailure(context.WithoutCancel(r.Context()), ip)
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())

	case errors.Is(err, authshared.ErrTokenNotFound),
		errors.Is(err, authshared.ErrTokenExpired),
		errors.Is(err, authshared.ErrTokenAlreadyUsed):
		// Operational failures — not OTP-guessing, so no backoff penalty.
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())

	case errors.Is(err, authshared.ErrTooManyAttempts):
		// Security: WithoutCancel so a client disconnect cannot abort the backoff
		// penalty write and allow the attacker to reset their OTP attempt window.
		h.verifyBackoff.RecordFailure(context.WithoutCancel(r.Context()), ip)
		respond.Error(w, http.StatusTooManyRequests, "too_many_attempts", err.Error())

	case errors.Is(err, authshared.ErrAccountLocked):
		respond.Error(w, http.StatusLocked, "account_locked", err.Error())

	case errors.Is(err, authshared.ErrAlreadyVerified):
		// Idempotent — treat as success, clear any outstanding backoff.
		h.verifyBackoff.Reset(r.Context(), ip)
		respond.JSON(w, http.StatusOK, map[string]string{
			"message": "email already verified",
		})

	default:
		slog.ErrorContext(r.Context(), "verification.VerifyEmail: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// ── ResendVerification ────────────────────────────────────────────────────────

// ResendVerification handles POST /resend-verification.
// Always returns 202 regardless of whether an email was actually sent
// (anti-enumeration invariant).
func (h *Handler) ResendVerification(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	req, ok := respond.DecodeJSON[resendVerificationRequest](w, r)
	if !ok {
		return
	}

	if err := validateResendRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	result, err := h.svc.ResendVerification(r.Context(), ResendInput{
		Email:     req.Email,
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "verification.ResendVerification: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	// SendOTPEmail is a no-op when result.RawCode is empty (anti-enumeration path);
	// the rawCode == "" guard is enforced inside mailer.SendOTPEmail itself.
	if err := mailer.SendOTPEmail(r.Context(), h.OTPHandlerBase, result.UserID, result.Email, result.RawCode, "verification"); err != nil {
		// We know the account is real here, so 503 does not break anti-enumeration.
		slog.ErrorContext(r.Context(), "verification.ResendVerification: mail delivery failed", "error", err)
		respond.Error(w, http.StatusServiceUnavailable, "mail_delivery_failed",
			"could not send verification email — please try again later")
		return
	}

	// Always respond with 202 — the caller cannot infer whether an email was sent.
	respond.JSON(w, http.StatusAccepted, map[string]string{
		"message": "if that email is registered and unverified, a new code has been sent",
	})
}
