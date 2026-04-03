package rpc

import (
	"context"
	"encoding/json"
)

// BlockchainReader provides read-only access to blockchain data.
type BlockchainReader interface {
	GetBlockchainInfo(ctx context.Context) (BlockchainInfo, error)
	GetBlockHeader(ctx context.Context, hash string) (BlockHeader, error)
	GetBlock(ctx context.Context, hash string, verbosity int) (json.RawMessage, error)
	GetBlockVerbose(ctx context.Context, hash string) (VerboseBlock, error)
	GetBlockHash(ctx context.Context, height int) (string, error)
	GetBlockCount(ctx context.Context) (int, error)
}

// WalletReader provides read-only access to wallet data.
type WalletReader interface {
	GetTransaction(ctx context.Context, txid string, verbose bool) (WalletTx, error)
	GetAddressInfo(ctx context.Context, address string) (AddressInfo, error)
	GetWalletInfo(ctx context.Context) (WalletInfo, error)
}

// WalletWriter provides write access to the wallet.
type WalletWriter interface {
	GetNewAddress(ctx context.Context, label, addressType string) (string, error)
	KeypoolRefill(ctx context.Context, newSize int) error
	WalletCreateFundedPSBT(ctx context.Context, outputs []map[string]any, options map[string]any) (FundedPSBT, error)
	WalletProcessPSBT(ctx context.Context, psbt string) (ProcessedPSBT, error)
	FinalizePSBT(ctx context.Context, psbt string) (FinalizedPSBT, error)
}

// MempoolReader provides read-only access to the mempool.
type MempoolReader interface {
	GetMempoolEntry(ctx context.Context, txid string) (MempoolEntry, error)
	GetRawTransaction(ctx context.Context, txid string, verbosity int) (RawTx, error)
}

// FeeEstimator provides fee estimation.
type FeeEstimator interface {
	EstimateSmartFee(ctx context.Context, confTarget int, mode string) (FeeEstimate, error)
}

// Broadcaster broadcasts transactions to the network.
type Broadcaster interface {
	SendRawTransaction(ctx context.Context, hexTx string, maxFeeRate float64) (string, error)
}

// Client is the read/write interface for Bitcoin Core's RPC API.
// Depend on this interface in domain packages — never on the concrete *client
// directly. This decouples domain packages from the platform layer and makes
// them trivially testable with a mock.
type Client interface {
	BlockchainReader
	WalletReader
	WalletWriter
	MempoolReader
	FeeEstimator
	Broadcaster
	Close(ctx context.Context)
}
