package kvstore

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

// RedisStore is a Store backed by a Redis instance.
//
// It uses atomic Lua scripts for token-bucket, backoff, and counter operations,
// making it safe for multi-instance deployments where a process-local mutex
// cannot provide the required isolation.
type RedisStore struct {
	client         *redis.Client
	limiter        *redis_rate.Limiter
	incrSHA        string // SHA of atomicBackoffIncrementScript pre-loaded at startup
	allowSHA       string // SHA of atomicBackoffAllowScript pre-loaded at startup
	counterIncrSHA string // SHA of atomicCounterIncrementScript
	counterDecrSHA string // SHA of atomicCounterDecrementScript
	counterAcqSHA  string // SHA of atomicCounterAcquireScript
}

// NewRedisStore dials a Redis server at the given URL and returns a RedisStore.
// Returns an error if the URL cannot be parsed, if the initial ping fails, or
// if the Lua scripts cannot be loaded into the Redis script cache.
func NewRedisStore(url string) (*RedisStore, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, telemetry.KVStore("NewRedisStore.parse_url", err)
	}

	// Override connection-pool defaults to ensure fast failure detection.
	//
	// DialTimeout: how long a new TCP connection attempt may take before the
	// pool gives up. The default (5 s) means the first request after Redis
	// goes down blocks for 5 s before surfacing an error. 2 s is tight enough
	// to detect outages quickly while still tolerating a slow-starting Redis.
	//
	// ReadTimeout / WriteTimeout: per-command deadline. Lowered from the
	// default 3 s to match DialTimeout so every failed command surfaces within
	// the same 2 s window.
	//
	// ConnMaxIdleTime: maximum time a connection may sit idle in the pool
	// before it is closed and replaced on next checkout. Without this, idle
	// connections are never health-checked and the pool appears healthy even
	// when Redis has been unreachable for minutes. 30 s ensures that within
	// one InfraPoller cycle (15 s poll + up to 30 s idle) a stale connection
	// is discovered and StaleConns increments — making the dashboard reflect
	// the outage within ~45 s worst-case instead of 3+ minutes.
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

	// Ping: verify connectivity with a dedicated 5-second deadline.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.connect", err)
	}

	// ScriptLoad: pre-load Lua scripts with their own 5-second deadline so a
	// slow Ping cannot exhaust the budget before script registration begins.
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer loadCancel()

	incrSHA, err := client.ScriptLoad(loadCtx, atomicBackoffIncrementScript).Result()
	if err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.load_increment_script", err)
	}
	allowSHA, err := client.ScriptLoad(loadCtx, atomicBackoffAllowScript).Result()
	if err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.load_allow_script", err)
	}
	counterIncrSHA, err := client.ScriptLoad(loadCtx, atomicCounterIncrementScript).Result()
	if err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.load_counter_incr_script", err)
	}
	counterDecrSHA, err := client.ScriptLoad(loadCtx, atomicCounterDecrementScript).Result()
	if err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.load_counter_decr_script", err)
	}
	counterAcqSHA, err := client.ScriptLoad(loadCtx, atomicCounterAcquireScript).Result()
	if err != nil {
		_ = client.Close()
		return nil, telemetry.KVStore("NewRedisStore.load_counter_acq_script", err)
	}

	return &RedisStore{
		client:         client,
		limiter:        redis_rate.NewLimiter(client),
		incrSHA:        incrSHA,
		allowSHA:       allowSHA,
		counterIncrSHA: counterIncrSHA,
		counterDecrSHA: counterDecrSHA,
		counterAcqSHA:  counterAcqSHA,
	}, nil
}

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
// Uses SCAN instead of KEYS to avoid blocking the Redis event loop on large keyspaces.
//
// Note: SCAN does not provide a point-in-time snapshot. Keys inserted or deleted
// between cursor iterations may appear zero or two times in the result. Do not
// use this method on security-critical paths; it is intended for diagnostic and
// administrative use only (see ADR-009).
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

