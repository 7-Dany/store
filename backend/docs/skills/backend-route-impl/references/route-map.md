# Route Map — Resolved Package Paths

Current state of `docs/map/INCOMING.md` with resolved Go package paths.
Update this file when a route is marked `[x]` (implemented).

---

## Group A — Extends existing packages (no schema changes)

| § | Route | Method | Path | Go package |
|---|---|---|---|---|
| A-1 | Profile Update | PATCH | `/api/v1/auth/me/profile` | `internal/domain/auth/profile/` |
| A-2 | Set Password | POST | `/api/v1/auth/set-password` | `internal/domain/auth/set-password/` |

---

## Group B — New packages (requires schema migration §B-0)

| § | Route | Method | Path | Go package |
|---|---|---|---|---|
| B-0 | Schema Migration | — | — | `sql/migrations/` |
| B-1a | Username Availability | GET | `/api/v1/auth/username/available` | `internal/domain/auth/username/` |
| B-1b | Update Username | PATCH | `/api/v1/auth/me/username` | `internal/domain/auth/username/` |
| B-2a | Email Change — Request | POST | `/api/v1/auth/me/email/request-change` | `internal/domain/auth/email/request/` |
| B-2b | Email Change — Verify Current | POST | `/api/v1/auth/me/email/verify-current` | `internal/domain/auth/email/verify/` |
| B-2c | Email Change — Confirm New | POST | `/api/v1/auth/me/email/confirm-change` | `internal/domain/auth/email/confirm/` |
| B-3 | Delete Account | DELETE | `/api/v1/auth/me` | `internal/domain/auth/delete/` |

---

## Group C — No external dependencies

| § | Route | Method | Path | Go package |
|---|---|---|---|---|
| C-1 | Owner Bootstrap | POST | `/api/v1/auth/owner/bootstrap` | `internal/domain/auth/bootstrap/` |

---

## Group D — OAuth (requires external OIDC / HMAC setup)

| § | Route | Method | Path | Go package |
|---|---|---|---|---|
| D-1 | Google OAuth | GET + GET + POST + DELETE | `/api/v1/auth/oauth/google{/callback,/link,/unlink}` | `internal/domain/auth/oauth/google/` |
| D-2 | Telegram OAuth | POST + POST + DELETE | `/api/v1/auth/oauth/telegram{/callback,/link,/unlink}` | `internal/domain/auth/oauth/telegram/` |

---

## Group E — Post-OAuth (requires at least one OAuth provider live)

| § | Route | Method | Path | Go package |
|---|---|---|---|---|
| E-1 | Linked Accounts | GET | `/api/v1/auth/me/identities` | `internal/domain/auth/identities/` |

---

## Group F — Admin domain (requires owner bootstrap)

| § | Route | Method | Path | Go package |
|---|---|---|---|---|
| F-1a | User List | GET | `/api/v1/admin/users` | `internal/domain/admin/users/` |
| F-1b | User Detail | GET | `/api/v1/admin/users/{id}` | `internal/domain/admin/users/` |
| F-2a | User Audit Log | GET | `/api/v1/admin/users/{id}/audit` | `internal/domain/admin/audit/` |
| F-2b | Global Audit Log | GET | `/api/v1/admin/audit` | `internal/domain/admin/audit/` |
| F-3a | List User Sessions | GET | `/api/v1/admin/users/{id}/sessions` | `internal/domain/admin/sessions/` |
| F-3b | Revoke All Sessions | DELETE | `/api/v1/admin/users/{id}/sessions` | `internal/domain/admin/sessions/` |
| F-3c | Revoke One Session | DELETE | `/api/v1/admin/users/{id}/sessions/{sid}` | `internal/domain/admin/sessions/` |
| F-4a | Lock User | PATCH | `/api/v1/admin/users/{id}/lock` | `internal/domain/admin/lock/` |
| F-4b | Unlock User | PATCH | `/api/v1/admin/users/{id}/unlock` | `internal/domain/admin/lock/` |
| F-5a | Admin Email Change | PATCH | `/api/v1/admin/users/{id}/email` | `internal/domain/admin/recovery/` |
| F-5b | Issue Magic Link | POST | `/api/v1/admin/users/{id}/magic-link` | `internal/domain/admin/recovery/` |
| F-5c | Force Password Reset | POST | `/api/v1/admin/users/{id}/force-password-reset` | `internal/domain/admin/recovery/` |
| F-5d | Magic Link Verify | GET | `/api/v1/auth/magic-link/verify` | `internal/domain/auth/magiclink/` |

---

## Notes

- B-1a and B-1b share `auth/username/` — same resource, availability is a stateless helper.
- F-1a and F-1b share `admin/users/` — same resource, list + detail.
- F-2a and F-2b share `admin/audit/` — same resource, scoped vs global.
- F-3a/b/c share `admin/sessions/` — same resource, all operate on sessions.
- F-4a and F-4b share `admin/lock/` — same resource, lock + unlock.
- F-5a/b/c share `admin/recovery/` — same workflow (CS-assisted account recovery).
- F-5d lives in `auth/magiclink/` not `admin/` — it is the user-facing end of the recovery flow.
