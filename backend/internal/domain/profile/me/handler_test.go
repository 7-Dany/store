// Package me_test — handler-layer unit tests for the me feature.
//
// Covers every branch in Me and UpdateProfile.
// No build tag — these are pure unit tests that run in both regular and
// integration_test configurations.
package me_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/profile/me"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── test fixtures ─────────────────────────────────────────────────────────────

// phSecret is the JWT signing secret used in me handler tests.
const phSecret = "me-handler-unit-test-secret-XYZ-!"

// newPH builds a Handler backed by svc.
func newPH(svc me.Servicer) *me.Handler {
	return me.NewHandler(svc)
}

// phRouter builds a chi Mux with the real token.Auth middleware and the
// GET /me route registered. Used to exercise "no/bad token" middleware rejection paths.
func phRouter(h *me.Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Use(token.Auth(phSecret, nil, nil))
	r.Get("/me", h.Me)
	return r
}

// phBearerToken signs a real access JWT for userID.
func phBearerToken(t *testing.T, userID string) string {
	t.Helper()
	tok, err := token.GenerateAccessToken(userID, uuid.NewString(), time.Hour, phSecret)
	require.NoError(t, err)
	return tok
}

// doRouted fires method+path through the full chi router (with JWT middleware).
// If userID is non-empty an Authorization: Bearer header is attached.
func doRouted(t *testing.T, router *chi.Mux, method, path, userID string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader = bytes.NewReader(nil)
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.Header.Set("Authorization", "Bearer "+phBearerToken(t, userID))
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// doDirectMe calls h.Me directly on a request whose context does NOT contain a
// userID — exercises the handler's own guard.
func doDirectMe(h *me.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	w := httptest.NewRecorder()
	h.Me(w, req)
	return w
}

// jsonBody marshals v to a JSON byte slice.
func jsonBody(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("jsonBody: " + err.Error())
	}
	return b
}

// ── TestHandler_Me ────────────────────────────────────────────────────────────

