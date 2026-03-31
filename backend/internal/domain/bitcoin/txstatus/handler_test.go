// Package txstatus_test exercises the txstatus HTTP handler through its public API.
package txstatus_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/domain/bitcoin/txstatus"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

type fakeSvc struct {
	createTrackedFn       func(ctx context.Context, in txstatus.CreateTrackedTxStatusInput) (txstatus.TrackedTxStatus, error)
	getTrackedFn          func(ctx context.Context, in txstatus.GetTrackedTxStatusInput) (txstatus.TrackedTxStatus, error)
	listTrackedFn         func(ctx context.Context, in txstatus.ListTrackedTxStatusesInput) ([]txstatus.TrackedTxStatus, error)
	updateTrackedFn       func(ctx context.Context, in txstatus.UpdateTrackedTxStatusInput) (txstatus.TrackedTxStatus, error)
	deleteTrackedStatusFn func(ctx context.Context, in txstatus.DeleteTrackedTxStatusInput) error
}

var _ txstatus.Servicer = (*fakeSvc)(nil)

func (f *fakeSvc) CreateTrackedTxStatus(ctx context.Context, in txstatus.CreateTrackedTxStatusInput) (txstatus.TrackedTxStatus, error) {
	if f.createTrackedFn != nil {
		return f.createTrackedFn(ctx, in)
	}
	panic("fakeSvc.CreateTrackedTxStatus: createTrackedFn not set")
}

func (f *fakeSvc) GetTrackedTxStatus(ctx context.Context, in txstatus.GetTrackedTxStatusInput) (txstatus.TrackedTxStatus, error) {
	if f.getTrackedFn != nil {
		return f.getTrackedFn(ctx, in)
	}
	panic("fakeSvc.GetTrackedTxStatus: getTrackedFn not set")
}

func (f *fakeSvc) ListTrackedTxStatuses(ctx context.Context, in txstatus.ListTrackedTxStatusesInput) ([]txstatus.TrackedTxStatus, error) {
	if f.listTrackedFn != nil {
		return f.listTrackedFn(ctx, in)
	}
	panic("fakeSvc.ListTrackedTxStatuses: listTrackedFn not set")
}

func (f *fakeSvc) UpdateTrackedTxStatus(ctx context.Context, in txstatus.UpdateTrackedTxStatusInput) (txstatus.TrackedTxStatus, error) {
	if f.updateTrackedFn != nil {
		return f.updateTrackedFn(ctx, in)
	}
	panic("fakeSvc.UpdateTrackedTxStatus: updateTrackedFn not set")
}

func (f *fakeSvc) DeleteTrackedTxStatus(ctx context.Context, in txstatus.DeleteTrackedTxStatusInput) error {
	if f.deleteTrackedStatusFn != nil {
		return f.deleteTrackedStatusFn(ctx, in)
	}
	panic("fakeSvc.DeleteTrackedTxStatus: deleteTrackedStatusFn not set")
}

func validTxID() string { return strings.Repeat("a", 64) }

func withAuthCtx(r *http.Request, userID string) *http.Request {
	return r.WithContext(token.InjectUserIDForTest(r.Context(), userID))
}

func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func assertJSONCode(t *testing.T, body []byte, wantCode string) {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m), "body is not valid JSON: %s", body)
	assert.Equal(t, wantCode, m["code"], "JSON 'code' mismatch; body: %s", body)
}

func newHandler(svc txstatus.Servicer) *txstatus.Handler {
	return txstatus.NewHandler(svc, "testnet4")
}

const trackedTestUserID = "11111111-1111-1111-1111-111111111111"

