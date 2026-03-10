# §B-3 — Delete Account — Stage 0: Design & Decisions (v3 — FINAL)

**Requirement source:** `docs/map/INCOMING.md §B-3` + owner clarifications
**Target package:** `internal/domain/profile/delete-account/` (package `deleteaccount`)

---

## Resolved open questions (from prior drafts)

| Q | Resolution |
|---|---|
| Q-01: `token_type` ENUM or CHECK? | **ENUM** — `one_time_token_type` in `sql/schema/001_core.sql`. Add `account_deletion` via `ALTER TYPE one_time_token_type ADD VALUE 'account_deletion';` in a new migration. |
| Q-02: `deleted_at` column exists? | **Yes** — confirmed in `001_core.sql`. No column migration needed. |
| Q-03: Block expired-grace-period accounts from login? | **Yes** — add `AND (deleted_at IS NULL OR deleted_at > NOW() - INTERVAL '30 days')` to `GetUserForLogin`. |
| Q-04: Own package or extend `me/`? | **Own package** — `internal/domain/profile/delete-account/` (package `deleteaccount`). Too much logic to co-locate with `me/`. |

---

## Read first (no modifications)

| File | What to extract |
|---|---|
| `docs/map/INCOMING.md §B-3` | Requirement text |
| `docs/RULES.md` | Global conventions, error handling, ADR-004 |
| `docs/rules/auth.md` | OTP patterns, guard ordering, ADR references |
| `internal/domain/profile/routes.go` | Assembler — new import + `deleteaccount.Routes` call goes here |
| `internal/domain/profile/me/handler.go` | mustUserID pattern to replicate |
| `internal/domain/profile/me/store.go` | BaseStore usage pattern |
| `internal/domain/profile/shared/store.go` | profileshared.BaseStore alias |
| `internal/domain/profile/shared/errors.go` | Shared sentinels |
| `internal/domain/auth/login/service.go` | LoginUser model — thread scheduled_deletion_at |
| `internal/domain/auth/login/models.go` | LoggedInSession — additive field |
| `internal/domain/auth/login/handler.go` | Login response struct |
| `internal/domain/auth/oauth/telegram/` | Telegram HMAC verification — reuse the same logic |
| `sql/queries/auth.sql` | GetUserForLogin — add deleted_at + expiry guard |
| `sql/queries/oauth.sql` | GetUserAuthMethods, GetIdentityByUserAndProvider |
| `internal/db/oauth.sql.go` | GetUserAuthMethods generated signature |
| `internal/domain/auth/shared/testutil/fake_storer.go` | Add DeleteAccountFakeStorer here |
| `internal/domain/auth/shared/testutil/fake_servicer.go` | Add DeleteAccountFakeServicer here |
| `internal/audit/audit.go` | Confirm no name collision |
| `internal/platform/kvstore/store.go` | KV store interface |
| `sql/schema/001_core.sql` | Confirm one_time_token_type ENUM values |

---

## 1. Feature summary

Three-phase account lifecycle implemented across a new `deleteaccount` package,
minor additive changes to `auth/login`, and a new background worker.

**Phase 1 — Soft-delete (`DELETE /me`):** After confirmation (varies by auth
method — see §3), `users.deleted_at` is stamped. Sessions and tokens are **not**
revoked — the user keeps access so they can cancel. An `account_deletion_requested`
audit row is written inside the transaction.

**Phase 2 — Grace period (30 days):** Login still succeeds but includes
`scheduled_deletion_at` in the response. `GET /me` also exposes it. The user
cancels via `POST /me/cancel-deletion`, which clears `deleted_at` and writes an
`account_deletion_cancelled` audit row.

**Phase 3 — Hard-delete (background worker):** A goroutine started at app boot
scans hourly for `deleted_at < NOW() - INTERVAL '30 days'`, hard-deletes each
expired account (CASCADE handles child rows), and writes a row to
`account_purge_log` (no FK to `users` — the user row will be gone).

---

## 2. HTTP contract

### A. DELETE /api/v1/profile/me — Initiate deletion

**Auth required:** Yes — valid JWT

**Request body — four variants depending on auth method and step:**

```jsonc
// (1) Password user — single step:
{ "password": "current-password" }

// (2) Email-OTP user, step 1 — trigger OTP (empty body or missing field):
{}

// (3) Email-OTP user, step 2 — confirm OTP:
{ "code": "123456" }

// (4) Telegram-only user, step 2 — HMAC confirmation:
{
  "telegram_auth": {
    "id":         123456789,
    "first_name": "Alice",
    "username":   "alice_tg",
    "photo_url":  "https://...",   // optional
    "auth_date":  1700000000,
    "hash":       "abc123..."
  }
}
```

