// Package rpc provides a thin JSON-RPC client for the Bitcoin Core HTTP API.
//
// It translates Go method calls into HTTP POST requests to Bitcoin Core's RPC
// endpoint and parses the JSON responses into typed Go structs. Every domain
// package that needs Bitcoin network data calls through this client.
//
// Design constraints:
//   - Zero domain imports — this is a pure platform concern.
//   - Credential safety: user/pass are stored in an unexported type whose
//     Stringer returns "[redacted]", making accidental logging impossible.
//   - BTC-to-satoshi precision: all BTC amounts are typed as the unexported
//     btcRawAmount; callers must use BtcToSat() for conversion.
//   - No txindex dependency on wallet/mempool paths: wallet-native RPCs
//     (gettransaction, getaddressinfo, getrawtransaction for mempool, etc.)
//     work without txindex=1. Block-hash readers still depend on the node
//     retaining the referenced block data.
//   - Full observability: every call is metered via RPCRecorder (recorder.go).
//     Pass deps.Metrics directly — *telemetry.Registry satisfies the interface.
//   - Host safety: New() panics if the host is not a loopback address. RPC
//     credentials must never be transmitted over a non-loopback interface.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// ── Package-level logger ──────────────────────────────────────────────────────

// logger is the structured logger for this package. All records carry component="rpc".
var logger = telemetry.New("rpc")

// ── Transport constants ───────────────────────────────────────────────────────

const (
	// rpcDialTimeout is the maximum time allowed for the TCP connect phase.
	rpcDialTimeout = 5 * time.Second

	// rpcTLSHandshakeTimeout is defensive: RPC uses plain HTTP today, but the
	// transport is ready if TLS is ever added.
	rpcTLSHandshakeTimeout = 5 * time.Second

	// rpcResponseHeaderTimeout is the maximum time Bitcoin Core may hold an open
	// TCP connection without sending a response header. Without this, a node under
	// memory pressure can stall the call indefinitely even after the context
	// deadline fires, because ResponseHeaderTimeout operates at the transport
	// layer independently of context cancellation.
	rpcResponseHeaderTimeout = 10 * time.Second

	// rpcIdleConnTimeout matches http.DefaultTransport.
	rpcIdleConnTimeout = 90 * time.Second

	// rpcMaxIdleConnsPerHost is the keep-alive pool size for one Bitcoin Core node.
	// Bounded so the pool never grows unbounded under burst concurrency.
	rpcMaxIdleConnsPerHost = 4

	// rpcMaxResponseBytes is the hard cap on response body size. Bitcoin Core's
	// largest legitimate response is a verbosity=2 mainnet block at roughly 4 MiB;
	// 8 MiB is generous. Without this cap, a misbehaving or malicious node could
	// exhaust process memory via io.ReadAll.
	rpcMaxResponseBytes = 8 << 20 // 8 MiB
)

// ── Retry constants ───────────────────────────────────────────────────────────

const (
	// rpcRetryBase is the initial backoff before the first retry attempt.
	rpcRetryBase = 1 * time.Second

	// rpcRetryCeiling caps the maximum backoff between retries. Kept shorter than
	// the ZMQ ceiling because RPC callers supply their own context deadlines.
	rpcRetryCeiling = 30 * time.Second

	// rpcMaxRetries is the number of additional attempts after the first call.
	// Total maximum calls per retryCall invocation = 1 + rpcMaxRetries.
	rpcMaxRetries = 4
)

// ── Status label constants ────────────────────────────────────────────────────

const (
	RPCStatusSuccess = "success"
	RPCStatusError   = "error"
)

// ── Error type label constants ────────────────────────────────────────────────

const (
	RPCErrNotFound = "not_found"
	RPCErrPruned   = "pruned"
	RPCErrRPC      = "rpc_error"
	RPCErrNetwork  = "network"
	RPCErrTimeout  = "timeout"
	RPCErrCanceled = "canceled"
	RPCErrUnknown  = "unknown"
)

// ── Method name constants ─────────────────────────────────────────────────────

