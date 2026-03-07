# Email Change Flow — Stage 0: Design & Decisions

**Requirement source:** `docs/map/INCOMING.md §B-2`
**Target package:** `internal/domain/profile/email/`
**Status:** ⏳ Awaiting approval before Stage 1 begins

---

## Read first (no modifications)

| File | What to extract |
|---|---|
| `docs/map/INCOMING.md §B-2` | Full requirement text — three-step flow spec |
| `docs/RULES.md` | Global conventions, error wrapping, comment style |
| `internal/domain/profile/username/service.go` | Closest analogous feature — same domain, guard ordering style |
| `internal/domain/profile/username/handler.go` | Handler + error switch pattern, mustUserID helper |
| `internal/domain/profile/shared/errors.go` | profileshared.ErrUserNotFound alias |
| `internal/audit/audit.go` | Existing event names — confirm no collision |
| `sql/queries/auth.sql` (last 150 lines) | Username and Profile sections — confirm naming/style |
| `internal/platform/kvstore/store.go` | KV interface: Set, Get, Delete, TTL semantics |

---

## 1. Feature Summary

Email change is a **three-step verified flow** that proves ownership of both the
current address (step 1→2) and the new address (step 2→3) before updating the
`users.email` column. Because email is the primary login identifier, a successful
change atomically revokes all active refresh tokens, ends all sessions, and
blocklists the current access token — forcing a full re-login on all devices. All
three endpoints live in `internal/domain/profile/email/` under one package.

---

## 2. HTTP Contract

### Step 1 — `POST /api/v1/profile/me/email/request-change`

**Auth:** Valid JWT required.

**Request body:**
```json
{ "new_email": "string — valid email format, max 254 bytes" }
```

**Success:** `202 Accepted`
```json
{ "message": "a verification code has been sent to your current email address" }
```

**Errors:**

| Status | Code | Condition |
|---|---|---|
| 422 | `validation_error` | `new_email` empty / invalid format / too long |
| 422 | `same_email` | `new_email` equals current email |
| 409 | `email_taken` | `new_email` already registered to another account |
| 429 | `cooldown_active` | Another OTP was issued within the last 2 minutes |
| 429 | `too_many_requests` | Rate limit hit (3 req / 10 min per user) |
| 500 | `internal_error` | Unexpected store or infrastructure error |

---

### Step 2 — `POST /api/v1/profile/me/email/verify-current`

**Auth:** Valid JWT required.

**Request body:**
```json
{ "code": "string — exactly 6 digits" }
```

**Success:** `200 OK`
```json
{ "grant_token": "string — opaque UUID held by client for step 3", "expires_in": 600 }
```

**Errors:**

| Status | Code | Condition |
|---|---|---|
| 422 | `validation_error` | `code` empty / not exactly 6 digits / non-digit chars |
| 422 | `validation_error` | OTP not found (`ErrTokenNotFound`) |
| 422 | `validation_error` | OTP expired (`ErrTokenExpired`) |
| 422 | `validation_error` | OTP wrong code (`ErrInvalidCode`) |
| 429 | `too_many_attempts` | OTP attempt budget exhausted (`ErrTooManyAttempts`) |
| 429 | `too_many_requests` | Rate limit hit (5 req / 15 min per user) |
| 500 | `internal_error` | Unexpected store or infrastructure error |

---

### Step 3 — `POST /api/v1/profile/me/email/confirm-change`

**Auth:** Valid JWT required.

**Request body:**
```json
{ "grant_token": "string — opaque UUID from step 2", "code": "string — exactly 6 digits" }
```

**Success:** `200 OK`
```json
{ "message": "email address updated successfully" }
```

**Errors:**

| Status | Code | Condition |
|---|---|---|
| 422 | `validation_error` | `grant_token` or `code` empty / malformed |
| 422 | `invalid_grant_token` | Grant token absent from KV, expired, or belongs to another user |
| 422 | `validation_error` | OTP not found for new email (`ErrTokenNotFound`) |
| 422 | `validation_error` | OTP expired (`ErrTokenExpired`) |
| 422 | `validation_error` | OTP wrong code (`ErrInvalidCode`) |
| 429 | `too_many_attempts` | OTP attempt budget exhausted |
| 409 | `email_taken` | new_email taken inside the commit transaction (race) |
| 429 | `too_many_requests` | Rate limit hit (5 req / 15 min per user) |
| 500 | `internal_error` | Unexpected store or infrastructure error |

