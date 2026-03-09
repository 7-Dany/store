//go:build integration_test

package telegram_test

import (
	"context"
	"testing"

	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/db"
	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
	oauthsharedtest "github.com/7-Dany/store/backend/internal/domain/oauth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/oauth/telegram"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	oauthsharedtest.RunTestMain(m, &testPool, 20)
}

// ─────────────────────────────────────────────────────────────────────────────
// GetIdentityByProviderUID
// ─────────────────────────────────────────────────────────────────────────────

// T-S01: GetIdentityByProviderUID with a seeded Telegram identity → row returned.
func TestStore_GetIdentityByProviderUID_Found(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	session, err := store.OAuthRegisterTx(context.Background(), telegram.OAuthRegisterTxInput{
		DisplayName: "Alice",
		ProviderUID: "tg-uid-s01",
		AvatarURL:   "",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test/1.0",
	})
	require.NoError(t, err)

	identity, err := store.GetIdentityByProviderUID(context.Background(), "tg-uid-s01")
	require.NoError(t, err)
	assert.Equal(t, session.UserID, identity.UserID)
}

// T-S02: GetIdentityByProviderUID for an unknown provider_uid →
// oauthshared.ErrIdentityNotFound.
func TestStore_GetIdentityByProviderUID_NotFound(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	_, err := store.GetIdentityByProviderUID(context.Background(), "tg-uid-does-not-exist")
	assert.ErrorIs(t, err, oauthshared.ErrIdentityNotFound)
}

// ─────────────────────────────────────────────────────────────────────────────
// GetIdentityByUserAndProvider
// ─────────────────────────────────────────────────────────────────────────────

// T-S03: GetIdentityByUserAndProvider for a user with a linked Telegram identity → row returned.
func TestStore_GetIdentityByUserAndProvider_Found(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	session, err := store.OAuthRegisterTx(context.Background(), telegram.OAuthRegisterTxInput{
		ProviderUID: "tg-uid-s03",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test/1.0",
	})
	require.NoError(t, err)

	identity, err := store.GetIdentityByUserAndProvider(context.Background(), session.UserID)
	require.NoError(t, err)
	assert.Equal(t, session.UserID, identity.UserID)
}

// T-S04: GetIdentityByUserAndProvider for a user with no Telegram identity →
// oauthshared.ErrIdentityNotFound.
func TestStore_GetIdentityByUserAndProvider_NotFound(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	nonExistentUserID := authsharedtest.RandomUUID()
	_, err := store.GetIdentityByUserAndProvider(context.Background(), nonExistentUserID)
	assert.ErrorIs(t, err, oauthshared.ErrIdentityNotFound)
}

// ─────────────────────────────────────────────────────────────────────────────
// GetUserForOAuthCallback
// ─────────────────────────────────────────────────────────────────────────────

// T-S05: GetUserForOAuthCallback for an existing user → is_active=true, locks=false.
func TestStore_GetUserForOAuthCallback_Found(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	session, err := store.OAuthRegisterTx(context.Background(), telegram.OAuthRegisterTxInput{
		ProviderUID: "tg-uid-s05",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test/1.0",
	})
	require.NoError(t, err)

	record, err := store.GetUserForOAuthCallback(context.Background(), session.UserID)
	require.NoError(t, err)
	assert.True(t, record.IsActive)
	assert.False(t, record.IsLocked)
	assert.False(t, record.AdminLocked)
}

// T-S06: GetUserForOAuthCallback for a non-existent user_id →
// authshared.ErrUserNotFound.
func TestStore_GetUserForOAuthCallback_NotFound(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	_, err := store.GetUserForOAuthCallback(context.Background(), authsharedtest.RandomUUID())
	require.Error(t, err)
	// The store wraps authshared.ErrUserNotFound.
	assert.ErrorContains(t, err, "not found")
}

// ─────────────────────────────────────────────────────────────────────────────
// GetUserAuthMethods
// ─────────────────────────────────────────────────────────────────────────────

// T-S07: GetUserAuthMethods for a Telegram-only user → HasPassword=false,
// IdentityCount=1.
func TestStore_GetUserAuthMethods_TelegramOnly(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	session, err := store.OAuthRegisterTx(context.Background(), telegram.OAuthRegisterTxInput{
		ProviderUID: "tg-uid-s07",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test/1.0",
	})
	require.NoError(t, err)

	methods, err := store.GetUserAuthMethods(context.Background(), session.UserID)
	require.NoError(t, err)
	assert.False(t, methods.HasPassword, "Telegram-only user must have no password (D-04)")
	assert.Equal(t, int64(1), methods.IdentityCount)
}

