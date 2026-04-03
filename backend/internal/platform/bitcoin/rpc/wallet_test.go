package rpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCall_MethodConstantUsed_GetNewAddress(t *testing.T) {
	rec := &captureRecorder{}
	body := `{"jsonrpc":"2.0","result":"tb1qexampleaddress","error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, rec)

	_, err := c.GetNewAddress(context.Background(), InvoiceAddressLabel, InvoiceAddressType)
	require.NoError(t, err)

	require.Equal(t, 1, rec.callCount())
	rec.mu.Lock()
	assert.Equal(t, rpcMethodGetNewAddress, rec.calls[0].method)
	rec.mu.Unlock()
}

func TestGetWalletInfo_ReturnsKeypoolSize(t *testing.T) {
	rec := &captureRecorder{}
	body := `{"jsonrpc":"2.0","result":{"walletname":"","walletversion":169900,"keypoolsize":9500,"keypoolsize_hd_internal":9500,"keypoololdest":0,"descriptors":true},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, rec)

	info, err := c.GetWalletInfo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 9500, info.KeypoolSize)

	rec.SetKeypoolSize(info.KeypoolSize)
	rec.mu.Lock()
	require.Len(t, rec.keypoolSeen, 1)
	assert.Equal(t, 9500, rec.keypoolSeen[0])
	rec.mu.Unlock()
}

func TestGetTransaction_ConfirmedTx_ReturnsBlockHeight(t *testing.T) {
	body := `{"jsonrpc":"2.0","result":{"txid":"abc","confirmations":3,"blockhash":"bbb","blockheight":500,"blocktime":1700000000,"timereceived":1700000000,"details":[]},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	tx, err := c.GetTransaction(context.Background(), "abc", false)
	require.NoError(t, err)
	assert.Equal(t, 3, tx.Confirmations)
	assert.Equal(t, 500, tx.BlockHeight)
}

func TestGetTransaction_MempoolTx_ZeroConfirmations(t *testing.T) {
	body := `{"jsonrpc":"2.0","result":{"txid":"abc","confirmations":0,"blockhash":"","blockheight":0,"blocktime":0,"timereceived":1700000000,"details":[]},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	tx, err := c.GetTransaction(context.Background(), "abc", false)
	require.NoError(t, err)
	assert.Equal(t, 0, tx.Confirmations)
	assert.Equal(t, 0, tx.BlockHeight)
	assert.Empty(t, tx.BlockHash)
}

func TestGetTransaction_Verbose_PopulatesDecoded(t *testing.T) {
	body := `{"jsonrpc":"2.0","result":{"txid":"abc","confirmations":1,"blockhash":"bbb","blockheight":1,"blocktime":1,"timereceived":1,"details":[],"decoded":{"txid":"abc","vout":[{"value":0.001,"n":0,"scriptPubKey":{"address":"tb1qtest","type":"witness_v0_keyhash"}}]}},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	tx, err := c.GetTransaction(context.Background(), "abc", true)
	require.NoError(t, err)
	require.NotNil(t, tx.Decoded)
	require.Len(t, tx.Decoded.Vout, 1)

	sat, err := BtcToSat(tx.Decoded.Vout[0].Value)
	require.NoError(t, err)
	assert.Equal(t, int64(100_000), sat)
}

func TestGetTransaction_NegativeConfirmations_IsConflicting(t *testing.T) {
	body := `{"jsonrpc":"2.0","result":{"txid":"abc","confirmations":-1,"blockhash":"oldhash","blockheight":100,"blocktime":1700000000,"timereceived":1700000000,"details":[]},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	tx, err := c.GetTransaction(context.Background(), "abc", false)
	require.NoError(t, err)
	assert.Equal(t, -1, tx.Confirmations, "Confirmations must be -1 for a tx in a conflicting chain")
	assert.True(t, IsConflicting(tx), "IsConflicting must return true for negative Confirmations")
}

func TestGetAddressInfo_ReturnsInfo(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"address":"tb1qtest","ismine":true,"iswatchonly":false,"solvable":true,"ischange":false,"label":"test"},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	info, err := c.GetAddressInfo(context.Background(), "tb1qtest")
	require.NoError(t, err)
	assert.Equal(t, "tb1qtest", info.Address)
	assert.True(t, info.IsMine)
}

func TestInvoiceAddressConstants_Correct(t *testing.T) {
	assert.Equal(t, "invoice", InvoiceAddressLabel)
	assert.Equal(t, "bech32", InvoiceAddressType)
}

func TestIsConflicting_PositiveConfirmations_ReturnsFalse(t *testing.T) {
	tx := WalletTx{Confirmations: 3}
	assert.False(t, IsConflicting(tx))
}

func TestIsConflicting_ZeroConfirmations_ReturnsFalse(t *testing.T) {
	tx := WalletTx{Confirmations: 0}
	assert.False(t, IsConflicting(tx), "Zero confirmations = mempool, not conflicting")
}
