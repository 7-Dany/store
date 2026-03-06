# ─── Migration Operations ─────────────────────────────────────────────────────

.PHONY: migrate-status migrate-up migrate-up-one migrate-up-to
.PHONY: migrate-down migrate-down-to migrate-reset migrate-redo migrate-refresh
.PHONY: migrate-create migrate-validate migrate-version

migrate-status: ## Show current migration status
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SCHEMA_DIR))) { Write-Host "[ERROR] Directory '$(SCHEMA_DIR)' not found" -ForegroundColor Red; exit 1 }
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(DB_URL)" status
else
	@if [ ! -d "$(SCHEMA_DIR)" ]; then echo "[ERROR] Directory '$(SCHEMA_DIR)' not found"; exit 1; fi
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(DB_URL)" status
endif

migrate-up: ## Apply all pending migrations
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SCHEMA_DIR))) { Write-Host "[ERROR] Directory '$(SCHEMA_DIR)' not found" -ForegroundColor Red; exit 1 }
	@Write-Host "[INFO] Applying migrations..." -ForegroundColor Cyan
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(DB_URL)" up; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] All migrations applied" -ForegroundColor Green } else { Write-Host "[ERROR] Migration failed" -ForegroundColor Red; exit 1 }
else
	@if [ ! -d "$(SCHEMA_DIR)" ]; then echo "[ERROR] Directory '$(SCHEMA_DIR)' not found"; exit 1; fi
	@echo "[INFO] Applying migrations..."
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(DB_URL)" up && echo "[OK] All migrations applied" || (echo "[ERROR] Migration failed"; exit 1)
endif

migrate-up-one: ## Apply next pending migration only
ifeq ($(DETECTED_OS),Windows)
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(DB_URL)" up-by-one
else
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(DB_URL)" up-by-one
endif

migrate-up-to: ## Apply migrations up to VERSION (VERSION=N)
ifndef VERSION
	@echo "[ERROR] Please specify VERSION=N"
	@exit 1
endif
ifeq ($(DETECTED_OS),Windows)
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(DB_URL)" up-to $(VERSION)
else
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(DB_URL)" up-to $(VERSION)
endif

migrate-down: ## Rollback the last migration
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[WARNING] Rolling back last migration..." -ForegroundColor Yellow
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(DB_URL)" down
else
	@echo "[WARNING] Rolling back last migration..."
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(DB_URL)" down
endif

migrate-down-to: ## Rollback to VERSION (VERSION=N)
ifndef VERSION
	@echo "[ERROR] Please specify VERSION=N"
	@exit 1
endif
ifeq ($(DETECTED_OS),Windows)
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(DB_URL)" down-to $(VERSION)
else
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(DB_URL)" down-to $(VERSION)
endif

migrate-reset: ## Rollback ALL migrations (destructive — prompts for confirmation)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[WARNING] This will rollback ALL migrations" -ForegroundColor Yellow
	@$$response = Read-Host "Type 'yes' to confirm"; if ($$response -ne 'yes') { Write-Host "Aborted"; exit 0 }
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(DB_URL)" reset
else
	@echo "[WARNING] This will rollback ALL migrations"; \
	read -p "Type 'yes' to confirm: " response; \
	if [ "$$response" != "yes" ]; then echo "Aborted"; exit 0; fi; \
	cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(DB_URL)" reset
endif

migrate-refresh: ## Rollback ALL migrations then re-apply them (no confirmation — dev shortcut)
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SCHEMA_DIR))) { Write-Host "[ERROR] Directory '$(SCHEMA_DIR)' not found" -ForegroundColor Red; exit 1 }
	@Write-Host "[INFO] Rolling back all migrations..." -ForegroundColor Cyan
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(DB_URL)" reset; if ($$LASTEXITCODE -ne 0) { Write-Host "[ERROR] Reset failed" -ForegroundColor Red; exit 1 }
	@Write-Host "[INFO] Applying all migrations..." -ForegroundColor Cyan
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(DB_URL)" up; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] migrate-refresh complete" -ForegroundColor Green } else { Write-Host "[ERROR] Migration up failed" -ForegroundColor Red; exit 1 }
else
	@if [ ! -d "$(SCHEMA_DIR)" ]; then echo "[ERROR] Directory '$(SCHEMA_DIR)' not found"; exit 1; fi
	@echo "[INFO] Rolling back all migrations..."
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(DB_URL)" reset && echo "[OK] All migrations rolled back" || (echo "[ERROR] Reset failed"; exit 1)
	@echo "[INFO] Applying all migrations..."
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(DB_URL)" up && echo "[OK] migrate-refresh complete" || (echo "[ERROR] Migration up failed"; exit 1)
endif

