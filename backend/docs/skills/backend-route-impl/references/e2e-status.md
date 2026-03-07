# E2E Status — Endpoint Completion Summary

Compact reference for the AI. Full test-case details live in `CHECKLIST.md`.
The AI only needs columns 1-3 to avoid re-implementing done work and to check KV prefix collisions.

**Legend:** ✓ = E2E passing and production-ready | ⏳ = not yet started | ~ = in progress

---

## Auth domain (`internal/domain/auth/`)

| Status | Endpoint | KV prefixes used |
|---|---|---|
| ✓ | POST /auth/register | `reg:ip:` |
| ✓ | POST /auth/verify-email | `vfy:ip:` |
| ✓ | POST /auth/resend-verification | `rsnd:ip:` |
| ✓ | POST /auth/login | `lgn:ip:` |
| ✓ | POST /auth/refresh | `rfsh:ip:` |
| ✓ | POST /auth/logout | `lgout:ip:` |
| ✓ | POST /auth/request-unlock | `unlk:ip:` (shared with confirm-unlock) |
| ✓ | POST /auth/confirm-unlock | `unlk:ip:` (shared with request-unlock) |
| ✓ | POST /auth/forgot-password | `fpw:ip:` |
| ✓ | POST /auth/verify-reset-code | `vpc:ip:` |
| ✓ | POST /auth/reset-password | `rpw:ip:` |
| ✓ | POST /auth/change-password | `cpw:ip:` |

## Profile domain (`internal/domain/profile/`)

| Status | Endpoint | KV prefixes used |
|---|---|---|
| ✓ | GET /profile/me | `pme:ip:` |
| ✓ | PATCH /profile/me | `prof:ip:` |
| ✓ | GET /profile/sessions | `psess:ip:` |
| ✓ | DELETE /profile/sessions/{id} | `rsess:ip:` |
| ✓ | POST /profile/set-password | `spw:usr:` |
| ✓ | GET /profile/username/available | `unav:ip:` |
| ✓ | PATCH /profile/me/username | `uchg:usr:` |
| ⏳ | POST /profile/me/email/request-change | `echg:usr:` |
| ⏳ | POST /profile/me/email/verify-current | `echg:usr:vfy:` |
| ⏳ | POST /profile/me/email/confirm-change | `echg:usr:cnf:` |
| ⏳ | DELETE /profile/me | *(TBD — see §B-3)* |

## Other domains (not yet started)

| Status | Group | Contains |
|---|---|---|
| ⏳ | D — OAuth | Google + Telegram OAuth |
| ⏳ | E — Post-OAuth | GET /profile/me/identities |
| ⏳ | C — Owner bootstrap | POST /admin/bootstrap |
| ⏳ | F — Admin | All admin routes |

---

## All KV prefixes in use (collision reference)

`reg:ip:` `vfy:ip:` `rsnd:ip:` `lgn:ip:` `rfsh:ip:` `lgout:ip:`
`unlk:ip:` `fpw:ip:` `vpc:ip:` `rpw:ip:` `cpw:ip:`
`pme:ip:` `psess:ip:` `rsess:ip:` `prof:ip:`
`spw:usr:` `unav:ip:` `uchg:usr:`
`blocklist:jti:`
*(planned)* `echg:usr:` `echg:usr:vfy:` `echg:usr:cnf:` `echg:pending:` `echg:gt:`

New prefixes must not appear in this list.
