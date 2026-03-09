//go:build integration_test

package google_test

import (
	"context"
	"testing"

	"github.com/7-Dany/store/backend/internal/db"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/oauth/google"
	oauthsharedtest "github.com/7-Dany/store/backend/internal/domain/oauth/shared/testutil"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	oauthsharedtest.RunTestMain(m, &testPool, 20)
}

// T-36: OAuthRegisterTx → new user row has email_verified=TRUE, is_active=TRUE,
// password_hash=NULL (D-10, D-11).
func TestStore_OAuthRegisterTx_NewUserRow(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)

	store := google.NewStore(testPool).WithQuerier(q)
	session, err := store.OAuthRegisterTx(context.Background(), google.OAuthRegisterTxInput{
		Email:         "register-t36@example.com",
		DisplayName:   "T36 User",
		ProviderUID:   "google-uid-t36",
		ProviderEmail: "register-t36@example.com",
		AvatarURL:     "https://example.com/avatar.jpg",
		AccessToken:   "enc:tok36",
		IPAddress:     "127.0.0.1",
		UserAgent:     "test/1.0",
	})
	require.NoError(t, err)
	require.NotZero(t, session.UserID)

	row, err := q.TestGetUserFlags(context.Background(), authsharedtest.ToPgtypeUUID(session.UserID))
	require.NoError(t, err)

	assert.True(t, row.EmailVerified, "email_verified must be TRUE for OAuth-registered users (D-10)")
	assert.True(t, row.IsActive, "is_active must be TRUE for OAuth-registered users (D-10)")
	assert.False(t, row.PasswordHash.Valid, "password_hash must be NULL for Google-only users (D-11)")
}

// T-37: UpsertUserIdentity called twice with a different display_name → DB row updated, not duplicated.
func TestStore_UpsertUserIdentity_UpdatesExistingRow(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)

	userID, err := q.CreateOAuthUser(context.Background(), db.CreateOAuthUserParams{})
	require.NoError(t, err)
	uid := [16]byte(userID)

	store := google.NewStore(testPool).WithQuerier(q)

	err = store.UpsertUserIdentity(context.Background(), google.UpsertIdentityInput{
		UserID:      uid,
		Provider:    "google",
		ProviderUID: "google-uid-t37",
		DisplayName: "Original Name",
		AccessToken: "enc:tok-a",
	})
	require.NoError(t, err)

	err = store.UpsertUserIdentity(context.Background(), google.UpsertIdentityInput{
		UserID:      uid,
		Provider:    "google",
		ProviderUID: "google-uid-t37",
		DisplayName: "Updated Name",
		AccessToken: "enc:tok-b",
	})
	require.NoError(t, err)

	displayRow, err := q.TestGetGoogleIdentityDisplayName(context.Background(), authsharedtest.ToPgtypeUUID(uid))
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", displayRow.String)

	count, err := q.TestCountGoogleIdentities(context.Background(), authsharedtest.ToPgtypeUUID(uid))
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "upsert must not create duplicate rows")
}

// T-38: Email-match path — seed user with email; UpsertUserIdentity to link
// the identity; call OAuthLoginTx → user_identities row linked to seeded user.
func TestStore_OAuthLoginTx_EmailMatchPath(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)

	seededID, err := q.CreateOAuthUser(context.Background(), db.CreateOAuthUserParams{
		Email: pgtype.Text{String: "email-match-t38@example.com", Valid: true},
	})
	require.NoError(t, err)
	uid := [16]byte(seededID)

	store := google.NewStore(testPool).WithQuerier(q)

	err = store.UpsertUserIdentity(context.Background(), google.UpsertIdentityInput{
		UserID:        uid,
		Provider:      "google",
		ProviderUID:   "google-uid-t38",
		ProviderEmail: "email-match-t38@example.com",
		AccessToken:   "enc:tok38",
	})
	require.NoError(t, err)

	session, err := store.OAuthLoginTx(context.Background(), google.OAuthLoginTxInput{
		UserID:    uid,
		IPAddress: "10.0.0.1",
		UserAgent: "test/1.0",
		NewUser:   false,
	})
	require.NoError(t, err)
	assert.Equal(t, uid, session.UserID)

	identityRow, err := q.TestGetIdentityUserIDByProviderUID(context.Background(), "google-uid-t38")
	require.NoError(t, err)
	assert.Equal(t, uid, identityRow.Bytes, "identity must be linked to the seeded user")
}

// T-50: GetUserAuthMethods → correct HasPassword / IdentityCount for seeded user.
func TestStore_GetUserAuthMethods(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)

	userID, err := q.CreateOAuthUser(context.Background(), db.CreateOAuthUserParams{
		Email: pgtype.Text{String: "authmethods-t50@example.com", Valid: true},
	})
	require.NoError(t, err)
	uid := [16]byte(userID)

	store := google.NewStore(testPool).WithQuerier(q)

	err = store.UpsertUserIdentity(context.Background(), google.UpsertIdentityInput{
		UserID:      uid,
		Provider:    "google",
		ProviderUID: "google-uid-t50",
		AccessToken: "enc:tok50",
	})
	require.NoError(t, err)

	methods, err := store.GetUserAuthMethods(context.Background(), uid)
	require.NoError(t, err)

	assert.False(t, methods.HasPassword, "OAuth-only user should have no password (D-11)")
	assert.Equal(t, int64(1), methods.IdentityCount)
}

// T-51: DeleteUserIdentity → row gone from user_identities.
func TestStore_DeleteUserIdentity(t *testing.T) {
	_, q := authsharedtest.MustBeginTx(t, testPool)

	userID, err := q.CreateOAuthUser(context.Background(), db.CreateOAuthUserParams{})
	require.NoError(t, err)
	uid := [16]byte(userID)

	store := google.NewStore(testPool).WithQuerier(q)

	err = store.UpsertUserIdentity(context.Background(), google.UpsertIdentityInput{
		UserID:      uid,
		Provider:    "google",
		ProviderUID: "google-uid-t51",
		AccessToken: "enc:tok51",
	})
	require.NoError(t, err)

	rowsAffected, err := store.DeleteUserIdentity(context.Background(), uid)
	require.NoError(t, err)
	assert.Equal(t, int64(1), rowsAffected)

	count, err := q.TestCountGoogleIdentities(context.Background(), authsharedtest.ToPgtypeUUID(uid))
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}
