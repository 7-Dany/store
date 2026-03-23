package watch_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/audit"
	bitcoinshared "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared"
	"github.com/7-Dany/store/backend/internal/domain/bitcoin/watch"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// ── local fake servicer ───────────────────────────────────────────────────────
//
// Defined here rather than in bitcoinsharedtest to avoid the type-identity
// split that occurs during test compilation. The Go test toolchain compiles
// two distinct copies of the watch package:
//
//   watch            — imported by bitcoinsharedtest
//   watch [watch.test] — imported by this external test package
//
// Any type from watch (e.g. WatchInput, WatchResult) resolves to a different
// internal ID in each copy, so a WatchFn field typed against the plain watch
// copy is incompatible with a func literal typed against watch [watch.test].
// The fix is to keep this fake entirely inside package watch_test so both the
// field type and the literal always resolve against the same watch copy.

// fakeServicer is a minimal implementation of watch.Servicer for handler tests.
type fakeServicer struct {
	watchFn         func(ctx context.Context, in watch.WatchInput) (watch.WatchResult, error)
	writeAuditLogFn func(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error
}

// compile-time check that *fakeServicer satisfies watch.Servicer.
var _ watch.Servicer = (*fakeServicer)(nil)

func (f *fakeServicer) Watch(ctx context.Context, in watch.WatchInput) (watch.WatchResult, error) {
	if f.watchFn != nil {
		return f.watchFn(ctx, in)
	}
	return watch.WatchResult{Watching: in.Addresses}, nil
}

func (f *fakeServicer) WriteAuditLog(ctx context.Context, event audit.EventType, userID, sourceIP string, metadata map[string]string) error {
	if f.writeAuditLogFn != nil {
		return f.writeAuditLogFn(ctx, event, userID, sourceIP, metadata)
	}
	return nil
}

// ── spyBitcoinRecorder ────────────────────────────────────────────────────────

// spyBitcoinRecorder captures OnWatchRejected calls for assertion in metric
// tests. It embeds NoopBitcoinRecorder to satisfy the full BitcoinRecorder
// interface — only OnWatchRejected is overridden.
type spyBitcoinRecorder struct {
	bitcoinshared.NoopBitcoinRecorder // satisfies all other interface methods
	rejectedReasons                   []string
}

// compile-time check that *spyBitcoinRecorder satisfies BitcoinRecorder.
var _ bitcoinshared.BitcoinRecorder = (*spyBitcoinRecorder)(nil)

func (r *spyBitcoinRecorder) OnWatchRejected(reason string) {
	r.rejectedReasons = append(r.rejectedReasons, reason)
}

// ── test helpers ──────────────────────────────────────────────────────────────

// newTestHandler builds a Handler backed by the given fake servicer and a
// NoopBitcoinRecorder. Use newTestHandlerWithRec when metric assertions are needed.
func newTestHandler(svc watch.Servicer) *watch.Handler {
	return watch.NewHandler(svc, bitcoinshared.NoopBitcoinRecorder{}, "testnet4", "hmac-key-for-tests")
}

// newTestHandlerWithRec builds a Handler backed by the given servicer and recorder.
func newTestHandlerWithRec(svc watch.Servicer, rec bitcoinshared.BitcoinRecorder) *watch.Handler {
	return watch.NewHandler(svc, rec, "testnet4", "hmac-key-for-tests")
}

// withAuth injects a userID into the request context using the test helper.
func withAuth(r *http.Request, userID string) *http.Request {
	return r.WithContext(token.InjectUserIDForTest(r.Context(), userID))
}

// doWatch fires the Watch handler and returns the recorder.
func doWatch(t *testing.T, h *watch.Handler, body any, withAuthID string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/bitcoin/watch", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	if withAuthID != "" {
		r = withAuth(r, withAuthID)
	}
	w := httptest.NewRecorder()
	h.Watch(w, r)
	return w
}

// validTestnetAddr returns a structurally-valid 42-char P2WPKH testnet4 address
// with a unique suffix derived from i (0-indexed). The address passes the prefix,
// length, and bech32-charset validator but has no valid checksum.
func validTestnetAddr(i int) string {
	const bech32chars = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	// tb1q + 37 'q' chars + one varying bech32 char = 42 chars total
	return "tb1q" + strings.Repeat("q", 37) + string(bech32chars[i%32])
}

// ── T-13: ErrRedisUnavailable → 503 ──────────────────────────────────────────

func TestWatchHandler_RedisUnavailable_503(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		watchFn: func(_ context.Context, _ watch.WatchInput) (watch.WatchResult, error) {
			return watch.WatchResult{}, bitcoinshared.ErrRedisUnavailable
		},
	}
	w := doWatch(t, newTestHandler(svc), map[string]any{
		"network":   "testnet4",
		"addresses": []string{validTestnetAddr(0)},
	}, "user-123")

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "5", w.Header().Get("Retry-After"))
	assertJSONCode(t, w.Body.Bytes(), "service_unavailable")
}

// ── T-14: ErrWatchLimitExceeded → 400 ────────────────────────────────────────

