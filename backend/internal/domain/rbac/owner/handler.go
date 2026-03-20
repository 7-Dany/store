// Package owner provides HTTP handlers, service logic, and database access for
// the initial owner-role assignment and ownership-transfer operations.
package owner

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	mailertemplates "github.com/7-Dany/store/backend/internal/platform/mailer/templates"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/google/uuid"
)

// Servicer is the subset of the service that the handler requires.
type Servicer interface {
	AssignOwner(ctx context.Context, in AssignOwnerInput) (AssignOwnerResult, error)
	HasPendingTransfer(ctx context.Context) (bool, error)
	InitiateTransfer(ctx context.Context, in InitiateInput) (InitiateResult, string, error)
	AcceptTransfer(ctx context.Context, in AcceptInput) (AcceptResult, error)
	CancelTransfer(ctx context.Context, actingOwnerID [16]byte, ipAddress, userAgent string) error
}

// Handler is the HTTP layer for the owner domain (assign + transfer routes).
type Handler struct {
	svc              Servicer
	secret           string // BOOTSTRAP_SECRET env value; never empty after Routes wires it
	deps             handlerDeps
	mailer           *mailer.SMTPMailer // for sending the transfer invitation email
	mailQueue        *mailer.Queue      // async delivery queue
	initiateLimiter  *ratelimit.UserRateLimiter // applied after validation in InitiateTransfer
}

// handlerDeps is the minimal interface the handler needs beyond the service.
type handlerDeps interface {
	isOwner(ctx context.Context, userID string) (bool, error)
}

// rbacDeps wraps *rbac.Checker to satisfy handlerDeps.
type rbacDeps struct{ checker *rbac.Checker }

func (d *rbacDeps) isOwner(ctx context.Context, userID string) (bool, error) {
	return d.checker.IsOwner(ctx, userID)
}

// NewHandler constructs a Handler.
func NewHandler(svc Servicer, secret string, checker *rbac.Checker, m *mailer.SMTPMailer, queue *mailer.Queue, initiateLimiter *ratelimit.UserRateLimiter) *Handler {
	return &Handler{
		svc:             svc,
		secret:          secret,
		deps:            &rbacDeps{checker: checker},
		mailer:          m,
		mailQueue:       queue,
		initiateLimiter: initiateLimiter,
	}
}

// ── AssignOwner ───────────────────────────────────────────────────────────────

