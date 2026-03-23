// Package kvstore — Lua script constants for RedisStore.
//
// scripts.go contains every Lua script that RedisStore pre-loads into Redis at
// startup. Nothing else belongs here: no imports, no types, no methods.
//
// # Ordering rule (mirrors redis.go — see §3.16 of RULES.md)
//
// Scripts are declared in load-sequence order (1 → N), matching the SHA struct
// field order in RedisStore and the ScriptLoad call order in NewRedisStore.
// When adding a new script, append it at the end of this file and follow the
// step-by-step procedure in RULES.md §3.16.
//
// Never reorder existing constants — the load sequence is observable through
// integration-test telemetry labels and must not change without updating those
// tests.
//
// # Current load sequence
//
//	1  atomicBackoffIncrementScript   AtomicBackoffStore
//	2  atomicBackoffAllowScript       AtomicBackoffStore
//	3  atomicCounterIncrementScript   AtomicCounterStore
//	4  atomicCounterDecrementScript   AtomicCounterStore
//	5  atomicCounterAcquireScript     AtomicCounterStore
//	6  watchCapLuaScript              WatchCapStore
package kvstore

// ── AtomicBackoffStore scripts (load order 1–2) ───────────────────────────────

// atomicBackoffIncrementScript atomically increments the failure counter,
// computes the exponential backoff delay, stores the unlock timestamp, and
// refreshes the key TTL — all in a single server-side round-trip.
//
// Time comes from Redis TIME so both increment and allow scripts share the same
// monotonically-advancing clock, preventing flakiness from OS NTP adjustments.
//
// KEYS[1]: backoff entry hash key
// ARGV[1]: baseDelay in milliseconds
// ARGV[2]: maxDelay in milliseconds
// ARGV[3]: idleTTL in milliseconds (0 = no PEXPIRE; key persists indefinitely)
//
// Returns: [failures int64, unlocksAtUnixMs int64]
//
// Guard: PEXPIRE is skipped when ttlMs ≤ 0; a non-positive PEXPIRE argument is
// an error in Redis ≥ 2.6 and may immediately delete the key in some versions.
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

if ttlMs > 0 then
    redis.call('PEXPIRE', key, ttlMs)
end

return {failures, unlocksAtMs}
`

// atomicBackoffAllowScript atomically checks whether the backoff window for the
// given key has expired. Uses Redis TIME to match the clock source of
// atomicBackoffIncrementScript so the unlocks_at comparison is always coherent.
//
// KEYS[1]: backoff entry hash key
//
// Returns: [allowed int64 (0 or 1), remainingMs int64]
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

// ── AtomicCounterStore scripts (load order 3–5) ───────────────────────────────

// atomicCounterIncrementScript atomically increments a plain integer counter.
//
// KEYS[1]: counter key
// ARGV[1]: ttl in milliseconds (0 = permanent; existing TTL is preserved on 0)
//
// Returns: new count int64
//
// Requires Redis 6.0+ for SET … KEEPTTL in the ttl == 0 branch.
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

// atomicCounterDecrementScript atomically decrements a plain integer counter,
// flooring at 0 and deleting the key when the count reaches 0.
//
// KEYS[1]: counter key
// ARGV[1]: ttl in milliseconds — when > 0 AND count > 0 after decrement, the
//          TTL is refreshed (C-03: prevents a safety TTL from expiring while
//          other connections are still open)
//
// Returns: new count int64 (never negative)
//
// Requires Redis 6.0+ for SET … KEEPTTL in the corruption-repair path.
const atomicCounterDecrementScript = `
local key = KEYS[1]
local ttlMs = tonumber(ARGV[1])
local current = tonumber(redis.call('GET', key))
if current == nil then
    return 0
end
if current <= 0 then
    -- Corruption repair: reset to 0 and preserve TTL.
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

// atomicCounterAcquireScript atomically increments the counter when the current
// count is below max, returning -1 when already at or above the cap.
//
// KEYS[1]: counter key
// ARGV[1]: max (integer ceiling)
// ARGV[2]: ttl in milliseconds (0 = permanent)
//
// Returns: new count int64 on success; -1 when at or above cap
//
// Requires Redis 6.0+ for SET … KEEPTTL.
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

// ── WatchCapStore scripts (load order 6) ──────────────────────────────────────

// watchCapLuaScript atomically enforces the per-user watch-address count cap
// and the 7-day absolute registration window for the Bitcoin display-watch
// system.
//
// KEYS[1] = {btc:user:{userID}}:addresses      (watch SET)
// KEYS[2] = {btc:user:{userID}}:registered_at  (set once with NX; never refreshed)
// KEYS[3] = {btc:user:{userID}}:last_active    (refreshed on every call)
//
// All three keys share the hash tag {btc:user:{userID}} so they land on the
// same Redis Cluster slot (Q-91 cluster safety).
//
// ARGV[1] = limit               (BTC_MAX_WATCH_PER_USER)
// ARGV[2] = watch-set TTL secs  (1800 = 30 min); set only when ≥1 address is added
// ARGV[3] = last_active TTL secs (1800)
// ARGV[4..n] = candidate addresses (trimmed + lowercased by validateAndNormalise)
//
// Returns: {success, newCount, addedCount}
//   success ==  1: completed; addedCount may be 0 (all addresses pre-existing)
//   success ==  0: count cap exceeded; watch set is unchanged (rollback applied)
//   success == -1: 7-day registration window expired
const watchCapLuaScript = `
local setKey        = KEYS[1]
local regAtKey      = KEYS[2]
local lastActiveKey = KEYS[3]

local nowArr = redis.call('TIME')
local now = tonumber(nowArr[1])  -- Unix seconds from Redis server clock

-- 7-day absolute cap check.
-- registered_at is set with NX on first registration and is NEVER refreshed.
-- Once 604800 s have elapsed all further registrations are rejected.
local regAt = redis.call('GET', regAtKey)
if regAt ~= false then
    if now - tonumber(regAt) > 604800 then
        -- M-02/OD-07: schedule registered_at cleanup so stale keys do not
        -- accumulate indefinitely. 30-day TTL from first registration;
        -- minimum 1-day grace period.
        local elapsed = now - tonumber(regAt)
        local cleanupIn = 2592000 - elapsed
        if cleanupIn < 86400 then cleanupIn = 86400 end
        redis.call('EXPIRE', regAtKey, cleanupIn)
        return {-1, 0, 0}
    end
end

local current = redis.call('SCARD', setKey)
local added = {}
for i = 4, #ARGV do
    if redis.call('SADD', setKey, ARGV[i]) == 1 then
        added[#added+1] = ARGV[i]
    end
end

if current + #added > tonumber(ARGV[1]) then
    -- Count cap exceeded: roll back speculative adds.
    for _, addr in ipairs(added) do redis.call('SREM', setKey, addr) end
    return {0, current, 0}
end

if #added > 0 then
    -- Only set EXPIRE when at least one new address was added.
    redis.call('EXPIRE', setKey, ARGV[2])
    -- NX: set registered_at only on first registration.
    redis.call('SET', regAtKey, now, 'NX')
end

-- Always refresh last_active (even on re-registration).
redis.call('SET', lastActiveKey, now, 'EX', ARGV[3])

return {1, current + #added, #added}
`