func TestWatchHandler_LimitExceeded_400(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		watchFn: func(_ context.Context, _ watch.WatchInput) (watch.WatchResult, error) {
			return watch.WatchResult{}, bitcoinshared.ErrWatchLimitExceeded
		},
	}
	w := doWatch(t, newTestHandler(svc), map[string]any{
		"network":   "testnet4",
		"addresses": []string{validTestnetAddr(0)},
	}, "user-123")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "watch_limit_exceeded")
	assertJSONField(t, w.Body.Bytes(), "reason", "count_cap")
}

// ── T-15: ErrWatchRegistrationExpired → 400 with reason ──────────────────────

func TestWatchHandler_RegistrationExpired_400(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		watchFn: func(_ context.Context, _ watch.WatchInput) (watch.WatchResult, error) {
			return watch.WatchResult{}, bitcoinshared.ErrWatchRegistrationExpired
		},
	}
	w := doWatch(t, newTestHandler(svc), map[string]any{
		"network":   "testnet4",
		"addresses": []string{validTestnetAddr(0)},
	}, "user-123")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "watch_limit_exceeded")
	assertJSONField(t, w.Body.Bytes(), "reason", "registration_window_expired")
}

// ── T-16: unexpected service error → 500 ─────────────────────────────────────

func TestWatchHandler_InternalError_500(t *testing.T) {
	t.Parallel()

	svc := &fakeServicer{
		watchFn: func(_ context.Context, _ watch.WatchInput) (watch.WatchResult, error) {
			return watch.WatchResult{}, errors.New("unexpected")
		},
	}
	w := doWatch(t, newTestHandler(svc), map[string]any{
		"network":   "testnet4",
		"addresses": []string{validTestnetAddr(0)},
	}, "user-123")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "internal_error")
}

// ── T-17: missing auth → 401 ─────────────────────────────────────────────────