// AtomicBucketAllow implements AtomicBucketStore using go-redis-rate's Lua-script
// backed token bucket. The entire read-modify-write is executed atomically on the
// Redis server, making it safe across any number of application instances.
//
// idleTTL is intentionally unused: go-redis-rate derives the key TTL from
// burst/rate automatically — after burst/rate seconds of inactivity the bucket
// is naturally full and can be discarded.
func (s *RedisStore) AtomicBucketAllow(ctx context.Context, key string, rate, burst float64, _ time.Duration) (bool, error) {
	b := int(math.Round(burst))
	if b <= 0 {
		b = 1
	}

	// Derive the redis_rate.Limit from rate (tokens/sec) and burst.
	//
	// Rounding rate to an int loses all sub-1/sec precision — e.g.
	// 5/(10*60)=0.00833 rounds to 0, gets clamped to 1, and the limit
	// becomes "1/sec burst 5" instead of "5 per 10 min".
	//
	// Instead: express as Rate=burst, Period=burst/rate.
	// This preserves the caller's intent exactly:
	//   burst=5, rate=0.00833  →  Rate=5, Period=600s (10 min) ✓
	//   burst=10, rate=10      →  Rate=10, Period=1s            ✓
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

// blocklistKeyPrefix is already declared in store.go.

// BlockToken adds jti to the Redis blocklist with the given TTL.
// Calling BlockToken with ttl ≤ 0 is a no-op.
func (s *RedisStore) BlockToken(ctx context.Context, jti string, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	key := blocklistKeyPrefix + jti
	// Security: detach from the request context so a client-timed disconnect
	// cannot abort the blocklist write and leave a revoked token accepted.
	if err := s.client.Set(context.WithoutCancel(ctx), key, "1", ttl).Err(); err != nil {
		return telemetry.KVStore("BlockToken.redis_set", err)
	}
	return nil
}

// IsTokenBlocked reports whether jti exists in the Redis blocklist.
func (s *RedisStore) IsTokenBlocked(ctx context.Context, jti string) (bool, error) {
	return s.Exists(ctx, blocklistKeyPrefix+jti)
}

// atomicCounterIncrementScript atomically increments a plain integer counter.
// ARGV[1]: ttl in milliseconds (0 = permanent; existing TTL preserved on 0).
// Returns: new count.
// Requires Redis 6.0+ for SET ... KEEPTTL in the ttl==0 branch.
const atomicCounterIncrementScript = `
local key = KEYS[1]
local ttlMs = tonumber(ARGV[1])
local current = tonumber(redis.call('GET', key))
if current == nil then
    current = 0
elseif current < 0 then
    current = 0  -- corruption repair
end
local next = current + 1
if ttlMs > 0 then
    redis.call('SET', key, next, 'PX', ttlMs)
elseif redis.call('EXISTS', key) == 1 then
    redis.call('SET', key, next, 'KEEPTTL')
else
    redis.call('SET', key, next)
end
return next
`

// atomicCounterDecrementScript atomically decrements a plain integer counter, flooring at 0.
// ARGV[1]: ttl in milliseconds for TTL refresh when count > 0 after decrement (0 = no refresh).
// Returns: new count (never negative).
// Requires Redis 6.0+ for SET ... KEEPTTL in the corruption repair path.
const atomicCounterDecrementScript = `
local key = KEYS[1]
local ttlMs = tonumber(ARGV[1])
local current = tonumber(redis.call('GET', key))
if current == nil then
    return 0
end
if current <= 0 then
    -- Corruption repair: reset to 0, preserve TTL.
    redis.call('SET', key, 0, 'KEEPTTL')
    return 0
end
local next = current - 1
if next == 0 then
    redis.call('DEL', key)
elseif ttlMs > 0 then
    redis.call('SET', key, next, 'PX', ttlMs)
else
    redis.call('SET', key, next, 'KEEPTTL')
end
return next
`

// atomicCounterAcquireScript atomically increments if current count < max.
// ARGV[1]: max (integer ceiling).
// ARGV[2]: ttl in milliseconds (0 = permanent).
// Returns: new count on success; -1 when at or above cap.
// Requires Redis 6.0+ for SET ... KEEPTTL.
const atomicCounterAcquireScript = `
local key = KEYS[1]
local max = tonumber(ARGV[1])
local ttlMs = tonumber(ARGV[2])
local current = tonumber(redis.call('GET', key))
if current == nil then
    current = 0
elseif current < 0 then
    current = 0  -- corruption repair
end
if current >= max then
    return -1
end
local next = current + 1
if ttlMs > 0 then
    redis.call('SET', key, next, 'PX', ttlMs)
elseif redis.call('EXISTS', key) == 1 then
    redis.call('SET', key, next, 'KEEPTTL')
else
    redis.call('SET', key, next)
end
return next
`

// RefreshTTL resets the TTL of an existing Redis key.
// Redis EXPIRE returns 1 when key exists and TTL was set, 0 when key does not exist.
func (s *RedisStore) RefreshTTL(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, fmt.Errorf("kvstore.RefreshTTL: ttl must be positive, got %v", ttl)
	}
	// Expire returns true when the key exists and the TTL was set,
	// false when the key does not exist. go-redis v9 returns bool, not int.
	existed, err := s.client.Expire(ctx, key, ttl).Result()
	if err != nil {
		return false, telemetry.KVStore("RefreshTTL.redis_expire", err)
	}
	return existed, nil
}

