# Bitcoin Review Thread Prompts

Use this file to open separate review threads for each bounded Bitcoin target.

Primary review contract:

- `D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md`

Use the `/review` variants when the target already has local changes and you want
Codex to review the current diff. Use the plain prompt variants when you want a
full scoped audit of the current code, or when using Claude or another reviewer.

---

## Thread Naming

Suggested thread names:

- `btc-schema-review`
- `btc-rpc-review`
- `btc-zmq-review`
- `btc-root-shared-review`
- `btc-watch-review`
- `btc-events-review`
- `btc-txstatus-review`
- `btc-block-review`
- `btc-tests-review`
- `btc-docs-review`

---

## 1. Schema and Queries

### Codex `/review`

```text
/review Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and schema-and-query review pattern.
Scope this review to backend/sql/schema and backend/sql/queries/btc.sql only.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

### Plain Prompt

```text
Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and schema-and-query review pattern.
Review only backend/sql/schema and backend/sql/queries/btc.sql.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

---

## 2. RPC

### Codex `/review`

```text
/review Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and platform-package review pattern.
Scope this review to backend/internal/platform/bitcoin/rpc only.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

### Plain Prompt

```text
Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and platform-package review pattern.
Review only backend/internal/platform/bitcoin/rpc.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

---

## 3. ZMQ

### Codex `/review`

```text
/review Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and platform-package review pattern.
Scope this review to backend/internal/platform/bitcoin/zmq only.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

### Plain Prompt

```text
Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and platform-package review pattern.
Review only backend/internal/platform/bitcoin/zmq.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

---

## 4. Domain Root and Shared

### Codex `/review`

```text
/review Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the domain-root/shared review pattern.
Scope this review to backend/internal/domain/bitcoin/routes.go and backend/internal/domain/bitcoin/shared only.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

### Plain Prompt

```text
Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the domain-root/shared review pattern.
Review only backend/internal/domain/bitcoin/routes.go and backend/internal/domain/bitcoin/shared.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

---

## 5. Watch

### Codex `/review`

```text
/review Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the domain-package review pattern.
Scope this review to backend/internal/domain/bitcoin/watch only.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

### Plain Prompt

```text
Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the domain-package review pattern.
Review only backend/internal/domain/bitcoin/watch.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

---

## 6. Events

### Codex `/review`

```text
/review Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the domain-package review pattern.
Scope this review to backend/internal/domain/bitcoin/events only.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

### Plain Prompt

```text
Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the domain-package review pattern.
Review only backend/internal/domain/bitcoin/events.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

---

## 7. Txstatus

### Codex `/review`

```text
/review Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the domain-package review pattern.
Scope this review to backend/internal/domain/bitcoin/txstatus only.
Include artifact-hygiene checks for stray backup or generated files in the package directory.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

### Plain Prompt

```text
Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the domain-package review pattern.
Review only backend/internal/domain/bitcoin/txstatus.
Include artifact-hygiene checks for stray backup or generated files in the package directory.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

---

## 8. Block

### Codex `/review`

```text
/review Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the domain-package review pattern.
Scope this review to backend/internal/domain/bitcoin/block only.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

### Plain Prompt

```text
Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the domain-package review pattern.
Review only backend/internal/domain/bitcoin/block.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

---

## 9. Artifact Hygiene and Tests

### Codex `/review`

```text
/review Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the artifact-hygiene/tests review pattern.
Scope this review to the Bitcoin test files under backend/internal/platform/bitcoin and backend/internal/domain/bitcoin only.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

### Plain Prompt

```text
Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the artifact-hygiene/tests review pattern.
Review only the Bitcoin test files under backend/internal/platform/bitcoin and backend/internal/domain/bitcoin.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

---

## 10. Docs Drift

### Codex `/review`

```text
/review Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the docs-only review pattern.
Scope this review to backend/docs/design/btc and any Bitcoin README or Mint docs that describe the implementation.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

### Plain Prompt

```text
Use D:\Projects\store\backend\docs\review\bitcoin-review-playbook.md as the review contract.
Apply its core rules, inherited feature-dev rules, output contract, and the docs-only review pattern.
Review only backend/docs/design/btc and any Bitcoin README or Mint docs that describe the implementation.
Findings first, ordered by severity, with file references.
Do not edit code yet.
```

---

## Usage Notes

- Use one thread per target.
- Prefer the plain prompt when the target has no local diff.
- Prefer `/review` when you already changed files in that target and want Codex to review the patch.
- Keep fix work in separate threads after review findings are confirmed.
