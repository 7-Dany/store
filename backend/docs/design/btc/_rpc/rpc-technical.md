# RPC Client — Technical Implementation

> **What this file is:** Implementation details for `internal/platform/bitcoin/rpc/`.
> Covers constructor, every method signature, all types, BtcToSat safety,
> node setup (bitcoin.conf, startup checks, version requirements), and the
> complete test inventory for this package.
>
> **Read first:** `rpc-feature.md` — behavioral contract and edge cases.

---

## Table of Contents

1. [Package Location & Rules](#1--package-location--rules)
2. [Constructor](#2--constructor)
3. [Method Signatures](#3--method-signatures)
4. [Types](#4--types)
5. [BtcToSat — Precision Safety](#5--btctosat--precision-safety)
6. [Node Setup — bitcoin.conf](#6--node-setup--bitcoinconf)
7. [Startup Checks (server.go)](#7--startup-checks-servergo)
8. [Minimum Version Requirements](#8--minimum-version-requirements)
9. [app.Deps Wiring](#9--appdeps-wiring)
10. [Test Inventory](#10--test-inventory)

---

## §1 — Package Location & Rules

```
internal/platform/bitcoin/rpc/
├── client.go    # Client struct, New(), all RPC methods, BtcToSat
└── types.go     # BlockHeader, BlockchainInfo, DecodedTx, TxVout, btcRawAmount
```

**Hard rule:** zero domain imports. This package must compile without any import
from `internal/domain/`.

**CI rules enforced on the entire bitcoin package tree:**
- No `*1e8` anywhere — use `BtcToSat()`.
- No `*100000000` anywhere — use `BtcToSat()`.
- No direct `hex.EncodeToString(event.Hash[:])` on ZMQ event hashes — use `HashHex()`.

---

## §2 — Constructor

```go
// New creates a new RPC client.
// host: IP or hostname of the Bitcoin Core node (e.g. "127.0.0.1").
// port: string representation of the RPC port; validated as numeric 1–65535.
//       Testnet4 default: 48332. Mainnet default: 8332.
//       HIGH #6 fix: port is validated at config.validate() time, not at first use.
// user, pass: HTTP Basic Auth credentials for bitcoin.conf rpcuser/rpcpassword.
//             Stored in an unexported credential type; never appear in logs.
func New(host, port, user, pass string) (*Client, error)
```

Port validation is also performed at `config.validate()` time (before any
bitcoin domain wiring occurs) so misconfigured ports fail early with a clear error,
not at first RPC call with an opaque connection refused message.

---

## §3 — Method Signatures

```go
// GetRawTransaction fetches a transaction from the node.
// verbose=false: returns raw hex. verbose=true: returns JSON with in_active_chain,
// confirmations, blockheight, etc.
// Requires txindex=1 for confirmed transactions. Without txindex, confirmed txids
// return error code -5 "No such mempool or blockchain transaction".
func (c *Client) GetRawTransaction(ctx context.Context, txid string, verbose bool) (json.RawMessage, error)

// DecodeRawTransaction decodes a raw hex transaction into structured output.
// Returns DecodedTx with Vout.Value typed as btcRawAmount (callers must use BtcToSat).
func (c *Client) DecodeRawTransaction(ctx context.Context, hexTx string) (DecodedTx, error)

// GetBlockHeader returns lightweight block metadata: height, hash, time.
// Used by the events domain's BlockEvent handler to get block height for new_block events.
// Does NOT require txindex.
func (c *Client) GetBlockHeader(ctx context.Context, hash string) (BlockHeader, error)

// GetBlock fetches block data at the specified verbosity level.
//   verbosity=1: block metadata + list of txids. Used by events domain for confirmed_tx.
//   verbosity=2: full transaction data for all txs. Used by settlement reconciliation.
// Does NOT require txindex.
func (c *Client) GetBlock(ctx context.Context, hash string, verbosity int) (json.RawMessage, error)

// GetBlockchainInfo returns node chain info: chain name, best block hash,
// block count, pruneheight (if pruned), txindex status.
// Used at startup to verify chain matches BTC_NETWORK, and by the liveness goroutine.
func (c *Client) GetBlockchainInfo(ctx context.Context) (BlockchainInfo, error)

// GetBlockHash returns the block hash at the given height on the active chain.
// Used by the settlement reconciliation loop and reorg rollback common-ancestor walk.
func (c *Client) GetBlockHash(ctx context.Context, height int) (string, error)

// GetBlockCount returns the current height of the active chain tip.
// Used by the settlement reconciliation loop to determine how many blocks to scan.
func (c *Client) GetBlockCount(ctx context.Context) (int, error)
```

---

## §4 — Types

```go
// btcRawAmount is an unexported named alias for float64.
// JSON decoder writes Bitcoin Core's floating-point BTC values into this type.
// External callers receive btcRawAmount from TxVout.Value and must call BtcToSat().
// They cannot construct btcRawAmount directly — it is unexported.
// This makes it a compile-time error to use a raw float64 where BTC is expected.
type btcRawAmount float64

// BlockHeader is the lightweight block metadata returned by getblockheader.
type BlockHeader struct {
    Height int32  `json:"height"`
    Hash   string `json:"hash"`
    Time   int64  `json:"time"`
}

// BlockchainInfo is the response from getblockchaininfo.
type BlockchainInfo struct {
    Chain         string `json:"chain"`          // "main", "test4", etc.
    Blocks        int    `json:"blocks"`          // current chain height
    BestBlockHash string `json:"bestblockhash"`   // current tip hash (big-endian)
    Pruned        bool   `json:"pruned"`
    PruneHeight   int    `json:"pruneheight"`     // only present when Pruned=true
}

// DecodedTx is the response from decoderawtransaction.
// M-12/OD-05 fix: Vout.Value is typed as btcRawAmount, not float64.
// External callers receive btcRawAmount and must call BtcToSat() — they cannot
// bypass the type protection by casting float64.
type DecodedTx struct {
    Txid string   `json:"txid"`
    Vout []TxVout `json:"vout"`
}

type TxVout struct {
    Value        btcRawAmount `json:"value"`       // MUST use BtcToSat() — never cast directly
    N            int          `json:"n"`           // vout index (0-based)
    ScriptPubKey struct {
        Address string `json:"address"`            // normalised to lowercase by Bitcoin Core
        Type    string `json:"type"`               // "pubkeyhash", "scripthash", "witness_v0_keyhash",
                                                   // "witness_v0_scripthash", "witness_v1_taproot"
    } `json:"scriptPubKey"`
}
```

---

## §5 — BtcToSat — Precision Safety

```go
// BtcToSat converts a btcRawAmount to satoshis safely using math.Round.
//
// WHY NOT float64 * 1e8:
// IEEE 754 double precision cannot represent most decimal fractions exactly.
// 0.1 * 1e8 = 9999999.999999776 → int64 truncation → 9999999 (wrong by 1 sat).
// math.Round(0.1 * 1e8) = 10000000 (correct).
//
// WHY math.Round AND NOT math.Floor or int64():
// int64(9999999.999999776) = 9999999 (truncates toward zero — wrong).
// math.Floor(9999999.999999776) = 9999999 (same — wrong).
// math.Round(9999999.999999776) = 10000000 (correct).
//
// OVERFLOW CHECK: max Bitcoin supply = 21,000,000 BTC = 2,100,000,000,000,000 sat.
// int64 max = 9,223,372,036,854,775,807. No overflow possible for valid BTC values.
// We still check for negative amounts (invalid RPC response) and return error.
func BtcToSat(btc btcRawAmount) (int64, error) {
    if btc < 0 {
        return 0, fmt.Errorf("BtcToSat: negative amount %v", btc)
    }
    sat := math.Round(float64(btc) * 1e8)
    if sat > math.MaxInt64 {
        return 0, fmt.Errorf("BtcToSat: overflow for amount %v", btc)
    }
    return int64(sat), nil
}
```

---

## §6 — Node Setup — bitcoin.conf

```ini
[testnet4]
zmqpubhashblock=tcp://127.0.0.1:28332
zmqpubhashblockhwm=5000
zmqpubhashtx=tcp://127.0.0.1:28333
zmqpubhashtxhwm=5000
# RPC server (for this app):
rpcuser=<your_rpc_user>
rpcpassword=<your_rpc_password>
rpcbind=127.0.0.1
rpcallowip=127.0.0.1
# Required for settlement — confirmed tx lookups fail without txindex.
txindex=1

# M-14 fix: if running BOTH mainnet and testnet4 on the same node,
# testnet4 MUST use different ZMQ ports to avoid a port conflict.
# Each network's ZMQ feed would otherwise be silently mixed.
#
# Dual-network example:
#   [testnet4]
#   zmqpubhashblock=tcp://127.0.0.1:38332
#   zmqpubhashtx=tcp://127.0.0.1:38333
#   [main]
#   zmqpubhashblock=tcp://127.0.0.1:28332
#   zmqpubhashtx=tcp://127.0.0.1:28333
#
# Set BTC_ZMQ_BLOCK and BTC_ZMQ_TX to the correct ports for the active network.

[main]
zmqpubhashblock=tcp://127.0.0.1:28332
zmqpubhashblockhwm=5000
zmqpubhashtx=tcp://127.0.0.1:28333
zmqpubhashtxhwm=5000
rpcuser=<your_rpc_user>
rpcpassword=<your_rpc_password>
rpcbind=127.0.0.1
rpcallowip=127.0.0.1
txindex=1
```

**HWM note:** both publisher (bitcoin.conf) and subscriber (application) HWM must
be 5000. Bitcoin Core defaults to 1000. Effective queue depth =
`min(publisher_hwm, subscriber_hwm)`. If publisher HWM is 1000 and subscriber is
5000, the effective depth is 1000 — the publisher drops messages before the
subscriber can buffer them.

Verify ZMQ is active after node restart:
```bash
bitcoin-cli -testnet4 getzmqnotifications
```

**Security:** The RPC port must not be exposed to external networks. `rpcbind=127.0.0.1`
and `rpcallowip=127.0.0.1` restrict access to loopback only. ZMQ likewise has no
authentication — enforced by `requireZMQEndpoint` in the application.

---

## §7 — Startup Checks (server.go)

These checks run inside the Bitcoin wiring block in `server.go` when
`cfg.BitcoinEnabled == true`. They panic or log-error (never silently ignore).

```go
// 1. Verify RPC connectivity and chain match.
info, err := rpc.GetBlockchainInfo(ctx)
if err != nil {
    panic("bitcoin: GetBlockchainInfo failed at startup — " +
        "check BTC_RPC_HOST, BTC_RPC_PORT, BTC_RPC_USER, BTC_RPC_PASS")
}
if info.Chain != cfg.BitcoinNetwork {
    panic(fmt.Sprintf(
        "bitcoin: node reports chain=%q but BTC_NETWORK=%q — wrong node or wrong network config",
        info.Chain, cfg.BitcoinNetwork))
}

// 2. Verify txindex is enabled.
// Strategy: fetch genesis block hash, get full block at verbosity=2,
// take the coinbase txid, call GetRawTransaction. If txindex is disabled,
// Bitcoin Core returns -5 for the coinbase txid of a confirmed block.
genesisHash, err := rpc.GetBlockHash(ctx, 0)
if err != nil {
    log.Error().Err(err).Msg("bitcoin: could not fetch genesis hash for txindex check")
} else {
    genesisBlock, err := rpc.GetBlock(ctx, genesisHash, 2)
    // ... extract first txid from coinbase ...
    if _, err := rpc.GetRawTransaction(ctx, coinbaseTxid, false); err != nil {
        if isTxNotFoundError(err) {
            log.Error().Msg(
                "bitcoin: txindex is DISABLED on this node. " +
                "Confirmed transaction lookups will return not_found. " +
                "Settlement processing will be permanently stalled. " +
                "Add txindex=1 to bitcoin.conf and reindex (bitcoin-cli reindex).")
        }
    }
}
```

**What these checks catch:**
- Wrong `BTC_RPC_PORT` (testnet4 uses 48332, mainnet uses 8332 by default).
- Node running the wrong network (mainnet node with testnet4 config or vice versa).
- `txindex=1` missing from `bitcoin.conf` — the most common operational error.

---

## §8 — Minimum Version Requirements

| Component | Minimum Version | Reason |
|---|---|---|
| Bitcoin Core | **27.0** (Oct 2024) | Required for testnet4 network support. Mainnet-only: 0.21+ sufficient. Earlier versions start but report wrong chain via `getblockchaininfo`. |
| Redis | **6.0+** | Required for `SCAN TYPE` command in `reconcileGlobalWatchCount` (watch domain). Redis 5.x returns an error on the first reconciliation tick — logged but not fatal. |

---

## §9 — app.Deps Wiring

```go
// app.Deps additions:
BitcoinRPC     *rpc.Client
BitcoinNetwork string

// BitcoinRedis is the raw *redis.Client for bitcoin-specific raw ops
// (Lua cap script, Lua JTI script, SADD/SCARD/SSCAN).
// Type: *redis.Client, NOT *kvstore.RedisStore — domain requires redis.Client.Eval()
// which is not on the kvstore interface.
//
// INVARIANT: BitcoinRedis and deps.RedisStore MUST wrap the same underlying
// Redis connection. Enforce in the Deps constructor:
//   if deps.RedisStore.Client() != deps.BitcoinRedis {
//       panic("Deps: RedisStore and BitcoinRedis must share the same *redis.Client")
//   }
BitcoinRedis   *redis.Client
```

---

## §10 — Test Inventory

### Unit tests (no external deps)

| ID | Test | Notes |
|---|---|---|
| T-08 | `TestBtcToSat_Precision_PointOneBTC` | 0.1 BTC → 10000000 sat (not 9999999) |
| T-09 | `TestBtcToSat_MaxSatoshi` | 21000000 BTC → 2100000000000000 sat, no overflow |
| T-10 | `TestBtcToSat_Negative_ReturnsError` | negative btcRawAmount → error |
| T-121 | `TestBlockEvent_HashHex_IsReversed` | ZMQ raw bytes → HashHex() == known RPC big-endian hash |
| T-122 | `TestTxEvent_HashHex_IsReversed` | same for TxEvent |
| T-168 | `TestConfig_RPCPort_Numeric_ValidatedAtConfigTime` | BTC_RPC_PORT="not-a-port" → config.validate() error before any wiring |

### Integration tests (require live or mock Bitcoin Core)

| Test | Notes |
|---|---|
| `TestRPC_GetBlockchainInfo_MainnetChain` | info.Chain == "main" |
| `TestRPC_GetBlockchainInfo_Testnet4Chain` | info.Chain == "test4" |
| `TestRPC_GetBlockHash_Height0_ReturnsGenesisHash` | known genesis hash per network |
| `TestRPC_GetBlock_Verbosity1_ContainsTxids` | verbosity=1 response has txids array |
| `TestRPC_GetBlock_Verbosity2_ContainsVouts` | verbosity=2 response has full tx data |
| `TestRPC_GetRawTransaction_MemPoolTx_Mempool` | in_active_chain absent for mempool tx |
| `TestRPC_GetRawTransaction_ConfirmedTx_ActiveChain` | in_active_chain=true, confirmations>0 |
| `TestRPC_GetRawTransaction_UnknownTxid_Error` | -5 error from Bitcoin Core |
| `TestRPC_CredentialsNeverInLogs` | log output for any RPC error contains "[redacted]" not raw password |
| `TestRPC_ContextCancellation_StopsRequest` | context.WithTimeout cancels in-flight HTTP |

### Startup validation tests (RPC-related)

| ID | Test | Notes |
|---|---|---|
| T-80 | `TestStartup_RPCPortInvalid_Rejected` | BTC_RPC_PORT non-numeric → error |
| T-93 | `TestStartup_NetworkInvalid` | BTC_NETWORK not "mainnet" or "testnet4" → error |

### Tests forwarded from zmq-technical.md

These tests were originally specified in `zmq-technical.md` but require the RPC
client. Implement them here when this package is built.

| ID | Test | Notes |
|---|---|---|
| T-47 | `TestSubscriber_RPCFailureDoesNotFlipIsConnected` | RPC failure must not affect `IsConnected()` — ZMQ liveness is based on ZMQ messages only, not RPC responses |
| T-48 | `TestSubscriber_404SkippedGracefully` | unknown txid from RPC (“No such transaction”) → no panic, no metric increment, log at WARN only |
