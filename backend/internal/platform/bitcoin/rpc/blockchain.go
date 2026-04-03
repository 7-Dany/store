package rpc

import (
	"context"
	"encoding/json"
	"strconv"
)

// ── Blockchain methods ────────────────────────────────────────────────────────

// GetBlockchainInfo returns node chain info (chain, best block hash, height,
// pruning status). Connected-state tracking is handled by call() for all methods.
func (c *client) GetBlockchainInfo(ctx context.Context) (BlockchainInfo, error) {
	var result BlockchainInfo
	err := c.retryCall(ctx, rpcMethodGetBlockchainInfo, nil, &result)
	return result, err
}

// GetBlockHeader returns lightweight block metadata (height, hash, timestamp).
func (c *client) GetBlockHeader(ctx context.Context, hash string) (BlockHeader, error) {
	var result BlockHeader
	err := c.retryCall(ctx, rpcMethodGetBlockHeader, []any{hash, true}, &result)
	return result, err
}

// GetBlock fetches block data at the specified verbosity level.
//
//   - verbosity=0: hex-encoded block data.
//   - verbosity=1: block metadata + list of txids.
//   - verbosity=2: full transaction data (2–4 MiB on mainnet — use with care).
//   - verbosity=3: verbosity=2 with prevout data for each input.
//
// For decoded transactions, use GetBlockVerbose (verbosity=2). GetBlock is
// useful for verbosity=0 (hex block) or verbosity=1 (txid list) where typed
// parsing is unnecessary.
func (c *client) GetBlock(ctx context.Context, hash string, verbosity int) (json.RawMessage, error) {
	if verbosity < 0 || verbosity > 3 {
		return nil, ErrInvalidVerbosity{Method: "GetBlock", Got: verbosity, Max: 3}
	}
	var result json.RawMessage
	err := c.retryCall(ctx, rpcMethodGetBlock, []any{hash, verbosity}, &result)
	return result, err
}

// GetBlockVerbose returns a block with decoded transactions.
func (c *client) GetBlockVerbose(ctx context.Context, hash string) (VerboseBlock, error) {
	var result VerboseBlock
	err := c.retryCall(ctx, rpcMethodGetBlock, []any{hash, 2}, &result)
	return result, err
}

// GetBlockHash returns the block hash at the given height on the active chain.
func (c *client) GetBlockHash(ctx context.Context, height int) (string, error) {
	var result string
	err := c.retryCall(ctx, rpcMethodGetBlockHash, []any{height}, &result)
	return result, err
}

// GetBlockCount returns the current height of the active chain tip.
func (c *client) GetBlockCount(ctx context.Context) (int, error) {
	var result int
	err := c.retryCall(ctx, rpcMethodGetBlockCount, nil, &result)
	return result, err
}

// ErrInvalidVerbosity is returned when a verbosity parameter is out of range.
type ErrInvalidVerbosity struct {
	Method string
	Got    int
	Max    int
}

func (e ErrInvalidVerbosity) Error() string {
	return e.Method + ": verbosity must be 0–" + strconv.Itoa(e.Max) + ", got " + strconv.Itoa(e.Got)
}