**Success responses:**

| Status | Body | Condition |
|---|---|---|
| `200 OK` | `{"message":"account scheduled for deletion","scheduled_deletion_at":"<RFC3339>"}` | Soft-delete completed (password user or OTP/Telegram step 2) |
| `202 Accepted` | `{"message":"verification code sent to your email"}` | Email-OTP step 1 — OTP sent |
| `202 Accepted` | `{"message":"authenticate via Telegram to confirm deletion","auth_method":"telegram"}` | Telegram-only step 1 — widget prompt |

**Error responses:**

| Status | Code | Condition |
|---|---|---|
| 400 | `validation_error` | Password user submitted request with no `password` field |
| 401 | `unauthorized` | Missing or invalid JWT |
| 401 | `invalid_credentials` | Password supplied but bcrypt mismatch |
| 401 | `invalid_telegram_auth` | HMAC verification failed **or** `auth_date` > 86400 s old |
| 401 | `telegram_identity_mismatch` | Telegram payload `id` doesn't match user's linked `provider_uid` |
| 409 | `already_pending_deletion` | `deleted_at` already set |
| 422 | `validation_error` | OTP code not exactly 6 digits |
| 422 | `invalid_code` | OTP code incorrect |
| 422 | `token_not_found` | No active `account_deletion` OTP for this user |
| 422 | `token_already_used` | OTP already consumed |
| 429 | `too_many_requests` | Rate limit exceeded |
| 429 | `too_many_attempts` | OTP attempt budget exhausted (max 3) |
| 500 | `internal_error` | Unexpected store or infrastructure error |

---

### B. POST /api/v1/profile/me/cancel-deletion — Cancel during grace period

**Auth required:** Yes — valid JWT
**Request body:** None

**Success response:** `200 OK`
```json
{ "message": "account deletion cancelled" }
```

| Status | Code | Condition |
|---|---|---|
| 401 | `unauthorized` | Missing or invalid JWT |
| 409 | `not_pending_deletion` | `deleted_at` is already NULL |
| 429 | `too_many_requests` | Rate limit exceeded |
| 500 | `internal_error` | Unexpected store error |

---

### C. Additive changes to existing endpoints

**`GET /me` response — new optional field:**
```json
{ "scheduled_deletion_at": "2025-08-15T10:30:00Z" }
```
`omitempty` — absent for normal accounts. Value is `deleted_at + 30 days`.

**`POST /login` response — new optional field:**
```json
{ "scheduled_deletion_at": "2025-08-15T10:30:00Z" }
```
`omitempty`. Login returns `200 OK` for pending-deletion accounts — blocking would
prevent the user from reaching the cancel flow.

---

## 3. Decisions

