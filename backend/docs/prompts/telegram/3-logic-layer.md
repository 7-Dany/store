# §D-2 Telegram OAuth — Stage 3: Logic Layer

**Depends on:** Stage 2 complete — `internal/domain/oauth/telegram/service.go` declares
the Storer interface, `internal/domain/oauth/telegram/store.go` implements it,
`TelegramFakeStorer` is in `internal/domain/oauth/shared/testutil/fake_storer.go`,
and both packages compile cleanly.

**Stage goal:** Service methods implemented (`HandleCallback`, `LinkTelegram`,
`UnlinkTelegram`), Servicer interface declared (in `handler.go`), `TelegramFakeServicer`
added to `internal/domain/oauth/shared/testutil/fake_servicer.go`, and service unit
tests written and passing for every S-layer case below.

---

## Read first (no modifications)

| File | Why |
|---|---|
| `docs/prompts/telegram/0-design.md` | Source of truth — guard ordering (§4), error mapping (§10), security decisions |
| `internal/domain/oauth/telegram/service.go` | Storer interface — what the service can call |
| `internal/domain/oauth/telegram/models.go` | Input/result types |
| `internal/domain/oauth/telegram/errors.go` | Package sentinel errors |
| `internal/domain/oauth/google/service.go` | Canonical service pattern to mirror for HandleCallback and UnlinkTelegram |
| `internal/domain/oauth/shared/errors.go` | ErrIdentityNotFound, ErrAccountLocked, ErrLastAuthMethod |
| `internal/domain/auth/shared/errors.go` | ErrUserNotFound |
| `internal/domain/oauth/shared/testutil/fake_storer.go` | TelegramFakeStorer — Stage 2 state |
| `internal/domain/oauth/shared/testutil/fake_servicer.go` | GoogleFakeServicer layout to mirror for TelegramFakeServicer |
| `internal/audit/audit.go` | EventOAuthLogin, EventOAuthLinked, EventOAuthUnlinked |
| `docs/RULES.md §3.4, §3.6` | Error wrapping: `fmt.Errorf("telegram.Method: step: %w", err)`, WithoutCancel |
| `docs/RULES.md §4.6` | Security annotation format |

---

## Pre-flight

1. Confirm Stage 2 compiles:
   ```
   go build ./internal/domain/oauth/telegram/...
   go build ./internal/domain/oauth/shared/testutil/...
   ```
2. Confirm `internal/domain/oauth/telegram/handler.go` contains only the package
   declaration (Servicer interface and Handler struct go here — Stage 3 adds the interface,
   Stage 4 adds the full handler).
3. Confirm `TelegramFakeServicer` does NOT yet exist in `fake_servicer.go`.
4. Confirm existing tests still compile: `go test -run=^$ ./internal/domain/oauth/...`

---

## Deliverables

### 1. `internal/domain/oauth/telegram/service.go` — Service + three methods

Add imports, the `Service` struct, `NewService`, the compile-time check
`var _ Servicer = (*Service)(nil)`, and all three service methods.

#### Guard ordering — HandleCallback

HMAC verification and auth_date checking happen in the handler. The service
receives an already-validated `CallbackInput.User`.

```
1. GetIdentityByProviderUID(ctx, providerUID):
     FOUND    → Existing-user path
     NOT FOUND → New-user path

Existing-user path:
  2. GetUserForOAuthCallback(identity.UserID)
       → any error except no-rows → wrap and return
       → ErrUserNotFound (should not happen — identity references live user) → wrap
       → user.IsLocked || user.AdminLocked → ErrAccountLocked
  3. OAuthLoginTx(context.WithoutCancel(ctx), ...)  ← Security: WithoutCancel (D-17)
       → error → wrap

New-user path:
  2. OAuthRegisterTx(context.WithoutCancel(ctx), ...)  ← Security: WithoutCancel (D-17)
       → error → wrap
```

Return `CallbackResult{Session: session, NewUser: newUser}`.

#### Guard ordering — LinkTelegram

```
1. GetUserForOAuthCallback(ctx, in.UserID)
     → error → wrap
     → user.IsLocked || user.AdminLocked → oauthshared.ErrAccountLocked

2. GetIdentityByUserAndProvider(ctx, in.UserID)
     FOUND → ErrProviderAlreadyLinked

3. GetIdentityByProviderUID(ctx, providerUID)
     FOUND and row.UserID != in.UserID → ErrProviderUIDTaken
     FOUND and row.UserID == in.UserID → fall through (idempotent)
     NOT FOUND → continue

4. InsertUserIdentity(ctx, InsertIdentityInput{...})
     → error → wrap

5. InsertAuditLogTx(context.WithoutCancel(ctx), ...)  ← Security: WithoutCancel (D-17)
     → error → wrap
```

