// store_test.go intentionally has no //go:build integration_test tag.
// Unlike feature sub-packages, authshared exposes a large unit-testable
// surface (BaseStore helpers) that must run without a database.
// TestMain and integration tests live here so SetBcryptCostForTest is
// always called before any unit test; integration tests skip themselves
// when TEST_DATABASE_URL is not set (t.Skip inside txStores / mustSetup).
package authshared_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/db"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// zeroBase returns a BaseStore with no pool and no querier, useful for
// exercising the pure conversion helpers.
func zeroBase() authshared.BaseStore {
	return authshared.BaseStore{}
}

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	authsharedtest.RunTestMain(m, &testPool, 20)
}

// txStores returns a BaseStore and a db.Querier both scoped to a single
// rolled-back transaction. The transaction is rolled back in t.Cleanup so no
// data persists after the test. Skips when testPool is nil.
func txStores(t *testing.T) (authshared.BaseStore, *db.Queries) {
	t.Helper()
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	return authshared.NewBaseStore(testPool).WithQuerier(q), q
}

// ─── WithQuerier / TxBound ────────────────────────────────────────────────────

func TestWithQuerier_SetsTxBound(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	require.False(t, b.TxBound)
	b2 := b.WithQuerier(nil)
	require.True(t, b2.TxBound)
}

func TestWithQuerier_ReplacesQueriesField(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	b2 := b.WithQuerier(nil)
	require.Nil(t, b2.Queries)
}

func TestWithQuerier_DoesNotMutateOriginal(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	_ = b.WithQuerier(nil)
	require.False(t, b.TxBound)
}

// ─── BeginOrBind — TxBound path ───────────────────────────────────────────────

func TestBeginOrBind_TxBound_ReturnsNoopCommitAndRollback(t *testing.T) {
	t.Parallel()
	b := zeroBase().WithQuerier(nil)
	helpers, err := b.BeginOrBind(context.Background())
	require.NoError(t, err)

	require.NoError(t, helpers.Commit(), "Commit must be a no-op")
	require.NotPanics(t, func() { helpers.Rollback() }, "Rollback must be a no-op")
}

func TestBeginOrBind_TxBound_QIsInjectedQuerier(t *testing.T) {
	t.Parallel()
	b := zeroBase().WithQuerier(nil)
	helpers, err := b.BeginOrBind(context.Background())
	require.NoError(t, err)
	require.Nil(t, helpers.Q)
}

// ─── ToPgtypeUUID ─────────────────────────────────────────────────────────────

func TestToPgtypeUUID_Valid(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	var raw [16]byte
	raw[0] = 0xAB
	result := b.ToPgtypeUUID(raw)
	require.True(t, result.Valid)
	require.Equal(t, raw, result.Bytes)
}

func TestToPgtypeUUID_ZeroBytes_StillValid(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	result := b.ToPgtypeUUID([16]byte{})
	require.True(t, result.Valid)
}

// ─── UUIDToPgtypeUUID ─────────────────────────────────────────────────────────

func TestUUIDToPgtypeUUID_Roundtrip(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	u := uuid.New()
	pg := b.UUIDToPgtypeUUID(u)
	require.True(t, pg.Valid)
	require.Equal(t, [16]byte(u), pg.Bytes)
}

// ─── UUIDToBytes ─────────────────────────────────────────────────────────────

func TestUUIDToBytes_Roundtrip(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	u := uuid.New()
	raw := b.UUIDToBytes(u)
	require.Equal(t, [16]byte(u), raw)
}

// ─── ParseUUIDString ─────────────────────────────────────────────────────────

func TestParseUUIDString_ValidUUID(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	u := uuid.New()
	pg, err := b.ParseUUIDString(u.String())
	require.NoError(t, err)
	require.True(t, pg.Valid)
	require.Equal(t, [16]byte(u), pg.Bytes)
}

func TestParseUUIDString_InvalidString_ReturnsError(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	_, err := b.ParseUUIDString("not-a-uuid")
	require.Error(t, err)
}

func TestParseUUIDString_EmptyString_ReturnsError(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	_, err := b.ParseUUIDString("")
	require.Error(t, err)
}

// ─── ToText ───────────────────────────────────────────────────────────────────

func TestToText_NonEmpty_IsValid(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	result := b.ToText("hello")
	require.Equal(t, pgtype.Text{String: "hello", Valid: true}, result)
}

func TestToText_Empty_IsInvalid(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	result := b.ToText("")
	require.False(t, result.Valid)
}

// ─── MustJSON ─────────────────────────────────────────────────────────────────

func TestMustJSON_MarshalableValue(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	data := b.MustJSON(map[string]string{"key": "value"})
	var out map[string]string
	require.NoError(t, json.Unmarshal(data, &out))
	require.Equal(t, "value", out["key"])
}

func TestMustJSON_Panics_OnUnmarshalableValue(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	require.Panics(t, func() {
		b.MustJSON(make(chan int)) // channels cannot be marshalled
	})
}

func TestMustJSON_NilValue_ProducesNull(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	data := b.MustJSON(nil)
	require.Equal(t, []byte("null"), bytes.TrimSpace(data))
}

