# {Name} Domain Rules

**Reference implementation:** `internal/domain/{name}`  
**Last updated:** YYYY-MM

Read `docs/RULES.md` first. This file documents only what is specific to the
{name} domain: its feature set, concrete decisions, and patterns that deviate
from or extend the global rules.

See `docs/rules/auth.md` for a complete worked example of this format.

---

## Table of Contents

1. [Conflicts and Clarifications](#1-conflicts-and-clarifications)
2. [Domain Structure](#2-domain-structure)
   - 2.1 [Feature Sub-Packages](#21-feature-sub-packages)
   - 2.2 [Shared Package (`{name}shared`)](#22-shared-package-nameshared)
   - 2.3 [Testutil Package (`{name}sharedtest`)](#23-testutil-package-namesharedtest)
3. [Code Flow Traces](#3-code-flow-traces)
4. [Domain-Specific Conventions](#4-domain-specific-conventions)
5. [Domain-Specific ADRs](#5-domain-specific-adrs)

---

## 1. Conflicts and Clarifications

_List every place where this domain deviates from, extends, or clarifies a
rule in `docs/RULES.md`. The implementation wins. Use this table format:_

| # | RULES.md says | {Name} actually does | Resolution |
|---|---|---|---|
| C-01 | | | |

_Delete this section if there are no conflicts._

---

## 2. Domain Structure

### 2.1 Feature Sub-Packages

```
internal/domain/{name}/
├── routes.go          # package {name} — root assembler only; returns *chi.Mux
├── shared/            # package {name}shared
└── {feature}/         # one sub-package per feature
```

**Features:**

| Package | HTTP Endpoints | Notes |
|---|---|---|
| `{feature}` | `METHOD /path` | |

_Fill in the table as features are defined. Do this before writing any Go._

---

### 2.2 Shared Package (`{name}shared`)

`internal/domain/{name}/shared/` (package `{name}shared`) holds everything
that more than one feature sub-package needs.

```
shared/
├── errors.go      # Cross-feature sentinel errors
├── models.go      # Shared types
├── store.go       # BaseStore: pool, BeginOrBind, conversion helpers
├── validators.go  # Shared validators (omit if none)
└── testutil/      # package {name}sharedtest
```

_Add or remove files as the domain dictates. Document what each file contains
once the domain has been built._

---

### 2.3 Testutil Package (`{name}sharedtest`)

`internal/domain/{name}/shared/testutil/` (package `{name}sharedtest`).
Must never be imported by production code.

| File | Contents |
|---|---|
| `fake_storer.go` | One `{Feature}FakeStorer` per feature |
| `fake_servicer.go` | One `{Feature}FakeServicer` per feature |
| `querier_proxy.go` | `QuerierProxy` + `ErrProxy` sentinel |
| `builders.go` | Pool creation, `MustBeginTx`, seed helpers, `RunTestMain` |

---

## 3. Code Flow Traces

_Provide at least one end-to-end trace for the most representative feature.
Use the auth.md §3 traces as a format reference._

```
HTTP Client
    │  METHOD /api/v1/{name}/{feature}
    ▼
...
```

---

## 4. Domain-Specific Conventions

_Document every pattern that is specific to this domain and would not be
obvious from reading RULES.md alone. Delete sub-sections that don't apply._

### 4.1 Global Middleware on the {Name} Router

_What middleware does the root assembler apply, and why?_

### 4.2 [Additional conventions as needed]

---

## 5. Domain-Specific ADRs

_Record decisions that are non-obvious and specific to this domain.
Use the auth.md §6 format. Number them ADR-{Name}-01, ADR-{Name}-02, etc._

---

### ADR-{Name}-01 — [Title]

**Context:** [What situation prompted this decision?]

**Decision:** [What was decided?]

**Why:** [Reasoning. Be specific enough that a future contributor cannot
"helpfully" undo the decision without understanding the trade-off.]

**Consequence:** [What is the cost or trade-off?]