---

## 3. Decisions

| # | Question | Decision | Rationale |
|---|---|---|---|
| D-01 | How is `new_email` carried from step 1 → step 2? | KV entry (key `echg:pending:{userID}`, TTL 12 min). INCOMING.md says "stored in token metadata" but one_time_tokens has no metadata column (confirmed by auth.sql scan). KV is the existing pattern for short-lived opaque values (see reset-password grant token). | Avoids schema change; consistent with grant-token pattern. |
| D-02 | How is the email_change_verify OTP looked up in step 2? | By `(user_id, token_type='email_change_verify')` using FOR UPDATE, ordered newest-first. User is authenticated; no email needed in the body. | User is authenticated; same pattern as username change — no anti-enumeration concern. |
| D-03 | How is the email_change_confirm OTP looked up in step 3? | By `(user_id, token_type='email_change_confirm')` using FOR UPDATE. Grant token provides new_email for the transaction; OTP is keyed to user_id. | Consistent with D-02; grant token cross-validates new_email. |
| D-04 | Grant token format | Random UUID stored as KV key. Value = `{user_id_hex}:{new_email}` (`:` delimiter). Key prefix: `echg:gt:`. TTL: 10 min. Single-use: key is deleted on first confirm attempt (success or token-not-found after correct OTP). | Matches reset-password grant token pattern. UUID is unpredictable; no JWT needed for a 10-min one-way credential. |
| D-05 | Re-check email uniqueness in confirm tx | Yes. `ConfirmEmailChangeTx` re-checks via unique-constraint: `SetUserEmail` will return 23505 on conflict → `ErrEmailTaken`. The service maps this to a 409 response. | Closes the TOCTOU window between step 1 validation and the commit. |
| D-06 | Token revocation scope on step 3 success | Revoke all refresh tokens (`reason = 'email_changed'`), end all sessions, blocklist the current access token (JWT JTI extracted by handler, passed to service as `AccessJTI`). Send confirmation to old email. | INCOMING.md §B-2 step 3 spec. Email is the primary identifier; every credential surface must be invalidated. |
| D-07 | User-not-found after JWT auth on all three steps | Surface as 500 (same as username). An authenticated user whose row is absent indicates a deleted account with a still-valid token — a configuration or integrity issue, not a user error. | Consistent with username handler decision (Stage 0 D-05 analogue). |
| D-08 | Cooldown check implementation | `GetLatestEmailChangeVerifyTokenCreatedAt(user_id)`: returns created_at of most recent active token. Service checks `time.Since(createdAt) < 2 * time.Minute`. | Mirrors resend-verification cooldown pattern. |
| D-09 | `email_change_confirm` token email field | Store `new_email` in the `email` column of the token row. Not used for lookup (lookup is by user_id) but preserved for audit trail readability in the raw tokens table. | Mirrors email_verification and unlock patterns where `email` records the address the OTP was sent to. |
| D-10 | Invalide old tokens before creating new | Yes. Before `CreateEmailChangeVerifyToken` call `InvalidateUserEmailChangeVerifyTokens`. Before `CreateEmailChangeConfirmToken` call `InvalidateUserEmailChangeConfirmTokens`. | Prevents token accumulation; matches resend and password-reset patterns. |
| D-11 | SQL file | Append to `sql/queries/auth.sql` under a new `/* ── Email change ── */` section. Profile domain currently uses auth.sql (confirmed by `CheckUsernameAvailable` and `GetUserProfile` location). | No separate profile.sql exists; all user-row queries live in auth.sql. |
| D-12 | max_attempts for new OTP tokens | 5 for email_change_verify and email_change_confirm. (Registration tokens use 3; password-reset uses 3; a slightly higher limit is reasonable for email change since it requires auth.) | Higher is more user-friendly given the 2-step OTP entry. |

---

## 4. Data Model

### New SQL queries (append to auth.sql `/* ── Email change ── */` section)