// AtomicIncrement atomically increments the counter at key by 1.
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

// AtomicAcquire atomically increments the counter if current count < max.
// Returns -1 when at or above cap.
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

// LPush prepends one or more values to the list at key.
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

// BRPop pops the rightmost element from the first non-empty list, blocking up to timeout.
// Returns ("", "", ErrNotFound) on timeout.
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

// LLen returns the current list length.
func (s *RedisStore) LLen(ctx context.Context, key string) (int64, error) {
	n, err := s.client.LLen(ctx, key).Result()
	if err != nil {
		return 0, telemetry.KVStore("LLen.redis_llen", err)
	}
	return n, nil
}

// Publish sends payload to all subscribers of channel.
func (s *RedisStore) Publish(ctx context.Context, channel, payload string) (int64, error) {
	n, err := s.client.Publish(ctx, channel, payload).Result()
	if err != nil {
		return 0, telemetry.KVStore("Publish.redis_publish", err)
	}
	return n, nil
}

// Subscribe returns a channel of PubSubMessages and a cancel func.
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
						}
					}
				case <-ctxSub.Done():
					_ = sub.Close()
					return
				}
			}
			_ = sub.Close()
			// Reconnect with backoff.
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

// atomicBackoffIncrementScript is a Lua script that atomically increments the
// failure counter, computes the exponential backoff delay, sets the unlock
// timestamp, and updates the key TTL.
//
// Time is obtained from Redis via TIME so that both increment and allow
// scripts share the same monotonically-advancing clock source. This avoids
// flakiness caused by OS wall-clock adjustments (e.g. NTP) between the two
// Go call sites.
//
// KEYS[1]: the backoff entry key
// ARGV[1]: baseDelay in milliseconds
// ARGV[2]: maxDelay in milliseconds
// ARGV[3]: idleTTL in milliseconds
//
// Returns: [failures, unlocksAtUnixMs]
const atomicBackoffIncrementScript = `
local key = KEYS[1]
local baseDelayMs = tonumber(ARGV[1])
local maxDelayMs = tonumber(ARGV[2])
local ttlMs = tonumber(ARGV[3])

local t = redis.call('TIME')
local nowMs = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)

local failures = redis.call('HINCRBY', key, 'failures', 1)
redis.call('HSET', key, 'last_seen', nowMs)

local exp = math.pow(2, failures - 1)
local delayMs = math.min(baseDelayMs * exp, maxDelayMs)

local unlocksAtMs = nowMs + delayMs
redis.call('HSET', key, 'unlocks_at', unlocksAtMs)

-- Guard: only set PEXPIRE when ttlMs is positive.
-- PEXPIRE with a non-positive value is an error in Redis ≥ 2.6 and may
-- delete the key immediately in some versions, corrupting backoff state.
if ttlMs > 0 then
    redis.call('PEXPIRE', key, ttlMs)
end

return {failures, unlocksAtMs}
`

// atomicBackoffAllowScript is a Lua script that atomically checks if a key
// is allowed to proceed based on the current backoff state.
//
// Time is obtained from Redis via TIME, matching the clock source used by
// atomicBackoffIncrementScript so the unlocks_at comparison is always coherent.
//
// KEYS[1]: the backoff entry key
//
// Returns: [allowed (0 or 1), remainingMs]
const atomicBackoffAllowScript = `
local key = KEYS[1]

local t = redis.call('TIME')
local nowMs = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)

if redis.call('EXISTS', key) == 0 then
    return {1, 0}
end

local failures = tonumber(redis.call('HGET', key, 'failures'))
if not failures or failures == 0 then
    return {1, 0}
end

local unlocksAtMs = tonumber(redis.call('HGET', key, 'unlocks_at'))
if not unlocksAtMs then
    return {1, 0}
end

local remainingMs = unlocksAtMs - nowMs
if remainingMs <= 0 then
    return {1, 0}
end

return {0, remainingMs}
`

