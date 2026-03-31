package events

// White-box service tests — package events (not events_test) so test doubles
// can satisfy the unexported recorder and ZMQSubscriber interfaces without an
// export shim, and so private helpers (computeSID, computeIPClaim) are directly
// accessible.
//
// NOTE: this file must NOT import bitcoinsharedtest (or any package that
// imports events). Because this file is package events, not events_test, doing
// so would create the import cycle:
//
//	events → bitcoinsharedtest → events
//
// All fakes are defined locally below.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// ── local fakes ───────────────────────────────────────────────────────────────

// localFakeStorer is a hand-written Storer for service unit tests.
// Each method delegates to its Fn field if non-nil; otherwise returns safe defaults.
type localFakeStorer struct {
	StoreSessionSIDFn             func(ctx context.Context, jti, sessionID string, ttl time.Duration) error
	GetDelSessionSIDFn            func(ctx context.Context, jti string) (string, error)
	ConsumeJTIFn                  func(ctx context.Context, jti string, ttl time.Duration) (bool, error)
	RecordTokenIssuanceFn         func(ctx context.Context, vendorID [16]byte, network, jtiHash string, sourceIPHash *string, expiresAt time.Time) error
	WriteAuditLogFn               func(ctx context.Context, event audit.EventType, userID string, metadata map[string]any) error
	GetUserWatchAddressesFn       func(ctx context.Context, userID, network string) ([]string, error)
	UpsertWatchBitcoinTxStatusFn  func(ctx context.Context, in TrackedStatusUpsertInput) error
	TouchBitcoinTxStatusMempoolFn func(ctx context.Context, userID, network, txid string, feeRateSatVByte float64, lastSeenAt time.Time) error
	ConfirmBitcoinTxStatusFn      func(ctx context.Context, userID, network, txid, blockHash string, confirmations int, blockHeight int64, confirmedAt time.Time) error
	MarkBitcoinTxStatusReplacedFn func(ctx context.Context, userID, network, replacedTxID, replacementTxID string, replacedAt time.Time) error
	ListBitcoinTxStatusUsersFn    func(ctx context.Context, network, txid string) ([]string, error)
	ListActiveTxWatchUsersFn      func(ctx context.Context, network, txid string) ([]string, error)
}

var _ Storer = (*localFakeStorer)(nil)

