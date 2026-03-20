package google

import (
	"context"
	"fmt"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// Store implements Storer using pgx and the generated db.Querier.
type Store struct {
	oauthshared.BaseStore
}

// NewStore creates a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{BaseStore: oauthshared.NewBaseStore(pool)}
}

// WithQuerier returns a shallow copy of s whose Queries field is replaced by q
// and whose TxBound flag is set. Used in tests to scope all writes to a single
// rolled-back transaction.
func (s *Store) WithQuerier(q db.Querier) *Store {
	c := *s
	c.BaseStore = s.BaseStore.WithQuerier(q)
	return &c
}

// ── Simple query methods ─────────────────────────────────────────────────────

// GetIdentityByProviderUID looks up user_identities by (provider=google, provider_uid).
// Returns oauthshared.ErrIdentityNotFound on no-rows.
// AccessToken is intentionally not populated here — it lives in user_identity_tokens
// and is excluded from bulk identity lookups to prevent ciphertext leaking in API
// responses. Callers that need the token must query user_identity_tokens separately.
func (s *Store) GetIdentityByProviderUID(ctx context.Context, providerUID string) (ProviderIdentity, error) {
	row, err := s.Queries.GetIdentityByProviderUID(ctx, db.GetIdentityByProviderUIDParams{
		Provider:    db.AuthProviderGoogle,
		ProviderUid: providerUID,
	})
	if err != nil {
		if s.IsNoRows(err) {
			return ProviderIdentity{}, oauthshared.ErrIdentityNotFound
		}
		return ProviderIdentity{}, telemetry.Store("GetIdentityByProviderUID.query", err)
	}
	return ProviderIdentity{
		ID:            row.ID,
		UserID:        row.UserID.Bytes,
		ProviderEmail: row.ProviderEmail.String,
		DisplayName:   row.DisplayName.String,
		AvatarURL:     row.AvatarURL.String,
	}, nil
}

// GetIdentityByUserAndProvider looks up user_identities by (user_id, provider=google).
// Returns oauthshared.ErrIdentityNotFound on no-rows.
func (s *Store) GetIdentityByUserAndProvider(ctx context.Context, userID [16]byte) (ProviderIdentity, error) {
	row, err := s.Queries.GetIdentityByUserAndProvider(ctx, db.GetIdentityByUserAndProviderParams{
		UserID:   s.ToPgtypeUUID(userID),
		Provider: db.AuthProviderGoogle,
	})
	if err != nil {
		if s.IsNoRows(err) {
			return ProviderIdentity{}, oauthshared.ErrIdentityNotFound
		}
		return ProviderIdentity{}, telemetry.Store("GetIdentityByUserAndProvider.query", err)
	}
	return ProviderIdentity{
		ID:     row.ID,
		UserID: row.UserID.Bytes,
	}, nil
}

// GetUserByEmailForOAuth fetches a user by email for the email-match path.
// Returns oauthshared.ErrIdentityNotFound on no-rows (service treats this as the register path).
func (s *Store) GetUserByEmailForOAuth(ctx context.Context, email string) (OAuthUserRecord, error) {
	row, err := s.Queries.GetUserByEmailForOAuth(ctx, s.ToText(email))
	if err != nil {
		if s.IsNoRows(err) {
			return OAuthUserRecord{}, oauthshared.ErrIdentityNotFound
		}
		return OAuthUserRecord{}, telemetry.Store("GetUserByEmailForOAuth.query", err)
	}
	return OAuthUserRecord{
		ID:          row.ID,
		IsActive:    row.IsActive,
		IsLocked:    row.IsLocked || row.AdminLocked,
		AdminLocked: row.AdminLocked,
	}, nil
}

// GetUserForOAuthCallback fetches a user by ID for the lock guard.
// Returns authshared.ErrUserNotFound on no-rows.
func (s *Store) GetUserForOAuthCallback(ctx context.Context, userID [16]byte) (OAuthUserRecord, error) {
	row, err := s.Queries.GetUserForOAuthCallback(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return OAuthUserRecord{}, authshared.ErrUserNotFound
		}
		return OAuthUserRecord{}, telemetry.Store("GetUserForOAuthCallback.query", err)
	}
	return OAuthUserRecord{
		ID:          row.ID,
		IsActive:    row.IsActive,
		IsLocked:    row.IsLocked || row.AdminLocked,
		AdminLocked: row.AdminLocked,
	}, nil
}

