---
name: feature-dev
description: >
  Implement a new backend route or feature in the store project. Use when the
  user mentions implementing a route, starting a new feature, working on a
  requirement section (e.g. "let's do §G-1"), generating a stage prompt
  (design, foundations, data layer, logic, HTTP, audit, docs), asking what to
  implement next, or reviewing implementation order. Also triggers on partial
  phrases like "let's start bootstrap", "write the stage 0 for magic link",
  "what stage am I on", "continue {feature}", or "next route".
compatibility: Requires filesystem access to the store backend project root.
metadata:
  project: store-backend
---

# Feature Dev

Guide for building features in `internal/domain/` using the project's
10-stage process (0–8 + finalize). Always read files directly — never guess at contents.

Skill root: `.agents/skills/feature-dev/`
All other paths below are relative to the **project root** (`go.mod` directory).

---

## Efficiency rules

**E-1 — Lean read first.** Read the minimum set of files. Open additional files
only when a specific unknown arises.

**E-2 — Skip non-existent files silently.** Note "not found — new domain" and
continue. Do not retry with different paths.

**E-3 — Read large files by section.** For files > 200 lines use `head`/`tail`
or a targeted range. Per-stage read lists are in `references/stages.md`.

**E-4 — Never re-read a file already in context.**

**E-5 — Infer from analogues.** Read one analogous feature (most similar by
complexity) instead of all features. The pattern is consistent within a domain.

**E-6 — Context snapshot.** After Stage 0, write `context/{feature}/context.md`
immediately (relative to skill root). All subsequent sessions load it instead
of re-reading Stage 0. Template: `references/templates.md §Appendix A`.

---

## Principles

1. **Stage 0 is mandatory.** Never write code until the design doc is approved
   and every open question is answered.
2. **Work bottom-up.** SQL → store → service → handler → routes. Top-down
   produces type mismatches.
3. **One route, one folder.** Exceptions: same-resource endpoints and
   availability-check + mutation pairs. See `references/architecture.md`.
4. **Platform packages over hand-rolled equivalents.** `respond.JSON`,
   `respond.DecodeJSON`, `token.UserIDFromContext`, etc. — always.
5. **Three-file syncs are atomic.** Audit triad (§S-1), Querier triad (§S-2),
   DecodeJSON 413 path (§S-3). See `references/conventions.md`.
6. **Domain rules first.** Read `docs/rules/{domain}.md` before designing.
   If it doesn't exist, create from `docs/rules/_template.md`.

---

## Quick-start workflow

### Step 1 — Orient

Read these **four files** every session:

| File | What to extract |
|---|---|
| `docs/map/project-map.md` | Package locations, exported APIs, KV prefixes, platform interfaces |
| `context/{feature}/context.md` | Previously resolved context — skip Steps 2–3 if exists |

First path is relative to project root; `context/` is relative to skill root.
Do **not** read `docs/map/endpoints.md` or `docs/map/backlog.md` in full.
To find requirement text, read only the `§{section}` block needed.

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
| 9 — Finalize | "finalize", "wrap up", "update the map", "stage 9" |

#### Resolving "continue {feature}" (no stage number given)

1. List `context/{feature}/` (skill root) to find the highest-numbered stage prompt.
2. Check whether the implementation files for that stage exist on disk.
3. Prompt exists **but code files don't** → help implement that stage (produce code).
4. Prompt **and** code files exist → produce the next stage prompt.
5. Never silently skip — confirm with the user if ambiguous.

---

### Step 3 — Load references

Read `references/stages.md` for the exact per-stage file-read table.

For **every** stage also read:
- `docs/rules/RULES.md §3.13` + the `§3.x` rule specific to this stage
- `docs/rules/{domain}.md` (full if < 300 lines; §2 + §5 ADRs if longer)

**Load these on demand — only when the situation requires it:**

| Reference | Load when |
|---|---|
| `references/architecture.md` | Routing/import question; wiring into domain assembler |
| `references/conventions.md` | Writing any Go file — naming, layout, SQL, three-file syncs |
| `references/comments.md` | Writing Go comments or security annotations |
| `references/doc-rules.md` | Producing Stage 8 mint MDX output |
| `references/templates.md` | Producing any stage prompt document |
| `references/review.md` | Stage 5 audit — load before any pass |

All paths above are relative to the skill root.

**Context folder:** Stage prompts live at `context/{feature}/` (skill root).
Create on first use. Never write into `docs/`.

---

### Step 4 — Read the target package

Check `internal/domain/{domain}/{route}/`. Read `service.go`, `handler.go`,
`routes.go`, `models.go` if it exists. New routes won't have it — note and continue.

---

### Step 5 — Produce the stage output

Stages 0–5 and 8 → produce the completed stage prompt. Templates in
`references/templates.md`. Stage 5 → produce the 4-part multi-role audit prompt.

Save to: `context/{feature}/{N}-{stage-name}.md` (skill root). Create the
folder if it does not exist.

---

### Step 6 — Write context.md (Stage 0 only)

Immediately after saving `0-design.md`, write `context/{feature}/context.md`.
Template: `references/templates.md §Appendix A`. Keep under 80 lines.

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
| Stage 8 | User: "docs merged" | Stage 9 — Finalize |

**Stage 6 is manual** (no AI output): run `go test ./...`, fix failures, then tell the AI "unit tests pass".

Tell the user: `"Next stage prompt saved to context/{feature}/{N}-{stage-name}.md. Open it in a fresh session."`

---

## Post-feature update checklist

Stage 9 handles this. See the Stage 9 section in `references/stages.md`.

---

## References index

All files below are relative to the skill root (`references/`).

| File | Contents | Load when |
|---|---|---|
| `references/stages.md` | Per-stage file-read tables + test-case derivation algorithm | Step 3 — every stage |
| `references/templates.md` | All stage prompt templates + context.md + audit prompt | Producing any stage prompt |
| `references/architecture.md` | Layer contracts, import rules, wiring model, type boundaries | Routing/import question |
| `references/conventions.md` | File layout, naming, SQL, HTTP, testing, three-file syncs | Writing any Go file |
| `references/comments.md` | Go comment conventions, security annotations, checklist | Writing Go comments |
| `references/mintlify.md` | Mintlify MDX doc rules and full template | Stage 8 output |
| `references/review.md` | 4-part review framework (rules, logic, platform, tests) | Stage 5 audit — load before any pass |
| `docs/rules/RULES.md` | Project-wide Go conventions and hard rules | Every stage — load `§3.13` + relevant `§3.x` |
| `docs/map/project-map.md` | Package locations, exported APIs, KV prefixes, platform interfaces | Step 1 — every session |
