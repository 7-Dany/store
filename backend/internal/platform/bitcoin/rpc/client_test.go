package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// ── Shared test helpers ───────────────────────────────────────────────────────

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
	status   string // RPCStatus.String() output
	duration float64
}

type capturedError struct {
	method  string
	errType string // RPCErrType.String() output
}

func (r *captureRecorder) OnRPCCall(method, status string, durationSeconds float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, capturedCall{method, status, durationSeconds})
}

func (r *captureRecorder) OnRPCError(method, errType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, capturedError{method, errType})
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

func (r *captureRecorder) errTypes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.errors))
	for i, e := range r.errors {
		out[i] = e.errType
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

// newTestServer starts an httptest.Server that echoes the request ID back in
// the response and returns the concrete *client so tests can set retryBase=0.
func newTestServer(tb testing.TB, statusCode int, bodyTemplate string, rec RPCRecorder) (*client, *httptest.Server) {
	tb.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read request to extract the ID for response echoing.
		raw, _ := io.ReadAll(r.Body)
		var reqEnvelope struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(raw, &reqEnvelope)

		// Replace the placeholder "id":1 in the template with the actual request ID.
		idStr := string(reqEnvelope.ID)
		if idStr == "" {
			idStr = "1" // fallback for malformed requests
		}
		response := strings.Replace(bodyTemplate, `"id":1`, `"id":`+idStr, 1)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(response))
	}))
	tb.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host, port := parts[0], parts[1]

	iface, err := New(host, port, "user", "pass", rec)
	require.NoError(tb, err)
	c := iface.(*client)
	// Tests that use newTestServer always want zero-delay retries so the suite
	// stays fast. Individual tests that need real backoff set their own values.
	c.retryBase = 0
	c.retryCeiling = 0
	return c, srv
}

// closedLoopbackAddr returns a host and port for a TCP address that was free at
// call time but has no listener.
func closedLoopbackAddr(t *testing.T) (host, port string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().(*net.TCPAddr)
	ln.Close()
	return "127.0.0.1", strconv.Itoa(addr.Port)
}

// ── Constructor / port validation ─────────────────────────────────────────────

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

// ── Transport config ───────────────────────────────────────────────────────────

func TestNew_TransportHasResponseHeaderTimeout(t *testing.T) {
	t.Parallel()
	iface, err := New("127.0.0.1", "8332", "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	require.Equal(t, RPCResponseHeaderTimeout, c.transport.ResponseHeaderTimeout,
		"transport must have a non-zero ResponseHeaderTimeout to bound header-stall hangs")
}

func TestNew_TransportHasMaxIdleConns(t *testing.T) {
	t.Parallel()
	iface, err := New("127.0.0.1", "8332", "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	require.Equal(t, RPCMaxIdleConnsPerHost, c.transport.MaxIdleConnsPerHost,
		"transport must cap the keep-alive pool to prevent unbounded growth")
}

func TestNew_TransportHasMaxConnsPerHost(t *testing.T) {
	t.Parallel()
	iface, err := New("127.0.0.1", "8332", "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	require.Equal(t, RPCMaxConnsPerHost, c.transport.MaxConnsPerHost,
		"transport must cap total connections to prevent port exhaustion")
}

func TestNew_TransportHasDialerControl(t *testing.T) {
	t.Parallel()
	iface, err := New("127.0.0.1", "8332", "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	require.NotNil(t, c.transport.DialContext, "transport must have a custom DialContext for loopback validation")
}

// ── Close() ───────────────────────────────────────────────────────────────────

func TestClose_NoRaceOnConcurrentCalls(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			_, _ = c.GetBlockchainInfo(context.Background())
		})
	}
	wg.Wait()
	require.NotPanics(t, func() { c.Close(context.Background()) })
}

