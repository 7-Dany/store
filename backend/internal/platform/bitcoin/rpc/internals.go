package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"sync/atomic"
	"time"
)

// ── Credential safety ─────────────────────────────────────────────────────────

// credential wraps an RPC credential string whose Stringer/GoStringer/
// MarshalText/MarshalJSON/LogValue all return "[redacted]", making accidental
// logging of the raw value impossible across all serialization paths.
type credential string

func (c credential) String() string               { return "[redacted]" }
func (c credential) GoString() string             { return "[redacted]" }
func (c credential) MarshalText() ([]byte, error) { return []byte("[redacted]"), nil }
func (c credential) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func (c credential) LogValue() slog.Value         { return slog.StringValue("[redacted]") }

// ── JSON-RPC envelope ─────────────────────────────────────────────────────────

// rpcRequest is the JSON-RPC 2.0 request envelope sent to Bitcoin Core.
// Bitcoin Core 30.x fully supports JSON-RPC 2.0.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

// rpcResponse is the JSON-RPC response envelope from Bitcoin Core.
//
// ID is json.RawMessage rather than int64 because some proxies or test fixtures
// may echo back a string ID. We validate it against the sent ID to detect stale
// responses from timed-out requests.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error"`
	ID      json.RawMessage `json:"id"`
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
//
// Maximum overshoot: one io.ReadAll buffer (~32 KiB) past context cancellation,
// which is acceptable for the stated use case.
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

// ── Circuit breaker ───────────────────────────────────────────────────────────

// circuitBreaker implements a simple failure-counting circuit breaker.
// After circuitBreakerThreshold consecutive infrastructure failures (network
// errors and timeouts only), the circuit opens for circuitBreakerCooldown,
// short-circuiting all calls without hitting the network.
//
// Concurrency: allow() is eventually consistent under concurrent access.
// It reads failures and lastFailure as two separate atomic loads, so a call
// may be incorrectly blocked or allowed for a single attempt. This is
// acceptable — the next call self-corrects.
//
// After cooldown expiry, all concurrent callers are allowed through
// simultaneously (no half-open state). This is intentional — Bitcoin Core
// handles concurrent connections gracefully. A recordFailure() during the
// reset window may be lost but self-corrects on the next call.
const circuitBreakerThreshold = 10
const circuitBreakerCooldown = 30 * time.Second

type circuitBreaker struct {
	failures    atomic.Int64
	lastFailure atomic.Int64 // unix nanos
}

// allow returns false when the circuit is open (too many recent failures).
func (cb *circuitBreaker) allow() bool {
	f := cb.failures.Load()
	if f < circuitBreakerThreshold {
		return true
	}
	// Check if cooldown has elapsed.
	elapsed := time.Since(time.Unix(0, cb.lastFailure.Load()))
	if elapsed >= circuitBreakerCooldown {
		// Reset the circuit — allow one attempt through.
		cb.failures.Store(0)
		logger.Info(context.Background(), "rpc: circuit breaker reset — allowing requests after cooldown")
		return true
	}
	return false
}

// recordSuccess resets the failure counter.
func (cb *circuitBreaker) recordSuccess() {
	prev := cb.failures.Swap(0)
	if prev >= circuitBreakerThreshold {
		logger.Info(context.Background(), "rpc: circuit breaker closed — node recovered, allowing requests")
	}
}

// recordFailure increments the failure counter and records the timestamp.
func (cb *circuitBreaker) recordFailure() {
	prev := cb.failures.Add(1)
	cb.lastFailure.Store(time.Now().UnixNano())
	if prev == circuitBreakerThreshold {
		logger.Warn(context.Background(), "rpc: circuit breaker open — too many consecutive failures, cooling down",
			"threshold", circuitBreakerThreshold,
			"cooldown", circuitBreakerCooldown,
		)
	}
}
