package userlock_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/7-Dany/store/backend/internal/domain/rbac/userlock"
	platformrbac "github.com/7-Dany/store/backend/internal/platform/rbac"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	hTargetID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	hActorID  = "ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb"
)

func authedLockReq(t *testing.T, method, path string, body io.Reader) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, body)
	ctx := token.InjectUserIDForTest(r.Context(), hActorID)
	return r.WithContext(ctx)
}

func jsonLockBuf(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewBuffer(b)
}

func decodeLockBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

func injectLockChi(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// ── POST /lock ────────────────────────────────────────────────────────────────

// T-R45h: success → 204
func TestHandler_LockUser_Success(t *testing.T) {
	t.Parallel()
	h := userlock.NewHandler(&rbacsharedtest.UserLockFakeServicer{})
	w := httptest.NewRecorder()
	r := injectLockChi(
		authedLockReq(t, http.MethodPost, "/admin/users/"+hTargetID+"/lock",
			jsonLockBuf(t, map[string]any{"reason": "spam"})),
		map[string]string{"user_id": hTargetID},
	)
	h.LockUser(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

// T-R45i: reason empty → 422 reason_required (caught by handler validation, service never called)
func TestHandler_LockUser_ReasonRequired(t *testing.T) {
	t.Parallel()
	// Default fake: LockUserFn is nil, so if the handler incorrectly calls the
	// service it returns nil and the test will fail on the status code assertion.
	h := userlock.NewHandler(&rbacsharedtest.UserLockFakeServicer{})
	w := httptest.NewRecorder()
	r := injectLockChi(
		authedLockReq(t, http.MethodPost, "/admin/users/"+hTargetID+"/lock",
			jsonLockBuf(t, map[string]any{"reason": ""})),
		map[string]string{"user_id": hTargetID},
	)
	h.LockUser(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "reason_required", decodeLockBody(t, w)["code"])
}

// T-R45j: svc returns ErrCannotLockSelf → 409 cannot_lock_self
func TestHandler_LockUser_CannotLockSelf(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserLockFakeServicer{
		LockUserFn: func(_ context.Context, _, _ string, _ userlock.LockUserInput) error {
			return platformrbac.ErrCannotLockSelf
		},
	}
	h := userlock.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectLockChi(
		authedLockReq(t, http.MethodPost, "/admin/users/"+hTargetID+"/lock",
			jsonLockBuf(t, map[string]any{"reason": "test"})),
		map[string]string{"user_id": hTargetID},
	)
	h.LockUser(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "cannot_lock_self", decodeLockBody(t, w)["code"])
}

// T-R45k: svc returns ErrCannotLockOwner → 409 cannot_lock_owner
func TestHandler_LockUser_CannotLockOwner(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserLockFakeServicer{
		LockUserFn: func(_ context.Context, _, _ string, _ userlock.LockUserInput) error {
			return platformrbac.ErrCannotLockOwner
		},
	}
	h := userlock.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectLockChi(
		authedLockReq(t, http.MethodPost, "/admin/users/"+hTargetID+"/lock",
			jsonLockBuf(t, map[string]any{"reason": "test"})),
		map[string]string{"user_id": hTargetID},
	)
	h.LockUser(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "cannot_lock_owner", decodeLockBody(t, w)["code"])
}

// T-R45l: svc returns ErrUserNotFound → 404 user_not_found
func TestHandler_LockUser_UserNotFound(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserLockFakeServicer{
		LockUserFn: func(_ context.Context, _, _ string, _ userlock.LockUserInput) error {
			return userlock.ErrUserNotFound
		},
	}
	h := userlock.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectLockChi(
		authedLockReq(t, http.MethodPost, "/admin/users/"+hTargetID+"/lock",
			jsonLockBuf(t, map[string]any{"reason": "test"})),
		map[string]string{"user_id": hTargetID},
	)
	h.LockUser(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "user_not_found", decodeLockBody(t, w)["code"])
}

// T-R45m: no JWT auth → 401 unauthorized
func TestHandler_LockUser_NoAuth(t *testing.T) {
	t.Parallel()
	h := userlock.NewHandler(&rbacsharedtest.UserLockFakeServicer{})
	w := httptest.NewRecorder()
	r := injectLockChi(
		httptest.NewRequest(http.MethodPost, "/admin/users/"+hTargetID+"/lock",
			jsonLockBuf(t, map[string]any{"reason": "test"})),
		map[string]string{"user_id": hTargetID},
	)
	h.LockUser(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "unauthorized", decodeLockBody(t, w)["code"])
}

// T-R45n: svc returns generic error → 500 internal_error
func TestHandler_LockUser_InternalError(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserLockFakeServicer{
		LockUserFn: func(_ context.Context, _, _ string, _ userlock.LockUserInput) error {
			return errors.New("db down")
		},
	}
	h := userlock.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectLockChi(
		authedLockReq(t, http.MethodPost, "/admin/users/"+hTargetID+"/lock",
			jsonLockBuf(t, map[string]any{"reason": "test"})),
		map[string]string{"user_id": hTargetID},
	)
	h.LockUser(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodeLockBody(t, w)["code"])
}

// T-R45o: malformed JSON body → 4xx
func TestHandler_LockUser_MalformedBody(t *testing.T) {
	t.Parallel()
	h := userlock.NewHandler(&rbacsharedtest.UserLockFakeServicer{})
	w := httptest.NewRecorder()
	r := injectLockChi(
		authedLockReq(t, http.MethodPost, "/admin/users/"+hTargetID+"/lock",
			bytes.NewBufferString("not json")),
		map[string]string{"user_id": hTargetID},
	)
	h.LockUser(w, r)
	assert.True(t, w.Code >= 400, "expected 4xx, got %d", w.Code)
}

// ── DELETE /lock ──────────────────────────────────────────────────────────────

// T-R46h: success → 204
func TestHandler_UnlockUser_Success(t *testing.T) {
	t.Parallel()
	h := userlock.NewHandler(&rbacsharedtest.UserLockFakeServicer{})
	w := httptest.NewRecorder()
	r := injectLockChi(
		authedLockReq(t, http.MethodDelete, "/admin/users/"+hTargetID+"/lock", nil),
		map[string]string{"user_id": hTargetID},
	)
	h.UnlockUser(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}

// T-R46i: svc returns ErrUserNotFound → 404 user_not_found
func TestHandler_UnlockUser_UserNotFound(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserLockFakeServicer{
		UnlockUserFn: func(_ context.Context, _, _ string) error {
			return userlock.ErrUserNotFound
		},
	}
	h := userlock.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectLockChi(
		authedLockReq(t, http.MethodDelete, "/admin/users/"+hTargetID+"/lock", nil),
		map[string]string{"user_id": hTargetID},
	)
	h.UnlockUser(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "user_not_found", decodeLockBody(t, w)["code"])
}

// T-R46j: no JWT auth → 401 unauthorized
func TestHandler_UnlockUser_NoAuth(t *testing.T) {
	t.Parallel()
	h := userlock.NewHandler(&rbacsharedtest.UserLockFakeServicer{})
	w := httptest.NewRecorder()
	r := injectLockChi(
		httptest.NewRequest(http.MethodDelete, "/admin/users/"+hTargetID+"/lock", nil),
		map[string]string{"user_id": hTargetID},
	)
	h.UnlockUser(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "unauthorized", decodeLockBody(t, w)["code"])
}

// T-R46k: svc returns generic error → 500 internal_error
func TestHandler_UnlockUser_InternalError(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserLockFakeServicer{
		UnlockUserFn: func(_ context.Context, _, _ string) error {
			return errors.New("db down")
		},
	}
	h := userlock.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectLockChi(
		authedLockReq(t, http.MethodDelete, "/admin/users/"+hTargetID+"/lock", nil),
		map[string]string{"user_id": hTargetID},
	)
	h.UnlockUser(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodeLockBody(t, w)["code"])
}

// ── GET /lock ─────────────────────────────────────────────────────────────────

// T-R47h: success → 200 with user_id in body
func TestHandler_GetLockStatus_Success(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserLockFakeServicer{
		GetLockStatusFn: func(_ context.Context, targetUserID string) (userlock.UserLockStatus, error) {
			return userlock.UserLockStatus{UserID: targetUserID, AdminLocked: false}, nil
		},
	}
	h := userlock.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectLockChi(
		httptest.NewRequest(http.MethodGet, "/admin/users/"+hTargetID+"/lock", nil),
		map[string]string{"user_id": hTargetID},
	)
	h.GetLockStatus(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeLockBody(t, w)
	assert.Equal(t, hTargetID, body["user_id"])
}

// T-R47i: svc returns ErrUserNotFound → 404 user_not_found
func TestHandler_GetLockStatus_UserNotFound(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserLockFakeServicer{
		GetLockStatusFn: func(_ context.Context, _ string) (userlock.UserLockStatus, error) {
			return userlock.UserLockStatus{}, userlock.ErrUserNotFound
		},
	}
	h := userlock.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectLockChi(
		httptest.NewRequest(http.MethodGet, "/admin/users/"+hTargetID+"/lock", nil),
		map[string]string{"user_id": hTargetID},
	)
	h.GetLockStatus(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "user_not_found", decodeLockBody(t, w)["code"])
}

// T-R47j: svc returns generic error → 500 internal_error
func TestHandler_GetLockStatus_InternalError(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.UserLockFakeServicer{
		GetLockStatusFn: func(_ context.Context, _ string) (userlock.UserLockStatus, error) {
			return userlock.UserLockStatus{}, errors.New("db down")
		},
	}
	h := userlock.NewHandler(svc)
	w := httptest.NewRecorder()
	r := injectLockChi(
		httptest.NewRequest(http.MethodGet, "/admin/users/"+hTargetID+"/lock", nil),
		map[string]string{"user_id": hTargetID},
	)
	h.GetLockStatus(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodeLockBody(t, w)["code"])
}
