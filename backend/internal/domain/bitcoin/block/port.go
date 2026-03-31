package block

import (
	"context"

	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
)

// Servicer is the subset of the Service that the Handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
	GetBlock(ctx context.Context, in GetBlockInput) (Result, error)
	// GetLatestBlock returns the details of the current chain tip.
	// Returns ErrRPCUnavailable on any RPC error.
	GetLatestBlock(ctx context.Context) (Result, error)
}

// Querier is the narrow RPC interface the Service requires.
// rpc.Client satisfies this interface structurally.
type Querier interface {
	GetBlockHeader(ctx context.Context, hash string) (rpc.BlockHeader, error)
	// GetBlockchainInfo returns node chain info (chain, best block hash, height).
	// Used by GetLatestBlock to resolve the chain tip without a known hash.
	GetBlockchainInfo(ctx context.Context) (rpc.BlockchainInfo, error)
}

// compile-time check that rpc.Client satisfies Querier.
var _ Querier = rpc.Client(nil)
