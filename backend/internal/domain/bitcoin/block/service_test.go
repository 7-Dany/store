package block_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	blockdomain "github.com/7-Dany/store/backend/internal/domain/bitcoin/block"
	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
)

// ── fakeRPC ───────────────────────────────────────────────────────────────────

// fakeRPC is a minimal implementation of block.Querier for service unit tests.
type fakeRPC struct {
	getBlockHeaderFn     func(ctx context.Context, hash string) (rpc.BlockHeader, error)
	getBlockchainInfoFn  func(ctx context.Context) (rpc.BlockchainInfo, error)
}

// compile-time check that *fakeRPC satisfies block.Querier.
var _ blockdomain.Querier = (*fakeRPC)(nil)

func (f *fakeRPC) GetBlockHeader(ctx context.Context, hash string) (rpc.BlockHeader, error) {
	if f.getBlockHeaderFn != nil {
		return f.getBlockHeaderFn(ctx, hash)
	}
	panic("fakeRPC.GetBlockHeader: getBlockHeaderFn not set — configure it for this test")
}

func (f *fakeRPC) GetBlockchainInfo(ctx context.Context) (rpc.BlockchainInfo, error) {
	if f.getBlockchainInfoFn != nil {
		return f.getBlockchainInfoFn(ctx)
	}
	panic("fakeRPC.GetBlockchainInfo: getBlockchainInfoFn not set — configure it for this test")
}

// ── GetBlock tests ────────────────────────────────────────────────────────────