func trackedFixture() txstatus.TrackedTxStatus {
	now := time.Date(2026, time.March, 30, 17, 0, 0, 0, time.UTC)
	address := "tb1qfc74wvs6wnz3p2twgza26vukqct4emt2v47xwq"
	blockHash := strings.Repeat("b", 64)
	blockHeight := int64(120)
	return txstatus.TrackedTxStatus{
		ID:              7,
		UserID:          trackedTestUserID,
		Network:         "testnet4",
		TrackingMode:    txstatus.TrackingModeTxID,
		Address:         &address,
		TxID:            validTxID(),
		Status:          txstatus.TxStatusConfirmed,
		Confirmations:   2,
		AmountSat:       1234,
		FeeRateSatVByte: 5.5,
		FirstSeenAt:     now.Add(-10 * time.Minute),
		LastSeenAt:      now.Add(-5 * time.Minute),
		ConfirmedAt:     &now,
		BlockHash:       &blockHash,
		BlockHeight:     &blockHeight,
		CreatedAt:       now.Add(-10 * time.Minute),
		UpdatedAt:       now,
	}
}

func decodeJSONMap(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	return payload
}

func TestIsHex(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  bool
	}{
		{"", true},
		{"0123456789abcdef", true},
		{strings.Repeat("a", 64), true},
		{"ABCDEF", false},
		{"abcdefg", false},
		{"abc def", false},
		{"abcdef\x00", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, txstatus.IsHex(tc.input))
		})
	}
}

func TestCreateTrackedTxStatus_Success_Returns201(t *testing.T) {
	t.Parallel()

	var got txstatus.CreateTrackedTxStatusInput
	h := newHandler(&fakeSvc{
		createTrackedFn: func(_ context.Context, in txstatus.CreateTrackedTxStatusInput) (txstatus.TrackedTxStatus, error) {
			got = in
			return trackedFixture(), nil
		},
	})

	req := withAuthCtx(httptest.NewRequest(http.MethodPost, "/bitcoin/tx",
		strings.NewReader(`{"network":"testnet4","txid":"`+strings.Repeat("A", 64)+`","address":"tb1qfc74wvs6wnz3p2twgza26vukqct4emt2v47xwq"}`)), trackedTestUserID)
	rec := httptest.NewRecorder()

	h.CreateTrackedTxStatus(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, trackedTestUserID, got.UserID)
	assert.Equal(t, "testnet4", got.Network)
	assert.Equal(t, validTxID(), got.TxID)
	require.NotNil(t, got.Address)
	assert.Equal(t, "tb1qfc74wvs6wnz3p2twgza26vukqct4emt2v47xwq", *got.Address)
	assert.Equal(t, float64(7), decodeJSONMap(t, rec.Body.Bytes())["id"])
}

