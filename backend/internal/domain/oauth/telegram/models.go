// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram

import (
	"github.com/7-Dany/store/backend/internal/audit"
	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
)

// TelegramUser holds the fields extracted from a validated Telegram Login Widget
// payload. All string fields may be empty except ID (which is always present).
type TelegramUser struct {
	ID        int64  // Telegram user ID (stable provider UID)
	FirstName string
	LastName  string // optional
	Username  string // optional
	PhotoURL  string // optional
	AuthDate  int64  // Unix timestamp; validated by CheckAuthDate
}

// CallbackInput is the input to Service.HandleCallback.
type CallbackInput struct {
	User      TelegramUser
	IPAddress string
	UserAgent string
}

// CallbackResult is returned by Service.HandleCallback on success.
// Exactly one of Linked or (Session + NewUser) is meaningful:
//   - Linked == true  → link mode succeeded; no session is issued
//   - Linked == false → login/register mode succeeded; Session carries token metadata
type CallbackResult struct {
	Session oauthshared.LoggedInSession
	NewUser bool // true when a new users row was created
	Linked  bool // true when link mode ran successfully
}

// LinkInput is the input to Service.LinkTelegram.
type LinkInput struct {
	UserID    [16]byte
	User      TelegramUser
	IPAddress string
	UserAgent string
}

// ProviderIdentity is the store-layer view of a user_identities row for the
// Telegram provider. Returned by GetIdentityByProviderUID and
// GetIdentityByUserAndProvider.
type ProviderIdentity struct {
	ID     [16]byte
	UserID [16]byte
}

// OAuthUserRecord is the minimal user view needed for lock guards in callback
// and link flows. Returned by GetUserForOAuthCallback.
type OAuthUserRecord struct {
	ID          [16]byte
	IsActive    bool
	IsLocked    bool
	AdminLocked bool
}

// UserAuthMethods is returned by GetUserAuthMethods for the last-auth-method
// guard in the unlink flow.
type UserAuthMethods struct {
	HasPassword   bool
	IdentityCount int64
}

// InsertIdentityInput carries the fields written by InsertTelegramIdentity
// during the link flow.
type InsertIdentityInput struct {
	UserID      [16]byte
	ProviderUID string // string form of TelegramUser.ID
	DisplayName string // first_name + " " + last_name (or first_name only)
	AvatarURL   string // photo_url, may be empty
}

// OAuthLoginTxInput carries the parameters for the existing-user session Tx.
type OAuthLoginTxInput struct {
	UserID    [16]byte
	IPAddress string
	UserAgent string
	NewUser   bool
}

// OAuthRegisterTxInput carries the parameters for the new-user registration Tx.
// Email is always empty for Telegram (D-04).
type OAuthRegisterTxInput struct {
	DisplayName string // may be empty
	ProviderUID string
	AvatarURL   string
	IPAddress   string
	UserAgent   string
}

// OAuthAuditInput carries the parameters for standalone audit log writes in
// link and unlink flows.
type OAuthAuditInput struct {
	UserID    [16]byte
	Event     audit.EventType
	IPAddress string
	UserAgent string
	Metadata  map[string]any
}
