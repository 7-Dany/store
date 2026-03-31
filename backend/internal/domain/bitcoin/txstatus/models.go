package txstatus

import "time"

// TxStatus is the resolved on-chain status of a Bitcoin transaction.
type TxStatus string

const (
	// TxStatusConfirmed means the transaction has at least one confirmation.
	TxStatusConfirmed TxStatus = "confirmed"
	// TxStatusMempool means the transaction is known but still unconfirmed.
	TxStatusMempool TxStatus = "mempool"
	// TxStatusNotFound means neither the wallet nor the mempool knows the txid.
	TxStatusNotFound TxStatus = "not_found"
	// TxStatusConflicting means Bitcoin Core marked the transaction as conflicting.
	TxStatusConflicting TxStatus = "conflicting"
	// TxStatusAbandoned means the wallet explicitly abandoned the transaction.
	TxStatusAbandoned TxStatus = "abandoned"
	// TxStatusReplaced means a newer transaction replaced the original txid.
	TxStatusReplaced TxStatus = "replaced"
)

// TrackingMode identifies how a persisted txstatus row was created.
type TrackingMode string

const (
	// TrackingModeTxID means the row was created from an explicit txid request.
	TrackingModeTxID TrackingMode = "txid"
	// TrackingModeWatch means the row was discovered from a watched address event.
	TrackingModeWatch TrackingMode = "watch"
)

// TxStatusResult holds the live resolved status of a single Bitcoin transaction.
type TxStatusResult struct {
	Status        TxStatus
	Confirmations int
	BlockHeight   int
}

// TrackedTxStatus is one durable txstatus row exposed by the txstatus CRUD API.
type TrackedTxStatus struct {
	ID              int64
	UserID          string
	Network         string
	TrackingMode    TrackingMode
	Address         *string
	Addresses       []TrackedTxStatusAddress
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
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// TrackedTxStatusAddress is one watched address linked to a durable txstatus row.
type TrackedTxStatusAddress struct {
	Address   string
	AmountSat int64
}

// GetTxStatusInput is the input type for a single live tx lookup.
type GetTxStatusInput struct {
	UserID string
	TxID   string
}

// GetTxStatusBatchInput is the input type for a live batch status lookup.
type GetTxStatusBatchInput struct {
	UserID string
	TxIDs  []string
}

// CreateTrackedTxStatusInput creates one explicit txid-tracking record.
type CreateTrackedTxStatusInput struct {
	UserID  string
	Network string
	TxID    string
	Address *string
}

// UpdateTrackedTxStatusInput refreshes one explicit txid-tracking row and may
// replace its optional associated address.
type UpdateTrackedTxStatusInput struct {
	ID      int64
	UserID  string
	Address *string
}

// GetTrackedTxStatusInput selects one durable txstatus row by ID.
type GetTrackedTxStatusInput struct {
	ID     int64
	UserID string
}

// ListTrackedTxStatusesInput filters the durable txstatus list.
type ListTrackedTxStatusesInput struct {
	UserID         string
	Network        string
	Address        string
	TxID           string
	TrackingMode   string
	BeforeSortTime *time.Time
	BeforeID       int64
	Limit          int
}

// DeleteTrackedTxStatusInput deletes one durable txstatus row.
type DeleteTrackedTxStatusInput struct {
	ID     int64
	UserID string
}