// GetUserAuthMethods returns HasPassword and IdentityCount for the unlink guard.
// Returns authshared.ErrUserNotFound on no-rows.
func (s *Store) GetUserAuthMethods(ctx context.Context, userID [16]byte) (UserAuthMethods, error) {
	row, err := s.Queries.GetUserAuthMethods(ctx, s.ToPgtypeUUID(userID))
	if err != nil {
		if s.IsNoRows(err) {
			return UserAuthMethods{}, authshared.ErrUserNotFound
		}
		return UserAuthMethods{}, telemetry.Store("GetUserAuthMethods.query", err)
	}
	hasPassword := false
	if row.HasPassword != nil {
		if b, ok := row.HasPassword.(bool); ok {
			hasPassword = b
		} else {
			log.Warn(ctx, "GetUserAuthMethods: unexpected HasPassword type", "type", fmt.Sprintf("%T", row.HasPassword))
		}
	}
	return UserAuthMethods{
		HasPassword:   hasPassword,
		IdentityCount: row.IdentityCount,
	}, nil
}

// UpsertUserIdentity inserts or updates a user_identities metadata row, then
// persists the encrypted access token in the companion user_identity_tokens table.
// Both writes are wrapped in a transaction so they always succeed or fail together.
func (s *Store) UpsertUserIdentity(ctx context.Context, in UpsertIdentityInput) error {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return telemetry.Store("UpsertUserIdentity.begin_tx", err)
	}

	row, err := h.Q.UpsertUserIdentity(ctx, db.UpsertUserIdentityParams{
		UserID:        s.ToPgtypeUUID(in.UserID),
		Provider:      db.AuthProviderGoogle,
		ProviderUid:   in.ProviderUID,
		ProviderEmail: s.ToText(in.ProviderEmail),
		DisplayName:   s.ToText(in.DisplayName),
		AvatarURL:     s.ToText(in.AvatarURL),
	})
	if err != nil {
		h.Rollback()
		return telemetry.Store("UpsertUserIdentity.upsert", err)
	}

	// Persist the encrypted access token. chk_uit_access_token_expiry_coherent
	// requires expires_at to be non-NULL whenever access_token is non-NULL;
	// use a 1-hour default (Google's standard token lifetime).
	var expiresAt pgtype.Timestamptz
	if in.AccessToken != "" {
		expiresAt = pgtype.Timestamptz{Time: time.Now().UTC().Add(time.Hour), Valid: true}
	}
	if err := h.Q.UpsertUserIdentityTokens(ctx, db.UpsertUserIdentityTokensParams{
		IdentityID:           s.UUIDToPgtypeUUID(row.ID),
		AccessToken:          pgtype.Text{String: in.AccessToken, Valid: in.AccessToken != ""},
		AccessTokenExpiresAt: expiresAt,
		RefreshTokenProvider: pgtype.Text{},
	}); err != nil {
		h.Rollback()
		return telemetry.Store("UpsertUserIdentity.upsert_tokens", err)
	}

	if err := h.Commit(); err != nil {
		return telemetry.Store("UpsertUserIdentity.commit", err)
	}
	return nil
}

// DeleteUserIdentity deletes a user_identities row by (user_id, provider=google).
// Returns (rowsAffected, error); the service checks rowsAffected == 0 → ErrIdentityNotFound.
func (s *Store) DeleteUserIdentity(ctx context.Context, userID [16]byte) (int64, error) {
	n, err := s.Queries.DeleteUserIdentity(ctx, db.DeleteUserIdentityParams{
		UserID:   s.ToPgtypeUUID(userID),
		Provider: db.AuthProviderGoogle,
	})
	if err != nil {
		return 0, telemetry.Store("DeleteUserIdentity.delete", err)
	}
	return n, nil
}

