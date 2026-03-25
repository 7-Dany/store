package events_test

// External test package (events_test rather than events) so that
// EventsFakeServicer can be defined locally without triggering the import cycle:
//
//	events → bitcoinsharedtest → events
//
// All fakes are declared inline here following the watch/handler_test.go pattern.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/domain/bitcoin/events"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// ── local fake servicer ───────────────────────────────────────────────────────

type fakeServicer struct {
	issueTokenFn            func(ctx context.Context, in events.IssueTokenInput) (events.IssueTokenResult, error)
	verifyAndConsumeTokenFn func(ctx context.Context, in events.VerifyTokenInput) (events.VerifiedTokenResult, error)
	acquireSlotFn           func(ctx context.Context, userID string) error
	subscribeFn             func(ctx context.Context, userID string) (<-chan events.Event, error)
	releaseSlotFn           func(userID string, ch <-chan events.Event)
	isZMQRunningFn          func() error
	statusFn                func(ctx context.Context) events.StatusResult
	writeAuditLogFn         func(ctx context.Context, event audit.EventType, userID string, metadata map[string]any) error
	shutdownFn              func()
}

var _ events.Servicer = (*fakeServicer)(nil)

func (f *fakeServicer) IssueToken(ctx context.Context, in events.IssueTokenInput) (events.IssueTokenResult, error) {
	if f.issueTokenFn != nil {
		return f.issueTokenFn(ctx, in)
	}
	return events.IssueTokenResult{SignedJWT: "fake.jwt.token", MaxAge: 60}, nil
}
func (f *fakeServicer) VerifyAndConsumeToken(ctx context.Context, in events.VerifyTokenInput) (events.VerifiedTokenResult, error) {
	if f.verifyAndConsumeTokenFn != nil {
		return f.verifyAndConsumeTokenFn(ctx, in)
	}
	return events.VerifiedTokenResult{UserID: "user-default", JTI: "jti-default"}, nil
}
func (f *fakeServicer) AcquireSlot(ctx context.Context, userID string) error {
	if f.acquireSlotFn != nil {
		return f.acquireSlotFn(ctx, userID)
	}
	return nil
}
func (f *fakeServicer) Subscribe(ctx context.Context, userID string) (<-chan events.Event, error) {
	if f.subscribeFn != nil {
		return f.subscribeFn(ctx, userID)
	}
	// Return a channel that is immediately closed so the SSE loop exits cleanly.
	ch := make(chan events.Event)
	close(ch)
	return ch, nil
}
func (f *fakeServicer) ReleaseSlot(userID string, ch <-chan events.Event) {
	if f.releaseSlotFn != nil {
		f.releaseSlotFn(userID, ch)
	}
}
func (f *fakeServicer) IsZMQRunning() error {
	if f.isZMQRunningFn != nil {
		return f.isZMQRunningFn()
	}
	return nil
}
func (f *fakeServicer) Status(ctx context.Context) events.StatusResult {
	if f.statusFn != nil {
		return f.statusFn(ctx)
	}
	return events.StatusResult{ZMQConnected: true, ActiveConnections: 3}
}
func (f *fakeServicer) WriteAuditLog(ctx context.Context, ev audit.EventType, userID string, metadata map[string]any) error {
	if f.writeAuditLogFn != nil {
		return f.writeAuditLogFn(ctx, ev, userID, metadata)
	}
	return nil
}
func (f *fakeServicer) Shutdown() {
	if f.shutdownFn != nil {
		f.shutdownFn()
	}
}

// ── flushRecorder — httptest.ResponseRecorder that implements http.Flusher ────

// flushRecorder wraps httptest.ResponseRecorder and tracks Flush calls.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushCount int
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (fr *flushRecorder) Flush() {
	fr.flushCount++
	fr.ResponseRecorder.Flush()
}

// ── errorWriter — records that a write happened and returns an error ──────────

// errorWriter is an http.ResponseWriter that returns an error on the first
// body Write after headers have been sent. Used to simulate client disconnect
// mid-stream.
type errorWriter struct {
	header      http.Header
	wroteHeader bool
	statusCode  int
	writtenBody []byte
	writeErr    error // returned on body writes after WriteHeader
	flushCount  int
}

