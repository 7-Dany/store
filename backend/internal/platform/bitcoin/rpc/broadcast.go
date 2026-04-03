package rpc

import (
	"context"
	"fmt"
)

// ── Broadcast ─────────────────────────────────────────────────────────────────

// SendRawTransaction broadcasts a signed raw transaction to the Bitcoin network.
//
// MaxFeeRate is the maximum acceptable fee rate in BTC/kB. Bitcoin Core rejects
// the broadcast if the transaction's effective fee rate exceeds this value.
// Passing 0 (the Go zero-value) removes the cap entirely and can result in
// permanent fund loss if the fee estimator misbehaves — callers must always
// pass a positive value.
//
// Not retried — broadcasting twice causes "transaction already in mempool" errors
// and complicates double-spend detection. The settlement engine owns the retry
// loop with its own idempotency check.
func (c *client) SendRawTransaction(ctx context.Context, hexTx string, maxFeeRate float64) (string, error) {
	if maxFeeRate <= 0 {
		return "", fmt.Errorf("sendrawtransaction: maxFeeRate must be > 0 (got %v) — "+
			"passing 0 removes the fee-rate cap and can permanently burn funds", maxFeeRate)
	}
	var result string
	err := c.call(ctx, rpcMethodSendRawTransaction, []any{hexTx, maxFeeRate}, &result)
	return result, err
}
