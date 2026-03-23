// Package kvstore provides a generic key-value store for rate limiting and token blocklisting.
package kvstore

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by Get and other read operations when the requested
// key does not exist or has already expired.
var ErrNotFound = errors.New("kvstore: key not found")

// blocklistKeyPrefix is the key namespace for JTI blocklist entries shared by
// all Store implementations. Full key format: "blocklist:jti:<jti>".
const blocklistKeyPrefix = "blocklist:jti:"

// Store is a generic, TTL-aware key-value store.
// All implementations must be safe for concurrent use from multiple goroutines.
type Store interface {
	// Get returns the string value stored under key.
	// Returns ErrNotFound if the key does not exist or has expired.
	Get(ctx context.Context, key string) (string, error)

	// Set stores value under key. If ttl > 0 the entry is automatically
	// expired after that duration. Passing ttl = 0 stores the entry without
	// any expiry (it lives until explicitly deleted or the process restarts).
	// Passing a negative ttl returns an error.
	Set(ctx context.Context, key, value string, ttl time.Duration) error

	// Delete removes key from the store.
	// It is a no-op if the key does not exist.
	Delete(ctx context.Context, key string) error

	// Exists reports whether key is present and has not expired.
	Exists(ctx context.Context, key string) (bool, error)

	// Keys returns every key whose name starts with prefix.
	// Passing an empty string returns all non-expired keys.
	// The order of the returned slice is unspecified.
	Keys(ctx context.Context, prefix string) ([]string, error)

	// StartCleanup runs background maintenance (e.g. periodic eviction of
	// expired entries) until ctx is cancelled.
	// Implementations that rely on server-side TTL (e.g. Redis) may treat
	// this as a no-op.
	StartCleanup(ctx context.Context)

	// Close releases any resources held by the store (connections, goroutines,
	// file handles, etc.).
	Close() error

	// RefreshTTL resets the TTL of an existing key without modifying its value.
	// Returns (true, nil)  — key existed, TTL updated.
	// Returns (false, nil) — key does not exist (expired or never created).
	//                        Caller should treat this as expiry and take recovery action.
	// Returns (false, err) — store error; caller should log and retry.
	// Passing ttl ≤ 0 returns an error; a zero TTL would delete the key immediately.
	RefreshTTL(ctx context.Context, key string, ttl time.Duration) (existed bool, err error)
}

// TokenBlocklist provides immediate access-token revocation by maintaining a
// set of blocked JTIs until they expire naturally.
//
// All implementations must be safe for concurrent use from multiple goroutines.
type TokenBlocklist interface {
	// BlockToken adds jti to the blocklist for ttl duration.
	// After ttl elapses the entry is removed automatically (Redis EX /
	// in-memory lazy expiry). Passing ttl ≤ 0 is a no-op: a token that has
	// already expired needs no blocklist entry.
	BlockToken(ctx context.Context, jti string, ttl time.Duration) error

	// IsTokenBlocked reports whether jti is currently in the blocklist.
	// Returns (false, nil) when the jti is absent (never blocked or already
	// expired), (true, nil) when it is present, and (false, err) on transient
	// store errors — callers should fail closed (treat as blocked) on errors.
	IsTokenBlocked(ctx context.Context, jti string) (bool, error)
}

// AtomicBucketStore is an optional extension of Store for backends (e.g. Redis)
// that can execute a token-bucket allow check atomically on the server side.
//
// Implementations must guarantee that the entire read-modify-write cycle is
// atomic with respect to all other callers, making them safe for multi-instance
// deployments where a process-local mutex cannot provide the required isolation.
//
// InMemoryStore intentionally does not implement this interface: it is a
// single-process backend whose process-local mutex is already sufficient.
type AtomicBucketStore interface {
	Store

	// AtomicBucketAllow atomically checks and decrements a token-bucket entry.
	//
	//   key:     bucket identifier (must already include the caller's namespace prefix)
	//   rate:    tokens added per second
	//   burst:   maximum token capacity; also the initial fill for new buckets
	//   idleTTL: hint for how long an idle bucket key should be retained.
	//            Implementations backed by a library that manages TTL automatically
	//            (e.g. go-redis-rate) may ignore this value; see the implementation
	//            doc comment for the actual TTL behaviour.
	//
	// Returns (true, nil)  when a token was successfully consumed.
	// Returns (false, nil) when the bucket was empty (caller should 429).
	// Returns (false, err) on a transient store error; callers should fall back
	// to a less-strict path rather than failing open or closed.
	AtomicBucketAllow(ctx context.Context, key string, rate, burst float64, idleTTL time.Duration) (bool, error)

	// AtomicBucketPeek reports whether a token is currently available WITHOUT
	// consuming one. It is the read-only counterpart of AtomicBucketAllow.
	//
	// Use it to fast-fail before a cheap idempotency check so that
	// duplicate/no-op requests do not drain the bucket. Always follow a
	// successful Peek with a call to AtomicBucketAllow to consume the token.
	//
	// Returns (true, nil)  when at least one token is available.
	// Returns (false, nil) when the bucket is empty (caller should 429).
	// Returns (false, err) on a transient store error.
	AtomicBucketPeek(ctx context.Context, key string, rate, burst float64, idleTTL time.Duration) (bool, error)
}

