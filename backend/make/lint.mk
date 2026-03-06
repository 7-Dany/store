# ─── Linting & Static Analysis ───────────────────────────────────────────────
#
# Requires golangci-lint v2+. Install with: make install-lint
#
# Linters enabled (see .golangci.yml for full config):
#
#   BUGS & CORRECTNESS
#     errcheck      - unchecked errors (in standard preset)
#     errorlint     - wrong error comparison (== instead of errors.Is); fmt.Errorf without %w
#     bodyclose     - HTTP response body not closed
#     contextcheck  - context replaced mid-chain with context.Background()
#     exhaustive    - missing switch cases on enum types
#     nilerr        - nil returned instead of the received error
#     nilnil        - (nil, nil) ambiguous return
#     noctx         - http.NewRequest without a context
#     rowserrcheck  - sql.Rows.Err() not checked after iteration
#     sqlclosecheck - sql.Rows / sql.Stmt not closed
#     unparam       - params that always receive the same value
#     makezero      - make() with non-zero length used with append
#
#   SECURITY
#     gosec         - hardcoded creds, weak crypto, SQL injection, file permissions
#
#   PERFORMANCE
#     prealloc      - slice pre-allocation opportunities
#     govet         - includes copylocks analyser (sync.Mutex copy bugs)
#
#   DEAD CODE & STYLE
#     unused        - unused vars, consts, functions, types (in standard preset)
#     ineffassign   - assignments whose value is never used (in standard preset)
#     revive        - doc comments, naming conventions, error conventions
#     gocritic      - broad correctness + style checks
#     godot         - exported doc comments must end with a period
#
#   CI ONLY
#     godox         - flags TODO/FIXME/HACK (disabled locally, enabled in lint-ci)
#
# CI vs local:
#   make lint       - all linters except godox; safe to run during development
#   make lint-ci    - all linters including godox; TODOs/FIXMEs fail the build
#   make lint-fix   - auto-fix what is fixable; review git diff afterward
# ─────────────────────────────────────────────────────────────────────────────

.PHONY: lint lint-ci lint-fix install-lint

# golangci-lint v2 is required — config uses version: "2" schema.
# Check for latest: https://github.com/golangci/golangci-lint/releases
GOLANGCI_VER ?= v2.1.6

install-lint: ## Install golangci-lint $(GOLANGCI_VER) (skips if already installed at that version)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Installing golangci-lint $(GOLANGCI_VER)..." -ForegroundColor Cyan
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_VER)
	@Write-Host "[OK] golangci-lint $(GOLANGCI_VER) installed" -ForegroundColor Green
else
	@echo "[INFO] Installing golangci-lint $(GOLANGCI_VER)..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_VER) \
		&& echo "[OK] golangci-lint $(GOLANGCI_VER) installed" \
		|| { echo "[ERROR] Install failed — see https://golangci-lint.run/welcome/install/"; exit 1; }
endif

lint: ## Run all linters (local mode — godox/TODO checks disabled)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Running linters..." -ForegroundColor Cyan
	@golangci-lint run --disable godox ./...
	@Write-Host "[OK] No lint issues found" -ForegroundColor Green
else
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "[ERROR] golangci-lint not found — run: make install-lint"; exit 1; \
	fi
	@echo "[INFO] Running linters..."
	@golangci-lint run --disable godox ./... \
		&& echo "[OK] No lint issues found" \
		|| (echo "[WARN] Lint issues found — see above"; exit 1)
endif

lint-ci: ## Run all linters in CI mode (godox enabled — TODOs/FIXMEs fail the build)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Running linters (CI mode — godox enabled)..." -ForegroundColor Cyan
	@golangci-lint run ./...
	@Write-Host "[OK] No lint issues found" -ForegroundColor Green
else
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "[ERROR] golangci-lint not found — run: make install-lint"; exit 1; \
	fi
	@echo "[INFO] Running linters (CI mode — godox enabled)..."
	@golangci-lint run ./... \
		&& echo "[OK] No lint issues found" \
		|| (echo "[ERROR] Lint issues found"; exit 1)
endif

lint-fix: ## Auto-fix lint issues where possible (gofmt, goimports, unused imports)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Auto-fixing lint issues..." -ForegroundColor Cyan
	@golangci-lint run --fix --disable godox ./...
	@Write-Host "[OK] Auto-fix applied — run 'git diff' to review changes; remaining issues (if any) are shown above" -ForegroundColor Green
else
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "[ERROR] golangci-lint not found — run: make install-lint"; exit 1; \
	fi
	@echo "[INFO] Auto-fixing lint issues..."
	@golangci-lint run --fix --disable godox ./... || true
	@echo "[OK] Auto-fix applied — run 'git diff' to review changes; remaining issues (if any) are shown above"
endif