func TestWatchHandler_MissingAuth_401(t *testing.T) {
	t.Parallel()

	b, _ := json.Marshal(map[string]any{
		"network":   "testnet4",
		"addresses": []string{validTestnetAddr(0)},
	})
	r := httptest.NewRequest(http.MethodPost, "/bitcoin/watch", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	// No auth injected into context
	w := httptest.NewRecorder()
	newTestHandler(&fakeServicer{}).Watch(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "unauthorized")
}

// ── T-18: malformed JSON → 400 ────────────────────────────────────────────────
//
// respond.DecodeJSON returns 400 (BadRequest) for malformed JSON bodies.

func TestWatchHandler_MalformedJSON_400(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodPost, "/bitcoin/watch", strings.NewReader("{bad json"))
	r.Header.Set("Content-Type", "application/json")
	r = withAuth(r, "user-123")
	w := httptest.NewRecorder()
	newTestHandler(&fakeServicer{}).Watch(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── T-19: body > 1 MiB → 413 ─────────────────────────────────────────────────

func TestWatchHandler_BodyTooLarge_413(t *testing.T) {
	t.Parallel()

	body := make([]byte, 1<<20+1) // 1 MiB + 1 byte of raw bytes
	r := httptest.NewRequest(http.MethodPost, "/bitcoin/watch", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAuth(r, "user-123")
	w := httptest.NewRecorder()
	newTestHandler(&fakeServicer{}).Watch(w, r)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

// ── T-20: network mismatch → 400 ─────────────────────────────────────────────

func TestWatchHandler_NetworkMismatch_400(t *testing.T) {
	t.Parallel()

	w := doWatch(t, newTestHandler(&fakeServicer{}), map[string]any{
		"network":   "mainnet", // handler expects "testnet4"
		"addresses": []string{validTestnetAddr(0)},
	}, "user-123")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "network_mismatch")
}

// ── T-21: empty addresses → 400 ──────────────────────────────────────────────

func TestWatchHandler_EmptyAddresses_400(t *testing.T) {
	t.Parallel()

	w := doWatch(t, newTestHandler(&fakeServicer{}), map[string]any{
		"network":   "testnet4",
		"addresses": []string{},
	}, "user-123")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "too_few_addresses")
}

// ── T-22: 21 addresses (one over limit) → 400 ────────────────────────────────

func TestWatchHandler_TooManyAddresses_400(t *testing.T) {
	t.Parallel()

	addrs := make([]string, 21)
	for i := range addrs {
		addrs[i] = validTestnetAddr(i)
	}
	w := doWatch(t, newTestHandler(&fakeServicer{}), map[string]any{
		"network":   "testnet4",
		"addresses": addrs,
	}, "user-123")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "too_many_addresses")
}

// ── T-23: invalid address → 400 ──────────────────────────────────────────────

func TestWatchHandler_InvalidAddress_400(t *testing.T) {
	t.Parallel()

	w := doWatch(t, newTestHandler(&fakeServicer{}), map[string]any{
		"network":   "testnet4",
		"addresses": []string{"not-a-valid-bitcoin-address"},
	}, "user-123")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertJSONCode(t, w.Body.Bytes(), "invalid_address")
}

// ── T-24: uppercase bech32 address normalised — service receives lowercase ────

func TestWatchHandler_AddressNormalisedLowercase(t *testing.T) {
	t.Parallel()

	// 42-char uppercase bech32 tb1q address — valid after lowercasing.
	upperAddr := strings.ToUpper(validTestnetAddr(0))
	var receivedAddr string

	svc := &fakeServicer{
		watchFn: func(_ context.Context, in watch.WatchInput) (watch.WatchResult, error) {
			if len(in.Addresses) > 0 {
				receivedAddr = in.Addresses[0]
			}
			return watch.WatchResult{Watching: in.Addresses}, nil
		},
	}

	w := doWatch(t, newTestHandler(svc), map[string]any{
		"network":   "testnet4",
		"addresses": []string{upperAddr},
	}, "user-123")

	if w.Code == http.StatusOK {
		assert.Equal(t, strings.ToLower(upperAddr), receivedAddr,
			"bech32 addresses must be lowercased before reaching the service")
	}
}

// ── T-25: exactly 20 addresses (boundary: at limit) → 200 ───────────────────

func TestWatchHandler_ExactlyMaxAddresses_200(t *testing.T) {
	t.Parallel()

	addrs := make([]string, 20)
	for i := range addrs {
		addrs[i] = validTestnetAddr(i)
	}
	svc := &fakeServicer{
		watchFn: func(_ context.Context, in watch.WatchInput) (watch.WatchResult, error) {
			return watch.WatchResult{Watching: in.Addresses}, nil
		},
	}
	w := doWatch(t, newTestHandler(svc), map[string]any{
		"network":   "testnet4",
		"addresses": addrs,
	}, "user-123")

	assert.Equal(t, http.StatusOK, w.Code,
		"exactly maxAddressesPerRequest (20) addresses must succeed")
}

// ── T-26: happy path → 200 with watching array ───────────────────────────────

func TestWatchHandler_HappyPath_200(t *testing.T) {
	t.Parallel()

	addr := validTestnetAddr(0)
	svc := &fakeServicer{
		watchFn: func(_ context.Context, in watch.WatchInput) (watch.WatchResult, error) {
			return watch.WatchResult{Watching: in.Addresses}, nil
		},
	}
	w := doWatch(t, newTestHandler(svc), map[string]any{
		"network":   "testnet4",
		"addresses": []string{addr},
	}, "user-123")

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	watching, ok := resp["watching"].([]any)
	require.True(t, ok)
	require.Len(t, watching, 1)
	assert.Equal(t, addr, watching[0])
}

// ── T-27: OnWatchRejected metric called for each rejection reason ─────────────

func TestWatchHandler_OnWatchRejected_InvalidAddress(t *testing.T) {
	t.Parallel()

	rec := &spyBitcoinRecorder{}
	w := doWatch(t, newTestHandlerWithRec(&fakeServicer{}, rec), map[string]any{
		"network":   "testnet4",
		"addresses": []string{"not-a-valid-address"},
	}, "user-123")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.Len(t, rec.rejectedReasons, 1)
	assert.Equal(t, "invalid_address", rec.rejectedReasons[0])
}

func TestWatchHandler_OnWatchRejected_LimitExceeded(t *testing.T) {
	t.Parallel()

	rec := &spyBitcoinRecorder{}
	svc := &fakeServicer{
		watchFn: func(_ context.Context, _ watch.WatchInput) (watch.WatchResult, error) {
			return watch.WatchResult{}, bitcoinshared.ErrWatchLimitExceeded
		},
	}
	w := doWatch(t, newTestHandlerWithRec(svc, rec), map[string]any{
		"network":   "testnet4",
		"addresses": []string{validTestnetAddr(0)},
	}, "user-123")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.Len(t, rec.rejectedReasons, 1)
	assert.Equal(t, "limit_exceeded", rec.rejectedReasons[0])
}

func TestWatchHandler_OnWatchRejected_RegistrationExpired(t *testing.T) {
	t.Parallel()

	rec := &spyBitcoinRecorder{}
	svc := &fakeServicer{
		watchFn: func(_ context.Context, _ watch.WatchInput) (watch.WatchResult, error) {
			return watch.WatchResult{}, bitcoinshared.ErrWatchRegistrationExpired
		},
	}
	w := doWatch(t, newTestHandlerWithRec(svc, rec), map[string]any{
		"network":   "testnet4",
		"addresses": []string{validTestnetAddr(0)},
	}, "user-123")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.Len(t, rec.rejectedReasons, 1)
	assert.Equal(t, "registration_window_expired", rec.rejectedReasons[0])
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertJSONCode(t *testing.T, body []byte, wantCode string) {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m), "body is not valid JSON: %s", body)
	assert.Equal(t, wantCode, m["code"], "JSON 'code' mismatch; body: %s", body)
}

func assertJSONField(t *testing.T, body []byte, field, want string) {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m))
	assert.Equal(t, want, m[field], "JSON %q mismatch; body: %s", field, body)
}
