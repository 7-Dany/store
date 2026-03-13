package roles_test

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

	"github.com/7-Dany/store/backend/internal/domain/rbac/roles"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testRoleID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testPermID = "11111111-2222-3333-4444-555555555555"
	testUserID = "ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb"
)

// authedReq creates a request with testUserID injected into the context.
func authedReq(t *testing.T, method, path string, body io.Reader) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, body)
	ctx := token.InjectUserIDForTest(r.Context(), testUserID)
	return r.WithContext(ctx)
}

func jsonBodyBytes(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewBuffer(b)
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

// injectChi sets chi URL params on r to simulate chi router extraction.
func injectChi(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func sampleRole() roles.Role {
	return roles.Role{
		ID:           testRoleID,
		Name:         "test_role",
		IsSystemRole: false,
		IsOwnerRole:  false,
		IsActive:     true,
		CreatedAt:    time.Now(),
	}
}

// ── TestHandler_ListRoles ─────────────────────────────────────────────────────

func TestHandler_ListRoles(t *testing.T) {
	t.Parallel()

	t.Run("returns 200 with roles key", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			ListRolesFn: func(_ context.Context) ([]roles.Role, error) {
				return []roles.Role{sampleRole(), sampleRole()}, nil
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		h.ListRoles(w, httptest.NewRequest(http.MethodGet, "/rbac/roles", nil))

		require.Equal(t, http.StatusOK, w.Code)
		body := decodeBody(t, w)
		rolesList, ok := body["roles"].([]any)
		require.True(t, ok)
		assert.Len(t, rolesList, 2)
	})

	t.Run("empty slice marshals as []", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			ListRolesFn: func(_ context.Context) ([]roles.Role, error) {
				return []roles.Role{}, nil
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		h.ListRoles(w, httptest.NewRequest(http.MethodGet, "/rbac/roles", nil))

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"roles":[]`)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			ListRolesFn: func(_ context.Context) ([]roles.Role, error) {
				return nil, errors.New("db error")
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		h.ListRoles(w, httptest.NewRequest(http.MethodGet, "/rbac/roles", nil))

		require.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "internal_error", decodeBody(t, w)["code"])
	})
}

// ── TestHandler_CreateRole ────────────────────────────────────────────────────

func TestHandler_CreateRole(t *testing.T) {
	t.Parallel()

	t.Run("valid body returns 201 with role", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			CreateRoleFn: func(_ context.Context, in roles.CreateRoleInput) (roles.Role, error) {
				return roles.Role{ID: testRoleID, Name: in.Name}, nil
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, map[string]any{"name": "my_role"})
		h.CreateRole(w, httptest.NewRequest(http.MethodPost, "/rbac/roles", body))

		require.Equal(t, http.StatusCreated, w.Code)
		resp := decodeBody(t, w)
		assert.Equal(t, "my_role", resp["name"])
	})

	t.Run("empty name returns 422 validation_error", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, map[string]any{"name": ""})
		h.CreateRole(w, httptest.NewRequest(http.MethodPost, "/rbac/roles", body))

		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "validation_error", decodeBody(t, w)["code"])
	})

	t.Run("name 101 chars returns 422", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, map[string]any{"name": strings.Repeat("a", 101)})
		h.CreateRole(w, httptest.NewRequest(http.MethodPost, "/rbac/roles", body))

		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "validation_error", decodeBody(t, w)["code"])
	})

	t.Run("malformed JSON returns 400", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		h.CreateRole(w, httptest.NewRequest(http.MethodPost, "/rbac/roles", strings.NewReader("{bad")))

		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("body exceeds MaxBodyBytes returns 413", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		huge := strings.NewReader(`{"name":"` + strings.Repeat("x", 2<<20) + `"}`)
		h.CreateRole(w, httptest.NewRequest(http.MethodPost, "/rbac/roles", huge))

		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			CreateRoleFn: func(_ context.Context, _ roles.CreateRoleInput) (roles.Role, error) {
				return roles.Role{}, errors.New("db error")
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, map[string]any{"name": "ok_role"})
		h.CreateRole(w, httptest.NewRequest(http.MethodPost, "/rbac/roles", body))

		require.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "internal_error", decodeBody(t, w)["code"])
	})
}

// ── TestHandler_GetRole ───────────────────────────────────────────────────────

func TestHandler_GetRole(t *testing.T) {
	t.Parallel()

	t.Run("existing role returns 200", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			GetRoleFn: func(_ context.Context, _ string) (roles.Role, error) {
				return sampleRole(), nil
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		r := injectChi(httptest.NewRequest(http.MethodGet, "/rbac/roles/"+testRoleID, nil), map[string]string{"id": testRoleID})
		h.GetRole(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, testRoleID, decodeBody(t, w)["id"])
	})

	t.Run("ErrRoleNotFound returns 404 role_not_found", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			GetRoleFn: func(_ context.Context, _ string) (roles.Role, error) {
				return roles.Role{}, roles.ErrRoleNotFound
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		r := injectChi(httptest.NewRequest(http.MethodGet, "/rbac/roles/bad", nil), map[string]string{"id": "bad"})
		h.GetRole(w, r)

		require.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "role_not_found", decodeBody(t, w)["code"])
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			GetRoleFn: func(_ context.Context, _ string) (roles.Role, error) {
				return roles.Role{}, errors.New("db error")
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		r := injectChi(httptest.NewRequest(http.MethodGet, "/rbac/roles/"+testRoleID, nil), map[string]string{"id": testRoleID})
		h.GetRole(w, r)

		require.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// ── TestHandler_UpdateRole ────────────────────────────────────────────────────

func TestHandler_UpdateRole(t *testing.T) {
	t.Parallel()

	nameVal := "updated_role"

	t.Run("valid patch returns 200 with updated role", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			UpdateRoleFn: func(_ context.Context, _ string, in roles.UpdateRoleInput) (roles.Role, error) {
				r := sampleRole()
				if in.Name != nil {
					r.Name = *in.Name
				}
				return r, nil
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, map[string]any{"name": &nameVal})
		r := injectChi(authedReq(t, http.MethodPatch, "/rbac/roles/"+testRoleID, body), map[string]string{"id": testRoleID})
		h.UpdateRole(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "updated_role", decodeBody(t, w)["name"])
	})

	t.Run("empty body (no fields) returns 422", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, map[string]any{})
		r := injectChi(httptest.NewRequest(http.MethodPatch, "/rbac/roles/"+testRoleID, body), map[string]string{"id": testRoleID})
		h.UpdateRole(w, r)

		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "validation_error", decodeBody(t, w)["code"])
	})

	t.Run("name pointer to empty string returns 422", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		empty := ""
		body := jsonBodyBytes(t, map[string]any{"name": &empty})
		r := injectChi(httptest.NewRequest(http.MethodPatch, "/rbac/roles/"+testRoleID, body), map[string]string{"id": testRoleID})
		h.UpdateRole(w, r)

		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "validation_error", decodeBody(t, w)["code"])
	})

	t.Run("name 101 chars returns 422", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		long := strings.Repeat("a", 101)
		body := jsonBodyBytes(t, map[string]any{"name": &long})
		r := injectChi(httptest.NewRequest(http.MethodPatch, "/rbac/roles/"+testRoleID, body), map[string]string{"id": testRoleID})
		h.UpdateRole(w, r)

		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "validation_error", decodeBody(t, w)["code"])
	})

	t.Run("malformed JSON returns 400", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		r := injectChi(httptest.NewRequest(http.MethodPatch, "/rbac/roles/"+testRoleID, strings.NewReader("{bad")), map[string]string{"id": testRoleID})
		h.UpdateRole(w, r)

		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("body exceeds MaxBodyBytes returns 413", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		huge := strings.NewReader(`{"name":"` + strings.Repeat("x", 2<<20) + `"}`)
		r := injectChi(httptest.NewRequest(http.MethodPatch, "/rbac/roles/"+testRoleID, huge), map[string]string{"id": testRoleID})
		h.UpdateRole(w, r)

		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	t.Run("ErrRoleNotFound returns 404", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			UpdateRoleFn: func(_ context.Context, _ string, _ roles.UpdateRoleInput) (roles.Role, error) {
				return roles.Role{}, roles.ErrRoleNotFound
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, map[string]any{"name": &nameVal})
		r := injectChi(httptest.NewRequest(http.MethodPatch, "/rbac/roles/"+testRoleID, body), map[string]string{"id": testRoleID})
		h.UpdateRole(w, r)

		require.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "role_not_found", decodeBody(t, w)["code"])
	})

	t.Run("ErrSystemRoleImmutable returns 409", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			UpdateRoleFn: func(_ context.Context, _ string, _ roles.UpdateRoleInput) (roles.Role, error) {
				return roles.Role{}, rbac.ErrSystemRoleImmutable
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, map[string]any{"name": &nameVal})
		r := injectChi(httptest.NewRequest(http.MethodPatch, "/rbac/roles/"+testRoleID, body), map[string]string{"id": testRoleID})
		h.UpdateRole(w, r)

		require.Equal(t, http.StatusConflict, w.Code)
		assert.Equal(t, "system_role_immutable", decodeBody(t, w)["code"])
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			UpdateRoleFn: func(_ context.Context, _ string, _ roles.UpdateRoleInput) (roles.Role, error) {
				return roles.Role{}, errors.New("db error")
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, map[string]any{"name": &nameVal})
		r := injectChi(httptest.NewRequest(http.MethodPatch, "/rbac/roles/"+testRoleID, body), map[string]string{"id": testRoleID})
		h.UpdateRole(w, r)

		require.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// ── TestHandler_DeleteRole ────────────────────────────────────────────────────

func TestHandler_DeleteRole(t *testing.T) {
	t.Parallel()

	t.Run("success returns 204", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		r := injectChi(httptest.NewRequest(http.MethodDelete, "/rbac/roles/"+testRoleID, nil), map[string]string{"id": testRoleID})
		h.DeleteRole(w, r)

		require.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("ErrRoleNotFound returns 404 role_not_found", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			DeleteRoleFn: func(_ context.Context, _ string) error { return roles.ErrRoleNotFound },
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		r := injectChi(httptest.NewRequest(http.MethodDelete, "/rbac/roles/"+testRoleID, nil), map[string]string{"id": testRoleID})
		h.DeleteRole(w, r)

		require.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "role_not_found", decodeBody(t, w)["code"])
	})

	t.Run("ErrSystemRoleImmutable returns 409 system_role_immutable", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			DeleteRoleFn: func(_ context.Context, _ string) error { return rbac.ErrSystemRoleImmutable },
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		r := injectChi(httptest.NewRequest(http.MethodDelete, "/rbac/roles/"+testRoleID, nil), map[string]string{"id": testRoleID})
		h.DeleteRole(w, r)

		require.Equal(t, http.StatusConflict, w.Code)
		assert.Equal(t, "system_role_immutable", decodeBody(t, w)["code"])
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			DeleteRoleFn: func(_ context.Context, _ string) error { return errors.New("db error") },
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		r := injectChi(httptest.NewRequest(http.MethodDelete, "/rbac/roles/"+testRoleID, nil), map[string]string{"id": testRoleID})
		h.DeleteRole(w, r)

		require.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// ── TestHandler_ListRolePermissions ──────────────────────────────────────────

func TestHandler_ListRolePermissions(t *testing.T) {
	t.Parallel()

	t.Run("returns 200 with permissions key", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			ListRolePermissionsFn: func(_ context.Context, _ string) ([]roles.RolePermission, error) {
				return []roles.RolePermission{
					{PermissionID: testPermID, CanonicalName: "rbac:read", AccessType: "direct", Scope: "all"},
				}, nil
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		r := injectChi(httptest.NewRequest(http.MethodGet, "/rbac/roles/"+testRoleID+"/permissions", nil), map[string]string{"id": testRoleID})
		h.ListRolePermissions(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		body := decodeBody(t, w)
		perms, ok := body["permissions"].([]any)
		require.True(t, ok)
		assert.Len(t, perms, 1)
	})

	t.Run("empty slice marshals as []", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		r := injectChi(httptest.NewRequest(http.MethodGet, "/rbac/roles/"+testRoleID+"/permissions", nil), map[string]string{"id": testRoleID})
		h.ListRolePermissions(w, r)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"permissions":[]`)
	})

	t.Run("ErrRoleNotFound returns 404 role_not_found", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			ListRolePermissionsFn: func(_ context.Context, _ string) ([]roles.RolePermission, error) {
				return nil, roles.ErrRoleNotFound
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		r := injectChi(httptest.NewRequest(http.MethodGet, "/rbac/roles/bad/permissions", nil), map[string]string{"id": "bad"})
		h.ListRolePermissions(w, r)

		require.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "role_not_found", decodeBody(t, w)["code"])
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			ListRolePermissionsFn: func(_ context.Context, _ string) ([]roles.RolePermission, error) {
				return nil, errors.New("db error")
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		r := injectChi(httptest.NewRequest(http.MethodGet, "/rbac/roles/"+testRoleID+"/permissions", nil), map[string]string{"id": testRoleID})
		h.ListRolePermissions(w, r)

		require.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// ── TestHandler_AddRolePermission ─────────────────────────────────────────────

func TestHandler_AddRolePermission(t *testing.T) {
	t.Parallel()

	validBody := func() map[string]any {
		return map[string]any{
			"permission_id":  testPermID,
			"access_type":    "direct",
			"scope":          "all",
			"granted_reason": "integration test",
		}
	}

	t.Run("no user ID in context returns 401", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, validBody())
		r := injectChi(httptest.NewRequest(http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", body), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "unauthorized", decodeBody(t, w)["code"])
	})

	t.Run("valid body returns 204", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, validBody())
		r := injectChi(authedReq(t, http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", body), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("empty permission_id returns 422", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		b := validBody()
		b["permission_id"] = ""
		body := jsonBodyBytes(t, b)
		r := injectChi(authedReq(t, http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", body), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "validation_error", decodeBody(t, w)["code"])
	})

	t.Run("empty granted_reason returns 422", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		b := validBody()
		b["granted_reason"] = ""
		body := jsonBodyBytes(t, b)
		r := injectChi(authedReq(t, http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", body), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "validation_error", decodeBody(t, w)["code"])
	})

	t.Run("invalid access_type returns 422", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		b := validBody()
		b["access_type"] = "bad_type"
		body := jsonBodyBytes(t, b)
		r := injectChi(authedReq(t, http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", body), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "validation_error", decodeBody(t, w)["code"])
	})

	t.Run("invalid scope returns 422", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		b := validBody()
		b["scope"] = "bad_scope"
		body := jsonBodyBytes(t, b)
		r := injectChi(authedReq(t, http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", body), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "validation_error", decodeBody(t, w)["code"])
	})

	t.Run("malformed JSON returns 400", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		r := injectChi(authedReq(t, http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", strings.NewReader("{bad")), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("body exceeds MaxBodyBytes returns 413", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		huge := strings.NewReader(`{"permission_id":"` + strings.Repeat("x", 2<<20) + `"}`)
		r := injectChi(authedReq(t, http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", huge), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	t.Run("malformed permission_id UUID returns 422", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		b := validBody()
		b["permission_id"] = "not-a-uuid"
		body := jsonBodyBytes(t, b)
		r := injectChi(authedReq(t, http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", body), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Equal(t, "validation_error", decodeBody(t, w)["code"])
	})

	t.Run("ErrRoleNotFound returns 404 role_not_found", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			AddRolePermissionFn: func(_ context.Context, _ string, _ roles.AddRolePermissionInput) error {
				return roles.ErrRoleNotFound
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, validBody())
		r := injectChi(authedReq(t, http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", body), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "role_not_found", decodeBody(t, w)["code"])
	})

	t.Run("ErrPermissionNotFound returns 404 permission_not_found", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
		AddRolePermissionFn: func(_ context.Context, _ string, _ roles.AddRolePermissionInput) error {
		return roles.ErrPermissionNotFound
		},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, validBody())
		r := injectChi(authedReq(t, http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", body), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "permission_not_found", decodeBody(t, w)["code"])
	})

	// T-R31c — duplicate grant returns 409 grant_already_exists
	t.Run("duplicate grant returns 409 grant_already_exists", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			AddRolePermissionFn: func(_ context.Context, _ string, _ roles.AddRolePermissionInput) error {
				return roles.ErrGrantAlreadyExists
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, validBody())
		r := injectChi(authedReq(t, http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", body), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusConflict, w.Code)
		assert.Equal(t, "grant_already_exists", decodeBody(t, w)["code"])
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			AddRolePermissionFn: func(_ context.Context, _ string, _ roles.AddRolePermissionInput) error {
				return errors.New("db error")
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		body := jsonBodyBytes(t, validBody())
		r := injectChi(authedReq(t, http.MethodPost, "/rbac/roles/"+testRoleID+"/permissions", body), map[string]string{"id": testRoleID})
		h.AddRolePermission(w, r)

		require.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

// ── TestHandler_RemoveRolePermission ─────────────────────────────────────────

func TestHandler_RemoveRolePermission(t *testing.T) {
	t.Parallel()

	// T-R31b — no user ID in context returns 401
	t.Run("no user ID in context returns 401", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		// Request without token context — no authedReq.
		r := injectChi(
			httptest.NewRequest(http.MethodDelete, "/rbac/roles/"+testRoleID+"/permissions/"+testPermID, nil),
			map[string]string{"id": testRoleID, "perm_id": testPermID},
		)
		h.RemoveRolePermission(w, r)

		require.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "unauthorized", decodeBody(t, w)["code"])
	})

	t.Run("success returns 204", func(t *testing.T) {
		t.Parallel()
		h := roles.NewHandler(&rbacsharedtest.RolesFakeServicer{})
		w := httptest.NewRecorder()
		r := injectChi(
			authedReq(t, http.MethodDelete, "/rbac/roles/"+testRoleID+"/permissions/"+testPermID, nil),
			map[string]string{"id": testRoleID, "perm_id": testPermID},
		)
		h.RemoveRolePermission(w, r)

		require.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("ErrRolePermissionNotFound returns 404 role_permission_not_found", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			RemoveRolePermissionFn: func(_ context.Context, _, _, _ string) error {
				return roles.ErrRolePermissionNotFound
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		r := injectChi(
			authedReq(t, http.MethodDelete, "/rbac/roles/"+testRoleID+"/permissions/"+testPermID, nil),
			map[string]string{"id": testRoleID, "perm_id": testPermID},
		)
		h.RemoveRolePermission(w, r)

		require.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "role_permission_not_found", decodeBody(t, w)["code"])
	})

	t.Run("service error returns 500", func(t *testing.T) {
		t.Parallel()
		svc := &rbacsharedtest.RolesFakeServicer{
			RemoveRolePermissionFn: func(_ context.Context, _, _, _ string) error {
				return errors.New("db error")
			},
		}
		h := roles.NewHandler(svc)
		w := httptest.NewRecorder()
		r := injectChi(
			authedReq(t, http.MethodDelete, "/rbac/roles/"+testRoleID+"/permissions/"+testPermID, nil),
			map[string]string{"id": testRoleID, "perm_id": testPermID},
		)
		h.RemoveRolePermission(w, r)

		require.Equal(t, http.StatusInternalServerError, w.Code)
	})
}
