# Delete Account — Post-Review Fix Prompt

**Feature:** Delete Account (§B-3)
**Package:** `internal/domain/profile/delete-account/`
**Depends on:** Stage 5 complete. Based on the full review in `docs/prompts/delete/REVIEW.md`.
Apply every fix exactly as specified. Do not reformat or reorganise code outside
the targeted lines.

---

## Before you start

Read these files in order before touching anything:

| File | Why |
|---|---|
| `docs/RULES.md` | Full conventions reference |
| `docs/prompts/delete/context.md` | Resolved paths, decisions, sentinel names |
| `internal/domain/profile/delete-account/models.go` | Target for N-1 provider field |
| `internal/domain/profile/delete-account/service.go` | Several targets |
| `internal/domain/profile/delete-account/handler.go` | Several targets |
| `internal/domain/profile/delete-account/store.go` | All `fmt.Errorf` prefixes, audit provider, `// Unreachable:` |
| `internal/domain/profile/delete-account/validators.go` | Dead-code removal |
| `internal/domain/profile/delete-account/store_test.go` | TestMain merge target; new integration tests |
| `internal/domain/profile/delete-account/main_test.go` | Will be deleted |
| `internal/domain/profile/delete-account/handler_test.go` | Stale comment; missing tests |
| `internal/domain/profile/delete-account/service_test.go` | Missing tests |
| `internal/domain/profile/delete-account/routes.go` | Doc comment |
| `internal/audit/audit.go` | New constant target |
| `internal/domain/auth/shared/testutil/fake_storer.go` | No changes needed |
| `internal/domain/auth/shared/testutil/querier_proxy.go` | Two missing Fail flags |
| `e2e/profile/delete-account.json` | New folders |

Fixes are ordered from highest-severity to lowest. Apply them in the order given.

---

## Fix 1 (Critical) — Add `AttemptEvent` to `IncrementInput` in `ConfirmEmailDeletion`

**File:** `internal/domain/profile/delete-account/service.go`

The `IncrementInput` struct literal in `ConfirmEmailDeletion` omits `AttemptEvent`.
`BaseStore.IncrementAttemptsTx` returns an error immediately when `AttemptEvent == ""`,
so every wrong OTP submission returns 500 instead of 422 and the attempt counter is
never incremented — the lockout logic is completely non-functional.

**Step 1a — Add audit constant**

In `internal/audit/audit.go`, add `EventAccountDeletionOTPFailed` to all three required
locations (RULES §3.14 Sync S-1 — these three edits must be in the same commit):

1. **`const` block** — after `EventAccountDeletionCancelled`:

```go
EventAccountDeletionOTPFailed EventType = "account_deletion_otp_failed"
```

2. **`AllEvents()` return slice** — add after `EventAccountDeletionCancelled,`:

```go
EventAccountDeletionOTPFailed,
```

3. **`audit_test.go` `TestEventConstants_ExactValues` cases table** — add the matching row:

```go
{audit.EventAccountDeletionOTPFailed, "account_deletion_otp_failed"},
```

**Step 1b — Fix the `IncrementInput` literal in `service.go`**

Locate in `ConfirmEmailDeletion` (inside the `errors.Is(checkErr, authshared.ErrInvalidCode)` block):

```go
			if incErr := s.store.IncrementAttemptsTx(context.WithoutCancel(ctx), authshared.IncrementInput{
				TokenID:     token.ID,
				UserID:      uid,
				Attempts:    token.Attempts,
				MaxAttempts: token.MaxAttempts,
				IPAddress:   in.IPAddress,
				UserAgent:   in.UserAgent,
			}); incErr != nil {
```

Replace with:

```go
			if incErr := s.store.IncrementAttemptsTx(context.WithoutCancel(ctx), authshared.IncrementInput{
				TokenID:      token.ID,
				UserID:       uid,
				Attempts:     token.Attempts,
				MaxAttempts:  token.MaxAttempts,
				AttemptEvent: audit.EventAccountDeletionOTPFailed,
				IPAddress:    in.IPAddress,
				UserAgent:    in.UserAgent,
			}); incErr != nil {
```

Ensure `"github.com/7-Dany/store/backend/internal/audit"` is in the import block of
`service.go`. Add it if missing.

---

## Fix 2 (Medium Bug) — Add `Provider` field to `ScheduleDeletionInput`; fix audit log for Telegram path

**Files:** `internal/domain/profile/delete-account/models.go`,
`internal/domain/profile/delete-account/service.go`,
`internal/domain/profile/delete-account/store.go`

`ScheduleDeletionTx` hardcodes `Provider: db.AuthProviderEmail` in its audit log INSERT.
It is also called by `ConfirmTelegramDeletion` (Path C-2), causing the audit row for a
Telegram-confirmed deletion to record `provider = "email"` — the wrong value.

**Step 2a — Add `Provider` field to `ScheduleDeletionInput` in `models.go`**

Locate `ScheduleDeletionInput`:

```go
// ScheduleDeletionInput holds the caller-supplied data for service.ScheduleDeletion.
type ScheduleDeletionInput struct {
	UserID    string
	IPAddress string
	UserAgent string
}
```

Replace with:

```go
// ScheduleDeletionInput holds the caller-supplied data for service.ScheduleDeletion.
// Provider is the auth provider that confirmed the deletion; used for the audit row.
// Pass db.AuthProviderEmail for password and email-OTP paths, db.AuthProviderTelegram
// for the Telegram confirmation path.
type ScheduleDeletionInput struct {
	UserID    string
	IPAddress string
	UserAgent string
	Provider  db.AuthProvider
}
```

Add `"github.com/7-Dany/store/backend/internal/db"` to the import block of `models.go`.

**Step 2b — Thread the provider from each service call site in `service.go`**

There are two `ScheduleDeletionTx` call sites in `service.go`:

_In `DeleteWithPassword` (Path A — password user):_

