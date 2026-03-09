package setpassword_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	setpassword "github.com/7-Dany/store/backend/internal/domain/profile/set-password"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// newTestHandler returns a Handler backed by a fake servicer.
func newTestHandler(svc setpassword.Servicer) *setpassword.Handler {
	return setpassword.NewHandler(svc)
}

// postJSONWithUserID fires a POST with a fake user ID injected into the context.
func postJSONWithUserID(h http.HandlerFunc, userID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := token.InjectUserIDForTest(req.Context(), userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// postJSON fires a POST without a user ID in the context (simulates no JWT).
func postJSON(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

const (
	validUID      = "00000000-0000-0000-0000-000000000001"
	validPassword = "Str0ng!Pass"
	validBody     = `{"new_password":"` + validPassword + `"}`
)

// ── TestHandler_SetPassword ───────────────────────────────────────────────────

func TestHandler_SetPassword(t *testing.T) {
	t.Parallel()

	// ── T-01: Happy path ──────────────────────────────────────────────────────

	t.Run("success returns 200 with message", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{} // SetPasswordFn nil → nil error
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, validBody)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), "password set successfully")
	})

	// ── T-02: User already has a password ─────────────────────────────────────

	t.Run("ErrPasswordAlreadySet returns 422 password_already_set", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{
			SetPasswordFn: func(_ context.Context, _ setpassword.SetPasswordInput) error {
				return setpassword.ErrPasswordAlreadySet
			},
		}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, validBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "password_already_set")
	})

	// ── T-03: Concurrency race ─────────────────────────────────────────────────

	t.Run("concurrency race — service returns ErrPasswordAlreadySet from TX — 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{
			SetPasswordFn: func(_ context.Context, _ setpassword.SetPasswordInput) error {
				return setpassword.ErrPasswordAlreadySet
			},
		}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, validBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// ── T-04: Ghost user ──────────────────────────────────────────────────────

	t.Run("ErrUserNotFound returns 404 not_found", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{
			SetPasswordFn: func(_ context.Context, _ setpassword.SetPasswordInput) error {
				return profileshared.ErrUserNotFound
			},
		}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, validBody)
		require.Equal(t, http.StatusNotFound, w.Code)
		require.Contains(t, w.Body.String(), "not_found")
	})

	// ── T-06: Empty new_password ───────────────────────────────────────────────

	t.Run("empty new_password returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, `{"new_password":""}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-07: Too short ────────────────────────────────────────────────────────

	t.Run("too short — 4 chars — returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, `{"new_password":"Ab1!"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// ── T-08: Too long ─────────────────────────────────────────────────────────

	t.Run("too long — 73 chars — returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{}
		// 73 chars: 'A' + 'b' + '1' + '!' to hit the char-class guards
		// then padded with 'x' to exactly 73 bytes.
		long := "Ab1!" + strings.Repeat("x", 69) // 4 + 69 = 73 bytes
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID,
			`{"new_password":"`+long+`"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// ── T-09: Missing uppercase ────────────────────────────────────────────────

	t.Run("no uppercase — returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, `{"new_password":"abcd1234!"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// ── T-10: Missing lowercase ────────────────────────────────────────────────

	t.Run("no lowercase — returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, `{"new_password":"ABCD1234!"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// ── T-11: Missing digit ────────────────────────────────────────────────────

	t.Run("no digit — returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, `{"new_password":"Abcdefgh!"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// ── T-12: Missing symbol ───────────────────────────────────────────────────

	t.Run("no symbol — returns 422", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, `{"new_password":"Abcde123"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// ── T-13: Body > 1 MiB ────────────────────────────────────────────────────

	t.Run("body exceeds MaxBodyBytes — returns 413", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{}
		bigBody := strings.Repeat("x", 2<<20) // 2 MiB of non-JSON bytes
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, bigBody)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	// ── T-14: Malformed JSON ───────────────────────────────────────────────────

	t.Run("malformed JSON — returns 400", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, `{"new_password":`)
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	// ── T-16: Store error on GetUserForSetPassword ─────────────────────────────
	// At the handler level the error is opaque — any non-sentinel service error
	// maps to 500. The service test (T-16) verifies the wrapping; this test only
	// verifies the handler maps it correctly.

	t.Run("unexpected service error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{
			SetPasswordFn: func(_ context.Context, _ setpassword.SetPasswordInput) error {
				return errors.New("db: connection refused")
			},
		}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, validBody)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── T-17: Store error on SetPasswordHashTx ────────────────────────────────
	// Same handler path as T-16 — covered by the same case above (both are
	// unrecognised service errors). Kept as a named sub-test for traceability.

	t.Run("T-17: SetPasswordHashTx service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{
			SetPasswordFn: func(_ context.Context, _ setpassword.SetPasswordInput) error {
				return errors.New("store: tx aborted")
			},
		}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, validUID, validBody)
		require.Equal(t, http.StatusInternalServerError, w.Code)
	})

	// ── Missing user ID in context (JWT absent — handler guard) ───────────────
	// T-15 (JWT middleware) is covered by middleware tests. This sub-test
	// verifies the handler's own mustUserID guard independently.

	t.Run("missing user ID in context returns 401", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{}
		// postJSON fires without injecting a user ID — simulates missing JWT.
		w := postJSON(newTestHandler(svc).SetPassword, validBody)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	})

	// ── Service receives correct input ─────────────────────────────────────────

	t.Run("service receives user ID from context and new_password from body", func(t *testing.T) {
		t.Parallel()
		uid := uuid.New().String()
		var captured setpassword.SetPasswordInput
		svc := &authsharedtest.SetPasswordFakeServicer{
			SetPasswordFn: func(_ context.Context, in setpassword.SetPasswordInput) error {
				captured = in
				return nil
			},
		}
		w := postJSONWithUserID(newTestHandler(svc).SetPassword, uid,
			`{"new_password":"Str0ng!Pass"}`)
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, uid, captured.UserID)
		require.Equal(t, "Str0ng!Pass", captured.NewPassword)
	})

	// ── T-20: Rate limit (6th request from the same user) ─────────────────────
	// Tests the UserRateLimiter middleware integrated with the handler.
	// The limiter is wired in routes.go; here we simulate it directly.

	t.Run("T-20: 6th request from same user within window returns 429", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.SetPasswordFakeServicer{} // always succeeds
		h := newTestHandler(svc)

		store := kvstore.NewInMemoryStore(0)
		limiter := ratelimit.NewUserRateLimiter(store, "spw:usr:", 5.0/(15*60), 5, 15*time.Minute)

		// Build a handler chain: limiter → handler.SetPassword.
		// Injects a user ID into each request.
		uid := uuid.New().String()
		chain := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := token.InjectUserIDForTest(r.Context(), uid)
			r = r.WithContext(ctx)
			limiter.Limit(http.HandlerFunc(h.SetPassword)).ServeHTTP(w, r)
		})

		// Requests 1–5 must succeed (200 or 422 from service — just not 429).
		for i := range 5 {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(validBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			chain.ServeHTTP(w, req)
			require.NotEqual(t, http.StatusTooManyRequests, w.Code,
				"request %d must not be rate-limited", i+1)
		}

		// 6th request must be rate-limited.
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(validBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		chain.ServeHTTP(w, req)
		require.Equal(t, http.StatusTooManyRequests, w.Code,
			"6th request must return 429 Too Many Requests")
		require.NotEmpty(t, w.Header().Get("Retry-After"),
			"Retry-After header must be present on 429 response")
	})
}
