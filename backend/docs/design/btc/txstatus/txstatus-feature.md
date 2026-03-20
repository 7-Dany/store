# Txstatus Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of what the transaction status
> endpoints do, every rule they enforce, and every edge case they handle. Read this
> to understand the feature contract before looking at any implementation detail.
>
> **Companion:** `txstatus-technical.md` — guard sequences, RPC wiring, test inventory.
> **Source of truth for HTTP contract:** `../1-zmq-system.md §2`.

---

## What this feature does

Two endpoints allow clients to query the current on-chain status of Bitcoin
transactions. These endpoints are the reconciliation mechanism for the SSE stream —
because the stream is best-effort and stateless, clients use txstatus to fill in
any gaps after a disconnection.

- `GET /api/v1/bitcoin/tx/{txid}/status` — query a single transaction.
- `GET /api/v1/bitcoin/tx/status?ids={txid},{txid},...` — query up to 20 transactions
  in a single request.

---

## Display-only contract

These endpoints are **display-only**. They report what the Bitcoin node knows about
a transaction at the moment of the query. They do NOT trigger any invoice status
change, do NOT advance any settlement state machine, and must never be used as the
basis for financial decisions.

The authoritative source for invoice payment status is the invoice endpoint, not
the txstatus endpoint.

---

## What the status values mean

**`confirmed`** — the transaction is in a block that is part of the active chain.
The response includes `confirmations` (how many blocks have been built on top of
the block containing the transaction) and `block_height` (the height of the block
containing the transaction). Both values are correct at the moment of the query
but may become stale if a reorg occurs between the query and when the client
uses the data.

**`mempool`** — the transaction is known to the node but has not yet been included
in a block. No confirmation count is included because there are no confirmations.

**`not_found`** — the node has no record of this transaction. This covers four
distinct scenarios that the client cannot distinguish from each other:
1. The transaction was never broadcast to the network.
2. The transaction was replaced by an RBF transaction and dropped from the mempool.
3. The transaction was in the mempool but dropped due to low fees or mempool pressure.
4. The transaction was in a block but that block was later reorged out, and the
   transaction is not currently in another block or in the mempool.

Clients that need to distinguish these cases should combine the txstatus result with
the SSE stream's `mempool_tx_replaced` event (for case 2) and monitor block
confirmations (for case 4).

---

## The txindex requirement

Both endpoints require `txindex=1` in `bitcoin.conf` on the Bitcoin Core node. Without
txindex, `GetRawTransaction` only finds transactions that are currently in the mempool
— confirmed transactions return `not_found`. This means a transaction that was
`mempool` on one query would become `not_found` after confirmation, which is the
opposite of the expected behavior.

The startup check validates that txindex is active by attempting to fetch the genesis
block's coinbase transaction. If txindex is disabled, a warning is logged but the
service starts — the warning makes the failure mode detectable.

---

## Single txid endpoint

The simplest case: query one transaction and get its current status. The txid must
be a valid 64-character lowercase hex string. Any other format returns
`400 invalid_txid`. The validation runs before the RPC call.

If the Bitcoin node is unavailable, the endpoint returns `503 service_unavailable`.
There is no caching — every request triggers a fresh RPC call.

---

## Batch endpoint

The batch endpoint accepts up to 20 comma-separated txids as a query parameter.
It returns a JSON object mapping each txid to its status. The server resolves all
txids against a single `getblock` RPC call, making the batch form substantially
more efficient than looping the single-txid endpoint from the client side.

The response always contains an entry for every txid in the request — there is no
concept of a partial response. If the node cannot be reached, the entire request
fails with 503. If any single txid in the batch is malformed, the entire request
fails with `400 invalid_txid`.

The 20-txid limit balances efficiency against the risk of a single request
monopolizing the RPC client.

---

## Post-reconnect reconciliation use case

The canonical use case for these endpoints is client-side reconciliation after an
SSE reconnect:

1. Client tracks every txid for which it received `mempool_tx` but not yet
   `confirmed_tx`.
2. SSE connection drops.
3. Client reconnects, re-registers addresses with POST /watch.
4. Client calls `GET /tx/status?ids={all tracked pending txids}` to find out which
   ones confirmed during the disconnection.
5. For each confirmed txid, the client treats it as if it had received `confirmed_tx`
   on the stream.

The batch endpoint is designed specifically for this step — after a 60-second
disconnection there could be several pending transactions to reconcile, and a single
batch call is far more efficient than separate requests.

---

## Relationship to settlement

The txstatus endpoints are intentionally decoupled from settlement. The settlement
engine derives confirmation status independently via RPC-based block scanning with
its own DB cursor. The txstatus endpoints do not read from or write to any settlement
state. This isolation ensures that a bug in the display-side status reporting cannot
affect payment processing.

---

## Rate limiting

Both endpoints share an IP-based rate limiter: 20 requests per minute, burst of 20.
This is a higher limit than the watch and token endpoints because the txstatus
endpoints are the reconciliation path — clients that frequently lose and regain
connectivity may need to reconcile many times per minute.

---

## Edge cases worth noting

**Confirmed transaction with 0 confirmations:** Immediately after a transaction is
included in a block but before the next block is mined, `confirmations` will be 1
(the block itself counts as the first confirmation in Bitcoin's model). The
`block_height` field will be set correctly.

**Very long txids in the batch query string:** The URL length limit of browsers and
proxies (~8KB) limits the practical batch size well below 20 for most clients. Clients
constructing batch URLs with many long txids should be aware of this.

**Same txid submitted multiple times in a batch:** The server does not deduplicate.
Each occurrence is resolved independently. The RPC is efficient enough that this is
not a meaningful concern, but clients should deduplicate for clarity.
