# Route Map — Resolved Package Paths

Current state of `docs/map/INCOMING.md` with resolved Go package paths.
Update this file when a route is marked `[x]` (implemented).


## Group B — Profile Management

| § | Route | Method | Path | Go package |
|---|---|---|---|---|
| B-3a | Delete Account | DELETE | `/api/v1/profile/me` | `internal/domain/profile/delete-account/` |
| B-3b | Cancel Deletion | POST | `/api/v1/profile/me/cancel-deletion` | `internal/domain/profile/delete-account/` |
| B-3c | Deletion Method | GET | `/api/v1/profile/me/deletion-method` | `internal/domain/profile/delete-account/` |

---

## Group E — Post-OAuth (requires at least one OAuth provider live)

| § | Route | Method | Path | Go package |
|---|---|---|---|---|
| E-1 | Linked Accounts | GET | `/api/v1/profile/me/identities` | `internal/domain/profile/me/` |

---

## Group F — Admin domain (requires owner bootstrap + RBAC)

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

- B-3a/b/c share `profile/delete-account/` — all three endpoints are part of the deletion flow. Package name is `deleteaccount` (hyphen stripped per Go rules).
- E-1 extends `profile/me/` — GET on a sub-resource of the authenticated user.
- F-1a and F-1b share `admin/users/` — same resource, list + detail.
- F-2a and F-2b share `admin/audit/` — same resource, scoped vs global.
- F-3a/b/c share `admin/sessions/` — same resource, all operate on sessions.
- F-4a and F-4b share `admin/lock/` — same resource, lock + unlock.
- F-5a/b/c share `admin/recovery/` — same workflow (CS-assisted account recovery).
- F-5d lives in `auth/magiclink/` not `admin/` — it is the user-facing end of the recovery flow.

## Already Implemented (for reference)

- B-1a/b (`profile/username/`) — ✓ done
- B-2a/b/c (`profile/email/`) — ✓ done
- D-1 Google OAuth — ✓ done at `internal/domain/oauth/google/` (mounted at `/api/v1/oauth/google`)
- D-2 Telegram OAuth — ✓ done at `internal/domain/oauth/telegram/` (mounted at `/api/v1/oauth/telegram`)
- C-1 Owner Bootstrap — ✓ done at `internal/domain/rbac/bootstrap/` (mounted at `/api/v1/owner/bootstrap`)
- RBAC roles/permissions/userroles — ✓ done at `internal/domain/rbac/{roles,permissions,userroles}/` (mounted at `/api/v1/admin/rbac/...`)
