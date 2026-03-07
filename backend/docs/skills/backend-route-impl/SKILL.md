---
name: backend-route-impl
description: >
  Implement a new backend route or feature for the store auth/admin system.
  Use this skill whenever the user mentions implementing a route, starting a
  new feature, working on a specific INCOMING.md section (e.g. "let's do §B-1"),
  generating a stage prompt (design, foundations, data layer, logic, HTTP,
  audit, docs), asking what to implement next, or reviewing implementation order.
  Trigger even for partial phrases like "let's start username", "write the
  stage 0 for email change", "what stage am I on", or "next route".
---

# Backend Route Implementation Skill

This skill guides implementation of new routes in `internal/domain/` using the
project's 9-stage process. It is optimised for **minimum token consumption** and
**maximum accuracy** — read only what you need, skip what you can infer.

**Filesystem access is available.** Always use the Filesystem tools to read
files directly — never guess at file contents, conventions, or guard ordering.

---

## Project root

```
D:\Projects\store\backend
```

All paths in this skill are relative to that root. When using Filesystem tools,
prefix every path with `D:\Projects\store\backend\`.

The skill's own reference files live at:
```
D:\Projects\store\backend\docs\skills\backend-route-impl\references\
```

---

## Efficiency Rules (read before starting)

These rules exist to reduce token consumption without sacrificing accuracy.

**E-1 — Lean read first.** Read the minimum set of files to confirm the target
and understand its context. Only open additional files when a specific unknown
arises (e.g. "I need to know what Storer methods already exist").

**E-2 — Skip non-existent files silently.** If a `Read first` file doesn't exist
(e.g. `docs/rules/profile.md` for a new domain), skip it and note "not found —
new domain, no domain rules yet." Do not retry with different paths.

**E-3 — Read large files by section, not whole.** For files > 200 lines (e.g.
`auth.sql`, `RULES.md`, `PROMPT-TEMPLATE.md`), read only the relevant section
using `head`/`tail` or a targeted range. The sections to read per stage are
listed in the stage steps below.

**E-4 — Never re-read a file you already have in context.** If you read
`RULES.md` in Step 3, do not re-read it in Step 6.

**E-5 — Infer from analogues.** For a new feature in an existing domain, read
one analogous existing feature (the most similar one by complexity) instead of
all features. The pattern is consistent within a domain.

**E-6 — Context snapshot.** After producing Stage 0, write a compact
`docs/prompts/{feature}/context.md` file (template in §Appendix A). Every
subsequent stage session loads `context.md` instead of re-reading Stage 0 +
RULES.md from scratch.

---

## Step 1 — Orient

Read these **four files** to establish baseline context:

| Full path | What to extract |
|---|---|
| `D:\Projects\store\backend\docs\skills\backend-route-impl\references\e2e-status.md` | What's done (✓/⏳), KV prefix collision check — **replaces reading CHECKLIST.md** |
| `D:\Projects\store\backend\docs\skills\backend-route-impl\references\project-map.md` | Package locations, exported APIs, all platform interfaces — **replaces guessing** |
| `D:\Projects\store\backend\docs\skills\backend-route-impl\references\route-map.md` | Resolved Go package paths for all planned routes |
| `D:\Projects\store\backend\docs\prompts\{feature}\context.md` | Previously resolved context (if exists) |

**Do NOT read `CHECKLIST.md` or `INCOMING.md` in full.** They are 600+ and 300+ lines respectively.
- To check what's done → `e2e-status.md`
- To find requirement text → read only the `§{section}` block from `INCOMING.md` that the user named

If the user has not named a specific route, show the ⏳ items from `e2e-status.md` grouped by group and ask which to start.

If `context.md` exists for the feature, skip Steps 2–3 and proceed directly
to Step 4 using the resolved paths in `context.md`.

---

## Step 2 — Identify the stage

Ask which stage the user wants, or infer from context:

| Stage | User might say |
|---|---|
| 0 — Design | "design", "spec", "stage 0", "what decisions" |
| 1 — Foundations | "SQL", "types", "models", "stage 1" |
| 2 — Data layer | "store", "storer", "stage 2" |
| 3 — Logic layer | "service", "stage 3" |
| 4 — HTTP layer | "handler", "routes", "stage 4" |
| 5 — Audit | "audit check", "verify", "stage 5" |
| 6 — Unit tests | "run tests" — manual step, no AI needed |
| 7 — E2E | "e2e" — manual step, no AI needed |
| 8 — Docs | "docs", "mdx", "stage 8" |

If the user says "start from scratch" or "new route", begin at Stage 0.

---

## Step 3 — Targeted file reads (stage-specific)

**Do not read PROMPT-TEMPLATE.md in full.** It is 400+ lines. Read only the
section for the requested stage using the line ranges below, or read the
specific stage template by searching for `## Stage N`.

