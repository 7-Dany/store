---
name: backend-route-impl
description: >
  Implement a new backend route or feature. Use this skill whenever the user
  mentions implementing a route, starting a new feature, working on a specific
  requirement section (e.g. "let's do §G-1"), generating a stage prompt
  (design, foundations, data layer, logic, HTTP, audit, docs), asking what to
  implement next, or reviewing implementation order. Trigger even for partial
  phrases like "let's start bootstrap", "write the stage 0 for magic link",
  "what stage am I on", or "next route".
user-invocable: false
---

# Backend Route Implementation Skill

Implementation guide for new routes in `internal/domain/` using the project's
9-stage process. Optimised for **minimum token consumption** and **maximum
accuracy** — read only what you need, skip what you can infer.

**Filesystem access is available.** Always use Filesystem tools to read files
directly — never guess at file contents, conventions, or guard ordering.

---

## Paths

All paths in this skill are **relative to the project root** (the directory
containing `go.mod`). When using Filesystem tools, resolve them against the
absolute project root discovered at session start.

Skill files live at: `.agents/skills/backend-route-impl/`

---

## Efficiency rules

**E-1 — Lean read first.** Read the minimum set of files. Open additional files
only when a specific unknown arises.

**E-2 — Skip non-existent files silently.** Note "not found — new domain" and
continue. Do not retry with different paths.

**E-3 — Read large files by section.** For files > 200 lines use `head`/`tail`
or a targeted range. Per-stage read lists are in [stages/stages.md](./stages/stages.md).

**E-4 — Never re-read a file already in context.**

**E-5 — Infer from analogues.** Read one analogous feature (most similar by
complexity) instead of all features. The pattern is consistent within a domain.

**E-6 — Context snapshot.** After Stage 0, write
`docs/prompts/{feature}/context.md` immediately. All subsequent sessions load
it instead of re-reading Stage 0. Template: [templates/templates.md](./templates/templates.md) §Appendix A.

---

## Principles

1. **Stage 0 is mandatory.** Never write code until the design doc is approved
   and every open question is answered.
2. **Work bottom-up.** SQL → store → service → handler → routes. Top-down
   produces type mismatches.
3. **One route, one folder.** Exceptions: same-resource endpoints and
   availability-check + mutation pairs. See [rules/architecture.md](./rules/architecture.md).
4. **Platform packages over hand-rolled equivalents.** `respond.JSON`,
   `respond.DecodeJSON`, `token.UserIDFromContext`, etc. — always.
5. **Three-file syncs are atomic.** Audit triad (§S-1), Querier triad (§S-2),
   DecodeJSON 413 path (§S-3). See [rules/conventions.md](./rules/conventions.md).
6. **Domain rules first.** Before designing a feature, read
   `docs/rules/{domain}.md`. If it doesn't exist, create it from
   `docs/rules/_template.md` before writing any Go.

---

## Quick-start workflow

### Step 1 — Orient

Read these **four files** every session:

| File | What to extract |
|---|---|
| `.agents/skills/backend-route-impl/references/e2e-status.md` | Done/pending endpoints; KV prefix collision check |
| `.agents/skills/backend-route-impl/references/project-map.md` | Package locations, exported APIs, platform interfaces |
| `.agents/skills/backend-route-impl/references/route-map.md` | Resolved Go package paths for all planned routes |
| `docs/prompts/{feature}/context.md` | Previously resolved context (if exists) |

Do **not** read `docs/map/CHECKLIST.md` or `docs/map/INCOMING.md` in full.
To find requirement text, read only the `§{section}` block needed.

If `context.md` exists for the feature, skip Steps 2–3 → go to Step 4.

---

### Step 2 — Identify the stage

| Stage | User might say |
|---|---|
| 0 — Design | "design", "spec", "stage 0", "what decisions" |
| 1 — Foundations | "SQL", "types", "models", "stage 1" |
| 2 — Data layer | "store", "storer", "stage 2" |
| 3 — Logic layer | "service", "stage 3" |
| 4 — HTTP layer | "handler", "routes", "stage 4" |
| 5 — Audit | "audit check", "verify", "stage 5" |
| 6 — Unit tests | "run tests" — manual, no AI needed |
| 7 — E2E | "e2e" — manual, no AI needed |
| 8 — Docs | "docs", "mdx", "stage 8" |

#### Resolving "continue {feature}" (no stage number given)