| Query name | Type | Purpose |
|---|---|---|
| `CheckEmailAvailableForChange` | `:one` | EXISTS check: any user with `email = @new_email AND id != @user_id` |
| `GetLatestEmailChangeVerifyTokenCreatedAt` | `:one` | created_at of most recent active `email_change_verify` token for user_id (cooldown check) |
| `InvalidateUserEmailChangeVerifyTokens` | `:exec` | SET used_at=NOW() WHERE user_id + token_type = 'email_change_verify' AND used_at IS NULL |
| `CreateEmailChangeVerifyToken` | `:one` | INSERT one_time_tokens(type='email_change_verify', user_id, email=current_email, code_hash, ttl, max_attempts=5) |
| `GetEmailChangeVerifyToken` | `:one` | SELECT FOR UPDATE by (user_id, 'email_change_verify', used_at IS NULL) newest-first |
| `ConsumeEmailChangeToken` | `:execrows` | UPDATE SET used_at=NOW() WHERE id = @id AND used_at IS NULL (used for both verify + confirm tokens) |
| `InvalidateUserEmailChangeConfirmTokens` | `:exec` | SET used_at=NOW() WHERE user_id + token_type = 'email_change_confirm' AND used_at IS NULL |
| `CreateEmailChangeConfirmToken` | `:one` | INSERT one_time_tokens(type='email_change_confirm', user_id, email=new_email, code_hash, ttl, max_attempts=5) |
| `GetEmailChangeConfirmToken` | `:one` | SELECT FOR UPDATE by (user_id, 'email_change_confirm', used_at IS NULL) newest-first |
| `GetUserForEmailChangeTx` | `:one` | SELECT id, email FROM users WHERE id = @user_id FOR UPDATE (used in ConfirmEmailChangeTx) |
| `SetUserEmail` | `:execrows` | UPDATE users SET email = @new_email WHERE id = @user_id; rows==0 → ErrUserNotFound; 23505 → ErrEmailTaken |

**Reused existing queries (no new SQL):**
- `GetUserProfile` — step 1 uses this to fetch the user's current email
- `RevokeAllUserRefreshTokens` — step 3 revokes all tokens with reason `'email_changed'`
- `EndAllUserSessions` — step 3 closes all sessions
- `IncrementVerificationAttempts` — step 2 and step 3 increment OTP attempt counters

### New schema changes

None. `one_time_tokens` supports the two new token_type string values without a migration because token_type is `text`, not an enum. Confirm this by checking `001_core.sql` before Stage 1.

### New audit events (add to `internal/audit/audit.go`)

| Constant | Value string | When emitted |
|---|---|---|
| `EventEmailChangeRequested` | `"email_change_requested"` | Step 1 succeeds (OTP sent to current email) |
| `EventEmailChangeCurrentVerified` | `"email_change_current_verified"` | Step 2 succeeds (OTP confirmed, grant token issued) |
| `EventEmailChanged` | `"email_changed"` | Step 3 succeeds (email updated, old+new in metadata) |

**Sync reminder (RULES.md §3.14 Sync S-1):** Add each constant to `const` block, `AllEvents()` slice, and `TestEventConstants_ExactValues` table in the same commit.

---

## 5. Guard Ordering

### Step 1 — `RequestEmailChange`

```
1.  Validate new_email format (IDNA normalise + max 254 bytes)
    → on failure: validation sentinel (ErrInvalidEmailFormat / ErrEmailTooLong)
2.  store.GetUserProfile(ctx, userID) — get current email
    → on no-rows: profileshared.ErrUserNotFound  (→ 500, see D-07)
3.  Check new_email != current_email
    → on equal: ErrSameEmail
4.  store.CheckEmailAvailableForChange(ctx, new_email, userID)
    → on taken: ErrEmailTaken
5.  store.GetLatestEmailChangeVerifyTokenCreatedAt(ctx, userID) — cooldown
    → if created_at within 2 min: ErrCooldownActive
6.  Generate OTP: rawCode, codeHash = authshared.GenerateCodeHash()
7.  kv.Set("echg:pending:{userID}", new_email, 12 min TTL)
8.  store.InvalidateUserEmailChangeVerifyTokens(ctx, userID)
9.  store.CreateEmailChangeVerifyToken(ctx, in) → token (expires_at)
10. mailer.SendOTP(current_email, rawCode, subject="email change verification")
11. audit: EventEmailChangeRequested — context.WithoutCancel (D-06)
```

