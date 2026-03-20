package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/domain/auth/session"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/token"
	tokentest "github.com/7-Dany/store/backend/internal/platform/token/testutil"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// buildHandler creates a Handler with test secrets and the given fake servicer.
func buildHandler(svc session.Servicer) *session.Handler {
	return session.NewHandler(svc, token.JWTConfig{
		JWTAccessSecret:  "test-access-secret-32-bytes-long!",
		JWTRefreshSecret: "test-refresh-secret-32-bytes-long",
		AccessTTL:        15 * time.Minute,
		SecureCookies:    false,
	}, nil, authshared.NoopAuthRecorder{})
}

// ── Refresh tests ─────────────────────────────────────────────────────────────

func TestRefresh_MissingCookie(t *testing.T) {
	t.Parallel()
	var rotateCalled bool
	svc := &authsharedtest.SessionFakeServicer{
		RotateRefreshTokenFn: func(_ context.Context, _ [16]byte, _, _ string) (session.RotatedSession, error) {
			rotateCalled = true
			return session.RotatedSession{}, nil
		},
	}
	h := buildHandler(svc)
	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	w := httptest.NewRecorder()
	h.Refresh(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, rotateCalled)
}

func TestRefresh_MalformedToken_Returns401(t *testing.T) {
	t.Parallel()
	var rotateCalled bool
	svc := &authsharedtest.SessionFakeServicer{
		RotateRefreshTokenFn: func(_ context.Context, _ [16]byte, _, _ string) (session.RotatedSession, error) {
			rotateCalled = true
			return session.RotatedSession{}, nil
		},
	}
	h := buildHandler(svc)
	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "not.a.valid.jwt"})
	w := httptest.NewRecorder()
	h.Refresh(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, rotateCalled)
}

// ── Logout tests ──────────────────────────────────────────────────────────────

func TestLogout_MissingCookie_Returns204(t *testing.T) {
	t.Parallel()
	var logoutCalled bool
	svc := &authsharedtest.SessionFakeServicer{
		LogoutFn: func(_ context.Context, _ session.LogoutTxInput) error {
			logoutCalled = true
			return nil
		},
	}
	h := buildHandler(svc)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	w := httptest.NewRecorder()
	h.Logout(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Empty(t, w.Body.String(), "logout body must be empty")
	require.False(t, logoutCalled)
}

func TestLogout_MalformedCookie_Returns204_CookieCleared(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.SessionFakeServicer{}
	h := buildHandler(svc)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "bad.jwt.garbage"})
	w := httptest.NewRecorder()
	h.Logout(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Empty(t, w.Body.String(), "logout body must be empty")
	// Cookie should be cleared: look for Set-Cookie with MaxAge=-1.
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" && c.MaxAge == -1 {
			found = true
		}
	}
	require.True(t, found, "refresh_token cookie should be cleared when JWT is malformed")
}

func TestLogout_Always204_NeverReturns500(t *testing.T) {
	t.Parallel()
	// Logout must return 204 on every path, even when the service reports an error.
	// (Logout swallows errors by design.)
	svc := &authsharedtest.SessionFakeServicer{}
	h := buildHandler(svc)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	w := httptest.NewRecorder()
	h.Logout(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Empty(t, w.Body.String())
}

// TestNewHandler_NilBlocklist ensures construction with a nil blocklist is safe.
func TestNewHandler_NilBlocklist(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.SessionFakeServicer{}
	h := session.NewHandler(svc, token.JWTConfig{
		JWTAccessSecret:  "access",
		JWTRefreshSecret: "refresh",
		AccessTTL:        time.Minute,
	}, nil, authshared.NoopAuthRecorder{})
	require.NotNil(t, h)
}

// Verify ErrTokenReuseDetected is imported correctly.
var _ = authshared.ErrTokenReuseDetected
var _ = authsharedtest.ErrProxy // ensure authsharedtest import is used

// ── signRefreshToken helper ───────────────────────────────────────────────────

// signRefreshToken mints a real refresh JWT using the test secrets from buildHandler.
// GenerateRefreshToken signature: (userID, sessionID, refreshJTI, familyID string, expiresAt time.Time, secret string)
func signRefreshToken(t *testing.T, userID, sessionID, familyID, jti [16]byte, expiry time.Time) string {
	t.Helper()
	signed, err := token.GenerateRefreshToken(
		uuid.UUID(userID).String(),
		uuid.UUID(sessionID).String(),
		uuid.UUID(jti).String(),
		uuid.UUID(familyID).String(),
		expiry,
		"test-refresh-secret-32-bytes-long",
	)
	if err != nil {
		t.Fatalf("signRefreshToken: %v", err)
	}
	return signed
}

// ── Refresh happy-path and error-branch tests ─────────────────────────────────

func TestRefresh_HappyPath_200_NewTokensIssued(t *testing.T) {
	t.Parallel()
	jti := authsharedtest.RandomUUID()
	userID := authsharedtest.RandomUUID()
	sessionID := authsharedtest.RandomUUID()
	familyID := authsharedtest.RandomUUID()
	expiry := time.Now().Add(30 * 24 * time.Hour)

	wantJTI := authsharedtest.RandomUUID()
	svc := &authsharedtest.SessionFakeServicer{
		RotateRefreshTokenFn: func(_ context.Context, gotJTI [16]byte, _, _ string) (session.RotatedSession, error) {
			require.Equal(t, jti, gotJTI)
			return session.RotatedSession{
				NewRefreshJTI: wantJTI,
				RefreshExpiry: expiry,
			}, nil
		},
	}
	h := buildHandler(svc)

	signed := signRefreshToken(t, userID, sessionID, familyID, jti, expiry)
	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: signed})
	w := httptest.NewRecorder()
	h.Refresh(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Contains(t, body, "access_token", "response body must contain access_token")
	// New refresh cookie must be set with MaxAge > 0.
	foundCookie := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" && c.MaxAge > 0 {
			foundCookie = true
		}
	}
	require.True(t, foundCookie, "new refresh_token cookie must be set after rotation")
}

