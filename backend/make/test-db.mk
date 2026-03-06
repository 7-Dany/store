# ─── Test Database ───────────────────────────────────────────────────────────
#
# All targets here operate on TEST_DB_NAME (default: <DB_NAME>_test) using the
# same server credentials as the dev database.  TEST_DATABASE_URL is used
# verbatim by goose — it matches what Go tests read from the environment.
#
# Typical first-time workflow:
#   make test-db-setup          # create + migrate in one shot
#
# Reset before a test run:
#   make test-db-reset          # drop + create + migrate
# ─────────────────────────────────────────────────────────────────────────────

.PHONY: test-db-create test-db-drop test-db-reset test-db-setup test-db-connect
.PHONY: test-migrate-status test-migrate-up test-migrate-down test-migrate-reset test-migrate-version test-migrate-refresh
.PHONY: test-seed test-seed-reset test-seed-status

# ── Test Database ─────────────────────────────────────────────────────────────

test-db-create: ## Create the test database
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Creating test database $(TEST_DB_NAME)..." -ForegroundColor Cyan
	@$$ErrorActionPreference = 'SilentlyContinue'; $(SET_PGPASS) $(PSQL) -d postgres -c "CREATE DATABASE $(TEST_DB_NAME);" 2>$(NULL); if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Test database '$(TEST_DB_NAME)' created" -ForegroundColor Green } else { Write-Host "[WARNING] Test database '$(TEST_DB_NAME)' may already exist" -ForegroundColor Yellow }
else
	@echo "[INFO] Creating test database $(TEST_DB_NAME)..."
	@$(SET_PGPASS) $(PSQL) -d postgres -c "CREATE DATABASE $(TEST_DB_NAME);" 2>$(NULL) && echo "[OK] Test database '$(TEST_DB_NAME)' created" || echo "[WARNING] Test database '$(TEST_DB_NAME)' may already exist"
endif

test-db-drop: ## Drop the test database (no confirmation — test DBs are disposable)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Dropping test database $(TEST_DB_NAME)..." -ForegroundColor Cyan
	@$$ErrorActionPreference = 'SilentlyContinue'; $(SET_PGPASS) $(PSQL) -d postgres -c "DROP DATABASE IF EXISTS $(TEST_DB_NAME);" *>$(NULL); $$global:LASTEXITCODE = 0
	@Write-Host "[OK] Test database '$(TEST_DB_NAME)' dropped" -ForegroundColor Green
else
	@echo "[INFO] Dropping test database $(TEST_DB_NAME)..."
	@$(SET_PGPASS) $(PSQL) -d postgres -c "DROP DATABASE IF EXISTS $(TEST_DB_NAME);" 2>$(NULL) || true
	@echo "[OK] Test database '$(TEST_DB_NAME)' dropped"
endif

test-db-reset: test-db-drop test-db-create test-migrate-up ## Drop, create, and migrate the test database
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[OK] Test database reset — '$(TEST_DB_NAME)' is ready" -ForegroundColor Green
else
	@echo "[OK] Test database reset — '$(TEST_DB_NAME)' is ready"
endif

test-db-setup: test-db-create test-migrate-up ## Create test database and apply all migrations (idempotent first-run)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[OK] Test database setup complete" -ForegroundColor Green
else
	@echo "[OK] Test database setup complete"
endif

test-db-connect: ## Connect to the test database with psql
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Connecting to test database '$(TEST_DB_NAME)'..." -ForegroundColor Cyan
	@$(SET_PGPASS) $(PSQL) -d $(TEST_DB_NAME)
else
	@echo "[INFO] Connecting to test database '$(TEST_DB_NAME)'..."
	@$(SET_PGPASS) $(PSQL) -d $(TEST_DB_NAME)
endif

# ── Test Migrations ───────────────────────────────────────────────────────────

test-migrate-status: ## Show migration status of the test database
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SCHEMA_DIR))) { Write-Host "[ERROR] Directory '$(SCHEMA_DIR)' not found" -ForegroundColor Red; exit 1 }
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(TEST_DATABASE_URL)" status
else
	@if [ ! -d "$(SCHEMA_DIR)" ]; then echo "[ERROR] Directory '$(SCHEMA_DIR)' not found"; exit 1; fi
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(TEST_DATABASE_URL)" status
endif

test-migrate-up: ## Apply all pending migrations to the test database
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SCHEMA_DIR))) { Write-Host "[ERROR] Directory '$(SCHEMA_DIR)' not found" -ForegroundColor Red; exit 1 }
	@Write-Host "[INFO] Applying migrations to test database..." -ForegroundColor Cyan
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(TEST_DATABASE_URL)" up; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Test migrations applied" -ForegroundColor Green } else { Write-Host "[ERROR] Migration failed" -ForegroundColor Red; exit 1 }
else
	@if [ ! -d "$(SCHEMA_DIR)" ]; then echo "[ERROR] Directory '$(SCHEMA_DIR)' not found"; exit 1; fi
	@echo "[INFO] Applying migrations to test database..."
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(TEST_DATABASE_URL)" up && echo "[OK] Test migrations applied" || (echo "[ERROR] Migration failed"; exit 1)
endif

test-migrate-down: ## Rollback the last migration on the test database
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[WARNING] Rolling back last test migration..." -ForegroundColor Yellow
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(TEST_DATABASE_URL)" down
else
	@echo "[WARNING] Rolling back last test migration..."
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(TEST_DATABASE_URL)" down
endif

