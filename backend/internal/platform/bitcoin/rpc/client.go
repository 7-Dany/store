package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// ── Concrete client ───────────────────────────────────────────────────────────

// client is the concrete JSON-RPC implementation of Client. Unexported — callers
// depend on the interface. Safe for concurrent use; each call is an independent
// HTTP request. Keep-alive connections are managed by the embedded transport.
type client struct {
	baseURL    string
	user       credential
	pass       credential
	authHeader string // pre-computed "Basic base64(user:pass)" — one copy ever
	http       *http.Client
	transport  *http.Transport // retained for Close() and transport-config tests
	recorder   RPCRecorder

	// nextID generates unique per-request IDs to detect stale responses.
	nextID atomic.Int64

	// closed guards against use after Close.
	closed atomic.Bool

	// cb is a simple failure-counting circuit breaker that short-circuits calls
	// after consecutive infrastructure failures, preventing retry storms when
	// the node is down.
	cb circuitBreaker

	// retryBase and retryCeiling control the backoff used by retryCall.
	// They are set to RPCRetryBase / RPCRetryCeiling by New() and may be
	// overridden in tests (same package) to make retries instantaneous:
	//   c.retryBase = 0
	//   c.retryCeiling = 0
	retryBase    time.Duration
	retryCeiling time.Duration
}

// compile-time assertion that *client satisfies Client.
var _ Client = (*client)(nil)

// MarshalJSON redacts sensitive fields (authHeader, user, pass, baseURL) to prevent
// accidental credential or infrastructure leakage if a client struct is ever serialized.
func (c *client) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"baseURL":    "[redacted]",
		"user":       c.user.String(),
		"pass":       c.pass.String(),
		"authHeader": "[redacted]",
		"closed":     c.closed.Load(),
	})
}

