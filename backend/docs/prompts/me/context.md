# §E-1 Linked Accounts — Resolved Context

**Section:** INCOMING.md §E-1
**Package:** `internal/domain/profile/me/`
**Status:** Stage 0 approved

## Resolved paths
- SQL file: `sql/queries/oauth.sql` (append new query)
- Models: `internal/domain/profile/me/models.go`
- Requests/responses: `internal/domain/profile/me/requests.go`
- Store interface: `internal/domain/profile/me/service.go` (Storer)
- Store impl: `internal/domain/profile/me/store.go`
- Service interface: `internal/domain/profile/me/handler.go` (Servicer)
- Service impl: `internal/domain/profile/me/service.go`
- Handler: `internal/domain/profile/me/handler.go`
- Routes: `internal/domain/profile/me/routes.go`
- FakeStorer: `internal/domain/auth/shared/testutil/fake_storer.go` (MeFakeStorer)
- FakeServicer: `internal/domain/auth/shared/testutil/fake_servicer.go` (MeFakeServicer)

## Key decisions (from Stage 0 §3)
- D-01: Empty list → 200 `{"identities":[]}`, never 404
- D-02: Ordered by `created_at ASC` (oldest first)
- D-03: `access_token` never returned in any form
- D-04: Password-only user (no identities) → 200 with empty array
- D-05: No audit row — read-only endpoint
- D-06: Rate-limit per IP (`ident:ip:`), 20 req / 1 min

## New SQL queries
- `GetUserIdentities` — `:many` on `user_identities`, `user_id = $1`, `ORDER BY created_at ASC`
- Columns returned: `provider`, `provider_email`, `display_name`, `avatar_url`, `created_at`
- File: `sql/queries/oauth.sql`

## New audit events
None — read-only endpoint.

## New sentinel errors
None — only `internal_error` (500) besides middleware 401/429.

## Rate-limit prefixes
- `ident:ip:` — GET /me/identities, 20 req / 1 min per IP

## Test case IDs (from Stage 0 §7)
- S-layer: T-01 (happy path ≥1 identity), T-02 (empty list), T-08 (store error → wrapped error)
- H-layer: T-04 (access_token absent), T-05 (refresh_token absent), T-06 (nullable fields omit), T-07 (no auth → 401), T-08 (store error → 500)
- I-layer: T-01, T-02, T-03 (order ASC), T-09 (correct columns, no access_token)
