package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetBlockVerbose_ReturnsDecodedBlockAndUsesVerbosityTwo(t *testing.T) {
	t.Parallel()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotBody = string(raw)

		var reqEnvelope struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(raw, &reqEnvelope)

		resultJSON := `{"confirmations":9,"height":777001,"hash":"blockhash","tx":[{"txid":"abc","vin":[{"txid":"prev","vout":1}],"vout":[{"value":0.001,"n":0,"scriptPubKey":{"address":"tb1qdest","type":"witness_v0_keyhash"}}]}]}`

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":%s,"error":null,"id":%s}`, resultJSON, string(reqEnvelope.ID))
	}))
	t.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	iface, err := New(parts[0], parts[1], "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 0
	c.retryCeiling = 0

	block, err := c.GetBlockVerbose(context.Background(), "blockhash")
	require.NoError(t, err)
	assert.Equal(t, "blockhash", block.Hash)
	assert.Equal(t, 777001, block.Height)
	assert.Equal(t, 9, block.Confirmations)
	require.Len(t, block.Tx, 1)
	assert.Equal(t, "abc", block.Tx[0].TxID)
	require.Len(t, block.Tx[0].Vout, 1)
	assert.Equal(t, "tb1qdest", block.Tx[0].Vout[0].ScriptPubKey.Address)

	assert.Contains(t, gotBody, `"method":"getblock"`)
	assert.Contains(t, gotBody, `"params":["blockhash",2]`)
}

func TestGetBlockVerbose_PrunedBlockError_ReturnsStructuredError(t *testing.T) {
	t.Parallel()

	rec := &captureRecorder{}
	body := `{"jsonrpc":"2.0","result":null,"error":{"code":-1,"message":"Block not found on disk: pruned data"},"id":1}`
	c, _ := newTestServer(t, http.StatusInternalServerError, body, rec)

	_, err := c.GetBlockVerbose(context.Background(), "deadbeef")
	require.Error(t, err)
	assert.True(t, IsPrunedBlockError(err))

	require.Equal(t, 1, rec.callCount(), "pruned block errors are deterministic and must not retry")
	rec.mu.Lock()
	require.Len(t, rec.errors, 1)
	assert.Equal(t, RPCErrPruned.String(), rec.errors[0].errType)
	rec.mu.Unlock()
}

func TestGetBlock_ReturnsRawMessage(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":"01000000...","error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	raw, err := c.GetBlock(context.Background(), "blockhash", 0)
	require.NoError(t, err)
	assert.Equal(t, json.RawMessage(`"01000000..."`), raw)
}

func TestGetBlock_InvalidVerbosity_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"jsonrpc":"2.0","result":{},"error":null,"id":1}`, nil)

	_, err := c.GetBlock(context.Background(), "hash", 4)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verbosity must be 0–3")

	_, err = c.GetBlock(context.Background(), "hash", -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verbosity must be 0–3")
}

func TestGetBlockHash_ReturnsHash(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":"000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f","error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	hash, err := c.GetBlockHash(context.Background(), 0)
	require.NoError(t, err)
	assert.Equal(t, "000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f", hash)
}

func TestGetBlockCount_ReturnsCount(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":800000,"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	count, err := c.GetBlockCount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 800000, count)
}
