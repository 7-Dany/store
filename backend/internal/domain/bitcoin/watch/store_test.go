//go:build integration_test

package watch

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	bitcoinsharedtest "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared/testutil"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	bitcoinsharedtest.RunTestMainWithDB(m, &testPool, 5)
}

func testStore(t *testing.T) *Store {
	t.Helper()
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return NewStore(testPool)
}

func createTestUser(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	q := db.New(testPool)

	hash, err := bcrypt.GenerateFromPassword([]byte("test-watch-store"), bcrypt.MinCost)
	require.NoError(t, err)

	email := fmt.Sprintf("btc-watch-test+%s@example.com", uuid.New().String())
	row, err := q.CreateUser(ctx, db.CreateUserParams{
		Email:        pgtype.Text{String: email, Valid: true},
		DisplayName:  pgtype.Text{String: "Watch Test User", Valid: true},
		PasswordHash: pgtype.Text{String: string(hash), Valid: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.New(testPool).DeleteUserByEmail(context.Background(), email) })
	return row.ID.String()
}

func TestCreateGetDeleteWatch_Integration(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	userID := createTestUser(t)

	address := "tb1qexampleaddress0000000000000000000000000"
	userUUID, err := uuid.Parse(userID)
	require.NoError(t, err)

	created, err := store.CreateWatch(ctx, watchWriteInput{
		UserID:    userUUID,
		Network:   "testnet4",
		WatchType: WatchTypeAddress,
		Address:   &address,
	})
	require.NoError(t, err)
	assert.Equal(t, WatchTypeAddress, created.WatchType)

	got, err := store.GetWatch(ctx, GetWatchInput{ID: created.ID, UserID: userID})
	require.NoError(t, err)
	require.NotNil(t, got.Address)
	assert.Equal(t, address, *got.Address)

	err = store.DeleteWatch(ctx, DeleteWatchInput{ID: created.ID, UserID: userID})
	require.NoError(t, err)

	_, err = store.GetWatch(ctx, GetWatchInput{ID: created.ID, UserID: userID})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrWatchNotFound))
}

func TestCountActiveAddressWatches_Integration(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	userID := createTestUser(t)
	userUUID, err := uuid.Parse(userID)
	require.NoError(t, err)

	addressA := "tb1qcountaddressa00000000000000000000000000"
	addressB := "tb1qcountaddressb00000000000000000000000000"

	_, err = store.CreateWatch(ctx, watchWriteInput{
		UserID:    userUUID,
		Network:   "testnet4",
		WatchType: WatchTypeAddress,
		Address:   &addressA,
	})
	require.NoError(t, err)
	_, err = store.CreateWatch(ctx, watchWriteInput{
		UserID:    userUUID,
		Network:   "testnet4",
		WatchType: WatchTypeAddress,
		Address:   &addressB,
	})
	require.NoError(t, err)

	count, err := store.CountActiveAddressWatches(ctx, userID, "testnet4")
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestWriteAuditLog_Integration(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	userID := createTestUser(t)
	err := store.WriteAuditLog(ctx, audit.EventBitcoinAddressWatched, userID, "127.0.0.1", map[string]string{"action": "create"})
	require.NoError(t, err)

	uid, err := uuid.Parse(userID)
	require.NoError(t, err)
	count, err := db.New(testPool).CountAuditEventsByUser(ctx, db.CountAuditEventsByUserParams{
		UserID:    pgtype.UUID{Bytes: [16]byte(uid), Valid: true},
		EventType: string(audit.EventBitcoinAddressWatched),
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), count)
}
