# Email Change — Resolved Context

**Section:** INCOMING.md §B-2
**Package:** `internal/domain/profile/email/`
**Status:** Stage 0 approved (pending Q-01/Q-02/Q-03 answers) → Stage 1 ready

## Resolved paths
- SQL file: `sql/queries/auth.sql` (new section: `/* ── Email change ── */`)
- Models: `internal/domain/profile/email/models.go`
- Errors: `internal/domain/profile/email/errors.go`
- Validators: `internal/domain/profile/email/validators.go`
- Requests: `internal/domain/profile/email/requests.go`
- Store: `internal/domain/profile/email/store.go`
- Service: `internal/domain/profile/email/service.go`
- Handler: `internal/domain/profile/email/handler.go`
- Routes: `internal/domain/profile/email/routes.go`
- FakeStorer: `internal/domain/auth/shared/testutil/fake_storer.go`
- FakeServicer: `internal/domain/auth/shared/testutil/fake_servicer.go`
- QuerierProxy: `internal/domain/auth/shared/testutil/querier_proxy.go`
- Analogous feature: `internal/domain/profile/username/` (closest analogue)

## Key decisions (from Stage 0 §3)
- D-01: new_email carry step1→2 via KV (`echg:pending:{userID}`, 12 min TTL). Update if Q-01 resolves metadata column.
- D-02: email_change_verify OTP lookup by (user_id, token_type) not email
- D-03: email_change_confirm OTP lookup by (user_id, token_type); grant token holds new_email
- D-04: Grant token = random UUID key, KV prefix `echg:gt:`, TTL 10 min, value = `{userID}:{new_email}`
- D-05: Uniqueness re-checked inside ConfirmEmailChangeTx (23505 → ErrEmailTaken)
- D-06: Step 3 revokes all refresh tokens (reason="email_changed") + ends sessions + blocklists access token
- D-07: ErrUserNotFound on authenticated user → 500 (not 404)
- D-08: Cooldown via GetLatestEmailChangeVerifyTokenCreatedAt < 2 min
- D-09: email_change_confirm token stores new_email in `email` column for audit readability
- D-10: Invalidate old tokens before creating new (both verify and confirm types)
- D-11: SQL appended to auth.sql; no profile.sql exists
- D-12: max_attempts = 5 for both new token types

## New SQL queries (11 total, append to auth.sql)
- CheckEmailAvailableForChange
- GetLatestEmailChangeVerifyTokenCreatedAt
- InvalidateUserEmailChangeVerifyTokens
- CreateEmailChangeVerifyToken
- GetEmailChangeVerifyToken (FOR UPDATE)
- ConsumeEmailChangeToken (covers both token types)
- InvalidateUserEmailChangeConfirmTokens
- CreateEmailChangeConfirmToken
- GetEmailChangeConfirmToken (FOR UPDATE)
- GetUserForEmailChangeTx (FOR UPDATE)
- SetUserEmail

## New audit events
- EventEmailChangeRequested = "email_change_requested"
- EventEmailChangeCurrentVerified = "email_change_current_verified"
- EventEmailChanged = "email_changed"

## New sentinel errors (in internal/domain/profile/email/errors.go)
- ErrInvalidEmailFormat, ErrEmailTooLong (validation)
- ErrInvalidCodeFormat, ErrGrantTokenEmpty (validation)
- ErrSameEmail, ErrEmailTaken, ErrCooldownActive, ErrGrantTokenInvalid (flow)
- OTP sentinels: check authshared before defining (ErrTokenNotFound etc. may exist there)

## Rate-limit prefixes (user-scoped, all three endpoints)
- `echg:usr:` — POST /email/request-change (3 req / 10 min)
- `echg:usr:vfy:` — POST /email/verify-current (5 req / 15 min)
- `echg:usr:cnf:` — POST /email/confirm-change (5 req / 15 min)

## Test case IDs (from Stage 0 §7)
- Step 1: T-01 to T-12 (S+H+I)
- Step 2: T-13 to T-24 (S+H+I)
- Step 3: T-25 to T-42 (S+H+I)
- S-layer: T-01,05,06,07,09,10 + T-13,16,17,18,19,20,21 + T-25,28,29,30,31,32,33,34,35,36,37,38,39
- H-layer: T-02,03,04,05,06,07,08 + T-14,15,16,17,18,22 + T-26,27,28,30,31,32,34
- I-layer: T-01,11,12 + T-13,23,24 + T-25,40,41,42
