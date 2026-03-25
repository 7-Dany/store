package events

import "time"

// ── Service I/O types ─────────────────────────────────────────────────────────

// IssueTokenInput carries the parameters for IssueToken (POST /events/token).
type IssueTokenInput struct {
	// VendorID is the authenticated vendor's UUID bytes — used for RecordTokenIssuance.
	VendorID [16]byte
	// SessionID is the server-side session identifier from the auth JWT (sid claim).
	SessionID string
	// ClientIP is the trusted real client IP from respond.ClientIP(r).
	ClientIP string
}

// IssueTokenResult is the success result of IssueToken.
type IssueTokenResult struct {
	// SignedJWT is the one-time HS256 SSE token. Write to a Secure HttpOnly cookie.
	SignedJWT string
	// MaxAge is the cookie Max-Age in seconds, equal to cfg.TokenTTL.Seconds().
	MaxAge int
}

// VerifyTokenInput carries the parameters for VerifyAndConsumeToken (GET /events).
type VerifyTokenInput struct {
	// RawCookie is the raw SSE JWT value from the request cookie.
	RawCookie string
	// ClientIP is the trusted real client IP from respond.ClientIP(r).
	ClientIP string
}

// VerifiedTokenResult is the success result of VerifyAndConsumeToken.
type VerifiedTokenResult struct {
	// UserID is the authenticated vendor's UUID string (sub claim from JWT).
	UserID string
	// JTI is the consumed one-time JTI (used for audit and cleanup logging only).
	JTI string
}

// StatusResult is the result of Status (GET /bitcoin/events/status).
type StatusResult struct {
	// ZMQConnected reports whether the ZMQ subscriber is connected.
	ZMQConnected bool `json:"zmq_connected"`
	// RPCConnected reports whether the Bitcoin RPC client is reachable.
	RPCConnected bool `json:"rpc_connected"`
	// ActiveConnections is the number of SSE connections active on this instance.
	ActiveConnections int `json:"active_connections"`
	// LastBlockHashAge is the seconds elapsed since the last ZMQ hashblock message.
	// Zero when no block has been seen since startup.
	LastBlockHashAge float64 `json:"last_block_hash_age"`
}

// ── Domain event ─────────────────────────────────────────────────────────────

// Event is a domain event emitted by the SSE broker to connected clients.
// The Type field identifies the event class; Payload contains the JSON body.
type Event struct {
	// Type is the SSE event type string sent as "event: <Type>\n".
	Type string
	// Payload is the JSON-encoded data sent as "data: <Payload>\n\n".
	Payload []byte
}

// ── Configuration ─────────────────────────────────────────────────────────────

// EventsConfig holds all feature-level config for the events package.
// Populated from environment variables in the bitcoin domain assembler.
type EventsConfig struct {
	// TokenTTL is the lifetime of the one-time SSE JWT (BTC_SSE_TOKEN_TTL).
	TokenTTL time.Duration
	// SigningSecret is the HS256 key for SSE JWTs (BTC_SSE_SIGNING_SECRET, ≥ 32 bytes).
	SigningSecret string
	// SessionSecret is the HMAC key for the sid binding (BTC_SESSION_SECRET, ≥ 32 bytes).
	SessionSecret string
	// ServerSecret is the HMAC key for jti_hash DB records (BTC_SERVER_SECRET, ≥ 32 bytes).
	ServerSecret string
	// DailyRotationKey is the daily-rotating key for source_ip_hash (BTC_DAILY_ROTATION_KEY).
	DailyRotationKey string
	// Network is the active Bitcoin network label ("testnet4" or "mainnet").
	Network string
	// BindIP controls whether an IPv4 /24 subnet claim is embedded in tokens.
	// Sourced from BTC_SSE_TOKEN_BIND_IP.
	BindIP bool
	// MaxSSEPerUser is the per-user concurrent connection ceiling (BTC_MAX_SSE_PER_USER).
	MaxSSEPerUser int
	// MaxSSEProcess is the per-process broker connection ceiling (BTC_MAX_SSE_PROCESS).
	MaxSSEProcess int
	// BlockRPCTimeout is the per-call timeout for the liveness RPC probe.
	BlockRPCTimeout time.Duration
	// PendingMempoolMaxSize caps the in-process pendingMempool map
	// (BTC_PENDING_MEMPOOL_MAX_SIZE). Default 10 000.
	PendingMempoolMaxSize int
	// MempoolPendingMaxAgeDays is max age of pendingMempool entries in days
	// before hourly pruning removes them (BTC_MEMPOOL_PENDING_MAX_AGE_DAYS). Default 14.
	MempoolPendingMaxAgeDays int
	// PingInterval is how often a keep-alive ping event is sent to each SSE client.
	// Defaults to 30s when zero. Sourced from BTC_SSE_PING_INTERVAL.
	// Setting a small value (e.g. 1ms) in tests allows ping-timer tests without waiting.
	PingInterval time.Duration

	// WatchAddrCacheTTL is the TTL for the in-process per-user watch-address cache
	// used by MempoolTracker to reduce Redis SScan calls on the ZMQ hot path.
	// Defaults to 5s when zero. Set to a negative value to disable caching.
	// Sourced from BTC_WATCH_ADDR_CACHE_TTL.
	WatchAddrCacheTTL time.Duration
}
