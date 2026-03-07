// Package session_test — handler-layer unit tests for the profile/session feature.
//
// Covers every branch in Sessions and RevokeSession.
// No build tag — these are pure unit tests that run in both regular and
// integration_test configurations.
package session_test

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
	"github.com/7-Dany/store/backend/internal/domain/profile/session"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── test fixtures ─────────────────────────────────────────────────────────────

// phSecret is the JWT signing secret used in session handler tests.
const phSecret = "session-handler-unit-test-secret-XYZ-!"

// newSH builds a Handler backed by svc.
func newSH(svc session.Servicer) *session.Handler {
	return session.NewHandler(svc)
}

// sessRouter builds a chi Mux with the real token.Auth middleware and both
// session routes registered. Used to exercise "no/bad token" middleware
// rejection paths and to ensure chi populates URL parameters (e.g. {id}).
func sessRouter(h *session.Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Use(token.Auth(phSecret, nil, nil))
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

// doDirectSessions calls h.Sessions without a userID in context.
func doDirectSessions(h *session.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	w := httptest.NewRecorder()
	h.Sessions(w, req)
	return w
}

// doDirectRevokeSession calls h.RevokeSession without a userID in context.
func doDirectRevokeSession(h *session.Handler, sessionID string) *httptest.ResponseRecorder {
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
func doDirectRevokeSessionWithUID(h *session.Handler, userID, sessionID string) *httptest.ResponseRecorder {
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

// ── TestHandler_Sessions ──────────────────────────────────────────────────────

func TestHandler_Sessions(t *testing.T) {
	t.Parallel()

	uid := uuid.NewString()
	sidA := [16]byte(uuid.New())
	sidB := [16]byte(uuid.New())
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("success returns 200 with sessions list", func(t *testing.T) {
		t.Parallel()
		want := []session.ActiveSession{
			{ID: sidA, IPAddress: "1.2.3.4", UserAgent: "Go/1.0", StartedAt: now, LastActiveAt: now},
			{ID: sidB, IPAddress: "5.6.7.8", UserAgent: "Firefox/120", StartedAt: now, LastActiveAt: now},
		}
		svc := &authsharedtest.ProfileSessionFakeServicer{
			GetActiveSessionsFn: func(_ context.Context, _ string) ([]session.ActiveSession, error) {
				return want, nil
			},
		}
		h := newSH(svc)
		w := doRouted(t, sessRouter(h), http.MethodGet, "/sessions", uid, nil)

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
		svc := &authsharedtest.ProfileSessionFakeServicer{
			GetActiveSessionsFn: func(_ context.Context, _ string) ([]session.ActiveSession, error) {
				return nil, nil
			},
		}
		h := newSH(svc)
		w := doRouted(t, sessRouter(h), http.MethodGet, "/sessions", uid, nil)

		require.Equal(t, http.StatusOK, w.Code)
		var body struct {
			Sessions []any `json:"sessions"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Empty(t, body.Sessions)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.ProfileSessionFakeServicer{
			GetActiveSessionsFn: func(_ context.Context, _ string) ([]session.ActiveSession, error) {
				return nil, errors.New("store failure")
			},
		}
		h := newSH(svc)
		w := doRouted(t, sessRouter(h), http.MethodGet, "/sessions", uid, nil)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("missing userID in context returns 401", func(t *testing.T) {
		t.Parallel()
		h := newSH(&authsharedtest.ProfileSessionFakeServicer{})
		w := doDirectSessions(h)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("no Authorization header returns 401", func(t *testing.T) {
		t.Parallel()
		h := newSH(&authsharedtest.ProfileSessionFakeServicer{})
		w := doRouted(t, sessRouter(h), http.MethodGet, "/sessions", "", nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("invalid token returns 401 from middleware", func(t *testing.T) {
		t.Parallel()
		h := newSH(&authsharedtest.ProfileSessionFakeServicer{})
		req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
		req.Header.Set("Authorization", "Bearer not.a.real.jwt")
		w := httptest.NewRecorder()
		sessRouter(h).ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("session IDs are formatted as UUID strings", func(t *testing.T) {
		t.Parallel()
		sid := [16]byte(uuid.New())
		svc := &authsharedtest.ProfileSessionFakeServicer{
			GetActiveSessionsFn: func(_ context.Context, _ string) ([]session.ActiveSession, error) {
				return []session.ActiveSession{{ID: sid, StartedAt: now, LastActiveAt: now}}, nil
			},
		}
		h := newSH(svc)
		w := doRouted(t, sessRouter(h), http.MethodGet, "/sessions", uid, nil)

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
		svc := &authsharedtest.ProfileSessionFakeServicer{
			GetActiveSessionsFn: func(_ context.Context, _ string) ([]session.ActiveSession, error) {
				return []session.ActiveSession{
					{ID: currentSID, StartedAt: now, LastActiveAt: now},
					{ID: otherSID, StartedAt: now, LastActiveAt: now},
				}, nil
			},
		}
		h := newSH(svc)
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
		svc := &authsharedtest.ProfileSessionFakeServicer{
			RevokeSessionFn: func(_ context.Context, _, _, _, _ string) error { return nil },
		}
		h := newSH(svc)
		w := doRouted(t, sessRouter(h), http.MethodDelete, "/sessions/"+validSID, uid, nil)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("session not found returns 404", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.ProfileSessionFakeServicer{
			RevokeSessionFn: func(_ context.Context, _, _, _, _ string) error {
				return authshared.ErrSessionNotFound
			},
		}
		h := newSH(svc)
		w := doRouted(t, sessRouter(h), http.MethodDelete, "/sessions/"+validSID, uid, nil)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &authsharedtest.ProfileSessionFakeServicer{
			RevokeSessionFn: func(_ context.Context, _, _, _, _ string) error {
				return errors.New("db gone")
			},
		}
		h := newSH(svc)
		w := doRouted(t, sessRouter(h), http.MethodDelete, "/sessions/"+validSID, uid, nil)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("non-UUID session id returns 422", func(t *testing.T) {
		t.Parallel()
		h := newSH(&authsharedtest.ProfileSessionFakeServicer{})
		w := doRouted(t, sessRouter(h), http.MethodDelete, "/sessions/not-a-uuid", uid, nil)
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("missing session id returns 422", func(t *testing.T) {
		t.Parallel()
		h := newSH(&authsharedtest.ProfileSessionFakeServicer{})
		w := doDirectRevokeSessionWithUID(h, uid, "")
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("missing userID in context returns 401", func(t *testing.T) {
		t.Parallel()
		h := newSH(&authsharedtest.ProfileSessionFakeServicer{})
		w := doDirectRevokeSession(h, validSID)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("no Authorization header returns 401", func(t *testing.T) {
		t.Parallel()
		h := newSH(&authsharedtest.ProfileSessionFakeServicer{})
		w := doRouted(t, sessRouter(h), http.MethodDelete, "/sessions/"+validSID, "", nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("invalid token returns 401 from middleware", func(t *testing.T) {
		t.Parallel()
		h := newSH(&authsharedtest.ProfileSessionFakeServicer{})
		req := httptest.NewRequest(http.MethodDelete, "/sessions/"+validSID, nil)
		req.Header.Set("Authorization", "Bearer not.a.real.jwt")
		w := httptest.NewRecorder()
		sessRouter(h).ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("correct userID and sessionID strings are forwarded to service", func(t *testing.T) {
		t.Parallel()
		sid := uuid.New()
		var gotSessionID, gotOwnerID string
		svc := &authsharedtest.ProfileSessionFakeServicer{
			RevokeSessionFn: func(_ context.Context, userID, sessionID, _, _ string) error {
				gotOwnerID = userID
				gotSessionID = sessionID
				return nil
			},
		}
		h := newSH(svc)
		w := doRouted(t, sessRouter(h), http.MethodDelete, "/sessions/"+sid.String(), uid, nil)
		require.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, uid, gotOwnerID)
		assert.Equal(t, sid.String(), gotSessionID)
	})
}

// ── Forwarding tests ──────────────────────────────────────────────────────────

func TestHandler_Sessions_UserIDForwarding(t *testing.T) {
	t.Parallel()

	uid := uuid.NewString()

	var receivedUID string
	svc := &authsharedtest.ProfileSessionFakeServicer{
		GetActiveSessionsFn: func(_ context.Context, userID string) ([]session.ActiveSession, error) {
			receivedUID = userID
			return nil, nil
		},
	}
	h := newSH(svc)
	doRouted(t, sessRouter(h), http.MethodGet, "/sessions", uid, nil)
	assert.Equal(t, uid, receivedUID)
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
	svc := &authsharedtest.ProfileSessionFakeServicer{
		GetActiveSessionsFn: func(_ context.Context, _ string) ([]session.ActiveSession, error) {
			return []session.ActiveSession{
				{ID: sid1, StartedAt: now, LastActiveAt: now},
				{ID: sid2, StartedAt: now, LastActiveAt: now},
			}, nil
		},
	}
	h := newSH(svc)
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
