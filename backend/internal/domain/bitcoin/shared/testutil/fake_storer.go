// Package bitcoinsharedtest provides test-only helpers shared across all
// Bitcoin domain feature sub-packages. It must never be imported by production code.
package bitcoinsharedtest

import (
	"context"
	"time"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/domain/bitcoin/events"
)

// ── EventsFakeStorer ──────────────────────────────────────────────────────────

// EventsFakeStorer is a hand-written implementation of events.Storer for service
// unit tests. Each method delegates to its Fn field if non-nil; otherwise it
// returns a safe zero value and nil error.
type EventsFakeStorer struct {
	StoreSessionSIDFn             func(ctx context.Context, jti, sessionID string, ttl time.Duration) error
	GetDelSessionSIDFn            func(ctx context.Context, jti string) (string, error)
	ConsumeJTIFn                  func(ctx context.Context, jti string, ttl time.Duration) (bool, error)
	RecordTokenIssuanceFn         func(ctx context.Context, vendorID [16]byte, network, jtiHash string, sourceIPHash *string, expiresAt time.Time) error
	WriteAuditLogFn               func(ctx context.Context, event audit.EventType, userID string, metadata map[string]any) error
	GetUserWatchAddressesFn       func(ctx context.Context, userID, network string) ([]string, error)
	UpsertWatchBitcoinTxStatusFn  func(ctx context.Context, in events.TrackedStatusUpsertInput) error
	TouchBitcoinTxStatusMempoolFn func(ctx context.Context, userID, network, txid string, feeRateSatVByte float64, lastSeenAt time.Time) error
	ConfirmBitcoinTxStatusFn      func(ctx context.Context, userID, network, txid, blockHash string, confirmations int, blockHeight int64, confirmedAt time.Time) error
	MarkBitcoinTxStatusReplacedFn func(ctx context.Context, userID, network, replacedTxID, replacementTxID string, replacedAt time.Time) error
	ListBitcoinTxStatusUsersFn    func(ctx context.Context, network, txid string) ([]string, error)
	ListActiveTxWatchUsersFn      func(ctx context.Context, network, txid string) ([]string, error)
}

// compile-time check that *EventsFakeStorer satisfies events.Storer.
var _ events.Storer = (*EventsFakeStorer)(nil)

func (f *EventsFakeStorer) StoreSessionSID(ctx context.Context, jti, sessionID string, ttl time.Duration) error {
	if f.StoreSessionSIDFn != nil {
		return f.StoreSessionSIDFn(ctx, jti, sessionID, ttl)
	}
	return nil
}

func (f *EventsFakeStorer) GetDelSessionSID(ctx context.Context, jti string) (string, error) {
	if f.GetDelSessionSIDFn != nil {
		return f.GetDelSessionSIDFn(ctx, jti)
	}
	return "fake-session-id", nil
}

func (f *EventsFakeStorer) ConsumeJTI(ctx context.Context, jti string, ttl time.Duration) (bool, error) {
	if f.ConsumeJTIFn != nil {
		return f.ConsumeJTIFn(ctx, jti, ttl)
	}
	return true, nil // default: token not yet consumed
}

func (f *EventsFakeStorer) RecordTokenIssuance(ctx context.Context, vendorID [16]byte, network, jtiHash string, sourceIPHash *string, expiresAt time.Time) error {
	if f.RecordTokenIssuanceFn != nil {
		return f.RecordTokenIssuanceFn(ctx, vendorID, network, jtiHash, sourceIPHash, expiresAt)
	}
	return nil
}

// WriteAuditLog delegates to WriteAuditLogFn if set.
func (f *EventsFakeStorer) WriteAuditLog(ctx context.Context, event audit.EventType, userID string, metadata map[string]any) error {
	if f.WriteAuditLogFn != nil {
		return f.WriteAuditLogFn(ctx, event, userID, metadata)
	}
	return nil
}

func (f *EventsFakeStorer) GetUserWatchAddresses(ctx context.Context, userID, network string) ([]string, error) {
	if f.GetUserWatchAddressesFn != nil {
		return f.GetUserWatchAddressesFn(ctx, userID, network)
	}
	return []string{}, nil
}

func (f *EventsFakeStorer) UpsertWatchBitcoinTxStatus(ctx context.Context, in events.TrackedStatusUpsertInput) error {
	if f.UpsertWatchBitcoinTxStatusFn != nil {
		return f.UpsertWatchBitcoinTxStatusFn(ctx, in)
	}
	return nil
}

func (f *EventsFakeStorer) TouchBitcoinTxStatusMempool(ctx context.Context, userID, network, txid string, feeRateSatVByte float64, lastSeenAt time.Time) error {
	if f.TouchBitcoinTxStatusMempoolFn != nil {
		return f.TouchBitcoinTxStatusMempoolFn(ctx, userID, network, txid, feeRateSatVByte, lastSeenAt)
	}
	return nil
}

func (f *EventsFakeStorer) ConfirmBitcoinTxStatus(ctx context.Context, userID, network, txid, blockHash string, confirmations int, blockHeight int64, confirmedAt time.Time) error {
	if f.ConfirmBitcoinTxStatusFn != nil {
		return f.ConfirmBitcoinTxStatusFn(ctx, userID, network, txid, blockHash, confirmations, blockHeight, confirmedAt)
	}
	return nil
}

func (f *EventsFakeStorer) MarkBitcoinTxStatusReplaced(ctx context.Context, userID, network, replacedTxID, replacementTxID string, replacedAt time.Time) error {
	if f.MarkBitcoinTxStatusReplacedFn != nil {
		return f.MarkBitcoinTxStatusReplacedFn(ctx, userID, network, replacedTxID, replacementTxID, replacedAt)
	}
	return nil
}

func (f *EventsFakeStorer) ListBitcoinTxStatusUsersByTxID(ctx context.Context, network, txid string) ([]string, error) {
	if f.ListBitcoinTxStatusUsersFn != nil {
		return f.ListBitcoinTxStatusUsersFn(ctx, network, txid)
	}
	return nil, nil
}

func (f *EventsFakeStorer) ListActiveBitcoinTransactionWatchUsersByTxID(ctx context.Context, network, txid string) ([]string, error) {
	if f.ListActiveTxWatchUsersFn != nil {
		return f.ListActiveTxWatchUsersFn(ctx, network, txid)
	}
	return nil, nil
}
