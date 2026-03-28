package events

import (
	"context"
	"crypto/hmac"
	"errors"
	"net/netip"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/7-Dany/store/backend/internal/audit"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// var log is the package-level logger for the events feature.
var log = telemetry.New("events")

// ── Storer interface ──────────────────────────────────────────────────────────

// Storer is the external I/O contract for the events service.
//
// It covers only Redis and DB operations. The SSE broker (in-process channel
// fan-out) and connection counter (Redis via ratelimit.ConnectionCounter) are
// injected into Service as separate fields — they are NOT part of Storer.
type Storer interface {
	// StoreSessionSID writes the server-side session ID for the given JTI.
	// Must succeed before the SSE JWT is signed. Returns error → 503.
	StoreSessionSID(ctx context.Context, jti, sessionID string, ttl time.Duration) error

	// GetDelSessionSID reads and deletes the session ID for SID verification.
	// Returns kvstore.ErrNotFound when the key is absent (expired or never set).
	// Caller maps kvstore.ErrNotFound to ErrSSETokenExpired (401) and any other
	// error to ErrSSERedisUnavailable (503).
	GetDelSessionSID(ctx context.Context, jti string) (string, error)

	// ConsumeJTI atomically consumes the one-time JTI token.
	// Returns (true, nil) when newly consumed; (false, nil) when already used;
	// (false, err) on Redis failure.
	ConsumeJTI(ctx context.Context, jti string, ttl time.Duration) (bool, error)

	// RecordTokenIssuance inserts an sse_token_issuances row.
	// NON-FATAL — caller logs error and continues; never blocks token issuance.
	RecordTokenIssuance(ctx context.Context, vendorID [16]byte, network, jtiHash string, sourceIPHash *string, expiresAt time.Time) error

	// WriteAuditLog inserts one row into auth_audit_log.
	// Errors are non-fatal — audit write failures never fail the primary operation.
	WriteAuditLog(ctx context.Context, event audit.EventType, userID string, metadata map[string]any) error

	// GetUserWatchAddresses returns the set of registered watch addresses for userID.
	// Returns an empty slice (not an error) when the set is absent (user has no watches).
	// Used by MempoolTracker to check whether a transaction's outputs overlap a user's set.
	GetUserWatchAddresses(ctx context.Context, userID string) ([]string, error)
}

// ── ZMQSubscriber interface ───────────────────────────────────────────────────

// ZMQSubscriber is the narrow interface the events service needs from the
// bitcoin ZMQ subscriber. Depend on this interface, never on *zmq.subscriber
// directly, so the domain layer stays decoupled from the platform layer and
// tests can inject a simple fake.
type ZMQSubscriber interface {
	// IsReady reports whether the required ZMQ subscriptions are currently
	// dialled and ready to deliver events. Unlike IsConnected, this excludes
	// age-based liveness so SSE admission is not blocked by a quiet chain.
	IsReady() bool
	// IsConnected reports whether the ZMQ subscriber is currently healthy
	// (both sockets dialled and last block within the configured idle timeout).
	IsConnected() bool
	// LastSeenHash returns the most recently observed block hash in
	// Block hash in the same hex form used by RPC and block explorers. Returns
	// "" before the first block.
	LastSeenHash() string
	// LastHashTime returns the Unix nanosecond timestamp of the last
	// received block. Returns 0 before the first block is received.
	LastHashTime() int64
}

// ── RPCHealthChecker interface ────────────────────────────────────────────────

// RPCHealthChecker is the narrow interface the service needs to populate the
// RPCConnected field in StatusResult. Satisfied by a thin wrapper around the
// Bitcoin RPC client's GetBlockchainInfo call; inject via Service.SetRPCHealthCheck.
type RPCHealthChecker interface {
	// IsRPCHealthy reports whether the Bitcoin RPC endpoint is currently reachable.
	// Implementations should apply a short timeout (≤2s) to avoid blocking Status.
	IsRPCHealthy() bool
}

// ── recorder interface (narrow) ───────────────────────────────────────────────

// recorder is the narrow telemetry interface this sub-package needs.
// bitcoinshared.BitcoinRecorder already defines all required methods;
// this local narrow interface restricts what service.go can call.
type recorder interface {
	SetSSEConnections(count int)
	OnTokenConsumeFailed(reason string)
	OnMessageDropped(reason string)
	SetZMQConnected(connected bool)
	SetRPCConnected(connected bool)
	SetZMQLastMessageAge(seconds float64)
	// OnTokenIssuanceDBMiss is called when RecordTokenIssuance fails (GDPR gap).
	OnTokenIssuanceDBMiss()
}

// ── Service ───────────────────────────────────────────────────────────────────

// Service implements the business logic for the bitcoin events feature.
// It owns the SSE broker, connection counter, and ZMQ liveness goroutine.
type Service struct {
	store       Storer
	broker      *Broker
	connCounter *ratelimit.ConnectionCounter
	rec         recorder
	cfg         EventsConfig
	subscriber  ZMQSubscriber

	// rpcHealth is optional; when non-nil it is called by Status to populate
	// RPCConnected. Set via SetRPCHealthCheck after construction.
	rpcHealth RPCHealthChecker

	// activeConns tracks userIDs with active connections for the heartbeat goroutine.
	activeConns   map[string]int
	activeConnsMu sync.Mutex

	// startedAt is used by tickLiveness to emit a ZMQ-age sentinel gauge
	// before the first block is received, so dashboards show increasing age.
	startedAt time.Time

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewService constructs a Service and starts background goroutines.
//
// ctx must be the application root context — goroutines exit when it is cancelled.
// Call Shutdown() during graceful server shutdown to drain the WaitGroup.
func NewService(
	ctx context.Context,
	store Storer,
	broker *Broker,
	connCounter *ratelimit.ConnectionCounter,
	rec recorder,
	subscriber ZMQSubscriber,
	cfg EventsConfig,
) *Service {
	svcCtx, cancel := context.WithCancel(ctx)
	svc := &Service{
		store:       store,
		broker:      broker,
		connCounter: connCounter,
		rec:         rec,
		cfg:         cfg,
		subscriber:  subscriber,
		activeConns: make(map[string]int),
		startedAt:   time.Now(),
		ctx:         svcCtx,
		cancel:      cancel,
	}
	// C-01: wg.Add BEFORE go func() in the calling goroutine.
	svc.wg.Add(2)
	go svc.runLiveness()
	go svc.runHeartbeat()
	return svc
}

// SetRPCHealthCheck wires an RPCHealthChecker so that Status can populate
// RPCConnected. Call this once after NewService, before serving requests.
// Thread-safe only when called before the first Status() invocation.
func (s *Service) SetRPCHealthCheck(h RPCHealthChecker) {
	s.rpcHealth = h
}

// Shutdown cancels the service context and waits for all goroutines to exit.
func (s *Service) Shutdown() {
	s.cancel()
	s.wg.Wait()
}

// ── Service methods ───────────────────────────────────────────────────────────

// IssueToken generates a short-lived SSE one-time JWT and stores the server-side
// session binding in Redis.
//
// The caller (handler) is responsible for:
//   - Verifying the Bearer JWT and extracting userID + sessionID (steps 1–2)
//   - Writing the Set-Cookie response header from IssueTokenResult
func (s *Service) IssueToken(ctx context.Context, in IssueTokenInput) (IssueTokenResult, error) {
	jti := uuid.New().String()
	userIDStr := uuid.UUID(in.VendorID).String()

	// Step 4: compute sid HMAC (length-prefixed — H-01 fix).
	// NEVER put sessionID in the JWT. Store it server-side in Redis.
	sid := computeSID(s.cfg.SessionSecret, in.SessionID, jti)
	ipClaim := computeIPClaim(in.ClientIP, s.cfg.BindIP)

	// Security: if this SET fails, no token is issued — fail closed.
	if err := s.store.StoreSessionSID(ctx, jti, in.SessionID, s.cfg.TokenTTL); err != nil {
		return IssueTokenResult{}, ErrSSERedisUnavailable
	}

	// Step 5: DB record — non-fatal. Log, increment GDPR-miss metric, and continue.
	jtiHash := computeJTIHash(jti, s.cfg.ServerSecret)
	ipHash := computeIPHash(in.ClientIP, s.cfg.DailyRotationKey)
	expiresAt := time.Now().Add(s.cfg.TokenTTL)

	if err := s.store.RecordTokenIssuance(
		context.WithoutCancel(ctx),
		in.VendorID, s.cfg.Network, jtiHash, ipHash, expiresAt,
	); err != nil {
		// ERROR-level: a missing sse_token_issuances row creates a GDPR IP-erasure
		// gap. Alert on-call if this rate exceeds 0 for more than 5 minutes.
		log.Error(ctx, "events.IssueToken: GDPR IP-audit row missing — erasure coverage gap (non-fatal)", "error", err)
		if s.rec != nil {
			s.rec.OnTokenIssuanceDBMiss()
		}
	}

	// Step 6: audit — WithoutCancel so client disconnect cannot abort it.
	// NOTE: sourceIP is NOT written to financial_audit_events (immutable, PII).
	// The sse_token_issuances row carries the IP hash for GDPR-erasable audit.
	_ = s.store.WriteAuditLog(context.WithoutCancel(ctx), audit.EventBitcoinSSETokenIssued, userIDStr, map[string]any{
		"user_id":    userIDStr,
		"jti_hash":   jtiHash,
		"expires_at": expiresAt,
	})

	// Step 7: sign JWT.
	signed, err := token.GenerateBitcoinSSEToken(token.BitcoinSSETokenInput{
		UserID:        userIDStr,
		JTI:           jti,
		SID:           sid,
		IPClaim:       ipClaim,
		TTL:           s.cfg.TokenTTL,
		SigningSecret: s.cfg.SigningSecret,
	})
	if err != nil {
		return IssueTokenResult{}, telemetry.Service("IssueToken.sign", err)
	}

	return IssueTokenResult{SignedJWT: signed, MaxAge: int(s.cfg.TokenTTL.Seconds())}, nil
}

// VerifyAndConsumeToken validates the SSE cookie JWT, verifies the sid HMAC,
// checks the optional IP binding, pre-checks the connection cap, and atomically
// consumes the JTI one-time token.
//
// Guard sequence (events-technical.md §2, steps 3–7):
//
//  3. JWT parse + exp guard
//  4. GetDelSessionSID + sid HMAC verify (constant-time)
//  5. IP binding check (IPv4 /24 only) — audited on failure
//  6. Cap pre-check (read-only — preserves JTI if cap exceeded; fails closed on Redis error)
//  7. ConsumeJTI (authoritative one-time gate) — TTL re-checked before call
func (s *Service) VerifyAndConsumeToken(ctx context.Context, in VerifyTokenInput) (VerifiedTokenResult, error) {
	// Step 3: parse JWT.
	claims, err := token.ParseBitcoinSSEToken(in.RawCookie, s.cfg.SigningSecret)
	if err != nil {
		// Audit JWT parse failure with empty subject (identity not yet known).
		_ = s.store.WriteAuditLog(context.WithoutCancel(ctx), audit.EventBitcoinSSETokenConsumeFailure, "",
			map[string]any{"reason": "jwt_invalid"})
		return VerifiedTokenResult{}, ErrSSETokenInvalid
	}
	// Step 3 guard: prevent EX=0 on the Lua ConsumeJTI script.
	if time.Until(claims.ExpiresAt.Time) < time.Second {
		return VerifiedTokenResult{}, ErrSSETokenInvalid
	}

	// Step 4: fetch + delete server-side session ID.
	storedSessionID, err := s.store.GetDelSessionSID(ctx, claims.ID)
	if err != nil {
		// Distinguish "key expired/missing" (→ 401) from "Redis down" (→ 503).
		if errors.Is(err, kvstore.ErrNotFound) {
			// Audit the expiry so post-incident analysis can distinguish a legitimate
			// session-rotation from a replay attempt after TTL expiry.
			_ = s.store.WriteAuditLog(context.WithoutCancel(ctx), audit.EventBitcoinSSETokenConsumeFailure, "",
				map[string]any{"reason": "sid_key_expired"})
			return VerifiedTokenResult{}, ErrSSETokenExpired
		}
		return VerifiedTokenResult{}, ErrSSERedisUnavailable
	}
	expected := computeSID(s.cfg.SessionSecret, storedSessionID, claims.ID)
	// Constant-time comparison to prevent timing-oracle attacks on the HMAC.
	if !hmac.Equal([]byte(claims.SID), []byte(expected)) {
		// Security: WithoutCancel — audit must survive client disconnect.
		_ = s.store.WriteAuditLog(context.WithoutCancel(ctx), audit.EventBitcoinSSETokenConsumeFailure, claims.Subject,
			map[string]any{"reason": "sid_mismatch", "user_id": claims.Subject})
		s.rec.OnTokenConsumeFailed("sid_mismatch")
		return VerifiedTokenResult{}, ErrSSESIDMismatch
	}

	// Step 5: IPv4 /24 subnet binding.
	if claims.IPClaim != "" {
		subnet, parseErr := netip.ParsePrefix(claims.IPClaim)
		if parseErr != nil {
			return VerifiedTokenResult{}, ErrSSETokenInvalid
		}
		clientIP, addrErr := netip.ParseAddr(in.ClientIP)
		if addrErr != nil || !subnet.Contains(clientIP) {
			// Audit IP mismatch — potential token theft/replay from a different subnet.
			_ = s.store.WriteAuditLog(context.WithoutCancel(ctx), audit.EventBitcoinSSETokenConsumeFailure, claims.Subject,
				map[string]any{"reason": "ip_mismatch", "user_id": claims.Subject})
			s.rec.OnTokenConsumeFailed("ip_mismatch")
			return VerifiedTokenResult{}, ErrSSEIPMismatch
		}
	}

	// Step 6: cap pre-check (read-only — JTI NOT consumed yet so client can retry).
	// Count now returns (int64, error); fail closed on Redis error to preserve JTI.
	count, countErr := s.connCounter.Count(ctx, claims.Subject)
	if countErr != nil {
		// Redis unavailable — preserve JTI by returning 503 before consuming it.
		return VerifiedTokenResult{}, ErrSSERedisUnavailable
	}
	if count >= int64(s.cfg.MaxSSEPerUser) {
		_ = s.store.WriteAuditLog(context.WithoutCancel(ctx), audit.EventBitcoinSSECapExceeded, claims.Subject,
			map[string]any{"reason": "user_cap", "user_id": claims.Subject})
		return VerifiedTokenResult{}, ErrSSECapExceeded
	}

	// Step 7: atomically consume JTI.
	// Re-evaluate TTL immediately before calling ConsumeJTI; processing steps 4–6
	// may have taken enough time that the token has now expired.
	ttlRemaining := time.Until(claims.ExpiresAt.Time)
	if ttlRemaining <= 0 {
		return VerifiedTokenResult{}, ErrSSETokenInvalid
	}
	consumed, err := s.store.ConsumeJTI(ctx, claims.ID, ttlRemaining)
	if err != nil {
		return VerifiedTokenResult{}, ErrSSERedisUnavailable
	}
	if !consumed {
		_ = s.store.WriteAuditLog(context.WithoutCancel(ctx), audit.EventBitcoinSSETokenConsumeFailure, claims.Subject,
			map[string]any{"reason": "already_used", "user_id": claims.Subject})
		s.rec.OnTokenConsumeFailed("already_used")
		return VerifiedTokenResult{}, ErrSSETokenInvalid
	}

	return VerifiedTokenResult{
		UserID: claims.Subject,
		JTI:    claims.ID,
	}, nil
}

// AcquireSlot increments the per-user connection counter (step 8).
// Returns ErrSSECapExceeded if still at cap after JTI consumption (race guard).
// Returns ErrSSERedisUnavailable on Redis error.
func (s *Service) AcquireSlot(ctx context.Context, userID string) error {
	if err := s.connCounter.Acquire(ctx, userID); err != nil {
		if errors.Is(err, ratelimit.ErrAtCapacity) {
			return ErrSSECapExceeded
		}
		return ErrSSERedisUnavailable
	}
	s.activeConnsMu.Lock()
	s.activeConns[userID]++
	s.activeConnsMu.Unlock()

	s.rec.SetSSEConnections(s.broker.Count())
	return nil
}

// Subscribe registers a channel in the SSE broker (step 9).
// Returns ErrSSEProcessCapReached if broker BTC_MAX_SSE_PROCESS is exceeded.
func (s *Service) Subscribe(_ context.Context, userID string) (<-chan Event, error) {
	ch, err := s.broker.Subscribe(userID)
	if err != nil {
		if errors.Is(err, ErrCapReached) {
			return nil, ErrSSEProcessCapReached
		}
		return nil, err
	}
	return ch, nil
}

// ReleaseSlot decrements the connection counter and unsubscribes the channel.
// Called exactly once from doCleanup via sync.Once.
// Uses context.Background() with a 5s timeout — the handler ctx is cancelled.
// Passing a nil ch is safe — broker.Unsubscribe treats it as a no-op.
func (s *Service) ReleaseSlot(userID string, ch <-chan Event) {
	s.broker.Unsubscribe(userID, ch)
	s.connCounter.Release(userID)

	s.activeConnsMu.Lock()
	s.activeConns[userID]--
	if s.activeConns[userID] <= 0 {
		delete(s.activeConns, userID)
	}
	s.activeConnsMu.Unlock()

	s.rec.SetSSEConnections(s.broker.Count())
}

// IsZMQRunning reports whether the ZMQ subscriber is currently connected.
// Returns nil when healthy; ErrSSEZMQUnhealthy when disconnected.
func (s *Service) IsZMQRunning() error {
	if !s.subscriber.IsReady() {
		return ErrSSEZMQUnhealthy
	}
	return nil
}

// Status returns a snapshot of per-instance ZMQ health and SSE state.
func (s *Service) Status(_ context.Context) StatusResult {
	var lastBlockAge float64
	if t := s.subscriber.LastHashTime(); t > 0 {
		lastBlockAge = float64(time.Now().UnixNano()-t) / float64(time.Second)
	}
	var rpcOK bool
	if s.rpcHealth != nil {
		rpcOK = s.rpcHealth.IsRPCHealthy()
	}
	return StatusResult{
		ZMQConnected:      s.subscriber.IsConnected(),
		RPCConnected:      rpcOK,
		ActiveConnections: s.broker.Count(),
		LastBlockHashAge:  lastBlockAge,
	}
}

// WriteAuditLog delegates an audit write to the store layer.
// Used by the handler to record SSEConnected / SSEDisconnected events.
// Errors are non-fatal — the caller logs and continues.
func (s *Service) WriteAuditLog(ctx context.Context, event audit.EventType, userID string, metadata map[string]any) error {
	return s.store.WriteAuditLog(ctx, event, userID, metadata)
}

// ── Background goroutines ─────────────────────────────────────────────────────

// runLiveness ticks every 30s, updates ZMQ/RPC connected gauges.
// Exits on svc.ctx cancellation.
//
// 30-second interval: ZMQ disconnection will not be reflected in metrics for
// up to 30s (one missed tick). Operators should alert when the
// zmq_connected gauge drops to 0 or when zmq_last_message_age_seconds
// exceeds 2× the expected block interval (e.g. >20 min on mainnet).
func (s *Service) runLiveness() {
	defer s.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.tickLiveness()
		case <-s.ctx.Done():
			return
		}
	}
}