// ── Transaction methods ──────────────────────────────────────────────────────

// OAuthLoginTx creates a session + refresh token + stamps last_login_at +
// writes an oauth_login audit row — all in a single transaction.
// The audit InsertAuditLog call uses context.WithoutCancel (D-17).
func (s *Store) OAuthLoginTx(ctx context.Context, in OAuthLoginTxInput) (oauthshared.LoggedInSession, error) {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthLoginTx.begin_tx", err)
	}

	userPgUUID := s.ToPgtypeUUID(in.UserID)

	// 1. Create session row.
	sessionRow, err := h.Q.CreateUserSession(ctx, db.CreateUserSessionParams{
		UserID:       userPgUUID,
		AuthProvider: db.AuthProviderGoogle,
		IpAddress:    s.IPToNullable(in.IPAddress),
		UserAgent:    s.ToText(s.TruncateUserAgent(in.UserAgent)),
	})
	if err != nil {
		h.Rollback()
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthLoginTx.create_session", err)
	}

	// 2. Issue root refresh token.
	tokenRow, err := h.Q.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    userPgUUID,
		SessionID: s.UUIDToPgtypeUUID(sessionRow.ID),
	})
	if err != nil {
		h.Rollback()
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthLoginTx.create_token", err)
	}

	// 3. Stamp last_login_at.
	if err := h.Q.UpdateLastLoginAt(ctx, userPgUUID); err != nil {
		h.Rollback()
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthLoginTx.update_login", err)
	}

	// 3a. Backfill avatar_url into users if not already set (e.g. accounts that
	// registered via email/password before linking Google). The query is a no-op
	// when users.avatar_url IS NOT NULL, so it never overwrites a custom avatar.
	if in.AvatarURL != "" {
		if err := h.Q.UpdateUserAvatarIfNull(ctx, db.UpdateUserAvatarIfNullParams{
			AvatarURL: s.ToText(in.AvatarURL),
			UserID:    userPgUUID,
		}); err != nil {
			h.Rollback()
			return oauthshared.LoggedInSession{}, telemetry.Store("OAuthLoginTx.backfill_avatar", err)
		}
	}

	// 4. Audit log — use context.WithoutCancel so a client disconnect cannot
	// abort the write (D-17).
	if err := h.Q.InsertAuditLog(context.WithoutCancel(ctx), db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventOAuthLogin),
		Provider:  db.AuthProviderGoogle,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  s.MustJSON(map[string]any{"provider": "google", "new_user": in.NewUser}),
	}); err != nil {
		h.Rollback()
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthLoginTx.audit", err)
	}

	if err := h.Commit(); err != nil {
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthLoginTx.commit", err)
	}

	return oauthshared.LoggedInSession{
		UserID:        in.UserID,
		SessionID:     s.UUIDToBytes(sessionRow.ID),
		RefreshJTI:    tokenRow.Jti.Bytes,
		FamilyID:      tokenRow.FamilyID.Bytes,
		RefreshExpiry: tokenRow.ExpiresAt.Time.UTC(),
	}, nil
}

