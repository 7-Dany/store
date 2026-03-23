package kvstore

// redis.go contains the RedisStore struct, its constructor, and all interface
// method implementations. Lua script constants live in scripts.go.
//
// # Mandatory ordering convention (enforced by §3.16 of RULES.md)
//
// Three locations in this file must stay in sync with scripts.go:
//
//  1. RedisStore SHA struct fields  (order matches scripts.go load sequence)
//  2. ScriptLoad calls in NewRedisStore  (same order, numbered comments)
//  3. Implementation method sections  (interface declaration order from store.go)
//  4. Compile-time interface checks  (same as method section order)
//
// See §3.16 of RULES.md for the step-by-step procedure for adding a new
// Lua-backed interface or a new method to an existing interface.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	redis_rate "github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"

	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

// ── RedisStore ────────────────────────────────────────────────────────────────

// RedisStore is a Store backed by a Redis instance.
//
// Lua-backed operations are executed atomically on the Redis server, making
// RedisStore safe for multi-instance deployments where a process-local mutex
// cannot provide the required isolation.
//
// SHA fields are pre-loaded at construction time by NewRedisStore. They follow
// the same order as the ScriptLoad calls so diffs between the two are trivially
// auditable. A NOSCRIPT error on any EvalSha call triggers an automatic reload
// and Eval fallback — see evalScript.
type RedisStore struct {
	client  *redis.Client
	limiter *redis_rate.Limiter

	// AtomicBackoffStore Lua script SHAs (load order 1–2).
	incrSHA  string // SHA of atomicBackoffIncrementScript
	allowSHA string // SHA of atomicBackoffAllowScript

	// AtomicCounterStore Lua script SHAs (load order 3–5).
	counterIncrSHA string // SHA of atomicCounterIncrementScript
	counterDecrSHA string // SHA of atomicCounterDecrementScript
	counterAcqSHA  string // SHA of atomicCounterAcquireScript

	// WatchCapStore Lua script SHA (load order 6).
	watchCapSHA string // SHA of watchCapLuaScript
}

// NewRedisStore dials a Redis server at the given URL, verifies connectivity,
// pre-loads all Lua scripts into the Redis script cache, and returns a ready
// RedisStore. Returns an error if the URL is malformed, the ping fails, or any
// script cannot be loaded.
func NewRedisStore(url string) (*RedisStore, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, telemetry.KVStore("NewRedisStore.parse_url", err)
	}

	// Override pool defaults to ensure fast failure detection.
	//
	// DialTimeout (2 s): default 5 s means the first request after Redis goes
	// down blocks for 5 s before surfacing an error.
	//
	// ReadTimeout / WriteTimeout (2 s): lowered from 3 s so every failed
	// command surfaces within the same 2 s window as a dial failure.
	//
	// ConnMaxIdleTime (30 s): without this, idle connections are never
	// health-checked. With 30 s idle + 15 s InfraPoller cycle a stale
	// connection is discovered within ~45 s worst-case instead of 3+ minutes.
	if opts.DialTimeout == 0 {
		opts.DialTimeout = 2 * time.Second
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = 2 * time.Second
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = 2 * time.Second
	}
	if opts.ConnMaxIdleTime == 0 {
		opts.ConnMaxIdleTime = 30 * time.Second
	}

	client := redis.NewClient(opts)

	// Verify connectivity with a dedicated 5-second deadline so a slow network
	// does not block indefinitely.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.connect", err)
	}

	// Pre-load all Lua scripts. Scripts are loaded in the same order as the
	// SHA struct fields (load order 1–6). Each load uses its own 5-second
	// deadline so a slow Ping cannot consume the entire budget.
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer loadCancel()

	// Load order 1: atomicBackoffIncrementScript
	incrSHA, err := client.ScriptLoad(loadCtx, atomicBackoffIncrementScript).Result()
	if err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.load_increment_script", err)
	}

	// Load order 2: atomicBackoffAllowScript
	allowSHA, err := client.ScriptLoad(loadCtx, atomicBackoffAllowScript).Result()
	if err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.load_allow_script", err)
	}

	// Load order 3: atomicCounterIncrementScript
	counterIncrSHA, err := client.ScriptLoad(loadCtx, atomicCounterIncrementScript).Result()
	if err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.load_counter_incr_script", err)
	}

	// Load order 4: atomicCounterDecrementScript
	counterDecrSHA, err := client.ScriptLoad(loadCtx, atomicCounterDecrementScript).Result()
	if err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.load_counter_decr_script", err)
	}

	// Load order 5: atomicCounterAcquireScript
	counterAcqSHA, err := client.ScriptLoad(loadCtx, atomicCounterAcquireScript).Result()
	if err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.load_counter_acq_script", err)
	}

	// Load order 6: watchCapLuaScript
	watchCapSHA, err := client.ScriptLoad(loadCtx, watchCapLuaScript).Result()
	if err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.load_watch_cap_script", err)
	}

	return &RedisStore{
		client:         client,
		limiter:        redis_rate.NewLimiter(client),
		incrSHA:        incrSHA,
		allowSHA:       allowSHA,
		counterIncrSHA: counterIncrSHA,
		counterDecrSHA: counterDecrSHA,
		counterAcqSHA:  counterAcqSHA,
		watchCapSHA:    watchCapSHA,
	}, nil
}

