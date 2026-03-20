package login_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/domain/auth/login"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/stretchr/testify/require"
)

// successSession returns a non-zero LoggedInSession for sign/cookie tests.
func successSession() login.LoggedInSession {
	return login.LoggedInSession{
		UserID:        authsharedtest.RandomUUID(),
		SessionID:     authsharedtest.RandomUUID(),
		RefreshJTI:    authsharedtest.RandomUUID(),
		FamilyID:      authsharedtest.RandomUUID(),
		RefreshExpiry: time.Now().Add(7 * 24 * time.Hour),
	}
}

// makeHandler creates a Handler with test-safe JWT secrets.
func makeHandler(svc login.Servicer) *login.Handler {
	return login.NewHandler(svc, token.JWTConfig{
		JWTAccessSecret:  "test-access-secret-32-bytes-long!",
		JWTRefreshSecret: "test-refresh-secret-32-bytes-long",
		AccessTTL:        15 * time.Minute,
		SecureCookies:    false,
	}, authshared.NoopAuthRecorder{})
}

// postLogin sends a POST /login with the given JSON body.
func postLogin(h *login.Handler, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	h.Login(w, r)
	return w
}

func TestHandler_Login_Success(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return successSession(), nil
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.NotEmpty(t, body["access_token"])

	// Refresh cookie must be set.
	cookies := w.Result().Cookies()
	var refreshCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "refresh_token" {
			refreshCookie = c
			break
		}
	}
	require.NotNil(t, refreshCookie, "expected refresh_token cookie")
	require.Equal(t, "/api/v1/auth", refreshCookie.Path)
	require.True(t, refreshCookie.HttpOnly)
	// SameSite and Secure are required security attributes (RULES.md §3.10).
	require.Equal(t, http.SameSiteStrictMode, refreshCookie.SameSite)
	require.False(t, refreshCookie.Secure, "Secure must be false in test (SecureCookies: false)")
	// MaxAge must be positive so the browser retains the cookie.
	require.Positive(t, refreshCookie.MaxAge, "expected positive MaxAge derived from refresh expiry")
}

