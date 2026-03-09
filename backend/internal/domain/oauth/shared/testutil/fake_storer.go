// Package oauthsharedtest provides test-only helpers shared across all oauth
// feature sub-packages. It must never be imported by production code.
package oauthsharedtest

import (
	"context"

	"github.com/7-Dany/store/backend/internal/domain/oauth/google"
	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
	"github.com/7-Dany/store/backend/internal/domain/oauth/telegram"
)

// ─────────────────────────────────────────────────────────────────────────────
// GoogleFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// GoogleFakeStorer is a hand-written implementation of google.Storer for service
// unit tests. Each method delegates to its Fn field if non-nil, otherwise
// returns a safe default so tests only configure the fields they care about.
type GoogleFakeStorer struct {
	GetIdentityByProviderUIDFn     func(ctx context.Context, providerUID string) (google.ProviderIdentity, error)
	GetIdentityByUserAndProviderFn func(ctx context.Context, userID [16]byte) (google.ProviderIdentity, error)
	GetUserByEmailForOAuthFn       func(ctx context.Context, email string) (google.OAuthUserRecord, error)
	GetUserForOAuthCallbackFn      func(ctx context.Context, userID [16]byte) (google.OAuthUserRecord, error)
	GetUserAuthMethodsFn           func(ctx context.Context, userID [16]byte) (google.UserAuthMethods, error)
	OAuthLoginTxFn                 func(ctx context.Context, in google.OAuthLoginTxInput) (oauthshared.LoggedInSession, error)
	OAuthRegisterTxFn              func(ctx context.Context, in google.OAuthRegisterTxInput) (oauthshared.LoggedInSession, error)
	UpsertUserIdentityFn           func(ctx context.Context, in google.UpsertIdentityInput) error
	DeleteUserIdentityFn           func(ctx context.Context, userID [16]byte) (int64, error)
	InsertAuditLogTxFn             func(ctx context.Context, in google.OAuthAuditInput) error
}

// compile-time interface check.
var _ google.Storer = (*GoogleFakeStorer)(nil)

// GetIdentityByProviderUID delegates to GetIdentityByProviderUIDFn if set.
// Default: returns ErrIdentityNotFound — signals "no existing identity".
func (f *GoogleFakeStorer) GetIdentityByProviderUID(ctx context.Context, providerUID string) (google.ProviderIdentity, error) {
	if f.GetIdentityByProviderUIDFn != nil {
		return f.GetIdentityByProviderUIDFn(ctx, providerUID)
	}
	return google.ProviderIdentity{}, oauthshared.ErrIdentityNotFound
}

// GetIdentityByUserAndProvider delegates to GetIdentityByUserAndProviderFn if set.
// Default: returns ErrIdentityNotFound.
func (f *GoogleFakeStorer) GetIdentityByUserAndProvider(ctx context.Context, userID [16]byte) (google.ProviderIdentity, error) {
	if f.GetIdentityByUserAndProviderFn != nil {
		return f.GetIdentityByUserAndProviderFn(ctx, userID)
	}
	return google.ProviderIdentity{}, oauthshared.ErrIdentityNotFound
}

// GetUserByEmailForOAuth delegates to GetUserByEmailForOAuthFn if set.
// Default: returns ErrIdentityNotFound — signals "no email match" (register path).
func (f *GoogleFakeStorer) GetUserByEmailForOAuth(ctx context.Context, email string) (google.OAuthUserRecord, error) {
	if f.GetUserByEmailForOAuthFn != nil {
		return f.GetUserByEmailForOAuthFn(ctx, email)
	}
	return google.OAuthUserRecord{}, oauthshared.ErrIdentityNotFound
}

// GetUserForOAuthCallback delegates to GetUserForOAuthCallbackFn if set.
// Default: returns an active, unlocked user.
func (f *GoogleFakeStorer) GetUserForOAuthCallback(ctx context.Context, userID [16]byte) (google.OAuthUserRecord, error) {
	if f.GetUserForOAuthCallbackFn != nil {
		return f.GetUserForOAuthCallbackFn(ctx, userID)
	}
	return google.OAuthUserRecord{IsActive: true}, nil
}

