package rpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── BtcToSat precision ────────────────────────────────────────────────────────

func TestBtcToSat_Precision_PointOneBTC(t *testing.T) {
	// 0.1 * 1e8 in IEEE 754 = 9999999.999999776 → truncation gives 9999999 (wrong).
	// math.Round gives 10000000 (correct).
	sat, err := BtcToSat(0.1)
	require.NoError(t, err)
	assert.Equal(t, int64(10_000_000), sat, "0.1 BTC must equal exactly 10000000 sat")
}

func TestBtcToSat_Precision_SmallAmount(t *testing.T) {
	sat, err := BtcToSat(0.00005)
	require.NoError(t, err)
	assert.Equal(t, int64(5_000), sat)
}

func TestBtcToSat_Precision_InvoiceAmount(t *testing.T) {
	sat, err := BtcToSat(0.00123456)
	require.NoError(t, err)
	assert.Equal(t, int64(123_456), sat)
}

func TestBtcToSat_MaxSatoshi(t *testing.T) {
	sat, err := BtcToSat(21_000_000)
	require.NoError(t, err)
	assert.Equal(t, int64(2_100_000_000_000_000), sat)
}

func TestBtcToSat_Zero(t *testing.T) {
	sat, err := BtcToSat(0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), sat)
}

func TestBtcToSat_Negative_ReturnsError(t *testing.T) {
	_, err := BtcToSat(-0.001)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "negative amount")
}

func TestBtcToSat_OneSatoshi(t *testing.T) {
	sat, err := BtcToSat(0.00000001)
	require.NoError(t, err)
	assert.Equal(t, int64(1), sat)
}

// ── IsNotFoundError ───────────────────────────────────────────────────────────

func TestIsNotFoundError_RPCErrorCode5_ReturnsTrue(t *testing.T) {
	err := &RPCError{Code: -5, Message: "No such wallet transaction"}
	assert.True(t, IsNotFoundError(err))
}

func TestIsNotFoundError_RPCErrorCode5_MempoolMessage_ReturnsTrue(t *testing.T) {
	err := &RPCError{Code: -5, Message: "Transaction not in mempool"}
	assert.True(t, IsNotFoundError(err))
}

func TestIsNotFoundError_OtherRPCCode_ReturnsFalse(t *testing.T) {
	err := &RPCError{Code: -8, Message: "Invalid parameter"}
	assert.False(t, IsNotFoundError(err))
}

func TestIsNotFoundError_NonRPCError_ReturnsFalse(t *testing.T) {
	err := errors.New("connection refused")
	assert.False(t, IsNotFoundError(err))
}

func TestIsNotFoundError_Nil_ReturnsFalse(t *testing.T) {
	assert.False(t, IsNotFoundError(nil))
}

// ── IsPrunedBlockError ────────────────────────────────────────────────────────

func TestIsPrunedBlockError_PrunedData_ReturnsTrue(t *testing.T) {
	err := &RPCError{Code: -1, Message: "Block not found on disk: pruned data"}
	assert.True(t, IsPrunedBlockError(err))
}

func TestIsPrunedBlockError_BlockNotAvailable_ReturnsTrue(t *testing.T) {
	err := errors.New("Block not available (pruned data)")
	assert.True(t, IsPrunedBlockError(err))
}

func TestIsPrunedBlockError_UnrelatedError_ReturnsFalse(t *testing.T) {
	err := errors.New("Block not found")
	assert.False(t, IsPrunedBlockError(err))
}

func TestIsPrunedBlockError_Nil_ReturnsFalse(t *testing.T) {
	assert.False(t, IsPrunedBlockError(nil))
}

// ── Constructor port validation ───────────────────────────────────────────────

func TestNew_ValidPort_Succeeds(t *testing.T) {
	for _, port := range []string{"8332", "48332", "1", "65535"} {
		c, err := New("127.0.0.1", port, "user", "pass", nil)
		require.NoError(t, err, "port %s should be valid", port)
		assert.NotNil(t, c)
	}
}

func TestNew_NonNumericPort_ReturnsError(t *testing.T) {
	_, err := New("127.0.0.1", "not-a-port", "user", "pass", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid RPC port")
}

func TestNew_ZeroPort_ReturnsError(t *testing.T) {
	_, err := New("127.0.0.1", "0", "user", "pass", nil)
	require.Error(t, err)
}

func TestNew_PortAbove65535_ReturnsError(t *testing.T) {
	_, err := New("127.0.0.1", "65536", "user", "pass", nil)
	require.Error(t, err)
}

func TestNew_NegativePort_ReturnsError(t *testing.T) {
	_, err := New("127.0.0.1", "-1", "user", "pass", nil)
	require.Error(t, err)
}

func TestNew_NilRecorder_SubstitutesNoop(t *testing.T) {
	iface, err := New("127.0.0.1", "8332", "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	assert.NotNil(t, c.recorder, "recorder must never be nil after New")
	require.NotPanics(t, func() {
		c.recorder.OnRPCCall("x", "success", 0)
		c.recorder.OnRPCError("x", "network")
		c.recorder.SetRPCConnected(true)
		c.recorder.SetKeypoolSize(100)
	})
}

// ── Loopback enforcement ───────────────────────────────────────────────────────

// TestNew_NonLoopbackHost_Panics mirrors TestNew_NonLoopbackEndpoint_PanicsAtConstruction
// in the ZMQ package. A non-loopback host panics at construction time so that
// a misconfigured BTC_RPC_HOST fails loudly at startup.
func TestNew_NonLoopbackHost_Panics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host      string
		wantPanic bool
	}{
		{"127.0.0.1", false},
		{"127.1.2.3", false},
		{"0.0.0.0", true},
		{"192.168.1.1", true},
		{"10.0.0.1", true},
		{"8.8.8.8", true},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			fn := func() { _, _ = New(tc.host, "8332", "user", "pass", nil) }
			if tc.wantPanic {
				require.Panics(t, fn, "host %q should panic", tc.host)
			} else {
				require.NotPanics(t, fn, "host %q should not panic", tc.host)
			}
		})
	}
}

