# §D-2 Telegram OAuth — Stage 1: Foundations

**Depends on:** Stage 0 approved (design doc at `docs/prompts/telegram/0-design.md`).
**Stage goal:** Config + Deps wired; sentinel errors, service I/O types, HTTP request
structs, and HMAC/auth_date validators in place. No store, no service, no handler.

---

## Read first (no modifications)

| File | Why |
|---|---|
| `docs/prompts/telegram/0-design.md` | Source of truth — decisions, errors, models, validators |
| `docs/prompts/telegram/context.md` | Resolved paths and key decisions |
| `docs/RULES.md` | Global conventions |
| `internal/config/config.go` | Where to add `TelegramBotToken`; how existing fields are declared, validated, and defaulted |
| `internal/app/deps.go` | `OAuthConfig` struct — add `TelegramBotToken string` here |
| `internal/domain/oauth/google/errors.go` | Error declaration style to follow |
| `internal/domain/oauth/google/models.go` | Model declaration style to follow |
| `internal/domain/oauth/shared/errors.go` | Errors already in shared — do NOT re-declare these in telegram |
| `sql/queries/oauth.sql` | Confirm all needed queries already exist (no new SQL needed) |
| `internal/db/models.go` | Confirm `db.AuthProviderTelegram` exists |

---

## Pre-flight

1. Confirm `db.AuthProviderTelegram` exists in `internal/db/models.go` — it does; no schema migration needed.
2. Confirm all required queries already exist in `sql/queries/oauth.sql`:
   `GetIdentityByProviderUID`, `GetIdentityByUserAndProvider`, `GetUserForOAuthCallback`,
   `GetUserAuthMethods`, `CreateOAuthUser`, `UpsertUserIdentity`, `DeleteUserIdentity`,
   `InsertAuditLog`, `CreateUserSession`, `CreateRefreshToken`, `UpdateLastLoginAt` — all present.
   **No new SQL is needed. Do not run `make sqlc`.**
3. Confirm `internal/domain/oauth/telegram/` does not yet exist.
4. Confirm `oauthshared.ErrIdentityNotFound`, `oauthshared.ErrLastAuthMethod`,
   `oauthshared.ErrAccountLocked` already exist — do NOT re-declare these in the telegram package.

---

## Deliverables

### 1. `internal/config/config.go` — add `TelegramBotToken`

Add to the `Config` struct, in the `── OAuth ──` section, directly after `OAuthErrorURL`:

```go
// TelegramBotToken is the Telegram Bot API token used to verify HMAC-SHA256
// signatures on Login Widget payloads. Required when Telegram OAuth is enabled.
// Generate at https://t.me/BotFather. Keep out of version control.
TelegramBotToken string
```

In `Load()`, add to the config literal in the OAuth section:

```go
TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
```

In `validate()`, add to the `fields` slice alongside the other OAuth fields:

```go
{"TELEGRAM_BOT_TOKEN", c.TelegramBotToken},
```

### 2. `internal/app/deps.go` — add `TelegramBotToken` to `OAuthConfig`

Add to the `OAuthConfig` struct, after `ErrorURL`:

```go
// TelegramBotToken is the raw Telegram Bot API token used to derive the HMAC
// secret key for Login Widget signature verification. Sourced from
// TELEGRAM_BOT_TOKEN in config.Config and validated as non-empty at startup.
TelegramBotToken string
```

### 3. Wire `TelegramBotToken` in `internal/server/`

Find where `OAuthConfig` is assembled (likely `server/router.go` or `server/server.go`
where `app.Deps` is constructed) and add:

```go
TelegramBotToken: cfg.TelegramBotToken,
```

alongside the existing Google fields. Read the file before editing to find the exact location.

### 4. `internal/domain/oauth/telegram/errors.go`

```go
// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram

import "errors"

// ErrInvalidTelegramSignature is returned when the HMAC-SHA256 signature on
// the Telegram widget payload does not match the expected value.
var ErrInvalidTelegramSignature = errors.New("invalid telegram signature")

// ErrTelegramAuthDateExpired is returned when the auth_date field in the
// widget payload is more than 86400 seconds old or more than 60 seconds in
// the future (replay protection).
var ErrTelegramAuthDateExpired = errors.New("telegram auth_date too old or in future")

// ErrProviderAlreadyLinked is returned when the authenticated user already has
// a Telegram identity linked to their account.
var ErrProviderAlreadyLinked = errors.New("telegram account already linked to this user")

// ErrProviderUIDTaken is returned when the Telegram user ID in the widget
// payload is already linked to a different platform account.
var ErrProviderUIDTaken = errors.New("telegram account already linked to another user")

// ErrProviderNotLinked is returned when the authenticated user does not have
// a Telegram identity linked and an unlink is requested.
var ErrProviderNotLinked = errors.New("no telegram identity linked to this account")
```