// AtomicCounterStore is an optional extension of Store for backends that
// support atomic increment, decrement, and acquire operations.
// Both InMemoryStore and RedisStore implement this interface.
// RefreshTTL is inherited from the embedded Store interface.
type AtomicCounterStore interface {
	Store

	// AtomicIncrement increments the counter at key by 1.
	// New key initialised to 1. ttl>0 sets/refreshes the TTL. ttl==0 is permanent.
	// If key exists and ttl>0, existing TTL is refreshed.
	// If key exists and ttl==0, existing TTL is preserved.
	// Returns the new count.
	AtomicIncrement(ctx context.Context, key string, ttl time.Duration) (int64, error)

	// AtomicDecrement decrements the counter at key by 1, flooring at 0.
	// Returns 0 when key does not exist. Never returns a negative value.
	// If ttl>0 AND resulting count>0, the TTL is refreshed. This prevents
	// the safety TTL from expiring while other connections remain open (C-03 fix).
	AtomicDecrement(ctx context.Context, key string, ttl time.Duration) (int64, error)

	// AtomicAcquire increments the counter if current count < max.
	// Returns the new count (≥1) on success.
	// Returns (-1, nil) when already at or above max — caller returns 429/503.
	// Returns (0, err) on store error — caller fails closed.
	AtomicAcquire(ctx context.Context, key string, max int, ttl time.Duration) (int64, error)
}

// PubSubMessage is a message received from a PubSubStore subscription.
type PubSubMessage struct {
	Channel string
	Payload string
}

// ListStore is an optional extension of Store for backends that support
// list operations (LPUSH/BRPOP/LLEN). Used by the bitcoin settlement overflow queue.
type ListStore interface {
	Store

	// LPush prepends one or more values to the list at key.
	// Creates the list if it does not exist.
	// Returns the new list length after all values are pushed.
	LPush(ctx context.Context, key string, values ...string) (int64, error)

	// BRPop pops the rightmost element from the first non-empty list.
	// Blocks up to timeout if all lists are empty.
	// Returns ("", "", ErrNotFound) on timeout (both Redis and InMemoryStore).
	// InMemoryStore does NOT block — returns ErrNotFound immediately when empty.
	// Use a polling loop with a ticker when using InMemoryStore.
	BRPop(ctx context.Context, timeout time.Duration, keys ...string) (key, value string, err error)

	// LLen returns the current list length. Returns 0 when key does not exist.
	LLen(ctx context.Context, key string) (int64, error)
}

// PubSubStore is an optional extension of Store for backends that support
// publish/subscribe messaging. Used for cross-instance cache invalidation.
type PubSubStore interface {
	Store

	// Publish sends payload to all subscribers of channel.
	// Returns subscriber count (0 is not an error).
	Publish(ctx context.Context, channel, payload string) (int64, error)

	// Subscribe returns a channel receiving messages and a cancel func.
	// The message channel is closed when cancel() is called or ctx is cancelled.
	// Callers MUST always call the returned cancel func.
	//
	// RedisStore: reconnects with exponential backoff (1s initial, 60s ceiling).
	// Messages published during a reconnect window are permanently lost.
	// Callers must NOT assume guaranteed delivery — use periodic full reloads
	// as the authoritative recovery path.
	//
	// InMemoryStore: single-process only; no reconnection. Messages sent before
	// Subscribe is called are lost. Not suitable for multi-instance deployments.
	Subscribe(ctx context.Context, channels ...string) (<-chan PubSubMessage, func())
}

// SetStore is an optional extension of Store for backends that support
// Redis-style set operations. Used by the bitcoin domain for watch address sets.
type SetStore interface {
	Store

	// SAdd adds one or more members to the set at key.
	// Creates the set if it does not exist.
	// Returns the number of members actually added (excluding already-present ones).
	SAdd(ctx context.Context, key string, members ...string) (int64, error)

	// SRem removes one or more members from the set at key.
	// Missing members are ignored.
	// Returns the number of members actually removed.
	SRem(ctx context.Context, key string, members ...string) (int64, error)

	// SCard returns the number of members in the set at key.
	// Returns 0 when key does not exist.
	SCard(ctx context.Context, key string) (int64, error)

	// SScan incrementally iterates over members of the set at key.
	// Pass cursor=0 to start a new iteration; iteration is complete when the
	// returned cursor is 0. match filters members by glob pattern ("" = all).
	// count is a hint for how many elements to return per call.
	//
	// InMemoryStore returns all matching members in one shot (cursor always 0).
	SScan(ctx context.Context, key string, cursor uint64, match string, count int64) (members []string, nextCursor uint64, err error)
}

