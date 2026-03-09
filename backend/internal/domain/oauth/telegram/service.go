// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/7-Dany/store/backend/internal/audit"
	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
)

// ─────────────────────────────────────────────────────────────────────────────
// Interfaces
// ─────────────────────────────────────────────────────────────────────────────

// Storer is the persistence interface consumed by Service.
// All methods are implemented by *Store. FakeStorer in shared/testutil implements
// this interface for service unit tests.
type Storer interface {
	// GetIdentityByProviderUID looks up user_identities by (provider=telegram, provider_uid).
	// Returns oauthshared.ErrIdentityNotFound on no-rows.
	GetIdentityByProviderUID(ctx context.Context, providerUID string) (ProviderIdentity, error)

	// GetIdentityByUserAndProvider looks up user_identities by (user_id, provider=telegram).
	// Returns oauthshared.ErrIdentityNotFound on no-rows.
	GetIdentityByUserAndProvider(ctx context.Context, userID [16]byte) (ProviderIdentity, error)

	// GetUserForOAuthCallback fetches a user by ID for the lock guard.
	// Returns authshared.ErrUserNotFound on no-rows.
	GetUserForOAuthCallback(ctx context.Context, userID [16]byte) (OAuthUserRecord, error)

	// GetUserAuthMethods returns HasPassword and IdentityCount for the unlink guard.
	// Returns authshared.ErrUserNotFound on no-rows.
	GetUserAuthMethods(ctx context.Context, userID [16]byte) (UserAuthMethods, error)

	// InsertUserIdentity inserts a new user_identities row for the Telegram provider.
	// Used exclusively by the link flow. Unlike UpsertUserIdentity (used by Google),
	// this is a plain INSERT — the duplicate-provider guard runs before this call.
	// Returns error only.
	InsertUserIdentity(ctx context.Context, in InsertIdentityInput) error

	// DeleteUserIdentity deletes a user_identities row by (user_id, provider=telegram).
	// Returns (rowsAffected, error); the service maps 0 rows → ErrProviderNotLinked.
	DeleteUserIdentity(ctx context.Context, userID [16]byte) (int64, error)

	// OAuthLoginTx creates a session + refresh token + stamps last_login_at +
	// writes an oauth_login audit row — all in one transaction.
	OAuthLoginTx(ctx context.Context, in OAuthLoginTxInput) (oauthshared.LoggedInSession, error)

	// OAuthRegisterTx creates a new user + identity + session + refresh token +
	// last_login_at + audit row — all in one transaction.
	// Email is always empty for Telegram (D-04).
	OAuthRegisterTx(ctx context.Context, in OAuthRegisterTxInput) (oauthshared.LoggedInSession, error)

	// InsertAuditLogTx writes a standalone audit row for link and unlink flows.
	// Caller must pass a context.WithoutCancel ctx.
	InsertAuditLogTx(ctx context.Context, in OAuthAuditInput) error
}

// ─────────────────────────────────────────────────────────────────────────────
// Service
// ─────────────────────────────────────────────────────────────────────────────

// Service holds pure business logic for Telegram OAuth.
type Service struct {
	store Storer
}

// NewService constructs a Service with the given dependencies.
func NewService(store Storer) *Service {
	return &Service{store: store}
}

// compile-time interface check.
var _ Servicer = (*Service)(nil)

// ─────────────────────────────────────────────────────────────────────────────
// HandleCallback
// ─────────────────────────────────────────────────────────────────────────────

