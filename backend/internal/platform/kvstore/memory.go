package kvstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// ── item ─────────────────────────────────────────────────────────────────────

type item struct {
	value     string
	expiresAt time.Time // zero value → no expiry
}

func (i *item) expired(now time.Time) bool {
	return !i.expiresAt.IsZero() && now.After(i.expiresAt)
}

// ── InMemoryStore ─────────────────────────────────────────────────────────────

// InMemoryStore is a thread-safe, in-process key-value Store with optional
// per-entry TTL and background eviction.
//
// It is not shared across processes and its state is lost on restart.
// Use it for local development, single-instance deployments, and as the
// default backing store for the rate-limit and backoff middleware.
type InMemoryStore struct {
	mu              sync.RWMutex
	items           map[string]*item
	cleanupInterval time.Duration
}

// NewInMemoryStore creates an InMemoryStore with the given cleanup interval.
//
//   - cleanupInterval: how often expired entries are swept from the map.
//     Pass 0 to disable background cleanup (entries are still lazily evicted
//     on the next Get or Exists call that hits them).
//
// Note: the store has no maximum capacity. Workloads that generate an
// unbounded number of distinct keys (e.g. from spoofed source IPs) will grow
// the map without limit. Ensure short TTLs and upstream network-level rate
// limiting are in place for any key space that is not inherently bounded.
func NewInMemoryStore(cleanupInterval time.Duration) *InMemoryStore {
	return &InMemoryStore{
		items:           make(map[string]*item),
		cleanupInterval: cleanupInterval,
	}
}

// Get returns the value stored for key.
// If the entry exists but has expired it is deleted lazily and ErrNotFound
// is returned.
func (s *InMemoryStore) Get(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	it, ok := s.items[key]
	if !ok {
		return "", ErrNotFound
	}
	if it.expired(time.Now()) {
		delete(s.items, key)
		return "", ErrNotFound
	}
	return it.value, nil
}

