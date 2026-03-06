package authsharedtest_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/7-Dany/store/backend/internal/config"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var testPool *pgxpool.Pool

// TestMain lowers the bcrypt work factor for the entire package so that every
// test that calls MustHashPassword or MustOTPHash completes in milliseconds.
// Both builders.go and querier_proxy.go live in the same package under test, so
// a single TestMain in this file covers both test files.
// When TEST_DATABASE_URL is set it also opens a pool for integration tests.
func TestMain(m *testing.M) {
	authshared.SetBcryptCostForTest(bcrypt.MinCost)
	if dsn := config.TestDatabaseURL(); dsn != "" {
		testPool = authsharedtest.MustNewTestPool(dsn, 20)
	}
	code := m.Run()
	if testPool != nil {
		testPool.Close()
	}
	os.Exit(code)
}

// ── MustUUID ──────────────────────────────────────────────────────────────────

func TestMustUUID_ValidString_ReturnsBytes(t *testing.T) {
	t.Parallel()
	const uuidStr = "550e8400-e29b-41d4-a716-446655440000"
	got := authsharedtest.MustUUID(uuidStr)
	require.NotEqual(t, [16]byte{}, got)
}

func TestMustUUID_InvalidString_Panics(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() { authsharedtest.MustUUID("not-a-uuid") })
}

// ── RandomUUID ────────────────────────────────────────────────────────────────

func TestRandomUUID_ReturnsDifferentValues(t *testing.T) {
	t.Parallel()
	a := authsharedtest.RandomUUID()
	b := authsharedtest.RandomUUID()
	require.NotEqual(t, a, b)
}

// ── ToPgtypeUUID ──────────────────────────────────────────────────────────────

func TestToPgtypeUUID_ValidAndBytes(t *testing.T) {
	t.Parallel()
	b := authsharedtest.RandomUUID()
	got := authsharedtest.ToPgtypeUUID(b)
	require.True(t, got.Valid)
	require.Equal(t, b, got.Bytes)
}

func TestToPgtypeUUID_ZeroInput_StillValid(t *testing.T) {
	t.Parallel()
	got := authsharedtest.ToPgtypeUUID([16]byte{})
	require.IsType(t, pgtype.UUID{}, got)
	require.True(t, got.Valid)
}

// ── MustHashPassword ──────────────────────────────────────────────────────────

func TestMustHashPassword_ReturnsNonEmptyHash(t *testing.T) {
	t.Parallel()
	h := authsharedtest.MustHashPassword(t, "S3cure!Pass")
	require.NotEmpty(t, h)
}

func TestMustHashPassword_DifferentPasswordsDifferentHashes(t *testing.T) {
	t.Parallel()
	h1 := authsharedtest.MustHashPassword(t, "P@ssword1!")
	h2 := authsharedtest.MustHashPassword(t, "P@ssword2!")
	require.NotEqual(t, h1, h2)
}



// ── MustOTPHash ───────────────────────────────────────────────────────────────

func TestMustOTPHash_ReturnsNonEmptyHash(t *testing.T) {
	t.Parallel()
	h := authsharedtest.MustOTPHash(t)
	require.NotEmpty(t, h)
	require.Contains(t, h, "$2a$")
}

func TestMustOTPHash_TwoCallsReturnDifferentHashes(t *testing.T) {
	t.Parallel()
	// bcrypt uses a random salt each time, so two calls must differ.
	h1 := authsharedtest.MustOTPHash(t)
	h2 := authsharedtest.MustOTPHash(t)
	require.NotEqual(t, h1, h2)
}



// ── OTPPlaintext ──────────────────────────────────────────────────────────────

func TestOTPPlaintext_IsExportedAndNonEmpty(t *testing.T) {
	t.Parallel()
	require.NotEmpty(t, authsharedtest.OTPPlaintext)
}

// ── NewEmail ──────────────────────────────────────────────────────────────────

func TestNewEmail_ContainsExpectedSuffix(t *testing.T) {
	t.Parallel()
	e := authsharedtest.NewEmail(t)
	require.Contains(t, e, "@example.com")
	require.Contains(t, e, "test+")
}

func TestNewEmail_TwoCallsReturnDifferentAddresses(t *testing.T) {
	t.Parallel()
	e1 := authsharedtest.NewEmail(t)
	e2 := authsharedtest.NewEmail(t)
	require.NotEqual(t, e1, e2)
}

// ── Future / Past ─────────────────────────────────────────────────────────────

func TestFuture_IsAfterNow(t *testing.T) {
	t.Parallel()
	require.True(t, authsharedtest.Future().After(time.Now()))
}

func TestPast_IsBeforeNow(t *testing.T) {
	t.Parallel()
	require.True(t, authsharedtest.Past().Before(time.Now()))
}

// ── JSONRequest ───────────────────────────────────────────────────────────────

func TestJSONRequest_SetsContentTypeHeader(t *testing.T) {
	t.Parallel()
	req := authsharedtest.JSONRequest(http.MethodPost, "/test", map[string]string{"key": "value"})
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
}

func TestJSONRequest_BodyIsValidJSON(t *testing.T) {
	t.Parallel()
	payload := map[string]string{"hello": "world"}
	req := authsharedtest.JSONRequest(http.MethodPost, "/test", payload)
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	var decoded map[string]string
	require.NoError(t, json.Unmarshal(body, &decoded))
	require.Equal(t, "world", decoded["hello"])
}