func TestRequireLoopbackHost_IPv6Loopback_Succeeds(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() { requireLoopbackHost("::1", "TEST") })
}

func TestRequireLoopbackHost_Localhost_Succeeds(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() { requireLoopbackHost("localhost", "TEST") })
}

// ── Transport config ───────────────────────────────────────────────────────────

func TestNew_TransportHasResponseHeaderTimeout(t *testing.T) {
	t.Parallel()
	iface, err := New("127.0.0.1", "8332", "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	require.Equal(t, rpcResponseHeaderTimeout, c.transport.ResponseHeaderTimeout,
		"transport must have a non-zero ResponseHeaderTimeout to bound header-stall hangs")
}

func TestNew_TransportHasMaxIdleConns(t *testing.T) {
	t.Parallel()
	iface, err := New("127.0.0.1", "8332", "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	require.Equal(t, rpcMaxIdleConnsPerHost, c.transport.MaxIdleConnsPerHost,
		"transport must cap the keep-alive pool to prevent unbounded growth")
}

// ── Close() ─────────────────────────────────────────────────────────────────────

// TestClose_NoRaceOnConcurrentCalls verifies Close() does not race with
// concurrent in-flight calls. Run with -race.
func TestClose_NoRaceOnConcurrentCalls(t *testing.T) {
	t.Parallel()
	// "id":"btc" is valid because rpcResponse.ID is json.RawMessage.
	body := `{"result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.GetBlockchainInfo(context.Background())
		}()
	}
	wg.Wait()
	require.NotPanics(t, func() { c.Close() })
}

// ── Credential redaction ──────────────────────────────────────────────────────

func TestCredential_Stringer_ReturnsRedacted(t *testing.T) {
	c := credential("super-secret-password")
	assert.Equal(t, "[redacted]", c.String())
}

func TestNew_CredentialsNeverInError(t *testing.T) {
	_, err := New("127.0.0.1", "bad-port", "my-user", "my-secret-pass", nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "my-user")
	assert.NotContains(t, err.Error(), "my-secret-pass")
}

// ── RPCError ──────────────────────────────────────────────────────────────────

func TestRPCError_Error_IncludesCodeAndMessage(t *testing.T) {
	err := &RPCError{Code: -5, Message: "No such wallet transaction"}
	assert.Contains(t, err.Error(), "-5")
	assert.Contains(t, err.Error(), "No such wallet transaction")
}

// ── classifyError ─────────────────────────────────────────────────────────────

func TestClassifyError_NotFound(t *testing.T) {
	err := &RPCError{Code: -5, Message: "No such wallet transaction"}
	assert.Equal(t, RPCErrNotFound, classifyError(err))
}

func TestClassifyError_Pruned(t *testing.T) {
	err := errors.New("Block not available (pruned data)")
	assert.Equal(t, RPCErrPruned, classifyError(err))
}

func TestClassifyError_RPCError(t *testing.T) {
	err := &RPCError{Code: -25, Message: "Insufficient funds"}
	assert.Equal(t, RPCErrRPC, classifyError(err))
}

func TestClassifyError_Timeout(t *testing.T) {
	assert.Equal(t, RPCErrTimeout, classifyError(context.DeadlineExceeded))
}

func TestClassifyError_Canceled(t *testing.T) {
	assert.Equal(t, RPCErrCanceled, classifyError(context.Canceled))
}

func TestClassifyError_Nil(t *testing.T) {
	assert.Equal(t, "", classifyError(nil))
}

func TestClassifyError_Unknown(t *testing.T) {
	assert.Equal(t, RPCErrUnknown, classifyError(errors.New("marshal failure")))
}

// ── Constants sanity ──────────────────────────────────────────────────────────

func TestInvoiceAddressConstants_Correct(t *testing.T) {
	assert.Equal(t, "invoice", InvoiceAddressLabel)
	assert.Equal(t, "bech32", InvoiceAddressType)
}

// ── captureRecorder ───────────────────────────────────────────────────────────

// captureRecorder records all recorder calls for assertion in tests.
// All methods are mutex-guarded for safety in concurrent retry tests.
type captureRecorder struct {
	mu            sync.Mutex
	calls         []capturedCall
	errors        []capturedError
	connectedSeen []bool
	keypoolSeen   []int
}

type capturedCall struct {
	method   string
	status   string
	duration float64
}

type capturedError struct {
	method    string
	errorType string
}

func (r *captureRecorder) OnRPCCall(method, status string, durationSeconds float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, capturedCall{method, status, durationSeconds})
}

func (r *captureRecorder) OnRPCError(method, errorType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, capturedError{method, errorType})
}

func (r *captureRecorder) SetRPCConnected(connected bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connectedSeen = append(r.connectedSeen, connected)
}

func (r *captureRecorder) SetKeypoolSize(size int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keypoolSeen = append(r.keypoolSeen, size)
}

func (r *captureRecorder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *captureRecorder) errorTypes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.errors))
	for i, e := range r.errors {
		out[i] = e.errorType
	}
	return out
}

func (r *captureRecorder) connectedValues() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]bool, len(r.connectedSeen))
	copy(out, r.connectedSeen)
	return out
}

var _ RPCRecorder = (*captureRecorder)(nil)

// ── newTestServer ─────────────────────────────────────────────────────────────

// newTestServer starts an httptest.Server that returns the given status and body
// for every request and returns the concrete *client so tests can set retryBase=0.
func newTestServer(t *testing.T, statusCode int, body string, rec RPCRecorder) (*client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host, port := parts[0], parts[1]

	iface, err := New(host, port, "user", "pass", rec)
	require.NoError(t, err)
	c := iface.(*client)
	// Tests that use newTestServer always want zero-delay retries so the suite
	// stays fast. Individual tests that need real backoff set their own values.
	c.retryBase = 0
	c.retryCeiling = 0
	return c, srv
}

// ── Recorder instrumentation tests ───────────────────────────────────────────

// TestCall_Success_RecordsSuccessMetric verifies that a 200 response with a
// string ID ("btc") in the envelope is correctly accepted — rpcResponse.ID is
// json.RawMessage so it accepts both numeric and string IDs.
func TestCall_Success_RecordsSuccessMetric(t *testing.T) {
	rec := &captureRecorder{}
	body := `{"result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, rec)

	_, err := c.GetBlockchainInfo(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1, rec.callCount())
	rec.mu.Lock()
	assert.Equal(t, rpcMethodGetBlockchainInfo, rec.calls[0].method)
	assert.Equal(t, RPCStatusSuccess, rec.calls[0].status)
	assert.Greater(t, rec.calls[0].duration, 0.0)
	assert.Empty(t, rec.errors)
	rec.mu.Unlock()
}

func TestCall_Success_SetsRPCConnectedTrue(t *testing.T) {
	rec := &captureRecorder{}
	body := `{"result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, rec)

	_, err := c.GetBlockchainInfo(context.Background())
	require.NoError(t, err)

	vals := rec.connectedValues()
	require.NotEmpty(t, vals)
	assert.True(t, vals[len(vals)-1], "SetRPCConnected must be true after a successful GetBlockchainInfo")
}

// TestCall_RPCError_RecordsErrorAndErrorType verifies that a not_found error is
// classified correctly and not retried (deterministic Bitcoin Core response).
func TestCall_RPCError_RecordsErrorAndErrorType(t *testing.T) {
	rec := &captureRecorder{}
	// "id":"btc" — accepted because rpcResponse.ID is json.RawMessage.
	body := `{"result":null,"error":{"code":-5,"message":"No such wallet transaction"},"id":"btc"}`
	c, _ := newTestServer(t, 200, body, rec)

	_, err := c.GetTransaction(context.Background(), "deadbeef", false)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err))

	// not_found is deterministic — exactly 1 attempt, no retries.
	require.Equal(t, 1, rec.callCount())
	rec.mu.Lock()
	assert.Equal(t, rpcMethodGetTransaction, rec.calls[0].method)
	assert.Equal(t, RPCStatusError, rec.calls[0].status)
	require.Len(t, rec.errors, 1)
	assert.Equal(t, RPCErrNotFound, rec.errors[0].errorType)
	rec.mu.Unlock()
}

// closedLoopbackAddr returns a host and port for a TCP address that was free at
// call time but has no listener. Attempts to connect will receive "connection
// refused" immediately, simulating a node that is not running.
// Using a dynamically allocated port avoids conflicts with services bound to
// hardcoded ports (e.g. 19998, 19999) on CI machines.
func closedLoopbackAddr(t *testing.T) (host, port string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().(*net.TCPAddr)
	ln.Close() // close immediately — connections will be refused
	return "127.0.0.1", strconv.Itoa(addr.Port)
}

// TestCall_NetworkError_RecordsNetworkErrorType verifies that a connection
// refused error is classified as RPCErrNetwork. The context timeout ensures the
// test does not take more than ~300 ms even if retries fire.
func TestCall_NetworkError_RecordsNetworkErrorType(t *testing.T) {
	rec := &captureRecorder{}
	host, port := closedLoopbackAddr(t)
	iface, err := New(host, port, "user", "pass", rec)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 0
	c.retryCeiling = 0

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, callErr := c.GetBlockchainInfo(ctx)
	require.Error(t, callErr)

	require.GreaterOrEqual(t, rec.callCount(), 1)
	errTypes := rec.errorTypes()
	require.NotEmpty(t, errTypes)
	assert.Equal(t, RPCErrNetwork, errTypes[0])
}

func TestCall_NetworkError_SetsRPCConnectedFalse(t *testing.T) {
	rec := &captureRecorder{}
	host, port := closedLoopbackAddr(t)
	iface, err := New(host, port, "user", "pass", rec)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 0
	c.retryCeiling = 0

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _ = c.GetBlockchainInfo(ctx)

	vals := rec.connectedValues()
	require.NotEmpty(t, vals)
	assert.False(t, vals[len(vals)-1])
}

func TestCall_ParseError_RecordsUnknownErrorType(t *testing.T) {
	rec := &captureRecorder{}
	c, _ := newTestServer(t, 200, "not json at all", rec)

	_, err := c.GetBlockchainInfo(context.Background())
	require.Error(t, err)

	// unknown errors are not retried.
	errTypes := rec.errorTypes()
	require.NotEmpty(t, errTypes)
	assert.Equal(t, RPCErrUnknown, errTypes[0])
}

// ── HTTP status code validation ────────────────────────────────────────────────

func TestDoCall_HTTP401_ReturnsAuthError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, http.StatusUnauthorized, "Unauthorized\n", nil)

	_, err := c.GetBlockchainInfo(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Contains(t, err.Error(), "BTC_RPC_USER")
}

func TestDoCall_HTTP403_ReturnsForbiddenError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, http.StatusForbidden, "Forbidden\n", nil)

	_, err := c.GetBlockchainInfo(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "rpcallowip")
}

func TestDoCall_UnexpectedHTTPStatus_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, http.StatusServiceUnavailable, "", nil)

	_, err := c.GetBlockchainInfo(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

// ── Body size cap ──────────────────────────────────────────────────────────────

func TestDoCall_BodyTooLarge_ReturnsError(t *testing.T) {
	t.Parallel()
	oversized := bytes.Repeat([]byte("x"), int(rpcMaxResponseBytes)+1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(oversized)
	}))
	t.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	iface, err := New(parts[0], parts[1], "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 0

	_, err = c.GetBlockchainInfo(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

// ── contextReader ──────────────────────────────────────────────────────────────

func TestContextReader_CancelsRead(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	pr, _ := io.Pipe()
	t.Cleanup(func() { pr.Close() })

	cr := &contextReader{ctx: ctx, r: pr}
	buf := make([]byte, 8)
	n, err := cr.Read(buf)
	assert.Equal(t, 0, n)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestContextReader_PassesThrough(t *testing.T) {
	t.Parallel()
	data := []byte("hello")
	cr := &contextReader{ctx: context.Background(), r: bytes.NewReader(data)}
	buf := make([]byte, len(data))
	n, err := cr.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, data, buf)
}

// ── Retry / backoff ────────────────────────────────────────────────────────────

// TestRetryCall_NetworkError_RetriesUpToMax verifies that retryCall makes
// exactly rpcMaxRetries+1 total attempts on persistent network errors.
func TestRetryCall_NetworkError_RetriesUpToMax(t *testing.T) {
	t.Parallel()

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		// Close the connection immediately to produce a network-level error.
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", 500)
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	t.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	iface, err := New(parts[0], parts[1], "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 0
	c.retryCeiling = 0

	_, err = c.GetBlockchainInfo(context.Background())
	require.Error(t, err)

	// retryCall loop: rpcMaxRetries iterations + 1 final attempt.
	assert.Equal(t, rpcMaxRetries+1, attempts,
		"retryCall must attempt exactly rpcMaxRetries+1 times on persistent network errors")
}

// TestRetryCall_RPCError_DoesNotRetry verifies that a deterministic RPC error
// (code -5 not_found) is returned immediately without retrying.
func TestRetryCall_RPCError_DoesNotRetry(t *testing.T) {
	t.Parallel()
	rec := &captureRecorder{}
	// "id":"btc" is valid — rpcResponse.ID is json.RawMessage.
	body := `{"result":null,"error":{"code":-5,"message":"No such wallet transaction"},"id":"btc"}`
	c, _ := newTestServer(t, 200, body, rec)

	_, err := c.GetTransaction(context.Background(), "abc", false)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err))

	assert.Equal(t, 1, rec.callCount(),
		"deterministic RPC errors must not be retried")
}

// TestRetryCall_ContextCanceled_AbortsImmediately verifies that cancelling the
// context during a backoff sleep aborts retryCall before further attempts.
func TestRetryCall_ContextCanceled_AbortsImmediately(t *testing.T) {
	t.Parallel()

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	t.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	iface, err := New(parts[0], parts[1], "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	// Use a real backoff so the context cancellation has time to fire during sleep.
	c.retryBase = 10 * time.Millisecond
	c.retryCeiling = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, callErr := c.GetBlockchainInfo(ctx)
		done <- callErr
	}()

	// Let at least one attempt happen, then cancel during the backoff sleep.
	time.Sleep(5 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Less(t, attempts, rpcMaxRetries+1,
			"cancelling mid-retry must abort before all retries are exhausted")
	case <-time.After(2 * time.Second):
		t.Fatal("retryCall did not return after context cancellation")
	}
}

// TestRetryCall_SucceedsAfterTransientFailure verifies that retryCall returns
// nil when a later attempt succeeds after transient network errors.
func TestRetryCall_SucceedsAfterTransientFailure(t *testing.T) {
	t.Parallel()

	attempt := 0
	// "id":"btc" on the success response — valid with json.RawMessage ID.
	successBody := `{"result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":"btc"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt < 3 {
			// First two attempts fail at the network level.
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(successBody))
	}))
	t.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	iface, err := New(parts[0], parts[1], "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 0
	c.retryCeiling = 0

	info, err := c.GetBlockchainInfo(context.Background())
	require.NoError(t, err, "retryCall must succeed after transient failures")
	assert.Equal(t, "test4", info.Chain)
	assert.Equal(t, 3, attempt, "exactly 3 attempts: 2 network failures + 1 success")
}