func (f *localFakeStorer) StoreSessionSID(ctx context.Context, jti, sessionID string, ttl time.Duration) error {
	if f.StoreSessionSIDFn != nil {
		return f.StoreSessionSIDFn(ctx, jti, sessionID, ttl)
	}
	return nil
}
func (f *localFakeStorer) GetDelSessionSID(ctx context.Context, jti string) (string, error) {
	if f.GetDelSessionSIDFn != nil {
		return f.GetDelSessionSIDFn(ctx, jti)
	}
	return "default-session-id", nil
}
func (f *localFakeStorer) ConsumeJTI(ctx context.Context, jti string, ttl time.Duration) (bool, error) {
	if f.ConsumeJTIFn != nil {
		return f.ConsumeJTIFn(ctx, jti, ttl)
	}
	return true, nil
}
func (f *localFakeStorer) RecordTokenIssuance(ctx context.Context, vendorID [16]byte, network, jtiHash string, sourceIPHash *string, expiresAt time.Time) error {
	if f.RecordTokenIssuanceFn != nil {
		return f.RecordTokenIssuanceFn(ctx, vendorID, network, jtiHash, sourceIPHash, expiresAt)
	}
	return nil
}
func (f *localFakeStorer) WriteAuditLog(ctx context.Context, event audit.EventType, userID string, metadata map[string]any) error {
	if f.WriteAuditLogFn != nil {
		return f.WriteAuditLogFn(ctx, event, userID, metadata)
	}
	return nil
}
func (f *localFakeStorer) GetUserWatchAddresses(ctx context.Context, userID, network string) ([]string, error) {
	if f.GetUserWatchAddressesFn != nil {
		return f.GetUserWatchAddressesFn(ctx, userID, network)
	}
	return nil, nil
}
func (f *localFakeStorer) UpsertWatchBitcoinTxStatus(ctx context.Context, in TrackedStatusUpsertInput) error {
	if f.UpsertWatchBitcoinTxStatusFn != nil {
		return f.UpsertWatchBitcoinTxStatusFn(ctx, in)
	}
	return nil
}
func (f *localFakeStorer) TouchBitcoinTxStatusMempool(ctx context.Context, userID, network, txid string, feeRateSatVByte float64, lastSeenAt time.Time) error {
	if f.TouchBitcoinTxStatusMempoolFn != nil {
		return f.TouchBitcoinTxStatusMempoolFn(ctx, userID, network, txid, feeRateSatVByte, lastSeenAt)
	}
	return nil
}
func (f *localFakeStorer) ConfirmBitcoinTxStatus(ctx context.Context, userID, network, txid, blockHash string, confirmations int, blockHeight int64, confirmedAt time.Time) error {
	if f.ConfirmBitcoinTxStatusFn != nil {
		return f.ConfirmBitcoinTxStatusFn(ctx, userID, network, txid, blockHash, confirmations, blockHeight, confirmedAt)
	}
	return nil
}
func (f *localFakeStorer) MarkBitcoinTxStatusReplaced(ctx context.Context, userID, network, replacedTxID, replacementTxID string, replacedAt time.Time) error {
	if f.MarkBitcoinTxStatusReplacedFn != nil {
		return f.MarkBitcoinTxStatusReplacedFn(ctx, userID, network, replacedTxID, replacementTxID, replacedAt)
	}
	return nil
}
func (f *localFakeStorer) ListBitcoinTxStatusUsersByTxID(ctx context.Context, network, txid string) ([]string, error) {
	if f.ListBitcoinTxStatusUsersFn != nil {
		return f.ListBitcoinTxStatusUsersFn(ctx, network, txid)
	}
	return nil, nil
}
func (f *localFakeStorer) ListActiveBitcoinTransactionWatchUsersByTxID(ctx context.Context, network, txid string) ([]string, error) {
	if f.ListActiveTxWatchUsersFn != nil {
		return f.ListActiveTxWatchUsersFn(ctx, network, txid)
	}
	return nil, nil
}

// newRoundTripStorer returns a Storer that saves StoreSessionSID values and
// returns them in GetDelSessionSID so IssueToken + VerifyAndConsumeToken
// roundtrips work correctly in unit tests.
func newRoundTripStorer() *localFakeStorer {
	var mu sync.Mutex
	sids := map[string]string{}
	jtis := map[string]bool{}
	return &localFakeStorer{
		StoreSessionSIDFn: func(_ context.Context, jti, sessionID string, _ time.Duration) error {
			mu.Lock()
			sids[jti] = sessionID
			mu.Unlock()
			return nil
		},
		GetDelSessionSIDFn: func(_ context.Context, jti string) (string, error) {
			mu.Lock()
			sid, ok := sids[jti]
			delete(sids, jti)
			mu.Unlock()
			if !ok {
				return "", kvstore.ErrNotFound
			}
			return sid, nil
		},
		ConsumeJTIFn: func(_ context.Context, jti string, _ time.Duration) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			if jtis[jti] {
				return false, nil
			}
			jtis[jti] = true
			return true, nil
		},
	}
}

// fakeRecorder satisfies the unexported recorder interface.
type fakeRecorder struct {
	tokenConsumeFailedReason string
	sseConnCount             int
	msgDropCount             int32
}