// ── Store ─────────────────────────────────────────────────────────────────────

// Get returns the value stored under key.
// Returns ErrNotFound if the key does not exist or has expired.
func (s *RedisStore) Get(ctx context.Context, key string) (string, error) {
	val, err := s.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", telemetry.KVStore("Get.redis_get", err)
	}
	return val, nil
}

// Set stores value under key with the given TTL.
// Passing ttl = 0 stores the entry without expiry.
// Passing a negative ttl returns an error.
func (s *RedisStore) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if ttl < 0 {
		return fmt.Errorf("kvstore.Set: negative ttl: %v", ttl)
	}
	if err := s.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return telemetry.KVStore("Set.redis_set", err)
	}
	return nil
}

// Delete removes key from the store. It is a no-op if the key does not exist.
func (s *RedisStore) Delete(ctx context.Context, key string) error {
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return telemetry.KVStore("Delete.redis_del", err)
	}
	return nil
}

// Exists reports whether key is present and has not expired.
func (s *RedisStore) Exists(ctx context.Context, key string) (bool, error) {
	n, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return false, telemetry.KVStore("Exists.redis_exists", err)
	}
	return n > 0, nil
}

// Keys returns every key whose name starts with prefix.
// Uses SCAN to avoid blocking the Redis event loop on large keyspaces.
//
// Note: SCAN does not provide a point-in-time snapshot. Keys inserted or
// deleted between cursor iterations may appear zero or two times. Do not use
// this method on security-critical paths (see ADR-009).
func (s *RedisStore) Keys(ctx context.Context, prefix string) ([]string, error) {
	pattern := prefix + "*"
	if prefix == "" {
		pattern = "*"
	}

	const batchSize = 100
	var (
		keys   []string
		cursor uint64
	)
	for {
		batch, nextCursor, err := s.client.Scan(ctx, cursor, pattern, batchSize).Result()
		if err != nil {
			return nil, telemetry.KVStore("Keys.redis_scan", err)
		}
		keys = append(keys, batch...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return keys, nil
}

// StartCleanup is a no-op for RedisStore; Redis manages TTL expiry server-side.
func (s *RedisStore) StartCleanup(_ context.Context) {}

// Close closes the underlying Redis client connection.
func (s *RedisStore) Close() error {
	return s.client.Close()
}

// RefreshTTL resets the TTL of an existing key without modifying its value.
// Returns (true, nil) when the key exists and the TTL was updated.
// Returns (false, nil) when the key does not exist — caller should treat this
// as expiry and take recovery action.
// Passing ttl ≤ 0 returns an error; a zero TTL would delete the key immediately.
func (s *RedisStore) RefreshTTL(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, fmt.Errorf("kvstore.RefreshTTL: ttl must be positive, got %v", ttl)
	}
	existed, err := s.client.Expire(ctx, key, ttl).Result()
	if err != nil {
		return false, telemetry.KVStore("RefreshTTL.redis_expire", err)
	}
	return existed, nil
}

// ── TokenBlocklist ────────────────────────────────────────────────────────────

// BlockToken adds jti to the Redis blocklist with the given TTL.
// Calling BlockToken with ttl ≤ 0 is a no-op.
func (s *RedisStore) BlockToken(ctx context.Context, jti string, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	// Security: detach from the request context so a client-timed disconnect
	// cannot abort the blocklist write and leave a revoked token accepted.
	if err := s.client.Set(context.WithoutCancel(ctx), blocklistKeyPrefix+jti, "1", ttl).Err(); err != nil {
		return telemetry.KVStore("BlockToken.redis_set", err)
	}
	return nil
}

// IsTokenBlocked reports whether jti exists in the Redis blocklist.
func (s *RedisStore) IsTokenBlocked(ctx context.Context, jti string) (bool, error) {
	return s.Exists(ctx, blocklistKeyPrefix+jti)
}

// ── AtomicBucketStore ─────────────────────────────────────────────────────────

// AtomicBucketAllow implements AtomicBucketStore using go-redis-rate's
// server-side Lua token bucket. The entire read-modify-write is atomic across
// any number of application instances.
//
// idleTTL is intentionally unused: go-redis-rate derives the key TTL from
// burst/rate automatically.
//
// Rate precision: passing rate as a float to go-redis-rate's int-based Limit
// would lose sub-1/sec precision (e.g. 5/10min = 0.00833 rounds to 0).
// Instead the limit is expressed as Rate=burst, Period=burst/rate, which
// preserves the caller's intent exactly.
func (s *RedisStore) AtomicBucketAllow(ctx context.Context, key string, rate, burst float64, _ time.Duration) (bool, error) {
	b := int(math.Round(burst))
	if b <= 0 {
		b = 1
	}
	periodSec := float64(b) / rate
	limit := redis_rate.Limit{
		Rate:   b,
		Burst:  b,
		Period: time.Duration(periodSec * float64(time.Second)),
	}
	res, err := s.limiter.Allow(ctx, key, limit)
	if err != nil {
		return false, telemetry.KVStore("AtomicBucketAllow.redis_allow", err)
	}
	return res.Allowed > 0, nil
}

// AtomicBucketPeek reports whether at least one token is available in the
// bucket WITHOUT consuming one. Uses go-redis-rate AllowN(n=0), which is a
// pure read — bucket state is never decremented.
func (s *RedisStore) AtomicBucketPeek(ctx context.Context, key string, rate, burst float64, _ time.Duration) (bool, error) {
	b := int(math.Round(burst))
	if b <= 0 {
		b = 1
	}
	periodSec := float64(b) / rate
	limit := redis_rate.Limit{
		Rate:   b,
		Burst:  b,
		Period: time.Duration(periodSec * float64(time.Second)),
	}
	res, err := s.limiter.AllowN(ctx, key, limit, 0)
	if err != nil {
		return false, telemetry.KVStore("AtomicBucketPeek.redis_peek", err)
	}
	return res.Remaining >= 1, nil
}

// ── AtomicCounterStore ────────────────────────────────────────────────────────

// AtomicIncrement atomically increments the counter at key by 1.
// New key is initialised to 1. If ttl > 0 the TTL is set (new key) or
// refreshed (existing key). If ttl == 0 the key is permanent and existing TTL
// is preserved. Returns the new count.
func (s *RedisStore) AtomicIncrement(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	result, err := s.evalScript(ctx, s.counterIncrSHA, atomicCounterIncrementScript,
		[]string{key}, ttl.Milliseconds())
	if err != nil {
		return 0, telemetry.KVStore("AtomicIncrement.redis_eval", err)
	}
	n, ok := result.(int64)
	if !ok {
		return 0, errors.New("kvstore.AtomicIncrement: unexpected result type")
	}
	return n, nil
}

// AtomicDecrement atomically decrements the counter at key by 1, flooring at 0.
// Returns 0 when the key does not exist. When ttl > 0 and the count after
// decrement is > 0 the TTL is refreshed (C-03 fix).
func (s *RedisStore) AtomicDecrement(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	result, err := s.evalScript(ctx, s.counterDecrSHA, atomicCounterDecrementScript,
		[]string{key}, ttl.Milliseconds())
	if err != nil {
		return 0, telemetry.KVStore("AtomicDecrement.redis_eval", err)
	}
	n, ok := result.(int64)
	if !ok {
		return 0, errors.New("kvstore.AtomicDecrement: unexpected result type")
	}
	return n, nil
}

// AtomicAcquire atomically increments the counter when current count < max.
// Returns the new count on success, -1 when already at or above cap.
func (s *RedisStore) AtomicAcquire(ctx context.Context, key string, max int, ttl time.Duration) (int64, error) {
	result, err := s.evalScript(ctx, s.counterAcqSHA, atomicCounterAcquireScript,
		[]string{key}, max, ttl.Milliseconds())
	if err != nil {
		return 0, telemetry.KVStore("AtomicAcquire.redis_eval", err)
	}
	n, ok := result.(int64)
	if !ok {
		return 0, errors.New("kvstore.AtomicAcquire: unexpected result type")
	}
	return n, nil
}

// ── ListStore ─────────────────────────────────────────────────────────────────

// LPush prepends one or more values to the list at key, creating it if needed.
// Returns the new list length.
func (s *RedisStore) LPush(ctx context.Context, key string, values ...string) (int64, error) {
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	n, err := s.client.LPush(ctx, key, args...).Result()
	if err != nil {
		return 0, telemetry.KVStore("LPush.redis_lpush", err)
	}
	return n, nil
}

// BRPop pops the rightmost element from the first non-empty list, blocking up
// to timeout. Returns ("", "", ErrNotFound) on timeout.
func (s *RedisStore) BRPop(ctx context.Context, timeout time.Duration, keys ...string) (string, string, error) {
	result, err := s.client.BRPop(ctx, timeout, keys...).Result()
	if errors.Is(err, redis.Nil) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", telemetry.KVStore("BRPop.redis_brpop", err)
	}
	if len(result) != 2 {
		return "", "", errors.New("kvstore.BRPop: unexpected result length")
	}
	return result[0], result[1], nil
}