| # | Question | Decision | Rationale |
|---|---|---|---|
| D-01 | Soft or immediate hard-delete? | Soft + 30-day grace, then background hard-delete | Owner requirement. |
| D-02 | Revoke sessions at soft-delete? | **No** | User must stay logged in to cancel. |
| D-03 | Cancel mechanism? | `POST /me/cancel-deletion`, JWT required, no extra confirmation | Authenticated user cancelling deletion is low-risk; one-call cancel is sufficient. |
| D-04 | Login for pending-deletion accounts? | Succeeds; `scheduled_deletion_at` in response | Frontend shows warning + cancel button. API does not block. |
| D-05 | Login for post-grace-period accounts not yet purged? | Blocked — add `AND (deleted_at IS NULL OR deleted_at > NOW() - INTERVAL '30 days')` to `GetUserForLogin` | A user whose grace period expired but who hasn't been purged yet should not be able to log in and undo via the cancel endpoint. The cancel endpoint itself will 409 because `deleted_at > 30 days ago` is a non-null value, but login is the cleaner gate. |
| D-06 | Password user confirmation? | `{ "password": "..." }` in body — bcrypt verify, single step | Synchronous, no OTP infrastructure needed. |
| D-07 | Email-OTP user confirmation? | Two-step via same endpoint: empty body → OTP + 202; `{ "code": "..." }` → verify + 200 | Consistent with all existing OTP flows in the codebase. |
| D-08 | Telegram-only user confirmation? | **Telegram HMAC re-auth** — two-step: empty body → 202 `auth_method: telegram`; `{ "telegram_auth": {...} }` → HMAC verify + 200 | Telegram accounts have no email, so OTP to email is impossible. Re-running the same HMAC verification already implemented in `oauth/telegram/` reuses existing code and provides equivalent proof-of-possession. |
| D-09 | Telegram HMAC verification details? | Reuse the same `VerifyTelegramHMAC(botToken, data)` function from `oauth/telegram/`. Verify `auth_date` < 86400 s old. Verify `id` == user's `provider_uid` from `user_identities` (fetched in the same DB call). | The `provider_uid` check is the ownership proof — it ensures the Telegram account in the widget response is the one actually linked to this user, not any arbitrary Telegram account. |
| D-10 | Step 1 for Telegram-only users — what does 202 tell the frontend? | `{ "message": "...", "auth_method": "telegram" }` | Frontend reads `auth_method` to know it must render the Telegram Login Widget rather than an OTP input. |
| D-11 | Auth method dispatch order in the handler? | (1) Check `deleted_at` for 409. (2) If `password_field` present → password path. (3) Else call `GetUserAuthMethods`. (4) If `has_password` → 400 (they should have sent a password). (5) If `identity_count > 0`, determine provider: if `code` or `telegram_auth` present → confirm step; else → step 1 / discovery. | `GetUserAuthMethods` is called only once, after the guard checks. |
| D-12 | What if a user has both a password AND a linked Telegram account? | Password path always takes priority — if `password` field is present, use it. This is consistent with the existing pattern: password users always authenticate with their password. | |
| D-13 | What if a user has a linked Google account (email provided by Google) and no `users.email`? | `users.email` is always populated for Google OAuth users (Google guarantees an email). Telegram is the only provider where `users.email` can be NULL. So the email-OTP path covers Google users. | |
| D-14 | Hard-delete scope? | `DELETE FROM users WHERE id = $1` — all child tables (refresh_tokens, user_sessions, one_time_tokens, user_identities, auth_audit_log, user_roles) CASCADE. `account_purge_log` row written **before** the DELETE. | |
| D-15 | Why a separate `account_purge_log` table? | `auth_audit_log.user_id` is a FK to `users`. Once the user row is deleted, inserting an audit row would violate the FK. `account_purge_log` stores a bare UUID with no FK so it survives the hard-delete. | |
| D-16 | Hard-delete worker trigger? | `PurgeHandler` in `internal/worker/purge.go` implements `jobqueue.Handler` from the start. Core purge logic lives in `Handle(ctx context.Context, job jobqueue.Job) error`, which loops internally until the full batch drains (< 100 returned). A thin `PurgeWorker` goroutine calls `Handle` on a 1-hour ticker until the job queue is wired (Phase 7 of the job queue implementation). At that point, the goroutine is deleted and a `purge_accounts_hourly` schedule in `job_schedules` drives it instead — zero refactoring of the handler logic required. | Implements `jobqueue.Handler` contract from day one; the goroutine is only the temporary trigger. |
| D-17 | Rate limits? | `DELETE /me`: 3 req / 1 hr per user (`del:usr:`). `POST /me/cancel-deletion`: 5 req / 10 min per user (`delc:usr:`). Both user-keyed, inside JWT middleware. | Per INCOMING.md for DELETE; cancel is more permissive. |
| D-18 | Already-pending 409 check — where in the guard ordering? | **First guard after mustUserID**, before any DB auth-method lookup. | Avoids unnecessary DB work and returns a clear signal. |
| D-19 | OTP TTL and max attempts? | 15 min TTL (`config.OTPValidMinutes`), max 3 attempts. | Consistent with all other OTP flows. |
| D-20 | Package name? | Folder: `delete-account/`. Package: `deleteaccount`. | `delete` is a Go keyword; follows the `set-password` → `setpassword` convention already in this domain. |
| D-21 | Why implement `PurgeHandler` as `jobqueue.Handler` before the job queue exists? | Implement `Handle(ctx context.Context, job jobqueue.Job) error` from the start so Phase 6 of the job queue requires no handler refactoring — only the `PurgeWorker` goroutine trigger is swapped for a scheduled job. `KindPurgeAccounts` constant is defined in `internal/worker/kinds.go` alongside this feature, even though it is unused until Phase 7. | Prevents a refactor-later trap; the goroutine is throwaway, the handler logic is not. |

---

## 4. Data model

### New SQL queries

