# Txstatus — Technical Implementation

> **What this file is:** Implementation details for `GET /api/v1/bitcoin/tx/{txid}/status`
> and `GET /api/v1/bitcoin/tx/status?ids=...`. Covers guard sequences, RPC wiring,
> validation, and the complete test inventory for this feature.
>
> **Read first:** `txstatus-feature.md` — behavioral contract and edge cases.
> **Shared platform details:** `../btc-shared.md` — RPC client, app.Deps wiring.

---

## Table of Contents

1. [Handler Guard Ordering — Single Txid](#1--handler-guard-ordering--single-txid)
2. [Handler Guard Ordering — Batch](#2--handler-guard-ordering--batch)
3. [RPC Resolution Logic](#3--rpc-resolution-logic)
4. [Response Shapes](#4--response-shapes)
5. [Error Responses](#5--error-responses)
6. [Rate Limiter](#6--rate-limiter)
7. [Redis Key Reference](#7--redis-key-reference)
8. [Test Inventory](#8--test-inventory)

---

## §1 — Handler Guard Ordering — Single Txid

`GET /api/v1/bitcoin/tx/{txid}/status`

```
1. Auth           token.UserIDFromContext → 401
2. Rate limit     txstatusLimiter.Limit middleware (20 req/min, burst 20)
3. Validate       txid = chi.URLParam(r, "txid")
                  len(txid) != 64 || !isHex(txid) → 400 invalid_txid
4. Service        svc.GetTxStatus(ctx, txid)
                    → 503 service_unavailable if RPC node unavailable
                    → TxStatusResult{Status, Confirmations, BlockHeight}
5. Response       respond.JSON(200, singleStatusResponse)
```

---

## §2 — Handler Guard Ordering — Batch

`GET /api/v1/bitcoin/tx/status?ids={txid},{txid},...`

```
1. Auth           token.UserIDFromContext → 401
2. Rate limit     txstatusLimiter.Limit middleware (shared with single-txid endpoint)
3. Validate       raw = r.URL.Query().Get("ids")
                  raw == "" → 400 missing_ids
                  parts = strings.Split(raw, ",")
                  len(parts) > 20 → 400 too_many_ids
                  for each part:
                      len(part) != 64 || !isHex(part) → 400 invalid_txid
4. Service        svc.GetTxStatusBatch(ctx, txids)
                    → 503 service_unavailable if RPC node unavailable
                    → map[string]TxStatusResult
5. Response       respond.JSON(200, batchStatusResponse)
```

---

## §3 — RPC Resolution Logic

### Single txid

```go
func (s *Service) GetTxStatus(ctx context.Context, txid string) (TxStatusResult, error) {
    raw, err := s.rpc.GetRawTransaction(ctx, txid, true)
    if err != nil {
        // Bitcoin Core returns -5 "No such mempool or blockchain transaction"
        // for unknown txids. Map to not_found.
        if isNotFoundError(err) {
            return TxStatusResult{Status: "not_found"}, nil
        }
        return TxStatusResult{}, ErrRPCUnavailable
    }

    var result struct {
        InActiveChain *bool `json:"in_active_chain"`
        Confirmations int   `json:"confirmations"`
        BlockHeight   int   `json:"blockheight"`
    }
    if err := json.Unmarshal(raw, &result); err != nil {
        return TxStatusResult{}, fmt.Errorf("GetTxStatus: unmarshal: %w", err)
    }

    switch {
    case result.InActiveChain != nil && *result.InActiveChain:
        return TxStatusResult{
            Status:        "confirmed",
            Confirmations: result.Confirmations,
            BlockHeight:   result.BlockHeight,
        }, nil
    case result.InActiveChain == nil:
        // InActiveChain is absent for mempool transactions (not yet in a block).
        return TxStatusResult{Status: "mempool"}, nil
    default:
        // InActiveChain present but false: tx in a block that is not on the active chain.
        return TxStatusResult{Status: "not_found"}, nil
    }
}
```

### Batch txid

The batch service call resolves all txids in a single `getblock` RPC pass rather than
calling `GetRawTransaction` once per txid. This makes the batch dramatically more
efficient at scale.

```go
func (s *Service) GetTxStatusBatch(ctx context.Context, txids []string) (map[string]TxStatusResult, error) {
    results := make(map[string]TxStatusResult, len(txids))
    for _, txid := range txids {
        // GetRawTransaction with verbose=true is still the most portable approach.
        // A future optimization could use GetBlock at verbosity=2 to batch multiple
        // lookups in a single call if txids share the same block, but the simple
        // approach is correct and sufficient for the current batch size limit of 20.
        res, err := s.GetTxStatus(ctx, txid)
        if err != nil {
            return nil, err
        }
        results[txid] = res
    }
    return results, nil
}
```

> **Note on "single getblock RPC pass":** The original design note in `1-zmq-system.md`
> describes resolving all txids via a single `getblock` call. This is an optimization
> applicable when all txids in a batch happen to be in the same block. In practice,
> pending-reconciliation txids may span multiple blocks or include mempool transactions,
> making a true single-pass impossible in the general case. The implementation above
> uses individual `GetRawTransaction` calls per txid. If profiling reveals this is a
> bottleneck, consider grouping by block_height (from a first-pass metadata fetch) and
> then fetching each unique block once.

---

## §4 — Response Shapes

### Single txid — confirmed

```json
{
  "status": "confirmed",
  "confirmations": 3,
  "block_height": 126378
}
```

### Single txid — mempool

```json
{
  "status": "mempool"
}
```

### Single txid — not found

```json
{
  "status": "not_found"
}
```

### Batch (200 — always includes all requested txids)

```json
{
  "statuses": {
    "abc123...": {"status": "confirmed", "confirmations": 3, "block_height": 126378},
    "def456...": {"status": "mempool"},
    "ghi789...": {"status": "not_found"}
  }
}
```

---

## §5 — Error Responses

| Status | Code | Condition |
|---|---|---|
| 400 | `invalid_txid` | txid is not a valid 64-character lowercase hex string |
| 400 | `too_many_ids` | `ids` contains more than 20 entries |
| 400 | `missing_ids` | `ids` query parameter is absent or empty |
| 401 | `unauthorized` | Missing or invalid JWT |
| 429 | `rate_limit_exceeded` | IP rate limit hit |
| 503 | `service_unavailable` | RPC node unavailable |

---

## §6 — Rate Limiter

| Limiter | KV prefix | Rate | Burst |
|---|---|---|---|
| `txstatusLimiter` | `btc:txstatus:ip:` | 20 req/min | 20 |

Shared between the single-txid and batch endpoints. The higher limit (vs watch/token)
reflects the reconciliation use case — clients may need to query frequently after
reconnects.

---

## §7 — Redis Key Reference

Only the rate limiter bucket keys are Redis-backed for this feature.

| Key | Type | TTL | Purpose |
|---|---|---|---|
| `btc:txstatus:ip:{ip}` | String | 1 min | Txstatus endpoint IP rate limiter bucket |

No other Redis state is used. All data comes directly from RPC calls to Bitcoin Core.

---

## §8 — Test Inventory

### Unit tests (no external deps)

| ID | Test | Notes |
|---|---|---|
| T-168 | `TestConfig_RPCPort_Numeric_ValidatedAtConfigTime` | BTC_RPC_PORT="not-a-port" → config.validate() returns error before any wiring |

### Integration tests (require RPC / mock RPC)

| ID | Test | Notes |
|---|---|---|
| T-169 | `TestWatch_BatchTxStatus_ValidIds_Returns200` | GET /tx/status?ids=txid1,txid2 → 200 with status map |
| T-170 | `TestWatch_BatchTxStatus_TooManyIds_Returns400` | 21 txids → 400 too_many_ids |
| T-171 | `TestWatch_BatchTxStatus_InvalidHex_Returns400` | one non-hex id in batch → 400 invalid_txid |

### Additional tests (to be added)

The following test cases are implied by the feature contract but not yet in the
original test inventory. Recommended additions:

| Test | Notes |
|---|---|
| `TestSingleTxStatus_ValidTxid_Confirmed_Returns200` | Mock RPC returns confirmed |
| `TestSingleTxStatus_ValidTxid_Mempool_Returns200` | Mock RPC returns mempool |
| `TestSingleTxStatus_ValidTxid_NotFound_Returns200` | RPC returns -5 error → not_found |
| `TestSingleTxStatus_InvalidHex_Returns400` | Non-hex txid |
| `TestSingleTxStatus_TooShort_Returns400` | 63-char hex |
| `TestSingleTxStatus_TooLong_Returns400` | 65-char hex |
| `TestSingleTxStatus_RPCDown_Returns503` | RPC unavailable |
| `TestSingleTxStatus_MissingAuth_Returns401` | No JWT |
| `TestBatchTxStatus_MissingIdsParam_Returns400` | ?ids omitted |
| `TestBatchTxStatus_EmptyIdsParam_Returns400` | ?ids= (empty value) |
| `TestBatchTxStatus_AllNotFound_Returns200` | All txids unknown → all not_found |
| `TestBatchTxStatus_RPCDown_Returns503` | |
| `TestBatchTxStatus_SameTxidTwice_BothResolved` | Duplicate in batch |
| `TestBatchTxStatus_RateLimit_Returns429` | |