// LLen returns the current list length. Returns 0 when the key does not exist.
func (s *RedisStore) LLen(ctx context.Context, key string) (int64, error) {
	n, err := s.client.LLen(ctx, key).Result()
	if err != nil {
		return 0, telemetry.KVStore("LLen.redis_llen", err)
	}
	return n, nil
}

// ── PubSubStore ───────────────────────────────────────────────────────────────

// Publish sends payload to all subscribers of channel.
// Returns subscriber count (0 is not an error).
func (s *RedisStore) Publish(ctx context.Context, channel, payload string) (int64, error) {
	n, err := s.client.Publish(ctx, channel, payload).Result()
	if err != nil {
		return 0, telemetry.KVStore("Publish.redis_publish", err)
	}
	return n, nil
}

// Subscribe returns a buffered channel of PubSubMessages and a cancel func.
// The message channel is closed when cancel() is called or ctx is cancelled.
// Callers MUST always call the returned cancel func.
//
// Reconnects with exponential backoff (1s initial, 60s ceiling) on disconnect.
// Messages published during reconnect windows are permanently lost — callers
// must use periodic full reloads as the authoritative recovery path.
func (s *RedisStore) Subscribe(ctx context.Context, channels ...string) (<-chan PubSubMessage, func()) {
	ch := make(chan PubSubMessage, 64)
	ctxSub, cancelSub := context.WithCancel(ctx)

	go func() {
		defer close(ch)
		backoff := time.Second
		const maxBackoff = 60 * time.Second
		for {
			sub := s.client.Subscribe(ctxSub, channels...)
			msgCh := sub.Channel()
			broken := false
			for !broken {
				select {
				case msg, ok := <-msgCh:
					if !ok {
						broken = true
					} else {
						select {
						case ch <- PubSubMessage{Channel: msg.Channel, Payload: msg.Payload}:
						default:
							// Subscriber channel full — message dropped.
						}
					}
				case <-ctxSub.Done():
					_ = sub.Close()
					return
				}
			}
			_ = sub.Close()
			select {
			case <-time.After(backoff):
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			case <-ctxSub.Done():
				return
			}
		}
	}()

	return ch, cancelSub
}

