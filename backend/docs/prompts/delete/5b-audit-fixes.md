# Delete Account — Stage 5 Audit: Fix Prompt

**Feature:** Delete Account (§B-3)
**Package:** `internal/domain/profile/delete-account/`
**Depends on:** Stage 5 audit complete. All findings below are verified against the
current file states. Apply every fix exactly as specified. Do not reformat or
reorganise code outside the targeted lines.

---

## Before you start

Read these files so you understand context before touching anything:

| File | Why |
|---|---|
| `docs/prompts/delete/context.md` | Resolved paths, decisions, sentinel names |
| `internal/domain/profile/delete-account/handler.go` | Already fixed (N-1/N-2/N-3) — reference only |
| `internal/domain/profile/delete-account/models.go` | `TelegramAuthPayload` already removed — reference only |
| `internal/domain/profile/delete-account/requests.go` | Target for N-5 |
| `internal/domain/profile/delete-account/service.go` | Target for N-4 |
| `internal/domain/profile/delete-account/routes.go` | Target for signature fix |
| `internal/domain/profile/delete-account/handler_test.go` | Target for signature fix + 10 missing tests |
| `internal/domain/auth/shared/testutil/fake_servicer.go` | Target for N-7 |

---

## Fix 1 — N-5: Move `TelegramAuthPayload` to `requests.go`

**File:** `internal/domain/profile/delete-account/requests.go`

`TelegramAuthPayload` was removed from `models.go` in a prior session. It must now
be declared in `requests.go` because it is a wire type that carries JSON tags.
`models.go` must never contain `json:` tags (ADR-012 / §3.13).

Add the struct **after** the `deleteAccountRequest` declaration and **before**
`cancelDeletionRequest`. The exact text to insert:

```go
// TelegramAuthPayload carries the Telegram Login Widget fields submitted by the
// client in step 2 of the Telegram confirmation path (D-08).
type TelegramAuthPayload struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
	PhotoURL  string `json:"photo_url"`
	AuthDate  int64  `json:"auth_date"`
	Hash      string `json:"hash"`
}
```

The file should look like this after the edit:

```go
package deleteaccount

// deleteAccountRequest is the JSON body for DELETE /me.
// All fields are optional; the handler dispatches based on which fields are present.
type deleteAccountRequest struct {
	Password     string               `json:"password"`
	Code         string               `json:"code"`
	TelegramAuth *TelegramAuthPayload `json:"telegram_auth"`
}

// TelegramAuthPayload carries the Telegram Login Widget fields submitted by the
// client in step 2 of the Telegram confirmation path (D-08).
type TelegramAuthPayload struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
	PhotoURL  string `json:"photo_url"`
	AuthDate  int64  `json:"auth_date"`
	Hash      string `json:"hash"`
}

// cancelDeletionRequest is the JSON body for POST /me/cancel-deletion.
// No fields — the endpoint takes no body; the struct exists for DecodeJSON[T] consistency.
type cancelDeletionRequest struct{}
```

---

## Fix 2 — N-4: Add `// Security:` prefix to three lines in `service.go`

**File:** `internal/domain/profile/delete-account/service.go`

Rule §4.6 requires a `// Security:` comment immediately before any line that
implements a security-critical decision. Three lines are missing this annotation.

### 2a — `ConfirmEmailDeletion`: `context.WithoutCancel` call

Locate this block (around "6. Validate the OTP"):

```go
		if errors.Is(checkErr, authshared.ErrInvalidCode) {
			// Record the failed attempt; detach from request ctx so a client disconnect
			// cannot abort the increment and grant unlimited OTP retries (ADR-004).
			if incErr := s.store.IncrementAttemptsTx(context.WithoutCancel(ctx), authshared.IncrementInput{
```

Replace the comment with a `// Security:` prefixed version:

```go
		if errors.Is(checkErr, authshared.ErrInvalidCode) {
			// Security: detach from the request context so a client disconnect cannot
			// abort the counter increment and grant unlimited OTP retries (ADR-004).
			if incErr := s.store.IncrementAttemptsTx(context.WithoutCancel(ctx), authshared.IncrementInput{
```

### 2b — `ConfirmTelegramDeletion`: replay guard (step 5)

Locate:

```go
	// 5. Replay guard: auth_date must be within the last 24 hours.
	if in.TelegramAuth.AuthDate <= time.Now().Unix()-86400 {
```

Replace with:

```go
	// Security: reject auth_date older than 24 hours to prevent replay attacks.
	if in.TelegramAuth.AuthDate <= time.Now().Unix()-86400 {
```

