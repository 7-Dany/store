# ─── Schema Testing (pgTAP) ──────────────────────────────────────────────────
#
# Uses psql -f to run tests directly — no pg_prove/Perl required.
# pgTAP extension must be installed in PostgreSQL first:
#   make db-test-schema-install-pgtap
#
# USAGE:
#   make db-test-schema                                         # all *_test.sql files
#   make db-test-schema-fresh                                   # migrate-up then test
#   make db-test-schema-file TEST_FILE=sql/tests/foo_test.sql  # single file
#   TEST_PATTERN='*functions_test.sql' make db-test-schema      # filtered subset
# ─────────────────────────────────────────────────────────────────────────────

.PHONY: db-test-schema db-test-schema-verbose db-test-schema-file
.PHONY: db-test-schema-fresh db-test-schema-install-pgtap db-test-schema-check-deps

db-test-schema: ## Run all pgTAP schema tests (TESTS_DIR=sql/tests TEST_PATTERN=*_test.sql)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Running pgTAP schema tests via Docker..." -ForegroundColor Cyan
	@$$files = Get-ChildItem -Path "$(TESTS_DIR)" -Filter "$(TEST_PATTERN)" -Recurse -ErrorAction SilentlyContinue | Sort-Object FullName; if (-not $$files) { Write-Host "[WARNING] No test files matching '$(TEST_PATTERN)' found in '$(TESTS_DIR)'" -ForegroundColor Yellow; exit 0 }
	@$$failed = 0; foreach ($$f in (Get-ChildItem -Path "$(TESTS_DIR)" -Filter "$(TEST_PATTERN)" -Recurse | Sort-Object FullName)) { Write-Host "[RUN] $$($$f.Name)" -ForegroundColor Cyan; Get-Content $$f.FullName | docker exec -i $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -X -q; if ($$LASTEXITCODE -ne 0) { $$failed++ } }; if ($$failed -eq 0) { Write-Host "[OK] All schema tests passed" -ForegroundColor Green } else { Write-Host "[FAIL] $$failed test file(s) failed" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Running pgTAP schema tests via Docker..."
	@if [ ! -d "$(TESTS_DIR)" ]; then echo "[ERROR] Tests directory '$(TESTS_DIR)' not found"; exit 1; fi
	@failed=0; \
	for f in $$(find $(TESTS_DIR) -name "$(TEST_PATTERN)" | sort); do \
		echo "[RUN] $$f"; \
		docker exec -i $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -X -q < $$f || failed=$$((failed+1)); \
	done; \
	if [ $$failed -eq 0 ]; then echo "[OK] All schema tests passed"; \
	else echo "[FAIL] $$failed test file(s) failed"; exit 1; fi
endif

db-test-schema-verbose: ## Run pgTAP tests with full TAP output + timing
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Running pgTAP schema tests (verbose)..." -ForegroundColor Cyan
	@$$files = Get-ChildItem -Path "$(TESTS_DIR)" -Filter "$(TEST_PATTERN)" -Recurse -ErrorAction SilentlyContinue | Sort-Object FullName; if (-not $$files) { Write-Host "[WARNING] No test files found in '$(TESTS_DIR)'" -ForegroundColor Yellow; exit 0 }
	@$$failed = 0; foreach ($$f in (Get-ChildItem -Path "$(TESTS_DIR)" -Filter "$(TEST_PATTERN)" -Recurse | Sort-Object FullName)) { Write-Host "[RUN] $$($$f.Name)" -ForegroundColor Cyan; $$start = Get-Date; Get-Content $$f.FullName | docker exec -i $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -X; $$elapsed = ((Get-Date) - $$start).TotalMilliseconds; if ($$LASTEXITCODE -ne 0) { $$failed++; Write-Host "[FAIL] $$($$f.Name) ($([math]::Round($$elapsed))ms)" -ForegroundColor Red } else { Write-Host "[OK] $$($$f.Name) ($([math]::Round($$elapsed))ms)" -ForegroundColor Green } }; if ($$failed -eq 0) { Write-Host "[OK] All schema tests passed" -ForegroundColor Green } else { Write-Host "[FAIL] $$failed test file(s) failed" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Running pgTAP schema tests (verbose)..."
	@if [ ! -d "$(TESTS_DIR)" ]; then echo "[ERROR] Tests directory '$(TESTS_DIR)' not found"; exit 1; fi
	@failed=0; \
	for f in $$(find $(TESTS_DIR) -name "$(TEST_PATTERN)" | sort); do \
		echo "[RUN] $$f"; \
		start=$$(date +%s%3N); \
		docker exec -i $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -X < $$f || failed=$$((failed+1)); \
		elapsed=$$(( $$(date +%s%3N) - start )); \
		echo "    (took $${elapsed}ms)"; \
	done; \
	if [ $$failed -eq 0 ]; then echo "[OK] All schema tests passed"; \
	else echo "[FAIL] $$failed test file(s) failed"; exit 1; fi
endif

db-test-schema-file: ## Run a single pgTAP test file (TEST_FILE=path/to/file_test.sql)
ifndef TEST_FILE
	@echo "[ERROR] Please specify TEST_FILE=path/to/file_test.sql"
	@exit 1
endif
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path "$(TEST_FILE)")) { Write-Host "[ERROR] Test file '$(TEST_FILE)' not found" -ForegroundColor Red; exit 1 }
	@Write-Host "[RUN] $(TEST_FILE)" -ForegroundColor Cyan
	@Get-Content "$(TEST_FILE)" | docker exec -i $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -X; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Test passed" -ForegroundColor Green } else { Write-Host "[FAIL] Test failed" -ForegroundColor Red; exit 1 }
