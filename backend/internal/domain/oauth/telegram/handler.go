// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// Servicer is the business-logic contract for the Telegram OAuth handler.
// *Service satisfies this interface; TelegramFakeServicer in shared/testutil
// satisfies it for handler unit tests.
type Servicer interface {
	HandleCallback(ctx context.Context, in CallbackInput) (CallbackResult, error)
	LinkTelegram(ctx context.Context, in LinkInput) error
	UnlinkTelegram(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error
}

// Handler is the HTTP layer for Telegram Login Widget authentication:
// callback, link, and unlink.
type Handler struct {
	svc           Servicer
	botToken      string
	cfg           token.JWTConfig
	secureCookies bool
}

// NewHandler constructs a Handler with the given dependencies.
func NewHandler(svc Servicer, botToken string, cfg token.JWTConfig, secureCookies bool) *Handler {
	return &Handler{svc: svc, botToken: botToken, cfg: cfg, secureCookies: secureCookies}
}

// HandleCallback handles POST /oauth/telegram/callback.
//
// Guard ordering (Stage 0 §4.1):
//  1. MaxBytesReader
//  2. DecodeJSON → telegramCallbackRequest
//  3. Validate req.ID != 0 → 422 validation_error
//  4. VerifyHMAC(req, botToken) → 401 invalid_signature
//  5. CheckAuthDate(req.AuthDate) → 401 auth_date_expired
//  6. svc.HandleCallback → error switch
//  7. token.MintTokens → error → 500 internal_error
//  8. result.NewUser == true → 201; else → 200
func (h *Handler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	// 1. Limit request body size.
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	// 2. Decode JSON body.
	req, ok := respond.DecodeJSON[telegramCallbackRequest](w, r)
	if !ok {
		return
	}

	// 3. Validate required field.
	if req.ID == 0 {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "id is required")
		return
	}

	// 4. Verify Telegram HMAC signature.
	if err := VerifyHMAC(req, h.botToken); err != nil {
		respond.Error(w, http.StatusUnauthorized, "invalid_signature", "invalid telegram signature")
		return
	}

	// 5. Verify auth_date freshness.
	if err := CheckAuthDate(req.AuthDate); err != nil {
		respond.Error(w, http.StatusUnauthorized, "auth_date_expired", "telegram auth_date is expired or invalid")
		return
	}

	// 6. Delegate to service.
	result, err := h.svc.HandleCallback(r.Context(), CallbackInput{
		User: TelegramUser{
			ID:        req.ID,
			FirstName: req.FirstName,
			LastName:  req.LastName,
			Username:  req.Username,
			PhotoURL:  req.PhotoURL,
			AuthDate:  req.AuthDate,
		},
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidTelegramSignature):
			respond.Error(w, http.StatusUnauthorized, "invalid_signature", "invalid telegram signature")
		case errors.Is(err, ErrTelegramAuthDateExpired):
			respond.Error(w, http.StatusUnauthorized, "auth_date_expired", "telegram auth_date is expired or invalid")
		case errors.Is(err, ErrProviderUIDTaken):
			respond.Error(w, http.StatusConflict, "provider_uid_taken", "telegram account already linked to another user")
		case errors.Is(err, oauthshared.ErrAccountLocked):
			respond.Error(w, http.StatusLocked, "account_locked", "account is locked")
		case errors.Is(err, oauthshared.ErrAccountInactive):
			respond.Error(w, http.StatusForbidden, "account_inactive", "account is inactive")
		default:
			slog.ErrorContext(r.Context(), "telegram.HandleCallback: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	// 7. Mint tokens (SetRefreshCookie sets the refresh_token HttpOnly cookie).
	mintResult, mintErr := token.MintTokens(w, token.MintTokensInput{
		UserID:        result.Session.UserID,
		SessionID:     result.Session.SessionID,
		RefreshJTI:    result.Session.RefreshJTI,
		FamilyID:      result.Session.FamilyID,
		RefreshExpiry: result.Session.RefreshExpiry,
	}, h.cfg)
	if mintErr != nil {
		slog.ErrorContext(r.Context(), "telegram.HandleCallback: mint tokens", "error", mintErr)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	// 8. Respond: 201 for new users, 200 for returning users.
	status := http.StatusOK
	if result.NewUser {
		status = http.StatusCreated
	}
	respond.JSON(w, status, map[string]any{
		"access_token": mintResult.AccessToken,
		"token_type":   "Bearer",
		"expires_in":   int(h.cfg.AccessTTL.Seconds()),
	})
}

// HandleLink handles POST /oauth/telegram/link.
//
// Guard ordering (Stage 0 §4.2):
//  1. token.UserIDFromContext → missing/empty → 401 unauthorized
//  2. uuid.Parse(userIDStr) → error → 401 unauthorized
//  3. MaxBytesReader
//  4. DecodeJSON → telegramCallbackRequest
//  5. Validate req.ID != 0 → 422 validation_error
//  6. VerifyHMAC(req, botToken) → 401 invalid_signature
//  7. CheckAuthDate(req.AuthDate) → 401 auth_date_expired
//  8. svc.LinkTelegram → error switch
//  9. respond.NoContent(w) — 204
func (h *Handler) HandleLink(w http.ResponseWriter, r *http.Request) {
	// 1. Require authenticated user.
	userIDStr, ok := token.UserIDFromContext(r.Context())
	if !ok || userIDStr == "" {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "missing authentication")
		return
	}

	// 2. Parse user ID.
	parsed, err := uuid.Parse(userIDStr)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "invalid user identity")
		return
	}

	// 3. Limit request body size.
	r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)

	// 4. Decode JSON body.
	req, ok := respond.DecodeJSON[telegramCallbackRequest](w, r)
	if !ok {
		return
	}

	// 5. Validate required field.
	if req.ID == 0 {
		respond.Error(w, http.StatusUnprocessableEntity, "validation_error", "id is required")
		return
	}

	// 6. Verify Telegram HMAC signature.
	if err := VerifyHMAC(req, h.botToken); err != nil {
		respond.Error(w, http.StatusUnauthorized, "invalid_signature", "invalid telegram signature")
		return
	}

	// 7. Verify auth_date freshness.
	if err := CheckAuthDate(req.AuthDate); err != nil {
		respond.Error(w, http.StatusUnauthorized, "auth_date_expired", "telegram auth_date is expired or invalid")
		return
	}

	// 8. Delegate to service.
	if err := h.svc.LinkTelegram(r.Context(), LinkInput{
		UserID: [16]byte(parsed),
		User: TelegramUser{
			ID:        req.ID,
			FirstName: req.FirstName,
			LastName:  req.LastName,
			Username:  req.Username,
			PhotoURL:  req.PhotoURL,
			AuthDate:  req.AuthDate,
		},
		IPAddress: respond.ClientIP(r),
		UserAgent: r.UserAgent(),
	}); err != nil {
		switch {
		case errors.Is(err, ErrInvalidTelegramSignature):
			respond.Error(w, http.StatusUnauthorized, "invalid_signature", "invalid telegram signature")
		case errors.Is(err, ErrTelegramAuthDateExpired):
			respond.Error(w, http.StatusUnauthorized, "auth_date_expired", "telegram auth_date is expired or invalid")
		case errors.Is(err, ErrProviderAlreadyLinked):
			respond.Error(w, http.StatusConflict, "provider_already_linked", "telegram account already linked to this user")
		case errors.Is(err, ErrProviderUIDTaken):
			respond.Error(w, http.StatusConflict, "provider_uid_taken", "telegram account already linked to another user")
		case errors.Is(err, oauthshared.ErrAccountLocked):
			respond.Error(w, http.StatusLocked, "account_locked", "account is locked")
		case errors.Is(err, oauthshared.ErrAccountInactive):
			respond.Error(w, http.StatusForbidden, "account_inactive", "account is inactive")
		default:
			slog.ErrorContext(r.Context(), "telegram.HandleLink: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	// 9. Success.
	respond.NoContent(w)
}

// HandleUnlink handles DELETE /oauth/telegram/unlink.
//
// Guard ordering (Stage 0 §4.3):
//  1. token.UserIDFromContext → missing/empty → 401 unauthorized
//  2. uuid.Parse(userIDStr) → error → 401 unauthorized
//  3. svc.UnlinkTelegram → error switch
//  4. respond.NoContent(w) — 204
func (h *Handler) HandleUnlink(w http.ResponseWriter, r *http.Request) {
	// 1. Require authenticated user.
	userIDStr, ok := token.UserIDFromContext(r.Context())
	if !ok || userIDStr == "" {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "missing authentication")
		return
	}

	// 2. Parse user ID.
	parsed, err := uuid.Parse(userIDStr)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "invalid user identity")
		return
	}

	// 3. Delegate to service.
	if err := h.svc.UnlinkTelegram(r.Context(), [16]byte(parsed), respond.ClientIP(r), r.UserAgent()); err != nil {
		switch {
		case errors.Is(err, ErrProviderNotLinked):
			respond.Error(w, http.StatusNotFound, "provider_not_linked", "no telegram identity linked to this account")
		case errors.Is(err, oauthshared.ErrLastAuthMethod):
			respond.Error(w, http.StatusConflict, "last_auth_method", "cannot remove the last authentication method")
		default:
			slog.ErrorContext(r.Context(), "telegram.HandleUnlink: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	// 4. Success.
	respond.NoContent(w)
}