const (
	rpcMethodGetBlockchainInfo      = "getblockchaininfo"
	rpcMethodGetBlockHeader         = "getblockheader"
	rpcMethodGetBlock               = "getblock"
	rpcMethodGetBlockHash           = "getblockhash"
	rpcMethodGetBlockCount          = "getblockcount"
	rpcMethodGetTransaction         = "gettransaction"
	rpcMethodGetNewAddress          = "getnewaddress"
	rpcMethodGetAddressInfo         = "getaddressinfo"
	rpcMethodGetMempoolEntry        = "getmempoolentry"
	rpcMethodGetWalletInfo          = "getwalletinfo"
	rpcMethodKeypoolRefill          = "keypoolrefill"
	rpcMethodEstimateSmartFee       = "estimatesmartfee"
	rpcMethodWalletCreateFundedPSBT = "walletcreatefundedpsbt"
	rpcMethodWalletProcessPSBT      = "walletprocesspsbt"
	rpcMethodFinalizePSBT           = "finalizepsbt"
	rpcMethodSendRawTransaction     = "sendrawtransaction"
	rpcMethodGetRawTransaction      = "getrawtransaction"
)

// ── Invoice address constants ─────────────────────────────────────────────────

const InvoiceAddressLabel = "invoice"
const InvoiceAddressType = "bech32"

// ── Credential safety ─────────────────────────────────────────────────────────

// credential wraps an RPC credential string whose String() always returns
// "[redacted]", making accidental logging of the raw value impossible.
type credential string

func (c credential) String() string { return "[redacted]" }

// ── JSON-RPC envelope ─────────────────────────────────────────────────────────

// rpcRequest is the JSON-RPC request envelope sent to Bitcoin Core.
// The "jsonrpc" version field is intentionally omitted — Bitcoin Core v27+
// rejects "1.1" and "2.0" on some builds but always accepts the version-less
// format used by bitcoin-cli itself.
type rpcRequest struct {
	ID     int    `json:"id"`
	Method string `json:"method"`
	Params []any  `json:"params"`
}

// rpcResponse is the JSON-RPC response envelope from Bitcoin Core.
//
// ID is json.RawMessage rather than int because Bitcoin Core (and some test
// fixtures) may echo back a string ID (e.g. "btc") instead of the numeric 1
// we sent. We never inspect the echoed ID, so accepting any JSON value is safe
// and avoids spurious unmarshal errors on otherwise valid responses.
type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
	ID     json.RawMessage `json:"id"`
}

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

// ── Client interface ──────────────────────────────────────────────────────────

// Client is the read/write interface for Bitcoin Core's RPC API.
// Depend on this interface in domain packages — never on the concrete *client
// directly. This decouples domain packages from the platform layer and makes
// them trivially testable with a mock.
type Client interface {
	GetBlockchainInfo(ctx context.Context) (BlockchainInfo, error)
	GetBlockHeader(ctx context.Context, hash string) (BlockHeader, error)
	GetBlock(ctx context.Context, hash string, verbosity int) (json.RawMessage, error)
	GetBlockVerbose(ctx context.Context, hash string) (VerboseBlock, error)
	GetBlockHash(ctx context.Context, height int) (string, error)
	GetBlockCount(ctx context.Context) (int, error)
	GetTransaction(ctx context.Context, txid string, verbose bool) (WalletTx, error)
	GetNewAddress(ctx context.Context, label, addressType string) (string, error)
	GetAddressInfo(ctx context.Context, address string) (AddressInfo, error)
	GetMempoolEntry(ctx context.Context, txid string) (MempoolEntry, error)
	GetWalletInfo(ctx context.Context) (WalletInfo, error)
	KeypoolRefill(ctx context.Context, newSize int) error
	EstimateSmartFee(ctx context.Context, confTarget int, mode string) (FeeEstimate, error)
	WalletCreateFundedPSBT(ctx context.Context, outputs []map[string]any, options map[string]any) (FundedPSBT, error)
	WalletProcessPSBT(ctx context.Context, psbt string) (ProcessedPSBT, error)
	FinalizePSBT(ctx context.Context, psbt string) (FinalizedPSBT, error)
	SendRawTransaction(ctx context.Context, hexTx string, maxFeeRate float64) (string, error)
	GetRawTransaction(ctx context.Context, txid string, verbosity int) (RawTx, error)
	Close()
}

// ── Concrete client ───────────────────────────────────────────────────────────