func (r *fakeRecorder) SetSSEConnections(count int)        { r.sseConnCount = count }
func (r *fakeRecorder) OnTokenConsumeFailed(reason string) { r.tokenConsumeFailedReason = reason }
func (r *fakeRecorder) OnMessageDropped(_ string)          { atomic.AddInt32(&r.msgDropCount, 1) }
func (r *fakeRecorder) SetZMQConnected(_ bool)             {}
func (r *fakeRecorder) SetRPCConnected(_ bool)             {}
func (r *fakeRecorder) SetZMQLastMessageAge(_ float64)     {}
func (r *fakeRecorder) OnTokenIssuanceDBMiss()             {}

// fakeSubscriber satisfies the unexported ZMQSubscriber interface.
type fakeSubscriber struct {
	ready        bool
	connected    bool
	lastSeenHash string
	lastHashTime int64
}

func (s *fakeSubscriber) IsReady() bool        { return s.ready }
func (s *fakeSubscriber) IsConnected() bool    { return s.connected }
func (s *fakeSubscriber) LastSeenHash() string { return s.lastSeenHash }
func (s *fakeSubscriber) LastHashTime() int64  { return s.lastHashTime }

// ── test helpers ──────────────────────────────────────────────────────────────

// testCfg returns a minimal EventsConfig suitable for unit tests.
func testCfg() EventsConfig {
	return EventsConfig{
		TokenTTL:         5 * time.Minute,
		SigningSecret:    "test-signing-secret-32-bytes-long!",
		SessionSecret:    "test-session-secret-32-bytes-long",
		ServerSecret:     "test-server-secret-32-bytes-long!",
		DailyRotationKey: "daily-rotation-key",
		Network:          "testnet4",
		BindIP:           false,
		MaxSSEPerUser:    3,
		MaxSSEProcess:    100,
		BlockRPCTimeout:  2 * time.Second,
	}
}

// newTestCounter returns a ConnectionCounter backed by InMemoryStore.
func newTestCounter(max int) *ratelimit.ConnectionCounter {
	mem := kvstore.NewInMemoryStore(0)
	return ratelimit.NewConnectionCounter(mem, "test:conn:", max, 2*time.Hour, nil)
}

// newTestService constructs a Service for unit tests.
// Background goroutines are started; t.Cleanup calls svc.Shutdown().
func newTestService(t *testing.T, st Storer, cfg EventsConfig) *Service {
	t.Helper()
	broker := NewBroker(cfg.MaxSSEProcess, nil)
	counter := newTestCounter(cfg.MaxSSEPerUser)
	svc := NewService(context.Background(), st, broker, counter, &fakeRecorder{}, &fakeSubscriber{ready: true, connected: true}, cfg)
	t.Cleanup(func() { svc.Shutdown() })
	return svc
}

// issueAndGetRaw calls IssueToken and returns the raw signed JWT.
func issueAndGetRaw(t *testing.T, svc *Service, sessionID, clientIP string) string {
	t.Helper()
	result, err := svc.IssueToken(context.Background(), IssueTokenInput{
		VendorID:  [16]byte(uuid.New()),
		SessionID: sessionID,
		ClientIP:  clientIP,
	})
	require.NoError(t, err)
	return result.SignedJWT
}

// ── T-69 – T-71: computeIPClaim ───────────────────────────────────────────────

func TestTokenIPBinding_IPv4_ClaimSet(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "203.0.113.0/24", computeIPClaim("203.0.113.42", true))
}

func TestTokenIPBinding_IPv6_NoClaimSet(t *testing.T) {
	t.Parallel()
	assert.Empty(t, computeIPClaim("2001:db8::1", true), "IPv6 must produce empty claim even with BindIP=true")
}

func TestTokenIPBinding_DisabledFlag_NoClaimSet(t *testing.T) {
	t.Parallel()
	assert.Empty(t, computeIPClaim("203.0.113.42", false), "BindIP=false must produce empty claim")
}

// ── T-141 – T-146: IP binding via full IssueToken → VerifyAndConsumeToken ────