// evalScript executes the given Lua script using EvalSha for reduced network
// overhead (the full source — ~400–600 bytes — is not retransmitted on every
// call). On a NOSCRIPT error (script evicted after a Redis restart or
// FLUSHALL), it falls back to Eval with the full source and re-registers the
// script in the server cache. The SHA is deterministic and does not change, so
// no struct field update is required after the fallback.
func (s *RedisStore) evalScript(ctx context.Context, sha, script string, keys []string, args ...any) (any, error) {
	result, err := s.client.EvalSha(ctx, sha, keys, args...).Result()
	// strings.Contains rather than HasPrefix: go-redis may prepend a wrapper
	// message in a future release; Contains tolerates any future prefix while
	// still matching the Redis server error token "NOSCRIPT".
	if err != nil && strings.Contains(err.Error(), "NOSCRIPT") {
		// Re-register the script, then fall back to a full EVAL.
		_, _ = s.client.ScriptLoad(ctx, script).Result()
		return s.client.Eval(ctx, script, keys, args...).Result()
	}
	return result, err
}

// AtomicBackoffIncrement atomically increments the failure counter and computes
// the new unlock timestamp using exponential backoff with a cap.
func (s *RedisStore) AtomicBackoffIncrement(ctx context.Context, key string, baseDelay, maxDelay, idleTTL time.Duration) (time.Time, int, error) {
	baseDelayMs := baseDelay.Milliseconds()
	maxDelayMs := maxDelay.Milliseconds()
	ttlMs := idleTTL.Milliseconds()

	result, err := s.evalScript(ctx, s.incrSHA, atomicBackoffIncrementScript, []string{key}, baseDelayMs, maxDelayMs, ttlMs)
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

// AtomicBackoffAllow atomically checks if the key is allowed to proceed based
// on the current backoff state.
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

// AtomicBucketPeek reports whether at least one token is available in the
// bucket WITHOUT consuming it, by requesting 0 tokens from go-redis-rate.
//
// go-redis-rate's AllowN(n=0) returns the current remaining token count
// without modifying bucket state, making it a true non-destructive read.
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
	// AllowN with n=0: the Lua script never decrements the bucket, so this is
	// a pure read. res.Remaining reflects current available tokens.
	res, err := s.limiter.AllowN(ctx, key, limit, 0)
	if err != nil {
		return false, telemetry.KVStore("AtomicBucketPeek.redis_peek", err)
	}
	return res.Remaining >= 1, nil
}

// PoolStats returns the current Redis connection pool statistics.
// Satisfies telemetry.RedisStatsProvider so the InfraPoller can report
// redis_pool_* metrics without importing the kvstore package.
func (s *RedisStore) PoolStats() *redis.PoolStats {
	return s.client.PoolStats()
}

// Ping issues a PING command to Redis with the given context.
// Satisfies telemetry.RedisStatsProvider so the InfraPoller can actively probe
// Redis on every tick, surfacing outages within one 15-second poll cycle
// regardless of whether any request traffic is hitting Redis at the time.
func (s *RedisStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

// SAdd adds one or more members to the set at key.
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

// SRem removes one or more members from the set at key.
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
func (s *RedisStore) SCard(ctx context.Context, key string) (int64, error) {
	n, err := s.client.SCard(ctx, key).Result()
	if err != nil {
		return 0, telemetry.KVStore("SCard.redis_scard", err)
	}
	return n, nil
}

// SScan incrementally iterates over members of the set at key.
func (s *RedisStore) SScan(ctx context.Context, key string, cursor uint64, match string, count int64) ([]string, uint64, error) {
	members, nextCursor, err := s.client.SScan(ctx, key, cursor, match, count).Result()
	if err != nil {
		return nil, 0, telemetry.KVStore("SScan.redis_sscan", err)
	}
	return members, nextCursor, nil
}

// ConsumeOnce atomically sets key with ttl only if it does not already exist
// (Redis SET NX PX). Returns true when the key was newly created.
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

// compile-time interface checks.
var _ Store = (*RedisStore)(nil)
var _ TokenBlocklist = (*RedisStore)(nil)
var _ AtomicBucketStore = (*RedisStore)(nil)
var _ AtomicBackoffStore = (*RedisStore)(nil)
var _ AtomicCounterStore = (*RedisStore)(nil)
var _ ListStore = (*RedisStore)(nil)
var _ PubSubStore = (*RedisStore)(nil)
var _ SetStore = (*RedisStore)(nil)
var _ OnceStore = (*RedisStore)(nil)

// Ensure RedisStore satisfies the telemetry.RedisStatsProvider interface so
// a missing Ping() method is caught at compile time, not at server startup.
// Import cycle is avoided because telemetry does not import kvstore.
type redisStatsProviderCheck interface {
	PoolStats() *redis.PoolStats
	Ping(ctx context.Context) error
}

var _ redisStatsProviderCheck = (*RedisStore)(nil)
