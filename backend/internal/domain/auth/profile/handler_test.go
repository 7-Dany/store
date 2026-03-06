// Package profile_test — handler-layer unit tests for the profile feature.
//
// Covers every branch in Me, Sessions, and RevokeSession.
// No build tag — these are pure unit tests that run in both regular and
// integration_test configurations.
package profile_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
	authsharedtest "github.com/7-Dany/store/backend/internal/domain/auth/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/auth/profile"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── test fixtures ─────────────────────────────────────────────────────────────

// phSecret is the JWT signing secret used in profile handler tests.
const phSecret = "profile-handler-unit-test-secret-XYZ-!"

// newPH builds a Handler backed by svc.
func newPH(svc profile.Servicer) *profile.Handler {
	return profile.NewHandler(svc)
}

// phRouter builds a chi Mux with the real token.Auth middleware and every
// profile route registered. Used to exercise the "no/bad token" middleware
// rejection paths and to ensure chi populates URL parameters (e.g. {id}).
func phRouter(h *profile.Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Use(token.Auth(phSecret, nil, nil))
	r.Get("/me", h.Me)
	r.Get("/sessions", h.Sessions)
	r.Delete("/sessions/{id}", h.RevokeSession)
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
func doDirectMe(h *profile.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	w := httptest.NewRecorder()
	h.Me(w, req)
	return w
}

// doDirectSessions calls h.Sessions without a userID in context.
func doDirectSessions(h *profile.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	w := httptest.NewRecorder()
	h.Sessions(w, req)
	return w
}

// doDirectRevokeSession calls h.RevokeSession without a userID in context.
func doDirectRevokeSession(h *profile.Handler, sessionID string) *httptest.ResponseRecorder {
	rctx := chi.NewRouteContext()
	if sessionID != "" {
		rctx.URLParams.Add("id", sessionID)
	}
	req := httptest.NewRequest(http.MethodDelete, "/sessions/"+sessionID, nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.RevokeSession(w, req)
	return w
}

// doDirectRevokeSessionWithUID calls h.RevokeSession with a userID in context
// and the given session ID in the chi route context.
func doDirectRevokeSessionWithUID(h *profile.Handler, userID, sessionID string) *httptest.ResponseRecorder {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", sessionID)
	req := httptest.NewRequest(http.MethodDelete, "/sessions/"+sessionID, nil)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = token.InjectUserIDForTest(ctx, userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.RevokeSession(w, req)
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
		wantProfile := profile.UserProfile{
			ID:            [16]byte(uidParsed),
			Email:         "alice@example.com",
			DisplayName:   "Alice",
			AvatarURL:     "https://cdn.example.com/avatar.png",
			EmailVerified: true,
			IsActive:      true,
			LastLoginAt:   &now,
			CreatedAt:     now,
		}
		svc := &authsharedtest.ProfileFakeServicer{
			GetUserProfileFn: func(_ context.Context, _ string) (profile.UserProfile, error) {
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
		svc := &authsharedtest.ProfileFakeServicer{
			GetUserProfileFn: func(_ context.Context, _ string) (profile.UserProfile, error) {
				return profile.UserProfile{ID: [16]byte(uidParsed), CreatedAt: now}, nil
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
		svc := &authsharedtest.ProfileFakeServicer{
			GetUserProfileFn: func(_ context.Context, _ string) (profile.UserProfile, error) {
				return profile.UserProfile{}, authshared.ErrUserNotFound
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodGet, "/me", uid, nil)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.ProfileFakeServicer{
			GetUserProfileFn: func(_ context.Context, _ string) (profile.UserProfile, error) {
				return profile.UserProfile{}, errors.New("db exploded")
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodGet, "/me", uid, nil)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("missing userID in context returns 401", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.ProfileFakeServicer{})
		w := doDirectMe(h)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("no Authorization header returns 401 from middleware", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.ProfileFakeServicer{})
		w := doRouted(t, phRouter(h), http.MethodGet, "/me", "", nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("invalid token returns 401 from middleware", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.ProfileFakeServicer{})
		req := httptest.NewRequest(http.MethodGet, "/me", nil)
		req.Header.Set("Authorization", "Bearer not.a.real.jwt")
		w := httptest.NewRecorder()
		phRouter(h).ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// ── TestHandler_Sessions ──────────────────────────────────────────────────────

func TestHandler_Sessions(t *testing.T) {
	t.Parallel()

	uid := uuid.NewString()
	sidA := [16]byte(uuid.New())
	sidB := [16]byte(uuid.New())
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("success returns 200 with sessions list", func(t *testing.T) {
		t.Parallel()
		want := []profile.ActiveSession{
			{ID: sidA, IPAddress: "1.2.3.4", UserAgent: "Go/1.0", StartedAt: now, LastActiveAt: now},
			{ID: sidB, IPAddress: "5.6.7.8", UserAgent: "Firefox/120", StartedAt: now, LastActiveAt: now},
		}
		svc := &authsharedtest.ProfileFakeServicer{
			GetActiveSessionsFn: func(_ context.Context, _ string) ([]profile.ActiveSession, error) {
				return want, nil
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodGet, "/sessions", uid, nil)

		require.Equal(t, http.StatusOK, w.Code)
		var body struct {
			Sessions []map[string]any `json:"sessions"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Len(t, body.Sessions, 2)
		assert.Equal(t, "1.2.3.4", body.Sessions[0]["ip_address"])
		assert.Equal(t, "Firefox/120", body.Sessions[1]["user_agent"])
	})

	t.Run("empty sessions returns 200 with empty list", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.ProfileFakeServicer{
			GetActiveSessionsFn: func(_ context.Context, _ string) ([]profile.ActiveSession, error) {
				return nil, nil
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodGet, "/sessions", uid, nil)

		require.Equal(t, http.StatusOK, w.Code)
		var body struct {
			Sessions []any `json:"sessions"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Empty(t, body.Sessions)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.ProfileFakeServicer{
			GetActiveSessionsFn: func(_ context.Context, _ string) ([]profile.ActiveSession, error) {
				return nil, errors.New("store failure")
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodGet, "/sessions", uid, nil)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("missing userID in context returns 401", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.ProfileFakeServicer{})
		w := doDirectSessions(h)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("no Authorization header returns 401", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.ProfileFakeServicer{})
		w := doRouted(t, phRouter(h), http.MethodGet, "/sessions", "", nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("invalid token returns 401 from middleware", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.ProfileFakeServicer{})
		req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
		req.Header.Set("Authorization", "Bearer not.a.real.jwt")
		w := httptest.NewRecorder()
		phRouter(h).ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("session IDs are formatted as UUID strings", func(t *testing.T) {
		t.Parallel()
		sid := [16]byte(uuid.New())
		svc := &authsharedtest.ProfileFakeServicer{
			GetActiveSessionsFn: func(_ context.Context, _ string) ([]profile.ActiveSession, error) {
				return []profile.ActiveSession{{ID: sid, StartedAt: now, LastActiveAt: now}}, nil
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodGet, "/sessions", uid, nil)

		require.Equal(t, http.StatusOK, w.Code)
		var body struct {
			Sessions []struct {
				ID string `json:"id"`
			} `json:"sessions"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Len(t, body.Sessions, 1)
		_, parseErr := uuid.Parse(body.Sessions[0].ID)
		assert.NoError(t, parseErr)
		assert.Equal(t, uuid.UUID(sid).String(), body.Sessions[0].ID)
	})

	t.Run("is_current is true for session matching JWT session", func(t *testing.T) {
		t.Parallel()
		currentSID := [16]byte(uuid.New())
		otherSID := [16]byte(uuid.New())
		svc := &authsharedtest.ProfileFakeServicer{
			GetActiveSessionsFn: func(_ context.Context, _ string) ([]profile.ActiveSession, error) {
				return []profile.ActiveSession{
					{ID: currentSID, StartedAt: now, LastActiveAt: now},
					{ID: otherSID, StartedAt: now, LastActiveAt: now},
				}, nil
			},
		}
		h := newPH(svc)
		req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
		ctx := token.InjectUserIDForTest(req.Context(), uid)
		ctx = token.InjectSessionIDForTest(ctx, uuid.UUID(currentSID).String())
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		h.Sessions(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		var body struct {
			Sessions []struct {
				ID        string `json:"id"`
				IsCurrent bool   `json:"is_current"`
			} `json:"sessions"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Len(t, body.Sessions, 2)
		var currentCount int
		for _, s := range body.Sessions {
			if s.ID == uuid.UUID(currentSID).String() {
				assert.True(t, s.IsCurrent)
				currentCount++
			} else {
				assert.False(t, s.IsCurrent)
			}
		}
		assert.Equal(t, 1, currentCount)
	})
}

// ── TestHandler_RevokeSession ─────────────────────────────────────────────────

func TestHandler_RevokeSession(t *testing.T) {
	t.Parallel()

	uid := uuid.NewString()
	validSID := uuid.NewString()

	t.Run("success returns 204 No Content", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.ProfileFakeServicer{
			RevokeSessionFn: func(_ context.Context, _, _, _, _ string) error { return nil },
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodDelete, "/sessions/"+validSID, uid, nil)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("session not found returns 404", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.ProfileFakeServicer{
			RevokeSessionFn: func(_ context.Context, _, _, _, _ string) error {
				return authshared.ErrSessionNotFound
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodDelete, "/sessions/"+validSID, uid, nil)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.ProfileFakeServicer{
			RevokeSessionFn: func(_ context.Context, _, _, _, _ string) error {
				return errors.New("db gone")
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodDelete, "/sessions/"+validSID, uid, nil)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("non-UUID session id returns 422", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.ProfileFakeServicer{})
		w := doRouted(t, phRouter(h), http.MethodDelete, "/sessions/not-a-uuid", uid, nil)
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("missing session id returns 422", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.ProfileFakeServicer{})
		w := doDirectRevokeSessionWithUID(h, uid, "")
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("missing userID in context returns 401", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.ProfileFakeServicer{})
		w := doDirectRevokeSession(h, validSID)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("no Authorization header returns 401", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.ProfileFakeServicer{})
		w := doRouted(t, phRouter(h), http.MethodDelete, "/sessions/"+validSID, "", nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("invalid token returns 401 from middleware", func(t *testing.T) {
		t.Parallel()
		h := newPH(&authsharedtest.ProfileFakeServicer{})
		req := httptest.NewRequest(http.MethodDelete, "/sessions/"+validSID, nil)
		req.Header.Set("Authorization", "Bearer not.a.real.jwt")
		w := httptest.NewRecorder()
		phRouter(h).ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("correct userID and sessionID strings are forwarded to service", func(t *testing.T) {
		t.Parallel()
		sid := uuid.New()
		var gotSessionID, gotOwnerID string
		svc := &authsharedtest.ProfileFakeServicer{
			RevokeSessionFn: func(_ context.Context, userID, sessionID, _, _ string) error {
				gotOwnerID = userID
				gotSessionID = sessionID
				return nil
			},
		}
		h := newPH(svc)
		w := doRouted(t, phRouter(h), http.MethodDelete, "/sessions/"+sid.String(), uid, nil)
		require.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, uid, gotOwnerID)
		assert.Equal(t, sid.String(), gotSessionID)
	})
}

// ── Forwarding tests ──────────────────────────────────────────────────────────

func TestHandler_Me_UserIDForwarding(t *testing.T) {
	t.Parallel()

	uid := uuid.NewString()
	now := time.Now().UTC()

	var receivedUID string
	svc := &authsharedtest.ProfileFakeServicer{
		GetUserProfileFn: func(_ context.Context, userID string) (profile.UserProfile, error) {
			receivedUID = userID
			uidParsed, _ := uuid.Parse(userID)
			return profile.UserProfile{ID: [16]byte(uidParsed), CreatedAt: now}, nil
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
	svc := &authsharedtest.ProfileFakeServicer{
		GetUserProfileFn: func(_ context.Context, _ string) (profile.UserProfile, error) {
			return profile.UserProfile{
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

// TestHandler_Sessions_IsCurrentFalseWhenNoSessionIDInContext asserts that all
// sessions carry is_current: false when no session ID claim is present in the
// request context (e.g. a JWT that pre-dates the session ID claim addition).
func TestHandler_Sessions_IsCurrentFalseWhenNoSessionIDInContext(t *testing.T) {
	t.Parallel()
	uid := uuid.NewString()
	sid1 := [16]byte(uuid.New())
	sid2 := [16]byte(uuid.New())
	now := time.Now().UTC()
	svc := &authsharedtest.ProfileFakeServicer{
		GetActiveSessionsFn: func(_ context.Context, _ string) ([]profile.ActiveSession, error) {
			return []profile.ActiveSession{
				{ID: sid1, StartedAt: now, LastActiveAt: now},
				{ID: sid2, StartedAt: now, LastActiveAt: now},
			}, nil
		},
	}
	h := newPH(svc)
	// Inject userID but deliberately omit sessionID — simulates a JWT with no sid claim.
	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	ctx := token.InjectUserIDForTest(req.Context(), uid)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.Sessions(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Sessions []struct {
			IsCurrent bool `json:"is_current"`
		} `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Sessions, 2)
	for _, s := range body.Sessions {
		assert.False(t, s.IsCurrent, "is_current must be false when no session ID claim is present")
	}
}

func TestHandler_Sessions_UserIDForwarding(t *testing.T) {
	t.Parallel()

	uid := uuid.NewString()

	var receivedUID string
	svc := &authsharedtest.ProfileFakeServicer{
		GetActiveSessionsFn: func(_ context.Context, userID string) ([]profile.ActiveSession, error) {
			receivedUID = userID
			return nil, nil
		},
	}
	h := newPH(svc)
	doRouted(t, phRouter(h), http.MethodGet, "/sessions", uid, nil)
	assert.Equal(t, uid, receivedUID)
}
