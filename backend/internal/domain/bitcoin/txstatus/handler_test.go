package txstatus_test

// Black-box handler tests — package txstatus_test.
// Uses httptest recorders, a fakeSvc implementing Servicer, and chi route context
// injection for path parameters. Auth is provided via token.InjectUserIDForTest.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/domain/bitcoin/txstatus"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// ── fakeSvc ───────────────────────────────────────────────────────────────────

// fakeSvc is a minimal implementation of txstatus.Servicer for handler tests.
// Each method panics when its Fn field is nil so that tests which reach the
// service without configuring the function fail loudly rather than silently.
type fakeSvc struct {
	getTxStatusFn      func(ctx context.Context, in txstatus.GetTxStatusInput) (txstatus.TxStatusResult, error)
	getTxStatusBatchFn func(ctx context.Context, in txstatus.GetTxStatusBatchInput) (map[string]txstatus.TxStatusResult, error)
}

// compile-time check that *fakeSvc satisfies txstatus.Servicer.
var _ txstatus.Servicer = (*fakeSvc)(nil)

func (f *fakeSvc) GetTxStatus(ctx context.Context, in txstatus.GetTxStatusInput) (txstatus.TxStatusResult, error) {
	if f.getTxStatusFn != nil {
		return f.getTxStatusFn(ctx, in)
	}
	panic("fakeSvc.GetTxStatus: getTxStatusFn not set — configure it for this test")
}

func (f *fakeSvc) GetTxStatusBatch(ctx context.Context, in txstatus.GetTxStatusBatchInput) (map[string]txstatus.TxStatusResult, error) {
	if f.getTxStatusBatchFn != nil {
		return f.getTxStatusBatchFn(ctx, in)
	}
	panic("fakeSvc.GetTxStatusBatch: getTxStatusBatchFn not set — configure it for this test")
}

// ── test helpers ──────────────────────────────────────────────────────────────

// validTxID returns a 64-character all-'a' hex string.
func validTxID() string { return strings.Repeat("a", 64) }

// txIDWith returns a 64-character string composed entirely of char.
func txIDWith(char byte) string { return strings.Repeat(string([]byte{char}), 64) }

// withAuthCtx injects a userID into the request context via the test helper.
func withAuthCtx(r *http.Request, userID string) *http.Request {
	return r.WithContext(token.InjectUserIDForTest(r.Context(), userID))
}

// withChiParam injects a chi route-context path parameter into the request.
func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// doSingle fires the GetTxStatus handler and returns the recorder.
func doSingle(h *txstatus.Handler, txid string, authed bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/tx/"+txid+"/status", nil)
	if authed {
		r = withAuthCtx(r, "user-123")
	}
	r = withChiParam(r, "txid", txid)
	w := httptest.NewRecorder()
	h.GetTxStatus(w, r)
	return w
}

// doBatch fires the GetTxStatusBatch handler with ?ids=<ids> and returns the recorder.
func doBatch(h *txstatus.Handler, ids string, authed bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/tx/status?ids="+ids, nil)
	if authed {
		r = withAuthCtx(r, "user-123")
	}
	w := httptest.NewRecorder()
	h.GetTxStatusBatch(w, r)
	return w
}

// doBatchNoIds fires the GetTxStatusBatch handler with no ?ids param at all.
func doBatchNoIds(h *txstatus.Handler) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/tx/status", nil)
	r = withAuthCtx(r, "user-123")
	w := httptest.NewRecorder()
	h.GetTxStatusBatch(w, r)
	return w
}

// assertJSONCode unmarshals body and asserts the "code" field matches wantCode.
func assertJSONCode(t *testing.T, body []byte, wantCode string) {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m), "body is not valid JSON: %s", body)
	assert.Equal(t, wantCode, m["code"], "JSON 'code' mismatch; body: %s", body)
}

func assertContentTypeJSON(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	ct := w.Header().Get("Content-Type")
	assert.Contains(t, ct, "application/json", "Content-Type must be application/json")
}

// ── single-tx handler tests ───────────────────────────────────────────────────