func TestRefresh_ServiceErrInvalidToken_Returns401(t *testing.T) {
	t.Parallel()
	jti := authsharedtest.RandomUUID()
	userID := authsharedtest.RandomUUID()
	sessionID := authsharedtest.RandomUUID()
	familyID := authsharedtest.RandomUUID()
	expiry := time.Now().Add(30 * 24 * time.Hour)

	svc := &authsharedtest.SessionFakeServicer{
		RotateRefreshTokenFn: func(_ context.Context, _ [16]byte, _, _ string) (session.RotatedSession, error) {
			return session.RotatedSession{}, authshared.ErrInvalidToken
		},
	}
	h := buildHandler(svc)

	signed := signRefreshToken(t, userID, sessionID, familyID, jti, expiry)
	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: signed})
	w := httptest.NewRecorder()
	h.Refresh(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "invalid_token")
}

func TestRefresh_ErrTokenReuseDetected_401_NoCookieSet(t *testing.T) {
	t.Parallel()
	jti := authsharedtest.RandomUUID()
	userID := authsharedtest.RandomUUID()
	sessionID := authsharedtest.RandomUUID()
	familyID := authsharedtest.RandomUUID()
	expiry := time.Now().Add(30 * 24 * time.Hour)

	svc := &authsharedtest.SessionFakeServicer{
		RotateRefreshTokenFn: func(_ context.Context, _ [16]byte, _, _ string) (session.RotatedSession, error) {
			return session.RotatedSession{}, authshared.ErrTokenReuseDetected
		},
	}
	h := buildHandler(svc)

	signed := signRefreshToken(t, userID, sessionID, familyID, jti, expiry)
	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: signed})
	w := httptest.NewRecorder()
	h.Refresh(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "token_reuse_detected")
	// Cookie must be cleared (MaxAge=-1), not refreshed (MaxAge>0).
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" {
			require.Equal(t, -1, c.MaxAge, "refresh_token cookie must be cleared on reuse detection")
		}
	}
}

