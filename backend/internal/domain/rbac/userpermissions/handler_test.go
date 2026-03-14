package userpermissions_test

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

	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/rbac/userpermissions"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	hTargetID     = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	hActorID      = "ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb"
	hPermissionID = "cccccccc-dddd-eeee-ffff-aaaaaaaaaaaa"
	hGrantID      = "dddddddd-eeee-ffff-aaaa-bbbbbbbbbbbb"
)

func authedPermReq(t *testing.T, method, path string, body io.Reader) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, body)
	ctx := token.InjectUserIDForTest(r.Context(), hActorID)
	return r.WithContext(ctx)
}

func jsonPermBuf(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewBuffer(b)
}

func decodePermBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

func injectPermChi(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func samplePermission() userpermissions.UserPermission {
	return userpermissions.UserPermission{
		ID:            hGrantID,
		CanonicalName: "rbac:read",
		Name:          "RBAC Read",
		ResourceType:  "rbac",
		Scope:         "own",
		ExpiresAt:     time.Now().Add(24 * time.Hour),
		GrantedAt:     time.Now(),
		GrantedReason: "test grant",
	}
}

// ── T-R40h: GET 200 ListPermissions returns array ────────────────────────────

func TestHandler_ListPermissions_Success(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserPermissionsFakeServicer{
		ListPermissionsFn: func(_ context.Context, _ string) ([]userpermissions.UserPermission, error) {
			return []userpermissions.UserPermission{samplePermission()}, nil
		},
	}
	h := userpermissions.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectPermChi(
		httptest.NewRequest(http.MethodGet, "/rbac/users/"+hTargetID+"/permissions", nil),
		map[string]string{"user_id": hTargetID},
	)
	h.ListPermissions(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodePermBody(t, w)
	perms, ok := body["permissions"].([]any)
	require.True(t, ok)
	require.Len(t, perms, 1)
}

// ── GET 404 user_not_found on bad user UUID ───────────────────────────────────

func TestHandler_ListPermissions_UserNotFound(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserPermissionsFakeServicer{
		ListPermissionsFn: func(_ context.Context, _ string) ([]userpermissions.UserPermission, error) {
			return nil, userpermissions.ErrGrantNotFound
		},
	}
	h := userpermissions.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectPermChi(
		httptest.NewRequest(http.MethodGet, "/rbac/users/"+hTargetID+"/permissions", nil),
		map[string]string{"user_id": hTargetID},
	)
	h.ListPermissions(w, r)

	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "user_not_found", decodePermBody(t, w)["code"])
}

// ── T-R39h: POST 201 GrantPermission success ─────────────────────────────────

func TestHandler_GrantPermission_Success(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserPermissionsFakeServicer{
		GrantPermissionFn: func(_ context.Context, _, _ string, _ userpermissions.GrantPermissionInput) (userpermissions.UserPermission, error) {
			return samplePermission(), nil
		},
	}
	h := userpermissions.NewHandler(svc)
	w := httptest.NewRecorder()
	expiresAt := time.Now().Add(24 * time.Hour)
	body := jsonPermBuf(t, map[string]any{
		"permission_id":  hPermissionID,
		"granted_reason": "integration test",
		"expires_at":     expiresAt,
	})
	r := injectPermChi(
		authedPermReq(t, http.MethodPost, "/rbac/users/"+hTargetID+"/permissions", body),
		map[string]string{"user_id": hTargetID},
	)
	h.GrantPermission(w, r)

	require.Equal(t, http.StatusCreated, w.Code)
	resp := decodePermBody(t, w)
	assert.Equal(t, hGrantID, resp["id"])
}

// ── T-R39i: POST 422 permission_id_required ───────────────────────────────────

func TestHandler_GrantPermission_PermissionIDRequired(t *testing.T) {
	t.Parallel()
	h := userpermissions.NewHandler(&rbacsharedtest.UserPermissionsFakeServicer{})
	w := httptest.NewRecorder()
	expiresAt := time.Now().Add(24 * time.Hour)
	body := jsonPermBuf(t, map[string]any{
		"permission_id":  "",
		"granted_reason": "test",
		"expires_at":     expiresAt,
	})
	r := injectPermChi(
		authedPermReq(t, http.MethodPost, "/rbac/users/"+hTargetID+"/permissions", body),
		map[string]string{"user_id": hTargetID},
	)
	h.GrantPermission(w, r)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "permission_id_required", decodePermBody(t, w)["code"])
}