migrate-redo: ## Rollback and reapply last migration
ifeq ($(DETECTED_OS),Windows)
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(DB_URL)" redo
else
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(DB_URL)" redo
endif

migrate-create: ## Create new migration file (NAME=name)
ifndef NAME
	@echo "[ERROR] Please specify NAME=migration_name"
	@exit 1
endif
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SCHEMA_DIR))) { New-Item -ItemType Directory -Path $(SCHEMA_DIR) -Force | Out-Null }
	@$$last = Get-ChildItem -Path $(SCHEMA_DIR) -Filter "*.sql" | Where-Object { $$_.Name -match '^(\d+)_' } | ForEach-Object { [int]$$Matches[1] } | Measure-Object -Maximum | Select-Object -ExpandProperty Maximum; if (-not $$last) { $$last = 0 }; $$next = ([string]($$last + 1)).PadLeft(3, '0'); $$file = "$(SCHEMA_DIR)/$$($$next)_$(NAME).sql"; New-Item -ItemType File -Path $$file | Out-Null; Set-Content -Path $$file -Value "-- +goose Up`n-- +goose StatementBegin`n`n`n-- +goose StatementEnd`n`n-- +goose Down`n-- +goose StatementBegin`n`n`n-- +goose StatementEnd"; Write-Host "[OK] Created $$file" -ForegroundColor Green
else
	@if [ ! -d "$(SCHEMA_DIR)" ]; then mkdir -p $(SCHEMA_DIR); fi
	@last=$$(ls $(SCHEMA_DIR)/*.sql 2>/dev/null | grep -oE '[0-9]+(?=_)' | sort -n | tail -1); \
	next=$$(printf "%03d" $$(( $${last:-0} + 1 ))); \
	file="$(SCHEMA_DIR)/$${next}_$(NAME).sql"; \
	printf '-- +goose Up\n-- +goose StatementBegin\n\n\n-- +goose StatementEnd\n\n-- +goose Down\n-- +goose StatementBegin\n\n\n-- +goose StatementEnd\n' > "$$file"; \
	echo "[OK] Created $$file"
endif

migrate-validate: ## Check migration files without running them
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SCHEMA_DIR))) { Write-Host "[ERROR] Directory '$(SCHEMA_DIR)' not found" -ForegroundColor Red; exit 1 }
	@Set-Location $(SCHEMA_DIR); $(GOOSE) -dir . validate; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] All migration files are valid" -ForegroundColor Green } else { Write-Host "[ERROR] Validation failed" -ForegroundColor Red; exit 1 }
else
	@if [ ! -d "$(SCHEMA_DIR)" ]; then echo "[ERROR] Directory '$(SCHEMA_DIR)' not found"; exit 1; fi
	@cd $(SCHEMA_DIR) && $(GOOSE) -dir . validate && echo "[OK] All migration files are valid" || (echo "[ERROR] Validation failed"; exit 1)
endif

migrate-version: ## Show current schema version
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SCHEMA_DIR))) { Write-Host "[ERROR] Directory '$(SCHEMA_DIR)' not found" -ForegroundColor Red; exit 1 }
	@Set-Location $(SCHEMA_DIR); $(GOOSE) postgres "$(DB_URL)" version
else
	@if [ ! -d "$(SCHEMA_DIR)" ]; then echo "[ERROR] Directory '$(SCHEMA_DIR)' not found"; exit 1; fi
	@cd $(SCHEMA_DIR) && $(GOOSE) postgres "$(DB_URL)" version
endif