func TestClose_AfterClose_ReturnsError(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	c.Close(context.Background())

	_, err := c.GetBlockchainInfo(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client is closed")
}

// ── Core engine (doCall) ──────────────────────────────────────────────────────

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

func TestDoCall_BodyTooLarge_ReturnsError(t *testing.T) {
	t.Parallel()
	oversized := bytes.Repeat([]byte("x"), int(RPCMaxResponseBytes)+1)

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

func TestDoCall_HTTP500_StructuredRPCError_ReturnsWrappedError(t *testing.T) {
	t.Parallel()
	rec := &captureRecorder{}
	body := `{"jsonrpc":"2.0","result":null,"error":{"code":-5,"message":"No such wallet transaction"},"id":1}`
	c, _ := newTestServer(t, http.StatusInternalServerError, body, rec)

	_, err := c.GetTransaction(context.Background(), "deadbeef", false)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err), "HTTP 500 with code -5 must be classified as not_found")
}

func TestDoCall_EmptyBody_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, "", nil)

	_, err := c.GetBlockchainInfo(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestDoCall_HTMLBody_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, "<html><body>Service Unavailable</body></html>", nil)

	_, err := c.GetBlockchainInfo(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestDoCall_UnexpectedJSON_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"foo":"bar"}`, nil)

	_, err := c.GetBlockchainInfo(context.Background())
	require.Error(t, err)
	// {"foo":"bar"} has no "id" field → ID mismatch (empty string vs sent ID).
	assert.Contains(t, err.Error(), "id_mismatch")
}

func TestDoCall_NullResult_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"jsonrpc":"2.0","result":null,"error":null,"id":1}`, nil)

	_, err := c.GetBlockchainInfo(context.Background())
	require.Error(t, err, "null result with no error must return an error, not a zero-value struct")
	assert.Contains(t, err.Error(), "null result")
}

func TestDoCall_NullResult_SendRawTransaction_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"jsonrpc":"2.0","result":null,"error":null,"id":1}`, nil)

	_, err := c.SendRawTransaction(context.Background(), "deadbeef", 0.001)
	require.Error(t, err, "null txid result must not be returned as a success")
	assert.Contains(t, err.Error(), "null result")
}

func TestDoCall_NullResult_GetNewAddress_ReturnsError(t *testing.T) {
	t.Parallel()
	c, _ := newTestServer(t, 200, `{"jsonrpc":"2.0","result":null,"error":null,"id":1}`, nil)

	_, err := c.GetNewAddress(context.Background(), InvoiceAddressLabel, InvoiceAddressType)
	require.Error(t, err, "null address result must not be returned as a success")
	assert.Contains(t, err.Error(), "null result")
}

func TestDoCall_NilOut_SucceedsWithNullResult(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":null,"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, nil)

	err := c.KeypoolRefill(context.Background(), 100)
	require.NoError(t, err, "KeypoolRefill (out=nil) must succeed with null result")
}

func TestDoCall_SendsJSONRPC2_0(t *testing.T) {
	t.Parallel()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotBody = string(raw)

		var reqEnvelope struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(raw, &reqEnvelope)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":%s}`, string(reqEnvelope.ID))
	}))
	t.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	iface, err := New(parts[0], parts[1], "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 0
	c.retryCeiling = 0

	_, err = c.GetBlockchainInfo(context.Background())
	require.NoError(t, err)

	assert.Contains(t, gotBody, `"jsonrpc":"2.0"`)
}

func TestDoCall_NilParams_MarshalsToEmptyArray(t *testing.T) {
	t.Parallel()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotBody = string(raw)

		var reqEnvelope struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(raw, &reqEnvelope)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":%s}`, string(reqEnvelope.ID))
	}))
	t.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	iface, err := New(parts[0], parts[1], "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 0
	c.retryCeiling = 0

	_, err = c.GetBlockchainInfo(context.Background())
	require.NoError(t, err)

	assert.Contains(t, gotBody, `"params":[]`)
	assert.NotContains(t, gotBody, `"params":null`)
}

func TestDoCall_UniqueRequestIDs(t *testing.T) {
	t.Parallel()

	var ids []int64
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req rpcRequest
		_ = json.Unmarshal(raw, &req)
		mu.Lock()
		ids = append(ids, req.ID)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":%d}`, req.ID)
	}))
	t.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	iface, err := New(parts[0], parts[1], "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 0
	c.retryCeiling = 0

	ctx := context.Background()
	for range 5 {
		_, _ = c.GetBlockchainInfo(ctx)
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, ids, 5)
	// All IDs must be unique.
	seen := make(map[int64]bool)
	for _, id := range ids {
		assert.False(t, seen[id], "request ID %d was reused", id)
		seen[id] = true
	}
}