```go
	result, err := s.store.ScheduleDeletionTx(ctx, ScheduleDeletionInput{
		UserID:    in.UserID,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
	})
```

Replace with:

```go
	result, err := s.store.ScheduleDeletionTx(ctx, ScheduleDeletionInput{
		UserID:    in.UserID,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
		Provider:  db.AuthProviderEmail,
	})
```

_In `ConfirmTelegramDeletion` (Path C-2 — Telegram user):_

```go
	result, err := s.store.ScheduleDeletionTx(ctx, ScheduleDeletionInput{
		UserID:    in.UserID,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
	})
```

Replace with:

```go
	result, err := s.store.ScheduleDeletionTx(ctx, ScheduleDeletionInput{
		UserID:    in.UserID,
		IPAddress: in.IPAddress,
		UserAgent: in.UserAgent,
		Provider:  db.AuthProviderTelegram,
	})
```

Add `"github.com/7-Dany/store/backend/internal/db"` to the import block of `service.go`
if not already present.

**Step 2c — Use `in.Provider` in `ScheduleDeletionTx` in `store.go`**

Locate in `ScheduleDeletionTx` the audit INSERT:

```go
	if err := h.Q.InsertAuditLog(context.WithoutCancel(ctx), db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventAccountDeletionRequested),
		Provider:  db.AuthProviderEmail,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
```

Replace `Provider: db.AuthProviderEmail` with `Provider: in.Provider`:

```go
	if err := h.Q.InsertAuditLog(context.WithoutCancel(ctx), db.InsertAuditLogParams{
		UserID:    userPgUUID,
		EventType: string(audit.EventAccountDeletionRequested),
		Provider:  in.Provider,
		IpAddress: s.IPToNullable(in.IPAddress),
		UserAgent: s.ToText(s.TruncateUserAgent(in.UserAgent)),
		Metadata:  []byte("{}"),
	}); err != nil {
```

Also update the `Storer` interface doc comment for `ScheduleDeletionTx` in `service.go`
to mention the provider field:

```go
	// ScheduleDeletionTx stamps deleted_at = NOW(), writes the audit row, and
	// returns DeletionScheduled{ScheduledDeletionAt: deleted_at + 30 days}.
	// Maps no-rows from ScheduleUserDeletion to profileshared.ErrUserNotFound.
	// in.Provider is used for the audit row (AuthProviderEmail or AuthProviderTelegram).
	ScheduleDeletionTx(ctx context.Context, in ScheduleDeletionInput) (DeletionScheduled, error)
```

Update `DeleteAccountFakeStorer.ScheduleDeletionTxFn` callers in `service_test.go` and
`store_test.go` to pass `Provider: db.AuthProviderEmail` where `ScheduleDeletionInput`
is constructed. The fake storer itself does not need changes.

---

## Fix 3 (Error) — Delete `main_test.go`; merge its contents into `store_test.go`

**Files:** `internal/domain/profile/delete-account/main_test.go` (delete),
`internal/domain/profile/delete-account/store_test.go` (edit)

RULES §3.8 and §3.13 require `testPool`, `TestMain`, and all integration-test
infrastructure to live in `store_test.go` behind the `//go:build integration_test` tag.
A standalone `main_test.go` with no build tag runs bcrypt-lowering for all test
binaries (including CI runs that never set `TEST_DATABASE_URL`) and splits the
infrastructure across two files.

**Step 3a — Add `testPool` and `TestMain` to `store_test.go`**

`store_test.go` already has `//go:build integration_test` on line 1. It defines
`txStores`, `withProxy`, and seed helpers but does NOT declare `testPool` or
`TestMain` (those are in `main_test.go`). Add them immediately after the import
block, before the `// ── helpers ──` separator:

```go
var testPool *pgxpool.Pool

// TestMain lowers bcrypt cost for fast unit tests and (when TEST_DATABASE_URL is
// set) initialises testPool for integration tests.
// maxConns=20 satisfies ADR-003 (IncrementAttemptsTx opens independent connections).
func TestMain(m *testing.M) { authsharedtest.RunTestMain(m, &testPool, 20) }
```

The `pgxpool` import is already present in `store_test.go`; verify before adding.

**Step 3b — Delete `main_test.go`**

Delete the file `internal/domain/profile/delete-account/main_test.go` entirely.
There must be no file with this name in the package directory.

---

## Fix 4 (Error) — Move `Servicer` interface from `service.go` to `handler.go`

**Files:** `internal/domain/profile/delete-account/service.go` (remove),
`internal/domain/profile/delete-account/handler.go` (add)

RULES §3.3 and §3.13 require the `Servicer` interface to be defined in `handler.go`,
directly above the `Handler` struct. `service.go` must retain only `Storer` and
`Service`.

**Step 4a — Remove `Servicer` from `service.go`**

Locate and delete the entire `Servicer` interface block (from its doc comment through
the closing `}`).

**Step 4b — Remove the compile-time check from `service.go`**

Locate and delete:

```go
// compile-time check that *Service satisfies Servicer.
var _ Servicer = (*Service)(nil)
```

The check is unnecessary: `routes.go` passes `*Service` to `NewHandler(svc Servicer, ...)`,
which verifies the relationship at compile time (RULES §3.3).

**Step 4c — Add `Servicer` to `handler.go`**

In `handler.go`, immediately **before** the `// Handler is the HTTP layer...` doc comment,
insert the full `Servicer` interface verbatim from `service.go`. Preserve every method
doc comment, every method signature, and all formatting. Ensure `"context"` is in
the `handler.go` import block.

---

## Fix 5 (Warning) — Replace `"deleteaccount."` with `"store."` in all `store.go` `fmt.Errorf` calls

**File:** `internal/domain/profile/delete-account/store.go`

RULES §3.13 requires store error-wrapping prefixes to use `"store.{Method}"`.
Every `fmt.Errorf` in `store.go` uses `"deleteaccount.{Method}"`.