**Add to `sql/queries/auth.sql` (under the Profile section):**

| Query | Type | Purpose |
|---|---|---|
| `GetUserForDeletion` | `:one` | Fetch `id`, `email`, `deleted_at` for the authenticated user. `AND deleted_at IS NULL OR deleted_at > NOW() - INTERVAL '30 days'` — so expired-grace accounts return no-rows. |
| `ScheduleUserDeletion` | `:one` | `SET deleted_at = NOW()` returning `deleted_at`. Called inside `ScheduleDeletionTx`. |
| `CancelUserDeletion` | `:execrows` | `SET deleted_at = NULL WHERE deleted_at IS NOT NULL`. Returns rows-affected to detect no-op → 409. |
| `InvalidateUserDeletionTokens` | `:exec` | Void all unused `account_deletion` OTP tokens for this user. |
| `CreateAccountDeletionToken` | `:one` | Insert `account_deletion` OTP; returns `id`, `expires_at`. |
| `GetAccountDeletionToken` | `:one` | Fetch active `account_deletion` token by `user_id`; `FOR UPDATE`. |
| `ConsumeAccountDeletionToken` | `:execrows` | Mark token `used_at = NOW() WHERE used_at IS NULL`. |

**Modify in `sql/queries/auth.sql` (existing query):**

`GetUserForLogin` — two changes:
1. Add `deleted_at` to the SELECT column list.
2. Add to the WHERE clause: `AND (deleted_at IS NULL OR deleted_at > NOW() - INTERVAL '30 days')`.

**Add to `sql/queries/oauth.sql` (or a new `sql/queries/worker.sql`):**

| Query | Type | Purpose |
|---|---|---|
| `GetAccountsDueForPurge` | `:many` | `WHERE deleted_at < NOW() - INTERVAL '30 days' LIMIT 100` |
| `HardDeleteUser` | `:exec` | `DELETE FROM users WHERE id = $1` — cascades everything |
| `InsertPurgeLog` | `:exec` | Insert into `account_purge_log` |

### Schema changes — new migration

```sql
-- 1. Add account_deletion to the one_time_token_type ENUM.
ALTER TYPE one_time_token_type ADD VALUE 'account_deletion';

-- 2. Create account_purge_log (no FK to users — user row will be gone).
CREATE TABLE account_purge_log (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL,
    purged_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata   JSONB       NOT NULL DEFAULT '{}'
);

COMMENT ON TABLE account_purge_log IS
    'Permanent record of hard-purged accounts. user_id has no FK constraint
     because the users row is deleted before this record is written.';
```

No column migration needed for `users.deleted_at` (already exists).

### New audit events

| Constant | Value | When |
|---|---|---|
| `EventAccountDeletionRequested` | `"account_deletion_requested"` | Inside `ScheduleDeletionTx`, after `ScheduleUserDeletion` succeeds |
| `EventAccountDeletionOTPRequested` | `"account_deletion_otp_requested"` | Inside `SendDeletionOTPTx`, after OTP token is created |
| `EventAccountDeletionCancelled` | `"account_deletion_cancelled"` | Inside `CancelDeletionTx`, after `CancelUserDeletion` succeeds |

No audit event at hard-delete — user row and its FK-child audit rows are gone; purge is recorded in `account_purge_log` instead (D-15).

---

## 5. Guard ordering

### DELETE /me — Path A: Password user (single step)

```
1.  mustUserID → 401 if absent
2.  GetUserForDeletion(userID) → 500 on error; 500 if not found (JWT user must exist)
3.  Guard: user.DeletedAt != nil → 409 already_pending_deletion
4.  Confirm has_password = true (else → Path B or C)
5.  Validate request: password field present and non-empty → 400 validation_error
6.  bcrypt.CompareHashAndPassword(storedHash, password) → 401 invalid_credentials on mismatch
7.  ScheduleDeletionTx:                                    [context.WithoutCancel]
    a. ScheduleUserDeletion(userID) RETURNING deleted_at
    b. InsertAuditLog(account_deletion_requested)
    c. Commit
8.  Respond 200 {"message":"account scheduled for deletion",
                 "scheduled_deletion_at": deleted_at.Add(30*24*time.Hour)}
```

Sessions and tokens are **not** revoked. Refresh cookie is **not** cleared.

---

### DELETE /me — Path B: Email-OTP user, Step 1 (trigger OTP)

