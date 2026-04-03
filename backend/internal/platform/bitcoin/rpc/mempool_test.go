package rpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMempoolEntry_ReturnsEntry(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"vsize":100,"weight":400,"time":1700000000,"height":100,"fees":{"base":0.00001000,"modified":0.00001000},"bip125-replaceable":true},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	entry, err := c.GetMempoolEntry(context.Background(), "abc")
	require.NoError(t, err)
	assert.Equal(t, 100, entry.VSize)
	require.NotNil(t, entry.BIP125Replaceable)
	assert.True(t, *entry.BIP125Replaceable)
}

func TestGetRawTransaction_ReturnsRawTx(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"txid":"abc","hash":"wtxid","vin":[],"vout":[],"size":100,"vsize":100,"weight":400,"version":2,"locktime":0},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	tx, err := c.GetRawTransaction(context.Background(), "abc", 1)
	require.NoError(t, err)
	assert.Equal(t, "abc", tx.TxID)
	assert.Equal(t, "wtxid", tx.Hash)
	assert.Equal(t, 100, tx.VSize)
}

func TestGetRawTransaction_InvalidVerbosity_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"jsonrpc":"2.0","result":{},"error":null,"id":1}`, nil)

	_, err := c.GetRawTransaction(context.Background(), "abc", 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verbosity must be 0–1")
}
