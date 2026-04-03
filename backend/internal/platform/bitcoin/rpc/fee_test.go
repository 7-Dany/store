package rpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimateSmartFee_ReturnsFeeRateAndBlocks(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"feerate":0.00012345,"blocks":3},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	fee, err := c.EstimateSmartFee(context.Background(), 3, "economical")
	require.NoError(t, err)
	assert.Equal(t, 3, fee.Blocks)
	sat, err := BtcToSat(fee.FeeRate)
	require.NoError(t, err)
	assert.Equal(t, int64(12345), sat)
}

func TestEstimateSmartFee_ZeroFeeRate_NodeLacksData(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"feerate":0,"blocks":1},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	fee, err := c.EstimateSmartFee(context.Background(), 1, "economical")
	require.NoError(t, err)
	sat, err := BtcToSat(fee.FeeRate)
	require.NoError(t, err)
	assert.Equal(t, int64(0), sat)
}

func TestEstimateSmartFee_InvalidConfTarget_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"jsonrpc":"2.0","result":{"feerate":0.0001,"blocks":1},"error":null,"id":1}`, nil)

	_, err := c.EstimateSmartFee(context.Background(), 0, "economical")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confTarget must be 1")

	_, err = c.EstimateSmartFee(context.Background(), 1009, "economical")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confTarget must be 1")
}

func TestEstimateSmartFee_InvalidMode_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"jsonrpc":"2.0","result":{"feerate":0.0001,"blocks":1},"error":null,"id":1}`, nil)

	_, err := c.EstimateSmartFee(context.Background(), 3, "INVALID")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode must be")
}
