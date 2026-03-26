package txstatus

import (
	"context"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// log is the package-level structured logger. Shared across handler.go and
// service.go since both live in package txstatus.
var log = telemetry.New("txstatus")

// noopTxStatusRecorder discards all metric calls.
// Substituted by NewService when rec is nil.
type noopTxStatusRecorder struct{}

func (noopTxStatusRecorder) OnTxStatusResolved(string) {}

// Service implements Servicer using a Bitcoin Core RPC client.
type Service struct {
	rpc TxQuerier
	rec TxStatusRecorder
}

// NewService constructs a Service with the given RPC client and metrics recorder.
// Pass deps.Metrics as rec; *telemetry.Registry satisfies TxStatusRecorder
// structurally. Passing nil substitutes a no-op recorder.
func NewService(rpcClient TxQuerier, rec TxStatusRecorder) *Service {
	if rec == nil {
		rec = noopTxStatusRecorder{}
	}
	return &Service{rpc: rpcClient, rec: rec}
}

// GetTxStatus resolves the on-chain status of a single transaction.
//
// If the wallet does not recognise the txid (RPC error -5), GetMempoolEntry is
// called as a fallback to distinguish a genuinely absent transaction from one
// that is in the public mempool but not wallet-tracked. This prevents a false
// not_found for untracked mempool transactions.
func (s *Service) GetTxStatus(ctx context.Context, in GetTxStatusInput) (TxStatusResult, error) {
	tx, err := s.rpc.GetTransaction(ctx, in.TxID, false)
	if err != nil {
		if rpc.IsNotFoundError(err) {
			// -5 from GetTransaction means "not in wallet". Fall back to the
			// public mempool: a valid unconfirmed tx may be present there even
			// if the wallet has never seen it.
			_, mempoolErr := s.rpc.GetMempoolEntry(ctx, in.TxID)
			if mempoolErr == nil {
				s.rec.OnTxStatusResolved(string(TxStatusMempool))
				return TxStatusResult{Status: TxStatusMempool}, nil
			}
			if rpc.IsNotFoundError(mempoolErr) {
				s.rec.OnTxStatusResolved(string(TxStatusNotFound))
				return TxStatusResult{Status: TxStatusNotFound}, nil
			}
			// Genuine RPC infrastructure failure from GetMempoolEntry.
			log.Error(ctx, "txstatus: GetMempoolEntry RPC error",
				"txid", in.TxID, "error", mempoolErr)
			return TxStatusResult{}, ErrRPCUnavailable
		}
		if rpc.IsNoWalletError(err) {
			log.Error(ctx, "txstatus: GetTransaction RPC error — no wallet loaded",
				"txid", in.TxID, "error", err)
			return TxStatusResult{}, ErrWalletNotLoaded
		}
		log.Error(ctx, "txstatus: GetTransaction RPC error",
			"txid", in.TxID, "error", err)
		return TxStatusResult{}, ErrRPCUnavailable
	}

	if tx.Confirmations < 0 {
		if tx.Confirmations == -1 {
			// -1: conflicting — the transaction was double-spent or replaced by
			// a confirmed block. It will never confirm.
			s.rec.OnTxStatusResolved(string(TxStatusConflicting))
			return TxStatusResult{Status: TxStatusConflicting}, nil
		}
		// -2 and below: abandoned — the wallet explicitly marked this tx dead
		// via bitcoin-cli abandontransaction or equivalent. Semantically
		// distinct from conflicting: no replacement tx confirmed; the wallet
		// just gave up on it.
		s.rec.OnTxStatusResolved(string(TxStatusAbandoned))
		return TxStatusResult{Status: TxStatusAbandoned}, nil
	}

	if tx.Confirmations > 0 {
		s.rec.OnTxStatusResolved(string(TxStatusConfirmed))
		return TxStatusResult{
			Status:        TxStatusConfirmed,
			Confirmations: tx.Confirmations,
			BlockHeight:   tx.BlockHeight,
		}, nil
	}

	// tx.Confirmations == 0: transaction is in the mempool.
	s.rec.OnTxStatusResolved(string(TxStatusMempool))
	return TxStatusResult{Status: TxStatusMempool}, nil
}

// GetTxStatusBatch resolves the status of multiple transactions concurrently.
//
// All txids are queried in parallel via goroutines coordinated by errgroup.
// The first RPC failure cancels all in-flight requests and returns
// ErrRPCUnavailable. Duplicate txids in in.TxIDs produce a single map entry;
// the caller (handler) is expected to deduplicate before calling this method.
func (s *Service) GetTxStatusBatch(ctx context.Context, in GetTxStatusBatchInput) (map[string]TxStatusResult, error) {
	results := make(map[string]TxStatusResult, len(in.TxIDs))
	var mu sync.Mutex

	g, gctx := errgroup.WithContext(ctx)
	for _, txid := range in.TxIDs {
		g.Go(func() error {
			res, err := s.GetTxStatus(gctx, GetTxStatusInput{TxID: txid})
			if err != nil {
				return err
			}
			mu.Lock()
			results[txid] = res
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}
