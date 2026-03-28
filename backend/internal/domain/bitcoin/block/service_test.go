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
	getBlockHeaderFn func(ctx context.Context, hash string) (rpc.BlockHeader, error)
}

// compile-time check that *fakeRPC satisfies block.Querier.
var _ blockdomain.Querier = (*fakeRPC)(nil)

func (f *fakeRPC) GetBlockHeader(ctx context.Context, hash string) (rpc.BlockHeader, error) {
	if f.getBlockHeaderFn != nil {
		return f.getBlockHeaderFn(ctx, hash)
	}
	panic("fakeRPC.GetBlockHeader: getBlockHeaderFn not set — configure it for this test")
}

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
