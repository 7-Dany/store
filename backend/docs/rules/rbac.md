# RBAC Domain Rules

**Reference implementation:** `internal/domain/rbac`  
**Last updated:** 2026-03

Read `docs/RULES.md` first. This file documents only what is specific to the
RBAC domain: its feature set, concrete decisions, and patterns that deviate
from or extend the global rules.

See `docs/rules/auth.md` for a complete worked example of this format.

---

## Table of Contents

1. [Conflicts and Clarifications](#1-conflicts-and-clarifications)
2. [Domain Structure](#2-domain-structure)
   - 2.1 [Feature Sub-Packages](#21-feature-sub-packages)
   - 2.2 [Shared Package (`rbacshared`)](#22-shared-package-rbacshared)
   - 2.3 [Testutil Package (`rbacsharedtest`)](#23-testutil-package-rbacsharedtest)
3. [Code Flow Traces](#3-code-flow-traces)
   - 3.1 [Assign User Role (PUT /admin/rbac/users/{user_id}/role)](#31-assign-user-role)
   - 3.2 [Remove User Role (DELETE /admin/rbac/users/{user_id}/role)](#32-remove-user-role)
4. [Domain-Specific Conventions](#4-domain-specific-conventions)
   - 4.1 [Global Middleware on the RBAC Router](#41-global-middleware-on-the-rbac-router)
   - 4.2 [Handler-Layer Validation + Defence-in-Depth Service Validation](#42-handler-layer-validation--defence-in-depth-service-validation)
   - 4.3 [WithActingUser SET LOCAL Pattern](#43-withactinguser-set-local-pattern)
   - 4.4 [One-Role-Per-User Invariant](#44-one-role-per-user-invariant)
   - 4.5 [BeginOrBind Transaction Pattern in AssignUserRoleTx](#45-beginorbind-transaction-pattern-in-assignuserroletx)
   - 4.6 [GetUserRole SQL Omits granted_by](#46-getuserrole-sql-omits-granted_by)
   - 4.7 [Tx Suffix on Mutating Store Methods](#47-tx-suffix-on-mutating-store-methods)
5. [Domain-Specific ADRs](#5-domain-specific-adrs)

---

## 1. Conflicts and Clarifications

| # | RULES.md says | RBAC actually does | Resolution |
|---|---|---|---|
| C-01 | §3.1 "Every domain package contains exactly these files" listing `validators.go` | `permissions` has no `validators.go`; `bootstrap` and `userroles` do | `validators.go` is conditional — create it only when the feature has feature-exclusive input validators. |
| C-02 | §3.10 "Validate input in the service layer" | `userroles` validates in the handler first, then re-validates in the service as defence-in-depth | Handler-first is the preferred pattern for this domain; see §4.2. |
| C-03 | §3.11 "Mutating store methods use the {Action}Tx suffix" | `userroles.Store.AssignUserRoleTx` follows the rule; earlier code used `AssignUserRole` | The rename is retroactive. All new mutating store methods must use the Tx suffix. |

---

## 2. Domain Structure

### 2.1 Feature Sub-Packages

```
internal/domain/rbac/
├── routes.go           # package rbac — root assembler only; returns *chi.Mux
├── shared/             # package rbacshared
└── {feature}/          # one sub-package per feature
```

**Currently implemented features:**

| Package | HTTP Endpoints | Notes |
|---|---|---|
| `bootstrap` | `POST /owner/bootstrap` | Self-grants owner role; only works when no active owner exists |
| `permissions` | `GET /admin/rbac/permissions`, `GET /admin/rbac/permission-groups` | Read-only permission catalogue |
| `roles` | `GET/POST /admin/rbac/roles`, `GET/PATCH/DELETE /admin/rbac/roles/{id}`, `GET/POST/DELETE /admin/rbac/roles/{id}/permissions/{perm_id}` | Full CRUD for non-system roles |
| `userroles` | `GET /admin/rbac/users/{user_id}/role`, `PUT /admin/rbac/users/{user_id}/role`, `DELETE /admin/rbac/users/{user_id}/role` | One-role-per-user assignment |

All `/admin/*` endpoints require `Content-Type: application/json` (enforced by
`chimiddleware.AllowContentType` in the admin sub-router — see §4.1).

---

### 2.2 Shared Package (`rbacshared`)

`internal/domain/rbac/shared/` (package `rbacshared`) holds everything that
more than one feature sub-package needs.

```
shared/
├── errors.go      # Cross-feature sentinel: ErrUserNotFound
├── store.go       # BaseStore: pool, BeginOrBind, TxHelpers, conversion helpers,
│                  #   WithActingUser, IsNoRows, IsDuplicateEmail, etc.
├── validators.go  # Shared validators: ErrUserIDEmpty
└── testutil/      # package rbacsharedtest
```

---

### 2.3 Testutil Package (`rbacsharedtest`)

`internal/domain/rbac/shared/testutil/` (package `rbacsharedtest`).
Must never be imported by production code.

| File | Contents |
|---|---|
| `fake_storer.go` | One `{Feature}FakeStorer` per feature |
| `fake_servicer.go` | One `{Feature}FakeServicer` per feature |
| `querier_proxy.go` | `QuerierProxy` + `ErrProxy` sentinel |
| `builders.go` | Pool creation, `MustBeginTx`, seed helpers, `RunTestMain` |

---

## 3. Code Flow Traces

### 3.1 Assign User Role

```
HTTP Client
    │  PUT /api/v1/admin/rbac/users/{user_id}/role
    │  Body: { role_id, granted_reason, expires_at? }
    ▼
rbac/routes.go  adminRoutes()
    │  r.Use(chimiddleware.AllowContentType("application/json"))
    │  userroles.Routes(ctx, r, deps)
    ▼
userroles/handler.go  h.AssignRole()
    │  1. chi.URLParam → userID
    │  2. token.UserIDFromContext → actingUserID (401 if absent)
    │  3. respond.DecodeJSON[assignRoleRequest] → req
    │  4. validateAssignRole(in) → 422 role_id_required / granted_reason_required
    │  5. h.svc.AssignRole(ctx, userID, actingUserID, in)
    │  6. error switch → 409 / 422 / 500
    │  7. success: respond.JSON 200 userRoleResponse
    ▼
userroles/service.go  s.AssignRole()
    │  1. parseID(targetUserID)        → ErrUserRoleNotFound on bad UUID
    │  2. parseID(actingUserID)        → 500 on bad UUID (JWT misconfiguration)
    │  3. targetID == actorID          → ErrCannotModifyOwnRole
    │  4. validateAssignRole(in)       → defence-in-depth (handler already validated)
    │  5. parseID(in.RoleID)           → ErrRoleNotFound on bad UUID
    │  6. store.GetUserRole(targetID)  → if IsOwnerRole → ErrCannotReassignOwner
    │  7. store.AssignUserRoleTx(...)  → ErrRoleNotFound if role inactive/missing
    ▼
userroles/store.go  s.AssignUserRoleTx()
    │  BeginOrBind(ctx) → TxHelpers{Q, Commit, Rollback}
    │  1. Q.GetRoleByID(roleID)   → ErrRoleNotFound if no-rows or !is_active
    │  2. Q.AssignUserRole(...)   → upsert (ON CONFLICT user_id DO UPDATE)
    │  3. Q.GetUserRole(userID)   → re-read for role_name, is_owner_role, granted_reason
    │  Commit()
    ▼
internal/db/  (sqlc-generated)
    ▼
PostgreSQL
```

**Handler error mapping for AssignRole:**

| Sentinel | HTTP Status | Code string |
|---|---|---|
| `ErrRoleIDEmpty` (handler, pre-service) | 422 | `role_id_required` |
| `ErrGrantedReasonEmpty` (handler, pre-service) | 422 | `granted_reason_required` |
| `platformrbac.ErrCannotModifyOwnRole` | 409 | `cannot_modify_own_role` |
| `platformrbac.ErrCannotReassignOwner` | 409 | `cannot_reassign_owner` |
| `ErrRoleNotFound` | 422 | `role_not_found` |
| anything else | 500 (logged) | `internal_error` |

---

### 3.2 Remove User Role

```
userroles/handler.go  h.RemoveRole()
    │  1. chi.URLParam → userID
    │  2. token.UserIDFromContext → actingUserID (401 if absent)
    │  3. h.svc.RemoveRole(ctx, userID, actingUserID)
    │  4. error switch → 409 / 404 / 500
    │  5. success: respond.NoContent 204
    ▼
userroles/service.go  s.RemoveRole()
    │  1. parseID(targetUserID)        → ErrUserRoleNotFound on bad UUID
    │  2. parseID(actingUserID)        → 500 on bad UUID
    │  3. targetID == actorID          → ErrCannotModifyOwnRole
    │  4. store.GetUserRole(targetID)  → propagates ErrUserRoleNotFound
    │  5. existing.IsOwnerRole         → ErrCannotReassignOwner
    │  6. store.RemoveUserRole(...)    → ErrLastOwnerRemoval from DB trigger
    ▼
userroles/store.go  s.RemoveUserRole()
    │  WithActingUser(ctx, actingUserID, fn):
    │    SET LOCAL rbac.acting_user = actingUserID
    │    Q.RemoveUserRole(userID)
    │    Clear rbac.acting_user
    │  rowsAffected == 0 → ErrUserRoleNotFound
    │  isOrphanedOwnerViolation(err) → ErrLastOwnerRemoval
```

**Handler error mapping for RemoveRole:**

| Sentinel | HTTP Status | Code string |
|---|---|---|
| `platformrbac.ErrCannotModifyOwnRole` | 409 | `cannot_modify_own_role` |
| `platformrbac.ErrCannotReassignOwner` | 409 | `cannot_reassign_owner` |
| `ErrUserRoleNotFound` | 404 | `user_role_not_found` |
| `ErrLastOwnerRemoval` | 409 | `last_owner_removal` |
| anything else | 500 (logged) | `internal_error` |

---

## 4. Domain-Specific Conventions

### 4.1 Global Middleware on the RBAC Router

The RBAC admin sub-router (`rbac/routes.go` `adminRoutes`) applies:

```go
r.Use(chimiddleware.AllowContentType("application/json"))
```

Every admin endpoint consumes JSON. The owner sub-router applies the same
middleware for the bootstrap endpoint. There is no unauthenticated non-JSON
route in this domain.

---

### 4.2 Handler-Layer Validation + Defence-in-Depth Service Validation

`userroles` performs validation at two levels:

1. **Handler** (`handler.go AssignRole`): calls `validateAssignRole` immediately
   after `respond.DecodeJSON`. Validation failures return 422 before any service
   call is made. This is the authoritative validation for HTTP clients.

2. **Service** (`service.go AssignRole`): calls `validateAssignRole` again with
   a `// Defence-in-depth` comment. This protects callers that bypass the HTTP
   layer (tests, future CLI tools, background jobs).

The service-level call returns the same sentinels (`ErrRoleIDEmpty`,
`ErrGrantedReasonEmpty`). Any future non-HTTP caller will see a meaningful
error rather than a DB constraint violation.

**Do not remove the service-level call.** Do not remove the handler-level call.
Both exist for independent reasons.

---

### 4.3 WithActingUser SET LOCAL Pattern

All hard-delete operations on `user_roles` (and `role_permissions`) must call
`rbacshared.BaseStore.WithActingUser(ctx, actingUserID, fn)` before the DELETE.
This issues `SET LOCAL rbac.acting_user = actingUserID` so the audit trigger
(`fn_audit_user_roles`) records the correct deletion actor.

**Why `SET LOCAL` and not a parameter:** The audit trigger reads
`current_setting('rbac.acting_user', true)` and cannot accept parameters. The
session variable is the only way to pass the actor into the trigger.

**Constraint (V1):** `SET LOCAL` is transaction-scoped in PostgreSQL. On the
V1 single-statement autocommit path, it behaves as `SET SESSION`. The
`WithActingUser` implementation clears the variable after the DELETE as a
belt-and-suspenders measure for long-lived connections. See the `WithActingUser`
doc comment in `shared/store.go` for the full trade-off analysis.

---

### 4.4 One-Role-Per-User Invariant

The `user_roles` table has a PRIMARY KEY on `user_id`, enforcing that each user
holds at most one role at a time. The upsert query (`AssignUserRole`) uses
`ON CONFLICT (user_id) DO UPDATE` to atomically replace an existing assignment.

**Service-layer consequence:** `AssignRole` does not need to check for an
existing non-owner role before upserting — the DB handles replacement atomically.
The owner guard (`step 6`) is the only pre-upsert existence check.

---

### 4.5 BeginOrBind Transaction Pattern in AssignUserRoleTx

`AssignUserRoleTx` wraps three DB calls (GetRoleByID, AssignUserRole,
GetUserRole) in a single transaction using `BeginOrBind`. This prevents a TOCTOU
race where the role is deactivated between the existence check and the upsert.

In integration tests, `WithQuerier(q)` sets `TxBound = true`. `BeginOrBind`
then returns the injected querier with no-op Commit/Rollback, so all writes
remain inside the outer rolled-back test transaction.

In production, `BeginOrBind` opens a real pool transaction.

---

### 4.6 GetUserRole SQL Omits granted_by

The `GetUserRole` SQL query (`sql/queries/rbac.sql`) does not SELECT
`ur.granted_by`. The `GetUserRoleRow` struct therefore has no `GrantedBy` field,
and `UserRole.GrantedBy` has been intentionally removed.

**Rationale:** The endpoint that reads a user's role (`GET /role`) is primarily
consumed by authorization checks and admin UIs that need the role name and
expiry. The granter identity is an audit concern, not a display concern. It is
available in the audit log.

**If future requirements need it:** Add `ur.granted_by` to the `GetUserRole`
query, regenerate sqlc, add `GrantedBy string` back to `UserRole`, map it in
`mapUserRole`, and expose it in `userRoleResponse`.

---

### 4.7 Tx Suffix on Mutating Store Methods

Per RULES.md §3.11, all mutating `Store` methods that run one or more DB
writes must use the `{Action}Tx` suffix (e.g., `AssignUserRoleTx`,
`BootstrapOwnerTx`). This makes transaction boundaries visible at the call site.

Read-only methods (`GetUserRole`, `GetRoleByID`, etc.) do not use the suffix.

---

## 5. Domain-Specific ADRs

---

### ADR-RBAC-01 — Owner role cannot be reassigned via userroles endpoints

**Context:** The owner role is the bootstrap superuser role. Allowing normal
admin workflows to replace an existing owner's role (by upserting a different
role_id) could inadvertently strip ownership from the system's last owner.

**Decision:** `userroles.Service.AssignRole` checks the target user's current
role before the upsert. If the target already holds an owner role,
`platformrbac.ErrCannotReassignOwner` is returned (HTTP 409). The endpoint
cannot be used to downgrade an owner to a non-owner role. Owner role management
is exclusively the domain of the `bootstrap` package.

**Why not a DB constraint:** A DB-level trigger would fire with a generic
`23000` error that is difficult to distinguish from `ErrLastOwnerRemoval`. An
application-level guard produces a clear sentinel that maps to a precise HTTP
status code and error code.

**Consequence:** To change an owner's role, an operator must use a direct DB
migration or a future dedicated owner-transfer endpoint. This is intentional.

---

### ADR-RBAC-02 — fn_prevent_orphaned_owner as last-resort DB backstop

**Context:** The service-level owner guard (`ErrCannotReassignOwner`) prevents
replacing an owner's role via the HTTP API. However, direct DB access, future
service bypass paths, or bugs could still attempt to delete the last owner row.

**Decision:** The PostgreSQL trigger `fn_prevent_orphaned_owner` fires
`BEFORE DELETE ON user_roles` and raises `SQLSTATE 23000` with message
`"cannot remove last active owner"` if the delete would leave the system with
no active owner role assignment.

The store detects this via `isOrphanedOwnerViolation(err)` and maps it to
`ErrLastOwnerRemoval`. The handler maps `ErrLastOwnerRemoval` to HTTP 409
`last_owner_removal`.

**Why two layers:** The service-layer owner guard and the DB trigger serve
different threat models. The service guard handles normal HTTP clients with
meaningful error messages. The DB trigger is a correctness backstop that
protects against all callers regardless of how they reach the DB.

**Consequence:** Integration tests for `ErrLastOwnerRemoval` require a DB
without a bootstrapped owner (the trigger only fires when removing the last
owner). Tests that run against a seeded DB skip with
`t.Skip("bootstrapped owner exists")`.

---

### ADR-RBAC-03 — is_active check applied in AssignUserRoleTx, not in SQL

**Context:** The `GetRoleByID` SQL query does not filter by `is_active`. Other
callers (e.g., the roles admin package) intentionally inspect inactive roles to
display their history.

**Decision:** `store.AssignUserRoleTx` fetches the role via `GetRoleByID` and
then explicitly checks `role.IsActive`. If false, it returns `ErrRoleNotFound`
before the upsert.

**Why not modify the SQL:** Changing `GetRoleByID` to filter `is_active = TRUE`
would break existing callers that need to read inactive roles. Adding a second
`GetActiveRoleByID` query would duplicate SQL for a single use case.

**Consequence:** A role deactivated between the `GetRoleByID` check and the
`AssignUserRole` upsert within the same transaction cannot sneak through,
because both calls run inside the same `BeginOrBind` transaction and the role
row is not modified between them.
