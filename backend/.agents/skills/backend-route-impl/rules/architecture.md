# Architecture — Layer Contracts, Import Rules, Wiring

---

## Folder structure

```
internal/
├── server/          # Process boundary: startup, shutdown, root router
├── config/          # All env-var reading. Only place os.Getenv is called
├── app/             # app.Deps struct — leaf package, no domain imports
├── domain/          # Business logic. One sub-package per bounded context
│   ├── auth/        # package auth — root assembler only
│   │   ├── routes.go             # The ONLY file in package auth
│   │   ├── shared/               # package authshared
│   │   └── {feature}/            # One sub-package per feature
│   ├── profile/     # package profile — same pattern
│   ├── oauth/       # package oauth — root assembler only
│   │   ├── routes.go             # Returns *chi.Mux; no AllowContentType (OAuth is browser-redirect)
│   │   ├── shared/               # package oauthshared
│   │   └── {provider}/           # google/, telegram/
│   └── rbac/        # package rbac — root assembler with /owner + /admin sub-routers
│       ├── routes.go             # Returns *chi.Mux; mounts /owner and /admin
│       ├── shared/               # package rbacshared
│       └── {feature}/            # bootstrap/, permissions/, roles/, userroles/
├── platform/        # Shared infrastructure. No business logic
│   ├── token/       # JWT generation, parsing, middleware, context helpers
│   ├── kvstore/     # Generic key-value store
│   ├── ratelimit/   # Token-bucket and backoff rate limiters
│   ├── mailer/      # SMTP delivery and async queue
│   ├── respond/     # JSON, Error, NoContent HTTP response helpers
│   ├── rbac/        # RBAC Checker, Require/ApprovalGate middleware, Perm* constants
│   └── crypto/      # AES-256-GCM for OAuth token encryption
├── db/              # sqlc-generated query code. Never edited by hand
├── audit/           # Typed audit-event constants. No dependencies
└── worker/          # Background job types and purge worker
```

---

## Import direction (reversing any arrow = build violation)

```
cmd/api
  └─► server
        └─► domain/{auth, profile, oauth, rbac}
              ├─► app           (routes.go only — shared runtime deps)
              ├─► platform/*    (any domain package may import)
              ├─► db            (stores only — never handlers or services)
              └─► audit         (stores only — event name constants)
```

**Within any domain (auth, profile, oauth, rbac):**
```
{domain}/routes.go (root assembler)
  ├─► {domain}/shared/
  └─► {domain}/{feature}/    ← ONLY root assembler imports feature packages
        └─► {domain}/shared/  ← features import shared, never each other
```

**rbac domain exception — platform/rbac:**
```
domain/rbac/{feature}/routes.go
  └─► platform/rbac    ← imports permission constants (rbac.Perm*) and Checker type
```
This is NOT a circular import: `platform/rbac` has no dependency on `domain/rbac`.

**Hard rules:**
- `domain` packages never import each other (ADR-010).
- `platform` packages never import `domain` packages.
- `db` imported by domain stores only — never by handlers or services.
- `app` imported by `domain/*/routes.go` only.
- `config` imported by `server` only. Domain packages receive values via `*app.Deps`.

---

## Layer contracts

### Handler (`handler.go`) — HTTP boundary

**May:** import `net/http`, `platform/respond`, `platform/token` (context helpers),
call its service via `Servicer`, set cookies, sign tokens via `platform/token`.

**Must not:** import `pgtype`, `pgxpool`, `pgx`, `internal/db`. Contain business rules.
Call store methods directly.

Defines its own `Servicer` interface (only the methods it calls).

### Service (`service.go`) — business logic boundary

**May:** import `platform/mailer`, `internal/audit` (event constants),
`internal/db` enum types only (e.g. `db.AuthProvider` — never query types).

**Must not:** import `net/http`, `pgtype`, `pgxpool`, `pgx`, `platform/token`,
`config.Config`. Call another feature's service.

Defines `Storer` interface (only the methods it calls).

### Store (`store.go`) — data-access boundary