// ── Proactive connectivity gauge ───────────────────────────────────────────────

// TestCall_NetworkError_SetsConnectedFalseProactively verifies that methods
// other than GetBlockchainInfo also trigger SetRPCConnected(false) on network
// errors, providing sub-liveness-interval connectivity resolution.
func TestCall_NetworkError_SetsConnectedFalseProactively(t *testing.T) {
	t.Parallel()
	rec := &captureRecorder{}
	host, port := closedLoopbackAddr(t)
	iface, err := New(host, port, "user", "pass", rec)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 0
	c.retryCeiling = 0

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	// GetBlockHeader is NOT GetBlockchainInfo — uses retryCall.
	_, _ = c.GetBlockHeader(ctx, "00000000abc")

	vals := rec.connectedValues()
	require.NotEmpty(t, vals, "SetRPCConnected must be called proactively on network errors")
	assert.False(t, vals[0])
}

// ── rpcNextBackoff ────────────────────────────────────────────────────────────

func TestRpcNextBackoff_StaysWithinCeiling(t *testing.T) {
	t.Parallel()
	current := rpcRetryBase
	for range 20 {
		next := rpcNextBackoff(current, rpcRetryCeiling)
		assert.LessOrEqual(t, next, rpcRetryCeiling)
		assert.Greater(t, next, time.Duration(0))
		current = next
	}
}