Apply these replacements **only in `store.go`**. Service prefixes using `"deleteaccount."` are
correct per §3.13 and must not be changed.

| Find (in store.go fmt.Errorf) | Replace with |
|---|---|
| `"deleteaccount.GetUserForDeletion:` | `"store.GetUserForDeletion:` |
| `"deleteaccount.GetUserAuthMethods:` | `"store.GetUserAuthMethods:` |
| `"deleteaccount.GetIdentityByUserAndProvider:` | `"store.GetIdentityByUserAndProvider:` |
| `"deleteaccount.GetAccountDeletionToken:` | `"store.GetAccountDeletionToken:` |
| `"deleteaccount.ScheduleDeletionTx:` | `"store.ScheduleDeletionTx:` |
| `"deleteaccount.SendDeletionOTPTx:` | `"store.SendDeletionOTPTx:` |
| `"deleteaccount.ConfirmOTPDeletionTx:` | `"store.ConfirmOTPDeletionTx:` |
| `"deleteaccount.CancelDeletionTx:` | `"store.CancelDeletionTx:` |

There is exactly one `fmt.Errorf` per error site — do a file-scoped find-and-replace.

---

## Fix 6 (Warning) — Move response types from `handler.go` to `requests.go`

**Files:** `internal/domain/profile/delete-account/handler.go` (remove types),
`internal/domain/profile/delete-account/requests.go` (add types)

RULES §1.8 and §3.1 require all HTTP request/response structs with `json:` tags to live
in `requests.go`.

**Step 6a — Remove from `handler.go`**

Locate and delete the `// ── Response types ──` section entirely: both struct definitions
and the `newDeletionScheduledResponse` constructor.

**Step 6b — Add to `requests.go`**

Append the following at the end of `requests.go` (after `cancelDeletionRequest`):

```go
// ── Response types ────────────────────────────────────────────────────────────

// deletionScheduledResponse is the JSON body returned on successful account deletion scheduling.
type deletionScheduledResponse struct {
	Message             string    `json:"message"`
	ScheduledDeletionAt time.Time `json:"scheduled_deletion_at"`
}

// deletionInitiatedResponse is the JSON body returned on step-1 (OTP issued or Telegram prompt)
// and on successful CancelDeletion.
type deletionInitiatedResponse struct {
	Message    string `json:"message"`
	AuthMethod string `json:"auth_method,omitempty"`
	ExpiresIn  int    `json:"expires_in,omitempty"` // seconds until OTP expires; present only on Path B-1
}

func newDeletionScheduledResponse(result DeletionScheduled) deletionScheduledResponse {
	return deletionScheduledResponse{
		Message:             "account scheduled for deletion",
		ScheduledDeletionAt: result.ScheduledDeletionAt,
	}
}
```

Add `"time"` to the import block of `requests.go`.

---

## Fix 7 (Warning) — Delete `validateOTPCode` from `validators.go`

**File:** `internal/domain/profile/delete-account/validators.go`

`validateOTPCode` is never called. `service.go` calls `authshared.ValidateOTPCode` directly.
The local wrapper is dead code (RULES §2.4).

Delete the entire function and its doc comment. After deletion, check whether `"unicode"`
and `authshared` are still used in the file. `validateOTPCode` is the only user of both —
remove them from the import block if now unused.

---

## Fix 8 (High) — Remove dead `ErrUserNotFound` case from `DeletionMethod` handler

**File:** `internal/domain/profile/delete-account/handler.go`

In `DeletionMethod`, the `errors.Is(err, authshared.ErrUserNotFound)` case is dead code.
The service wraps `profileshared.ErrUserNotFound` opaquely; `authshared.ErrUserNotFound`
can never reach the handler.

Locate in `DeletionMethod`:

```go
		case errors.Is(err, authshared.ErrUserNotFound):
			respond.Error(w, http.StatusNotFound, "not_found", "user not found")
```

Delete this case. The final `if err != nil` block must contain only:

```go
	if err != nil {
		switch {
		case errors.Is(err, ErrAlreadyPendingDeletion):
			respond.Error(w, http.StatusConflict, "already_pending_deletion", err.Error())
		default:
			slog.ErrorContext(r.Context(), "deleteaccount.DeletionMethod: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
```

If `authshared` is no longer imported anywhere in `handler.go` after this change, remove
it from the import block. It is still used for `authshared.ErrInvalidCredentials`,
`authshared.ErrCodeInvalidFormat`, etc. in `mapDeleteError` — verify before removing.

---

## Fix 9 (Medium) — Document the suppressed `tokenID` parameter in `ConfirmOTPDeletionTx`

**File:** `internal/domain/profile/delete-account/store.go`

The `tokenID [16]byte` parameter is accepted then immediately suppressed with `_ = tokenID`.
Locate:

```go
	_ = tokenID // tokenID is the service-layer token; we use the DB-locked row's ID above
```

Replace with:

```go
	// tokenID is the service-layer token ID passed in for documentation purposes.
	// We intentionally use the DB-locked row's ID (lockedRow.ID) acquired inside this
	// transaction rather than the caller-supplied value, because the FOR UPDATE
	// re-fetch is the authoritative TOCTOU guard. The parameter exists so callers
	// understand which token they intend to consume; a mismatch would indicate a
	// programming error that should surface as a test failure, not a runtime panic.
	_ = tokenID
```

---

## Fix 10 (Low) — Fix step-6 doc comment in `ConfirmEmailDeletion`

**File:** `internal/domain/profile/delete-account/service.go`

The guard-ordering godoc says `"other sentinels: return as-is"` but the code explicitly
maps `ErrTokenExpired → ErrTokenNotFound`.

Locate in the `ConfirmEmailDeletion` doc comment:

```
//  6. CheckOTPToken(token, in.Code, time.Now())
//     - ErrInvalidCode: IncrementAttemptsTx then return ErrInvalidCode
//     - other sentinels: return as-is
```