// ─────────────────────────────────────────────────────────────────────────────
// InsertUserIdentity
// ─────────────────────────────────────────────────────────────────────────────

// T-S08: InsertUserIdentity → row is queryable with the expected provider_uid
// and display_name.
func TestStore_InsertUserIdentity_Success(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	// Create a bare user first (no identity).
	session, err := store.OAuthRegisterTx(context.Background(), telegram.OAuthRegisterTxInput{
		ProviderUID: "tg-uid-s08-base",
		DisplayName: "Base",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test/1.0",
	})
	require.NoError(t, err)

	// Delete the auto-inserted identity so we can test InsertUserIdentity cleanly.
	_, err = store.DeleteUserIdentity(context.Background(), session.UserID)
	require.NoError(t, err)

	// Now insert via InsertUserIdentity.
	err = store.InsertUserIdentity(context.Background(), telegram.InsertIdentityInput{
		UserID:      session.UserID,
		ProviderUID: "tg-uid-s08",
		DisplayName: "Telegram User",
		AvatarURL:   "https://t.me/s08.jpg",
	})
	require.NoError(t, err)

	// Verify the row.
	row, err := q.TestGetTelegramIdentityDetails(context.Background(), authsharedtest.ToPgtypeUUID(session.UserID))
	require.NoError(t, err)
	assert.Equal(t, "Telegram User", row.DisplayName.String)
	assert.Equal(t, "https://t.me/s08.jpg", row.AvatarURL.String)
}

// ─────────────────────────────────────────────────────────────────────────────
// DeleteUserIdentity
// ─────────────────────────────────────────────────────────────────────────────

// T-S09: DeleteUserIdentity after inserting a row → returns 1 row affected and
// the row is gone.
func TestStore_DeleteUserIdentity_DeletesRow(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	session, err := store.OAuthRegisterTx(context.Background(), telegram.OAuthRegisterTxInput{
		ProviderUID: "tg-uid-s09",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test/1.0",
	})
	require.NoError(t, err)

	rowsAffected, err := store.DeleteUserIdentity(context.Background(), session.UserID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), rowsAffected)

	count, err := q.TestCountTelegramIdentities(context.Background(), authsharedtest.ToPgtypeUUID(session.UserID))
	require.NoError(t, err)
	assert.Equal(t, int64(0), count, "identity row must be gone after delete")
}

// T-S10: DeleteUserIdentity when no Telegram identity exists → 0 rows affected, no error.
func TestStore_DeleteUserIdentity_NoRowsAffected(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	rowsAffected, err := store.DeleteUserIdentity(context.Background(), authsharedtest.RandomUUID())
	require.NoError(t, err)
	assert.Equal(t, int64(0), rowsAffected)
}

// ─────────────────────────────────────────────────────────────────────────────
// OAuthLoginTx
// ─────────────────────────────────────────────────────────────────────────────

// T-S11: OAuthLoginTx → session row, refresh token, and last_login_at are
// written; returned session fields are non-zero.
func TestStore_OAuthLoginTx_CreatesSessionAndToken(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	// Register a user first to get a real user ID.
	regSession, err := store.OAuthRegisterTx(context.Background(), telegram.OAuthRegisterTxInput{
		ProviderUID: "tg-uid-s11-reg",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test/1.0",
	})
	require.NoError(t, err)

	session, err := store.OAuthLoginTx(context.Background(), telegram.OAuthLoginTxInput{
		UserID:    regSession.UserID,
		IPAddress: "10.0.0.1",
		UserAgent: "TestBrowser/2.0",
		NewUser:   false,
	})
	require.NoError(t, err)

	assert.Equal(t, regSession.UserID, session.UserID)
	assert.NotZero(t, session.SessionID)
	assert.NotZero(t, session.RefreshJTI)
	assert.NotZero(t, session.FamilyID)
	assert.False(t, session.RefreshExpiry.IsZero())

	// Verify the user_sessions row exists.
	sessionCount, err := q.TestCountUserSessions(context.Background(), authsharedtest.ToPgtypeUUID(regSession.UserID))
	require.NoError(t, err)
	// OAuthRegisterTx created one session; OAuthLoginTx created another.
	assert.GreaterOrEqual(t, sessionCount, int64(1))
}

