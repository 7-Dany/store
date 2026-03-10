# §B-3 Delete Account — Stage 3: Logic Layer

**Feature:** Delete Account (§B-3)
**Package:** `internal/domain/profile/delete-account/` (package `deleteaccount`)
**Depends on:** Stage 2 complete — all packages compile and vet clean.

---

## Read first (before writing any code)

| File | What to extract |
|---|---|
| `docs/prompts/delete/context.md` | Resolved paths, decisions, sentinel names |
| `internal/domain/profile/delete-account/service.go` | Existing Storer interface + bare Service struct |
| `internal/domain/profile/delete-account/models.go` | All input/result types — note PasswordHash is NOT in DeletionUser (see §Required fix below) |
| `internal/domain/profile/delete-account/errors.go` | ErrAlreadyPendingDeletion, ErrNotPendingDeletion, ErrInvalidTelegramAuth, ErrTelegramIdentityMismatch |
| `internal/domain/profile/set-password/service.go` | Pattern for guard ordering, authshared.ParseUserID usage, error wrapping |
| `internal/domain/auth/shared/testutil/fake_servicer.go` | Tail 60 lines — SetPasswordFakeServicer + EmailChangeFakeServicer patterns |
| `docs/RULES.md §3.3` | Layer rules — service has no knowledge of pgtype, pgxpool, JWT signing |
| `docs/RULES.md §3.4` | Error wrapping — `%w` + `"deleteaccount.MethodName:"` prefix |

---

## Required fix before implementing service methods

`DeletionUser` does not include the password hash, which `DeleteWithPassword` needs.
Make these two small changes **before** writing service methods:

### 1. `internal/domain/profile/delete-account/models.go`

Add `PasswordHash *string` to `DeletionUser`:

```go
type DeletionUser struct {
    ID           [16]byte
    Email        *string    // nil for Telegram-only accounts
    PasswordHash *string    // nil for OAuth-only accounts
    DeletedAt    *time.Time // non-nil if deletion is already pending
}
```

### 2. `internal/domain/profile/delete-account/store.go`

Update `GetUserForDeletion` to map `row.PasswordHash` → `*string`:

```go
func (s *Store) GetUserForDeletion(ctx context.Context, userID [16]byte) (DeletionUser, error) {
    row, err := s.Queries.GetUserForDeletion(ctx, s.ToPgtypeUUID(userID))
    if err != nil {
        if s.IsNoRows(err) {
            return DeletionUser{}, profileshared.ErrUserNotFound
        }
        return DeletionUser{}, fmt.Errorf("deleteaccount.GetUserForDeletion: query: %w", err)
    }
    var email *string
    if row.Email.Valid {
        email = &row.Email.String
    }
    var passwordHash *string
    if row.PasswordHash.Valid {
        passwordHash = &row.PasswordHash.String
    }
    return DeletionUser{
        ID:           [16]byte(row.ID),
        Email:        email,
        PasswordHash: passwordHash,
        DeletedAt:    row.DeletedAt,
    }, nil
}
```

> **Note:** The generated `db.GetUserForDeletionRow` must include a `PasswordHash pgtype.Text`
> field. Verify in `internal/db/auth.sql.go`. If the SQL query does not SELECT `password_hash`,
> update `sql/queries/auth.sql` and re-run `make sqlc` before proceeding.

Also add `DeleteWithPasswordInput` to `models.go`:

```go
// DeleteWithPasswordInput holds the caller-supplied data for service.DeleteWithPassword.
type DeleteWithPasswordInput struct {
    UserID    string
    Password  string
    IPAddress string
    UserAgent string
}
```

---

## Deliverables

### 1. Declare `Servicer` interface and expand `Service` struct in `service.go`

Add the `Servicer` interface above the `Service` struct. Declare it in `service.go`
(same file as the `Storer` interface), following the project pattern.