// AssignOwner handles POST /owner/assign.
//
// This is the one-time bootstrap path: the first authenticated user to present
// the correct server secret is assigned the owner role. It fails if an active
// owner already exists.
//
// Guards (in order):
//  1. JWT middleware (applied in routes.go) — rejects unauthenticated callers.
//  2. secret field — must match BOOTSTRAP_SECRET (constant-time compare).
//  3. Service layer — rejects if an active owner already exists.
func (h *Handler) AssignOwner(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
	req, ok := respond.DecodeJSON[assignOwnerRequest](w, r)
	if !ok {
		return
	}

	if err := validateAssignOwnerRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	// Constant-time comparison prevents timing attacks against the secret.
	if subtle.ConstantTimeCompare([]byte(req.Secret), []byte(h.secret)) != 1 {
		respond.Error(w, http.StatusForbidden, "forbidden", "invalid secret")
		return
	}

	// Unreachable: userID is the JWT sub claim, already validated as a valid UUID
	// by the token middleware before this handler is reached.
	parsed, _ := uuid.Parse(userID)
	result, err := h.svc.AssignOwner(r.Context(), AssignOwnerInput{
		UserID:    [16]byte(parsed),
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err == nil {
		respond.JSON(w, http.StatusCreated, assignOwnerResponse{
			UserID:    result.UserID,
			RoleName:  result.RoleName,
			GrantedAt: result.GrantedAt,
		})
		return
	}

	switch {
	case errors.Is(err, ErrOwnerAlreadyExists):
		respond.Error(w, http.StatusConflict, "owner_already_exists", "an active owner already exists")
	case errors.Is(err, rbacshared.ErrUserNotFound):
		respond.Error(w, http.StatusNotFound, "user_not_found", "authenticated user account no longer exists")
	case errors.Is(err, ErrUserNotActive):
		respond.Error(w, http.StatusUnprocessableEntity, "user_not_active", "user account is not active")
	case errors.Is(err, ErrUserNotVerified):
		respond.Error(w, http.StatusUnprocessableEntity, "email_not_verified", "user email address must be verified before being assigned owner")
	default:
		log.Error(r.Context(), "AssignOwner: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// ── InitiateTransfer ──────────────────────────────────────────────────────────

// InitiateTransfer handles POST /owner/transfer.
//
// Guards (in order):
//  1. mustUserID                      → 401 if no JWT
//  2. deps.isOwner                    → 403 if not owner
//  3. MaxBytesReader + DecodeJSON     → 400 on decode failure
//  4. validateInitiateRequest         → 422 on validation error
//  5. self-transfer check             → 409 cannot_transfer_to_self (free, no DB)
//  6. pending-transfer check          → 409 transfer_already_pending (1 DB read)
//  7. rate limiter                    → 429 (only genuine new attempts are charged)
//  8. svc.InitiateTransfer            → 404/409/422 per error table
//  9. respond.JSON 201
func (h *Handler) InitiateTransfer(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	isOwner, err := h.deps.isOwner(r.Context(), userID)
	if err != nil {
		log.Error(r.Context(), "InitiateTransfer: IsOwner check failed", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !isOwner {
		respond.Error(w, http.StatusForbidden, "forbidden", "owner role required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
	req, ok := respond.DecodeJSON[initiateRequest](w, r)
	if !ok {
		return
	}

	if err := validateInitiateRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	// Parse UUIDs early so the pre-rate-limit guards below can compare them.
	// Unreachable errors: both values were validated as proper UUIDs above / by JWT middleware.
	parsed, _ := uuid.Parse(userID)
	targetParsed, _ := uuid.Parse(req.TargetUserID)

	// Self-transfer is a pure logic rejection — never charge a rate-limit token.
	if [16]byte(parsed) == [16]byte(targetParsed) {
		respond.Error(w, http.StatusConflict, "cannot_transfer_to_self", "cannot transfer ownership to yourself")
		return
	}

	// Peek at the rate-limit bucket WITHOUT consuming a token.
	// If the bucket is already exhausted the request is dead regardless of
	// whether a transfer is pending — respond 429 immediately so the caller
	// sees the correct signal (bucket empty, not idempotency clash).
	if h.initiateLimiter != nil && !h.initiateLimiter.Peek(r.Context(), userID) {
		w.Header().Set("Retry-After", h.initiateLimiter.RetryAfterSecs())
		respond.Error(w, http.StatusTooManyRequests, "too_many_requests", "too many requests — please slow down")
		return
	}

	// Bucket has capacity. A duplicate-pending request should be rejected as
	// 409 without consuming that remaining token.
	pending, err := h.svc.HasPendingTransfer(r.Context())
	if err != nil {
		log.Error(r.Context(), "InitiateTransfer: HasPendingTransfer failed", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if pending {
		respond.Error(w, http.StatusConflict, "transfer_already_pending", "an ownership transfer is already pending")
		return
	}

	// Genuine new initiation attempt — consume the token now.
	if h.initiateLimiter != nil && !h.initiateLimiter.Allow(r.Context(), userID) {
		w.Header().Set("Retry-After", h.initiateLimiter.RetryAfterSecs())
		respond.Error(w, http.StatusTooManyRequests, "too_many_requests", "too many requests — please slow down")
		return
	}

	result, rawToken, err := h.svc.InitiateTransfer(r.Context(), InitiateInput{
		ActingOwnerID: [16]byte(parsed),
		TargetUserID:  [16]byte(targetParsed),
		IPAddress:     respond.ClientIP(r),
		UserAgent:     r.UserAgent(),
	})
	if err == nil {
		// Enqueue the ownership-transfer invitation email to the target user.
		// Non-fatal: a queue failure is logged but does not abort the response;
		// the token is already persisted and the owner can initiate again.
		if h.mailQueue != nil && h.mailer != nil {
			token := rawToken
			email := result.TargetEmail
			asyncCtx, asyncCancel := context.WithDeadline(context.Background(), time.Now().Add(30*time.Second))
			if enqErr := h.mailQueue.Enqueue(mailer.Job{
				Ctx:     asyncCtx,
				Email:   email,
				Code:    token,
				Deliver: h.mailer.Send(mailertemplates.OwnerTransferKey),
			}); enqErr != nil {
				asyncCancel()
				log.Warn(r.Context(), "InitiateTransfer: enqueue invitation email failed", "error", enqErr)
			} else {
				_ = asyncCancel // context expires at deadline; cancel is a no-op but suppresses vet warning
			}
		}
		respond.JSON(w, http.StatusCreated, initiateResponse{
			TransferID:   result.TransferID,
			TargetUserID: result.TargetUserID,
			ExpiresAt:    result.ExpiresAt,
		})
		return
	}

	switch {
	case errors.Is(err, ErrTransferAlreadyPending):
		respond.Error(w, http.StatusConflict, "transfer_already_pending", "an ownership transfer is already pending")
	case errors.Is(err, rbacshared.ErrUserNotFound):
		respond.Error(w, http.StatusNotFound, "user_not_found", "target user not found")
	case errors.Is(err, ErrUserNotActive):
		respond.Error(w, http.StatusUnprocessableEntity, "user_not_active", "target user account is not active")
	case errors.Is(err, ErrUserNotVerified):
		respond.Error(w, http.StatusUnprocessableEntity, "email_not_verified", "target user email is not verified")
	case errors.Is(err, ErrUserIsAlreadyOwner):
		respond.Error(w, http.StatusConflict, "user_is_already_owner", "target user is already the owner")
	case errors.Is(err, ErrCannotTransferToSelf):
		respond.Error(w, http.StatusConflict, "cannot_transfer_to_self", "cannot transfer ownership to yourself")
	default:
		log.Error(r.Context(), "InitiateTransfer: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// ── AcceptTransfer ────────────────────────────────────────────────────────────

// AcceptTransfer handles POST /owner/transfer/accept.
// No JWT required — the raw token is the credential.
//
// Guards (in order):
//  1. MaxBytesReader + DecodeJSON → 400 on decode failure
//  2. validateAcceptRequest       → 422 if token field is empty
//  3. svc.AcceptTransfer          → 410/422/409 per error table
//  4. respond.JSON 200
func (h *Handler) AcceptTransfer(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
	req, ok := respond.DecodeJSON[acceptRequest](w, r)
	if !ok {
		return
	}

	if err := validateAcceptRequest(&req); err != nil {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}

	result, err := h.svc.AcceptTransfer(r.Context(), AcceptInput{
		RawToken:  req.Token,
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err == nil {
		respond.JSON(w, http.StatusOK, acceptResponse{
			NewOwnerID:      result.NewOwnerID,
			PreviousOwnerID: result.PreviousOwnerID,
			TransferredAt:   result.TransferredAt,
		})
		return
	}

	switch {
	case errors.Is(err, ErrTransferTokenInvalid):
		respond.Error(w, http.StatusGone, "token_invalid", "transfer token is invalid, expired, or already used")
	case errors.Is(err, ErrUserNotEligible):
		respond.Error(w, http.StatusUnprocessableEntity, "user_not_eligible", "target user is no longer active or email-verified")
	case errors.Is(err, ErrInitiatorNotOwner):
		respond.Error(w, http.StatusConflict, "initiator_not_owner", "initiating user no longer holds the owner role")
	default:
		log.Error(r.Context(), "AcceptTransfer: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// ── CancelTransfer ────────────────────────────────────────────────────────────

// CancelTransfer handles DELETE /owner/transfer.
//
// Guards (in order):
//  1. mustUserID         → 401 if no JWT
//  2. deps.isOwner       → 403 if not owner
//  3. svc.CancelTransfer → 404 if no pending transfer
//  4. respond.NoContent 204
func (h *Handler) CancelTransfer(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	isOwner, err := h.deps.isOwner(r.Context(), userID)
	if err != nil {
		log.Error(r.Context(), "CancelTransfer: IsOwner check failed", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !isOwner {
		respond.Error(w, http.StatusForbidden, "forbidden", "owner role required")
		return
	}

	// Unreachable: userID is the JWT sub claim, already validated as a valid UUID
	// by the token middleware before this handler is reached.
	parsed, _ := uuid.Parse(userID)
	if err := h.svc.CancelTransfer(r.Context(), [16]byte(parsed), respond.ClientIP(r), r.UserAgent()); err != nil {
		if errors.Is(err, ErrNoPendingTransfer) {
			respond.Error(w, http.StatusNotFound, "no_pending_transfer", "no pending ownership transfer found")
			return
		}
		log.Error(r.Context(), "CancelTransfer: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	respond.NoContent(w)
}

// ── Private helpers ───────────────────────────────────────────────────────────

// mustUserID extracts the authenticated user ID from the JWT context.
// Writes 401 and returns ("", false) if absent or empty.
func (h *Handler) mustUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "missing or invalid access token")
		return "", false
	}
	return userID, true
}