// HandleCallback executes the Telegram Login Widget callback flow.
// HMAC verification and auth_date checking are performed by the handler;
// the service receives an already-validated CallbackInput.User.
//
// Guard ordering (matches Stage 0 §4):
//  1. GetIdentityByProviderUID:
//     FOUND    → Existing-user path.
//     NOT FOUND → New-user path.
//
// Existing-user path:
//
//  2. GetUserForOAuthCallback(identity.UserID) — any error → wrap.
//     is_locked || admin_locked → ErrAccountLocked.
//
//  3. OAuthLoginTx(context.WithoutCancel(ctx), ...) — error → wrap.
//
// New-user path:
//
//  2. OAuthRegisterTx(context.WithoutCancel(ctx), ...) — error → wrap.
func (s *Service) HandleCallback(ctx context.Context, in CallbackInput) (CallbackResult, error) {
	providerUID := strconv.FormatInt(in.User.ID, 10)

	slog.DebugContext(ctx, "telegram.HandleCallback: looking up identity",
		"provider_uid", providerUID,
		"ip", in.IPAddress,
	)

	identity, err := s.store.GetIdentityByProviderUID(ctx, providerUID)
	if err == nil {
		// ── EXISTING-USER PATH ────────────────────────────────────────────────
		slog.DebugContext(ctx, "telegram.HandleCallback: existing identity found → login path",
			"user_id", fmt.Sprintf("%x", identity.UserID),
			"provider_uid", providerUID,
		)

		user, err := s.store.GetUserForOAuthCallback(ctx, identity.UserID)
		if err != nil {
			slog.ErrorContext(ctx, "telegram.HandleCallback: get user failed (login path)",
				"user_id", fmt.Sprintf("%x", identity.UserID),
				"error", err,
			)
			return CallbackResult{}, fmt.Errorf("telegram.HandleCallback: get user: %w", err)
		}
		if user.IsLocked || user.AdminLocked {
			slog.DebugContext(ctx, "telegram.HandleCallback: user is locked (login path)",
				"user_id", fmt.Sprintf("%x", identity.UserID),
			)
			return CallbackResult{}, oauthshared.ErrAccountLocked
		}
		if !user.IsActive {
			slog.DebugContext(ctx, "telegram.HandleCallback: user is inactive (login path)",
				"user_id", fmt.Sprintf("%x", identity.UserID),
			)
			return CallbackResult{}, oauthshared.ErrAccountInactive
		}

		slog.DebugContext(ctx, "telegram.HandleCallback: running OAuthLoginTx",
			"user_id", fmt.Sprintf("%x", identity.UserID),
		)
		// WithoutCancel: the Tx writes the audit log internally; protect it from
		// a client disconnect.
		session, err := s.store.OAuthLoginTx(context.WithoutCancel(ctx), OAuthLoginTxInput{
			UserID:    identity.UserID,
			IPAddress: in.IPAddress,
			UserAgent: in.UserAgent,
			NewUser:   false,
		})
		if err != nil {
			slog.ErrorContext(ctx, "telegram.HandleCallback: OAuthLoginTx failed",
				"user_id", fmt.Sprintf("%x", identity.UserID),
				"error", err,
			)
			return CallbackResult{}, fmt.Errorf("telegram.HandleCallback: login tx: %w", err)
		}
		slog.DebugContext(ctx, "telegram.HandleCallback: OAuthLoginTx OK",
			"user_id", fmt.Sprintf("%x", identity.UserID),
			"session_id", fmt.Sprintf("%x", session.SessionID),
		)
		return CallbackResult{Session: session, NewUser: false}, nil
	}

	if !errors.Is(err, oauthshared.ErrIdentityNotFound) {
		return CallbackResult{}, fmt.Errorf("telegram.HandleCallback: get identity by provider uid: %w", err)
	}

	// ── NEW-USER PATH ─────────────────────────────────────────────────────────
	slog.DebugContext(ctx, "telegram.HandleCallback: no existing identity → register path",
		"provider_uid", providerUID,
	)

	displayName := buildDisplayName(in.User.FirstName, in.User.LastName)

	// WithoutCancel: the Tx writes the audit log internally; protect it from
	// a client disconnect.
	session, err := s.store.OAuthRegisterTx(context.WithoutCancel(ctx), OAuthRegisterTxInput{
		DisplayName: displayName,
		ProviderUID: providerUID,
		AvatarURL:   in.User.PhotoURL,
		IPAddress:   in.IPAddress,
		UserAgent:   in.UserAgent,
	})
	if err != nil {
		slog.ErrorContext(ctx, "telegram.HandleCallback: OAuthRegisterTx failed", "error", err)
		return CallbackResult{}, fmt.Errorf("telegram.HandleCallback: register tx: %w", err)
	}
	slog.DebugContext(ctx, "telegram.HandleCallback: OAuthRegisterTx OK",
		"user_id", fmt.Sprintf("%x", session.UserID),
		"session_id", fmt.Sprintf("%x", session.SessionID),
	)

	slog.DebugContext(ctx, "telegram.HandleCallback: complete (new user)",
		"user_id", fmt.Sprintf("%x", session.UserID),
		"session_id", fmt.Sprintf("%x", session.SessionID),
	)
	return CallbackResult{Session: session, NewUser: true}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// LinkTelegram
// ─────────────────────────────────────────────────────────────────────────────

// LinkTelegram associates a Telegram identity with an authenticated user account.
//
// Guard ordering (matches Stage 0 §4):
//  1. GetUserForOAuthCallback(in.UserID) — error → wrap; locked → ErrAccountLocked.
//  2. GetIdentityByUserAndProvider(in.UserID): FOUND → ErrProviderAlreadyLinked.
//  3. GetIdentityByProviderUID(providerUID):
//     FOUND and row.UserID != in.UserID → ErrProviderUIDTaken.
//     FOUND and row.UserID == in.UserID → fall through (idempotent).
//     NOT FOUND → continue.
//  4. InsertUserIdentity — error → wrap.
//  5. InsertAuditLogTx(context.WithoutCancel(ctx), ...) — error → wrap.
func (s *Service) LinkTelegram(ctx context.Context, in LinkInput) error {
	providerUID := strconv.FormatInt(in.User.ID, 10)

	slog.DebugContext(ctx, "telegram.LinkTelegram: resolving user",
		"user_id", fmt.Sprintf("%x", in.UserID),
		"provider_uid", providerUID,
		"ip", in.IPAddress,
	)

	// 1. Resolve the user and check for locks.
	user, err := s.store.GetUserForOAuthCallback(ctx, in.UserID)
	if err != nil {
		slog.ErrorContext(ctx, "telegram.LinkTelegram: get user failed",
			"user_id", fmt.Sprintf("%x", in.UserID),
			"error", err,
		)
		return fmt.Errorf("telegram.LinkTelegram: get user: %w", err)
	}
	if user.IsLocked || user.AdminLocked {
		slog.DebugContext(ctx, "telegram.LinkTelegram: user is locked",
			"user_id", fmt.Sprintf("%x", in.UserID),
		)
		return oauthshared.ErrAccountLocked
	}
	if !user.IsActive {
		slog.DebugContext(ctx, "telegram.LinkTelegram: user is inactive",
			"user_id", fmt.Sprintf("%x", in.UserID),
		)
		return oauthshared.ErrAccountInactive
	}

	// 2. Check whether this user already has a Telegram identity.
	_, err = s.store.GetIdentityByUserAndProvider(ctx, in.UserID)
	if err == nil {
		slog.DebugContext(ctx, "telegram.LinkTelegram: user already has telegram identity",
			"user_id", fmt.Sprintf("%x", in.UserID),
		)
		return ErrProviderAlreadyLinked
	}
	if !errors.Is(err, oauthshared.ErrIdentityNotFound) {
		return fmt.Errorf("telegram.LinkTelegram: get identity by user: %w", err)
	}

	// 3. Check whether this Telegram UID is already bound to another account.
	existing, err := s.store.GetIdentityByProviderUID(ctx, providerUID)
	if err == nil {
		if existing.UserID != in.UserID {
			slog.DebugContext(ctx, "telegram.LinkTelegram: provider uid taken by another user",
				"provider_uid", providerUID,
				"owner_user_id", fmt.Sprintf("%x", existing.UserID),
			)
			return ErrProviderUIDTaken
		}
		// Same user already has this identity — idempotent fall-through.
		slog.DebugContext(ctx, "telegram.LinkTelegram: provider uid already belongs to this user (idempotent)",
			"user_id", fmt.Sprintf("%x", in.UserID),
		)
	} else if !errors.Is(err, oauthshared.ErrIdentityNotFound) {
		return fmt.Errorf("telegram.LinkTelegram: get identity by provider uid: %w", err)
	}

	// 4. Insert the new identity row.
	displayName := buildDisplayName(in.User.FirstName, in.User.LastName)
	slog.DebugContext(ctx, "telegram.LinkTelegram: inserting identity",
		"user_id", fmt.Sprintf("%x", in.UserID),
		"provider_uid", providerUID,
		"display_name", displayName,
	)
	if err := s.store.InsertUserIdentity(ctx, InsertIdentityInput{
		UserID:      in.UserID,
		ProviderUID: providerUID,
		DisplayName: displayName,
		AvatarURL:   in.User.PhotoURL,
	}); err != nil {
		slog.ErrorContext(ctx, "telegram.LinkTelegram: insert identity failed",
			"user_id", fmt.Sprintf("%x", in.UserID),
			"error", err,
		)
		return fmt.Errorf("telegram.LinkTelegram: insert identity: %w", err)
	}
	slog.DebugContext(ctx, "telegram.LinkTelegram: identity inserted OK",
		"user_id", fmt.Sprintf("%x", in.UserID),
	)

	// 5. Write audit log. WithoutCancel so a client disconnect cannot abort it (D-17).
	if err := s.store.InsertAuditLogTx(context.WithoutCancel(ctx), OAuthAuditInput{
		UserID:    in.UserID,
		Event:     audit.EventOAuthLinked,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
		Metadata:  map[string]any{"provider": "telegram"},
	}); err != nil {
		return fmt.Errorf("telegram.LinkTelegram: insert audit: %w", err)
	}

	slog.DebugContext(ctx, "telegram.LinkTelegram: link complete",
		"user_id", fmt.Sprintf("%x", in.UserID),
	)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UnlinkTelegram
// ─────────────────────────────────────────────────────────────────────────────

// UnlinkTelegram removes the Telegram identity from the given user account.
//
// Guard ordering (matches Stage 0 §4):
//  1. GetUserAuthMethods — counts remaining auth methods.
//  2. GetIdentityByUserAndProvider — ErrIdentityNotFound → ErrProviderNotLinked.
//  3. (password ? 1 : 0) + identity_count <= 1 → ErrLastAuthMethod.
//  4. DeleteUserIdentity — 0 rows → ErrProviderNotLinked (lost race).
//  5. InsertAuditLogTx(context.WithoutCancel(ctx), ...) — error → wrap.
func (s *Service) UnlinkTelegram(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error {
	slog.DebugContext(ctx, "telegram.UnlinkTelegram: fetching auth methods",
		"user_id", fmt.Sprintf("%x", userID),
		"ip", ipAddress,
	)

	// 1. Fetch auth-method counts for the last-method guard.
	methods, err := s.store.GetUserAuthMethods(ctx, userID)
	if err != nil {
		slog.ErrorContext(ctx, "telegram.UnlinkTelegram: get auth methods failed",
			"user_id", fmt.Sprintf("%x", userID),
			"error", err,
		)
		return fmt.Errorf("telegram.UnlinkTelegram: get auth methods: %w", err)
	}

	// 2. Confirm the identity exists before evaluating the guard.
	_, err = s.store.GetIdentityByUserAndProvider(ctx, userID)
	if errors.Is(err, oauthshared.ErrIdentityNotFound) {
		slog.DebugContext(ctx, "telegram.UnlinkTelegram: identity not found",
			"user_id", fmt.Sprintf("%x", userID),
		)
		return ErrProviderNotLinked
	}
	if err != nil {
		return fmt.Errorf("telegram.UnlinkTelegram: get identity: %w", err)
	}

	// 3. Last-auth-method guard: sum password (1 or 0) + linked identities.
	// If the total is <= 1, removing this identity would leave the user locked out.
	var pwCount int64
	if methods.HasPassword {
		pwCount = 1
	}
	if pwCount+methods.IdentityCount <= 1 {
		slog.DebugContext(ctx, "telegram.UnlinkTelegram: last auth method guard triggered",
			"user_id", fmt.Sprintf("%x", userID),
			"has_password", methods.HasPassword,
			"identity_count", methods.IdentityCount,
		)
		return oauthshared.ErrLastAuthMethod
	}

	// 4. Delete the identity row.
	slog.DebugContext(ctx, "telegram.UnlinkTelegram: deleting identity",
		"user_id", fmt.Sprintf("%x", userID),
	)
	rows, err := s.store.DeleteUserIdentity(ctx, userID)
	if err != nil {
		slog.ErrorContext(ctx, "telegram.UnlinkTelegram: delete identity failed",
			"user_id", fmt.Sprintf("%x", userID),
			"error", err,
		)
		return fmt.Errorf("telegram.UnlinkTelegram: delete identity: %w", err)
	}
	if rows == 0 {
		// Lost a race — another request deleted the row first.
		slog.DebugContext(ctx, "telegram.UnlinkTelegram: delete returned 0 rows (lost race)",
			"user_id", fmt.Sprintf("%x", userID),
		)
		return ErrProviderNotLinked
	}
	slog.DebugContext(ctx, "telegram.UnlinkTelegram: identity deleted OK",
		"user_id", fmt.Sprintf("%x", userID),
	)

	// 5. Write audit log. WithoutCancel so a client disconnect cannot abort it (D-17).
	if err := s.store.InsertAuditLogTx(context.WithoutCancel(ctx), OAuthAuditInput{
		UserID:    userID,
		Event:     audit.EventOAuthUnlinked,
		IPAddress: ipAddress,
		UserAgent: userAgent,
		Metadata:  map[string]any{"provider": "telegram"},
	}); err != nil {
		return fmt.Errorf("telegram.UnlinkTelegram: insert audit: %w", err)
	}

	slog.DebugContext(ctx, "telegram.UnlinkTelegram: unlink complete",
		"user_id", fmt.Sprintf("%x", userID),
	)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// buildDisplayName joins first and last name, trimming whitespace.
// Returns first_name alone when last_name is empty.
func buildDisplayName(firstName, lastName string) string {
	parts := []string{firstName}
	if lastName != "" {
		parts = append(parts, lastName)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}
