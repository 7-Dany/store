package username_test

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

	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	profileshared "github.com/7-Dany/store/backend/internal/domain/profile/shared"
	"github.com/7-Dany/store/backend/internal/domain/profile/username"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// newTestHandler returns a Handler backed by a fake servicer.
func newTestHandler(svc username.Servicer) *username.Handler {
	return username.NewHandler(svc)
}

// getAvailable fires a GET /username/available?username=X against the handler.
// Pass an empty string to simulate a request with no query parameter.
func getAvailable(h http.HandlerFunc, uname string) *httptest.ResponseRecorder {
	target := "/username/available"
	if uname != "" {
		target += "?username=" + uname
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// patchWithUserID fires a PATCH with a fake user ID injected into the context.
func patchWithUserID(h http.HandlerFunc, userID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := token.InjectUserIDForTest(req.Context(), userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

// patchNoUserID fires a PATCH without a user ID (simulates absent JWT).
func patchNoUserID(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

const (
	testUID       = "00000000-0000-0000-0000-000000000001"
	validUsername = "alice_wonder"
	validBody     = `{"username":"` + validUsername + `"}`
)

// ── TestHandler_Available ─────────────────────────────────────────────────────

func TestHandler_Available(t *testing.T) {
	t.Parallel()

	// ── T-01: available → 200 {"available":true} ──────────────────────────────

	t.Run("available username returns 200 available:true", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{
			CheckUsernameAvailableFn: func(_ context.Context, _ string) (bool, error) {
				return true, nil
			},
		}
		w := getAvailable(newTestHandler(svc).Available, validUsername)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"available":true`)
	})

	// ── T-02: taken → 200 {"available":false} ────────────────────────────────

	t.Run("taken username returns 200 available:false", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{
			CheckUsernameAvailableFn: func(_ context.Context, _ string) (bool, error) {
				return false, nil
			},
		}
		w := getAvailable(newTestHandler(svc).Available, validUsername)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"available":false`)
	})

	// ── T-03: missing ?username → 422 ────────────────────────────────────────
	// Service receives "" → NormaliseAndValidateUsername → ErrUsernameEmpty.

	t.Run("missing username query param returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{} // default (true, nil) never reached
		w := getAvailable(newTestHandler(svc).Available, "")
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-04: username too short → 422 ───────────────────────────────────────

	t.Run("username too short returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := getAvailable(newTestHandler(svc).Available, "ab")
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-05: username too long → 422 ────────────────────────────────────────

	t.Run("username too long returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := getAvailable(newTestHandler(svc).Available, strings.Repeat("a", 31))
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-06: invalid charset → 422 ──────────────────────────────────────────

	t.Run("username with invalid chars returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := getAvailable(newTestHandler(svc).Available, "al!ce")
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-07: leading underscore → 422 ───────────────────────────────────────

	t.Run("leading underscore returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := getAvailable(newTestHandler(svc).Available, "_alice")
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-08: trailing underscore → 422 ──────────────────────────────────────

	t.Run("trailing underscore returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := getAvailable(newTestHandler(svc).Available, "alice_")
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-09: consecutive underscores → 422 ──────────────────────────────────

	t.Run("consecutive underscores returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := getAvailable(newTestHandler(svc).Available, "al__ice")
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-10: unexpected service error → 500 ─────────────────────────────────

	t.Run("unexpected service error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{
			CheckUsernameAvailableFn: func(_ context.Context, _ string) (bool, error) {
				return false, errors.New("db: connection refused")
			},
		}
		w := getAvailable(newTestHandler(svc).Available, validUsername)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── Service receives the raw query-param value ────────────────────────────

	t.Run("handler passes the raw query-param value to the service", func(t *testing.T) {
		t.Parallel()
		var captured string
		svc := &authsharedtest.UsernameFakeServicer{
			CheckUsernameAvailableFn: func(_ context.Context, uname string) (bool, error) {
				captured = uname
				return true, nil
			},
		}
		// URL-encoding is applied to spaces by http.NewRequest; for this test we
		// use a clean value to confirm the passthrough without URL-decode ambiguity.
		getAvailable(newTestHandler(svc).Available, "alice_wonder")
		require.Equal(t, "alice_wonder", captured,
			"handler must pass the raw query-param string to the service")
	})
}

// ── TestHandler_UpdateUsername ────────────────────────────────────────────────

func TestHandler_UpdateUsername(t *testing.T) {
	t.Parallel()

	// ── T-15: Happy path → 200 ───────────────────────────────────────────────

	t.Run("success returns 200 with message", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{} // UpdateUsernameFn nil → nil error
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, validBody)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), "username updated successfully")
	})

	// ── T-16: empty username → 422 ───────────────────────────────────────────

	t.Run("empty username field returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, `{"username":""}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-17: username too short → 422 ───────────────────────────────────────

	t.Run("username too short returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, `{"username":"ab"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-18: username too long → 422 ────────────────────────────────────────

	t.Run("username too long returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		long := `{"username":"` + strings.Repeat("a", 31) + `"}`
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, long)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-19: invalid charset → 422 ──────────────────────────────────────────

	t.Run("invalid charset returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, `{"username":"al!ce"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-20: leading underscore → 422 ───────────────────────────────────────

	t.Run("leading underscore returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, `{"username":"_alice"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-21: trailing underscore → 422 ──────────────────────────────────────

	t.Run("trailing underscore returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, `{"username":"alice_"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-22: consecutive underscores → 422 ──────────────────────────────────

	t.Run("consecutive underscores returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, `{"username":"al__ice"}`)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "validation_error")
	})

	// ── T-23: ErrSameUsername → 422 same_username ─────────────────────────────

	t.Run("ErrSameUsername returns 422 same_username", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{
			UpdateUsernameFn: func(_ context.Context, _ username.UpdateUsernameInput) error {
				return username.ErrSameUsername
			},
		}
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, validBody)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		require.Contains(t, w.Body.String(), "same_username")
	})

	// ── T-24: ErrUsernameTaken → 409 username_taken ───────────────────────────

	t.Run("ErrUsernameTaken returns 409 username_taken", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{
			UpdateUsernameFn: func(_ context.Context, _ username.UpdateUsernameInput) error {
				return username.ErrUsernameTaken
			},
		}
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, validBody)
		require.Equal(t, http.StatusConflict, w.Code)
		require.Contains(t, w.Body.String(), "username_taken")
	})

	// ── T-25: ErrUserNotFound → 500 internal_error ────────────────────────────

	t.Run("ErrUserNotFound returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{
			UpdateUsernameFn: func(_ context.Context, _ username.UpdateUsernameInput) error {
				return profileshared.ErrUserNotFound
			},
		}
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, validBody)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── T-29: missing JWT → 401 unauthorized ──────────────────────────────────

	t.Run("missing JWT returns 401 unauthorized", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := patchNoUserID(newTestHandler(svc).UpdateUsername, validBody)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Contains(t, w.Body.String(), "unauthorized")
	})

	// ── T-31: body > 1 MiB → 413 ─────────────────────────────────────────────

	t.Run("body exceeds MaxBodyBytes returns 413", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		bigBody := strings.Repeat("x", 2<<20) // 2 MiB
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, bigBody)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	// ── T-32: malformed JSON → 422 ────────────────────────────────────────────

	t.Run("malformed JSON returns 400", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{}
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, `{"username":`)
		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	// ── Unexpected service error → 500 ───────────────────────────────────────

	t.Run("unexpected service error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{
			UpdateUsernameFn: func(_ context.Context, _ username.UpdateUsernameInput) error {
				return errors.New("db: tx aborted")
			},
		}
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, testUID, validBody)
		require.Equal(t, http.StatusInternalServerError, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})

	// ── Service receives correct input ────────────────────────────────────────

	t.Run("service receives user ID parsed from context and username from body", func(t *testing.T) {
		t.Parallel()
		uid := uuid.New().String()
		var captured username.UpdateUsernameInput
		svc := &authsharedtest.UsernameFakeServicer{
			UpdateUsernameFn: func(_ context.Context, in username.UpdateUsernameInput) error {
				captured = in
				return nil
			},
		}
		w := patchWithUserID(newTestHandler(svc).UpdateUsername, uid, validBody)
		require.Equal(t, http.StatusOK, w.Code)
		parsedUID, _ := uuid.Parse(uid)
		require.Equal(t, [16]byte(parsedUID), captured.UserID,
			"UserID must be the parsed JWT claim, not a string")
		require.Equal(t, validUsername, captured.Username,
			"Username must be the raw body value; normalisation is the service's job")
	})

	// ── T-39: rate limit (6th request from same user) ─────────────────────────

	t.Run("T-39: 6th request from same user within window returns 429", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.UsernameFakeServicer{} // always succeeds
		h := newTestHandler(svc)

		kv := kvstore.NewInMemoryStore(0)
		limiter := ratelimit.NewUserRateLimiter(kv, "uchg:usr:", 5.0/(10*60), 5, 10*time.Minute)

		uid := uuid.New().String()
		chain := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := token.InjectUserIDForTest(r.Context(), uid)
			r = r.WithContext(ctx)
			limiter.Limit(http.HandlerFunc(h.UpdateUsername)).ServeHTTP(w, r)
		})

		// Requests 1–5 must succeed.
		for i := range 5 {
			req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(validBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			chain.ServeHTTP(w, req)
			require.NotEqual(t, http.StatusTooManyRequests, w.Code,
				"request %d must not be rate-limited", i+1)
		}

		// 6th request must be rate-limited.
		req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(validBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		chain.ServeHTTP(w, req)
		require.Equal(t, http.StatusTooManyRequests, w.Code,
			"6th request must return 429 Too Many Requests")
		require.NotEmpty(t, w.Header().Get("Retry-After"),
			"429 response must carry a Retry-After header")
	})
}
