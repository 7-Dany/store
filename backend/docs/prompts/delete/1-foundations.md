# §B-3 Delete Account — Stage 1: Foundations

**Feature:** Delete Account (§B-3)
**Package:** `internal/domain/profile/delete-account/` (package `deleteaccount`)
**Depends on:** Stage 0 approved (`docs/prompts/delete/0-design.md`)

---

## Read first (before writing any code)

| File | What to extract |
|---|---|
| `docs/prompts/delete/context.md` | Resolved paths, decisions, sentinel names, prefixes |
| `docs/prompts/delete/0-design.md` §3–§4 | Decisions + full data model |
| `sql/queries/auth.sql` (tail 60 lines) | Append position; confirm section style |
| `internal/audit/audit.go` | Full `const` block + `AllEvents()` — sync triad |
| `internal/domain/profile/set-password/models.go` | Model shape pattern |
| `internal/domain/profile/set-password/errors.go` | Sentinel style pattern |
| `docs/RULES.md §3.9` | SQL conventions |
| `docs/RULES.md §3.11` | Naming conventions |

---

## Deliverables

Implement all of the following. No new files outside this list.

> **Job queue forward-compatibility (D-21):** `PurgeHandler` must implement
> `jobqueue.Handler` (`Handle(ctx context.Context, job jobqueue.Job) error`) from
> this stage. The `jobqueue` package does not exist yet — import the interface
> type from `internal/platform/jobqueue` once Phase 3 of the job queue lands.
> For now, define the signature exactly and use a build tag or a stub if the
> package is not yet present. The `PurgeWorker` goroutine is the temporary
> trigger; it is the only thing removed in job queue Phase 7.

---

### 0. Worker kind constant — `internal/worker/kinds.go`

Create this file (or add to it if it already exists) defining the `KindPurgeAccounts`
constant. This is required now so that the job queue Phase 6 can import it without
adding a new constant at that time.

```go
package worker

import "github.com/7-Dany/store/backend/internal/platform/jobqueue"

const (
    // KindPurgeAccounts is the job kind that drives the hourly account purge.
    // Registered in server.go during job queue Phase 7; the constant is defined
    // here so PurgeHandler can reference it without a forward dependency.
    KindPurgeAccounts jobqueue.Kind = "purge_accounts"
)
```

> If `internal/platform/jobqueue` does not yet exist, use `type Kind = string`
> locally and replace the import when the package lands. The constant value
> `"purge_accounts"` must not change.

---

### 1. New migration — `sql/schema/00N_account_deletion.sql`

Find the correct migration number by listing `sql/schema/` and using the next available N.

```sql
-- Add account_deletion value to the one_time_token_type ENUM.
-- NOTE: ALTER TYPE ... ADD VALUE cannot run inside a transaction block.
-- goose uses separate Up/Down sections; this runs outside a txn automatically.
ALTER TYPE one_time_token_type ADD VALUE IF NOT EXISTS 'account_deletion';

-- account_purge_log records permanently purged accounts.
-- user_id intentionally has NO FK to users — the user row is deleted first (D-15).
CREATE TABLE IF NOT EXISTS account_purge_log (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL,
    purged_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata   JSONB       NOT NULL DEFAULT '{}'
);

COMMENT ON TABLE account_purge_log IS
    'Permanent record of hard-purged accounts. user_id has no FK constraint '
    'because the users row is deleted before this record is written.';
```

Use the correct goose migration header/footer format matching the existing migrations in `sql/schema/`.

---

### 2. New SQL queries — `sql/queries/auth.sql`

#### 2a. Modify `GetUserForLogin` (existing query)

Two additive changes only:
1. Add `deleted_at` to the SELECT column list.
2. Add to the WHERE clause: `AND (deleted_at IS NULL OR deleted_at > NOW() - INTERVAL '30 days')`.

#### 2b. Append a new `/* ── Delete Account ── */` section at the end of the file

Add these seven queries in order. Follow the existing section header style exactly.

```sql
/* ── Delete Account ── */

-- name: GetUserForDeletion :one
-- Returns id, email, and deleted_at for the authenticated user.
-- Returns no-rows for expired-grace-period accounts (deleted_at older than 30 days),
-- consistent with the login gate (D-05). The handler treats no-rows as a 500
-- (JWT user must always exist within the active window).
SELECT
    id,
    email,
    deleted_at
FROM users
WHERE id = @user_id::uuid
  AND (deleted_at IS NULL OR deleted_at > NOW() - INTERVAL '30 days');


-- name: ScheduleUserDeletion :one
-- Stamps deleted_at = NOW() for the given active user.
-- Returns deleted_at so the handler can compute scheduled_deletion_at = deleted_at + 30d.
UPDATE users
SET deleted_at = NOW()
WHERE id         = @user_id::uuid
  AND deleted_at IS NULL
RETURNING deleted_at;


-- name: CancelUserDeletion :execrows
-- Clears deleted_at for a pending-deletion user.
-- Returns rows-affected: 0 means the user was not pending deletion → 409 not_pending_deletion.
UPDATE users
SET deleted_at = NULL
WHERE id         = @user_id::uuid
  AND deleted_at IS NOT NULL;


-- name: InvalidateUserDeletionTokens :exec
-- Voids all unused account_deletion OTP tokens for this user before issuing a new one.
-- Prevents token accumulation from repeated step-1 calls.
UPDATE one_time_tokens
SET used_at = NOW()
WHERE user_id    = @user_id::uuid
  AND token_type = 'account_deletion'
  AND used_at    IS NULL;


-- name: CreateAccountDeletionToken :one
-- Issues a new account_deletion OTP token.
-- max_attempts = 3 (D-19). TTL is supplied by the service as @ttl_seconds.
INSERT INTO one_time_tokens (
    token_type,
    user_id,
    email,
    code_hash,
    expires_at,
    ip_address,
    max_attempts
)
VALUES (
    'account_deletion',
    @user_id::uuid,
    @email,
    @code_hash,
    NOW() + make_interval(secs => @ttl_seconds::float8),
    sqlc.narg('ip_address')::inet,
    3
)
RETURNING
    id,
    expires_at;


-- name: GetAccountDeletionToken :one
-- Fetches the active account_deletion token for the given user.
-- FOR UPDATE prevents concurrent double-consumption.
SELECT
    id,
    user_id,
    email,
    code_hash,
    attempts,
    max_attempts,
    expires_at,
    used_at
FROM one_time_tokens
WHERE user_id    = @user_id::uuid
  AND token_type = 'account_deletion'
  AND code_hash  IS NOT NULL
  AND used_at    IS NULL
ORDER BY created_at DESC, id DESC
LIMIT 1
FOR UPDATE;


-- name: ConsumeAccountDeletionToken :execrows
-- Marks the token as used. Returns rows-affected: 0 means already consumed.
UPDATE one_time_tokens
SET used_at = NOW()
WHERE id      = @id::uuid
  AND used_at IS NULL;
```

---

### 3. New SQL file — `sql/queries/worker.sql`

Create this new file with a section header matching the style in `auth.sql`.

```sql
/* ── Background purge worker ── */

-- name: GetAccountsDueForPurge :many
-- Returns up to 100 user IDs whose grace period has expired.
-- The worker processes these in a loop, purging each in its own transaction.
SELECT id
FROM users
WHERE deleted_at < NOW() - INTERVAL '30 days'
LIMIT 100;


-- name: HardDeleteUser :exec
-- Permanently deletes a user row. All child rows (refresh_tokens, user_sessions,
-- one_time_tokens, user_identities, auth_audit_log, user_roles) are removed via
-- CASCADE. Must be called AFTER InsertPurgeLog within the same transaction (D-14).
DELETE FROM users
WHERE id = @user_id::uuid;


-- name: InsertPurgeLog :exec
-- Writes a permanent record of the purge before the user row is deleted (D-15).
-- metadata is a JSONB blob — callers pass {"deleted_at": "<RFC3339>"} at minimum.
INSERT INTO account_purge_log (user_id, metadata)
VALUES (@user_id::uuid, @metadata::jsonb);
```

---

### 4. Audit events — `internal/audit/audit.go`

Add three new constants to the `const` block and three corresponding entries to `AllEvents()`. Place them after the existing OAuth events at the end of both the `const` block and the `AllEvents()` slice.