// client is the concrete JSON-RPC implementation of Client. Unexported — callers
// depend on the interface. Safe for concurrent use; each call is an independent
// HTTP request. Keep-alive connections are managed by the embedded transport.
type client struct {
	baseURL   string
	user      credential
	pass      credential
	http      *http.Client
	transport *http.Transport // retained for Close() and transport-config tests
	recorder  RPCRecorder

	// retryBase and retryCeiling control the backoff used by retryCall.
	// They are set to rpcRetryBase / rpcRetryCeiling by New() and may be
	// overridden in tests (same package) to make retries instantaneous:
	//   c.retryBase = 0
	//   c.retryCeiling = 0
	retryBase    time.Duration
	retryCeiling time.Duration
}

// compile-time assertion that *client satisfies Client.
var _ Client = (*client)(nil)

// New creates a new RPC client and returns the Client interface.
//
// host must be a loopback address (127.x.x.x or ::1). New() panics if it is
// not — RPC credentials must never be transmitted over a non-loopback interface.
//
// port must be a numeric string in 1–65535.
//
// recorder may be nil; a no-op recorder is substituted automatically.
func New(host, port, user, pass string, recorder RPCRecorder) (Client, error) {
	// Panic at construction if host is not loopback. Misconfiguration fails
	// loudly at startup rather than silently on the first RPC call.
	requireLoopbackHost(host, "BTC_RPC_HOST")

	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return nil, telemetry.RPC("New.validate",
			errors.New("invalid RPC port \""+port+"\": must be numeric and in 1–65535"))
	}
	if recorder == nil {
		recorder = noopRPCRecorder{}
	}

	// Purpose-built HTTP transport with per-phase timeouts and a bounded
	// connection pool scoped to this client. Never shares http.DefaultTransport.
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   rpcDialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   rpcTLSHandshakeTimeout,
		ResponseHeaderTimeout: rpcResponseHeaderTimeout,
		IdleConnTimeout:       rpcIdleConnTimeout,
		MaxIdleConns:          rpcMaxIdleConnsPerHost,
		MaxIdleConnsPerHost:   rpcMaxIdleConnsPerHost,
		// Bitcoin Core responses are JSON text; compression wastes CPU and adds
		// latency without meaningful bandwidth savings on loopback.
		DisableCompression: true,
	}

	return &client{
		baseURL:      "http://" + host + ":" + port + "/",
		user:         credential(user),
		pass:         credential(pass),
		http:         &http.Client{Transport: transport},
		transport:    transport,
		recorder:     recorder,
		retryBase:    rpcRetryBase,
		retryCeiling: rpcRetryCeiling,
	}, nil
}

