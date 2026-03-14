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
