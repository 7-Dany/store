package tokentest_test

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/platform/token"
	tokentest "github.com/7-Dany/store/backend/internal/platform/token/testutil"
)

const testSecret = "test-secret-for-tokentest-package"

// fakeT is a minimal testing.TB that captures Fatalf calls without actually
// failing the outer test. Because t.Fatalf ultimately calls runtime.Goexit(),
// callers must invoke the helper inside a separate goroutine and wait for it.
//
// Only Helper and Fatalf are implemented; all other testing.TB methods are
// satisfied by the embedded nil interface — they must not be called.
type fakeT struct {
	testing.TB // nil; satisfies the interface but panics if any other method is called
	mu      sync.Mutex
	failed  bool
	message string
}

func (f *fakeT) Helper() {}

func (f *fakeT) Fatalf(format string, args ...any) {
	f.mu.Lock()
	f.failed = true
	f.message = fmt.Sprintf(format, args...)
	f.mu.Unlock()
	runtime.Goexit() // mirrors what *testing.T.Fatalf does
}

// runInGoroutine runs fn in a fresh goroutine and blocks until it exits
// (either normally or via runtime.Goexit from Fatalf).
func runInGoroutine(fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
}

// ── InjectUserIDForTest ───────────────────────────────────────────────────────

func TestInjectUserIDForTest_RoundTrip(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	ctx := tokentest.InjectUserIDForTest(context.Background(), userID)
	got, ok := token.UserIDFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, userID, got)
}

func TestInjectUserIDForTest_DoesNotAffectSessionID(t *testing.T) {
	t.Parallel()
	ctx := tokentest.InjectUserIDForTest(context.Background(), uuid.NewString())
	_, ok := token.SessionIDFromContext(ctx)
	assert.False(t, ok)
}

// ── InjectSessionIDForTest ────────────────────────────────────────────────────

func TestInjectSessionIDForTest_RoundTrip(t *testing.T) {
	t.Parallel()
	sessionID := uuid.NewString()
	ctx := tokentest.InjectSessionIDForTest(context.Background(), sessionID)
	got, ok := token.SessionIDFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, sessionID, got)
}

func TestInjectSessionIDForTest_DoesNotAffectUserID(t *testing.T) {
	t.Parallel()
	ctx := tokentest.InjectSessionIDForTest(context.Background(), uuid.NewString())
	_, ok := token.UserIDFromContext(ctx)
	assert.False(t, ok)
}

func TestInjectBoth_IndependentKeys(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	sessionID := uuid.NewString()
	ctx := tokentest.InjectUserIDForTest(context.Background(), userID)
	ctx = tokentest.InjectSessionIDForTest(ctx, sessionID)

	gotUser, okUser := token.UserIDFromContext(ctx)
	gotSession, okSession := token.SessionIDFromContext(ctx)
	require.True(t, okUser)
	require.True(t, okSession)
	assert.Equal(t, userID, gotUser)
	assert.Equal(t, sessionID, gotSession)
}

// ── MakeAccessToken ───────────────────────────────────────────────────────────

func TestMakeAccessToken_ReturnsValidToken(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	sessionID := uuid.NewString()

	tok := tokentest.MakeAccessToken(t, userID, sessionID, testSecret, 15*time.Minute)
	require.NotEmpty(t, tok)

	claims, err := token.ParseAccessToken(tok, testSecret)
	require.NoError(t, err)
	assert.Equal(t, userID, claims.Subject)
	assert.Equal(t, sessionID, claims.SessionID)
}

func TestMakeAccessToken_TokenIsNotExpired(t *testing.T) {
	t.Parallel()
	tok := tokentest.MakeAccessToken(t, uuid.NewString(), uuid.NewString(), testSecret, time.Hour)
	_, err := token.ParseAccessToken(tok, testSecret)
	require.NoError(t, err, "freshly-minted token should not be expired")
}

func TestMakeAccessToken_UniquePerCall(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	sessionID := uuid.NewString()

	tok1 := tokentest.MakeAccessToken(t, userID, sessionID, testSecret, time.Minute)
	tok2 := tokentest.MakeAccessToken(t, userID, sessionID, testSecret, time.Minute)
	assert.NotEqual(t, tok1, tok2, "each call embeds a unique JTI so tokens must differ")
}

// TestMakeAccessToken_FatalfOnEmptySecret covers the t.Fatalf branch (line 30-32).
// An empty secret causes GenerateAccessToken to return an error, which the
// helper converts into a fatal test failure via t.Fatalf + runtime.Goexit.
func TestMakeAccessToken_FatalfOnEmptySecret(t *testing.T) {
	t.Parallel()
	ft := &fakeT{}
	runInGoroutine(func() {
		tokentest.MakeAccessToken(ft, uuid.NewString(), uuid.NewString(), "", time.Minute)
	})
	assert.True(t, ft.failed, "MakeAccessToken must call Fatalf when secret is empty")
	assert.Contains(t, ft.message, "MakeAccessToken")
}

// ── MakeRefreshToken ──────────────────────────────────────────────────────────

