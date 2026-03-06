# ─── Go Tests ────────────────────────────────────────────────────────────────
#
# Four test modes:
#   make test              — unit tests only (fast, no external services)
#   make test-integration  — integration tests only (requires TEST_DATABASE_URL
#                            and TEST_REDIS_URL; run docker-compose up first)
#   make test-all          — unit + integration (same flags as test-integration)
#   make test-coverage     — unit + integration tests with HTML coverage report
#
# Integration tests that require a live database are gated by the build tag
# `integration_test`.  Redis tests carry no build tag and skip at runtime when
# TEST_REDIS_URL / REDIS_URL are unset.
# Run `make test-db-setup` once before using test-integration.
# ─────────────────────────────────────────────────────────────────────────────

.PHONY: test test-integration test-all test-coverage test-coverage-all test-uncovered

# Directories to test — override on the command line: PKG=./internal/platform/respond/...
PKG ?= ./...

# Flags shared by all test invocations
# -race requires cgo (gcc). Omitted here — enable explicitly if you have gcc:
#   TEST_FLAGS="-race -count=1" make test
TEST_FLAGS ?= -count=1

# Output file for test-uncovered report — override: UNCOVERED_FILE=my.txt make test-uncovered
UNCOVERED_FILE ?= uncovered.txt

# Go module prefix stripped from file paths in the coverage report
MODULE := github.com/7-Dany/store/backend/

# Comma-separated path prefixes to exclude from the coverage report.
# internal/db is sqlc-generated and has no business logic to test.
# Override: COVERAGE_EXCLUDE=internal/db,internal/mock make test-uncovered
COVERAGE_EXCLUDE ?= internal/db

test: ## Run unit tests (no build tags — excludes integration_test files)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Running unit tests..." -ForegroundColor Cyan
	@go test $(TEST_FLAGS) $(PKG); if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Unit tests passed" -ForegroundColor Green } else { Write-Host "[ERROR] Unit tests failed" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Running unit tests..."
	@go test $(TEST_FLAGS) $(PKG) && echo "[OK] Unit tests passed" || (echo "[ERROR] Unit tests failed"; exit 1)
endif

