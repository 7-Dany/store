# Email Change Flow — Stage 1: Foundations

**Depends on:** Stage 0 approved (all open questions Q-01 / Q-02 / Q-03 answered).
**Stage goal:** New SQL queries added and regenerated (`make sqlc`), three audit event
constants added, service-layer input/result types added (`models.go`), HTTP
request/response types added (`requests.go`), feature-exclusive validators added
(`validators.go`), feature-exclusive errors added (`errors.go`). **No store method,
no service method, no handler.**

---

## Read first (no modifications)

| File | Why |
|---|---|
| `docs/prompts/email-change/0-design.md` | Source of truth — SQL query list (§4), audit events (§4), guard ordering (§5) |
| `sql/queries/auth.sql` (last 80 lines) | Username section — confirm style before appending email-change section |
| `internal/audit/audit.go` | Event naming convention; confirm no collision; read `AllEvents()` and the test sync note |
| `internal/domain/profile/username/models.go` | Input/Result type layout in this domain |
| `internal/domain/profile/username/requests.go` | Request/response struct layout |
| `internal/domain/profile/username/validators.go` | Validator pattern (NormaliseAndValidateUsername) |
| `internal/domain/profile/username/errors.go` | Feature-exclusive error style |
| `docs/RULES.md §3.9` | SQL conventions — no raw SQL, PascalCase names, named params with @, sections |
| `docs/RULES.md §3.11` | Naming: Input/Result types, sentinel errors |
| `docs/RULES.md §3.14 Sync S-1` | Audit event triad — const + AllEvents() + test table must sync in one commit |
| `migrations/001_core.sql` (or equivalent) | Confirm: (a) token_type is text, not enum; (b) chk_ott constraints; (c) answer Q-01 / Q-03 from Stage 0 |

---

## Pre-flight

1. Confirm Stage 0 is approved with all three open questions (Q-01, Q-02, Q-03) answered and D-01 updated if the one_time_tokens metadata column exists.
2. Confirm `internal/domain/profile/email/` does NOT yet exist (new package).
3. Confirm none of the new SQL query names, audit event constants, or type names already exist in their target files.
4. Confirm `one_time_tokens.token_type` is `text` (not an enum) — no migration needed for new type strings. If it IS an enum, add a `migrations/XXX_email_change_token_types.sql` migration first.

If any pre-flight check fails, stop and report.

---

## Deliverables

### 1. SQL — append to `sql/queries/auth.sql`

Append a new `/* ── Email change ── */` section at the end of auth.sql (after the Username section). Write all 11 new queries listed in Stage 0 §4 in the same style as the existing sections:
- Named params with `@` prefix
- `:one` / `:exec` / `:execrows` annotations
- Comment block above each query explaining guards, index usage, and caller responsibilities
- `ORDER BY created_at DESC, id DESC` on all FOR UPDATE token lookups (matches existing pattern)
- `FOR UPDATE` on `GetEmailChangeVerifyToken` and `GetEmailChangeConfirmToken`
- TTL param as `@ttl_seconds::float8` using `make_interval(secs => ...)` (matches CreateEmailVerificationToken pattern)
- `max_attempts = 5` for both new token types (Stage 0 D-12)

After writing, run:
```bash
make sqlc
go build ./internal/db/...
```

Then read `internal/db/auth.sql.go` to confirm all 11 generated method names and their parameter/return types. Note any discrepancies from what models.go will need.

### 2. Audit events — `internal/audit/audit.go`

Add to the `const` block (after `EventUsernameChanged`):

```go
// EventEmailChangeRequested is emitted when a user initiates an email change
// and an OTP is sent to their current email address.
EventEmailChangeRequested EventType = "email_change_requested"

// EventEmailChangeCurrentVerified is emitted when a user successfully verifies
// their current email OTP and receives a grant token for step 3.
EventEmailChangeCurrentVerified EventType = "email_change_current_verified"

// EventEmailChanged is emitted when a user's email address is successfully updated.
// The metadata field contains old_email and new_email.
EventEmailChanged EventType = "email_changed"
```

Also add all three to `AllEvents()` return slice, and add three rows to
`TestEventConstants_ExactValues` in `internal/audit/audit_test.go`.
**All three files in the same commit (RULES.md §3.14 Sync S-1).**

Verify:
```bash
go test ./internal/audit/...
```

### 3. Models — `internal/domain/profile/email/models.go`

Package: `email`

