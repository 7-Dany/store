# §E-1 — Linked Accounts — Stage 0: Design & Decisions

**Requirement source:** `docs/map/INCOMING.md §E-1`
**Target package:** `internal/domain/profile/me/`

---

## Read first (no modifications)

| File | What to extract |
|---|---|
| `docs/map/INCOMING.md §E-1` | Full requirement text |
| `docs/RULES.md` | Global conventions, error handling, comment style |
| `internal/domain/profile/me/service.go` | Existing Storer + Servicer interfaces |
| `internal/domain/profile/me/store.go` | Existing store methods |
| `internal/domain/profile/me/handler.go` | Existing handler methods + error switch |
| `internal/domain/profile/me/routes.go` | Existing rate limiters and route registrations |
| `internal/domain/profile/me/models.go` | Existing models |
| `internal/domain/profile/shared/errors.go` | Sentinel errors in use |
| `internal/domain/auth/shared/testutil/fake_storer.go` | MeFakeStorer — add new Fn field here |
| `internal/domain/auth/shared/testutil/fake_servicer.go` | MeFakeServicer — add new Fn field here |
| `internal/audit/audit.go` | Existing event names — confirm no collision |
| `sql/queries/oauth.sql` | `user_identities` query style and column names |
| `internal/db/oauth.sql.go` | `UserIdentity` row type — column list and Go types |

---

## 1. Feature summary

`GET /api/v1/profile/me/identities` returns every linked OAuth identity for the
authenticated user. Each identity row describes a provider (Google, Telegram)
along with display metadata. Sensitive fields (`access_token`,
`refresh_token_provider`) are never returned. The endpoint is read-only and
emits no audit row. It extends the existing `profile/me` package by adding a
new store method, service method, and handler method — no new package folder is
required.

---

## 2. HTTP contract

**Method and path:** `GET /api/v1/profile/me/identities`

**Auth required:** Yes — valid JWT

**Request body:** None

**Success response:** `200 OK`
```json
{
  "identities": [
    {
      "provider":       "google",
      "provider_email": "alice@gmail.com",
      "display_name":   "Alice Smith",
      "avatar_url":     "https://lh3.googleusercontent.com/...",
      "created_at":     "2025-01-15T10:30:00Z"
    }
  ]
}
```

- `identities` is always an array — never null; empty array when no identities linked.
- `provider_email`, `display_name`, `avatar_url` are omitted from the JSON object
  when the DB value is NULL (use `omitempty`).
- `created_at` is always present; returned as UTC RFC3339.
- `access_token` and `refresh_token_provider` are **never** returned.

**Error responses:**

| Status | Code | Condition |
|---|---|---|
| 401 | `unauthorized` | Missing or invalid JWT (middleware) |
| 429 | `too_many_requests` | Rate limit exceeded |
| 500 | `internal_error` | Unexpected store error |

---

## 3. Decisions

| # | Question | Decision | Rationale |
|---|---|---|---|
| D-01 | Should an empty identities list return 200 or 404? | 200 with `{"identities":[]}` | Resource exists (the user), sub-collection just happens to be empty; matches REST convention and avoids misleading 404. |
| D-02 | What order are identities returned in? | `created_at ASC` | Stable, deterministic ordering; oldest link first is most intuitive in a settings UI. |
| D-03 | Should `access_token` (which is stored encrypted) be returned decrypted? | Never returned in any form | The `access_token` column is provider-internal and carries the `enc:` prefix; exposing it would be a security leak regardless of decryption state. |
| D-04 | Should a user who has no linked identities (password-only) see a different response? | No — 200 with empty array | Consistent contract; avoids branching logic in the handler. |
| D-05 | Does this route need an audit row? | No | Read-only endpoint; no state change occurs. |
| D-06 | Rate-limit key: IP or user? | Per IP (`ident:ip:`) | Matches INCOMING.md exactly: "20 req / 1 min per IP". |

---

## 4. Data model

**New SQL query required:**

| Query name | Type | Purpose |
|---|---|---|
| `GetUserIdentities` | `:many` | Fetch all `user_identities` rows for a given `user_id`, ordered by `created_at ASC`. Returns only the columns needed for the response (`provider`, `provider_email`, `display_name`, `avatar_url`, `created_at`). |

Add this query to `sql/queries/oauth.sql` (the file that owns `user_identities` queries).

**New schema changes:** None. `user_identities` table already exists.

**New audit events:** None. Read-only endpoint.

---

## 5. Guard ordering

```
1. Extract user_id from JWT context — 401 if absent (mustUserID helper, already exists)
2. Call store.GetUserIdentities(ctx, userID) — 500 on unexpected error; empty slice is OK
3. Map []db row → []LinkedIdentity response objects (strip access_token, refresh_token_provider)
4. Respond 200 with {"identities": [...]}
```

**Timing invariants:** None — this is a pure read with no credential comparison.

**context.WithoutCancel usage:** None — no security-critical writes.

---

## 6. Rate limiting

| Endpoint | Limit | KV prefix | Rationale |
|---|---|---|---|
| `GET /me/identities` | 20 req / 1 min per IP | `ident:ip:` | Matches INCOMING.md; prevents bulk enumeration of identity data. |

**Prefix collision check:** `ident:ip:` does not appear anywhere in `E2E_CHECKLIST.md`.

---

## 7. Test case inventory

**Legend:** S = service unit test, H = handler unit test, I = store integration test

| # | Case | Layer | Setup | Expected outcome |
|---|---|---|---|---|
| T-01 | Happy path — user has identities | S, H, I | User with ≥ 1 identity rows | 200; response contains correct provider, provider_email, display_name, avatar_url, created_at |
| T-02 | Happy path — user has no identities | S, H, I | User with 0 identity rows | 200; `"identities": []` (never null) |
| T-03 | Identities ordered by created_at ASC | I | User with 2 identities inserted at different times | Older identity appears first in response |
| T-04 | access_token never in response | H | FakeServicer returns identity with non-empty access_token | Response JSON does not contain "access_token" key |
| T-05 | refresh_token_provider never in response | H | (same as T-04 setup) | Response JSON does not contain "refresh_token_provider" key |
| T-06 | Nullable fields omitted when NULL | H | FakeServicer returns identity with nil provider_email, display_name, avatar_url | Response object omits those keys (omitempty) |
| T-07 | Missing auth → 401 | H | No JWT in context | 401 `unauthorized` |
| T-08 | Store error → 500 | S | FakeStorer.GetUserIdentitiesFn returns non-nil error | Service wraps and returns error; handler responds 500 `internal_error` |
| T-09 | DB integration — correct columns returned, access_token excluded | I | Seed user + identity; call store method | Returned slice contains correct field values; no access_token field on the Go type |

---

## 8. Open questions

None. All design points resolved above.

---

## Approval checklist

- [x] HTTP contract (§2) matches the requirement exactly — no gaps
- [x] Every decision in §3 has a rationale
- [x] Guard ordering (§5) is complete — no guard from the requirement is missing
- [x] Test case inventory (§7) covers every path in §5 and every error in §2
- [x] No open questions in §8
- [x] Rate-limit prefix `ident:ip:` is unique across the codebase
- [x] Target package follows one-route-one-folder rule (extends existing `me/` — permitted because it operates on the authenticated user's own sub-resource)

**Stage 0 approved. Stage 1 may begin.**

---

## Stage 1 notes (pre-filled for implementer)

**SQL to add to `sql/queries/oauth.sql`:**
```sql
-- name: GetUserIdentities :many
-- Returns all linked OAuth identities for the given user, oldest first.
-- access_token and refresh_token_provider are intentionally excluded —
-- they are provider secrets and must never be returned to clients.
SELECT
    provider,
    provider_email,
    display_name,
    avatar_url,
    created_at
FROM user_identities
WHERE user_id = @user_id::uuid
ORDER BY created_at ASC;
```

**New model to add to `internal/domain/profile/me/models.go`:**
```go
// LinkedIdentity is the service-layer representation of a single linked OAuth
// identity. access_token and refresh_token_provider are intentionally absent.
type LinkedIdentity struct {
    Provider      string
    ProviderEmail *string
    DisplayName   *string
    AvatarURL     *string
    CreatedAt     time.Time
}
```

**New response type (add to a `responses.go` or `requests.go` file in `me/`):**
```go
type identityItem struct {
    Provider      string     `json:"provider"`
    ProviderEmail *string    `json:"provider_email,omitempty"`
    DisplayName   *string    `json:"display_name,omitempty"`
    AvatarURL     *string    `json:"avatar_url,omitempty"`
    CreatedAt     time.Time  `json:"created_at"`
}

type identitiesResponse struct {
    Identities []identityItem `json:"identities"`
}
```

**Storer interface addition (in `service.go`):**
```go
GetUserIdentities(ctx context.Context, userID [16]byte) ([]LinkedIdentity, error)
```

**Servicer interface addition (in `handler.go`):**
```go
GetUserIdentities(ctx context.Context, userID string) ([]LinkedIdentity, error)
```

**FakeStorer addition (`authsharedtest.MeFakeStorer`):**
```go
GetUserIdentitiesFn func(ctx context.Context, userID [16]byte) ([]me.LinkedIdentity, error)
```
Default should return `([]me.LinkedIdentity{}, nil)` — empty slice, not nil.

**FakeServicer addition (`authsharedtest.MeFakeServicer`):**
```go
GetUserIdentitiesFn func(ctx context.Context, userID string) ([]me.LinkedIdentity, error)
```
Default: `([]me.LinkedIdentity{}, nil)`.