Read these files **for every stage**:

| File | What to read | Why |
|---|---|---|
| `docs/RULES.md` | §3.13 checklist + the specific §3.x rule relevant to this stage | Global conventions |
| `docs/rules/{domain}.md` | Full file if < 300 lines; §2 (features) + §5 (ADRs) if longer | Domain-specific patterns |
| `docs/skills/backend-route-impl/references/route-map.md` | Already read in Step 1 — skip | — |

Then read the **stage-specific** files listed below. Only read files marked **Required**.
Files marked **If needed** should only be opened if something in the Required files
is unclear or missing.

### Stage 0 — Design

| File | Required / If needed | Note |
|---|---|---|
| `docs/map/INCOMING.md §{section}` | Required | **Target section only** — use filesystem head/tail or search |
| Closest analogous package (`handler.go` + `service.go`) | Required | Use `project-map.md §1` to locate it; files are small |
| `internal/audit/audit.go` | Required | `const` block only (first 80 lines) — event names |
| `sql/queries/auth.sql` | Required | **Tail 60 lines** — existing query style at append point |
| `project-map.md §5` | Already loaded in Step 1 | kvstore/ratelimit/respond/token interfaces — **skip re-reading platform files** |
| `project-map.md §2` | Already loaded in Step 1 | authshared errors + OTP functions — **skip re-reading shared files** |

### Stage 1 — Foundations

| File | Required / If needed |
|---|---|
| `sql/queries/auth.sql` | Required — **tail 60 lines** (append position, confirm section style) |
| `internal/audit/audit.go` | Required — full `const` + `AllEvents()` (needed to write the sync triad) |
| Analogous `models.go` + `requests.go` + `validators.go` + `errors.go` | Required — one analogous feature only; use `project-map.md §1` to locate it |
| `project-map.md §2` (authshared errors) | Already loaded — check here first before defining new sentinels |
| `RULES.md §3.9` (SQL) + `§3.11` (naming) | Required |

### Stage 2 — Data Layer

| File | Required / If needed |
|---|---|
| `internal/db/auth.sql.go` | Required — confirm generated method signatures (new queries from Stage 1) |
| `{feature}/service.go` | Required — Storer interface definition |
| `auth/shared/testutil/fake_storer.go` | Required — existing FakeStorer struct layout (add new entry) |
| `auth/shared/testutil/querier_proxy.go` | Required — existing QuerierProxy layout (add Fail* fields) |
| `project-map.md §3` (authsharedtest) | Already loaded — confirms exact FakeStorer/Proxy patterns |
| `RULES.md §3.3` (store shapes) + `§3.4` (error wrapping) | Required |
| Analogous `store.go` | Required — one file; use `project-map.md §1` to pick the closest analogue |

### Stage 3 — Logic Layer

| File | Required / If needed |
|---|---|
| `{feature}/service.go` | Required — constructor + Storer interface |
| `{feature}/handler.go` | Required — Servicer interface location |
| `{feature}/models.go` | Required |
| `shared/testutil/fake_servicer.go` | Required — existing layout |
| `0-design.md §5` (guard ordering) + `§7` (S-layer test cases) | Required |
| `shared/otp.go` | If OTP used |
| `RULES.md §3.4, §3.6, §3.7` | Required |

### Stage 4 — HTTP Layer