test-integration: ## Run integration tests (requires TEST_DATABASE_URL set in .env)
ifeq ($(DETECTED_OS),Windows)
	@& { if ([string]::IsNullOrEmpty($$env:TEST_DATABASE_URL)) { Write-Host "[ERROR] TEST_DATABASE_URL is not set - run: make test-db-setup" -ForegroundColor Red; exit 1 }; Write-Host "[INFO] Running integration tests..." -ForegroundColor Cyan; go test $(TEST_FLAGS) -tags integration_test $(PKG); if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Integration tests passed" -ForegroundColor Green } else { Write-Host "[ERROR] Integration tests failed" -ForegroundColor Red; exit 1 } }
else
	@if [ -z "$$TEST_DATABASE_URL" ]; then echo "[ERROR] TEST_DATABASE_URL is not set - run: make test-db-setup"; exit 1; fi
	@echo "[INFO] Running integration tests..."
	@go test $(TEST_FLAGS) -tags integration_test $(PKG) && echo "[OK] Integration tests passed" || (echo "[ERROR] Integration tests failed"; exit 1)
endif

test-all: ## Run unit and integration tests together
ifeq ($(DETECTED_OS),Windows)
	@& { if ([string]::IsNullOrEmpty($$env:TEST_DATABASE_URL)) { Write-Host "[ERROR] TEST_DATABASE_URL is not set - run: make test-db-setup" -ForegroundColor Red; exit 1 }; Write-Host "[INFO] Running all tests (unit + integration)..." -ForegroundColor Cyan; go test $(TEST_FLAGS) -tags integration_test $(PKG); if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] All tests passed" -ForegroundColor Green } else { Write-Host "[ERROR] Tests failed" -ForegroundColor Red; exit 1 } }
else
	@if [ -z "$$TEST_DATABASE_URL" ]; then echo "[ERROR] TEST_DATABASE_URL is not set - run: make test-db-setup"; exit 1; fi
	@echo "[INFO] Running all tests (unit + integration)..."
	@go test $(TEST_FLAGS) -tags integration_test $(PKG) && echo "[OK] All tests passed" || (echo "[ERROR] Tests failed"; exit 1)
endif

test-coverage: ## Run unit + integration tests and open an HTML coverage report
ifeq ($(DETECTED_OS),Windows)
	@& { if ([string]::IsNullOrEmpty($$env:TEST_DATABASE_URL)) { Write-Host "[ERROR] TEST_DATABASE_URL is not set - run: make test-db-setup" -ForegroundColor Red; exit 1 }; Write-Host "[INFO] Running all tests with coverage (unit + integration)..." -ForegroundColor Cyan; go test $(TEST_FLAGS) -tags integration_test "-coverprofile=coverage.out" $(PKG); if ($$LASTEXITCODE -ne 0) { Write-Host "[ERROR] Tests failed" -ForegroundColor Red; exit 1 }; go tool cover "-html=coverage.out" "-o" "coverage.html"; Write-Host "[OK] Coverage report written to coverage.html" -ForegroundColor Green; Start-Process "coverage.html" }
else
	@if [ -z "$$TEST_DATABASE_URL" ]; then echo "[ERROR] TEST_DATABASE_URL is not set - run: make test-db-setup"; exit 1; fi
	@echo "[INFO] Running all tests with coverage (unit + integration)..."
	@go test $(TEST_FLAGS) -tags integration_test -coverprofile=coverage.out $(PKG) && echo "[OK] Tests passed" || (echo "[ERROR] Tests failed"; exit 1)
	@go tool cover -html=coverage.out -o coverage.html
	@echo "[OK] Coverage report written to coverage.html"
	@open coverage.html 2>/dev/null || xdg-open coverage.html 2>/dev/null || echo "  Open coverage.html in your browser"
endif

test-coverage-all: ## Run unit + integration tests with HTML coverage report (requires Docker)
ifeq ($(DETECTED_OS),Windows)
	@& { Write-Host "[INFO] Running all tests with coverage (unit + integration)..." -ForegroundColor Cyan; go test $(TEST_FLAGS) -tags integration_test "-coverprofile=coverage.out" $(PKG); if ($$LASTEXITCODE -ne 0) { Write-Host "[ERROR] Tests failed" -ForegroundColor Red; exit 1 }; go tool cover "-html=coverage.out" "-o" "coverage.html"; Write-Host "[OK] Coverage report written to coverage.html" -ForegroundColor Green; Start-Process "coverage.html" }
else
	@echo "[INFO] Running all tests with coverage (unit + integration)..."
	@go test $(TEST_FLAGS) -tags integration_test -coverprofile=coverage.out $(PKG) && echo "[OK] Tests passed" || (echo "[ERROR] Tests failed"; exit 1)
	@go tool cover -html=coverage.out -o coverage.html
	@echo "[OK] Coverage report written to coverage.html"
	@open coverage.html 2>/dev/null || xdg-open coverage.html 2>/dev/null || echo "  Open coverage.html in your browser"
endif

# ─── test-uncovered ───────────────────────────────────────────────────────────
# Runs unit tests then prints each incomplete file sorted worst-first.
# Under each file, every function that is not fully covered is listed with its
# line number so you can jump straight to it.  Files at 100% are omitted.
# The same output (without ANSI colour) is written to $(UNCOVERED_FILE).
#
# Powered by make/coverage_report.go (//go:build ignore) — cross-platform,
# no shell-escaping issues, same behaviour on Windows and Linux.
#
# Usage:
#   make test-uncovered
#   make test-uncovered UNCOVERED_FILE=report.txt
# ─────────────────────────────────────────────────────────────────────────────
test-uncovered: ## Per-file+function coverage sorted lowest-first; print to terminal + save to $(UNCOVERED_FILE)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Running unit tests with coverage..." -ForegroundColor Cyan
	@go test $(TEST_FLAGS) "-coverprofile=coverage.out" $(PKG) | Out-Null; if ($$LASTEXITCODE -ne 0) { Write-Host "[ERROR] Tests failed" -ForegroundColor Red; exit 1 }
	@go run make/coverage_report.go coverage.out "$(UNCOVERED_FILE)" "$(MODULE)" "$(COVERAGE_EXCLUDE)"
else
	@echo "[INFO] Running unit tests with coverage..."
	@go test $(TEST_FLAGS) -coverprofile=coverage.out $(PKG) > /dev/null || (echo "[ERROR] Tests failed"; exit 1)
	@go run make/coverage_report.go coverage.out $(UNCOVERED_FILE) $(MODULE) $(COVERAGE_EXCLUDE)
endif
