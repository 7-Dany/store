package owner_test

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

	"github.com/7-Dany/store/backend/internal/domain/rbac/owner"
	rbacshared "github.com/7-Dany/store/backend/internal/domain/rbac/shared"
	rbacsharedtest "github.com/7-Dany/store/backend/internal/domain/rbac/shared/testutil"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testOwnerActorID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testTargetID     = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	testBootSecret   = "super-secret-bootstrap"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func authedOwnerReq(t *testing.T, method, path string, body io.Reader) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, body)
	ctx := token.InjectUserIDForTest(r.Context(), testOwnerActorID)
	return r.WithContext(ctx)
}

func jsonOwnerBuf(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewBuffer(b)
}

func decodeOwnerBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

// newHandler constructs a handler with default deps (caller is owner).
func newHandler(svc owner.Servicer) *owner.Handler {
	return owner.NewHandlerForTest(svc, testBootSecret, &owner.FakeOwnerDeps{})
}

// newHandlerWithDeps constructs a handler with customised deps.
func newHandlerWithDeps(svc owner.Servicer, deps *owner.FakeOwnerDeps) *owner.Handler {
	return owner.NewHandlerForTest(svc, testBootSecret, deps)
}

// sampleAssignResult returns a populated AssignOwnerResult for success tests.
func sampleAssignResult() owner.AssignOwnerResult {
	return owner.AssignOwnerResult{UserID: testOwnerActorID, RoleName: "owner", GrantedAt: time.Now()}
}

// sampleInitiateResult returns a populated InitiateResult for success tests.
func sampleInitiateResult() owner.InitiateResult {
	return owner.InitiateResult{
		TransferID:   "cccccccc-dddd-eeee-ffff-aaaaaaaaaaaa",
		TargetUserID: testTargetID,
		ExpiresAt:    time.Now().Add(48 * time.Hour),
	}
}

// sampleAcceptResult returns a populated AcceptResult for success tests.
func sampleAcceptResult() owner.AcceptResult {
	return owner.AcceptResult{
		NewOwnerID:      testTargetID,
		PreviousOwnerID: testOwnerActorID,
		TransferredAt:   time.Now(),
	}
}

// ── AssignOwner ───────────────────────────────────────────────────────────────

func TestHandler_AssignOwner_NoAuth(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/owner/assign",
		jsonOwnerBuf(t, map[string]any{"secret": testBootSecret}))
	h.AssignOwner(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "unauthorized", decodeOwnerBody(t, w)["code"])
}

func TestHandler_AssignOwner_EmptySecret(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/assign",
		jsonOwnerBuf(t, map[string]any{"secret": ""}))
	h.AssignOwner(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "validation_error", decodeOwnerBody(t, w)["code"])
}

// TestHandler_AssignOwner_WhitespaceSecret verifies that a whitespace-only
// secret is rejected with 422 validation_error.
func TestHandler_AssignOwner_WhitespaceSecret(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/assign",
		jsonOwnerBuf(t, map[string]any{"secret": "   "}))
	h.AssignOwner(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "validation_error", decodeOwnerBody(t, w)["code"])
}

func TestHandler_AssignOwner_WrongSecret(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/assign",
		jsonOwnerBuf(t, map[string]any{"secret": "wrong-secret"}))
	h.AssignOwner(w, r)
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "forbidden", decodeOwnerBody(t, w)["code"])
}

func TestHandler_AssignOwner_OwnerAlreadyExists(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		AssignOwnerFn: func(_ context.Context, _ owner.AssignOwnerInput) (owner.AssignOwnerResult, error) {
			return owner.AssignOwnerResult{}, owner.ErrOwnerAlreadyExists
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/assign",
		jsonOwnerBuf(t, map[string]any{"secret": testBootSecret}))
	h.AssignOwner(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "owner_already_exists", decodeOwnerBody(t, w)["code"])
}

func TestHandler_AssignOwner_UserNotFound(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		AssignOwnerFn: func(_ context.Context, _ owner.AssignOwnerInput) (owner.AssignOwnerResult, error) {
			return owner.AssignOwnerResult{}, rbacshared.ErrUserNotFound
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/assign",
		jsonOwnerBuf(t, map[string]any{"secret": testBootSecret}))
	h.AssignOwner(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "user_not_found", decodeOwnerBody(t, w)["code"])
}

func TestHandler_AssignOwner_UserNotActive(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		AssignOwnerFn: func(_ context.Context, _ owner.AssignOwnerInput) (owner.AssignOwnerResult, error) {
			return owner.AssignOwnerResult{}, owner.ErrUserNotActive
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/assign",
		jsonOwnerBuf(t, map[string]any{"secret": testBootSecret}))
	h.AssignOwner(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "user_not_active", decodeOwnerBody(t, w)["code"])
}