// OAuthRegisterTx creates a new user row + identity + session + refresh token +
// last_login_at + audit row — all in a single transaction.
func (s *Store) OAuthRegisterTx(ctx context.Context, in OAuthRegisterTxInput) (oauthshared.LoggedInSession, error) {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthRegisterTx.begin_tx", err)
	}

	// 1. Create user row.
	newUserID, err := h.Q.CreateOAuthUser(ctx, db.CreateOAuthUserParams{
		Email:       s.ToText(in.Email),
		DisplayName: s.ToText(in.DisplayName),
		AvatarURL:   s.ToText(in.AvatarURL),
	})
	if err != nil {
		h.Rollback()
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthRegisterTx.create_user", err)
	}

	userPgUUID := s.UUIDToPgtypeUUID(newUserID)

	// 2. Upsert identity metadata row — call the querier directly to stay in the same TX.
	identityRow, err := h.Q.UpsertUserIdentity(ctx, db.UpsertUserIdentityParams{
		UserID:        userPgUUID,
		Provider:      db.AuthProviderGoogle,
		ProviderUid:   in.ProviderUID,
		ProviderEmail: s.ToText(in.ProviderEmail),
		DisplayName:   s.ToText(in.DisplayName),
		AvatarURL:     s.ToText(in.AvatarURL),
	})
	if err != nil {
		h.Rollback()
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthRegisterTx.upsert_identity", err)
	}

	// 2a. Persist encrypted access token in the companion tokens table.
	var tokenExpiresAt pgtype.Timestamptz
	if in.AccessToken != "" {
		tokenExpiresAt = pgtype.Timestamptz{Time: time.Now().UTC().Add(time.Hour), Valid: true}
	}
	if err := h.Q.UpsertUserIdentityTokens(ctx, db.UpsertUserIdentityTokensParams{
		IdentityID:           s.UUIDToPgtypeUUID(identityRow.ID),
		AccessToken:          pgtype.Text{String: in.AccessToken, Valid: in.AccessToken != ""},
		AccessTokenExpiresAt: tokenExpiresAt,
		RefreshTokenProvider: pgtype.Text{},
	}); err != nil {
		h.Rollback()
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthRegisterTx.upsert_tokens", err)
	}

	// 3. Create session row.
	sessionRow, err := h.Q.CreateUserSession(ctx, db.CreateUserSessionParams{
		UserID:       userPgUUID,
		AuthProvider: db.AuthProviderGoogle,
		IpAddress:    s.IPToNullable(in.IPAddress),
		UserAgent:    s.ToText(s.TruncateUserAgent(in.UserAgent)),
	})
	if err != nil {
		h.Rollback()
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthRegisterTx.create_session", err)
	}

	// 4. Issue root refresh token.
	tokenRow, err := h.Q.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    userPgUUID,
		SessionID: s.UUIDToPgtypeUUID(sessionRow.ID),
	})
	if err != nil {
		h.Rollback()
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthRegisterTx.create_token", err)
	}

	// 5. Stamp last_login_at.
	if err := h.Q.UpdateLastLoginAt(ctx, userPgUUID); err != nil {
		h.Rollback()
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthRegisterTx.update_login", err)
	}

	// 6. Audit log — context.WithoutCancel (D-17).
	if err := h.Q.InsertAuditLog(context.WithoutCancel(ctx), db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventOAuthLogin),
		Provider:  db.AuthProviderGoogle,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  s.MustJSON(map[string]any{"provider": "google", "new_user": true}),
	}); err != nil {
		h.Rollback()
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthRegisterTx.audit", err)
	}

	if err := h.Commit(); err != nil {
		return oauthshared.LoggedInSession{}, telemetry.Store("OAuthRegisterTx.commit", err)
	}

	return oauthshared.LoggedInSession{
		UserID:        newUserID,
		SessionID:     s.UUIDToBytes(sessionRow.ID),
		RefreshJTI:    tokenRow.Jti.Bytes,
		FamilyID:      tokenRow.FamilyID.Bytes,
		RefreshExpiry: tokenRow.ExpiresAt.Time.UTC(),
	}, nil
}

// InsertAuditLogTx writes a single audit row for link and unlink flows.
// The caller must pass a context.WithoutCancel ctx (D-17 — enforced by convention).
func (s *Store) InsertAuditLogTx(ctx context.Context, in OAuthAuditInput) error {
	h, err := s.BeginOrBind(ctx)
	if err != nil {
		return telemetry.Store("InsertAuditLogTx.begin_tx", err)
	}

	if err := h.Q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		UserID:    s.ToPgtypeUUID(in.UserID),
		EventType: string(in.Event),
		Provider:  db.AuthProviderGoogle,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  s.MustJSON(in.Metadata),
	}); err != nil {
		h.Rollback()
		return telemetry.Store("InsertAuditLogTx.audit", err)
	}

	if err := h.Commit(); err != nil {
		return telemetry.Store("InsertAuditLogTx.commit", err)
	}
	return nil
}
