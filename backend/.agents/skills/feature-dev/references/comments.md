# Comments — Go Comment Conventions

---

## Core principle

A comment is for the reader, not the writer. Before adding a comment, ask:
**would an experienced Go developer be confused without it?** If no — delete it.

---

## Comment mechanics

Use `//` line comments everywhere. `/* */` block comments are for package-level
doc comments only when the comment must span before a `package` clause with no
closing declaration, or to disable a large swath of code temporarily.

**Blank line before, none between.** A doc comment is separated from whatever
comes above it by a blank line, but there must be **no blank line** between the
doc comment and the declaration it documents. A blank line breaks the
association — godoc will not attach the comment to the declaration.

```go
// Good — comment directly above declaration
// NewStore constructs a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {

// Bad — blank line breaks the association
// NewStore constructs a Store backed by pool.

func NewStore(pool *pgxpool.Pool) *Store {
```

**First sentence is the summary.** Godoc extracts the first sentence for
indexes and hover text. It must be a complete, standalone sentence that makes
sense without the rest of the comment:

```go
// Good — first sentence is self-contained
// CreateUserTx inserts a new user and issues an email verification token.
// Returns ErrEmailTaken if the address is already registered.

// Bad — first sentence is not useful on its own
// This function is used to create a new user.
```

**Code examples** in doc comments use `//` + tab indent, not fenced blocks.
Godoc renders them as preformatted text:

```go
// Routes returns a self-contained chi sub-router for all /auth endpoints.
// Mount at /api/v1/auth in the server router:
//
//	r.Mount("/auth", auth.Routes(ctx, deps))
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
```

---

## Package comments

One doc comment on the `package` declaration. One sentence. Starts with
`Package {name}`. States what the package provides — not how it works.

```go
// Package authshared holds primitives shared by all auth feature sub-packages.
package authshared

// Package profile provides the HTTP handler, service, and store for
// authenticated user profile operations.
package profile
```

---

## Exported identifier comments

Every exported type, function, method, variable, and constant must have a doc
comment. Start with the **exact name**. One sentence. Period at the end.
Add a second sentence only for non-obvious error conditions or side effects.

```go
// UserProfile is the store-layer representation of a user's public profile.
type UserProfile struct { ... }

// NewService constructs a Service with the given store.
func NewService(store Storer) *Service { ... }

// CreateUserTx inserts a new user and issues an email verification token.
// Returns ErrEmailTaken if the address is already registered.
func (s *Store) CreateUserTx(...) (CreatedUser, error)

// ErrUserNotFound is returned when the user record cannot be located.
var ErrUserNotFound = errors.New("user not found")
```

**What to avoid:**
```go
// Bad — restates the type name
// UserProfile is a struct that contains user profile data.

// Bad — implementation detail instead of contract
// GetUserProfile queries the users table by ID and returns the result.
```

---

## Multi-paragraph doc comments

A second sentence (same paragraph) documents non-obvious error conditions or
side effects. A blank line inside the doc comment starts a new paragraph —
use this for longer explanations, not for every function:

```go
// LoginTx validates credentials and creates a persistent session.
// Returns ErrInvalidCredentials if the password does not match.
// Returns ErrAccountLocked if the account is admin-locked.
//
// Timing invariant: the password hash check always runs even when the user is
// not found, to prevent timing-based email enumeration.
func (s *Service) LoginTx(ctx context.Context, in LoginInput) (LoggedInSession, error)
```

Keep it concise — if the explanation exceeds six lines, the function is
probably doing too much.

---

## Interface and type comments

**Interfaces** get a one-sentence comment starting with the type name.
Describe what the interface *represents*, not what implementors must do:

```go
// Storer is the data-access contract for the login feature.
type Storer interface {
    GetUserForLoginTx(ctx context.Context, in GetUserForLoginInput) (UserForLogin, error)
}

// Servicer is the business-logic contract for the login feature.
type Servicer interface {
    LoginTx(ctx context.Context, in LoginInput) (LoggedInSession, error)
}
```

**Request and response types** — same rule: one sentence, starts with the name:

