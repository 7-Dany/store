# Prerequisites — app.Deps Additions

> **Package:** `internal/app/`
> **Files affected:** `deps.go`
>
> **Status:** Must be merged before any bitcoin domain code is written.
> **Depends on:** kvstore, ratelimit, config prerequisites all merged.
> **Blocks:** `server.go` bitcoin wiring block, all bitcoin domain services.

---

## Overview

`app.Deps` is the dependency container passed from `server.go` to all domain
`Routes()` functions. Three new fields are needed for the bitcoin domain. One
constructor-time invariant must be enforced.

---

## New Fields

Add to `deps.go` after the last existing field:

```go
// BitcoinZMQ is the ZMQ subscriber for hashblock and hashtx events.
// Nil when BitcoinEnabled is false.
BitcoinZMQ *zmq.Subscriber

// BitcoinRPC is the Bitcoin Core JSON-RPC client.
// Nil when BitcoinEnabled is false.
BitcoinRPC *rpc.Client

// BitcoinNetwork is the active network ("testnet4" or "mainnet").
// Empty string when BitcoinEnabled is false.
BitcoinNetwork string

// BitcoinRedis is the raw *redis.Client used for bitcoin-specific raw ops:
// Lua cap script, Lua JTI script, SADD/SREM/SCARD/SSCAN on watch address sets.
//
// Type is *redis.Client (not *kvstore.RedisStore) because the bitcoin domain
// requires redis.Client.Eval() for address-set operations and JTI consumption,
// which is not exposed on the kvstore interface.
//
// INVARIANT: BitcoinRedis and deps.RedisStore MUST wrap the same underlying
// Redis connection. This is enforced in the Deps constructor — see below.
// Nil when BitcoinEnabled is false.
BitcoinRedis *redis.Client
```

---

## Constructor Invariant

Enforce in the `Deps` constructor (or `NewDeps` function, wherever `deps.go`
initialises the struct) immediately after wiring both fields:

```go
if deps.BitcoinEnabled {
    if deps.RedisStore.Client() != deps.BitcoinRedis {
        panic("app.Deps: RedisStore and BitcoinRedis must wrap the same *redis.Client. " +
            "Create BitcoinRedis from the same go-redis client instance used by RedisStore.")
    }
}
```

This prevents a class of silent bugs where `deps.RedisStore` and `deps.BitcoinRedis`
point to different Redis connections (e.g. different pool sizes, different database
indices, or different servers). The bitcoin domain intermixes operations on both —
a split connection would cause watch-set Lua operations to land on a different Redis
instance than the TTL goroutine's `RefreshTTL` calls, silently causing watch-set keys
to expire without emitting `stream_requires_reregistration`.

---

## Usage in NewService

```go
// watch/service.go — NewService wiring
svc.kvStore      = deps.KVStore              // kvstore.Store — for rate limiters ONLY
svc.counterStore = deps.RedisStore           // kvstore.AtomicCounterStore — global watch count
svc.listStore    = deps.RedisStore           // kvstore.ListStore — settlement overflow
svc.pubsubStore  = deps.RedisStore           // kvstore.PubSubStore — cache invalidation
svc.connCounter  = ratelimit.NewConnectionCounter(
                       deps.RedisStore,
                       ratelimit.DefaultBTCSSEConnKeyPrefix,
                       cfg.BitcoinMaxSSEPerUser,
                       2*time.Hour)
svc.redisClient  = deps.BitcoinRedis         // *redis.Client — raw watch-set Lua + JTI Lua
```

**Key rule:** `deps.KVStore` is used exclusively for rate limiter buckets. All other
bitcoin Redis operations go through the typed interfaces cast from `deps.RedisStore`,
or through `deps.BitcoinRedis` for operations that require `*redis.Client.Eval`.

---

## No Test Inventory

The `deps.go` changes are covered by existing server startup tests and by the
constructor invariant panic (which is tested implicitly by any test that constructs
a `Deps` with mismatched clients). No new test file is needed for this change alone.
