package rpc

import (
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// RPCRecorder is the narrow observability interface required by Client.
//
// *telemetry.Registry satisfies this interface structurally via the hook
// methods in internal/platform/telemetry/bitcoin_hooks.go — pass deps.Metrics
// directly; no factory method is needed. Pass nil to silence all metrics.
//
// Label safety — all label values come from bounded constant sets in client.go.
// Never pass user input, error message strings, or dynamic values as labels.
//
// Estimated cardinality per metric:
//
//	bitcoin_rpc_calls_total:             16 methods × 2 statuses    = 32 series
//	bitcoin_rpc_duration_seconds:        16 methods                 = 16 series
//	bitcoin_rpc_errors_total:            16 methods × 7 error types = 112 series
//	bitcoin_keypool_size:                gauge, no labels            = 1 series
type RPCRecorder interface {
	// OnRPCCall fires after every RPC method returns.
	// method must be one of the rpcMethod* constants.
	// status must be RPCStatusSuccess or RPCStatusError.
	// durationSeconds is the wall-clock elapsed time for the call.
	OnRPCCall(method, status string, durationSeconds float64)

	// OnRPCError classifies a failed call by method and error type.
	// errorType must be one of the RPCErr* constants.
	// Called in addition to OnRPCCall when status is RPCStatusError.
	OnRPCError(method, errorType string)

	// SetRPCConnected sets bitcoin_rpc_connected to 1 (reachable) or 0 (unreachable).
	//
	// Called from two sources:
	//   1. GetBlockchainInfo — the designated liveness probe; the only method that
	//      affirmatively sets the gauge to 1.
	//   2. call() — proactively sets the gauge to 0 on any RPCErrNetwork or
	//      RPCErrTimeout error from any method, providing sub-liveness-interval
	//      disconnection detection without waiting for the next probe tick.
	//
	// Implementations must be safe to call from multiple goroutines concurrently.
	SetRPCConnected(connected bool)

	// SetKeypoolSize records the current pre-generated address pool depth.
	// The keypool monitoring job calls this after every GetWalletInfo call.
	// Triggers WARNING alert below 100 and CRITICAL alert below 10.
	SetKeypoolSize(size int)
}

// compile-time check that *telemetry.Registry satisfies RPCRecorder.
var _ RPCRecorder = (*telemetry.Registry)(nil)

// noopRPCRecorder discards all metric calls.
// Substituted automatically when New() receives a nil recorder.
type noopRPCRecorder struct{}

func (noopRPCRecorder) OnRPCCall(_, _ string, _ float64) {}
func (noopRPCRecorder) OnRPCError(_, _ string)            {}
func (noopRPCRecorder) SetRPCConnected(_ bool)            {}
func (noopRPCRecorder) SetKeypoolSize(_ int)              {}
