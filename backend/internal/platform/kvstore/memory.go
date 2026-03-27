package kvstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
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
	sets            map[string]map[string]struct{} // protected by mu
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
		sets:            make(map[string]map[string]struct{}),
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

// RefreshTTL resets the TTL of an existing, non-expired key without modifying its value.
// Returns (true, nil) when the key exists and the TTL was updated.
// Returns (false, nil) when the key is absent or expired (caller should treat as expiry).
// Returns (false, error) when ttl ≤ 0.
func (s *InMemoryStore) RefreshTTL(_ context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, fmt.Errorf("kvstore.RefreshTTL: ttl must be positive, got %v", ttl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[key]
	if !ok || it.expired(time.Now()) {
		delete(s.items, key)
		return false, nil
	}
	it.expiresAt = time.Now().Add(ttl)
	return true, nil
}

// ── AtomicCounterStore ──────────────────────────────────────────────────────────────────────

// AtomicIncrement atomically increments the counter at key by 1 under s.mu.
// If ttl > 0 the key's TTL is set (new key) or refreshed (existing key).
// If ttl == 0 the key is permanent; existing TTL is preserved on subsequent calls.
func (s *InMemoryStore) AtomicIncrement(_ context.Context, key string, ttl time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	it, ok := s.items[key]
	var count int64
	if !ok || it.expired(now) {
		it = &item{value: "1"}
		count = 1
	} else {
		n, _ := strconv.ParseInt(it.value, 10, 64)
		count = n + 1
		it.value = strconv.FormatInt(count, 10)
	}
	if ttl > 0 {
		it.expiresAt = now.Add(ttl)
	}
	// ttl == 0: zero expiresAt means permanent — no change needed.
	s.items[key] = it
	return count, nil
}

// AtomicDecrement atomically decrements the counter at key by 1, flooring at 0.
// If ttl > 0 and the resulting count > 0, the TTL is refreshed (C-03 fix).
// Returns 0 when the key does not exist.
func (s *InMemoryStore) AtomicDecrement(_ context.Context, key string, ttl time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	it, ok := s.items[key]
	if !ok || it.expired(now) {
		delete(s.items, key)
		return 0, nil
	}
	n, _ := strconv.ParseInt(it.value, 10, 64)
	if n <= 0 {
		// Corruption repair: negative value means no connections are held.
		// Delete the key entirely so callers see a clean absent state (consistent
		// with the n==0 path below, and so Get returns ErrNotFound).
		delete(s.items, key)
		return 0, nil
	}
	n--
	it.value = strconv.FormatInt(n, 10)
	if ttl > 0 && n > 0 {
		it.expiresAt = now.Add(ttl)
	}
	if n == 0 {
		delete(s.items, key)
	}
	return n, nil
}

// AtomicAcquire atomically increments the counter if current count < max.
// Returns the new count on success, -1 when at cap, 0 on error.
func (s *InMemoryStore) AtomicAcquire(_ context.Context, key string, max int, ttl time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	it, ok := s.items[key]
	var current int64
	if ok && !it.expired(now) {
		current, _ = strconv.ParseInt(it.value, 10, 64)
		if current < 0 {
			current = 0 // corruption repair
		}
	} else {
		it = &item{}
		current = 0
	}
	if current >= int64(max) {
		return -1, nil
	}
	current++
	it.value = strconv.FormatInt(current, 10)
	if ttl > 0 {
		it.expiresAt = now.Add(ttl)
	}
	s.items[key] = it
	return current, nil
}

// ── ListStore ─────────────────────────────────────────────────────────────────────────────

// lists stores list state separately from the string KV items map.
// Protected by s.mu.
type listEntry struct {
	elements []string
}

func (s *InMemoryStore) listKey(key string) string { return "\x00list:\x00" + key }

// LPush prepends one or more values to the list at key.
func (s *InMemoryStore) LPush(_ context.Context, key string, values ...string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.listKey(key)
	var elems []string
	if it, ok := s.items[k]; ok {
		_ = json.Unmarshal([]byte(it.value), &elems)
	}
	// LPUSH prepends in order — each value goes to the front.
	new := make([]string, 0, len(values)+len(elems))
	for i := len(values) - 1; i >= 0; i-- {
		new = append(new, values[i])
	}
	new = append(new, elems...)
	data, _ := json.Marshal(new)
	s.items[k] = &item{value: string(data)}
	return int64(len(new)), nil
}

// BRPop pops the rightmost element. InMemoryStore does NOT block — returns
// ErrNotFound immediately if the list is empty. Use a polling loop.
func (s *InMemoryStore) BRPop(_ context.Context, _ time.Duration, keys ...string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		k := s.listKey(key)
		it, ok := s.items[k]
		if !ok {
			continue
		}
		var elems []string
		_ = json.Unmarshal([]byte(it.value), &elems)
		if len(elems) == 0 {
			continue
		}
		val := elems[len(elems)-1]
		elems = elems[:len(elems)-1]
		if len(elems) == 0 {
			delete(s.items, k)
		} else {
			data, _ := json.Marshal(elems)
			s.items[k] = &item{value: string(data)}
		}
		return key, val, nil
	}
	return "", "", ErrNotFound
}