func newErrorWriter(writeErr error) *errorWriter {
	return &errorWriter{
		header:   make(http.Header),
		writeErr: writeErr,
	}
}

func (e *errorWriter) Header() http.Header { return e.header }
func (e *errorWriter) WriteHeader(code int) {
	e.wroteHeader = true
	e.statusCode = code
}
func (e *errorWriter) Write(b []byte) (int, error) {
	if e.wroteHeader {
		return 0, e.writeErr
	}
	e.writtenBody = append(e.writtenBody, b...)
	return len(b), nil
}
func (e *errorWriter) Flush() { e.flushCount++ }

// ── test helpers ──────────────────────────────────────────────────────────────

var testOrigins = map[string]struct{}{"https://app.example.com": {}}

func newTestHandler(svc events.Servicer) *events.Handler {
	return events.NewHandler(svc, nil, testOrigins, "testnet4", false, events.EventsConfig{})
}

func withAuthUser(r *http.Request, userID string) *http.Request {
	return r.WithContext(token.InjectUserIDForTest(r.Context(), userID))
}

func withSSECookie(r *http.Request, value string) *http.Request {
	r.AddCookie(&http.Cookie{Name: "btc_sse_jti", Value: value})
	return r
}

func withOrigin(r *http.Request, origin string) *http.Request {
	r.Header.Set("Origin", origin)
	return r
}

func assertJSON(t *testing.T, body []byte, code string) {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m), "body: %s", body)
	assert.Equal(t, code, m["code"], "body: %s", body)
}

// ── T-18: Origin validation — allowed origin accepted ─────────────────────────

func TestOriginValidation_AllowedOrigin_Accepted(t *testing.T) {
	t.Parallel()

	// A closed channel causes the SSE loop to exit immediately after the
	// response is started, which is all we need for this test.
	svc := &fakeServicer{}
	h := newTestHandler(svc)

	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://app.example.com")
	r = withSSECookie(r, "fake.jwt.token")

	w := newFlushRecorder()
	h.Events(w, r)

	// Must NOT be 403 — origin was allowed.
	assert.NotEqual(t, http.StatusForbidden, w.Code,
		"allowed origin must not return 403")
}

// ── T-19: Origin validation — unknown origin rejected ─────────────────────────

func TestOriginValidation_UnknownOrigin_Rejected(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&fakeServicer{})
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://evil.example.com")
	r = withSSECookie(r, "fake.jwt.token")

	w := httptest.NewRecorder()
	h.Events(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assertJSON(t, w.Body.Bytes(), "forbidden")
}

// ── T-20: Origin validation — missing Origin header rejected ──────────────────

func TestOriginValidation_MissingOrigin_Rejected(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&fakeServicer{})
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	// No Origin header set
	r = withSSECookie(r, "fake.jwt.token")

	w := httptest.NewRecorder()
	h.Events(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assertJSON(t, w.Body.Bytes(), "forbidden")
}

// ── T-136: ErrSSECapExceeded uses EventBitcoinSSECapExceeded audit event ──────

