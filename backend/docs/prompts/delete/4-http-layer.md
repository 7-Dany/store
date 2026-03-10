# §B-3 Delete Account — Stage 4: HTTP Layer

**Feature:** Delete Account (§B-3)
**Package:** `internal/domain/profile/delete-account/` (package `deleteaccount`)
**Depends on:** Stage 3 complete — all packages compile and vet clean.

---

## Read first (before writing any code)

| File | What to extract |
|---|---|
| `docs/prompts/delete/context.md` | Resolved paths, decisions, sentinel names |
| `docs/prompts/delete/0-design.md §2` | Full HTTP contract — every status code, body variant, error code |
| `docs/prompts/delete/0-design.md §5` | Guard orderings for all paths (handler dispatch logic) |
| `docs/prompts/delete/0-design.md §6` | Rate-limit prefixes (`del:usr:`, `delc:usr:`) and burst/window values |
| `internal/domain/profile/delete-account/service.go` | Servicer interface — method names, input/result types |
| `internal/domain/profile/delete-account/models.go` | All input/result types; TelegramAuthPayload fields |
| `internal/domain/profile/delete-account/requests.go` | deleteAccountRequest, cancelDeletionRequest |
| `internal/domain/profile/delete-account/errors.go` | All package sentinels |
| `internal/domain/profile/email/handler.go` | enqueueEmail pattern, mustUserID, error switch style |
| `internal/domain/profile/email/routes.go` | Route registration pattern with per-route limiters |
| `internal/domain/profile/set-password/routes.go` | NewService(store) + NewHandler(svc) wiring pattern |
| `internal/app/deps.go` | Deps fields — OAuth.TelegramBotToken, OTPTokenTTL, MailQueue, Mailer, MailDeliveryTimeout |
| `internal/domain/auth/shared/errors.go` | authshared sentinels referenced by the handler switch |
| `internal/platform/mailer/templates/registry.go` | Existing template keys — no account_deletion key exists yet |
| `internal/platform/respond/respond.go` | respond.JSON, respond.Error, respond.DecodeJSON, respond.ClientIP, respond.MaxBodyBytes |
| `internal/platform/ratelimit/ratelimit.go` | NewUserRateLimiter, Limit middleware signature |

---

## Deliverables

### 0. New mailer template — `internal/platform/mailer/templates/account_deletion.go`

No account-deletion OTP template exists yet. Create it following the pattern of
`password_reset.go` or `unlock.go`.

```go
package templates

// AccountDeletionOTPKey is the registry key for the account-deletion OTP email.
const AccountDeletionOTPKey = "account_deletion_otp"

// AccountDeletionOTPTemplate is the HTML template for the account-deletion
// OTP email. {{.Code}} is the plaintext OTP code; {{.AppName}} is the
// application name injected by the mailer.
var AccountDeletionOTPTemplate = &accountDeletionOTPHTML

var accountDeletionOTPHTML = `<!DOCTYPE html>
<html>
<body>
  <p>You requested to delete your {{.AppName}} account.</p>
  <p>Your confirmation code is: <strong>{{.Code}}</strong></p>
  <p>This code expires in 15 minutes. If you did not request account deletion, you can ignore this email.</p>
</body>
</html>`
```

Register it in `internal/platform/mailer/templates/registry.go`:

```go
AccountDeletionOTPKey: {
    Key:        AccountDeletionOTPKey,
    SubjectFmt: "Delete your %s account",
    HTML:       AccountDeletionOTPTemplate,
},
```

---

### 1. `handler.go`

#### Handler struct and constructor

```go
// Handler is the HTTP layer for DELETE /me and POST /me/cancel-deletion.
// It parses requests, dispatches to the service, and maps sentinel errors to
// HTTP status codes. It has no knowledge of pgtype, pgxpool, JWT signing, or
// the KV store.
type Handler struct {
	svc  Servicer
	deps *app.Deps
}

func NewHandler(svc Servicer, deps *app.Deps) *Handler {
	return &Handler{svc: svc, deps: deps}
}
```

The `Servicer` interface **must not** be redeclared in `handler.go` — it is already
declared in `service.go` (same package). Do not duplicate it.

#### `enqueueEmail` helper

Copy the pattern from `internal/domain/profile/email/handler.go` exactly.
The `logPrefix` for the deletion OTP is `"deleteaccount.Delete"`.

#### `Delete` handler — `DELETE /me`

This is the most complex handler in the package. It dispatches across five paths
based on the request body fields and the user's auth methods.

**Full dispatch logic:**