```go
// Servicer is the subset of service methods the Handler requires.
// *Service satisfies this interface; tests supply DeleteAccountFakeServicer.
type Servicer interface {
    // ResolveUserForDeletion fetches the user and auth methods for empty-body dispatch.
    // The handler calls this when no password, code, or telegram_auth is present
    // to determine whether to trigger email-OTP (Path B step 1) or respond with
    // the Telegram widget prompt (Path C step 1).
    // Returns ErrAlreadyPendingDeletion when deleted_at is already set.
    ResolveUserForDeletion(ctx context.Context, userID string) (DeletionUser, UserAuthMethods, error)

    // DeleteWithPassword completes soft-deletion for a password-authenticated user (Path A).
    // Returns ErrAlreadyPendingDeletion (409), ErrInvalidCredentials (401).
    DeleteWithPassword(ctx context.Context, in DeleteWithPasswordInput) (DeletionScheduled, error)

    // InitiateEmailDeletion triggers the email-OTP flow (Path B step 1).
    // Returns OTPIssuanceResult so the handler can enqueue the email.
    // Returns ErrAlreadyPendingDeletion (409).
    InitiateEmailDeletion(ctx context.Context, in ScheduleDeletionInput) (authshared.OTPIssuanceResult, error)

    // ConfirmEmailDeletion validates the OTP and completes soft-deletion (Path B step 2).
    // Returns ErrAlreadyPendingDeletion (409), ErrTokenNotFound (422), ErrTooManyAttempts (429),
    // ErrInvalidCode (422), ErrTokenAlreadyUsed (422).
    ConfirmEmailDeletion(ctx context.Context, in ConfirmOTPDeletionInput) (DeletionScheduled, error)

    // ConfirmTelegramDeletion validates HMAC re-auth and completes soft-deletion (Path C step 2).
    // Returns ErrAlreadyPendingDeletion (409), ErrInvalidTelegramAuth (401),
    // ErrTelegramIdentityMismatch (401).
    ConfirmTelegramDeletion(ctx context.Context, in ConfirmTelegramDeletionInput) (DeletionScheduled, error)

    // CancelDeletion clears deleted_at for a pending-deletion account (POST /me/cancel-deletion).
    // Returns ErrNotPendingDeletion (409).
    CancelDeletion(ctx context.Context, in CancelDeletionInput) error
}
```

Expand `Service` struct and `NewService`:

```go
// Service is the business-logic layer for DELETE /me and POST /me/cancel-deletion.
type Service struct {
    store            Storer
    otpTTL           time.Duration // from deps.OTPTokenTTL
    telegramBotToken string        // from deps config; used for HMAC verification
}

// NewService constructs a Service backed by s.
// otpTTL controls the lifetime of account_deletion OTP tokens (same as all OTP flows).
// telegramBotToken is the Telegram Bot API token used to verify HMAC re-auth payloads.
func NewService(s Storer, otpTTL time.Duration, telegramBotToken string) *Service {
    return &Service{store: s, otpTTL: otpTTL, telegramBotToken: telegramBotToken}
}
```

Add compile-time check:
```go
var _ Servicer = (*Service)(nil)
```

---

### 2. Implement service methods

Implement all six methods on `*Service`. Place them after `NewService`.

**Error wrapping convention:** prefix all `fmt.Errorf` calls with `"deleteaccount.{MethodName}:"`.

#### `ResolveUserForDeletion`

```
Guard ordering (§5 paths, shared prefix):
1. ParseUserID("deleteaccount.ResolveUserForDeletion", userID) → wrap error
2. store.GetUserForDeletion(ctx, uid)
   - ErrUserNotFound → wrap as 500 (JWT user must exist)
   - other errors → wrap
3. user.DeletedAt != nil → return ErrAlreadyPendingDeletion
4. store.GetUserAuthMethods(ctx, uid)
   - errors → wrap
5. return (user, authMethods, nil)
```

#### `DeleteWithPassword`

```
Guard ordering (§5 Path A):
1. ParseUserID → wrap
2. store.GetUserForDeletion(ctx, uid) → ErrUserNotFound wraps as 500; other errors wrap
3. user.DeletedAt != nil → ErrAlreadyPendingDeletion
4. user.PasswordHash == nil → authshared.ErrInvalidCredentials
   (no hash means not a password account; treat as wrong credentials, not a 400)
5. authshared.CheckPassword(*user.PasswordHash, in.Password)
   - error → authshared.ErrInvalidCredentials (CheckPassword already returns this on mismatch)
6. store.ScheduleDeletionTx(ctx, ScheduleDeletionInput{UserID, IPAddress, UserAgent})
   - ErrUserNotFound → wrap as 500
   - other errors → wrap
7. return result, nil
```

#### `InitiateEmailDeletion`

```
Guard ordering (§5 Path B step 1):
1. ParseUserID → wrap
2. store.GetUserForDeletion(ctx, uid)
   - ErrUserNotFound → wrap as 500
   - other errors → wrap
3. user.DeletedAt != nil → ErrAlreadyPendingDeletion
4. user.Email == nil → wrap as 500 (handler only routes here when email is non-nil)
5. store.SendDeletionOTPTx(ctx, SendDeletionOTPInput{
       UserID:     in.UserID,
       Email:      *user.Email,
       TTLSeconds: s.otpTTL.Seconds(),
       IPAddress:  in.IPAddress,
       UserAgent:  in.UserAgent,
   }) → wrap errors
6. return authshared.NewOTPIssuanceResult(in.UserID, *user.Email, result.RawCode), nil
```

