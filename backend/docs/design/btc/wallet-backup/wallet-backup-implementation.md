# Wallet Backup — Implementation Plan

> **What this file is:** A complete, phased implementation spec. Every decision
> is made here. Each phase can be executed in a single session by reading only
> this file plus the files listed under "Read before starting" for that phase.
>
> **Companion files:**
> - `wallet-backup-feature.md` — strategy, recovery scenarios, pre-mainnet checklist
> - `wallet-backup-technical.md` — backup schedule, keypool procedure, bitcoin.conf,
>   verification schedule, test inventory

---

## Architecture Decisions (all final)

### AD-01 — Package location
`internal/domain/bitcoin/backup/` (no HTTP endpoint; background job only).
Not a sub-router. No `handler.go`, no `routes.go`. The service is started from
`bitcoin.Routes()` and shut down via `deps.BitcoinBackupShutdown`.

### AD-02 — Backup interval
Derived from network at startup. Not configurable at runtime.
- `mainnet`  → `4 * time.Hour`
- `testnet4` → `24 * time.Hour`

Overdue alert threshold = 1× the interval (fire after one missed cycle).

### AD-03 — Deployment topology
Co-located: Bitcoin Core and the Go app run on the same host. The backup file is
written to a local directory by the `backupwallet` RPC, then copied to an archive
directory on the same host by the Go service. This is the "Deployment topology
documented: Bitcoin Core co-location with app confirmed" item from the pre-mainnet
checklist. Cloud storage is a future concern.

### AD-04 — Copy + verify step
After the `backupwallet` RPC succeeds, the service:
1. Reads the file and computes SHA-256 using `crypto/sha256`.
2. Copies the file to the archive directory using `io.Copy`.
3. Re-reads the archive copy and re-computes SHA-256 to verify integrity.
4. Writes the `wallet_backup_success` DB record **only after all three steps pass**.
If any step fails: log ERROR, fire CRITICAL alert, schedule an early retry in 15
minutes. **Never write the DB record on partial success.**

### AD-05 — Filename format
```
wallet-{network}-{RFC3339UTC}.dat
Example: wallet-testnet4-2025-04-01T14-30-00Z.dat
```
Colons replaced with hyphens so the filename is safe on all OS and storage backends.

### AD-06 — Keypool monitoring
Checked on every backup cycle AND on a separate 30-minute health tick. Both paths
call the same `runKeypoolCheck(ctx)` method on the service.

| `keypoolsize` | Action |
|---|---|
| < 100 | WARNING alert (`OnKeypoolWarning`); call `KeypoolRefill(ctx, defaultKeypoolSize)` |
| < 10  | CRITICAL alert (`OnKeypoolCritical`); call `KeypoolRefill` |

`defaultKeypoolSize = 10000` (matches `keypool=10000` in `bitcoin.conf`).

### AD-07 — Label integrity check
Run once per week (168-hour interval). The service tracks `lastLabelCheck time.Time`
(zero value = never run; triggers a run on the first backup cycle). The check calls
`ListLabelAddresses(ctx, "invoice")` and `CountInvoiceAddressesByNetwork`. A mismatch
fires `OnLabelIntegrityMismatch` and logs at ERROR; it does NOT abort the backup job.

### AD-08 — Metrics: extend BitcoinRecorder
`SetWalletBackupAge(float64)` already exists on `BitcoinRecorder`. Add four new
methods to the interface AND to `NoopBitcoinRecorder`:

```go
OnBackupFailed(network, reason string)    // reason: "rpc", "checksum", "copy", "db"
OnBackupCopyFailed(network string)        // copy step specifically (CRITICAL alert)
OnKeypoolWarning(network string)          // keypoolsize < 100
OnKeypoolCritical(network string)         // keypoolsize < 10
OnLabelIntegrityMismatch(network string)  // wallet count ≠ DB count
```

These five methods are added to `BitcoinRecorder` in
`internal/domain/bitcoin/shared/recorder.go` and stubbed on `NoopBitcoinRecorder`.
The concrete implementation is in `internal/platform/telemetry/bitcoin_hooks.go`
(add Prometheus counter/gauge for each).

### AD-09 — RPC methods to add
Three new methods on `rpc.Client`:

| Method | RPC call | Retried? | Notes |
|---|---|---|---|
| `BackupWallet(ctx, destination string) error` | `backupwallet` | No — mutation | Pass `out=nil`; null result is expected and correct |
| `ListLabelAddresses(ctx, label string) ([]string, error)` | `listlabeladdresses` | Yes | Returns JSON array of address strings |
| `ListDescriptors(ctx, private bool) (DescriptorList, error)` | `listdescriptors` | Yes | `private=true` includes xprv; used for descriptor export verification only |

`BackupWallet` is NOT retried because it writes a file; a retry after a partial
success would silently overwrite a partially-written backup. The service's outer
backup loop owns retry semantics (early retry after copy failure).

### AD-10 — SQL queries to add
Three new named queries in `sql/queries/btc.sql`:

```
InsertWalletBackupSuccess     :one   — write after RPC + copy + verify all pass
GetLatestWalletBackupSuccess  :one   — get most recent for overdue alert
CountInvoiceAddressesByNetwork :one  — label integrity: COUNT(*) WHERE network=@network
```

Run `make sqlc` after adding. The generated types live in `internal/db/btc.sql.go`.

### AD-11 — New Deps fields
Add to `internal/app/deps.go` (Bitcoin section):

```go
// BitcoinBackupDir is the directory where backupwallet writes backup files
// (BTC_BACKUP_DIR). Must be a local path writable by the app process.
// Default: /backups/bitcoin
BitcoinBackupDir string

// BitcoinBackupArchive is the directory where backup files are copied for
// long-term retention (BTC_BACKUP_ARCHIVE). Must differ from BitcoinBackupDir
// to ensure the copy step actually moves the file off the hot working dir.
// Default: /backups/bitcoin/archive
BitcoinBackupArchive string

// BitcoinBackupShutdown drains the backup service's background goroutines.
// Set by backup.Start; nil when BitcoinEnabled is false.
BitcoinBackupShutdown func()
```

### AD-12 — Wiring in bitcoin.Routes()
`bitcoin.Routes()` in `internal/domain/bitcoin/routes.go` currently wires
`watch`, `events`, and `txstatus`. Add after the existing wiring:

```go
svc := backup.NewService(backup.Config{...}, deps.BitcoinRPC, backupStore, deps.Metrics)
deps.BitcoinBackupShutdown = svc.Start(ctx)
```

No routes registered. `backup.NewService` must be called even though there is no
sub-router entry.

### AD-13 — Early-retry mechanism
When the copy or verify step fails, the backup service schedules an early retry
after 15 minutes using a `time.AfterFunc` on a `retryTimer` field. This is
separate from the main ticker. On a successful backup, the retry timer is stopped
and reset. The retry fires at most once before the next regular cycle.

### AD-14 — Retention policy (not implemented in Go)
The 7-daily / 4-weekly / 3-monthly retention policy is enforced by a separate
OS-level cron script or storage lifecycle rule, NOT by the Go service. The Go
service only writes new backups; it never deletes old ones. This is documented
here to prevent accidental deletion logic from being added.

### AD-15 — `backupwallet` null result handling
`doCall` in `rpc/client.go` currently panics when `out == nil`:
```go
// Guard against null result
if len(rpcResp.Result) == 0 || bytes.Equal(rpcResp.Result, []byte("null")) {
    return telemetry.RPC(...)  // only runs when out != nil
}
```
The guard only fires when `out != nil`. Passing `nil` as `out` causes `doCall`
to return `nil` immediately after the HTTP round-trip succeeds — which is correct
for `backupwallet`. No change to `doCall` is needed.

---

## Phase 1 — RPC Extension

**Goal:** Add `BackupWallet`, `ListLabelAddresses`, `ListDescriptors` to the RPC
client. This has no side effects on existing code.

### Read before starting
- `internal/platform/bitcoin/rpc/client.go` (full)
- `internal/platform/bitcoin/rpc/types.go` (full)

### Files to change

#### `internal/platform/bitcoin/rpc/types.go`
Add at the end of the file:

```go
// ── Descriptor types ─────────────────────────────────────────────────────────

// DescriptorList is returned by listdescriptors.
// Contains wallet name and all descriptors (external + internal + change paths).
// When private=true, Desc contains the xprv master key — handle with care.
type DescriptorList struct {
	WalletName  string       `json:"wallet_name"`
	Descriptors []Descriptor `json:"descriptors"`
}

// Descriptor is one entry in a DescriptorList.
// Range is nil for non-range descriptors.
type Descriptor struct {
	Desc      string `json:"desc"`
	Timestamp int64  `json:"timestamp"`
	Active    bool   `json:"active"`
	Internal  bool   `json:"internal"`
	Range     []int  `json:"range,omitempty"`
	Next      int    `json:"next,omitempty"`
}
```

#### `internal/platform/bitcoin/rpc/client.go`
**Step 1 — add three method name constants** (in the method name constants block):
```go
rpcMethodBackupWallet       = "backupwallet"
rpcMethodListLabelAddresses = "listlabeladdresses"
rpcMethodListDescriptors    = "listdescriptors"
```

**Step 2 — add three methods to the `Client` interface**:
```go
// BackupWallet creates a backup of the wallet to the given destination path.
// The path must be writable on the Bitcoin Core host filesystem.
// Not retried — callers own retry semantics. Returns nil on success.
// Bitcoin Core returns a null result on success; this is correct and expected.
BackupWallet(ctx context.Context, destination string) error

// ListLabelAddresses returns all addresses in the wallet carrying the given
// label. Used for label integrity checks (verify "invoice" count == DB count).
ListLabelAddresses(ctx context.Context, label string) ([]string, error)

// ListDescriptors returns the wallet's descriptor set.
// When private is true, the output includes the xprv master key.
// Handle the output with care — never log it, never store it unencrypted.
ListDescriptors(ctx context.Context, private bool) (DescriptorList, error)
```

**Step 3 — add three method implementations** on `*client` (after the existing
`KeypoolRefill` implementation):

```go
// BackupWallet creates a backup of the wallet to the given destination path.
// Not retried — backupwallet is a mutation; the caller owns retry semantics.
// Bitcoin Core returns null on success; passing out=nil skips the null guard
// in doCall, which is correct for void-return RPCs.
func (c *client) BackupWallet(ctx context.Context, destination string) error {
    return c.call(ctx, rpcMethodBackupWallet, []any{destination}, nil)
}

// ListLabelAddresses returns all wallet addresses carrying the given label.
// Retried — this is a read-only query.
func (c *client) ListLabelAddresses(ctx context.Context, label string) ([]string, error) {
    var result []string
    err := c.retryCall(ctx, rpcMethodListLabelAddresses, []any{label}, &result)
    return result, err
}

// ListDescriptors returns the wallet's descriptor set.
// When private is true, the output contains the xprv master key.
// Retried — read-only. Never log the result when private=true.
func (c *client) ListDescriptors(ctx context.Context, private bool) (DescriptorList, error) {
    var result DescriptorList
    err := c.retryCall(ctx, rpcMethodListDescriptors, []any{private}, &result)
    return result, err
}
```

### Validation
- `go build ./internal/platform/bitcoin/rpc/...` must pass.
- `go vet ./internal/platform/bitcoin/rpc/...` must pass.
- Existing `client_test.go` must still pass.
- No mock needs updating yet — the mock is in the backup domain (Phase 3).

---

## Phase 2 — SQL Queries

**Goal:** Add the three backup queries to `btc.sql`, then regenerate sqlc.

### Read before starting
- `sql/queries/btc.sql` — last section (SSE token issuances), to find the correct
  append position
- `sql/schema/017_btc_audit.sql` — the `wallet_backup_success` table definition
  (to confirm column names before writing the INSERT)

### Queries to add

Append to the end of `sql/queries/btc.sql` under a new section header:

```sql
/* ════════════════════════════════════════════════════════════
   WALLET BACKUP
   Written only after RPC + file copy + checksum verify all succeed.
   The "Wallet backup overdue" alert monitors timestamp, not job run time.
   ════════════════════════════════════════════════════════════ */

-- name: InsertWalletBackupSuccess :one
-- Write a backup completion record. Called ONLY after:
--   1. backupwallet RPC returned success.
--   2. File copied to archive directory.
--   3. SHA-256 of archive copy matches SHA-256 of original.
-- A job that runs but fails any of steps 2–3 must NOT call this query.
-- The overdue alert fires when this table's latest timestamp is stale.
INSERT INTO wallet_backup_success (
    network,
    timestamp,
    filename,
    sha256_checksum,
    storage_location
)
VALUES (
    @network,
    @timestamp::timestamptz,
    @filename,
    @sha256_checksum,
    @storage_location
)
RETURNING id, network, timestamp, filename, sha256_checksum, storage_location, created_at;


-- name: GetLatestWalletBackupSuccess :one
-- Return the most recent backup success record for a network.
-- Used by the overdue alert check and by the backup age metric.
-- Returns pgx.ErrNoRows on a fresh deployment with no backups yet.
-- Index: idx_wbs_latest ON wallet_backup_success(network, timestamp DESC)
SELECT id, network, timestamp, filename, sha256_checksum, storage_location, created_at
FROM wallet_backup_success
WHERE network = @network
ORDER BY timestamp DESC
LIMIT 1;


-- name: CountInvoiceAddressesByNetwork :one
-- Label integrity check: count of invoice addresses in the DB for a network.
-- Compare with len(listlabeladdresses("invoice")) in the Bitcoin Core wallet.
-- A mismatch indicates a label divergence or wallet/DB sync issue.
SELECT COUNT(*)::bigint AS count
FROM invoice_addresses
WHERE network = @network;
```

### After adding queries
Run: `make sqlc` (or the project's equivalent sqlc generation command).

Verify that `internal/db/btc.sql.go` now contains:
- `InsertWalletBackupSuccess` method on `*Queries`
- `GetLatestWalletBackupSuccess` method on `*Queries`
- `CountInvoiceAddressesByNetwork` method on `*Queries`
- The sqlc-generated param structs:
  - `InsertWalletBackupSuccessParams`
  - `GetLatestWalletBackupSuccessRow` (or `WalletBackupSuccess` — depends on sqlc config)

And that `internal/db/querier.go` includes the three new method signatures.

### Validation
- `go build ./internal/db/...` must pass.
- `go build ./...` must pass.

---

## Phase 3 — Backup Domain Package

**Goal:** Create `internal/domain/bitcoin/backup/` with the full backup service.

### Read before starting
- `internal/domain/bitcoin/shared/recorder.go` (full) — to know the existing
  `BitcoinRecorder` interface before extending it
- `internal/domain/bitcoin/watch/service.go` (full) — canonical pattern for a
  Bitcoin domain service with a background goroutine
- `internal/platform/bitcoin/rpc/client.go` (the `Client` interface block only)
- `internal/db/btc.sql.go` (the three new methods added in Phase 2)

### Step 3a — Extend BitcoinRecorder

**File:** `internal/domain/bitcoin/shared/recorder.go`

Add five new methods to the `BitcoinRecorder` interface (after `SetWalletBackupAge`):

```go
// OnBackupFailed increments the backup failure counter for a network.
// reason is one of: "rpc", "checksum", "copy", "db".
OnBackupFailed(network, reason string)
// OnBackupCopyFailed fires a CRITICAL alert when the copy step fails.
// The overdue alert will also fire at the next check cycle if not resolved.
OnBackupCopyFailed(network string)
// OnKeypoolWarning fires when keypoolsize < 100.
OnKeypoolWarning(network string)
// OnKeypoolCritical fires when keypoolsize < 10.
OnKeypoolCritical(network string)
// OnLabelIntegrityMismatch fires when wallet label count ≠ DB count.
OnLabelIntegrityMismatch(network string)
```

Add matching no-op stubs to `NoopBitcoinRecorder`:
```go
func (NoopBitcoinRecorder) OnBackupFailed(string, string)   {}
func (NoopBitcoinRecorder) OnBackupCopyFailed(string)       {}
func (NoopBitcoinRecorder) OnKeypoolWarning(string)         {}
func (NoopBitcoinRecorder) OnKeypoolCritical(string)        {}
func (NoopBitcoinRecorder) OnLabelIntegrityMismatch(string) {}
```

**File:** `internal/platform/telemetry/bitcoin_hooks.go`
Add concrete Prometheus implementations for all five methods on `*Registry`.
Use `counter` for `OnBackupFailed` (with `network` + `reason` labels),
`OnBackupCopyFailed`, `OnKeypoolWarning`, `OnKeypoolCritical`, and
`OnLabelIntegrityMismatch`. Register counters under the `btc_` namespace:
- `btc_backup_failures_total{network,reason}`
- `btc_backup_copy_failures_total{network}`
- `btc_keypool_warning_total{network}`
- `btc_keypool_critical_total{network}`
- `btc_label_integrity_mismatch_total{network}`

### Step 3b — Create the package files

Create directory: `internal/domain/bitcoin/backup/`

---

#### File: `internal/domain/bitcoin/backup/models.go`

```go
package backup

import "time"

// Config holds all parameters for the backup service.
// Constructed in bitcoin.Routes() from app.Deps.
type Config struct {
    // Network is "mainnet" or "testnet4".
    Network string
    // BackupDir is the directory where backupwallet writes the raw backup file.
    // Must be writable by the process running Bitcoin Core (co-located topology).
    BackupDir string
    // ArchiveDir is the destination directory for the post-copy backup file.
    // Must differ from BackupDir.
    ArchiveDir string
    // Interval is how often to run the backup job (4h mainnet, 24h testnet4).
    Interval time.Duration
    // OverdueThreshold is the maximum age of the latest backup_success record
    // before the overdue alert fires. Set equal to Interval.
    OverdueThreshold time.Duration
    // LabelCheckInterval is how often to run the label integrity check.
    // Default: 168h (7 days).
    LabelCheckInterval time.Duration
    // KeypoolCheckInterval is the independent keypool health tick interval.
    // Default: 30 minutes.
    KeypoolCheckInterval time.Duration
}

// BackupResult is the output of a completed backup operation.
type BackupResult struct {
    // Filename is the base name of the archive file (e.g. wallet-mainnet-2025-04-01T14-30-00Z.dat).
    Filename string
    // SHA256Checksum is the hex-encoded SHA-256 of the archive file.
    SHA256Checksum string
    // StorageLocation is the full path of the archive file.
    StorageLocation string
    // Timestamp is when the backup completed (all three steps passed).
    Timestamp time.Time
}

// KeypoolStatus captures a keypool health snapshot.
type KeypoolStatus struct {
    Size       int
    IsLow      bool // keypoolsize < 100 → WARNING
    IsCritical bool // keypoolsize < 10  → CRITICAL
}

// LabelIntegrityResult captures the result of the weekly label integrity check.
type LabelIntegrityResult struct {
    WalletCount int64
    DBCount     int64
    Match       bool
}
```

---

#### File: `internal/domain/bitcoin/backup/errors.go`

```go
package backup

import "errors"

var (
    // ErrBackupRPCFailed is returned when the backupwallet RPC call fails.
    ErrBackupRPCFailed = errors.New("backup rpc call failed")
    // ErrBackupCopyFailed is returned when the file copy to archive dir fails.
    ErrBackupCopyFailed = errors.New("backup file copy to archive failed")
    // ErrBackupVerifyFailed is returned when the archive copy checksum differs
    // from the source checksum — indicates a corrupt copy.
    ErrBackupVerifyFailed = errors.New("backup file checksum mismatch after copy")
    // ErrNoBackupRecord is returned by GetLatestWalletBackupSuccess on a fresh
    // deployment with no backup records. Not an error condition; callers treat it
    // as "no prior backup" and skip the overdue check until the first run.
    ErrNoBackupRecord = errors.New("no backup record found")
)
```

---

#### File: `internal/domain/bitcoin/backup/store.go`

```go
package backup

import (
    "context"
    "errors"
    "time"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/7-Dany/store/backend/internal/db"
)

// Storer is the data-access contract for the backup service.
type Storer interface {
    // InsertWalletBackupSuccess writes a successful backup record.
    // Called ONLY after RPC + copy + verify all pass.
    InsertWalletBackupSuccess(ctx context.Context, p InsertBackupSuccessParams) (db.WalletBackupSuccess, error)
    // GetLatestWalletBackupSuccess returns the most recent backup record.
    // Returns ErrNoBackupRecord (wrapping pgx.ErrNoRows) on a fresh deployment.
    GetLatestWalletBackupSuccess(ctx context.Context, network string) (db.WalletBackupSuccess, error)
    // CountInvoiceAddresses returns the count of invoice addresses for a network.
    // Used for weekly label integrity check.
    CountInvoiceAddresses(ctx context.Context, network string) (int64, error)
}

// InsertBackupSuccessParams groups the parameters for InsertWalletBackupSuccess.
type InsertBackupSuccessParams struct {
    Network         string
    Timestamp       time.Time
    Filename        string
    SHA256Checksum  string
    StorageLocation string
}

// Store is the concrete pgx-backed Storer implementation.
type Store struct {
    q *db.Queries
}

// NewStore constructs a Store from the shared connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
    return &Store{q: db.New(pool)}
}

func (s *Store) InsertWalletBackupSuccess(ctx context.Context, p InsertBackupSuccessParams) (db.WalletBackupSuccess, error) {
    return s.q.InsertWalletBackupSuccess(ctx, db.InsertWalletBackupSuccessParams{
        Network:         p.Network,
        Timestamp:       p.Timestamp,
        Filename:        p.Filename,
        Sha256Checksum:  p.SHA256Checksum,
        StorageLocation: p.StorageLocation,
    })
}

func (s *Store) GetLatestWalletBackupSuccess(ctx context.Context, network string) (db.WalletBackupSuccess, error) {
    row, err := s.q.GetLatestWalletBackupSuccess(ctx, network)
    if errors.Is(err, pgx.ErrNoRows) {
        return db.WalletBackupSuccess{}, ErrNoBackupRecord
    }
    return row, err
}

func (s *Store) CountInvoiceAddresses(ctx context.Context, network string) (int64, error) {
    return s.q.CountInvoiceAddressesByNetwork(ctx, network)
}
```

---

#### File: `internal/domain/bitcoin/backup/service.go`

This is the largest file. Document it fully before writing.

**Exported types:**
- `Service` struct — the backup runner

**Service fields:**
```go
type Service struct {
    cfg           Config
    rpc           BackupRPCClient    // narrow RPC interface
    store         Storer
    rec           bitcoinshared.BitcoinRecorder

    lastLabelCheck time.Time         // zero = never run; checked against LabelCheckInterval
    retryTimer     *time.Timer       // early retry after copy/verify failure; may be nil

    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}
```

**BackupRPCClient interface** (narrow — only what backup needs):
```go
type BackupRPCClient interface {
    BackupWallet(ctx context.Context, destination string) error
    ListLabelAddresses(ctx context.Context, label string) ([]string, error)
    GetWalletInfo(ctx context.Context) (rpc.WalletInfo, error)
    KeypoolRefill(ctx context.Context, newSize int) error
}
```

**Constructor:**
```go
func NewService(cfg Config, rpcClient BackupRPCClient, store Storer, rec bitcoinshared.BitcoinRecorder) *Service
```
- Sets `cfg.LabelCheckInterval = 168*time.Hour` if zero.
- Sets `cfg.KeypoolCheckInterval = 30*time.Minute` if zero.
- If `rec == nil`, substitutes `bitcoinshared.NoopBitcoinRecorder{}`.

**`Start(ctx context.Context) func()`**
- Derives a cancellable child context.
- Launches two goroutines:
  1. `s.backupLoop()` — main backup + label check tick.
  2. `s.keypoolLoop()` — independent 30-minute keypool check.
- Returns a shutdown function that cancels the context and calls `s.wg.Wait()`.

**`backupLoop()`**
```
ticker := time.NewTicker(cfg.Interval)
defer ticker.Stop()

// Run immediately on first tick.
for {
    select {
    case <-s.ctx.Done():
        return
    case <-ticker.C:
        s.runBackupCycle(s.ctx)
    }
}
```

**`runBackupCycle(ctx)`**
1. Call `runBackup(ctx)` — full backup sequence.
2. Call `runKeypoolCheck(ctx)` — always runs after backup.
3. If `time.Since(s.lastLabelCheck) >= s.cfg.LabelCheckInterval`: call `runLabelIntegrityCheck(ctx)`.

**`runBackup(ctx) error`** — the core backup sequence:
```
1. Generate filename: "wallet-{network}-{time.Now().UTC().Format("2006-01-02T15-04-05Z")}.dat"
2. rawPath := filepath.Join(cfg.BackupDir, filename)
3. Call rpc.BackupWallet(ctx, rawPath) — if error: log ERROR, rec.OnBackupFailed(network, "rpc"), schedule early retry, return err.
4. Compute SHA-256 of rawPath → sourceChecksum.
   If error: log ERROR, rec.OnBackupFailed(network, "checksum"), schedule early retry, return err.
5. archivePath := filepath.Join(cfg.ArchiveDir, filename)
6. Copy rawPath → archivePath using io.Copy in a read+write goroutine.
   If error: log ERROR, rec.OnBackupFailed(network, "copy"), rec.OnBackupCopyFailed(network), schedule early retry, return err.
7. Compute SHA-256 of archivePath → archiveChecksum.
8. If sourceChecksum != archiveChecksum: log ERROR, rec.OnBackupFailed(network, "checksum"), rec.OnBackupCopyFailed(network), schedule early retry, return ErrBackupVerifyFailed.
9. Call store.InsertWalletBackupSuccess(ctx, ...) — if error: log ERROR, rec.OnBackupFailed(network, "db"), return err.
10. rec.OnBackupSuccess(network)  ← NOTE: add this to BitcoinRecorder in step 3a.
    Wait — OnBackupSuccess is NOT in the current list of five new methods.
    Add it: OnBackupSuccess(network string).
11. rec.SetWalletBackupAge(0) — just completed; age is zero.
12. Cancel any pending early retry timer.
13. Return nil.
```

> **Correction from step 10:** `OnBackupSuccess(network string)` must be added to
> `BitcoinRecorder` and `NoopBitcoinRecorder` as the sixth new method (AD-08 updated).
> Telemetry: increment `btc_backup_success_total{network}` counter.

**`computeSHA256(path string) (string, error)`** — private helper:
```go
func computeSHA256(path string) (string, error) {
    f, err := os.Open(path)
    if err != nil { return "", err }
    defer f.Close()
    h := sha256.New()
    if _, err := io.Copy(h, f); err != nil { return "", err }
    return hex.EncodeToString(h.Sum(nil)), nil
}
```

**`copyFile(src, dst string) error`** — private helper using `io.Copy`.

**`scheduleEarlyRetry()`** — fires `runBackupCycle` after 15 minutes:
```go
func (s *Service) scheduleEarlyRetry() {
    if s.retryTimer != nil {
        s.retryTimer.Stop()
    }
    s.retryTimer = time.AfterFunc(15*time.Minute, func() {
        s.runBackupCycle(s.ctx)
    })
}
```

**`runKeypoolCheck(ctx) KeypoolStatus`**:
1. Call `rpc.GetWalletInfo(ctx)`.
2. If error: log WARN (non-fatal; don't block the backup).
3. size := info.KeypoolSize.
4. If size < 10: log ERROR, rec.OnKeypoolCritical(network), call KeypoolRefill(ctx, 10000).
5. Else if size < 100: log WARN, rec.OnKeypoolWarning(network), call KeypoolRefill(ctx, 10000).
6. Return KeypoolStatus{Size: size, IsLow: size < 100, IsCritical: size < 10}.

**`runLabelIntegrityCheck(ctx)`**:
1. Call `rpc.ListLabelAddresses(ctx, "invoice")` → walletAddresses.
2. Call `store.CountInvoiceAddresses(ctx, network)` → dbCount.
3. walletCount := int64(len(walletAddresses)).
4. If walletCount != dbCount:
   - log ERROR with walletCount and dbCount.
   - rec.OnLabelIntegrityMismatch(network).
   - NOTE: mismatch does NOT abort the backup or affect `lastLabelCheck`.
5. Update `s.lastLabelCheck = time.Now()`.

**`keypoolLoop()`**:
```
ticker := time.NewTicker(cfg.KeypoolCheckInterval)
defer ticker.Stop()
for {
    select {
    case <-s.ctx.Done(): return
    case <-ticker.C:
        s.runKeypoolCheck(s.ctx)
    }
}
```

**`UpdateBackupAge(ctx context.Context)`** — called from an external "age probe"
goroutine (see Phase 4 — the backup age metric must be updated periodically, not
just after a successful backup):

Actually this is simpler: on each backup tick and on each keypool tick, call:
```go
latest, err := s.store.GetLatestWalletBackupSuccess(ctx, s.cfg.Network)
if err == nil {
    s.rec.SetWalletBackupAge(time.Since(latest.Timestamp).Seconds())
}
```
Do this at the top of `runBackupCycle` AND inside `keypoolLoop` on each tick.

---

#### File: `internal/domain/bitcoin/backup/export_test.go`

```go
//go:build !integration

package backup

// Exported for testing only.
var ComputeSHA256 = computeSHA256
var CopyFile = copyFile
```

---

### Validation after Phase 3
- `go build ./internal/domain/bitcoin/backup/...` must pass.
- `go vet ./internal/domain/bitcoin/backup/...` must pass.
- `go build ./internal/domain/bitcoin/shared/...` must pass (recorder extended).
- `go build ./internal/platform/telemetry/...` must pass (new hooks added).

---

## Phase 4 — Wiring

**Goal:** Wire the backup service into the application startup path.

### Read before starting
- `internal/app/deps.go` (full)
- `internal/domain/bitcoin/routes.go` (full)
- `internal/domain/bitcoin/backup/service.go` (the one written in Phase 3)

### Step 4a — Extend `app.Deps`

In `internal/app/deps.go`, in the Bitcoin section after `BitcoinEventsShutdown`, add:

```go
// BitcoinBackupDir is the local directory where backupwallet writes backup files.
// Sourced from BTC_BACKUP_DIR env var (default: /backups/bitcoin).
// Must be writable by the Bitcoin Core process on the same host.
BitcoinBackupDir string

// BitcoinBackupArchive is the local directory where backup files are copied
// for long-term retention. Sourced from BTC_BACKUP_ARCHIVE env var
// (default: /backups/bitcoin/archive). Must differ from BitcoinBackupDir.
BitcoinBackupArchive string

// BitcoinBackupShutdown drains the backup service's background goroutines.
// Set by backup.Start in bitcoin.Routes; nil when BitcoinEnabled=false.
BitcoinBackupShutdown func()
```

### Step 4b — Add env var parsing in server.go / config.go

In the project's config struct (wherever `BTC_*` env vars are parsed):
```
BTC_BACKUP_DIR      → deps.BitcoinBackupDir     (default "/backups/bitcoin")
BTC_BACKUP_ARCHIVE  → deps.BitcoinBackupArchive  (default "/backups/bitcoin/archive")
```

Validate at startup (when BitcoinEnabled=true):
- Both paths must be non-empty.
- They must not be equal (copy must move the file).

### Step 4c — Wire in `bitcoin.Routes()`

In `internal/domain/bitcoin/routes.go`, after `txstatus.Routes(ctx, r, deps)`:

```go
// Wallet backup background service (no HTTP routes).
backupStore := backup.NewStore(deps.Pool)
backupInterval := 4 * time.Hour
if deps.BitcoinNetwork == "testnet4" {
    backupInterval = 24 * time.Hour
}
backupSvc := backup.NewService(
    backup.Config{
        Network:    deps.BitcoinNetwork,
        BackupDir:  deps.BitcoinBackupDir,
        ArchiveDir: deps.BitcoinBackupArchive,
        Interval:   backupInterval,
        OverdueThreshold: backupInterval,
    },
    deps.BitcoinRPC,
    backupStore,
    deps.Metrics,
)
deps.BitcoinBackupShutdown = backupSvc.Start(ctx)
```

Add import: `"github.com/7-Dany/store/backend/internal/domain/bitcoin/backup"`

### Step 4d — Wire shutdown in server.go

In `server.go`'s cleanup/shutdown sequence, after `deps.BitcoinEventsShutdown()`:
```go
if deps.BitcoinBackupShutdown != nil {
    deps.BitcoinBackupShutdown()
}
```

### Validation after Phase 4
- `go build ./...` must pass.
- Server startup with `BitcoinEnabled=false` must not call backup code.
- Server startup with `BitcoinEnabled=true` and `BTC_BACKUP_DIR` + `BTC_BACKUP_ARCHIVE`
  set must start both backup goroutines.

---

## Phase 5 — Tests

**Goal:** Implement the test cases from TI-22 in `wallet-backup-technical.md §10`.

### Test file locations
```
internal/domain/bitcoin/backup/service_test.go   — UNIT + INTG tests (TI-22-01 to -12, -16)
internal/domain/bitcoin/backup/store_test.go     — INTG store tests
```

MANUAL tests (TI-22-13 to -15, -17) are operational drills; no test file.

### Test structure

Service tests use a `FakeStorer` (defined in the test file) and a mock `BackupRPCClient`.
For INTG tests, use a real pgxpool connected to the test database.

**`FakeStorer`** fields:
```go
type FakeStorer struct {
    InsertErr          error
    GetLatestErr       error
    GetLatestResult    db.WalletBackupSuccess
    CountResult        int64
    InsertedParams     []backup.InsertBackupSuccessParams
}
```

**`FakeRPCClient`** fields:
```go
type FakeRPCClient struct {
    BackupWalletErr         error
    ListLabelAddressesResult []string
    ListLabelAddressesErr    error
    WalletInfoResult         rpc.WalletInfo
    WalletInfoErr            error
    KeypoolRefillErr         error
}
```

**`FakeRecorder`** (embed `bitcoinshared.NoopBitcoinRecorder`, override relevant methods):
```go
type FakeRecorder struct {
    bitcoinshared.NoopBitcoinRecorder
    BackupFailed            []string  // collected reason args
    BackupCopyFailed        int
    KeypoolWarnings         int
    KeypoolCriticals        int
    LabelMismatches         int
    BackupSuccesses         int
    BackupAgeSecs           float64
}
```

### Key test cases to implement

**TI-22-02** `TestBackup_SuccessRecord_Written_OnlyAfterCopyCompletes`:
- Set up temp dirs for BackupDir and ArchiveDir.
- Call `svc.runBackupCycle(ctx)`.
- Assert `FakeStorer.InsertedParams` has exactly one entry.
- Inject a copy failure via a patched `copyFile`; assert no entry in InsertedParams.

**TI-22-03** `TestBackup_CopyStep_Failure_CRITICAL_Alert_Immediate`:
- Inject copy failure.
- Assert `rec.BackupCopyFailed == 1` immediately after `runBackupCycle`.
- Assert retryTimer != nil (early retry scheduled).

**TI-22-04** `TestBackup_OverdueAlert_BasedOn_SuccessRecord_Not_JobRun`:
- Set `FakeStorer.GetLatestResult.Timestamp = time.Now().Add(-5 * time.Hour)`.
- Backup interval = 4h; call the age-check path.
- Assert `rec.BackupAgeSecs > 4*3600`.

**TI-22-05/06** `TestBackup_Keypool*`:
- Set `FakeRPCClient.WalletInfoResult.KeypoolSize` to 50 (warning) or 5 (critical).
- Call `svc.runKeypoolCheck(ctx)`.
- Assert correct recorder calls.
- Assert `KeypoolRefill` was called on the RPC mock.

**TI-22-07/08** Label integrity tests:
- Match: wallet count = DB count → no mismatch recorded.
- Mismatch: wallet count ≠ DB count → `rec.LabelMismatches == 1`.

**TI-22-09/10** MAX(id) monotonic tests:
- These cover DB backup integrity, not the Go service directly.
- Test is a DB-level INTG test: insert two backup records and verify timestamps
  are strictly increasing. The Go query `GetLatestWalletBackupSuccess` must
  return the later record.

**TI-22-11/12** Descriptor range tests:
- These are UNIT tests on the descriptor import formula in `wallet-backup-feature.md §Scenario C`.
- Verify the formula `max(200000, ceil(DB_MAX * 1.2))` is never hardcoded as 200000.
- These tests belong in a small utility function in the backup package:
  `ComputeDescriptorImportRange(dbMax int64) int64`
  Add this function to `service.go` (or `models.go`) and test it in `service_test.go`.

**TI-22-16** `TestBackup_PruneWindowCheck_AtStartup`:
- This test verifies the prune height startup check (part of the `events` or `watch`
  domain startup, not the backup domain). Check where `BTC_RECONCILIATION_START_HEIGHT`
  vs `pruneheight` is validated — implement the check there, not in backup.
  This test is noted here for completeness but tracked in the events/watch domain.

**TI-22-17** `TestBackup_RpcauthSet_RpcpasswordNotSet`:
- Manual config verification. Not a Go unit test.

### `ComputeDescriptorImportRange` function (for TI-22-11/12)

Add to `internal/domain/bitcoin/backup/service.go`:
```go
const minDescriptorImportRange = 200_000

// ComputeDescriptorImportRange computes the descriptor import range for
// Scenario C recovery. The range is the DB max derivation index × 1.2,
// with a minimum floor of 200,000.
//
// IMPORTANT: the hardcoded 200,000 value is the FLOOR only and must NEVER
// be used as the range on a live deployment. Always pass the actual DB max
// from invoice_addresses.hd_derivation_index. This function is exported for
// testing (TI-22-11, TI-22-12).
func ComputeDescriptorImportRange(dbMax int64) int64 {
    computed := int64(math.Ceil(float64(dbMax) * 1.2))
    if computed < minDescriptorImportRange {
        return minDescriptorImportRange
    }
    return computed
}
```

---

## Phase 6 — Design Doc Updates + Project Map

**Goal:** Mark decisions in the existing docs and update project-map.md.

### Step 6a — Update `wallet-backup-technical.md`

Add a new section `§11 — Go Implementation Reference` at the end:

```markdown
## §11 — Go Implementation Reference

| Concern | Location |
|---|---|
| Backup service | `internal/domain/bitcoin/backup/service.go` |
| DB store | `internal/domain/bitcoin/backup/store.go` |
| Models + config | `internal/domain/bitcoin/backup/models.go` |
| Errors | `internal/domain/bitcoin/backup/errors.go` |
| RPC methods | `internal/platform/bitcoin/rpc/client.go` (`BackupWallet`, `ListLabelAddresses`, `ListDescriptors`) |
| RPC types | `internal/platform/bitcoin/rpc/types.go` (`DescriptorList`, `Descriptor`) |
| SQL queries | `sql/queries/btc.sql` (`InsertWalletBackupSuccess`, `GetLatestWalletBackupSuccess`, `CountInvoiceAddressesByNetwork`) |
| Metrics interface | `internal/domain/bitcoin/shared/recorder.go` |
| Metrics hooks | `internal/platform/telemetry/bitcoin_hooks.go` |
| Deps fields | `internal/app/deps.go` (`BitcoinBackupDir`, `BitcoinBackupArchive`, `BitcoinBackupShutdown`) |
| Wiring | `internal/domain/bitcoin/routes.go` |
| `ComputeDescriptorImportRange` | `internal/domain/bitcoin/backup/service.go` |

Backup interval: 4h mainnet / 24h testnet4. Computed from `deps.BitcoinNetwork` at startup.
Copy step: local filesystem copy (co-located deployment). Cloud storage is a future concern.
```

### Step 6b — Update `project-map.md`

In section `1 — Domain → Package → Files`, under `internal/domain/bitcoin/`:

Add a new row to the bitcoin table:

```
| `bitcoin/backup` | — (no HTTP endpoint) | service, store, models, errors, export_test |
```

In section `6 — SQL File Map`, under the btc.sql line, append:
```
  btc.sql sections:
    ...
    Wallet backup  — InsertWalletBackupSuccess, GetLatestWalletBackupSuccess,
                     CountInvoiceAddressesByNetwork
```

Add to the `app.Deps` quick-reference in `project-map.md`:
```
BitcoinBackupDir     string   — BTC_BACKUP_DIR (backupwallet output directory)
BitcoinBackupArchive string   — BTC_BACKUP_ARCHIVE (archive directory after copy)
BitcoinBackupShutdown func()  — drains backup goroutines
```

---

## Phase Execution Order and Dependencies

```
Phase 1 (RPC)    ──► Phase 3a (extend recorder) ──► Phase 3b (domain package)
Phase 2 (SQL)    ──► Phase 3b (domain package)
Phase 3 (domain) ──► Phase 4 (wiring)
Phase 4 (wiring) ──► Phase 5 (tests)
Phase 5 (tests)  ──► Phase 6 (docs)
```

Phase 1 and Phase 2 have no dependency on each other and can be done in either order.
Phase 3a (recorder extension) must precede Phase 3b (service) because the service
depends on the extended `BitcoinRecorder`.

---

## Open Questions (none — all resolved)

| # | Question | Resolution |
|---|---|---|
| OQ-01 | Should backup use worker.Dispatcher or internal goroutines? | Internal goroutines (same pattern as watch/service.go reconciliation loop). The worker.Dispatcher is for one-shot jobs; backup is a long-running loop. |
| OQ-02 | Where is the cloud storage copy hook? | Not implemented. Co-located filesystem copy only. Cloud storage: future concern, add a `Copier` interface at that time. |
| OQ-03 | Should retention policy be in Go? | No. OS-level cron or storage lifecycle rule. Go service writes only; never deletes. |
| OQ-04 | Should backupwallet be retried on network error? | No. `backupwallet` writes a file; retrying may silently overwrite a partial write. The outer 15-minute early retry handles transient RPC failures. |
| OQ-05 | Does the prune height check belong in the backup domain? | No. It belongs in the startup check that runs before serving traffic (events or a dedicated startup health check). TI-22-16 is tracked in the events domain. |
| OQ-06 | Should the label integrity check block on mismatch? | No. It fires an alert only. The operator investigates. Blocking would prevent future backups from running. |
| OQ-07 | What happens on `GetLatestWalletBackupSuccess` returning ErrNoBackupRecord? | Skip the overdue check. The backup has never run. This is expected on a fresh deployment. |
| OQ-08 | Are the `DescriptorList`/`Descriptor` types needed in Phase 1 even if `ListDescriptors` isn't called by the backup service? | Yes. Add them. The backup feature doc references descriptor export for Scenario C recovery. The function exists for ops tooling even if the background job doesn't call it automatically. |
