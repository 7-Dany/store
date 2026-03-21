// Package bitcoinshared holds primitives shared across all Bitcoin domain sub-packages.
// It must never import any Bitcoin feature sub-package.
package bitcoinshared

// BitcoinRecorder is the observability interface for all Bitcoin domain events.
//
// *telemetry.Registry satisfies this interface structurally via the hook methods
// in internal/platform/telemetry/bitcoin_hooks.go. Pass deps.Metrics directly
// from server.New — no factory method needed.
//
// All implementations must be safe for concurrent use.
//
// Compile-time structural assertion in server.New:
//
//	var _ bitcoinshared.BitcoinRecorder = (*telemetry.Registry)(nil)
type BitcoinRecorder interface {
	// ── Stage 0 — ZMQ infrastructure ─────────────────────────────────────

	// SetZMQConnected sets the ZMQ connectivity gauge (1=connected, 0=disconnected).
	SetZMQConnected(connected bool)
	// SetRPCConnected sets the RPC connectivity gauge (1=connected, 0=disconnected).
	SetRPCConnected(connected bool)
	// SetZMQLastMessageAge records seconds elapsed since the last ZMQ message.
	SetZMQLastMessageAge(seconds float64)
	// OnHandlerPanic increments the handler panic counter for the named handler.
	OnHandlerPanic(handler string)
	// OnHandlerTimeout increments the handler timeout counter for the named handler.
	// Called when a handler's context deadline expires before the handler returns.
	// The goroutine continues running until it honours ctx.Done().
	OnHandlerTimeout(handler string)
	// SetHandlerGoroutines records the current number of in-flight handler goroutines.
	SetHandlerGoroutines(count int)
	// OnMessageDropped increments the dropped ZMQ messages counter for the given reason.
	OnMessageDropped(reason string)
	// SetSSEConnections records the current number of active SSE connections.
	SetSSEConnections(count int)
	// OnTokenConsumeFailed increments the SSE token consume failure counter.
	OnTokenConsumeFailed(reason string)

	// ── Stage 2a — Invoice ────────────────────────────────────────────────

	// OnInvoiceDetected observes the invoice detection latency histogram.
	OnInvoiceDetected(durationSeconds float64)
	// SetInvoiceCount sets the invoice count gauge for the given status label.
	SetInvoiceCount(status string, count float64)
	// SetRateFeedStaleness records seconds since the last exchange rate update.
	SetRateFeedStaleness(seconds float64)
	// SetReconciliationLag records blocks the reconciliation job is behind chain tip.
	SetReconciliationLag(blocks float64)

	// ── Stage 2b — Settlement ─────────────────────────────────────────────

	// SetBalanceDrift records the accounting drift in satoshis. Must be zero.
	SetBalanceDrift(satoshis int64)
	// SetReconciliationHold sets the sweep-hold mode gauge (1=active, 0=inactive).
	SetReconciliationHold(active bool)
	// OnReorgDetected increments the reorg detected counter.
	OnReorgDetected()

	// ── Stage 2c — Payouts ────────────────────────────────────────────────

	// OnPayoutFailed increments the payout failure counter.
	OnPayoutFailed()
	// SetFeeEstimate records the current fee estimate for the given confirmation target.
	SetFeeEstimate(targetBlocks int, satPerVbyte float64)
	// OnSweepStuck increments the sweep stuck counter.
	OnSweepStuck()
	// SetWalletBackupAge records seconds since the last successful wallet backup.
	SetWalletBackupAge(seconds float64)
	// SetUTXOCount records the current number of UTXOs in the wallet.
	SetUTXOCount(count float64)
}

// ── NoopBitcoinRecorder ───────────────────────────────────────────────────────

// NoopBitcoinRecorder satisfies [BitcoinRecorder] with empty method bodies.
// Use in Bitcoin domain unit tests that do not need metric assertions.
type NoopBitcoinRecorder struct{}

func (NoopBitcoinRecorder) SetZMQConnected(bool)            {}
func (NoopBitcoinRecorder) SetRPCConnected(bool)            {}
func (NoopBitcoinRecorder) SetZMQLastMessageAge(float64)    {}
func (NoopBitcoinRecorder) OnHandlerPanic(string)           {}
func (NoopBitcoinRecorder) OnHandlerTimeout(string)         {}
func (NoopBitcoinRecorder) SetHandlerGoroutines(int)        {}
func (NoopBitcoinRecorder) OnMessageDropped(string)         {}
func (NoopBitcoinRecorder) SetSSEConnections(int)           {}
func (NoopBitcoinRecorder) OnTokenConsumeFailed(string)     {}
func (NoopBitcoinRecorder) OnInvoiceDetected(float64)       {}
func (NoopBitcoinRecorder) SetInvoiceCount(string, float64) {}
func (NoopBitcoinRecorder) SetRateFeedStaleness(float64)    {}
func (NoopBitcoinRecorder) SetReconciliationLag(float64)    {}
func (NoopBitcoinRecorder) SetBalanceDrift(int64)           {}
func (NoopBitcoinRecorder) SetReconciliationHold(bool)      {}
func (NoopBitcoinRecorder) OnReorgDetected()                {}
func (NoopBitcoinRecorder) OnPayoutFailed()                 {}
func (NoopBitcoinRecorder) SetFeeEstimate(int, float64)     {}
func (NoopBitcoinRecorder) OnSweepStuck()                   {}
func (NoopBitcoinRecorder) SetWalletBackupAge(float64)      {}
func (NoopBitcoinRecorder) SetUTXOCount(float64)            {}

// Compile-time assertion: NoopBitcoinRecorder must satisfy BitcoinRecorder.
// NoopBitcoinRecorder is a struct, not a pointer, so use a zero value — not nil.
var _ BitcoinRecorder = NoopBitcoinRecorder{}
