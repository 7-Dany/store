package block

import (
	"context"

	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// log is the package-level structured logger. Shared across handler.go and
// service.go since both live in package block.
var log = telemetry.New("block")

// Service implements Servicer using a Bitcoin Core RPC client.
type Service struct {
	rpc Querier
}

// NewService constructs a Service with the given RPC client.
func NewService(rpcClient Querier) *Service {
	return &Service{rpc: rpcClient}
}

// GetBlock resolves the details of a single Bitcoin block by hash.
func (s *Service) GetBlock(ctx context.Context, in GetBlockInput) (Result, error) {
	header, err := s.rpc.GetBlockHeader(ctx, in.Hash)
	if err != nil {
		if rpc.IsNotFoundError(err) {
			return Result{}, ErrBlockNotFound
		}
		log.Error(ctx, "block: GetBlockHeader RPC error", "hash", in.Hash, "error", err)
		return Result{}, ErrRPCUnavailable
	}

	return Result{
		Hash:              header.Hash,
		Confirmations:     header.Confirmations,
		Height:            header.Height,
		Version:           header.Version,
		MerkleRoot:        header.MerkleRoot,
		Time:              header.Time,
		MedianTime:        header.MedianTime,
		Nonce:             header.Nonce,
		Bits:              header.Bits,
		Difficulty:        header.Difficulty,
		Chainwork:         header.Chainwork,
		TxCount:           header.NTx,
		PreviousBlockHash: header.PreviousBlock,
		NextBlockHash:     header.NextBlock,
	}, nil
}

// GetLatestBlock returns the details of the current chain tip by calling
// GetBlockchainInfo to discover the best block hash, then GetBlockHeader for
// the full header details.
//
// Returns ErrRPCUnavailable on any RPC error — the chain tip is always expected
// to exist, so ErrBlockNotFound is remapped to ErrRPCUnavailable as a safety net.
func (s *Service) GetLatestBlock(ctx context.Context) (Result, error) {
	info, err := s.rpc.GetBlockchainInfo(ctx)
	if err != nil {
		log.Error(ctx, "block: GetBlockchainInfo RPC error", "error", err)
		return Result{}, ErrRPCUnavailable
	}
	result, err := s.GetBlock(ctx, GetBlockInput{Hash: info.BestBlockHash})
	if err != nil {
		// ErrBlockNotFound for the chain tip is a node consistency error;
		// surface it as ErrRPCUnavailable so callers see a consistent sentinel.
		log.Error(ctx, "block: GetBlock failed for chain tip", "hash", info.BestBlockHash, "error", err)
		return Result{}, ErrRPCUnavailable
	}
	return result, nil
}
