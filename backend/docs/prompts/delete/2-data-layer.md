# §B-3 Delete Account — Stage 2: Data Layer

**Feature:** Delete Account (§B-3)
**Package:** `internal/domain/profile/delete-account/` (package `deleteaccount`)
**Depends on:** Stage 1 complete — `make sqlc` run, `internal/db/auth.sql.go` and `internal/db/worker.sql.go` regenerated, all packages compile.

---

## Read first (before writing any code)

| File | What to extract |
|---|---|
| `docs/prompts/delete/context.md` | Resolved paths, decisions, sentinel names |
| `internal/db/auth.sql.go` | Generated signatures for all 7 new delete-account queries + modified GetUserForLogin |
| `internal/db/worker.sql.go` | Generated signatures for GetAccountsDueForPurge, HardDeleteUser, InsertPurgeLog |
| `internal/domain/profile/delete-account/service.go` | Storer interface (if it exists from a prior partial implementation) |
| `internal/domain/profile/set-password/store.go` | BaseStore usage pattern — WithQuerier, BeginOrBind, ToPgtypeUUID, mustParseUserID |
| `internal/domain/auth/shared/testutil/fake_storer.go` | Existing FakeStorer layout — append new entry at end |
| `internal/domain/auth/shared/testutil/querier_proxy.go` | Existing QuerierProxy layout — append new Fail* fields + overrides at end |
| `docs/RULES.md §3.3` | Layer type rules — store shapes |
| `docs/RULES.md §3.4` | Error handling — %w wrapping, sentinel package ownership |

---

## Deliverables

### 1. `internal/domain/profile/delete-account/store.go`

Implement all Storer methods. The Storer interface must be declared here (or in `service.go` — check which pattern the analogous `set-password` package uses and match it).

**Storer interface** (declare in `service.go` per the project pattern; implement in `store.go`):

```go
type Storer interface {
    GetUserForDeletion(ctx context.Context, userID [16]byte) (DeletionUser, error)
    GetUserAuthMethods(ctx context.Context, userID [16]byte) (UserAuthMethods, error)
    GetIdentityByUserAndProvider(ctx context.Context, userID [16]byte, provider string) (string, error) // returns provider_uid
    ScheduleDeletionTx(ctx context.Context, in ScheduleDeletionInput) (DeletionScheduled, error)
    SendDeletionOTPTx(ctx context.Context, in SendDeletionOTPInput) (SendDeletionOTPResult, error)
    GetAccountDeletionToken(ctx context.Context, userID [16]byte) (authshared.VerificationToken, error)
    IncrementAttemptsTx(ctx context.Context, in authshared.IncrementInput) error
    ConfirmOTPDeletionTx(ctx context.Context, in ScheduleDeletionInput, tokenID [16]byte) (DeletionScheduled, error)
    CancelDeletionTx(ctx context.Context, in CancelDeletionInput) error
}
```

**Implementation notes:**

- `GetUserForDeletion` — maps `db.GetUserForDeletionRow` → `DeletionUser`. Maps no-rows to `profileshared.ErrUserNotFound`. Maps `deleted_at` pgtype.Timestamptz → `*time.Time`. Maps `email` pgtype.Text → `*string`.

- `GetUserAuthMethods` — calls `db.GetUserAuthMethods`. Maps to `UserAuthMethods{HasPassword, IdentityCount}`. Verify the generated signature in `internal/db/auth.sql.go` (this query should already exist from oauth work or needs to be added — check first). If not present, add it to `sql/queries/oauth.sql` and re-run `make sqlc`.

- `GetIdentityByUserAndProvider` — calls `db.GetIdentityByUserAndProvider` (verify existence in generated code first). Returns `provider_uid` string. Maps no-rows to `authshared.ErrUserNotFound`.

- `ScheduleDeletionTx` — transaction:
  1. `ScheduleUserDeletion(userID)` → returns `deleted_at`; maps no-rows to `profileshared.ErrUserNotFound`
  2. `InsertAuditLog(EventAccountDeletionRequested)` — using `context.WithoutCancel` (D-02 / §3.6)
  3. Commit
  Returns `DeletionScheduled{ScheduledDeletionAt: deletedAt.Add(30 * 24 * time.Hour)}`.