// ── SetStore ──────────────────────────────────────────────────────────────────

// SAdd adds one or more members to the set at key, creating it if needed.
// Returns the number of members actually added (excluding pre-existing ones).
func (s *RedisStore) SAdd(ctx context.Context, key string, members ...string) (int64, error) {
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	n, err := s.client.SAdd(ctx, key, args...).Result()
	if err != nil {
		return 0, telemetry.KVStore("SAdd.redis_sadd", err)
	}
	return n, nil
}

// SRem removes one or more members from the set at key. Missing members are
// ignored. Returns the number of members actually removed.
func (s *RedisStore) SRem(ctx context.Context, key string, members ...string) (int64, error) {
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	n, err := s.client.SRem(ctx, key, args...).Result()
	if err != nil {
		return 0, telemetry.KVStore("SRem.redis_srem", err)
	}
	return n, nil
}

// SCard returns the number of members in the set at key.
// Returns 0 when the key does not exist.
func (s *RedisStore) SCard(ctx context.Context, key string) (int64, error) {
	n, err := s.client.SCard(ctx, key).Result()
	if err != nil {
		return 0, telemetry.KVStore("SCard.redis_scard", err)
	}
	return n, nil
}

// SScan incrementally iterates over members of the set at key.
// Pass cursor=0 to start a new iteration; iteration is complete when the
// returned cursor is 0. match filters by glob pattern ("" = all). count is a
// hint for elements per call — not a guarantee.
func (s *RedisStore) SScan(ctx context.Context, key string, cursor uint64, match string, count int64) ([]string, uint64, error) {
	members, nextCursor, err := s.client.SScan(ctx, key, cursor, match, count).Result()
	if err != nil {
		return nil, 0, telemetry.KVStore("SScan.redis_sscan", err)
	}
	return members, nextCursor, nil
}

// ── OnceStore ─────────────────────────────────────────────────────────────────

// ConsumeOnce atomically creates key with ttl only if it does not already exist
// (Redis SET NX PX). Returns true when the key was newly created — the caller
// owns the slot. Returns false when the key already existed — already consumed.
// ttl must be positive; a zero or negative ttl returns an error.
func (s *RedisStore) ConsumeOnce(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, fmt.Errorf("kvstore.ConsumeOnce: ttl must be positive, got %v", ttl)
	}
	ok, err := s.client.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		return false, telemetry.KVStore("ConsumeOnce.redis_setnx", err)
	}
	return ok, nil
}

// ── AtomicBackoffStore ────────────────────────────────────────────────────────

// AtomicBackoffIncrement atomically increments the failure counter and computes
// the new unlock timestamp using exponential backoff with a cap.
func (s *RedisStore) AtomicBackoffIncrement(ctx context.Context, key string, baseDelay, maxDelay, idleTTL time.Duration) (time.Time, int, error) {
	result, err := s.evalScript(ctx, s.incrSHA, atomicBackoffIncrementScript,
		[]string{key}, baseDelay.Milliseconds(), maxDelay.Milliseconds(), idleTTL.Milliseconds())
	if err != nil {
		return time.Time{}, 0, telemetry.KVStore("AtomicBackoffIncrement.redis_eval", err)
	}

	values, ok := result.([]any)
	if !ok || len(values) != 2 {
		return time.Time{}, 0, errors.New("kvstore.AtomicBackoffIncrement: unexpected result format")
	}
	failures, ok1 := values[0].(int64)
	unlocksAtMs, ok2 := values[1].(int64)
	if !ok1 || !ok2 {
		return time.Time{}, 0, errors.New("kvstore.AtomicBackoffIncrement: unexpected value types")
	}
	return time.UnixMilli(unlocksAtMs), int(failures), nil
}

