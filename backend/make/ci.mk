# ─── Validation & CI ─────────────────────────────────────────────────────────

.PHONY: check ci-test ci-clean test-makefile

check: migrate-validate lint db-test-schema ## Run all validations (lint + migrate-validate + schema tests)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[OK] All checks passed" -ForegroundColor Green
else
	@echo "[OK] All checks passed"
endif

ci-test: install-tools db-create migrate-up lint-ci db-test-schema-install-pgtap db-test-schema ## CI/CD pipeline: install tools, setup DB, lint, run tests
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[OK] CI pipeline complete" -ForegroundColor Green
else
	@echo "[OK] CI pipeline complete"
endif

ci-clean: ## CI/CD cleanup: drop the test database
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] CI cleanup: dropping '$(DB_NAME)'..." -ForegroundColor Cyan
	@$$ErrorActionPreference = 'SilentlyContinue'; $(SET_PGPASS) $(PSQL) -d postgres -c "DROP DATABASE IF EXISTS $(DB_NAME);" *>$(NULL); $$global:LASTEXITCODE = 0
	@Write-Host "[OK] CI cleanup complete" -ForegroundColor Green
else
	@echo "[INFO] CI cleanup: dropping '$(DB_NAME)'..."
	@$(SET_PGPASS) $(PSQL) -d postgres -c "DROP DATABASE IF EXISTS $(DB_NAME);" 2>$(NULL) || true
	@echo "[OK] CI cleanup complete"
endif

test-makefile: ## Self-test: verify required directories and tools exist
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Running Makefile self-test..." -ForegroundColor Cyan
	@$$ok = $$true
	@foreach ($$dir in @('$(SCHEMA_DIR)', '$(SEEDS_DIR)', '$(QUERIES_DIR)')) { if (Test-Path $$dir) { Write-Host "[OK] Dir exists: $$dir" -ForegroundColor Green } else { Write-Host "[WARN] Dir missing: $$dir" -ForegroundColor Yellow; $$ok = $$false } }
	@foreach ($$tool in @('goose', 'sqlc', 'psql', 'docker')) { if (Get-Command $$tool -ErrorAction SilentlyContinue) { Write-Host "[OK] Tool found: $$tool" -ForegroundColor Green } else { Write-Host "[WARN] Tool missing: $$tool" -ForegroundColor Yellow; $$ok = $$false } }
	@if ($$ok) { Write-Host "[OK] All checks passed" -ForegroundColor Green } else { Write-Host "[WARN] Some checks failed — see above" -ForegroundColor Yellow }
else
	@echo "[INFO] Running Makefile self-test..."
	@ok=true; \
	for dir in $(SCHEMA_DIR) $(SEEDS_DIR) $(QUERIES_DIR); do \
		if [ -d "$$dir" ]; then echo "[OK] Dir exists: $$dir"; \
		else echo "[WARN] Dir missing: $$dir"; ok=false; fi; \
	done; \
	for tool in goose sqlc psql docker; do \
		if command -v $$tool >/dev/null 2>&1; then echo "[OK] Tool found: $$tool"; \
		else echo "[WARN] Tool missing: $$tool"; ok=false; fi; \
	done; \
	if $$ok; then echo "[OK] All checks passed"; else echo "[WARN] Some checks failed — see above"; fi
endif
