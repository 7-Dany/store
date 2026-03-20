# Wallet Backup Feature — Strategy & Recovery Procedures

> **What this file is:** A plain-language description of what must be backed up, why
> each asset is irreplaceable, the three backup layers, the four recovery scenarios,
> and the pre-mainnet checklist. Read this before touching any implementation detail.
>
> **Companion:** `wallet-backup-technical.md` — keypool cursor advance procedure,
> backup schedule and success definition, `bitcoin.conf` settings, verification
> schedule, test inventory.
> **Node configuration:** Pruned production node (`prune=10000`). `txindex` permanently
> disabled. This is the definitive production choice.

---

## Why This Document Exists

The platform's entire Bitcoin operation depends on a single Bitcoin Core descriptor
wallet. This wallet contains the master seed from which every invoice address the
platform has ever generated — or will ever generate — is derived. If this wallet is
lost without a backup, those funds are permanently and irrecoverably gone. There is
no support call, no password reset, and no authority to appeal to.

The wallet backup and the application database backup are inseparable. The wallet
tells you *which addresses you own and what funds they hold*. The database tells you
*which address belongs to which invoice, vendor, and payout record*. Losing either
one without the other leaves you with an incomplete recovery at best.

**This is not optional operational hygiene. It is a hard prerequisite for handling
real money.**

---

## Pre-Flight: Verify Wallet Type

Before setting up any backup procedure, confirm the wallet is a descriptor wallet:

```bash
bitcoin-cli -datadir=/var/lib/bitcoin -testnet4 getwalletinfo
```

Look for `"descriptors": true` in the output. If the value is `false`, the node is
using a legacy wallet. **Do not proceed with building the transaction system against
a legacy wallet.** Legacy wallets are deprecated, have worse backup semantics, and
cannot export a descriptor. Migrate to a descriptor wallet first using `migratewallet`.

---

## Pruning Decision

**The production node runs with `prune=10000` (~10 GB of blocks retained). This is
the definitive, documented production choice.**

Pruning deletes old raw block files. It does NOT touch the wallet. Bitcoin Core's
wallet index (`wallet.dat`) is stored entirely separate from block files.

`txindex=1` and `prune=N` are mutually exclusive in Bitcoin Core. The production node
therefore does **not** use `txindex`. This is a permanent decision. The `bitcoin.conf`
must never contain `txindex=1`.

**Scenario C recovery always requires a temporary full node** because the production
node is pruned. Expected recovery time for Scenario C: **3–7 days**. Scenario B
(with a good `wallet.dat` backup) recovers in hours and is the expected path.
Scenario C should never occur if `wallet.dat` backup discipline is followed.

---

## What You Are Backing Up

| Asset | What it is | What happens if lost |
|-------|-----------|---------------------|
| Wallet descriptor (xprv) | Master seed — derives every address ever used | All unswept funds permanently inaccessible |
| `wallet.dat` file | Core's full wallet database — keys, transaction history, labels | Need Scenario C (3–7 day outage on pruned node) |
| Application database | Invoice → address mapping, payout records, audit trail | Recovery becomes a manual forensic exercise |

All three must be backed up. None is a substitute for the others.

---

## Layer 1 — Descriptor Export (Seed Backup)

### How to export

```bash
bitcoin-cli -datadir=/var/lib/bitcoin -testnet4 listdescriptors true
```

The `true` argument includes private keys (xprv). **Save the entire JSON output.**

### Where to store it

| Location | Requirement |
|----------|------------|
| **Primary offline copy** | Printed on paper or written on metal (fireproof). Locked, fireproof safe on-premises. Never on any internet-connected device. |
| **Secondary offline copy** | Identical copy offsite — bank safe deposit box, trusted person's fireproof safe, or commercial secure storage. |
| **Encrypted digital backup** | GPG-encrypted with a strong passphrase (stored separately from the file). Encrypted USB drive kept separate from both servers. Convenience copy — not a replacement for physical copies. |

**Never store the raw (unencrypted) descriptor in cloud storage, email, chat, or any
internet-connected system.**

### When to re-export
The descriptor is stable. Re-export only when: the wallet is migrated or recreated,
the node is moved to new hardware, after any suspected security incident, or annually
as a scheduled verification.

---

## Layer 2 — `wallet.dat` Hot Backup

**Never copy `wallet.dat` directly while Bitcoin Core is running.** Use `backupwallet`
RPC. See `wallet-backup-technical.md §Backup Schedule and Success Definition` for the
full procedure, copy step requirements, and success definition.

**Important — deployment topology:** `backupwallet <path>` writes to the filesystem
of the machine running Bitcoin Core. If Bitcoin Core runs in a separate container, VM,
or server, the backup file exists only on that machine. The backup job must explicitly
copy the resulting file from the Bitcoin Core host to the backup storage location.
Document your deployment topology and verify this copy step before going to mainnet.

### Retention policy
- Last 7 daily backups
- Last 4 weekly backups
- Last 3 monthly backups

### Encrypting backups before offsite transfer
```bash
gpg --symmetric --cipher-algo AES256 \
    --output /backups/bitcoin/wallet_backup.dat.gpg \
    /backups/bitcoin/wallet_backup.dat
# Store passphrase separately from the encrypted file
```

The 3-2-1 rule: **3** copies, on **2** different media types, with **1** offsite.

---

## Layer 3 — Application Database Backup