**Timing invariants:** None (user is authenticated; no anti-enumeration requirement).

**context.WithoutCancel:** Step 11 (audit write). No other writes require it since failures here don't create a security gap.

---

### Step 2 — `VerifyCurrentEmail`

```
1.  Validate code format (exactly 6 digits)
    → on failure: validation sentinel
2.  store.GetEmailChangeVerifyToken(ctx, userID) FOR UPDATE
    → on no-rows: ErrTokenNotFound
3.  Check token.ExpiresAt > now()
    → on expired: ErrTokenExpired
4.  Check token.Attempts < token.MaxAttempts
    → on exhausted: ErrTooManyAttempts
5.  authshared.VerifyCodeHash(token.CodeHash, code)
    → on wrong code:
        store.IncrementVerificationAttempts(context.WithoutCancel(ctx), token.ID)
        return ErrInvalidCode
6.  store.ConsumeEmailChangeToken(ctx, token.ID) — mark used
7.  Read new_email from kv.Get("echg:pending:{userID}") → ErrGrantTokenInvalid if absent
8.  Delete kv.Delete("echg:pending:{userID}")
9.  Generate OTP for new_email: rawCode2, codeHash2
10. store.InvalidateUserEmailChangeConfirmTokens(ctx, userID)
11. store.CreateEmailChangeConfirmToken(ctx, userID, new_email, codeHash2, ttl)
12. Grant token: grantToken = uuid.NewRandom(); kv.Set("echg:gt:"+grantToken, userID+":"+new_email, 10 min TTL)
13. mailer.SendOTP(new_email, rawCode2, subject="confirm your new email address")
14. audit: EventEmailChangeCurrentVerified — context.WithoutCancel
15. Return {GrantToken: grantToken, ExpiresIn: 600}
```

**context.WithoutCancel:** Step 5 increment (security-critical counter), Step 14 (audit).

---

### Step 3 — `ConfirmEmailChange`

```
1.  Validate grant_token not empty; validate code format (6 digits)
    → on failure: validation sentinel
2.  kv.Get("echg:gt:"+grantToken) → parse userID + new_email
    → on absent/parse-error: ErrGrantTokenInvalid
3.  Check parsed userID == JWT userID (prevents cross-user grant reuse)
    → on mismatch: ErrGrantTokenInvalid
4.  store.GetEmailChangeConfirmToken(ctx, userID) FOR UPDATE
    → on no-rows: ErrTokenNotFound
5.  Check token.ExpiresAt > now()
    → on expired: ErrTokenExpired
6.  Check token.Attempts < token.MaxAttempts
    → on exhausted: ErrTooManyAttempts
7.  authshared.VerifyCodeHash(token.CodeHash, code)
    → on wrong code:
        store.IncrementVerificationAttempts(context.WithoutCancel(ctx), token.ID)
        return ErrInvalidCode
8.  store.ConsumeEmailChangeToken(ctx, token.ID)
9.  kv.Delete("echg:gt:"+grantToken) — single-use
10. store.ConfirmEmailChangeTx(ctx, in):
    a. GetUserForEmailChangeTx(userID) FOR UPDATE → old_email; on no-rows: ErrUserNotFound
    b. SetUserEmail(userID, new_email) → on 23505: ErrEmailTaken; on rows==0: ErrUserNotFound
    c. InsertAuditLog(EventEmailChanged, metadata={old_email, new_email}) — context.WithoutCancel
11. store.RevokeAllUserRefreshTokens(context.WithoutCancel(ctx), userID, "email_changed")
12. store.EndAllUserSessions(context.WithoutCancel(ctx), userID)
13. kv.BlocklistAccessToken(in.AccessJTI, remaining TTL of the current JWT)
14. mailer.SendEmailChangeConfirmation(old_email, new_email)
```

**context.WithoutCancel:** Steps 7 increment, 10c audit, 11 revoke, 12 end sessions — all must complete even if client disconnects.

**Note on step 10:** `ConfirmEmailChangeTx` is a dedicated store `*Tx` method wrapping steps a+b+c inside a single `BeginOrBind` transaction. It handles the `pgconn.PgError` 23505 → `ErrEmailTaken` mapping internally.

