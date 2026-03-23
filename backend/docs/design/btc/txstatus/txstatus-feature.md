# Txstatus Feature — Behavior & Edge Cases

> **What this file is:** A plain-language description of what the transaction status
> endpoints do, every rule they enforce, and every edge case they handle. Read this
> to understand the feature contract before looking at any implementation detail.
>
> **Companion:** `txstatus-technical.md` — guard sequences, RPC wiring, test inventory.

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

## RPC method used

Both endpoints use `GetTransaction` (Bitcoin Core's `gettransaction` RPC). This is
a wallet-native method — it queries the node's wallet transaction index, not the
global `txindex`. This has two important implications:

**No `txindex=1` required.** The platform wallet tracks every transaction it has
ever sent or received. `GetTransaction` covers exactly that set, which is all the
transactions that any invoice payment address could ever receive.

**Only wallet-visible transactions are queryable.** A transaction that the wallet
has never seen (e.g. one that was never broadcast, or paid to an address the wallet
does not control) returns `not_found`. This is the correct behavior for this feature
— it is not a limitation.

---

## What the status values mean

**`confirmed`** — the transaction is in a block on the active chain with at least
one confirmation. The response includes `confirmations` (how many blocks deep) and
`block_height`. Both values are current at the moment of the query but may become
stale if a reorg occurs.

**`mempool`** — the transaction is in the node's mempool (zero confirmations).
No block height is returned.

**`not_found`** — `GetTransaction` returned error code -5. This covers:
1. The transaction was never broadcast to this node.
2. The transaction was replaced via RBF and dropped from the mempool.
3. The transaction was dropped from the mempool due to low fees or pressure.
4. The transaction was in a block that was reorged out, and it is not currently
   in another block or in the mempool.
5. The transaction paid an address not controlled by this wallet.

Clients that need to distinguish these cases should combine txstatus with the SSE
stream's `mempool_tx_replaced` event (for case 2) and monitor block confirmations
(for case 4).

**`conflicting`** — `GetTransaction` returned a negative `Confirmations` value.
This means the transaction is in a block that is no longer on the active chain
(displaced by a reorganisation). This is a distinct status from `not_found` — the
transaction exists in the wallet's history but is no longer on the main chain.
The settlement engine treats conflicting transactions as reorg events. The frontend
should display these as failed/reorged and prompt the user to check the current
chain state.

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
It returns a JSON object mapping each txid to its status. Each txid is resolved
via an independent `GetTransaction` call. The response always contains an entry
for every txid in the request.

If the node cannot be reached, the entire request fails with 503. If any single
txid in the batch is malformed, the entire request fails with `400 invalid_txid`.

The 20-txid limit balances efficiency against the risk of a single request
monopolizing the RPC client. Each call is individually retried on transient
network errors per the RPC client's retry policy.

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
6. For each `conflicting` txid, the client marks the transaction as reorged and
   clears it from its pending list.

The batch endpoint is designed specifically for this step.

---

## Relationship to settlement

The txstatus endpoints are intentionally decoupled from settlement. The settlement
engine derives confirmation status independently via RPC-based block scanning with
its own DB cursor (`bitcoin_sync_state`, `bitcoin_block_history`). The txstatus
endpoints do not read from or write to any settlement state. This isolation ensures
that a bug in the display-side status reporting cannot affect payment processing.

---

## Rate limiting

Both endpoints share an IP-based rate limiter: 20 requests per minute, burst of 20.
This is a higher limit than the watch and token endpoints because the txstatus
endpoints are the reconciliation path — clients that frequently lose and regain
connectivity may need to reconcile many times per minute.

---

## Edge cases worth noting

**Immediately after confirmation:** A transaction confirmed in the most recent block
has `confirmations = 1`. `block_height` is set correctly.

**Very long txids in the batch query string:** The URL length limit of browsers and
proxies (~8KB) limits the practical batch size well below 20 for most clients.

**Same txid submitted multiple times in a batch:** The server does not deduplicate.
Each occurrence is resolved independently. Clients should deduplicate for clarity.

**Conflicting vs not_found distinction:** Unlike many Bitcoin status APIs, this
endpoint distinguishes between a transaction that was never seen (`not_found`) and
one that was seen but is now on a non-active chain (`conflicting`). This distinction
is important for reorg handling — the client should surface these differently to
the user.