```go
// EventAccountDeletionRequested is emitted inside ScheduleDeletionTx after
// deleted_at is stamped. Written with context.WithoutCancel so a client
// disconnect cannot abort the write.
EventAccountDeletionRequested EventType = "account_deletion_requested"

// EventAccountDeletionOTPRequested is emitted inside SendDeletionOTPTx after
// the account_deletion OTP token is created and before the email is dispatched.
EventAccountDeletionOTPRequested EventType = "account_deletion_otp_requested"

// EventAccountDeletionCancelled is emitted inside CancelDeletionTx after
// deleted_at is cleared. Written with context.WithoutCancel.
EventAccountDeletionCancelled EventType = "account_deletion_cancelled"
```

---

### 5. Feature models — `internal/domain/profile/delete-account/models.go`

```go
// Package deleteaccount provides the HTTP handler, service, and store for
// DELETE /api/v1/profile/me (account deletion) and POST /api/v1/profile/me/cancel-deletion.
// The folder name uses a hyphen (delete-account) because "delete" is a Go keyword;
// the package name is deleteaccount (D-20).
package deleteaccount

import "time"

// DeletionUser is the minimal user view returned by store.GetUserForDeletion.
type DeletionUser struct {
    ID        [16]byte
    Email     *string    // nil for Telegram-only accounts
    DeletedAt *time.Time // non-nil if deletion is already pending
}

// UserAuthMethods holds the result of GetUserAuthMethods, used to dispatch
// the correct confirmation path in the handler (D-11).
type UserAuthMethods struct {
    HasPassword   bool
    IdentityCount int
}

// TelegramAuthPayload carries the Telegram Login Widget fields submitted
// by the client in step 2 of the Telegram confirmation path (D-08).
type TelegramAuthPayload struct {
    ID        int64  `json:"id"`
    FirstName string `json:"first_name"`
    Username  string `json:"username"`
    PhotoURL  string `json:"photo_url"`
    AuthDate  int64  `json:"auth_date"`
    Hash      string `json:"hash"`
}

// ScheduleDeletionInput holds the caller-supplied data for service.ScheduleDeletion.
type ScheduleDeletionInput struct {
    UserID    string
    IPAddress string
    UserAgent string
}

// SendDeletionOTPInput holds the caller-supplied data for service.SendDeletionOTP.
type SendDeletionOTPInput struct {
    UserID    string
    Email     string
    IPAddress string
    UserAgent string
}

// ConfirmOTPDeletionInput holds the caller-supplied data for service.ConfirmOTPDeletion.
type ConfirmOTPDeletionInput struct {
    UserID    string
    Code      string
    IPAddress string
    UserAgent string
}

// ConfirmTelegramDeletionInput holds the caller-supplied data for service.ConfirmTelegramDeletion.
type ConfirmTelegramDeletionInput struct {
    UserID       string
    TelegramAuth TelegramAuthPayload
    IPAddress    string
    UserAgent    string
}

// CancelDeletionInput holds the caller-supplied data for service.CancelDeletion.
type CancelDeletionInput struct {
    UserID    string
    IPAddress string
    UserAgent string
}

// DeletionScheduled is the result returned by ScheduleDeletion and Confirm* methods.
// ScheduledDeletionAt is deleted_at + 30 days.
type DeletionScheduled struct {
    ScheduledDeletionAt time.Time
}
```

---

### 6. Feature requests — `internal/domain/profile/delete-account/requests.go`

```go
package deleteaccount

// deleteAccountRequest is the JSON body for DELETE /me.
// All fields are optional; the handler dispatches based on which fields are present.
type deleteAccountRequest struct {
    Password     string               `json:"password"`
    Code         string               `json:"code"`
    TelegramAuth *TelegramAuthPayload `json:"telegram_auth"`
}

// cancelDeletionRequest is the JSON body for POST /me/cancel-deletion.
// No fields — the endpoint takes no body; the struct exists for DecodeJSON[T] consistency.
type cancelDeletionRequest struct{}
```

---

### 7. Feature validators — `internal/domain/profile/delete-account/validators.go`

```go
package deleteaccount

import (
    "fmt"
    "unicode"

    authshared "github.com/7-Dany/store/backend/internal/domain/auth/shared"
)

// validateOTPCode validates that the submitted OTP code is exactly 6 decimal digits.
// Returns authshared.ErrCodeInvalidFormat on failure so the handler maps it to
// 422 validation_error (consistent with all other OTP flows).
func validateOTPCode(code string) error {
    if len(code) != 6 {
        return authshared.ErrCodeInvalidFormat
    }
    for _, ch := range code {
        if !unicode.IsDigit(ch) {
            return authshared.ErrCodeInvalidFormat
        }
    }
    return nil
}

// validateTelegramAuthPayload checks that the mandatory Telegram fields are present.
// Returns a descriptive error mapped to 400 validation_error by the handler.
func validateTelegramAuthPayload(p *TelegramAuthPayload) error {
    if p == nil {
        return fmt.Errorf("telegram_auth is required")
    }
    if p.ID == 0 {
        return fmt.Errorf("telegram_auth.id is required")
    }
    if p.AuthDate == 0 {
        return fmt.Errorf("telegram_auth.auth_date is required")
    }
    if p.Hash == "" {
        return fmt.Errorf("telegram_auth.hash is required")
    }
    return nil
}
```

---

### 8. Feature errors — `internal/domain/profile/delete-account/errors.go`

```go
package deleteaccount

import "errors"

// ErrAlreadyPendingDeletion is returned when the user's deleted_at is already set.
// Maps to 409 already_pending_deletion.
var ErrAlreadyPendingDeletion = errors.New("account is already scheduled for deletion")

// ErrNotPendingDeletion is returned by CancelDeletion when deleted_at is NULL.
// Maps to 409 not_pending_deletion.
var ErrNotPendingDeletion = errors.New("account is not scheduled for deletion")

// ErrInvalidTelegramAuth is returned when Telegram HMAC verification fails
// or when auth_date is more than 86400 seconds old (replay protection).
// Maps to 401 invalid_telegram_auth.
var ErrInvalidTelegramAuth = errors.New("telegram authentication is invalid or expired")

// ErrTelegramIdentityMismatch is returned when the Telegram payload id does
// not match the provider_uid stored in user_identities for this user.
// Maps to 401 telegram_identity_mismatch.
var ErrTelegramIdentityMismatch = errors.New("telegram identity does not match linked account")
```

---

### 9. Additive changes to `auth/login`

#### `internal/domain/auth/login/models.go`

Add `DeletedAt *time.Time` to `LoginUser`. Add `ScheduledDeletionAt *time.Time` to `LoggedInSession` (computed as `deletedAt + 30 days` when non-nil).

Find the existing struct definitions and add the fields — do not restructure or reformat existing fields.

#### `internal/domain/auth/login/handler.go`

Find the login response struct (the anonymous or named struct used in `respond.JSON`) and add:

```go
ScheduledDeletionAt *time.Time `json:"scheduled_deletion_at,omitempty"`
```

Populate it from `session.ScheduledDeletionAt`.

#### `internal/domain/auth/login/service.go`

In the service method that constructs `LoggedInSession`, set `ScheduledDeletionAt`:

```go
if user.DeletedAt != nil {
    t := user.DeletedAt.Add(30 * 24 * time.Hour)
    session.ScheduledDeletionAt = &t
}
```

---

### 10. Purge worker — `internal/worker/purge.go`

Implement two things in this file. **Do not merge them** — keep them separate so
Phase 7 can delete `PurgeWorker` without touching `PurgeHandler`.

#### Part A — `PurgeHandler` (permanent; implements `jobqueue.Handler`)

```go
// PurgeHandler implements jobqueue.Handler for the "purge_accounts" kind.
// Core logic lives here so that the job queue can drive it after Phase 7
// without any refactoring (D-21).
type PurgeHandler struct {
    pool *pgxpool.Pool
}

func NewPurgeHandler(pool *pgxpool.Pool) *PurgeHandler {
    return &PurgeHandler{pool: pool}
}

// Handle purges all accounts whose grace period has expired.
// It loops internally until the batch is exhausted (< 100 rows returned),
// then returns nil. The job queue scheduler calls it once per hour;
// the PurgeWorker goroutine below calls it with a synthetic job until
// job queue Phase 7 is wired.
func (h *PurgeHandler) Handle(ctx context.Context, job jobqueue.Job) error {
    for {
        ids, err := h.getAccountsDueForPurge(ctx)
        if err != nil {
            return fmt.Errorf("purge.Handle: fetch batch: %w", err)
        }
        for _, userID := range ids {
            if err := h.purgeOne(ctx, userID); err != nil {
                slog.Error("purge: failed to purge account", "user_id", userID, "err", err)
                // continue — never abort the batch for a single failure
            }
        }
        if len(ids) < 100 {
            break // batch exhausted
        }
    }
    return nil
}
```

`getAccountsDueForPurge` and `purgeOne` (runs InsertPurgeLog + HardDeleteUser in a
transaction) are unexported helpers on `PurgeHandler`. They call the generated DB
methods directly via a `*pgxpool.Pool`.

#### Part B — `PurgeWorker` (temporary goroutine; removed in job queue Phase 7)

```go
// PurgeWorker is a thin goroutine wrapper around PurgeHandler.
// It runs until ctx is cancelled, calling Handle every hour.
// REMOVED in job queue Phase 7 — replaced by the purge_accounts_hourly schedule.
type PurgeWorker struct {
    handler *PurgeHandler
}

func NewPurgeWorker(pool *pgxpool.Pool) *PurgeWorker {
    return &PurgeWorker{handler: NewPurgeHandler(pool)}
}

func (w *PurgeWorker) Start(ctx context.Context) {
    ticker := time.NewTicker(time.Hour)
    defer ticker.Stop()
    for {
        _ = w.handler.Handle(ctx, jobqueue.Job{Kind: KindPurgeAccounts})
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
        }
    }
}
```

Register in `server.go` as today: `go worker.NewPurgeWorker(pool).Start(ctx)`.
The sole change in job queue Phase 7 is removing this `go` call and adding
`mgr.Register(worker.KindPurgeAccounts, worker.NewPurgeHandler(pool))`.

---

### 11. Additive changes to `profile/me`

#### `internal/domain/profile/me/models.go`

Add `ScheduledDeletionAt *time.Time` to `UserProfile`.

#### `internal/domain/profile/me/handler.go`

Find the GET /me response struct and add:

```go
ScheduledDeletionAt *time.Time `json:"scheduled_deletion_at,omitempty"`
```

Populate it from `profile.ScheduledDeletionAt`.

#### SQL — `sql/queries/auth.sql` (existing `GetUserProfile` query)

Add `deleted_at` to the SELECT list so the store can compute `ScheduledDeletionAt`.

#### `internal/domain/profile/me/store.go`

In the mapping from db row → `UserProfile`, add:

```go
if row.DeletedAt.Valid {
    t := row.DeletedAt.Time.Add(30 * 24 * time.Hour)
    profile.ScheduledDeletionAt = &t
}
```

---

## Run after implementing

```bash
make sqlc          # regenerates internal/db/ from all sql/queries/*.sql
go build ./...     # confirm all packages compile
go vet ./...       # no vet issues
```

Verify that:
- `internal/db/auth.sql.go` contains generated signatures for all 7 new delete-account queries and the modified `GetUserForLogin`.
- `internal/db/worker.sql.go` (new file) contains generated signatures for the 3 worker queries.
- `internal/audit/audit.go` compiles and `AllEvents()` includes the 3 new events.
- `internal/domain/auth/login` compiles with the new `DeletedAt` / `ScheduledDeletionAt` fields.
- `internal/domain/profile/me` compiles with the new `ScheduledDeletionAt` field.
- `internal/worker/kinds.go` compiles and exports `KindPurgeAccounts`.
- `go build ./internal/worker/...` passes (PurgeHandler + PurgeWorker both compile).

---

## Stage 1 complete → proceed to Stage 2

Once `go build ./...` and `go vet ./...` pass, Stage 2 (Data Layer) may begin.
Stage 2 prompt saved to: `docs/prompts/delete/2-data-layer.md`