func TestSingleTxStatus_MissingAuth_Returns401(t *testing.T) {
	t.Parallel()

	h := txstatus.NewHandler(&fakeSvc{})
	w := doSingle(h, validTxID(), false /* no auth */)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "unauthorized")
	assertContentTypeJSON(t, w)
}

func TestSingleTxStatus_InvalidHex_Returns400(t *testing.T) {
	t.Parallel()

	// 64 chars containing 'g' — not a valid hex character.
	h := txstatus.NewHandler(&fakeSvc{})
	w := doSingle(h, txIDWith('g'), true)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "invalid_txid")
}

func TestSingleTxStatus_TooShort_Returns400(t *testing.T) {
	t.Parallel()

	h := txstatus.NewHandler(&fakeSvc{})
	w := doSingle(h, strings.Repeat("a", 63), true)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "invalid_txid")
}

func TestSingleTxStatus_TooLong_Returns400(t *testing.T) {
	t.Parallel()

	h := txstatus.NewHandler(&fakeSvc{})
	w := doSingle(h, strings.Repeat("a", 65), true)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "invalid_txid")
}

// TestSingleTxStatus_UppercaseHex_Returns200 verifies that uppercase txids are
// normalised to lowercase before validation, so callers may submit txids in any
// case (block explorers commonly return uppercase).
func TestSingleTxStatus_UppercaseHex_Returns200(t *testing.T) {
	t.Parallel()
	svc := &fakeSvc{
		getTxStatusFn: func(_ context.Context, _ txstatus.GetTxStatusInput) (txstatus.TxStatusResult, error) {
			return txstatus.TxStatusResult{Status: txstatus.TxStatusConfirmed, Confirmations: 1, BlockHeight: 800000}, nil
		},
	}
	h := txstatus.NewHandler(svc)
	// 64-char uppercase hex — valid after normalisation.
	w := doSingle(h, strings.Repeat("A", 64), true)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSingleTxStatus_Confirmed_Returns200(t *testing.T) {
	t.Parallel()

	svc := &fakeSvc{
		getTxStatusFn: func(_ context.Context, _ txstatus.GetTxStatusInput) (txstatus.TxStatusResult, error) {
			return txstatus.TxStatusResult{Status: txstatus.TxStatusConfirmed, Confirmations: 6, BlockHeight: 800000}, nil
		},
	}
	h := txstatus.NewHandler(svc)
	w := doSingle(h, validTxID(), true)

	assert.Equal(t, http.StatusOK, w.Code)
	assertContentTypeJSON(t, w)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "confirmed", resp["status"])
	assert.Equal(t, float64(6), resp["confirmations"])
	assert.Equal(t, float64(800000), resp["block_height"])
}

func TestSingleTxStatus_Mempool_IncludesZeroConfirmations(t *testing.T) {
	t.Parallel()
	svc := &fakeSvc{
		getTxStatusFn: func(_ context.Context, _ txstatus.GetTxStatusInput) (txstatus.TxStatusResult, error) {
			return txstatus.TxStatusResult{Status: txstatus.TxStatusMempool}, nil
		},
	}
	h := txstatus.NewHandler(svc)
	w := doSingle(h, validTxID(), true)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "mempool", resp["status"])
	// Confirmations == 0 must be present in the response (meaningful for mempool txs).
	conf, ok := resp["confirmations"]
	require.True(t, ok, "confirmations field must be present for mempool status")
	assert.Equal(t, float64(0), conf)
	// BlockHeight must be absent for mempool transactions.
	_, hasHeight := resp["block_height"]
	assert.False(t, hasHeight, "block_height must be absent for mempool status")
}

func TestSingleTxStatus_NotFound_Returns200(t *testing.T) {
	t.Parallel()

	svc := &fakeSvc{
		getTxStatusFn: func(_ context.Context, _ txstatus.GetTxStatusInput) (txstatus.TxStatusResult, error) {
			return txstatus.TxStatusResult{Status: txstatus.TxStatusNotFound}, nil
		},
	}
	h := txstatus.NewHandler(svc)
	w := doSingle(h, validTxID(), true)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "not_found", resp["status"])
	_, hasConf := resp["confirmations"]
	assert.False(t, hasConf, "confirmations must be absent for not_found status")
}

