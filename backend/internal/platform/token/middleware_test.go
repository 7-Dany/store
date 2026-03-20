package token_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/token"
	tokentest "github.com/7-Dany/store/backend/internal/platform/token/testutil"
)

// okHandler is a trivial next handler that records whether it was called and
// captures the request context for assertion.
type okHandler struct {
	called bool
	ctx    context.Context
}

func (h *okHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.called = true
	h.ctx = r.Context()
	w.WriteHeader(http.StatusOK)
}

// fakeBlocklist is a TokenBlocklist stub for middleware tests.
type fakeBlocklist struct {
	blocked bool
	err     error
}

func (f *fakeBlocklist) BlockToken(_ context.Context, _ string, _ time.Duration) error {
	return nil
}

func (f *fakeBlocklist) IsTokenBlocked(_ context.Context, _ string) (bool, error) {
	return f.blocked, f.err
}

var _ kvstore.TokenBlocklist = (*fakeBlocklist)(nil)

func makeRequest(authHeader string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/ping", nil)
	if authHeader != "" {
		r.Header.Set("Authorization", authHeader)
	}
	return r
}

func validBearerHeader(t *testing.T, userID, sessionID string) string {
	t.Helper()
	tok, err := token.GenerateAccessToken(userID, sessionID, time.Minute, testSecret)
	require.NoError(t, err)
	return "Bearer " + tok
}

// ── Construction-time guards ─────────────────────────────────────────────────

// TestAuth_PanicsOnEmptySecret asserts that Auth panics immediately when
// constructed with an empty secret, before any request is served.
func TestAuth_PanicsOnEmptySecret(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() {
		token.Auth("", nil, nil)
	}, "Auth must panic on empty secret so misconfiguration is caught at startup")
}

// ── Missing / malformed header ────────────────────────────────────────────────

func TestAuth_NoAuthorizationHeader(t *testing.T) {
	t.Parallel()
	next := &okHandler{}
	mw := token.Auth(testSecret, nil, nil)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest(""))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, next.called)
}

func TestAuth_MissingBearerPrefix(t *testing.T) {
	t.Parallel()
	next := &okHandler{}
	mw := token.Auth(testSecret, nil, nil)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest("Token abc123"))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, next.called)
}

func TestAuth_BearerPrefixWithNoToken(t *testing.T) {
	t.Parallel()
	next := &okHandler{}
	mw := token.Auth(testSecret, nil, nil)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest("Bearer "))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, next.called)
}

// ── Invalid / expired tokens ──────────────────────────────────────────────────

func TestAuth_InvalidToken(t *testing.T) {
	t.Parallel()
	next := &okHandler{}
	mw := token.Auth(testSecret, nil, nil)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest("Bearer not.a.valid.jwt"))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, next.called)
}

func TestAuth_WrongSecret(t *testing.T) {
	t.Parallel()
	tok, err := token.GenerateAccessToken(uuid.NewString(), uuid.NewString(), time.Minute, testSecret)
	require.NoError(t, err)
	next := &okHandler{}
	mw := token.Auth("different-secret", nil, nil)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest("Bearer "+tok))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, next.called)
}

// ── Happy path: valid token, nil blocklist ────────────────────────────────────

func TestAuth_ValidToken_NilBlocklist(t *testing.T) {
	t.Parallel()
	userID := uuid.NewString()
	sessionID := uuid.NewString()
	next := &okHandler{}
	mw := token.Auth(testSecret, nil, nil)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest(validBearerHeader(t, userID, sessionID)))
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, next.called)

	gotUser, ok := token.UserIDFromContext(next.ctx)
	require.True(t, ok)
	require.Equal(t, userID, gotUser)

	gotSession, ok := token.SessionIDFromContext(next.ctx)
	require.True(t, ok)
	require.Equal(t, sessionID, gotSession)
}

// ── Blocklist checks ──────────────────────────────────────────────────────────

func TestAuth_BlocklistedToken_Returns401(t *testing.T) {
	t.Parallel()
	bl := &fakeBlocklist{blocked: true}
	next := &okHandler{}
	mw := token.Auth(testSecret, bl, nil)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest(validBearerHeader(t, uuid.NewString(), uuid.NewString())))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, next.called)
}

// TestAuth_BlocklistError_FailsOpen asserts that a transient blocklist store
// error (e.g. Redis timeout) does NOT reject the request. The session stays
// alive and the error is only logged. Access tokens expire naturally (≤15 min),
// bounding the window where a revoked JTI could slip through during an outage.
func TestAuth_BlocklistError_FailsOpen(t *testing.T) {
	t.Parallel()
	bl := &fakeBlocklist{err: errors.New("redis timeout")}
	next := &okHandler{}
	mw := token.Auth(testSecret, bl, nil)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest(validBearerHeader(t, uuid.NewString(), uuid.NewString())))
	// Request must succeed — a Redis outage must not log the user out.
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, next.called)
}