```go
// EmailChangeRequestInput is the service-layer input for step 1 (POST /email/request-change).
// UserID is a [16]byte parsed from the JWT user_id claim by the handler.
type EmailChangeRequestInput struct {
    UserID    [16]byte
    NewEmail  string
    IPAddress string
    UserAgent string
}

// EmailChangeVerifyCurrentInput is the service-layer input for step 2 (POST /email/verify-current).
type EmailChangeVerifyCurrentInput struct {
    UserID    [16]byte
    Code      string
    IPAddress string
    UserAgent string
}

// EmailChangeVerifyCurrentResult is the service result for step 2 on success.
type EmailChangeVerifyCurrentResult struct {
    GrantToken string
    ExpiresIn  int // seconds; always 600
}

// EmailChangeConfirmInput is the service-layer input for step 3 (POST /email/confirm-change).
// AccessJTI is the JTI of the caller's current access token, extracted by the handler
// from the JWT claims, used to blocklist the token after a successful email change.
type EmailChangeConfirmInput struct {
    UserID     [16]byte
    GrantToken string
    Code       string
    IPAddress  string
    UserAgent  string
    AccessJTI  string
}
```

No `json:` tags. No `pgtype`. No `db.*` types.

### 4. Requests — `internal/domain/profile/email/requests.go`

Package: `email`

```go
// requestChangeRequest is the decoded JSON body for POST /email/request-change.
type requestChangeRequest struct {
    NewEmail string `json:"new_email"`
}

// verifyCurrentRequest is the decoded JSON body for POST /email/verify-current.
type verifyCurrentRequest struct {
    Code string `json:"code"`
}

// verifyCurrentResponse is the JSON body returned on a successful step 2.
type verifyCurrentResponse struct {
    GrantToken string `json:"grant_token"`
    ExpiresIn  int    `json:"expires_in"`
}

// confirmChangeRequest is the decoded JSON body for POST /email/confirm-change.
type confirmChangeRequest struct {
    GrantToken string `json:"grant_token"`
    Code       string `json:"code"`
}

// messageResponse is the shared success body for steps 1 and 3.
type messageResponse struct {
    Message string `json:"message"`
}
```

### 5. Validators — `internal/domain/profile/email/validators.go`

Package: `email`

Add `NormaliseAndValidateNewEmail(raw string) (string, error)`:
- Trim whitespace
- Lowercase
- IDNA normalisation (same as register flow — confirm the shared helper location)
- Max 254 bytes after normalisation
- Must contain `@` and a domain with at least one `.`
- Returns `(normalised, nil)` on success
- Returns `("", ErrInvalidEmailFormat)` or `("", ErrEmailTooLong)` on failure

Add `ValidateOTPCode(code string) error`:
- Must be exactly 6 ASCII digits
- Returns `ErrInvalidCodeFormat` on failure

Add `ValidateGrantToken(token string) error`:
- Must be non-empty after trim
- Returns `ErrGrantTokenEmpty` on failure (handler-level guard only)

### 6. Errors — `internal/domain/profile/email/errors.go`

Package: `email`

```go
// Validation sentinels — returned by validators, mapped to 422 by the handler.
var ErrInvalidEmailFormat = errors.New("invalid email format")
var ErrEmailTooLong       = errors.New("email must not exceed 254 bytes")
var ErrInvalidCodeFormat  = errors.New("code must be exactly 6 digits")
var ErrGrantTokenEmpty    = errors.New("grant_token is required")

// Flow sentinels — returned by the service, mapped to 4xx by the handler.
var ErrSameEmail          = errors.New("new email is the same as your current email")
var ErrEmailTaken         = errors.New("email already registered")
var ErrCooldownActive     = errors.New("please wait before requesting another code")
var ErrGrantTokenInvalid  = errors.New("grant token is invalid or expired")

// OTP sentinels — re-exported from authshared where they already exist; only define here
// if authshared.ErrTokenNotFound etc. are NOT already exported. Check before adding.
// If authshared already exports these, import and use them directly — do not redefine.
```

**Note:** Check `internal/domain/auth/shared/errors.go` before defining
`ErrTokenNotFound`, `ErrTokenExpired`, `ErrInvalidCode`, `ErrTooManyAttempts`.
If they exist there, use the authshared aliases (like `profileshared.ErrUserNotFound`).

---

## Done when

```bash
make sqlc
go build ./internal/db/...
go build ./internal/audit/...
go test ./internal/audit/...
go vet ./internal/domain/profile/email/...
go build ./internal/domain/profile/email/...
```

All must pass. `go vet` must produce no output. The package compiles with the five new
files but no service, store, or handler yet.

**Stop here. Do not implement store methods. Stage 2 starts in a separate session.**

---

## Next stage context (for Stage 2 session)

When opening Stage 2, load this file plus `docs/prompts/email-change/0-design.md`.
The Stage 2 session needs:
- Resolved paths from this file (above)
- SQL query names from §4 of Stage 0 (confirmed against generated `internal/db/auth.sql.go`)
- Model types from `models.go` (Stage 1 §3)
- Authshared OTP sentinels location (resolved in Stage 1 §6 check)
