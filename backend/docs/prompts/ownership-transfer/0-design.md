# Ownership Transfer — Stage 0 Design

## Overview

Add a transfer-ownership flow to the existing `internal/domain/rbac/bootstrap/`
package (to be renamed `internal/domain/rbac/owner/`). The flow lets the current
owner atomically hand ownership to another verified user through a
token-confirmed accept step.

This is the **only** legitimate path that bypasses `ErrCannotReassignOwner`
and the `fn_prevent_owner_role_escalation` trigger. No other code path may
perform this swap.

---

## 1. Package rename — `bootstrap/` → `owner/`

### Files to rename / update

| Old path | New path |
|---|---|
| `internal/domain/rbac/bootstrap/` | `internal/domain/rbac/owner/` |
| All `package bootstrap` declarations | `package owner` |
| All import paths ending in `.../rbac/bootstrap` | `.../rbac/owner` |

### Import sites to update

| File | Change |
|---|---|
| `internal/domain/rbac/routes.go` | `import ".../rbac/owner"` — rename `bootstrap.Routes` → `owner.Routes` |
| `internal/server/routes.go` (if it imports bootstrap directly) | same |
| All `_test.go` files that import the bootstrap package | same |

### Existing bootstrap symbols to keep (unchanged behaviour)

All existing types, functions, and routes stay intact under the new package name:
`Bootstrap`, `BootstrapInput`, `BootstrapResult`, `BootstrapUser`, `BootstrapTxInput`,
`ErrUserNotActive`, `ErrUserNotVerified`, `ErrBootstrapSecretEmpty`, `ErrInvalidBootstrapSecret`.

The route itself (`POST /owner/bootstrap`) does **not** change its URL — only
the Go package name changes.

---

## 2. New routes

All three routes are owner-only. The middleware stack rejects any caller that
does not hold `is_owner_role = TRUE` (checked via `deps.RBAC.IsOwner`).

| # | Method | Path | Auth | Description |
|---|--------|------|------|-------------|
| 21 | POST | `/api/v1/owner/transfer` | JWT + owner | Initiate transfer; emails target |
| 22 | POST | `/api/v1/owner/transfer/accept` | none (token is the credential) | Target accepts; atomic role swap |
| 23 | DELETE | `/api/v1/owner/transfer` | JWT + owner | Cancel pending transfer |

---

## 3. Request / response contracts

### Route 21 — Initiate transfer (`POST /owner/transfer`)

**Request body:**
```json
{ "target_user_id": "<uuid>" }
```

**Success:** `201 Created`
```json
{
  "transfer_id": "<uuid>",
  "target_user_id": "<uuid>",
  "expires_at": "<RFC3339>"
}
```

**Error codes:**

| Condition | Status | code |
|---|---|---|
| Another pending transfer already exists | 409 | `transfer_already_pending` |
| Target user not found / deleted | 404 | `user_not_found` |
| Target not active | 422 | `user_not_active` |
| Target email not verified | 422 | `email_not_verified` |
| Target is already the owner | 409 | `user_is_already_owner` |
| Target is self | 409 | `cannot_transfer_to_self` |

---

### Route 22 — Accept transfer (`POST /owner/transfer/accept`)

**Request body:**
```json
{ "token": "<raw_token_string>" }
```

No JWT required — the raw token is the credential.

**Success:** `200 OK`
```json
{
  "new_owner_id": "<uuid>",
  "previous_owner_id": "<uuid>",
  "transferred_at": "<RFC3339>"
}
```

**Error codes:**

| Condition | Status | code |
|---|---|---|
| Token not found / expired / used | 410 | `token_invalid` |
| Target user no longer active/verified at accept time | 422 | `user_not_eligible` |
| Initiating owner no longer holds owner role (race) | 409 | `initiator_not_owner` |

---

### Route 23 — Cancel transfer (`DELETE /owner/transfer`)

No request body.

**Success:** `204 No Content`