- `SendDeletionOTPTx` — transaction:
  1. `InvalidateUserDeletionTokens(userID)`
  2. `authshared.GenerateCodeHash()` → rawCode, codeHash
  3. `CreateAccountDeletionToken(userID, email, codeHash, ttl_seconds)` — use `deps.OTPTokenTTL.Seconds()`
  4. `InsertAuditLog(EventAccountDeletionOTPRequested)` — using `context.WithoutCancel`
  5. Commit
  Returns the raw OTP code in a result struct so the service can enqueue the email.
  **Add a `SendDeletionOTPResult` type to `models.go`**: `type SendDeletionOTPResult struct { RawCode string }`.

- `GetAccountDeletionToken` — calls `db.GetAccountDeletionToken`. Maps to `authshared.VerificationToken`. Maps no-rows to `authshared.ErrTokenNotFound`. The `FOR UPDATE` lock is acquired by the query; the caller must be inside a transaction or this will panic — document this in a comment.

- `IncrementAttemptsTx` — reuse the same pattern as other OTP stores (e.g., `password` store). Calls `db.IncrementVerificationAttempts` + `InsertAuditLog` in a transaction. Use `context.WithoutCancel` on the audit write.

- `ConfirmOTPDeletionTx` — transaction:
  1. `GetAccountDeletionToken(userID)` FOR UPDATE
  2. `ConsumeAccountDeletionToken(tokenID)` → rows affected; 0 → `authshared.ErrTokenAlreadyUsed`
  3. `ScheduleUserDeletion(userID)` RETURNING deleted_at
  4. `InsertAuditLog(EventAccountDeletionRequested)` — `context.WithoutCancel`
  5. Commit
  Returns `DeletionScheduled`.

- `CancelDeletionTx` — transaction:
  1. `CancelUserDeletion(userID)` → rowsAffected; 0 → rollback + `ErrNotPendingDeletion`
  2. `InsertAuditLog(EventAccountDeletionCancelled)` — `context.WithoutCancel`
  3. Commit

**Error wrapping convention** (RULES.md §3.4): every `fmt.Errorf` must use `%w` and prefix with `"deleteaccount.{MethodName}:"`.

---

### 2. Add `DeleteAccountFakeStorer` to `internal/domain/auth/shared/testutil/fake_storer.go`

Append at the end of the file. Follow the exact pattern of `SetPasswordFakeStorer` directly above:
- One `Fn` field per Storer method
- Compile-time interface check `var _ deleteaccount.Storer = (*DeleteAccountFakeStorer)(nil)`
- Each method: delegate to `Fn` if non-nil, else return zero value + nil error

Import: `deleteaccount "github.com/7-Dany/store/backend/internal/domain/profile/delete-account"`

```go
// ─────────────────────────────────────────────────────────────────────────────
// DeleteAccountFakeStorer
// ─────────────────────────────────────────────────────────────────────────────

type DeleteAccountFakeStorer struct {
    GetUserForDeletionFn           func(ctx context.Context, userID [16]byte) (deleteaccount.DeletionUser, error)
    GetUserAuthMethodsFn           func(ctx context.Context, userID [16]byte) (deleteaccount.UserAuthMethods, error)
    GetIdentityByUserAndProviderFn func(ctx context.Context, userID [16]byte, provider string) (string, error)
    ScheduleDeletionTxFn           func(ctx context.Context, in deleteaccount.ScheduleDeletionInput) (deleteaccount.DeletionScheduled, error)
    SendDeletionOTPTxFn            func(ctx context.Context, in deleteaccount.SendDeletionOTPInput) (deleteaccount.SendDeletionOTPResult, error) // returns raw OTP code in result
    GetAccountDeletionTokenFn      func(ctx context.Context, userID [16]byte) (authshared.VerificationToken, error)
    IncrementAttemptsTxFn          func(ctx context.Context, in authshared.IncrementInput) error
    ConfirmOTPDeletionTxFn         func(ctx context.Context, in deleteaccount.ScheduleDeletionInput, tokenID [16]byte) (deleteaccount.DeletionScheduled, error)
    CancelDeletionTxFn             func(ctx context.Context, in deleteaccount.CancelDeletionInput) error
}
```