// LLen returns the current list length.
func (s *InMemoryStore) LLen(_ context.Context, key string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k := s.listKey(key)
	it, ok := s.items[k]
	if !ok {
		return 0, nil
	}
	var elems []string
	_ = json.Unmarshal([]byte(it.value), &elems)
	return int64(len(elems)), nil
}

// ── PubSubStore ─────────────────────────────────────────────────────────────────────────

// pubsubMu guards pubsubSubs. It is separate from s.mu to avoid
// holding the main store lock during channel sends.
var pubsubMu sync.RWMutex
var pubsubSubs = make(map[*InMemoryStore]map[string][]chan PubSubMessage)

// Publish sends payload to all in-process subscribers of channel.
func (s *InMemoryStore) Publish(_ context.Context, channel, payload string) (int64, error) {
	pubsubMu.RLock()
	chans, ok := pubsubSubs[s][channel]
	pubsubMu.RUnlock()
	if !ok {
		return 0, nil
	}
	var count int64
	for _, ch := range chans {
		if trySend(ch, PubSubMessage{Channel: channel, Payload: payload}) {
			count++
		}
	}
	return count, nil
}

// trySend attempts a non-blocking send to ch.
// Returns false if the channel is full or closed. The recover call handles
// the closed-channel case: cancel() may close ch between Publish reading the
// subscriber list and the actual send, so a plain select would panic.
func trySend(ch chan PubSubMessage, msg PubSubMessage) (sent bool) {
	defer func() {
		if recover() != nil {
			sent = false
		}
	}()
	select {
	case ch <- msg:
		return true
	default:
		return false
	}
}

// Subscribe returns a buffered channel of PubSubMessages and a cancel func.
// The channel is closed when cancel() is called or ctx is cancelled.
// Callers MUST always call the returned cancel func.
func (s *InMemoryStore) Subscribe(ctx context.Context, channels ...string) (<-chan PubSubMessage, func()) {
	ch := make(chan PubSubMessage, 64)
	pubsubMu.Lock()
	if pubsubSubs[s] == nil {
		pubsubSubs[s] = make(map[string][]chan PubSubMessage)
	}
	for _, c := range channels {
		pubsubSubs[s][c] = append(pubsubSubs[s][c], ch)
	}
	pubsubMu.Unlock()

	// once guards close(ch) so that calling cancel() while the ctx-watcher
	// goroutine also fires cancel() cannot produce a double-close panic.
	var once sync.Once
	closeCh := func() { once.Do(func() { close(ch) }) }

	cancel := func() {
		pubsubMu.Lock()
		for _, c := range channels {
			list := pubsubSubs[s][c]
			newList := list[:0]
			for _, existing := range list {
				if existing != ch {
					newList = append(newList, existing)
				}
			}
			if len(newList) == 0 {
				delete(pubsubSubs[s], c)
			} else {
				pubsubSubs[s][c] = newList
			}
		}
		pubsubMu.Unlock()
		closeCh()
	}

	go func() {
		<-ctx.Done()
		cancel()
	}()

	return ch, cancel
}

// ── SetStore ───────────────────────────────────────────────────────────────────────────────

// SAdd adds members to the set at key. Returns the count of newly added members.
func (s *InMemoryStore) SAdd(_ context.Context, key string, members ...string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sets[key] == nil {
		s.sets[key] = make(map[string]struct{})
	}
	var added int64
	for _, m := range members {
		if _, exists := s.sets[key][m]; !exists {
			s.sets[key][m] = struct{}{}
			added++
		}
	}
	return added, nil
}

// SRem removes members from the set at key. Returns the count of removed members.
func (s *InMemoryStore) SRem(_ context.Context, key string, members ...string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.sets[key]
	if set == nil {
		return 0, nil
	}
	var removed int64
	for _, m := range members {
		if _, exists := set[m]; exists {
			delete(set, m)
			removed++
		}
	}
	if len(set) == 0 {
		delete(s.sets, key)
	}
	return removed, nil
}

// SCard returns the number of members in the set at key.
func (s *InMemoryStore) SCard(_ context.Context, key string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int64(len(s.sets[key])), nil
}

// SScan returns all members matching the glob pattern in one shot.
// InMemoryStore does not do cursor-based iteration; nextCursor is always 0.
func (s *InMemoryStore) SScan(_ context.Context, key string, _ uint64, match string, _ int64) ([]string, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := s.sets[key]
	var out []string
	for m := range set {
		if match == "" || match == "*" || m == match {
			out = append(out, m)
		}
	}
	return out, 0, nil
}