// OnceStore is an optional extension of Store for atomic one-time-use keys.
// Used by the bitcoin domain for SSE JTI one-time token consumption.
type OnceStore interface {
	Store

	// ConsumeOnce atomically creates key with the given TTL only if it does not
	// already exist (Redis SET NX PX).
	// Returns true when the key was newly created — the caller owns the slot.
	// Returns false when the key already existed — the token was already consumed.
	// ttl must be positive; a zero or negative TTL returns an error.
	ConsumeOnce(ctx context.Context, key string, ttl time.Duration) (consumed bool, err error)
}

// AtomicBackoffStore is an optional extension of Store for backends that can
// execute backoff increment and allow operations atomically.
//
// Implementations must guarantee that the entire read-modify-write cycle is
// atomic with respect to all other callers. Both InMemoryStore (using its
// internal mutex) and Redis-backed stores implement this interface, making
// them safe for concurrent use without an additional process-local mutex.
type AtomicBackoffStore interface {
	Store

	// AtomicBackoffIncrement atomically increments the failure counter and computes
	// the new unlock timestamp using exponential backoff with a cap.
	//
	//   key:       backoff entry identifier (must already include the caller's namespace prefix)
	//   baseDelay: delay after the first failure (e.g. 2s)
	//   maxDelay:  ceiling for the exponential growth (e.g. 5min)
	//   idleTTL:   duration after which an idle key is expired by the store
	//
	// Returns the new unlock timestamp, the updated failure count, and any error.
	AtomicBackoffIncrement(ctx context.Context, key string, baseDelay, maxDelay, idleTTL time.Duration) (unlocksAt time.Time, failures int, err error)

	// AtomicBackoffAllow atomically checks if the key is allowed to proceed based
	// on the current backoff state.
	//
	//   key: backoff entry identifier (must already include the caller's namespace prefix)
	//
	// Returns (true, 0, nil) if the key may proceed (no failures or unlocked).
	// Returns (false, remaining, nil) if still within a backoff window.
	// Returns (false, 0, err) on a transient store error.
	AtomicBackoffAllow(ctx context.Context, key string) (allowed bool, remaining time.Duration, err error)
}

// WatchCapStore executes atomic watch-address cap operations for the bitcoin
// display-watch system. Only RedisStore implements this interface; it is not
// implemented by InMemoryStore because the bitcoin watch feature requires Redis
// for cross-instance coordination and TTL semantics.
//
// Use deps.KVStore.(watch.KVClient) in bitcoin/watch/routes.go.
// The type assertion panics at startup when Bitcoin is enabled without Redis.
type WatchCapStore interface {
	Store

	// RunWatchCapScript atomically enforces the per-user address cap and
	// 7-day registration window using a single server-side Lua round-trip.
	//
	// All three key arguments must share the same Redis Cluster hash tag
	// (e.g. {btc:user:{userID}}) so they land on the same cluster slot.
	//
	// addresses must already be normalised (trimmed + lowercased) by the
	// caller before this method is invoked.
	//
	// Returns (success, newCount, addedCount):
	//   success == 1:  completed; addedCount may be 0 (all addresses pre-existing)
	//   success == 0:  per-user count cap exceeded; watch set is unchanged
	//   success == -1: 7-day absolute registration window has expired
	RunWatchCapScript(
		ctx context.Context,
		setKey, regAtKey, lastActiveKey string,
		limit int,
		watchTTL, lastActiveTTL time.Duration,
		addresses []string,
	) (success, newCount, addedCount int64, err error)

	// ScanWatchAddressKeys iterates over all watch-address SET keys in the
	// keyspace matching the pattern "*:addresses" of type "set". Used by the
	// reconciliation goroutine to recompute the global watch count from ground truth.
	//
	// Pass cursor=0 to start; iteration is complete when nextCursor returns 0.
	// count is a hint for the number of keys per page (not a guarantee).
	//
	// Warning: in a Redis Cluster deployment SCAN operates on a single shard.
	// The returned total will undercount unless a ClusterScan approach is used.
	ScanWatchAddressKeys(ctx context.Context, cursor uint64, count int64) (keys []string, nextCursor uint64, err error)
}
