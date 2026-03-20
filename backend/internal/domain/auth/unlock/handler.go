package unlock

import (
	"context"
	"errors"
	"net/http"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	"github.com/7-Dany/store/backend/internal/platform/respond"
)

// Recorder is the narrow observability interface for the unlock handler.
// *telemetry.Registry satisfies this interface structurally.
type Recorder interface {
	OnUnlockRequested()
	OnUnlockCompleted()
}

// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
	RequestUnlock(ctx context.Context, in RequestUnlockInput) (authshared.OTPIssuanceResult, error)
	ConsumeUnlockToken(ctx context.Context, in ConfirmUnlockInput) error
}

// Handler is the HTTP layer for the unlock domain. It parses requests, calls
// the service, maps sentinel errors to HTTP status codes, and enqueues or
// synchronously delivers unlock emails.
type Handler struct {
	svc      Servicer
	recorder Recorder
	mailer.OTPHandlerBase
}

// NewHandler constructs a Handler with the given dependencies.
//
// When base.Queue is non-nil, mail jobs are enqueued asynchronously via the queue.
// When base.Queue is nil, base.Send is called synchronously —
// the preferred path in tests.
func NewHandler(svc Servicer, base mailer.OTPHandlerBase, recorder Recorder) *Handler {
	return &Handler{svc: svc, OTPHandlerBase: base, recorder: recorder}
}

// ── RequestUnlock ──────────────────────────────────────────────────────────────

// RequestUnlock handles POST /request-unlock.
//
// Always returns 202 Accepted after validation — unknown email, unlocked account,
// and service errors are all silently suppressed. Format-validation failures
// (empty or malformed email) return 422 and reveal no account state.
func (h *Handler) RequestUnlock(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	uniformResponse := func() {
		respond.JSON(w, http.StatusAccepted, map[string]string{
			"message": "if that email is registered and locked, an unlock code has been sent",
		})
	}

	req, ok := respond.DecodeJSON[requestUnlockRequest](w, r)
	if !ok {
		return
	}

	if err := validateUnlockRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	result, err := h.svc.RequestUnlock(r.Context(), RequestUnlockInput{
		Email:     req.Email,
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		log.Error(r.Context(), "RequestUnlock: service error", "error", err)
		// Do not expose 500 — always 202 for anti-enumeration.
		uniformResponse()
		return
	}

	// SendOTPEmail is a no-op when result.RawCode is empty (anti-enumeration path).
	if err := mailer.SendOTPEmail(r.Context(), h.OTPHandlerBase, result.UserID, result.Email, result.RawCode, "unlock"); err != nil {
		// Security: do not surface mail failure — any non-202 response on this
		// path reveals that the email is registered and the account is locked.
		log.Warn(r.Context(), "RequestUnlock: mail delivery failed", "error", err, "email", result.Email)
	} else if result.RawCode != "" {
		h.recorder.OnUnlockRequested()
	}

	uniformResponse()
}

// ── ConfirmUnlock ─────────────────────────────────────────────────────────────

// ConfirmUnlock handles POST /confirm-unlock.
func (h *Handler) ConfirmUnlock(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	req, ok := respond.DecodeJSON[confirmUnlockRequest](w, r)
	if !ok {
		return
	}

	if err := validateConfirmUnlockRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	err := h.svc.ConsumeUnlockToken(r.Context(), ConfirmUnlockInput{
		Email:     req.Email,
		Code:      req.Code,
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})

	if err == nil {
		h.recorder.OnUnlockCompleted()
		respond.JSON(w, http.StatusOK, map[string]string{"message": "account unlocked successfully"})
		return
	}

	switch {
	case errors.Is(err, authshared.ErrTokenExpired),
		errors.Is(err, authshared.ErrTokenNotFound),
		errors.Is(err, authshared.ErrTokenAlreadyUsed),
		errors.Is(err, authshared.ErrInvalidCode):
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
	case errors.Is(err, authshared.ErrTooManyAttempts):
		respond.Error(w, http.StatusTooManyRequests, "too_many_attempts", err.Error())
	case errors.Is(err, authshared.ErrAccountLocked):
		respond.Error(w, http.StatusLocked, "account_locked", err.Error())
	default:
		log.Error(r.Context(), "ConfirmUnlock: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
