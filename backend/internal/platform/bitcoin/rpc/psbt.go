package rpc

import "context"

// ── PSBT sweep methods ────────────────────────────────────────────────────────

// WalletCreateFundedPSBT constructs a PSBT, selecting inputs automatically.
// Not retried — the caller drives the sweep broadcast loop.
//
// Outputs must not be nil — pass an empty slice to let Bitcoin Core select
// all UTXOs automatically. A nil slice marshals to JSON null rather than []
// and causes Bitcoin Core to return an RPC error -1/-8.
//
// Fixed positional parameters sent to Bitcoin Core:
//   - inputs:      [] (empty — let the wallet select UTXOs automatically)
//   - locktime:    0  (no CLTV locktime)
//   - bip32derivs: true (include BIP-32 derivation paths in the PSBT — required
//     for walletprocesspsbt to locate signing keys)
func (c *client) WalletCreateFundedPSBT(ctx context.Context, outputs []map[string]any, options map[string]any) (FundedPSBT, error) {
	if outputs == nil {
		return FundedPSBT{}, ErrNilOutputs
	}
	var result FundedPSBT
	params := []any{[]any{}, outputs, 0, options, true}
	err := c.call(ctx, rpcMethodWalletCreateFundedPSBT, params, &result)
	return result, err
}

// WalletProcessPSBT signs a PSBT with the wallet's private keys.
// Not retried — the caller drives the sweep broadcast loop.
func (c *client) WalletProcessPSBT(ctx context.Context, psbt string) (ProcessedPSBT, error) {
	var result ProcessedPSBT
	err := c.call(ctx, rpcMethodWalletProcessPSBT, []any{psbt}, &result)
	return result, err
}

// FinalizePSBT extracts a broadcast-ready transaction from a fully signed PSBT.
// Not retried — the caller drives the sweep broadcast loop.
func (c *client) FinalizePSBT(ctx context.Context, psbt string) (FinalizedPSBT, error) {
	var result FinalizedPSBT
	err := c.call(ctx, rpcMethodFinalizePSBT, []any{psbt}, &result)
	return result, err
}

// ErrNilOutputs is returned when WalletCreateFundedPSBT receives nil outputs.
var ErrNilOutputs = &ValidationError{
	Message: "WalletCreateFundedPSBT: outputs must not be nil — " +
		"pass an empty slice to auto-select UTXOs; nil marshals to JSON null and Bitcoin Core rejects it",
}

// ValidationError is returned for client-side input validation failures.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }
