# Glossary — BTC Payment System Terms

Short, precise definitions for terms used across the BTC schema, codebase, and technical docs.
Scope column indicates where the term applies: **Bitcoin** (protocol-level), **Platform** (this system's logic), or **Both**.

---

| Term | Short explanation | Scope |
|---|---|---|
| **Invoice** | A payment request issued to a buyer: a specific satoshi amount, a unique deposit address, and an expiry window. The root record that drives the entire payment pipeline. | Platform |
| **Settlement** | The internal accounting step that runs after a payment reaches the required confirmation depth. Calculates the vendor's net amount (received minus platform fee), credits their balance or creates a payout record. No Bitcoin moves during settlement — it is pure bookkeeping. | Platform |
| **Payout** | A record tracking the lifecycle of getting a vendor's settled funds to them on-chain. Starts as a debt on the books (`held`), progresses through `queued → constructing → broadcast → confirmed` as the Bitcoin transaction is built and confirmed. | Platform |
| **Sweep** | The job that batches one or more queued payout records into a single Bitcoin transaction and broadcasts it. Batching amortises the fixed transaction overhead across multiple vendors. | Platform |
| **Reconciliation** | A periodic audit that checks whether the platform wallet's on-chain UTXO value equals the sum of all internal obligations (vendor balances + in-flight payouts + unconfirmed payments + treasury reserve). A discrepancy halts all sweeps. | Platform |
| **Discrepancy** | A non-zero difference between the reconciliation formula's two sides. Indicates the internal books do not match on-chain reality. Triggers `sweep_hold_mode` and a CRITICAL alert until an admin investigates and clears it. | Platform |
| **Treasury reserve** | The satoshi amount retained by the platform from miner fees collected during sweeps. A required term in the reconciliation formula; if not tracked accurately the formula permanently drifts. | Platform |
| **Wallet mode** | A per-vendor setting that controls how settled funds are handled. `bridge` — forwarded to an external address on each settlement. `platform` — accumulated as an internal balance. `hybrid` — accumulated until a threshold is crossed, then auto-swept. Snapshotted onto every invoice at creation. | Platform |
| **Snapshot** | A copy of the vendor's tier config and wallet settings baked into the invoice row at creation time. The settlement engine reads exclusively from the snapshot — changes to tier or vendor config after creation have no effect on in-flight invoices. | Platform |
| **Confirmation depth** | The number of blocks that must be mined on top of the block containing a payment before the platform considers it confirmed and triggers settlement. Higher depth = less reorg risk. Free tier uses 6 (~60 min), Enterprise uses 1. | Both |
| **Block reorg** | A reorganisation of the blockchain where previously confirmed blocks are replaced by a competing chain of greater cumulative work. Can un-confirm a payment or sweep that was already settled. Triggers `reorg_admin_required` status. | Bitcoin |
| **Mempool** | The set of unconfirmed transactions waiting to be included in a block by miners. A payment is `detected` when seen in the mempool, before any block confirmation. | Bitcoin |
| **Mempool drop** | When a transaction disappears from the mempool without confirming — typically because it was replaced, expired, or evicted due to a low fee. Moves the invoice to `mempool_dropped` status. | Bitcoin |
| **UTXO** | Unspent Transaction Output. The fundamental unit of Bitcoin ownership. The platform wallet holds a set of UTXOs; the reconciliation formula validates their total value matches internal obligations. | Bitcoin |
| **HD wallet / derivation index** | Hierarchical Deterministic wallet. A single seed generates an unlimited sequence of addresses via a tree of key derivations. Each invoice gets a unique address at a specific leaf index (`hd_derivation_index`), stored to enable wallet recovery. | Bitcoin |
| **RBF — Replace-By-Fee** | A Bitcoin protocol feature allowing an unconfirmed transaction to be replaced by a higher-fee version, incentivising miners to include the replacement. Used when a broadcast sweep is stuck due to a low fee rate. The original `txid` is preserved in `original_txid` alongside the new `batch_txid` for a full audit trail. | Bitcoin |
| **Miner fee** | The satoshi amount paid to the miner who includes a transaction in a block. Proportional to transaction size in virtual bytes (vbytes) and current network congestion. Deducted from the sweep gross and tracked in `miner_fee_satoshis`. | Bitcoin |
| **Fee rate (sat/vbyte)** | The price per virtual byte of transaction data, used to estimate and cap miner fees. The platform enforces a per-tier `miner_fee_cap_sat_vbyte` ceiling to prevent runaway costs during high network congestion. | Bitcoin |
| **P2WPKH / bech32** | Pay-to-Witness-Public-Key-Hash. A native SegWit address format (prefix `bc1q`). All platform invoice addresses use this format because it produces the smallest transaction size, minimising miner fees. | Bitcoin |
| **ZMQ** | ZeroMQ. A messaging socket used by Bitcoin Core to push real-time notifications (new transactions, new blocks) to subscribers without polling. The platform's ZMQ subscriber detects incoming payments and block events as they happen. | Both |
| **Expiry compensation** | Extends an invoice's effective expiry window by the total duration of any node outage that overlapped with the invoice's life. Ensures buyers are not penalised for platform-side downtime. | Platform |
| **Overpayment thresholds** | Two limits that must both be exceeded before a payment is flagged `overpaid`: a relative threshold (e.g. 5% above invoiced amount) AND an absolute threshold (e.g. 10,000 sat above). Both must hold simultaneously. | Platform |
| **Payment tolerance** | A percentage band around the invoiced amount within which a slight underpayment or overpayment is settled as if exact. Prevents trivial rounding differences from triggering special-case handling. | Platform |
| **KYC / AML** | Know Your Customer / Anti-Money Laundering. Regulatory identity checks on vendors. The schema carries `kyc_status` as a placeholder; logic is gated behind non-NULL tier thresholds and not yet implemented. | Platform |
| **TOCTOU** | Time-of-Check to Time-of-Use. A concurrency bug where a condition is validated, time passes, and the condition changes before the dependent action executes. Mitigated in the payout guard trigger via `SELECT ... FOR SHARE`. | Platform |