func TestHandler_Login_InvalidCredentials(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{}, authshared.ErrInvalidCredentials
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"wrong"}`)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	// Verify the machine-readable code field so a typo in respond.Error is caught.
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "invalid_credentials", body["code"])
}

func TestHandler_Login_EmailNotVerified(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{}, authshared.ErrEmailNotVerified
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandler_Login_AccountInactive(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{}, authshared.ErrAccountInactive
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandler_Login_AccountLocked_Returns423(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{}, authshared.ErrAccountLocked
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	// Must be 423 Locked, not 403 Forbidden (see audit A2).
	require.Equal(t, http.StatusLocked, w.Code)
}

func TestHandler_Login_LoginLocked_Returns429WithRetryAfter(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{}, &authshared.LoginLockedError{RetryAfter: 5 * time.Minute}
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.NotEmpty(t, w.Header().Get("Retry-After"))
}

func TestHandler_Login_ValidationFailure_EmptyIdentifier(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{}
	w := postLogin(makeHandler(svc), `{"identifier":"","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_Login_ValidationFailure_EmptyPassword(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":""}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_Login_BodyTooLarge(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{}
	huge := strings.Repeat("x", (1<<20)+1)
	body := `{"identifier":"` + huge + `","password":"pw"}`
	w := postLogin(makeHandler(svc), body)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestHandler_Login_InvalidJSON(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{}
	w := postLogin(makeHandler(svc), `not json`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_Login_InternalError(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{}, errors.New("unexpected db error")
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestNewHandler_PanicOnShortAccessSecret verifies that NewHandler panics when
// JWTAccessSecret is shorter than 32 bytes. Catching this at construction time
// surfaces the misconfiguration at startup rather than at first login (ADR-001).
func TestNewHandler_PanicOnShortAccessSecret(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{}
	require.Panics(t, func() {
		login.NewHandler(svc, token.JWTConfig{
			JWTAccessSecret:  "short",
			JWTRefreshSecret: "test-refresh-secret-32-bytes-long",
		}, authshared.NoopAuthRecorder{})
	})
}

// TestNewHandler_PanicOnShortRefreshSecret verifies that NewHandler panics when
// JWTRefreshSecret is shorter than 32 bytes.
func TestNewHandler_PanicOnShortRefreshSecret(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{}
	require.Panics(t, func() {
		login.NewHandler(svc, token.JWTConfig{
			JWTAccessSecret:  "test-access-secret-32-bytes-long!",
			JWTRefreshSecret: "short",
		}, authshared.NoopAuthRecorder{})
	})
}

// ── NewHandler boundary tests ───────────────────────────────────────────────────────────────────

func TestNewHandler_PanicOnExactly31ByteAccessSecret(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{}
	require.Panics(t, func() {
		login.NewHandler(svc, token.JWTConfig{
			JWTAccessSecret:  strings.Repeat("x", 31),
			JWTRefreshSecret: "test-refresh-secret-32-bytes-long",
		}, authshared.NoopAuthRecorder{})
	})
}

func TestNewHandler_PanicOnExactly31ByteRefreshSecret(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{}
	require.Panics(t, func() {
		login.NewHandler(svc, token.JWTConfig{
			JWTAccessSecret:  "test-access-secret-32-bytes-long!",
			JWTRefreshSecret: strings.Repeat("x", 31),
		}, authshared.NoopAuthRecorder{})
	})
}

func TestNewHandler_NoPanicOnExactly32ByteAccessSecret(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{}
	require.NotPanics(t, func() {
		login.NewHandler(svc, token.JWTConfig{
			JWTAccessSecret:  strings.Repeat("x", 32),
			JWTRefreshSecret: "test-refresh-secret-32-bytes-long",
		}, authshared.NoopAuthRecorder{})
	})
}

func TestNewHandler_NoPanicOnExactly32ByteRefreshSecret(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{}
	require.NotPanics(t, func() {
		login.NewHandler(svc, token.JWTConfig{
			JWTAccessSecret:  "test-access-secret-32-bytes-long!",
			JWTRefreshSecret: strings.Repeat("x", 32),
		}, authshared.NoopAuthRecorder{})
	})
}

// ── Request parsing ───────────────────────────────────────────────────────────────────────────

func TestHandler_Login_EmptyBody(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{}
	w := postLogin(makeHandler(svc), "")
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandler_Login_IdentifierNormalisedBeforeService(t *testing.T) {
	t.Parallel()
	var gotInput login.LoginInput
	svc := &authsharedtest.LoginFakeServicer{
		LoginFn: func(_ context.Context, in login.LoginInput) (login.LoggedInSession, error) {
			gotInput = in
			return login.LoggedInSession{}, authshared.ErrInvalidCredentials
		},
	}
	postLogin(makeHandler(svc), `{"identifier":"  ALICE@EXAMPLE.COM  ","password":"Passw0rd!1"}`)
	require.Equal(t, "alice@example.com", gotInput.Identifier)
}

// ── Input propagation ──────────────────────────────────────────────────────────────────────────

func TestHandler_Login_IPAddressPropagated(t *testing.T) {
	t.Parallel()
	var gotInput login.LoginInput
	svc := &authsharedtest.LoginFakeServicer{
		LoginFn: func(_ context.Context, in login.LoginInput) (login.LoggedInSession, error) {
			gotInput = in
			return login.LoggedInSession{}, authshared.ErrInvalidCredentials
		},
	}
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"identifier":"u@x.com","password":"pw"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "10.0.0.1:9999"
	w := httptest.NewRecorder()
	makeHandler(svc).Login(w, r)
	require.Equal(t, "10.0.0.1", gotInput.IPAddress)
}

func TestHandler_Login_UserAgentPropagated(t *testing.T) {
	t.Parallel()
	var gotInput login.LoginInput
	svc := &authsharedtest.LoginFakeServicer{
		LoginFn: func(_ context.Context, in login.LoginInput) (login.LoggedInSession, error) {
			gotInput = in
			return login.LoggedInSession{}, authshared.ErrInvalidCredentials
		},
	}
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"identifier":"u@x.com","password":"pw"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "MyBrowser/1.0")
	w := httptest.NewRecorder()
	makeHandler(svc).Login(w, r)
	require.Equal(t, "MyBrowser/1.0", gotInput.UserAgent)
}

// ── Cookie attributes ──────────────────────────────────────────────────────────────────────────

func TestHandler_Login_CookieSecureTrue_WhenSecureCookiesEnabled(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return successSession(), nil
	}}
	h := login.NewHandler(svc, token.JWTConfig{
		JWTAccessSecret:  "test-access-secret-32-bytes-long!",
		JWTRefreshSecret: "test-refresh-secret-32-bytes-long",
		AccessTTL:        15 * time.Minute,
		SecureCookies:    true,
	}, authshared.NoopAuthRecorder{})
	w := postLogin(h, `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusOK, w.Code)
	var refreshCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" {
			refreshCookie = c
			break
		}
	}
	require.NotNil(t, refreshCookie)
	require.True(t, refreshCookie.Secure, "Secure must be true when SecureCookies: true")
}

func TestHandler_Login_CookieMaxAgeDerivedFromRefreshExpiry(t *testing.T) {
	t.Parallel()
	expiry := time.Now().Add(7 * 24 * time.Hour)
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{
			UserID:        authsharedtest.RandomUUID(),
			SessionID:     authsharedtest.RandomUUID(),
			RefreshJTI:    authsharedtest.RandomUUID(),
			FamilyID:      authsharedtest.RandomUUID(),
			RefreshExpiry: expiry,
		}, nil
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusOK, w.Code)
	var refreshCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" {
			refreshCookie = c
			break
		}
	}
	require.NotNil(t, refreshCookie)
	wantMaxAge := int(time.Until(expiry).Seconds())
	require.InDelta(t, wantMaxAge, refreshCookie.MaxAge, 2)
}

// ── Response body shape ────────────────────────────────────────────────────────────────────────

func TestHandler_Login_ResponseBodyContainsAccessToken(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return successSession(), nil
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Contains(t, body, "access_token", "response body must contain access_token key")
}

func TestHandler_Login_SuccessResponseContentType(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return successSession(), nil
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")
}

// ── LoginLockedError boundary ────────────────────────────────────────────────────────────────

func TestHandler_Login_LoginLocked_ZeroRetryAfterClampedToOne(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{}, &authshared.LoginLockedError{RetryAfter: 0}
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "1", w.Header().Get("Retry-After"), "zero RetryAfter must be clamped to 1")
}

func TestHandler_Login_PlainErrLoginLocked_NoRetryAfterHeader(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{}, authshared.ErrLoginLocked // plain sentinel, not *LoginLockedError
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Empty(t, w.Header().Get("Retry-After"), "plain ErrLoginLocked must not set Retry-After")
}

// ── Error response invariants ───────────────────────────────────────────────────────────────────

func TestHandler_Login_AllErrorResponsesHaveCodeField(t *testing.T) {
	t.Parallel()
	errCases := []struct {
		name string
		err  error
	}{
		{"invalid_credentials", authshared.ErrInvalidCredentials},
		{"email_not_verified", authshared.ErrEmailNotVerified},
		{"account_inactive", authshared.ErrAccountInactive},
		{"account_locked", authshared.ErrAccountLocked},
		{"login_locked", &authshared.LoginLockedError{RetryAfter: 5 * time.Minute}},
		{"internal_error", errors.New("unexpected db error")},
	}
	for _, tc := range errCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
				return login.LoggedInSession{}, tc.err
			}}
			w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Contains(t, body, "code", "error response for %s must contain code field", tc.name)
		})
	}
}

func TestHandler_Login_AllErrorResponsesHaveJSONContentType(t *testing.T) {
	t.Parallel()
	errCases := []struct {
		name string
		err  error
	}{
		{"invalid_credentials", authshared.ErrInvalidCredentials},
		{"email_not_verified", authshared.ErrEmailNotVerified},
		{"account_inactive", authshared.ErrAccountInactive},
		{"account_locked", authshared.ErrAccountLocked},
		{"login_locked", &authshared.LoginLockedError{RetryAfter: 5 * time.Minute}},
		{"internal_error", errors.New("unexpected")},
	}
	for _, tc := range errCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
				return login.LoggedInSession{}, tc.err
			}}
			w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
			require.Contains(t, w.Header().Get("Content-Type"), "application/json",
				"error response for %s must have application/json Content-Type", tc.name)
		})
	}
}

// TestHandler_Login_TokenMintError verifies the handler returns HTTP 500 when
// token.MintTokens fails. A zero RefreshExpiry (not in the future) causes
// GenerateRefreshToken to reject it, which exercises the signErr != nil branch
// in handler.go after LoginTx has already committed (ADR-001).
func TestHandler_Login_TokenMintError(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{
		LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
			return login.LoggedInSession{
				UserID:        authsharedtest.RandomUUID(),
				SessionID:     authsharedtest.RandomUUID(),
				RefreshJTI:    authsharedtest.RandomUUID(),
				FamilyID:      authsharedtest.RandomUUID(),
				RefreshExpiry: time.Time{}, // zero value — not in the future; GenerateRefreshToken rejects it
			}, nil
		},
	}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "internal_error", body["code"])
}

func TestHandler_Login_TokenMintError_PastExpiry(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{
		LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
			return login.LoggedInSession{
				UserID:        authsharedtest.RandomUUID(),
				SessionID:     authsharedtest.RandomUUID(),
				RefreshJTI:    authsharedtest.RandomUUID(),
				FamilyID:      authsharedtest.RandomUUID(),
				RefreshExpiry: time.Now().Add(-1 * time.Minute), // already in the past
			}, nil
		},
	}
	w := postLogin(makeHandler(svc), `{"identifier":"user@example.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "internal_error", body["code"])
}

func TestHandler_Login_EmailNotVerified_CodeField(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{}, authshared.ErrEmailNotVerified
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"u@x.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusForbidden, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "email_not_verified", body["code"])
}

func TestHandler_Login_AccountInactive_CodeField(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{}, authshared.ErrAccountInactive
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"u@x.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusForbidden, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "account_inactive", body["code"])
}

func TestHandler_Login_AccountLocked_CodeField(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{}, authshared.ErrAccountLocked
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"u@x.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusLocked, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "account_locked", body["code"])
}

func TestHandler_Login_LoginLocked_CodeField(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.LoginFakeServicer{LoginFn: func(_ context.Context, _ login.LoginInput) (login.LoggedInSession, error) {
		return login.LoggedInSession{}, &authshared.LoginLockedError{RetryAfter: 5 * time.Minute}
	}}
	w := postLogin(makeHandler(svc), `{"identifier":"u@x.com","password":"Passw0rd!1"}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "login_locked", body["code"])
}