```
1. MaxBytesReader
2. mustUserID → 401 if absent
3. DecodeJSON[deleteAccountRequest] → 400/422 if malformed

4. Path A — password present:
   if req.Password != "" {
       call svc.DeleteWithPassword(...)
       map errors (see §Error mapping below)
       on success → respond 200 with deletionScheduledResponse
       return
   }

5. Path B step 2 — code present:
   if req.Code != "" {
       call svc.ConfirmEmailDeletion(...)
       map errors
       on success → respond 200 with deletionScheduledResponse
       return
   }

6. Path C step 2 — telegram_auth present:
   if req.TelegramAuth != nil {
       call svc.ConfirmTelegramDeletion(...)
       map errors
       on success → respond 200 with deletionScheduledResponse
       return
   }

7. Empty body — resolve auth methods and dispatch step 1:
   call svc.ResolveUserForDeletion(ctx, userID)
   map errors

   // D-11: password account that sent no password field → 400
   if authMethods.HasPassword {
       respond.Error(w, 400, "validation_error",
           "password is required to delete a password-protected account")
       return
   }

   if user.Email != nil {
       // Path B step 1 — email user: issue OTP
       call svc.InitiateEmailDeletion(ctx, ScheduleDeletionInput{UserID, IPAddress, UserAgent})
       map errors
       enqueue OTP email to *user.Email (best-effort, AccountDeletionOTPKey)
       respond 202 {"message": "verification code sent to your email"}
       return
   }

   // Path C step 1 — Telegram-only user: prompt widget
   respond 202 {"message": "authenticate via Telegram to confirm deletion",
                "auth_method": "telegram"}
```

**Response structs (unexported, file-level):**

```go
type deletionScheduledResponse struct {
	Message             string    `json:"message"`
	ScheduledDeletionAt time.Time `json:"scheduled_deletion_at"`
}

type deletionInitiatedResponse struct {
	Message    string `json:"message"`
	AuthMethod string `json:"auth_method,omitempty"`
}
```

Helper shared by all success paths that return a scheduled timestamp:

```go
func newDeletionScheduledResponse(result DeletionScheduled) deletionScheduledResponse {
	return deletionScheduledResponse{
		Message:             "account scheduled for deletion",
		ScheduledDeletionAt: result.ScheduledDeletionAt,
	}
}
```

#### Error mapping for `Delete`

Use a **single shared private method** rather than copy-pasting the switch into every
code path. Suggested signature:

```go
func (h *Handler) mapDeleteError(w http.ResponseWriter, r *http.Request, err error)
```

Error → HTTP mapping:

```
ErrAlreadyPendingDeletion          → 409 "already_pending_deletion"
authshared.ErrInvalidCredentials   → 401 "invalid_credentials"
ErrInvalidTelegramAuth             → 401 "invalid_telegram_auth"
ErrTelegramIdentityMismatch        → 401 "telegram_identity_mismatch"
authshared.ErrCodeInvalidFormat    → 422 "validation_error"
authshared.ErrTokenNotFound        → 422 "token_not_found"
authshared.ErrTokenAlreadyUsed     → 422 "token_already_used"
authshared.ErrInvalidCode          → 422 "invalid_code"
authshared.ErrTooManyAttempts      → 429 "too_many_attempts"
default                            → slog.ErrorContext + 500 "internal_error"
```

#### `CancelDeletion` handler — `POST /me/cancel-deletion`

```
1. MaxBytesReader
2. mustUserID → 401 if absent
3. DecodeJSON[cancelDeletionRequest] (empty struct — keeps pattern, swallows body)
4. svc.CancelDeletion(ctx, CancelDeletionInput{UserID, IPAddress, UserAgent})
5. Error mapping:
   ErrNotPendingDeletion → 409 "not_pending_deletion"
   default               → slog.ErrorContext + 500 "internal_error"
6. respond 200 {"message": "account deletion cancelled"}
```

#### `mustUserID` helper

```go
func (h *Handler) mustUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := token.UserIDFromContext(r.Context())
	if !ok || userID == "" {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "missing user id in context")
		return "", false
	}
	return userID, true
}
```

---

### 2. `routes.go`

