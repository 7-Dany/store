package txstatus

import (
	"context"
	"errors"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Servicer is the subset of the Service that the Handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
	CreateTrackedTxStatus(ctx context.Context, in CreateTrackedTxStatusInput) (TrackedTxStatus, error)
	GetTrackedTxStatus(ctx context.Context, in GetTrackedTxStatusInput) (TrackedTxStatus, error)
	ListTrackedTxStatuses(ctx context.Context, in ListTrackedTxStatusesInput) ([]TrackedTxStatus, error)
	UpdateTrackedTxStatus(ctx context.Context, in UpdateTrackedTxStatusInput) (TrackedTxStatus, error)
	DeleteTrackedTxStatus(ctx context.Context, in DeleteTrackedTxStatusInput) error
}

// TxQuerier is the narrow RPC interface the Service requires.
// rpc.Client satisfies this interface structurally. Tests may supply a fake
// that only implements these two methods, avoiding the full rpc.Client stub.
type TxQuerier interface {
	GetTransaction(ctx context.Context, txid string, verbose bool) (rpc.WalletTx, error)
	GetMempoolEntry(ctx context.Context, txid string) (rpc.MempoolEntry, error)
	GetBlockHeader(ctx context.Context, hash string) (rpc.BlockHeader, error)
	GetBlockVerbose(ctx context.Context, hash string) (rpc.VerboseBlock, error)
}

// compile-time check that rpc.Client satisfies TxQuerier.
var _ TxQuerier = (rpc.Client)(nil)

// Storer is the durable data-access contract for txstatus CRUD operations.
type Storer interface {
	CreateTrackedTxStatus(ctx context.Context, in trackedTxStatusWriteInput) (TrackedTxStatus, error)
	GetTrackedTxStatus(ctx context.Context, in GetTrackedTxStatusInput) (TrackedTxStatus, error)
	ListTrackedTxStatuses(ctx context.Context, in ListTrackedTxStatusesInput) ([]TrackedTxStatus, error)
	UpdateTrackedTxStatus(ctx context.Context, in trackedTxStatusUpdateInput) (TrackedTxStatus, error)
	DeleteTrackedTxStatus(ctx context.Context, in DeleteTrackedTxStatusInput) error
}

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

// trackedTxStatusWriteInput is the fully-resolved row payload sent to the store.
type trackedTxStatusWriteInput struct {
	UserID          uuid.UUID
	Network         string
	Address         *string
	TxID            string
	Status          TxStatus
	Confirmations   int
	AmountSat       int64
	FeeRateSatVByte float64
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	ConfirmedAt     *time.Time
	BlockHash       *string
	BlockHeight     *int64
	ReplacementTxID *string
}

// trackedTxStatusUpdateInput is the fully-resolved update payload sent to the store.
type trackedTxStatusUpdateInput struct {
	ID     int64
	UserID uuid.UUID
	trackedTxStatusWriteInput
}

// DBConflictInspector is the narrow PG error helper the store uses to detect unique conflicts.
type DBConflictInspector interface {
	IsUniqueViolation(err error, constraint string) bool
}

type pgConflictInspector struct{}

func (pgConflictInspector) IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