// tickLiveness performs a single liveness probe. Separated from the loop so
// tests can call it directly without ticker machinery.
func (s *Service) tickLiveness() {
	zmqOK := s.subscriber.IsConnected()
	s.rec.SetZMQConnected(zmqOK)

	// Update ZMQ last-message age gauge.
	// When no block has been received yet, emit uptime-in-seconds as a sentinel
	// so dashboards show an increasing age value rather than a missing metric.
	if hash := s.subscriber.LastSeenHash(); hash != "" {
		if t := s.subscriber.LastHashTime(); t > 0 {
			ageSec := float64(time.Now().UnixNano()-t) / float64(time.Second)
			s.rec.SetZMQLastMessageAge(ageSec)
		}
	} else {
		// Sentinel: emit seconds since service start so dashboards always have
		// a value. Operators should alert when this exceeds the expected block
		// interval (e.g. >20 min on mainnet) to detect ZMQ silence.
		s.rec.SetZMQLastMessageAge(time.Since(s.startedAt).Seconds())
	}
}

// runHeartbeat ticks every 2min and refreshes the TTL on every active connection
// counter key. Without this, a connection held for exactly 2 hours would cause
// the Redis safety TTL to expire, allowing one extra connection beyond the cap.
func (s *Service) runHeartbeat() {
	defer s.wg.Done()
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.heartbeatAll(s.ctx)
		case <-s.ctx.Done():
			return
		}
	}
}

