package txstatus

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/google/uuid"
)

// log is the package-level structured logger. Shared across handler.go and
// service.go since both live in package txstatus.
var log = telemetry.New("txstatus")

// noopTxStatusRecorder discards all metric calls.
type noopTxStatusRecorder struct{}

func (noopTxStatusRecorder) OnTxStatusResolved(string) {}

// Service implements Servicer using a Bitcoin Core RPC client.
type Service struct {
	rpc     TxQuerier
	store   Storer
	rec     TxStatusRecorder
	network string
}

// NewService constructs a Service with the given RPC client and metrics recorder.
//
// Pass deps.Metrics as rec; *telemetry.Registry satisfies TxStatusRecorder
// structurally. Passing nil substitutes a no-op recorder.
func NewService(rpcClient TxQuerier, store Storer, rec TxStatusRecorder, network string) *Service {
	if rec == nil {
		rec = noopTxStatusRecorder{}
	}
	return &Service{rpc: rpcClient, store: store, rec: rec, network: network}
}

// GetTxStatus resolves the on-chain status of a single transaction.
//
// If the wallet does not recognise the txid (RPC error -5), GetMempoolEntry is
// called as a fallback to distinguish a genuinely absent transaction from one
// that is in the public mempool but not wallet-tracked. This prevents a false
// not_found for untracked mempool transactions.
func (s *Service) GetTxStatus(ctx context.Context, in GetTxStatusInput) (TxStatusResult, error) {
	return s.resolveTxStatus(ctx, in.UserID, in.TxID)
}

