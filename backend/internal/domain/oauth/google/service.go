package google

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/7-Dany/store/backend/internal/audit"
	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

var log = telemetry.New("google")

// ─────────────────────────────────────────────────────────────────────────────
// Interfaces
// ─────────────────────────────────────────────────────────────────────────────

// Storer is the data-access contract for the Google OAuth service.
// Implemented by *Store (production) and GoogleFakeStorer (tests).
type Storer interface {
	GetIdentityByProviderUID(ctx context.Context, providerUID string) (ProviderIdentity, error)
	GetIdentityByUserAndProvider(ctx context.Context, userID [16]byte) (ProviderIdentity, error)
	GetUserByEmailForOAuth(ctx context.Context, email string) (OAuthUserRecord, error)
	GetUserForOAuthCallback(ctx context.Context, userID [16]byte) (OAuthUserRecord, error)
	GetUserAuthMethods(ctx context.Context, userID [16]byte) (UserAuthMethods, error)
	OAuthLoginTx(ctx context.Context, in OAuthLoginTxInput) (oauthshared.LoggedInSession, error)
	OAuthRegisterTx(ctx context.Context, in OAuthRegisterTxInput) (oauthshared.LoggedInSession, error)
	UpsertUserIdentity(ctx context.Context, in UpsertIdentityInput) error
	DeleteUserIdentity(ctx context.Context, userID [16]byte) (int64, error)
	InsertAuditLogTx(ctx context.Context, in OAuthAuditInput) error
}

// OAuthProvider is the seam for Google OIDC token exchange and verification.
// Implemented by *GoogleProvider (production) and a fake in tests.
type OAuthProvider interface {
	ExchangeCode(ctx context.Context, code, codeVerifier string) (GoogleTokens, error)
	VerifyIDToken(ctx context.Context, rawIDToken string) (GoogleClaims, error)
}

// Encryptor is the seam for AES-256-GCM access-token encryption.
// In production, deps.Encryptor satisfies this interface.
type Encryptor interface {
	Encrypt(plaintext string) (string, error)
}