#### `ConfirmEmailDeletion`

```
Guard ordering (§5 Path B step 2):
1. ParseUserID → wrap
2. store.GetUserForDeletion(ctx, uid)
   - ErrUserNotFound → wrap as 500
   - other errors → wrap
3. user.DeletedAt != nil → ErrAlreadyPendingDeletion
4. validateOTPCode(in.Code) → return error as-is (already an authshared sentinel)
5. store.GetAccountDeletionToken(ctx, uid)
   - authshared.ErrTokenNotFound → return as-is
   - other errors → wrap
6. authshared.CheckOTPToken(token, in.Code, time.Now())
   - returns ErrTokenNotFound (expired), ErrTooManyAttempts, ErrInvalidCode
   - on ErrInvalidCode:
       a. store.IncrementAttemptsTx(ctx, authshared.IncrementInput{...})  → wrap errors
       b. return authshared.ErrInvalidCode
   - other errors → return as-is (already sentinels)
7. store.ConfirmOTPDeletionTx(ctx, ScheduleDeletionInput{UserID, IPAddress, UserAgent}, token.ID)
   - authshared.ErrTokenAlreadyUsed → return as-is
   - ErrUserNotFound → wrap as 500
   - other errors → wrap
8. return result, nil
```

**`IncrementInput` construction:**
```go
authshared.IncrementInput{
    TokenID:      token.ID,
    UserID:       uid,
    Attempts:     token.Attempts,
    MaxAttempts:  token.MaxAttempts,
    IPAddress:    in.IPAddress,
    UserAgent:    in.UserAgent,
    AttemptEvent: audit.EventAccountDeletionOTPRequested,
}
```

#### `ConfirmTelegramDeletion`

```
Guard ordering (§5 Path C step 2):
1. ParseUserID → wrap
2. store.GetUserForDeletion(ctx, uid)
   - ErrUserNotFound → wrap as 500
   - other errors → wrap
3. user.DeletedAt != nil → ErrAlreadyPendingDeletion
4. validateTelegramAuthPayload(&in.TelegramAuth)  — already validates id/auth_date/hash present
5. Check replay: in.TelegramAuth.AuthDate > time.Now().Unix()-86400
   → false: return ErrInvalidTelegramAuth
6. verifyTelegramHMAC(s.telegramBotToken, in.TelegramAuth)
   → false: return ErrInvalidTelegramAuth
7. store.GetIdentityByUserAndProvider(ctx, uid, "telegram")
   - authshared.ErrUserNotFound → return authshared.ErrInvalidCredentials
     (no telegram identity linked — treat as unauthorised, not a 404)
   - other errors → wrap
8. providerUID != strconv.FormatInt(in.TelegramAuth.ID, 10)
   → ErrTelegramIdentityMismatch
9. store.ScheduleDeletionTx(ctx, ScheduleDeletionInput{UserID, IPAddress, UserAgent})
   - ErrUserNotFound → wrap as 500
   - other errors → wrap
10. return result, nil
```

#### `CancelDeletion`

```
Guard ordering (§5 POST /me/cancel-deletion):
1. ParseUserID → wrap
2. store.CancelDeletionTx(ctx, in)
   - ErrNotPendingDeletion → return as-is
   - other errors → wrap
3. return nil
```

Note: no `GetUserForDeletion` call here — the store's `CancelUserDeletion` returns 0 rows
when the user has no pending deletion, which `CancelDeletionTx` maps to `ErrNotPendingDeletion`.

---

### 3. Implement `verifyTelegramHMAC` (private function)

Add this as a package-level private function in `service.go` (or a new `telegram.go` file
in the same package if you prefer to keep the file focused).

The standard Telegram Login Widget HMAC algorithm:

```go
// verifyTelegramHMAC verifies that data is a genuine Telegram Login Widget payload
// signed by the bot identified by botToken.
//
// Algorithm (per Telegram docs):
//  1. Build check string: sort all non-hash fields as "key=value" pairs joined by "\n".
//  2. secretKey = SHA-256(botToken)   — raw bytes, NOT hex
//  3. sig = HMAC-SHA256(secretKey, checkString)
//  4. Return sig (hex) == p.Hash
func verifyTelegramHMAC(botToken string, p TelegramAuthPayload) bool {
    // 1. Build the sorted key=value check string.
    //    Include only non-empty fields; exclude "hash". auth_date is always included.
    parts := []string{
        fmt.Sprintf("auth_date=%d", p.AuthDate),
        fmt.Sprintf("id=%d", p.ID),
    }
    if p.FirstName != "" {
        parts = append(parts, fmt.Sprintf("first_name=%s", p.FirstName))
    }
    if p.PhotoURL != "" {
        parts = append(parts, fmt.Sprintf("photo_url=%s", p.PhotoURL))
    }
    if p.Username != "" {
        parts = append(parts, fmt.Sprintf("username=%s", p.Username))
    }
    sort.Strings(parts)
    checkString := strings.Join(parts, "\n")

    // 2. secret = SHA-256(bot token) — Telegram uses the raw hash bytes as the HMAC key.
    h := sha256.Sum256([]byte(botToken))

    // 3. HMAC-SHA256 over the check string using the secret key.
    mac := hmac.New(sha256.New, h[:])
    mac.Write([]byte(checkString))
    sig := hex.EncodeToString(mac.Sum(nil))

    // 4. Constant-time comparison.
    return hmac.Equal([]byte(sig), []byte(p.Hash))
}
```

Imports needed: `"crypto/hmac"`, `"crypto/sha256"`, `"encoding/hex"`, `"fmt"`, `"sort"`, `"strings"`.

> **TODO:** When `internal/domain/auth/oauth/telegram` is implemented (Group D), move
> `verifyTelegramHMAC` to `authshared` or the telegram package and update the import here.

---

### 4. Add `DeleteAccountFakeServicer` to `internal/domain/auth/shared/testutil/fake_servicer.go`

Append at the end of the file, following the `EmailChangeFakeServicer` pattern directly above.

```go
// ─────────────────────────────────────────────────────────────────────────────
// DeleteAccountFakeServicer
// ─────────────────────────────────────────────────────────────────────────────

// DeleteAccountFakeServicer is a hand-written implementation of deleteaccount.Servicer
// for handler unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns the zero value and nil error so tests only configure the fields
// they care about.
type DeleteAccountFakeServicer struct {
    ResolveUserForDeletionFn   func(ctx context.Context, userID string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error)
    DeleteWithPasswordFn       func(ctx context.Context, in deleteaccount.DeleteWithPasswordInput) (deleteaccount.DeletionScheduled, error)
    InitiateEmailDeletionFn    func(ctx context.Context, in deleteaccount.ScheduleDeletionInput) (authshared.OTPIssuanceResult, error)
    ConfirmEmailDeletionFn     func(ctx context.Context, in deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error)
    ConfirmTelegramDeletionFn  func(ctx context.Context, in deleteaccount.ConfirmTelegramDeletionInput) (deleteaccount.DeletionScheduled, error)
    CancelDeletionFn           func(ctx context.Context, in deleteaccount.CancelDeletionInput) error
}

// compile-time interface check.
var _ deleteaccount.Servicer = (*DeleteAccountFakeServicer)(nil)

func (f *DeleteAccountFakeServicer) ResolveUserForDeletion(ctx context.Context, userID string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
    if f.ResolveUserForDeletionFn != nil {
        return f.ResolveUserForDeletionFn(ctx, userID)
    }
    return deleteaccount.DeletionUser{}, deleteaccount.UserAuthMethods{}, nil
}

func (f *DeleteAccountFakeServicer) DeleteWithPassword(ctx context.Context, in deleteaccount.DeleteWithPasswordInput) (deleteaccount.DeletionScheduled, error) {
    if f.DeleteWithPasswordFn != nil {
        return f.DeleteWithPasswordFn(ctx, in)
    }
    return deleteaccount.DeletionScheduled{}, nil
}

func (f *DeleteAccountFakeServicer) InitiateEmailDeletion(ctx context.Context, in deleteaccount.ScheduleDeletionInput) (authshared.OTPIssuanceResult, error) {
    if f.InitiateEmailDeletionFn != nil {
        return f.InitiateEmailDeletionFn(ctx, in)
    }
    return authshared.OTPIssuanceResult{}, nil
}

func (f *DeleteAccountFakeServicer) ConfirmEmailDeletion(ctx context.Context, in deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
    if f.ConfirmEmailDeletionFn != nil {
        return f.ConfirmEmailDeletionFn(ctx, in)
    }
    return deleteaccount.DeletionScheduled{}, nil
}

func (f *DeleteAccountFakeServicer) ConfirmTelegramDeletion(ctx context.Context, in deleteaccount.ConfirmTelegramDeletionInput) (deleteaccount.DeletionScheduled, error) {
    if f.ConfirmTelegramDeletionFn != nil {
        return f.ConfirmTelegramDeletionFn(ctx, in)
    }
    return deleteaccount.DeletionScheduled{}, nil
}

func (f *DeleteAccountFakeServicer) CancelDeletion(ctx context.Context, in deleteaccount.CancelDeletionInput) error {
    if f.CancelDeletionFn != nil {
        return f.CancelDeletionFn(ctx, in)
    }
    return nil
}
```

