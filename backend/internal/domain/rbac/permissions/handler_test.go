package permissions_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/7-Dany/store/backend/internal/domain/rbac/permissions"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHandler(svc permissions.Servicer) *permissions.Handler {
	return permissions.NewHandler(svc)
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

// ── TestHandler_ListPermissions ───────────────────────────────────────────────

func TestHandler_ListPermissions(t *testing.T) {
	t.Parallel()

	t.Run("returns 200 with permissions key", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.PermissionsFakeServicer{
			ListPermissionsFn: func(_ context.Context) ([]permissions.Permission, error) {
				return []permissions.Permission{
					{ID: "id-1", CanonicalName: "rbac:read", ResourceType: "rbac", Name: "RBAC Read"},
					{ID: "id-2", CanonicalName: "rbac:manage", ResourceType: "rbac", Name: "RBAC Manage"},
				}, nil
			},
		}
		h := newTestHandler(svc)
		w := httptest.NewRecorder()
		h.ListPermissions(w, httptest.NewRequest(http.MethodGet, "/permissions", nil))

		require.Equal(t, http.StatusOK, w.Code)
		body := decodeBody(t, w)
		perms, ok := body["permissions"].([]any)
		require.True(t, ok)
		assert.Len(t, perms, 2)
	})

	t.Run("empty slice marshals as [] not null", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.PermissionsFakeServicer{
			ListPermissionsFn: func(_ context.Context) ([]permissions.Permission, error) {
				return []permissions.Permission{}, nil
			},
		}
		h := newTestHandler(svc)
		w := httptest.NewRecorder()
		h.ListPermissions(w, httptest.NewRequest(http.MethodGet, "/permissions", nil))

		require.Equal(t, http.StatusOK, w.Code)
		body := decodeBody(t, w)
		perms, ok := body["permissions"].([]any)
		require.True(t, ok)
		assert.Empty(t, perms)
		// Confirm raw JSON has [] not null
		assert.Contains(t, w.Body.String(), `"permissions":[]`)
	})

	t.Run("service error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.PermissionsFakeServicer{
			ListPermissionsFn: func(_ context.Context) ([]permissions.Permission, error) {
				return nil, errors.New("db error")
			},
		}
		h := newTestHandler(svc)
		w := httptest.NewRecorder()
		h.ListPermissions(w, httptest.NewRequest(http.MethodGet, "/permissions", nil))

		require.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "internal_error", decodeBody(t, w)["code"])
	})

	t.Run("nil service result marshals as [] not null", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.PermissionsFakeServicer{
			ListPermissionsFn: func(_ context.Context) ([]permissions.Permission, error) {
				return nil, nil
			},
		}
		h := newTestHandler(svc)
		w := httptest.NewRecorder()
		h.ListPermissions(w, httptest.NewRequest(http.MethodGet, "/permissions", nil))

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"permissions":[]`)
	})
}

// ── TestHandler_ListPermissionGroups ─────────────────────────────────────────

func TestHandler_ListPermissionGroups(t *testing.T) {
	t.Parallel()

	t.Run("returns 200 with groups key", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.PermissionsFakeServicer{
			ListPermissionGroupsFn: func(_ context.Context) ([]permissions.PermissionGroup, error) {
				return []permissions.PermissionGroup{
					{ID: "g-1", Name: "Group A", Members: []permissions.PermissionGroupMember{}},
					{ID: "g-2", Name: "Group B", Members: []permissions.PermissionGroupMember{}},
				}, nil
			},
		}
		h := newTestHandler(svc)
		w := httptest.NewRecorder()
		h.ListPermissionGroups(w, httptest.NewRequest(http.MethodGet, "/permissions/groups", nil))

		require.Equal(t, http.StatusOK, w.Code)
		body := decodeBody(t, w)
		groups, ok := body["groups"].([]any)
		require.True(t, ok)
		assert.Len(t, groups, 2)
	})

	t.Run("empty slice marshals as [] not null", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.PermissionsFakeServicer{
			ListPermissionGroupsFn: func(_ context.Context) ([]permissions.PermissionGroup, error) {
				return []permissions.PermissionGroup{}, nil
			},
		}
		h := newTestHandler(svc)
		w := httptest.NewRecorder()
		h.ListPermissionGroups(w, httptest.NewRequest(http.MethodGet, "/permissions/groups", nil))

		require.Equal(t, http.StatusOK, w.Code)
		body := decodeBody(t, w)
		groups, ok := body["groups"].([]any)
		require.True(t, ok)
		assert.Empty(t, groups)
		assert.Contains(t, w.Body.String(), `"groups":[]`)
	})

	t.Run("service error returns 500 internal_error", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.PermissionsFakeServicer{
			ListPermissionGroupsFn: func(_ context.Context) ([]permissions.PermissionGroup, error) {
				return nil, errors.New("db error")
			},
		}
		h := newTestHandler(svc)
		w := httptest.NewRecorder()
		h.ListPermissionGroups(w, httptest.NewRequest(http.MethodGet, "/permissions/groups", nil))

		require.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "internal_error", decodeBody(t, w)["code"])
	})

	t.Run("nil service result marshals as [] not null", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.PermissionsFakeServicer{
			ListPermissionGroupsFn: func(_ context.Context) ([]permissions.PermissionGroup, error) {
				return nil, nil
			},
		}
		h := newTestHandler(svc)
		w := httptest.NewRecorder()
		h.ListPermissionGroups(w, httptest.NewRequest(http.MethodGet, "/permissions/groups", nil))

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"groups":[]`)
	})
}
