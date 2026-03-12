// Package google handles Google OAuth authentication: initiate, callback, and unlink.
package google

import (
	"github.com/7-Dany/store/backend/internal/audit"
	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
)

// CallbackInput is the input to Service.HandleCallback.
// All strings are pre-validated by the handler's guard sequence.
type CallbackInput struct {
	Code         string // authorization code from Google
	CodeVerifier string // PKCE code_verifier from KV state entry
	LinkUserID   string // non-empty when the initiate request carried a valid JWT
	IPAddress    string
	UserAgent    string
}

// CallbackResult is returned by Service.HandleCallback on success.
// Exactly one of Linked or (Session + NewUser) is meaningful:
//   - Linked == true → link mode succeeded; no session is issued
//   - Linked == false → login/register mode succeeded; Session carries token metadata
type CallbackResult struct {
	Session oauthshared.LoggedInSession
	NewUser bool // true when a new users row was created
	Linked  bool // true when link mode ran successfully
}

// GoogleClaims contains the verified claims extracted from a Google OIDC ID token.
type GoogleClaims struct {
	Sub     string // Google subject (stable provider UID)
	Email   string // may be empty for accounts without a verified email
	Name    string // display name
	Picture string // avatar URL
}

// ProviderIdentity is the store-layer view of a user_identities row, returned
// by Store.GetIdentityByProviderUID and Store.GetIdentityByUserAndProvider.
type ProviderIdentity struct {
	ID            [16]byte
	UserID        [16]byte
	ProviderEmail string
	DisplayName   string
	AvatarURL     string
	AccessToken   string // encrypted; carries "enc:" prefix
}

// OAuthUserRecord is the minimal user view returned by Store.GetUserByEmailForOAuth
// and Store.GetUserForOAuthCallback. Carries only the fields needed for lock guards.
type OAuthUserRecord struct {
	ID          [16]byte
	IsActive    bool
	IsLocked    bool
	AdminLocked bool
}

// UserAuthMethods is returned by Store.GetUserAuthMethods for the unlink
// last-auth-method guard.
type UserAuthMethods struct {
	HasPassword   bool
	IdentityCount int64
}

// UpsertIdentityInput carries the fields written by Store.UpsertUserIdentity.
type UpsertIdentityInput struct {
	UserID        [16]byte
	Provider      string // "google"
	ProviderUID   string // Google subject
	ProviderEmail string
	DisplayName   string
	AvatarURL     string
	AccessToken   string // must already carry "enc:" prefix
}

// OAuthLoginTxInput carries the parameters for Store.OAuthLoginTx.
type OAuthLoginTxInput struct {
	UserID    [16]byte
	IPAddress string
	UserAgent string
	NewUser   bool   // true when registering a brand-new user via OAuth
	AvatarURL string // provider avatar; backfilled into users.avatar_url only when currently NULL
}

// OAuthRegisterTxInput carries the parameters for Store.OAuthRegisterTx.
type OAuthRegisterTxInput struct {
	Email         string // may be empty
	DisplayName   string // may be empty
	ProviderUID   string // Google subject
	ProviderEmail string
	AvatarURL     string
	AccessToken   string // must already carry "enc:" prefix
	IPAddress     string
	UserAgent     string
}

// OAuthAuditInput carries the parameters for Store.InsertAuditLogTx.
// Used by link and unlink flows that write their own audit row outside
// OAuthLoginTx / OAuthRegisterTx.
type OAuthAuditInput struct {
	UserID    [16]byte
	Event     audit.EventType
	IPAddress string
	UserAgent string
	Metadata  map[string]any
}

// GoogleTokens holds the raw tokens returned by the Google token endpoint.
type GoogleTokens struct {
	IDToken     string
	AccessToken string
}

// OAuthState is the JSON value stored in KV under "goauth:state:<state>".
// It carries the PKCE code verifier and an optional link_user_id that
// encodes whether the initiate request was made by an authenticated user
// (link mode) or an anonymous visitor (login/register mode).
type OAuthState struct {
	CodeVerifier string `json:"code_verifier"`
	LinkUserID   string `json:"link_user_id"` // empty string when not in link mode
}