// Set stores value under key. If ttl > 0 the entry expires after that
// duration. Passing ttl = 0 stores the entry without expiry.
// Passing a negative ttl returns an error.
func (s *InMemoryStore) Set(_ context.Context, key, value string, ttl time.Duration) error {
	if ttl < 0 {
		return fmt.Errorf("kvstore.Set: negative ttl: %v", ttl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	it := &item{value: value}
	if ttl > 0 {
		it.expiresAt = time.Now().Add(ttl)
	}
	s.items[key] = it
	return nil
}

// Delete removes key from the store. It is a no-op if the key does not exist.
func (s *InMemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, key)
	return nil
}

// Exists reports whether key is present and has not expired.
func (s *InMemoryStore) Exists(ctx context.Context, key string) (bool, error) {
	return existsFromGet(ctx, key, s.Get)
}

// existsFromGet is the shared implementation of Exists, extracted so that the
// error-propagation branch (unreachable through InMemoryStore.Get itself) can
// be exercised in isolation by the test suite.
func existsFromGet(ctx context.Context, key string, get func(context.Context, string) (string, error)) (bool, error) {
	_, err := get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Keys returns all non-expired keys whose names start with prefix.
// Passing an empty string returns all non-expired keys.
//
// Expired entries encountered during the scan are skipped but not evicted;
// a subsequent Get or Exists call on the same key will perform the lazy
// eviction.
func (s *InMemoryStore) Keys(_ context.Context, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var keys []string
	for k, it := range s.items {
		if it.expired(now) {
			continue
		}
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

// StartCleanup evicts expired entries on each tick. It blocks until ctx is cancelled.
// Run it in a goroutine:
//
//	go store.StartCleanup(ctx)
//
// If cleanupInterval was set to 0 at construction this method returns immediately.
// Calling StartCleanup more than once for the same store wastes resources; ensure
// it is invoked exactly once.
func (s *InMemoryStore) StartCleanup(ctx context.Context) {
	if s.cleanupInterval <= 0 {
		return
	}
	t := time.NewTicker(s.cleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.evict()
		case <-ctx.Done():
			return
		}
	}
}

// Close is a no-op for the in-memory store; it satisfies the Store interface.
func (s *InMemoryStore) Close() error { return nil }

// ── internal helpers ──────────────────────────────────────────────────────────

// evict removes all expired entries from the map.
// Callers must NOT hold s.mu.
func (s *InMemoryStore) evict() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, it := range s.items {
		if it.expired(now) {
			delete(s.items, k)
		}
	}
}

// ── TokenBlocklist ────────────────────────────────────────────────────────────

// BlockToken inserts jti into the in-memory blocklist for ttl duration.
// Calling BlockToken with ttl ≤ 0 is a no-op.
func (s *InMemoryStore) BlockToken(ctx context.Context, jti string, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	// Security: detach from the request context so a client-timed disconnect
	// cannot abort the blocklist write and leave a revoked token accepted.
	return s.Set(context.WithoutCancel(ctx), blocklistKeyPrefix+jti, "1", ttl)
}

// IsTokenBlocked reports whether jti is currently in the in-memory blocklist.
// Expired entries are lazily evicted on the underlying Exists call.
func (s *InMemoryStore) IsTokenBlocked(ctx context.Context, jti string) (bool, error) {
	return s.Exists(ctx, blocklistKeyPrefix+jti)
}

// jsonMarshal is the json.Marshal implementation used by AtomicBackoffIncrement.
// It is a package-level variable so that the error branch can be exercised in
// tests by temporarily substituting a failing implementation.
var jsonMarshal = json.Marshal

// backoffData mirrors the JSON format used by the ratelimit package's backoffEntry.
// Kept private here to avoid an import cycle (ratelimit → kvstore).
type backoffData struct {
	Failures  int       `json:"failures"`
	UnlocksAt time.Time `json:"unlocks_at"`
	LastSeen  time.Time `json:"last_seen"`
}

// AtomicBackoffIncrement atomically loads the backoff entry for key, increments
// the failure counter, computes the new unlock timestamp using exponential
// backoff, and persists the result — all under s.mu. This makes the entire
// read-modify-write cycle safe for concurrent callers without any additional
// process-local mutex.
func (s *InMemoryStore) AtomicBackoffIncrement(
	_ context.Context,
	key string,
	baseDelay, maxDelay, idleTTL time.Duration,
) (unlocksAt time.Time, failures int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	var e backoffData
	if it, ok := s.items[key]; ok && !it.expired(now) {
		_ = json.Unmarshal([]byte(it.value), &e) // start fresh on decode error
	}

	e.Failures++
	e.LastSeen = now

	exp := math.Pow(2, float64(e.Failures-1))
	delay := time.Duration(float64(baseDelay) * exp)
	delay = min(delay, maxDelay)

	e.UnlocksAt = now.Add(delay)

	data, marshalErr := jsonMarshal(e)
	if marshalErr != nil {
		return time.Time{}, 0, marshalErr
	}

	newItem := &item{value: string(data)}
	if idleTTL > 0 {
		newItem.expiresAt = now.Add(idleTTL)
	}
	s.items[key] = newItem

	return e.UnlocksAt, e.Failures, nil
}

// AtomicBackoffAllow atomically checks whether key may proceed based on the
// current backoff state. The read is performed under s.mu.RLock so it is
// consistent with concurrent AtomicBackoffIncrement calls while allowing
// multiple allow-checks to proceed in parallel.
func (s *InMemoryStore) AtomicBackoffAllow(
	_ context.Context,
	key string,
) (allowed bool, remaining time.Duration, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()

	it, ok := s.items[key]
	if !ok || it.expired(now) {
		return true, 0, nil
	}

	var e backoffData
	if jsonErr := json.Unmarshal([]byte(it.value), &e); jsonErr != nil {
		return true, 0, nil // treat corrupt entry as unlocked
	}

	if e.Failures == 0 {
		return true, 0, nil
	}

	rem := e.UnlocksAt.Sub(now)
	if rem <= 0 {
		return true, 0, nil
	}

	return false, rem, nil
}

// compile-time interface checks.
var _ Store = (*InMemoryStore)(nil)
var _ TokenBlocklist = (*InMemoryStore)(nil)
var _ AtomicBackoffStore = (*InMemoryStore)(nil)