func TestHandler_Me(t *testing.T) {
	t.Parallel()

	uid := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("success returns 200 with profile fields", func(t *testing.T) {
		t.Parallel()
		uidParsed, _ := uuid.Parse(uid)
		wantProfile := me.UserProfile{
			ID:            [16]byte(uidParsed),
			Email:         "alice@example.com",
			DisplayName:   "Alice",
			AvatarURL:     "https://cdn.example.com/avatar.png",
			EmailVerified: true,
			IsActive:      true,
			LastLoginAt:   &now,
			CreatedAt:     now,
		}
		svc := &authsharedtest.MeFakeServicer{
			GetUserProfileFn: func(_ context.Context, _ string) (me.UserProfile, error) {
				return wantProfile, nil
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodGet, "/me", uid, nil)

		require.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Equal(t, uid, body["id"])
		assert.Equal(t, "alice@example.com", body["email"])
		assert.Equal(t, "Alice", body["display_name"])
		assert.Equal(t, "https://cdn.example.com/avatar.png", body["avatar_url"])
		assert.Equal(t, true, body["email_verified"])
		assert.Equal(t, true, body["is_active"])
		assert.Equal(t, false, body["is_locked"])
	})

	t.Run("success nil LastLoginAt is omitted from response", func(t *testing.T) {
		t.Parallel()
		uidParsed, _ := uuid.Parse(uid)
		svc := &authsharedtest.MeFakeServicer{
			GetUserProfileFn: func(_ context.Context, _ string) (me.UserProfile, error) {
				return me.UserProfile{ID: [16]byte(uidParsed), CreatedAt: now}, nil
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodGet, "/me", uid, nil)

		require.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		_, hasLastLogin := body["last_login_at"]
		assert.False(t, hasLastLogin, "last_login_at should be omitted when nil")
	})

	t.Run("user not found returns 404", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.MeFakeServicer{
			GetUserProfileFn: func(_ context.Context, _ string) (me.UserProfile, error) {
				return me.UserProfile{}, authshared.ErrUserNotFound
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodGet, "/me", uid, nil)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.MeFakeServicer{
			GetUserProfileFn: func(_ context.Context, _ string) (me.UserProfile, error) {
				return me.UserProfile{}, errors.New("db exploded")
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodGet, "/me", uid, nil)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("missing userID in context returns 401", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		w := doDirectMe(h)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("no Authorization header returns 401 from middleware", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		w := doRouted(t, phRouter(h), http.MethodGet, "/me", "", nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("invalid token returns 401 from middleware", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req.Header.Set("Authorization", "Bearer not.a.real.jwt")
		w := httptest.NewRecorder()
		phRouter(h).ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// ── Forwarding tests ──────────────────────────────────────────────────────────

func TestHandler_Me_UserIDForwarding(t *testing.T) {
	t.Parallel()

	uid := uuid.NewString()
	now := time.Now().UTC()

	var receivedUID string
	svc := &authsharedtest.MeFakeServicer{
		GetUserProfileFn: func(_ context.Context, userID string) (me.UserProfile, error) {
			receivedUID = userID
			uidParsed, _ := uuid.Parse(userID)
			return me.UserProfile{ID: [16]byte(uidParsed), CreatedAt: now}, nil
		},
	}
	h := newPH(svc)
	w := doRouted(t, phRouter(h), http.MethodGet, "/me", uid, nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, uid, receivedUID,
		"userID extracted from JWT must be forwarded as-is to the service")
}

// TestHandler_Me_AvatarURLOmittedWhenEmpty asserts that a zero-value AvatarURL
// is omitted from the JSON response body (omitempty on the requests.go field).
func TestHandler_Me_AvatarURLOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	uid := uuid.NewString()
	uidParsed, _ := uuid.Parse(uid)
	svc := &authsharedtest.MeFakeServicer{
		GetUserProfileFn: func(_ context.Context, _ string) (me.UserProfile, error) {
			return me.UserProfile{
				ID:        [16]byte(uidParsed),
				AvatarURL: "", // empty string must be omitted by omitempty
				CreatedAt: time.Now().UTC(),
			}, nil
		},
	}
	h := newPH(svc)
	w := doRouted(t, phRouter(h), http.MethodGet, "/me", uid, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	_, hasAvatarURL := body["avatar_url"]
	assert.False(t, hasAvatarURL, "avatar_url must be omitted from the response when empty")
}

// ── UpdateProfile helpers ─────────────────────────────────────────────────────────

// phRouterWithUpdateProfile builds a chi Mux with both me routes registered.
func phRouterWithUpdateProfile(h *me.Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Use(token.Auth(phSecret, nil, nil))
	r.Get("/me", h.Me)
	r.Patch("/me", h.UpdateProfile)
	return r
}

// doDirectUpdateProfile calls h.UpdateProfile with userID injected into context.
func doDirectUpdateProfile(h *me.Handler, userID string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/me", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := token.InjectUserIDForTest(req.Context(), userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.UpdateProfile(w, req)
	return w
}

// ── TestHandler_UpdateProfile ────────────────────────────────────────────────────────

func TestHandler_UpdateProfile(t *testing.T) {
	t.Parallel()

	uid := uuid.NewString()

	// T-01: happy path — both fields.
	t.Run("success with display_name and avatar_url returns 200", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.MeFakeServicer{
			UpdateProfileFn: func(_ context.Context, _ me.UpdateProfileInput) error { return nil },
		}
		h := newPH(svc)
		dn := "Alice"
		au := "https://cdn.example.com/avatar.png"
		body := jsonBody(map[string]any{"display_name": dn, "avatar_url": au})
		w := doRouted(t, phRouterWithUpdateProfile(h), http.MethodPatch, "/me", uid, body)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "profile updated successfully", resp["message"])
	})

	// T-02: happy path — display_name only.
	t.Run("success with display_name only returns 200", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.MeFakeServicer{
			UpdateProfileFn: func(_ context.Context, _ me.UpdateProfileInput) error { return nil },
		}
		h := newPH(svc)
		body := jsonBody(map[string]any{"display_name": "Bob"})
		w := doRouted(t, phRouterWithUpdateProfile(h), http.MethodPatch, "/me", uid, body)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	// T-03: happy path — avatar_url only.
	t.Run("success with avatar_url only returns 200", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.MeFakeServicer{
			UpdateProfileFn: func(_ context.Context, _ me.UpdateProfileInput) error { return nil },
		}
		h := newPH(svc)
		body := jsonBody(map[string]any{"avatar_url": "https://example.com/img.png"})
		w := doRouted(t, phRouterWithUpdateProfile(h), http.MethodPatch, "/me", uid, body)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	// T-04: empty patch — both fields nil.
	t.Run("empty patch returns 422", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		w := doDirectUpdateProfile(h, uid, jsonBody(map[string]any{}))
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// T-05: display_name empty after trim.
	t.Run("blank display_name returns 422", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		w := doDirectUpdateProfile(h, uid, jsonBody(map[string]any{"display_name": "   "}))
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// T-06: display_name too long (101 runes).
	t.Run("display_name over 100 runes returns 422", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		long := strings.Repeat("a", 101)
		w := doDirectUpdateProfile(h, uid, jsonBody(map[string]any{"display_name": long}))
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// T-07: display_name exactly 100 runes (boundary accepted).
	t.Run("display_name exactly 100 runes returns 200", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.MeFakeServicer{
			UpdateProfileFn: func(_ context.Context, _ me.UpdateProfileInput) error { return nil },
		}
		h := newPH(svc)
		exact := strings.Repeat("a", 100)
		w := doDirectUpdateProfile(h, uid, jsonBody(map[string]any{"display_name": exact}))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	// T-08: display_name contains ASCII control character.
	t.Run("display_name with control char returns 422", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		w := doDirectUpdateProfile(h, uid, jsonBody(map[string]any{"display_name": "a\x01b"}))
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// T-09: avatar_url invalid — no scheme.
	t.Run("avatar_url with no scheme returns 422", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		w := doDirectUpdateProfile(h, uid, jsonBody(map[string]any{"avatar_url": "not-a-url"}))
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// T-10: avatar_url invalid scheme (ftp://).
	t.Run("avatar_url with ftp scheme returns 422", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		w := doDirectUpdateProfile(h, uid, jsonBody(map[string]any{"avatar_url": "ftp://example.com/img.png"}))
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// T-11: avatar_url too long (2049 bytes).
	t.Run("avatar_url over 2048 bytes returns 422", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		prefix := "https://example.com/"
		pad := strings.Repeat("a", 2049-len(prefix))
		long := prefix + pad
		require.Equal(t, 2049, len(long))
		w := doDirectUpdateProfile(h, uid, jsonBody(map[string]any{"avatar_url": long}))
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// T-12: avatar_url exactly 2048 bytes (boundary accepted).
	t.Run("avatar_url exactly 2048 bytes returns 200", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.MeFakeServicer{
			UpdateProfileFn: func(_ context.Context, _ me.UpdateProfileInput) error { return nil },
		}
		h := newPH(svc)
		prefix := "https://example.com/"
		pad := strings.Repeat("a", 2048-len(prefix))
		exact := prefix + pad
		require.Equal(t, 2048, len(exact))
		w := doDirectUpdateProfile(h, uid, jsonBody(map[string]any{"avatar_url": exact}))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	// T-13: avatar_url empty string.
	t.Run("avatar_url empty string returns 422", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		w := doDirectUpdateProfile(h, uid, jsonBody(map[string]any{"avatar_url": ""}))
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// T-14: body > 1 MiB.
	t.Run("body larger than 1 MiB returns 413", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		oversized := make([]byte, (1<<20)+1)
		for i := range oversized {
			oversized[i] = 'a'
		}
		w := doDirectUpdateProfile(h, uid, oversized)
		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	// T-15: malformed JSON.
	t.Run("malformed JSON returns 422", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		w := doDirectUpdateProfile(h, uid, []byte(`{bad json`))
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	// T-16: missing Authorization header.
	t.Run("no Authorization header returns 401", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.MeFakeServicer{})
		body := jsonBody(map[string]any{"display_name": "Alice"})
		w := doRouted(t, phRouterWithUpdateProfile(h), http.MethodPatch, "/me", "", body)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	// T-17: service returns unexpected error.
	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.MeFakeServicer{
			UpdateProfileFn: func(_ context.Context, _ me.UpdateProfileInput) error {
				return errors.New("db down")
			},
		}
		h := newPH(svc)
		body := jsonBody(map[string]any{"display_name": "Alice"})
		w := doDirectUpdateProfile(h, uid, body)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	// UpdateProfileInput fields forwarded to service correctly.
	t.Run("UpdateProfileInput fields forwarded to service", func(t *testing.T) {
		t.Parallel()
		var capturedIn me.UpdateProfileInput
		svc := &authsharedtest.MeFakeServicer{
			UpdateProfileFn: func(_ context.Context, in me.UpdateProfileInput) error {
				capturedIn = in
				return nil
			},
		}
		h := newPH(svc)
		dn := "Charlie"
		au := "https://cdn.example.com/c.png"
		body := jsonBody(map[string]any{"display_name": dn, "avatar_url": au})
		w := doDirectUpdateProfile(h, uid, body)
		require.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, capturedIn.DisplayName)
		assert.Equal(t, dn, *capturedIn.DisplayName)
		require.NotNil(t, capturedIn.AvatarURL)
		assert.Equal(t, au, *capturedIn.AvatarURL)
		_ = capturedIn.IPAddress
		_ = capturedIn.UserAgent
	})
}
