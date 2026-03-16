# Project Rules

**Stack:** Go · PostgreSQL 18 · chi · pgx v5 · sqlc · goose  
**Last updated:** 2026-03-11

> **This file contains only conventions that apply globally across all packages.**  
> Auth-specific patterns, code flow traces, and auth ADRs live in [`docs/rules/auth.md`](rules/auth.md).  
> Every new domain package gets its own `docs/rules/{domain}.md` following the same pattern.

This is the single source of truth for how this codebase is structured, how
code flows through it, how it must be written, and why non-obvious decisions
were made. Read it before building a new feature. Update it when you make a
decision that future contributors will find surprising.

---

## Table of Contents

1. [Folder Structure & Code Flow](#1-folder-structure--code-flow)
   - 1.1 [Top-Level Layout](#11-top-level-layout)
   - 1.2 [Domain Folder Layout](#12-domain-folder-layout)
   - 1.3 [Platform Folder Layout](#13-platform-folder-layout)
   - 1.4 [Code Flow: HTTP Request → Response](#14-code-flow-http-request--response)
   - 1.5 [Wiring Flow: How a Feature Gets Built](#15-wiring-flow-how-a-feature-gets-built)
   - 1.6 [Type Transformation at Each Boundary](#16-type-transformation-at-each-boundary)
   - 1.7 [Import Direction Rules](#17-import-direction-rules)
   - 1.8 [File Responsibility Quick Reference](#18-file-responsibility-quick-reference)
   - 1.9 [Adding a New Feature: Step-by-Step](#19-adding-a-new-feature-step-by-step)
   - 1.10 [Adding a New Domain: Step-by-Step](#110-adding-a-new-domain-step-by-step)

2. [Architecture Reference](#2-architecture-reference)
   - 2.1 [Dependency Graph](#21-dependency-graph)
   - 2.2 [Layer Contracts](#22-layer-contracts)
   - 2.3 [Layer Interface Summary](#23-layer-interface-summary)
   - 2.4 [Wiring Model](#24-wiring-model)
   - 2.5 [JWT Token Flow](#25-jwt-token-flow)
   - 2.6 [Background Goroutine Ownership](#26-background-goroutine-ownership)
   - 2.7 [Database Access Model](#27-database-access-model)
   - 2.8 [Redis / In-Memory Strategy](#28-redis--in-memory-strategy)

3. [Conventions](#3-conventions)
   - 3.1 [File Layout](#31-file-layout)
   - 3.2 [Package Naming](#32-package-naming)
   - 3.3 [Layer Type Rules](#33-layer-type-rules)
   - 3.4 [Error Handling](#34-error-handling)
   - 3.5 [Audit Log](#35-audit-log)
   - 3.6 [context.WithoutCancel Rule](#36-contextwithoutcancel-rule)
   - 3.7 [Anti-Enumeration Timing](#37-anti-enumeration-timing)
   - 3.8 [Testing](#38-testing)
   - 3.9 [SQL Conventions](#39-sql-conventions) — includes No Raw SQL in Go rule
   - 3.10 [HTTP Conventions](#310-http-conventions)
   - 3.11 [Naming Quick Reference](#311-naming-quick-reference)
   - 3.12 [Configuration](#312-configuration)
   - 3.13 [Sub-Package Split Checklist](#313-sub-package-split-checklist)
   - 3.14 [Mandatory Three-File Sync Rules](#314-mandatory-three-file-sync-rules)
   - 3.15 [Mailer Template Convention](#315-mailer-template-convention)

4. [Go Comment Conventions](#4-go-comment-conventions)
   - 4.1 [The Core Principle](#41-the-core-principle)
   - 4.2 [Package Comments](#42-package-comments)
   - 4.3 [Exported Identifier Comments](#43-exported-identifier-comments)
   - 4.4 [Unexported Identifiers](#44-unexported-identifiers)
   - 4.5 [Inline Comments — Why, Not What](#45-inline-comments--why-not-what)
   - 4.6 [Security Annotations](#46-security-annotations)
   - 4.7 [Timing Invariant Annotations](#47-timing-invariant-annotations)
   - 4.8 [Numbered Steps in Transaction Methods](#48-numbered-steps-in-transaction-methods)
   - 4.9 [Blocking Method Annotations](#49-blocking-method-annotations)
   - 4.10 [Compile-Time Interface Checks](#410-compile-time-interface-checks)
   - 4.11 [Section Separators](#411-section-separators)
   - 4.12 [FakeStorer and QuerierProxy Comments](#412-fakestorer-and-querierproxy-comments)
   - 4.13 [Routes Comments](#413-routes-comments)
   - 4.14 [What to Omit](#414-what-to-omit)
   - 4.15 [Comment Checklist](#415-comment-checklist)

5. [Architecture Decisions (ADRs)](#5-architecture-decisions-adrs)
   - [ADR-001 — JWT signing belongs in the handler, not the service](#adr-001--jwt-signing-belongs-in-the-handler-not-the-service)
   - [ADR-002 — Three-layer architecture with strict boundary contracts](#adr-002--three-layer-architecture-with-strict-boundary-contracts)
   - [ADR-003 — The txBound / WithQuerier pattern for test isolation](#adr-003--the-txbound--withquerier-pattern-for-test-isolation)
   - [ADR-004 — context.WithoutCancel for security-critical writes](#adr-004--contextwithoutcancel-for-security-critical-writes)
   - [ADR-005 — OTP consumption uses a checkFn closure to avoid deadlocks](#adr-005--otp-consumption-uses-a-checkfn-closure-to-avoid-deadlocks)
   - [ADR-006 — Anti-enumeration: uniform 202 + timing equalization](#adr-006--anti-enumeration-uniform-202--timing-equalization)
   - [ADR-007 — One Storer interface per domain, defined in service.go](#adr-007--one-storer-interface-per-domain-defined-in-servicego)
   - [ADR-008 — Audit event constants in internal/audit, not inline strings](#adr-008--audit-event-constants-in-internalaudit-not-inline-strings)
   - [ADR-009 — Shared KV store instance for all rate limiters in a domain](#adr-009--shared-kv-store-instance-for-all-rate-limiters-in-a-domain)
   - [ADR-010 — Domain packages never import each other](#adr-010--domain-packages-never-import-each-other)
   - [ADR-011 — Token family revocation on reuse detection](#adr-011--token-family-revocation-on-reuse-detection)
   - [ADR-012 — models.go contains only service-layer I/O types](#adr-012--modelsgo-contains-only-service-layer-io-types)

---

## 1. Folder Structure & Code Flow

Read this section first when building a new feature. It tells you where every
file goes and in what order code executes.

### 1.1 Top-Level Layout

```
backend/
├── cmd/api/main.go          # Entry point. Calls server.Run(). Nothing else.
├── internal/                # All application code. Never imported from outside.
│   ├── server/              # Process boundary: startup, shutdown, root router.
│   ├── config/              # All env-var reading. The only place os.Getenv is called.
│   ├── domain/              # Business logic. One sub-package per bounded context.
│   ├── platform/            # Shared infrastructure. No business logic.
│   ├── db/                  # sqlc-generated query code. Never edited by hand.
│   └── audit/               # Typed audit-event constants. No dependencies.
├── sql/                     # Raw SQL: schema migrations, sqlc query files.
├── docs/                    # Architecture, rules, planning documents.
├── make/                    # Makefile fragments.
├── Makefile
├── Dockerfile
├── docker-compose.yml
└── sqlc.yaml
```

**Rules:**
- Nothing outside `internal/` may import anything inside `internal/`.
- `cmd/api/main.go` contains only a call to `server.Run()`. No logic, no flags, no env reads.
- `docs/` is for humans only. No Go file imports anything from `docs/`.

---

### 1.2 Domain Folder Layout

Every bounded context lives under `internal/domain/{name}/`. The auth domain
is currently the only context; future contexts (`rbac`, `catalog`, `approvals`)
follow the same layout.

```
internal/domain/auth/
├── routes.go                # Root assembler. The ONLY file in package auth.
│                            # Mounts every feature sub-router. Imports every
│                            # feature sub-package. Signature:
│                            # Routes(ctx context.Context, deps *app.Deps) *chi.Mux
│
├── shared/                  # package authshared — primitives shared across features.
│   ├── errors.go            # All cross-feature sentinel errors.
│   ├── models.go            # Shared types (VerificationToken, TokenResult, etc.)
│   ├── otp.go               # OTP generation and verification helpers.
│   ├── password.go          # bcrypt helpers, dummy hash.
│   ├── store.go             # BaseStore: pool, BeginOrBind, conversion helpers.
│   └── validators.go        # ValidatePassword and other shared validators.
│
└── {feature}/               # One sub-package per feature (login, register, etc.)
    ├── handler.go           # HTTP layer. Defines Servicer interface.
    ├── service.go           # Business logic. Defines Storer interface.
    ├── store.go             # Data access. Implements Storer.
    ├── routes.go            # Wiring only. Exports Routes(ctx, deps).
    ├── models.go            # Service-layer I/O structs. No json tags.
    ├── requests.go          # HTTP request/response structs. All json tags.
    ├── errors.go            # Feature-exclusive sentinel errors (omit if none).
    ├── validators.go        # Feature-exclusive validators (omit if none).
    ├── handler_test.go      # Handler unit tests.
    ├── service_test.go      # Service unit tests.
    └── store_test.go        # TestMain, testPool, txStores, seed helpers, and all
                             # store integration tests (//go:build integration_test).

Feature sub-packages do **not** contain a `testutil/` sub-folder. All test
helpers — `FakeStorer` implementations, `QuerierProxy`, pool helpers, builders
— live in `internal/domain/auth/shared/testutil/` (package `authsharedtest`)
and are imported from there. This eliminates duplication and keeps every
feature's `Storer` fake in one place.
```

**Feature sub-packages** are listed in [`docs/rules/auth.md §2.1`](rules/auth.md#21-feature-sub-packages) alongside their endpoints, shared-package contents, and testutil file inventory.

---

### 1.3 Platform Folder Layout

`internal/platform/` holds infrastructure packages that any domain may import.
They contain zero business logic.

```
internal/platform/
├── token/       # JWT generation, parsing, middleware, context helpers.
├── kvstore/     # Generic key-value store (rate limiters, token blocklist).
├── ratelimit/   # Token-bucket and backoff rate limiters.
├── mailer/      # SMTP delivery and async queue.
├── respond/     # JSON, Error, NoContent HTTP response helpers.
└── crypto/      # AES-256-GCM for OAuth token encryption.
```

**Rules:**
- Platform packages may be imported by any domain package.
- Platform packages must never import domain packages.
- `platform/token` is not named `jwt` to avoid collision with the JWT library import.
- `platform/kvstore` is not named `store` to avoid collision with domain store structs.

---

### 1.4 Code Flow: HTTP Request → Response

Every feature follows this path — only the names change:

```
HTTP Client
    │  POST /api/v1/{domain}/{feature}
    ▼
server/router.go                    ← mounts all domain sub-routers
    │  r.Mount("/api/v1/{domain}", {domain}.Routes(...))
    ▼
domain/{name}/routes.go             ← root assembler: applies global middleware,
    │                                  calls each feature's Routes(ctx, r, deps)
    ▼
domain/{name}/{feature}/routes.go   ← feature wiring
    │  store := NewStore(deps.Pool)
    │  svc   := NewService(store)
    │  h     := NewHandler(svc, deps.*)
    │  r.With(limiter.Limit).Post("/{feature}", h.Method)
    ▼
{feature}/handler.go  h.Method()    ← HTTP boundary
    │  1. Cap body: http.MaxBytesReader
    │  2. respond.DecodeJSON[{feature}Request](w, r)
    │  3. validateAndNormalise(&req)
    │  4. svc.Method(ctx, Input{...})
    │  5. On success: respond.JSON / respond.NoContent / set cookie
    │  6. On error: switch on sentinels → HTTP status codes
    ▼
{feature}/service.go  s.Method()    ← business logic boundary
    │  business rules, guard ordering, side-effects via store
    ▼
{feature}/store.go                  ← data access boundary
    │  maps plain Go types ↔ pgtype.*
    │  calls db.Querier methods; never writes raw SQL
    ▼
internal/db/ (sqlc-generated)
    ▼
PostgreSQL
```

See [`docs/rules/auth.md §3`](rules/auth.md#3-code-flow-traces) for the
complete auth-domain trace including login guard ordering, OTP closure
pattern, and token rotation.

---

### 1.5 Wiring Flow: How a Feature Gets Built

`routes.go` is the only place where a feature's dependencies are constructed.
No struct builds its own dependencies. The pattern is always:

```
*app.Deps (passed down from the root assembler)
    │
    ├── store := NewStore(deps.Pool)           ← store gets the pool
    ├── svc   := NewService(store)             ← service gets the store
    └── h     := NewHandler(svc, deps.*)       ← handler gets the service + config primitives
```

This means:
- The service never sees `*pgxpool.Pool` — only the `Storer` interface.
- The handler never sees `*Service` — only the `Servicer` interface.
- JWT secrets never reach the service — only the handler holds them via `deps.JWTConfig`.
- `config.Config` never reaches the service or store — only primitive values are injected via `deps`.

---

### 1.6 Type Transformation at Each Boundary

As data moves through the layers, its type changes at each boundary. Crossing
a boundary with the wrong type is a build violation.

```
PostgreSQL row (sqlc-generated)
    pgtype.UUID        → [16]byte          (store: UUIDToBytes / UUIDToPgtypeUUID)
    pgtype.Text        → string            (store: .String field)
    pgtype.Timestamptz → time.Time         (store: .Time.UTC())
    pgtype.Bool        → bool
         │
         ▼  store boundary
Plain Go structs (models.go)
    [16]byte   UUIDs
    string     text fields
    time.Time  timestamps
    bool       flags
         │
         ▼  service boundary
Plain Go structs (models.go / requests.go)
    string     UUIDs  (uuid.UUID([16]byte).String())
    string     text fields
    time.Time  timestamps
         │
         ▼  handler boundary
HTTP response (JSON)
    string  UUIDs
    string  tokens
    int     expires_in seconds
```

**The rule in one line:** `pgtype.*` never exits the store. `http.*` never
enters the service. `[16]byte` UUIDs live between store and service.
`string` UUIDs live between service and handler.

---

### 1.7 Import Direction Rules

Arrows show the allowed direction. Reversing any arrow is a build violation.

```
cmd/api
  └─► server
        └─► domain/{auth, rbac, catalog, approvals}
              ├─► app         (routes.go only — shared runtime dependencies)
              ├─► platform/{token, kvstore, ratelimit, mailer, respond, crypto}
              ├─► db          (stores only — never handlers or services)
              ├─► audit       (stores only — event name constants)
              └─► config      (server only)
```

**Within the auth domain:**

```
auth/routes.go (root assembler)
  ├─► auth/shared/
  └─► auth/{login, register, session, ...}  ← ONLY the root assembler imports features
        └─► auth/shared/                    ← features import shared, never each other
```

A feature package that imports another feature package is a build violation.
`auth/shared/` must never import any feature package.

---

### 1.8 File Responsibility Quick Reference

When adding code, this table tells you which file it belongs in:

| What you're adding | File |
|---|---|
| A new HTTP endpoint handler method | `handler.go` |
| A new business rule or guard | `service.go` |
| A new DB query call | `store.go` |
| Wiring a new dependency, route, or middleware | `routes.go` |
| A new service I/O struct (no json tags) | `models.go` |
| A new HTTP request or response struct (json tags) | `requests.go` |
| A new sentinel error or typed error | `errors.go` |
| A new input validation function | `validators.go` |
| A shared helper used by more than one feature in the same domain | `internal/domain/{name}/shared/` |
| A new infrastructure concern (caching, mail, etc.) | `internal/platform/{name}/` |
| A new audit event name | `internal/audit/audit.go` |
| A new production SQL query | `sql/queries/{domain}.sql` (append to the relevant section) |
| A new test-only SQL query | `sql/queries_test/{domain}_test.sql` (append at the end) — **never** use raw SQL strings or `pool.Exec`/`tx.QueryRow` in test files |
| A new env variable | `internal/config/config.go` only |
| A new transactional email type | `internal/platform/mailer/templates/{name}.go` + `registry.go` |

**Files that are banned by name:** `helpers.go`, `utils.go`, `common.go`.
If a file needs one of these names, the code belongs somewhere more specific.

---

### 1.9 Adding a New Feature: Step-by-Step

Follow this order when building a new feature inside an existing domain:

1. **SQL first.** Append the query to `sql/queries/{domain}.sql` (production queries) or `sql/queries_test/{domain}_test.sql` (test-only helper queries). Run `make sqlc` to regenerate `internal/db/`.
2. **Audit event.** Add a constant to `internal/audit/audit.go`.
3. **Models.** Define `{Operation}Input` and `{Operation}Result` structs in `models.go`. No `json` tags. No `pgtype`.
4. **Requests.** Define the HTTP request/response struct in `requests.go` with `json` tags.
5. **Store.** Implement the store method in `store.go`. Map all `pgtype.*` to plain Go types before returning. Add the method to the `Storer` interface in `service.go`.
6. **Service.** Implement the business logic in `service.go`. Add the method to the `Servicer` interface in `handler.go`.
7. **Handler.** Implement the HTTP method in `handler.go`. Map domain errors to HTTP status codes in a `switch` statement. Sign JWTs here if needed.
8. **Routes.** Register the route in `routes.go`. Apply rate-limiter middleware here, not in the handler.
9. **Tests.** Write service unit tests (using `FakeStorer`) and store integration tests (using `QuerierProxy`). Update `fake_storer.go` and `querier_proxy.go` for the new store method.
10. **Root assembler.** If the feature is a new sub-package (not just a new method on an existing one), mount it in `domain/{name}/routes.go`.

**Always work bottom-up:** SQL → store → service → handler → routes. Building
top-down leads to interfaces and return types that don't match the actual data.

---

### 1.10 Adding a New Domain: Step-by-Step

When starting an entirely new bounded context (e.g. `catalog`):

1. **Create `docs/rules/{name}.md`** using [`docs/rules/_template.md`](rules/_template.md) as the starting point. Fill in the feature table and any domain-specific decisions before writing any Go. This file is the contract for every feature in the domain.
2. Create `internal/domain/{name}/` with a `routes.go` (package `{name}`).
3. Create `internal/domain/{name}/shared/` (package `{name}shared`) with at minimum `errors.go`, `store.go` (BaseStore), and any validators or helpers shared across features.
4. Create `internal/domain/{name}/shared/testutil/` (package `{name}sharedtest`) with `fake_storer.go`, `fake_servicer.go`, `querier_proxy.go`, and `builders.go`. Populate them as the first feature is built — never leave the package empty.
5. Follow the same feature sub-package layout as `auth` for every feature (see §1.2).
6. Mount the domain router in `internal/server/router.go`.
7. Create `sql/queries/{name}.sql` for its production queries and `sql/queries_test/{name}_test.sql` for test-only helper queries.
8. The new domain must never import any other domain. If shared data is needed, resolve it via the DB layer or an injected interface (see ADR-010).

**Always write `docs/rules/{name}.md` first.** The rules file forces you to identify your features, their HTTP surface, their shared primitives, and their key decisions before any code exists. Domains built without this step accumulate undocumented deviations that are expensive to untangle.

---

## 2. Architecture Reference

### 2.1 Dependency Graph

Arrows show the direction of allowed imports. Reversing any arrow is a build violation.

```
cmd/api
  │
  └─► server
        │
        └─► domain/{auth, rbac, catalog, approvals}
              │
              └─► app             (shared runtime deps; imported by routes.go only)
              │
              └─► platform/{token, kvstore, ratelimit, mailer, respond, crypto}
              │
              └─► db              (sqlc-generated; all domains share it)
              │
              └─► audit           (typed event constants; no deps)
```

**Hard rules:**
- `domain` packages never import each other.
- `platform` packages never import `domain` packages.
- `db` is imported by domain stores only — never by handlers or services.
- `app` is imported by `domain/*/routes.go` only — never by handlers, services, or stores.
- `config` is imported by `server` only — domain packages receive all values via `*app.Deps`.

**Auth sub-package import rules:**

```
auth/shared/         → db, pgxpool, pgx, pgtype, audit, uuid, bcrypt, encoding/json
                       Never imports any auth feature package.

auth/{feature}/      → auth/shared/, db, platform/*, chi, app
                       Never imports another auth feature package.
                       routes.go imports app for *app.Deps and platform/ratelimit
                       for constructing rate limiters locally.

auth/ (root)         → all feature packages, auth/shared/, platform/*, app, chi
                       The ONLY location that imports all feature packages simultaneously.
```

---

### 2.2 Layer Contracts

Every domain package contains three layers with absolute contracts.

#### Handler layer (`handler.go`)

The handler is the HTTP boundary. It translates HTTP into domain types and domain results into HTTP responses.

**May:**
- Import `net/http`, `encoding/json`, `platform/respond`, `platform/token` (context helpers)
- Call its own service methods
- Read from `r.Context()` to extract authenticated identity
- Set cookies and response headers
- Call `platform/token.GenerateAccessToken` and `GenerateRefreshToken` to sign tokens

**Must not:**
- Import `pgtype`, `pgxpool`, `pgx`, or `internal/db`
- Contain `if/switch` chains that implement business rules
- Call store methods directly

`handler.go` defines its own `Servicer` interface listing only the service methods the handler calls:

```go
// Servicer is the subset of the service that the handler requires.
// *Service satisfies this interface; tests may supply a fake implementation.
type Servicer interface {
    GetUserProfile(ctx context.Context, userID string) (UserProfile, error)
    // ... only the methods this handler calls
}
```

#### Service layer (`service.go`)

The service is the business logic boundary. It contains all rules, guard ordering, and invariants.

**May:**
- Import `platform/mailer` (to send emails as a side-effect of business operations)
- Import `internal/audit` (for event name constants)
- Import `internal/db` enum types only (e.g. `db.AuthProvider`) — never query types
- Call its own store methods through the `Storer` interface

**Must not:**
- Import `net/http`, `encoding/json`, or any HTTP type
- Import `pgtype`, `pgxpool`, `pgx`
- Import `platform/token` — services never sign JWTs; that is the handler's job
- Import `config.Config` — configuration values are injected as primitives in the constructor

#### Store layer (`store.go`)

The store is the data-access boundary. It translates between domain types and the database.

**May:**
- Import `pgtype`, `pgxpool`, `pgx/v5`, `internal/db`
- Open and commit transactions
- Call `internal/audit` event constants in `InsertAuditLog` calls

**Must not:**
- Import `net/http`
- Return any `pgtype.*`, `pgx.*`, or `db.*` type through its public methods
- Contain business logic or guard conditions

Every feature's `store.go` embeds `authshared.BaseStore` and must provide a
`WithQuerier(q db.Querier) *Store` method for test transaction isolation.
The compile-time check `var _ Storer = (*Store)(nil)` lives in `store.go`.

---

### 2.3 Layer Interface Summary

| Layer | Receives | Returns |
|---|---|---|
| Handler | `*http.Request` | HTTP response via `http.ResponseWriter` |
| Service | Plain Go structs (`*Input` types) | Plain Go structs (`*Result` types) or error |
| Store | Plain Go structs (`*Input` types), `[16]byte` UUIDs, `string`, `time.Time` | Plain Go structs, `[16]byte`, `string`, `time.Time` |

`pgtype.*` never appears above the store boundary. `http.*` never appears below the handler boundary.

---

### 2.4 Wiring Model

Dependencies flow inward through constructor injection. No global state.

```
auth.Routes(ctx, deps)                              ← root assembler
  │
  ├─ profile.Routes(ctx, deps)
  │    └─ store := profile.NewStore(deps.Pool)
  │       svc   := profile.NewService(store)
  │       h     := profile.NewHandler(svc, deps.JWTConfig, deps.SecureCookies)
  │
  └─ register.Routes(ctx, deps)
       └─ ...
```

**Rules:**
- `routes.go` is the only place where a domain's dependencies are constructed and wired together.
- The service does not construct its own store. The handler does not construct its own service.
- JWT secrets are never stored on the service. They are passed by `routes.go` to the handler via `deps.JWTConfig`.
- The mail queue lifetime is owned by `server/router.go`.
- Domain-level root assemblers export two symbols: `Mount` and `Routes`. Feature sub-packages export only `Routes`.
- **Domain-level `Mount`** has the signature `Mount(ctx context.Context, r chi.Router, deps *app.Deps)` — it owns the canonical path prefix and calls `r.Mount("/path", Routes(ctx, deps))`. This is the only symbol the server ever calls.
- **Domain-level `Routes`** has the signature `Routes(ctx context.Context, deps *app.Deps) *chi.Mux` — it builds and returns the sub-router. Called by `Mount` and directly in tests.
- **Feature sub-package `Routes`** have the signature `Routes(ctx context.Context, r chi.Router, deps *app.Deps)` — they register routes directly on the provided router and return nothing. This avoids the `chi.Mount("/", ...)` panic that occurs when multiple sub-packages are mounted at the same path.
- Feature `routes.go` files construct their own rate limiters from `deps.KVStore` and start the cleanup goroutines. `deps` is the single source for all shared infrastructure — feature packages never import `config`.
- Rate limiters are always created inside the feature's `Routes` function, not pre-built externally. This keeps each feature's limiter configuration co-located with its route registration.

---

### 2.5 JWT Token Flow

JWT signing and parsing are handler-layer concerns, not service-layer concerns.

**General rule:** The service returns raw session metadata (`SessionID`, `RefreshJTI`, `FamilyID`, `RefreshExpiry` as plain Go types). The handler calls `platform/token` helpers to sign tokens and set cookies. JWT secrets never reach the service.

**Why the service must not sign tokens:** A service that holds JWT secrets cannot be tested without real secrets, cannot be swapped for a different token mechanism, and mixes cryptographic concerns with business logic. See ADR-001.

Auth-specific details (cookie attributes, blocklist on logout, handler-local claim types) are in [`docs/rules/auth.md §4`](rules/auth.md#4-jwt-token-flow).

---

### 2.6 Background Goroutine Ownership

Every background goroutine must be started by `routes.go` or `server/router.go` and must respect the application root context.

| Goroutine | Owner | Shutdown signal |
|---|---|---|
| Rate limiter cleanup | `domain/*/routes.go` | `<-ctx.Done()` |
| Backoff limiter cleanup | `domain/*/routes.go` | `<-ctx.Done()` |
| KV store close | `domain/*/routes.go` | `<-ctx.Done()` |
| Mail queue workers | `server/router.go` | `queue.Shutdown()` called in shutdown func |

A goroutine that ignores `ctx.Done()` is a shutdown bug. There are no exceptions.

---

### 2.7 Database Access Model

All SQL lives in `sql/queries/{domain}/`. All Go query code is generated by `sqlc` into `internal/db/`. Domain stores call `db.Querier` methods — they never write raw SQL in Go.

```
sql/queries/auth.sql          ← production queries (all auth features in one file)
sql/queries_test/auth_test.sql ← test-only helper queries (integration_test build tag)
  │
  sqlc generate (make sqlc)
  │
  └─► internal/db/auth.sql.go           (generated; never edited)
  └─► internal/db/auth_test.sql.go      (generated; tagged //go:build integration_test)
        │
        └─► domain/auth/store.go   (the only consumer)
```

The `db.Querier` interface is the stable contract between the store and the generated layer. Tests that need to substitute the query layer inject a fake `db.Querier`, not a fake pool.

---

### 2.8 Redis / In-Memory Strategy

The KV store backend (rate limiters, token blocklist) is selected once in `routes.go` based on whether `cfg.RedisURL` is set.

- **Development / single instance:** `kvstore.NewInMemoryStore(cleanupInterval)`
- **Production / multi-instance:** `kvstore.NewRedisStore(cfg.RedisURL)`

All rate limiters and the token blocklist share one store instance per domain routes file. This means one Redis connection pool, not one per limiter. The store interface is the only thing rate limiters depend on — swapping backends requires no changes to limiter code.

---

## 3. Conventions

Concrete rules for writing code in this codebase. Each rule is stated once and enforced in code review. When a rule needs an exception, the exception is recorded in §5 (ADRs), not made silently.

### 3.1 File Layout

Every domain package contains these **required** files:

| File | Purpose |
|---|---|
| `handler.go` | HTTP handlers. One handler method per endpoint. |
| `service.go` | Business logic. The `Storer` interface definition lives here. |
| `store.go` | Database access. The `Store` struct and all `*Tx` methods. |
| `routes.go` | Dependency wiring and route registration. No logic. |
| `models.go` | Service-layer I/O types. No `json:` tags. No pgtype. |

These files are **conditional** — create them only when the feature needs them:

| File | Create when |
|---|---|
| `requests.go` | The feature has at least one endpoint that reads a JSON request body |
| `errors.go` | The feature has feature-exclusive sentinel errors |
| `validators.go` | The feature has feature-exclusive validation functions |

**Note:** `password.go` and `otp.go` are shared-package files. They live in
`internal/domain/{name}/shared/` and are never created inside a feature sub-package.

**The names `helpers.go`, `utils.go`, and `common.go` are banned.** A file with one of these names is a sign that its contents belong somewhere more specific.

Additional files are allowed when a single coherent concern warrants extraction. The file name must state that concern clearly (e.g. `email.go`, `session.go`).

**Feature-file splitting.** When a domain grows beyond roughly five operations, `handler.go`, `service.go`, and `store.go` may be split by feature using the naming pattern `{feature}_{layer}.go` (e.g. `login_handler.go`, `login_service.go`, `login_store.go`). The base files then retain only struct definitions, constructors, interfaces, and shared private helpers. Every method implementation moves to a feature file. All files remain in the same package.

---

### 3.2 Package Naming

| Path | Package name | Notes |
|---|---|---|
| `internal/platform/token` | `token` | Not `jwt` — avoids collision with `github.com/golang-jwt/jwt/v5` |
| `internal/platform/kvstore` | `kvstore` | Not `store` — avoids collision with domain store structs |
| `internal/platform/ratelimit` | `ratelimit` | Not `middleware` — names the capability, not the position |
| `internal/domain/auth` | `auth` | |
| `internal/domain/auth/shared` | `authshared` | Not `shared` — avoids future collision with other `shared` packages |
| `internal/domain/auth/{feature}` | `{feature}` | e.g. `profile`, `register`, `login` |
| `internal/domain/auth/shared/testutil` | `authsharedtest` | Single shared test-helper package for all auth features |
| `internal/domain/rbac` | `rbac` | |
| `internal/audit` | `audit` | |

Import aliases are a signal that a package name is wrong. The only accepted aliases in this codebase are:
- `chimiddleware` for `github.com/go-chi/chi/v5/middleware`
- `authshared` for `github.com/7-Dany/store/backend/internal/domain/auth/shared`

---

### 3.3 Layer Type Rules

#### UUID handling

| Boundary | Type | Rule |
|---|---|---|
| Inside `store.go` | `pgtype.UUID` | Only here. Never returned or accepted across the store boundary. |
| Store ↔ Service | `[16]byte` | The canonical in-memory UUID form. Zero imports. |
| Service ↔ Handler | `string` | Standard UUID string form ("xxxxxxxx-xxxx-..."). |

```go
// pgtype.UUID → [16]byte
bytes := row.ID.Bytes

// [16]byte → string  (in service)
str := uuid.UUID(someBytes).String()

// string → [16]byte  (in service, parsing from JWT claims)
uid, err := uuid.Parse(str)
bytes := [16]byte(uid)

// [16]byte → pgtype.UUID  (in store)
pgUUID := pgtype.UUID{Bytes: b, Valid: true}
```

#### AuthProvider

`db.AuthProvider` is a typed enum. It must be used as-is at every call site inside `store.go`. The pattern `db.AuthProvider(someString)` is only permitted once: at the single point where raw user input is validated and mapped to a known provider value. In all other locations, pass `db.AuthProviderEmail` directly.

#### Service method signatures

Service input types are named `{Operation}Input`. Service output types are named `{Operation}Result` when they return data, or the domain noun directly when they return a domain object.

```go
func (s *Service) Register(ctx context.Context, in RegisterInput) (RegisterResult, error)
func (s *Service) Login(ctx context.Context, in LoginInput) (LoginResult, error)
func (s *Service) Me(ctx context.Context, userID string) (UserProfile, error)
func (s *Service) VerifyEmail(ctx context.Context, in VerifyEmailInput) error
```

#### Storer interface — in service.go

Every feature's `service.go` defines a `Storer` interface listing only the store methods that feature's service calls. The concrete `*Store` satisfies this interface via a compile-time check in `store.go`:

```go
// service.go
type Storer interface {
    GetUserProfile(ctx context.Context, userID [16]byte) (UserProfile, error)
}

// store.go
var _ Storer = (*Store)(nil)
```

#### Servicer interface — in handler.go

Every feature's `handler.go` defines a `Servicer` interface listing only the service methods that feature's handler calls:

```go
// handler.go
type Servicer interface {
    GetUserProfile(ctx context.Context, userID string) (UserProfile, error)
}
```

The `Servicer` interface is never checked with a compile-time assertion — `*Service` implementing `Servicer` is verified implicitly because `routes.go` passes a `*Service` to `NewHandler(svc Servicer)`.

#### Store method signatures

Store methods that write to the database are named `*Tx`. Read-only methods are named `Get*` or `List*`.

```go
func (s *Store) CreateUserTx(ctx context.Context, in CreateUserInput, codeHash string) (CreatedUser, error)
func (s *Store) GetUserForLogin(ctx context.Context, identifier string) (LoginUser, error)
func (s *Store) GetActiveSessions(ctx context.Context, userID [16]byte) ([]ActiveSession, error)
```

#### Feature store struct

Every feature's `store.go` follows this exact shape:

```go
var _ Storer = (*Store)(nil)

type Store struct {
    authshared.BaseStore
}

func NewStore(pool *pgxpool.Pool) *Store {
    return &Store{BaseStore: authshared.NewBaseStore(pool)}
}

func (s *Store) WithQuerier(q db.Querier) *Store {
    c := *s
    c.BaseStore = s.BaseStore.WithQuerier(q)
    return &c
}
```

A feature `Store` that is missing `WithQuerier` cannot be used in integration tests with transaction isolation.

---

### 3.4 Error Handling

#### Sentinel errors

Sentinel errors are defined in `errors.go` for the domain that owns them. They are `var` declarations, not constants.

```go
var ErrEmailTaken = errors.New("email already registered")
var ErrTokenNotFound = errors.New("token not found")
```

Typed errors (carrying data) are structs that implement `error` and `Unwrap`:

```go
type LoginLockedError struct {
    RetryAfter time.Duration
}
func (e *LoginLockedError) Error() string { return ErrLoginLocked.Error() }
func (e *LoginLockedError) Unwrap() error { return ErrLoginLocked }
```

Callers use `errors.Is` and `errors.As`. They never inspect error strings.

#### DB error mapping

The store maps database errors to domain sentinels at the boundary. The service never sees `pgx.ErrNoRows` or `pgconn.PgError`:

```go
if isNoRows(err) {
    return LoginUser{}, ErrUserNotFound
}
if isDuplicateEmail(err) {
    return CreatedUser{}, ErrEmailTaken
}
```

#### Handler error mapping

The handler translates domain sentinels to HTTP status codes using a `switch` statement. Every `case` must be explicit. The `default` case is always a 500 with a log line:

```go
switch {
case errors.Is(err, ErrInvalidCredentials):
    respond.Error(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
case errors.Is(err, ErrAccountLocked):
    respond.Error(w, http.StatusLocked, "account_locked", err.Error())
default:
    slog.ErrorContext(r.Context(), "auth.Login: service error", "error", err)
    respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
}
```

#### Error wrapping

Every `fmt.Errorf` call in a service or store uses `%w` and includes the function name as a prefix:

```go
return RegisterResult{}, fmt.Errorf("register.Register: hash password: %w", err)
return CreatedUser{}, fmt.Errorf("store.CreateUserTx: create user: %w", err)
```

---

### 3.5 Audit Log

Audit log event names are typed string constants defined in `internal/audit/audit.go`. No string literal event name may appear anywhere in any store file.

Event names are typed as EventType (a named string type) to prevent an arbitrary string from being assigned to an event field at compile time.

```go
// internal/audit/audit.go
const (
    EventRegister  EventType = "register"
    EventLogin     EventType = "login"
    EventLoginFailed EventType = "login_failed"
)

// store.go — correct usage
h.q.InsertAuditLog(ctx, db.InsertAuditLogParams{
    EventType: audit.EventRegister,
})
```

When a new event is needed, the constant is added to `audit/audit.go` first.

---

### 3.6 `context.WithoutCancel` Rule

Security-critical writes that must not be aborted by a client disconnect use `context.WithoutCancel(ctx)`:

- Incrementing OTP attempt counters
- Incrementing login failure counters
- Logging failed login audit rows
- Revoking token families after reuse detection
- Clearing the refresh token cookie on logout

```go
s.store.IncrementAttemptsTx(context.WithoutCancel(ctx), ...)
```

**Why:** An HTTP request context is cancelled when the client disconnects. Without `WithoutCancel`, a client who times a disconnect precisely can abort a counter increment, effectively getting unlimited OTP attempts. See ADR-004.

---

### 3.7 Anti-Enumeration Timing

Any endpoint that looks up a user by email and may reveal whether the email exists must equalise response latency between the "found" and "not found" paths. Two techniques are used together:

1. **Dummy hash compare on no-rows:** When the user is not found, still call `CheckPassword` / `VerifyCodeHash` against a precomputed dummy hash. Discard the result. This equalises bcrypt/HMAC latency.

2. **Uniform 202 on ambiguous outcomes:** Resend/forgot-password/unlock endpoints always return `202 Accepted` with the same body regardless of whether the email exists. The service returns `nil, zero-result` for these suppressed paths — never a sentinel.

See [`docs/rules/auth.md §5.3`](rules/auth.md#53-anti-enumeration-timing) for the annotation conventions and concrete examples.

---

### 3.8 Testing

#### Default: use real functions

The first choice is always to call the real function. Fakes are a last resort. The following cases never need a fake: tests for `validators.go`, `password.go`, `otp.go`, `errors.go`, `models.go`, and store integration tests that drive the real DB into a specific state.

#### When a fake is necessary

| Test target | What is faked | Why |
|---|---|---|
| Service unit tests | `Storer` interface | Service tests must not require a real DB |
| Handler unit tests | `Servicer` interface | Handler tests must not require a real service |
| Store error-path tests | `db.Querier` proxy | Forces individual DB calls to fail without extra infra |

#### Fake location

**Domain fakes** (implementations of `Storer` and `Servicer` interfaces) for
a domain live exclusively in `internal/domain/{name}/shared/testutil/` and are
never imported by production code. Feature sub-packages do **not** have their
own `testutil/` folder.

**Platform fakes** (test doubles for interfaces defined under `internal/platform/`)
are exempt from this rule. When domain code depends on a platform interface, the
test double may be:
- defined locally in the feature's `handler_test.go` when simple and used only there, or
- provided by a `testutil/` sub-package within the platform package itself.

Platform test doubles are **not** placed in the domain's testutil package.

**Exception — domain-internal interfaces:** If a platform-style interface is
defined inside the domain (e.g. `verification.BackoffChecker` in the auth domain),
its fake belongs in the domain's `shared/testutil/`, not in a platform package.
See [`docs/rules/auth.md §5.6`](rules/auth.md#56-backoffgo-and-nopbackoffchecker).

The auth domain testutil contains these files:

| File | Type | Purpose |
|---|---|---|
| `shared/testutil/fake_storer.go` | `{Feature}FakeStorer` | One struct per feature implementing that feature's `Storer` |
| `shared/testutil/fake_servicer.go` | `{Feature}FakeServicer` | One struct per feature implementing that feature's `Servicer` |
| `shared/testutil/querier_proxy.go` | `QuerierProxy` | Single proxy wrapping `db.Querier` with `Fail*` flags for every query across all features |
| `shared/testutil/builders.go` | helpers | Pool creation, `MustBeginTx`, `CreateUser`, `RunTestMain`, HTTP request builders, UUID/password/OTP helpers |

When building a new domain, create the equivalent `shared/testutil/` package
with the same four-file structure. See [`docs/rules/auth.md §2.3`](rules/auth.md#23-testutil-package-authsharedtest)
for the full auth implementation as a reference.

#### One test file per source file

Each source file `{name}.go` has exactly one test file `{name}_test.go` containing both unit tests and integration tests (guarded by `//go:build integration_test`).

Integration test functions carry the suffix `_Integration`:

```go
func TestCreateUserTx_Integration(t *testing.T) { ... }
```

#### bcrypt cost in tests

`SetBcryptCostForTest` controls the bcrypt cost used by both `HashPassword` and
`GenerateCodeHash`. There is no separate OTP setter — both are controlled by a
single package-level variable in `internal/domain/auth/shared`.

`RunTestMain` (called from `TestMain` in `store_test.go`) lowers the bcrypt
cost for the **entire test binary**, including unit tests compiled in the same
package. There is no separate `main_test.go` file — `TestMain` lives
exclusively in `store_test.go` behind the `//go:build integration_test` tag.
Do not add a standalone `TestMain` in any other file.

Unit tests that need hashed values (OTP codes, passwords) must use
`authsharedtest` builder helpers such as `MustOTPHash` and `MustHashPassword`
rather than calling `bcrypt.GenerateFromPassword` directly. This keeps hashing
aligned with the single package-level cost variable regardless of build tag.

#### Integration-test pool and transaction helpers

Do not write pool-creation or transaction boilerplate by hand. Use the two
centralised helpers from `authsharedtest`:

```go
// MustNewTestPool creates a pgxpool.Pool with MaxConns set.
// Always pass maxConns = 20 (required by ADR-003).
pool := authsharedtest.MustNewTestPool(dsn, 20)

// MustBeginTx begins a transaction, registers a rollback cleanup, and
// returns (pgx.Tx, *db.Queries) both scoped to the same transaction.
tx, q := authsharedtest.MustBeginTx(t, testPool)
```

**Why 20 connections:** ADR-003 requires `MaxConns >= 20` per feature test suite.
`IncrementAttemptsTx` and `IncrementLoginFailuresTx` always open a **fresh
pool transaction** that must run concurrently with the outer test transaction.
With `pgxpool`'s default of 4 connections this deadlocks.

#### Feature sub-package test file layout

| File | Build tag | Contents |
|---|---|---|
| `handler_test.go` | none | Handler unit tests using `httptest.NewRecorder` and a fake `Servicer` |
| `service_test.go` | none | Service unit tests using `FakeStorer` |
| `store_test.go` | `//go:build integration_test` | `var testPool`; `TestMain`; `txStores`; seed helpers; all store integration tests |

There is **no** non-build-tagged `{feature}_test.go` file and **no**
`main_test.go` file. `testPool`, `TestMain`, `txStores`, and seed helpers all
live in `store_test.go` behind the `//go:build integration_test` tag. This keeps all integration-test
infrastructure co-located with the tests that need it.

`TestMain` initialises `testPool` only when `TEST_DATABASE_URL` is set. Individual integration tests skip with `t.Skip(...)` when `testPool == nil`.

A canonical `TestMain` delegates entirely to `authsharedtest.RunTestMain` — do not deviate:

```go
func TestMain(m *testing.M) { authsharedtest.RunTestMain(m, &testPool, 20) }
```

`RunTestMain` lowers the bcrypt cost for fast unit tests, initialises
`testPool` when `TEST_DATABASE_URL` is set, runs the suite, closes the pool,
and calls `os.Exit`. Writing this boilerplate by hand in each package is
banned.

**Never use `defer` to close `testPool`** — `os.Exit` bypasses all deferred
calls, so the pool would never be closed.

#### `txStores` convention

Every feature's `store_test.go` defines a `txStores(t)` that skips on
`testPool == nil`, calls `authsharedtest.MustBeginTx`, and wires the result
into the feature's `NewStore(testPool).WithQuerier(q)`:

```go
func txStores(t *testing.T) (*myfeature.Store, *db.Queries) {
    t.Helper()
    if testPool == nil {
        t.Skip("no test database configured")
    }
    _, q := authsharedtest.MustBeginTx(t, testPool)
    return myfeature.NewStore(testPool).WithQuerier(q), q
}
```

Features that also expose the raw `pgx.Tx` (e.g. for ad-hoc assertion queries)
assign the first return value:

```go
tx, q := authsharedtest.MustBeginTx(t, testPool)
```

#### Structurally unreachable branches

Some branches are permanently untestable not because of a missing environment
variable or an unimplemented fake, but because of a fundamental seam
limitation — `BeginOrBind` with `TxBound=true` never calls `Pool.Begin`;
`QuerierProxy` intercepts `db.Querier` methods but cannot intercept
`pgx.Tx.Commit`; a helper that accepts `*testing.T` cannot accept a mock `T`.

**Do not create a `t.Skip` stub for these paths.** `t.Skip` signals "runnable
under the right conditions". A permanently unreachable branch will never
satisfy that signal — the stub just adds noise to `go test -v` output and
puts documentation in the wrong place.

Instead, place an `// Unreachable:` comment directly above the dead branch in
the **source file**, explaining why no test can reach it:

```go
// Unreachable: BeginOrBind with TxBound=true never calls Pool.Begin
// and always returns nil error. No test can trigger this branch.
if err != nil {
    return fmt.Errorf("store.IncrementAttemptsTx: begin tx: %w", err)
}

// Unreachable via QuerierProxy: on the TxBound path commitFn is a no-op
// that always returns nil; on the non-TxBound path commitFn wraps
// pgx.Tx.Commit which the proxy cannot intercept.
if err := commitFn(); err != nil {
    return fmt.Errorf("store.IncrementAttemptsTx: commit: %w", err)
}
```

This keeps the explanation next to the code where the next reader will land,
makes the gap greppable (`grep -rn "// Unreachable:" ./internal/`), and leaves
the test suite free of permanently-skipped functions.

The same rule applies to `t.Fatalf` branches in test helpers that accept
`*testing.T`: they cannot be exercised without injecting a mock `T`, which
Go's testing model does not support. No stub is needed — the branch is
structurally unreachable and needs no documentation beyond what the helper's
own code already makes obvious.

---

#### Seed helpers: naming

Seed helpers in `store_test.go` that insert prerequisite rows are unexported
functions (e.g. `createUser`, `withProxy`). They call `t.Fatalf` on error —
never `require.NoError` directly — so the failure message includes the helper
name. Seed helpers that create a user always go through
`authsharedtest.CreateUser` or `authsharedtest.CreateUserUUID`; they never
call `db.Querier.CreateUser` directly with `pgtype` fields.

#### authsharedtest package patterns

**`shared/testutil/fake_storer.go`** — package `authsharedtest`:

One named `FakeStorer` struct per feature, following the `{Feature}FakeStorer`
naming convention. Each has one `{MethodName}Fn` field per `Storer` method.
When the `Fn` field is non-nil it is called; otherwise the zero value and nil
error are returned so tests only configure the fields they need.

```go
// LoginFakeStorer implements login.Storer for service unit tests.
type LoginFakeStorer struct {
    GetUserForLoginFn func(ctx context.Context, identifier string) (login.LoginUser, error)
    // ... one Fn per Storer method
}

// compile-time interface check.
var _ login.Storer = (*LoginFakeStorer)(nil)

func (f *LoginFakeStorer) GetUserForLogin(ctx context.Context, identifier string) (login.LoginUser, error) {
    if f.GetUserForLoginFn != nil {
        return f.GetUserForLoginFn(ctx, identifier)
    }
    return login.LoginUser{}, nil
}
```

**`shared/testutil/querier_proxy.go`** — package `authsharedtest`:

A single `QuerierProxy` struct covers every feature. `Fail*` fields are
grouped by feature with section separator comments.

```go
var ErrProxy = errors.New("querier_proxy: injected error")

type QuerierProxy struct {
    Base db.Querier
    // ── login ────────────────────────────────────────────────────────────
    FailGetUserForLogin bool
    // ... all features' flags
}

var _ db.Querier = (*QuerierProxy)(nil)

func NewQuerierProxy(base db.Querier) *QuerierProxy {
    return &QuerierProxy{Base: base}
}
```

When a new store method is added to any feature, add the corresponding `Fail*`
field to `QuerierProxy` and the corresponding `{Feature}FakeStorer` method to
`fake_storer.go`. Never create a per-feature `testutil/` folder.

---

### 3.9 SQL Conventions

All SQL lives in two flat files per domain. Production queries go in `sql/queries/{domain}.sql`; test-only helper queries go in `sql/queries_test/{domain}_test.sql`. Every query has a `-- name:` directive in `PascalCase`. Queries within each file are grouped by auth flow with section comments.

```sql
-- name: GetUserForLogin :one
-- name: CreateUserSession :one
-- name: IncrementVerificationAttempts :execrows
```

Token consumption queries use `FOR UPDATE` to prevent concurrent double-consumption. RBAC entities are soft-deleted via `is_active = FALSE`. Auth tokens are hard-deleted only by cleanup jobs, never in request paths.

#### No raw SQL in Go

**Raw SQL strings are banned from all Go files — production and test alike.** Every query must live in a `.sql` file under `sql/queries/` and be referenced only through the sqlc-generated query layer.

| Need | Correct location |
|---|---|
| Production query | `sql/queries/{domain}.sql` — append to the relevant section comment |
| Test-only helper query (read-back assertions, seed data, state coercion, timestamp manipulation) | `sql/queries_test/{domain}_test.sql` — append at the end |

Test-only `.sql` files follow the same `-- name:` + `PascalCase` convention and the same section-comment grouping style. Run `make sqlc` after adding any query.

**Banned patterns in any `.go` file (including test files):**

```go
// All of these are banned — move the SQL to a .sql file and run make sqlc.
pool.Exec(ctx, "UPDATE users SET email_verified = TRUE WHERE id = $1", id)
tx.QueryRow(ctx, "SELECT id FROM roles WHERE is_owner_role = TRUE")
conn.Query(ctx, "INSERT INTO user_roles ...")
```

**Calling test-only generated methods:** Methods generated from `sql/queries_test/` are defined directly on `*db.Queries` (they carry the `//go:build integration_test` tag) but are **not** added to the `db.Querier` interface (which has no build tag). Test code that needs these methods must obtain a `*db.Queries` value rather than the interface:

```go
// Wrong — db.Querier does not expose test-only methods.
_, q := authsharedtest.MustBeginTx(t, testPool)   // q is db.Querier
q.CreateVerifiedUserWithUsername(...)              // compile error

// Correct — use db.New(tx) to get the concrete *db.Queries.
tx, _ := authsharedtest.MustBeginTx(t, testPool)
q := db.New(tx)                                    // *db.Queries exposes test methods
q.CreateVerifiedUserWithUsername(...)

// Also correct — type-assert when you already have db.Querier.
_, iface := authsharedtest.MustBeginTx(t, testPool)
cq := iface.(*db.Queries)
cq.CreateVerifiedUserWithUsername(...)
```

This rule exists because raw SQL in Go files bypasses `go vet`, type-checking, and the sqlc schema-diff check. A raw SQL string that drifts from the schema fails only at runtime in CI, not at compile time.

---

### 3.10 HTTP Conventions

Every handler caps the request body before decoding:

```go
r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)
```

Validation (`validateAndNormalise` or equivalent) is called in the handler before any service call. The service never validates raw HTTP input.

#### URL path naming

Paths identify **resources and sub-resources**. The HTTP method carries the verb. Path segments are always lowercase nouns or noun-phrases — never verb phrases.

**Rules:**

1. **No verb prefixes.** Strip any leading action word and reorganise around the resource.
   | ❌ Banned | ✓ Correct | Why |
   |---|---|---|
   | `POST /auth/forgot-password` | `POST /auth/password/reset` | `password` is the resource, `reset` is the sub-action noun |
   | `POST /auth/verify-reset-code` | `POST /auth/password/reset/verify` | `verify` names the step, not the operation |
   | `POST /auth/change-password` | `PATCH /auth/password` | PATCH already says "change" |
   | `POST /auth/resend-verification` | `POST /auth/verification/resend` | resource first, then step |
   | `POST /auth/request-unlock` | `POST /auth/unlock` | POST already says "request" |
   | `POST /auth/confirm-unlock` | `PUT /auth/unlock` | PUT signals replacement/confirmation |
   | `POST /admin/users/{id}/force-password-reset` | `POST /admin/users/{id}/password/reset` | same resource hierarchy as auth domain |
   | `POST /profile/me/cancel-deletion` | `DELETE /profile/me/deletion` | DELETE already says "cancel/remove" |

2. **Resource hierarchy first.** Build paths from general to specific: `/{resource}/{sub-resource}/{step}`.
   - `password/reset` — the resource is `password`, the sub-resource is `reset` (the reset flow)
   - `password/reset/verify` — one level deeper: the verify step within the reset flow
   - `owner/transfer` — the resource is `owner`, the sub-resource is `transfer` (the transfer flow)

3. **Multi-step flows share the base path; the method and an optional `/step` distinguish phases.**
   ```
   POST   /password/reset          ← step 1: request OTP
   POST   /password/reset/verify   ← step 2: verify OTP, receive grant token
   PUT    /password/reset          ← step 3: apply new password
   ```
   ```
   POST   /me/email                ← step 1: request change OTP
   POST   /me/email/verify         ← step 2: verify current-email OTP
   PUT    /me/email                ← step 3: confirm new-email OTP
   ```

4. **Allowed single-word action nouns as step segments.** A small set of words name a distinct lifecycle phase cleanly and are permitted as the final segment: `verify`, `resend`, `assign`, `transfer`. These are nouns in context (the verify step, the transfer resource), not imperative verbs.

5. **Hyphens in path segments are banned.** A hyphenated segment is a signal to either split into two segments (`password-reset` ❌ → `password/reset` ✓) or drop the redundant word entirely when the shorter form is unambiguous (`magic-link` ❌ → `magic` ✓, since no other `/auth/magic` resource exists).

6. **Admin sub-resources follow the same hierarchy.** Put the user anchor first, then the resource:
   - ✓ `/admin/users/{id}/role`
   - ✓ `/admin/users/{id}/lock`
   - ✓ `/admin/users/{id}/password/reset`
   - ❌ `/admin/lock-user/{id}` — verb phrase
   - ❌ `/admin/force-password-reset/{id}` — verb phrase

#### Always use `platform/*` packages — never hand-roll their equivalents

The packages under `internal/platform/` exist specifically to centralise
cross-cutting concerns. Using them everywhere is what keeps those concerns
consistent. Hand-rolling an alternative in a handler or route file is a
convention violation, regardless of how simple it looks.

| Concern | Required package | Banned alternatives |
|---|---|---|
| JSON success response | `respond.JSON(w, status, v)` | `w.Header().Set("Content-Type", ...); w.WriteHeader(...); w.Write(...)` |
| JSON error response | `respond.Error(w, status, code, msg)` | raw `w.Write([]byte(...))`, `json.NewEncoder(w).Encode(...)` |
| 204 No Content | `respond.NoContent(w)` | `w.WriteHeader(http.StatusNoContent)` |
| Decode request body | `respond.DecodeJSON[T](w, r)` | `json.NewDecoder(r.Body).Decode(...)` directly |
| Client IP | `respond.ClientIP(r)` | `r.RemoteAddr` directly |
| JWT signing / parsing | `platform/token` helpers | `github.com/golang-jwt/jwt` calls in handler code |
| Rate limiting | `platform/ratelimit` limiters | ad-hoc counters, `sync.Map`, in-memory maps |
| Key-value / blocklist | `platform/kvstore` | direct `redis.Client` or `sync.Map` calls outside the kvstore layer |
| Email delivery | `platform/mailer.SendOTP` | direct `h.Mailer.SendVerificationEmail` calls in handlers |

Violations are caught in the §3.13 sub-package split checklist under the
handler-layer checks.

The only exception: `respond.NoContent` calls `w.WriteHeader(http.StatusNoContent)`
internally — that line in `platform/respond/respond.go` itself is the canonical
location. Everywhere else in the codebase, call `respond.NoContent(w)`.

Errors always use `respond.Error(w, status, code, message)`. The `code` field is `snake_case` and machine-readable. The `message` field is human-readable. Before calling `respond.Error` for a 5xx status, the caller must log the underlying error with `slog.ErrorContext`; `respond.Error` does not log on its own.

Refresh token cookies are always set with:
- `HttpOnly: true`
- `SameSite: http.SameSiteStrictMode`
- `Secure: h.secureCookies`
- `Path: "/api/v1/auth"`
- `MaxAge` driven by the DB row's `expires_at` — never a hardcoded duration

Authenticated routes always extract identity via `token.UserIDFromContext(r.Context())`. They never read the `Authorization` header directly.

---

### 3.11 Naming Quick Reference

| Thing | Convention | Example |
|---|---|---|
| Service input struct | `{Operation}Input` | `RegisterInput`, `LoginInput` |
| Service result struct | `{Operation}Result` | `RegisterResult`, `LoginResult` |
| Store mutating method | `{Action}Tx` | `CreateUserTx`, `LoginTx` |
| Store read method | `Get{Thing}` or `List{Things}` | `GetUserForLogin`, `GetActiveSessions` |
| Sentinel error | `Err{Condition}` | `ErrEmailTaken`, `ErrTokenExpired` |
| Typed error | `{Condition}Error` | `LoginLockedError` |
| Audit event constant | `Event{PastTense}` | `EventEmailVerified`, `EventPasswordChanged` |
| Rate limiter variable | `{scope}{action}Limiter` | `loginLimiter`, `forgotLimiter`, `resendLimiter` |

> **Note:** `EventRegister`, `EventLogin`, `EventLogout`, `EventLoginLockout`,
> and `EventResendVerification` pre-date this rule and retain their original
> names to avoid a data migration on existing audit rows. New constants must
> use the `Event{PastTense}` pattern.
| Feature `Storer` | `Storer` | same name in every feature package |
| Feature `Servicer` | `Servicer` | same name in every feature package |
| Domain-level `Mount` function | `Mount(ctx context.Context, r chi.Router, deps *app.Deps)` | root assembler only — owns the canonical path prefix; the only domain symbol the server ever calls |
| Domain-level `Routes` function | `Routes(ctx context.Context, deps *app.Deps) *chi.Mux` | root assembler only — builds and returns the sub-router; called by `Mount` and in tests |
| Feature sub-package `Routes` function | `Routes(ctx context.Context, r chi.Router, deps *app.Deps)` | registers routes directly on r; no return value |
| Cross-namespace admin helper | `Register{Scope}Routes(ctx context.Context, r chi.Router, deps *app.Deps)` | only when a domain contributes to a URL tree it does not own (e.g. `rbac.RegisterAdminRoutes`); the server router owns the group |
| Auth testutil package | `authsharedtest` | `internal/domain/auth/shared/testutil/` — one package for all auth features |
| Auth testutil fake | `{Feature}FakeStorer` | `LoginFakeStorer`, `ProfileFakeStorer`, etc. — all in `authsharedtest` |
| Auth testutil proxy | `QuerierProxy` | single struct in `authsharedtest` covering all features |
| Auth testutil proxy sentinel | `ErrProxy` | defined once in `authsharedtest/querier_proxy.go` |

---

### 3.12 Configuration

`internal/config/config.go` is the **only** file in the codebase that may call `os.Getenv`. No other file — production or test — reads environment variables directly.

`config.Load()` is called once at startup. Services and stores never receive a `*Config`; they receive only the specific primitive values they need, injected by `routes.go`.

For tests, use the dedicated helpers:

```go
dsn := config.TestDatabaseURL()   // reads TEST_DATABASE_URL
url := config.TestRedisURL()      // reads TEST_REDIS_URL, falls back to REDIS_URL

// Wrong — bypasses the single-point-of-reading rule:
dsn := os.Getenv("TEST_DATABASE_URL")
```

---

### 3.13 Sub-Package Split Checklist

Use this checklist when extracting or verifying a feature sub-package.

**Package structure**
- [ ] Package name is `{feature}`, not `auth`.
- [ ] `models.go` has only types specific to this feature. No `json:` tags.
- [ ] `requests.go` has only HTTP request/response structs with `json:` tags.
- [ ] `errors.go` exists only if the feature has feature-exclusive sentinel errors.
- [ ] `validators.go` exists only if the feature has feature-exclusive validators.
- [ ] No file is named `helpers.go`, `utils.go`, or `common.go`.
- [ ] `password.go` and `otp.go` do not exist — those live in `shared/`.

**`routes.go`**
- [ ] If this is a domain root assembler: exports exactly two symbols — `Mount` and `Routes`.
- [ ] If this is a feature sub-package: exports exactly one symbol — `Routes`.
- [ ] Domain-level `Mount` signature: `Mount(ctx context.Context, r chi.Router, deps *app.Deps)` — calls `r.Mount("/path", Routes(ctx, deps))`.
- [ ] Domain-level `Routes` signature: `Routes(ctx context.Context, deps *app.Deps) *chi.Mux` — builds and returns the sub-router.
- [ ] Feature sub-package `Routes` signature: `Routes(ctx context.Context, r chi.Router, deps *app.Deps)` with no return value.
- [ ] **Exception:** a domain that contributes to a URL tree it does not own (e.g. `rbac` → `/admin`) may also export one `Register{Scope}Routes` helper; document the exception in the package comment.
- [ ] If this exports `Register{Scope}Routes`: the caller (server/routes.go) owns the URL group and its middleware — the domain assembler must not create the group internally.
- [ ] Domain `routes.go` never imports another domain package (ADR-010). If cross-domain wiring is needed, the server router performs it.
- [ ] Rate limiters are constructed locally from `deps.KVStore` — not received as pre-built values.
- [ ] `deps.JWTAuth` used directly as middleware where authentication is required.
- [ ] Does not import `config`. All values come from `*app.Deps`.
- [ ] Background goroutine cleanup (rate limiters) started here and listens on `ctx.Done()`.

**`service.go`**
- [ ] `Storer` interface defined here.
- [ ] No import of `pgtype`, `pgxpool`, `pgx`, `net/http`, `encoding/json`.
- [ ] No import of `config` or `platform/token`.
- [ ] No import of any other feature sub-package.
- [ ] Constructor: `func NewService(store Storer) *Service`.
- [ ] Error wrapping: `fmt.Errorf("{feature}.{Method}: {step}: %w", err)`.
- [ ] `context.WithoutCancel` used for security-critical writes.

**`store.go`**
- [ ] `var _ Storer = (*Store)(nil)` compile-time check present.
- [ ] `Store` embeds `authshared.BaseStore`.
- [ ] `NewStore(pool *pgxpool.Pool) *Store` present.
- [ ] `WithQuerier(q db.Querier) *Store` present.
- [ ] Conversion helpers called as `s.ToPgtypeUUID(...)`, `s.ToText(...)`, etc.
- [ ] `s.IsNoRows(err)` used for no-rows detection.
- [ ] `s.IsDuplicateEmail(err)` used for unique-violation detection.
- [ ] No `pgtype.*`, `pgx.*`, or `db.*` type in public method signatures.
- [ ] `InsertAuditLog` calls use `audit.Event*` constants — no string literals.
- [ ] No raw SQL strings — every query is a generated `db.Querier` or `*db.Queries` method call (RULES.md §3.9).
- [ ] No `pool.Exec`, `tx.Exec`, `pool.QueryRow`, `tx.QueryRow` calls with inline SQL strings anywhere in any `.go` file.
- [ ] New production queries added to `sql/queries/{domain}.sql`; new test helpers added to `sql/queries_test/{domain}_test.sql`; `make sqlc` run after any addition.
- [ ] Error wrapping: `fmt.Errorf("store.{Method}: {step}: %w", err)`.
- [ ] Every multi-step Tx method has numbered step comments.

**`handler.go`**
- [ ] `Servicer` interface defined here.
- [ ] `Handler` struct fields: `svc Servicer` plus primitive config values only.
- [ ] `NewHandler(svc Servicer, ...) *Handler` constructor present.
- [ ] No import of `pgtype`, `pgxpool`, `pgx`, `internal/db`.
- [ ] `r.Body = http.MaxBytesReader(w, r.Body, respond.MaxBodyBytes)` at the top of every POST/PUT/PATCH handler.
- [ ] Identity extracted via `token.UserIDFromContext(r.Context())`.
- [ ] Error `switch` covers every sentinel; `default` always logs and returns 500.
- [ ] Log key is `"error"`, never `"err"`.
- [ ] All JSON responses use `respond.JSON` or `respond.Error` — no hand-rolled `w.Write`, `w.WriteHeader`, or `json.NewEncoder` calls.
- [ ] All 204 responses use `respond.NoContent(w)` — never bare `w.WriteHeader(http.StatusNoContent)`.
- [ ] All request body decoding uses `respond.DecodeJSON[T]` — no direct `json.NewDecoder(r.Body).Decode` calls.
- [ ] Client IP extracted via `respond.ClientIP(r)` — never `r.RemoteAddr` directly.
- [ ] Every `respond.Error` call for a 5xx status is preceded by `slog.ErrorContext` on the underlying error.

**Import rules**
- [ ] Does not import any other feature package within the same domain.
- [ ] Does not import `{domain}/shared/testutil` (`{domain}sharedtest`) in production files.
- [ ] Domain shared package imported with the correct alias (`{name}shared "github.com/.../domain/{name}/shared"`).

**`shared/testutil/fake_storer.go`** (package `{domain}sharedtest`)
- [ ] No per-feature `testutil/` folder exists — domain fakes live in `shared/testutil/` only.
- [ ] Platform interface fakes for interfaces defined under `internal/platform/` are **not** placed here — they belong in the feature's own test file or in the platform package's `testutil/` sub-package. Domain-internal interface fakes (e.g. an interface defined within the domain, not under `platform/`) may live here.
- [ ] Struct named `{Feature}FakeStorer` (e.g. `LoginFakeStorer`, `ProfileFakeStorer`).
- [ ] One `{MethodName}Fn` field per `Storer` method.
- [ ] Each method: if `Fn != nil` delegate; else return zero value + nil error (or a deliberate non-nil default when the zero-value would cause a false positive — document the reason).
- [ ] `var _ {feature}.Storer = (*{Feature}FakeStorer)(nil)` present.
- [ ] New feature's fake added here, not in a new `testutil/` folder.

**`shared/testutil/fake_servicer.go`** (package `{domain}sharedtest`)
- [ ] Struct named `{Feature}FakeServicer` (e.g. `LoginFakeServicer`, `ProfileFakeServicer`).
- [ ] One `{MethodName}Fn` field per `Servicer` method.
- [ ] Each method: if `Fn != nil` delegate; else return zero value + nil error.
- [ ] `var _ {feature}.Servicer = (*{Feature}FakeServicer)(nil)` present.
- [ ] New feature's servicer fake added here alongside its storer fake.

**`shared/testutil/querier_proxy.go`** (package `{domain}sharedtest`)
- [ ] No per-feature `testutil/` folder exists — proxy lives in `shared/testutil/` only.
- [ ] `ErrProxy` sentinel present.
- [ ] `QuerierProxy` has `Base db.Querier` field.
- [ ] New `Fail{MethodName} bool` fields added under the feature's section separator comment.
- [ ] `var _ db.Querier = (*QuerierProxy)(nil)` present.
- [ ] `NewQuerierProxy(base db.Querier) *QuerierProxy` present.

**`store_test.go`**
- [ ] `//go:build integration_test` on line 1.
- [ ] Package: `{feature}_test`.
- [ ] `var testPool *pgxpool.Pool` at package level.
- [ ] `TestMain` is a one-liner: `func TestMain(m *testing.M) { {domain}sharedtest.RunTestMain(m, &testPool, 20) }`.
- [ ] No manual bcrypt cost setting, pool creation, or `os.Exit` call — `RunTestMain` handles all of it.
- [ ] `txStores` is defined here; delegates to `{domain}sharedtest.MustBeginTx` for transaction setup.
- [ ] Seed helpers (e.g. `createUser`, `withProxy`) defined here; use `{domain}sharedtest` builder helpers rather than calling `db.Querier` methods with pgtype fields directly.
- [ ] Every test function name ends with `_Integration`.
- [ ] No raw SQL strings — all read-back and seed queries are generated `db.Querier` or `*db.Queries` methods (RULES.md §3.9).
- [ ] No `pool.Exec`, `tx.Exec`, `pool.QueryRow`, `tx.QueryRow` calls with inline SQL strings — add queries to `sql/queries_test/{domain}_test.sql` and run `make sqlc` instead (RULES.md §3.9).
- [ ] Test-only generated methods (from `queries_test/`) are called on `db.New(tx)` or a type-asserted `*db.Queries`, never on the `db.Querier` interface (RULES.md §3.9).
- [ ] No `{feature}_test.go` file exists — there is no non-build-tagged test file in the package.
- [ ] No `main_test.go` file exists — `TestMain` lives here, behind the `integration_test` build tag. `RunTestMain` lowers the bcrypt cost for the whole test binary including unit tests.

**`service_test.go`**
- [ ] No build tag. Uses `FakeStorer`. Tests are parallel (`t.Parallel()`).
- [ ] Timing invariants (dummy hash paths) are explicitly asserted.
- [ ] Helper functions that construct hashed values (tokens, passwords) use `{domain}sharedtest` builders (`MustOTPHash`, `MustHashPassword`) — no direct `bcrypt.GenerateFromPassword` calls.

**Final checks**
- [ ] `go build ./internal/domain/{name}/{feature}/...` passes.
- [ ] `go vet ./internal/domain/{name}/{feature}/...` produces no output.
- [ ] `go test ./internal/domain/{name}/{feature}/...` (unit only) passes.
- [ ] `go test -tags integration_test ./internal/domain/{name}/{feature}/...` passes.

---

### 3.14 Mandatory Three-File Sync Rules

Three groups of files must always be updated together. Forgetting any one member
causes an **immediate build or test failure in a different package** — the kind
that looks unrelated to what you just changed. Treat these as atomic commits.

#### Sync S-1 — Audit event triad

`internal/audit/audit.go` has three independently-maintained lists that must
stay in exact lockstep:

| Location | What to add |
|---|---|
| `const` block | `EventXxx EventType = "value_string"` |
| `AllEvents()` return slice | `EventXxx,` |
| `audit_test.go` `TestEventConstants_ExactValues` `cases` table | `{audit.EventXxx, "value_string"},` |

The test enforces `len(AllEvents()) == len(cases)`. A count mismatch of even
one entry fails the entire `internal/audit` package regardless of what other
code is in review.

**Rule:** Never add a constant to the `const` block without also adding it to
`AllEvents()` and `cases` in the same commit.

#### Sync S-2 — Querier / QuerierProxy / nopQuerier triad

Every time `make sqlc` adds a new method to `db.Querier`
(`internal/db/querier.go`), three hand-maintained files must be updated:

| File | What to add |
|---|---|
| `shared/testutil/querier_proxy.go` | Forwarding method + `Fail{MethodName} bool` field |
| `querier_proxy_test.go` | Stub method on `nopQuerier` returning zero value + `nil` |
| Any other `*_test.go` file with a local `db.Querier` implementation | Same stub method |

Missing any one of these produces a **build failure** in the testutil package,
which blocks the entire test suite. Search for `db.Querier` in `*_test.go`
files to find all local implementations that need the new stub.

**Rule:** Run `go build ./internal/domain/{name}/shared/testutil/...` after
every `make sqlc` to catch missing stubs before pushing.

#### Sync S-3 — DecodeJSON 413 path

`respond.DecodeJSON` in `internal/platform/respond/respond.go` drains the
remaining body after a JSON syntax error and re-checks for `*http.MaxBytesError`.
This is required because `json.NewDecoder` stops reading at the first invalid
byte, so `MaxBytesReader` never fires its size-limit error on a non-JSON
oversized payload — the handler returns 422 instead of 413.

**Rule:** Do not remove the `io.Copy(io.Discard, r.Body)` drain in
`DecodeJSON`. Handler tests that assert `413 Request Entity Too Large` **must**
send a raw byte slice (not valid JSON) as the oversized body so this path is
actually exercised.

---

### 3.15 Mailer Template Convention

All transactional email types are registered through the template registry. No
handler or service ever calls a bespoke `Send{Type}Email` method — the extension
point is the registry, not `SMTPMailer`.

#### Adding a new email type: three-file change

| File | What to add |
|---|---|
| `internal/platform/mailer/templates/{name}.go` | `{Name}Key` const, exported `*string` var, unexported template string var |
| `internal/platform/mailer/templates/registry.go` | One `Entry{Key, SubjectFmt, HTML}` in the `Registry()` map |
| Handler that sends the email | `deps.Mailer.Send(mailertemplates.{Name}Key)(ctx, toAddr, code)` call |

Nothing in `mailer.go` or `SMTPMailer` ever changes for a new email type.

#### Template file shape

Follow the pattern established in `verification.go` and `unlock.go`:

```go
// {Name}Key is the canonical identifier for the {description} template.
const {Name}Key = "{snake_case_key}"

// {Name}Template is the HTML template for ...
var {Name}Template = &{name}TemplateStr

var {name}TemplateStr = `<!DOCTYPE html>...`
```

The exported var is a `*string` pointer to the unexported var so tests can swap
the template string to force parse/execute error paths without touching the
mailer infrastructure.

#### Sending from a handler

Handlers import the templates package under the alias `mailertemplates` and
enqueue mail as a best-effort fire-and-forget:

```go
import mailertemplates "github.com/7-Dany/store/backend/internal/platform/mailer/templates"

deps.MailQueue.Enqueue(r.Context(), func(ctx context.Context) error {
	return deps.Mailer.Send(mailertemplates.{Name}Key)(ctx, toAddr, code)
})
```

For notification templates that do not render a code, pass `""` as the third
argument. The template must not reference `{{.Code}}`, or must guard it — the
behaviour is documented on the `{Name}Key` constant.

#### Never add methods to SMTPMailer

`SMTPMailer` already exposes `Send(key string) func(context.Context, string,
string) error`. Adding per-type methods (`SendEmailChangeOTP`, etc.) is a
convention violation regardless of how convenient it looks at the call site.
The registry pattern is the extension point — use it.

---

## 4. Go Comment Conventions

The authoritative references are the standard library packages `fmt`, `net/http`,
and `context` — read them before arguing with any rule here.

### 4.1 The Core Principle

A comment is for the reader of the code, not the writer. Before adding a comment, ask:
**would an experienced Go developer be confused or miss something important without it?**
If no — delete the comment.

---

### 4.2 Package Comments

Every package has exactly one doc comment, on the `package` declaration in the file
that best represents the package's primary concern (usually the file named after the
package, or `models.go` for feature sub-packages).

One sentence. Starts with `Package {name}`. States what the package provides — not how
it works, not why it was designed this way.

```go
// Package authshared holds primitives shared by all auth feature sub-packages.
package authshared

// Package profile provides the HTTP handler, service, and store for
// authenticated user profile operations.
package profile

// Package kvstore provides a generic key-value store for rate limiting and token blocklisting.
package kvstore
```

**What to avoid:**

```go
// Bad — implementation detail
// Package authshared provides shared primitives.
// It uses bcrypt for password hashing and implements sync.Once for lazy initialization.
package authshared

// Bad — design document
// Package profile handles profile operations.
//
// Architecture:
//   - Handler calls Service.
//   - Service calls Store through the Storer interface.
package profile
```

Design rationale belongs in §5 (ADRs). Architecture belongs in §2. The package comment
is for `go doc` consumers who need one sentence of context.

---

### 4.3 Exported Identifier Comments

Every exported identifier — type, function, method, variable, constant — must have
a doc comment. No exceptions.

Start with the **exact name** of the identifier. Use a complete sentence. End with a
period. One sentence is almost always enough.

```go
// UserProfile is the store-layer representation of a user's public profile.
type UserProfile struct { ... }

// NewService constructs a Service with the given store.
func NewService(store Storer) *Service { ... }

// GetUserProfile returns the public profile for the given user.
// Returns authshared.ErrUserNotFound on no-rows.
func (s *Service) GetUserProfile(ctx context.Context, userID string) (UserProfile, error)

// ErrUserNotFound is returned when the user record cannot be located.
var ErrUserNotFound = errors.New("user not found")

// Handler handles HTTP requests for the login feature.
type Handler struct { ... }
```

Add a second sentence only to document a non-obvious error condition, a constraint on
the caller, or a side effect the signature does not make clear:

```go
// CreateUserTx inserts a new user and issues an email verification token.
// Returns ErrEmailTaken if the address is already registered.
func (s *Store) CreateUserTx(...) (CreatedUser, error)

// UpdatePasswordHashTx updates the user's password hash, revokes all active
// refresh tokens, ends all sessions, and writes a password_changed audit row —
// all in one transaction.
func (s *Store) UpdatePasswordHashTx(ctx context.Context, ...) error
```

**What to avoid:**

```go
// Bad — restates the type name
// UserProfile is a struct that contains user profile data.

// Bad — describes implementation, not the contract
// GetUserProfile queries the users table by ID and returns the result.

// Bad — omits the error condition the caller must handle
// GetUserProfile returns the public profile for the given user.  ← missing ErrUserNotFound
```

---

### 4.4 Unexported Identifiers

Unexported identifiers do **not** need doc comments unless the logic would confuse
a competent Go developer reading the function body for the first time.

```go
// maxUserAgentBytes is the maximum number of bytes stored in the user_agent column.
const maxUserAgentBytes = 512   // worth a comment — the reader needs to know WHY 512

const maxBodyBytes = 1 << 20   // no comment needed — 1 MiB is self-documenting
```

---

### 4.5 Inline Comments — Why, Not What

Inline `//` comments explain **why** the code does something, or what constraint it
satisfies. They never explain **what** the code does — the code already shows that.

```go
// Bad — restates the code
cost := bcrypt.DefaultCost // set cost to default

// Good — explains the constraint
cost := bcrypt.DefaultCost // minimum 12 in production; lowered in tests via SetBcryptCostForTest
```

```go
// Bad
if session.UserID.Bytes != ownerUserID { // check if user owns the session

// Good — explains WHY we return NotFound instead of Forbidden
// Returning NotFound (not Forbidden) prevents the caller from inferring
// that a session with this ID exists but belongs to someone else (IDOR).
if session.UserID.Bytes != ownerUserID {
```

---

### 4.6 Security Annotations

Any line that encodes a non-obvious security invariant gets a `// Security:` comment
immediately above it. The comment names the invariant and explains what breaks if it
is removed.

```go
// Security: detach from the request context so a client-timed disconnect cannot
// abort the counter increment and grant the attacker unlimited OTP attempts.
if err := s.store.IncrementAttemptsTx(context.WithoutCancel(ctx), in); err != nil {

// Security: cookie flags are required. Removing Secure or relaxing SameSite makes
// token theft feasible over HTTP.
http.SetCookie(w, &http.Cookie{
    HttpOnly: true,
    Secure:   h.secureCookies,
    SameSite: http.SameSiteStrictMode,
})
```

The `// Security:` prefix makes it trivially greppable:

```sh
grep -rn "// Security:" ./internal/
```

---

### 4.7 Timing Invariant Annotations

Any code block that equalises response time between "user found" and "user not found"
paths gets a doc comment on the method explaining the invariant, and an inline comment
at the dummy-hash call site.

**Doc comment (on the method):**

```go
// UpdatePasswordHash verifies the caller's current password and replaces it
// with a new one, revoking all active sessions on success.
//
// Timing invariant: CheckPassword always runs, even if the user is not found.
// The dummy password hash is used on the no-rows path.
func (s *Service) UpdatePasswordHash(ctx context.Context, in ChangePasswordInput) error {
```

**Inline comment (at the dummy-hash call site):**

```go
// Timing invariant: always run CheckPassword, even on no-rows, to prevent
// timing-based email enumeration (§3.7).
var pwHash string
if lookupErr == nil {
    pwHash = creds.PasswordHash
} else {
    pwHash = authshared.GetDummyPasswordHash() // constant-time placeholder
}
pwErr := authshared.CheckPassword(pwHash, in.OldPassword)
```

---

### 4.8 Numbered Steps in Transaction Methods

`*Tx` methods that execute more than two DB calls use numbered inline comments to make
the transaction sequence scannable. The doc comment on the method summarises the steps
in prose — it does NOT duplicate the numbered list.

```go
// LoginTx runs the post-authentication persistence work inside a single transaction:
// creates a session row, issues a refresh token, stamps last_login_at, and writes
// the audit log.
func (s *Store) LoginTx(ctx context.Context, in LoginTxInput) (LoggedInSession, error) {
    h, err := s.BeginOrBind(ctx)
    ...

    // 1. Open a session row.
    sessionRow, err := h.Q.CreateUserSession(ctx, ...)

    // 2. Issue a root refresh token tied to the session.
    tokenRow, err := h.Q.CreateRefreshToken(ctx, ...)

    // 3. Stamp last_login_at.
    if err := h.Q.UpdateLastLoginAt(ctx, userPgUUID); err != nil {

    // 4. Audit log.
    if err := h.Q.InsertAuditLog(ctx, ...); err != nil {

    return h.Commit()
}
```

The doc comment lists the steps in prose. The inline numbered comments label them in
the body. They stay in sync — if you add a step, add it to both.

---

### 4.9 Blocking Method Annotations

Any method that blocks until context cancellation must state this in its doc comment
and show the goroutine launch pattern. The code example uses `//` with a tab indent
(standard godoc example format). Do not use a fenced code block inside a doc comment.

```go
// StartCleanup evicts expired entries on each tick. It blocks until ctx is cancelled.
// Run it in a goroutine:
//
//	go store.StartCleanup(ctx)
func (s *InMemoryStore) StartCleanup(ctx context.Context) { ... }
```

---

### 4.10 Compile-Time Interface Checks

The line `var _ Storer = (*Store)(nil)` does not need a doc comment. One brief inline
comment is sufficient:

```go
// compile-time check that *Store satisfies Storer.
var _ Storer = (*Store)(nil)

// compile-time interface check.
var _ db.Querier = (*QuerierProxy)(nil)
```

Use the same phrasing in every file. Do not use "static assertion" or "type assertion
check" — those mean different things in Go.

---

### 4.11 Section Separators

Use a ruled separator when a file contains distinct logical groups that are large enough
to get lost in:

```go
// ── Cross-feature sentinel errors ─────────────────────────────────────────────

var ErrUserNotFound = errors.New("user not found")

// ── Input-validation sentinel errors ──────────────────────────────────────────

var ErrEmailEmpty = errors.New("email is required")

// ── Typed errors ───────────────────────────────────────────────────────────────

type LoginLockedError struct { ... }
```

Use `──` (U+2500) + space + title + space + `─` (repeated to ~80 chars). Keep titles as
short noun-phrases. Use sparingly — if a file needs more than three sections, consider
splitting it.

**Do not** use `//===`, `//---`, `//***`, or any other separator style.

---

### 4.12 FakeStorer, FakeServicer, and QuerierProxy Comments

**`shared/testutil/fake_storer.go`** and **`shared/testutil/fake_servicer.go`** — package doc comment:

```go
// Package {domain}sharedtest provides test-only helpers shared across all
// {domain} feature sub-packages. It must never be imported by production code.
package {domain}sharedtest
```

`{Feature}FakeStorer` struct doc comment:

```go
// ProfileFakeStorer is a hand-written implementation of profile.Storer for
// service unit tests. Each method delegates to its Fn field if non-nil;
// otherwise it returns the zero value and nil error so tests only configure
// the fields they need.
type ProfileFakeStorer struct { ... }
```

`{Feature}FakeServicer` struct doc comment:

```go
// LoginFakeServicer is a hand-written implementation of login.Servicer for
// handler unit tests. Each method delegates to its Fn field if non-nil,
// otherwise returns the zero value and nil error.
type LoginFakeServicer struct { ... }
```

Each method gets a one-liner:

```go
// GetUserProfile delegates to GetUserProfileFn if set.
func (f *ProfileFakeStorer) GetUserProfile(ctx context.Context, userID [16]byte) (profile.UserProfile, error) {
```

**`shared/testutil/querier_proxy.go`** — `ErrProxy` and `QuerierProxy` doc comments:

```go
// ErrProxy is the sentinel error returned by any QuerierProxy when a Fail* flag is set.
var ErrProxy = errors.New("querier_proxy: injected error")

// QuerierProxy implements all db.Querier methods, delegating each call to
// Base unless the corresponding Fail* flag is set — in that case ErrProxy is
// returned immediately without calling Base. Fail* flags are grouped by
// feature with section separator comments.
type QuerierProxy struct { ... }
```

Feature groups in `querier_proxy.go` use the standard section separator style:

```go
// ── login ────────────────────────────────────────────────────────────────────
FailGetUserForLogin bool

// ── profile ──────────────────────────────────────────────────────────────────
FailGetUserProfile bool
```

**Note on section separators in testutil files:** `fake_storer.go` and
`fake_servicer.go` use a full-width title-bar style (two lines: a full-width
rule, then the struct name) instead of the standard `// ── Title ───` style.
This deviation is intentional for these two files only. See the auth reference
implementation and [`docs/rules/auth.md §5.9`](rules/auth.md#59-section-separator-style-in-testutil).

---

### 4.13 Routes Comments

`Mount` and `Routes` each get a doc comment. The call pattern shown in godoc
uses tab-indented code blocks.

**Domain root assemblers** — `Mount` owns the path and calls `Routes`:

```go
// Mount registers the auth sub-router at /auth on r.
// Call from server/routes.go inside the /api/v1 route group:
//
//	auth.Mount(ctx, r, deps)
func Mount(ctx context.Context, r chi.Router, deps *app.Deps) {
	r.Mount("/auth", Routes(ctx, deps))
}

// Routes returns a self-contained chi sub-router for all /auth endpoints.
// Called by Mount. Use directly in tests to exercise the full domain routing.
//
//	r.Mount("/auth", auth.Routes(ctx, deps))
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux { ... }
```

**Feature sub-packages** accept a `chi.Router` and return nothing:

```go
// Routes registers the login endpoint on r.
// Call from the auth root assembler:
//
//	login.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST /login: 12 req / 15 min per IP
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) { ... }
```

**Cross-namespace admin helpers** (`Register{Scope}Routes`) show the full
server-side call site so the reader can see the group they contribute to:

```go
// RegisterAdminRoutes registers the RBAC catalog endpoints on r.
// Call from server/routes.go inside the /admin route group:
//
//	r.Route("/admin", func(r chi.Router) {
//		r.Use(chimiddleware.AllowContentType("application/json"))
//		rbacdomain.RegisterAdminRoutes(ctx, r, deps) // /roles/*, /permissions/*
//		admindomain.Routes(ctx, r, deps)             // /users/*
//	})
func RegisterAdminRoutes(ctx context.Context, r chi.Router, deps *app.Deps) { ... }
```

**Package comment rules for `routes.go`:**
- Every domain-level `routes.go` must have a package comment starting with
  `// Package {name} assembles the {name} domain sub-router.`
- Package comments must NOT carry call patterns or rate-limit tables — those
  belong on the `Routes` (or `Register*`) func doc.
- Feature sub-package `routes.go` package comments state what endpoints the
  package registers, in one sentence:
  ```go
  // Package login registers the POST /login endpoint.
  package login
  ```

Rate-limiter variables are unexported locals. Inline comments on each limiter
construction call name the attack it defends against:

```go
// 12 req / 15 min per IP — burst kept above the per-user lockout threshold
// so IP limiting never fires before the account lockout path is reachable.
// rate = 12 / (15 * 60) = 0.01333 tokens/sec.
ipLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "lgn:ip:", 12.0/(15*60), 12, 15*time.Minute)
go ipLimiter.StartCleanup(ctx)
```

**Import order in `routes.go`** follows the standard three-group rule:
1. stdlib (`"context"`, `"net/http"`, `"time"`)
2. third-party (`"github.com/go-chi/chi/v5"`, `"github.com/go-chi/chi/v5/middleware"`)
3. internal (`"github.com/7-Dany/store/backend/internal/..."`)

---

### 4.14 What to Omit

- No doc comments on unexported types/functions unless the logic would confuse a competent reader.
- No restating comments — if the code says `cost := bcrypt.DefaultCost`, do not write `// set cost to default`.
- No implementation-detail doc comments — describe the contract, not the body.
- No commented-out code — delete it; Git keeps history.
- No bare `// TODO` without a tracked issue reference:
  ```go
  // TODO(#142): replace with a circuit-breaker.  ← good
  // TODO: fix this later                          ← banned
  // FIXME: this is broken                         ← banned
  ```
- No separator lines (`//---`, `//===`, `//***`) — use the `// ──` style (§4.11) or nothing.
- No `// nolint` comments without a reason:
  ```go
  //nolint:exhaustruct // optional fields have safe zero values
  ```
- No joke or sarcastic comments in production files.

---

### 4.15 Comment Checklist

Use during code review of any new or modified file.

**Package comment:**
- [ ] Present on the package declaration in the primary file.
- [ ] Starts with `Package {name}`.
- [ ] One or two sentences; no design rationale.

**Exported identifiers:**
- [ ] Every exported type, function, method, var, const has a doc comment.
- [ ] Doc comment starts with the exact identifier name.
- [ ] Non-obvious error returns are documented.
- [ ] Non-obvious side effects (sessions revoked, audit row written) are documented.

**Inline comments:**
- [ ] Explain *why*, not *what*.
- [ ] Security-critical lines have a `// Security:` comment.
- [ ] Timing invariant dummy-hash call sites have an inline comment.
- [ ] Multi-step Tx methods use numbered step comments.

**Form:**
- [ ] No commented-out code.
- [ ] No bare `// TODO` / `// FIXME`.
- [ ] Section separators use the `// ──` style, not `//---` or `//===`.
- [ ] Compile-time checks use the standard one-liner comment.
- [ ] Doc comment code examples use `//` + tab indent, not fenced code blocks.

---

## 5. Architecture Decisions (ADRs)

This section records the non-obvious design choices in this codebase. Each entry
explains what was decided, why, and what the consequences are. When a rule in §3
(Conventions) seems strange, the explanation is here.

Entries are permanent. A decision is not removed when it is superseded — a new entry
is added that references the old one and explains what changed and why.

---

### ADR-001 — JWT signing belongs in the handler, not the service

**Context:** The service performs login and token rotation. It calls `LoginTx` which returns session metadata. Somewhere between that DB write and the HTTP response, a signed JWT must be produced.

**Decision:** The handler is responsible for JWT signing. The service returns raw `[16]byte` and `time.Time` values. The handler calls `platform/token.GenerateAccessToken(...)` and `GenerateRefreshToken(...)`.

**Why not in the service:**
- A service that holds JWT secrets cannot be tested without real secrets.
- JWT format is a presentation-layer decision. Changing to PASETO should not touch business logic.
- Services must be importable by future background jobs or CLIs without pulling in the JWT library.

**Consequence:** A signing failure after a committed `LoginTx` leaves an orphaned session row. This is logged at ERROR. It is acceptable because HMAC-SHA256 signing cannot fail unless the secret is empty, which is caught at startup by `NewHandler` panicking on an empty secret.

---

### ADR-002 — Three-layer architecture with strict boundary contracts

**Context:** The question is how strictly to enforce the contracts between handler, service, and store.

**Decision:** The boundaries are absolute. `pgtype.*` never appears above the store boundary. `http.*` never appears below the handler boundary.

**Why absolute:** Partial enforcement produces the worst outcome — the codebase looks layered but is not. A store that leaks one `pgtype.Timestamptz` into the service means every future feature must decide whether to leak or not. Within two years the service is full of pgtype casts.

**Consequence:** Some boilerplate exists at the store boundary. This is intentional — a future migration from pgx v5 to a different driver touches only `store.go`.

---

### ADR-003 — The `txBound` / `WithQuerier` pattern for test isolation

**Context:** Store integration tests need to scope all DB writes to a single transaction that rolls back at test cleanup. But `*Tx` methods open their own transactions internally.

**Decision:** `Store` has a `txBound bool` flag. When true, `beginOrBind` returns the injected `db.Querier` with no-op commit and rollback. When false, it opens a real transaction from the pool.

**Exception:** `IncrementAttemptsTx` and `IncrementLoginFailuresTx` always open a fresh pool transaction, ignoring `txBound`. These must commit independently even if the caller's transaction rolls back.

**Consequence:** Tests require `DB_MAX_CONNS >= 20`. `VerifyEmailTx` holds a FOR UPDATE lock while `checkFn` runs. `IncrementAttemptsTx` needs a separate pool connection to UPDATE the same row after the lock is released. With the default pgxpool maximum of 4 connections, this deadlocks in tests.

---

### ADR-004 — `context.WithoutCancel` for security-critical writes

**Context:** An HTTP request context is cancelled when the client disconnects. DB writes using a cancelled context return `context.Canceled` and do not reach PostgreSQL.

**Decision:** Writes that must not be skippable by a client disconnect use `context.WithoutCancel(ctx)`: OTP attempt counters, login failure counters, login failed audit rows, token family revocation, password change and session revocation.

**Why:** An attacker who can abort a network request at the right moment could submit unlimited wrong OTP codes without consuming their attempt budget.

**Consequence:** These writes may complete after the HTTP handler has returned. If a write fails, the error is logged but not returned to the caller — the client should not receive information about whether a security counter was successfully updated.

---

### ADR-005 — OTP consumption uses a checkFn closure to avoid deadlocks

**Context:** The OTP verification flow needs to: (1) lock the token row, (2) check the code, (3) consume the token, and (4) on wrong code, increment the attempt counter. Steps 1-3 must be atomic. Step 4 must happen after the lock is released.

**Decision:** `VerifyEmailTx` accepts a `checkFn func(VerificationToken) error` closure. The store opens a transaction, locks the row, calls `checkFn`, and if it returns nil proceeds to consume the token. The service calls `IncrementAttemptsTx` after `VerifyEmailTx` returns.

**Why not call IncrementAttemptsTx inside checkFn:** `IncrementAttemptsTx` opens an independent transaction and issues an `UPDATE` on the same row currently locked by `FOR UPDATE`. This creates a PostgreSQL row-level lock deadlock that hangs forever.

**Consequence:** There is a narrow window where a client could see `ErrInvalidCode` but the counter has not yet incremented (e.g. if the process crashes between the two transactions). This means an attacker gets at most one extra free attempt during a crash, which is not exploitable in practice.

---

### ADR-006 — Anti-enumeration: uniform 202 + timing equalization

**Context:** Endpoints that accept an email address must not reveal whether the email exists in the system.

**Decision:** Two techniques are used together: (1) resend/unlock/forgot-password endpoints always return `202 Accepted` with the same body regardless of whether the email exists; (2) endpoints that compare a code or password run the comparison even on the no-rows path, against a precomputed dummy hash.

**Why timing matters:** If the no-rows path returns in 1ms and the wrong-code path returns in 300ms, an attacker can determine which emails are registered by measuring response times from any network.

**Consequence:** Legitimate users cannot distinguish "email not found" from "email found but code was wrong" on resend and unlock endpoints. This is acceptable — it prevents enumeration without affecting the user's ability to complete the flow.

---

### ADR-007 — One `Storer` interface per domain, defined in `service.go`

**Context:** The service needs a way to call the store without depending on the concrete `*Store` type.

**Decision:** Each domain's `service.go` defines a single `Storer` interface listing all methods the service calls. The concrete `*Store` satisfies this interface. Tests inject a hand-written fake.

**Why one interface:** Many small interfaces (`TokenGetter`, `SessionCreator`) create naming confusion and make it harder to see at a glance what the service depends on.

**Why in service.go:** The interface and the service that uses it change for the same reason. Splitting them means every new service method requires changes to two files with no benefit.

**Consequence:** As the service grows, `Storer` grows. A `Storer` with 30+ methods is a signal the domain is doing too much and should be split.

---

### ADR-008 — Audit event constants in `internal/audit`, not inline strings

**Context:** `store.go` calls `InsertAuditLog` in many places, each with a string event name.

**Decision:** All event name strings are constants in `internal/audit/audit.go`. No string literal event name appears in any store file.

**Why:** A typo in an audit event name (`"login_faild"`) compiles, runs, produces no error, and writes a silently wrong audit trail. A typed constant fails at compile time.

**Consequence:** Adding a new audit event requires two changes: add the constant, then use it. This makes `audit/audit.go` a complete inventory of every event the system emits.

---

### ADR-009 — Shared KV store instance for all rate limiters in a domain

**Context:** Each domain routes file creates multiple rate limiters. Each could have its own Redis connection pool.

**Decision:** One `kvstore.Store` instance is created per domain routes file and shared across all rate limiters and the token blocklist.

**Why:** Opening one Redis connection pool per limiter with 6 limiters plus a blocklist would open 7 pools. One pool per domain is sufficient because limiters use distinct key prefixes (`ip:`, `backoff:`, `blocklist:`).

**Consequence:** A transient Redis error affects all rate limiters simultaneously. Each limiter falls back to its local in-memory state — the system degrades gracefully to single-instance rate limiting rather than failing open.

---

### ADR-010 — Domain packages never import each other

**Context:** Auth needs to know if a user has an RBAC role before serving certain endpoints. The naive approach is `auth` importing `rbac`.

**Decision:** Domain packages never import each other. Cross-domain dependencies are resolved by: (1) adding the needed data as a field to an existing DB query result; (2) injecting a read-only interface via `routes.go`; or (3) using a shared table in `internal/db`.

**Why:** An `auth → rbac` import creates a build cycle risk and logical coupling. When the RBAC schema changes, the auth package must be recompiled and retested for every change.

**Consequence:** Some patterns that feel natural (calling `rbac.GetUserRole(userID)` from the auth service) require slightly more ceremony. This is the correct tradeoff. The boundary is the architecture.

---

### ADR-011 — Token family revocation on reuse detection

**Context:** Refresh tokens rotate on every use. If a client presents a token that has already been rotated, it means either a legitimate client is replaying a stale token, or an attacker has obtained and is using a previously consumed token.

**Decision:** When a revoked refresh token is presented to `/auth/refresh`, the entire token family (all tokens sharing the same `family_id`) is revoked atomically. The user is forced to re-authenticate.

**Why revoke the whole family:** If an attacker obtained token generation N and the legitimate client has rotated to N+1, revoking only generation N accomplishes nothing. Revoking the family forces re-authentication regardless of which generation the attacker holds.

**Consequence:** Legitimate clients that retry a rotation request after a network timeout will find their entire family revoked. They must re-login. This is acceptable — the alternative (allowing retry within a window) requires tracking rotation timestamps and introduces race conditions.

---

### ADR-012 — `models.go` contains only service-layer I/O types

**Context:** The auth package has HTTP request structs (with `json:` tags), service I/O types (plain Go structs), and store I/O types. All need to live somewhere.

**Decision:** HTTP request/response structs with `json:` tags go in `requests.go`. Service-layer I/O types go in `models.go`. Store-internal types are declared inline in `store.go`.

**Why split requests.go from models.go:** HTTP structs change when the API contract changes. Service types change when the business logic changes. These are different stakeholders with different change frequencies. A single file that contains both means every API change touches the same file as every business logic change.

**Consequence:** A new service method requires changes to `models.go` and `service.go`. A new endpoint requires changes to `requests.go` and `handler.go`. These pairs change together; the files are sized to that change unit.