```go
// LoginRequest is the HTTP request body for POST /auth/login.
type LoginRequest struct {
    Identifier string `json:"identifier"`
    Password   string `json:"password"`
}

// LoggedInSession is the response body for a successful login.
type LoggedInSession struct {
    AccessToken string `json:"access_token"`
    ExpiresIn   int    `json:"expires_in"`
}
```

**Model types** (service-layer Input/Result) follow the same pattern:

```go
// LoginInput is the service-layer input for authenticating a user.
type LoginInput struct {
    Identifier string
    Password   string
    IPAddress  string
}

// LoggedInSession is the service-layer result for a successful login.
type LoggedInSession struct { ... }
```

---

## Unexported identifiers

No doc comments unless the logic would confuse a competent reader. Constants
that encode non-obvious constraints do warrant a comment:

```go
// maxUserAgentBytes is the maximum number of bytes stored in the user_agent column.
const maxUserAgentBytes = 512
```

---

## Grouped const blocks

Const groups with a shared concept get a single comment **above the group**.
Do not comment each constant individually unless its meaning is non-obvious
from the name:

```go
// Event types for the audit log. Every value must appear in AllEvents().
const (
    EventUserRegistered    EventType = "user_registered"
    EventEmailVerified     EventType = "email_verified"
    EventPasswordChanged   EventType = "password_changed"
    EventSessionCreated    EventType = "session_created"
)
```

For iota groups, document the type and its zero value:

```go
// AccessType controls how a permission grant is enforced.
// The zero value is not valid; use AccessTypeDirect for unconditional grants.
type AccessType int

const (
    AccessTypeDirect      AccessType = iota + 1
    AccessTypeConditional
    AccessTypeRequest
    AccessTypeDenied
)
```

Individual constants only get their own comment when the name alone is
insufficient:

```go
const (
    // maxOTPAttempts is the number of wrong guesses before a token is locked.
    // Matches the value in sql/seeds/002_permissions.sql.
    maxOTPAttempts = 5
)
```

---

## Named return parameters

Use named return parameters when the names serve as documentation — i.e.
when two return values of the same type would be ambiguous at the call site:

```go
// Good — names clarify which int is which
func nextToken(b []byte, pos int) (value, nextPos int)

// Unnecessary — context is obvious from a single return
func (s *Store) CountSessions(ctx context.Context) (n int, err error)  // bad
func (s *Store) CountSessions(ctx context.Context) (int, error)        // good
```

Do **not** use named returns just to enable a bare `return`. Bare returns
make it harder to trace what is being returned. The only exception is short
functions where the naked return is idiomatic (e.g. `io.ReadFull`-style
wrappers under ~10 lines).

---

## Deprecation

Mark deprecated identifiers with a `// Deprecated:` paragraph in the doc
comment. This is recognised by `gopls`, `go vet`, and IDEs:

```go
// CheckOTPTokenLegacy verifies an OTP token using the old 4-digit format.
//
// Deprecated: Use CheckOTPToken which requires 6-digit codes. This function
// will be removed once all clients have migrated.
func CheckOTPTokenLegacy(token, code string) error {
```

The `// Deprecated:` line must be its own paragraph (preceded by a blank
comment line). Do not add this annotation without also filing a tracked issue
or adding a `// TODO(#NNN):` note.

---

## Inline comments — why, not what

Explain **why**, never **what**. The code shows what.

```go
// Bad
cost := bcrypt.DefaultCost // set cost to default

// Good
cost := bcrypt.DefaultCost // minimum 12 in production; lowered in tests via SetBcryptCostForTest
```

```go
// Bad
if session.UserID.Bytes != ownerUserID { // check ownership

// Good — explains WHY NotFound instead of Forbidden
// Returning NotFound (not Forbidden) prevents IDOR — caller cannot infer
// that a session with this ID exists but belongs to someone else.
if session.UserID.Bytes != ownerUserID {
```

---

## Security annotations

Any line encoding a non-obvious security invariant gets a `// Security:` comment
immediately above it.

```go
// Security: detach from the request context so a client-timed disconnect
// cannot abort the counter increment and grant unlimited OTP attempts.
if err := s.store.IncrementAttemptsTx(context.WithoutCancel(ctx), in); err != nil {

// Security: cookie flags are required. Removing Secure or relaxing SameSite
// makes token theft feasible over HTTP.
http.SetCookie(w, &http.Cookie{
    HttpOnly: true,
    Secure:   h.secureCookies,
    SameSite: http.SameSiteStrictMode,
})
```

Greppable: `grep -rn "// Security:" ./internal/`

---

## Timing invariant annotations

Doc comment on the method (explains invariant) + inline comment at the
dummy-hash call site:

```go
// UpdatePasswordHash verifies the caller's current password and replaces it.
//
// Timing invariant: CheckPassword always runs, even if the user is not found.
func (s *Service) UpdatePasswordHash(ctx context.Context, in ChangePasswordInput) error {

    // Timing invariant: always run CheckPassword, even on no-rows, to prevent
    // timing-based email enumeration (§3.7).
    if lookupErr == nil {
        pwHash = creds.PasswordHash
    } else {
        pwHash = authshared.GetDummyPasswordHash()
    }
```

---

## Numbered steps in Tx methods

`*Tx` methods with more than two DB calls use numbered step comments. The doc
comment summarises the steps in prose — do not duplicate the numbered list.

```go
// LoginTx runs post-authentication persistence: creates a session row, issues
// a refresh token, stamps last_login_at, and writes the audit log.
func (s *Store) LoginTx(ctx context.Context, in LoginTxInput) (LoggedInSession, error) {
    h, err := s.BeginOrBind(ctx)

    // 1. Open a session row.
    sessionRow, err := h.Q.CreateUserSession(ctx, ...)

    // 2. Issue a root refresh token.
    tokenRow, err := h.Q.CreateRefreshToken(ctx, ...)

    // 3. Stamp last_login_at.
    if err := h.Q.UpdateLastLoginAt(ctx, userPgUUID); err != nil {

    // 4. Audit log.
    if err := h.Q.InsertAuditLog(ctx, ...); err != nil {
}
```

---

## Blocking method annotations

Any method that blocks until cancellation must say so and show the goroutine pattern:

```go
// StartCleanup evicts expired entries on each tick. It blocks until ctx is cancelled.
// Run it in a goroutine:
//
//	go store.StartCleanup(ctx)
func (s *InMemoryStore) StartCleanup(ctx context.Context) { ... }
```

Note: code examples use `//` + tab indent (godoc format), not fenced blocks.

---

## Compile-time interface checks

One brief inline comment:

```go
// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// compile-time interface check.
var _ db.Querier = (*QuerierProxy)(nil)
```

Use exactly these phrasings. Not "static assertion" or "type assertion check".

---

## Section separators

Use when a file has distinct logical groups large enough to get lost in:

```go
// ── Cross-feature sentinel errors ─────────────────────────────────────────────

// ── Typed errors ───────────────────────────────────────────────────────────────
```

Use `──` (U+2500) + space + title + space + `─` repeated to ~80 chars.
Use sparingly — if a file needs more than three sections, consider splitting.

**Do not** use `//===`, `//---`, `//***`.

---

## FakeStorer, FakeServicer, QuerierProxy comments

**Package doc:**
```go
// Package authsharedtest provides test-only helpers shared across all auth
// feature sub-packages. It must never be imported by production code.
package authsharedtest
```

**FakeStorer struct:**
```go
// ProfileFakeStorer is a hand-written implementation of profile.Storer for
// service unit tests. Each method delegates to its Fn field if non-nil;
// otherwise returns the zero value and nil error.
type ProfileFakeStorer struct { ... }
```

**Each method:**
```go
// GetUserProfile delegates to GetUserProfileFn if set.
func (f *ProfileFakeStorer) GetUserProfile(...) (...) {
```

**QuerierProxy:**
```go
// ErrProxy is the sentinel error returned by QuerierProxy when a Fail* flag is set.
var ErrProxy = errors.New("querier_proxy: injected error")

// QuerierProxy implements db.Querier, delegating each call to Base unless the
// corresponding Fail* flag is set — in that case ErrProxy is returned.
type QuerierProxy struct { ... }
```