// AtomicBackoffAllow atomically checks whether the key is allowed to proceed
// based on the current backoff state.
// Returns (true, 0, nil) when the key may proceed (no failures or unlocked).
// Returns (false, remaining, nil) when still within a backoff window.
// Returns (false, 0, err) on a transient store error.
func (s *RedisStore) AtomicBackoffAllow(ctx context.Context, key string) (bool, time.Duration, error) {
	result, err := s.evalScript(ctx, s.allowSHA, atomicBackoffAllowScript, []string{key})
	if err != nil {
		return false, 0, telemetry.KVStore("AtomicBackoffAllow.redis_eval", err)
	}

	values, ok := result.([]any)
	if !ok || len(values) != 2 {
		return false, 0, errors.New("kvstore.AtomicBackoffAllow: unexpected result format")
	}
	allowedInt, ok1 := values[0].(int64)
	remainingMs, ok2 := values[1].(int64)
	if !ok1 || !ok2 {
		return false, 0, errors.New("kvstore.AtomicBackoffAllow: unexpected value types")
	}
	return allowedInt == 1, time.Duration(remainingMs) * time.Millisecond, nil
}

// ── WatchCapStore ─────────────────────────────────────────────────────────────

// RunWatchCapScript executes watchCapLuaScript atomically for the given user.
//
// addresses must already be normalised (trimmed + lowercased) by the handler
// before this method is invoked.
//
// Returns (success, newCount, addedCount):
//   - success ==  1: completed; addedCount may be 0 if all addresses were pre-existing
//   - success ==  0: count cap exceeded; watch set is unchanged
//   - success == -1: 7-day registration window has expired
func (s *RedisStore) RunWatchCapScript(
	ctx context.Context,
	setKey, regAtKey, lastActiveKey string,
	limit int,
	watchTTL, lastActiveTTL time.Duration,
	addresses []string,
) (success, newCount, addedCount int64, err error) {
	keys := []string{setKey, regAtKey, lastActiveKey}

	args := make([]any, 3+len(addresses))
	args[0] = limit
	args[1] = int(watchTTL.Seconds())
	args[2] = int(lastActiveTTL.Seconds())
	for i, addr := range addresses {
		args[3+i] = addr
	}

	raw, err := s.evalScript(ctx, s.watchCapSHA, watchCapLuaScript, keys, args...)
	if err != nil {
		return 0, 0, 0, telemetry.KVStore("RunWatchCapScript.redis_eval", err)
	}

	values, ok := raw.([]any)
	if !ok || len(values) != 3 {
		return 0, 0, 0, errors.New("kvstore.RunWatchCapScript: unexpected result format")
	}
	s0, ok0 := values[0].(int64)
	s1, ok1 := values[1].(int64)
	s2, ok2 := values[2].(int64)
	if !ok0 || !ok1 || !ok2 {
		return 0, 0, 0, errors.New("kvstore.RunWatchCapScript: unexpected value types")
	}
	return s0, s1, s2, nil
}