Return `nil` on success.

> `providerUID` = `strconv.FormatInt(in.User.ID, 10)`

#### Guard ordering — UnlinkTelegram

```
1. GetUserAuthMethods(ctx, userID)
     → error → wrap

2. GetIdentityByUserAndProvider(ctx, userID)
     FOUND → continue
     ErrIdentityNotFound → ErrProviderNotLinked
     other error → wrap

3. Last-auth-method guard:
     pwCount = 0; if methods.HasPassword { pwCount = 1 }
     if pwCount + methods.IdentityCount <= 1 → oauthshared.ErrLastAuthMethod

4. DeleteUserIdentity(ctx, userID)
     → error → wrap
     → 0 rows → ErrProviderNotLinked (lost race)

5. InsertAuditLogTx(context.WithoutCancel(ctx), ...)  ← Security: WithoutCancel (D-17)
     → error → wrap
```

Return `nil` on success.

---

### 2. `internal/domain/oauth/telegram/handler.go` — Servicer interface

Replace the package-declaration-only stub with the Servicer interface. Mirror the
google Servicer interface pattern.

```go
// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram

import "context"

// Servicer is the business-logic contract for the Telegram OAuth handler.
// *Service satisfies this interface; TelegramFakeServicer in shared/testutil
// satisfies it for handler unit tests.
type Servicer interface {
	HandleCallback(ctx context.Context, in CallbackInput) (CallbackResult, error)
	LinkTelegram(ctx context.Context, in LinkInput) error
	UnlinkTelegram(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error
}
```

---

### 3. `internal/domain/oauth/shared/testutil/fake_servicer.go` — TelegramFakeServicer

Add `TelegramFakeServicer` below `GoogleFakeServicer`. Mirror the GoogleFakeServicer
structure exactly.

```go
// TelegramFakeServicer is a hand-written implementation of telegram.Servicer for
// handler unit tests. Each method delegates to its Fn field if non-nil, otherwise
// returns the zero value and nil error.
type TelegramFakeServicer struct {
    HandleCallbackFn func(ctx context.Context, in telegram.CallbackInput) (telegram.CallbackResult, error)
    LinkTelegramFn   func(ctx context.Context, in telegram.LinkInput) error
    UnlinkTelegramFn func(ctx context.Context, userID [16]byte, ipAddress, userAgent string) error
}

var _ telegram.Servicer = (*TelegramFakeServicer)(nil)
```

Add a compile-time check: `var _ telegram.Servicer = (*TelegramFakeServicer)(nil)`

> **Import note:** Add `"github.com/7-Dany/store/backend/internal/domain/oauth/telegram"` to
> the import block in `fake_servicer.go`.

---

### 4. `internal/domain/oauth/telegram/service_test.go` — Service unit tests

Test function: `TestService_HandleCallback`, `TestService_LinkTelegram`,
`TestService_UnlinkTelegram`. Use `t.Parallel()` in every sub-test.
Use `oauthsharedtest.TelegramFakeStorer` for all store fakes.

#### S-layer test cases