// GetTxStatusBatch resolves the status of multiple transactions concurrently.
func (s *Service) GetTxStatusBatch(ctx context.Context, in GetTxStatusBatchInput) (map[string]TxStatusResult, error) {
	results := make(map[string]TxStatusResult, len(in.TxIDs))
	var mu sync.Mutex

	g, gctx := errgroup.WithContext(ctx)
	for _, txid := range in.TxIDs {
		txid := txid
		g.Go(func() error {
			res, err := s.resolveTxStatus(gctx, in.UserID, txid)
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

// CreateTrackedTxStatus resolves the tx from the node and persists an explicit txid-tracking row.
func (s *Service) CreateTrackedTxStatus(ctx context.Context, in CreateTrackedTxStatusInput) (TrackedTxStatus, error) {
	userUUID, err := uuid.Parse(in.UserID)
	if err != nil {
		return TrackedTxStatus{}, telemetry.Service("CreateTrackedTxStatus.parse_user_id", err)
	}

	resolved, err := s.resolveTxStatus(ctx, in.UserID, in.TxID)
	if err != nil {
		return TrackedTxStatus{}, err
	}

	now := time.Now().UTC()
	write := trackedTxStatusWriteInput{
		UserID:          userUUID,
		Network:         in.Network,
		Address:         in.Address,
		TxID:            in.TxID,
		Status:          resolved.Status,
		Confirmations:   resolved.Confirmations,
		AmountSat:       0,
		FeeRateSatVByte: 0,
		FirstSeenAt:     now,
		LastSeenAt:      now,
	}
	if resolved.Status == TxStatusConfirmed {
		write.ConfirmedAt = &now
		height := int64(resolved.BlockHeight)
		write.BlockHeight = &height
	}

	row, err := s.store.CreateTrackedTxStatus(ctx, write)
	if err != nil {
		return TrackedTxStatus{}, telemetry.Service("CreateTrackedTxStatus.create", err)
	}
	return row, nil
}

// GetTrackedTxStatus returns one durable txstatus row by ID.
func (s *Service) GetTrackedTxStatus(ctx context.Context, in GetTrackedTxStatusInput) (TrackedTxStatus, error) {
	row, err := s.store.GetTrackedTxStatus(ctx, in)
	if err != nil {
		return TrackedTxStatus{}, telemetry.Service("GetTrackedTxStatus.get", err)
	}
	return row, nil
}

// ListTrackedTxStatuses returns durable txstatus rows for one user.
func (s *Service) ListTrackedTxStatuses(ctx context.Context, in ListTrackedTxStatusesInput) ([]TrackedTxStatus, error) {
	rows, err := s.store.ListTrackedTxStatuses(ctx, in)
	if err != nil {
		return nil, telemetry.Service("ListTrackedTxStatuses.list", err)
	}
	return rows, nil
}

// UpdateTrackedTxStatus refreshes one explicit txid-tracking row using its
// existing txid and may replace its optional associated address.
func (s *Service) UpdateTrackedTxStatus(ctx context.Context, in UpdateTrackedTxStatusInput) (TrackedTxStatus, error) {
	current, err := s.store.GetTrackedTxStatus(ctx, GetTrackedTxStatusInput{ID: in.ID, UserID: in.UserID})
	if err != nil {
		return TrackedTxStatus{}, telemetry.Service("UpdateTrackedTxStatus.get", err)
	}
	if current.TrackingMode != TrackingModeTxID {
		return TrackedTxStatus{}, ErrWatchManagedTrackedTxStatus
	}

	userUUID, err := uuid.Parse(in.UserID)
	if err != nil {
		return TrackedTxStatus{}, telemetry.Service("UpdateTrackedTxStatus.parse_user_id", err)
	}

	resolved, err := s.resolveTxStatus(ctx, in.UserID, current.TxID)
	if err != nil {
		return TrackedTxStatus{}, err
	}

	now := time.Now().UTC()
	update := trackedTxStatusUpdateInput{
		ID:     in.ID,
		UserID: userUUID,
		trackedTxStatusWriteInput: trackedTxStatusWriteInput{
			UserID:          userUUID,
			Network:         current.Network,
			Address:         in.Address,
			TxID:            current.TxID,
			Status:          resolved.Status,
			Confirmations:   resolved.Confirmations,
			AmountSat:       current.AmountSat,
			FeeRateSatVByte: current.FeeRateSatVByte,
			FirstSeenAt:     current.FirstSeenAt,
			LastSeenAt:      now,
		},
	}
	if resolved.Status == TxStatusConfirmed {
		update.ConfirmedAt = &now
		height := int64(resolved.BlockHeight)
		update.BlockHeight = &height
	}

	row, err := s.store.UpdateTrackedTxStatus(ctx, update)
	if err != nil {
		return TrackedTxStatus{}, telemetry.Service("UpdateTrackedTxStatus.update", err)
	}
	return row, nil
}

// DeleteTrackedTxStatus removes one durable txstatus row.
func (s *Service) DeleteTrackedTxStatus(ctx context.Context, in DeleteTrackedTxStatusInput) error {
	if err := s.store.DeleteTrackedTxStatus(ctx, in); err != nil {
		return telemetry.Service("DeleteTrackedTxStatus.delete", err)
	}
	return nil
}

// resolveTxStatus is the shared live RPC resolution path used by read and CRUD flows.
//
// When the node can no longer resolve a tracked transaction directly, resolveTxStatus
// falls back to the caller's saved txstatus rows. This keeps previously discovered
// non-wallet transactions queryable after the events flow has already recorded
// their confirmed block height.
func (s *Service) resolveTxStatus(ctx context.Context, userID, txid string) (TxStatusResult, error) {
	tx, err := s.rpc.GetTransaction(ctx, txid, false)
	if err != nil {
		if rpc.IsNotFoundError(err) {
			// -5 from GetTransaction means "not in wallet". Fall back to the
			// public mempool: a valid unconfirmed tx may be present there even
			// if the wallet has never seen it.
			_, mempoolErr := s.rpc.GetMempoolEntry(ctx, txid)
			if mempoolErr == nil {
				s.rec.OnTxStatusResolved(string(TxStatusMempool))
				return TxStatusResult{Status: TxStatusMempool}, nil
			}
			if rpc.IsNotFoundError(mempoolErr) {
				if durable, ok := s.resolveTrackedTxStatus(ctx, userID, txid); ok {
					return durable, nil
				}
				s.rec.OnTxStatusResolved(string(TxStatusNotFound))
				return TxStatusResult{Status: TxStatusNotFound}, nil
			}
			if durable, ok := s.resolveTrackedTxStatus(ctx, userID, txid); ok {
				return durable, nil
			}
			// Genuine RPC infrastructure failure from GetMempoolEntry.
			log.Error(ctx, "txstatus: GetMempoolEntry RPC error",
				"txid", txid, "error", mempoolErr)
			return TxStatusResult{}, ErrRPCUnavailable
		}
		if durable, ok := s.resolveTrackedTxStatus(ctx, userID, txid); ok {
			return durable, nil
		}
		if rpc.IsNoWalletError(err) {
			log.Error(ctx, "txstatus: GetTransaction RPC error — no wallet loaded",
				"txid", txid, "error", err)
			return TxStatusResult{}, ErrWalletNotLoaded
		}
		log.Error(ctx, "txstatus: GetTransaction RPC error",
			"txid", txid, "error", err)
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

// resolveTrackedTxStatus returns a durable fallback result when the caller has
// already tracked txid through the txstatus or watch/event flows.
func (s *Service) resolveTrackedTxStatus(ctx context.Context, userID, txid string) (TxStatusResult, bool) {
	if s.store == nil || userID == "" {
		return TxStatusResult{}, false
	}

	rows, err := s.store.ListTrackedTxStatuses(ctx, ListTrackedTxStatusesInput{
		UserID:  userID,
		Network: s.network,
		TxID:    txid,
		Limit:   1,
	})
	if err != nil {
		log.Warn(ctx, "txstatus: durable fallback lookup failed", "txid", txid, "user_id", userID, "error", err)
		return TxStatusResult{}, false
	}
	if len(rows) == 0 {
		return TxStatusResult{}, false
	}

	row := rows[0]
	switch row.Status {
	case TxStatusConfirmed:
		return s.resolveConfirmedTrackedTx(ctx, txid, row)
	case TxStatusConflicting, TxStatusAbandoned, TxStatusReplaced:
		s.rec.OnTxStatusResolved(string(row.Status))
		return TxStatusResult{Status: row.Status}, true
	default:
		return TxStatusResult{}, false
	}
}

// resolveConfirmedTrackedTx refreshes a confirmed tracked row from its saved block anchor.
func (s *Service) resolveConfirmedTrackedTx(ctx context.Context, txid string, row TrackedTxStatus) (TxStatusResult, bool) {
	if row.BlockHash != nil {
		block, err := s.rpc.GetBlockVerbose(ctx, *row.BlockHash)
		if err == nil {
			for _, candidate := range block.Tx {
				if candidate.TxID != txid {
					continue
				}
				confirmations := max(block.Confirmations, 1)
				height := block.Height
				if height == 0 && row.BlockHeight != nil {
					height = int(*row.BlockHeight)
				}
				s.rec.OnTxStatusResolved(string(TxStatusConfirmed))
				return TxStatusResult{
					Status:        TxStatusConfirmed,
					Confirmations: confirmations,
					BlockHeight:   height,
				}, true
			}
			log.Warn(ctx, "txstatus: saved block does not contain tracked txid", "txid", txid, "block_hash", *row.BlockHash)
		} else {
			log.Warn(ctx, "txstatus: GetBlockVerbose failed for tracked fallback", "txid", txid, "block_hash", *row.BlockHash, "error", err)
		}

		header, err := s.rpc.GetBlockHeader(ctx, *row.BlockHash)
		if err == nil && header.Confirmations > 0 {
			s.rec.OnTxStatusResolved(string(TxStatusConfirmed))
			return TxStatusResult{
				Status:        TxStatusConfirmed,
				Confirmations: header.Confirmations,
				BlockHeight:   header.Height,
			}, true
		}
		if err != nil {
			log.Warn(ctx, "txstatus: GetBlockHeader failed for tracked fallback", "txid", txid, "block_hash", *row.BlockHash, "error", err)
		}
	}

	confirmations := row.Confirmations
	if confirmations < 1 {
		confirmations = 1
	}
	height := 0
	if row.BlockHeight != nil {
		height = int(*row.BlockHeight)
	}
	s.rec.OnTxStatusResolved(string(TxStatusConfirmed))
	return TxStatusResult{
		Status:        TxStatusConfirmed,
		Confirmations: confirmations,
		BlockHeight:   height,
	}, true
}
