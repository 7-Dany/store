// Package deleteaccount provides the HTTP handler, service, and store for
// DELETE /api/v1/profile/me (account deletion) and POST /api/v1/profile/me/cancel-deletion.
package deleteaccount

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	mailertemplates "github.com/7-Dany/store/backend/internal/platform/mailer/templates"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// Servicer is the subset of service methods the Handler requires.
// *Service satisfies this interface; tests supply DeleteAccountFakeServicer.
type Servicer interface {
	// ResolveUserForDeletion fetches the user and auth methods for empty-body dispatch.
	// The handler calls this when no password, code, or telegram_auth is present
	// to determine whether to trigger email-OTP (Path B step 1) or respond with
	// the Telegram widget prompt (Path C step 1).
	// Returns ErrAlreadyPendingDeletion when deleted_at is already set.
	ResolveUserForDeletion(ctx context.Context, userID string) (DeletionUser, UserAuthMethods, error)

	// DeleteWithPassword completes soft-deletion for a password-authenticated user (Path A).
	// Returns ErrAlreadyPendingDeletion (409), ErrInvalidCredentials (401).
	DeleteWithPassword(ctx context.Context, in DeleteWithPasswordInput) (DeletionScheduled, error)

	// InitiateEmailDeletion triggers the email-OTP flow (Path B step 1).
	// Returns OTPIssuanceResult so the handler can enqueue the email.
	// Returns ErrAlreadyPendingDeletion (409).
	InitiateEmailDeletion(ctx context.Context, in ScheduleDeletionInput) (authshared.OTPIssuanceResult, error)

	// ConfirmEmailDeletion validates the OTP and completes soft-deletion (Path B step 2).
	// Returns ErrAlreadyPendingDeletion (409), ErrTokenNotFound (422), ErrTooManyAttempts (429),
	// ErrInvalidCode (422), ErrTokenAlreadyUsed (422).
	ConfirmEmailDeletion(ctx context.Context, in ConfirmOTPDeletionInput) (DeletionScheduled, error)

	// ConfirmTelegramDeletion validates HMAC re-auth and completes soft-deletion (Path C step 2).
	// Returns ErrAlreadyPendingDeletion (409), ErrInvalidTelegramAuth (401),
	// ErrTelegramIdentityMismatch (401).
	ConfirmTelegramDeletion(ctx context.Context, in ConfirmTelegramDeletionInput) (DeletionScheduled, error)

	// CancelDeletion clears deleted_at for a pending-deletion account (POST /me/cancel-deletion).
	// Returns ErrNotPendingDeletion (409).
	CancelDeletion(ctx context.Context, in CancelDeletionInput) error

	// GetDeletionMethod returns the confirmation method the client should prepare
	// for this user (GET /me/deletion-method). The result mirrors the empty-body
	// dispatch logic in handler.Delete so the frontend can render the correct UI
	// without firing a blind DELETE first.
	// Returns ErrAlreadyPendingDeletion (409) when deletion is already scheduled.
	GetDeletionMethod(ctx context.Context, userID string) (DeletionMethodResult, error)
}

// Handler is the HTTP layer for DELETE /me and POST /me/cancel-deletion.
// It parses requests, dispatches to the service, and maps sentinel errors to
// HTTP status codes. It has no knowledge of pgtype, pgxpool, JWT signing, or
// the KV store.
type Handler struct {
	svc                 Servicer
	mailer              *mailer.SMTPMailer
	mailQueue           *mailer.Queue
	mailDeliveryTimeout time.Duration
	otpTTL              time.Duration // lifetime of the email-deletion OTP; included in Path B-1 202 as expires_in
}

