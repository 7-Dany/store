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
	tx, q := authsharedtest.MustBeginTx(t, testPool)

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

	var emailVerified bool
	var passwordHash *string
	var isActive bool
	err = tx.QueryRow(context.Background(),
		`SELECT email_verified, password_hash, is_active FROM users WHERE id = $1`,
		authsharedtest.ToPgtypeUUID(session.UserID),
	).Scan(&emailVerified, &passwordHash, &isActive)
	require.NoError(t, err)

	assert.True(t, emailVerified, "email_verified must be TRUE for OAuth-registered users (D-10)")
	assert.True(t, isActive, "is_active must be TRUE for OAuth-registered users (D-10)")
	assert.Nil(t, passwordHash, "password_hash must be NULL for Google-only users (D-11)")
}

// T-37: UpsertUserIdentity called twice with a different display_name → DB row updated, not duplicated.
func TestStore_UpsertUserIdentity_UpdatesExistingRow(t *testing.T) {
	tx, q := authsharedtest.MustBeginTx(t, testPool)

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

	var displayName string
	err = tx.QueryRow(context.Background(),
		`SELECT display_name FROM user_identities WHERE user_id = $1 AND provider = 'google'`,
		authsharedtest.ToPgtypeUUID(uid),
	).Scan(&displayName)
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", displayName)

	var count int
	err = tx.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM user_identities WHERE user_id = $1 AND provider = 'google'`,
		authsharedtest.ToPgtypeUUID(uid),
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "upsert must not create duplicate rows")
}

// T-38: Email-match path — seed user with email; UpsertUserIdentity to link
// the identity; call OAuthLoginTx → user_identities row linked to seeded user.
func TestStore_OAuthLoginTx_EmailMatchPath(t *testing.T) {
	tx, q := authsharedtest.MustBeginTx(t, testPool)

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

	var linkedUserID pgtype.UUID
	err = tx.QueryRow(context.Background(),
		`SELECT user_id FROM user_identities WHERE provider_uid = $1`,
		"google-uid-t38",
	).Scan(&linkedUserID)
	require.NoError(t, err)
	assert.Equal(t, uid, linkedUserID.Bytes, "identity must be linked to the seeded user")
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
	tx, q := authsharedtest.MustBeginTx(t, testPool)

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

	var count int
	err = tx.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM user_identities WHERE user_id = $1 AND provider = 'google'`,
		authsharedtest.ToPgtypeUUID(uid),
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}