func TestTokenIPBinding_FalseWithIPv4_NoIPClaim(t *testing.T) {
	t.Parallel()
	cfg := testCfg()
	cfg.BindIP = false
	svc := newTestService(t, newRoundTripStorer(), cfg)

	raw := issueAndGetRaw(t, svc, "sess-t141", "10.0.0.1")

	result, err := svc.VerifyAndConsumeToken(context.Background(), VerifyTokenInput{
		RawCookie: raw,
		ClientIP:  "172.16.0.99", // different subnet — OK because no claim embedded
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result.UserID)
}

func TestTokenIPBinding_NoIPClaim_DifferentIPv4_Succeeds(t *testing.T) {
	t.Parallel()
	cfg := testCfg()
	cfg.BindIP = false
	svc := newTestService(t, newRoundTripStorer(), cfg)

	raw := issueAndGetRaw(t, svc, "sess-t142", "10.0.0.1")
	_, err := svc.VerifyAndConsumeToken(context.Background(), VerifyTokenInput{
		RawCookie: raw,
		ClientIP:  "192.168.1.1",
	})
	require.NoError(t, err)
}

func TestTokenIPBinding_IPv4Claim_DifferentSlash24_Fails(t *testing.T) {
	t.Parallel()
	cfg := testCfg()
	cfg.BindIP = true
	svc := newTestService(t, newRoundTripStorer(), cfg)

	raw := issueAndGetRaw(t, svc, "sess-t143", "203.0.113.1") // claim: 203.0.113.0/24
	_, err := svc.VerifyAndConsumeToken(context.Background(), VerifyTokenInput{
		RawCookie: raw,
		ClientIP:  "203.0.114.1", // different /24
	})
	assert.ErrorIs(t, err, ErrSSEIPMismatch)
}

func TestTokenIPBinding_IPv4Claim_SameSlash24_DifferentHost_Succeeds(t *testing.T) {
	t.Parallel()
	cfg := testCfg()
	cfg.BindIP = true
	svc := newTestService(t, newRoundTripStorer(), cfg)

	raw := issueAndGetRaw(t, svc, "sess-t144", "203.0.113.10")
	_, err := svc.VerifyAndConsumeToken(context.Background(), VerifyTokenInput{
		RawCookie: raw,
		ClientIP:  "203.0.113.200", // same /24, different host
	})
	require.NoError(t, err)
}

func TestTokenIPBinding_IPv4Claim_SubnetBoundary_DotZero_Succeeds(t *testing.T) {
	t.Parallel()
	cfg := testCfg()
	cfg.BindIP = true
	svc := newTestService(t, newRoundTripStorer(), cfg)

	raw := issueAndGetRaw(t, svc, "sess-t145", "10.0.0.1")
	_, err := svc.VerifyAndConsumeToken(context.Background(), VerifyTokenInput{
		RawCookie: raw,
		ClientIP:  "10.0.0.0", // network address (.0) — must be inside /24
	})
	require.NoError(t, err)
}

func TestTokenIPBinding_IPv4Claim_SubnetBoundary_Dot255_Succeeds(t *testing.T) {
	t.Parallel()
	cfg := testCfg()
	cfg.BindIP = true
	svc := newTestService(t, newRoundTripStorer(), cfg)

	raw := issueAndGetRaw(t, svc, "sess-t146", "10.0.0.1")
	_, err := svc.VerifyAndConsumeToken(context.Background(), VerifyTokenInput{
		RawCookie: raw,
		ClientIP:  "10.0.0.255", // broadcast (.255) — must be inside /24
	})
	require.NoError(t, err)
}

// ── T-147 – T-148: computeSID encoding ───────────────────────────────────────

func TestSIDBinding_LengthPrefixedEncoding_Roundtrip(t *testing.T) {
	t.Parallel()
	key := "test-session-secret-32-bytes-long"
	sessionID := "session:with:colons:in:it"
	jti := uuid.New().String()

	sid1 := computeSID(key, sessionID, jti)
	sid2 := computeSID(key, sessionID, jti)

	assert.Equal(t, sid1, sid2, "computeSID must be deterministic")
	assert.NotEmpty(t, sid1)
}

func TestSIDBinding_OldColonSeparator_Fails(t *testing.T) {
	t.Parallel()
	// The H-01 length prefix prevents second-preimage collisions for session IDs
	// that contain colons. Verify that two inputs that would collide under naive
	// colon concatenation produce different HMACs under the length-prefix scheme.
	//
	// Without length prefix:
	//   sessionID="abc:x", jti="z"  → concat = "abc:x:z"
	//   sessionID="abc",   jti="x:z"→ concat = "abc:x:z"  ← collision!
	//
	// With length prefix:
	//   sessionID="abc:x", jti="z"  → msg = "5:abc:x:z"
	//   sessionID="abc",   jti="x:z"→ msg = "3:abc:x:z"  ← different!
	key := "test-session-secret-32-bytes-long"

	sid1 := computeSID(key, "abc:x", "z")
	sid2 := computeSID(key, "abc", "x:z")

	assert.NotEqual(t, sid1, sid2, "length prefix must prevent session:jti collision")
}

// ── T-87 – T-88: SID HMAC verification ───────────────────────────────────────

func TestTokenSIDBinding_CorrectHMAC_Accepted(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, newRoundTripStorer(), testCfg())

	raw := issueAndGetRaw(t, svc, "test-session-t87", "10.0.0.1")
	result, err := svc.VerifyAndConsumeToken(context.Background(), VerifyTokenInput{
		RawCookie: raw,
		ClientIP:  "10.0.0.1",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result.UserID)
	assert.NotEmpty(t, result.JTI)
}

func TestTokenSIDBinding_WrongHMAC_Rejected(t *testing.T) {
	t.Parallel()
	// Storer returns a different sessionID than was stored — simulating a
	// tampered session. The sid HMAC check must reject it.
	st := &localFakeStorer{
		StoreSessionSIDFn: func(_ context.Context, _, _ string, _ time.Duration) error {
			return nil
		},
		GetDelSessionSIDFn: func(_ context.Context, _ string) (string, error) {
			return "TAMPERED-SESSION-ID", nil // different from what was issued
		},
		ConsumeJTIFn: func(_ context.Context, _ string, _ time.Duration) (bool, error) {
			return true, nil
		},
	}
	svc := newTestService(t, st, testCfg())

	raw := issueAndGetRaw(t, svc, "correct-session-id", "10.0.0.1")
	_, err := svc.VerifyAndConsumeToken(context.Background(), VerifyTokenInput{
		RawCookie: raw,
		ClientIP:  "10.0.0.1",
	})
	assert.ErrorIs(t, err, ErrSSESIDMismatch)
}

// ── T-77: ReleaseSlot runs after context cancellation ────────────────────────

func TestSSECap_DecrRunsAfterContextCancellation(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, newRoundTripStorer(), testCfg())

	ctx, cancel := context.WithCancel(context.Background())

	require.NoError(t, svc.AcquireSlot(ctx, "user-t77"))
	ch, err := svc.Subscribe(ctx, "user-t77")
	require.NoError(t, err)

	cancel() // simulate client disconnect

	done := make(chan struct{})
	go func() {
		svc.ReleaseSlot("user-t77", ch)
		close(done)
	}()
	select {
	case <-done:
		// ReleaseSlot returned — success.
	case <-time.After(3 * time.Second):
		t.Fatal("ReleaseSlot blocked for > 3s after ctx cancellation")
	}
}