---

## Service test cases covered by this stage

From Stage 0 §9 — S-layer cases (implement in `service_test.go`):

| # | Case | Method | Setup | Expected |
|---|---|---|---|---|
| T-06 | Already pending deletion → ErrAlreadyPendingDeletion | all methods | GetUserForDeletionFn returns user with DeletedAt set | ErrAlreadyPendingDeletion |
| T-08 | Wrong password → ErrInvalidCredentials | DeleteWithPassword | GetUserForDeletionFn returns user with PasswordHash; CheckPassword fails | ErrInvalidCredentials |
| T-10 | No active token → ErrTokenNotFound | ConfirmEmailDeletion | GetAccountDeletionTokenFn returns ErrTokenNotFound | ErrTokenNotFound |
| T-11 | Token expired → ErrTokenNotFound | ConfirmEmailDeletion | token.ExpiresAt in past | ErrTokenNotFound |
| T-12 | Wrong code, attempts < max → ErrInvalidCode | ConfirmEmailDeletion | VerifyCodeHash fails; IncrementAttemptsTxFn called | ErrInvalidCode; increment called once |
| T-13 | Attempt ceiling → ErrTooManyAttempts | ConfirmEmailDeletion | token.Attempts == token.MaxAttempts | ErrTooManyAttempts; increment NOT called |
| T-14 | HMAC fails → ErrInvalidTelegramAuth | ConfirmTelegramDeletion | verifyTelegramHMAC returns false | ErrInvalidTelegramAuth |
| T-16 | provider_uid mismatch → ErrTelegramIdentityMismatch | ConfirmTelegramDeletion | GetIdentityByUserAndProviderFn returns different UID | ErrTelegramIdentityMismatch |
| T-21 | context.WithoutCancel on ScheduleDeletionTx writes | DeleteWithPassword / ConfirmEmailDeletion | inspect ctx passed to store Fn | ctx.Done() == nil |
| T-22 | context.WithoutCancel on SendDeletionOTPTx | InitiateEmailDeletion | inspect ctx | ctx.Done() == nil |
| T-23 | context.WithoutCancel on IncrementAttemptsTx | ConfirmEmailDeletion (wrong code) | inspect ctx | ctx.Done() == nil |
| T-24 | Store error in ScheduleDeletionTx → wrapped error | DeleteWithPassword | ScheduleDeletionTxFn returns error | error contains "deleteaccount.DeleteWithPassword:" |
| T-25 | Store error in SendDeletionOTPTx → wrapped error | InitiateEmailDeletion | SendDeletionOTPTxFn returns error | error contains "deleteaccount.InitiateEmailDeletion:" |
| T-28 | Not pending deletion → ErrNotPendingDeletion | CancelDeletion | CancelDeletionTxFn returns ErrNotPendingDeletion | ErrNotPendingDeletion |
| T-30 | context.WithoutCancel on CancelDeletionTx audit write | CancelDeletion | inspect ctx | ctx.Done() == nil |
| T-31 | Store error in CancelDeletionTx → wrapped error | CancelDeletion | CancelDeletionTxFn returns error | error contains "deleteaccount.CancelDeletion:" |

> Note: T-21/22/23/30 assert `context.WithoutCancel` behavior. In the service layer,
> you do NOT call `context.WithoutCancel` — that is done inside the store's Tx methods.
> These test cases verify the store methods are called (not skipped), not the ctx shape.
> The `context.WithoutCancel` tests are I-layer (integration) tests.

---

## Run after implementing

```bash
go build ./internal/domain/profile/delete-account/...
go build ./internal/domain/auth/shared/testutil/...
go vet  ./internal/domain/profile/delete-account/...
go vet  ./internal/domain/auth/shared/testutil/...
```

All must pass with no errors before proceeding to Stage 4.

---

## Stage 3 complete → proceed to Stage 4

Once all packages compile and vet clean, Stage 4 (HTTP Layer) may begin.
Stage 4 prompt saved to: `docs/prompts/delete/4-http-layer.md`