func TestSingleTxStatus_RPCDown_Returns502(t *testing.T) {
	t.Parallel()

	svc := &fakeSvc{
		getTxStatusFn: func(_ context.Context, _ txstatus.GetTxStatusInput) (txstatus.TxStatusResult, error) {
			return txstatus.TxStatusResult{}, txstatus.ErrRPCUnavailable
		},
	}
	h := txstatus.NewHandler(svc)
	w := doSingle(h, validTxID(), true)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "service_unavailable")
}

func TestSingleTxStatus_InternalError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &fakeSvc{
		getTxStatusFn: func(_ context.Context, _ txstatus.GetTxStatusInput) (txstatus.TxStatusResult, error) {
			return txstatus.TxStatusResult{}, errors.New("unexpected internal failure")
		},
	}
	h := txstatus.NewHandler(svc)
	w := doSingle(h, validTxID(), true)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "internal_error")
}

// ── isHex white-box tests ─────────────────────────────────────────────────────

func TestIsHex(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  bool
	}{
		{"", true},
		{"0123456789abcdef", true},
		{strings.Repeat("a", 64), true},
		{"ABCDEF", false},         // uppercase rejected — normalise before calling isHex
		{"abcdefg", false},        // 'g' is not a hex digit
		{"abc def", false},        // space is not a hex digit
		{"abcdef\x00", false},    // null byte is not a hex digit
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, txstatus.IsHex(tc.input))
		})
	}
}

// ── batch handler tests ───────────────────────────────────────────────────────

func TestBatchTxStatus_MissingAuth_Returns401(t *testing.T) {
	t.Parallel()

	h := txstatus.NewHandler(&fakeSvc{})
	w := doBatch(h, validTxID(), false /* no auth */)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "unauthorized")
}

func TestBatchTxStatus_MissingIdsParam_Returns400(t *testing.T) {
	t.Parallel()

	h := txstatus.NewHandler(&fakeSvc{})
	w := doBatchNoIds(h)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "missing_ids")
}

func TestBatchTxStatus_EmptyIdsParam_Returns400(t *testing.T) {
	t.Parallel()

	// ?ids= is present but empty — Query().Get("ids") returns "".
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/tx/status?ids=", nil)
	r = withAuthCtx(r, "user-123")
	w := httptest.NewRecorder()
	txstatus.NewHandler(&fakeSvc{}).GetTxStatusBatch(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "missing_ids")
}

func TestBatchTxStatus_TooManyIds_Returns400(t *testing.T) {
	t.Parallel()

	ids := make([]string, 21)
	for i := range ids {
		ids[i] = validTxID()
	}
	h := txstatus.NewHandler(&fakeSvc{})
	w := doBatch(h, strings.Join(ids, ","), true)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "too_many_ids")
}

func TestBatchTxStatus_InvalidHex_Returns400(t *testing.T) {
	t.Parallel()

	// One valid txid + one txid containing 'z' (non-hex character).
	invalidID := txIDWith('z')
	ids := strings.Join([]string{validTxID(), invalidID}, ",")
	h := txstatus.NewHandler(&fakeSvc{})
	w := doBatch(h, ids, true)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "invalid_txid")
}

func TestBatchTxStatus_AllNotFound_Returns200(t *testing.T) {
	t.Parallel()

	txid := validTxID()
	svc := &fakeSvc{
		getTxStatusBatchFn: func(_ context.Context, in txstatus.GetTxStatusBatchInput) (map[string]txstatus.TxStatusResult, error) {
			m := make(map[string]txstatus.TxStatusResult, len(in.TxIDs))
			for _, id := range in.TxIDs {
				m[id] = txstatus.TxStatusResult{Status: "not_found"}
			}
			return m, nil
		},
	}
	h := txstatus.NewHandler(svc)
	w := doBatch(h, txid, true)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	statuses, ok := resp["statuses"].(map[string]any)
	require.True(t, ok)
	entry, ok := statuses[txid].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "not_found", entry["status"])
}