```go
// Routes registers DELETE /me and POST /me/cancel-deletion on r.
// Call from the profile domain assembler:
//
//	deleteaccount.Routes(ctx, r, deps)
//
// Rate limits (Stage 0 §6):
//   - DELETE /me:               3 req / 1 hr per user  ("del:usr:")
//   - POST /me/cancel-deletion: 5 req / 10 min per user ("delc:usr:")
//
// Middleware ordering:
//
//	JWTAuth → UserRateLimiter → handler.{Method}
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 3 req / 1 hr per user.
	// rate = 3.0 / (60 * 60) ≈ 0.000833 tokens/sec.
	deleteLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "del:usr:", 3.0/(60*60), 3, 1*time.Hour,
	)
	go deleteLimiter.StartCleanup(ctx)

	// 5 req / 10 min per user.
	// rate = 5.0 / (10 * 60) ≈ 0.00833 tokens/sec.
	cancelLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "delc:usr:", 5.0/(10*60), 5, 10*time.Minute,
	)
	go cancelLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store, deps.OTPTokenTTL, deps.OAuth.TelegramBotToken)
	h := NewHandler(svc, deps)

	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.With(deleteLimiter.Limit).Delete("/me", h.Delete)
		r.With(cancelLimiter.Limit).Post("/me/cancel-deletion", h.CancelDeletion)
	})
}
```

---

### 3. Wire into `internal/domain/profile/routes.go`

Add the import and call:

```go
import (
	// ... existing imports ...
	deleteaccount "github.com/7-Dany/store/backend/internal/domain/profile/delete-account"
)

func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	// ... existing registrations ...
	deleteaccount.Routes(ctx, r, deps)
	return r
}
```

---

### 4. `handler_test.go`

Write handler unit tests using `DeleteAccountFakeServicer` from
`internal/domain/auth/shared/testutil`.

Follow the table-driven pattern of `internal/domain/profile/email/handler_test.go`
or `internal/domain/profile/set-password/handler_test.go`.

**Test cases to cover (H-layer cases from Stage 0 §9):**

| # | Case | Handler method | Setup | Expected |
|---|---|---|---|---|
| T-04 | Telegram-only step 1 → 202 | Delete | ResolveUserForDeletionFn returns user with Email==nil + IdentityCount>0 | 202; body `"auth_method":"telegram"` |
| T-06 | Already pending → 409 | Delete | Any service Fn returns ErrAlreadyPendingDeletion | 409 `already_pending_deletion` |
| T-07 | Password account, no password field → 400 | Delete | ResolveUserForDeletionFn returns authMethods.HasPassword==true | 400 `validation_error` |
| T-09 | OTP wrong format → 422 | Delete | ConfirmEmailDeletionFn returns authshared.ErrCodeInvalidFormat | 422 `validation_error` |
| T-14 | Telegram HMAC fails → 401 | Delete | ConfirmTelegramDeletionFn returns ErrInvalidTelegramAuth | 401 `invalid_telegram_auth` |
| T-15 | auth_date too old → 401 | Delete | ConfirmTelegramDeletionFn returns ErrInvalidTelegramAuth | 401 `invalid_telegram_auth` |
| T-17 | No JWT → 401 | Delete + CancelDeletion | No JWT in context | 401 `unauthorized` |
| T-20 | Success includes scheduled_deletion_at | Delete | DeleteWithPasswordFn returns DeletionScheduled{t} | JSON field matches t |
| T-27 | Cancel happy path → 200 | CancelDeletion | CancelDeletionFn returns nil | 200 `"account deletion cancelled"` |
| T-28 | Not pending → 409 | CancelDeletion | CancelDeletionFn returns ErrNotPendingDeletion | 409 `not_pending_deletion` |
| T-29 | No JWT on cancel → 401 | CancelDeletion | No JWT | 401 `unauthorized` |

---

## Dispatch summary (complete)

```
DELETE /me
├── req.Password != ""       → Path A  — DeleteWithPassword       → 200
├── req.Code != ""           → Path B2 — ConfirmEmailDeletion      → 200
├── req.TelegramAuth != nil  → Path C2 — ConfirmTelegramDeletion   → 200
└── (empty body)
    ResolveUserForDeletion
    ├── error                     → mapDeleteError
    ├── authMethods.HasPassword   → 400 validation_error  (D-11)
    ├── user.Email != nil         → Path B1 — InitiateEmailDeletion → 202
    └── user.Email == nil         → Path C1 — 202 auth_method:telegram
```

---

## Run after implementing

```bash
go build ./internal/platform/mailer/templates/...
go build ./internal/domain/profile/delete-account/...
go build ./internal/domain/profile/...
go vet  ./internal/platform/mailer/templates/...
go vet  ./internal/domain/profile/delete-account/...
go test ./internal/domain/profile/delete-account/... -run TestHandler -v
```

All must pass before proceeding to Stage 5.

---

## Stage 4 complete → proceed to Stage 5

Stage 5 is the store integration tests (`store_test.go`) and the background purge worker
(`internal/worker/purge.go`).