// ── T-125: Shutdown drains domain goroutines ──────────────────────────────────

func TestService_Shutdown_DrainsDomainGoroutines(t *testing.T) {
	t.Parallel()
	cfg := testCfg()
	svc := NewService(
		context.Background(),
		&localFakeStorer{},
		NewBroker(cfg.MaxSSEProcess, nil),
		newTestCounter(cfg.MaxSSEPerUser),
		&fakeRecorder{},
		&fakeSubscriber{connected: true},
		cfg,
	)
	done := make(chan struct{})
	go func() {
		svc.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Shutdown did not drain goroutines within 15s")
	}
}

// ── T-137: C-01 race — wg.Add before go func ─────────────────────────────────

func TestService_Shutdown_CalledBeforeGoroutineSchedules_NoPanic(t *testing.T) {
	t.Parallel()
	cfg := testCfg()
	broker := NewBroker(cfg.MaxSSEProcess, nil)
	counter := newTestCounter(cfg.MaxSSEPerUser)
	// Repeatedly create + immediately shut down to exercise the wg.Add-before-go race.
	for i := 0; i < 30; i++ {
		svc := NewService(
			context.Background(),
			&localFakeStorer{},
			broker,
			counter,
			&fakeRecorder{},
			&fakeSubscriber{},
			cfg,
		)
		svc.Shutdown()
	}
}

