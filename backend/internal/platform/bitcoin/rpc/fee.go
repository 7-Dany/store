package rpc

import (
	"context"
	"fmt"
	"strings"
)

// ── Fee estimation ────────────────────────────────────────────────────────────

// EstimateSmartFee returns a fee rate estimate for the given confirmation target.
// FeeEstimate.FeeRate is zero when the node lacks sufficient data for estimation.
func (c *client) EstimateSmartFee(ctx context.Context, confTarget int, mode string) (FeeEstimate, error) {
	if confTarget < 1 || confTarget > 1008 {
		return FeeEstimate{}, fmt.Errorf("EstimateSmartFee: confTarget must be 1–1008, got %d", confTarget)
	}
	modeUpper := strings.ToUpper(mode)
	if modeUpper != "ECONOMICAL" && modeUpper != "CONSERVATIVE" {
		return FeeEstimate{}, fmt.Errorf("EstimateSmartFee: mode must be \"ECONOMICAL\" or \"CONSERVATIVE\", got %q", mode)
	}
	var result FeeEstimate
	err := c.retryCall(ctx, rpcMethodEstimateSmartFee, []any{confTarget, modeUpper}, &result)
	return result, err
}
