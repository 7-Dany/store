package block_test

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

	blockdomain "github.com/7-Dany/store/backend/internal/domain/bitcoin/block"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// ── fakeSvc ───────────────────────────────────────────────────────────────────

// fakeSvc is a minimal implementation of block.Servicer for handler tests.
type fakeSvc struct {
	getBlockFn       func(ctx context.Context, in blockdomain.GetBlockInput) (blockdomain.Result, error)
	getLatestBlockFn func(ctx context.Context) (blockdomain.Result, error)
}

// compile-time check that *fakeSvc satisfies block.Servicer.
var _ blockdomain.Servicer = (*fakeSvc)(nil)

func (f *fakeSvc) GetBlock(ctx context.Context, in blockdomain.GetBlockInput) (blockdomain.Result, error) {
	if f.getBlockFn != nil {
		return f.getBlockFn(ctx, in)
	}
	panic("fakeSvc.GetBlock: getBlockFn not set — configure it for this test")
}

func (f *fakeSvc) GetLatestBlock(ctx context.Context) (blockdomain.Result, error) {
	if f.getLatestBlockFn != nil {
		return f.getLatestBlockFn(ctx)
	}
	panic("fakeSvc.GetLatestBlock: getLatestBlockFn not set — configure it for this test")
}

// ── test helpers ──────────────────────────────────────────────────────────────

func validBlockHash() string { return strings.Repeat("a", 64) }

func withAuthCtx(r *http.Request, userID string) *http.Request {
	return r.WithContext(token.InjectUserIDForTest(r.Context(), userID))
}

func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func doGetBlock(h *blockdomain.Handler, hash string, authed bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/block/"+hash, http.NoBody)
	if authed {
		r = withAuthCtx(r, "user-123")
	}
	r = withChiParam(r, "hash", hash)
	w := httptest.NewRecorder()
	h.GetBlock(w, r)
	return w
}

func assertJSONCode(t *testing.T, body []byte, wantCode string) {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m), "body is not valid JSON: %s", body)
	assert.Equal(t, wantCode, m["code"], "JSON 'code' mismatch; body: %s", body)
}

func TestGetBlock_MissingAuth_Returns401(t *testing.T) {
	t.Parallel()

	h := blockdomain.NewHandler(&fakeSvc{})
	w := doGetBlock(h, validBlockHash(), false)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "unauthorized")
}

func TestGetBlock_InvalidHash_Returns400(t *testing.T) {
	t.Parallel()

	h := blockdomain.NewHandler(&fakeSvc{})
	w := doGetBlock(h, strings.Repeat("g", 64), true)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "invalid_block_hash")
}

func TestGetBlock_UppercaseHash_Returns200(t *testing.T) {
	t.Parallel()

	h := blockdomain.NewHandler(&fakeSvc{
		getBlockFn: func(_ context.Context, in blockdomain.GetBlockInput) (blockdomain.Result, error) {
			require.Equal(t, strings.Repeat("a", 64), in.Hash)
			return blockdomain.Result{Hash: in.Hash, Height: 1}, nil
		},
	})
	w := doGetBlock(h, strings.Repeat("A", 64), true)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetBlock_Success_Returns200(t *testing.T) {
	t.Parallel()

	const blockHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	h := blockdomain.NewHandler(&fakeSvc{
		getBlockFn: func(_ context.Context, _ blockdomain.GetBlockInput) (blockdomain.Result, error) {
			return blockdomain.Result{
				Hash:              blockHash,
				Confirmations:     3,
				Height:            127724,
				Version:           536870912,
				MerkleRoot:        "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Time:              1700000000,
				MedianTime:        1699999990,
				Nonce:             42,
				Bits:              "1d00ffff",
				Difficulty:        12345.5,
				Chainwork:         "000000000000000000000000000000000000000000000000000000000000abcd",
				TxCount:           7,
				PreviousBlockHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				NextBlockHash:     "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			}, nil
		},
	})

	w := doGetBlock(h, blockHash, true)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, blockHash, resp["hash"])
	assert.Equal(t, float64(127724), resp["height"])
	assert.Equal(t, float64(7), resp["tx_count"])
	assert.Equal(t, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", resp["previous_block_hash"])
}

func TestGetBlock_NotFound_Returns404(t *testing.T) {
	t.Parallel()

	h := blockdomain.NewHandler(&fakeSvc{
		getBlockFn: func(_ context.Context, _ blockdomain.GetBlockInput) (blockdomain.Result, error) {
			return blockdomain.Result{}, blockdomain.ErrBlockNotFound
		},
	})

	w := doGetBlock(h, validBlockHash(), true)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "block_not_found")
}

func TestGetBlock_RPCDown_Returns502(t *testing.T) {
	t.Parallel()

	h := blockdomain.NewHandler(&fakeSvc{
		getBlockFn: func(_ context.Context, _ blockdomain.GetBlockInput) (blockdomain.Result, error) {
			return blockdomain.Result{}, blockdomain.ErrRPCUnavailable
		},
	})

	w := doGetBlock(h, validBlockHash(), true)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "service_unavailable")
}

func TestGetBlock_InternalError_Returns500(t *testing.T) {
	t.Parallel()

	h := blockdomain.NewHandler(&fakeSvc{
		getBlockFn: func(_ context.Context, _ blockdomain.GetBlockInput) (blockdomain.Result, error) {
			return blockdomain.Result{}, errors.New("unexpected failure")
		},
	})

	w := doGetBlock(h, validBlockHash(), true)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "internal_error")
}
