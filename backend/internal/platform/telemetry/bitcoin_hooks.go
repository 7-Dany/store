package telemetry

import "strconv"

// Bitcoin hook methods implement the bitcoinshared.BitcoinRecorder interface
// structurally. The interface is defined in domain/bitcoin/shared/recorder.go;
// *Registry satisfies it without importing that package (no import cycle).
//
// All methods are nil-safe: calling them on a nil *Registry is a no-op.
// Stage 2+ fields have an additional nil guard for the gauge itself, so the
// package compiles and runs correctly in Stage 0 deployments.

// ── Stage 0 — ZMQ infrastructure ─────────────────────────────────────────────

// SetZMQConnected sets bitcoin_zmq_connected to 1 (connected) or 0 (disconnected).
func (r *Registry) SetZMQConnected(connected bool) {
	if r == nil {
		return
	}
	if connected {
		r.bitcoinZMQConnected.Set(1)
	} else {
		r.bitcoinZMQConnected.Set(0)
	}
}

// SetRPCConnected sets bitcoin_rpc_connected to 1 (connected) or 0 (disconnected).
func (r *Registry) SetRPCConnected(connected bool) {
	if r == nil {
		return
	}
	if connected {
		r.bitcoinRPCConnected.Set(1)
	} else {
		r.bitcoinRPCConnected.Set(0)
	}
}

// SetZMQLastMessageAge records the seconds elapsed since the last ZMQ message.
func (r *Registry) SetZMQLastMessageAge(seconds float64) {
	if r == nil {
		return
	}
	r.bitcoinZMQLastMessageAge.Set(seconds)
}

// OnHandlerPanic increments the bitcoin_handler_panics_total counter for the
// named handler.
func (r *Registry) OnHandlerPanic(handler string) {
	if r == nil {
		return
	}
	r.bitcoinHandlerPanics.WithLabelValues(handler).Inc()
}

// OnHandlerTimeout increments the bitcoin_handler_timeouts_total counter for
// the named handler. Called when a handler's context expires before the handler
// returns — the goroutine continues running but the worker slot is released.
func (r *Registry) OnHandlerTimeout(handler string) {
	if r == nil {
		return
	}
	r.bitcoinHandlerTimeouts.WithLabelValues(handler).Inc()
}

// SetHandlerGoroutines records the current number of in-flight ZMQ handler goroutines.
func (r *Registry) SetHandlerGoroutines(count int) {
	if r == nil {
		return
	}
	r.bitcoinHandlerGoroutines.Set(float64(count))
}

// OnMessageDropped increments dropped_zmq_messages_total for the given reason.
func (r *Registry) OnMessageDropped(reason string) {
	if r == nil {
		return
	}
	r.bitcoinDroppedMessages.WithLabelValues(reason).Inc()
}

// SetSSEConnections records the number of active Bitcoin SSE connections.
func (r *Registry) SetSSEConnections(count int) {
	if r == nil {
		return
	}
	r.bitcoinSSEConnections.Set(float64(count))
}

// OnTokenConsumeFailed increments bitcoin_token_consume_failures_total for the given reason.
func (r *Registry) OnTokenConsumeFailed(reason string) {
	if r == nil {
		return
	}
	r.bitcoinTokenConsumeFailures.WithLabelValues(reason).Inc()
}

// OnTokenIssuanceDBMiss increments bitcoin_token_issuance_db_miss_total.
// Called when RecordTokenIssuance fails to write the sse_token_issuances row,
// indicating a GDPR IP-audit gap for this token issuance.
func (r *Registry) OnTokenIssuanceDBMiss() {
	if r == nil {
		return
	}
	r.bitcoinTokenIssuanceDBMiss.Inc()
}

// SetPendingMempoolSize records the current size of the in-process pending mempool map.
func (r *Registry) SetPendingMempoolSize(n int) {
	if r == nil {
		return
	}
	r.bitcoinPendingMempoolSize.Set(float64(n))
}

// OnMempoolEntryDropped increments bitcoin_mempool_entry_dropped_total for the given reason.
// Called when a pendingMempool insert is skipped because the cap has been reached.
func (r *Registry) OnMempoolEntryDropped(reason string) {
	if r == nil {
		return
	}
	r.bitcoinMempoolEntryDropped.WithLabelValues(reason).Inc()
}

