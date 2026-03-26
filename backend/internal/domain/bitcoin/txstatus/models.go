// Package txstatus provides the HTTP handler, service, and port interfaces
// for resolving the on-chain status of Bitcoin transactions.
package txstatus

// TxStatus is the resolved on-chain status of a Bitcoin transaction.
type TxStatus string

const (
	// TxStatusConfirmed means the transaction has at least one confirmation.
	// Clients are responsible for applying their own confirmation-count threshold.
	// Note: this status reflects the node's current view and may be transiently
	// stale during chain reorganisations.
	TxStatusConfirmed TxStatus = "confirmed"

	// TxStatusMempool means the transaction is in the node's public mempool
	// with zero confirmations.
	TxStatusMempool TxStatus = "mempool"

	// TxStatusNotFound means the transaction is unknown to the wallet and
	// absent from the node's public mempool.
	TxStatusNotFound TxStatus = "not_found"

	// TxStatusConflicting means the transaction conflicts with a confirmed block
	// (Confirmations == -1 in Bitcoin Core). It was double-spent or replaced
	// and will never confirm.
	TxStatusConflicting TxStatus = "conflicting"

	// TxStatusAbandoned means the wallet has explicitly abandoned this transaction
	// (Confirmations == -2 in Bitcoin Core). The node considers it permanently dead.
	TxStatusAbandoned TxStatus = "abandoned"
)

// TxStatusResult holds the resolved status of a single Bitcoin transaction.
//
// Confirmations and BlockHeight are only meaningful when Status is
// TxStatusConfirmed or TxStatusMempool (where Confirmations is always 0).
type TxStatusResult struct {
	Status        TxStatus
	Confirmations int
	BlockHeight   int
}

// GetTxStatusInput is the input type for a single-transaction status lookup.
type GetTxStatusInput struct {
	TxID string
}

// GetTxStatusBatchInput is the input type for a batch status lookup.
type GetTxStatusBatchInput struct {
	TxIDs []string
}
