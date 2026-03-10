// Package oauthsharedtest provides test-only helpers shared across all oauth
// feature sub-packages. It must never be imported by production code.
package oauthsharedtest

import (
	"context"
	"errors"

	"github.com/7-Dany/store/backend/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrProxy is the sentinel error returned by QuerierProxy when a Fail* flag is set.
var ErrProxy = errors.New("querier_proxy: injected error")

// QuerierProxy embeds db.Querier so every method not explicitly overridden below
// is forwarded automatically to the underlying implementation. Fail* flags allow
// tests to inject errors for specific queries without a real DB failure.
//
// Usage in tests:
//
//	proxy := oauthsharedtest.NewQuerierProxy(realQuerier)
//	proxy.FailUpsertUserIdentity = true
type QuerierProxy struct {
	db.Querier // embedded — auto-forwards any method not overridden below

	// ── google ───────────────────────────────────────────────────────────────
	FailGetIdentityByProviderUID     bool
	FailGetIdentityByUserAndProvider bool
	FailUpsertUserIdentity           bool
	FailDeleteUserIdentity           bool
	FailGetUserAuthMethods           bool
	FailCreateOAuthUser              bool
	FailGetUserByEmailForOAuth       bool
	FailGetUserForOAuthCallback      bool

	// ── oauth tx (shared queries) ────────────────────────────────────────────
	FailCreateUserSession bool
	FailCreateRefreshToken bool
	FailUpdateLastLoginAt  bool
	FailInsertAuditLog     bool
	// InsertAuditLogFailOnCall, when non-zero, causes InsertAuditLog to fail only
	// on the Nth call (1-based). When zero and FailInsertAuditLog is true, every call fails.
	InsertAuditLogFailOnCall int
	InsertAuditLogCallCount  int
}

// compile-time interface check.
var _ db.Querier = (*QuerierProxy)(nil)

// NewQuerierProxy wraps base with a zero-valued QuerierProxy.
func NewQuerierProxy(base db.Querier) *QuerierProxy {
	return &QuerierProxy{Querier: base}
}

// ── google ────────────────────────────────────────────────────────────────────

func (p *QuerierProxy) GetIdentityByProviderUID(ctx context.Context, arg db.GetIdentityByProviderUIDParams) (db.GetIdentityByProviderUIDRow, error) {
	if p.FailGetIdentityByProviderUID {
		return db.GetIdentityByProviderUIDRow{}, ErrProxy
	}
	return p.Querier.GetIdentityByProviderUID(ctx, arg)
}

func (p *QuerierProxy) GetIdentityByUserAndProvider(ctx context.Context, arg db.GetIdentityByUserAndProviderParams) (db.GetIdentityByUserAndProviderRow, error) {
	if p.FailGetIdentityByUserAndProvider {
		return db.GetIdentityByUserAndProviderRow{}, ErrProxy
	}
	return p.Querier.GetIdentityByUserAndProvider(ctx, arg)
}

func (p *QuerierProxy) UpsertUserIdentity(ctx context.Context, arg db.UpsertUserIdentityParams) (db.UpsertUserIdentityRow, error) {
	if p.FailUpsertUserIdentity {
		return db.UpsertUserIdentityRow{}, ErrProxy
	}
	return p.Querier.UpsertUserIdentity(ctx, arg)
}

func (p *QuerierProxy) DeleteUserIdentity(ctx context.Context, arg db.DeleteUserIdentityParams) (int64, error) {
	if p.FailDeleteUserIdentity {
		return 0, ErrProxy
	}
	return p.Querier.DeleteUserIdentity(ctx, arg)
}

func (p *QuerierProxy) GetUserAuthMethods(ctx context.Context, userID pgtype.UUID) (db.GetUserAuthMethodsRow, error) {
	if p.FailGetUserAuthMethods {
		return db.GetUserAuthMethodsRow{}, ErrProxy
	}
	return p.Querier.GetUserAuthMethods(ctx, userID)
}

func (p *QuerierProxy) CreateOAuthUser(ctx context.Context, arg db.CreateOAuthUserParams) (uuid.UUID, error) {
	if p.FailCreateOAuthUser {
		return uuid.UUID{}, ErrProxy
	}
	return p.Querier.CreateOAuthUser(ctx, arg)
}

func (p *QuerierProxy) GetUserByEmailForOAuth(ctx context.Context, email pgtype.Text) (db.GetUserByEmailForOAuthRow, error) {
	if p.FailGetUserByEmailForOAuth {
		return db.GetUserByEmailForOAuthRow{}, ErrProxy
	}
	return p.Querier.GetUserByEmailForOAuth(ctx, email)
}

func (p *QuerierProxy) GetUserForOAuthCallback(ctx context.Context, userID pgtype.UUID) (db.GetUserForOAuthCallbackRow, error) {
	if p.FailGetUserForOAuthCallback {
		return db.GetUserForOAuthCallbackRow{}, ErrProxy
	}
	return p.Querier.GetUserForOAuthCallback(ctx, userID)
}

// ── oauth tx (shared queries) ─────────────────────────────────────────────────

func (p *QuerierProxy) CreateUserSession(ctx context.Context, arg db.CreateUserSessionParams) (db.CreateUserSessionRow, error) {
	if p.FailCreateUserSession {
		return db.CreateUserSessionRow{}, ErrProxy
	}
	return p.Querier.CreateUserSession(ctx, arg)
}

func (p *QuerierProxy) CreateRefreshToken(ctx context.Context, arg db.CreateRefreshTokenParams) (db.CreateRefreshTokenRow, error) {
	if p.FailCreateRefreshToken {
		return db.CreateRefreshTokenRow{}, ErrProxy
	}
	return p.Querier.CreateRefreshToken(ctx, arg)
}

func (p *QuerierProxy) UpdateLastLoginAt(ctx context.Context, userID pgtype.UUID) error {
	if p.FailUpdateLastLoginAt {
		return ErrProxy
	}
	return p.Querier.UpdateLastLoginAt(ctx, userID)
}

func (p *QuerierProxy) InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) error {
	if p.FailInsertAuditLog {
		p.InsertAuditLogCallCount++
		if p.InsertAuditLogFailOnCall == 0 || p.InsertAuditLogCallCount == p.InsertAuditLogFailOnCall {
			return ErrProxy
		}
	}
	return p.Querier.InsertAuditLog(ctx, arg)
}
