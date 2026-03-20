# Prerequisites — rbac Additions

> **Package:** `internal/platform/rbac/`
> **Files affected:** `checker.go`
>
> **Status:** Must be merged before any bitcoin domain code is written.
> **Depends on:** Nothing — constant additions only.
> **Blocks:** Nothing in Stage 0 (not yet enforced). Stage 2 routes will reference
>   these constants when access control is wired.

---

## Overview

Stage 0 does not gate bitcoin endpoints behind RBAC — any authenticated user can
call `/watch`, `/events/token`, `/events`, and `/status`. These constants are added
pre-emptively so that when Stage 2 introduces access control, developers reference
typed constants rather than raw string literals that are easy to mistype and
impossible to grep reliably.

No logic changes. No changes to `checker.go` beyond adding the constants.

---

## New Constants

Add to the existing `const` block in `checker.go` alongside existing `Perm*` constants:

```go
// Bitcoin payment domain permissions.
// Stage 0: not yet enforced — all authenticated users may access bitcoin endpoints.
// Stage 2+: apply rbac.Require(rbac.PermBitcoinWatch) to POST /watch and GET /events.
const (
    PermBitcoinWatch  = "bitcoin:watch"   // register addresses for SSE notification
    PermBitcoinStatus = "bitcoin:status"  // read ZMQ subscriber health (GET /status)
    PermBitcoinManage = "bitcoin:manage"  // admin: adjust watch limits, flush caches
)
```

---

## Stage 2 Pre-Launch Checklist

When `rbac.Require(PermBitcoin*)` is wired on any route in Stage 2:

1. Add the corresponding rows to the DB permissions seed migration
   (`db/migrations/` or `db/seed/permissions.sql`) before the route is wired.
2. Add an integration test (or startup check) asserting that if
   `rbac.Require(PermBitcoinWatch)` is wired, the `bitcoin:watch` row exists in
   the DB permissions table. A missing seed migration causes a silent global 403
   on all bitcoin endpoints in any environment that skips the migration.
3. Add "rbac-seed-migration-verified" to the Stage 2 pre-launch hard-blocker checklist.

---

## No Test Inventory

No new logic — pure constant additions. Existing `rbac` tests remain unchanged.
The Stage 2 DB assertion test is added in the Stage 2 implementation, not here.
