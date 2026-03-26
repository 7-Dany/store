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
)