// ─── IPToNullable ─────────────────────────────────────────────────────────────

func TestIPToNullable_EmptyString_ReturnsNil(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	require.Nil(t, b.IPToNullable(""))
}

func TestIPToNullable_ValidIPv4_ReturnsAddr(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	ptr := b.IPToNullable("192.168.1.1")
	require.NotNil(t, ptr)
	expected, _ := netip.ParseAddr("192.168.1.1")
	require.Equal(t, expected, *ptr)
}

func TestIPToNullable_ValidIPv6_ReturnsAddr(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	ptr := b.IPToNullable("::1")
	require.NotNil(t, ptr)
}

func TestIPToNullable_InvalidString_ReturnsNil(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	require.Nil(t, b.IPToNullable("not-an-ip"))
}

// ─── TruncateUserAgent ────────────────────────────────────────────────────────

func TestTruncateUserAgent_ShortString_Unchanged(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	require.Equal(t, "Mozilla/5.0", b.TruncateUserAgent("Mozilla/5.0"))
}

func TestTruncateUserAgent_ExactlyMaxBytes_Unchanged(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	ua := strings.Repeat("x", 512)
	require.Equal(t, ua, b.TruncateUserAgent(ua))
}

func TestTruncateUserAgent_OverMax_TruncatesTo512(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	ua := strings.Repeat("x", 600)
	result := b.TruncateUserAgent(ua)
	require.Len(t, result, 512)
	require.Equal(t, strings.Repeat("x", 512), result)
}

func TestTruncateUserAgent_Empty_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	require.Equal(t, "", b.TruncateUserAgent(""))
}

// ─── IsNoRows ─────────────────────────────────────────────────────────────────

func TestIsNoRows_pgxErrNoRows_ReturnsTrue(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	require.True(t, b.IsNoRows(pgx.ErrNoRows))
}

func TestIsNoRows_WrappedErrNoRows_ReturnsTrue(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	wrapped := errors.Join(pgx.ErrNoRows, errors.New("wrapper"))
	require.True(t, b.IsNoRows(wrapped))
}

func TestIsNoRows_OtherError_ReturnsFalse(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	require.False(t, b.IsNoRows(errors.New("some other error")))
}

func TestIsNoRows_Nil_ReturnsFalse(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	require.False(t, b.IsNoRows(nil))
}

// ─── IsDuplicateEmail ────────────────────────────────────────────────────────

func TestIsDuplicateEmail_UniqueViolationOnEmail_ReturnsTrue(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "idx_users_email"}
	require.True(t, b.IsDuplicateEmail(pgErr))
}

func TestIsDuplicateEmail_UniqueViolationOnOtherConstraint_ReturnsFalse(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "users_username_key"}
	require.False(t, b.IsDuplicateEmail(pgErr))
}

func TestIsDuplicateEmail_DifferentPgCode_ReturnsFalse(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	pgErr := &pgconn.PgError{Code: "23503", ConstraintName: "users_email_fk"}
	require.False(t, b.IsDuplicateEmail(pgErr))
}

func TestIsDuplicateEmail_NonPgError_ReturnsFalse(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	require.False(t, b.IsDuplicateEmail(errors.New("not a pg error")))
}

func TestIsDuplicateEmail_NilError_ReturnsFalse(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	require.False(t, b.IsDuplicateEmail(nil))
}

func TestIsDuplicateEmail_WrappedPgError_ReturnsTrue(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "idx_users_email"}
	wrapped := fmt.Errorf("store: %w", pgErr)
	require.True(t, b.IsDuplicateEmail(wrapped))
}

// ─── LogRollback ─────────────────────────────────────────────────────────────

type mockTx struct {
	err error
}

func (m *mockTx) Rollback(_ context.Context) error { return m.err }

func TestLogRollback_SuccessfulRollback_NoPanic(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() {
		authshared.LogRollback(context.Background(), &mockTx{err: nil}, "test")
	})
}

func TestLogRollback_ErrTxClosed_NoPanic(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() {
		authshared.LogRollback(context.Background(), &mockTx{err: pgx.ErrTxClosed}, "test")
	})
}

func TestLogRollback_OtherError_NoPanic(t *testing.T) {
	t.Parallel()
	// Should log but not panic.
	require.NotPanics(t, func() {
		authshared.LogRollback(context.Background(), &mockTx{err: errors.New("db gone")}, "test")
	})
}

// ═══════════════════════════════════════════════════════════════════════════════
// Integration tests (skipped automatically when TEST_DATABASE_URL is not set)
// ═══════════════════════════════════════════════════════════════════════════════