// Servicer is the business-logic contract for the Google OAuth handler.
type Servicer interface {
	HandleCallback(ctx context.Context, in CallbackInput) (CallbackResult, error)
	UnlinkGoogle(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error
}

// ─────────────────────────────────────────────────────────────────────────────
// Service
// ─────────────────────────────────────────────────────────────────────────────

// Service holds pure business logic for Google OAuth.
type Service struct {
	store     Storer
	provider  OAuthProvider
	encryptor Encryptor
}

// NewService constructs a Service with the given dependencies.
func NewService(store Storer, provider OAuthProvider, encryptor Encryptor) *Service {
	return &Service{store: store, provider: provider, encryptor: encryptor}
}

// compile-time interface check.
var _ Servicer = (*Service)(nil)

// ─────────────────────────────────────────────────────────────────────────────
// HandleCallback
// ─────────────────────────────────────────────────────────────────────────────

// HandleCallback executes the OAuth callback flow: token exchange, ID-token
// verification, and either link, login, or register depending on the state.
//
// Guard ordering (matches Stage 0 §7.2):
//  1. ExchangeCode → ErrTokenExchangeFailed on failure.
//  2. VerifyIDToken → ErrInvalidIDToken on failure.
//  3. Encrypt access token → internal error on failure.
//
// Link mode (in.LinkUserID != ""):
//  4. GetUserForOAuthCallback(linkUserID) — any error is internal.
//     is_locked || admin_locked → ErrAccountLocked.
//  5. GetIdentityByProviderUID: FOUND and row.UserID != linkUserID → ErrProviderAlreadyLinked.
//  6. UpsertUserIdentity.
//  7. InsertAuditLogTx(context.WithoutCancel, EventOAuthLinked).
//  8. Return CallbackResult{Linked: true}.
//
// Login/Register mode (in.LinkUserID == ""):
//
//	4a. GetIdentityByProviderUID — FOUND → refresh identity, OAuthLoginTx.
//	4b. NOT FOUND → GetUserByEmailForOAuth.
//	    Email match → link identity, OAuthLoginTx.
//	    No email match → OAuthRegisterTx (new user).
func (s *Service) HandleCallback(ctx context.Context, in CallbackInput) (CallbackResult, error) {
	// 1. Exchange authorization code for Google tokens.
	log.Debug(ctx, "HandleCallback: exchanging code",
		"link_user_id", in.LinkUserID,
		"ip", in.IPAddress,
	)
	tokens, err := s.provider.ExchangeCode(ctx, in.Code, in.CodeVerifier)
	if err != nil {
		log.Error(ctx, "HandleCallback: code exchange failed", "error", err)
		return CallbackResult{}, ErrTokenExchangeFailed
	}
	log.Debug(ctx, "HandleCallback: code exchange OK")

	// 2. Verify ID token and extract claims.
	claims, err := s.provider.VerifyIDToken(ctx, tokens.IDToken)
	if err != nil {
		log.Error(ctx, "HandleCallback: ID token verification failed", "error", err)
		return CallbackResult{}, ErrInvalidIDToken
	}
	log.Debug(ctx, "HandleCallback: ID token verified",
		"sub", claims.Sub,
		"email", claims.Email,
		"name", claims.Name,
	)

	// 3. Encrypt access token before persisting.
	encryptedToken, err := s.encryptor.Encrypt(tokens.AccessToken)
	if err != nil {
		log.Error(ctx, "HandleCallback: access token encryption failed", "error", err)
		return CallbackResult{}, telemetry.Service("HandleCallback.encrypt", err)
	}
	log.Debug(ctx, "HandleCallback: access token encrypted OK",
		"encrypted_len", len(encryptedToken),
	)

	// ── LINK MODE ────────────────────────────────────────────────────────────
	if in.LinkUserID != "" {
		log.Debug(ctx, "HandleCallback: entering link mode", "link_user_id", in.LinkUserID)
		parsed, err := uuid.Parse(in.LinkUserID)
		if err != nil {
			return CallbackResult{}, telemetry.Service("HandleCallback.parse_link_user_id", err)
		}
		linkUserID := [16]byte(parsed)

		// 4. Resolve the linking user; any store error is unexpected (ID came from JWT).
		user, err := s.store.GetUserForOAuthCallback(ctx, linkUserID)
		if err != nil {
			log.Error(ctx, "HandleCallback: get link user failed", "link_user_id", in.LinkUserID, "error", err)
			return CallbackResult{}, telemetry.Service("HandleCallback.get_link_user", err)
		}
		if user.IsLocked || user.AdminLocked {
			log.Debug(ctx, "HandleCallback: link user is locked", "link_user_id", in.LinkUserID)
			return CallbackResult{}, oauthshared.ErrAccountLocked
		}

		// 5. Check for a conflicting binding on this provider UID.
		identity, err := s.store.GetIdentityByProviderUID(ctx, claims.Sub)
		if err == nil {
			// Identity exists — conflict if bound to a different user.
			if identity.UserID != linkUserID {
				return CallbackResult{}, oauthshared.ErrProviderAlreadyLinked
			}
			// Same user → fall through; upsert is idempotent.
		} else if !errors.Is(err, oauthshared.ErrIdentityNotFound) {
			return CallbackResult{}, telemetry.Service("HandleCallback.get_identity_link", err)
		}

		// 6. Upsert the identity row.
		log.Debug(ctx, "HandleCallback: upserting identity (link mode)",
			"user_id", in.LinkUserID,
			"provider_uid", claims.Sub,
			"provider_email", claims.Email,
		)
		if err := s.store.UpsertUserIdentity(ctx, UpsertIdentityInput{
			UserID:        linkUserID,
			Provider:      "google",
			ProviderUID:   claims.Sub,
			ProviderEmail: claims.Email,
			DisplayName:   claims.Name,
			AvatarURL:     claims.Picture,
			AccessToken:   encryptedToken,
		}); err != nil {
			log.Error(ctx, "HandleCallback: upsert identity failed (link mode)", "error", err)
			return CallbackResult{}, telemetry.Service("HandleCallback.upsert_identity_link", err)
		}
		log.Debug(ctx, "HandleCallback: identity upserted OK (link mode)", "user_id", in.LinkUserID)

		// 7. Write audit log. WithoutCancel so a client disconnect cannot abort it.
		if err := s.store.InsertAuditLogTx(context.WithoutCancel(ctx), OAuthAuditInput{
			UserID:    linkUserID,
			Event:     audit.EventOAuthLinked,
			IPAddress: in.IPAddress,
			UserAgent: in.UserAgent,
			Metadata:  map[string]any{"provider": "google"},
		}); err != nil {
			return CallbackResult{}, telemetry.Service("HandleCallback.insert_audit_link", err)
		}

		log.Debug(ctx, "HandleCallback: link mode complete", "user_id", in.LinkUserID)
		return CallbackResult{Linked: true}, nil
	}

	// ── LOGIN / REGISTER MODE ─────────────────────────────────────────────────
	log.Debug(ctx, "HandleCallback: entering login/register mode",
		"sub", claims.Sub,
		"email", claims.Email,
	)
	var (
		session oauthshared.LoggedInSession
		newUser bool
	)

	identity, err := s.store.GetIdentityByProviderUID(ctx, claims.Sub)
	if err == nil {
		// Existing identity → refresh it and issue a session.
		log.Debug(ctx, "HandleCallback: existing identity found → login path",
			"user_id", fmt.Sprintf("%x", identity.UserID),
			"sub", claims.Sub,
		)
		user, err := s.store.GetUserForOAuthCallback(ctx, identity.UserID)
		if err != nil {
			log.Error(ctx, "HandleCallback: get user failed (login path)", "error", err)
			return CallbackResult{}, telemetry.Service("HandleCallback.get_user_login", err)
		}
		if user.IsLocked || user.AdminLocked {
			log.Debug(ctx, "HandleCallback: user is locked (login path)",
				"user_id", fmt.Sprintf("%x", identity.UserID),
			)
			return CallbackResult{}, oauthshared.ErrAccountLocked
		}

		log.Debug(ctx, "HandleCallback: upserting identity (login path)",
			"user_id", fmt.Sprintf("%x", identity.UserID),
			"provider_email", claims.Email,
		)
		if err := s.store.UpsertUserIdentity(ctx, UpsertIdentityInput{
			UserID:        identity.UserID,
			Provider:      "google",
			ProviderUID:   claims.Sub,
			ProviderEmail: claims.Email,
			DisplayName:   claims.Name,
			AvatarURL:     claims.Picture,
			AccessToken:   encryptedToken,
		}); err != nil {
			log.Error(ctx, "HandleCallback: upsert identity failed (login path)", "error", err)
			return CallbackResult{}, telemetry.Service("HandleCallback.upsert_identity_login", err)
		}

		// WithoutCancel: the Tx writes the audit log internally; protect it from
		// a client disconnect.
		log.Debug(ctx, "HandleCallback: running OAuthLoginTx",
			"user_id", fmt.Sprintf("%x", identity.UserID),
		)
		session, err = s.store.OAuthLoginTx(context.WithoutCancel(ctx), OAuthLoginTxInput{
			UserID:    identity.UserID,
			IPAddress: in.IPAddress,
			UserAgent: in.UserAgent,
			NewUser:   false,
			AvatarURL: claims.Picture,
		})
		if err != nil {
			log.Error(ctx, "HandleCallback: OAuthLoginTx failed", "error", err)
			return CallbackResult{}, telemetry.Service("HandleCallback.login_tx", err)
		}
		log.Debug(ctx, "HandleCallback: OAuthLoginTx OK",
			"user_id", fmt.Sprintf("%x", identity.UserID),
			"session_id", fmt.Sprintf("%x", session.SessionID),
		)
		newUser = false

	} else if errors.Is(err, oauthshared.ErrIdentityNotFound) {
		// No identity — check for an email-matched existing account.
		existing, emailErr := s.store.GetUserByEmailForOAuth(ctx, claims.Email)
		if emailErr == nil {
			// Email match → auto-link and issue a session.
			log.Debug(ctx, "HandleCallback: email-match found → auto-link path",
			"user_id", fmt.Sprintf("%x", existing.ID),
			"email", claims.Email,
			)
			if existing.IsLocked || existing.AdminLocked {
			log.Debug(ctx, "HandleCallback: email-match user is locked",
			"user_id", fmt.Sprintf("%x", existing.ID),
			)
				return CallbackResult{}, oauthshared.ErrAccountLocked
			}

			log.Debug(ctx, "HandleCallback: upserting identity (email-match path)",
				"user_id", fmt.Sprintf("%x", existing.ID),
				"provider_email", claims.Email,
			)
			if err := s.store.UpsertUserIdentity(ctx, UpsertIdentityInput{
				UserID:        existing.ID,
				Provider:      "google",
				ProviderUID:   claims.Sub,
				ProviderEmail: claims.Email,
				DisplayName:   claims.Name,
				AvatarURL:     claims.Picture,
				AccessToken:   encryptedToken,
			}); err != nil {
				log.Error(ctx, "HandleCallback: upsert identity failed (email-match path)", "error", err)
				return CallbackResult{}, telemetry.Service("HandleCallback.upsert_identity_email", err)
			}

			log.Debug(ctx, "HandleCallback: running OAuthLoginTx (email-match path)",
				"user_id", fmt.Sprintf("%x", existing.ID),
			)
			session, err = s.store.OAuthLoginTx(context.WithoutCancel(ctx), OAuthLoginTxInput{
				UserID:    existing.ID,
				IPAddress: in.IPAddress,
				UserAgent: in.UserAgent,
				NewUser:   false,
				AvatarURL: claims.Picture,
			})
			if err != nil {
				log.Error(ctx, "HandleCallback: OAuthLoginTx failed (email-match path)", "error", err)
				return CallbackResult{}, telemetry.Service("HandleCallback.login_tx_email", err)
			}
			log.Debug(ctx, "HandleCallback: OAuthLoginTx OK (email-match path)",
				"user_id", fmt.Sprintf("%x", existing.ID),
				"session_id", fmt.Sprintf("%x", session.SessionID),
			)
			newUser = false

		} else if errors.Is(emailErr, oauthshared.ErrIdentityNotFound) {
			// Brand-new user — register.
			log.Debug(ctx, "HandleCallback: no existing account → register path",
				"email", claims.Email,
			)
			session, err = s.store.OAuthRegisterTx(context.WithoutCancel(ctx), OAuthRegisterTxInput{
				Email:         claims.Email,
				DisplayName:   claims.Name,
				ProviderUID:   claims.Sub,
				ProviderEmail: claims.Email,
				AvatarURL:     claims.Picture,
				AccessToken:   encryptedToken,
				IPAddress:     in.IPAddress,
				UserAgent:     in.UserAgent,
			})
			if err != nil {
				log.Error(ctx, "HandleCallback: OAuthRegisterTx failed", "error", err)
				return CallbackResult{}, telemetry.Service("google.HandleCallback: register tx", err)
			}
			log.Debug(ctx, "HandleCallback: OAuthRegisterTx OK",
				"user_id", fmt.Sprintf("%x", session.UserID),
				"session_id", fmt.Sprintf("%x", session.SessionID),
				"email", claims.Email,
			)
			newUser = true

		} else {
			return CallbackResult{}, telemetry.Service("HandleCallback.get_user_by_email", emailErr)
		}

	} else {
		return CallbackResult{}, telemetry.Service("HandleCallback.get_identity", err)
	}

	log.Debug(ctx, "HandleCallback: complete",
		"new_user", newUser,
		"user_id", fmt.Sprintf("%x", session.UserID),
		"session_id", fmt.Sprintf("%x", session.SessionID),
	)
	return CallbackResult{Session: session, NewUser: newUser}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// UnlinkGoogle
// ─────────────────────────────────────────────────────────────────────────────

// UnlinkGoogle removes the Google identity from the given user account.
//
// Guard ordering (matches Stage 0 §7.3):
//  1. GetUserAuthMethods — counts remaining auth methods.
//  2. GetIdentityByUserAndProvider — NOT FOUND → ErrIdentityNotFound.
//  3. (password ? 1 : 0) + identity_count <= 1 → ErrLastAuthMethod.
//  4. DeleteUserIdentity — 0 rows → ErrIdentityNotFound (lost race).
//  5. InsertAuditLogTx(context.WithoutCancel, EventOAuthUnlinked).
func (s *Service) UnlinkGoogle(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error {
	// 1. Fetch auth-method counts for the last-method guard.
	methods, err := s.store.GetUserAuthMethods(ctx, userID)
	if err != nil {
		return telemetry.Service("UnlinkGoogle.get_auth_methods", err)
	}

	// 2. Confirm the identity exists before evaluating the guard.
	_, err = s.store.GetIdentityByUserAndProvider(ctx, userID)
	if errors.Is(err, oauthshared.ErrIdentityNotFound) {
		return oauthshared.ErrIdentityNotFound
	}
	if err != nil {
		return telemetry.Service("UnlinkGoogle.get_identity", err)
	}

	// 3. Last-auth-method guard: sum password (1 or 0) + linked identities.
	// If the total is <= 1, removing this identity would leave the user locked out.
	var pwCount int64
	if methods.HasPassword {
		pwCount = 1
	}
	if pwCount+methods.IdentityCount <= 1 {
		return oauthshared.ErrLastAuthMethod
	}

	// 4. Delete the identity row.
	rows, err := s.store.DeleteUserIdentity(ctx, userID)
	if err != nil {
		return telemetry.Service("google.UnlinkGoogle: delete identity", err)
	}
	if rows == 0 {
		// Lost a race — another request deleted the row first.
		return oauthshared.ErrIdentityNotFound
	}

	// 5. Write audit log. WithoutCancel so a client disconnect cannot abort it (D-17).
	if err := s.store.InsertAuditLogTx(context.WithoutCancel(ctx), OAuthAuditInput{
	UserID:    userID,
	Event:     audit.EventOAuthUnlinked,
	IPAddress: ipAddress,
	UserAgent: userAgent,
	Metadata:  map[string]any{"provider": "google"},
	}); err != nil {
	return telemetry.Service("UnlinkGoogle.insert_audit", err)
	}

	return nil
}