// ── T-R39j: POST 422 granted_reason_required ─────────────────────────────────

func TestHandler_GrantPermission_GrantedReasonRequired(t *testing.T) {
	t.Parallel()
	h := userpermissions.NewHandler(&rbacsharedtest.UserPermissionsFakeServicer{})
	w := httptest.NewRecorder()
	expiresAt := time.Now().Add(24 * time.Hour)
	body := jsonPermBuf(t, map[string]any{
		"permission_id":  hPermissionID,
		"granted_reason": "",
		"expires_at":     expiresAt,
	})
	r := injectPermChi(
		authedPermReq(t, http.MethodPost, "/rbac/users/"+hTargetID+"/permissions", body),
		map[string]string{"user_id": hTargetID},
	)
	h.GrantPermission(w, r)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "granted_reason_required", decodePermBody(t, w)["code"])
}

// ── POST 422 expires_at_in_past ────────────────────────────────────────────────

func TestHandler_GrantPermission_ExpiresAtInPast(t *testing.T) {
	t.Parallel()
	h := userpermissions.NewHandler(&rbacsharedtest.UserPermissionsFakeServicer{})
	w := httptest.NewRecorder()
	body := jsonPermBuf(t, map[string]any{
		"permission_id":  hPermissionID,
		"granted_reason": "test",
		"expires_at":     time.Now().Add(-time.Second),
	})
	r := injectPermChi(
		authedPermReq(t, http.MethodPost, "/rbac/users/"+hTargetID+"/permissions", body),
		map[string]string{"user_id": hTargetID},
	)
	h.GrantPermission(w, r)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "expires_at_in_past", decodePermBody(t, w)["code"])
}

// ── T-R39k: POST 422 expires_at_required ─────────────────────────────────────

func TestHandler_GrantPermission_ExpiresAtRequired(t *testing.T) {
	t.Parallel()
	h := userpermissions.NewHandler(&rbacsharedtest.UserPermissionsFakeServicer{})
	w := httptest.NewRecorder()
	body := jsonPermBuf(t, map[string]any{
		"permission_id":  hPermissionID,
		"granted_reason": "test",
		// no expires_at
	})
	r := injectPermChi(
		authedPermReq(t, http.MethodPost, "/rbac/users/"+hTargetID+"/permissions", body),
		map[string]string{"user_id": hTargetID},
	)
	h.GrantPermission(w, r)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "expires_at_required", decodePermBody(t, w)["code"])
}

// ── T-R39l: POST 409 permission_already_granted ───────────────────────────────

func TestHandler_GrantPermission_AlreadyGranted(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserPermissionsFakeServicer{
		GrantPermissionFn: func(_ context.Context, _, _ string, _ userpermissions.GrantPermissionInput) (userpermissions.UserPermission, error) {
			return userpermissions.UserPermission{}, userpermissions.ErrPermissionAlreadyGranted
		},
	}
	h := userpermissions.NewHandler(svc)
	w := httptest.NewRecorder()
	expiresAt := time.Now().Add(24 * time.Hour)
	body := jsonPermBuf(t, map[string]any{
		"permission_id":  hPermissionID,
		"granted_reason": "test",
		"expires_at":     expiresAt,
	})
	r := injectPermChi(
		authedPermReq(t, http.MethodPost, "/rbac/users/"+hTargetID+"/permissions", body),
		map[string]string{"user_id": hTargetID},
	)
	h.GrantPermission(w, r)

	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "permission_already_granted", decodePermBody(t, w)["code"])
}

// ── T-R39m: POST 403 privilege_escalation ────────────────────────────────────

func TestHandler_GrantPermission_PrivilegeEscalation(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserPermissionsFakeServicer{
		GrantPermissionFn: func(_ context.Context, _, _ string, _ userpermissions.GrantPermissionInput) (userpermissions.UserPermission, error) {
			return userpermissions.UserPermission{}, userpermissions.ErrPrivilegeEscalation
		},
	}
	h := userpermissions.NewHandler(svc)
	w := httptest.NewRecorder()
	expiresAt := time.Now().Add(24 * time.Hour)
	body := jsonPermBuf(t, map[string]any{
		"permission_id":  hPermissionID,
		"granted_reason": "test",
		"expires_at":     expiresAt,
	})
	r := injectPermChi(
		authedPermReq(t, http.MethodPost, "/rbac/users/"+hTargetID+"/permissions", body),
		map[string]string{"user_id": hTargetID},
	)
	h.GrantPermission(w, r)

	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "privilege_escalation", decodePermBody(t, w)["code"])
}