**May:** import `pgtype`, `pgxpool`, `pgx/v5`, `internal/db`, `internal/audit`.

**Must not:** import `net/http`. Return `pgtype.*`, `pgx.*`, or `db.*` through public methods.
Contain business logic or guard conditions.

---

## Type boundary rules

| Boundary | UUID type | Notes |
|---|---|---|
| Inside store | `pgtype.UUID` | Only here |
| Store ↔ Service | `[16]byte` | Canonical in-memory form |
| Service ↔ Handler | `string` | Standard UUID string form |

`pgtype.*` never exits the store.
`http.*` never enters the service.

```go
// pgtype.UUID → [16]byte (in store)
bytes := row.ID.Bytes

// [16]byte → string (in service)
str := uuid.UUID(someBytes).String()

// [16]byte → pgtype.UUID (in store)
pgUUID := pgtype.UUID{Bytes: b, Valid: true}
```

---

## Wiring model

`routes.go` is the ONLY place where dependencies are constructed:

```
routes.go
  store := NewStore(deps.Pool)
  svc   := NewService(store)
  h     := NewHandler(svc, deps.JWTConfig, deps.SecureCookies)
```

- Service never sees `*pgxpool.Pool`.
- Handler never sees `*Service` — only `Servicer` interface.
- JWT secrets never reach the service.
- `config.Config` never reaches the service or store.

### Routes function signatures

**Domain root assembler** (`{domain}/routes.go`):
```go
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux
```

**Feature sub-package** (`{domain}/{feature}/routes.go`):
```go
func Routes(ctx context.Context, r chi.Router, deps *app.Deps)
// No return value — registers directly on r to avoid chi.Mount panic
```

**rbac domain root assembler** (`rbac/routes.go`) — dual sub-router pattern:
```go
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
    r := chi.NewRouter()
    r.Mount("/owner", ownerRoutes(ctx, deps))  // unauthenticated — bootstrap only
    r.Mount("/admin", adminRoutes(ctx, deps))  // RBAC-gated admin routes
    return r
}
```
Mounted at `/api/v1/` (not `/api/v1/rbac/`) so paths resolve to `/api/v1/owner/...` and `/api/v1/admin/...`.

---

## Package layout rule — one route, one folder

One feature sub-package = one folder. Exceptions:

- **Same resource:** OAuth initiate + callback + link + unlink share one folder.
- **Availability + mutation:** `GET /username/available` + `PATCH /username` share `username/`.
- **Multi-step same resource:** all email-change steps share `email/`.

When unsure — give it its own folder. Splitting later is cheaper than a bloated file.

---

## Background goroutine ownership

Every goroutine started by `routes.go` must respect `ctx.Done()`.

| Goroutine | Owner | Shutdown |
|---|---|---|
| Rate limiter cleanup | `domain/*/routes.go` | `<-ctx.Done()` |
| Backoff limiter cleanup | `domain/*/routes.go` | `<-ctx.Done()` |
| Mail queue workers | `server/router.go` | `queue.Shutdown()` |

A goroutine that ignores `ctx.Done()` is a shutdown bug. No exceptions.

---

## JWT token flow

Services return raw session metadata (SessionID, RefreshJTI, FamilyID as plain
Go types). Handlers call `platform/token` helpers to sign tokens and set cookies.
JWT secrets never reach the service. (ADR-001)

---

## `app.Deps` — available dependencies

See `project-map.md §4` for the full, always-current field list. Key fields:

```go
Pool                *pgxpool.Pool
KVStore             kvstore.Store
Blocklist           kvstore.TokenBlocklist   // may be nil; nil-check before use
Mailer              *mailer.SMTPMailer
MailQueue           *mailer.Queue
JWTAuth             func(http.Handler) http.Handler
SecureCookies       bool
OTPTokenTTL         time.Duration
Encryptor           *crypto.Encryptor        // OAuth tokens only
RBAC                *rbac.Checker            // deps.RBAC.Require(perm) as middleware
BootstrapSecret     string
OAuth               app.OAuthConfig          // GoogleClientID, TelegramBotToken, etc.
```