// ── T-149: svc.cancel() exits the heartbeat goroutine ────────────────────────

func TestTTLGoroutine_ExitsOnSvcCtx_WithoutHTTPServerShutdown(t *testing.T) {
	t.Parallel()
	cfg := testCfg()
	svc := NewService(
		context.Background(),
		&localFakeStorer{},
		NewBroker(cfg.MaxSSEProcess, nil),
		newTestCounter(cfg.MaxSSEPerUser),
		&fakeRecorder{},
		&fakeSubscriber{connected: true},
		cfg,
	)
	done := make(chan struct{})
	go func() {
		svc.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("svc.Shutdown() blocked > 5s — heartbeat goroutine may not exit on ctx cancel")
	}
}

// ── T-159 / T-160 / T-161: Heartbeat timing tests (stubs) ───────────────────

func TestTTLGoroutine_Heartbeat_KeepsCounterKeyAlive(t *testing.T) {
	t.Skip("requires clock injection for TTL expiry control; implement in a later stage")
}

func TestTTLGoroutine_NoHeartbeat_CounterKeyExpires_CapBypassed(t *testing.T) {
	t.Skip("documents the vulnerability heartbeat fixes; requires TTL injection")
}

func TestTTLGoroutine_Heartbeat_KeyMissing_EmitsWarningMetric(t *testing.T) {
	t.Skip("requires ConnCounterRecorder injection; implement with recorder mock")
}

// ── T-167: Audit write failure falls back to log+metric (stub) ───────────────

func TestDoCleanup_AuditWriteFailure_FallbackLogged(t *testing.T) {
	t.Skip("implement when audit.Write has a testable injection point")
}

// TestIPMismatch_AuditEventEmitted verifies that ErrSSEIPMismatch also writes
// an EventBitcoinSSETokenConsumeFailure audit record with reason "ip_mismatch".
// Finding: 1g / A1g-1 — IP mismatch not audited.
func TestIPMismatch_AuditEventEmitted(t *testing.T) {
	t.Parallel()
	cfg := testCfg()
	cfg.BindIP = true

	var auditReason string
	var mu sync.Mutex
	st := newRoundTripStorer()
	st.WriteAuditLogFn = func(_ context.Context, _ audit.EventType, _ string, md map[string]any) error {
		mu.Lock()
		if r, ok := md["reason"].(string); ok {
			auditReason = r
		}
		mu.Unlock()
		return nil
	}
	svc := newTestService(t, st, cfg)

	raw := issueAndGetRaw(t, svc, "sess-ipmismatch", "10.0.0.1") // claim: 10.0.0.0/24
	_, err := svc.VerifyAndConsumeToken(context.Background(), VerifyTokenInput{
		RawCookie: raw,
		ClientIP:  "192.168.1.1", // different /24 — must fail
	})

	assert.ErrorIs(t, err, ErrSSEIPMismatch)
	mu.Lock()
	reason := auditReason
	mu.Unlock()
	assert.Equal(t, "ip_mismatch", reason, "audit must be written with reason=ip_mismatch")
}

