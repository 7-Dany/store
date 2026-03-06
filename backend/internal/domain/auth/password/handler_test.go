package password_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/7-Dany/store/backend/internal/domain/auth/password"
	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	mailertest "github.com/7-Dany/store/backend/internal/platform/mailer/testutil"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/stretchr/testify/require"
)

func newTestHandler(svc password.Servicer) *password.Handler {
	return password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, kvstore.NewInMemoryStore(0), 10*time.Minute)
}

func postJSON(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// preloadGrant creates an in-memory KV store pre-populated with one grant token
// whose value is "<email>\n<code>". Returns the store, the token UUID string, and
// a convenience request body string for POST /reset-password.
func preloadGrant(t *testing.T, email, code, newPassword string) (kvstore.Store, string, string) {
	t.Helper()
	gs := kvstore.NewInMemoryStore(0)
	tok := uuid.New().String()
	err := gs.Set(context.Background(), "prc:"+tok, email+"\n"+code, 10*time.Minute)
	require.NoError(t, err)
	body := `{"reset_token":"` + tok + `","new_password":"` + newPassword + `"}`
	return gs, tok, body
}

// ── ForgotPassword ────────────────────────────────────────────────────────────

func TestHandler_ForgotPassword_AlwaysReturns202(t *testing.T) {
	t.Parallel()

	t.Run("success with raw code returns 202", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			RequestPasswordResetFn: func(_ context.Context, _ password.ForgotPasswordInput) (authshared.OTPIssuanceResult, error) {
				return authshared.OTPIssuanceResult{UserID: "uid", Email: "a@example.com", RawCode: "123456"}, nil
			},
		}
		w := postJSON(password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, kvstore.NewInMemoryStore(0), 10*time.Minute).ForgotPassword,
			`{"email":"a@example.com"}`)
		require.Equal(t, http.StatusAccepted, w.Code)
	})

	t.Run("service error still returns 202 (BUG 1 regression)", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			RequestPasswordResetFn: func(_ context.Context, _ password.ForgotPasswordInput) (authshared.OTPIssuanceResult, error) {
				return authshared.OTPIssuanceResult{}, errors.New("db exploded")
			},
		}
		w := postJSON(newTestHandler(svc).ForgotPassword, `{"email":"a@example.com"}`)
		require.Equal(t, http.StatusAccepted, w.Code)
	})

	t.Run("mail delivery failure still returns 202 (BUG 1 regression)", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			RequestPasswordResetFn: func(_ context.Context, _ password.ForgotPasswordInput) (authshared.OTPIssuanceResult, error) {
				return authshared.OTPIssuanceResult{UserID: "uid", Email: "a@example.com", RawCode: "123456"}, nil
			},
		}
		base := mailertest.ErrorBase(errors.New("smtp down"))
		base.Timeout = 1
		h := password.NewHandler(svc, base, nil, nil, 0, false, kvstore.NewInMemoryStore(0), 10*time.Minute)
		w := postJSON(h.ForgotPassword, `{"email":"a@example.com"}`)
		require.Equal(t, http.StatusAccepted, w.Code)
	})

	t.Run("unknown email returns 202 (zero result)", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			RequestPasswordResetFn: func(_ context.Context, _ password.ForgotPasswordInput) (authshared.OTPIssuanceResult, error) {
				return authshared.OTPIssuanceResult{}, nil // empty RawCode
			},
		}
		w := postJSON(newTestHandler(svc).ForgotPassword, `{"email":"ghost@example.com"}`)
		require.Equal(t, http.StatusAccepted, w.Code)
	})

	t.Run("uniform 202 message body", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		w := postJSON(newTestHandler(svc).ForgotPassword, `{"email":"a@example.com"}`)
		require.Equal(t, http.StatusAccepted, w.Code)
		require.Contains(t, w.Body.String(), "if that email is registered")
	})

	t.Run("validation failure empty email returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		w := postJSON(newTestHandler(svc).ForgotPassword, `{"email":""}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("body too large returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		bigBody := strings.Repeat("x", 2<<20) // 2 MiB > maxBodyBytes
		// The body is not valid JSON, so the decoder returns a SyntaxError before
		// MaxBytesReader fires. Both paths map to 422 (deliberate design: all
		// body-decode failures share one status code — see respond.DecodeJSON).
		w := postJSON(newTestHandler(svc).ForgotPassword, bigBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("invalid email format returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		w := postJSON(newTestHandler(svc).ForgotPassword, `{"email":"not-an-email"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})
}

// ── VerifyResetCode ───────────────────────────────────────────────────────────

func TestHandler_VerifyResetCode(t *testing.T) {
	t.Parallel()

	t.Run("success returns 200 with reset_token and expires_in", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			VerifyResetCodeFn: func(_ context.Context, _ password.VerifyResetCodeInput) (string, error) {
				return "a@example.com", nil
			},
		}
		gs := kvstore.NewInMemoryStore(0)
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.VerifyResetCode, `{"email":"a@example.com","code":"123456"}`)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.NotEmpty(t, resp["reset_token"], "reset_token must be present in response")
		require.Equal(t, float64(600), resp["expires_in"], "expires_in must be 600 seconds (10 minutes)")
	})

	t.Run("ErrTokenExpired returns 410", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			VerifyResetCodeFn: func(_ context.Context, _ password.VerifyResetCodeInput) (string, error) {
				return "", authshared.ErrTokenExpired
			},
		}
		w := postJSON(newTestHandler(svc).VerifyResetCode, `{"email":"a@example.com","code":"123456"}`)
		require.Equal(t, http.StatusGone, w.Code)
	})

	t.Run("ErrTooManyAttempts returns 429", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			VerifyResetCodeFn: func(_ context.Context, _ password.VerifyResetCodeInput) (string, error) {
				return "", authshared.ErrTooManyAttempts
			},
		}
		w := postJSON(newTestHandler(svc).VerifyResetCode, `{"email":"a@example.com","code":"123456"}`)
		require.Equal(t, http.StatusTooManyRequests, w.Code)
	})

	t.Run("ErrTokenNotFound returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			VerifyResetCodeFn: func(_ context.Context, _ password.VerifyResetCodeInput) (string, error) {
				return "", authshared.ErrTokenNotFound
			},
		}
		w := postJSON(newTestHandler(svc).VerifyResetCode, `{"email":"a@example.com","code":"123456"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("ErrTokenAlreadyUsed returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			VerifyResetCodeFn: func(_ context.Context, _ password.VerifyResetCodeInput) (string, error) {
				return "", authshared.ErrTokenAlreadyUsed
			},
		}
		w := postJSON(newTestHandler(svc).VerifyResetCode, `{"email":"a@example.com","code":"123456"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("ErrInvalidCode returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			VerifyResetCodeFn: func(_ context.Context, _ password.VerifyResetCodeInput) (string, error) {
				return "", authshared.ErrInvalidCode
			},
		}
		w := postJSON(newTestHandler(svc).VerifyResetCode, `{"email":"a@example.com","code":"123456"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("unknown service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			VerifyResetCodeFn: func(_ context.Context, _ password.VerifyResetCodeInput) (string, error) {
				return "", errors.New("unexpected db error")
			},
		}
		w := postJSON(newTestHandler(svc).VerifyResetCode, `{"email":"a@example.com","code":"123456"}`)
		require.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("validation failure — empty email returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		w := postJSON(newTestHandler(svc).VerifyResetCode, `{"email":"","code":"123456"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("validation failure — bad code format returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		w := postJSON(newTestHandler(svc).VerifyResetCode, `{"email":"a@b.com","code":"12x"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("nil grantStore returns 503", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			VerifyResetCodeFn: func(_ context.Context, _ password.VerifyResetCodeInput) (string, error) {
				return "a@example.com", nil
			},
		}
		// Passing nil as grantStore triggers the 503 path in the handler.
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, nil, 10*time.Minute)
		w := postJSON(h.VerifyResetCode, `{"email":"a@example.com","code":"123456"}`)
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("service NOT called on validation failure", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			VerifyResetCodeFn: func(_ context.Context, _ password.VerifyResetCodeInput) (string, error) {
				t.Fatal("VerifyResetCode service must not be called on handler-level validation failure")
				return "", nil
			},
		}
		// Empty email — validation fires before the service call.
		w := postJSON(newTestHandler(svc).VerifyResetCode, `{"email":"","code":"123456"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})
}

// ── ResetPassword ─────────────────────────────────────────────────────────────

func TestHandler_ResetPassword(t *testing.T) {
	t.Parallel()

	t.Run("success returns 200", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		gs, _, body := preloadGrant(t, "a@example.com", "123456", "NewPassw0rd!")
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("ErrTokenExpired returns 410", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			ConsumePasswordResetTokenFn: func(_ context.Context, _ password.ResetPasswordInput) ([16]byte, error) {
				return [16]byte{}, authshared.ErrTokenExpired
			},
		}
		gs, _, body := preloadGrant(t, "a@example.com", "123456", "NewPassw0rd!")
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusGone, w.Code)
	})

	t.Run("ErrTokenNotFound returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			ConsumePasswordResetTokenFn: func(_ context.Context, _ password.ResetPasswordInput) ([16]byte, error) {
				return [16]byte{}, authshared.ErrTokenNotFound
			},
		}
		gs, _, body := preloadGrant(t, "a@example.com", "123456", "NewPassw0rd!")
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("ErrTokenAlreadyUsed returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			ConsumePasswordResetTokenFn: func(_ context.Context, _ password.ResetPasswordInput) ([16]byte, error) {
				return [16]byte{}, authshared.ErrTokenAlreadyUsed
			},
		}
		gs, _, body := preloadGrant(t, "a@example.com", "123456", "NewPassw0rd!")
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("ErrInvalidCode returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			ConsumePasswordResetTokenFn: func(_ context.Context, _ password.ResetPasswordInput) ([16]byte, error) {
				return [16]byte{}, authshared.ErrInvalidCode
			},
		}
		gs, _, body := preloadGrant(t, "a@example.com", "123456", "NewPassw0rd!")
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("ErrTooManyAttempts returns 429", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			ConsumePasswordResetTokenFn: func(_ context.Context, _ password.ResetPasswordInput) ([16]byte, error) {
				return [16]byte{}, authshared.ErrTooManyAttempts
			},
		}
		gs, _, body := preloadGrant(t, "a@example.com", "123456", "NewPassw0rd!")
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusTooManyRequests, w.Code)
	})

	t.Run("ErrPasswordTooShort returns 422 (BUG 2 regression)", func(t *testing.T) {
		t.Parallel()
		// Password strength is validated by validateResetPasswordRequest before the
		// service is called; this confirms the validator path is wired correctly.
		svc := &authsharedtest.PasswordFakeServicer{}
		w := postJSON(newTestHandler(svc).ResetPassword, `{"reset_token":"some-uuid","new_password":"Sh0rt!"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("ErrPasswordNoSymbol returns 422 (BUG 2 regression)", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		w := postJSON(newTestHandler(svc).ResetPassword, `{"reset_token":"some-uuid","new_password":"NoSymbol123"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("unknown service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			ConsumePasswordResetTokenFn: func(_ context.Context, _ password.ResetPasswordInput) ([16]byte, error) {
				return [16]byte{}, errors.New("unexpected db error")
			},
		}
		gs, _, body := preloadGrant(t, "a@example.com", "123456", "NewPassw0rd!")
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("success calls blocklist with correct user ID", func(t *testing.T) {
		t.Parallel()
		wantUID := [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1} // 00000000-0000-0000-0000-000000000001
		svc := &authsharedtest.PasswordFakeServicer{
			ConsumePasswordResetTokenFn: func(_ context.Context, _ password.ResetPasswordInput) ([16]byte, error) {
				return wantUID, nil
			},
		}
		bl := &mockBlocklist{}
		gs, _, body := preloadGrant(t, "a@example.com", "123456", "NewPassw0rd!")
		h := password.NewHandler(svc, mailertest.NoopBase(), bl, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusOK, w.Code)
		bl.mu.Lock()
		defer bl.mu.Unlock()
		require.Len(t, bl.calls, 1)
		require.Equal(t, "00000000-0000-0000-0000-000000000001", bl.calls[0])
	})

	t.Run("success returns 200 even when blocklist fails", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		bl := &mockBlocklist{err: errors.New("kv store down")}
		gs, _, body := preloadGrant(t, "a@example.com", "123456", "NewPassw0rd!")
		h := password.NewHandler(svc, mailertest.NoopBase(), bl, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("ErrSamePassword returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			ConsumePasswordResetTokenFn: func(_ context.Context, _ password.ResetPasswordInput) ([16]byte, error) {
				return [16]byte{}, password.ErrSamePassword
			},
		}
		gs, _, body := preloadGrant(t, "a@example.com", "123456", "NewPassw0rd!")
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("nil blocklist with successful service call returns 200", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		gs, _, body := preloadGrant(t, "a@example.com", "123456", "NewPassw0rd!")
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("ErrPasswordEmpty returns 422", func(t *testing.T) {
		t.Parallel()
		// Validator catches empty new_password before the grant token is looked up.
		svc := &authsharedtest.PasswordFakeServicer{}
		w := postJSON(newTestHandler(svc).ResetPassword, `{"reset_token":"some-uuid","new_password":""}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("body too large returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		bigBody := strings.Repeat("x", 2<<20)
		// Invalid JSON → SyntaxError → 422 (same as other MaxBytesReader cases).
		w := postJSON(newTestHandler(svc).ResetPassword, bigBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// ── New cases required by the 3-step flow ────────────────────────────────

	t.Run("missing reset_token returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		w := postJSON(newTestHandler(svc).ResetPassword, `{"reset_token":"","new_password":"NewPassw0rd!"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("unknown or expired grant token returns 422", func(t *testing.T) {
		t.Parallel()
		// The KV store is empty — grantStore.Get returns ErrNotFound → 422.
		svc := &authsharedtest.PasswordFakeServicer{}
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, kvstore.NewInMemoryStore(0), 10*time.Minute)
		body := `{"reset_token":"` + uuid.New().String() + `","new_password":"NewPassw0rd!"}`
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("grant token is deleted from the KV store after a successful reset", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		gs, tok, body := preloadGrant(t, "a@example.com", "123456", "NewPassw0rd!")
		h := password.NewHandler(svc, mailertest.NoopBase(), nil, nil, 0, false, gs, 10*time.Minute)
		w := postJSON(h.ResetPassword, body)
		require.Equal(t, http.StatusOK, w.Code)
		// The key must be absent from the store — grant tokens are single-use.
		exists, err := gs.Exists(context.Background(), "prc:"+tok)
		require.NoError(t, err)
		require.False(t, exists, "grant token must be deleted after a successful reset")
	})
}

// mockBlocklist records Block calls and optionally returns a configured error.
type mockBlocklist struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (m *mockBlocklist) Block(_ context.Context, userID string) error {
	m.mu.Lock()
	m.calls = append(m.calls, userID)
	m.mu.Unlock()
	return m.err
}

// mockJTIBlocklist records BlockToken calls and optionally returns an error.
type mockJTIBlocklist struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (m *mockJTIBlocklist) BlockToken(_ context.Context, jti string, _ time.Duration) error {
	m.mu.Lock()
	m.calls = append(m.calls, jti)
	m.mu.Unlock()
	return m.err
}

// ── ChangePassword ────────────────────────────────────────────────────────────

// newCPHandler builds a Handler for ChangePassword tests.
func newCPHandler(svc password.Servicer, jtibl *mockJTIBlocklist) *password.Handler {
	var bl password.JTIBlocklist
	if jtibl != nil {
		bl = jtibl
	}
	return password.NewHandler(svc, mailertest.NoopBase(), nil, bl, 15*time.Minute, false, kvstore.NewInMemoryStore(0), 10*time.Minute)
}

// postJSONWithUserID fires a POST with a fake user-ID injected into the context.
func postJSONWithUserID(h http.HandlerFunc, userID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := token.InjectUserIDForTest(req.Context(), userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func TestHandler_ChangePassword(t *testing.T) {
	t.Parallel()

	uid := uuid.NewString()
	validBody := `{"old_password":"OldPassw0rd!","new_password":"NewPassw0rd!"}`

	t.Run("success returns 200 and clears refresh cookie", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{} // UpdatePasswordHashFn nil → nil error
		w := postJSONWithUserID(newCPHandler(svc, nil).ChangePassword, uid, validBody)
		require.Equal(t, http.StatusOK, w.Code)
		// The Set-Cookie header must contain the clear-cookie directive.
		found := false
		for _, c := range w.Result().Cookies() {
			if c.Name == "refresh_token" && c.MaxAge < 0 {
				found = true
			}
		}
		require.True(t, found, "refresh_token cookie must be cleared on success")
	})

	t.Run("ErrInvalidCredentials returns 401", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			UpdatePasswordHashFn: func(_ context.Context, _ password.ChangePasswordInput) error {
				return authshared.ErrInvalidCredentials
			},
		}
		w := postJSONWithUserID(newCPHandler(svc, nil).ChangePassword, uid, validBody)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("ErrUserNotFound returns 404", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			UpdatePasswordHashFn: func(_ context.Context, _ password.ChangePasswordInput) error {
				return authshared.ErrUserNotFound
			},
		}
		w := postJSONWithUserID(newCPHandler(svc, nil).ChangePassword, uid, validBody)
		require.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("password strength error returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			UpdatePasswordHashFn: func(_ context.Context, _ password.ChangePasswordInput) error {
				return authshared.ErrPasswordTooShort
			},
		}
		w := postJSONWithUserID(newCPHandler(svc, nil).ChangePassword, uid, validBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("unknown service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			UpdatePasswordHashFn: func(_ context.Context, _ password.ChangePasswordInput) error {
				return errors.New("db gone")
			},
		}
		w := postJSONWithUserID(newCPHandler(svc, nil).ChangePassword, uid, validBody)
		require.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("missing old_password returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		w := postJSONWithUserID(newCPHandler(svc, nil).ChangePassword, uid, `{"old_password":"","new_password":"NewPassw0rd!"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("missing new_password returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		w := postJSONWithUserID(newCPHandler(svc, nil).ChangePassword, uid, `{"old_password":"OldPassw0rd!","new_password":""}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("missing userID in context returns 401", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		// postJSON fires without userID in context.
		w := postJSON(newCPHandler(svc, nil).ChangePassword, validBody)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("success calls jtiBlocklist when JTI is in context", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		bl := &mockJTIBlocklist{}
		h := newCPHandler(svc, bl)
		// Inject both userID and JTI into the context.
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(validBody))
		req.Header.Set("Content-Type", "application/json")
		ctx := token.InjectUserIDForTest(req.Context(), uid)
		ctx = token.InjectJTIForTest(ctx, "test-jti-value")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		h.ChangePassword(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		bl.mu.Lock()
		defer bl.mu.Unlock()
		require.Len(t, bl.calls, 1)
		require.Equal(t, "test-jti-value", bl.calls[0])
	})

	t.Run("success returns 200 even when jtiBlocklist fails", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		bl := &mockJTIBlocklist{err: errors.New("kv store down")}
		h := newCPHandler(svc, bl)
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(validBody))
		req.Header.Set("Content-Type", "application/json")
		ctx := token.InjectUserIDForTest(req.Context(), uid)
		ctx = token.InjectJTIForTest(ctx, "some-jti")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		h.ChangePassword(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("nil jtiBlocklist does not panic on success", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		w := postJSONWithUserID(newCPHandler(svc, nil).ChangePassword, uid, validBody)
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("service receives correct input fields", func(t *testing.T) {
		t.Parallel()
		var captured password.ChangePasswordInput
		svc := &authsharedtest.PasswordFakeServicer{
			UpdatePasswordHashFn: func(_ context.Context, in password.ChangePasswordInput) error {
				captured = in
				return nil
			},
		}
		w := postJSONWithUserID(newCPHandler(svc, nil).ChangePassword, uid, validBody)
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, uid, captured.UserID)
		require.Equal(t, "OldPassw0rd!", captured.OldPassword)
		require.Equal(t, "NewPassw0rd!", captured.NewPassword)
	})

	t.Run("ErrSamePassword returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			UpdatePasswordHashFn: func(_ context.Context, _ password.ChangePasswordInput) error {
				return password.ErrSamePassword
			},
		}
		w := postJSONWithUserID(newCPHandler(svc, nil).ChangePassword, uid, validBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("ErrTooManyAttempts returns 429 with forgot-password hint", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{
			UpdatePasswordHashFn: func(_ context.Context, _ password.ChangePasswordInput) error {
				return authshared.ErrTooManyAttempts
			},
		}
		w := postJSONWithUserID(newCPHandler(svc, nil).ChangePassword, uid, validBody)
		require.Equal(t, http.StatusTooManyRequests, w.Code)
		require.Contains(t, w.Body.String(), "forgot-password",
			"429 body must hint at the forgot-password flow")
	})

	t.Run("success does not call jtiBlocklist when JTI is absent from context", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.PasswordFakeServicer{}
		bl := &mockJTIBlocklist{}
		h := newCPHandler(svc, bl)
		// postJSONWithUserID injects only userID, not JTI.
		w := postJSONWithUserID(h.ChangePassword, uid, validBody)
		require.Equal(t, http.StatusOK, w.Code)
		bl.mu.Lock()
		defer bl.mu.Unlock()
		require.Empty(t, bl.calls, "BlockToken must not be called when JTI is absent")
	})
}