// mustSetup commits a user + token to the DB and registers cleanup.
// Tokens are created via CreateEmailVerificationToken, which hardcodes
// max_attempts = 3. Caller may optionally pre-set the attempts counter.
// Returns the committed (userID, tokenID) as [16]byte.
//
// A UUID-suffixed email is generated per call so repeated test runs never
// collide on the idx_users_email unique constraint.
func mustSetup(t *testing.T, initialAttempts int16) ([16]byte, [16]byte) {
	t.Helper()
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	q := db.New(testPool)

	// Use a UUID suffix so each test run generates a distinct email address,
	// preventing duplicate-key violations when a prior run left rows behind.
	email := "store-test+" + uuid.New().String() + "@example.com"

	hash := authsharedtest.MustOTPHash(t)
	userRow, err := q.CreateUser(ctx, db.CreateUserParams{
		Email:        pgtype.Text{String: email, Valid: true},
		DisplayName:  pgtype.Text{String: "Test User", Valid: true},
		PasswordHash: pgtype.Text{String: hash, Valid: true},
	})
	require.NoError(t, err)
	userID := [16]byte(userRow.ID)

	_, codeHash, err := authshared.GenerateCodeHash()
	require.NoError(t, err)
	tokenRow, err := q.CreateEmailVerificationToken(ctx, db.CreateEmailVerificationTokenParams{
		UserID:     pgtype.UUID{Bytes: userID, Valid: true},
		Email:      email,
		CodeHash:   pgtype.Text{String: codeHash, Valid: true},
		TtlSeconds: 900, // 15 minutes
	})
	require.NoError(t, err)
	tokenID := [16]byte(tokenRow.ID)

	if initialAttempts > 0 {
		_, err = testPool.Exec(ctx,
			`UPDATE one_time_tokens SET attempts = $1 WHERE id = $2`,
			initialAttempts, pgtype.UUID{Bytes: tokenID, Valid: true},
		)
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		cleanQ := db.New(testPool)
		testPool.Exec(ctx, `DELETE FROM auth_audit_log WHERE user_id = $1`,
			pgtype.UUID{Bytes: userID, Valid: true})
		cleanQ.DeleteOTPTokenByID(ctx, pgtype.UUID{Bytes: tokenID, Valid: true})
		cleanQ.DeleteUserByEmail(ctx, email)
	})

	return userID, tokenID
}

// queryAttempts fetches the current attempts value for a token.
func queryAttempts(t *testing.T, tokenID [16]byte) int16 {
	t.Helper()
	q := db.New(testPool)
	attempts, err := q.GetTokenAttempts(context.Background(), pgtype.UUID{Bytes: tokenID, Valid: true})
	require.NoError(t, err)
	return attempts
}

// queryIsLocked reports whether a user's is_locked flag is set.
func queryIsLocked(t *testing.T, userID [16]byte) bool {
	t.Helper()
	q := db.New(testPool)
	locked, err := q.GetUserIsLocked(context.Background(), pgtype.UUID{Bytes: userID, Valid: true})
	require.NoError(t, err)
	return locked
}

// countAuditEvents returns the number of auth_audit_log rows for a user+event pair.
func countAuditEvents(t *testing.T, userID [16]byte, eventType audit.EventType) int {
	t.Helper()
	q := db.New(testPool)
	count, err := q.CountAuditEventsByUser(context.Background(), db.CountAuditEventsByUserParams{
		UserID:    pgtype.UUID{Bytes: userID, Valid: true},
		EventType: string(eventType),
	})
	require.NoError(t, err)
	return int(count)
}

// ─── IncrementAttemptsTx — happy path ────────────────────────────────────────

// TestIncrementAttemptsTx_IncrementsCounter_Integration verifies that a single
// call with a fresh token (attempts=0, max_attempts=3) increments the counter
// to 1, writes one attempt audit row, and does NOT lock the account.
//
// This test also covers store.go lines 228–236 (non-TxBound production tx
// path inside IncrementAttemptsTx). Those lines are only reachable when a
// genuine pgx.Tx is in play; the QuerierProxy unit tests cannot exercise them.
func TestIncrementAttemptsTx_IncrementsCounter_Integration(t *testing.T) {
	t.Parallel()
	userID, tokenID := mustSetup(t, 0)

	store := authshared.NewBaseStore(testPool)

	in := authshared.IncrementInput{
		TokenID:      tokenID,
		UserID:       userID,
		Attempts:     0,
		MaxAttempts:  3,
		AttemptEvent: audit.EventVerifyAttemptFailed,
	}

	require.NoError(t, store.IncrementAttemptsTx(context.Background(), in))

	require.Equal(t, int16(1), queryAttempts(t, tokenID))
	require.Equal(t, 1, countAuditEvents(t, userID, audit.EventVerifyAttemptFailed))
	require.False(t, queryIsLocked(t, userID))
}

// ─── IncrementAttemptsTx — threshold triggers lock ───────────────────────────

// TestIncrementAttemptsTx_AtThreshold_LocksAccount_Integration verifies that
// when the DB already has attempts=max_attempts-1 (2 of 3), one more increment
// pushes newAttempts to 3 >= 3, which locks the account and writes both
// EventVerifyAttemptFailed and EventAccountLocked audit rows.
func TestIncrementAttemptsTx_AtThreshold_LocksAccount_Integration(t *testing.T) {
	t.Parallel()
	// Pre-set attempts=2 so one more increment crosses max_attempts=3.
	userID, tokenID := mustSetup(t, 2)

	store := authshared.NewBaseStore(testPool)

	in := authshared.IncrementInput{
		TokenID:      tokenID,
		UserID:       userID,
		Attempts:     2,
		MaxAttempts:  3,
		AttemptEvent: audit.EventVerifyAttemptFailed,
	}

	require.NoError(t, store.IncrementAttemptsTx(context.Background(), in))

	require.True(t, queryIsLocked(t, userID), "account must be locked at threshold")
	require.Equal(t, 1, countAuditEvents(t, userID, audit.EventVerifyAttemptFailed))
	require.Equal(t, 1, countAuditEvents(t, userID, audit.EventAccountLocked))
}