// OnRBFDetected increments bitcoin_rbf_detected_total.
// Called when a Replace-By-Fee replacement is detected in the mempool tracker.
func (r *Registry) OnRBFDetected() {
	if r == nil {
		return
	}
	r.bitcoinRBFDetected.Inc()
}

// OnMempoolPruned adds count to bitcoin_mempool_pruned_total.
// Called after pruneOldEntries removes stale entries from the pending mempool map.
func (r *Registry) OnMempoolPruned(count int) {
	if r == nil {
		return
	}
	r.bitcoinMempoolPruned.Add(float64(count))
}

// ── Stage 2a — Invoice ────────────────────────────────────────────────────────

// OnInvoiceDetected records the detection latency in the invoice detection histogram.
func (r *Registry) OnInvoiceDetected(durationSeconds float64) {
	if r == nil || r.bitcoinInvoiceDetection == nil {
		return
	}
	r.bitcoinInvoiceDetection.Observe(durationSeconds)
}

// SetInvoiceCount sets the current invoice count gauge for the given status.
func (r *Registry) SetInvoiceCount(status string, count float64) {
	if r == nil || r.bitcoinInvoiceState == nil {
		return
	}
	r.bitcoinInvoiceState.WithLabelValues(status).Set(count)
}

// SetRateFeedStaleness records the seconds since the last exchange rate update.
func (r *Registry) SetRateFeedStaleness(seconds float64) {
	if r == nil || r.bitcoinRateFeedStaleness == nil {
		return
	}
	r.bitcoinRateFeedStaleness.Set(seconds)
}

// SetReconciliationLag records the number of blocks the reconciliation job is
// behind the chain tip.
func (r *Registry) SetReconciliationLag(blocks float64) {
	if r == nil || r.bitcoinReconciliationLag == nil {
		return
	}
	r.bitcoinReconciliationLag.Set(blocks)
}

// ── Stage 2b — Settlement ─────────────────────────────────────────────────────

// SetBalanceDrift records the accounting drift in satoshis.
// Must be zero at all times. Any nonzero value triggers a CRITICAL alert.
func (r *Registry) SetBalanceDrift(satoshis int64) {
	if r == nil || r.bitcoinBalanceDrift == nil {
		return
	}
	r.bitcoinBalanceDrift.Set(float64(satoshis))
}

// SetReconciliationHold sets the reconciliation hold gauge to 1 (active) or 0 (inactive).
func (r *Registry) SetReconciliationHold(active bool) {
	if r == nil || r.bitcoinReconciliationHold == nil {
		return
	}
	if active {
		r.bitcoinReconciliationHold.Set(1)
	} else {
		r.bitcoinReconciliationHold.Set(0)
	}
}

// OnReorgDetected increments bitcoin_reorg_detected_total.
func (r *Registry) OnReorgDetected() {
	if r == nil || r.bitcoinReorgDetected == nil {
		return
	}
	r.bitcoinReorgDetected.Inc()
}

// ── Stage 2c — Payouts ────────────────────────────────────────────────────────

// OnPayoutFailed increments bitcoin_payout_failure_total.
func (r *Registry) OnPayoutFailed() {
	if r == nil || r.bitcoinPayoutFailures == nil {
		return
	}
	r.bitcoinPayoutFailures.Inc()
}

// SetFeeEstimate records the current fee estimate for the given confirmation target.
// targetBlocks is converted to a string label; use small bounded values (1, 3, 6, etc.).
func (r *Registry) SetFeeEstimate(targetBlocks int, satPerVbyte float64) {
	if r == nil || r.bitcoinFeeEstimate == nil {
		return
	}
	r.bitcoinFeeEstimate.WithLabelValues(strconv.Itoa(targetBlocks)).Set(satPerVbyte)
}

// OnSweepStuck increments bitcoin_sweep_stuck_total.
func (r *Registry) OnSweepStuck() {
	if r == nil || r.bitcoinSweepStuck == nil {
		return
	}
	r.bitcoinSweepStuck.Inc()
}

