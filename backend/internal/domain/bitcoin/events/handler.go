package events

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/7-Dany/store/backend/internal/audit"
	bitcoinshared "github.com/7-Dany/store/backend/internal/domain/bitcoin/shared"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

// ── Servicer interface ────────────────────────────────────────────────────────

// Servicer is the business-logic contract for the events handler.
//
// It is defined here (in handler.go) rather than service.go so that the handler
// file is the authoritative coupling point between the HTTP layer and the service.
type Servicer interface {
	// IssueToken generates a short-lived SSE one-time JWT and stores the
	// server-side session binding in Redis. Called by POST /bitcoin/events/token.
	IssueToken(ctx context.Context, in IssueTokenInput) (IssueTokenResult, error)

	// VerifyAndConsumeToken validates the SSE cookie JWT, verifies the sid HMAC,
	// checks the optional IP binding, pre-checks the connection cap, and
	// atomically consumes the JTI one-time token. Called at the start of
	// GET /bitcoin/events before the SSE stream is opened.
	VerifyAndConsumeToken(ctx context.Context, in VerifyTokenInput) (VerifiedTokenResult, error)

	// AcquireSlot increments the per-user connection counter (step 8 of the
	// GET /events guard sequence). Returns ErrSSECapExceeded or ErrSSERedisUnavailable.
	AcquireSlot(ctx context.Context, userID string) error

	// Subscribe registers a new SSE channel in the broker (step 9).
	// Returns ErrSSEProcessCapReached when the per-process ceiling is exceeded.
	Subscribe(ctx context.Context, userID string) (<-chan Event, error)

	// ReleaseSlot decrements the connection counter and unsubscribes the channel.
	// Must be called exactly once, via sync.Once in the handler cleanup path.
	// Passing a nil ch is safe — broker.Unsubscribe treats it as a no-op.
	ReleaseSlot(userID string, ch <-chan Event)

	// IsZMQRunning reports whether the ZMQ subscriber is currently connected.
	// Returns nil when healthy; ErrSSEZMQUnhealthy when disconnected.
	// Called at GET /events step 10 to gate the SSE stream open.
	IsZMQRunning() error

	// Status returns a snapshot of per-instance ZMQ health and SSE connection state.
	// Called by GET /bitcoin/events/status.
	Status(ctx context.Context) StatusResult

	// WriteAuditLog writes an audit record.
	// Used by the handler for SSEConnected / SSEDisconnected events that are
	// outside the service method boundaries.
	WriteAuditLog(ctx context.Context, event audit.EventType, userID string, metadata map[string]any) error

	// Shutdown cancels the service context and drains all background goroutines.
	// Must be called during graceful server shutdown.
	Shutdown()
}

// ── Constants ─────────────────────────────────────────────────────────────────

const (
	// sseCookieName is the HttpOnly cookie carrying the one-time SSE JWT.
	sseCookieName = "btc_sse_jti"

	// sseCookiePath restricts the SSE cookie to the events endpoint only.
	// The browser will not send it on any other path.
	sseCookiePath = "/api/v1/bitcoin/events"

	// defaultPingInterval is used when EventsConfig.PingInterval is zero.
	defaultPingInterval = 30 * time.Second
)

// pingInterval returns the configured ping interval, falling back to the default.
func pingInterval(cfg EventsConfig) time.Duration {
	if cfg.PingInterval > 0 {
		return cfg.PingInterval
	}
	return defaultPingInterval
}

// ── Handler ───────────────────────────────────────────────────────────────────

// Handler handles HTTP requests for the bitcoin events feature.
type Handler struct {
	svc             Servicer
	rec             bitcoinshared.BitcoinRecorder
	allowedOrigins  map[string]struct{}
	network         string
	secureCookies   bool
	pingInterval    time.Duration
	staticPingFrame []byte // pre-computed SSE ping frame; avoids per-connection allocs
}

// NewHandler constructs a Handler.
//
//   - svc:            business-logic service (typically *Service)
//   - rec:            metrics recorder (pass deps.Metrics)
//   - allowedOrigins: set of allowed CORS origins from BTC_ALLOWED_ORIGINS
//   - network:        active Bitcoin network label ("testnet4" or "mainnet")
//   - secureCookies:  whether to set the Secure attribute on the SSE cookie
//   - cfg:            feature configuration (used for PingInterval)
func NewHandler(
	svc Servicer,
	rec bitcoinshared.BitcoinRecorder,
	allowedOrigins map[string]struct{},
	network string,
	secureCookies bool,
	cfg EventsConfig,
) *Handler {
	// Pre-compute the static ping frame once at construction time.
	// Format: "event: ping\ndata: {"event":"ping","network":"<net>"}\n\n"
	// The network value is JSON-quoted (%q) so special chars are escaped.
	staticPing := fmt.Sprintf("event: ping\ndata: {\"event\":\"ping\",\"network\":%q}\n\n", network)
	return &Handler{
		svc:             svc,
		rec:             rec,
		allowedOrigins:  allowedOrigins,
		network:         network,
		secureCookies:   secureCookies,
		pingInterval:    pingInterval(cfg),
		staticPingFrame: []byte(staticPing),
	}
}