// ── T-R42h: POST 422 scope_not_allowed ───────────────────────────────────────

func TestHandler_GrantPermission_ScopeNotAllowed(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserPermissionsFakeServicer{
		GrantPermissionFn: func(_ context.Context, _, _ string, _ userpermissions.GrantPermissionInput) (userpermissions.UserPermission, error) {
			return userpermissions.UserPermission{}, rbacshared.ErrScopeNotAllowed
		},
	}
	h := userpermissions.NewHandler(svc)
	w := httptest.NewRecorder()
	expiresAt := time.Now().Add(24 * time.Hour)
	body := jsonPermBuf(t, map[string]any{
		"permission_id":  hPermissionID,
		"granted_reason": "test",
		"expires_at":     expiresAt,
		"scope":          "all",
	})
	r := injectPermChi(
		authedPermReq(t, http.MethodPost, "/rbac/users/"+hTargetID+"/permissions", body),
		map[string]string{"user_id": hTargetID},
	)
	h.GrantPermission(w, r)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "scope_not_allowed", decodePermBody(t, w)["code"])
}

// ── T-R43h: POST 422 permission_not_found ─────────────────────────────────────

func TestHandler_GrantPermission_PermissionNotFound(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserPermissionsFakeServicer{
		GrantPermissionFn: func(_ context.Context, _, _ string, _ userpermissions.GrantPermissionInput) (userpermissions.UserPermission, error) {
			return userpermissions.UserPermission{}, userpermissions.ErrPermissionNotFound
		},
	}
	h := userpermissions.NewHandler(svc)
	w := httptest.NewRecorder()
	expiresAt := time.Now().Add(24 * time.Hour)
	body := jsonPermBuf(t, map[string]any{
		"permission_id":  hPermissionID,
		"granted_reason": "test",
		"expires_at":     expiresAt,
	})
	r := injectPermChi(
		authedPermReq(t, http.MethodPost, "/rbac/users/"+hTargetID+"/permissions", body),
		map[string]string{"user_id": hTargetID},
	)
	h.GrantPermission(w, r)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "permission_not_found", decodePermBody(t, w)["code"])
}

// ── POST 401 missing auth ─────────────────────────────────────────────────────

func TestHandler_GrantPermission_NoAuth(t *testing.T) {
	t.Parallel()
	h := userpermissions.NewHandler(&rbacsharedtest.UserPermissionsFakeServicer{})
	w := httptest.NewRecorder()
	expiresAt := time.Now().Add(24 * time.Hour)
	body := jsonPermBuf(t, map[string]any{
		"permission_id":  hPermissionID,
		"granted_reason": "test",
		"expires_at":     expiresAt,
	})
	r := injectPermChi(
		httptest.NewRequest(http.MethodPost, "/rbac/users/"+hTargetID+"/permissions", body),
		map[string]string{"user_id": hTargetID},
	)
	h.GrantPermission(w, r)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "unauthorized", decodePermBody(t, w)["code"])
}

// ── POST 500 internal_error ───────────────────────────────────────────────────

func TestHandler_GrantPermission_InternalError(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserPermissionsFakeServicer{
		GrantPermissionFn: func(_ context.Context, _, _ string, _ userpermissions.GrantPermissionInput) (userpermissions.UserPermission, error) {
			return userpermissions.UserPermission{}, errors.New("db down")
		},
	}
	h := userpermissions.NewHandler(svc)
	w := httptest.NewRecorder()
	expiresAt := time.Now().Add(24 * time.Hour)
	body := jsonPermBuf(t, map[string]any{
		"permission_id":  hPermissionID,
		"granted_reason": "test",
		"expires_at":     expiresAt,
	})
	r := injectPermChi(
		authedPermReq(t, http.MethodPost, "/rbac/users/"+hTargetID+"/permissions", body),
		map[string]string{"user_id": hTargetID},
	)
	h.GrantPermission(w, r)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodePermBody(t, w)["code"])
}

// ── T-R41h: DELETE 204 RevokePermission success ───────────────────────────────