// ScanWatchAddressKeys iterates over all watch-address SET keys matching the
// pattern "*:addresses" with Redis SCAN TYPE set, returning one page per call.
// Pass cursor=0 to start; iteration is complete when nextCursor returns 0.
//
// Warning: in a Redis Cluster deployment SCAN operates on a single shard and
// will undercount. See watch-technical.md §5 for the cluster caveat.
func (s *RedisStore) ScanWatchAddressKeys(ctx context.Context, cursor uint64, count int64) (keys []string, nextCursor uint64, err error) {
	// ScanType requires Redis 6.0+ and prevents WRONGTYPE errors on non-SET
	// keys that happen to match the "*:addresses" pattern.
	result, nextC, err := s.client.ScanType(ctx, cursor, "*:addresses", count, "set").Result()
	if err != nil {
		return nil, 0, telemetry.KVStore("ScanWatchAddressKeys.redis_scan_type", err)
	}
	return result, nextC, nil
}

// ── Telemetry ─────────────────────────────────────────────────────────────────

// PoolStats returns the current Redis connection pool statistics.
// Satisfies telemetry.RedisStatsProvider so the InfraPoller can report
// redis_pool_* metrics without importing the kvstore package.
func (s *RedisStore) PoolStats() *redis.PoolStats {
	return s.client.PoolStats()
}

// Ping issues a PING command to Redis with the given context.
// Satisfies telemetry.RedisStatsProvider so the InfraPoller can actively probe
// Redis on every tick, surfacing outages within one 15-second poll cycle.
func (s *RedisStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

// ── internal ──────────────────────────────────────────────────────────────────

// evalScript executes a Lua script using EvalSha to avoid retransmitting the
// full source on every call. On a NOSCRIPT error (script evicted after a Redis
// restart or FLUSHALL), it reloads the script and falls back to Eval with the
// full source. The SHA is deterministic and the struct field does not need
// updating after a fallback.
func (s *RedisStore) evalScript(ctx context.Context, sha, script string, keys []string, args ...any) (any, error) {
	result, err := s.client.EvalSha(ctx, sha, keys, args...).Result()
	// strings.Contains rather than HasPrefix: go-redis may prepend a wrapper
	// message in a future release; Contains tolerates any future prefix.
	if err != nil && strings.Contains(err.Error(), "NOSCRIPT") {
		_, _ = s.client.ScriptLoad(ctx, script).Result()
		return s.client.Eval(ctx, script, keys, args...).Result()
	}
	return result, err
}

// ── Compile-time interface checks ─────────────────────────────────────────────
//
// Order mirrors the interface declaration order in store.go so diffs between
// the two files are trivially auditable. Add a new check here whenever a new
// interface is implemented (see §3.16 of RULES.md).

// compile-time interface checks.
var _ Store = (*RedisStore)(nil)
var _ TokenBlocklist = (*RedisStore)(nil)
var _ AtomicBucketStore = (*RedisStore)(nil)
var _ AtomicCounterStore = (*RedisStore)(nil)
var _ ListStore = (*RedisStore)(nil)
var _ PubSubStore = (*RedisStore)(nil)
var _ SetStore = (*RedisStore)(nil)
var _ OnceStore = (*RedisStore)(nil)
var _ AtomicBackoffStore = (*RedisStore)(nil)
var _ WatchCapStore = (*RedisStore)(nil)

// redisStatsProviderCheck ensures RedisStore satisfies telemetry.RedisStatsProvider
// at compile time. The import cycle is avoided because telemetry does not import kvstore.
type redisStatsProviderCheck interface {
	PoolStats() *redis.PoolStats
	Ping(ctx context.Context) error
}

var _ redisStatsProviderCheck = (*RedisStore)(nil)