func TestAuth_NotBlocklisted_AllowsThrough(t *testing.T) {
	t.Parallel()
	bl := &fakeBlocklist{blocked: false}
	userID := uuid.NewString()
	sessionID := uuid.NewString()
	next := &okHandler{}
	mw := token.Auth(testSecret, bl, nil)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest(validBearerHeader(t, userID, sessionID)))
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, next.called)
}

// ── In-memory blocklist integration ──────────────────────────────────────────

func TestAuth_InMemoryBlocklist_RevokedJTI(t *testing.T) {
	t.Parallel()
	store := kvstore.NewInMemoryStore(0)
	userID := uuid.NewString()
	sessionID := uuid.NewString()

	tok, err := token.GenerateAccessToken(userID, sessionID, time.Minute, testSecret)
	require.NoError(t, err)
	claims, err := token.ParseAccessToken(tok, testSecret)
	require.NoError(t, err)
	require.NoError(t, store.BlockToken(context.Background(), claims.ID, time.Minute))

	next := &okHandler{}
	mw := token.Auth(testSecret, store, store)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest("Bearer "+tok))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, next.called)
}

// ── Concurrent access ─────────────────────────────────────────────────────────

func TestAuth_ConcurrentRequests(t *testing.T) {
	t.Parallel()
	store := kvstore.NewInMemoryStore(0)
	mw := token.Auth(testSecret, store, store)

	const goroutines = 50
	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			next := &okHandler{}
			handler := mw(next)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, makeRequest(validBearerHeader(t, uuid.NewString(), uuid.NewString())))
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
}

// ── Per-user block (password-reset blocklist) ────────────────────────────────

// TestAuth_UserBlocked_Returns401 asserts that a valid token whose owner has a
// pr_blocked_user: key in the KV store is rejected with 401.
func TestAuth_UserBlocked_Returns401(t *testing.T) {
	t.Parallel()
	store := kvstore.NewInMemoryStore(0)
	userID := uuid.NewString()
	sessionID := uuid.NewString()

	// Write the per-user block key with a far-future Unix timestamp so that any
	// token minted during this test has iat <= blockTime and is therefore blocked.
	// Using "1" (Unix epoch) would let every real token through because their
	// iat is in 2026, which is after time.Unix(1, 0).
	blockTime := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	require.NoError(t, store.Set(context.Background(), "pr_blocked_user:"+userID, blockTime, time.Minute))

	next := &okHandler{}
	mw := token.Auth(testSecret, nil, store)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest(validBearerHeader(t, userID, sessionID)))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, next.called)
}

// TestAuth_UserNotBlocked_AllowsThrough asserts that a valid token whose owner
// has no pr_blocked_user: key passes step 3b and reaches the handler.
func TestAuth_UserNotBlocked_AllowsThrough(t *testing.T) {
	t.Parallel()
	store := kvstore.NewInMemoryStore(0)
	userID := uuid.NewString()
	sessionID := uuid.NewString()

	next := &okHandler{}
	mw := token.Auth(testSecret, nil, store)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest(validBearerHeader(t, userID, sessionID)))
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, next.called)
}

// ── FIX 10: additional tests ───────────────────────────────────────────────────

// TestAuth_ExpiredToken_Returns401 asserts that an expired access token is rejected with 401.
func TestAuth_ExpiredToken_Returns401(t *testing.T) {
	t.Parallel()
	tok := tokentest.MakeExpiredAccessToken(t, uuid.NewString(), uuid.NewString(), testSecret)
	next := &okHandler{}
	mw := token.Auth(testSecret, nil, nil)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest("Bearer "+tok))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, next.called)
}

// TestAuth_InvalidSubClaim_Returns401 asserts that a token with a non-UUID sub claim is rejected.
func TestAuth_InvalidSubClaim_Returns401(t *testing.T) {
	t.Parallel()
	now := time.Now()
	claims := token.AccessClaims{
		SessionID: uuid.NewString(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    token.Issuer,
			Subject:   "not-a-uuid",
			Audience:  jwt.ClaimStrings{token.AudienceAccess},
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	}
	raw := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := raw.SignedString([]byte(testSecret))
	require.NoError(t, err)

	next := &okHandler{}
	mw := token.Auth(testSecret, nil, nil)(next)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, makeRequest("Bearer "+signed))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, next.called)
}