// ─── IncrementAttemptsTx — cancelled context still commits ───────────────────

// TestIncrementAttemptsTx_CancelledContext_StillCommits_Integration verifies
// that context.WithoutCancel() inside IncrementAttemptsTx prevents a
// client-disconnect from aborting the attempt counter write (ADR-004).
func TestIncrementAttemptsTx_CancelledContext_StillCommits_Integration(t *testing.T) {
	t.Parallel()
	userID, tokenID := mustSetup(t, 0)

	store := authshared.NewBaseStore(testPool)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE the call

	in := authshared.IncrementInput{
		TokenID:      tokenID,
		UserID:       userID,
		Attempts:     0,
		MaxAttempts:  3,
		AttemptEvent: audit.EventVerifyAttemptFailed,
	}

	err := store.IncrementAttemptsTx(cancelledCtx, in)
	require.NoError(t, err, "cancelled context must not abort the counter increment")
	require.Equal(t, int16(1), queryAttempts(t, tokenID))
}

// ─── UpdatePasswordHashTx — full transaction ─────────────────────────────────

// TestUpdatePasswordHashTx_Integration verifies that UpdatePasswordHashTx
// updates the password hash and writes an EventPasswordChanged audit row.
func TestUpdatePasswordHashTx_Integration(t *testing.T) {
	t.Parallel()
	userID, _ := mustSetup(t, 0)

	store := authshared.NewBaseStore(testPool)

	// Generate a real bcrypt hash so it satisfies chk_users_password_hash_format.
	newHash := authsharedtest.MustHashPassword(t, "new-secure-password-1!")

	require.NoError(t, store.UpdatePasswordHashTx(context.Background(), userID, newHash, "127.0.0.1", "test-agent"))

	q := db.New(testPool)
	gotRow, err := q.GetUserPasswordHash(context.Background(), pgtype.UUID{Bytes: userID, Valid: true})
	require.NoError(t, err)
	require.Equal(t, newHash, gotRow.PasswordHash.String)
	require.Equal(t, 1, countAuditEvents(t, userID, audit.EventPasswordChanged))
}

// ─── UpdatePasswordHashTx — sessions and tokens revoked ────────────────────────

// countOpenSessions returns the number of user_sessions rows with ended_at IS NULL
// for the given user. Used to assert post-UpdatePasswordHashTx state.
func countOpenSessions(t *testing.T, userID [16]byte) int {
	t.Helper()
	q := db.New(testPool)
	count, err := q.CountOpenSessionsByUser(context.Background(), pgtype.UUID{Bytes: userID, Valid: true})
	require.NoError(t, err)
	return int(count)
}

// countActiveRefreshTokens returns the number of refresh_tokens rows that are
// neither revoked nor expired for the given user.
func countActiveRefreshTokens(t *testing.T, userID [16]byte) int {
	t.Helper()
	q := db.New(testPool)
	count, err := q.CountActiveRefreshTokensByUser(context.Background(), pgtype.UUID{Bytes: userID, Valid: true})
	require.NoError(t, err)
	return int(count)
}

// TestUpdatePasswordHashTx_SessionsAndTokensRevoked_Integration verifies the
// §2.13 security invariant: UpdatePasswordHashTx must end every open session
// and revoke every active refresh token in the same transaction as the hash
// update. Two sessions and two tokens are seeded so the assertions would catch
// a partial revocation (e.g. only one row affected).
func TestUpdatePasswordHashTx_SessionsAndTokensRevoked_Integration(t *testing.T) {
	t.Parallel()
	userID, _ := mustSetup(t, 0)

	ctx := context.Background()
	q := db.New(testPool)
	pgUserID := pgtype.UUID{Bytes: userID, Valid: true}

	// Seed two sessions, each with one refresh token, to ensure both rows are
	// terminated — a bug that only closes the first row would be caught here.
	for i := 0; i < 2; i++ {
		sessRow, err := q.CreateUserSession(ctx, db.CreateUserSessionParams{
			UserID:       pgUserID,
			AuthProvider: db.AuthProviderEmail,
			UserAgent:    pgtype.Text{String: "test-agent", Valid: true},
		})
		require.NoError(t, err)

		_, err = q.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
			UserID:    pgUserID,
			SessionID: pgtype.UUID{Bytes: [16]byte(sessRow.ID), Valid: true},
		})
		require.NoError(t, err)
	}

	// Confirm the seed is visible before the call.
	require.Equal(t, 2, countOpenSessions(t, userID), "pre-condition: 2 open sessions")
	require.Equal(t, 2, countActiveRefreshTokens(t, userID), "pre-condition: 2 active tokens")

	store := authshared.NewBaseStore(testPool)
	newHash := authsharedtest.MustHashPassword(t, "new-secure-password-1!")

	require.NoError(t, store.UpdatePasswordHashTx(ctx, userID, newHash, "127.0.0.1", "test-agent"))

	require.Equal(t, 0, countOpenSessions(t, userID),
		"UpdatePasswordHashTx must end all open sessions")
	require.Equal(t, 0, countActiveRefreshTokens(t, userID),
		"UpdatePasswordHashTx must revoke all active refresh tokens")
}