### 2c — `ConfirmTelegramDeletion`: HMAC check (step 6)

Locate:

```go
	// 6. HMAC verification.
	if !verifyTelegramHMAC(s.telegramBotToken, in.TelegramAuth) {
```

Replace with:

```go
	// Security: HMAC verification proves the payload was signed by Telegram's servers.
	if !verifyTelegramHMAC(s.telegramBotToken, in.TelegramAuth) {
```

---

## Fix 3 — Update `routes.go` to match new `NewHandler` signature

**File:** `internal/domain/profile/delete-account/routes.go`

`NewHandler` was updated in a prior session to accept four arguments instead of
`*app.Deps`. The `routes.go` call site still passes `deps`. Fix it.

Locate:

```go
	h := NewHandler(svc, deps)
```

Replace with:

```go
	h := NewHandler(svc, deps.Mailer, deps.MailQueue, deps.MailDeliveryTimeout)
```

No other changes to `routes.go`.

---

## Fix 4 — Update `handler_test.go` `newFakeHandler` signature

**File:** `internal/domain/profile/delete-account/handler_test.go`

`newFakeHandler` calls the old 2-argument `NewHandler`. Update it to use the new
4-argument signature (nil/zero for the mail deps is intentional — tests never
reach `enqueueEmail`).

Locate:

```go
func newFakeHandler(svc deleteaccount.Servicer) *deleteaccount.Handler {
	return deleteaccount.NewHandler(svc, nil)
}
```

Replace with:

```go
func newFakeHandler(svc deleteaccount.Servicer) *deleteaccount.Handler {
	return deleteaccount.NewHandler(svc, nil, nil, 0)
}
```

---

## Fix 5 — N-7: Add doc comments to `DeleteAccountFakeServicer` methods

**File:** `internal/domain/auth/shared/testutil/fake_servicer.go`

Rule §4.12 requires every exported method to have a one-liner doc comment.
The six `DeleteAccountFakeServicer` methods are missing them.

Add one-liner doc comments to all six methods. Apply the edits below exactly.

**`ResolveUserForDeletion`** — locate:

```go
func (f *DeleteAccountFakeServicer) ResolveUserForDeletion(ctx context.Context, userID string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
```

Replace with:

```go
// ResolveUserForDeletion delegates to ResolveUserForDeletionFn if set.
func (f *DeleteAccountFakeServicer) ResolveUserForDeletion(ctx context.Context, userID string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
```

**`DeleteWithPassword`** — locate:

```go
func (f *DeleteAccountFakeServicer) DeleteWithPassword(ctx context.Context, in deleteaccount.DeleteWithPasswordInput) (deleteaccount.DeletionScheduled, error) {
```

Replace with:

```go
// DeleteWithPassword delegates to DeleteWithPasswordFn if set.
func (f *DeleteAccountFakeServicer) DeleteWithPassword(ctx context.Context, in deleteaccount.DeleteWithPasswordInput) (deleteaccount.DeletionScheduled, error) {
```

**`InitiateEmailDeletion`** — locate:

```go
func (f *DeleteAccountFakeServicer) InitiateEmailDeletion(ctx context.Context, in deleteaccount.ScheduleDeletionInput) (authshared.OTPIssuanceResult, error) {
```

Replace with:

```go
// InitiateEmailDeletion delegates to InitiateEmailDeletionFn if set.
func (f *DeleteAccountFakeServicer) InitiateEmailDeletion(ctx context.Context, in deleteaccount.ScheduleDeletionInput) (authshared.OTPIssuanceResult, error) {
```

**`ConfirmEmailDeletion`** — locate:

```go
func (f *DeleteAccountFakeServicer) ConfirmEmailDeletion(ctx context.Context, in deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
```

Replace with:

```go
// ConfirmEmailDeletion delegates to ConfirmEmailDeletionFn if set.
func (f *DeleteAccountFakeServicer) ConfirmEmailDeletion(ctx context.Context, in deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
```

**`ConfirmTelegramDeletion`** — locate:

```go
func (f *DeleteAccountFakeServicer) ConfirmTelegramDeletion(ctx context.Context, in deleteaccount.ConfirmTelegramDeletionInput) (deleteaccount.DeletionScheduled, error) {
```

Replace with:

```go
// ConfirmTelegramDeletion delegates to ConfirmTelegramDeletionFn if set.
func (f *DeleteAccountFakeServicer) ConfirmTelegramDeletion(ctx context.Context, in deleteaccount.ConfirmTelegramDeletionInput) (deleteaccount.DeletionScheduled, error) {
```

