# RPC Client — Behavior & Edge Cases

> **What this file is:** Plain-language description of what the Bitcoin RPC client
> does, what it guarantees, and every edge case it handles. Read this to understand
> the platform contract before touching any implementation.
>
> **Package:** `internal/platform/bitcoin/rpc/`
> **Rule:** no domain imports — pure platform.
> **Companion:** `rpc-technical.md` — constructor, method signatures, types,
> BtcToSat safety, node setup, startup checks, test inventory.

---

## What this package does

The RPC client is a thin JSON-RPC wrapper around the Bitcoin Core HTTP API. It
translates Go method calls into HTTP POST requests to Bitcoin Core's RPC endpoint
and parses the JSON responses into typed Go structs.

Every domain package that needs Bitcoin network data — address decoding, block
scanning, transaction status, settlement reconciliation — calls through this client.
The client itself has no opinion about what the caller does with the data. It is
purely a transport and decoding layer.

---

## Why this exists as a separate package

Bitcoin Core's RPC is JSON-based but has several footguns that benefit from being
handled once at the platform layer rather than repeated in every caller:

- **Credential safety:** RPC credentials must never appear in logs. The client
  wraps them in a type that implements `fmt.Stringer` as `"[redacted]"`, making
  accidental logging impossible.
- **BTC-to-satoshi precision:** Bitcoin amounts in RPC responses are `float64`
  values representing BTC. Converting them to satoshis via `float64 * 1e8` is a
  precision error — floating-point arithmetic on monetary values causes rounding
  bugs. The client uses an unexported `btcRawAmount` type that forces all callers
  to use the `BtcToSat()` function, which handles the conversion safely.
- **Port validation:** the RPC port must be a valid integer in 1–65535. Validating
  this once at construction prevents a class of misconfiguration bugs.

---

## Credential safety

The `user` and `pass` arguments to `New()` are stored inside the client struct
but never returned, logged, or included in any error message. The credential type
implements `fmt.Stringer` as `"[redacted]"` so that even accidentally printing the
client struct in a log line will not expose credentials.

Any code that attempts to read the raw credential string after construction will
fail to compile — the field is unexported and the type provides no accessor.

---

## BTC-to-satoshi conversion safety

Bitcoin Core returns transaction output values as floating-point BTC amounts, for
example `0.00005000` for 5000 satoshis. The naive conversion `int64(value * 1e8)`
is incorrect because floating-point arithmetic cannot represent most decimal
fractions exactly. `0.1 * 1e8` in IEEE 754 double precision is `9999999.999999776`,
which truncates to `9999999` — off by one satoshi.

The client addresses this by:

1. Defining `btcRawAmount` as an unexported named alias for `float64`. JSON
   unmarshaling writes directly into this type.
2. Exposing only `BtcToSat(btc btcRawAmount) (int64, error)` for conversion.
   This function uses `math.Round` and checks for overflow, not simple truncation.
3. Making it impossible for callers to construct a `btcRawAmount` directly —
   they receive it from `TxVout.Value` and can only pass it to `BtcToSat`.

CI grep rules ban `*1e8` and `*100000000` anywhere in the bitcoin package tree.
Any attempt to bypass `BtcToSat` fails the CI check.

---

## Context and cancellation

Every method accepts a `context.Context` as its first argument. The underlying
HTTP request is cancelled when the context is cancelled. This is how the
`BTC_BLOCK_RPC_TIMEOUT_SECONDS` per-call deadline is enforced — callers create
a `context.WithTimeout` before calling any RPC method, and the HTTP transport
honours context cancellation.

Callers inside the events domain (specifically the BlockEvent handler, which calls
both `GetBlockHeader` and `GetBlock`) create independent per-call timeouts. Both
calls must complete within `BTC_HANDLER_TIMEOUT_MS` total, but each has its own
deadline so a slow `GetBlockHeader` does not silently consume the time budget for
`GetBlock`.

---

## txindex requirement

`GetRawTransaction` with `verbose=true` is used by both the txstatus feature
(display path) and the settlement engine (settlement path). For confirmed
transactions, this call requires `txindex=1` in `bitcoin.conf`. Without txindex,
Bitcoin Core can only find transactions that are currently in the mempool —
confirmed transactions return a "No such mempool or blockchain transaction" error,
which the txstatus feature maps to `not_found`.

This is a node configuration requirement, not a code requirement. The startup check
detects and warns when txindex is disabled, but does not prevent the server from
starting. The consequence of missing txindex is that:
- `GET /tx/{txid}/status` returns `not_found` for all confirmed transactions.
- Settlement's `checkTxStatus` returns `ErrTxRBFOrDropped` for confirmed
  transactions, which permanently stalls settlement processing for those invoices.

---

## GetBlock verbosity levels

`GetBlock` accepts a `verbosity` integer that controls response detail:

- `verbosity=0` — returns the raw hex-encoded block. Not used.
- `verbosity=1` — returns a JSON object with block metadata and a list of txids.
  Used by the events domain to check whether a pending mempool transaction was
  included in a block (`confirmed_tx` generation).
- `verbosity=2` — returns full transaction data for every transaction in the block.
  Used by the settlement engine's reconciliation loop to scan all outputs.

Callers must pass the correct verbosity for their use case. Passing verbosity=2
when verbosity=1 suffices wastes significant bandwidth and parsing time on busy
mainnet blocks.

---

## Error handling contract

All methods return `(result, error)`. The error wraps the underlying HTTP or
JSON parsing error. Callers are responsible for interpreting Bitcoin Core-specific
error codes. Two important cases:

- Error code `-5` with message containing "No such mempool or blockchain
  transaction" means the txid is unknown to the node (not in mempool, not in any
  indexed block, or txindex is disabled). The txstatus service maps this to
  `status: "not_found"`.
- Error containing "Block not available" or "pruned data" means the block was
  pruned from the node's local storage. The settlement reconciliation loop detects
  this via `isPrunedBlockError()` and skips the block with a warning.

The client itself does not interpret these errors — it returns them as-is wrapped
in a Go `error`. The domain layer is responsible for the interpretation.

---

## What this package does NOT do

- It does not cache responses. Every call hits Bitcoin Core.
- It does not retry on failure. Retry logic, if needed, belongs in the caller.
- It does not manage connection pooling — each RPC call creates an HTTP request.
  HTTP keep-alive is handled by the underlying `http.Client`.
- It does not support batch RPC calls (sending multiple methods in one HTTP
  request). Bitcoin Core supports this, but this client does not implement it.
- It does not validate txids or block hashes before sending them to Bitcoin Core.
  Invalid inputs produce a Bitcoin Core error, which is returned as a Go error.
- It does not know about watched addresses, invoices, or users.
