package watch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/audit"
	bitcoinshared "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared"
	"github.com/7-Dany/store/backend/internal/domain/bitcoin/watch"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

type fakeServicer struct {
	createWatchFn   func(ctx context.Context, in watch.CreateWatchInput) (watch.Watch, error)
	getWatchFn      func(ctx context.Context, in watch.GetWatchInput) (watch.Watch, error)
	listWatchesFn   func(ctx context.Context, in watch.ListWatchesInput) ([]watch.Watch, error)
	updateWatchFn   func(ctx context.Context, in watch.UpdateWatchInput) (watch.Watch, error)
	deleteWatchFn   func(ctx context.Context, in watch.DeleteWatchInput) error
	writeAuditLogFn func(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error
}

var _ watch.Servicer = (*fakeServicer)(nil)

func (f *fakeServicer) CreateWatch(ctx context.Context, in watch.CreateWatchInput) (watch.Watch, error) {
	if f.createWatchFn != nil {
		return f.createWatchFn(ctx, in)
	}
	return watch.Watch{}, nil
}

func (f *fakeServicer) GetWatch(ctx context.Context, in watch.GetWatchInput) (watch.Watch, error) {
	if f.getWatchFn != nil {
		return f.getWatchFn(ctx, in)
	}
	return watch.Watch{}, nil
}

func (f *fakeServicer) ListWatches(ctx context.Context, in watch.ListWatchesInput) ([]watch.Watch, error) {
	if f.listWatchesFn != nil {
		return f.listWatchesFn(ctx, in)
	}
	return nil, nil
}

func (f *fakeServicer) UpdateWatch(ctx context.Context, in watch.UpdateWatchInput) (watch.Watch, error) {
	if f.updateWatchFn != nil {
		return f.updateWatchFn(ctx, in)
	}
	return watch.Watch{}, nil
}

func (f *fakeServicer) DeleteWatch(ctx context.Context, in watch.DeleteWatchInput) error {
	if f.deleteWatchFn != nil {
		return f.deleteWatchFn(ctx, in)
	}
	return nil
}

func (f *fakeServicer) WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error {
	if f.writeAuditLogFn != nil {
		return f.writeAuditLogFn(ctx, event, userID, sourceIP, metadata)
	}
	return nil
}

type spyRecorder struct {
	bitcoinshared.NoopBitcoinRecorder
	reasons []string
}

func (r *spyRecorder) OnWatchRejected(reason string) {
	r.reasons = append(r.reasons, reason)
}

func withAuth(r *http.Request, userID string) *http.Request {
	return r.WithContext(token.InjectUserIDForTest(r.Context(), userID))
}

func newHandler(svc watch.Servicer, rec bitcoinshared.BitcoinRecorder) *watch.Handler {
	return watch.NewHandler(svc, rec, "testnet4", "test-hmac-key")
}

func TestCreateWatch_InvalidWatchType(t *testing.T) {
	t.Parallel()

	body := []byte(`{"network":"testnet4","watch_type":"weird"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/bitcoin/watch", bytes.NewReader(body)), "user-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	newHandler(&fakeServicer{}, bitcoinshared.NoopBitcoinRecorder{}).CreateWatch(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "invalid_watch_type")
}

func TestCreateWatch_InvalidAddress(t *testing.T) {
	t.Parallel()

	rec := &spyRecorder{}
	body := []byte(`{"network":"testnet4","watch_type":"address","address":"bad-address"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/bitcoin/watch", bytes.NewReader(body)), "user-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	newHandler(&fakeServicer{}, rec).CreateWatch(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "invalid_address")
	require.Len(t, rec.reasons, 1)
	assert.Equal(t, "invalid_address", rec.reasons[0])
}

func TestCreateWatch_LimitExceeded(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		createWatchFn: func(_ context.Context, _ watch.CreateWatchInput) (watch.Watch, error) {
			return watch.Watch{}, bitcoinshared.ErrWatchLimitExceeded
		},
	}
	rec := &spyRecorder{}
	body := []byte(`{"network":"testnet4","watch_type":"transaction","txid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/bitcoin/watch", bytes.NewReader(body)), "user-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	newHandler(svc, rec).CreateWatch(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "watch_limit_exceeded")
	require.Len(t, rec.reasons, 1)
	assert.Equal(t, "limit_exceeded", rec.reasons[0])
}

func TestCreateWatch_Success(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		createWatchFn: func(_ context.Context, in watch.CreateWatchInput) (watch.Watch, error) {
			return watch.Watch{
				ID:        11,
				Network:   in.Network,
				WatchType: in.WatchType,
				TxID:      in.TxID,
				Status:    watch.WatchStatusActive,
			}, nil
		},
	}
	body := []byte(`{"network":"testnet4","watch_type":"transaction","txid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/bitcoin/watch", bytes.NewReader(body)), "user-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	newHandler(svc, bitcoinshared.NoopBitcoinRecorder{}).CreateWatch(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(11), resp["id"])
	assert.Equal(t, "transaction", resp["watch_type"])
}

func TestGetWatch_NotFound(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		getWatchFn: func(_ context.Context, _ watch.GetWatchInput) (watch.Watch, error) {
			return watch.Watch{}, watch.ErrWatchNotFound
		},
	}
	req := withAuth(httptest.NewRequest(http.MethodGet, "/bitcoin/watch/9", nil), "user-1")
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "9")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	w := httptest.NewRecorder()

	newHandler(svc, bitcoinshared.NoopBitcoinRecorder{}).GetWatch(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListWatches_Success(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		listWatchesFn: func(_ context.Context, _ watch.ListWatchesInput) ([]watch.Watch, error) {
			addr := "tb1qexampleaddress0000000000000000000000000"
			return []watch.Watch{{ID: 1, Network: "testnet4", WatchType: watch.WatchTypeAddress, Address: &addr, Status: watch.WatchStatusActive}}, nil
		},
	}
	req := withAuth(httptest.NewRequest(http.MethodGet, "/bitcoin/watch?watch_type=address", nil), "user-1")
	w := httptest.NewRecorder()

	newHandler(svc, bitcoinshared.NoopBitcoinRecorder{}).ListWatches(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	items := resp["items"].([]any)
	require.Len(t, items, 1)
}

func assertJSONCode(t *testing.T, body []byte, want string) {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	assert.Equal(t, want, payload["code"])
}