### 5. `internal/domain/oauth/telegram/models.go`

Service-layer I/O types. No `json:` tags. No `pgtype`. Mirrors `google/models.go` but
adapted for Telegram (no PKCE, no access token, no email-match path).

```go
// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram

import (
	"github.com/7-Dany/store/backend/internal/audit"
	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
)

// TelegramUser holds the fields extracted from a validated Telegram Login Widget
// payload. All string fields may be empty except ID (which is always present).
type TelegramUser struct {
	ID        int64  // Telegram user ID (stable provider UID)
	FirstName string
	LastName  string // optional
	Username  string // optional
	PhotoURL  string // optional
	AuthDate  int64  // Unix timestamp; validated by CheckAuthDate
}

// CallbackInput is the input to Service.HandleCallback.
type CallbackInput struct {
	User      TelegramUser
	IPAddress string
	UserAgent string
}

// CallbackResult is returned by Service.HandleCallback on success.
// Exactly one of Linked or (Session + NewUser) is meaningful:
//   - Linked == true  → link mode succeeded; no session is issued
//   - Linked == false → login/register mode succeeded; Session carries token metadata
type CallbackResult struct {
	Session oauthshared.LoggedInSession
	NewUser bool // true when a new users row was created
	Linked  bool // true when link mode ran successfully
}

// LinkInput is the input to Service.LinkTelegram.
type LinkInput struct {
	UserID    [16]byte
	User      TelegramUser
	IPAddress string
	UserAgent string
}

// ProviderIdentity is the store-layer view of a user_identities row for the
// Telegram provider. Returned by GetIdentityByProviderUID and
// GetIdentityByUserAndProvider.
type ProviderIdentity struct {
	ID     [16]byte
	UserID [16]byte
}

// OAuthUserRecord is the minimal user view needed for lock guards in callback
// and link flows. Returned by GetUserForOAuthCallback.
type OAuthUserRecord struct {
	ID          [16]byte
	IsActive    bool
	IsLocked    bool
	AdminLocked bool
}

// UserAuthMethods is returned by GetUserAuthMethods for the last-auth-method
// guard in the unlink flow.
type UserAuthMethods struct {
	HasPassword   bool
	IdentityCount int64
}

// InsertIdentityInput carries the fields written by InsertTelegramIdentity
// during the link flow.
type InsertIdentityInput struct {
	UserID      [16]byte
	ProviderUID string // string form of TelegramUser.ID
	DisplayName string // first_name + " " + last_name (or first_name only)
	AvatarURL   string // photo_url, may be empty
}

// OAuthLoginTxInput carries the parameters for the existing-user session Tx.
type OAuthLoginTxInput struct {
	UserID    [16]byte
	IPAddress string
	UserAgent string
	NewUser   bool
}

// OAuthRegisterTxInput carries the parameters for the new-user registration Tx.
// Email is always empty for Telegram (D-04).
type OAuthRegisterTxInput struct {
	DisplayName string // may be empty
	ProviderUID string
	AvatarURL   string
	IPAddress   string
	UserAgent   string
}

// OAuthAuditInput carries the parameters for standalone audit log writes in
// link and unlink flows.
type OAuthAuditInput struct {
	UserID    [16]byte
	Event     audit.EventType
	IPAddress string
	UserAgent string
	Metadata  map[string]any
}
```

### 6. `internal/domain/oauth/telegram/requests.go`

HTTP request struct with `json:` tags. The same struct is used for both
`POST /callback` and `POST /link`.

```go
// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram

// telegramCallbackRequest is the JSON body posted by the Telegram Login Widget.
// Used for both POST /callback and POST /link.
//
// All fields except ID, AuthDate, and Hash are optional — Telegram does not
// guarantee their presence.
type telegramCallbackRequest struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
	PhotoURL  string `json:"photo_url"`
	AuthDate  int64  `json:"auth_date"`
	Hash      string `json:"hash"`
}
```

### 7. `internal/domain/oauth/telegram/validators.go`

HMAC verification and auth_date replay guard. These are called by the handler
**before** any service or store call.

```go
// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// VerifyHMAC verifies the HMAC-SHA256 signature on a Telegram Login Widget
// payload.
//
// The verification algorithm (Telegram spec):
//  1. secret_key = SHA256(raw_bytes(botToken))
//  2. data_check_string = alphabetically sorted "key=value" pairs of all
//     received fields except "hash", joined by "\n"
//  3. expected_hash = hex(HMAC_SHA256(secret_key, data_check_string))
//  4. valid = hmac.Equal(expected_hash_bytes, received_hash_bytes)
//
// Security: uses hmac.Equal for constant-time comparison (D-08).
// Returns ErrInvalidTelegramSignature on any mismatch or hex decode error.
func VerifyHMAC(req telegramCallbackRequest, botToken string) error {
	secretKey := sha256.Sum256([]byte(botToken))

	// Build the data_check_string: sorted "key=value" pairs, excluding "hash".
	fields := buildDataCheckFields(req)
	dataCheckString := strings.Join(fields, "\n")

	mac := hmac.New(sha256.New, secretKey[:])
	mac.Write([]byte(dataCheckString))
	expectedMAC := mac.Sum(nil)

	receivedMAC, err := hex.DecodeString(req.Hash)
	if err != nil {
		return ErrInvalidTelegramSignature
	}

	// Security: constant-time comparison prevents timing attacks (D-08).
	if !hmac.Equal(expectedMAC, receivedMAC) {
		return ErrInvalidTelegramSignature
	}
	return nil
}

// CheckAuthDate validates the auth_date field for replay protection.
// Rejects the payload if auth_date is more than 86400 seconds old or more
// than 60 seconds in the future (D-09).
// Returns ErrTelegramAuthDateExpired if the check fails.
func CheckAuthDate(authDate int64) error {
	now := time.Now().Unix()
	age := now - authDate
	// Reject stale payloads (older than 24 hours).
	if age > 86400 {
		return ErrTelegramAuthDateExpired
	}
	// Reject future-dated payloads (clock skew tolerance: 60 seconds).
	if authDate-now > 60 {
		return ErrTelegramAuthDateExpired
	}
	return nil
}

// displayName constructs a display name from first_name and last_name.
// Returns an empty string if both fields are absent (D-02).
func displayName(firstName, lastName string) string {
	firstName = strings.TrimSpace(firstName)
	lastName = strings.TrimSpace(lastName)
	switch {
	case firstName != "" && lastName != "":
		return firstName + " " + lastName
	case firstName != "":
		return firstName
	default:
		return ""
	}
}

// providerUID returns the string representation of a Telegram user ID.
func providerUID(id int64) string {
	return fmt.Sprintf("%d", id)
}

// buildDataCheckFields returns the sorted "key=value" pairs for the
// data_check_string, excluding the "hash" field and any fields with a zero /
// empty value that would not have been transmitted by the widget.
func buildDataCheckFields(req telegramCallbackRequest) []string {
	pairs := []string{
		fmt.Sprintf("auth_date=%d", req.AuthDate),
		fmt.Sprintf("id=%d", req.ID),
	}
	if req.FirstName != "" {
		pairs = append(pairs, "first_name="+req.FirstName)
	}
	if req.LastName != "" {
		pairs = append(pairs, "last_name="+req.LastName)
	}
	if req.Username != "" {
		pairs = append(pairs, "username="+req.Username)
	}
	if req.PhotoURL != "" {
		pairs = append(pairs, "photo_url="+req.PhotoURL)
	}
	sort.Strings(pairs)
	return pairs
}
```

### 8. `internal/domain/oauth/telegram/` — create placeholder skeleton files

The following files must exist for `go vet` to run against the package, even though
their implementations are written in later stages. Create them with just the package
declaration and a compile-time TODO comment:

**`service.go`** (package declaration only — Stage 3 will fill this):
```go
// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram
```

**`store.go`** (package declaration only — Stage 2 will fill this):
```go
// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram
```

**`handler.go`** (package declaration only — Stage 4 will fill this):
```go
// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram
```

**`routes.go`** (package declaration only — Stage 4 will fill this):
```go
// Package telegram handles Telegram Login Widget authentication: callback, link,
// and unlink.
package telegram
```

> **Note:** Do NOT wire `telegram.Routes` into `internal/domain/oauth/routes.go` yet.
> The oauth root assembler import will be added in Stage 4 once `Routes` is implemented.

---

## Done when

```bash
# Confirm no SQL changes needed
# (All required queries are already in sql/queries/oauth.sql — skip make sqlc)

# Config and Deps compile
go build ./internal/config/...
go build ./internal/app/...

# Telegram package compiles and vets cleanly
go vet ./internal/domain/oauth/telegram/...
go build ./internal/domain/oauth/telegram/...
```

No test failures should result — no tests exist yet for this package.

**Stop here. Do not implement store methods, service logic, handlers, or routes.**
**Stage 2 starts in a separate session.**