**Error codes:**

| Condition | Status | code |
|---|---|---|
| No pending transfer | 404 | `no_pending_transfer` |

---

## 4. Token storage — reuse `one_time_tokens`

Use the existing `one_time_tokens` table with `token_type = 'ownership_transfer'`.
No new table or migration is needed.

| Column | Value |
|---|---|
| `user_id` | target user (recipient of ownership) |
| `token_hash` | `bcrypt(raw_token)` — same pattern as OTP tokens |
| `token_type` | `'ownership_transfer'` |
| `expires_at` | `NOW() + INTERVAL '48 hours'` |
| `used_at` | set to `NOW()` on accept |
| `metadata` | `{"initiated_by": "<owner_uuid>"}` |

**One active transfer at a time** — the initiate store method checks for an
existing unexpired unused token with `token_type = 'ownership_transfer'` before
inserting. If one exists, return `ErrTransferAlreadyPending`.

**Raw token format** — 32 random bytes, base64url-encoded. The raw token is
emailed to the target; only the hash lives in the DB. Use
`authshared.GenerateCodeHash()` pattern adapted for 32 bytes.

---

## 5. Atomic role swap — SQL design

The accept path must atomically:
1. `SET LOCAL rbac.skip_escalation_check = '1'` (first statement in TX)
2. Mark the token `used_at = NOW()` (idempotency guard)
3. Verify old owner still holds owner role (guard against race)
4. `AssignUserRole` for new owner (owner role)
5. `RemoveUserRole` for old owner

These five operations run inside a single `AcceptTransferTx` store method.

### Why both triggers are satisfied

`fn_prevent_owner_role_escalation` fires on INSERT into `user_roles`.
Step 1 (`SET LOCAL`) suppresses it for the duration of this transaction only.

`fn_prevent_orphaned_owner` fires on DELETE. At step 5, two owner rows exist
briefly (old + new). The trigger's `CountActiveOwners` sees 2 and allows the
delete. No bypass needed.

### New sqlc queries (add to `sql/queries/rbac.sql`)

```sql
-- name: InsertOwnershipTransferToken :one
-- name: GetPendingOwnershipTransferToken :one
--   WHERE token_type = 'ownership_transfer' AND used_at IS NULL AND expires_at > NOW()
-- name: ConsumeOwnershipTransferToken :one
--   SET used_at = NOW() WHERE id = $1 AND used_at IS NULL RETURNING *
-- name: DeletePendingOwnershipTransferToken :exec
--   WHERE token_type = 'ownership_transfer' AND used_at IS NULL
--     AND metadata->>'initiated_by' = $1
```

`AcceptTransferTx` is a hand-written store method — not a sqlc query — because
it requires `SET LOCAL` and orchestrates multiple queries inside one transaction.

---

## 6. Guard ordering

### Handler — Initiate (Route 21)

```
1. mustUserID(w, r)                         → 401 if no JWT
2. deps.RBAC.IsOwner(ctx, userID)           → 403 if not owner
3. MaxBytesReader + DecodeJSON              → 400 on decode failure
4. validateInitiateRequest                  → 422 on validation error
5. svc.InitiateTransfer(ctx, in)            → 404/409/422 per §3 error table
6. respond.JSON 201
```

### Handler — Accept (Route 22)

```
1. MaxBytesReader + DecodeJSON              → 400 on decode failure
2. validateAcceptRequest                    → 422 if token field is empty
3. svc.AcceptTransfer(ctx, in)             → 410/422/409 per §3 error table
4. respond.JSON 200
```

No JWT middleware on this route — the raw token is the credential.

### Handler — Cancel (Route 23)

```
1. mustUserID(w, r)                         → 401 if no JWT
2. deps.RBAC.IsOwner(ctx, userID)           → 403 if not owner
3. svc.CancelTransfer(ctx, actingOwnerID)   → 404 if no pending transfer
4. respond.NoContent 204
```