// ─── UpdatePasswordHashTx — audit failure rolls back ─────────────────────────

// TestUpdatePasswordHashTx_FailOnAuditLog_RollsBack_Integration verifies that
// when InsertAuditLog fails, the whole transaction is rolled back and the
// password hash is NOT changed.
func TestUpdatePasswordHashTx_FailOnAuditLog_RollsBack_Integration(t *testing.T) {
	t.Parallel()
	userID, _ := mustSetup(t, 0)

	// Capture the original hash before the attempted change.
	q := db.New(testPool)
	originalRow, err := q.GetUserPasswordHash(context.Background(), pgtype.UUID{Bytes: userID, Valid: true})
	require.NoError(t, err)
	originalHash := originalRow.PasswordHash.String

	// Build a store using txStores so all operations happen inside a
	// rolled-back test transaction — the proxy injects an audit log failure.
	store, txQ := txStores(t)
	proxy := &authsharedtest.QuerierProxy{
		Base:               txQ,
		FailInsertAuditLog: true,
	}
	storeWithProxy := store.WithQuerier(proxy)

	err = storeWithProxy.UpdatePasswordHashTx(context.Background(), userID, authsharedtest.MustHashPassword(t, "should-not-appear-1!"), "127.0.0.1", "test-agent")
	require.Error(t, err, "UpdatePasswordHashTx must fail when audit log injection fails")

	// The test transaction is rolled back by t.Cleanup in txStores.
	// testPool cannot see uncommitted changes, so the original hash is visible.
	gotRow, err := q.GetUserPasswordHash(context.Background(), pgtype.UUID{Bytes: userID, Valid: true})
	require.NoError(t, err)
	require.Equal(t, originalHash, gotRow.PasswordHash.String, "password hash must not change when transaction is rolled back")
}

// ═══════════════════════════════════════════════════════════════════════════════
// Unit tests — no real database required
// ═══════════════════════════════════════════════════════════════════════════════

// ─── successQ ────────────────────────────────────────────────────────────────

// successQ shadows the methods that must succeed in unit tests that use a proxy
// chain without a real database. Any un-overridden method promoted from the
// embedded *QuerierProxy will attempt to call its nil Base and panic —
// that is intentional: it flags an unexpected call in a code path that should
// not have been reached.
type successQ struct {
	*authsharedtest.QuerierProxy
	incrReturn int16
}

func newSuccessQ(incrReturn int16) *successQ {
	return &successQ{
		QuerierProxy: &authsharedtest.QuerierProxy{Base: nil},
		incrReturn:   incrReturn,
	}
}

func (s *successQ) IncrementVerificationAttempts(_ context.Context, _ pgtype.UUID) (int16, error) {
	return s.incrReturn, nil
}
func (s *successQ) InsertAuditLog(_ context.Context, _ db.InsertAuditLogParams) error { return nil }
func (s *successQ) LockAccount(_ context.Context, _ pgtype.UUID) (int64, error)       { return 1, nil }
func (s *successQ) UpdatePasswordHash(_ context.Context, _ db.UpdatePasswordHashParams) error {
	return nil
}
func (s *successQ) RevokeAllUserRefreshTokens(_ context.Context, _ db.RevokeAllUserRefreshTokensParams) error {
	return nil
}
func (s *successQ) EndAllUserSessions(_ context.Context, _ pgtype.UUID) error { return nil }

// closedPool returns a *pgxpool.Pool that has already been closed so that any
// call to pool.Begin returns an error. The pool is created with a plausible
// DSN — pgxpool.New is lazy and does not dial until the first acquire.
func closedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "host=127.0.0.1 port=1 connect_timeout=1")
	require.NoError(t, err, "pgxpool.New must not fail (connection is lazy)")
	pool.Close()
	return pool
}

// makeInput returns a minimal valid IncrementInput for unit tests.
func makeInput(maxAttempts int16) authshared.IncrementInput {
	return authshared.IncrementInput{
		TokenID:      [16]byte(uuid.New()),
		UserID:       [16]byte(uuid.New()),
		MaxAttempts:  maxAttempts,
		AttemptEvent: audit.EventVerifyAttemptFailed,
	}
}

// ─── GROUP 1: BeginOrBind error paths ────────────────────────────────────────

// TestBeginOrBind_PoolError verifies that BeginOrBind returns an error when
// the underlying pool has been closed and Pool.Begin cannot acquire a connection.
func TestBeginOrBind_PoolError(t *testing.T) {
	t.Parallel()
	store := authshared.BaseStore{Pool: closedPool(t)}
	_, err := store.BeginOrBind(context.Background())
	require.Error(t, err, "BeginOrBind must propagate Pool.Begin failure")
}

