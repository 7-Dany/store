# Delete Account — Resolved Context

**Section:** INCOMING.md §B-3
**Package:** `internal/domain/profile/delete-account/` (package `deleteaccount`)
**Status:** Stage 0 approved

## Resolved paths
- SQL file (feature queries): `sql/queries/auth.sql` (new section: `/* ── Delete Account ── */`)
- SQL file (worker queries): `sql/queries/worker.sql` (new file)
- Migration: `sql/schema/00N_account_deletion.sql`
- Models: `internal/domain/profile/delete-account/models.go`
- Requests: `internal/domain/profile/delete-account/requests.go`
- Validators: `internal/domain/profile/delete-account/validators.go`
- Errors: `internal/domain/profile/delete-account/errors.go`
- Store: `internal/domain/profile/delete-account/store.go`
- Service: `internal/domain/profile/delete-account/service.go`
- Handler: `internal/domain/profile/delete-account/handler.go`
- Routes: `internal/domain/profile/delete-account/routes.go`
- Worker: `internal/worker/purge.go`
- FakeStorer: `internal/domain/auth/shared/testutil/fake_storer.go` (add `DeleteAccountFakeStorer`)
- FakeServicer: `internal/domain/auth/shared/testutil/fake_servicer.go` (add `DeleteAccountFakeServicer`)
- QuerierProxy: `internal/domain/auth/shared/testutil/querier_proxy.go`

## Key decisions (from Stage 0 §3)
- D-01: Soft + 30-day grace period, then background hard-delete
- D-02: Sessions NOT revoked at soft-delete (user must stay logged in to cancel)
- D-03: Cancel via POST /me/cancel-deletion, JWT required, no extra confirmation
- D-04: Login for pending-deletion accounts succeeds; scheduled_deletion_at in response
- D-05: Login for post-grace-period accounts blocked via GetUserForLogin WHERE clause
- D-06: Password user — single step, password in body
- D-07: Email-OTP user — two-step via same endpoint (empty body → OTP; code → verify)
- D-08: Telegram-only user — HMAC re-auth, two-step (empty body → 202; telegram_auth → verify)
- D-09: VerifyTelegramHMAC reused from oauth/telegram; provider_uid ownership check
- D-10: Telegram step 1 202 response includes auth_method: "telegram"
- D-11: Auth dispatch order: deleted_at check → password field? → GetUserAuthMethods → route
- D-12: Password path takes priority if password field present
- D-13: Google OAuth users always have users.email populated; Telegram is the only nil-email case
- D-14: Hard-delete via DELETE FROM users CASCADE; purge_log written before DELETE
- D-15: account_purge_log has no FK to users (user row is gone by then)
- D-16: PurgeHandler implements jobqueue.Handler (Handle loops until batch exhausted); thin PurgeWorker goroutine drives it until job queue Phase 7
- D-21: PurgeHandler shaped as jobqueue.Handler from day one; KindPurgeAccounts defined in kinds.go now to avoid later refactoring
- D-17: DELETE /me 3 req/1 hr per user (del:usr:); cancel 5 req/10 min per user (delc:usr:)
- D-18: 409 check is first guard after mustUserID
- D-19: OTP TTL = config.OTPValidMinutes (15 min), max 3 attempts
- D-20: Folder delete-account/, package deleteaccount (delete is a Go keyword)

## New SQL queries

### auth.sql — new `/* ── Delete Account ── */` section
- GetUserForDeletion
- ScheduleUserDeletion
- CancelUserDeletion
- InvalidateUserDeletionTokens
- CreateAccountDeletionToken
- GetAccountDeletionToken
- ConsumeAccountDeletionToken

### auth.sql — existing query modification
- GetUserForLogin: add deleted_at to SELECT; add grace-period guard to WHERE

### worker.sql (new file)
- GetAccountsDueForPurge
- HardDeleteUser
- InsertPurgeLog

## New audit events
- EventAccountDeletionRequested = "account_deletion_requested"
- EventAccountDeletionOTPRequested = "account_deletion_otp_requested"
- EventAccountDeletionCancelled = "account_deletion_cancelled"

## New sentinel errors (deleteaccount package)
- ErrAlreadyPendingDeletion — 409
- ErrNotPendingDeletion — 409

(All OTP-related errors reused from authshared: ErrTokenNotFound, ErrTokenAlreadyUsed,
ErrTooManyAttempts, ErrInvalidCode, ErrInvalidCredentials)
- ErrInvalidTelegramAuth — deleteaccount package (new)
- ErrTelegramIdentityMismatch — deleteaccount package (new)

## Rate-limit prefixes
- del:usr: → DELETE /me (3 req / 1 hr per user)
- delc:usr: → POST /me/cancel-deletion (5 req / 10 min per user)

## Test case IDs (from Stage 0 §7)
- DELETE /me: T-01 to T-26
- POST /me/cancel-deletion: T-27 to T-31
- Cross-cutting (login + GET /me): T-32 to T-36
- Worker: T-37 to T-42