---

## 7. Service layer

```go
// InitiateTransfer validates the target, generates the raw token, stores it
// (hashed), and enqueues the invitation email.
func (s *Service) InitiateTransfer(ctx context.Context, in InitiateInput) (InitiateResult, error)

// AcceptTransfer validates and consumes the token, re-checks target eligibility,
// calls store.AcceptTransferTx, then revokes the old owner's sessions.
func (s *Service) AcceptTransfer(ctx context.Context, in AcceptInput) (AcceptResult, error)

// CancelTransfer deletes the pending transfer token that actingOwnerID initiated.
// Returns ErrNoPendingTransfer if none exists.
func (s *Service) CancelTransfer(ctx context.Context, actingOwnerID [16]byte) error
```

**Session revocation after accept:** Call `RevokeAllUserSessions` (already in
`sql/queries/auth.sql`, used by forced-logout flow) for the old owner.
Use `context.WithoutCancel(ctx)` — a client disconnect must not suppress it.

---

## 8. Sentinel errors (add to `owner/errors.go`)

```go
var ErrTransferAlreadyPending = errors.New("an ownership transfer is already pending")
var ErrTransferTokenInvalid   = errors.New("transfer token is invalid, expired, or already used")
var ErrUserIsAlreadyOwner     = errors.New("target user is already the owner")
var ErrCannotTransferToSelf   = errors.New("cannot transfer ownership to yourself")
var ErrNoPendingTransfer      = errors.New("no pending ownership transfer found")
var ErrInitiatorNotOwner      = errors.New("initiating user no longer holds the owner role")
```

Keep all existing bootstrap sentinels (`ErrUserNotActive`, `ErrUserNotVerified`,
`ErrBootstrapSecretEmpty`, `ErrInvalidBootstrapSecret`) in the same file.

`ErrCannotReassignOwner` (platform-level) is intentionally **not** used in the
transfer path — this path is the single authorised exception to that guard.

---

## 9. Audit events

Add to `internal/audit/audit.go`:

```go
EventOwnerTransferInitiated audit.EventType = "owner_transfer_initiated"
EventOwnerTransferAccepted  audit.EventType = "owner_transfer_accepted"
EventOwnerTransferCancelled audit.EventType = "owner_transfer_cancelled"
```

All three writes use `context.WithoutCancel(ctx)`.

Metadata shape for `owner_transfer_accepted`:
```json
{ "previous_owner_id": "<uuid>", "new_owner_id": "<uuid>" }
```

---

## 10. Rate limits

| Route | Limiter | Key prefix | Limit |
|---|---|---|---|
| POST /owner/transfer | User (acting owner) | `xfr:usr:` | 3 req / 24 h |
| POST /owner/transfer/accept | IP | `xfra:ip:` | 10 req / 1 h |
| DELETE /owner/transfer | User (acting owner) | `xfrc:usr:` | 10 req / 1 h |

Prefixes `xfr:usr:`, `xfra:ip:`, `xfrc:usr:` do not appear in the KV prefix
collision table — safe to use.

---

## 11. Email notifications

**On initiate** — enqueue to target's registered email:
- Subject: "You've been invited to become the store owner"
- Body: raw token or deep link (`/owner/transfer/accept?token=<raw>`)
- TTL note: 48 hours
- Use `deps.MailQueue.Enqueue(...)` — never synchronous

**On accept** — enqueue to old owner's registered email:
- Subject: "Ownership of [store] has been transferred"
- Body: confirms new owner display name / email, timestamp

**On cancel** — no email sent.

---

## 12. Decisions