// New creates a new RPC client and returns the Client interface.
//
// Host must be a loopback address (127.x.x.x or ::1). New() panics if it is
// not — RPC credentials must never be transmitted over a non-loopback interface.
//
// Port must be a numeric string in 1–65535.
//
// Recorder may be nil; a no-op recorder is substituted automatically.
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

	// Pre-compute the Authorization header value once. This creates exactly
	// one heap-allocated copy of the plaintext credentials, rather than one
	// per RPC call.
	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))

	// Purpose-built HTTP transport with per-phase timeouts, a bounded
	// connection pool, and a dial-time loopback check to prevent TOCTOU
	// DNS attacks.
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   RPCDialTimeout,
			KeepAlive: 30 * time.Second,
			// Validate every TCP connection at dial time — not just at New()
			// construction. This prevents a DNS record that was loopback at
			// startup from being changed to a non-loopback address later.
			Control: func(_, address string, _ syscall.RawConn) error {
				h, _, err := net.SplitHostPort(address)
				if err != nil {
					return fmt.Errorf("rpc: cannot parse dial address %q: %w", address, err)
				}
				ip := net.ParseIP(h)
				if ip != nil && !ip.IsLoopback() {
					return fmt.Errorf("rpc: refusing non-loopback connection to %s", address)
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout:   RPCTLSHandshakeTimeout,
		ResponseHeaderTimeout: RPCResponseHeaderTimeout,
		IdleConnTimeout:       RPCIdleConnTimeout,
		MaxIdleConns:          RPCMaxIdleConnsPerHost,
		MaxIdleConnsPerHost:   RPCMaxIdleConnsPerHost,
		MaxConnsPerHost:       RPCMaxConnsPerHost,
		// Bitcoin Core responses are JSON text; compression wastes CPU and adds
		// latency without meaningful bandwidth savings on loopback.
		DisableCompression: true,
	}

	return &client{
		baseURL:      "http://" + host + ":" + port + "/",
		user:         credential(user),
		pass:         credential(pass),
		authHeader:   authHeader,
		http:         &http.Client{Transport: transport},
		transport:    transport,
		recorder:     recorder,
		retryBase:    RPCRetryBase,
		retryCeiling: RPCRetryCeiling,
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
func (c *client) Close(_ context.Context) {
	c.closed.Store(true)
	c.transport.CloseIdleConnections()
	logger.Info(context.Background(), "rpc: client closed — idle connections released")
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
			args := slices.Concat(baseArgs, rpcAttrs)
			logger.Debug(ctx, "rpc: expected absence", args...)

		case RPCErrCanceled:
			// Application or caller shutdown — not a failure worth logging.

		case RPCErrRPC, RPCErrNetwork, RPCErrTimeout, RPCErrUnknown:
			args := slices.Concat(baseArgs, []any{"elapsed_s", elapsed}, rpcAttrs)
			logger.Warn(ctx, "rpc: call failed", args...)
		}

		// Infrastructure health tracking
		if errType == RPCErrNetwork || errType == RPCErrTimeout {
			c.recorder.SetRPCConnected(false)
			// Only infrastructure failures trip the circuit breaker.
			// Expected absences (not_found, pruned) and deterministic RPC
			// errors must NOT trip the breaker — they are normal operation.
			c.cb.recordFailure()
		}

		c.recorder.OnRPCCall(method, RPCStatusError.String(), elapsed)
		c.recorder.OnRPCError(method, errType.String())
	} else {
		// Circuit breaker: reset failure counter on success.
		c.cb.recordSuccess()

		c.recorder.SetRPCConnected(true)
		c.recorder.OnRPCCall(method, RPCStatusSuccess.String(), elapsed)
	}

	return err
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
	// First attempt — outside the retry loop.
	err := c.call(ctx, method, params, out)
	if err == nil {
		return nil
	}

	backoff := c.retryBase

	for attempt := range RPCMaxRetries {
		errType := classifyError(err)

		if errType != RPCErrNetwork && errType != RPCErrTimeout {
			return err // deterministic error — no point retrying
		}

		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), sanitizeError(err))
		}

		logger.Warn(ctx, "rpc: transient error — retrying",
			"method", method,
			"retry", attempt+1,
			"max_retries", RPCMaxRetries,
			"backoff", backoff,
			"error", err,
		)

		if !sleepCtx(ctx, backoff) {
			return errors.Join(ctx.Err(), sanitizeError(err))
		}

		backoff = rpcNextBackoff(backoff, c.retryCeiling)

		err = c.call(ctx, method, params, out)
		if err == nil {
			logger.Info(ctx, "rpc: recovered after transient error",
				"method", method,
				"retries", attempt+1,
			)
			return nil
		}
	}

	// All retries exhaust — log and return the last error.
	errType := classifyError(err)

	if errType == RPCErrNetwork || errType == RPCErrTimeout {
		args := slices.Concat([]any{
			"method", method,
			"max_retries", RPCMaxRetries,
			"error", err,
		}, rpcErrorAttrs(err))

		logger.Error(ctx, "rpc: all retries exhausted — node unreachable", args...)
	}

	return err
}

