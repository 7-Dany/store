package rpc

import "context"

// ── Mempool methods ───────────────────────────────────────────────────────────

// GetMempoolEntry checks whether a transaction is currently in the mempool.
// Returns IsNotFoundError (code -5) when absent — the normal absent response.
func (c *client) GetMempoolEntry(ctx context.Context, txid string) (MempoolEntry, error) {
	var result MempoolEntry
	err := c.retryCall(ctx, rpcMethodGetMempoolEntry, []any{txid}, &result)
	return result, err
}

// GetRawTransaction fetches a raw decoded transaction from the mempool or (with txindex) chain.
// Verbosity=1 returns the decoded JSON object as a RawTx; verbosity=0 returns the hex string
// (not supported by this method — call GetBlock for that use case).
//
// Key property: works on ANY mempool transaction without txindex — unlike GetTransaction
// (wallet-only). Use this on the SSE display path to match arbitrary watched addresses.
//
// Uses retryCall for transient network errors. The "tx left mempool" scenario produces
// an RPCError (code -5 or HTTP 500 with JSON body), which classifyError maps to
// RPCErrNotFound or RPCErrRPC — neither of which is retried. Only RPCErrNetwork/
// RPCErrTimeout are retried, which is exactly correct.
func (c *client) GetRawTransaction(ctx context.Context, txid string, verbosity int) (RawTx, error) {
	if verbosity != 0 && verbosity != 1 {
		return RawTx{}, ErrInvalidVerbosity{Method: "GetRawTransaction", Got: verbosity, Max: 1}
	}
	var result RawTx
	err := c.retryCall(ctx, rpcMethodGetRawTransaction, []any{txid, verbosity}, &result)
	return result, err
}