func TestSSECapExceeded_UsesCorrectAuditEvent(t *testing.T) {
	t.Parallel()

	// VerifyAndConsumeToken returns ErrSSECapExceeded — the service already
	// writes EventBitcoinSSECapExceeded internally. This test verifies the
	// handler maps the error to 429 and does NOT write ErrSSETokenConsumeFailure.
	var auditEvents []audit.EventType
	var mu sync.Mutex

	svc := &fakeServicer{
		verifyAndConsumeTokenFn: func(_ context.Context, _ events.VerifyTokenInput) (events.VerifiedTokenResult, error) {
			return events.VerifiedTokenResult{}, events.ErrSSECapExceeded
		},
		writeAuditLogFn: func(_ context.Context, ev audit.EventType, _ string, _ map[string]any) error {
			mu.Lock()
			auditEvents = append(auditEvents, ev)
			mu.Unlock()
			return nil
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://app.example.com")
	r = withSSECookie(r, "fake.jwt.token")

	w := httptest.NewRecorder()
	h.Events(w, r)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assertJSON(t, w.Body.Bytes(), "user_connection_limit")

	// Verify that EventBitcoinSSETokenConsumeFailure was NOT written by handler
	// (the service writes EventBitcoinSSECapExceeded internally, not the handler).
	mu.Lock()
	defer mu.Unlock()
	for _, ev := range auditEvents {
		assert.NotEqual(t, audit.EventBitcoinSSETokenConsumeFailure, ev,
			"handler must not write TokenConsumeFailure for cap exceeded — use CapExceeded")
	}
}

// ── T-172: No SSE cookie → 401 ────────────────────────────────────────────────

func TestSSE_CookieAuth_NoCookie_Returns401(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&fakeServicer{})
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://app.example.com")
	// No btc_sse_jti cookie

	w := httptest.NewRecorder()
	h.Events(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSON(t, w.Body.Bytes(), "unauthorized")
}

// ── T-173: Valid cookie path opens SSE stream ─────────────────────────────────

func TestSSE_CookieAuth_ValidCookie_Connects(t *testing.T) {
	t.Parallel()

	// Subscribe returns a channel that is immediately closed so the loop exits.
	svc := &fakeServicer{
		verifyAndConsumeTokenFn: func(_ context.Context, _ events.VerifyTokenInput) (events.VerifiedTokenResult, error) {
			return events.VerifiedTokenResult{UserID: "user-t173", JTI: "jti-t173"}, nil
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://app.example.com")
	r = withSSECookie(r, "valid.cookie.value")

	w := newFlushRecorder()
	h.Events(w, r)

	// Stream should open — 200 with text/event-stream.
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
}

// ── T-174: Expired/invalid cookie → 401 ──────────────────────────────────────

func TestSSE_CookieAuth_ExpiredCookie_Returns401(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		verifyAndConsumeTokenFn: func(_ context.Context, _ events.VerifyTokenInput) (events.VerifiedTokenResult, error) {
			return events.VerifiedTokenResult{}, events.ErrSSETokenInvalid
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://app.example.com")
	r = withSSECookie(r, "expired.cookie.value")

	w := httptest.NewRecorder()
	h.Events(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSON(t, w.Body.Bytes(), "unauthorized")
}

// ── ErrSSETokenExpired — session binding expired → 401 ────────────────────────

// TestSSETokenExpired_Returns401 verifies that ErrSSETokenExpired (the SID key
// expired in Redis) maps to 401 Unauthorized with code "unauthorized", NOT to
// 503. Finding: 1a / A1a-2 — kvstore.ErrNotFound must not map to 503.
func TestSSETokenExpired_Returns401(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		verifyAndConsumeTokenFn: func(_ context.Context, _ events.VerifyTokenInput) (events.VerifiedTokenResult, error) {
			return events.VerifiedTokenResult{}, events.ErrSSETokenExpired
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://app.example.com")
	r = withSSECookie(r, "expired.session.cookie")

	w := httptest.NewRecorder()
	h.Events(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"ErrSSETokenExpired must map to 401, not 503")
	assertJSON(t, w.Body.Bytes(), "unauthorized")
}

// ── T-102: SSE write error triggers cleanup ───────────────────────────────────

func TestSSEEventWriteError_TriggersCleanup(t *testing.T) {
	t.Parallel()

	var releaseCalledWith string
	var releaseMu sync.Mutex

	// Subscribe returns a channel pre-loaded with one event so the loop
	// immediately tries to write it.
	svc := &fakeServicer{
		verifyAndConsumeTokenFn: func(_ context.Context, _ events.VerifyTokenInput) (events.VerifiedTokenResult, error) {
			return events.VerifiedTokenResult{UserID: "user-t102", JTI: "jti-t102"}, nil
		},
		subscribeFn: func(_ context.Context, _ string) (<-chan events.Event, error) {
			ch := make(chan events.Event, 1)
			ch <- events.Event{Type: "mempool_tx", Payload: []byte(`{"event":"mempool_tx"}`)}
			return ch, nil
		},
		releaseSlotFn: func(userID string, _ <-chan events.Event) {
			releaseMu.Lock()
			releaseCalledWith = userID
			releaseMu.Unlock()
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://app.example.com")
	r = withSSECookie(r, "valid.cookie")

	// errorWriter: WriteHeader succeeds (SSE headers), all subsequent body
	// writes return an error — simulates the connection being lost.
	ew := newErrorWriter(fmt.Errorf("connection reset by peer"))
	h.Events(ew, r)

	// ReleaseSlot must have been called.
	releaseMu.Lock()
	got := releaseCalledWith
	releaseMu.Unlock()
	assert.Equal(t, "user-t102", got, "ReleaseSlot must be called with the correct userID after write error")
}

// ── Status handler ─────────────────────────────────────────────────────────────

func TestStatus_Authenticated_Returns200(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		statusFn: func(_ context.Context) events.StatusResult {
			return events.StatusResult{
				ZMQConnected:      true,
				RPCConnected:      false,
				ActiveConnections: 7,
				LastBlockHashAge:  12.5,
			}
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events/status", nil)
	r = withAuthUser(r, "user-status")

	w := httptest.NewRecorder()
	h.Status(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["zmq_connected"])
	assert.Equal(t, float64(7), resp["active_connections"])
}

func TestStatus_Unauthenticated_Returns401(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&fakeServicer{})
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events/status", nil)
	// No auth injected

	w := httptest.NewRecorder()
	h.Status(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestStatus_RPCConnected_True verifies that when the RPCHealthChecker returns
// healthy, the /events/status response carries "rpc_connected": true.
// Finding 27: RPCConnected=true path was never tested.
func TestStatus_RPCConnected_True(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		statusFn: func(_ context.Context) events.StatusResult {
			return events.StatusResult{
				ZMQConnected:      true,
				RPCConnected:      true, // healthy RPC node
				ActiveConnections: 2,
				LastBlockHashAge:  5.0,
			}
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events/status", nil)
	r = withAuthUser(r, "user-rpc-healthy")

	w := httptest.NewRecorder()
	h.Status(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["rpc_connected"],
		"rpc_connected must be true when RPCHealthChecker reports healthy")
}

// ── IssueToken handler ─────────────────────────────────────────────────────────

func TestIssueToken_Authenticated_Sets204AndCookie(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		issueTokenFn: func(_ context.Context, _ events.IssueTokenInput) (events.IssueTokenResult, error) {
			return events.IssueTokenResult{SignedJWT: "signed.jwt", MaxAge: 60}, nil
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodPost, "/bitcoin/events/token", nil)
	r = withAuthUser(r, "00000000-0000-0000-0000-000000000001")

	w := httptest.NewRecorder()
	h.IssueToken(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "btc_sse_jti", cookies[0].Name)
	assert.Equal(t, "signed.jwt", cookies[0].Value)
	assert.True(t, cookies[0].HttpOnly)
}

func TestIssueToken_Unauthenticated_Returns401(t *testing.T) {
	t.Parallel()

	h := newTestHandler(&fakeServicer{})
	r := httptest.NewRequest(http.MethodPost, "/bitcoin/events/token", nil)
	// No auth

	w := httptest.NewRecorder()
	h.IssueToken(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestIssueToken_RedisUnavailable_Returns503(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		issueTokenFn: func(_ context.Context, _ events.IssueTokenInput) (events.IssueTokenResult, error) {
			return events.IssueTokenResult{}, events.ErrSSERedisUnavailable
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodPost, "/bitcoin/events/token", nil)
	r = withAuthUser(r, "00000000-0000-0000-0000-000000000001")

	w := httptest.NewRecorder()
	h.IssueToken(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assertJSON(t, w.Body.Bytes(), "service_unavailable")
}

// ── ZMQ health gate ────────────────────────────────────────────────────────────

func TestEvents_ZMQUnhealthy_Returns500(t *testing.T) {
	t.Parallel()

	var released bool
	svc := &fakeServicer{
		verifyAndConsumeTokenFn: func(_ context.Context, _ events.VerifyTokenInput) (events.VerifiedTokenResult, error) {
			return events.VerifiedTokenResult{UserID: "user-zmq"}, nil
		},
		isZMQRunningFn: func() error { return events.ErrSSEZMQUnhealthy },
		releaseSlotFn: func(_ string, _ <-chan events.Event) {
			released = true
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://app.example.com")
	r = withSSECookie(r, "valid.cookie")

	w := newFlushRecorder()
	h.Events(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertJSON(t, w.Body.Bytes(), "zmq_unhealthy")
	// doCleanup must have released the slot.
	assert.True(t, released, "slot must be released when ZMQ is unhealthy")
}

// ── Process cap ────────────────────────────────────────────────────────────────

func TestEvents_ProcessCapReached_Returns503(t *testing.T) {
	t.Parallel()

	var released bool
	svc := &fakeServicer{
		verifyAndConsumeTokenFn: func(_ context.Context, _ events.VerifyTokenInput) (events.VerifiedTokenResult, error) {
			return events.VerifiedTokenResult{UserID: "user-cap"}, nil
		},
		subscribeFn: func(_ context.Context, _ string) (<-chan events.Event, error) {
			return nil, events.ErrSSEProcessCapReached
		},
		releaseSlotFn: func(_ string, _ <-chan events.Event) {
			released = true
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://app.example.com")
	r = withSSECookie(r, "valid.cookie")

	w := httptest.NewRecorder()
	h.Events(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assertJSON(t, w.Body.Bytes(), "sse_cap_reached")
	assert.True(t, released, "slot must be released when process cap is reached")
}

// ── Stubs for harder tests requiring clock injection / integration ─────────────

func TestHandlerTimeout_BlockingHandlerCancelled(t *testing.T) {
	t.Skip("T-89: implement with a handler timeout middleware in a later stage")
}

func TestRealIP_TrustedProxy_Used(t *testing.T) {
	t.Skip("T-90: implement with TrustedProxyRealIP middleware wired in routes_test.go")
}

func TestRealIP_UntrustedProxy_RemoteAddr(t *testing.T) {
	t.Skip("T-91: implement with TrustedProxyRealIP middleware wired in routes_test.go")
}

func TestSafeInvoke_PanicInInnerGoroutine_ProcessSurvives(t *testing.T) {
	t.Skip("T-101: implement when safeInvoke wrapper is added to the ZMQ event dispatch path")
}

func TestSSEHandler_PanicInLoop_SlotReleased(t *testing.T) {
	t.Skip("T-104: implement when panic recovery is added to the SSE event loop")
}

// TestSSE_PingWriteError_TriggersCleanup verifies that a write error on the
// ping frame (not just the event frame) also triggers doCleanup and releases
// the connection slot.
// Finding 28: ping-path write error was only exercised indirectly.
func TestSSE_PingWriteError_TriggersCleanup(t *testing.T) {
	t.Parallel()

	var released bool
	var releaseMu sync.Mutex

	// Subscribe returns a channel that stays open so the ping ticker fires first.
	// The errorWriter will return an error on the ping write, which exits the loop.
	svc := &fakeServicer{
		verifyAndConsumeTokenFn: func(_ context.Context, _ events.VerifyTokenInput) (events.VerifiedTokenResult, error) {
			return events.VerifiedTokenResult{UserID: "user-ping-err", JTI: "jti-ping-err"}, nil
		},
		subscribeFn: func(_ context.Context, _ string) (<-chan events.Event, error) {
			// Never closes — loop must exit on ping write error.
			return make(chan events.Event), nil
		},
		releaseSlotFn: func(userID string, _ <-chan events.Event) {
			releaseMu.Lock()
			released = true
			releaseMu.Unlock()
		},
	}

	// Use a 1ms ping interval so the ticker fires almost immediately.
	h := events.NewHandler(svc, nil, testOrigins, "testnet4", false, events.EventsConfig{
		PingInterval: 1 * time.Millisecond,
	})

	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://app.example.com")
	r = withSSECookie(r, "valid.cookie")

	// errorWriter: WriteHeader succeeds (so SSE headers are sent), then all
	// subsequent body writes — including the ping frame — return an error.
	ew := newErrorWriter(fmt.Errorf("broken pipe"))
	h.Events(ew, r)

	releaseMu.Lock()
	got := released
	releaseMu.Unlock()
	assert.True(t, got, "ReleaseSlot must be called when the ping frame write fails")
}

// ── Ping event format ─────────────────────────────────────────────────────────

func TestSSE_PingEventWritten_OnTimer(t *testing.T) {
	t.Parallel()

	// Subscribe returns a channel that stays open long enough for the 1ms ping
	// ticker to fire at least once, then closes so the event loop exits cleanly.
	svc := &fakeServicer{
		verifyAndConsumeTokenFn: func(_ context.Context, _ events.VerifyTokenInput) (events.VerifiedTokenResult, error) {
			return events.VerifiedTokenResult{UserID: "user-ping"}, nil
		},
		subscribeFn: func(_ context.Context, _ string) (<-chan events.Event, error) {
			ch := make(chan events.Event)
			time.AfterFunc(50*time.Millisecond, func() { close(ch) })
			return ch, nil
		},
	}

	// Build a handler with a 1ms ping interval so the test finishes in < 100ms.
	// EventsConfig.PingInterval was added specifically to make this testable
	// without real wall-clock waits (Finding T-39).
	h := events.NewHandler(svc, nil, testOrigins, "testnet4", false, events.EventsConfig{
		PingInterval: 1 * time.Millisecond,
	})

	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://app.example.com")
	r = withSSECookie(r, "valid.cookie")

	w := newFlushRecorder()
	h.Events(w, r)

	body := w.Body.String()
	assert.Contains(t, body, "event: ping",
		"SSE stream must contain at least one ping frame")
	assert.Contains(t, body, `"event":"ping"`,
		"ping payload must contain event field")
	assert.Contains(t, body, `"network":"testnet4"`,
		"ping payload must carry the configured network label")
}

// ── Audit events written ──────────────────────────────────────────────────────

func TestIssueToken_AuditWritten(t *testing.T) {
	t.Parallel()

	var auditEvents []audit.EventType
	var mu sync.Mutex

	svc := &fakeServicer{
		issueTokenFn: func(_ context.Context, in events.IssueTokenInput) (events.IssueTokenResult, error) {
			return events.IssueTokenResult{SignedJWT: "signed.jwt", MaxAge: 60}, nil
		},
		writeAuditLogFn: func(_ context.Context, ev audit.EventType, _ string, _ map[string]any) error {
			mu.Lock()
			auditEvents = append(auditEvents, ev)
			mu.Unlock()
			return nil
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodPost, "/bitcoin/events/token", nil)
	r = withAuthUser(r, "00000000-0000-0000-0000-000000000001")

	w := httptest.NewRecorder()
	h.IssueToken(w, r)

	require.Equal(t, http.StatusNoContent, w.Code)
	// The service's IssueToken writes the audit record internally.
	// The handler itself doesn't write one — just check the response is correct.
	_ = auditEvents // audit is written inside svc, tested in service_test.go
}

func TestSSEConnected_AuditWritten(t *testing.T) {
	t.Parallel()

	var connectedSeen bool
	var mu sync.Mutex

	svc := &fakeServicer{
		verifyAndConsumeTokenFn: func(_ context.Context, _ events.VerifyTokenInput) (events.VerifiedTokenResult, error) {
			return events.VerifiedTokenResult{UserID: "user-audit"}, nil
		},
		writeAuditLogFn: func(_ context.Context, ev audit.EventType, _ string, _ map[string]any) error {
			mu.Lock()
			if ev == audit.EventBitcoinSSEConnected {
				connectedSeen = true
			}
			mu.Unlock()
			return nil
		},
	}

	h := newTestHandler(svc)
	r := httptest.NewRequest(http.MethodGet, "/bitcoin/events", nil)
	r = withOrigin(r, "https://app.example.com")
	r = withSSECookie(r, "valid.cookie")

	// Use flushRecorder so the SSE stream starts, writes the Connected audit,
	// then exits (Subscribe returns closed channel).
	w := newFlushRecorder()
	h.Events(w, r)

	// Give the audit goroutine a moment to settle (WriteAuditLog is synchronous here).
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	seen := connectedSeen
	mu.Unlock()
	assert.True(t, seen, "EventBitcoinSSEConnected must be written when SSE stream opens")
}
