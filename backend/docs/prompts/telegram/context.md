# Context Snapshot — §D-2 Telegram OAuth

**Package:** `internal/domain/auth/oauth/telegram/`  
**Status:** Stage 4 complete

---

## Resolved paths

| Item | Path |
|---|---|
| Package | `internal/domain/auth/oauth/telegram/` |
| SQL section | append `/* ── Telegram OAuth ── */` to end of `sql/queries/auth.sql` |
| Audit events | all 3 already exist in `internal/audit/audit.go` |
| DB enum | `db.AuthProviderTelegram` already in `internal/db/models.go` |
| DB model | `db.UserIdentity` in `internal/db/models.go` |

---

## Key decisions

| ID | Decision |
|---|---|
| D-01 | New Telegram users: email=NULL, password_hash=NULL, is_active=true, email_verified=false |
| D-02 | display_name = first_name + " " + last_name (or first_name only) |
| D-03 | access_token column = NULL for Telegram (no OAuth tokens issued) |
| D-04 | provider_email = NULL for Telegram |
| D-05 | provider_uid uniqueness checked in TX with SELECT FOR UPDATE |
| D-06 | last-auth-method check uses single query returning identities + hasPassword flag |
| D-07 | HMAC verified before any DB read |
| D-08 | constant-time compare: hmac.Equal |
| D-09 | Reject auth_date > 86400s old OR > 60s in future |
| D-10 | Bot token from deps.Config.TelegramBotToken only |
| D-11 | All audit writes use context.WithoutCancel |
| D-12 | No access_token stored |
| D-13 | Callback=IP rate limit, link/unlink=User rate limit |
| D-14 | provider_uid uniqueness enforced by DB unique index |
| D-15 | Bot token validated at startup (non-empty); routes skipped or handler panics if absent |
| D-16 | Verify trg_require_auth_method allows user+identity in same TX at Stage 1 |
| D-17 | No new DB enum values needed |
| D-18 | No new one_time_tokens.token_type values needed |

---

## KV prefixes

| Prefix | Handler | Type |
|---|---|---|
| `tgcb:ip:` | POST /callback | IP limiter |
| `tglnk:usr:` | POST /link | User limiter |
| `tgunlk:usr:` | DELETE /unlink | User limiter |

---

## New SQL queries (Stage 1)

`GetIdentityByProviderUID`, `GetIdentityByUserAndProvider`, `GetUserForOAuth`,
`CreateUserWithTelegramTx` (manual TX), `CreateOAuthSessionTx` (manual TX),
`UpdateIdentityProfile`, `InsertIdentity`, `GetUserIdentitiesWithPassword`,
`DeleteIdentity`

---

## New sentinel errors (package-local, not authshared)

`ErrInvalidTelegramSignature`, `ErrTelegramAuthDateExpired`,
`ErrProviderAlreadyLinked`, `ErrProviderUIDTaken`, `ErrProviderNotLinked`,
`ErrLastAuthMethod`

---

## Config dependency

`deps.Config.TelegramBotToken string` — must be added to `app.Deps` before Stage 3.

---

## Stage checklist

- [x] Stage 0 — Design
- [x] Stage 1 — Foundations (SQL + types + models + errors + validators skeleton)
- [x] Stage 2 — Data layer (store)
- [x] Stage 3 — Logic layer (service)
- [x] Stage 4 — HTTP layer (handler + routes)
- [ ] Stage 5 — Audit review
- [ ] Stage 6 — Unit tests (manual)
- [ ] Stage 7 — E2E (manual)
- [ ] Stage 8 — Docs