Feature groups in `querier_proxy.go` use section separators:
```go
// ── login ────────────────────────────────────────────────────────────────────
FailGetUserForLogin bool

// ── profile ──────────────────────────────────────────────────────────────────
FailGetUserProfile bool
```

---

## Routes comments

Domain root assembler:
```go
// Routes returns a self-contained chi sub-router for all /auth endpoints.
// Mount at /api/v1/auth in the server router:
//
//	r.Mount("/auth", auth.Routes(ctx, deps))
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux { ... }
```

Feature sub-package (rate-limited):
```go
// Routes registers the login endpoint on r.
// Call from the auth root assembler:
//
//	login.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST /login: 5 req / 15 min per IP
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) { ... }
```

RBAC-gated admin feature sub-package (no rate limiter):
```go
// Routes registers GET /rbac/roles and POST /rbac/roles on r.
// Called from adminRoutes in internal/domain/rbac/routes.go:
//
//	roles.Routes(ctx, r, deps)
//
// Both routes require a valid JWT and the rbac:read / rbac:manage permission.
// No additional rate limiter — admin routes are RBAC-gated.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) { ... }
```

RBAC root assembler:
```go
// Routes builds and returns the full rbac chi.Mux, mounting the /owner and
// /admin sub-routers internally. Callers in server/routes.go mount the result
// at the api root:
//
//	r.Mount("/", rbac.Routes(ctx, deps))
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux { ... }
```

Rate limiter inline comments:
```go
// 5 req / 15 min per IP — deters credential stuffing.
// rate = 5 / (15 * 60) = 0.00556 tokens/sec.
ipLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "lgn:ip:", 5.0/(15*60), 5, 15*time.Minute)
```

---

## What to omit

- No doc comments on unexported types/functions unless logic would confuse a competent reader.
- No restating comments — if the code says `cost := bcrypt.DefaultCost`, do not write `// set cost to default`.
- No commented-out code — delete it; Git keeps history.
- No bare `// TODO` without a tracked issue reference:
  ```go
  // TODO(#142): replace with a circuit-breaker.  ← good
  // TODO: fix this later                          ← banned
  ```
- No `// nolint` without a reason:
  ```go
  //nolint:exhaustruct // optional fields have safe zero values
  ```
- No separator lines using `//---`, `//===`, `//***`.

---

## Comment checklist (use in code review)

**Package comment:**
- [ ] Present on the package declaration in the primary file
- [ ] Starts with `Package {name}`
- [ ] One or two sentences; no design rationale

**Exported identifiers:**
- [ ] Every exported type/function/method/var/const has a doc comment
- [ ] Doc comment starts with the exact identifier name
- [ ] Non-obvious error returns documented
- [ ] Non-obvious side effects documented

**Inline comments:**
- [ ] Explain *why*, not *what*
- [ ] Security-critical lines have `// Security:` comment
- [ ] Timing invariant dummy-hash call sites have inline comment
- [ ] Multi-step Tx methods use numbered step comments

**Types and interfaces:**
- [ ] Every `Storer` and `Servicer` interface has a one-sentence doc comment
- [ ] Every request/response and Input/Result type has a doc comment
- [ ] Const groups have a single comment above the group, not per-constant
- [ ] Iota groups document the zero value on the type comment

**Named returns and deprecation:**
- [ ] Named return parameters used only when they clarify ambiguous same-type returns
- [ ] No bare `return` in functions over ~10 lines
- [ ] Deprecated identifiers use `// Deprecated:` as a separate paragraph
- [ ] Every `// Deprecated:` has a corresponding `// TODO(#NNN):` or filed issue

**Form:**
- [ ] No blank line between doc comment and its declaration
- [ ] No commented-out code
- [ ] No bare `// TODO` / `// FIXME`
- [ ] Section separators use `// ──` style only
- [ ] Compile-time checks use standard one-liner comment
- [ ] Code examples use `//` + tab indent, not fenced blocks