func TestGetBlock_Success(t *testing.T) {
	t.Parallel()

	const blockHash = "000000000b2ac1f75ad909ca14329139a7767e8bea15e65c908e8bad6249c945"
	svc := blockdomain.NewService(&fakeRPC{
		getBlockHeaderFn: func(_ context.Context, hash string) (rpc.BlockHeader, error) {
			require.Equal(t, blockHash, hash)
			return rpc.BlockHeader{
				Confirmations: 6,
				Height:        127724,
				Hash:          blockHash,
				Version:       536870912,
				MerkleRoot:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Time:          1700000000,
				MedianTime:    1699999990,
				Nonce:         42,
				Bits:          "1d00ffff",
				Difficulty:    12345.5,
				Chainwork:     "000000000000000000000000000000000000000000000000000000000000abcd",
				NTx:           12,
				PreviousBlock: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				NextBlock:     "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			}, nil
		},
	})

	result, err := svc.GetBlock(context.Background(), blockdomain.GetBlockInput{Hash: blockHash})

	require.NoError(t, err)
	assert.Equal(t, blockHash, result.Hash)
	assert.Equal(t, 6, result.Confirmations)
	assert.Equal(t, 127724, result.Height)
	assert.Equal(t, 536870912, result.Version)
	assert.Equal(t, 12, result.TxCount)
	assert.Equal(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", result.PreviousBlockHash)
	assert.Equal(t, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", result.NextBlockHash)
}

func TestGetBlock_NotFound(t *testing.T) {
	t.Parallel()

	svc := blockdomain.NewService(&fakeRPC{
		getBlockHeaderFn: func(_ context.Context, _ string) (rpc.BlockHeader, error) {
			return rpc.BlockHeader{}, &rpc.RPCError{Code: -5, Message: "Block not found"}
		},
	})

	_, err := svc.GetBlock(context.Background(), blockdomain.GetBlockInput{Hash: "abc"})

	require.Error(t, err)
	assert.True(t, errors.Is(err, blockdomain.ErrBlockNotFound))
}

func TestGetBlock_RPCDown(t *testing.T) {
	t.Parallel()

	svc := blockdomain.NewService(&fakeRPC{
		getBlockHeaderFn: func(_ context.Context, _ string) (rpc.BlockHeader, error) {
			return rpc.BlockHeader{}, &rpc.RPCError{Code: -8, Message: "node error"}
		},
	})

	_, err := svc.GetBlock(context.Background(), blockdomain.GetBlockInput{Hash: "abc"})

	require.Error(t, err)
	assert.True(t, errors.Is(err, blockdomain.ErrRPCUnavailable))
}

// ── GetLatestBlock tests ──────────────────────────────────────────────────────

func TestGetLatestBlock_Success(t *testing.T) {
	t.Parallel()

	const tipHash = "000000000b2ac1f75ad909ca14329139a7767e8bea15e65c908e8bad6249c945"
	svc := blockdomain.NewService(&fakeRPC{
		getBlockchainInfoFn: func(_ context.Context) (rpc.BlockchainInfo, error) {
			return rpc.BlockchainInfo{
				Chain:         "testnet4",
				Blocks:        127724,
				BestBlockHash: tipHash,
			}, nil
		},
		getBlockHeaderFn: func(_ context.Context, hash string) (rpc.BlockHeader, error) {
			require.Equal(t, tipHash, hash, "GetBlockHeader must be called with BestBlockHash from GetBlockchainInfo")
			return rpc.BlockHeader{
				Hash:          tipHash,
				Height:        127724,
				Confirmations: 1,
				NTx:           5,
			}, nil
		},
	})

	result, err := svc.GetLatestBlock(context.Background())

	require.NoError(t, err)
	assert.Equal(t, tipHash, result.Hash)
	assert.Equal(t, 127724, result.Height)
	assert.Equal(t, 1, result.Confirmations)
	assert.Equal(t, 5, result.TxCount)
}

func TestGetLatestBlock_BlockchainInfoRPCDown_ReturnsRPCUnavailable(t *testing.T) {
	t.Parallel()

	svc := blockdomain.NewService(&fakeRPC{
		getBlockchainInfoFn: func(_ context.Context) (rpc.BlockchainInfo, error) {
			return rpc.BlockchainInfo{}, errors.New("connection refused")
		},
	})

	_, err := svc.GetLatestBlock(context.Background())

	require.Error(t, err)
	assert.True(t, errors.Is(err, blockdomain.ErrRPCUnavailable), "expected ErrRPCUnavailable, got %v", err)
}

func TestGetLatestBlock_GetBlockHeaderRPCDown_ReturnsRPCUnavailable(t *testing.T) {
	t.Parallel()

	svc := blockdomain.NewService(&fakeRPC{
		getBlockchainInfoFn: func(_ context.Context) (rpc.BlockchainInfo, error) {
			return rpc.BlockchainInfo{BestBlockHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, nil
		},
		getBlockHeaderFn: func(_ context.Context, _ string) (rpc.BlockHeader, error) {
			return rpc.BlockHeader{}, &rpc.RPCError{Code: -8, Message: "node error"}
		},
	})

	_, err := svc.GetLatestBlock(context.Background())

	require.Error(t, err)
	assert.True(t, errors.Is(err, blockdomain.ErrRPCUnavailable), "expected ErrRPCUnavailable, got %v", err)
}

// TestGetLatestBlock_TipNotFound_ReturnsRPCUnavailable verifies that a -5
// (block not found) for the chain tip is remapped to ErrRPCUnavailable rather
// than ErrBlockNotFound, because the best block hash supplied by
// GetBlockchainInfo must always exist.
func TestGetLatestBlock_TipNotFound_ReturnsRPCUnavailable(t *testing.T) {
	t.Parallel()

	svc := blockdomain.NewService(&fakeRPC{
		getBlockchainInfoFn: func(_ context.Context) (rpc.BlockchainInfo, error) {
			return rpc.BlockchainInfo{BestBlockHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, nil
		},
		getBlockHeaderFn: func(_ context.Context, _ string) (rpc.BlockHeader, error) {
			return rpc.BlockHeader{}, &rpc.RPCError{Code: -5, Message: "Block not found"}
		},
	})

	_, err := svc.GetLatestBlock(context.Background())

	require.Error(t, err)
	// ErrBlockNotFound from GetBlock is remapped to ErrRPCUnavailable for the
	// chain tip, because a missing best block hash indicates a node consistency
	// error rather than a legitimate absence.
	assert.True(t, errors.Is(err, blockdomain.ErrRPCUnavailable), "expected ErrRPCUnavailable, got %v", err)
}
