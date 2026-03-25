package events

import "errors"

// Sentinel errors returned by the events service. Handlers map these to HTTP
// status codes — see handler.go for the mapping.

// ErrSSERedisUnavailable is returned when a required Redis operation fails.
// Maps to 503 Service Unavailable — fail closed.
var ErrSSERedisUnavailable = errors.New("sse: redis unavailable")

// ErrSSETokenExpired is returned when the server-side session binding (SID key)
// for the SSE token has expired in Redis (kvstore.ErrNotFound). Distinct from
// ErrSSERedisUnavailable (Redis is down). Maps to 401 Unauthorized — the client
// must request a new token via POST /events/token.
var ErrSSETokenExpired = errors.New("sse: token session binding expired")

// ErrSSETokenInvalid is returned when the SSE cookie JWT is missing, malformed,
// expired, or has already been consumed (JTI replay).
// Maps to 401 Unauthorized.
var ErrSSETokenInvalid = errors.New("sse: token invalid or expired")

// ErrSSESIDMismatch is returned when the sid HMAC in the token does not match
// the server-side session ID stored in Redis.
// Maps to 401 Unauthorized. Triggers a security audit event.
var ErrSSESIDMismatch = errors.New("sse: session ID mismatch")

// ErrSSEIPMismatch is returned when the client's IPv4 /24 subnet does not match
// the ip claim embedded in the token.
// Maps to 401 Unauthorized.
var ErrSSEIPMismatch = errors.New("sse: IP binding mismatch")

// ErrSSECapExceeded is returned when the per-user concurrent connection ceiling
// (BTC_MAX_SSE_PER_USER) is reached.
// Maps to 429 Too Many Requests.
var ErrSSECapExceeded = errors.New("sse: per-user connection cap exceeded")

// ErrSSEProcessCapReached is returned when the per-process broker capacity
// (BTC_MAX_SSE_PROCESS) is reached.
// Maps to 503 Service Unavailable.
var ErrSSEProcessCapReached = errors.New("sse: process-wide connection cap reached")

// ErrSSEZMQUnhealthy is returned when the ZMQ subscriber is not running at the
// moment a GET /events request arrives (step 10 of the guard sequence).
// Maps to 500 Internal Server Error — the Bitcoin node connection is broken.
var ErrSSEZMQUnhealthy = errors.New("sse: ZMQ subscriber not running")