1. List `docs/prompts/{feature}/` to find the highest-numbered stage prompt.
2. Check whether the implementation files for that stage exist on disk.
3. Prompt exists **but code files don't** → help implement that stage (produce code).
4. Prompt **and** code files exist → produce the next stage prompt.
5. Never silently skip — confirm with the user if ambiguous.

---

### Step 3 — Targeted file reads

See [stages/stages.md](./stages/stages.md) for the exact per-stage read tables.

For **every** stage also read:
- `docs/RULES.md §3.13` checklist + the specific `§3.x` rule relevant to this stage
- `docs/rules/{domain}.md` — domain-specific patterns (full file if < 300 lines;
  §2 features + §5 ADRs if longer)

---

### Step 4 — Read the target package

Check if the folder exists: `internal/domain/{domain}/{route}/`

If it exists, read: `service.go`, `handler.go`, `routes.go`, `models.go`.
If it doesn't — expected for new routes. Note it and continue.

---

### Step 5 — Produce the stage output

For stages 0–5 and 8 → produce the completed stage prompt document.
For Stage 5 specifically → produce the 4-part multi-role audit prompt.
See [templates/templates.md](./templates/templates.md) for all templates.

Save to: `docs/prompts/{feature}/{N}-{stage-name}.md`

---

### Step 6 — Write context.md (after Stage 0 only)

Immediately after saving `0-design.md`, write `docs/prompts/{feature}/context.md`.
Template: [templates/templates.md](./templates/templates.md) §Appendix A. Keep under 80 lines.

---

### Step 7 — Auto-produce the next stage prompt

After saving a stage prompt, immediately produce and save the next one.

| Just completed | Gate before next | AI produces |
|---|---|---|
| Stage 0 | — | Stage 1 prompt |
| Stage 1 | — | Stage 2 prompt |
| Stage 2 | — | Stage 3 prompt |
| Stage 3 | — | Stage 4 prompt |
| Stage 4 | — | Stage 5 prompt |
| Stage 5 | User: "unit tests pass" | Stage 7 — E2E collection |
| Stage 7 | User: "e2e pass" | Stage 8 — Docs |

**Stage 6 is manual** (no AI output): run `go test ./...`, fix failures, then tell the AI "unit tests pass".

Tell the user: "Next stage prompt saved to `{path}`. Open it in a fresh session."

---

## Post-feature update checklist

After Stage 8 is reviewed and merged, update these files atomically.
**Never skip this step — stale references break future AI sessions.**

| File | What to update |
|---|---|
| `docs/map/INCOMING.md §{section}` | Mark the route(s) `[x]` |
| `docs/map/CHECKLIST.md` | Add the new E2E scenario reference |
| `.agents/skills/backend-route-impl/references/e2e-status.md` | Change ⏳/~ to ✓; add new KV prefixes to collision table |
| `.agents/skills/backend-route-impl/references/route-map.md` | Remove completed route rows (or add note "implemented") |
| `.agents/skills/backend-route-impl/references/project-map.md` | Add new package row under the correct domain section |
| `docs/rules/{domain}.md` | Add the new endpoint to the feature table (§2.1); add new KV prefixes; add ADR if a non-obvious decision was made |
| `mint/api-reference/{domain}/` | Confirm `.mdx` file committed and `mint/docs.json` updated |
| `docs/prompts/{feature}/` | Archive or delete — they are scaffolding, not long-term docs |

---

## Detailed references

| File | Contents |
|---|---|
| [stages/stages.md](./stages/stages.md) | Per-stage file-read tables + test-case derivation algorithm |
| [templates/templates.md](./templates/templates.md) | All stage prompt templates + context.md + audit prompt |
| [rules/architecture.md](./rules/architecture.md) | Layer contracts, import rules, wiring model, type boundaries |
| [rules/conventions.md](./rules/conventions.md) | File layout, naming, SQL, HTTP, testing, three-file syncs |
| [rules/comments.md](./rules/comments.md) | Go comment conventions, security annotations, checklist |
| `mint/` MDX conventions | See closest existing `.mdx` in `mint/api-reference/{domain}/` as canonical reference |
| [references/project-map.md](./references/project-map.md) | Package locations, exported APIs, platform interfaces |
| [references/route-map.md](./references/route-map.md) | Resolved Go package paths for every remaining route |
| [references/e2e-status.md](./references/e2e-status.md) | Completion status + all KV prefixes in use |