// GetUserAuthMethods delegates to GetUserAuthMethodsFn if set.
// Default: returns HasPassword=false, IdentityCount=2 — safely unlinkable.
func (f *GoogleFakeStorer) GetUserAuthMethods(ctx context.Context, userID [16]byte) (google.UserAuthMethods, error) {
	if f.GetUserAuthMethodsFn != nil {
		return f.GetUserAuthMethodsFn(ctx, userID)
	}
	return google.UserAuthMethods{HasPassword: false, IdentityCount: 2}, nil
}

// OAuthLoginTx delegates to OAuthLoginTxFn if set.
// Default: returns zero LoggedInSession and nil error.
func (f *GoogleFakeStorer) OAuthLoginTx(ctx context.Context, in google.OAuthLoginTxInput) (oauthshared.LoggedInSession, error) {
	if f.OAuthLoginTxFn != nil {
		return f.OAuthLoginTxFn(ctx, in)
	}
	return oauthshared.LoggedInSession{}, nil
}

// OAuthRegisterTx delegates to OAuthRegisterTxFn if set.
// Default: returns zero LoggedInSession and nil error.
func (f *GoogleFakeStorer) OAuthRegisterTx(ctx context.Context, in google.OAuthRegisterTxInput) (oauthshared.LoggedInSession, error) {
	if f.OAuthRegisterTxFn != nil {
		return f.OAuthRegisterTxFn(ctx, in)
	}
	return oauthshared.LoggedInSession{}, nil
}

// UpsertUserIdentity delegates to UpsertUserIdentityFn if set.
// Default: returns nil error.
func (f *GoogleFakeStorer) UpsertUserIdentity(ctx context.Context, in google.UpsertIdentityInput) error {
	if f.UpsertUserIdentityFn != nil {
		return f.UpsertUserIdentityFn(ctx, in)
	}
	return nil
}

// DeleteUserIdentity delegates to DeleteUserIdentityFn if set.
// Default: returns (1, nil) — one row deleted.
func (f *GoogleFakeStorer) DeleteUserIdentity(ctx context.Context, userID [16]byte) (int64, error) {
	if f.DeleteUserIdentityFn != nil {
		return f.DeleteUserIdentityFn(ctx, userID)
	}
	return 1, nil
}