```
1.  mustUserID → 401 if absent
2.  GetUserForDeletion(userID) → 500 on error
3.  Guard: user.DeletedAt != nil → 409 already_pending_deletion
4.  GetUserAuthMethods(userID) — confirm has_password=false, identity_count≥0
5.  Confirm no `code` and no `telegram_auth` field in body (step 1)
6.  Confirm user.Email non-empty → 422 no_email_on_file if blank
    (If email is empty and identity_count > 0, fall to Path C)
7.  SendDeletionOTPTx:                                     [context.WithoutCancel]
    a. InvalidateUserDeletionTokens(userID)
    b. generateCodeHash() → raw_code, code_hash
    c. CreateAccountDeletionToken(userID, email, code_hash, ttl)
    d. InsertAuditLog(account_deletion_otp_requested)
    e. Commit
8.  Mailer: send raw_code to user.Email
9.  Respond 202 {"message":"verification code sent to your email"}
```

---

### DELETE /me — Path B: Email-OTP user, Step 2 (confirm OTP)

```
1.  mustUserID → 401 if absent
2.  GetUserForDeletion(userID) → 500 on error
3.  Guard: user.DeletedAt != nil → 409 already_pending_deletion
4.  GetUserAuthMethods(userID) — confirm has_password=false
5.  Confirm `code` field present in body
6.  Validate code format: exactly 6 digits → 422 validation_error
7.  GetAccountDeletionToken(userID) FOR UPDATE → 422 token_not_found if absent
8.  Check token.ExpiresAt > now → 422 token_not_found (expired = absent, per convention)
9.  Check token.Attempts < token.MaxAttempts → 429 too_many_attempts
10. VerifyCodeHash(code, token.CodeHash) — on mismatch:
    a. IncrementAttemptsTx                                 [context.WithoutCancel]
    b. 422 invalid_code
11. ScheduleDeletionTx:                                    [context.WithoutCancel]
    a. ConsumeAccountDeletionToken(token.ID)
    b. ScheduleUserDeletion(userID) RETURNING deleted_at
    c. InsertAuditLog(account_deletion_requested)
    d. Commit
12. Respond 200 {"message":"account scheduled for deletion",
                 "scheduled_deletion_at": deleted_at.Add(30*24*time.Hour)}
```

---

### DELETE /me — Path C: Telegram-only user, Step 1 (discovery)

```
1.  mustUserID → 401 if absent
2.  GetUserForDeletion(userID) → 500 on error
3.  Guard: user.DeletedAt != nil → 409 already_pending_deletion
4.  GetUserAuthMethods(userID) — confirm has_password=false, identity_count>0
5.  Confirm user.Email is empty (else → Path B)
6.  Confirm no `telegram_auth` field in body (step 1)
7.  Respond 202 {"message":"authenticate via Telegram to confirm deletion",
                 "auth_method":"telegram"}
```

No DB writes in step 1 for the Telegram path — no OTP token needed.

---

### DELETE /me — Path C: Telegram-only user, Step 2 (HMAC confirm)

```
1.  mustUserID → 401 if absent
2.  GetUserForDeletion(userID) → 500 on error
3.  Guard: user.DeletedAt != nil → 409 already_pending_deletion
4.  GetUserAuthMethods(userID) — confirm has_password=false
5.  Confirm `telegram_auth` field present in body
6.  Validate telegram_auth fields: id, auth_date, hash must be present → 400 validation_error
7.  Check auth_date > time.Now().Unix()-86400 → 401 invalid_telegram_auth (replay protection)
8.  VerifyTelegramHMAC(botToken, telegramAuthFields) → 401 invalid_telegram_auth on failure
9.  GetIdentityByUserAndProvider(userID, 'telegram') → 500 on error; 401 unauthorized if absent
    (absence means this user has no linked Telegram identity despite having no password — data inconsistency)
10. Guard: identity.ProviderUID == strconv.FormatInt(telegramAuth.ID, 10)
    → 401 telegram_identity_mismatch if not equal
11. ScheduleDeletionTx:                                    [context.WithoutCancel]
    a. ScheduleUserDeletion(userID) RETURNING deleted_at
    b. InsertAuditLog(account_deletion_requested)
    c. Commit
12. Respond 200 {"message":"account scheduled for deletion",
                 "scheduled_deletion_at": deleted_at.Add(30*24*time.Hour)}
```

---

### POST /me/cancel-deletion