// Close releases idle keep-alive connections back to the OS.
// In-flight requests are not cancelled — callers own their contexts.
//
// Shutdown order in server.go:
//  1. HTTP server shutdown.
//  2. sub.Shutdown() — drain ZMQ handler goroutines.
//  3. rpcClient.Close() — drain keep-alive pool.
//  4. q.Shutdown() — drain mail queue.
//  5. pool.Close() — close DB pool.
func (c *client) Close() {
	c.transport.CloseIdleConnections()
	logger.Info(context.Background(), "rpc: client closed — idle connections released")
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
func classifyError(err error) string {
	if err == nil {
		return ""
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
	if errors.Is(err, context.DeadlineExceeded) {
		return RPCErrTimeout
	}
	if errors.Is(err, context.Canceled) {
		return RPCErrCanceled
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

// ── Internal RPC machinery ────────────────────────────────────────────────────

// call issues one JSON-RPC call and records metrics for the result.
//
// Log levels by error type:
//   - network / timeout / rpc_error → Warn
//   - not_found / pruned → Debug (expected absence, not a failure)
//   - canceled → silent in logs (still recorded in metrics; cancellations
//     are expected during graceful shutdown and are tracked in Prometheus)
//
// Network and timeout errors immediately flip bitcoin_rpc_connected to 0,
// providing faster disconnection detection than waiting for the next
// GetBlockchainInfo liveness probe tick.
//
// All log records include rpc_code and rpc_message when the error is a
// structured *RPCError from Bitcoin Core, making log searches by error code
// reliable (e.g. rpc_code=-5 for not-found, rpc_code=-18 for no wallet).
func (c *client) call(ctx context.Context, method string, params []any, out any) error {
	start := time.Now()
	err := c.doCall(ctx, method, params, out)
	elapsed := time.Since(start).Seconds()

	if err != nil {
		errType := classifyError(err)

		// Extract structured fields
		rpcAttrs := rpcErrorAttrs(err)

		// Base structured args (shared across all branches)
		baseArgs := []any{
			"method", method,
			"error_type", errType,
			"error", err,
		}

		switch errType {
		case RPCErrNotFound, RPCErrPruned:
			args := append(baseArgs, rpcAttrs...)
			logger.Debug(ctx, "rpc: expected absence", args...)

		case RPCErrCanceled:
			// Application or caller shutdown — not a failure worth logging.

		case RPCErrUnknown:
			if method == rpcMethodGetRawTransaction {
				args := append(baseArgs, rpcAttrs...)
				logger.Debug(ctx, "rpc: expected race (tx left mempool before call)", args...)
			} else {
				args := append(baseArgs,
					"elapsed_s", elapsed,
				)
				args = append(args, rpcAttrs...)
				logger.Warn(ctx, "rpc: call failed", args...)
			}

		default:
			args := append(baseArgs,
				"elapsed_s", elapsed,
			)
			args = append(args, rpcAttrs...)
			logger.Warn(ctx, "rpc: call failed", args...)
		}

		// Infrastructure health tracking
		if errType == RPCErrNetwork || errType == RPCErrTimeout {
			c.recorder.SetRPCConnected(false)
		}

		c.recorder.OnRPCCall(method, RPCStatusError, elapsed)
		c.recorder.OnRPCError(method, errType)
	} else {
		c.recorder.OnRPCCall(method, RPCStatusSuccess, elapsed)
	}

	return err
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

// retryCall wraps call() with exponential backoff for idempotent RPC methods.
//
// Retries only on RPCErrNetwork and RPCErrTimeout. Never retries:
//   - RPCErrRPC, RPCErrNotFound, RPCErrPruned — deterministic Bitcoin Core responses
//   - RPCErrCanceled — context cancelled; abort immediately
//   - RPCErrUnknown — marshal/unmarshal failures; retrying won't help
//
// Non-idempotent methods (SendRawTransaction, WalletCreateFundedPSBT,
// WalletProcessPSBT, FinalizePSBT, KeypoolRefill, GetNewAddress) call call()
// directly — their callers own retry semantics.
func (c *client) retryCall(ctx context.Context, method string, params []any, out any) error {
	backoff := c.retryBase

	for attempt := range rpcMaxRetries {
		err := c.call(ctx, method, params, out)
		if err == nil {
			return nil
		}

		errType := classifyError(err)

		if errType != RPCErrNetwork && errType != RPCErrTimeout {
			return err // deterministic error — no point retrying
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		logger.Warn(ctx, "rpc: transient error — retrying",
			"method", method,
			"attempt", attempt+1,
			"max_retries", rpcMaxRetries,
			"backoff", backoff,
			"error", err,
		)

		if !sleepCtx(ctx, backoff) {
			return ctx.Err()
		}

		backoff = rpcNextBackoff(backoff, c.retryCeiling)
	}

	// Final attempt — let the error propagate to the caller.
	err := c.call(ctx, method, params, out)
	if err != nil {
		errType := classifyError(err)

		if errType == RPCErrNetwork || errType == RPCErrTimeout {
			baseArgs := []any{
				"method", method,
				"max_retries", rpcMaxRetries,
				"error", err,
			}

			args := append(baseArgs, rpcErrorAttrs(err)...)

			logger.Error(ctx, "rpc: all retries exhausted — node unreachable", args...)
		}
	}

	return err
}

// doCall performs the raw HTTP round-trip and JSON parsing without instrumentation.
// Separated from call() so the elapsed time always covers the full round-trip.
//
// HTTP status is validated before reading the body, preventing confusing errors
// on 401/403/500 responses that return plain text rather than JSON.
//
// The response body is capped at rpcMaxResponseBytes via io.LimitReader to
// prevent a misbehaving node from exhausting process memory.
//
// The body reader is wrapped in contextReader so cancelling the context aborts
// stalled large-body reads — most relevant for GetBlock verbosity=2, which
// returns up to 4 MiB on mainnet.
func (c *client) doCall(ctx context.Context, method string, params []any, out any) error {
	body, err := json.Marshal(rpcRequest{ID: 1, Method: method, Params: params})
	if err != nil {
		return telemetry.RPC(method+".marshal_request", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return telemetry.RPC(method+".build_request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(string(c.user), string(c.pass))

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Validate HTTP status before reading the body.
	//
	// Bitcoin Core returns HTTP 500 for certain application-layer errors that
	// carry a valid JSON-RPC error body (e.g. gettransaction on a txid not in
	// the wallet returns {"error":{"code":-5,…}} with HTTP 500). We must read
	// and parse the body in that case so callers can distinguish an expected
	// absence (code -5 → IsNotFoundError) from a genuine node failure.
	//
	// All other non-200 status codes are infrastructure failures and we return
	// immediately without attempting to read the body.
	switch resp.StatusCode {
	case http.StatusOK:
		// Normal — fall through.
	case http.StatusUnauthorized:
		return telemetry.RPC(method+".auth",
			fmt.Errorf("HTTP 401 Unauthorized — check BTC_RPC_USER / BTC_RPC_PASS (rpcuser / rpcpassword in bitcoin.conf)"))
	case http.StatusForbidden:
		return telemetry.RPC(method+".auth",
			fmt.Errorf("HTTP 403 Forbidden — check rpcallowip in bitcoin.conf"))
	case http.StatusInternalServerError:
		// Bitcoin Core uses HTTP 500 for both genuine node failures AND
		// application-layer RPC errors (e.g. code -5 "not in wallet").
		// Attempt to parse the body as a JSON-RPC error envelope; if it
		// contains a structured RPCError, return that so callers can
		// inspect the code (IsNotFoundError, IsPrunedBlockError, etc.).
		// Only fall back to the generic http_status error when the body is
		// absent, oversized, or not a valid JSON-RPC error response.
		ctxBody500 := &contextReader{ctx: ctx, r: resp.Body}
		raw500, readErr := io.ReadAll(io.LimitReader(ctxBody500, rpcMaxResponseBytes+1))
		if readErr == nil && len(raw500) <= rpcMaxResponseBytes {
			var rpcResp500 rpcResponse
			if jsonErr := json.Unmarshal(raw500, &rpcResp500); jsonErr == nil && rpcResp500.Error != nil {
				// Structured RPC error — return it unwrapped so errors.As works.
				return rpcResp500.Error
			}
		}
		// Body missing, oversized, or not a JSON-RPC envelope — genuine node failure.
		return telemetry.RPC(method+".http_status",
			fmt.Errorf("unexpected HTTP 500 from Bitcoin Core — check node logs for details"))
	default:
		return telemetry.RPC(method+".http_status",
			fmt.Errorf("unexpected HTTP %d from Bitcoin Core — check node logs for details", resp.StatusCode))
	}

	// Wrap the body so a cancelled context aborts a stalled read.
	ctxBody := &contextReader{ctx: ctx, r: resp.Body}

	// Read one byte past the cap to detect oversized responses without
	// allocating the full body first.
	raw, err := io.ReadAll(io.LimitReader(ctxBody, rpcMaxResponseBytes+1))
	if err != nil {
		return telemetry.RPC(method+".read_body", err)
	}
	if len(raw) > rpcMaxResponseBytes {
		return telemetry.RPC(method+".read_body",
			fmt.Errorf("response body exceeds %d-byte cap (%d bytes read) — possible protocol error or misbehaving node",
				rpcMaxResponseBytes, len(raw)))
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return telemetry.RPC(method+".unmarshal_envelope", err)
	}

	if rpcResp.Error != nil {
		// Return *RPCError unwrapped so callers can use errors.As or
		// IsNotFoundError without losing the concrete type.
		return rpcResp.Error
	}

	if out == nil {
		return nil
	}

	// Guard against a null or absent result with no error. Bitcoin Core should
	// never return {"result":null,"error":null} for a method that has a return
	// value, but a version mismatch, proxy, or corrupted frame could produce
	// this. In Go, json.Unmarshal(null, &v) silently leaves v at its zero value
	// with no error, which is catastrophic:
	//   SendRawTransaction → ("", nil) — settlement engine records a phantom txid.
	//   GetNewAddress → ("", nil) — empty string stored as invoice address.
	// Fail loudly rather than silently corrupt state.
	if len(rpcResp.Result) == 0 || bytes.Equal(rpcResp.Result, []byte("null")) {
		return telemetry.RPC(method+".null_result",
			fmt.Errorf("Bitcoin Core returned a null result with no error for %q — "+
				"possible version mismatch, proxy corruption, or protocol error", method))
	}

	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		return telemetry.RPC(method+".unmarshal_result", err)
	}
	return nil
}

// ── Blockchain methods ────────────────────────────────────────────────────────

// GetBlockchainInfo returns node chain info (chain, best block hash, height,
// pruning status). This is the designated connectivity probe — the only method
// that affirmatively flips bitcoin_rpc_connected to true.
func (c *client) GetBlockchainInfo(ctx context.Context) (BlockchainInfo, error) {
	var result BlockchainInfo
	err := c.retryCall(ctx, rpcMethodGetBlockchainInfo, nil, &result)
	c.recorder.SetRPCConnected(err == nil)
	return result, err
}

// GetBlockHeader returns lightweight block metadata (height, hash, timestamp).
func (c *client) GetBlockHeader(ctx context.Context, hash string) (BlockHeader, error) {
	var result BlockHeader
	err := c.retryCall(ctx, rpcMethodGetBlockHeader, []any{hash, true}, &result)
	return result, err
}

// GetBlock fetches block data at the specified verbosity level.
//
//   - verbosity=1: block metadata + list of txids.
//   - verbosity=2: full transaction data (2–4 MiB on mainnet — use with care).
func (c *client) GetBlock(ctx context.Context, hash string, verbosity int) (json.RawMessage, error) {
	var result json.RawMessage
	err := c.retryCall(ctx, rpcMethodGetBlock, []any{hash, verbosity}, &result)
	return result, err
}

// GetBlockVerbose returns a block with decoded transactions.
func (c *client) GetBlockVerbose(ctx context.Context, hash string) (VerboseBlock, error) {
	var result VerboseBlock
	err := c.retryCall(ctx, rpcMethodGetBlock, []any{hash, 2}, &result)
	return result, err
}

// GetBlockHash returns the block hash at the given height on the active chain.
func (c *client) GetBlockHash(ctx context.Context, height int) (string, error) {
	var result string
	err := c.retryCall(ctx, rpcMethodGetBlockHash, []any{height}, &result)
	return result, err
}

// GetBlockCount returns the current height of the active chain tip.
func (c *client) GetBlockCount(ctx context.Context) (int, error) {
	var result int
	err := c.retryCall(ctx, rpcMethodGetBlockCount, nil, &result)
	return result, err
}

// ── Wallet transaction methods ────────────────────────────────────────────────

// GetTransaction fetches a wallet transaction by txid.
// Returns IsNotFoundError if the txid is not known to the wallet.
//
// include_watchonly is hardcoded to false. This node operates a signing wallet
// only; watch-only addresses are not supported. If watch-only support is ever
// added this must be revisited — watch-only transactions would otherwise be
// silently invisible.
func (c *client) GetTransaction(ctx context.Context, txid string, verbose bool) (WalletTx, error) {
	var result WalletTx
	err := c.retryCall(ctx, rpcMethodGetTransaction, []any{txid, false, verbose}, &result)
	return result, err
}

// ── Address methods ───────────────────────────────────────────────────────────

// GetNewAddress generates a new P2WPKH bech32 address from the wallet's HD keypool.
//
// Not retried — address generation advances the keypool pointer. A retry after
// a partial success could silently skip a keypool slot. Callers own retry semantics.
func (c *client) GetNewAddress(ctx context.Context, label, addressType string) (string, error) {
	var result string
	err := c.call(ctx, rpcMethodGetNewAddress, []any{label, addressType}, &result)
	return result, err
}

// GetAddressInfo returns metadata about a wallet address.
func (c *client) GetAddressInfo(ctx context.Context, address string) (AddressInfo, error) {
	var result AddressInfo
	err := c.retryCall(ctx, rpcMethodGetAddressInfo, []any{address}, &result)
	return result, err
}

// ── Mempool methods ───────────────────────────────────────────────────────────

// GetMempoolEntry checks whether a transaction is currently in the mempool.
// Returns IsNotFoundError (code -5) when absent — the normal absent response.
func (c *client) GetMempoolEntry(ctx context.Context, txid string) (MempoolEntry, error) {
	var result MempoolEntry
	err := c.retryCall(ctx, rpcMethodGetMempoolEntry, []any{txid}, &result)
	return result, err
}

// ── Wallet management methods ─────────────────────────────────────────────────

// GetWalletInfo returns wallet metadata including the current keypool size.
func (c *client) GetWalletInfo(ctx context.Context) (WalletInfo, error) {
	var result WalletInfo
	err := c.retryCall(ctx, rpcMethodGetWalletInfo, nil, &result)
	return result, err
}

// KeypoolRefill instructs Bitcoin Core to top up its pre-generated address pool.
// Not retried — callers own retry semantics for this mutation.
func (c *client) KeypoolRefill(ctx context.Context, newSize int) error {
	return c.call(ctx, rpcMethodKeypoolRefill, []any{newSize}, nil)
}

// ── Fee estimation ────────────────────────────────────────────────────────────

// EstimateSmartFee returns a fee rate estimate for the given confirmation target.
// FeeEstimate.FeeRate is zero when the node lacks sufficient data for estimation.
func (c *client) EstimateSmartFee(ctx context.Context, confTarget int, mode string) (FeeEstimate, error) {
	var result FeeEstimate
	err := c.retryCall(ctx, rpcMethodEstimateSmartFee, []any{confTarget, mode}, &result)
	return result, err
}

// ── PSBT sweep methods ────────────────────────────────────────────────────────

// WalletCreateFundedPSBT constructs a PSBT, selecting inputs automatically.
// Not retried — the caller drives the sweep broadcast loop.
//
// outputs must not be nil — pass an empty slice to let Bitcoin Core select
// all UTXOs automatically. A nil slice marshals to JSON null rather than []
// and causes Bitcoin Core to return an RPC error -1/-8.
//
// Fixed positional parameters sent to Bitcoin Core:
//   - inputs:      [] (empty — let the wallet select UTXOs automatically)
//   - locktime:    0  (no CLTV locktime)
//   - bip32derivs: true (include BIP-32 derivation paths in the PSBT — required
//     for walletprocesspsbt to locate signing keys)
func (c *client) WalletCreateFundedPSBT(ctx context.Context, outputs []map[string]any, options map[string]any) (FundedPSBT, error) {
	if outputs == nil {
		return FundedPSBT{}, fmt.Errorf("WalletCreateFundedPSBT: outputs must not be nil — " +
			"pass an empty slice to auto-select UTXOs; nil marshals to JSON null and Bitcoin Core rejects it")
	}
	var result FundedPSBT
	params := []any{[]any{}, outputs, 0, options, true}
	err := c.call(ctx, rpcMethodWalletCreateFundedPSBT, params, &result)
	return result, err
}

// WalletProcessPSBT signs a PSBT with the wallet's private keys.
// Not retried — the caller drives the sweep broadcast loop.
func (c *client) WalletProcessPSBT(ctx context.Context, psbt string) (ProcessedPSBT, error) {
	var result ProcessedPSBT
	err := c.call(ctx, rpcMethodWalletProcessPSBT, []any{psbt}, &result)
	return result, err
}

// FinalizePSBT extracts a broadcast-ready transaction from a fully signed PSBT.
// Not retried — the caller drives the sweep broadcast loop.
func (c *client) FinalizePSBT(ctx context.Context, psbt string) (FinalizedPSBT, error) {
	var result FinalizedPSBT
	err := c.call(ctx, rpcMethodFinalizePSBT, []any{psbt}, &result)
	return result, err
}

// GetRawTransaction fetches a raw decoded transaction from the mempool or (with txindex) chain.
// verbosity=1 returns the decoded JSON object as a RawTx; verbosity=0 returns the hex string
// (not supported by this method — call GetBlock for that use case).
//
// Key property: works on ANY mempool transaction without txindex — unlike GetTransaction
// (wallet-only). Use this on the SSE display path to match arbitrary watched addresses.
//
// Not retried: on a pruned node without txindex, an HTTP 500 means the transaction left
// the mempool between the ZMQ hashtx event and this RPC call (a normal race condition).
// Retrying cannot recover from this — the tx is confirmed and unreachable without txindex.
func (c *client) GetRawTransaction(ctx context.Context, txid string, verbosity int) (RawTx, error) {
	var result RawTx
	err := c.call(ctx, rpcMethodGetRawTransaction, []any{txid, verbosity}, &result)
	return result, err
}

// SendRawTransaction broadcasts a signed raw transaction to the Bitcoin network.
//
// maxFeeRate is the maximum acceptable fee rate in BTC/kB. Bitcoin Core rejects
// the broadcast if the transaction's effective fee rate exceeds this value.
// Passing 0 (the Go zero-value) removes the cap entirely and can result in
// permanent fund loss if the fee estimator misbehaves — callers must always
// pass a positive value.
//
// Not retried — broadcasting twice causes "transaction already in mempool" errors
// and complicates double-spend detection. The settlement engine owns the retry
// loop with its own idempotency check.
func (c *client) SendRawTransaction(ctx context.Context, hexTx string, maxFeeRate float64) (string, error) {
	if maxFeeRate <= 0 {
		return "", fmt.Errorf("SendRawTransaction: maxFeeRate must be > 0 (got %v) — "+
			"passing 0 removes the fee-rate cap and can permanently burn funds", maxFeeRate)
	}
	var result string
	err := c.call(ctx, rpcMethodSendRawTransaction, []any{hexTx, maxFeeRate}, &result)
	return result, err
}

// ── Security helpers ──────────────────────────────────────────────────────────

// requireLoopbackHost panics if host is not a loopback address.
// Panic rather than error so a misconfigured host fails loudly at startup.
func requireLoopbackHost(host, envName string) {
	if host == "" {
		panic(fmt.Sprintf("rpc: %s: host must not be empty", envName))
	}
	ip := net.ParseIP(host)
	if ip != nil {
		// Literal IP address — validate directly.
		if !ip.IsLoopback() {
			panic(fmt.Sprintf(
				"rpc: %s: host must be a loopback address (e.g. 127.0.0.1 or ::1), got %q — "+
					"RPC credentials must never be transmitted over a non-loopback interface",
				envName, host))
		}
		return
	}
	// Hostname — resolve and check all returned addresses. A hostname that
	// resolves to even one non-loopback address is rejected: an attacker who
	// controls DNS could redirect RPC traffic off-loopback.
	addrs, err := net.LookupHost(host)
	if err != nil || len(addrs) == 0 {
		panic(fmt.Sprintf("rpc: %s: cannot resolve host %q: %v", envName, host, err))
	}
	for _, addr := range addrs {
		resolved := net.ParseIP(addr)
		if resolved == nil || !resolved.IsLoopback() {
			panic(fmt.Sprintf(
				"rpc: %s: host %q resolves to non-loopback address %q — "+
					"RPC credentials must never be transmitted over a non-loopback interface",
				envName, host, addr))
		}
	}
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// contextReader wraps an io.Reader and checks ctx.Err() before each Read.
// This propagates context cancellation into the body read after HTTP headers
// have been received — the standard http.Request context only cancels up to
// the point where the response header is fully received.
// Most relevant for GetBlock verbosity=2, which returns 2–4 MiB on mainnet.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (cr *contextReader) Read(p []byte) (int, error) {
	if err := cr.ctx.Err(); err != nil {
		return 0, err
	}
	return cr.r.Read(p)
}

// sleepCtx blocks for d, returning true when the sleep completes and false when
// ctx is cancelled before d elapses. Uses time.NewTimer (not time.After) to
// avoid the goroutine leak when ctx fires before d elapses.
// When d == 0 the timer fires immediately and returns true without blocking.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// rpcNextBackoff returns the next backoff duration: doubles current, adds up to
// 50% jitter, then caps at ceiling. When ceiling is 0 the result is always 0,
// which lets tests set retryCeiling=0 for instantaneous retries.
func rpcNextBackoff(current, ceiling time.Duration) time.Duration {
	if ceiling == 0 {
		return 0
	}
	doubled := current * 2
	jitterRange := max(int64(current/2), 1)
	jitter := time.Duration(rand.Int64N(jitterRange))
	return min(doubled+jitter, ceiling)
}