---

## 6. Rate Limiting

| Endpoint | Limit | KV prefix | Notes |
|---|---|---|---|
| `POST /email/request-change` | 3 req / 10 min per **user** | `echg:usr:` | User-scoped (JWT available at this point) |
| `POST /email/verify-current` | 5 req / 15 min per **user** | `echg:usr:vfy:` | Matches INCOMING.md |
| `POST /email/confirm-change` | 5 req / 15 min per **user** | `echg:usr:cnf:` | Matches INCOMING.md |

**Collision check:** None of these prefixes appear in `E2E_CHECKLIST.md`. All three limiters use the user ID as the key component (not IP) because the user is authenticated at every step.

---

## 7. Test Case Inventory

**Legend:** S = service unit test, H = handler unit test, I = store integration test

### Step 1 — RequestEmailChange

| # | Case | Layer | Setup | Expected outcome |
|---|---|---|---|---|
| T-01 | Happy path | S, H, I | valid auth, new_email unclaimed | 202; OTP token created; KV entry for new_email set; audit row |
| T-02 | new_email empty | H | body `{"new_email":""}` | 422 `validation_error` |
| T-03 | new_email invalid format | H | body `{"new_email":"notanemail"}` | 422 `validation_error` |
| T-04 | new_email too long (>254 bytes) | H | body with 255-byte email | 422 `validation_error` |
| T-05 | ErrSameEmail (new == current) | S, H | GetUserProfile returns email matching new_email | 422 `same_email` |
| T-06 | ErrEmailTaken | S, H | CheckEmailAvailableForChange returns taken | 409 `email_taken` |
| T-07 | ErrCooldownActive | S, H | GetLatestEmailChangeVerifyTokenCreatedAt < 2 min ago | 429 `cooldown_active` |
| T-08 | profileshared.ErrUserNotFound → 500 | H | svc returns ErrUserNotFound | 500 `internal_error` |
| T-09 | context.WithoutCancel on audit write | S | capture ctx in audit call | ctx.Done() == nil |
| T-10 | Store error wraps correctly | S | store returns raw error | error contains "email.RequestEmailChange:" |
| T-11 | KV pending entry set on happy path | I | seed user; call step 1; check KV | `echg:pending:{id}` = new_email |
| T-12 | Old verify tokens invalidated before new one | I | seed active token; call step 1; check old token used_at | used_at IS NOT NULL on old token |

### Step 2 — VerifyCurrentEmail

| # | Case | Layer | Setup | Expected outcome |
|---|---|---|---|---|
| T-13 | Happy path | S, H, I | valid auth, correct code, pending KV entry exists | 200; grant_token in KV; confirm OTP token created; audit row |
| T-14 | code empty | H | body `{"code":""}` | 422 `validation_error` |
| T-15 | code wrong format (non-digit) | H | body `{"code":"abc123"}` | 422 `validation_error` |
| T-16 | ErrTokenNotFound | S, H | GetEmailChangeVerifyToken returns no rows | 422 `validation_error` |
| T-17 | ErrTokenExpired | S, H | token.ExpiresAt in past | 422 `validation_error` |
| T-18 | ErrTooManyAttempts | S, H | token.Attempts >= token.MaxAttempts | 429 `too_many_attempts`; increment NOT called |
| T-19 | ErrInvalidCode — increment called | S | wrong code, budget > 0 | ErrInvalidCode returned; IncrementFn called once |
| T-20 | context.WithoutCancel on increment | S | wrong code path | captured increment ctx has nil Done() |
| T-21 | context.WithoutCancel on audit | S | happy path | captured audit ctx has nil Done() |
| T-22 | KV pending entry absent → ErrGrantTokenInvalid → 500 | H | svc returns ErrGrantTokenInvalid | 500 `internal_error` (unexpected: KV entry should exist if OTP was issued) — decide: map to 422 `invalid_grant_token` or 500? → See D-13 |
| T-23 | Grant token KV entry set on happy path | I | seed user + token; call step 2; check KV | `echg:gt:{token}` present; pending key deleted |
| T-24 | Verify token consumed (replay fails) | I | complete step 2; call step 2 again | ErrTokenNotFound on second call |

