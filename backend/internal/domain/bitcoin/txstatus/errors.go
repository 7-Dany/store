package txstatus

import "errors"

var (
	// ErrRPCUnavailable is returned when the Bitcoin Core RPC endpoint is
	// unreachable or returns an unexpected infrastructure error.
	ErrRPCUnavailable = errors.New("bitcoin rpc unavailable")

	// ErrWalletNotLoaded is returned when Bitcoin Core responds with RPC error
	// -18 ("No wallet is loaded"). This is a node misconfiguration — the wallet
	// must be loaded via loadwallet or createwallet before transaction queries
	// can be served.
	ErrWalletNotLoaded = errors.New("bitcoin wallet not loaded")

	// ErrTrackedTxStatusNotFound is returned when a durable txstatus row does not exist.
	ErrTrackedTxStatusNotFound = errors.New("tracked tx status not found")

	// ErrTrackedTxStatusExists is returned when a duplicate explicit txid tracking row exists.
	ErrTrackedTxStatusExists = errors.New("tracked tx status already exists")

	// ErrWatchManagedTrackedTxStatus is returned when a caller attempts to update a
	// watch-managed row through the explicit txstatus CRUD update path.
	ErrWatchManagedTrackedTxStatus = errors.New("tracked tx status is managed by watch events")
)
