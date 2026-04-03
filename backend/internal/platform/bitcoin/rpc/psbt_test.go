package rpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWalletCreateFundedPSBT_ReturnsPSBTAndFee(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"psbt":"cHNidP8B...","fee":0.00000500,"changepos":1},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	outputs := []map[string]any{{"tb1qrecipient": 0.001}}
	options := map[string]any{"fee_rate": 10}
	result, err := c.WalletCreateFundedPSBT(context.Background(), outputs, options)
	require.NoError(t, err)
	assert.Equal(t, "cHNidP8B...", result.PSBT)
	assert.Equal(t, 1, result.ChangePos)
	fee, err := BtcToSat(result.Fee)
	require.NoError(t, err)
	assert.Equal(t, int64(500), fee)
}

func TestWalletCreateFundedPSBT_NoChange_ChangePosMinusOne(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"psbt":"cHNidP8B...","fee":0.00000100,"changepos":-1},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	result, err := c.WalletCreateFundedPSBT(context.Background(), []map[string]any{}, nil)
	require.NoError(t, err)
	assert.Equal(t, -1, result.ChangePos, "ChangePos must be -1 when there is no change output")
}

func TestWalletCreateFundedPSBT_NilOutputs_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"jsonrpc":"2.0","result":{"psbt":"x","fee":0,"changepos":-1},"error":null,"id":1}`, nil)

	_, err := c.WalletCreateFundedPSBT(context.Background(), nil, nil)
	require.Error(t, err, "nil outputs must be rejected before contacting Bitcoin Core")
	assert.Contains(t, err.Error(), "outputs must not be nil")
}

func TestWalletProcessPSBT_Complete_ReturnsSignedPSBT(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"psbt":"cHNidP8BsIgnEd...","complete":true},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	result, err := c.WalletProcessPSBT(context.Background(), "cHNidP8B...")
	require.NoError(t, err)
	assert.True(t, result.Complete)
	assert.Equal(t, "cHNidP8BsIgnEd...", result.PSBT)
}

func TestFinalizePSBT_Complete_ReturnsHex(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"hex":"02000000000101...","complete":true},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	result, err := c.FinalizePSBT(context.Background(), "cHNidP8BsIgnEd...")
	require.NoError(t, err)
	assert.True(t, result.Complete)
	assert.Equal(t, "02000000000101...", result.Hex)
}

func TestFinalizePSBT_NotComplete_CallerMustCheckField(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"hex":"","complete":false},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	result, err := c.FinalizePSBT(context.Background(), "cHNidP8BPartial...")
	require.NoError(t, err, "complete=false is not an error — caller must check result.Complete")
	assert.False(t, result.Complete, "result.Complete must be false when PSBT is not fully signed")
	assert.Empty(t, result.Hex, "Hex must be empty when Complete=false")
}