```
1.  mustUserID → 401 if absent
2.  CancelDeletionTx:
    a. CancelUserDeletion(userID) → rowsAffected
    b. rowsAffected == 0 → rollback; 409 not_pending_deletion (no audit row)
    c. InsertAuditLog(account_deletion_cancelled)           [context.WithoutCancel]
    d. Commit
3.  Respond 200 {"message":"account deletion cancelled"}
```

---

### Background purge worker (`internal/worker/purge.go`)

Not an HTTP handler. Implements `jobqueue.Handler` from day one (D-21).

**`PurgeHandler.Handle` — the permanent logic:**
```
Handle(ctx, job):
  Loop:
    1. GetAccountsDueForPurge() → up to 100 user IDs (deleted_at < NOW()-30d)
    2. For each user_id:
       a. Begin transaction
       b. InsertPurgeLog(user_id, metadata={"deleted_at": <value>})  ← write FIRST
       c. DELETE FROM users WHERE id = user_id                        ← cascades all children
       d. Commit
       e. slog.Info("purged account", "user_id", userID)
       f. On any error: slog.Error(...); continue to next user (never abort batch)
    3. If len(results) < 100: break   ← batch exhausted, stop
  Return nil
```

**`PurgeWorker` — the temporary goroutine wrapper (removed in job queue Phase 7):**
```
Start(ctx):
  Loop forever:
    handler.Handle(ctx, jobqueue.Job{Kind: KindPurgeAccounts})   ← synthetic job
    sleep 1 hour
```

When the job queue is wired, `PurgeWorker.Start()` is deleted from `server.go` and replaced by:
```go
mgr.Register(worker.KindPurgeAccounts, worker.NewPurgeHandler(pool))
mgr.EnsureSchedule(ctx, jobqueue.ScheduleInput{
    Name: "purge_accounts_hourly", Kind: worker.KindPurgeAccounts,
    IntervalSeconds: 3600, SkipIfRunning: true,
})
```
`PurgeHandler.Handle` is unchanged — it is already the correct shape.

---

## 6. Rate limiting

| Endpoint | Limit | KV prefix | Rationale |
|---|---|---|---|
| `DELETE /me` | 3 req / 1 hr per user | `del:usr:` | Destructive; tight limit per INCOMING.md |
| `POST /me/cancel-deletion` | 5 req / 10 min per user | `delc:usr:` | More permissive — cancellation is safe |

Both user-keyed; placed **inside** the JWT middleware group in `routes.go`.

**Prefix collision check:** `del:usr:` and `delc:usr:` not present in `E2E_CHECKLIST.md`.

---

## 7. Cross-cutting changes to existing packages

### `auth/login` — additive

- `GetUserForLogin` SQL: add `deleted_at` to SELECT; add grace-period guard to WHERE (see §4).
- `login.LoginUser`: add `DeletedAt *time.Time`.
- `login.LoggedInSession`: add `ScheduledDeletionAt *time.Time` (computed as `deletedAt + 30 days` when non-nil).
- Login handler response struct: add `ScheduledDeletionAt *time.Time \`json:"scheduled_deletion_at,omitempty"\``.

### `profile/me` — additive

- `GetUserProfile` SQL: add `deleted_at` to SELECT (or derive `deleted_at + INTERVAL '30 days' AS scheduled_deletion_at`).
- `me.UserProfile`: add `ScheduledDeletionAt *time.Time`.
- `GET /me` handler response: add `ScheduledDeletionAt *time.Time \`json:"scheduled_deletion_at,omitempty"\``.

### `profile/routes.go` — wiring

Add `deleteaccount.Routes(ctx, r, deps)` call and the corresponding import.

---

## 8. File map for this package

| What | Path |
|---|---|
| Feature models | `internal/domain/profile/delete-account/models.go` |
| Feature requests | `internal/domain/profile/delete-account/requests.go` |
| Feature validators | `internal/domain/profile/delete-account/validators.go` |
| Feature errors | `internal/domain/profile/delete-account/errors.go` |
| Feature store | `internal/domain/profile/delete-account/store.go` |
| Feature service | `internal/domain/profile/delete-account/service.go` |
| Feature handler | `internal/domain/profile/delete-account/handler.go` |
| Feature routes | `internal/domain/profile/delete-account/routes.go` |
| Purge worker | `internal/worker/purge.go` — `PurgeHandler` (implements `jobqueue.Handler`) + thin `PurgeWorker` goroutine wrapper |
| Worker SQL | `sql/queries/worker.sql` (new file) |
| FakeStorer | `internal/domain/auth/shared/testutil/fake_storer.go` — add `DeleteAccountFakeStorer` |
| FakeServicer | `internal/domain/auth/shared/testutil/fake_servicer.go` — add `DeleteAccountFakeServicer` |
| Handler tests | `internal/domain/profile/delete-account/handler_test.go` |
| Service tests | `internal/domain/profile/delete-account/service_test.go` |
| Store tests | `internal/domain/profile/delete-account/store_test.go` |
| Worker tests | `internal/worker/purge_test.go` |
| Worker kind constant | `internal/worker/kinds.go` — add `KindPurgeAccounts` (used by job queue Phase 7; defined here to avoid adding it later) |
| Production SQL | `sql/queries/auth.sql` (new queries + GetUserForLogin modification) |
| New migration | `sql/schema/00N_account_deletion.sql` |