// TestVerifyAndConsumeToken_SIDKeyMissing_ReturnsTokenExpired verifies that
// kvstore.ErrNotFound from GetDelSessionSID maps to ErrSSETokenExpired (401)
// not ErrSSERedisUnavailable (503).
// Finding: 1a / A1a-2 — kvstore.ErrNotFound incorrectly mapped to 503.
func TestVerifyAndConsumeToken_SIDKeyMissing_ReturnsTokenExpired(t *testing.T) {
	t.Parallel()

	var auditCallCount int
	var lastAuditReason string
	var auditMu sync.Mutex

	st := &localFakeStorer{
		StoreSessionSIDFn: func(_ context.Context, _, _ string, _ time.Duration) error {
			return nil
		},
		GetDelSessionSIDFn: func(_ context.Context, _ string) (string, error) {
			return "", kvstore.ErrNotFound // key has expired
		},
		WriteAuditLogFn: func(_ context.Context, _ audit.EventType, _ string, md map[string]any) error {
			auditMu.Lock()
			auditCallCount++
			if r, ok := md["reason"].(string); ok {
				lastAuditReason = r
			}
			auditMu.Unlock()
			return nil
		},
	}
	svc := newTestService(t, st, testCfg())
	raw := issueAndGetRaw(t, svc, "sess-expired", "10.0.0.1")

	_, err := svc.VerifyAndConsumeToken(context.Background(), VerifyTokenInput{
		RawCookie: raw,
		ClientIP:  "10.0.0.1",
	})
	assert.ErrorIs(t, err, ErrSSETokenExpired,
		"expired SID key must return ErrSSETokenExpired (401), not ErrSSERedisUnavailable (503)")

	// Finding 26: verify an audit record is written with reason=sid_key_expired.
	auditMu.Lock()
	count := auditCallCount
	reason := lastAuditReason
	auditMu.Unlock()
	assert.GreaterOrEqual(t, count, 1, "at least one audit record must be written on sid_key_expired path")
	assert.Equal(t, "sid_key_expired", reason, "audit record must carry reason=sid_key_expired")
}

// TestIsZMQRunning_HealthyReturnsNil verifies the happy path.
func TestIsZMQRunning_HealthyReturnsNil(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, &localFakeStorer{}, testCfg())
	assert.NoError(t, svc.IsZMQRunning())
}

// TestIsZMQRunning_DisconnectedReturnsError verifies ErrSSEZMQUnhealthy is returned.
func TestIsZMQRunning_DisconnectedReturnsError(t *testing.T) {
	t.Parallel()
	cfg := testCfg()
	broker := NewBroker(cfg.MaxSSEProcess, nil)
	counter := newTestCounter(cfg.MaxSSEPerUser)
	svc := NewService(
		context.Background(),
		&localFakeStorer{},
		broker,
		counter,
		&fakeRecorder{},
		&fakeSubscriber{ready: false, connected: false}, // disconnected
		cfg,
	)
	t.Cleanup(svc.Shutdown)
	assert.ErrorIs(t, svc.IsZMQRunning(), ErrSSEZMQUnhealthy)
}

func TestIsZMQRunning_StaleBlockStillAllowsSSE(t *testing.T) {
	t.Parallel()
	cfg := testCfg()
	broker := NewBroker(cfg.MaxSSEProcess, nil)
	counter := newTestCounter(cfg.MaxSSEPerUser)
	svc := NewService(
		context.Background(),
		&localFakeStorer{},
		broker,
		counter,
		&fakeRecorder{},
		&fakeSubscriber{ready: true, connected: false},
		cfg,
	)
	t.Cleanup(svc.Shutdown)
	assert.NoError(t, svc.IsZMQRunning(),
		"stale block age must not block SSE when the ZMQ subscriptions are still ready")
}
