package userroles_test

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

	adminsharedtest "github.com/7-Dany/store/backend/internal/domain/admin/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/admin/userroles"
	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testHandlerTargetID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testHandlerActorID  = "ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb"
	testHandlerRoleID   = "cccccccc-dddd-eeee-ffff-aaaaaaaaaaaa"
)

func authedUserRoleReq(t *testing.T, method, path string, body io.Reader) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, body)
	ctx := token.InjectUserIDForTest(r.Context(), testHandlerActorID)
	return r.WithContext(ctx)
}

func jsonBuf(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewBuffer(b)
}

func decodeUserRoleBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

func injectUserRoleChi(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func sampleUserRole() userroles.UserRole {
	return userroles.UserRole{
		UserID:      testHandlerTargetID,
		RoleID:      testHandlerRoleID,
		RoleName:    "admin",
		IsOwnerRole: false,
		GrantedAt:   time.Now(),
	}
}

func TestHandler_GetUserRole_NotFound(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		GetUserRoleFn: func(_ context.Context, _ string) (userroles.UserRole, error) {
			return userroles.UserRole{}, userroles.ErrUserRoleNotFound
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectUserRoleChi(
		httptest.NewRequest(http.MethodGet, "/admin/users/"+testHandlerTargetID+"/role", nil),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.GetUserRole(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "user_role_not_found", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_GetUserRole_Success(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		GetUserRoleFn: func(_ context.Context, _ string) (userroles.UserRole, error) {
			return sampleUserRole(), nil
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectUserRoleChi(
		httptest.NewRequest(http.MethodGet, "/admin/users/"+testHandlerTargetID+"/role", nil),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.GetUserRole(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeUserRoleBody(t, w)
	assert.Equal(t, testHandlerTargetID, body["user_id"])
	assert.Equal(t, "admin", body["role_name"])
}

func TestHandler_AssignRole_CannotReassignOwner(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		AssignRoleFn: func(_ context.Context, _, _ string, _ userroles.AssignRoleInput) (userroles.UserRole, error) {
			return userroles.UserRole{}, platformrbac.ErrCannotReassignOwner
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	body := jsonBuf(t, map[string]any{"role_id": testHandlerRoleID, "granted_reason": "test"})
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodPut, "/admin/users/"+testHandlerTargetID+"/role", body),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.AssignRole(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "cannot_reassign_owner", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_AssignRole_CannotModifyOwnRole(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		AssignRoleFn: func(_ context.Context, _, _ string, _ userroles.AssignRoleInput) (userroles.UserRole, error) {
			return userroles.UserRole{}, platformrbac.ErrCannotModifyOwnRole
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	body := jsonBuf(t, map[string]any{"role_id": testHandlerRoleID, "granted_reason": "test"})
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodPut, "/admin/users/"+testHandlerTargetID+"/role", body),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.AssignRole(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "cannot_modify_own_role", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_AssignRole_RoleNotFound(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		AssignRoleFn: func(_ context.Context, _, _ string, _ userroles.AssignRoleInput) (userroles.UserRole, error) {
			return userroles.UserRole{}, userroles.ErrRoleNotFound
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	body := jsonBuf(t, map[string]any{"role_id": testHandlerRoleID, "granted_reason": "test"})
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodPut, "/admin/users/"+testHandlerTargetID+"/role", body),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.AssignRole(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "role_not_found", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_AssignRole_Success(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		AssignRoleFn: func(_ context.Context, _, _ string, _ userroles.AssignRoleInput) (userroles.UserRole, error) {
			return sampleUserRole(), nil
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	body := jsonBuf(t, map[string]any{"role_id": testHandlerRoleID, "granted_reason": "test"})
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodPut, "/admin/users/"+testHandlerTargetID+"/role", body),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.AssignRole(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "admin", decodeUserRoleBody(t, w)["role_name"])
}

func TestHandler_AssignRole_NoAuth(t *testing.T) {
	t.Parallel()
	h := userroles.NewHandler(&adminsharedtest.UserRolesFakeServicer{})
	w := httptest.NewRecorder()
	body := jsonBuf(t, map[string]any{"role_id": testHandlerRoleID, "granted_reason": "test"})
	r := injectUserRoleChi(
		httptest.NewRequest(http.MethodPut, "/admin/users/"+testHandlerTargetID+"/role", body),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.AssignRole(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "unauthorized", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_RemoveRole_LastOwnerRemoval(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		RemoveRoleFn: func(_ context.Context, _, _ string) error {
			return userroles.ErrLastOwnerRemoval
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodDelete, "/admin/users/"+testHandlerTargetID+"/role", nil),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.RemoveRole(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "last_owner_removal", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_RemoveRole_Success(t *testing.T) {
	t.Parallel()
	h := userroles.NewHandler(&adminsharedtest.UserRolesFakeServicer{})
	w := httptest.NewRecorder()
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodDelete, "/admin/users/"+testHandlerTargetID+"/role", nil),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.RemoveRole(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestHandler_RemoveRole_NotFound(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		RemoveRoleFn: func(_ context.Context, _, _ string) error {
			return userroles.ErrUserRoleNotFound
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodDelete, "/admin/users/"+testHandlerTargetID+"/role", nil),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.RemoveRole(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "user_role_not_found", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_GetUserRole_InternalError(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		GetUserRoleFn: func(_ context.Context, _ string) (userroles.UserRole, error) {
			return userroles.UserRole{}, errors.New("db down")
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectUserRoleChi(
		httptest.NewRequest(http.MethodGet, "/admin/users/"+testHandlerTargetID+"/role", nil),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.GetUserRole(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_AssignRole_RoleIDRequired(t *testing.T) {
	t.Parallel()
	h := userroles.NewHandler(&adminsharedtest.UserRolesFakeServicer{})
	w := httptest.NewRecorder()
	body := jsonBuf(t, map[string]any{"role_id": "", "granted_reason": "test"})
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodPut, "/admin/users/"+testHandlerTargetID+"/role", body),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.AssignRole(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "role_id_required", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_AssignRole_GrantedReasonRequired(t *testing.T) {
	t.Parallel()
	h := userroles.NewHandler(&adminsharedtest.UserRolesFakeServicer{})
	w := httptest.NewRecorder()
	body := jsonBuf(t, map[string]any{"role_id": testHandlerRoleID, "granted_reason": ""})
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodPut, "/admin/users/"+testHandlerTargetID+"/role", body),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.AssignRole(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "granted_reason_required", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_AssignRole_InternalError(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		AssignRoleFn: func(_ context.Context, _, _ string, _ userroles.AssignRoleInput) (userroles.UserRole, error) {
			return userroles.UserRole{}, errors.New("db down")
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	body := jsonBuf(t, map[string]any{"role_id": testHandlerRoleID, "granted_reason": "test"})
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodPut, "/admin/users/"+testHandlerTargetID+"/role", body),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.AssignRole(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_AssignRole_MalformedBody(t *testing.T) {
	t.Parallel()
	h := userroles.NewHandler(&adminsharedtest.UserRolesFakeServicer{})
	w := httptest.NewRecorder()
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodPut, "/admin/users/"+testHandlerTargetID+"/role",
			bytes.NewBufferString("not json")),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.AssignRole(w, r)
	assert.True(t, w.Code >= 400, "expected 4xx status, got %d", w.Code)
}

func TestHandler_RemoveRole_CannotModifyOwnRole(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		RemoveRoleFn: func(_ context.Context, _, _ string) error {
			return platformrbac.ErrCannotModifyOwnRole
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodDelete, "/admin/users/"+testHandlerTargetID+"/role", nil),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.RemoveRole(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "cannot_modify_own_role", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_RemoveRole_CannotReassignOwner(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		RemoveRoleFn: func(_ context.Context, _, _ string) error {
			return platformrbac.ErrCannotReassignOwner
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodDelete, "/admin/users/"+testHandlerTargetID+"/role", nil),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.RemoveRole(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "cannot_reassign_owner", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_RemoveRole_InternalError(t *testing.T) {
	t.Parallel()
	svc := &adminsharedtest.UserRolesFakeServicer{
		RemoveRoleFn: func(_ context.Context, _, _ string) error {
			return errors.New("db down")
		},
	}
	h := userroles.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectUserRoleChi(
		authedUserRoleReq(t, http.MethodDelete, "/admin/users/"+testHandlerTargetID+"/role", nil),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.RemoveRole(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodeUserRoleBody(t, w)["code"])
}

func TestHandler_RemoveRole_NoAuth(t *testing.T) {
	t.Parallel()
	h := userroles.NewHandler(&adminsharedtest.UserRolesFakeServicer{})
	w := httptest.NewRecorder()
	r := injectUserRoleChi(
		httptest.NewRequest(http.MethodDelete, "/admin/users/"+testHandlerTargetID+"/role", nil),
		map[string]string{"user_id": testHandlerTargetID},
	)
	h.RemoveRole(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "unauthorized", decodeUserRoleBody(t, w)["code"])
}
