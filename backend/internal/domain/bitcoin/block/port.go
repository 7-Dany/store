package block

import (
	"context"

	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
)

// Servicer is the subset of the Service that the Handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
	GetBlock(ctx context.Context, in GetBlockInput) (Result, error)
}

// Querier is the narrow RPC interface the Service requires.
// Rpc.Client satisfies this interface structurally.
type Querier interface {
	GetBlockHeader(ctx context.Context, hash string) (rpc.BlockHeader, error)
}

// compile-time check that rpc.Client satisfies Querier.
var _ Querier = rpc.Client(nil)
