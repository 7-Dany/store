# Store Frontend — Feature Backlog

> Checked = done and wired up. Unchecked = needs building.

---

## Auth

- [x] Login (2-step identifier → password)
- [x] Register (4-step multi-field flow)
- [x] Verify email (OTP)
- [x] Resend verification
- [x] Forgot password (send code)
- [x] Verify reset code
- [x] Reset password
- [x] Change password (`PATCH /auth/password`) — settings/password page
- [x] Account unlock — request (`POST /auth/unlock`) + confirm (`PUT /auth/unlock`) — `/unlock` page with 2-step flow; `use-login.ts` auto-redirects on 423
- [x] Logout (`POST /auth/logout`) — `/api/auth/logout` route fully implemented: calls backend, clears `session` + `refresh_token` cookies, redirects to `/login`

---

## OAuth

- [x] Google sign-in (initiate redirect)
- [x] Google OAuth callback handler
- [x] Telegram sign-in (widget → callback route)
- [x] Google link (authenticated, via `/api/oauth/google/link`)
- [x] Google unlink (`DELETE /oauth/google`)
- [x] Telegram link (`PUT /oauth/telegram`)
- [x] Telegram unlink (`DELETE /oauth/telegram`)

---

## Profile

- [x] View profile (`GET /profile/me`) — dashboard layout fetches and passes to sidebar
- [x] Connected accounts — link/unlink Google & Telegram (`GET /profile/me/identities` + provider link/unlink endpoints) — settings/connections
- [x] Edit profile — display name, avatar URL (`PATCH /profile/me`) — settings/profile
- [x] Update username + live availability typeahead (`PATCH /profile/me/username` + `GET /profile/me/username/available`) — settings/profile
- [x] Set password for OAuth-only accounts (`POST /profile/me/password`) — settings/password
- [x] Sessions list + per-session revoke (`GET /profile/me/sessions`, `DELETE /profile/me/sessions/{id}`) — settings/sessions
- [x] Account deletion flow — password + email-OTP paths (`DELETE /profile/me` + `GET /profile/me/deletion`) — settings/danger
- [x] Cancel pending deletion (`DELETE /profile/me/deletion`) — `use-cancel-deletion.ts` + `PendingDeletionBanner` shown in dashboard layout whenever `scheduled_deletion_at` is present
- [x] Email change — 3-step flow (`POST /profile/me/email` → `POST /profile/me/email/verify` → `PUT /profile/me/email`) — `use-change-email.ts` state machine + `EmailChangeDialog`; email field in `EditInfoCard` now has an Edit button

---

## Settings pages (new)

> All sections live in a single scroll page at `/dashboard/settings` (anchor-nav sidebar), not separate routes.

- [x] `/dashboard/settings` layout — sidebar nav with scroll-spy active state
- [x] `#profile` section — display name + username (auto-saves via `EditInfoCard`)
- [x] `#password` section — change or set password (auto-detected via `GET /profile/me/deletion`)
- [x] `#sessions` section — list + per-session revoke (`SessionsList`)
- [x] `#connections` section — Google & Telegram link/unlink (`LinkedAccounts`)
- [x] `#danger` section — delete account with full dialog flow (`DangerZone`)
- [x] `/dashboard/profile` — simplified read-only view + "Go to Settings" prompt

---

## Admin

- [ ] Lock user (`POST /admin/users/{id}/lock`)
- [ ] Unlock user (`DELETE /admin/users/{id}/lock`)
- [ ] View lock status — admin lock + login lockout (`GET /admin/users/{id}/lock`)
- [ ] Assign user role — permanent + temporary (`PUT /admin/users/{id}/role`)
- [ ] View user role (`GET /admin/users/{id}/role`)
- [ ] Remove user role (`DELETE /admin/users/{id}/role`)
- [ ] Grant direct permission (`POST /admin/users/{id}/permissions`)
- [ ] List user direct permissions (`GET /admin/users/{id}/permissions`)
- [ ] Revoke direct permission (`DELETE /admin/users/{id}/permissions/{grant_id}`)

---

## RBAC

- [ ] List roles (`GET /rbac/roles`)
- [ ] Get single role (`GET /rbac/roles/{id}`)
- [ ] Create role (`POST /rbac/roles`)
- [ ] Edit role — name / description (`PATCH /rbac/roles/{id}`)
- [ ] Delete role (`DELETE /rbac/roles/{id}`)
- [ ] List role permissions (`GET /rbac/roles/{id}/permissions`)
- [ ] Add permission to role (`POST /rbac/roles/{id}/permissions`)
- [ ] Remove permission from role (`DELETE /rbac/roles/{id}/permissions/{perm_id}`)
- [ ] Permissions catalogue (`GET /rbac/permissions`)
- [ ] Permission groups for picker UI (`GET /rbac/permissions/groups`)
- [ ] Bootstrap owner assign — one-time (`PUT /rbac/owner/assign`)
- [ ] Initiate ownership transfer (`POST /rbac/owner/transfer`)
- [ ] Accept ownership transfer — token link (`PUT /rbac/owner/transfer`)
- [ ] Cancel ownership transfer (`DELETE /rbac/owner/transfer`)

---

## Bitcoin

- [ ] Register watch addresses (`POST /bitcoin/watch`) — up to 20 addresses per call, max 100 per user; idempotent; resets 30-min inactivity timer
- [ ] Real-time transaction event stream (`GET /bitcoin/events`) — SSE; emits `mempool_tx`, `confirmed_tx`, `mempool_tx_replaced`, and `stream_requires_reregistration` events; client must re-call `POST /bitcoin/watch` on `stream_requires_reregistration`

---

## Profile (continued)

- [ ] Avatar upload — `EditInfoCard` has a disabled "Change photo (coming soon)" button; needs upload endpoint + storage

---

## General / UX

- [ ] Health check indicator — sidebar or topbar (`GET /health`)
- [ ] Global 401 interceptor — auto-refresh token or redirect to login
- [x] `?error=` param handler on `/login` — `LoginNotices` now handles `oauth_session_expired`, `oauth_cancelled`, `google_link_failed`, `account_locked`, `account_inactive`
- [x] Pending deletion banner — `PendingDeletionBanner` shown in dashboard layout; includes Cancel deletion button wired to `use-cancel-deletion.ts`
- [ ] Dashboard OAuth error toast — `?provider=google&action=linked` redirect is handled; `?error=google_link_failed` (and similar) from the OAuth link route still needs a toast
- [x] Token refresh / silent keep-alive (`POST /auth/refresh`) — `/api/auth/refresh/route.ts` proxy + axios interceptor in `client.ts` (queues concurrent retries, redirects to `/login` on failure); login route now also sets `refresh_token` cookie