// InsertAuditLogTx delegates to InsertAuditLogTxFn if set.
// Default: returns nil error.
func (f *GoogleFakeStorer) InsertAuditLogTx(ctx context.Context, in google.OAuthAuditInput) error {
	if f.InsertAuditLogTxFn != nil {
		return f.InsertAuditLogTxFn(ctx, in)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// TelegramFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

// TelegramFakeStorer is a hand-written implementation of telegram.Storer for
// service unit tests. Each method delegates to its Fn field if non-nil, otherwise
// returns a safe default so tests only configure the fields they care about.
type TelegramFakeStorer struct {
	GetIdentityByProviderUIDFn     func(ctx context.Context, providerUID string) (telegram.ProviderIdentity, error)
	GetIdentityByUserAndProviderFn func(ctx context.Context, userID [16]byte) (telegram.ProviderIdentity, error)
	GetUserForOAuthCallbackFn      func(ctx context.Context, userID [16]byte) (telegram.OAuthUserRecord, error)
	GetUserAuthMethodsFn           func(ctx context.Context, userID [16]byte) (telegram.UserAuthMethods, error)
	InsertUserIdentityFn           func(ctx context.Context, in telegram.InsertIdentityInput) error
	DeleteUserIdentityFn           func(ctx context.Context, userID [16]byte) (int64, error)
	OAuthLoginTxFn                 func(ctx context.Context, in telegram.OAuthLoginTxInput) (oauthshared.LoggedInSession, error)
	OAuthRegisterTxFn              func(ctx context.Context, in telegram.OAuthRegisterTxInput) (oauthshared.LoggedInSession, error)
	InsertAuditLogTxFn             func(ctx context.Context, in telegram.OAuthAuditInput) error
}

// compile-time interface check.
var _ telegram.Storer = (*TelegramFakeStorer)(nil)

// GetIdentityByProviderUID delegates to GetIdentityByProviderUIDFn if set.
// Default: returns ErrIdentityNotFound — signals "no existing identity".
func (f *TelegramFakeStorer) GetIdentityByProviderUID(ctx context.Context, providerUID string) (telegram.ProviderIdentity, error) {
	if f.GetIdentityByProviderUIDFn != nil {
		return f.GetIdentityByProviderUIDFn(ctx, providerUID)
	}
	return telegram.ProviderIdentity{}, oauthshared.ErrIdentityNotFound
}

// GetIdentityByUserAndProvider delegates to GetIdentityByUserAndProviderFn if set.
// Default: returns ErrIdentityNotFound.
func (f *TelegramFakeStorer) GetIdentityByUserAndProvider(ctx context.Context, userID [16]byte) (telegram.ProviderIdentity, error) {
	if f.GetIdentityByUserAndProviderFn != nil {
		return f.GetIdentityByUserAndProviderFn(ctx, userID)
	}
	return telegram.ProviderIdentity{}, oauthshared.ErrIdentityNotFound
}

// GetUserForOAuthCallback delegates to GetUserForOAuthCallbackFn if set.
// Default: returns an active, unlocked user.
func (f *TelegramFakeStorer) GetUserForOAuthCallback(ctx context.Context, userID [16]byte) (telegram.OAuthUserRecord, error) {
	if f.GetUserForOAuthCallbackFn != nil {
		return f.GetUserForOAuthCallbackFn(ctx, userID)
	}
	return telegram.OAuthUserRecord{IsActive: true}, nil
}

// GetUserAuthMethods delegates to GetUserAuthMethodsFn if set.
// Default: returns HasPassword=false, IdentityCount=2 — safely unlinkable.
func (f *TelegramFakeStorer) GetUserAuthMethods(ctx context.Context, userID [16]byte) (telegram.UserAuthMethods, error) {
	if f.GetUserAuthMethodsFn != nil {
		return f.GetUserAuthMethodsFn(ctx, userID)
	}
	return telegram.UserAuthMethods{HasPassword: false, IdentityCount: 2}, nil
}

// InsertUserIdentity delegates to InsertUserIdentityFn if set.
// Default: returns nil error.
func (f *TelegramFakeStorer) InsertUserIdentity(ctx context.Context, in telegram.InsertIdentityInput) error {
	if f.InsertUserIdentityFn != nil {
		return f.InsertUserIdentityFn(ctx, in)
	}
	return nil
}

// DeleteUserIdentity delegates to DeleteUserIdentityFn if set.
// Default: returns (1, nil) — one row deleted.
func (f *TelegramFakeStorer) DeleteUserIdentity(ctx context.Context, userID [16]byte) (int64, error) {
	if f.DeleteUserIdentityFn != nil {
		return f.DeleteUserIdentityFn(ctx, userID)
	}
	return 1, nil
}

// OAuthLoginTx delegates to OAuthLoginTxFn if set.
// Default: returns zero LoggedInSession and nil error.
func (f *TelegramFakeStorer) OAuthLoginTx(ctx context.Context, in telegram.OAuthLoginTxInput) (oauthshared.LoggedInSession, error) {
	if f.OAuthLoginTxFn != nil {
		return f.OAuthLoginTxFn(ctx, in)
	}
	return oauthshared.LoggedInSession{}, nil
}

// OAuthRegisterTx delegates to OAuthRegisterTxFn if set.
// Default: returns zero LoggedInSession and nil error.
func (f *TelegramFakeStorer) OAuthRegisterTx(ctx context.Context, in telegram.OAuthRegisterTxInput) (oauthshared.LoggedInSession, error) {
	if f.OAuthRegisterTxFn != nil {
		return f.OAuthRegisterTxFn(ctx, in)
	}
	return oauthshared.LoggedInSession{}, nil
}

// InsertAuditLogTx delegates to InsertAuditLogTxFn if set.
// Default: returns nil error.
func (f *TelegramFakeStorer) InsertAuditLogTx(ctx context.Context, in telegram.OAuthAuditInput) error {
	if f.InsertAuditLogTxFn != nil {
		return f.InsertAuditLogTxFn(ctx, in)
	}
	return nil
}