---

## 9. Test case inventory

**Legend:** S = service unit test, H = handler unit test, I = store integration test, W = worker unit test

### DELETE /me

| # | Case | Layer | Setup | Expected outcome |
|---|---|---|---|---|
| T-01 | Happy path — password user, correct password | S, H, I | User with password_hash; correct password in body | 200; deleted_at set; sessions/tokens NOT revoked |
| T-02 | Happy path — email-OTP user step 1 (trigger OTP) | S, H, I | User with no password, email on file, no code in body | 202 "verification code sent"; OTP row created; audit `account_deletion_otp_requested` |
| T-03 | Happy path — email-OTP user step 2 (correct code) | S, H, I | Active deletion token; correct 6-digit code | 200; token consumed; deleted_at set; audit `account_deletion_requested` |
| T-04 | Happy path — Telegram-only user step 1 (discovery) | S, H | User with no password, no email, Telegram identity | 202 "authenticate via Telegram"; `auth_method: telegram`; no DB write |
| T-05 | Happy path — Telegram-only user step 2 (valid HMAC) | S, H, I | Valid HMAC payload; provider_uid matches | 200; deleted_at set; audit `account_deletion_requested` |
| T-06 | Already pending deletion → 409 | S, H | User with deleted_at set | 409 `already_pending_deletion` |
| T-07 | Password user — password field absent | H | has_password=true; body `{}` | 400 `validation_error` |
| T-08 | Password user — wrong password | S | bcrypt mismatch | 401 `invalid_credentials` |
| T-09 | Email-OTP step 2 — code wrong format (non-digits or wrong length) | H | code = "abc123" or "12345" | 422 `validation_error` |
| T-10 | Email-OTP step 2 — no active token | S | GetAccountDeletionTokenFn returns ErrTokenNotFound | 422 `token_not_found` |
| T-11 | Email-OTP step 2 — token expired | S | token.ExpiresAt in the past | 422 `token_not_found` |
| T-12 | Email-OTP step 2 — wrong code, attempts < max | S | VerifyCodeHash fails; IncrementAttemptsTx called | 422 `invalid_code`; increment called once |
| T-13 | Email-OTP step 2 — attempt ceiling reached | S | token.Attempts == token.MaxAttempts | 429 `too_many_attempts`; increment NOT called |
| T-14 | Telegram step 2 — HMAC fails | S, H | VerifyTelegramHMAC returns false | 401 `invalid_telegram_auth` |
| T-15 | Telegram step 2 — auth_date too old (> 86400 s) | H | auth_date = now - 90000 | 401 `invalid_telegram_auth` |
| T-16 | Telegram step 2 — provider_uid mismatch | S | identity.ProviderUID != strconv(telegramAuth.ID) | 401 `telegram_identity_mismatch` |
| T-17 | Missing auth → 401 | H | No JWT in context | 401 `unauthorized` |
| T-18 | Sessions NOT revoked after soft-delete | I | Seed user + open session; call ScheduleDeletionTx | Session `ended_at` remains NULL |
| T-19 | Refresh tokens NOT revoked after soft-delete | I | Seed user + active token; call ScheduleDeletionTx | Token `revoked_at` remains NULL |
| T-20 | Response includes correct scheduled_deletion_at (deleted_at + 30d) | H | Successful delete | Response field == expected timestamp |
| T-21 | context.WithoutCancel on all writes in ScheduleDeletionTx | S | Capture ctx | ctx.Done() == nil for every write call |
| T-22 | context.WithoutCancel on SendDeletionOTPTx audit write | S | Capture ctx | ctx.Done() == nil |
| T-23 | context.WithoutCancel on IncrementAttemptsTx | S | Wrong code path; capture ctx | ctx.Done() == nil |
| T-24 | Store error in ScheduleDeletionTx → 500 | S | ScheduleDeletionTxFn returns error | 500 `internal_error` |
| T-25 | Store error in SendDeletionOTPTx → 500 | S | SendDeletionOTPTxFn returns error | 500 `internal_error` |
| T-26 | Service error wraps with correct prefix | S | Store returns raw error | err.Error() contains `"deleteaccount.ScheduleDeletion:"` or `"deleteaccount.SendDeletionOTP:"` |

