package unlock_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/auth/unlock"
	mailertest "github.com/7-Dany/store/backend/internal/platform/mailer/testutil"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newTestHandler(svc unlock.Servicer) *unlock.Handler {
	base := mailertest.NoopBase()
	base.Timeout = time.Second
	return unlock.NewHandler(svc, base, authshared.NoopAuthRecorder{})
}

func requestUnlockBody(email string) string {
	return `{"email":"` + email + `"}`
}

func confirmUnlockBody(email, code string) string {
	return `{"email":"` + email + `","code":"` + code + `"}`
}

// ── RequestUnlock handler tests ───────────────────────────────────────────────

func TestHandler_RequestUnlock_AlwaysReturns202(t *testing.T) {
	t.Parallel()

	t.Run("success returns 202", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{
			RequestUnlockFn: func(_ context.Context, _ unlock.RequestUnlockInput) (authshared.OTPIssuanceResult, error) {
				return authshared.OTPIssuanceResult{}, nil
			},
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/request-unlock", strings.NewReader(requestUnlockBody("a@example.com")))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).RequestUnlock(w, r)
		require.Equal(t, http.StatusAccepted, w.Code)
	})

	t.Run("service error still returns 202 (anti-enumeration)", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{
			RequestUnlockFn: func(_ context.Context, _ unlock.RequestUnlockInput) (authshared.OTPIssuanceResult, error) {
				return authshared.OTPIssuanceResult{}, authshared.ErrUserNotFound
			},
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/request-unlock", strings.NewReader(requestUnlockBody("ghost@example.com")))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).RequestUnlock(w, r)
		require.Equal(t, http.StatusAccepted, w.Code)
	})

	t.Run("non-empty RawCode with failing mailer still returns 202 (fire-and-forget, BUG 1 fix)", func(t *testing.T) {
		t.Parallel()
		// Service returns a real code, but the mailer is configured to fail.
		// The handler must log the error at WARN and still return 202.
		svc := &authsharedtest.UnlockFakeServicer{
			RequestUnlockFn: func(_ context.Context, _ unlock.RequestUnlockInput) (authshared.OTPIssuanceResult, error) {
				return authshared.OTPIssuanceResult{
					UserID:  uuid.New().String(),
					Email:   "a@example.com",
					RawCode: "123456",
				}, nil
			},
		}
		base := mailertest.ErrorBase(authshared.ErrAccountLocked)
		base.Timeout = time.Second
		h := unlock.NewHandler(svc, base, authshared.NoopAuthRecorder{})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/request-unlock", strings.NewReader(requestUnlockBody("a@example.com")))
		r.Header.Set("Content-Type", "application/json")
		h.RequestUnlock(w, r)
		require.Equal(t, http.StatusAccepted, w.Code)
	})

	t.Run("cooldown active (active token exists) returns 202 with identical body", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{
			RequestUnlockFn: func(_ context.Context, _ unlock.RequestUnlockInput) (authshared.OTPIssuanceResult, error) {
				return authshared.OTPIssuanceResult{}, nil // suppressed silently
			},
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/request-unlock",
			strings.NewReader(requestUnlockBody("a@example.com")))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).RequestUnlock(w, r)
		require.Equal(t, http.StatusAccepted, w.Code)
	})

	t.Run("validation error (empty email) returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/request-unlock", strings.NewReader(`{"email":""}`))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).RequestUnlock(w, r)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("body too large returns 413", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{}
		huge := `{"email":"` + strings.Repeat("a", 2<<20) + `@example.com"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/request-unlock", strings.NewReader(huge))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).RequestUnlock(w, r)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})
}

// ── ConfirmUnlock handler tests ───────────────────────────────────────────────

func TestHandler_ConfirmUnlock(t *testing.T) {
	t.Parallel()

	t.Run("success returns 200", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{
			ConsumeUnlockTokenFn: func(_ context.Context, _ unlock.ConfirmUnlockInput) error { return nil },
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/confirm-unlock", strings.NewReader(confirmUnlockBody("a@example.com", "123456")))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).ConfirmUnlock(w, r)
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("ErrTokenExpired returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{
			ConsumeUnlockTokenFn: func(_ context.Context, _ unlock.ConfirmUnlockInput) error {
				return authshared.ErrTokenExpired
			},
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/confirm-unlock", strings.NewReader(confirmUnlockBody("a@example.com", "123456")))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).ConfirmUnlock(w, r)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("ErrTokenNotFound returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{
			ConsumeUnlockTokenFn: func(_ context.Context, _ unlock.ConfirmUnlockInput) error {
				return authshared.ErrTokenNotFound
			},
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/confirm-unlock", strings.NewReader(confirmUnlockBody("a@example.com", "123456")))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).ConfirmUnlock(w, r)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("ErrTokenAlreadyUsed returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{
			ConsumeUnlockTokenFn: func(_ context.Context, _ unlock.ConfirmUnlockInput) error {
				return authshared.ErrTokenAlreadyUsed
			},
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/confirm-unlock", strings.NewReader(confirmUnlockBody("a@example.com", "123456")))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).ConfirmUnlock(w, r)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("ErrInvalidCode returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{
			ConsumeUnlockTokenFn: func(_ context.Context, _ unlock.ConfirmUnlockInput) error {
				return authshared.ErrInvalidCode
			},
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/confirm-unlock", strings.NewReader(confirmUnlockBody("a@example.com", "123456")))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).ConfirmUnlock(w, r)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("ErrTooManyAttempts returns 429", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{
			ConsumeUnlockTokenFn: func(_ context.Context, _ unlock.ConfirmUnlockInput) error {
				return authshared.ErrTooManyAttempts
			},
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/confirm-unlock", strings.NewReader(confirmUnlockBody("a@example.com", "123456")))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).ConfirmUnlock(w, r)
		require.Equal(t, http.StatusTooManyRequests, w.Code)
	})

	t.Run("unrecognised error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{
			ConsumeUnlockTokenFn: func(_ context.Context, _ unlock.ConfirmUnlockInput) error {
				return authshared.ErrUserNotFound // not mapped in ConfirmUnlock switch
			},
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/confirm-unlock", strings.NewReader(confirmUnlockBody("a@example.com", "123456")))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).ConfirmUnlock(w, r)
		require.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("ErrAccountLocked returns 423 (regression: was 500 before fix)", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{
			ConsumeUnlockTokenFn: func(_ context.Context, _ unlock.ConfirmUnlockInput) error {
				return authshared.ErrAccountLocked
			},
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/confirm-unlock", strings.NewReader(confirmUnlockBody("a@example.com", "123456")))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).ConfirmUnlock(w, r)
		// Must return 423 Locked, not 500 (Bug #1 fix).
		require.Equal(t, http.StatusLocked, w.Code)
		require.Contains(t, w.Body.String(), `"account_locked"`)
	})

	t.Run("validation failure (empty email) returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/confirm-unlock", strings.NewReader(`{"email":"","code":"123456"}`))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).ConfirmUnlock(w, r)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("invalid code format returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/confirm-unlock", strings.NewReader(confirmUnlockBody("a@example.com", "abc")))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).ConfirmUnlock(w, r)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("body too large returns 413", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UnlockFakeServicer{}
		huge := `{"email":"` + strings.Repeat("a", 2<<20) + `@example.com","code":"123456"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/confirm-unlock", strings.NewReader(huge))
		r.Header.Set("Content-Type", "application/json")
		newTestHandler(svc).ConfirmUnlock(w, r)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})
}