**`CancelDeletion`** — locate:

```go
func (f *DeleteAccountFakeServicer) CancelDeletion(ctx context.Context, in deleteaccount.CancelDeletionInput) error {
```

Replace with:

```go
// CancelDeletion delegates to CancelDeletionFn if set.
func (f *DeleteAccountFakeServicer) CancelDeletion(ctx context.Context, in deleteaccount.CancelDeletionInput) error {
```

---

## Fix 6 — Add 12 missing handler tests to `handler_test.go`

**File:** `internal/domain/profile/delete-account/handler_test.go`

The following test cases are missing. Add them all inside their respective
top-level test functions. Every new sub-test must be parallel (`t.Parallel()`).

Add `"github.com/7-Dany/store/backend/internal/platform/respond"` to the import
block if it is not already present (needed for `respond.MaxBodyBytes`).

### Add to `TestHandler_Delete`

**Body over MaxBodyBytes → 413**

```go
t.Run("body over MaxBodyBytes returns 413", func(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.DeleteAccountFakeServicer{}
	oversized := strings.Repeat("a", int(respond.MaxBodyBytes)+1)
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, oversized)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
})
```

**T-10: Path B-2 — ErrTokenNotFound → 422**

```go
t.Run("T-10: token not found returns 422 token_not_found", func(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.DeleteAccountFakeServicer{
		ConfirmEmailDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
			return deleteaccount.DeletionScheduled{}, authshared.ErrTokenNotFound
		},
	}
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"code":"123456"}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Contains(t, w.Body.String(), "token_not_found")
})
```

**T-11: Path B-2 — ErrTokenAlreadyUsed → 422**

```go
t.Run("T-11: token already used returns 422 token_already_used", func(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.DeleteAccountFakeServicer{
		ConfirmEmailDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
			return deleteaccount.DeletionScheduled{}, authshared.ErrTokenAlreadyUsed
		},
	}
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"code":"123456"}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Contains(t, w.Body.String(), "token_already_used")
})
```

**T-12: Path B-2 — ErrInvalidCode → 422**

```go
t.Run("T-12: invalid OTP code returns 422 invalid_code", func(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.DeleteAccountFakeServicer{
		ConfirmEmailDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
			return deleteaccount.DeletionScheduled{}, authshared.ErrInvalidCode
		},
	}
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"code":"123456"}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Contains(t, w.Body.String(), "invalid_code")
})
```

**T-13: Path B-2 — ErrTooManyAttempts → 429**

