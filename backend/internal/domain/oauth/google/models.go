// Package google handles Google OAuth authentication: initiate, callback, and unlink.
package google

import (
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