// doCall performs the raw HTTP round-trip and JSON parsing without instrumentation.
// Separated from call() so the elapsed time always covers the full round-trip.
//
// HTTP status is validated before reading the body, preventing confusing errors
// on 401/403/500 responses that return plain text rather than JSON.
//
// The response body is capped at RPCMaxResponseBytes via io.LimitReader to
// prevent a misbehaving node from exhausting process memory.
//
// The body reader is wrapped in contextReader so cancelling the context aborts
// stalled large-body reads — most relevant for GetBlock verbosity=2, which
// returns up to 4 MiB on mainnet.
func (c *client) doCall(ctx context.Context, method string, params []any, out any) error {
	if c.closed.Load() {
		return errors.New("rpc: client is closed")
	}

	// Circuit breaker: short-circuit when the node has been failing consecutively.
	if !c.cb.allow() {
		return errors.New("rpc: circuit breaker open — node unreachable, cooling down")
	}

	// Normalize nil params to an empty slice so json.Marshal produces "params":[]
	// rather than "params":null, which is required by JSON-RPC 1.0/2.0 specs.
	if params == nil {
		params = []any{}
	}

	// Generate a unique per-request ID to detect stale responses.
	reqID := c.nextID.Add(1)

	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: reqID, Method: method, Params: params})
	if err != nil {
		return telemetry.RPC(method+".marshal_request", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return telemetry.RPC(method+".build_request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Use the pre-computed Authorization header — one copy of credentials ever.
	req.Header.Set("Authorization", c.authHeader)

	resp, err := c.http.Do(req)
	if err != nil {
		// Defensive: if http.Do returns both a response and an error (only
		// happens when CheckRedirect fails), close the body to prevent a leak.
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
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
		raw500, readErr := io.ReadAll(io.LimitReader(ctxBody500, RPCMaxResponseBytes+1))
		if readErr == nil && len(raw500) <= RPCMaxResponseBytes {
			var rpcResp500 rpcResponse
			if jsonErr := json.Unmarshal(raw500, &rpcResp500); jsonErr == nil && rpcResp500.Error != nil {
				// Structured RPC error — wrap with telemetry for operational context
				// while preserving the error chain (telemetry.RPC uses %w).
				return telemetry.RPC(method+".rpc_error", rpcResp500.Error)
			}
		}
		// Body missing, oversized, or not a JSON-RPC envelope — genuine node failure.
		return telemetry.RPC(method+".http_status", &httpStatusError{StatusCode: http.StatusInternalServerError})
	default:
		return telemetry.RPC(method+".http_status", &httpStatusError{StatusCode: resp.StatusCode})
	}

	// Wrap the body so a cancelled context aborts a stalled read.
	ctxBody := &contextReader{ctx: ctx, r: resp.Body}

	// Read one byte past the cap to detect oversized responses without
	// allocating the full body first.
	raw, err := io.ReadAll(io.LimitReader(ctxBody, RPCMaxResponseBytes+1))
	if err != nil {
		return telemetry.RPC(method+".read_body", err)
	}
	if len(raw) > RPCMaxResponseBytes {
		return telemetry.RPC(method+".read_body",
			fmt.Errorf("response body exceeds %d-byte cap (%d bytes read) — possible protocol error or misbehaving node",
				RPCMaxResponseBytes, len(raw)))
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return telemetry.RPC(method+".unmarshal_envelope", err)
	}

	// Check error first — a real RPC error should surface the actual error,
	// not be masked by an ID mismatch from a stale proxy response.
	if rpcResp.Error != nil {
		// Return *RPCError unwrapped so callers can use errors.As or
		// IsNotFoundError without losing the concrete type.
		return rpcResp.Error
	}

	// Validate the response ID matches the request ID to detect stale responses
	// from timed-out requests on reused connections.
	// Fallback: some proxies coerce numeric IDs to strings (e.g. 1 → "1").
	// Try parsing the response ID as a number and comparing.
	sentID, _ := json.Marshal(reqID)
	if !bytes.Equal(rpcResp.ID, sentID) {
		var respIDNum int64
		if jsonErr := json.Unmarshal(rpcResp.ID, &respIDNum); jsonErr != nil || respIDNum != reqID {
			return telemetry.RPC(method+".id_mismatch",
				fmt.Errorf("response ID mismatch: sent %d, got %s — possible stale response from timed-out request",
					reqID, rpcResp.ID))
		}
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
	if len(rpcResp.Result) == 0 || bytes.Equal(rpcResp.Result, jsonNull) {
		return telemetry.RPC(method+".null_result",
			fmt.Errorf("bitcoin core returned a null result with no error for %q — "+
				"possible version mismatch, proxy corruption, or protocol error", method))
	}

	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		return telemetry.RPC(method+".unmarshal_result", err)
	}
	return nil
}
