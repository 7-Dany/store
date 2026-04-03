package rpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendRawTransaction_ZeroMaxFeeRate_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"jsonrpc":"2.0","result":"txid","error":null,"id":1}`, nil)

	_, err := c.SendRawTransaction(context.Background(), "deadbeef", 0)
	require.Error(t, err, "maxFeeRate=0 must be rejected before contacting Bitcoin Core")
	assert.Contains(t, err.Error(), "maxFeeRate")
	assert.Contains(t, err.Error(), "0")
}

func TestSendRawTransaction_NegativeMaxFeeRate_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"jsonrpc":"2.0","result":"txid","error":null,"id":1}`, nil)

	_, err := c.SendRawTransaction(context.Background(), "deadbeef", -0.001)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxFeeRate")
}

func TestSendRawTransaction_ValidFeeRate_CallsServer(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":"abcdef1234567890","error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	txid, err := c.SendRawTransaction(context.Background(), "deadbeef", 0.001)
	require.NoError(t, err)
	assert.Equal(t, "abcdef1234567890", txid)
}