// TestBeginOrBind_TxBound_NoopHelpers verifies the TxBound success path:
// Commit and Rollback are no-ops and Q is the injected querier.
func TestBeginOrBind_TxBound_NoopHelpers(t *testing.T) {
	t.Parallel()
	proxy := &authsharedtest.QuerierProxy{Base: nil}
	store := authshared.BaseStore{}.WithQuerier(proxy)
	h, err := store.BeginOrBind(context.Background())
	require.NoError(t, err)
	require.NoError(t, h.Commit())
	require.NotPanics(t, h.Rollback)
	require.Equal(t, proxy, h.Q)
}

// ─── GROUP 3: IncrementAttemptsTx error paths ────────────────────────────────

// TestIncrementAttemptsTx_FailIncrement covers the path where
// IncrementVerificationAttempts returns a non-ErrNoRows error (lines 212-214).
func TestIncrementAttemptsTx_FailIncrement(t *testing.T) {
	t.Parallel()
	proxy := &authsharedtest.QuerierProxy{
		Base:                              nil,
		FailIncrementVerificationAttempts: true,
	}
	store := authshared.BaseStore{}.WithQuerier(proxy)
	err := store.IncrementAttemptsTx(context.Background(), makeInput(3))
	require.Error(t, err)
	require.Contains(t, err.Error(), "increment attempts")
}

// TestIncrementAttemptsTx_FailAuditLogFirstCall covers lines 218-220: the
// first InsertAuditLog (attempt-failed event) returns an error.
func TestIncrementAttemptsTx_FailAuditLogFirstCall(t *testing.T) {
	t.Parallel()
	proxy := &authsharedtest.QuerierProxy{
		Base:                     newSuccessQ(1), // increment succeeds, returns 1
		FailInsertAuditLog:       true,
		InsertAuditLogFailOnCall: 1,
	}
	store := authshared.BaseStore{}.WithQuerier(proxy)
	err := store.IncrementAttemptsTx(context.Background(), makeInput(3))
	require.Error(t, err)
	require.Contains(t, err.Error(), "audit log")
}

// TestIncrementAttemptsTx_BelowThreshold covers line 223: newAttempts < MaxAttempts
// → no lock is applied and the function returns nil.
func TestIncrementAttemptsTx_BelowThreshold(t *testing.T) {
	t.Parallel()
	proxy := &authsharedtest.QuerierProxy{
		Base: newSuccessQ(1), // returns 1 < MaxAttempts(3)
	}
	store := authshared.BaseStore{}.WithQuerier(proxy)
	err := store.IncrementAttemptsTx(context.Background(), makeInput(3))
	require.NoError(t, err, "below threshold: no lock, no error expected")
}

// TestIncrementAttemptsTx_FailLockAccount covers lines 233-237: LockAccount
// fails after the threshold is reached.
func TestIncrementAttemptsTx_FailLockAccount(t *testing.T) {
	t.Parallel()
	const maxAttempts int16 = 3
	proxy := &authsharedtest.QuerierProxy{
		Base:            newSuccessQ(maxAttempts), // increment returns MaxAttempts → triggers lock
		FailLockAccount: true,
	}
	store := authshared.BaseStore{}.WithQuerier(proxy)
	err := store.IncrementAttemptsTx(context.Background(), makeInput(maxAttempts))
	require.Error(t, err)
	require.Contains(t, err.Error(), "lock account")
}

// TestIncrementAttemptsTx_FailAuditLogSecondCall covers line 239: the second
// InsertAuditLog (account_locked event) fails after a successful lock.
func TestIncrementAttemptsTx_FailAuditLogSecondCall(t *testing.T) {
	t.Parallel()
	const maxAttempts int16 = 3
	proxy := &authsharedtest.QuerierProxy{
		Base:                     newSuccessQ(maxAttempts),
		FailInsertAuditLog:       true,
		InsertAuditLogFailOnCall: 2, // first call (attempt event) succeeds; second (locked) fails
	}
	store := authshared.BaseStore{}.WithQuerier(proxy)
	err := store.IncrementAttemptsTx(context.Background(), makeInput(maxAttempts))
	require.Error(t, err)
	require.Contains(t, err.Error(), "audit log (account_locked)")
}

// ─── GROUP 4: UpdatePasswordHashTx error paths ───────────────────────────────

// TestUpdatePasswordHashTx_PoolBeginFails covers lines 291-293: Pool.Begin
// returns an error when the pool has been closed.
func TestUpdatePasswordHashTx_PoolBeginFails(t *testing.T) {
	t.Parallel()
	// TxBound=false (default), so BeginOrBind calls Pool.Begin.
	store := authshared.BaseStore{Pool: closedPool(t)}
	err := store.UpdatePasswordHashTx(context.Background(), [16]byte{}, "hash", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "begin tx")
}