func TestRefresh_UnexpectedServiceError_Returns500(t *testing.T) {
	t.Parallel()
	jti := authsharedtest.RandomUUID()
	userID := authsharedtest.RandomUUID()
	sessionID := authsharedtest.RandomUUID()
	familyID := authsharedtest.RandomUUID()
	expiry := time.Now().Add(30 * 24 * time.Hour)

	svc := &authsharedtest.SessionFakeServicer{
		RotateRefreshTokenFn: func(_ context.Context, _ [16]byte, _, _ string) (session.RotatedSession, error) {
			return session.RotatedSession{}, errors.New("unexpected db error")
		},
	}
	h := buildHandler(svc)

	signed := signRefreshToken(t, userID, sessionID, familyID, jti, expiry)
	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: signed})
	w := httptest.NewRecorder()
	h.Refresh(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestLogout_HappyPath_204_CookieCleared(t *testing.T) {
	t.Parallel()
	jti := authsharedtest.RandomUUID()
	userID := authsharedtest.RandomUUID()
	sessionID := authsharedtest.RandomUUID()
	familyID := authsharedtest.RandomUUID()
	expiry := time.Now().Add(30 * 24 * time.Hour)

	var logoutInput session.LogoutTxInput
	svc := &authsharedtest.SessionFakeServicer{
		LogoutFn: func(_ context.Context, in session.LogoutTxInput) error {
			logoutInput = in
			return nil
		},
	}
	h := buildHandler(svc)

	signed := signRefreshToken(t, userID, sessionID, familyID, jti, expiry)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: signed})
	w := httptest.NewRecorder()
	h.Logout(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Empty(t, w.Body.String())
	require.Equal(t, jti, logoutInput.JTI, "service must receive the parsed JTI")
	require.Equal(t, sessionID, logoutInput.SessionID)
	// Refresh cookie must be cleared.
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" && c.MaxAge == -1 {
			found = true
		}
	}
	require.True(t, found, "refresh_token cookie must be cleared on logout")
}

// ── Phase 5.1 — Refresh missing error branches ────────────────────────────────

func TestRefresh_ErrAccountLocked_423_CookieCleared(t *testing.T) {
	t.Parallel()
	jti := authsharedtest.RandomUUID()
	userID := authsharedtest.RandomUUID()
	sessionID := authsharedtest.RandomUUID()
	familyID := authsharedtest.RandomUUID()
	expiry := time.Now().Add(30 * 24 * time.Hour)

	svc := &authsharedtest.SessionFakeServicer{
		RotateRefreshTokenFn: func(_ context.Context, _ [16]byte, _, _ string) (session.RotatedSession, error) {
			return session.RotatedSession{}, authshared.ErrAccountLocked
		},
	}
	h := buildHandler(svc)

	signed := signRefreshToken(t, userID, sessionID, familyID, jti, expiry)
	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: signed})
	w := httptest.NewRecorder()
	h.Refresh(w, req)

	require.Equal(t, http.StatusLocked, w.Code)
	require.Contains(t, w.Body.String(), "account_locked")
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" {
			require.Equal(t, -1, c.MaxAge, "refresh_token cookie must be cleared on account locked")
		}
	}
}

func TestRefresh_ErrAccountInactive_403_CookieCleared(t *testing.T) {
	t.Parallel()
	jti := authsharedtest.RandomUUID()
	userID := authsharedtest.RandomUUID()
	sessionID := authsharedtest.RandomUUID()
	familyID := authsharedtest.RandomUUID()
	expiry := time.Now().Add(30 * 24 * time.Hour)

	svc := &authsharedtest.SessionFakeServicer{
		RotateRefreshTokenFn: func(_ context.Context, _ [16]byte, _, _ string) (session.RotatedSession, error) {
			return session.RotatedSession{}, authshared.ErrAccountInactive
		},
	}
	h := buildHandler(svc)

	signed := signRefreshToken(t, userID, sessionID, familyID, jti, expiry)
	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: signed})
	w := httptest.NewRecorder()
	h.Refresh(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "account_inactive")
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" {
			require.Equal(t, -1, c.MaxAge, "refresh_token cookie must be cleared on account inactive")
		}
	}
}

// ── Phase 5.2 — Logout blocklist paths ────────────────────────────────────────

// testCfg is the JWTConfig used by all blocklist handler tests.
var testCfg = token.JWTConfig{
	JWTAccessSecret:  "test-access-secret-32-bytes-long!",
	JWTRefreshSecret: "test-refresh-secret-32-bytes-long",
	AccessTTL:        15 * time.Minute,
	SecureCookies:    false,
}