// heartbeatAll calls connCounter.Heartbeat for every userID with active
// connections, using bounded concurrency (up to heartbeatConcurrency parallel
// Redis calls) to avoid blocking for seconds when there are many users.
//
// Ghost-TTL fix: before calling Heartbeat for an ID copied from the snapshot,
// we re-check activeConns under the lock. If the user disconnected between the
// snapshot copy and now, we skip the Heartbeat call to avoid spurious
// heartbeat-miss metrics.
const heartbeatConcurrency = 20

func (s *Service) heartbeatAll(ctx context.Context) {
	s.activeConnsMu.Lock()
	ids := make([]string, 0, len(s.activeConns))
	for id := range s.activeConns {
		ids = append(ids, id)
	}
	s.activeConnsMu.Unlock()

	sem := make(chan struct{}, heartbeatConcurrency)
	var wg sync.WaitGroup

	for _, id := range ids {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		default:
		}

		// Ghost-TTL guard: skip if this user has already fully disconnected.
		s.activeConnsMu.Lock()
		stillActive := s.activeConns[id] > 0
		s.activeConnsMu.Unlock()
		if !stillActive {
			continue
		}

		sem <- struct{}{}
		wg.Add(1)
		go func(userID string) {
			defer func() { <-sem; wg.Done() }()
			s.connCounter.Heartbeat(ctx, userID)
		}(id)
	}
	wg.Wait()
}