| Test ID | Method | Case | Setup | Assert |
|---|---|---|---|---|
| S-01 | HandleCallback | Happy path: new user | GetIdentityByProviderUID returns ErrIdentityNotFound; OAuthRegisterTxFn returns populated session | Returns CallbackResult{NewUser: true}; OAuthRegisterTx called once |
| S-02 | HandleCallback | Happy path: returning user | GetIdentityByProviderUID returns identity; GetUserForOAuthCallback returns active user; OAuthLoginTxFn returns session | Returns CallbackResult{NewUser: false}; OAuthLoginTx called once |
| S-03 | HandleCallback | Account locked (returning user) | GetIdentityByProviderUID FOUND; GetUserForOAuthCallback returns {IsLocked: true} | errors.Is(err, oauthshared.ErrAccountLocked) |
| S-04 | HandleCallback | GetIdentityByProviderUID unexpected error | GetIdentityByProviderUIDFn returns non-sentinel error | err.Error() contains "telegram.HandleCallback:" |
| S-05 | HandleCallback | OAuthLoginTx failure | GetIdentityByProviderUID FOUND, user active, OAuthLoginTxFn returns error | err.Error() contains "telegram.HandleCallback:" |
| S-06 | HandleCallback | OAuthRegisterTx failure | Not found, OAuthRegisterTxFn returns error | err.Error() contains "telegram.HandleCallback:" |
| S-07 | HandleCallback | OAuthLoginTx called with WithoutCancel ctx | GetIdentityByProviderUID FOUND, user active; capture ctx in OAuthLoginTxFn | captured ctx.Done() == nil |
| S-08 | HandleCallback | OAuthRegisterTx called with WithoutCancel ctx | Not found; capture ctx in OAuthRegisterTxFn | captured ctx.Done() == nil |
| S-09 | LinkTelegram | Happy path | GetUserForOAuthCallback active; GetIdentityByUserAndProvider ErrNotFound; GetIdentityByProviderUID ErrNotFound; InsertUserIdentity nil; InsertAuditLogTx nil | returns nil; InsertUserIdentity called once |
| S-10 | LinkTelegram | User already linked (ErrProviderAlreadyLinked) | GetUserForOAuthCallback active; GetIdentityByUserAndProvider returns identity (FOUND) | errors.Is(err, ErrProviderAlreadyLinked) |
| S-11 | LinkTelegram | Telegram UID taken by another user (ErrProviderUIDTaken) | GetIdentityByUserAndProvider ErrNotFound; GetIdentityByProviderUID returns identity with different UserID | errors.Is(err, ErrProviderUIDTaken) |
| S-12 | LinkTelegram | Idempotent: same user already has this identity | GetIdentityByUserAndProvider ErrNotFound; GetIdentityByProviderUID returns identity with SAME UserID | returns nil; InsertUserIdentity called once |
| S-13 | LinkTelegram | Account locked | GetUserForOAuthCallback returns {IsLocked: true} | errors.Is(err, oauthshared.ErrAccountLocked) |
| S-14 | LinkTelegram | InsertAuditLogTx uses WithoutCancel | Happy-path setup; capture ctx in InsertAuditLogTxFn | captured ctx.Done() == nil |
| S-15 | UnlinkTelegram | Happy path (user has password) | GetUserAuthMethods {HasPassword: true, IdentityCount: 1}; GetIdentityByUserAndProvider FOUND; DeleteUserIdentity (1, nil); InsertAuditLogTx nil | returns nil |
| S-16 | UnlinkTelegram | Happy path (user has other OAuth identity) | GetUserAuthMethods {HasPassword: false, IdentityCount: 2}; GetIdentityByUserAndProvider FOUND; DeleteUserIdentity (1, nil) | returns nil |
| S-17 | UnlinkTelegram | Provider not linked | GetIdentityByUserAndProvider returns ErrIdentityNotFound | errors.Is(err, ErrProviderNotLinked) |
| S-18 | UnlinkTelegram | Last auth method | GetUserAuthMethods {HasPassword: false, IdentityCount: 1}; GetIdentityByUserAndProvider FOUND | errors.Is(err, oauthshared.ErrLastAuthMethod) |
| S-19 | UnlinkTelegram | Delete returns 0 rows (lost race) | GetUserAuthMethods {HasPassword: true, IdentityCount: 1}; FOUND; DeleteUserIdentity (0, nil) | errors.Is(err, ErrProviderNotLinked) |
| S-20 | UnlinkTelegram | InsertAuditLogTx uses WithoutCancel | Happy-path setup; capture ctx in InsertAuditLogTxFn | captured ctx.Done() == nil |
| S-21 | UnlinkTelegram | GetUserAuthMethods error | GetUserAuthMethodsFn returns error | err.Error() contains "telegram.UnlinkTelegram:" |

---

## Done when

```bash
# Package compiles with Servicer interface declared
go build ./internal/domain/oauth/telegram/...

# Testutil compiles with TelegramFakeServicer added
go build ./internal/domain/oauth/shared/testutil/...

# All service unit tests pass
go test ./internal/domain/oauth/telegram/... -run TestService_ -v

# Full build still passes
go build ./internal/...
```

All service tests must pass. Fix failures before proceeding to Stage 4.

**Stop here. Do not implement the handler body or routes. Stage 4 starts in a separate session.**