// NewHandler constructs a Handler with the given service and mail delivery dependencies.
// mailer, mailQueue, and mailDeliveryTimeout may be nil/zero in tests that do not
// exercise the email notification path (Path B-1).
// otpTTL is the OTP token lifetime; it is included in the Path B-1 202 response
// as expires_in so clients can render a countdown timer without a separate request.
func NewHandler(svc Servicer, m *mailer.SMTPMailer, mailQueue *mailer.Queue, mailDeliveryTimeout, otpTTL time.Duration) *Handler {
	return &Handler{
		svc:                 svc,
		mailer:              m,
		mailQueue:           mailQueue,
		mailDeliveryTimeout: mailDeliveryTimeout,
		otpTTL:              otpTTL,
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// enqueueEmail submits a best-effort async mail job. On queue failure it logs
// a warning and cancels the context — the request still succeeds.
func (h *Handler) enqueueEmail(reqCtx context.Context, logPrefix, userID, toEmail, code, templateKey string) {
	if h.mailQueue == nil {
		return
	}
	mailCtx, mailCancel := context.WithTimeout(context.Background(), h.mailDeliveryTimeout)
	_ = mailCancel // ownership transferred to queue worker on success; cancel called below only on failure
	if err := h.mailQueue.Enqueue(mailer.Job{
		Ctx:     mailCtx,
		UserID:  userID,
		Email:   toEmail,
		Code:    code,
		Deliver: h.mailer.Send(templateKey),
	}); err != nil {
		mailCancel()
		slog.WarnContext(reqCtx, logPrefix+": enqueue mail failed (best-effort)", "error", err)
	}
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

// mapDeleteError maps service sentinel errors to the appropriate HTTP response.
// It is a shared private method used by all Delete sub-paths.
func (h *Handler) mapDeleteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrAlreadyPendingDeletion):
		respond.Error(w, http.StatusConflict, "already_pending_deletion", err.Error())
	case errors.Is(err, ErrDeletionTokenCooldown):
		respond.Error(w, http.StatusTooManyRequests, "deletion_token_cooldown", err.Error())
	case errors.Is(err, authshared.ErrInvalidCredentials):
		respond.Error(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
	case errors.Is(err, ErrInvalidTelegramAuth):
		respond.Error(w, http.StatusUnauthorized, "invalid_telegram_auth", err.Error())
	case errors.Is(err, ErrTelegramIdentityMismatch):
		respond.Error(w, http.StatusUnauthorized, "telegram_identity_mismatch", err.Error())
	case errors.Is(err, authshared.ErrCodeInvalidFormat):
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
	case errors.Is(err, authshared.ErrTokenNotFound):
		respond.Error(w, http.StatusUnprocessableEntity, "token_not_found", err.Error())
	case errors.Is(err, authshared.ErrTokenAlreadyUsed):
		respond.Error(w, http.StatusUnprocessableEntity, "token_already_used", err.Error())
	case errors.Is(err, authshared.ErrInvalidCode):
		respond.Error(w, http.StatusUnprocessableEntity, "invalid_code", err.Error())
	case errors.Is(err, authshared.ErrTooManyAttempts):
		respond.Error(w, http.StatusTooManyRequests, "too_many_attempts", err.Error())
	default:
		slog.ErrorContext(r.Context(), "deleteaccount.Delete: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// Delete handles DELETE /me.
//
// Dispatches across five paths based on request body fields and user auth methods:
//   - Path A:    password present → DeleteWithPassword
//   - Path B-2:  code present     → ConfirmEmailDeletion
//   - Path C-2:  telegram_auth present → ConfirmTelegramDeletion
//   - Path B-1:  empty body + email user → InitiateEmailDeletion (202)
//   - Path C-1:  empty body + Telegram-only user → 202 with auth_method:telegram
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	req, ok := respond.DecodeJSON[deleteAccountRequest](w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	ip := respond.ClientIP(r)
	ua := r.UserAgent()

	// Path A — password present.
	if req.Password != "" {
		result, err := h.svc.DeleteWithPassword(ctx, DeleteWithPasswordInput{
			UserID:    userID,
			Password:  req.Password,
			IPAddress: ip,
			UserAgent: ua,
		})
		if err != nil {
			h.mapDeleteError(w, r, err)
			return
		}
		respond.JSON(w, http.StatusOK, newDeletionScheduledResponse(result))
		return
	}

	// Path B-2 — OTP code present.
	if req.Code != "" {
		result, err := h.svc.ConfirmEmailDeletion(ctx, ConfirmOTPDeletionInput{
			UserID:    userID,
			Code:      req.Code,
			IPAddress: ip,
			UserAgent: ua,
		})
		if err != nil {
			h.mapDeleteError(w, r, err)
			return
		}
		respond.JSON(w, http.StatusOK, newDeletionScheduledResponse(result))
		return
	}

	// Path C-2 — Telegram auth present.
	if req.TelegramAuth != nil {
		if err := validateTelegramAuthPayload(req.TelegramAuth); err != nil {
			respond.Error(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		result, err := h.svc.ConfirmTelegramDeletion(ctx, ConfirmTelegramDeletionInput{
			UserID:       userID,
			TelegramAuth: *req.TelegramAuth,
			IPAddress:    ip,
			UserAgent:    ua,
		})
		if err != nil {
			h.mapDeleteError(w, r, err)
			return
		}
		respond.JSON(w, http.StatusOK, newDeletionScheduledResponse(result))
		return
	}

	// Empty body — resolve auth methods to determine step-1 path.
	//
	// Multi-auth priority for empty-body dispatch:
	//   1. HasPassword → 400 (password required; no OTP fallback for password accounts)
	//   2. HasEmail    → Path B-1 (email OTP), even if Telegram is also linked
	//   3. Telegram-only → Path C-1 (widget prompt)
	// Use GET /me/deletion-method to discover which path applies before sending DELETE.
	user, authMethods, err := h.svc.ResolveUserForDeletion(ctx, userID)
	if err != nil {
		h.mapDeleteError(w, r, err)
		return
	}

	// D-11: password account with no password field → 400.
	if authMethods.HasPassword {
		respond.Error(w, http.StatusBadRequest, "validation_error",
			"password is required to delete a password-protected account")
		return
	}

	// Path B-1 — email user: issue OTP.
	if user.Email != nil {
		issuance, err := h.svc.InitiateEmailDeletion(ctx, ScheduleDeletionInput{
			UserID:    userID,
			IPAddress: ip,
			UserAgent: ua,
		})
		if err != nil {
			h.mapDeleteError(w, r, err)
			return
		}
		// Best-effort: fire-and-forget OTP delivery.
		h.enqueueEmail(r.Context(), "deleteaccount.Delete", userID,
			issuance.Email, issuance.RawCode, mailertemplates.AccountDeletionOTPKey)
		respond.JSON(w, http.StatusAccepted, deletionInitiatedResponse{
			Message:    "verification code sent to your email",
			AuthMethod: "email_otp",
			ExpiresIn:  int(h.otpTTL.Seconds()),
		})
		return
	}

	// Path C-1 — Telegram-only user: prompt widget.
	respond.JSON(w, http.StatusAccepted, deletionInitiatedResponse{
		Message:    "authenticate via Telegram to confirm deletion",
		AuthMethod: "telegram",
	})
}

// CancelDeletion handles POST /me/cancel-deletion.
//
// Requires a valid JWT. Clears deleted_at for a pending-deletion account.
func (h *Handler) CancelDeletion(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	_, ok = respond.DecodeJSON[cancelDeletionRequest](w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	err := h.svc.CancelDeletion(ctx, CancelDeletionInput{
		UserID:    userID,
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotPendingDeletion):
			respond.Error(w, http.StatusConflict, "not_pending_deletion", err.Error())
		default:
			slog.ErrorContext(r.Context(), "deleteaccount.CancelDeletion: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	respond.JSON(w, http.StatusOK, deletionInitiatedResponse{Message: "account deletion cancelled"})
}

// DeletionMethod handles GET /me/deletion-method.
//
// Returns the confirmation method the client should prepare for this user so
// the frontend can render the correct UI before initiating deletion — without
// firing a blind DELETE first.
//
// Response JSON:
//
//	{ "deletion_method": "password" | "email_otp" | "telegram" }
func (h *Handler) DeletionMethod(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	result, err := h.svc.GetDeletionMethod(r.Context(), userID)
	if err != nil {
		switch {
		case errors.Is(err, ErrAlreadyPendingDeletion):
			respond.Error(w, http.StatusConflict, "already_pending_deletion", err.Error())
		default:
			slog.ErrorContext(r.Context(), "deleteaccount.DeletionMethod: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"deletion_method": result.Method})
}