```go
t.Run("T-13: too many OTP attempts returns 429 too_many_attempts", func(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.DeleteAccountFakeServicer{
		ConfirmEmailDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
			return deleteaccount.DeletionScheduled{}, authshared.ErrTooManyAttempts
		},
	}
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"code":"123456"}`)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Contains(t, w.Body.String(), "too_many_attempts")
})
```

**T-16: Path C-2 — ErrTelegramIdentityMismatch → 401**

```go
t.Run("T-16: Telegram identity mismatch returns 401 telegram_identity_mismatch", func(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.DeleteAccountFakeServicer{
		ConfirmTelegramDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmTelegramDeletionInput) (deleteaccount.DeletionScheduled, error) {
			return deleteaccount.DeletionScheduled{}, deleteaccount.ErrTelegramIdentityMismatch
		},
	}
	body := `{"telegram_auth":{"id":99999,"first_name":"Evil","auth_date":9999999999,"hash":"anyhash"}}`
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, body)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "telegram_identity_mismatch")
})
```

**Empty body — ResolveUserForDeletion error → 500**

```go
t.Run("ResolveUserForDeletion service error returns 500 internal_error", func(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.DeleteAccountFakeServicer{
		ResolveUserForDeletionFn: func(_ context.Context, _ string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
			return deleteaccount.DeletionUser{}, deleteaccount.UserAuthMethods{}, errors.New("db: timeout")
		},
	}
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Contains(t, w.Body.String(), "internal_error")
})
```

**T-02: Path B-1 happy path → 202**

```go
t.Run("T-02: email user step 1 returns 202 verification code sent", func(t *testing.T) {
	t.Parallel()
	userEmail := "user@example.com"
	svc := &authsharedtest.DeleteAccountFakeServicer{
		ResolveUserForDeletionFn: func(_ context.Context, _ string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
			return deleteaccount.DeletionUser{Email: &userEmail},
				deleteaccount.UserAuthMethods{HasPassword: false},
				nil
		},
		InitiateEmailDeletionFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput) (authshared.OTPIssuanceResult, error) {
			return authshared.OTPIssuanceResult{UserID: testUserID, Email: userEmail, RawCode: "123456"}, nil
		},
	}
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{}`)
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Contains(t, w.Body.String(), "verification code sent")
})
```

**Path B-1 — InitiateEmailDeletion error → 500**

```go
t.Run("InitiateEmailDeletion error returns 500 internal_error", func(t *testing.T) {
	t.Parallel()
	userEmail := "user@example.com"
	svc := &authsharedtest.DeleteAccountFakeServicer{
		ResolveUserForDeletionFn: func(_ context.Context, _ string) (deleteaccount.DeletionUser, deleteaccount.UserAuthMethods, error) {
			return deleteaccount.DeletionUser{Email: &userEmail},
				deleteaccount.UserAuthMethods{HasPassword: false},
				nil
		},
		InitiateEmailDeletionFn: func(_ context.Context, _ deleteaccount.ScheduleDeletionInput) (authshared.OTPIssuanceResult, error) {
			return authshared.OTPIssuanceResult{}, errors.New("store: insert failed")
		},
	}
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Contains(t, w.Body.String(), "internal_error")
})
```

**T-03: Path B-2 happy path → 200 with scheduled_deletion_at**

```go
t.Run("T-03: OTP confirm happy path returns 200 with scheduled_deletion_at", func(t *testing.T) {
	t.Parallel()
	scheduledAt := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	svc := &authsharedtest.DeleteAccountFakeServicer{
		ConfirmEmailDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmOTPDeletionInput) (deleteaccount.DeletionScheduled, error) {
			return deleteaccount.DeletionScheduled{ScheduledDeletionAt: scheduledAt}, nil
		},
	}
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, `{"code":"123456"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "scheduled_deletion_at")
	require.Contains(t, w.Body.String(), "2026-05-01")
})
```

**T-05: Path C-2 happy path → 200 with scheduled_deletion_at**

```go
t.Run("T-05: Telegram confirm happy path returns 200 with scheduled_deletion_at", func(t *testing.T) {
	t.Parallel()
	scheduledAt := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	svc := &authsharedtest.DeleteAccountFakeServicer{
		ConfirmTelegramDeletionFn: func(_ context.Context, _ deleteaccount.ConfirmTelegramDeletionInput) (deleteaccount.DeletionScheduled, error) {
			return deleteaccount.DeletionScheduled{ScheduledDeletionAt: scheduledAt}, nil
		},
	}
	body := `{"telegram_auth":{"id":12345,"first_name":"Test","auth_date":9999999999,"hash":"validhash"}}`
	w := deleteJSONWithUserID(newFakeHandler(svc).Delete, testUserID, body)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "scheduled_deletion_at")
})
```

### Add to `TestHandler_CancelDeletion`

**Cancel — malformed JSON → 400**

```go
t.Run("cancel malformed JSON returns 400", func(t *testing.T) {
	t.Parallel()
	svc := &authsharedtest.DeleteAccountFakeServicer{}
	w := postJSONWithUserID(newFakeHandler(svc).CancelDeletion, testUserID, `{bad}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
})
```

---

## Compile and test

After all fixes are applied, run in order:

```bash
go build ./internal/domain/profile/delete-account/...
go vet ./internal/domain/profile/delete-account/...
go test ./internal/domain/profile/delete-account/...
```

All three must pass before proceeding to Stage 6.

---

## Completion checklist

- [ ] Fix 1: `TelegramAuthPayload` added to `requests.go` with json tags
- [ ] Fix 2a: `// Security:` comment before `context.WithoutCancel` in `ConfirmEmailDeletion`
- [ ] Fix 2b: `// Security:` comment before replay guard in `ConfirmTelegramDeletion`
- [ ] Fix 2c: `// Security:` comment before HMAC check in `ConfirmTelegramDeletion`
- [ ] Fix 3: `routes.go` `NewHandler` call uses `deps.Mailer, deps.MailQueue, deps.MailDeliveryTimeout`
- [ ] Fix 4: `handler_test.go` `newFakeHandler` calls `NewHandler(svc, nil, nil, 0)`
- [ ] Fix 5: All 6 `DeleteAccountFakeServicer` methods have doc comments
- [ ] Fix 6: All 12 missing tests added to `handler_test.go`
- [ ] `go build` passes
- [ ] `go vet` passes
- [ ] `go test` green
