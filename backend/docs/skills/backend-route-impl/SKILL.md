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
project's 9-stage process.

**Filesystem access is available.** Always use the Filesystem tools to read
files directly — never guess at file contents, conventions, or guard ordering.

---

## Project root

The project root differs by environment. **Before doing anything else**, resolve
it using this priority order:

1. **`STORE_ROOT` environment variable** — if set, use it as-is.
2. **Filesystem tool auto-detect** — call `Filesystem:list_allowed_directories`.
   Look for an entry that contains `store/backend` (Linux/macOS) or
   `store\backend` (Windows). Use that as the project root.
3. **Known local fallback** — `D:\Projects\store\backend` (Windows dev machine).
4. **GitHub Actions fallback** — `/home/runner/work/store/backend` (CI).

Store the resolved value as `{ROOT}` and use it for every subsequent path in
this skill. Use forward slashes (`/`) when on Linux/macOS; backslashes (`\`) on
Windows. When in doubt, prefer forward slashes — Go tooling accepts them on all
platforms.

The skill's own reference files live at:
```
{ROOT}/docs/skills/backend-route-impl/references/
```

---

## Step 1 — Orient

Before anything else, use the Filesystem tool to read:

| Full path | Why |
|---|---|
| `{ROOT}/docs/map/INCOMING.md` | What routes exist, section numbers, current `[ ]`/`[~]`/`[x]` status |
| `{ROOT}/docs/map/CHECKLIST.md` | What is already production-ready — do not re-implement these |

If the user has not named a specific route, show them the next unstarted items
from INCOMING.md (status `[ ]`) grouped by implementation group (A, B, C…) and
ask which one to start. Do not proceed until the target route is confirmed.

---

## Step 2 — Identify the stage

Ask which stage the user wants to produce, or infer it from context:

| Stage | User might say |
|---|---|
| 0 — Design | "design", "spec", "stage 0", "what decisions do I need" |
| 1 — Foundations | "SQL", "types", "models", "stage 1" |
| 2 — Data layer | "store", "storer", "stage 2" |
| 3 — Logic layer | "service", "stage 3" |
| 4 — HTTP layer | "handler", "routes", "stage 4" |
| 5 — Audit | "audit check", "verify", "stage 5" |
| 6 — Unit tests | "run tests" — manual step, no AI session needed |
| 7 — E2E | "e2e", "e2e tests", "stage 7" — AI writes the collection; human runs it |
| 8 — Docs | "docs", "mdx", "stage 8" |

If the user says "start from scratch" or "new route", begin at Stage 0.

---

## Step 3 — Read the stage template and rules

Use the Filesystem tool to read all of these before producing any output:

| Full path | When to read |
|---|---|
| `{ROOT}/docs/prompts/PROMPT-TEMPLATE.md` | Every stage — contains the exact prompt structure to fill in |
| `{ROOT}/docs/RULES.md` | Every stage — global naming, error wrapping, comment style |
| `{ROOT}/docs/rules/auth.md` | Auth domain routes — guard ordering, OTP patterns, ADRs |
| `{ROOT}/docs/skills/backend-route-impl/references/route-map.md` | Every stage — resolved Go package paths for all routes |

If a `docs/rules/{domain}.md` exists for the target domain, read it. Check with
the Filesystem tool — do not assume it does or does not exist.

---

## Step 4 — Read the existing code for the target package

Before designing or implementing anything, use the Filesystem tool to read the
existing files in the target package folder (if it already exists). Key files:

```
{ROOT}/internal/domain/{domain}/{route}/service.go
{ROOT}/internal/domain/{domain}/{route}/handler.go
{ROOT}/internal/domain/{domain}/{route}/routes.go
{ROOT}/internal/domain/{domain}/shared/errors.go
{ROOT}/internal/domain/{domain}/shared/models.go
{ROOT}/internal/audit/audit.go
{ROOT}/sql/queries/{domain}.sql
```

If the package folder does not yet exist, that is expected for new routes — note
it and proceed.

---

## Step 5 — Produce the stage prompt or output

For stages 0–5 and 8, produce the completed stage prompt document (filled-in
template, ready to hand to a fresh AI session). The stage prompt is the
deliverable — do not implement code in this session.

For Stage 0 specifically: produce the full design doc including HTTP contract,
decisions table, guard ordering, and the complete test case inventory (§7).
Do not skip the test inventory — it is the most important output of Stage 0.

Save the completed stage prompt to:
```
{ROOT}/docs/prompts/{feature}/{N}-{stage-name}.md
```

Use the Filesystem write tool to save it so the user has it immediately.

---

## Step 5.5 — Update INCOMING.md status

After completing any stage (saving a stage prompt or confirming code/tests
passed), update the route's status line in `INCOMING.md`:

| Situation | Status to set |
|---|---|
| First stage started (Stage 0 prompt saved) | `[ ]` → `[~]` |
| All stages complete through Stage 8 reviewed | `[~]` → `[x]` |

Use the Filesystem write/edit tool to make the change. The status marker sits
at the start of each bullet under the relevant `§` heading, e.g.:
```
- [~] Requires valid JWT
```
Change **every** `[ ]` bullet under that section to `[~]`, or `[x]` when fully done.

Also append a progress note to the initial `docs/prompts/{feature}/0-design.md`
under an `## Stage Progress` section (create it if absent) listing which
stages are complete:

```markdown
## Stage Progress

| Stage | Status |
|---|---|
| 0 — Design | ✅ Complete |
| 1 — Foundations | ✅ Complete |
| 2 — Data Layer | ✅ Complete |
| 3 — Logic Layer | ✅ Complete |
| 4 — HTTP Layer | ✅ Complete |
| 5 — Audit | ✅ Complete |
| 6 — Unit Tests | ✅ Complete (manual) |
| 7 — E2E | ⏳ Manual — run when ready |
| 8 — Docs | ⏳ Prompt saved |
```

Update this table each time a stage completes.

---

## Step 6 — Automatically produce the next stage prompt

Immediately after **either** saving a stage prompt **or** finishing a stage
implementation (code changes applied and verified), generate and save the
prompt for the **next** stage. Do not wait to be asked in either case.

**Stage chaining map:**

| Just completed | Next prompt to produce | Skip reason |
|---|---|---|
| Stage 0 — Design | Stage 1 — Foundations | — |
| Stage 1 — Foundations | Stage 2 — Data Layer | — |
| Stage 2 — Data Layer | Stage 3 — Logic Layer | — |
| Stage 3 — Logic Layer | Stage 4 — HTTP Layer | — |
| Stage 4 — HTTP Layer | Stage 5 — Audit | — |
| Stage 5 — Audit | Stage 7 — E2E | Stage 6 is manual (run tests); AI creates the E2E collection |
| Stage 7 — E2E (collection written) | Stage 8 — Docs | Human runs + approves E2E first; then docs |
| Stage 8 — Docs | *(none — implementation complete)* | — |

**How to produce the next prompt:**

1. Re-read the PROMPT-TEMPLATE.md section for the next stage.
2. Fill in every `{placeholder}` using the same feature/domain/route values
   already resolved in the current session. When chaining from an implementation
   session (user supplied an existing stage prompt file), extract the feature
   name and resolved paths from that file — read it with the Filesystem tool
   if needed.
3. For stages that reference Stage 0 test cases (Stages 3, 4, 5), read the
   Stage 0 design doc from disk and copy the relevant rows directly.
4. Save the next prompt to:
   ```
   {ROOT}/docs/prompts/{feature}/{N}-{stage-name}.md
   ```
5. Tell the user: "Next stage prompt saved to `{path}`. Open it in a fresh
   session when you're ready to continue."

**What to fill in for each next-stage prompt:**
- All file paths resolved (no `{placeholder}` left behind)
- Pre-flight items specific to what this stage depends on
- Done-when build commands with the real package path
- For Stage 3/4: test case table populated from Stage 0 §7
- For Stage 5: audit checklist table pre-populated with test IDs from Stage 0 §7

**Do not** implement code for the next stage — only produce its prompt document.

---

## Project file map (resolved paths)

Use these in stage prompts to replace `{placeholder}` values.
Substitute `{ROOT}` with the project root resolved in the **Project root** section above.

```
Project root:        {ROOT}
Domain root:         {ROOT}/internal/domain/
Auth routes:         {ROOT}/internal/domain/auth/{route}/
Admin routes:        {ROOT}/internal/domain/admin/{route}/
Shared auth:         {ROOT}/internal/domain/auth/shared/
Shared testutil:     {ROOT}/internal/domain/auth/shared/testutil/
SQL queries:         {ROOT}/sql/queries/auth.sql  (or admin.sql)
SQL test queries:    {ROOT}/sql/queries_test/auth_test.sql
Generated DB:        {ROOT}/internal/db/  (read-only, regenerated by make sqlc)
Audit events:        {ROOT}/internal/audit/audit.go
KV store:            {ROOT}/internal/platform/kvstore/store.go
Token platform:      {ROOT}/internal/platform/token/
Mint docs:           {ROOT}/mint/api-reference/{domain}/{route}/
```

See `references/route-map.md` for every route's resolved Go package path.

---

## Package layout rule (enforce in every stage)

**One route → one folder.** A package folder never serves more than one HTTP
endpoint. The only exceptions:

- OAuth providers: initiate + callback + link + unlink share one folder (same
  `user_identities` resource).
- Availability check + mutation for the same resource share one folder
  (e.g. `GET /username/available` + `PATCH /me/username` → `auth/username/`).

If a stage prompt or design doc would put two unrelated routes in one folder,
flag it and split before proceeding.

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

Never start a group before its prerequisite is met.
