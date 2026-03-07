package email

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/7-Dany/store/backend/internal/app"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	mailertemplates "github.com/7-Dany/store/backend/internal/platform/mailer/templates"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// Servicer is the business-logic contract for the email-change feature.
// *Service satisfies this interface; tests use EmailChangeFakeServicer from
// internal/domain/auth/shared/testutil.
type Servicer interface {
	RequestEmailChange(ctx context.Context, in EmailChangeRequestInput) (EmailChangeRequestResult, error)
	VerifyCurrentEmail(ctx context.Context, in EmailChangeVerifyCurrentInput) (EmailChangeVerifyCurrentResult, error)
	ConfirmEmailChange(ctx context.Context, in EmailChangeConfirmInput) (ConfirmEmailChangeResult, error)
}

// Handler is the HTTP layer for the email-change feature. It parses requests,
// calls the service, and maps sentinel errors to HTTP status codes.
// It has no knowledge of pgtype, pgxpool, JWT signing, or the KV store.
type Handler struct {
	svc  Servicer
	deps *app.Deps
}

// NewHandler constructs a Handler with the given service and dependencies.
func NewHandler(svc Servicer, deps *app.Deps) *Handler {
	return &Handler{svc: svc, deps: deps}
}

// enqueueEmail submits a best-effort async mail job. On queue failure it logs
// a warning and cancels the context — the request still succeeds.
func (h *Handler) enqueueEmail(reqCtx context.Context, logPrefix, userID, toEmail, code, templateKey string) {
	mailCtx, mailCancel := context.WithTimeout(context.Background(), h.deps.MailDeliveryTimeout)
	_ = mailCancel // ownership transferred to queue worker on success; cancel called below only on failure
	if err := h.deps.MailQueue.Enqueue(mailer.Job{
		Ctx:     mailCtx,
		UserID:  userID,
		Email:   toEmail,
		Code:    code,
		Deliver: h.deps.Mailer.Send(templateKey),
	}); err != nil {
		mailCancel()
		slog.WarnContext(reqCtx, logPrefix+": enqueue mail failed (best-effort)", "error", err)
	}
}