func TestJSONRequest_UsesGivenMethod(t *testing.T) {
	t.Parallel()
	req := authsharedtest.JSONRequest(http.MethodPut, "/update", struct{}{})
	require.Equal(t, http.MethodPut, req.Method)
}

func TestJSONRequest_PanicsOnUnmarshalableValue(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() {
		authsharedtest.JSONRequest(http.MethodPost, "/", make(chan int))
	})
}

// ── Body builder helpers ──────────────────────────────────────────────────────

func TestRegisterBody_ContainsExpectedKeys(t *testing.T) {
	t.Parallel()
	b := authsharedtest.RegisterBody("Alice", "alice@example.com", "Secret1!")
	require.Equal(t, "Alice", b["display_name"])
	require.Equal(t, "alice@example.com", b["email"])
	require.Equal(t, "Secret1!", b["password"])
}

func TestLoginBody_ContainsExpectedKeys(t *testing.T) {
	t.Parallel()
	b := authsharedtest.LoginBody("alice@example.com", "Secret1!")
	require.Equal(t, "alice@example.com", b["identifier"])
	require.Equal(t, "Secret1!", b["password"])
}

func TestVerifyEmailBody_ContainsExpectedKeys(t *testing.T) {
	t.Parallel()
	b := authsharedtest.VerifyEmailBody("alice@example.com", "123456")
	require.Equal(t, "alice@example.com", b["email"])
	require.Equal(t, "123456", b["code"])
}

func TestResendBody_ContainsExpectedKey(t *testing.T) {
	t.Parallel()
	b := authsharedtest.ResendBody("alice@example.com")
	require.Equal(t, "alice@example.com", b["email"])
}

func TestConfirmUnlockBody_ContainsExpectedKeys(t *testing.T) {
	t.Parallel()
	b := authsharedtest.ConfirmUnlockBody("alice@example.com", "654321")
	require.Equal(t, "alice@example.com", b["email"])
	require.Equal(t, "654321", b["code"])
}

func TestResetPasswordBody_ContainsExpectedKeys(t *testing.T) {
	t.Parallel()
	b := authsharedtest.ResetPasswordBody("alice@example.com", "111111", "NewP@ss1!")
	require.Equal(t, "alice@example.com", b["email"])
	require.Equal(t, "111111", b["code"])
	require.Equal(t, "NewP@ss1!", b["new_password"])
}

// ═══════════════════════════════════════════════════════════════════════════════
// Phase 4 — Integration tests (skipped when TEST_DATABASE_URL is not set)
// ═══════════════════════════════════════════════════════════════════════════════

// TestCreateUser_Integration verifies that CreateUser inserts a full user row
// via the register flow and returns a non-empty UserID and Email.
func TestCreateUser_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	email := authsharedtest.NewEmail(t)
	result := authsharedtest.CreateUser(t, testPool, q, email)
	require.NotEmpty(t, result.UserID, "UserID must be non-empty")
	require.Equal(t, email, result.Email, "Email must match the input")
}

// TestCreateUserUUID_Integration verifies that CreateUserUUID returns a valid,
// non-zero UUID for the newly created user.
func TestCreateUserUUID_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	email := authsharedtest.NewEmail(t)
	id := authsharedtest.CreateUserUUID(t, testPool, q, email)
	require.NotEqual(t, [16]byte{}, [16]byte(id), "UUID must not be zero")
}

// TestCreateUserDirect_Integration verifies that CreateUserDirect inserts only
// the bare user row (no verification token) and returns a non-zero UUID.
func TestCreateUserDirect_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	_, q := authsharedtest.MustBeginTx(t, testPool)
	email := authsharedtest.NewEmail(t)
	id := authsharedtest.CreateUserDirect(t, q, email)
	require.NotEqual(t, [16]byte{}, [16]byte(id), "UUID must not be zero")
}

// TestMustBeginTx_Integration verifies that MustBeginTx opens a transaction,
// returns a usable *db.Queries, and rolls back on cleanup so no data persists.
func TestMustBeginTx_Integration(t *testing.T) {
	if testPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}

	tx, q := authsharedtest.MustBeginTx(t, testPool)
	require.NotNil(t, tx, "MustBeginTx must return a non-nil pgx.Tx")
	require.NotNil(t, q, "MustBeginTx must return a non-nil *db.Queries")

	// Insert a row inside the transaction and confirm it is visible within the
	// same tx (i.e. the queries object is really bound to the transaction).
	email := authsharedtest.NewEmail(t)
	id := authsharedtest.CreateUserDirect(t, q, email)
	require.NotEqual(t, [16]byte{}, [16]byte(id))

	// After cleanup (rollback) the row must NOT be visible via a fresh pool query.
	// We verify this by checking the test doesn't panic and the pool is still usable.
	var count int
	err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM users WHERE email = $1`, email).Scan(&count)
	// The row is visible within the transaction but we can only query it via the
	// pool after commit. Since we haven't committed, count == 0 from the pool's perspective.
	require.NoError(t, err)
	require.Equal(t, 0, count, "row must not be visible outside the uncommitted transaction")
}

