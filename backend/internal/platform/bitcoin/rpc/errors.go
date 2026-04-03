package rpc

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ── Status label constants ────────────────────────────────────────────────────

// RPCStatus labels used by RPCRecorder.OnRPCCall.
type RPCStatus string

const (
	// RPCStatusSuccess indicates the RPC call completed without error.
	RPCStatusSuccess RPCStatus = "success"
	// RPCStatusError indicates the RPC call returned an error.
	RPCStatusError RPCStatus = "error"
)

// String returns the string representation of the status label.
func (s RPCStatus) String() string { return string(s) }

// ── Error type label constants ────────────────────────────────────────────────

// RPCErrType labels used by RPCRecorder.OnRPCError and classifyError.
type RPCErrType string

const (
	// RPCErrNotFound indicates a Bitcoin Core "not found" response (code -5).
	RPCErrNotFound RPCErrType = "not_found"
	// RPCErrPruned indicates the requested block data was pruned from the node.
	RPCErrPruned RPCErrType = "pruned"
	// RPCErrRPC indicates a structured Bitcoin Core RPC error (non -5, non -1).
	RPCErrRPC RPCErrType = "rpc_error"
	// RPCErrNetwork indicates a transport-level failure (connection refused, dropped, HTTP 5xx).
	RPCErrNetwork RPCErrType = "network"
	// RPCErrTimeout indicates the call exceeded the context deadline.
	RPCErrTimeout RPCErrType = "timeout"
	// RPCErrCanceled indicates the caller cancelled the context.
	RPCErrCanceled RPCErrType = "canceled"
	// RPCErrUnknown indicates an unclassifiable error (marshal/unmarshal failure, protocol error).
	RPCErrUnknown RPCErrType = "unknown"
)

// String returns the string representation of the error type label.
func (t RPCErrType) String() string { return string(t) }

// ── RPCError ──────────────────────────────────────────────────────────────────

// RPCError represents a JSON-RPC error returned by Bitcoin Core.
// Exported so callers can use errors.As to inspect the code.
//
// Notable codes:
//
//	-5:  "No such wallet transaction" / "Transaction not in mempool" — normal absence.
//	-8:  Invalid parameter.
//	-18: No wallet loaded.
//	-25: Insufficient funds (WalletCreateFundedPSBT).
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return "bitcoin rpc error " + strconv.Itoa(e.Code) + ": " + e.Message
}

// httpStatusError is returned when Bitcoin Core (or a proxy) responds with an
// unexpected HTTP status code (e.g. 503, 502, 429). ClassifyError matches this
// type and returns RPCErrNetwork so retryCall will retry these transient errors.
type httpStatusError struct {
	StatusCode int
}

func (e *httpStatusError) Error() string {
	return "unexpected HTTP " + strconv.Itoa(e.StatusCode) + " from Bitcoin Core — check node logs for details"
}

// ── Error inspection helpers ──────────────────────────────────────────────────

// IsNoWalletError reports whether err is a Bitcoin Core "no wallet loaded" error (code -18).
func IsNoWalletError(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == -18
}

// IsNotFoundError reports whether err is a Bitcoin Core "not found" error (code -5).
// Both GetTransaction ("No such wallet transaction") and GetMempoolEntry
// ("Transaction not in mempool") use code -5 for the normal absent response.
func IsNotFoundError(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == -5
}

// IsPrunedBlockError reports whether err indicates the requested block's data
// was pruned from this node's local storage.
//
// Bitcoin Core returns code -1 for pruned-block errors. The code is checked
// first; the string fallback handles transitive error wrapping and any future
// message rephrasing by Bitcoin Core.
func IsPrunedBlockError(err error) bool {
	if err == nil {
		return false
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.Code == -1 &&
			(strings.Contains(rpcErr.Message, "pruned data") ||
				strings.Contains(rpcErr.Message, "Block not available"))
	}
	// Fallback for plain error wrapping or future message rephrasing.
	msg := err.Error()
	return strings.Contains(msg, "pruned data") || strings.Contains(msg, "Block not available")
}

// IsConflicting reports whether tx was displaced by a chain reorganisation.
// Negative Confirmations means the transaction is in a block that is no longer
// on the active chain. The settlement engine must treat this as a reorg event
// rather than as an unconfirmed mempool transaction.
func IsConflicting(tx WalletTx) bool {
	return tx.Confirmations < 0
}

// classifyError maps a call error to one of the RPCErr* constants used as the
// error_type label in bitcoin_rpc_errors_total.
//
// Context cancellation and deadline checks come first — after errors.Join
// (used in retryCall), both the context error and the last RPC error are in
// the chain. Checking context first ensures metrics and logging always reflect
// the cancellation, not the stale RPC error.
func classifyError(err error) RPCErrType {
	if err == nil {
		return ""
	}
	// Context checks first — they take precedence over all other error types.
	if errors.Is(err, context.DeadlineExceeded) {
		return RPCErrTimeout
	}
	if errors.Is(err, context.Canceled) {
		return RPCErrCanceled
	}
	if IsNotFoundError(err) {
		return RPCErrNotFound
	}
	if IsPrunedBlockError(err) {
		return RPCErrPruned
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return RPCErrRPC
	}
	// io.EOF and io.ErrUnexpectedEOF indicate a dropped connection — classify
	// as network so retryCall will retry them.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return RPCErrNetwork
	}
	// httpStatusError indicates an unexpected HTTP status code (503 Service
	// Unavailable, 502 Bad Gateway, etc.) — these are transient infrastructure
	// failures and should be retried.
	var httpErr *httpStatusError
	if errors.As(err, &httpErr) {
		return RPCErrNetwork
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return RPCErrTimeout
		}
		return RPCErrNetwork
	}
	return RPCErrUnknown
}

// rpcErrorAttrs extracts rpc_code and rpc_message from an *RPCError in the
// error chain and returns them as a flat key-value slice suitable for slog.
// Returns an empty slice when the error is not (or does not wrap) an *RPCError,
// so callers can always splat the result into a log call without a nil check.
func rpcErrorAttrs(err error) []any {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return []any{"rpc_code", rpcErr.Code, "rpc_message", rpcErr.Message}
	}
	return nil
}

// sanitizeError strips the URL from an error message to prevent leaking the
// loopback address and port in error strings returned to callers.
func sanitizeError(err error) error {
	// *url.Error wraps the real error and includes the full URL in its
	// Error() output. Extract the inner error message only.
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return errors.New(urlErr.Err.Error())
	}
	return err
}
