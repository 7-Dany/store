package bitcoinshared

import "errors"

// ErrWatchLimitExceeded is returned by Watch when the Lua script indicates the
// per-user address count cap has been reached (Lua result[0] == 0).
var ErrWatchLimitExceeded = errors.New("watch address limit exceeded")

// ErrWatchRegistrationExpired is returned by Watch when the 7-day absolute
// registration window has lapsed (Lua result[0] == -1).
var ErrWatchRegistrationExpired = errors.New("watch registration window expired")

// ErrRedisUnavailable is returned by Watch when the Redis Lua eval or any
// follow-up Redis operation fails. Maps to 503 Service Unavailable.
var ErrRedisUnavailable = errors.New("redis unavailable")

// ErrInvalidAddress is returned by validateAndNormalise when a single
// submitted address fails format or network check.
var ErrInvalidAddress = errors.New("invalid bitcoin address")