// ── POST /bitcoin/events/token ────────────────────────────────────────────────

// IssueToken handles POST /bitcoin/events/token.
//
// Guard sequence:
//  1. Auth — token.UserIDFromContext (enforced by JWTAuth middleware)
//  2. Rate limit — enforced by tokenLimiter middleware in routes.go
//     3–7. service.IssueToken
//  8. Set-Cookie: btc_sse_jti; respond 204
func (h *Handler) IssueToken(w http.ResponseWriter, r *http.Request) {
	// Step 1: auth guard — JWTAuth middleware populates context; handler reads it.
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	uid, err := uuid.Parse(userID)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "invalid user ID")
		return
	}

	// SessionID is injected by the auth middleware alongside userID.
	sessionID, _ := token.SessionIDFromContext(r.Context())
	clientIP := respond.ClientIP(r)

	result, err := h.svc.IssueToken(r.Context(), IssueTokenInput{
		VendorID:  [16]byte(uid),
		SessionID: sessionID,
		ClientIP:  clientIP,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrSSERedisUnavailable):
			respond.Error(w, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
		default:
			log.Error(r.Context(), "events.IssueToken: unexpected error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	// Set the SSE cookie — HttpOnly, SameSite=Strict, scoped to the events path.
	http.SetCookie(w, &http.Cookie{
		Name:     sseCookieName,
		Value:    result.SignedJWT,
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteStrictMode,
		Path:     sseCookiePath,
		MaxAge:   result.MaxAge,
	})
	respond.NoContent(w)
}

// ── GET /bitcoin/events ───────────────────────────────────────────────────────

// Events handles GET /bitcoin/events — the SSE stream endpoint.
//
// Guard sequence (events-technical.md §2):
//  1. Rate limit  — enforced by eventsLimiter middleware in routes.go
//  2. Origin check
//     3–7. service.VerifyAndConsumeToken
//  8. service.AcquireSlot
//  9. service.Subscribe
//  10. service.IsZMQRunning
//  11. SSE headers + flush
//  12. Audit EventBitcoinSSEConnected
//  13. Event loop (ping ticker, channel fan-out, ctx.Done)
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	clientIP := respond.ClientIP(r)
	startTime := time.Now()

	// Step 2: origin check — must run before any JWT / Redis I/O to avoid
	// leaking timing or error information to cross-origin requests.
	origin := r.Header.Get("Origin")
	if origin == "" {
		_ = h.svc.WriteAuditLog(r.Context(), audit.EventBitcoinSSETokenConsumeFailure, "",
			map[string]any{"reason": "missing_origin", "source_ip": clientIP})
		respond.Error(w, http.StatusForbidden, "forbidden", "missing Origin header")
		return
	}
	if _, allowed := h.allowedOrigins[origin]; !allowed {
		_ = h.svc.WriteAuditLog(r.Context(), audit.EventBitcoinSSETokenConsumeFailure, "",
			map[string]any{"reason": "bad_origin", "origin": origin, "source_ip": clientIP})
		respond.Error(w, http.StatusForbidden, "forbidden", "origin not permitted")
		return
	}

	// doCleanup — called exactly once via sync.Once regardless of exit path.
	// acquired / subscribed flags prevent double-release when a guard exits
	// before the corresponding resource was obtained.
	var (
		acquired   bool
		subscribed bool
		connCh     <-chan Event
		userID     string
		cleanOnce  sync.Once
	)
	doCleanup := func() {
		cleanOnce.Do(func() {
			if subscribed {
				h.svc.ReleaseSlot(userID, connCh) // handles unsubscribe + counter release
			} else if acquired {
				h.svc.ReleaseSlot(userID, nil) // unsubscribe is no-op; counter is released
			}
			// Audit disconnect — use context.Background() because handler ctx is cancelled.
			auditCtx, auditCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer auditCancel()
			durationMs := time.Since(startTime).Milliseconds()
			if auditErr := h.svc.WriteAuditLog(auditCtx, audit.EventBitcoinSSEDisconnected, userID, map[string]any{
				"user_id":     userID,
				"source_ip":   clientIP,
				"duration_ms": durationMs,
			}); auditErr != nil {
				log.Warn(auditCtx, "events.Events: disconnect audit write failed (non-fatal)", "error", auditErr)
			}
		})
	}
	defer doCleanup()

	// Steps 3–7: parse + verify the SSE cookie JWT, consume JTI.
	cookie, cookieErr := r.Cookie(sseCookieName)
	if cookieErr != nil {
		_ = h.svc.WriteAuditLog(r.Context(), audit.EventBitcoinSSETokenConsumeFailure, "",
			map[string]any{"reason": "missing_cookie", "source_ip": clientIP})
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "SSE token cookie missing")
		return
	}

	verified, err := h.svc.VerifyAndConsumeToken(r.Context(), VerifyTokenInput{
		RawCookie: cookie.Value,
		ClientIP:  clientIP,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrSSETokenInvalid):
			respond.Error(w, http.StatusUnauthorized, "unauthorized", "token invalid or expired")
		case errors.Is(err, ErrSSETokenExpired):
			respond.Error(w, http.StatusUnauthorized, "unauthorized", "token session binding expired — request a new token")
		case errors.Is(err, ErrSSESIDMismatch):
			respond.Error(w, http.StatusUnauthorized, "sid_mismatch", "session binding mismatch")
		case errors.Is(err, ErrSSEIPMismatch):
			respond.Error(w, http.StatusUnauthorized, "ip_mismatch", "IP binding mismatch")
		case errors.Is(err, ErrSSECapExceeded):
			respond.Error(w, http.StatusTooManyRequests, "user_connection_limit", "per-user connection limit reached")
		default:
			respond.Error(w, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
		}
		return
	}
	userID = verified.UserID

	// Step 8: acquire connection slot.
	if err := h.svc.AcquireSlot(r.Context(), userID); err != nil {
		switch {
		case errors.Is(err, ErrSSECapExceeded):
			respond.Error(w, http.StatusTooManyRequests, "user_connection_limit", "per-user connection limit reached")
		default:
			respond.Error(w, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
		}
		return
	}
	acquired = true

	// Step 9: subscribe to the broker.
	ch, err := h.svc.Subscribe(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrSSEProcessCapReached) {
			respond.Error(w, http.StatusServiceUnavailable, "sse_cap_reached", "server SSE capacity reached")
		} else {
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	subscribed = true
	connCh = ch

	// Step 10: ZMQ health gate — checked after subscription so the slot is
	// released by doCleanup if ZMQ is unhealthy.
	if err := h.svc.IsZMQRunning(); err != nil {
		respond.Error(w, http.StatusInternalServerError, "zmq_unhealthy", "Bitcoin ZMQ subscriber not running")
		return
	}

	// Step 11: verify streaming support + set SSE headers.
	// The flusher check must happen before WriteHeader — respond.Error calls
	// are still available here because Content-Type has not been set yet.
	flusher, ok := w.(http.Flusher)
	if !ok {
		respond.Error(w, http.StatusInternalServerError, "internal_error", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Step 12: audit — connection established.
	auditCtx := context.WithoutCancel(r.Context())
	_ = h.svc.WriteAuditLog(auditCtx, audit.EventBitcoinSSEConnected, userID, map[string]any{
		"user_id":   userID,
		"source_ip": clientIP,
	})

	// Step 13: event loop.
	pingTicker := time.NewTicker(h.pingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case e, more := <-connCh:
			if !more {
				return
			}
			if err := writeSSEEvent(w, flusher, e); err != nil {
				return // write error → doCleanup fires via defer
			}

		case <-pingTicker.C:
			if err := writeSSEPing(w, flusher, h.staticPingFrame); err != nil {
				return
			}

		case <-r.Context().Done():
			return
		}
	}
}

// ── GET /bitcoin/events/status ────────────────────────────────────────────────

// Status handles GET /bitcoin/events/status.
//
// Guard sequence (events-technical.md §3):
//  1. Auth   — JWTAuth middleware
//  2. Rate limit — statusLimiter middleware
//  3. svc.Status
//  4. respond 200 JSON
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	// Auth check: JWTAuth middleware populates context.
	if _, ok := token.UserIDFromContext(r.Context()); !ok {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	result := h.svc.Status(r.Context())
	respond.JSON(w, http.StatusOK, result)
}

// ── SSE write helpers ─────────────────────────────────────────────────────────

// writeSSEEvent writes one SSE event frame and flushes.
// Format: "event: <type>\ndata: <payload>\n\n"
//
// e.Type is sanitised: newlines and carriage returns are replaced with spaces
// to prevent SSE frame injection if a new event type ever carries dynamic data.
//
// Returns the first write/flush error, which signals connection loss.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, e Event) error {
	// Sanitize type to prevent SSE frame injection via embedded newlines.
	eventType := strings.NewReplacer("\n", " ", "\r", " ").Replace(e.Type)
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, e.Payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// writeSSEPing writes a pre-computed ping frame and flushes.
// The frame is built once at Handler construction and reused for every ping on
// every connection, eliminating the per-ping allocation from fmt.Sprintf.
func writeSSEPing(w http.ResponseWriter, flusher http.Flusher, frame []byte) error {
	if _, err := w.Write(frame); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