| File | Required / If needed |
|---|---|
| `{feature}/handler.go` | Required |
| `{feature}/routes.go` | Required |
| `{feature}/requests.go` + `validators.go` | Required |
| `{feature}/handler_test.go` (if exists) | Required |
| `shared/testutil/fake_servicer.go` | Required |
| `0-design.md §2` (HTTP contract) + `§7` (H+I test cases) | Required |
| `RULES.md §3.10` (HTTP) + `§3.13` (checklist) | Required |

### Stage 5 — Audit

| File | Required / If needed |
|---|---|
| All Stage 1–4 production files + test files for this feature | Required (full read) |
| `0-design.md` | Required (source of truth) |
| `RULES.md §3.13` | Required |

### Stage 8 — Docs

| File | Required / If needed |
|---|---|
| Closest existing `.mdx` file in `mint/api-reference/{domain}/` | Required |
| `mint/docs.json` | Required — navigation tree |
| `0-design.md §2` (HTTP contract) + `§6` (rate limits) | Required |

---

## Step 4 — Read the target package

Before designing or implementing, check if the target package folder exists:

```
D:\Projects\store\backend\internal\domain\{domain}\{route}\
```

If it exists, read: `service.go`, `handler.go`, `routes.go`, `models.go`.
If it doesn't exist — that is expected for new routes. Note it and proceed.

**Do not read all files in the folder blindly.** Read only the files relevant
to the current stage (see Step 3 lists above).

---

## Step 5 — Produce the stage prompt or output

For stages 0–5 and 8, produce the completed stage prompt document. The stage
prompt is the deliverable — do not implement code in this session.

For Stage 0 specifically, produce the full design doc including:
- HTTP contract (§2): every endpoint, every error code, exact status + code string
- Decisions table (§3): every open question answered with rationale
- Guard ordering (§5): numbered list, one entry per check/mutation
- **Complete test case inventory (§7)** — this is the most important output

**Test case inventory algorithm** (Stage 0 §7):

Derive from guard ordering mechanically:

1. **S-layer:** For every guard that returns an error sentinel: one test case.
   For every `context.WithoutCancel` call: one test case asserting `ctx.Done() == nil`.
   For every timing-invariant dummy call: one test case counting invocations.
   Add happy path (all guards pass → success result).

2. **H-layer:** For every sentinel the service can return: one handler test case
   mapping sentinel → HTTP status + code string. For every request field: one
   validation failure case (empty, wrong format, too long). Add missing auth → 401.
   Add malformed JSON → 422. Add body > 1 MiB → 413.

3. **I-layer:** For every store method that writes to DB: one integration test
   asserting the DB state after the call. For every store error path:
   one integration test using QuerierProxy Fail flag.

If a guard has no error sentinel (e.g. "extract user ID from JWT"), it produces
only an H-layer case (missing auth → 401), not an S-layer case.

Save the completed stage prompt to:
```
D:\Projects\store\backend\docs\prompts\{feature}\{N}-{stage-name}.md
```

---

## Step 6 — Write context.md (after Stage 0 only)

Immediately after saving `0-design.md`, write
`D:\Projects\store\backend\docs\prompts\{feature}\context.md`
using the template in §Appendix A.

This file is loaded by all subsequent stage sessions (Step 1) as a cheap
substitute for re-reading Stage 0 + RULES.md. It must stay under 80 lines.

---

## Step 7 — Auto-produce the next stage prompt

Immediately after saving a stage prompt, produce and save the next stage's
prompt. Do not wait to be asked.

**Stage chaining map:**

| Just completed | Who acts next | What happens | AI produces |
|---|---|---|---|
| Stage 0 — Design | AI | — | Stage 1 prompt |
| Stage 1 — Foundations | AI | — | Stage 2 prompt |
| Stage 2 — Data Layer | AI | — | Stage 3 prompt |
| Stage 3 — Logic Layer | AI | — | Stage 4 prompt |
| Stage 4 — HTTP Layer | AI | — | Stage 5 prompt |
| Stage 5 — Audit | **User** | Reads audit output, fixes issues, runs unit tests manually | *(nothing yet)* |
| User: "tests pass" | AI | — | Stage 6 — E2E tests file |
| Stage 6 — E2E tests | **User** | Runs the e2e test file to confirm | *(nothing yet)* |
| User: "e2e pass" | AI | — | Stage 7 — Docs |
| Stage 7 — Docs | *(complete)* | — | — |