Match the wallet backup schedule exactly. A wallet backup and a database backup taken
at the same time form a consistent recovery pair.

See `wallet-backup-technical.md §Backup Integrity Verification` for the verification
procedure including the monotonic MAX(id) check.

---

## Recovery Scenarios

### Scenario A — Corrupted `wallet.dat`, node still running
1. Stop Bitcoin Core.
2. Restore the most recent `wallet.dat` backup to the wallet directory.
3. Start Bitcoin Core.
4. Verify with `getwalletinfo` — check `"keypoolsize"` and `"txcount"`.
5. **Run the keypool cursor advance procedure.** Mandatory.
6. Run the reconciliation job manually to verify DB matches wallet state.

No blockchain rescan needed.

### Scenario B — Full server loss, `wallet.dat` backup available
1. Provision a new server (Linux, same OS as original production).
2. Install Bitcoin Core with the production `bitcoin.conf` (pruned, no txindex).
3. Let the node sync to chain tip.
4. Copy `wallet.dat` from backup to the wallet directory.
5. Start Bitcoin Core. Verify with `getwalletinfo`.
6. **Run the keypool cursor advance procedure.** Mandatory even in Scenario B.
7. Restore the database from the matching backup.
8. Run the reconciliation job manually before enabling any sweeps.
9. Verify that on-chain balances match the database's expected state.
10. Verify invoice address label integrity.
11. Enable the application.

### Scenario C — Full server loss, no `wallet.dat`, descriptor export only
Last-resort recovery. Expected recovery time: **3–7 days**. Always requires a
separate temporary non-pruned full node (NOT the production node).

1. Provision a **temporary full (non-pruned)** Bitcoin node.
2. Let it sync to chain tip.
3. Create a new empty descriptor wallet:
   ```bash
   bitcoin-cli createwallet "platform" false false "" false true true
   ```
4. Determine the descriptor import range dynamically:
   ```bash
   DB_MAX=$(psql -t -c "SELECT COALESCE(MAX(hd_derivation_index), 0) FROM invoice_addresses;" store_production | tr -d ' ')
   IMPORT_RANGE=$(python3 -c "import math; print(max(200000, math.ceil($DB_MAX * 1.2)))")
   # NEVER use the hardcoded value 200000 in production — always compute from DB_MAX.
   ```
5. Import the descriptor export with the correct timestamp and range (see
   `wallet-backup-technical.md §Scenario C Import Command`).
6. Run `rescanblockchain` from your platform's launch block height.
7. Export the rebuilt wallet from the temporary node.
8. Provision the production server with the production `bitcoin.conf` (pruned).
9. Copy `wallet_recovered.dat` to the production node's wallet directory.
10. **Run the keypool cursor advance procedure.** Mandatory.
11. Restore the database from the most recent backup.
12. Run the reconciliation job manually before enabling any sweeps.
13. Verify invoice address label integrity.
14. Decommission the temporary recovery node.

**Record `BTC_RECONCILIATION_START_HEIGHT` now** and keep it alongside the descriptor
export. It is required for step 6.

### Scenario D — Database lost, wallet intact
1. Restore the most recent database backup available (even if not current).
2. Use `listreceivedbyaddress` and `listtransactions` RPC calls to enumerate all
   wallet transactions.
3. Cross-reference each address against the DB's invoice addresses table.
   All invoice addresses carry the label `"invoice"` (set at
   `getnewaddress "invoice" "bech32"` time). **This label must be preserved exactly
   to support Scenario D** — never use a different label for invoice addresses.
4. Identify the gap (transactions received after the database backup) and manually
   reconcile each one against the audit trail.

**Label integrity is critical for Scenario D.** The weekly label integrity check is
the early-warning system for any code change that would break this recovery path.

---

## Pre-Mainnet Checklist

| Requirement | Status |
|-------------|--------|
| Wallet confirmed as descriptor wallet (`"descriptors": true`) | Must verify |
| Platform mainnet launch block height recorded as `BTC_RECONCILIATION_START_HEIGHT` | Must record |
| `BTC_RECONCILIATION_START_HEIGHT` verified to be ≥ node `pruneheight` (startup check passes) | Must verify |
| Descriptor export taken and stored in two offline locations | Must complete |
| `wallet.dat` automated backup job running, success record written to DB, alerting on failure | Must implement |
| Database backup running on same schedule as wallet backup | Must implement |
| Invoice address label integrity check passing | Must verify |
| Scenario B restore drill completed — including keypool cursor advance procedure | Must complete |
| Keypool cursor advance script tested against a real backup and verified correct | Must verify |
| `keypool=10000` set in production `bitcoin.conf` | Must set |
| `prune=10000` set in production `bitcoin.conf`; `txindex` NOT set | Must confirm |
| `rpcauth` set in production `bitcoin.conf`; `rpcpassword` NOT set | Must confirm |
| Bitcoin Core confirmed to start cleanly with the production `bitcoin.conf` | Must verify |
| Deployment topology documented: Bitcoin Core co-location with app confirmed; backup file copy step verified | Must document |
| Backup encryption passphrase stored separately from encrypted files | Must arrange |
| Scenario C SLA (3–7 day outage) acknowledged and accepted by platform owner | Must acknowledge |
| Backup integrity verification: MAX(id) monotonic check confirmed working | Must verify |
| Dynamic descriptor range formula (DB_MAX × 1.2) tested in Scenario C drill | Must verify |

None of these items can be deferred to after go-live.