// ── Recorder instrumentation ─────────────────────────────────────────────────

func TestCall_Success_RecordsSuccessMetric(t *testing.T) {
	rec := &captureRecorder{}
	body := `{"jsonrpc":"2.0","result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, rec)

	_, err := c.GetBlockchainInfo(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1, rec.callCount())
	rec.mu.Lock()
	assert.Equal(t, rpcMethodGetBlockchainInfo, rec.calls[0].method)
	assert.Equal(t, RPCStatusSuccess.String(), rec.calls[0].status)
	assert.Greater(t, rec.calls[0].duration, 0.0)
	assert.Empty(t, rec.errors)
	rec.mu.Unlock()
}

func TestCall_Success_SetsRPCConnectedTrue(t *testing.T) {
	rec := &captureRecorder{}
	body := `{"jsonrpc":"2.0","result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":1}`
	c, _ := newTestServer(t, 200, body, rec)

	_, err := c.GetBlockchainInfo(context.Background())
	require.NoError(t, err)

	vals := rec.connectedValues()
	require.NotEmpty(t, vals)
	assert.True(t, vals[len(vals)-1], "SetRPCConnected must be true after a successful GetBlockchainInfo")
}

func TestCall_RPCError_RecordsErrorAndErrorType(t *testing.T) {
	rec := &captureRecorder{}
	body := `{"jsonrpc":"2.0","result":null,"error":{"code":-5,"message":"No such wallet transaction"},"id":1}`
	c, _ := newTestServer(t, 200, body, rec)

	_, err := c.GetTransaction(context.Background(), "deadbeef", false)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err))

	// not_found is deterministic — exactly 1 attempt, no retries.
	require.Equal(t, 1, rec.callCount())
	rec.mu.Lock()
	assert.Equal(t, rpcMethodGetTransaction, rec.calls[0].method)
	assert.Equal(t, RPCStatusError.String(), rec.calls[0].status)
	require.Len(t, rec.errors, 1)
	assert.Equal(t, RPCErrNotFound.String(), rec.errors[0].errType)
	rec.mu.Unlock()
}

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
	errTypes := rec.errTypes()
	require.NotEmpty(t, errTypes)
	assert.Equal(t, RPCErrNetwork.String(), errTypes[0])
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

	errTypes := rec.errTypes()
	require.NotEmpty(t, errTypes)
	assert.Equal(t, RPCErrUnknown.String(), errTypes[0])
}

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

	_, _ = c.GetBlockHeader(ctx, "00000000abc")

	vals := rec.connectedValues()
	require.NotEmpty(t, vals, "SetRPCConnected must be called proactively on network errors")
	assert.False(t, vals[0])
}

// ── Retry / backoff ────────────────────────────────────────────────────────────

func TestRetryCall_NetworkError_RetriesUpToMax(t *testing.T) {
	t.Parallel()

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
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

	assert.Equal(t, RPCMaxRetries+1, attempts,
		"retryCall must attempt exactly RPCMaxRetries+1 times on persistent network errors")
}

func TestRetryCall_RPCError_DoesNotRetry(t *testing.T) {
	t.Parallel()
	rec := &captureRecorder{}
	body := `{"jsonrpc":"2.0","result":null,"error":{"code":-5,"message":"No such wallet transaction"},"id":1}`
	c, _ := newTestServer(t, 200, body, rec)

	_, err := c.GetTransaction(context.Background(), "abc", false)
	require.Error(t, err)
	assert.True(t, IsNotFoundError(err))

	assert.Equal(t, 1, rec.callCount(),
		"deterministic RPC errors must not be retried")
}

func TestRetryCall_ContextCanceled_AbortsImmediately(t *testing.T) {
	t.Parallel()

	attempts := 0
	firstReq := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		select {
		case firstReq <- struct{}{}:
		default:
		}
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	t.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	iface, err := New(parts[0], parts[1], "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 100 * time.Millisecond
	c.retryCeiling = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, callErr := c.GetBlockchainInfo(ctx)
		done <- callErr
	}()

	// Wait for the first request, then cancel during backoff.
	<-firstReq
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Less(t, attempts, RPCMaxRetries+1,
			"cancelling mid-retry must abort before all retries are exhausted")
	case <-time.After(2 * time.Second):
		t.Fatal("retryCall did not return after context cancellation")
	}
}

func TestRetryCall_SucceedsAfterTransientFailure(t *testing.T) {
	t.Parallel()

	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt < 3 {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		// Read the request to extract the ID for the response.
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		var req rpcRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		respBody := fmt.Sprintf(`{"jsonrpc":"2.0","result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":%d}`, req.ID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(respBody))
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

// ── Circuit breaker ───────────────────────────────────────────────────────────

func TestCircuitBreaker_AllowsUnderThreshold(t *testing.T) {
	t.Parallel()
	var cb circuitBreaker
	for range circuitBreakerThreshold - 1 {
		cb.recordFailure()
		assert.True(t, cb.allow(), "should allow below threshold")
	}
}

func TestCircuitBreaker_OpensAtThreshold(t *testing.T) {
	t.Parallel()
	var cb circuitBreaker
	for range circuitBreakerThreshold {
		cb.recordFailure()
	}
	assert.False(t, cb.allow(), "should block at threshold")
}

func TestCircuitBreaker_ResetsOnSuccess(t *testing.T) {
	t.Parallel()
	var cb circuitBreaker
	for range circuitBreakerThreshold {
		cb.recordFailure()
	}
	assert.False(t, cb.allow())
	cb.recordSuccess()
	assert.True(t, cb.allow(), "should allow after success reset")
}

func TestCircuitBreaker_CooldownResetsAfterExpiry(t *testing.T) {
	t.Parallel()
	var cb circuitBreaker
	for range circuitBreakerThreshold {
		cb.recordFailure()
	}
	assert.False(t, cb.allow())
	// Simulate time passing by directly setting the last failure to the past.
	cb.lastFailure.Store(time.Now().Add(-2 * circuitBreakerCooldown).UnixNano())
	assert.True(t, cb.allow(), "should allow after cooldown expires")
	assert.Equal(t, int64(0), cb.failures.Load(), "failures should be reset after cooldown")
}

func TestDoCall_CircuitBreakerOpen_ReturnsError(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":1}`
	c, srv := newTestServer(t, 200, body, nil)
	defer srv.Close()
	c.retryBase = 0
	c.retryCeiling = 0

	// Trip the circuit breaker by recording infrastructure failures.
	for range circuitBreakerThreshold {
		c.cb.recordFailure()
	}

	_, err := c.GetBlockchainInfo(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker open")
}

func TestCall_NotFoundError_DoesNotTripCircuitBreaker(t *testing.T) {
	t.Parallel()

	attempt := 0
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempt++
		mu.Unlock()

		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var req rpcRequest
		require.NoError(t, json.Unmarshal(raw, &req))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		respBody := fmt.Sprintf(`{"jsonrpc":"2.0","result":null,"error":{"code":-5,"message":"No such wallet transaction"},"id":%d}`, req.ID)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	iface, err := New(parts[0], parts[1], "user", "pass", nil)
	require.NoError(t, err)
	c := iface.(*client)
	c.retryBase = 0
	c.retryCeiling = 0

	// Make more than circuitBreakerThreshold not-found calls.
	for range circuitBreakerThreshold + 5 {
		_, _ = c.GetTransaction(context.Background(), "abc", false)
	}

	// The circuit must still be closed — not-found is expected, not infrastructure failure.
	assert.True(t, c.cb.allow(), "expected-absence errors must not trip the circuit breaker")
}

// ── Benchmark ────────────────────────────────────────────────────────────────

func BenchmarkDoCallSuccess(b *testing.B) {
	body := `{"jsonrpc":"2.0","result":{"chain":"test4","blocks":100,"bestblockhash":"abc","pruned":false},"error":null,"id":1}`
	c, srv := newTestServer(b, 200, body, nil)
	defer srv.Close()
	c.retryBase = 0
	c.retryCeiling = 0
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.GetBlockchainInfo(ctx)
	}
}