Add compile-time check and all method delegations.

---

### 3. Add delete-account Fail* fields to `internal/domain/auth/shared/testutil/querier_proxy.go`

Append these fields to the `QuerierProxy` struct (in the `// ── delete account ──` section at the end of the struct fields block):

```go
// ── delete account ────────────────────────────────────────────────────────────
FailGetUserForDeletion            bool
FailScheduleUserDeletion          bool
ScheduleUserDeletionZero          bool // returns (time.Time{}, nil) simulating no-rows race
FailCancelUserDeletion            bool
CancelUserDeletionZero            bool // returns (0, nil) simulating not-pending
FailInvalidateUserDeletionTokens  bool
FailCreateAccountDeletionToken    bool
FailGetAccountDeletionToken       bool
FailConsumeAccountDeletionToken   bool
ConsumeAccountDeletionTokenZero   bool // returns (0, nil) simulating already-used
```

Add corresponding override methods at the end of the file, following the same pattern as the email-change section.

---

### 4. Worker DB helpers — `internal/worker/purge.go` (unexported helpers on `PurgeHandler`)

`PurgeHandler` was implemented in Stage 1 with two unexported helpers that need the
generated DB signatures to compile. Now that `make sqlc` has run, verify the generated
method names in `internal/db/worker.sql.go` and fill in the helpers:

```go
// getAccountsDueForPurge fetches up to 100 user IDs past their grace period.
func (h *PurgeHandler) getAccountsDueForPurge(ctx context.Context) ([]pgtype.UUID, error) {
    q := db.New(h.pool)
    return q.GetAccountsDueForPurge(ctx)
}

// purgeOne writes a purge-log record then hard-deletes the user in a single
// transaction. InsertPurgeLog must commit before HardDeleteUser (D-14 / D-15).
func (h *PurgeHandler) purgeOne(ctx context.Context, userID pgtype.UUID) error {
    tx, err := h.pool.Begin(ctx)
    if err != nil {
        return fmt.Errorf("purge.purgeOne: begin: %w", err)
    }
    defer tx.Rollback(ctx)

    q := db.New(tx)

    if err := q.InsertPurgeLog(ctx, db.InsertPurgeLogParams{
        UserID:   userID,
        Metadata: []byte(`{}`),
    }); err != nil {
        return fmt.Errorf("purge.purgeOne: insert log: %w", err)
    }
    if err := q.HardDeleteUser(ctx, userID); err != nil {
        return fmt.Errorf("purge.purgeOne: delete user: %w", err)
    }
    if err := tx.Commit(ctx); err != nil {
        return fmt.Errorf("purge.purgeOne: commit: %w", err)
    }
    slog.Info("purged account", "user_id", userID)
    return nil
}
```

No separate `PurgeQuerier` interface needed — the helpers call `db.New(...)` directly,
which is the standard pattern for background workers that own their own pool.

> **Reminder:** `PurgeHandler` implements `jobqueue.Handler`. Once `internal/platform/jobqueue`
> exists (job queue Phase 3), add the compile-time check:
> ```go
> var _ jobqueue.Handler = (*PurgeHandler)(nil)
> ```

---

## Run after implementing

```bash
go build ./internal/domain/profile/delete-account/...
go build ./internal/domain/auth/shared/testutil/...
go build ./internal/worker/...
go vet  ./internal/domain/profile/delete-account/...
go vet  ./internal/domain/auth/shared/testutil/...
```

All must pass with no errors.

---

## Stage 2 complete → proceed to Stage 3

Once all packages compile and vet clean, Stage 3 (Logic Layer) may begin.
Stage 3 prompt saved to: `docs/prompts/delete/3-logic-layer.md`