**Key rule:** The AI never auto-produces Stage 6 or Stage 7 without explicit user confirmation from the previous gate. Stage 5 → wait for "tests pass". Stage 6 → wait for "e2e pass".

**How to fill in the next prompt efficiently:**
- Copy all resolved `{placeholder}` values from the current session's context
- For Stage 3/4/5: copy the relevant test-case rows directly from `0-design.md §7`
  (already read in Step 1 via `context.md`). Do not re-derive them.
- For the "Read first" table: use the stage-specific file list from Step 3 above
  (not the PROMPT-TEMPLATE.md verbose version)
- Leave no `{placeholder}` in the saved file

Tell the user: "Next stage prompt saved to `{path}`. Open it in a fresh session."

---

## Project file map (resolved paths)

```
Project root:        D:\Projects\store\backend
Domain root:         internal/domain/
Auth routes:         internal/domain/auth/{route}/
Profile routes:      internal/domain/profile/{route}/
Admin routes:        internal/domain/admin/{route}/
Shared auth:         internal/domain/auth/shared/
Shared profile:      internal/domain/profile/shared/
Shared testutil:     internal/domain/auth/shared/testutil/
SQL queries:         sql/queries/auth.sql  (all user-row queries, auth + profile domain)
SQL test queries:    sql/queries_test/auth_test.sql
Generated DB:        internal/db/  (read-only, regenerated by make sqlc)
Audit events:        internal/audit/audit.go
KV store:            internal/platform/kvstore/store.go
Token platform:      internal/platform/token/
Mint docs:           mint/api-reference/{domain}/{route}/
Stage prompts:       docs/prompts/{feature}/
```

See `references/route-map.md` for every route's resolved Go package path.

---

## Package layout rule (enforce in every stage)

**One route → one folder.** The only exceptions:
- OAuth providers: initiate + callback + link + unlink share one folder (same resource)
- Availability check + mutation for the same resource share one folder (e.g. `username/`)
- Multi-step flows for the same resource share one folder (e.g. `email/` for all three email-change steps)

---

## Quick reference: implementation groups

Full details in `references/route-map.md`. Summary:

| Group | Prerequisite | Contains |
|---|---|---|
| A | Nothing (extends existing packages) | Profile update, Set password |
| B | Schema migration §B-0 merged | Username, Email change, Delete account |
| C | Nothing | Owner bootstrap |
| D | Nothing (establishes OAuth patterns) | Google OAuth, Telegram OAuth |
| E | At least one OAuth provider live | Linked accounts |
| F | Owner bootstrap done | All admin routes |

---

## Appendix A — context.md template

Write this file immediately after Stage 0. Keep it under 80 lines.

```markdown
# {Feature} — Resolved Context

**Section:** INCOMING.md §{section}
**Package:** `internal/domain/{domain}/{route}/`
**Status:** Stage 0 approved / Stage N complete

## Resolved paths
- SQL file: `sql/queries/auth.sql` (new section: `/* ── {Feature} ── */`)
- Models: `internal/domain/{domain}/{route}/models.go`
- Store: `internal/domain/{domain}/{route}/store.go`
- Service: `internal/domain/{domain}/{route}/service.go`
- Handler: `internal/domain/{domain}/{route}/handler.go`
- Routes: `internal/domain/{domain}/{route}/routes.go`
- FakeStorer: `internal/domain/{domain}/shared/testutil/fake_storer.go`
- FakeServicer: `internal/domain/{domain}/shared/testutil/fake_servicer.go`
- QuerierProxy: `internal/domain/{domain}/shared/testutil/querier_proxy.go`

## Key decisions (from Stage 0 §3)
- D-01: {summary}
- D-02: {summary}
... (one line per decision)

## New SQL queries
{query names, one per line}

## New audit events
{EventName = "value_string", one per line}

## New sentinel errors
{ErrName — package location}

## Rate-limit prefixes
{prefix: endpoint mapping}

## Test case IDs (from Stage 0 §7)
- S-layer: T-01 to T-{N}
- H-layer: T-{N+1} to T-{M}
- I-layer: T-{M+1} to T-{K}
```