Replace with:

```
//  6. CheckOTPToken(token, in.Code, time.Now())
//     - ErrInvalidCode: IncrementAttemptsTx then return ErrInvalidCode
//     - ErrTokenExpired: return as ErrTokenNotFound (authshared convention)
//     - ErrTooManyAttempts / other sentinels: return as-is
```

---

## Fix 11 (Low) — Amend `GetAccountDeletionToken` Storer doc comment

**File:** `internal/domain/profile/delete-account/service.go`

The doc says "the service must call this inside a transaction" but `ConfirmEmailDeletion`
intentionally calls it outside a transaction first.

Locate in the `Storer` interface:

```go
	// GetAccountDeletionToken fetches the active account_deletion token for the user.
	// The underlying query uses FOR UPDATE; the service must call this inside a
	// transaction (or accept that the lock is released at statement end).
	// Maps no-rows to authshared.ErrTokenNotFound.
```

Replace with:

```go
	// GetAccountDeletionToken fetches the active account_deletion token for the user.
	// The underlying query uses FOR UPDATE; when called outside a transaction the lock
	// is released immediately at statement end. ConfirmOTPDeletionTx re-acquires
	// the lock inside its own transaction for TOCTOU protection — calling this outside
	// first is intentional (read-then-lock pattern).
	// Maps no-rows to authshared.ErrTokenNotFound.
```

---

## Fix 12 (Info) — Add `// Security:` comment to service-layer `validateTelegramAuthPayload` call

**File:** `internal/domain/profile/delete-account/service.go`

Locate in `ConfirmTelegramDeletion` (step 4):

```go
	// 4. Validate payload fields are present.
	if err := validateTelegramAuthPayload(&in.TelegramAuth); err != nil {
```

Replace with:

```go
	// 4. Defense-in-depth: re-validate payload presence in the service layer in case
	// the service is called directly (e.g. a future background job) without going
	// through the handler's Path C-2 guard.
	if err := validateTelegramAuthPayload(&in.TelegramAuth); err != nil {
```

---

## Fix 13 (Info) — Add `// Unreachable:` comments to all four Tx methods and `mustParseUserID`

**File:** `internal/domain/profile/delete-account/store.go`

### `BeginOrBind` begin-error in all four Tx methods

For each of `ScheduleDeletionTx`, `SendDeletionOTPTx`, `ConfirmOTPDeletionTx`,
`CancelDeletionTx`, add the comment immediately before the `if err != nil` check:

```go
	h, err := s.BeginOrBind(ctx)
	// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
	// and always returns nil error. No test can trigger this branch.
	if err != nil {
		return ..., fmt.Errorf("store.{Method}Tx: begin tx: %w", err)
	}
```

(Use the correct return type for each method: `DeletionScheduled{}` for Schedule/Confirm,
`SendDeletionOTPResult{}` for Send, bare `error` for Cancel.)

### `h.Commit()` in all four Tx methods

For each method add the comment immediately before the `if err := h.Commit()` check:

```go
	// Unreachable via QuerierProxy: on the TxBound path h.Commit is a no-op
	// that always returns nil; on the non-TxBound path h.Commit wraps
	// pgx.Tx.Commit which the proxy cannot intercept.
	if err := h.Commit(); err != nil {
		return ..., fmt.Errorf("store.{Method}Tx: commit: %w", err)
	}
```

### `mustParseUserID` panic branch

Locate:

```go
func (s *Store) mustParseUserID(id string) [16]byte {
	pgUUID, err := s.ParseUUIDString(id)
	if err != nil {
		panic(fmt.Sprintf("deleteaccount.Store: invalid user ID %q: %v", id, err))
	}
```

Add the comment immediately before the panic:

```go
	if err != nil {
		// Unreachable: service layer always calls authshared.ParseUserID before
		// the store method is reached, so an invalid UUID cannot arrive here.
		panic(fmt.Sprintf("deleteaccount.Store: invalid user ID %q: %v", id, err))
	}
```

---

## Fix 14 (Info) — Fix `routes.go` doc comment to include `delm:usr:` rate limiter

**File:** `internal/domain/profile/delete-account/routes.go`

Locate in the `Routes` doc comment:

```go
// Rate limits (Stage 0 §6):
//   - DELETE /me:               10 req / 1 hr per user  ("del:usr:")
//   - POST /me/cancel-deletion: 5 req / 10 min per user ("delc:usr:")
```

Replace with:

```go
// Rate limits (Stage 0 §6):
//   - DELETE /me:               10 req / 1 hr  per user ("del:usr:")
//   - POST /me/cancel-deletion: 5 req / 10 min per user ("delc:usr:")
//   - GET /me/deletion-method:  10 req / 1 min per user ("delm:usr:")
```

---

## Fix 15 (Warning) — Fix stale duplicate doc comment on `newFakeHandler`

**File:** `internal/domain/profile/delete-account/handler_test.go`

The function has two `// newFakeHandler` doc comment paragraphs merged together. The
first paragraph is stale: it says "Use only when enqueueEmail is never reached
(Path B-1 is not exercised)" which is now incorrect — tests DO exercise Path B-1 (T-02).

Locate the full comment block:

```go
// newFakeHandler builds a Handler wired with the provided fake servicer and
// nil deps (no mail delivery in unit tests). Use only when enqueueEmail is
// never reached (Path B-1 is not exercised) — or accept that it no-ops on nil deps.
// newFakeHandler builds a Handler wired with the provided fake servicer.
// otpTTL is set to 15 minutes — the same as the production default — so
// tests that check the Path B-1 202 body can assert on expires_in:900.
```

Replace with the single accurate comment:

```go
// newFakeHandler builds a Handler wired with the provided fake servicer.
// mailer/mailQueue are nil; enqueueEmail no-ops so Path B-1 tests can still
// assert on the 202 response body without a real mail backend.
// otpTTL is set to 15 minutes — matching the production default — so
// tests that check the Path B-1 202 body can assert on expires_in:900.
```