### Step 3 — ConfirmEmailChange

| # | Case | Layer | Setup | Expected outcome |
|---|---|---|---|---|
| T-25 | Happy path | S, H, I | valid grant_token + correct code | 200; users.email updated; all tokens revoked; access token blocklisted; audit row |
| T-26 | grant_token empty | H | body `{"grant_token":"","code":"123456"}` | 422 `validation_error` |
| T-27 | code empty | H | body `{"grant_token":"abc","code":""}` | 422 `validation_error` |
| T-28 | ErrGrantTokenInvalid (not in KV) | S, H | kv.Get returns absent | 422 `invalid_grant_token` |
| T-29 | ErrGrantTokenInvalid (user_id mismatch) | S, H | grant token user_id != JWT user_id | 422 `invalid_grant_token` |
| T-30 | ErrTokenNotFound (confirm token absent) | S, H | GetEmailChangeConfirmToken no rows | 422 `validation_error` |
| T-31 | ErrTokenExpired | S, H | token.ExpiresAt in past | 422 `validation_error` |
| T-32 | ErrTooManyAttempts | S, H | token.Attempts >= token.MaxAttempts | 429 `too_many_attempts` |
| T-33 | ErrInvalidCode — increment called | S | wrong code | ErrInvalidCode; IncrementFn called once |
| T-34 | ErrEmailTaken (race inside tx) | S, H | ConfirmEmailChangeTx returns ErrEmailTaken | 409 `email_taken` |
| T-35 | Grant token deleted after use | S | happy path | kv.Delete called with `echg:gt:{token}` |
| T-36 | All refresh tokens revoked on success | S | happy path | RevokeAllFn called with reason="email_changed" |
| T-37 | Access token blocklisted | S | happy path | BlocklistFn called with AccessJTI |
| T-38 | context.WithoutCancel on revoke + end sessions | S | happy path | captured ctxes have nil Done() |
| T-39 | context.WithoutCancel on increment | S | wrong code | captured ctx has nil Done() |
| T-40 | Email updated in DB | I | full happy path integration | users.email == new_email after call |
| T-41 | Old email login fails after change | I | full flow; attempt login with old email | login returns ErrInvalidCredentials or no user |
| T-42 | Confirm token consumed (replay fails) | I | complete step 3; call step 3 again with same grant_token | ErrGrantTokenInvalid (KV key deleted) |

---

## 8. Open Questions

| # | Question | Status |
|---|---|---|
| Q-01 | Does `one_time_tokens` have a `metadata` column? INCOMING.md says "stored in token metadata" but auth.sql CREATE queries show no such column. Verify `migrations/001_core.sql` or equivalent. If yes, prefer metadata over KV for new_email carry-through. D-01 assumes no metadata column — **update D-01 if wrong.** | ⏳ Check migration before Stage 1 |
| Q-02 | T-22: When step 2 KV pending entry is missing despite valid OTP (unexpected internal state), should the service return 422 `invalid_grant_token` or 500 `internal_error`? Recommendation: return 422 `invalid_grant_token` since the OTP was valid and the user can retry step 1. | ⏳ Decide before Stage 1 |
| Q-03 | Does `token_type = 'email_change_verify'` and `'email_change_confirm'` pass the `chk_ott_*` DB check constraints? Verify the constraint definition in the migration file. If it's an enum or list constraint, add the new type values to a new migration. | ⏳ Check migration before Stage 1 |

---

## Approval Checklist

Before Stage 1 begins, confirm:

- [ ] HTTP contract (§2) matches INCOMING.md §B-2 exactly — no gaps
- [ ] Every decision in §3 has a rationale
- [ ] Guard ordering (§5) is complete — every requirement bullet has a corresponding step
- [ ] Test case inventory (§7) covers every path in §5 and every error in §2
- [ ] Q-01, Q-02, Q-03 are answered and D-01 / T-22 updated if needed
- [ ] Rate-limit prefixes in §6 are unique — no collision with E2E_CHECKLIST.md
- [ ] Target package `internal/domain/profile/email/` follows one-route-one-folder rule (three endpoints share the package because they are the same multi-step resource — per PROMPT-TEMPLATE.md pattern)

**Stage 0 approved. Stage 1 may begin.**
