# Wallet Backup — Technical Implementation

> **What this file is:** Implementation contracts for the backup procedure, keypool
> cursor advance, backup success definition, `bitcoin.conf` production settings,
> verification schedule, and the complete test inventory for this package.
>
> **Read first:** `wallet-backup-feature.md` — strategy, recovery scenarios, and
> pre-mainnet checklist.

---

## Table of Contents

1. [Backup Schedule and Success Definition](#1--backup-schedule-and-success-definition)
2. [Backup Command](#2--backup-command)
3. [Keypool Cursor Advance Procedure](#3--keypool-cursor-advance-procedure)
4. [Backup Integrity Verification](#4--backup-integrity-verification)
5. [Invoice Address Label Integrity Check](#5--invoice-address-label-integrity-check)
6. [Scenario C Import Command](#6--scenario-c-import-command)
7. [bitcoin.conf Production Settings](#7--bitcoinconf-production-settings)
8. [Verification Schedule](#8--verification-schedule)
9. [Keypool Monitoring](#9--keypool-monitoring)
10. [Test Inventory](#10--test-inventory)

---

## §1 — Backup Schedule and Success Definition

| Environment | Frequency |
|-------------|-----------|
| Development (testnet4) | Daily |
| Production (mainnet) | Every 4 hours |

A backup is considered **successful** only when ALL of the following are true:
1. The `backupwallet` RPC returns success.
2. The backup file has been copied to the designated backup storage location and the
   copy has been verified (checksum match or size > 0).
3. A `wallet_backup_success` record is written to the DB with: timestamp, filename,
   sha256_checksum, storage_location.

The `Wallet backup overdue` CRITICAL alert is triggered when
`wallet_backup_success.timestamp` has not been updated within the expected backup
window — **not** merely when the backup job last ran. A job that runs but whose copy
step silently fails will trigger the alert.

If the backup job runs but the copy fails (network error, storage full, permission
denied): the failure is logged at ERROR, a CRITICAL alert fires immediately ("Backup
copy to storage failed — wallet backup incomplete"), and the next backup cycle is
triggered early (within 15 minutes).

---

## §2 — Backup Command

```bash
bitcoin-cli -datadir=/var/lib/bitcoin -testnet4 \
  backupwallet /backups/bitcoin/wallet_$(date +%Y%m%d_%H%M%S).dat
```

After the RPC call returns success, the backup file must be **explicitly copied** from
the Bitcoin Core host to the backup storage location. This is a required second step —
the RPC only writes to the local filesystem of the machine running Bitcoin Core.

---

## §3 — Keypool Cursor Advance Procedure

**Required in ALL recovery scenarios (A, B, and C).** Bitcoin Core's HD derivation
cursor may be behind the DB's maximum issued index after restoring from a backup. If
`getnewaddress` would re-issue already-assigned addresses, duplicate address
assignments and double-settlement risk follows.

```bash
# Step 1: Get the address Core would issue next
NEXT_ADDR=$(bitcoin-cli -datadir=/var/lib/bitcoin getnewaddress "probe" "bech32")

# Step 2: Get its derivation index
NEXT_INDEX=$(bitcoin-cli -datadir=/var/lib/bitcoin getaddressinfo "$NEXT_ADDR" | \
  python3 -c "import json,sys; p=json.load(sys.stdin)['hdkeypath']; print(p.split('/')[-1].rstrip(\"'\"))")

# Step 3: Get the DB's maximum issued derivation index
DB_MAX=$(psql -t -c "SELECT COALESCE(MAX(hd_derivation_index), 0) FROM invoice_addresses;" store_production | tr -d ' ')

# Step 4: If Core's next index is already > DB max, no advance needed
if [ "$NEXT_INDEX" -gt "$DB_MAX" ]; then
  echo "Cursor already past DB max ($NEXT_INDEX > $DB_MAX). Safe to proceed."
else
  # Step 5: Flush getnewaddress until Core's next index exceeds DB max
  echo "Advancing cursor from $NEXT_INDEX to past $DB_MAX..."
  while true; do
    ADDR=$(bitcoin-cli -datadir=/var/lib/bitcoin getnewaddress "flush" "bech32")
    IDX=$(bitcoin-cli -datadir=/var/lib/bitcoin getaddressinfo "$ADDR" | \
      python3 -c "import json,sys; p=json.load(sys.stdin)['hdkeypath']; print(p.split('/')[-1].rstrip(\"'\"))")
    if [ "$IDX" -gt "$DB_MAX" ]; then
      echo "Cursor advanced to $IDX. Safe to proceed."
      break
    fi
  done
fi

# Step 6: Verify — the next issued address must NOT appear in the DB
VERIFY_ADDR=$(bitcoin-cli -datadir=/var/lib/bitcoin getnewaddress "verify" "bech32")
FOUND=$(psql -t -c "SELECT COUNT(*) FROM invoice_addresses WHERE address='$VERIFY_ADDR';" store_production | tr -d ' ')
if [ "$FOUND" -ne "0" ]; then
  echo "ERROR: Next address $VERIFY_ADDR already in DB. Do NOT go online. Investigate."
  exit 1
fi
echo "Verification passed. Cursor advance complete."

# Step 7: Refill keypool buffer
bitcoin-cli -datadir=/var/lib/bitcoin keypoolrefill
```

> **Note on `keypoolrefill`:** `keypoolrefill [newsize]` sets the target number of
> pre-derived addresses to maintain in the pool — it does NOT advance the derivation
> cursor. Always use the flush loop above to advance the cursor, then call
> `keypoolrefill` to fill the pool forward from the current cursor position.

> **Table/column name:** the script references `invoice_addresses.hd_derivation_index`.
> Use the actual table and column names from the Stage 2 schema. Verify the query
> returns the expected maximum before running the advance procedure.

---

## §4 — Backup Integrity Verification

After each backup, run a test restore to a temporary database:

```bash
# 1. Schema integrity
pg_restore --schema-only --dbname=restored_db /backups/db/latest.dump

# 2. Row counts on critical tables — values must be monotonically increasing
#    compared to the PREVIOUS backup's counts.
psql -c "SELECT COUNT(*) FROM invoices;" restored_db
psql -c "SELECT COUNT(*) FROM payout_records;" restored_db
psql -c "SELECT COUNT(*) FROM financial_audit_events;" restored_db
psql -c "SELECT COUNT(*) FROM invoice_address_monitoring;" restored_db
psql -c "SELECT COUNT(*) FROM btc_outage_log;" restored_db

# 3. Integrity check: MAX(id) and COUNT(*) from financial_audit_events must
#    both be GREATER THAN the values from the previous backup.
#    An equal or lower MAX(id) is a hard failure indicating a corrupt or replayed backup.
psql -c "SELECT MAX(id), COUNT(*) FROM financial_audit_events;" restored_db
# Compare against: previous_backup.max_audit_id and previous_backup.audit_count

# 4. Verify wallet backup is readable (on a test node):
#    bitcoin-cli -datadir=/tmp/test_node -wallet=test loadwallet test
#    bitcoin-cli -datadir=/tmp/test_node -wallet=test getwalletinfo
#    Confirm "descriptors": true and keypoolsize > 0
```

An empty or smaller-than-expected count, a schema error, a MAX(id) that is not
greater than the previous backup, or a checksum mismatch means the backup is corrupt
and must be investigated immediately.

---

## §5 — Invoice Address Label Integrity Check

```bash
# Count of addresses with label "invoice" in Bitcoin Core wallet
WALLET_COUNT=$(bitcoin-cli -datadir=/var/lib/bitcoin \
  listlabeladdresses "invoice" | python3 -c "import json,sys; print(len(json.load(sys.stdin)))")

# Count of invoice addresses in the application database
DB_COUNT=$(psql -t -c "SELECT COUNT(*) FROM invoice_addresses;" store_production | tr -d ' ')

echo "Wallet label count: $WALLET_COUNT"
echo "DB count: $DB_COUNT"

if [ "$WALLET_COUNT" -ne "$DB_COUNT" ]; then
  echo "ALERT: Wallet label count does not match DB count. Investigate immediately."
  exit 1
fi
```

A mismatch indicates either: a code path used a different label (breaking Scenario D
recovery), or the DB and wallet have diverged. Run this check weekly. A failure blocks
the recovery path described in Scenario D.

---

## §6 — Scenario C Import Command

```bash
# Replace <UNIX_TIMESTAMP> with the Unix timestamp of your platform's mainnet launch.
# Replace <DESCRIPTOR_STRING> with each descriptor from your listdescriptors export.
# Replace <IMPORT_RANGE> with DB_MAX * 1.2 (computed dynamically — see feature doc).
bitcoin-cli importdescriptors '[
  {
    "desc": "<DESCRIPTOR_STRING>",
    "timestamp": <UNIX_TIMESTAMP_OF_LAUNCH>,
    "range": [0, <IMPORT_RANGE>],
    "watchonly": false,
    "label": "",
    "keypool": true,
    "internal": false
  }
]'
# Run this for each descriptor in the export (typically 6-8 descriptors).
# The "timestamp" field causes rescanblockchain to start from that point,
# avoiding a full chain scan from genesis.
```

After import, run:
```bash
bitcoin-cli rescanblockchain <BTC_RECONCILIATION_START_HEIGHT>
```

---

## §7 — bitcoin.conf Production Settings

```ini
[main]
# Keypool size — prevents exhaustion under load and provides buffer for
# keypool cursor advance during recovery
keypool=10000

# Wallet broadcast
walletbroadcast=1

# ZMQ — settlement-critical; bind to localhost only
zmqpubhashblock=tcp://127.0.0.1:28332
zmqpubhashtx=tcp://127.0.0.1:28333

# Pruning — REQUIRED. Retain approximately 10 GB of blocks.
prune=10000

# txindex is NOT set and must NEVER be set.
# txindex=1 is incompatible with prune=N. Bitcoin Core will refuse to start
# if both are present. The platform does not require txindex.

# RPC security — bind to localhost; allow only local connections
rpcbind=127.0.0.1
rpcallowip=127.0.0.1

# RPC authentication — use rpcauth (salted hash), never rpcpassword (plaintext).
# Generate with: python3 share/rpcauth/rpcauth.py <username>
# Copy the rpcauth= line from the output into this config.
# Store the generated password in your secrets manager, not in this file.
rpcauth=<username>:<salt>$<hash>
# rpcpassword MUST NOT be used in production — it stores credentials in plaintext.
```

---

## §8 — Verification Schedule

| Check | Frequency | Who |
|-------|-----------|-----|
| Confirm backup job ran AND `wallet_backup_success` record updated in DB | Every backup cycle (automated alert if overdue) | Job queue monitoring |
| Spot-check restored DB: row counts + MAX(id) monotonically increasing + schema integrity | Weekly | Platform operator |
| Invoice address label integrity: `listlabeladdresses("invoice")` count matches DB | Weekly | Platform operator |
| Full Scenario B restore drill **including keypool cursor advance procedure** | Monthly | Platform operator |
| Verify descriptor export is readable and decryptable | Quarterly | Platform owner |
| Verify offsite copies are accessible | Quarterly | Platform owner |
| Update descriptor export after any wallet change | On event | Platform operator |

The monthly Scenario B restore drill is the most important item. **The drill must
execute the complete keypool cursor advance procedure** and verify that the advance
script works correctly against a real backup.

---

## §9 — Keypool Monitoring

| Threshold | Action |
|-----------|--------|
| `keypoolsize < 100` | `Keypool low` WARNING alert fires; `keypoolrefill` called automatically |
| `keypoolsize < 10` | CRITICAL alert fires |

```bash
bitcoin-cli -datadir=/var/lib/bitcoin getwalletinfo | jq .keypoolsize
```

Set `keypool=10000` in `bitcoin.conf`.

---

## §10 — Test Inventory

### Legend
- `[UNIT]` — pure unit test, no DB or network
- `[INTG]` — integration test requiring real PostgreSQL
- `[MANUAL]` — operational drill; automated verification where possible

### TI-22: Wallet Backup and Recovery

| ID | Test Name | Class | Covers |
|----|-----------|-------|--------|
| TI-22-01 | `TestBackup_Schedule_4HourInterval_Production` | INTG | Job runs every 4 hours |
| TI-22-02 | `TestBackup_SuccessRecord_Written_OnlyAfterCopyCompletes` | INTG | **M-12**: backup_success written after copy; not after RPC |
| TI-22-03 | `TestBackup_CopyStep_Failure_CRITICAL_Alert_Immediate` | INTG | **M-12**: RPC succeeds; copy fails → CRITICAL alert; early retry |
| TI-22-04 | `TestBackup_OverdueAlert_BasedOn_SuccessRecord_Not_JobRun` | INTG | **M-12**: alert based on backup_success timestamp |
| TI-22-05 | `TestBackup_KeypoolLow_Below100_WarningAlert` | INTG | keypoolsize < 100 → WARNING; keypoolrefill called |
| TI-22-06 | `TestBackup_KeypoolCritical_Below10_CriticalAlert` | INTG | keypoolsize < 10 → CRITICAL |
| TI-22-07 | `TestBackup_LabelIntegrityCheck_MatchesDB` | INTG | **N-04**: listlabeladdresses("invoice") count == invoice_addresses count |
| TI-22-08 | `TestBackup_LabelIntegrityCheck_Mismatch_Alert` | INTG | **N-04**: mismatch → alert; investigation required |
| TI-22-09 | `TestBackup_IntegrityVerification_MaxID_Monotonic` | INTG | **N-05**: MAX(id) and COUNT(*) both increase between backups |
| TI-22-10 | `TestBackup_IntegrityVerification_MaxID_NotIncreasing_Fails` | INTG | **N-05**: equal MAX(id) = corrupt/replayed backup → failure |
| TI-22-11 | `TestBackup_DescriptorRange_Dynamic_Not_Hardcoded` | UNIT | **M-11**: Scenario C uses MAX(hd_derivation_index) × 1.2 |
| TI-22-12 | `TestBackup_DescriptorRange_NeverUsesHardcoded200000` | UNIT | **M-11**: static 200000 not present in recovery code |
| TI-22-13 | `TestBackup_ScenarioB_KeypoolCursorAdvance_ScriptCorrect` | MANUAL | Restore wallet.dat; cursor advance; verify_addr not in DB |
| TI-22-14 | `TestBackup_ScenarioB_VerificationStep_HaltsIfNextAddressInDB` | MANUAL | Incomplete cursor advance; verification step exits 1 |
| TI-22-15 | `TestBackup_DescriptorExport_Readable_Decryptable` | MANUAL | GPG-encrypted descriptor opens with stored passphrase |
| TI-22-16 | `TestBackup_PruneWindowCheck_AtStartup_BeforeServingTraffic` | INTG | **H-04**: prune check runs at startup; blocks traffic if failing |
| TI-22-17 | `TestBackup_RpcauthSet_RpcpasswordNotSet_In_Config` | MANUAL | **N-09**: bitcoin.conf has rpcauth; no rpcpassword line |