### POST /me/cancel-deletion

| # | Case | Layer | Setup | Expected outcome |
|---|---|---|---|---|
| T-27 | Happy path — cancel pending deletion | S, H, I | User with deleted_at set | 200; deleted_at = NULL; audit `account_deletion_cancelled` |
| T-28 | Not pending deletion → 409 | S, H | User with deleted_at = NULL | 409 `not_pending_deletion`; no audit row written |
| T-29 | Missing auth → 401 | H | No JWT | 401 `unauthorized` |
| T-30 | context.WithoutCancel on audit write in CancelDeletionTx | S | Capture ctx | ctx.Done() == nil |
| T-31 | Store error → 500 | S | CancelDeletionTxFn returns error | 500 `internal_error` |

### Cross-cutting: login + GET /me

| # | Case | Layer | Setup | Expected outcome |
|---|---|---|---|---|
| T-32 | GET /me — scheduled_deletion_at present for pending account | H (me) | UserProfile.ScheduledDeletionAt non-nil | Response contains `scheduled_deletion_at` |
| T-33 | GET /me — scheduled_deletion_at absent for normal account | H (me) | ScheduledDeletionAt == nil | Response omits key (`omitempty`) |
| T-34 | POST /login — scheduled_deletion_at in response for pending account | H (login) | LoginUser.DeletedAt non-nil | Login response includes `scheduled_deletion_at` |
| T-35 | POST /login — scheduled_deletion_at absent for normal account | H (login) | DeletedAt == nil | Login response omits key |
| T-36 | POST /login — account past 30-day window blocked | I (login) | User with deleted_at 31 days ago | GetUserForLogin returns no-rows → 401 `invalid_credentials` |

### Background purge worker

| # | Case | Layer | Setup | Expected outcome |
|---|---|---|---|---|
| T-37 | Worker purges account past 30-day window | W, I | User with deleted_at 31 days ago | User row deleted; purge_log row exists |
| T-38 | Worker skips account within grace period | W, I | User with deleted_at 10 days ago | User row untouched; no purge_log row |
| T-39 | Purge log written before user row deleted | I | Verify order inside transaction | purge_log INSERT committed; user row gone |
| T-40 | Worker handles per-user error and continues | W | First user causes DB error; second user valid | Error logged; second user purged |
| T-41 | Handle drains multiple batches until exhausted | W | GetAccountsDueForPurgeFn returns 100 rows on first call, 0 on second | Handle calls store twice; returns nil after second call |
| T-42 | PurgeWorker passes synthetic job to Handle | W | PurgeWorker.Start ticks; verify Handle called with KindPurgeAccounts | Handle invoked once per tick |

---

## 10. Open questions

None. All design points resolved.

---

## Approval checklist

- [x] HTTP contract (§2) covers DELETE /me (all 4 body variants), cancel-deletion, and the login/GET /me additions
- [x] Every decision in §3 has a rationale
- [x] Guard orderings (§5) complete for all paths (A, B step 1, B step 2, C step 1, C step 2, cancel, worker)
- [x] Telegram path uses HMAC re-auth; ownership proven by provider_uid comparison (D-08, D-09)
- [x] Cross-cutting changes to `auth/login` and `profile/me` documented (§7)
- [x] Full file map in §8
- [x] Test inventory (§9) covers all paths including Telegram, worker, and cross-cutting changes — 42 cases
- [x] Rate-limit prefixes `del:usr:` and `delc:usr:` confirmed unique
- [x] context.WithoutCancel usage documented for every write
- [x] Package naming: folder `delete-account/`, package `deleteaccount` — follows `set-password` convention
- [x] `PurgeHandler` implements `jobqueue.Handler` from day one (D-21); `KindPurgeAccounts` defined in `kinds.go`
- [x] No open questions

**Stage 0 approved. Stage 1 may begin.**