test-migrate-reset: ## Rollback ALL migrations on the test database (no confirmation needed)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Rolling back all test migrations..." -ForegroundColor Cyan
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(TEST_DATABASE_URL)" reset; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] All test migrations rolled back" -ForegroundColor Green } else { Write-Host "[ERROR] Reset failed" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Rolling back all test migrations..."
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(TEST_DATABASE_URL)" reset && echo "[OK] All test migrations rolled back" || (echo "[ERROR] Reset failed"; exit 1)
endif

test-migrate-refresh: ## Rollback ALL test migrations then re-apply them (no confirmation — dev shortcut)
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SCHEMA_DIR))) { Write-Host "[ERROR] Directory '$(SCHEMA_DIR)' not found" -ForegroundColor Red; exit 1 }
	@Write-Host "[INFO] Rolling back all test migrations..." -ForegroundColor Cyan
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(TEST_DATABASE_URL)" reset; if ($$LASTEXITCODE -ne 0) { Write-Host "[ERROR] Reset failed" -ForegroundColor Red; exit 1 }
	@Write-Host "[INFO] Applying all test migrations..." -ForegroundColor Cyan
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(TEST_DATABASE_URL)" up; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] test-migrate-refresh complete" -ForegroundColor Green } else { Write-Host "[ERROR] Migration up failed" -ForegroundColor Red; exit 1 }
else
	@if [ ! -d "$(SCHEMA_DIR)" ]; then echo "[ERROR] Directory '$(SCHEMA_DIR)' not found"; exit 1; fi
	@echo "[INFO] Rolling back all test migrations..."
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(TEST_DATABASE_URL)" reset && echo "[OK] All test migrations rolled back" || (echo "[ERROR] Reset failed"; exit 1)
	@echo "[INFO] Applying all test migrations..."
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(TEST_DATABASE_URL)" up && echo "[OK] test-migrate-refresh complete" || (echo "[ERROR] Migration up failed"; exit 1)
endif

test-migrate-version: ## Show current migration version of the test database
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SCHEMA_DIR))) { Write-Host "[ERROR] Directory '$(SCHEMA_DIR)' not found" -ForegroundColor Red; exit 1 }
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(TEST_DATABASE_URL)" version
else
	@if [ ! -d "$(SCHEMA_DIR)" ]; then echo "[ERROR] Directory '$(SCHEMA_DIR)' not found"; exit 1; fi
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(TEST_DATABASE_URL)" version
endif

# ── Test Seeds ────────────────────────────────────────────────────────────────

test-seed: ## Apply seed data to the test database
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SEEDS_DIR))) { Write-Host "[ERROR] Directory '$(SEEDS_DIR)' not found" -ForegroundColor Red; exit 1 }
	@Write-Host "[INFO] Applying seed data to test database..." -ForegroundColor Cyan
	@Set-Location $(SEEDS_DIR); $(GOOSE) -table goose_seed_version postgres "$(TEST_DATABASE_URL)" up; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Test seed data applied" -ForegroundColor Green } else { Write-Host "[ERROR] Seeding failed" -ForegroundColor Red; exit 1 }
else
	@if [ ! -d "$(SEEDS_DIR)" ]; then echo "[ERROR] Directory '$(SEEDS_DIR)' not found"; exit 1; fi
	@echo "[INFO] Applying seed data to test database..."
	@cd $(SEEDS_DIR) && $(GOOSE) -table goose_seed_version postgres "$(TEST_DATABASE_URL)" up && echo "[OK] Test seed data applied" || (echo "[ERROR] Seeding failed"; exit 1)
endif

test-seed-reset: ## Reset seed data on the test database (rollback then reapply)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Resetting seed data on test database..." -ForegroundColor Cyan
	@Set-Location $(SEEDS_DIR); $(GOOSE) -table goose_seed_version postgres "$(TEST_DATABASE_URL)" down-to 0; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Test seeds rolled back" -ForegroundColor Green } else { Write-Host "[ERROR] Seed rollback failed" -ForegroundColor Red; exit 1 }
	@Set-Location $(SEEDS_DIR); $(GOOSE) -table goose_seed_version postgres "$(TEST_DATABASE_URL)" up; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Test seed data reset" -ForegroundColor Green } else { Write-Host "[ERROR] Seeding failed" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Resetting seed data on test database..."
	@cd $(SEEDS_DIR) && $(GOOSE) -table goose_seed_version postgres "$(TEST_DATABASE_URL)" down-to 0 && echo "[OK] Test seeds rolled back" || (echo "[ERROR] Seed rollback failed"; exit 1)
	@cd $(SEEDS_DIR) && $(GOOSE) -table goose_seed_version postgres "$(TEST_DATABASE_URL)" up && echo "[OK] Test seed data reset" || (echo "[ERROR] Seeding failed"; exit 1)
endif

test-seed-status: ## Show seed status of the test database
ifeq ($(DETECTED_OS),Windows)
	@Set-Location $(SEEDS_DIR); $(GOOSE) -table goose_seed_version postgres "$(TEST_DATABASE_URL)" status
else
	@cd $(SEEDS_DIR) && $(GOOSE) -table goose_seed_version postgres "$(TEST_DATABASE_URL)" status
endif