func TestRpcNextBackoff_Increases(t *testing.T) {
	t.Parallel()
	next := rpcNextBackoff(rpcRetryBase, rpcRetryCeiling)
	assert.Greater(t, next, rpcRetryBase)
}

func TestRpcNextBackoff_JitterIsNonDeterministic(t *testing.T) {
	t.Parallel()
	first := rpcNextBackoff(4*time.Second, rpcRetryCeiling)
	varied := false
	for range 50 {
		if rpcNextBackoff(4*time.Second, rpcRetryCeiling) != first {
			varied = true
			break
		}
	}
	assert.True(t, varied, "jitter must produce variation across calls")
}

// TestRpcNextBackoff_ZeroCeiling_ReturnsZero verifies the test-override path:
// setting retryCeiling=0 makes all backoffs 0 so retries are instantaneous.
func TestRpcNextBackoff_ZeroCeiling_ReturnsZero(t *testing.T) {
	t.Parallel()
	assert.Equal(t, time.Duration(0), rpcNextBackoff(rpcRetryBase, 0))
}

// ── sleepCtx ──────────────────────────────────────────────────────────────────

func TestSleepCtx_CompletesDuration(t *testing.T) {
	t.Parallel()
	const sleep = 30 * time.Millisecond
	start := time.Now()
	ok := sleepCtx(context.Background(), sleep)
	assert.True(t, ok)
	// Allow up to 50 % undershoot to accommodate low-resolution OS timers on CI.
	assert.GreaterOrEqual(t, time.Since(start), sleep/2)
}