func TestHandler_AssignOwner_EmailNotVerified(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		AssignOwnerFn: func(_ context.Context, _ owner.AssignOwnerInput) (owner.AssignOwnerResult, error) {
			return owner.AssignOwnerResult{}, owner.ErrUserNotVerified
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/assign",
		jsonOwnerBuf(t, map[string]any{"secret": testBootSecret}))
	h.AssignOwner(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "email_not_verified", decodeOwnerBody(t, w)["code"])
}

func TestHandler_AssignOwner_InternalError(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		AssignOwnerFn: func(_ context.Context, _ owner.AssignOwnerInput) (owner.AssignOwnerResult, error) {
			return owner.AssignOwnerResult{}, errors.New("db down")
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/assign",
		jsonOwnerBuf(t, map[string]any{"secret": testBootSecret}))
	h.AssignOwner(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodeOwnerBody(t, w)["code"])
}

// TestHandler_AssignOwner_MalformedBody verifies that a non-JSON body returns 400.
func TestHandler_AssignOwner_MalformedBody(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/assign",
		bytes.NewBufferString("not json"))
	h.AssignOwner(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandler_AssignOwner_OversizedBody verifies that a body exceeding
// MaxBodyBytes returns 413. Per S-3, the body must be raw bytes (not valid JSON)
// so the DecodeJSON drain path fires MaxBytesReader.
func TestHandler_AssignOwner_OversizedBody(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	body := bytes.Repeat([]byte("x"), int(respond.MaxBodyBytes)+1)
	r := authedOwnerReq(t, http.MethodPost, "/owner/assign", bytes.NewReader(body))
	h.AssignOwner(w, r)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestHandler_AssignOwner_Success(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		AssignOwnerFn: func(_ context.Context, _ owner.AssignOwnerInput) (owner.AssignOwnerResult, error) {
			return sampleAssignResult(), nil
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/assign",
		jsonOwnerBuf(t, map[string]any{"secret": testBootSecret}))
	h.AssignOwner(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "owner", decodeOwnerBody(t, w)["role_name"])
}

// ── InitiateTransfer ──────────────────────────────────────────────────────────

func TestHandler_InitiateTransfer_NoAuth(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": testTargetID}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "unauthorized", decodeOwnerBody(t, w)["code"])
}

func TestHandler_InitiateTransfer_NotOwner(t *testing.T) {
	t.Parallel()
	deps := &owner.FakeOwnerDeps{
		IsOwnerFn: func(_ context.Context, _ string) (bool, error) { return false, nil },
	}
	h := newHandlerWithDeps(&rbacsharedtest.OwnerFakeServicer{}, deps)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": testTargetID}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "forbidden", decodeOwnerBody(t, w)["code"])
}

func TestHandler_InitiateTransfer_IsOwnerCheckError(t *testing.T) {
	t.Parallel()
	deps := &owner.FakeOwnerDeps{
		IsOwnerFn: func(_ context.Context, _ string) (bool, error) {
			return false, errors.New("rbac check failed")
		},
	}
	h := newHandlerWithDeps(&rbacsharedtest.OwnerFakeServicer{}, deps)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": testTargetID}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodeOwnerBody(t, w)["code"])
}

func TestHandler_InitiateTransfer_EmptyTargetUserID(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": ""}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "validation_error", decodeOwnerBody(t, w)["code"])
}

func TestHandler_InitiateTransfer_InvalidUUID(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": "not-a-uuid"}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "validation_error", decodeOwnerBody(t, w)["code"])
}

func TestHandler_InitiateTransfer_AlreadyPending(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		InitiateTransferFn: func(_ context.Context, _ owner.InitiateInput) (owner.InitiateResult, string, error) {
			return owner.InitiateResult{}, "", owner.ErrTransferAlreadyPending
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": testTargetID}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "transfer_already_pending", decodeOwnerBody(t, w)["code"])
}

func TestHandler_InitiateTransfer_TargetNotFound(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		InitiateTransferFn: func(_ context.Context, _ owner.InitiateInput) (owner.InitiateResult, string, error) {
			return owner.InitiateResult{}, "", rbacshared.ErrUserNotFound
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": testTargetID}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "user_not_found", decodeOwnerBody(t, w)["code"])
}

func TestHandler_InitiateTransfer_TargetNotActive(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		InitiateTransferFn: func(_ context.Context, _ owner.InitiateInput) (owner.InitiateResult, string, error) {
			return owner.InitiateResult{}, "", owner.ErrUserNotActive
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": testTargetID}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "user_not_active", decodeOwnerBody(t, w)["code"])
}

func TestHandler_InitiateTransfer_TargetEmailNotVerified(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		InitiateTransferFn: func(_ context.Context, _ owner.InitiateInput) (owner.InitiateResult, string, error) {
			return owner.InitiateResult{}, "", owner.ErrUserNotVerified
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": testTargetID}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "email_not_verified", decodeOwnerBody(t, w)["code"])
}

func TestHandler_InitiateTransfer_TargetIsAlreadyOwner(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		InitiateTransferFn: func(_ context.Context, _ owner.InitiateInput) (owner.InitiateResult, string, error) {
			return owner.InitiateResult{}, "", owner.ErrUserIsAlreadyOwner
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": testTargetID}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "user_is_already_owner", decodeOwnerBody(t, w)["code"])
}

func TestHandler_InitiateTransfer_CannotTransferToSelf(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		InitiateTransferFn: func(_ context.Context, _ owner.InitiateInput) (owner.InitiateResult, string, error) {
			return owner.InitiateResult{}, "", owner.ErrCannotTransferToSelf
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": testTargetID}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "cannot_transfer_to_self", decodeOwnerBody(t, w)["code"])
}

func TestHandler_InitiateTransfer_InternalError(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		InitiateTransferFn: func(_ context.Context, _ owner.InitiateInput) (owner.InitiateResult, string, error) {
			return owner.InitiateResult{}, "", errors.New("db down")
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": testTargetID}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodeOwnerBody(t, w)["code"])
}

// TestHandler_InitiateTransfer_OversizedBody verifies that a body exceeding
// MaxBodyBytes returns 413. Per S-3, the body must be raw bytes.
func TestHandler_InitiateTransfer_OversizedBody(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	body := bytes.Repeat([]byte("x"), int(respond.MaxBodyBytes)+1)
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer", bytes.NewReader(body))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestHandler_InitiateTransfer_Success(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		InitiateTransferFn: func(_ context.Context, _ owner.InitiateInput) (owner.InitiateResult, string, error) {
			return sampleInitiateResult(), "raw-token-value", nil
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodPost, "/owner/transfer",
		jsonOwnerBuf(t, map[string]any{"target_user_id": testTargetID}))
	h.InitiateTransfer(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, testTargetID, decodeOwnerBody(t, w)["target_user_id"])
}

// ── AcceptTransfer ────────────────────────────────────────────────────────────

func TestHandler_AcceptTransfer_EmptyToken(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/owner/transfer/accept",
		jsonOwnerBuf(t, map[string]any{"token": ""}))
	h.AcceptTransfer(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "validation_error", decodeOwnerBody(t, w)["code"])
}

// TestHandler_AcceptTransfer_WhitespaceToken verifies that a whitespace-only
// token is rejected with 422 validation_error.
func TestHandler_AcceptTransfer_WhitespaceToken(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/owner/transfer/accept",
		jsonOwnerBuf(t, map[string]any{"token": "   "}))
	h.AcceptTransfer(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "validation_error", decodeOwnerBody(t, w)["code"])
}

func TestHandler_AcceptTransfer_TokenInvalid(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		AcceptTransferFn: func(_ context.Context, _ owner.AcceptInput) (owner.AcceptResult, error) {
			return owner.AcceptResult{}, owner.ErrTransferTokenInvalid
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/owner/transfer/accept",
		jsonOwnerBuf(t, map[string]any{"token": "bad-token"}))
	h.AcceptTransfer(w, r)
	require.Equal(t, http.StatusGone, w.Code)
	assert.Equal(t, "token_invalid", decodeOwnerBody(t, w)["code"])
}

func TestHandler_AcceptTransfer_UserNotEligible(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		AcceptTransferFn: func(_ context.Context, _ owner.AcceptInput) (owner.AcceptResult, error) {
			return owner.AcceptResult{}, owner.ErrUserNotEligible
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/owner/transfer/accept",
		jsonOwnerBuf(t, map[string]any{"token": "some-token"}))
	h.AcceptTransfer(w, r)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Equal(t, "user_not_eligible", decodeOwnerBody(t, w)["code"])
}

func TestHandler_AcceptTransfer_InitiatorNotOwner(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		AcceptTransferFn: func(_ context.Context, _ owner.AcceptInput) (owner.AcceptResult, error) {
			return owner.AcceptResult{}, owner.ErrInitiatorNotOwner
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/owner/transfer/accept",
		jsonOwnerBuf(t, map[string]any{"token": "some-token"}))
	h.AcceptTransfer(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "initiator_not_owner", decodeOwnerBody(t, w)["code"])
}

func TestHandler_AcceptTransfer_InternalError(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		AcceptTransferFn: func(_ context.Context, _ owner.AcceptInput) (owner.AcceptResult, error) {
			return owner.AcceptResult{}, errors.New("db down")
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/owner/transfer/accept",
		jsonOwnerBuf(t, map[string]any{"token": "some-token"}))
	h.AcceptTransfer(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodeOwnerBody(t, w)["code"])
}

// TestHandler_AcceptTransfer_MalformedBody verifies that a non-JSON body returns 400.
func TestHandler_AcceptTransfer_MalformedBody(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/owner/transfer/accept",
		bytes.NewBufferString("not json"))
	h.AcceptTransfer(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandler_AcceptTransfer_OversizedBody verifies that a body exceeding
// MaxBodyBytes returns 413. Per S-3, the body must be raw bytes.
func TestHandler_AcceptTransfer_OversizedBody(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	body := bytes.Repeat([]byte("x"), int(respond.MaxBodyBytes)+1)
	r := httptest.NewRequest(http.MethodPost, "/owner/transfer/accept", bytes.NewReader(body))
	h.AcceptTransfer(w, r)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

// TestHandler_AcceptTransfer_NoAuthAllowed verifies that AcceptTransfer does
// not require a JWT: a request with no injected user context must reach the
// service and not return 401.
func TestHandler_AcceptTransfer_NoAuthAllowed(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		AcceptTransferFn: func(_ context.Context, _ owner.AcceptInput) (owner.AcceptResult, error) {
			return sampleAcceptResult(), nil
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	// Deliberately use httptest.NewRequest (no injected JWT context).
	r := httptest.NewRequest(http.MethodPost, "/owner/transfer/accept",
		jsonOwnerBuf(t, map[string]any{"token": "valid-token"}))
	h.AcceptTransfer(w, r)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHandler_AcceptTransfer_Success(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		AcceptTransferFn: func(_ context.Context, _ owner.AcceptInput) (owner.AcceptResult, error) {
			return sampleAcceptResult(), nil
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/owner/transfer/accept",
		jsonOwnerBuf(t, map[string]any{"token": "valid-token"}))
	h.AcceptTransfer(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeOwnerBody(t, w)
	assert.Equal(t, testTargetID, body["new_owner_id"])
	assert.Equal(t, testOwnerActorID, body["previous_owner_id"])
}

// ── CancelTransfer ────────────────────────────────────────────────────────────

func TestHandler_CancelTransfer_NoAuth(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/owner/transfer", nil)
	h.CancelTransfer(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "unauthorized", decodeOwnerBody(t, w)["code"])
}

func TestHandler_CancelTransfer_NotOwner(t *testing.T) {
	t.Parallel()
	deps := &owner.FakeOwnerDeps{
		IsOwnerFn: func(_ context.Context, _ string) (bool, error) { return false, nil },
	}
	h := newHandlerWithDeps(&rbacsharedtest.OwnerFakeServicer{}, deps)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodDelete, "/owner/transfer", nil)
	h.CancelTransfer(w, r)
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "forbidden", decodeOwnerBody(t, w)["code"])
}

func TestHandler_CancelTransfer_IsOwnerCheckError(t *testing.T) {
	t.Parallel()
	deps := &owner.FakeOwnerDeps{
		IsOwnerFn: func(_ context.Context, _ string) (bool, error) {
			return false, errors.New("rbac check failed")
		},
	}
	h := newHandlerWithDeps(&rbacsharedtest.OwnerFakeServicer{}, deps)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodDelete, "/owner/transfer", nil)
	h.CancelTransfer(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodeOwnerBody(t, w)["code"])
}

func TestHandler_CancelTransfer_NoPendingTransfer(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		CancelTransferFn: func(_ context.Context, _ [16]byte, _, _ string) error {
			return owner.ErrNoPendingTransfer
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodDelete, "/owner/transfer", nil)
	h.CancelTransfer(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "no_pending_transfer", decodeOwnerBody(t, w)["code"])
}

func TestHandler_CancelTransfer_InternalError(t *testing.T) {
	t.Parallel()
	svc := &rbacsharedtest.OwnerFakeServicer{
		CancelTransferFn: func(_ context.Context, _ [16]byte, _, _ string) error {
			return errors.New("db down")
		},
	}
	h := newHandler(svc)
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodDelete, "/owner/transfer", nil)
	h.CancelTransfer(w, r)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal_error", decodeOwnerBody(t, w)["code"])
}

func TestHandler_CancelTransfer_Success(t *testing.T) {
	t.Parallel()
	h := newHandler(&rbacsharedtest.OwnerFakeServicer{})
	w := httptest.NewRecorder()
	r := authedOwnerReq(t, http.MethodDelete, "/owner/transfer", nil)
	h.CancelTransfer(w, r)
	require.Equal(t, http.StatusNoContent, w.Code)
}