else
	@if [ ! -f "$(TEST_FILE)" ]; then echo "[ERROR] Test file '$(TEST_FILE)' not found"; exit 1; fi
	@echo "[RUN] $(TEST_FILE)"
	@docker exec -i $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -X < "$(TEST_FILE)" \
	&& echo "[OK] Test passed" \
	|| (echo "[FAIL] Test failed"; exit 1)
endif

db-test-schema-fresh: migrate-up db-test-schema ## Apply all pending migrations then run all schema tests
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[OK] Fresh migration + schema tests complete" -ForegroundColor Green
else
	@echo "[OK] Fresh migration + schema tests complete"
endif

db-test-schema-install-pgtap: ## Install pgTAP extension into the target database (via Docker container)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Installing pgTAP extension in '$(DB_NAME)' via Docker..." -ForegroundColor Cyan
	@& { docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -c 'CREATE EXTENSION IF NOT EXISTS pgtap;'; if ($$LASTEXITCODE -eq 0) { Write-Host '[OK] pgTAP installed (or already present)' -ForegroundColor Green } else { Write-Host '[ERROR] pgTAP install failed - is the container running? Try: make docker-up' -ForegroundColor Red; exit 1 } }
else
	@echo "[INFO] Installing pgTAP extension in '$(DB_NAME)' via Docker..."
	@docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -c "CREATE EXTENSION IF NOT EXISTS pgtap;" \
	&& echo "[OK] pgTAP installed (or already present)" \
	|| { echo "[ERROR] pgTAP install failed — is the container running? Try: make docker-up"; exit 1; }
endif

db-test-schema-check-deps: ## Check Docker container is running and pgTAP is installed
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Checking pgTAP dependencies..." -ForegroundColor Cyan
	@Write-Host "[INFO] Looking for container: $(PGTAP_CONTAINER)" -ForegroundColor Cyan
	@if (-not (docker ps --format '{{.Names}}' | Select-String -Quiet -SimpleMatch '$(PGTAP_CONTAINER)')) { Write-Host '[ERROR] Container $(PGTAP_CONTAINER) is not running - run: make docker-up' -ForegroundColor Red; exit 1 }; Write-Host '[OK] Container $(PGTAP_CONTAINER) is running' -ForegroundColor Green
	@$$q = 'SELECT installed_version FROM pg_available_extensions WHERE name = ' + [char]39 + 'pgtap' + [char]39 + ';'; $$v = docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -tAc $$q; if ($$LASTEXITCODE -ne 0) { Write-Host '[ERROR] Could not query container - try: make docker-up' -ForegroundColor Red; exit 1 }; $$v = if ($$v) { $$v.Trim() } else { '' }; if ($$v) { Write-Host ('[OK] pgTAP ' + $$v + ' is installed in $(DB_NAME)') -ForegroundColor Green } else { Write-Host '[ERROR] pgTAP not installed - run: make db-test-schema-install-pgtap' -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Checking pgTAP dependencies..."
	@echo "[INFO] Looking for container: $(PGTAP_CONTAINER)"
	@if docker ps --filter "name=^$(PGTAP_CONTAINER)$$" --format "{{.Names}}" | grep -q "$(PGTAP_CONTAINER)"; then \
		echo "[OK] Container '$(PGTAP_CONTAINER)' is running"; \
	else \
		echo "[ERROR] Container '$(PGTAP_CONTAINER)' is not running — run: make docker-up"; exit 1; \
	fi
	@ver=$$(docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -tAc \
		"SELECT installed_version FROM pg_available_extensions WHERE name = 'pgtap';" 2>&1) \
	|| { echo "[ERROR] Could not query container — is it healthy? Try: make docker-up"; exit 1; }; \
	ver=$$(echo "$$ver" | tr -d '[:space:]'); \
	if [ -n "$$ver" ]; then \
		echo "[OK] pgTAP $$ver is installed in $(DB_NAME)"; \
	else \
		echo "[ERROR] pgTAP extension not installed — run: make db-test-schema-install-pgtap"; exit 1; \
	fi
endif