func TestSleepCtx_CancelsEarly(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	ok := sleepCtx(ctx, 10*time.Second)
	assert.False(t, ok)
	assert.Less(t, time.Since(start), 5*time.Second)
}

// TestSleepCtx_ZeroDuration_ReturnsImmediately verifies that a zero duration
// returns true instantly (used when retryBase=0 in tests).
func TestSleepCtx_ZeroDuration_ReturnsImmediately(t *testing.T) {
	t.Parallel()
	start := time.Now()
	ok := sleepCtx(context.Background(), 0)
	assert.True(t, ok)
	assert.Less(t, time.Since(start), 50*time.Millisecond)
}

// ── Recorder instrumentation — existing method coverage ──────────────────────

func TestCall_MethodConstantUsed_GetNewAddress(t *testing.T) {
	rec := &captureRecorder{}
	body := `{"result":"tb1qexampleaddress","error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, rec)

	_, err := c.GetNewAddress(context.Background(), InvoiceAddressLabel, InvoiceAddressType)
	require.NoError(t, err)

	require.Equal(t, 1, rec.callCount())
	rec.mu.Lock()
	assert.Equal(t, rpcMethodGetNewAddress, rec.calls[0].method)
	rec.mu.Unlock()
}

func TestGetWalletInfo_ReturnsKeypoolSize(t *testing.T) {
	rec := &captureRecorder{}
	body := `{"result":{"walletname":"","walletversion":169900,"keypoolsize":9500,"keypoolsize_hd_internal":9500,"keypoololdest":0,"descriptors":true},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, rec)

	info, err := c.GetWalletInfo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 9500, info.KeypoolSize)

	rec.SetKeypoolSize(info.KeypoolSize)
	rec.mu.Lock()
	require.Len(t, rec.keypoolSeen, 1)
	assert.Equal(t, 9500, rec.keypoolSeen[0])
	rec.mu.Unlock()
}

