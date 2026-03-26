package txstatus

import (
	"context"

	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// Servicer is the subset of the Service that the Handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
	GetTxStatus(ctx context.Context, in GetTxStatusInput) (TxStatusResult, error)
	GetTxStatusBatch(ctx context.Context, in GetTxStatusBatchInput) (map[string]TxStatusResult, error)
}

// TxQuerier is the narrow RPC interface the Service requires.
// rpc.Client satisfies this interface structurally. Tests may supply a fake
// that only implements these two methods, avoiding the full rpc.Client stub.
type TxQuerier interface {
	GetTransaction(ctx context.Context, txid string, verbose bool) (rpc.WalletTx, error)
	GetMempoolEntry(ctx context.Context, txid string) (rpc.MempoolEntry, error)
}

// compile-time check that rpc.Client satisfies TxQuerier.
var _ TxQuerier = (rpc.Client)(nil)

// TxStatusRecorder is the narrow observability interface for the txstatus package.
// *telemetry.Registry satisfies this interface structurally via OnTxStatusResolved.
// Pass deps.Metrics directly; no factory method is needed.
// All implementations must be safe for concurrent use.
type TxStatusRecorder interface {
	// OnTxStatusResolved increments bitcoin_txstatus_resolved_total{status}.
	// status must be one of the TxStatus* constant string values.
	OnTxStatusResolved(status string)
}

// compile-time check that *telemetry.Registry satisfies TxStatusRecorder.
var _ TxStatusRecorder = (*telemetry.Registry)(nil)