func TestBatchTxStatus_MixedStatuses_Returns200(t *testing.T) {
	t.Parallel()

	const (
		txConfirmed   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		txMempool     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		txNotFound    = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		txConflicting = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
		txAbandoned   = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	)

	svc := &fakeSvc{
		getTxStatusBatchFn: func(_ context.Context, _ txstatus.GetTxStatusBatchInput) (map[string]txstatus.TxStatusResult, error) {
			return map[string]txstatus.TxStatusResult{
				txConfirmed:   {Status: txstatus.TxStatusConfirmed, Confirmations: 3, BlockHeight: 800000},
				txMempool:     {Status: txstatus.TxStatusMempool},
				txNotFound:    {Status: txstatus.TxStatusNotFound},
				txConflicting: {Status: txstatus.TxStatusConflicting},
				txAbandoned:   {Status: txstatus.TxStatusAbandoned},
			}, nil
		},
	}
	h := txstatus.NewHandler(svc)
	ids := strings.Join([]string{txConfirmed, txMempool, txNotFound, txConflicting, txAbandoned}, ",")
	w := doBatch(h, ids, true)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	statuses, ok := resp["statuses"].(map[string]any)
	require.True(t, ok)

	getStatus := func(id string) string {
		e, ok := statuses[id].(map[string]any)
		require.True(t, ok, "missing entry for %s", id)
		s, _ := e["status"].(string)
		return s
	}

	assert.Equal(t, "confirmed", getStatus(txConfirmed))
	assert.Equal(t, "mempool", getStatus(txMempool))
	assert.Equal(t, "not_found", getStatus(txNotFound))
	assert.Equal(t, "conflicting", getStatus(txConflicting))
	assert.Equal(t, "abandoned", getStatus(txAbandoned))
}

func TestBatchTxStatus_RPCDown_Returns502(t *testing.T) {
	t.Parallel()

	svc := &fakeSvc{
		getTxStatusBatchFn: func(_ context.Context, _ txstatus.GetTxStatusBatchInput) (map[string]txstatus.TxStatusResult, error) {
			return nil, txstatus.ErrRPCUnavailable
		},
	}
	h := txstatus.NewHandler(svc)
	w := doBatch(h, validTxID(), true)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "service_unavailable")
}

func TestBatchTxStatus_InternalError_Returns500(t *testing.T) {
	t.Parallel()
	svc := &fakeSvc{
		getTxStatusBatchFn: func(_ context.Context, _ txstatus.GetTxStatusBatchInput) (map[string]txstatus.TxStatusResult, error) {
			return nil, errors.New("unexpected internal failure")
		},
	}
	h := txstatus.NewHandler(svc)
	w := doBatch(h, validTxID(), true)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "internal_error")
}

// TestBatchTxStatus_DuplicateTxid_CollapsesToSingleEntry verifies that a
// duplicate txid in the ids param is deduplicated by the handler and collapses
// to a single entry in the response map — this is the specified behaviour.
func TestBatchTxStatus_DuplicateTxid_CollapsesToSingleEntry(t *testing.T) {
	t.Parallel()

	txid := validTxID()
	var callCount int
	svc := &fakeSvc{
		getTxStatusBatchFn: func(_ context.Context, in txstatus.GetTxStatusBatchInput) (map[string]txstatus.TxStatusResult, error) {
			callCount = len(in.TxIDs) // handler must have deduplicated before calling
			m := make(map[string]txstatus.TxStatusResult, len(in.TxIDs))
			for _, id := range in.TxIDs {
				m[id] = txstatus.TxStatusResult{Status: txstatus.TxStatusConfirmed}
			}
			return m, nil
		},
	}
	h := txstatus.NewHandler(svc)
	ids := strings.Join([]string{txid, txid}, ",")
	w := doBatch(h, ids, true)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, callCount, "handler must deduplicate before forwarding to service")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	statuses, ok := resp["statuses"].(map[string]any)
	require.True(t, ok)
	entry, ok := statuses[txid].(map[string]any)
	require.True(t, ok, "txid must appear in response even when duplicated in input")
	assert.Equal(t, "confirmed", entry["status"])
}