func TestGetTransaction_ConfirmedTx_ReturnsBlockHeight(t *testing.T) {
	body := `{"result":{"txid":"abc","confirmations":3,"blockhash":"bbb","blockheight":500,"blocktime":1700000000,"timereceived":1700000000,"details":[]},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	tx, err := c.GetTransaction(context.Background(), "abc", false)
	require.NoError(t, err)
	assert.Equal(t, 3, tx.Confirmations)
	assert.Equal(t, 500, tx.BlockHeight)
}

func TestGetTransaction_MempoolTx_ZeroConfirmations(t *testing.T) {
	body := `{"result":{"txid":"abc","confirmations":0,"blockhash":"","blockheight":0,"blocktime":0,"timereceived":1700000000,"details":[]},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	tx, err := c.GetTransaction(context.Background(), "abc", false)
	require.NoError(t, err)
	assert.Equal(t, 0, tx.Confirmations)
	assert.Equal(t, 0, tx.BlockHeight)
	assert.Empty(t, tx.BlockHash)
}

func TestGetTransaction_Verbose_PopulatesDecoded(t *testing.T) {
	body := `{"result":{"txid":"abc","confirmations":1,"blockhash":"bbb","blockheight":1,"blocktime":1,"timereceived":1,"details":[],"decoded":{"txid":"abc","vout":[{"value":0.001,"n":0,"scriptPubKey":{"address":"tb1qtest","type":"witness_v0_keyhash"}}]}},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	tx, err := c.GetTransaction(context.Background(), "abc", true)
	require.NoError(t, err)
	require.NotNil(t, tx.Decoded)
	require.Len(t, tx.Decoded.Vout, 1)

	sat, err := BtcToSat(tx.Decoded.Vout[0].Value)
	require.NoError(t, err)
	assert.Equal(t, int64(100_000), sat)
}

func TestGetBlockVerbose_ReturnsDecodedBlockAndUsesVerbosityTwo(t *testing.T) {
	t.Parallel()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotBody = string(raw)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":{"confirmations":9,"height":777001,"hash":"blockhash","tx":[{"txid":"abc","vin":[{"txid":"prev","vout":1}],"vout":[{"value":0.001,"n":0,"scriptPubKey":{"address":"tb1qdest","type":"witness_v0_keyhash"}}]}]},"error":null,"id":"btc"}`))
	}))
	t.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	iface, err := New(parts[0], parts[1], "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 0
	c.retryCeiling = 0

	block, err := c.GetBlockVerbose(context.Background(), "blockhash")
	require.NoError(t, err)
	assert.Equal(t, "blockhash", block.Hash)
	assert.Equal(t, 777001, block.Height)
	assert.Equal(t, 9, block.Confirmations)
	require.Len(t, block.Tx, 1)
	assert.Equal(t, "abc", block.Tx[0].TxID)
	require.Len(t, block.Tx[0].Vout, 1)
	assert.Equal(t, "tb1qdest", block.Tx[0].Vout[0].ScriptPubKey.Address)

	assert.Contains(t, gotBody, `"method":"getblock"`)
	assert.Contains(t, gotBody, `"params":["blockhash",2]`)
}

func TestGetBlockVerbose_PrunedBlockError_ReturnsStructuredError(t *testing.T) {
	t.Parallel()

	rec := &captureRecorder{}
	body := `{"result":null,"error":{"code":-1,"message":"Block not found on disk: pruned data"},"id":"btc"}`
	c, _ := newTestServer(t, http.StatusInternalServerError, body, rec)

	_, err := c.GetBlockVerbose(context.Background(), "deadbeef")
	require.Error(t, err)
	assert.True(t, IsPrunedBlockError(err))

	require.Equal(t, 1, rec.callCount(), "pruned block errors are deterministic and must not retry")
	rec.mu.Lock()
	require.Len(t, rec.errors, 1)
	assert.Equal(t, RPCErrPruned, rec.errors[0].errorType)
	rec.mu.Unlock()
}

// ── SendRawTransaction validation ──────────────────────────────────────────────────────────────────────────

// TestSendRawTransaction_ZeroMaxFeeRate_ReturnsError is the critical regression
// test for the fee-rate cap footgun. Passing 0 (the Go zero-value) to
// SendRawTransaction would tell Bitcoin Core to broadcast with no fee-rate cap,
// which can permanently burn funds if the fee estimator misbehaves.
func TestSendRawTransaction_ZeroMaxFeeRate_ReturnsError(t *testing.T) {
	t.Parallel()
	// Server body does not matter — the guard fires before the HTTP call.
	c, _ := newTestServer(t, 200, `{"result":"txid","error":null,"id":"btc"}`, nil)

	_, err := c.SendRawTransaction(context.Background(), "deadbeef", 0)
	require.Error(t, err, "maxFeeRate=0 must be rejected before contacting Bitcoin Core")
	assert.Contains(t, err.Error(), "maxFeeRate")
	assert.Contains(t, err.Error(), "0")
}