func TestHandler_RevokePermission_Success(t *testing.T) {
	t.Parallel()
	h := userpermissions.NewHandler(&rbacsharedtest.UserPermissionsFakeServicer{})
	w := httptest.NewRecorder()
	r := injectPermChi(
		authedPermReq(t, http.MethodDelete, "/rbac/users/"+hTargetID+"/permissions/"+hGrantID, nil),
		map[string]string{"user_id": hTargetID, "grant_id": hGrantID},
	)
	h.RevokePermission(w, r)

	require.Equal(t, http.StatusNoContent, w.Code)
}

// ── T-R41i: DELETE 404 grant_not_found ────────────────────────────────────────

func TestHandler_RevokePermission_GrantNotFound(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserPermissionsFakeServicer{
		RevokePermissionFn: func(_ context.Context, _, _, _ string) error {
			return userpermissions.ErrGrantNotFound
		},
	}
	h := userpermissions.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectPermChi(
		authedPermReq(t, http.MethodDelete, "/rbac/users/"+hTargetID+"/permissions/"+hGrantID, nil),
		map[string]string{"user_id": hTargetID, "grant_id": hGrantID},
	)
	h.RevokePermission(w, r)

	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "grant_not_found", decodePermBody(t, w)["code"])
}

// ── T-R41j: DELETE 401 missing auth ───────────────────────────────────────────

func TestHandler_RevokePermission_NoAuth(t *testing.T) {
	t.Parallel()
	h := userpermissions.NewHandler(&rbacsharedtest.UserPermissionsFakeServicer{})
	w := httptest.NewRecorder()
	r := injectPermChi(
		httptest.NewRequest(http.MethodDelete, "/rbac/users/"+hTargetID+"/permissions/"+hGrantID, nil),
		map[string]string{"user_id": hTargetID, "grant_id": hGrantID},
	)
	h.RevokePermission(w, r)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "unauthorized", decodePermBody(t, w)["code"])
}

// ── DELETE 500 internal_error ─────────────────────────────────────────────────

func TestHandler_RevokePermission_InternalError(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserPermissionsFakeServicer{
		RevokePermissionFn: func(_ context.Context, _, _, _ string) error {
			return errors.New("db down")
		},
	}
	h := userpermissions.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectPermChi(
		authedPermReq(t, http.MethodDelete, "/rbac/users/"+hTargetID+"/permissions/"+hGrantID, nil),
		map[string]string{"user_id": hTargetID, "grant_id": hGrantID},
	)
	h.RevokePermission(w, r)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodePermBody(t, w)["code"])
}

// ── GET 500 internal_error ────────────────────────────────────────────────────

func TestHandler_ListPermissions_InternalError(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserPermissionsFakeServicer{
		ListPermissionsFn: func(_ context.Context, _ string) ([]userpermissions.UserPermission, error) {
			return nil, errors.New("db down")
		},
	}
	h := userpermissions.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectPermChi(
		httptest.NewRequest(http.MethodGet, "/rbac/users/"+hTargetID+"/permissions", nil),
		map[string]string{"user_id": hTargetID},
	)
	h.ListPermissions(w, r)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodePermBody(t, w)["code"])
}

// ── POST malformed JSON body returns 4xx ──────────────────────────────────────

// ── GET 200 empty permissions list ──────────────────────────────────────────────────

func TestHandler_ListPermissions_EmptySlice(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserPermissionsFakeServicer{
		ListPermissionsFn: func(_ context.Context, _ string) ([]userpermissions.UserPermission, error) {
			return []userpermissions.UserPermission{}, nil
		},
	}
	h := userpermissions.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectPermChi(
		httptest.NewRequest(http.MethodGet, "/rbac/users/"+hTargetID+"/permissions", nil),
		map[string]string{"user_id": hTargetID},
	)
	h.ListPermissions(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	body := decodePermBody(t, w)
	perms, ok := body["permissions"].([]any)
	require.True(t, ok, "permissions must be a JSON array")
	require.Empty(t, perms)
}

func TestHandler_GrantPermission_MalformedBody(t *testing.T) {
	t.Parallel()
	h := userpermissions.NewHandler(&rbacsharedtest.UserPermissionsFakeServicer{})
	w := httptest.NewRecorder()
	r := injectPermChi(
		authedPermReq(t, http.MethodPost, "/rbac/users/"+hTargetID+"/permissions",
			bytes.NewBufferString("not json")),
		map[string]string{"user_id": hTargetID},
	)
	h.GrantPermission(w, r)

	assert.True(t, w.Code >= 400, "expected 4xx, got %d", w.Code)
}