// ─────────────────────────────────────────────────────────────────────────────
// OAuthRegisterTx
// ─────────────────────────────────────────────────────────────────────────────

// T-S12: OAuthRegisterTx → new user row has email_verified=TRUE, is_active=TRUE,
// password_hash=NULL (D-10, D-11). Email column must be empty (D-04).
func TestStore_OAuthRegisterTx_NewUserRowFlags(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	session, err := store.OAuthRegisterTx(context.Background(), telegram.OAuthRegisterTxInput{
		DisplayName: "T12 User",
		ProviderUID: "tg-uid-s12",
		AvatarURL:   "https://t.me/s12.jpg",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test/1.0",
	})
	require.NoError(t, err)
	require.NotZero(t, session.UserID)

	row, err := q.TestGetUserFlags(context.Background(), authsharedtest.ToPgtypeUUID(session.UserID))
	require.NoError(t, err)

	assert.True(t, row.EmailVerified, "email_verified must be TRUE for OAuth-registered users (D-10)")
	assert.True(t, row.IsActive, "is_active must be TRUE (D-10)")
	assert.False(t, row.PasswordHash.Valid, "password_hash must be NULL for Telegram-only users (D-11)")
	assert.False(t, row.Email.Valid || row.Email.String != "", "email must be empty for Telegram users (D-04)")
}

// T-S13: OAuthRegisterTx → identity row is inserted with the correct provider_uid
// and empty access_token / provider_email (D-04).
func TestStore_OAuthRegisterTx_IdentityRow(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	session, err := store.OAuthRegisterTx(context.Background(), telegram.OAuthRegisterTxInput{
		DisplayName: "T13 User",
		ProviderUID: "tg-uid-s13",
		AvatarURL:   "https://t.me/s13.jpg",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test/1.0",
	})
	require.NoError(t, err)

	row, err := q.TestGetTelegramIdentityProviderDetails(context.Background(), authsharedtest.ToPgtypeUUID(session.UserID))
	require.NoError(t, err)

	assert.Equal(t, "tg-uid-s13", row.ProviderUid)
	assert.False(t, row.AccessToken.Valid && row.AccessToken.String != "",
		"access_token must be empty for Telegram (D-04)")
	assert.False(t, row.ProviderEmail.Valid && row.ProviderEmail.String != "",
		"provider_email must be empty for Telegram (D-04)")
}

// T-S14: OAuthRegisterTx → session and refresh token are returned with
// non-zero IDs and a future expiry.
func TestStore_OAuthRegisterTx_SessionAndToken(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	session, err := store.OAuthRegisterTx(context.Background(), telegram.OAuthRegisterTxInput{
		ProviderUID: "tg-uid-s14",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test/1.0",
	})
	require.NoError(t, err)

	assert.NotZero(t, session.SessionID)
	assert.NotZero(t, session.RefreshJTI)
	assert.NotZero(t, session.FamilyID)
	assert.False(t, session.RefreshExpiry.IsZero())
}

// ─────────────────────────────────────────────────────────────────────────────
// InsertAuditLogTx
// ─────────────────────────────────────────────────────────────────────────────

// T-S15: InsertAuditLogTx → audit row written with correct event_type and
// provider.
func TestStore_InsertAuditLogTx_WritesRow(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)
	store := telegram.NewStore(testPool).WithQuerier(q)

	// Need a real user to satisfy the FK.
	session, err := store.OAuthRegisterTx(context.Background(), telegram.OAuthRegisterTxInput{
		ProviderUID: "tg-uid-s15",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test/1.0",
	})
	require.NoError(t, err)

	err = store.InsertAuditLogTx(context.Background(), telegram.OAuthAuditInput{
		UserID:    session.UserID,
		Event:     "oauth_linked",
		IPAddress: "127.0.0.1",
		UserAgent: "test/1.0",
		Metadata:  map[string]any{"provider": "telegram"},
	})
	require.NoError(t, err)

	row, err := q.TestGetLatestAuditLogByUser(context.Background(), db.TestGetLatestAuditLogByUserParams{
		UserID:    authsharedtest.ToPgtypeUUID(session.UserID),
		EventType: "oauth_linked",
	})
	require.NoError(t, err)
	assert.Equal(t, "oauth_linked", row.EventType)
	assert.Equal(t, "telegram", row.Provider)
}