func TestSendRawTransaction_NegativeMaxFeeRate_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"result":"txid","error":null,"id":"btc"}`, nil)

	_, err := c.SendRawTransaction(context.Background(), "deadbeef", -0.001)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxFeeRate")
}

func TestSendRawTransaction_ValidFeeRate_CallsServer(t *testing.T) {
	t.Parallel()
	body := `{"result":"abcdef1234567890","error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	txid, err := c.SendRawTransaction(context.Background(), "deadbeef", 0.001)
	require.NoError(t, err)
	assert.Equal(t, "abcdef1234567890", txid)
}

// ── PSBT method coverage ─────────────────────────────────────────────────────────────────────────────

func TestEstimateSmartFee_ReturnsFeeRateAndBlocks(t *testing.T) {
	t.Parallel()
	body := `{"result":{"feerate":0.00012345,"blocks":3},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	fee, err := c.EstimateSmartFee(context.Background(), 3, "economical")
	require.NoError(t, err)
	assert.Equal(t, 3, fee.Blocks)
	// Verify feerate survives the btcRawAmount round-trip.
	sat, err := BtcToSat(fee.FeeRate)
	require.NoError(t, err)
	assert.Equal(t, int64(12345), sat)
}

func TestEstimateSmartFee_ZeroFeeRate_NodeLacksData(t *testing.T) {
	t.Parallel()
	// Bitcoin Core returns feerate=0 when it lacks enough data for estimation
	// (common on early testnet4 or a freshly synced node).
	body := `{"result":{"feerate":0,"blocks":1},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	fee, err := c.EstimateSmartFee(context.Background(), 1, "economical")
	require.NoError(t, err)
	// FeeRate == 0 is a valid signal ("no data"), not an error.
	sat, err := BtcToSat(fee.FeeRate)
	require.NoError(t, err)
	assert.Equal(t, int64(0), sat)
}

func TestWalletCreateFundedPSBT_ReturnsPSBTAndFee(t *testing.T) {
	t.Parallel()
	body := `{"result":{"psbt":"cHNidP8B...","fee":0.00000500,"changepos":1},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	outputs := []map[string]any{{"tb1qrecipient": 0.001}}
	options := map[string]any{"fee_rate": 10}
	result, err := c.WalletCreateFundedPSBT(context.Background(), outputs, options)
	require.NoError(t, err)
	assert.Equal(t, "cHNidP8B...", result.PSBT)
	assert.Equal(t, 1, result.ChangePos)
	fee, err := BtcToSat(result.Fee)
	require.NoError(t, err)
	assert.Equal(t, int64(500), fee)
}

// TestWalletCreateFundedPSBT_NoChange verifies that ChangePos == -1 is parsed
// correctly and is distinct from ChangePos == 0 (change at first output).
func TestWalletCreateFundedPSBT_NoChange_ChangePosMinusOne(t *testing.T) {
	t.Parallel()
	body := `{"result":{"psbt":"cHNidP8B...","fee":0.00000100,"changepos":-1},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	// Pass an empty (non-nil) slice — nil would be rejected by the nil outputs guard.
	result, err := c.WalletCreateFundedPSBT(context.Background(), []map[string]any{}, nil)
	require.NoError(t, err)
	assert.Equal(t, -1, result.ChangePos, "ChangePos must be -1 when there is no change output")
}

func TestWalletProcessPSBT_Complete_ReturnsSignedPSBT(t *testing.T) {
	t.Parallel()
	body := `{"result":{"psbt":"cHNidP8BsIgnEd...","complete":true},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	result, err := c.WalletProcessPSBT(context.Background(), "cHNidP8B...")
	require.NoError(t, err)
	assert.True(t, result.Complete)
	assert.Equal(t, "cHNidP8BsIgnEd...", result.PSBT)
}

func TestFinalizePSBT_Complete_ReturnsHex(t *testing.T) {
	t.Parallel()
	body := `{"result":{"hex":"02000000000101...","complete":true},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	result, err := c.FinalizePSBT(context.Background(), "cHNidP8BsIgnEd...")
	require.NoError(t, err)
	assert.True(t, result.Complete)
	assert.Equal(t, "02000000000101...", result.Hex)
}

// TestFinalizePSBT_NotComplete verifies that FinalizePSBT returns a struct
// (not an error) when complete=false. Callers must check result.Complete before
// treating result.Hex as broadcast-ready.
func TestFinalizePSBT_NotComplete_CallerMustCheckField(t *testing.T) {
	t.Parallel()
	body := `{"result":{"hex":"","complete":false},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	result, err := c.FinalizePSBT(context.Background(), "cHNidP8BPartial...")
	require.NoError(t, err, "complete=false is not an error — caller must check result.Complete")
	assert.False(t, result.Complete, "result.Complete must be false when PSBT is not fully signed")
	assert.Empty(t, result.Hex, "Hex must be empty when Complete=false")
}

// ── Reorg / negative confirmations ───────────────────────────────────────────────────────────────

// TestGetTransaction_NegativeConfirmations_IsConflicting verifies that a
// transaction in a conflicting chain (post-reorg) has negative Confirmations
// and is correctly identified by IsConflicting.
func TestGetTransaction_NegativeConfirmations_IsConflicting(t *testing.T) {
	body := `{"result":{"txid":"abc","confirmations":-1,"blockhash":"oldhash","blockheight":100,"blocktime":1700000000,"timereceived":1700000000,"details":[]},"error":null,"id":"btc"}`
	c, _ := newTestServer(t, 200, body, nil)

	tx, err := c.GetTransaction(context.Background(), "abc", false)
	require.NoError(t, err)
	assert.Equal(t, -1, tx.Confirmations, "Confirmations must be -1 for a tx in a conflicting chain")
	assert.True(t, IsConflicting(tx), "IsConflicting must return true for negative Confirmations")
}

func TestIsConflicting_PositiveConfirmations_ReturnsFalse(t *testing.T) {
	tx := WalletTx{Confirmations: 3}
	assert.False(t, IsConflicting(tx))
}

func TestIsConflicting_ZeroConfirmations_ReturnsFalse(t *testing.T) {
	tx := WalletTx{Confirmations: 0}
	assert.False(t, IsConflicting(tx), "Zero confirmations = mempool, not conflicting")
}

// ── BtcToSat supply cap ──────────────────────────────────────────────────────────────────────────────

func TestBtcToSat_AboveMaxSupply_ReturnsError(t *testing.T) {
	t.Parallel()
	_, err := BtcToSat(21_000_001)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum Bitcoin supply")
}

func TestBtcToSat_ExactMaxSupply_Succeeds(t *testing.T) {
	t.Parallel()
	// 21_000_000 is the ceiling constant itself. The real Bitcoin supply cap is
	// ~20,999,999.97 BTC, but we use 21M as the guard value with > (not >=) so
	// that exactly 21M passes through. Amounts above 21M are the error case.
	_, err := BtcToSat(21_000_000)
	require.NoError(t, err)
}

// ── IsPrunedBlockError — RPC code path ──────────────────────────────────────────────────────────

// TestIsPrunedBlockError_RPCErrorCode verifies the primary RPC-code-based check.
// Bitcoin Core returns code -1 for pruned-block errors; the string matching is
// the fallback for transitive wrapping.
func TestIsPrunedBlockError_RPCErrorCodeMinus1WithMessage_ReturnsTrue(t *testing.T) {
	err := &RPCError{Code: -1, Message: "Block not found on disk: pruned data"}
	assert.True(t, IsPrunedBlockError(err))
}

func TestIsPrunedBlockError_RPCErrorCodeMinus1WrongMessage_ReturnsFalse(t *testing.T) {
	// Code -1 with an unrelated message should NOT be classified as pruned.
	err := &RPCError{Code: -1, Message: "Internal error"}
	assert.False(t, IsPrunedBlockError(err))
}

func TestIsPrunedBlockError_RPCErrorCodeOther_ReturnsFalse(t *testing.T) {
	// Code -5 with a pruned-sounding message is not a pruned-block error.
	err := &RPCError{Code: -5, Message: "pruned data somewhere"}
	assert.False(t, IsPrunedBlockError(err))
}

// ── Null result guard ─────────────────────────────────────────────────────────────────────

// TestDoCall_NullResult_ReturnsError is the critical regression test for the
// null-result guard. If Bitcoin Core returns {"result":null,"error":null} for
// a method that has a return value, we must error loudly rather than return a
// silent zero value. The most dangerous cases are SendRawTransaction (returns
// "" as txid, causing a phantom broadcast) and GetNewAddress (returns "" as
// address, which would be stored as the invoice receive address).
func TestDoCall_NullResult_ReturnsError(t *testing.T) {
	t.Parallel()
	// Bitcoin Core response with null result and no error.
	c, _ := newTestServer(t, 200, `{"result":null,"error":null,"id":"btc"}`, nil)

	// GetBlockchainInfo passes out != nil, so the null guard fires.
	_, err := c.GetBlockchainInfo(context.Background())
	require.Error(t, err, "null result with no error must return an error, not a zero-value struct")
	assert.Contains(t, err.Error(), "null result")
}

func TestDoCall_NullResult_SendRawTransaction_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"result":null,"error":null,"id":"btc"}`, nil)

	// SendRawTransaction must never return ("", nil) — that would be a phantom broadcast.
	_, err := c.SendRawTransaction(context.Background(), "deadbeef", 0.001)
	require.Error(t, err, "null txid result must not be returned as a success")
	assert.Contains(t, err.Error(), "null result")
}

func TestDoCall_NullResult_GetNewAddress_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"result":null,"error":null,"id":"btc"}`, nil)

	// GetNewAddress must never return ("", nil) — empty string would be stored as an invoice address.
	_, err := c.GetNewAddress(context.Background(), InvoiceAddressLabel, InvoiceAddressType)
	require.Error(t, err, "null address result must not be returned as a success")
	assert.Contains(t, err.Error(), "null result")
}

// ── WalletCreateFundedPSBT nil outputs guard ─────────────────────────────────────────

func TestWalletCreateFundedPSBT_NilOutputs_ReturnsError(t *testing.T) {
	t.Parallel()
	// nil outputs marshals to JSON null instead of [], causing Bitcoin Core to
	// return an RPC error. The client must catch this before making the HTTP call.
	c, _ := newTestServer(t, 200, `{"result":{"psbt":"x","fee":0,"changepos":-1},"error":null,"id":"btc"}`, nil)

	_, err := c.WalletCreateFundedPSBT(context.Background(), nil, nil)
	require.Error(t, err, "nil outputs must be rejected before contacting Bitcoin Core")
	assert.Contains(t, err.Error(), "outputs must not be nil")
}
