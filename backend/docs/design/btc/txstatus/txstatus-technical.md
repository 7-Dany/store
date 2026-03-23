# Txstatus — Technical Implementation

> **What this file is:** Implementation details for `GET /api/v1/bitcoin/tx/{txid}/status`
> and `GET /api/v1/bitcoin/tx/status?ids=...`. Covers guard sequences, RPC wiring,
> validation, and the complete test inventory for this feature.
>
> **Read first:** `txstatus-feature.md` — behavioral contract and edge cases.

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

Both endpoints use `rpc.Client.GetTransaction` (Bitcoin Core's `gettransaction` RPC).
This is a **wallet-native method** — no `txindex=1` required. It covers any transaction
the platform wallet has ever sent or received, which is the complete set of
transactions that could ever be associated with an invoice payment address.

### Single txid

```go
func (s *Service) GetTxStatus(ctx context.Context, txid string) (TxStatusResult, error) {
    tx, err := s.rpc.GetTransaction(ctx, txid, false)
    if err != nil {
        // rpc.IsNotFoundError: Bitcoin Core code -5
        // "No such wallet transaction" — normal absent response.
        if rpc.IsNotFoundError(err) {
            return TxStatusResult{Status: "not_found"}, nil
        }
        // All other errors: node connectivity problem.
        return TxStatusResult{}, ErrRPCUnavailable
    }

    // rpc.IsConflicting: Confirmations < 0 means the transaction is in a block
    // that is no longer on the active chain (displaced by a reorg).
    // This is distinct from not_found — the tx is known to the wallet but
    // is no longer valid on the current chain.
    if rpc.IsConflicting(tx) {
        return TxStatusResult{Status: "conflicting"}, nil
    }

    switch {
    case tx.Confirmations > 0:
        return TxStatusResult{
            Status:        "confirmed",
            Confirmations: tx.Confirmations,
            BlockHeight:   tx.BlockHeight,
        }, nil
    case tx.Confirmations == 0:
        // Zero confirmations: transaction is in the mempool.
        return TxStatusResult{Status: "mempool"}, nil
    default:
        // Should be unreachable after the IsConflicting check above,
        // but handle defensively.
        return TxStatusResult{Status: "not_found"}, nil
    }
}
```

### Batch txid

```go
func (s *Service) GetTxStatusBatch(ctx context.Context, txids []string) (map[string]TxStatusResult, error) {
    results := make(map[string]TxStatusResult, len(txids))
    for _, txid := range txids {
        res, err := s.GetTxStatus(ctx, txid)
        if err != nil {
            // ErrRPCUnavailable — abort the entire batch.
            return nil, err
        }
        results[txid] = res
    }
    return results, nil
}
```

**Note on batching efficiency:** Each txid makes one `GetTransaction` RPC call.
Because `GetTransaction` is wallet-native (no global index scan), each call is
fast. A future optimization could detect txids sharing the same `BlockHash` from
a first-pass metadata fetch and retrieve each block once at verbosity=1, but
the simple per-txid approach is correct and sufficient for the 20-txid limit.

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

### Single txid — conflicting (reorged)

```json
{
  "status": "conflicting"
}
```

### Batch (200 — always includes all requested txids)

```json
{
  "statuses": {
    "abc123...": {"status": "confirmed", "confirmations": 3, "block_height": 126378},
    "def456...": {"status": "mempool"},
    "ghi789...": {"status": "not_found"},
    "jkl012...": {"status": "conflicting"}
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
| 503 | `service_unavailable` | RPC node unavailable (non-404 error from GetTransaction) |

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

No other Redis state is used. All data comes directly from `GetTransaction` RPC calls.

---

## §8 — Test Inventory

### Unit tests (no external deps)

| ID | Test | Notes |
|---|---|---|
| T-168 | `TestConfig_RPCPort_Numeric_ValidatedAtConfigTime` | BTC_RPC_PORT="not-a-port" → config.validate() returns error |

### Integration tests (require mock RPC)

| ID | Test | Notes |
|---|---|---|
| T-169 | `TestBatchTxStatus_ValidIds_Returns200` | GET /tx/status?ids=txid1,txid2 → 200 with status map |
| T-170 | `TestBatchTxStatus_TooManyIds_Returns400` | 21 txids → 400 too_many_ids |
| T-171 | `TestBatchTxStatus_InvalidHex_Returns400` | one non-hex id in batch → 400 invalid_txid |

### Additional tests

| Test | Notes |
|---|---|
| `TestSingleTxStatus_Confirmed_Returns200` | Mock GetTransaction returns Confirmations=3 |
| `TestSingleTxStatus_Mempool_Returns200` | Mock returns Confirmations=0, BlockHash="" |
| `TestSingleTxStatus_NotFound_Returns200` | Mock returns IsNotFoundError (code -5) → status=not_found |
| `TestSingleTxStatus_Conflicting_Returns200` | Mock returns Confirmations=-1 → status=conflicting |
| `TestSingleTxStatus_InvalidHex_Returns400` | Non-hex txid |
| `TestSingleTxStatus_TooShort_Returns400` | 63-char hex |
| `TestSingleTxStatus_TooLong_Returns400` | 65-char hex |
| `TestSingleTxStatus_RPCDown_Returns503` | Non-404 RPC error → 503 |
| `TestSingleTxStatus_MissingAuth_Returns401` | No JWT |
| `TestBatchTxStatus_MissingIdsParam_Returns400` | ?ids omitted |
| `TestBatchTxStatus_EmptyIdsParam_Returns400` | ?ids= (empty value) |
| `TestBatchTxStatus_AllNotFound_Returns200` | All txids unknown → all not_found |
| `TestBatchTxStatus_MixedStatuses_Returns200` | confirmed + mempool + not_found + conflicting |
| `TestBatchTxStatus_RPCDown_Returns503` | First GetTransaction fails → abort entire batch |
| `TestBatchTxStatus_SameTxidTwice_BothResolved` | Duplicate in batch — each resolved independently |
| `TestBatchTxStatus_RateLimit_Returns429` | |
| `TestGetTxStatus_IsConflicting_NegativeConfirmations` | Confirmations=-2 → conflicting, not not_found |
| `TestGetTxStatus_IsNotFoundError_CodeMinus5` | RPCError{Code:-5} → not_found, not 503 |
| `TestGetTxStatus_OtherRPCError_Returns503` | RPCError{Code:-8} → ErrRPCUnavailable |