// TestUpdatePasswordHashTx_FailUpdateHash covers lines 301-304: UpdatePasswordHash
// returns an error.
func TestUpdatePasswordHashTx_FailUpdateHash(t *testing.T) {
	t.Parallel()
	proxy := &authsharedtest.QuerierProxy{
		Base:                   nil,
		FailUpdatePasswordHash: true,
	}
	store := authshared.BaseStore{}.WithQuerier(proxy)
	err := store.UpdatePasswordHashTx(context.Background(), [16]byte{}, "hash", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update hash")
}

// TestUpdatePasswordHashTx_FailRevokeTokens covers lines 310-313:
// RevokeAllUserRefreshTokens returns an error.
func TestUpdatePasswordHashTx_FailRevokeTokens(t *testing.T) {
	t.Parallel()
	proxy := &authsharedtest.QuerierProxy{
		Base:                           newSuccessQ(0),
		FailRevokeAllUserRefreshTokens: true,
	}
	store := authshared.BaseStore{}.WithQuerier(proxy)
	err := store.UpdatePasswordHashTx(context.Background(), [16]byte{}, "hash", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "revoke refresh tokens")
}

// TestUpdatePasswordHashTx_FailEndSessions covers lines 316-319:
// EndAllUserSessions returns an error.
func TestUpdatePasswordHashTx_FailEndSessions(t *testing.T) {
	t.Parallel()
	proxy := &authsharedtest.QuerierProxy{
		Base:                   newSuccessQ(0),
		FailEndAllUserSessions: true,
	}
	store := authshared.BaseStore{}.WithQuerier(proxy)
	err := store.UpdatePasswordHashTx(context.Background(), [16]byte{}, "hash", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "end sessions")
}

// TestUpdatePasswordHashTx_FailFinalAuditLog covers lines 334-336: the final
// InsertAuditLog (password_changed event) returns an error.
func TestUpdatePasswordHashTx_FailFinalAuditLog(t *testing.T) {
	t.Parallel()
	proxy := &authsharedtest.QuerierProxy{
		Base:               newSuccessQ(0),
		FailInsertAuditLog: true,
	}
	store := authshared.BaseStore{}.WithQuerier(proxy)
	err := store.UpdatePasswordHashTx(context.Background(), [16]byte{}, "hash", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "audit log")
}

// ─── IncrementAttemptsTx — ErrNoRows path ───────────────────────────────────

// errNoRowsIncrQ overrides IncrementVerificationAttempts to return pgx.ErrNoRows,
// simulating the case where the DB counter is already at the ceiling. All other
// operations succeed via the embedded successQ.
type errNoRowsIncrQ struct {
	*successQ
}

func (e *errNoRowsIncrQ) IncrementVerificationAttempts(_ context.Context, _ pgtype.UUID) (int16, error) {
	return 0, pgx.ErrNoRows
}

// TestIncrementAttemptsTx_ErrNoRows_UsesMaxAttempts covers the branch at
// store.go:223 where IncrementVerificationAttempts returns pgx.ErrNoRows
// (token already at ceiling), causing newAttempts to be set to in.MaxAttempts.
// Because newAttempts then equals MaxAttempts the lock path fires, and the
// overall call must succeed.
func TestIncrementAttemptsTx_ErrNoRows_UsesMaxAttempts(t *testing.T) {
	t.Parallel()
	const maxAttempts int16 = 3
	q := &errNoRowsIncrQ{successQ: newSuccessQ(0)}
	proxy := &authsharedtest.QuerierProxy{Base: q}
	store := authshared.BaseStore{}.WithQuerier(proxy)

	err := store.IncrementAttemptsTx(context.Background(), makeInput(maxAttempts))
	require.NoError(t, err, "ErrNoRows ceiling path must succeed and lock the account")
}

// ─── BeginOrBind — non-TxBound closures (integration) ────────────────────────

// TestBeginOrBind_NonTxBound_CommitAndRollback_Integration exercises both the
// Commit and Rollback closure bodies in the non-TxBound path of BeginOrBind
// (store.go lines covering the returned closures). A real transaction is
// required because the closures delegate to pgx.Tx.Commit / LogRollback.
func TestBeginOrBind_NonTxBound_CommitAndRollback_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	store := authshared.NewBaseStore(testPool)

	// Commit path: open a tx, commit it — exercises the Commit closure body.
	hCommit, err := store.BeginOrBind(context.Background())
	require.NoError(t, err)
	require.NoError(t, hCommit.Commit(), "non-TxBound Commit must delegate to tx.Commit")

	// Rollback path: open a new tx, roll it back — exercises LogRollback inside
	// the Rollback closure body.
	hRollback, err := store.BeginOrBind(context.Background())
	require.NoError(t, err)
	require.NotPanics(t, func() { hRollback.Rollback() }, "non-TxBound Rollback must not panic")
}