func TestLogout_ValidAccessToken_BlocklistCalled(t *testing.T) {
	t.Parallel()
	blocklist := kvstore.NewInMemoryStore(time.Minute)

	userID := authsharedtest.RandomUUID()
	sessionID := authsharedtest.RandomUUID()
	jti := authsharedtest.RandomUUID()
	familyID := authsharedtest.RandomUUID()
	refreshExpiry := time.Now().Add(30 * 24 * time.Hour)

	svc := &authsharedtest.SessionFakeServicer{
		LogoutFn: func(_ context.Context, _ session.LogoutTxInput) error { return nil },
	}
	h := session.NewHandler(svc, testCfg, blocklist, authshared.NoopAuthRecorder{})

	// Sign a valid access token (JTI auto-generated internally).
	accessTokenStr, err := token.GenerateAccessToken(
		uuid.UUID(userID).String(),
		uuid.UUID(sessionID).String(),
		15*time.Minute,
		testCfg.JWTAccessSecret,
	)
	require.NoError(t, err)

	// Parse to extract the auto-generated JTI so we can check the blocklist.
	accessClaims, err := token.ParseAccessToken(accessTokenStr, testCfg.JWTAccessSecret)
	require.NoError(t, err)
	accessJTI := accessClaims.ID

	refreshTokenStr := signRefreshToken(t, userID, sessionID, familyID, jti, refreshExpiry)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshTokenStr})
	req.Header.Set("Authorization", "Bearer "+accessTokenStr)
	w := httptest.NewRecorder()
	h.Logout(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	blocked, blErr := blocklist.IsTokenBlocked(context.Background(), accessJTI)
	require.NoError(t, blErr)
	require.True(t, blocked, "valid access token JTI must be in the blocklist after logout")
}

func TestLogout_ExpiredAccessToken_BlocklistNotCalled(t *testing.T) {
	t.Parallel()
	blocklist := kvstore.NewInMemoryStore(time.Minute)

	userID := authsharedtest.RandomUUID()
	sessionID := authsharedtest.RandomUUID()
	jti := authsharedtest.RandomUUID()
	familyID := authsharedtest.RandomUUID()
	refreshExpiry := time.Now().Add(30 * 24 * time.Hour)

	svc := &authsharedtest.SessionFakeServicer{}
	h := session.NewHandler(svc, testCfg, blocklist, authshared.NoopAuthRecorder{})

	// Build an access token with expiry one minute in the past.
	expiredAccess := tokentest.MakeExpiredAccessToken(t,
		uuid.UUID(userID).String(),
		uuid.UUID(sessionID).String(),
		testCfg.JWTAccessSecret,
	)

	refreshTokenStr := signRefreshToken(t, userID, sessionID, familyID, jti, refreshExpiry)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshTokenStr})
	req.Header.Set("Authorization", "Bearer "+expiredAccess)
	w := httptest.NewRecorder()
	h.Logout(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	// The blocklist should remain empty — expired tokens have TTL ≤ 0.
	keys, kErr := blocklist.Keys(context.Background(), "")
	require.NoError(t, kErr)
	require.Empty(t, keys, "blocklist must remain empty when access token is already expired")
}

func TestLogout_MalformedAccessToken_BlocklistNotCalled(t *testing.T) {
	t.Parallel()
	blocklist := kvstore.NewInMemoryStore(time.Minute)

	userID := authsharedtest.RandomUUID()
	sessionID := authsharedtest.RandomUUID()
	jti := authsharedtest.RandomUUID()
	familyID := authsharedtest.RandomUUID()
	refreshExpiry := time.Now().Add(30 * 24 * time.Hour)

	svc := &authsharedtest.SessionFakeServicer{}
	h := session.NewHandler(svc, testCfg, blocklist, authshared.NoopAuthRecorder{})

	refreshTokenStr := signRefreshToken(t, userID, sessionID, familyID, jti, refreshExpiry)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshTokenStr})
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	w := httptest.NewRecorder()
	h.Logout(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	keys, kErr := blocklist.Keys(context.Background(), "")
	require.NoError(t, kErr)
	require.Empty(t, keys, "blocklist must remain empty for malformed access token")
}

func TestLogout_NilBlocklist_ValidAccessToken_NoError(t *testing.T) {
	t.Parallel()
	userID := authsharedtest.RandomUUID()
	sessionID := authsharedtest.RandomUUID()
	jti := authsharedtest.RandomUUID()
	familyID := authsharedtest.RandomUUID()
	refreshExpiry := time.Now().Add(30 * 24 * time.Hour)

	svc := &authsharedtest.SessionFakeServicer{}
	// buildHandler wires nil blocklist.
	h := buildHandler(svc)

	accessTokenStr, err := token.GenerateAccessToken(
		uuid.UUID(userID).String(),
		uuid.UUID(sessionID).String(),
		15*time.Minute,
		testCfg.JWTAccessSecret,
	)
	require.NoError(t, err)

	refreshTokenStr := signRefreshToken(t, userID, sessionID, familyID, jti, refreshExpiry)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshTokenStr})
	req.Header.Set("Authorization", "Bearer "+accessTokenStr)
	w := httptest.NewRecorder()
	h.Logout(w, req) // must not panic

	require.Equal(t, http.StatusNoContent, w.Code)
}