func TestCreateTrackedTxStatus_NetworkMismatch_Returns400(t *testing.T) {
	t.Parallel()

	req := withAuthCtx(httptest.NewRequest(http.MethodPost, "/bitcoin/tx",
		strings.NewReader(`{"network":"mainnet","txid":"`+validTxID()+`"}`)), trackedTestUserID)
	rec := httptest.NewRecorder()

	newHandler(&fakeSvc{}).CreateTrackedTxStatus(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assertJSONCode(t, rec.Body.Bytes(), "network_mismatch")
}

func TestCreateTrackedTxStatus_InvalidAddress_Returns400(t *testing.T) {
	t.Parallel()

	req := withAuthCtx(httptest.NewRequest(http.MethodPost, "/bitcoin/tx",
		strings.NewReader(`{"network":"testnet4","txid":"`+validTxID()+`","address":"bad-address"}`)), trackedTestUserID)
	rec := httptest.NewRecorder()

	newHandler(&fakeSvc{}).CreateTrackedTxStatus(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assertJSONCode(t, rec.Body.Bytes(), "invalid_address")
}

func TestCreateTrackedTxStatus_Conflict_Returns409(t *testing.T) {
	t.Parallel()

	h := newHandler(&fakeSvc{
		createTrackedFn: func(_ context.Context, _ txstatus.CreateTrackedTxStatusInput) (txstatus.TrackedTxStatus, error) {
			return txstatus.TrackedTxStatus{}, txstatus.ErrTrackedTxStatusExists
		},
	})

	req := withAuthCtx(httptest.NewRequest(http.MethodPost, "/bitcoin/tx",
		strings.NewReader(`{"network":"testnet4","txid":"`+validTxID()+`"}`)), trackedTestUserID)
	rec := httptest.NewRecorder()

	h.CreateTrackedTxStatus(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assertJSONCode(t, rec.Body.Bytes(), "tx_status_exists")
}

func TestCreateTrackedTxStatus_InternalError_Returns500(t *testing.T) {
	t.Parallel()

	h := newHandler(&fakeSvc{
		createTrackedFn: func(_ context.Context, _ txstatus.CreateTrackedTxStatusInput) (txstatus.TrackedTxStatus, error) {
			return txstatus.TrackedTxStatus{}, errors.New("boom")
		},
	})

	req := withAuthCtx(httptest.NewRequest(http.MethodPost, "/bitcoin/tx",
		strings.NewReader(`{"network":"testnet4","txid":"`+validTxID()+`"}`)), trackedTestUserID)
	rec := httptest.NewRecorder()

	h.CreateTrackedTxStatus(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assertJSONCode(t, rec.Body.Bytes(), "internal_error")
}

func TestGetTrackedTxStatus_InvalidID_Returns400(t *testing.T) {
	t.Parallel()

	req := withAuthCtx(httptest.NewRequest(http.MethodGet, "/bitcoin/tx/abc", nil), trackedTestUserID)
	req = withChiParam(req, "id", "abc")
	rec := httptest.NewRecorder()

	newHandler(&fakeSvc{}).GetTrackedTxStatus(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assertJSONCode(t, rec.Body.Bytes(), "invalid_id")
}

func TestGetTrackedTxStatus_NotFound_Returns404(t *testing.T) {
	t.Parallel()

	h := newHandler(&fakeSvc{
		getTrackedFn: func(_ context.Context, _ txstatus.GetTrackedTxStatusInput) (txstatus.TrackedTxStatus, error) {
			return txstatus.TrackedTxStatus{}, txstatus.ErrTrackedTxStatusNotFound
		},
	})

	req := withAuthCtx(httptest.NewRequest(http.MethodGet, "/bitcoin/tx/7", nil), trackedTestUserID)
	req = withChiParam(req, "id", "7")
	rec := httptest.NewRecorder()

	h.GetTrackedTxStatus(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assertJSONCode(t, rec.Body.Bytes(), "tx_status_not_found")
}

func TestListTrackedTxStatuses_PassesFiltersToService(t *testing.T) {
	t.Parallel()

	var got txstatus.ListTrackedTxStatusesInput
	h := newHandler(&fakeSvc{
		listTrackedFn: func(_ context.Context, in txstatus.ListTrackedTxStatusesInput) ([]txstatus.TrackedTxStatus, error) {
			got = in
			return []txstatus.TrackedTxStatus{trackedFixture()}, nil
		},
	})

	url := "/bitcoin/tx?address=tb1qfc74wvs6wnz3p2twgza26vukqct4emt2v47xwq&txid=" + validTxID() + "&tracking_mode=txid&limit=5"
	req := withAuthCtx(httptest.NewRequest(http.MethodGet, url, nil), trackedTestUserID)
	rec := httptest.NewRecorder()

	h.ListTrackedTxStatuses(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, trackedTestUserID, got.UserID)
	assert.Equal(t, "testnet4", got.Network)
	assert.Equal(t, "tb1qfc74wvs6wnz3p2twgza26vukqct4emt2v47xwq", got.Address)
	assert.Equal(t, validTxID(), got.TxID)
	assert.Equal(t, "txid", got.TrackingMode)
	assert.Equal(t, 6, got.Limit)
	assert.Nil(t, got.BeforeSortTime)
	assert.Zero(t, got.BeforeID)
}

func TestListTrackedTxStatuses_InvalidTrackingMode_Returns400(t *testing.T) {
	t.Parallel()

	req := withAuthCtx(httptest.NewRequest(http.MethodGet, "/bitcoin/tx?tracking_mode=nope", nil), trackedTestUserID)
	rec := httptest.NewRecorder()

	newHandler(&fakeSvc{}).ListTrackedTxStatuses(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assertJSONCode(t, rec.Body.Bytes(), "invalid_tracking_mode")
}

func TestUpdateTrackedTxStatus_WatchManagedConflict_Returns409(t *testing.T) {
	t.Parallel()

	h := newHandler(&fakeSvc{
		updateTrackedFn: func(_ context.Context, _ txstatus.UpdateTrackedTxStatusInput) (txstatus.TrackedTxStatus, error) {
			return txstatus.TrackedTxStatus{}, txstatus.ErrWatchManagedTrackedTxStatus
		},
	})

	req := withAuthCtx(httptest.NewRequest(http.MethodPut, "/bitcoin/tx/7",
		strings.NewReader(`{"address":"tb1qfc74wvs6wnz3p2twgza26vukqct4emt2v47xwq"}`)), trackedTestUserID)
	req = withChiParam(req, "id", "7")
	rec := httptest.NewRecorder()

	h.UpdateTrackedTxStatus(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assertJSONCode(t, rec.Body.Bytes(), "watch_managed_record")
}

func TestUpdateTrackedTxStatus_Success_Returns200(t *testing.T) {
	t.Parallel()

	var got txstatus.UpdateTrackedTxStatusInput
	h := newHandler(&fakeSvc{
		updateTrackedFn: func(_ context.Context, in txstatus.UpdateTrackedTxStatusInput) (txstatus.TrackedTxStatus, error) {
			got = in
			return trackedFixture(), nil
		},
	})

	req := withAuthCtx(httptest.NewRequest(http.MethodPut, "/bitcoin/tx/7",
		strings.NewReader(`{"address":"tb1qfc74wvs6wnz3p2twgza26vukqct4emt2v47xwq"}`)), trackedTestUserID)
	req = withChiParam(req, "id", "7")
	rec := httptest.NewRecorder()

	h.UpdateTrackedTxStatus(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, int64(7), got.ID)
	assert.Equal(t, trackedTestUserID, got.UserID)
	require.NotNil(t, got.Address)
	assert.Equal(t, "tb1qfc74wvs6wnz3p2twgza26vukqct4emt2v47xwq", *got.Address)
}

func TestDeleteTrackedTxStatus_NotFound_Returns404(t *testing.T) {
	t.Parallel()

	h := newHandler(&fakeSvc{
		deleteTrackedStatusFn: func(_ context.Context, _ txstatus.DeleteTrackedTxStatusInput) error {
			return txstatus.ErrTrackedTxStatusNotFound
		},
	})

	req := withAuthCtx(httptest.NewRequest(http.MethodDelete, "/bitcoin/tx/7", nil), trackedTestUserID)
	req = withChiParam(req, "id", "7")
	rec := httptest.NewRecorder()

	h.DeleteTrackedTxStatus(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assertJSONCode(t, rec.Body.Bytes(), "tx_status_not_found")
}

func TestDeleteTrackedTxStatus_Success_Returns204(t *testing.T) {
	t.Parallel()

	var got txstatus.DeleteTrackedTxStatusInput
	h := newHandler(&fakeSvc{
		deleteTrackedStatusFn: func(_ context.Context, in txstatus.DeleteTrackedTxStatusInput) error {
			got = in
			return nil
		},
	})

	req := withAuthCtx(httptest.NewRequest(http.MethodDelete, "/bitcoin/tx/7", nil), trackedTestUserID)
	req = withChiParam(req, "id", "7")
	rec := httptest.NewRecorder()

	h.DeleteTrackedTxStatus(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, int64(7), got.ID)
	assert.Equal(t, trackedTestUserID, got.UserID)
}
