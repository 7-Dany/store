package register

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	"github.com/7-Dany/store/backend/internal/platform/respond"
)

// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
	Register(ctx context.Context, in RegisterInput) (RegisterResult, error)
}

// Handler is the HTTP layer for the register sub-package. It parses requests,
// calls the service, maps sentinel errors to HTTP status codes, and enqueues
// or synchronously delivers verification emails.
type Handler struct {
	svc Servicer
	mailer.OTPHandlerBase
}

// NewHandler constructs a Handler.
//
// When base.Queue is non-nil, mail jobs are enqueued asynchronously via the queue.
// When base.Queue is nil, base.Send is called synchronously on the handler
// goroutine — the preferred path in tests.
func NewHandler(svc Servicer, base mailer.OTPHandlerBase) *Handler {
	return &Handler{svc: svc, OTPHandlerBase: base}
}

// ── Register ──────────────────────────────────────────────────────────────────

// Register handles POST /register.
// On success it returns 201 and kicks off email delivery.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	req, ok := respond.DecodeJSON[registerRequest](w, r)
	if !ok {
		return
	}

	if err := validateAndNormalise(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	result, err := h.svc.Register(r.Context(), RegisterInput{
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Password:    req.Password,
		Username:    req.Username,
		IPAddress:   respond.ClientIP(r),
		UserAgent:   r.UserAgent(),
	})
	if err != nil {
		switch {
		case errors.Is(err, authshared.ErrEmailTaken):
			respond.Error(w, http.StatusConflict, "email_taken", "this email is already registered")
		case errors.Is(err, authshared.ErrUsernameTaken):
			respond.Error(w, http.StatusConflict, "username_taken", "this username is already taken")
		default:
			slog.ErrorContext(r.Context(), "register.Register: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	if err := mailer.SendOTPEmail(r.Context(), h.OTPHandlerBase, result.UserID, result.Email, result.RawCode, "register"); err != nil {
		// Registration succeeded but email delivery failed entirely (queue full
		// AND synchronous fallback failed). Tell the client so they know to retry
		// via the resend endpoint rather than waiting indefinitely.
		slog.ErrorContext(r.Context(), "register.Register: mail delivery failed", "error", err)
		respond.Error(w, http.StatusServiceUnavailable, "mail_delivery_failed",
			"registration succeeded but we could not send your verification email — please use the resend endpoint")
		return
	}

	respond.JSON(w, http.StatusCreated, map[string]string{
		"message": "registration successful — please check your email for a verification code",
	})
}