| ID | Decision | Rationale |
|----|----------|-----------|
| D-OT1 | Feature lives in `owner/` alongside bootstrap | All owner-domain routes belong together; avoids a one-file package |
| D-OT2 | Token in `one_time_tokens`, not a new table | Same TTL, consume, and expiry semantics; no new migration |
| D-OT3 | Accept endpoint is unauthenticated | Target may not be logged in when they follow the link; mirrors magic-link pattern |
| D-OT4 | `SET LOCAL rbac.skip_escalation_check` in accept TX | Only legitimate bypass; session-scoped, cannot leak outside the TX |
| D-OT5 | Old owner sessions revoked immediately on accept | No grace window; owner loses access at the moment of transfer |
| D-OT6 | One pending transfer at a time | Prevents confused-deputy attacks from multiple live tokens |
| D-OT7 | 48-hour TTL | High-stakes action; long enough to act, short enough to limit exposure |
| D-OT8 | Raw token in email, hash in DB | Same pattern as OTP; raw token never persisted |
| D-OT9 | Cancel requires owner role | Only the initiating owner can cancel; target cannot unilaterally abort |

---

## 13. Tests (H-layer, routes 21–23)

| ID | Route | Scenario | Layer |
|---|---|---|---|
| T-OT1 | 21 | Owner initiates transfer; 201; token row in DB | I |
| T-OT2 | 21 | Non-owner caller → 403 | U |
| T-OT3 | 21 | Target not found → 404 | U |
| T-OT4 | 21 | Target not active → 422 | U |
| T-OT5 | 21 | Target email not verified → 422 | U |
| T-OT6 | 21 | Target is already the owner → 409 | U |
| T-OT7 | 21 | Target is self → 409 | U |
| T-OT8 | 21 | Second initiate while one pending → 409 `transfer_already_pending` | I |
| T-OT9 | 22 | Valid token accepted; new owner in DB; old owner role removed | I |
| T-OT10 | 22 | Expired token → 410 | I |
| T-OT11 | 22 | Already-used token → 410 | I |
| T-OT12 | 22 | Target deactivated between initiate and accept → 422 | I |
| T-OT13 | 22 | Old owner's sessions revoked after accept | I |
| T-OT14 | 22 | Old owner's existing token rejected (401) after session revoke | I |
| T-OT15 | 23 | Owner cancels; 204; token row deleted | I |
| T-OT16 | 23 | Cancel with no pending transfer → 404 | U |
| T-OT17 | 23 | Non-owner cancel attempt → 403 | U |

---

## 14. Mint doc changes

Update these files **in the same PR** as the package rename:

| File | Change |
|---|---|
| `mint/api-reference/owner/bootstrap.mdx` | Fix any code samples referencing `rbac/bootstrap` → `rbac/owner` |
| `mint/api-reference/owner/transfer.mdx` | **NEW** — document route 21 |
| `mint/api-reference/owner/transfer-accept.mdx` | **NEW** — document route 22 |
| `mint/api-reference/owner/transfer-cancel.mdx` | **NEW** — document route 23 |
| `mint/docs.json` (or `mint.json`) | Add three new pages to the owner nav group |
| Any page saying "owner role reassignment is out of scope" | Remove that qualifier |

---

## 15. File map

```
internal/domain/rbac/owner/           ← renamed from bootstrap/
    routes.go                          ← add routes 21–23 alongside /bootstrap
    handler.go                         ← add InitiateTransfer, AcceptTransfer, CancelTransfer
    service.go                         ← add InitiateTransfer, AcceptTransfer, CancelTransfer
    store.go                           ← add token queries + AcceptTransferTx
    models.go                          ← add InitiateInput, InitiateResult, AcceptInput, AcceptResult
    requests.go                        ← add initiateRequest, acceptRequest
    validators.go                      ← add validateInitiateRequest, validateAcceptRequest
    errors.go                          ← add 6 new sentinels (keep existing bootstrap sentinels)
    handler_test.go                    ← T-OT1–T-OT17 (H-layer)
    service_test.go                    ← service unit tests
    store_test.go                      ← integration tests

sql/queries/rbac.sql                   ← 4 new sqlc queries (§5)
internal/audit/audit.go                ← 3 new event constants (§9)
```