func TestMakeRefreshToken_ReturnsValidToken(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	sessionID := uuid.NewString()

	tok := tokentest.MakeRefreshToken(t, userID, sessionID, testSecret, 7*24*time.Hour)
	require.NotEmpty(t, tok)

	claims, err := token.ParseRefreshToken(tok, testSecret)
	require.NoError(t, err)
	assert.Equal(t, userID, claims.Subject)
	assert.Equal(t, sessionID, claims.SessionID)
	assert.NotEmpty(t, claims.ID, "jti must be set")
	assert.NotEmpty(t, claims.FamilyID, "familyID must be set")
}

func TestMakeRefreshToken_TokenIsNotExpired(t *testing.T) {
	t.Parallel()
	tok := tokentest.MakeRefreshToken(t, uuid.NewString(), uuid.NewString(), testSecret, time.Hour)
	_, err := token.ParseRefreshToken(tok, testSecret)
	require.NoError(t, err, "freshly-minted refresh token should not be expired")
}

func TestMakeRefreshToken_IsRejectedByParseAccessToken(t *testing.T) {
	t.Parallel()
	tok := tokentest.MakeRefreshToken(t, uuid.NewString(), uuid.NewString(), testSecret, time.Hour)
	_, err := token.ParseAccessToken(tok, testSecret)
	require.Error(t, err, "refresh token must be rejected by ParseAccessToken")
}

// TestMakeRefreshToken_FatalfOnEmptySecret covers the t.Fatalf branch (line 43-45).
func TestMakeRefreshToken_FatalfOnEmptySecret(t *testing.T) {
	t.Parallel()
	ft := &fakeT{}
	runInGoroutine(func() {
		tokentest.MakeRefreshToken(ft, uuid.NewString(), uuid.NewString(), "", time.Hour)
	})
	assert.True(t, ft.failed, "MakeRefreshToken must call Fatalf when secret is empty")
	assert.Contains(t, ft.message, "MakeRefreshToken")
}

// ── MakeExpiredAccessToken ────────────────────────────────────────────────────

func TestMakeExpiredAccessToken_IsRejectedByParser(t *testing.T) {
	t.Parallel()
	tok := tokentest.MakeExpiredAccessToken(t, uuid.NewString(), uuid.NewString(), testSecret)
	require.NotEmpty(t, tok)

	_, err := token.ParseAccessToken(tok, testSecret)
	require.Error(t, err, "expired token must be rejected")
}

func TestMakeExpiredAccessToken_IsRejectedByAccessAndRefreshParsers(t *testing.T) {
	t.Parallel()
	tok := tokentest.MakeExpiredAccessToken(t, uuid.NewString(), uuid.NewString(), testSecret)

	_, accessErr := token.ParseAccessToken(tok, testSecret)
	_, refreshErr := token.ParseRefreshToken(tok, testSecret)

	require.Error(t, accessErr, "expired access token must be rejected by ParseAccessToken")
	require.Error(t, refreshErr, "access token must also be rejected by ParseRefreshToken (wrong audience)")
}

// TestMakeExpiredAccessToken_FatalfOnEmptySecret covers the t.Fatalf branch (line 54-56).
func TestMakeExpiredAccessToken_FatalfOnEmptySecret(t *testing.T) {
	t.Parallel()
	ft := &fakeT{}
	runInGoroutine(func() {
		tokentest.MakeExpiredAccessToken(ft, uuid.NewString(), uuid.NewString(), "")
	})
	assert.True(t, ft.failed, "MakeExpiredAccessToken must call Fatalf when secret is empty")
	assert.Contains(t, ft.message, "MakeExpiredAccessToken")
}

// ── MakeExpiredRefreshToken ─────────────────────────────────────────────────────────────────

// TestMakeExpiredRefreshToken_IsRejectedByParser asserts that the token is
// rejected by ParseRefreshToken due to expiry.
func TestMakeExpiredRefreshToken_IsRejectedByParser(t *testing.T) {
	t.Parallel()
	tok := tokentest.MakeExpiredRefreshToken(t, uuid.NewString(), uuid.NewString(), testSecret)
	require.NotEmpty(t, tok)

	_, err := token.ParseRefreshToken(tok, testSecret)
	require.Error(t, err, "expired refresh token must be rejected")
}

// TestMakeExpiredRefreshToken_IsRejectedByBothParsers asserts the token is
// rejected by ParseRefreshToken (expired) and by ParseAccessToken (wrong audience).
func TestMakeExpiredRefreshToken_IsRejectedByBothParsers(t *testing.T) {
	t.Parallel()
	tok := tokentest.MakeExpiredRefreshToken(t, uuid.NewString(), uuid.NewString(), testSecret)

	_, refreshErr := token.ParseRefreshToken(tok, testSecret)
	_, accessErr := token.ParseAccessToken(tok, testSecret)

	require.Error(t, refreshErr, "expired refresh token must be rejected by ParseRefreshToken")
	require.Error(t, accessErr, "refresh token must also be rejected by ParseAccessToken (wrong audience)")
}

// TestMakeExpiredRefreshToken_FatalfOnEmptySecret covers the Fatalf branch.
func TestMakeExpiredRefreshToken_FatalfOnEmptySecret(t *testing.T) {
	t.Parallel()
	ft := &fakeT{}
	runInGoroutine(func() {
		tokentest.MakeExpiredRefreshToken(ft, uuid.NewString(), uuid.NewString(), "")
	})
	assert.True(t, ft.failed, "MakeExpiredRefreshToken must call Fatalf when secret is empty")
	assert.Contains(t, ft.message, "MakeExpiredRefreshToken")
}