// ── OnceStore ───────────────────────────────────────────────────────────────────────────

// ConsumeOnce atomically creates key with ttl only if it does not already
// exist. Returns true when the key was newly created (caller owns the slot),
// false when it already existed (already consumed).
func (s *InMemoryStore) ConsumeOnce(_ context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, fmt.Errorf("kvstore.ConsumeOnce: ttl must be positive, got %v", ttl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if it, ok := s.items[key]; ok && !it.expired(now) {
		return false, nil // already exists
	}
	s.items[key] = &item{value: "1", expiresAt: now.Add(ttl)}
	return true, nil
}

// ── WatchCapStore ─────────────────────────────────────────────────────────────
//
// InMemoryStore implements WatchCapStore for local development and e2e testing.
// The 7-day registration window is enforced via s.items timestamp keys.
// TTL on the address set itself is not enforced — sets in InMemoryStore have
// no expiry mechanism — which is acceptable for non-production use.

// RunWatchCapScript mirrors the semantics of watchCapLuaScript (scripts.go)
// under s.mu for single-process atomicity.
//
// Returns (success, newCount, addedCount):
//
//	success ==  1: completed; addedCount may be 0 (all addresses pre-existing)
//	success ==  0: per-user count cap exceeded; watch set is unchanged
//	success == -1: 7-day absolute registration window has expired
func (s *InMemoryStore) RunWatchCapScript(
	_ context.Context,
	setKey, regAtKey, lastActiveKey string,
	limit int,
	_ time.Duration, // watchTTL — not enforced on in-memory sets
	lastActiveTTL time.Duration,
	addresses []string,
) (success, newCount, addedCount int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	nowUnix := now.Unix()

	// 7-day absolute cap check — mirrors the Lua TIME + EXPIRE logic.
	if it, ok := s.items[regAtKey]; ok && !it.expired(now) {
		regAtSec, _ := strconv.ParseInt(it.value, 10, 64)
		if nowUnix-regAtSec > 604800 {
			// M-02/OD-07: schedule cleanup so stale keys do not accumulate.
			// 30-day TTL from first registration; minimum 1-day grace period.
			elapsed := nowUnix - regAtSec
			cleanupIn := int64(2592000) - elapsed
			if cleanupIn < 86400 {
				cleanupIn = 86400
			}
			it.expiresAt = now.Add(time.Duration(cleanupIn) * time.Second)
			return -1, 0, 0, nil
		}
	}

	current := int64(len(s.sets[setKey]))

	// Speculatively add addresses.
	if s.sets[setKey] == nil {
		s.sets[setKey] = make(map[string]struct{})
	}
	var added []string
	for _, addr := range addresses {
		if _, exists := s.sets[setKey][addr]; !exists {
			s.sets[setKey][addr] = struct{}{}
			added = append(added, addr)
		}
	}

	// Roll back if cap would be exceeded.
	if current+int64(len(added)) > int64(limit) {
		for _, addr := range added {
			delete(s.sets[setKey], addr)
		}
		return 0, current, 0, nil
	}

	if len(added) > 0 {
		// NX: set registered_at only on first registration.
		// Guard also covers lazy-expired entries still in the map so that a
		// regAtKey whose cleanup TTL has elapsed (set by the 7-day path above)
		// is reset correctly on the next registration cycle, matching Redis
		// SET … NX semantics (expired keys are treated as non-existent).
		if existing, ok := s.items[regAtKey]; !ok || existing.expired(now) {
			s.items[regAtKey] = &item{value: strconv.FormatInt(nowUnix, 10)}
		}
	}

	// Always refresh last_active (even on re-registration).
	s.items[lastActiveKey] = &item{
		value:     strconv.FormatInt(nowUnix, 10),
		expiresAt: now.Add(lastActiveTTL),
	}

	return 1, current + int64(len(added)), int64(len(added)), nil
}

// ScanWatchAddressKeys returns all set keys whose name ends with ":addresses".
// InMemoryStore returns all matches in one call; nextCursor is always 0.
func (s *InMemoryStore) ScanWatchAddressKeys(_ context.Context, _ uint64, _ int64) (keys []string, nextCursor uint64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for k := range s.sets {
		if strings.HasSuffix(k, ":addresses") {
			keys = append(keys, k)
		}
	}
	return keys, 0, nil
}

// compile-time interface checks.
var _ Store = (*InMemoryStore)(nil)
var _ TokenBlocklist = (*InMemoryStore)(nil)
var _ AtomicBackoffStore = (*InMemoryStore)(nil)
var _ AtomicCounterStore = (*InMemoryStore)(nil)
var _ ListStore = (*InMemoryStore)(nil)
var _ PubSubStore = (*InMemoryStore)(nil)
var _ SetStore = (*InMemoryStore)(nil)
var _ OnceStore = (*InMemoryStore)(nil)
var _ WatchCapStore = (*InMemoryStore)(nil)
