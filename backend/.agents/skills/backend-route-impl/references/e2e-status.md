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
| ✓ | POST /profile/me/email/request-change | `echg:usr:` |
| ✓ | POST /profile/me/email/verify-current | `echg:usr:vfy:` |
| ✓ | POST /profile/me/email/confirm-change | `echg:usr:cnf:` |
| ⏳ | DELETE /profile/me | `del:usr:` |
| ⏳ | POST /profile/me/cancel-deletion | `delc:usr:` |
| ⏳ | GET /profile/me/deletion-method | `delm:usr:` |

## OAuth domain (`internal/domain/oauth/`)

| Status | Endpoint | KV prefixes used |
|---|---|---|
| ✓ | GET /oauth/google | `goauth:init:ip:` |
| ✓ | GET /oauth/google/callback | `goauth:cb:ip:` |
| ✓ | DELETE /oauth/google/unlink | `goauth:unl:usr:` |
| ✓ | POST /oauth/telegram/callback | `tgcb:ip:` |
| ✓ | POST /oauth/telegram/link | `tglnk:usr:` |
| ✓ | DELETE /oauth/telegram/unlink | `tgunlk:usr:` |

## RBAC domain (`internal/domain/rbac/`)

| Status | Endpoint | KV prefixes used |
|---|---|---|
| ✓ | POST /owner/bootstrap | `bstrp:ip:` |
| ✓ | GET /admin/permissions | — (JWT + RBAC gated) |
| ✓ | GET /admin/permissions/groups | — (JWT + RBAC gated) |
| ✓ | GET /admin/rbac/roles | — (JWT + RBAC gated) |
| ✓ | POST /admin/rbac/roles | — (JWT + RBAC gated) |
| ✓ | GET /admin/rbac/roles/{id} | — (JWT + RBAC gated) |
| ✓ | PATCH /admin/rbac/roles/{id} | — (JWT + RBAC gated) |
| ✓ | DELETE /admin/rbac/roles/{id} | — (JWT + RBAC gated) |
| ✓ | GET /admin/rbac/roles/{id}/permissions | — (JWT + RBAC gated) |
| ✓ | POST /admin/rbac/roles/{id}/permissions | — (JWT + RBAC gated) |
| ✓ | DELETE /admin/rbac/roles/{id}/permissions/{perm_id} | — (JWT + RBAC gated) |
| ✓ | GET /admin/rbac/users/{user_id}/role | — (JWT + RBAC gated) |
| ✓ | PUT /admin/rbac/users/{user_id}/role | — (JWT + RBAC gated) |
| ✓ | DELETE /admin/rbac/users/{user_id}/role | — (JWT + RBAC gated) |

## Other domains (not yet started)

| Status | Group | Contains |
|---|---|---|
| ⏳ | E — Post-OAuth | GET /profile/me/identities |
| ⏳ | F — Admin | User list, audit, sessions, lock/unlock, admin recovery |

---

## All KV prefixes in use (collision reference)

**New prefixes must not appear in this list, including reserved/planned ones.**
Prefixes marked with `# planned` below are pre-claimed by in-progress features
— do not use them even if the endpoint is still ⏳.

`reg:ip:` `vfy:ip:` `rsnd:ip:` `lgn:ip:` `rfsh:ip:` `lgout:ip:`
`unlk:ip:` `fpw:ip:` `vpc:ip:` `rpw:ip:` `cpw:ip:`
`pme:ip:` `psess:ip:` `rsess:ip:` `prof:ip:`
`spw:usr:` `unav:ip:` `uchg:usr:`
`echg:usr:` `echg:usr:vfy:` `echg:usr:cnf:` `echg:pending:` `echg:gt:` *(live — email change)*
`blocklist:jti:`
`goauth:init:ip:` `goauth:cb:ip:` `goauth:unl:usr:`
`tgcb:ip:` `tglnk:usr:` `tgunlk:usr:`
`bstrp:ip:`
`del:usr:` `delc:usr:` `delm:usr:` *(planned — delete-account ⏳)*
`health:ip:`
