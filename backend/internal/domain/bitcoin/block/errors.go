package block

import "errors"

var (
	// ErrRPCUnavailable is returned when the Bitcoin Core RPC endpoint is
	// unreachable or returns an unexpected infrastructure error.
	ErrRPCUnavailable = errors.New("bitcoin rpc unavailable")

	// ErrBlockNotFound is returned when Bitcoin Core responds with RPC error -5
	// for the requested block hash.
	ErrBlockNotFound = errors.New("bitcoin block not found")
)
