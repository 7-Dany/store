# ─── Database Operations ─────────────────────────────────────────────────────

.PHONY: db-create db-drop db-reset db-connect db-dump db-restore
.PHONY: db-tables db-schema db-indexes db-functions db-size db-table-sizes

db-create: ## Create the database
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Creating database $(DB_NAME)..." -ForegroundColor Cyan
	@$$ErrorActionPreference = 'SilentlyContinue'; $(SET_PGPASS) $(PSQL) -d postgres -c "CREATE DATABASE $(DB_NAME);" 2>$(NULL); if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Database '$(DB_NAME)' created successfully" -ForegroundColor Green } else { Write-Host "[WARNING] Database '$(DB_NAME)' may already exist" -ForegroundColor Yellow }
else
	@echo "[INFO] Creating database $(DB_NAME)..."
	@$(SET_PGPASS) $(PSQL) -d postgres -c "CREATE DATABASE $(DB_NAME);" 2>$(NULL) && echo "[OK] Database '$(DB_NAME)' created successfully" || echo "[WARNING] Database '$(DB_NAME)' may already exist"
endif

db-drop: ## Drop the database (destructive!)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[WARNING] This will permanently delete database '$(DB_NAME)'" -ForegroundColor Yellow
	@$$response = Read-Host "Type 'yes' to confirm"; if ($$response -ne 'yes') { Write-Host "Aborted" -ForegroundColor Cyan; exit 0 }
	@$$ErrorActionPreference = 'SilentlyContinue'; $(SET_PGPASS) $(PSQL) -d postgres -c "DROP DATABASE IF EXISTS $(DB_NAME);" *>$(NULL); $$global:LASTEXITCODE = 0
	@Write-Host "[OK] Database '$(DB_NAME)' dropped" -ForegroundColor Green
else
	@echo "[WARNING] This will permanently delete database '$(DB_NAME)'"; \
	read -p "Type 'yes' to confirm: " response; \
	if [ "$$response" != "yes" ]; then echo "Aborted"; exit 0; fi; \
	$(SET_PGPASS) $(PSQL) -d postgres -c "DROP DATABASE IF EXISTS $(DB_NAME);" 2>$(NULL); \
	echo "[OK] Database '$(DB_NAME)' dropped"
endif

db-reset: db-drop db-create migrate-up seed ## Complete database reset
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[OK] Database reset complete - '$(DB_NAME)' is ready" -ForegroundColor Green
else
	@echo "[OK] Database reset complete - '$(DB_NAME)' is ready"
endif

db-connect: ## Connect to database with psql
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Connecting to '$(DB_NAME)'..." -ForegroundColor Cyan
	@$(SET_PGPASS) $(PSQL) -d $(DB_NAME)
else
	@echo "[INFO] Connecting to '$(DB_NAME)'..."
	@$(SET_PGPASS) $(PSQL) -d $(DB_NAME)
endif

db-dump: ## Create database backup
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Creating backup of '$(DB_NAME)'..." -ForegroundColor Cyan
	@if (-not (Test-Path sql)) { New-Item -ItemType Directory -Path sql -Force | Out-Null }
	@$$timestamp = Get-Date -Format "yyyyMMdd_HHmmss"; $$file = "sql/backup_$$timestamp.sql"; $(SET_PGPASS) $(PG_DUMP) -d $(DB_NAME) > $$file; Write-Host "[OK] Backed up to $$file" -ForegroundColor Green
else
	@echo "[INFO] Creating backup of '$(DB_NAME)'..."
	@mkdir -p sql
	@timestamp=$$(date +%Y%m%d_%H%M%S); file="sql/backup_$$timestamp.sql"; $(SET_PGPASS) $(PG_DUMP) -d $(DB_NAME) > $$file; echo "[OK] Backed up to $$file"
endif

db-restore: ## Restore from backup (BACKUP_FILE=path/to/file.sql)
ifndef BACKUP_FILE
	@echo "[ERROR] Please specify BACKUP_FILE=path/to/file.sql"
	@exit 1
endif
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(BACKUP_FILE))) { Write-Host "[ERROR] File '$(BACKUP_FILE)' not found" -ForegroundColor Red; exit 1 }
	@Write-Host "[INFO] Restoring from $(BACKUP_FILE)..." -ForegroundColor Cyan
	@$(SET_PGPASS) Get-Content $(BACKUP_FILE) | $(PSQL) -d $(DB_NAME)
	@Write-Host "[OK] Database restored" -ForegroundColor Green
else
	@if [ ! -f "$(BACKUP_FILE)" ]; then echo "[ERROR] File '$(BACKUP_FILE)' not found"; exit 1; fi
	@echo "[INFO] Restoring from $(BACKUP_FILE)..."
	@$(SET_PGPASS) $(PSQL) -d $(DB_NAME) < $(BACKUP_FILE)
	@echo "[OK] Database restored"
endif

db-tables: ## List all database tables
	@$(SET_PGPASS) $(PSQL) -d $(DB_NAME) -c "\dt"

db-schema: ## Show complete database schema
	@$(SET_PGPASS) $(PSQL) -d $(DB_NAME) -c "\d+"

db-indexes: ## List all database indexes
	@$(SET_PGPASS) $(PSQL) -d $(DB_NAME) -c "\di"

db-functions: ## List all database functions
	@$(SET_PGPASS) $(PSQL) -d $(DB_NAME) -c "\df"

db-size: ## Show total database size
	@$(SET_PGPASS) $(PSQL) -d $(DB_NAME) -c "SELECT pg_size_pretty(pg_database_size('$(DB_NAME)')) as size;"

db-table-sizes: ## Show size of each table
	@$(SET_PGPASS) $(PSQL) -d $(DB_NAME) -c "SELECT tablename, pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) AS size FROM pg_tables WHERE schemaname = 'public' ORDER BY pg_total_relation_size(schemaname||'.'||tablename) DESC;"