---

## Fix 16 — Add 4 missing tests to `handler_test.go`

**File:** `internal/domain/profile/delete-account/handler_test.go`

### Add to `TestHandler_Delete`

**T-08: `ErrInvalidCredentials → 401`**

```go
t.Run("T-08: wrong password returns 401 invalid_credentials", func(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.DeleteAccountFakeServicer{
		DeleteWithPasswordFn: func(_ context.Context, _ deleteaccount.DeleteWithPasswordInput) (deleteaccount.DeletionScheduled, error) {
			return deleteaccount.DeletionScheduled{}, authshared.ErrInvalidCredentials
		},
	}
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"password":"wrongpass"}`)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "invalid_credentials")
})
```

**Empty-body `ResolveUserForDeletion → ErrAlreadyPendingDeletion → 409`**

```go
t.Run("empty body with pending account returns 409 already_pending_deletion", func(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.DeleteAccountFakeServicer{
		ResolveUserForDeletionFn: func(_ context.Context, _ string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
			return deleteaccount.DeletionUser{}, deleteaccount.UserAuthMethods{}, deleteaccount.ErrAlreadyPendingDeletion
		},
	}
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{}`)
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), "already_pending_deletion")
})
```

### Add to `TestHandler_CancelDeletion`

**Body over `MaxBodyBytes` → 413**

```go
t.Run("cancel body over MaxBodyBytes returns 413", func(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.DeleteAccountFakeServicer{}
	oversized := strings.Repeat("a", int(respond.MaxBodyBytes)+1)
	w := postJSONWithUserID(newFakeHandler(svc).CancelDeletion, testUserID, oversized)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
})
```

**Unexpected service error → 500 (for completeness; CancelDeletion 500 path)**

Already present as `"unexpected cancel service error returns 500 internal_error"` — skip.

---

## Fix 17 — Add 8 missing tests to `service_test.go`

**File:** `internal/domain/profile/delete-account/service_test.go`

### Add to `TestService_ResolveUserForDeletion`

**`GetUserAuthMethods` error path**

```go
t.Run("GetUserAuthMethods error wraps with prefix", func(t *testing.T) {
	t.Parallel()
	authErr := errors.New("db: auth methods query failed")
	store := &authsharedtest.DeleteAccountFakeStorer{
		GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
			return userEmailOAuth(), nil
		},
		GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (deleteaccount.UserAuthMethods, error) {
			return deleteaccount.UserAuthMethods{}, authErr
		},
	}
	_, _, err := newSvc(store).ResolveUserForDeletion(context.Background(), svcUserID)
	require.ErrorIs(t, err, authErr)
	require.ErrorContains(t, err, "deleteaccount.ResolveUserForDeletion: get auth methods:")
})
```

### Add to `TestService_DeleteWithPassword`

**`ScheduleDeletionTx → profileshared.ErrUserNotFound` (race: user hard-deleted)**

```go
t.Run("ScheduleDeletionTx ErrUserNotFound wraps as internal error", func(t *testing.T) {
	t.Parallel()
	store := &authsharedtest.DeleteAccountFakeStorer{
		GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
			return userWithPassword(t), nil
		},
		ScheduleDeletionTxFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput) (deleteaccount.DeletionScheduled, error) {
			return deleteaccount.DeletionScheduled{}, profileshared.ErrUserNotFound
		},
	}
	in := deleteaccount.DeleteWithPasswordInput{
		UserID: svcUserID, Password: svcPassword, IPAddress: svcIP, UserAgent: svcUA,
	}
	_, err := newSvc(store).DeleteWithPassword(context.Background(), in)
	require.Error(t, err)
	require.ErrorContains(t, err, "deleteaccount.DeleteWithPassword: schedule deletion: user not found:")
})
```

### Add to `TestService_ConfirmEmailDeletion`

**`GetUserForDeletion` raw DB error**

```go
t.Run("GetUserForDeletion raw DB error wraps with prefix", func(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db: connection reset")
	store := &authsharedtest.DeleteAccountFakeStorer{
		GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
			return deleteaccount.DeletionUser{}, dbErr
		},
	}
	_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), deleteaccount.ConfirmOTPDeletionInput{
		UserID: svcUserID, Code: authsharedtest.OTPPlaintext, IPAddress: svcIP, UserAgent: svcUA,
	})
	require.ErrorIs(t, err, dbErr)
	require.ErrorContains(t, err, "deleteaccount.ConfirmEmailDeletion: get user:")
})
```

**`GetAccountDeletionToken` non-`ErrTokenNotFound` DB error**

```go
t.Run("GetAccountDeletionToken DB error wraps with prefix", func(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db: deadlock")
	store := &authsharedtest.DeleteAccountFakeStorer{
		GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
			return userEmailOAuth(), nil
		},
		GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
			return authshared.VerificationToken{}, dbErr
		},
	}
	_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), deleteaccount.ConfirmOTPDeletionInput{
		UserID: svcUserID, Code: authsharedtest.OTPPlaintext, IPAddress: svcIP, UserAgent: svcUA,
	})
	require.ErrorIs(t, err, dbErr)
	require.ErrorContains(t, err, "deleteaccount.ConfirmEmailDeletion: get token:")
})
```

**`IncrementAttemptsTx` error on wrong code**

```go
t.Run("IncrementAttemptsTx error wraps with prefix", func(t *testing.T) {
	t.Parallel()
	incErr := errors.New("db: increment failed")
	store := &authsharedtest.DeleteAccountFakeStorer{
		GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
			return userEmailOAuth(), nil
		},
		GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
			return makeValidOTPToken(t), nil
		},
		IncrementAttemptsTxFn: func(_ context.Context, _ authshared.IncrementInput) error {
			return incErr
		},
	}
	_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), deleteaccount.ConfirmOTPDeletionInput{
		UserID: svcUserID, Code: "000000", IPAddress: svcIP, UserAgent: svcUA, // wrong but valid format
	})
	require.ErrorIs(t, err, incErr)
	require.ErrorContains(t, err, "deleteaccount.ConfirmEmailDeletion: increment attempts:")
})
```

**`ConfirmOTPDeletionTx → ErrTokenAlreadyUsed`**

```go
t.Run("ConfirmOTPDeletionTx ErrTokenAlreadyUsed returns ErrTokenAlreadyUsed", func(t *testing.T) {
	t.Parallel()
	store := &authsharedtest.DeleteAccountFakeStorer{
		GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
			return userEmailOAuth(), nil
		},
		GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
			return makeValidOTPToken(t), nil
		},
		ConfirmOTPDeletionTxFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput, _ [16]byte) (deleteaccount.DeletionScheduled, error) {
			return deleteaccount.DeletionScheduled{}, authshared.ErrTokenAlreadyUsed
		},
	}
	_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), deleteaccount.ConfirmOTPDeletionInput{
		UserID: svcUserID, Code: authsharedtest.OTPPlaintext, IPAddress: svcIP, UserAgent: svcUA,
	})
	require.ErrorIs(t, err, authshared.ErrTokenAlreadyUsed)
})
```

**`ConfirmOTPDeletionTx → profileshared.ErrUserNotFound` (race)**

```go
t.Run("ConfirmOTPDeletionTx ErrUserNotFound wraps as internal error", func(t *testing.T) {
	t.Parallel()
	store := &authsharedtest.DeleteAccountFakeStorer{
		GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
			return userEmailOAuth(), nil
		},
		GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
			return makeValidOTPToken(t), nil
		},
		ConfirmOTPDeletionTxFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput, _ [16]byte) (deleteaccount.DeletionScheduled, error) {
			return deleteaccount.DeletionScheduled{}, profileshared.ErrUserNotFound
		},
	}
	_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), deleteaccount.ConfirmOTPDeletionInput{
		UserID: svcUserID, Code: authsharedtest.OTPPlaintext, IPAddress: svcIP, UserAgent: svcUA,
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "deleteaccount.ConfirmEmailDeletion: confirm deletion: user not found:")
})
```

**`ConfirmOTPDeletionTx → generic DB error`**

```go
t.Run("ConfirmOTPDeletionTx DB error wraps with prefix", func(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db: tx aborted")
	store := &authsharedtest.DeleteAccountFakeStorer{
		GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
			return userEmailOAuth(), nil
		},
		GetAccountDeletionTokenFn: func(_ context.Context, _ [16]byte) (authshared.VerificationToken, error) {
			return makeValidOTPToken(t), nil
		},
		ConfirmOTPDeletionTxFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput, _ [16]byte) (deleteaccount.DeletionScheduled, error) {
			return deleteaccount.DeletionScheduled{}, dbErr
		},
	}
	_, err := newSvc(store).ConfirmEmailDeletion(context.Background(), deleteaccount.ConfirmOTPDeletionInput{
		UserID: svcUserID, Code: authsharedtest.OTPPlaintext, IPAddress: svcIP, UserAgent: svcUA,
	})
	require.ErrorIs(t, err, dbErr)
	require.ErrorContains(t, err, "deleteaccount.ConfirmEmailDeletion: confirm deletion:")
})
```

### Add `TestService_GetDeletionMethod`

Add the following top-level test function. Every sub-test must be parallel.
If `time` is not already imported in `service_test.go`, add it.

```go
func TestService_GetDeletionMethod(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("password user returns method:password", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (deleteaccount.UserAuthMethods, error) {
				return deleteaccount.UserAuthMethods{HasPassword: true}, nil
			},
		}
		result, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.NoError(t, err)
		require.Equal(t, "password", result.Method)
	})

	t.Run("email-only user returns method:email_otp", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (deleteaccount.UserAuthMethods, error) {
				return deleteaccount.UserAuthMethods{HasPassword: false}, nil
			},
		}
		result, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.NoError(t, err)
		require.Equal(t, "email_otp", result.Method)
	})

	t.Run("Telegram-only user returns method:telegram", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userTelegramOnly(), nil
			},
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (deleteaccount.UserAuthMethods, error) {
				return deleteaccount.UserAuthMethods{HasPassword: false}, nil
			},
		}
		result, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.NoError(t, err)
		require.Equal(t, "telegram", result.Method)
	})

	t.Run("already pending deletion returns ErrAlreadyPendingDeletion", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userPendingDeletion(), nil
			},
		}
		_, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.ErrorIs(t, err, deleteaccount.ErrAlreadyPendingDeletion)
	})

	t.Run("GetUserForDeletion ErrUserNotFound wraps as internal", func(t *testing.T) {
		t.Parallel()
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return deleteaccount.DeletionUser{}, profileshared.ErrUserNotFound
			},
		}
		_, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.Error(t, err)
		require.ErrorContains(t, err, "deleteaccount.GetDeletionMethod:")
		require.NotErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	t.Run("GetUserForDeletion DB error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		dbErr := errors.New("db: connection reset")
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return deleteaccount.DeletionUser{}, dbErr
			},
		}
		_, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.ErrorIs(t, err, dbErr)
		require.ErrorContains(t, err, "deleteaccount.GetDeletionMethod: get user:")
	})

	t.Run("GetUserAuthMethods error wraps with prefix", func(t *testing.T) {
		t.Parallel()
		authErr := errors.New("db: auth methods query failed")
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				return userEmailOAuth(), nil
			},
			GetUserAuthMethodsFn: func(_ context.Context, _ [16]byte) (deleteaccount.UserAuthMethods, error) {
				return deleteaccount.UserAuthMethods{}, authErr
			},
		}
		_, err := newSvc(store).GetDeletionMethod(ctx, svcUserID)
		require.ErrorIs(t, err, authErr)
		require.ErrorContains(t, err, "deleteaccount.GetDeletionMethod: get auth methods:")
	})

	t.Run("invalid userID returns parse error before any store call", func(t *testing.T) {
		t.Parallel()
		called := false
		store := &authsharedtest.DeleteAccountFakeStorer{
			GetUserForDeletionFn: func(_ context.Context, _ [16]byte) (deleteaccount.DeletionUser, error) {
				called = true
				return deleteaccount.DeletionUser{}, nil
			},
		}
		_, err := newSvc(store).GetDeletionMethod(ctx, "not-a-uuid")
		require.Error(t, err)
		require.False(t, called, "store must not be called on invalid userID")
	})
}
```

---

## Fix 18 — Add `FailGetUserAuthMethods` and `FailGetIdentityByUserAndProvider` to `QuerierProxy`

**File:** `internal/domain/auth/shared/testutil/querier_proxy.go`

Two flags are missing from the `// ── delete account ──` section, blocking integration
tests for those store methods.

**Step 18a — Add fields to the struct**

In the `// ── delete account ──` field block, add after `FailGetUserForDeletion bool`:

```go
	FailGetUserAuthMethods             bool
	FailGetIdentityByUserAndProvider   bool
```

**Step 18b — Add override methods**

Add these two methods in the `// ── delete account ──` section:

```go
func (b *QuerierProxy) GetUserAuthMethods(ctx context.Context, userID pgtype.UUID) (db.GetUserAuthMethodsRow, error) {
	if b.FailGetUserAuthMethods {
		return db.GetUserAuthMethodsRow{}, ErrProxy
	}
	return b.Querier.GetUserAuthMethods(ctx, userID)
}

func (b *QuerierProxy) GetIdentityByUserAndProvider(ctx context.Context, arg db.GetIdentityByUserAndProviderParams) (db.GetIdentityByUserAndProviderRow, error) {
	if b.FailGetIdentityByUserAndProvider {
		return db.GetIdentityByUserAndProviderRow{}, ErrProxy
	}
	return b.Querier.GetIdentityByUserAndProvider(ctx, arg)
}
```

Verify the exact return types from `db.Querier` (generated by `make sqlc`) before
applying — adjust the method signature to match if the generated types differ.

---

## Fix 19 — Add `GetUserAuthMethods` integration tests to `store_test.go`

**File:** `internal/domain/profile/delete-account/store_test.go`

`GetUserAuthMethods` has zero integration tests. Add:

```go
// ── TestGetUserAuthMethods_Integration ────────────────────────────────────────

func TestGetUserAuthMethods_Integration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("password user — HasPassword true, IdentityCount 0", func(t *testing.T) {
		t.Parallel()
		s, q := txStores(t)
		email := authsharedtest.NewEmail(t)
		userID := [16]byte(authsharedtest.CreateUserUUID(t, testPool, q, email))

		methods, err := s.GetUserAuthMethods(ctx, userID)
		require.NoError(t, err)
		require.True(t, methods.HasPassword, "password user must have HasPassword=true")
		require.Equal(t, 0, methods.IdentityCount)
	})

	t.Run("unknown userID returns ErrUserNotFound", func(t *testing.T) {
		t.Parallel()
		s, _ := txStores(t)
		_, err := s.GetUserAuthMethods(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, profileshared.ErrUserNotFound)
	})

	t.Run("FailGetUserAuthMethods returns wrapped ErrProxy", func(t *testing.T) {
		t.Parallel()
		_, q := txStores(t)
		proxy := authsharedtest.NewQuerierProxy(q)
		proxy.FailGetUserAuthMethods = true
		_, err := withProxy(q, proxy).GetUserAuthMethods(ctx, authsharedtest.RandomUUID())
		require.ErrorIs(t, err, authsharedtest.ErrProxy)
	})
}
```

Also update the coverage comment at the top of `store_test.go` to list `GetUserAuthMethods`
integration test coverage.

---

## Fix 20 — Add `happy-path-deletion-method` folder to `e2e/profile/delete-account.json`

**File:** `e2e/profile/delete-account.json`

The collection has no coverage for `GET /api/v1/profile/me/deletion-method`.
Add a new folder named `"happy-path-deletion-method"` as item index 2 (after
`"happy-path-cancel"` and before `"happy-path-email-otp"`). The folder uses
`del_user_a` (password user) who is live after `"happy-path-cancel"` restores
the account.

Three requests:

**Request 1 — password user → 200 `deletion_method:password`**

```json
{
  "name": "GET /me/deletion-method — password user → 200 deletion_method:password",
  "request": {
    "method": "GET",
    "header": [
      { "key": "Authorization", "value": "Bearer {{del_a_access}}" },
      { "key": "X-Forwarded-For", "value": "{{_xff}}" }
    ],
    "url": { "raw": "{{baseUrl}}/api/v1/profile/me/deletion-method" }
  },
  "event": [{
    "listen": "test",
    "script": { "exec": [
      "pm.test('200 with deletion_method:password', function() {",
      "  pm.response.to.have.status(200);",
      "  pm.expect(pm.response.json().deletion_method).to.eql('password');",
      "});"
    ]}
  }]
}
```

**Request 2 — no JWT → 401**

```json
{
  "name": "GET /me/deletion-method — no JWT → 401",
  "request": {
    "method": "GET",
    "header": [{ "key": "X-Forwarded-For", "value": "{{_xff}}" }],
    "url": { "raw": "{{baseUrl}}/api/v1/profile/me/deletion-method" }
  },
  "event": [{
    "listen": "test",
    "script": { "exec": [
      "pm.test('401 unauthorized without JWT', function() {",
      "  pm.response.to.have.status(401);",
      "});"
    ]}
  }]
}
```

**Request 3 — pending account → 409**

Soft-delete `del_user_a` with a password DELETE first (reuse Path A), call
`GET /me/deletion-method`, then cancel to restore state.

```json
{
  "name": "GET /me/deletion-method — already pending → 409 already_pending_deletion",
  "request": {
    "method": "GET",
    "header": [
      { "key": "Authorization", "value": "Bearer {{del_a_access}}" },
      { "key": "X-Forwarded-For", "value": "{{_xff}}" }
    ],
    "url": { "raw": "{{baseUrl}}/api/v1/profile/me/deletion-method" }
  },
  "event": [{
    "listen": "test",
    "script": { "exec": [
      "pm.test('409 already_pending_deletion when account is pending', function() {",
      "  pm.response.to.have.status(409);",
      "  pm.expect(pm.response.json().code).to.eql('already_pending_deletion');",
      "});"
    ]}
  }]
}
```

Update the collection `description` field execution order list to include:

```
  3. happy-path-deletion-method — GET /me/deletion-method: password user → 200;
                                   no JWT → 401; pending account → 409
```

Renumber the subsequent folders in the description (old 3 becomes 4, etc.).

---

## Blocked — D-09 (Telegram `provider_uid` query)

The following are **intentionally deferred** pending D-09:

- `store.go:GetIdentityByUserAndProvider` always returns `("", nil)` — SQL does not
  SELECT `provider_uid`. Makes `ConfirmTelegramDeletion` (Path C-2) always return 401.
- Integration tests for `GetIdentityByUserAndProvider` (Fix 18 adds the proxy flag;
  tests can be written once D-09 SQL is done).
- Service unit tests for the ownership-check and happy-path C-2 branches.

When D-09 is resolved:
1. Update `GetIdentityByUserAndProvider` SQL to `SELECT provider_uid`.
2. Update store method to return the actual value.
3. Write integration tests (proxy flag already added by Fix 18).
4. Write service unit tests for C-2 ownership check and happy path.

---

## Compile and test

After all fixes are applied, run in this exact order:

```bash
# 1. Audit constant sync
go test ./internal/audit/...

# 2. Package build + vet
go build ./internal/domain/profile/delete-account/...
go vet ./internal/domain/profile/delete-account/...

# 3. Unit tests (no tag)
go test ./internal/domain/profile/delete-account/...

# 4. Integration tests (requires TEST_DATABASE_URL)
go test -tags integration_test ./internal/domain/profile/delete-account/...

# 5. Shared testutil still compiles
go build ./internal/domain/auth/shared/testutil/...
```

All five must pass before marking this stage complete.

---

## Completion checklist

**Critical**
- [x] Fix 1a: `audit.EventAccountDeletionOTPFailed` added to const, `AllEvents()`, and test table
- [x] Fix 1b: `AttemptEvent: audit.EventAccountDeletionOTPFailed` in `IncrementInput`

**Medium Bugs**
- [x] Fix 2a: `Provider db.AuthProvider` field added to `ScheduleDeletionInput` in `models.go`
- [x] Fix 2b: `Provider: db.AuthProviderEmail` in `DeleteWithPassword`; `Provider: db.AuthProviderTelegram` in `ConfirmTelegramDeletion`
- [x] Fix 2c: `Provider: in.Provider` in `ScheduleDeletionTx` audit INSERT; Storer doc comment updated

**Errors**
- [x] Fix 3a: `var testPool` and `TestMain` added to `store_test.go`
- [x] Fix 3b: `main_test.go` emptied (package declaration only)
- [x] Fix 4a: `Servicer` interface removed from `service.go`
- [x] Fix 4b: `var _ Servicer = (*Service)(nil)` removed
- [x] Fix 4c: `Servicer` interface added to `handler.go`

**Warnings**
- [x] Fix 5: All `"deleteaccount."` → `"store."` in `store.go` `fmt.Errorf` calls
- [x] Fix 6a: Response types removed from `handler.go`
- [x] Fix 6b: Response types added to `requests.go` with `time` import
- [x] Fix 7: `validateOTPCode` deleted; unused imports removed
- [x] Fix 8: Dead `authshared.ErrUserNotFound` case removed from `DeletionMethod`

**Medium / Low**
- [x] Fix 9: `tokenID` suppression comment expanded
- [x] Fix 10: Step-6 doc comment corrected in `ConfirmEmailDeletion`
- [x] Fix 11: `GetAccountDeletionToken` Storer doc comment updated
- [x] Fix 12: Defense-in-depth comment on service `validateTelegramAuthPayload` call
- [x] Fix 13: `// Unreachable:` on all 8 begin/commit branches + `mustParseUserID` panic
- [x] Fix 14: `routes.go` doc comment updated with `delm:usr:`
- [x] Fix 15: Stale `newFakeHandler` doc comment replaced

**Tests — handler**
- [x] Fix 16: T-08 `ErrInvalidCredentials → 401` added
- [x] Fix 16: Empty-body `ErrAlreadyPendingDeletion → 409` added
- [x] Fix 16: Cancel body over `MaxBodyBytes` → 413 added

**Tests — service**
- [x] Fix 17: `ResolveUserForDeletion` `GetUserAuthMethods` error path added
- [x] Fix 17: `DeleteWithPassword` `ScheduleDeletionTx → ErrUserNotFound` added
- [x] Fix 17: 6 missing `ConfirmEmailDeletion` paths added
- [x] Fix 17: `TestService_GetDeletionMethod` (8 sub-tests) added

**Infrastructure**
- [x] Fix 18: `FailGetUserAuthMethods` and `FailGetIdentityByUserAndProvider` added to `QuerierProxy`
- [x] Fix 19: `TestGetUserAuthMethods_Integration` (3 sub-tests) added to `store_test.go`

**E2E**
- [x] Fix 20: `happy-path-deletion-method` folder (3 requests) added to `delete-account.json`

**Blocked (do not fix now)**
- [ ] D-09: `GetIdentityByUserAndProvider` SQL missing `provider_uid` — deferred