// RequestChange handles POST /email/request-change (step 1).
//
// Requires a valid JWT in the request context (set by JWTAuth middleware).
// On success, enqueues an OTP to the user's current email address and returns
// a 200 with a confirmation message.
func (h *Handler) RequestChange(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	req, ok := respond.DecodeJSON[requestChangeRequest](w, r)
	if !ok {
		return
	}

	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		slog.ErrorContext(r.Context(), "email.RequestChange: parse user id", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	result, err := h.svc.RequestEmailChange(r.Context(), EmailChangeRequestInput{
		UserID:    [16]byte(uid),
		NewEmail:  req.NewEmail,
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidEmailFormat),
			errors.Is(err, ErrEmailTooLong):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		case errors.Is(err, ErrSameEmail):
			respond.Error(w, http.StatusUnprocessableEntity, "same_email", err.Error())
		case errors.Is(err, ErrEmailTaken):
			respond.Error(w, http.StatusConflict, "email_taken", err.Error())
		case errors.Is(err, ErrCooldownActive):
			respond.Error(w, http.StatusTooManyRequests, "cooldown_active", err.Error())
		case errors.Is(err, profileshared.ErrUserNotFound):
			slog.ErrorContext(r.Context(), "email.RequestChange: user not found for authenticated request", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		default:
			slog.ErrorContext(r.Context(), "email.RequestChange: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	// Best-effort: fire-and-forget OTP delivery to current address.
	h.enqueueEmail(r.Context(), "email.RequestChange", userID,
		result.CurrentEmail, result.RawCode, mailertemplates.EmailChangeOTPKey)

	respond.JSON(w, http.StatusOK, messageResponse{Message: "verification code sent to your current email address"})
}

// VerifyCurrent handles POST /email/verify-current (step 2).
//
// Requires a valid JWT in the request context (set by JWTAuth middleware).
// On success, enqueues an OTP to the user's new email address and returns a
// grant token with its expiry.
func (h *Handler) VerifyCurrent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	req, ok := respond.DecodeJSON[verifyCurrentRequest](w, r)
	if !ok {
		return
	}

	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		slog.ErrorContext(r.Context(), "email.VerifyCurrent: parse user id", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	result, err := h.svc.VerifyCurrentEmail(r.Context(), EmailChangeVerifyCurrentInput{
		UserID:    [16]byte(uid),
		Code:      req.Code,
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidCodeFormat):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		case errors.Is(err, authshared.ErrTokenNotFound),
			errors.Is(err, authshared.ErrTokenExpired),
			errors.Is(err, authshared.ErrInvalidCode),
			errors.Is(err, authshared.ErrTokenAlreadyUsed):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		case errors.Is(err, authshared.ErrTooManyAttempts):
			respond.Error(w, http.StatusTooManyRequests, "too_many_attempts", err.Error())
		case errors.Is(err, profileshared.ErrUserNotFound):
			slog.ErrorContext(r.Context(), "email.VerifyCurrent: user not found for authenticated request", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		default:
			slog.ErrorContext(r.Context(), "email.VerifyCurrent: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	// Best-effort: fire-and-forget confirm OTP delivery to new address.
	h.enqueueEmail(r.Context(), "email.VerifyCurrent", userID,
		result.NewEmail, result.NewEmailRawCode, mailertemplates.EmailChangeConfirmOTPKey)

	respond.JSON(w, http.StatusOK, verifyCurrentResponse{
		GrantToken: result.GrantToken,
		ExpiresIn:  result.ExpiresIn,
	})
}

// ConfirmChange handles POST /email/confirm-change (step 3).
//
// Requires a valid JWT in the request context (set by JWTAuth middleware).
// On success, enqueues a notification to the old email address and returns a
// 200 with a confirmation message.
func (h *Handler) ConfirmChange(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	req, ok := respond.DecodeJSON[confirmChangeRequest](w, r)
	if !ok {
		return
	}

	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		slog.ErrorContext(r.Context(), "email.ConfirmChange: parse user id", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	jti, ok := h.mustJTI(w, r)
	if !ok {
		return
	}

	result, err := h.svc.ConfirmEmailChange(r.Context(), EmailChangeConfirmInput{
		UserID:     [16]byte(uid),
		GrantToken: req.GrantToken,
		Code:       req.Code,
		IPAddress:  respond.ClientIP(r),
		UserAgent:  r.UserAgent(),
		AccessJTI:  jti,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrGrantTokenEmpty),
			errors.Is(err, ErrInvalidCodeFormat):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		case errors.Is(err, ErrGrantTokenInvalid):
			respond.Error(w, http.StatusUnprocessableEntity, "invalid_grant_token", err.Error())
		case errors.Is(err, authshared.ErrTokenNotFound),
			errors.Is(err, authshared.ErrTokenExpired),
			errors.Is(err, authshared.ErrInvalidCode),
			errors.Is(err, authshared.ErrTokenAlreadyUsed):
			respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		case errors.Is(err, authshared.ErrTooManyAttempts):
			respond.Error(w, http.StatusTooManyRequests, "too_many_attempts", err.Error())
		case errors.Is(err, ErrEmailTaken):
			respond.Error(w, http.StatusConflict, "email_taken", err.Error())
		case errors.Is(err, profileshared.ErrUserNotFound):
			slog.ErrorContext(r.Context(), "email.ConfirmChange: user not found for authenticated request", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		default:
			slog.ErrorContext(r.Context(), "email.ConfirmChange: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	// Best-effort: fire-and-forget notification to old address.
	// The notification template does not use the Code field; pass "".
	mailCtx, mailCancel := context.WithTimeout(context.Background(), h.deps.MailDeliveryTimeout)
	_ = mailCancel // ownership transferred to queue worker on success; cancel called below only on failure
	if err := h.deps.MailQueue.Enqueue(mailer.Job{
		Ctx:     mailCtx,
		UserID:  userID,
		Email:   result.OldEmail,
		Code:    "",
		Deliver: h.deps.Mailer.Send(mailertemplates.EmailChangedNotificationKey),
	}); err != nil {
		mailCancel()
		slog.WarnContext(r.Context(), "email.ConfirmChange: enqueue notification failed (best-effort)", "error", err)
	}

	respond.JSON(w, http.StatusOK, messageResponse{Message: "email address updated successfully"})
}

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

// mustJTI extracts the JWT ID (jti) from the request context.
// If absent or empty it writes a 401 and returns ("", false) so the caller
// can return immediately.
func (h *Handler) mustJTI(w http.ResponseWriter, r *http.Request) (string, bool) {
	jti, ok := token.JTIFromContext(r.Context())
	if !ok || jti == "" {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "missing jti in context")
		return "", false
	}
	return jti, true
}


