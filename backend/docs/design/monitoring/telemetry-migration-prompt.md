# Telemetry Migration Prompt

> Use this prompt in a fresh session for each domain package.
> Copy the template below, fill the two placeholders, and paste it.

---

## Template

```
Migrate `internal/domain/{DOMAIN}/{PACKAGE}/` to the telemetry package.

Read these files before doing anything:
1. `internal/domain/{DOMAIN}/{PACKAGE}/service.go`
2. `internal/domain/{DOMAIN}/{PACKAGE}/handler.go`
3. `internal/domain/{DOMAIN}/{PACKAGE}/store.go`
   (and any extra files: routes.go, validators.go, etc.)
4. `.agents/skills/feature-dev/references/conventions.md §Telemetry & Observability`
   (the 9-gate decision process)

Then work through the 9 gates and produce the complete set of file edits.
No other changes — do not touch business logic, sentinels, validators, or tests.

---

## Gate decisions you must make before writing any code

Answer every gate explicitly in your response before producing any edits.

**Gate 1 — Logger (always yes)**
Which component names will you use?
  service.go  → `telemetry.New("{PACKAGE}")`
  handler.go  → `telemetry.New("{PACKAGE}")`
  store.go    → `telemetry.New("{PACKAGE}")`
  (add more files if the package has them)

**Gate 2 — Error wrapping (always yes)**
List every `fmt.Errorf` call you will replace and which constructor maps to it:

| Current call | Replacement |
|---|---|
| `fmt.Errorf("service.X: y: %w", err)` | `telemetry.Service("X.y", err)` |
| `fmt.Errorf("store.X: y: %w", err)` | `telemetry.Store("X.y", err)` |
| ... | ... |

List every `slog.ErrorContext` / `slog.WarnContext` call you will replace:

| Current call | Replacement | WARN or ERROR? |
|---|---|---|
| `slog.ErrorContext(r.Context(), "pkg.M: service error", "error", err)` | `log.Error(r.Context(), "M: service error", "error", err)` | ERROR — primary failure |
| `slog.ErrorContext(ctx, "pkg.M: audit log", "error", err)` | `log.Warn(ctx, "M: audit log failed", "error", err)` | WARN — best-effort secondary op |

**Gate 3 — Own metric needed?**
For each operation in this package ask:
"Would I page someone specifically because of this signal,
 or investigate it independently of the HTTP error rate?"

| Operation | Gate 3 answer | Reason |
|---|---|---|
| {operation} | Yes / No | {reason} |

If ALL are No → stop here. No recorder interface needed.
If any are Yes → continue to Gates 4–9.

**Gates 4–9** (only if Gate 3 = Yes for any operation)
Fill in the gate table from conventions.md §Telemetry & Observability.

---

## Required edits (produce these after the gate decisions)

### Rule: WARN vs ERROR

Use `log.Warn` when:
- The operation is a best-effort secondary side effect (audit write, email
  enqueue, heartbeat reset, counter increment after the primary response
  has already been committed).
- A failure here does not affect the HTTP response or the caller's correctness.

Use `log.Error` when:
- The primary operation failed.
- A dependency is down and the request cannot proceed.
- The error will cause the handler to return 5xx.

### Rule: Op naming

| Layer | Format | Example |
|---|---|---|
| Store query | `"TypeName.query"` | `"GetUserForLogin.query"` |
| Store TX step | `"TxName.step"` | `"LoginTx.create_session"` |
| Store TX begin | `"TxName.begin_tx"` | |
| Service op | `"MethodName.step"` | `"Login.login_tx"` |
| Mailer | `"FnName.smtp_send"` | |
| Redis/KV | `"FnName.redis_op"` | |
| Helper fn | `"helperName.step"` | `"generateToken.rand"` |

Ops are for logs only — never Prometheus labels. Short snake_case after the dot.

### Rule: imports

- Add `"github.com/7-Dany/store/backend/internal/platform/telemetry"` where needed.
- Remove `"log/slog"` from any file where it is no longer called directly.
- Remove `"fmt"` from any file where all `fmt.Errorf` calls have been replaced
  AND there are no remaining `fmt.*` calls.

### What NOT to change

- Sentinel error definitions (`var Err... = errors.New(...)`)
- Typed error structs
- Business logic, guard ordering, service contracts
- Store method signatures
- Test files
- `context.WithoutCancel` usage
- Audit log calls (the arguments to `InsertAuditLog`)
- `// Unreachable:` comments

---

## Output format

Produce one `Filesystem:edit_file` call per file that needs changes.
Show only the lines that change — use the minimal oldText/newText pairs.
After all edits, confirm: `go build ./internal/domain/{DOMAIN}/{PACKAGE}/...`
```

---

## How to use this for a whole domain

Run one session per sub-package, in this order:
1. `{domain}/shared/` — no service/handler but may have helpers with `fmt.Errorf`
2. `{domain}/{feature1}/` — start with the simplest feature
3. `{domain}/{feature2}/`
4. … continue until all sub-packages are done

After the last sub-package in the domain, run:
```
go build ./internal/domain/{domain}/...
```
to verify the whole domain compiles together.

---

## Example: RBAC domain migration order

```
1. rbac/shared/          (store helpers — fmt.Errorf only)
2. rbac/permissions/     (simplest — read-only, no complex wrapping)
3. rbac/roles/           (CRUD — moderate wrapping)
4. rbac/owner/           (most complex — multi-method, Warn vs Error calls)
```

after reading the files that will change 
apply D:\Projects\store\backend\docs\design\monitoring\telemetry-checklist.md
to each package you edit