// SetWalletBackupAge records the seconds since the last successful wallet backup.
func (r *Registry) SetWalletBackupAge(seconds float64) {
	if r == nil || r.bitcoinWalletBackupAge == nil {
		return
	}
	r.bitcoinWalletBackupAge.Set(seconds)
}

// SetUTXOCount records the current number of UTXOs in the Bitcoin wallet.
func (r *Registry) SetUTXOCount(count float64) {
	if r == nil || r.bitcoinUTXOCount == nil {
		return
	}
	r.bitcoinUTXOCount.Set(count)
}

// ── Bitcoin RPC ───────────────────────────────────────────────────────────────

// OnRPCCall records the completion of a single Bitcoin Core RPC call.
//
// method must be one of the bounded set of known RPC method names
// (e.g. "gettransaction", "getblockchaininfo"). It is used as a Prometheus
// label — never pass user input or dynamic strings.
// status must be "success" or "error".
// durationSeconds is the wall-clock time the call took including HTTP round-trip.
func (r *Registry) OnRPCCall(method, status string, durationSeconds float64) {
	if r == nil {
		return
	}
	if r.bitcoinRPCCallsTotal != nil {
		r.bitcoinRPCCallsTotal.WithLabelValues(method, status).Inc()
	}
	if r.bitcoinRPCDuration != nil {
		r.bitcoinRPCDuration.WithLabelValues(method).Observe(durationSeconds)
	}
}

// OnRPCError records a classified RPC error for the given method.
//
// errorType must be one of the bounded set of error classifications:
//   - "not_found"  — Bitcoin Core returned code -5 (no such wallet tx / not in mempool)
//   - "pruned"     — requested block data has been pruned from the node
//   - "rpc_error"  — Bitcoin Core returned a non-(-5) RPC error code
//   - "network"    — HTTP transport failure (connection refused, reset, etc.)
//   - "timeout"    — context deadline exceeded before the call completed
//   - "canceled"   — context was cancelled (e.g. graceful shutdown); call() still records
//     the error metric so Prometheus captures the cancellation rate
//   - "unknown"    — none of the above (marshal/unmarshal failure, etc.)
func (r *Registry) OnRPCError(method, errorType string) {
	if r == nil || r.bitcoinRPCErrorsTotal == nil {
		return
	}
	r.bitcoinRPCErrorsTotal.WithLabelValues(method, errorType).Inc()
}

// SetKeypoolSize records the current keypool depth reported by getwalletinfo.
// The keypool monitoring job calls this after every GetWalletInfo call.
func (r *Registry) SetKeypoolSize(size int) {
	if r == nil || r.bitcoinKeypoolSize == nil {
		return
	}
	r.bitcoinKeypoolSize.Set(float64(size))
}

// ── Watch ─────────────────────────────────────────────────────────────────────

// OnWatchRejected increments bitcoin_watch_rejected_total{reason}.
// reason is one of: "rate_limit", "invalid_address", "limit_exceeded",
// "registration_window_expired". These are bounded constants — never pass
// dynamic or user-controlled values.
func (r *Registry) OnWatchRejected(reason string) {
	if r == nil || r.bitcoinWatchRejected == nil {
		return
	}
	r.bitcoinWatchRejected.WithLabelValues(reason).Inc()
}

// SetGlobalWatchCountEstimate sets bitcoin_global_watch_count_estimate{network}
// to the reconciled total from the 15-minute SCAN goroutine.
// network is "testnet4" or "mainnet".
func (r *Registry) SetGlobalWatchCountEstimate(network string, count float64) {
	if r == nil || r.bitcoinGlobalWatchCountEstimate == nil {
		return
	}
	r.bitcoinGlobalWatchCountEstimate.WithLabelValues(network).Set(count)
}

// ── Bitcoin TxStatus ───────────────────────────────────────────────────

// OnTxStatusResolved increments bitcoin_txstatus_resolved_total{status}.
//
// status must be one of the TxStatus* constant string values defined in the
// txstatus package: "confirmed", "mempool", "not_found", "conflicting", "abandoned".
func (r *Registry) OnTxStatusResolved(status string) {
	if r == nil {
		return
	}
	r.bitcoinTxStatusResolved.WithLabelValues(status).Inc()
}