// TestUpdatePasswordHashTx_CancelledContext_RollsBack_Integration verifies
// that a cancelled context causes UpdatePasswordHashTx to fail and leave the
// password hash unchanged. Because UpdatePasswordHashTx does not apply
// context.WithoutCancel to the forward queries (only rollbacks are protected),
// a cancelled context aborts the first DB call and the transaction is rolled
// back cleanly — atomicity is preserved.
func TestUpdatePasswordHashTx_CancelledContext_RollsBack_Integration(t *testing.T) {
	// Use the TxBound path via txStores so the test is scoped to a rolled-back
	// transaction. mustSetup commits user rows independently so testPool can
	// read the original hash after the failed call.
	store, _ := txStores(t)
	userID, _ := mustSetup(t, 0)

	// Capture the original hash so we can assert it is unchanged after the call.
	q := db.New(testPool)
	originalRow, err := q.GetUserPasswordHash(context.Background(), pgtype.UUID{Bytes: userID, Valid: true})
	require.NoError(t, err)
	originalHash := originalRow.PasswordHash.String

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE the call so the first query fails

	err = store.UpdatePasswordHashTx(cancelledCtx, userID, authsharedtest.MustHashPassword(t, "new-secure-pwd-1!"), "", "")
	require.Error(t, err, "cancelled context must cause UpdatePasswordHashTx to fail")

	// Verify the hash was not modified — the transaction was rolled back.
	gotRow, err := q.GetUserPasswordHash(context.Background(), pgtype.UUID{Bytes: userID, Valid: true})
	require.NoError(t, err)
	require.Equal(t, originalHash, gotRow.PasswordHash.String,
		"password hash must be unchanged when context is cancelled mid-transaction")
}

// ─── ToPgtypeUUIDNullable ───────────────────────────────────────────────────

func TestToPgtypeUUIDNullable_ZeroInput_ReturnsInvalid(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	result := b.ToPgtypeUUIDNullable([16]byte{})
	require.False(t, result.Valid)
}

func TestToPgtypeUUIDNullable_NonZeroInput_ReturnsValid(t *testing.T) {
	t.Parallel()
	b := zeroBase()
	var raw [16]byte
	raw[0] = 0x01
	result := b.ToPgtypeUUIDNullable(raw)
	require.True(t, result.Valid)
	require.Equal(t, raw, result.Bytes)
}

// ─── IncrementAttemptsTx — empty AttemptEvent guard ──────────────────────────

func TestIncrementAttemptsTx_EmptyAttemptEvent_ReturnsError(t *testing.T) {
	t.Parallel()
	store := authshared.BaseStore{}.WithQuerier(nil)
	in := authshared.IncrementInput{
		TokenID:     [16]byte(uuid.New()),
		UserID:      [16]byte(uuid.New()),
		MaxAttempts: 3,
		// AttemptEvent intentionally left empty
	}
	err := store.IncrementAttemptsTx(context.Background(), in)
	require.Error(t, err)
	require.Contains(t, err.Error(), "AttemptEvent must not be empty")
}

// ─── IncrementAttemptsTx — token already at ceiling (integration) ─────────────

// TestIncrementAttemptsTx_TokenAlreadyAtCeiling_Integration exercises the
// store.go:235 branch where IncrementVerificationAttempts returns pgx.ErrNoRows
// because the DB-side guard (WHERE attempts < max_attempts) found the token was
// already at the ceiling. The branch sets newAttempts = in.MaxAttempts so the
// lock threshold fires, and the overall call must return nil.
func TestIncrementAttemptsTx_TokenAlreadyAtCeiling_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}

	// mustSetup creates a committed user+token (max_attempts=3, attempts=0).
	userID, tokenID := mustSetup(t, 0)

	ctx := context.Background()
	q := db.New(testPool)
	tokenPgUUID := pgtype.UUID{Bytes: tokenID, Valid: true}

	// Exhaust the token by calling IncrementVerificationAttempts until the DB guard
	// (WHERE attempts < max_attempts) finds nothing to update and returns ErrNoRows.
	for {
		_, err := q.IncrementVerificationAttempts(ctx, tokenPgUUID)
		if errors.Is(err, pgx.ErrNoRows) {
			break // token is now at ceiling
		}
		require.NoError(t, err, "unexpected error while exhausting token attempts")
	}

	// Now call IncrementAttemptsTx on the exhausted token. Internally,
	// IncrementVerificationAttempts will immediately return pgx.ErrNoRows;
	// the store sets newAttempts = in.MaxAttempts and proceeds to lock the account.
	store := authshared.NewBaseStore(testPool)
	err := store.IncrementAttemptsTx(ctx, authshared.IncrementInput{
		TokenID:      tokenID,
		UserID:       userID,
		Attempts:     3, // already at ceiling
		MaxAttempts:  3,
		AttemptEvent: audit.EventVerifyAttemptFailed,
	})
	require.NoError(t, err, "ceiling path must succeed and lock the account")
}

// ─── IncrementAttemptsTx — non-TxBound Pool.Begin failure ────────────────────

func TestIncrementAttemptsTx_NonTxBound_PoolBeginFails(t *testing.T) {
	t.Parallel()
	// TxBound=false (no WithQuerier), so the production path calls Pool.Begin directly.
	store := authshared.BaseStore{Pool: closedPool(t)}
	err := store.IncrementAttemptsTx(context.Background(), makeInput(3))
	require.Error(t, err)
	require.Contains(t, err.Error(), "begin tx")
}

