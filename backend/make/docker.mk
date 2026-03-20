# ─── Docker Operations ────────────────────────────────────────────────────────

.PHONY: docker-up docker-up-full docker-down docker-logs docker-setup
.PHONY: docker-db-tables docker-db-schema docker-db-indexes docker-db-functions
.PHONY: docker-db-size docker-db-table-sizes docker-db-connect docker-db-dump
.PHONY: docker-db-restore docker-db-reset
.PHONY: docker-migrate-status docker-migrate-up docker-migrate-down
.PHONY: docker-migrate-reset docker-migrate-redo docker-migrate-version
.PHONY: docker-seed docker-seed-reset docker-seed-status
.PHONY: docker-redis-cli docker-redis-flush docker-redis-logs
.PHONY: prometheus-up prometheus-down prometheus-logs prometheus-reload prometheus-status prometheus-open prometheus-purge

REDIS_PORT_DOCKER    ?= 6380
REDIS_PASSWORD       ?= changeme
REDIS_CONTAINER      ?= $(COMPOSE_PROJECT_NAME)_redis

PROMETHEUS_PORT      ?= 9090
PROMETHEUS_CONTAINER ?= $(COMPOSE_PROJECT_NAME)_prometheus

# ── Container Lifecycle ───────────────────────────────────────────────────────
#
# All targets use `docker-compose up -d --wait` which blocks until every
# container with a healthcheck reports healthy, then exits 0. No manual
# polling loops needed. Requires Docker Compose v2.1+ (Docker Desktop ships this).

docker-up: ## Start PostgreSQL and Redis and wait until both are healthy
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Starting postgres + redis..." -ForegroundColor Cyan
	@docker-compose up -d --wait postgres redis
	@Write-Host "[OK] postgres + redis are ready" -ForegroundColor Green
else
	@echo "[INFO] Starting postgres + redis..."
	@docker-compose up -d --wait postgres redis
	@echo "[OK] postgres + redis are ready"
endif

docker-up-full: ## Start PostgreSQL, Redis, and Prometheus (full dev stack)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Starting postgres + redis + prometheus..." -ForegroundColor Cyan
	@docker-compose up -d --wait postgres redis prometheus
	@Write-Host "[OK] Full dev stack ready — Prometheus at http://localhost:$(PROMETHEUS_PORT)" -ForegroundColor Green
else
	@echo "[INFO] Starting postgres + redis + prometheus..."
	@docker-compose up -d --wait postgres redis prometheus
	@echo "[OK] Full dev stack ready — Prometheus at http://localhost:$(PROMETHEUS_PORT)"
endif

docker-down: ## Stop all containers (postgres, redis, prometheus)
ifeq ($(DETECTED_OS),Windows)
	@docker-compose down
	@Write-Host "[OK] All containers stopped" -ForegroundColor Green
else
	@docker-compose down
	@echo "[OK] All containers stopped"
endif

docker-logs: ## Tail all container logs
	@docker-compose logs -f

docker-setup: docker-up docker-migrate-up db-test-schema-install-pgtap ## Start containers, run migrations, install pgTAP
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[OK] Docker environment ready" -ForegroundColor Green
else
	@echo "[OK] Docker environment ready"
endif

# ── Prometheus ────────────────────────────────────────────────────────────────
# Prometheus stores time-series data for every metric your Go app exposes at
# GET /metrics. It scrapes every 15s and keeps 30 days of history by default.
# Config file: config/prometheus/prometheus.yml

prometheus-up: ## Start Prometheus container only and wait until healthy
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Starting Prometheus..." -ForegroundColor Cyan
	@docker-compose up -d --wait prometheus
	@Write-Host "[OK] Prometheus ready — http://localhost:$(PROMETHEUS_PORT)" -ForegroundColor Green
else
	@echo "[INFO] Starting Prometheus..."
	@docker-compose up -d --wait prometheus
	@echo "[OK] Prometheus ready — http://localhost:$(PROMETHEUS_PORT)"
endif

prometheus-down: ## Stop Prometheus container only (preserves stored data)
ifeq ($(DETECTED_OS),Windows)
	@docker-compose stop prometheus
	@Write-Host "[OK] Prometheus stopped (data preserved in volume)" -ForegroundColor Green
else
	@docker-compose stop prometheus
	@echo "[OK] Prometheus stopped (data preserved in volume)"
endif

prometheus-logs: ## Tail Prometheus container logs
	@docker-compose logs -f prometheus

prometheus-reload: ## Hot-reload Prometheus config without restart (edit prometheus.yml then run this)
	# Requires --web.enable-lifecycle in docker-compose.yml (already set)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Reloading Prometheus config..." -ForegroundColor Cyan
	@try { Invoke-WebRequest -Uri "http://localhost:$(PROMETHEUS_PORT)/-/reload" -Method POST -UseBasicParsing | Out-Null; Write-Host "[OK] Prometheus config reloaded" -ForegroundColor Green } catch { Write-Host "[ERROR] Could not reach Prometheus — is it running?" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Reloading Prometheus config..."
	@curl -sf -X POST http://localhost:$(PROMETHEUS_PORT)/-/reload && echo "[OK] Prometheus config reloaded" || (echo "[ERROR] Could not reach Prometheus — is it running?"; exit 1)
endif

prometheus-status: ## Show which targets Prometheus is scraping and their state
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Prometheus scrape targets:" -ForegroundColor Cyan
	@try { $$r = Invoke-WebRequest -Uri "http://localhost:$(PROMETHEUS_PORT)/api/v1/targets" -UseBasicParsing | ConvertFrom-Json; $$r.data.activeTargets | ForEach-Object { Write-Host ("  " + $$_.labels.job + " — " + $$_.health + " — last scrape: " + $$_.lastScrape) } } catch { Write-Host "[ERROR] Could not reach Prometheus — run: make prometheus-up" -ForegroundColor Red }
else
	@echo "[INFO] Prometheus scrape targets:"
	@curl -sf http://localhost:$(PROMETHEUS_PORT)/api/v1/targets 2>/dev/null \
		| grep -o '"job":"[^"]*"\|"health":"[^"]*"\|"lastScrape":"[^"]*"' \
		| paste - - - \
		| sed 's/"job":"//g; s/"health":"//g; s/"lastScrape":"//g; s/"//g' \
		| awk '{printf "  %-30s %-10s %s\n", $$1, $$2, $$3}' \
		|| echo "[ERROR] Could not reach Prometheus — run: make prometheus-up"
endif

prometheus-open: ## Open Prometheus UI in the default browser
ifeq ($(DETECTED_OS),Windows)
	@Start-Process "http://localhost:$(PROMETHEUS_PORT)"
else ifeq ($(DETECTED_OS),macOS)
	@open "http://localhost:$(PROMETHEUS_PORT)"
else
	@xdg-open "http://localhost:$(PROMETHEUS_PORT)" 2>/dev/null || echo "Open http://localhost:$(PROMETHEUS_PORT) in your browser"
endif

prometheus-purge: ## Stop Prometheus and delete all stored metric data (irreversible)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[WARNING] This will delete ALL Prometheus metric history" -ForegroundColor Yellow
	@$$response = Read-Host "Type 'yes' to confirm"; if ($$response -ne 'yes') { Write-Host "Aborted"; exit 0 }
	@docker-compose stop prometheus
	@docker volume rm $$(docker volume ls -q | Select-String "prometheus_data") 2>$$null; Write-Host "[OK] Prometheus data purged" -ForegroundColor Green
	@Write-Host "[INFO] Run 'make prometheus-up' to restart with a clean slate" -ForegroundColor Cyan
else
	@echo "[WARNING] This will delete ALL Prometheus metric history"
	@read -p "Type 'yes' to confirm: " response; \
	if [ "$$response" != "yes" ]; then echo "Aborted"; exit 0; fi
	@docker-compose stop prometheus
	@docker volume rm $$(docker volume ls -q | grep prometheus_data) 2>/dev/null; echo "[OK] Prometheus data purged"
	@echo "[INFO] Run 'make prometheus-up' to restart with a clean slate"
endif

# ── Docker DB ─────────────────────────────────────────────────────────────────

docker-db-reset: ## Drop, create, migrate and seed inside Docker
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[WARNING] This will reset the Docker database '$(DB_NAME)'" -ForegroundColor Yellow
	@docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d postgres -c "DROP DATABASE IF EXISTS $(DB_NAME);"; docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d postgres -c "CREATE DATABASE $(DB_NAME);"; Write-Host "[OK] Docker database reset complete" -ForegroundColor Green
else
	@echo "[WARNING] This will reset the Docker database '$(DB_NAME)'"
	@docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d postgres -c "DROP DATABASE IF EXISTS $(DB_NAME);" && \
	docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d postgres -c "CREATE DATABASE $(DB_NAME);" && \
	echo "[OK] Docker database reset complete"
endif

docker-db-connect: ## Connect to Docker database with psql
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Connecting to '$(DB_NAME)' in container..." -ForegroundColor Cyan
	@docker exec -it $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME)
else
	@echo "[INFO] Connecting to '$(DB_NAME)' in container..."
	@docker exec -it $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME)
endif

docker-db-dump: ## Backup the Docker database
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Creating backup of Docker '$(DB_NAME)'..." -ForegroundColor Cyan
	@if (-not (Test-Path sql)) { New-Item -ItemType Directory -Path sql -Force | Out-Null }
	@$$timestamp = Get-Date -Format "yyyyMMdd_HHmmss"; $$file = "sql/backup_docker_$$timestamp.sql"; docker exec $(PGTAP_CONTAINER) pg_dump -U $(DB_USER) -d $(DB_NAME) > $$file; Write-Host "[OK] Backed up to $$file" -ForegroundColor Green
else
	@echo "[INFO] Creating backup of Docker '$(DB_NAME)'..."
	@mkdir -p sql
	@timestamp=$$(date +%Y%m%d_%H%M%S); file="sql/backup_docker_$$timestamp.sql"; docker exec $(PGTAP_CONTAINER) pg_dump -U $(DB_USER) -d $(DB_NAME) > $$file; echo "[OK] Backed up to $$file"
endif

docker-db-restore: ## Restore backup into Docker database (BACKUP_FILE=path/to/file.sql)
ifndef BACKUP_FILE
	@echo "[ERROR] Please specify BACKUP_FILE=path/to/file.sql"
	@exit 1
endif
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path "$(BACKUP_FILE)")) { Write-Host "[ERROR] File '$(BACKUP_FILE)' not found" -ForegroundColor Red; exit 1 }
	@Write-Host "[INFO] Restoring into Docker '$(DB_NAME)'..." -ForegroundColor Cyan
	@Get-Content "$(BACKUP_FILE)" | docker exec -i $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME)
	@Write-Host "[OK] Docker database restored" -ForegroundColor Green
else
	@if [ ! -f "$(BACKUP_FILE)" ]; then echo "[ERROR] File '$(BACKUP_FILE)' not found"; exit 1; fi
	@echo "[INFO] Restoring into Docker '$(DB_NAME)'..."
	@docker exec -i $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) < "$(BACKUP_FILE)"
	@echo "[OK] Docker database restored"
endif

docker-db-tables: ## List all tables in the container database
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Tables in '$(DB_NAME)' (container)..." -ForegroundColor Cyan
	@docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -c "\dt"
else
	@echo "[INFO] Tables in '$(DB_NAME)' (container)..."
	@docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -c "\dt"
endif

docker-db-schema: ## Show complete schema in Docker database
	@docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -c "\d+"

docker-db-indexes: ## List all indexes in Docker database
	@docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -c "\di"

docker-db-functions: ## List all functions in Docker database
	@docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -c "\df"

docker-db-size: ## Show Docker database size
	@docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -c "SELECT pg_size_pretty(pg_database_size('$(DB_NAME)')) as size;"

docker-db-table-sizes: ## Show per-table sizes in Docker database
	@docker exec $(PGTAP_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -c "SELECT tablename, pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) AS size FROM pg_tables WHERE schemaname = 'public' ORDER BY pg_total_relation_size(schemaname||'.'||tablename) DESC;"

# ── Docker Migrations ─────────────────────────────────────────────────────────

docker-migrate-status: ## Show goose migration status via the container
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Migration status (via container)..." -ForegroundColor Cyan
	@$(SET_PGPASS) Set-Location $(SCHEMA_DIR); goose postgres "$(DB_URL_DOCKER)" status
else
	@echo "[INFO] Migration status (via container)..."
	@cd $(SCHEMA_DIR) && goose postgres "$(DB_URL_DOCKER)" status
endif

docker-migrate-up: ## Run migrations against the Docker container database
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Running migrations against container..." -ForegroundColor Cyan
	@$(SET_PGPASS) Set-Location $(SCHEMA_DIR); goose postgres "$(DB_URL_DOCKER)" up; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Migrations applied" -ForegroundColor Green } else { Write-Host "[ERROR] Migration failed" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Running migrations against container..."
	@cd $(SCHEMA_DIR) && goose postgres "$(DB_URL_DOCKER)" up && echo "[OK] Migrations applied" || (echo "[ERROR] Migration failed"; exit 1)
endif

docker-migrate-down: ## Rollback last migration in Docker database
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[WARNING] Rolling back last migration in container..." -ForegroundColor Yellow
	@$(SET_PGPASS) Set-Location $(SCHEMA_DIR); goose postgres "$(DB_URL_DOCKER)" down
else
	@echo "[WARNING] Rolling back last migration in container..."
	@cd $(SCHEMA_DIR) && goose postgres "$(DB_URL_DOCKER)" down
endif

docker-migrate-reset: ## Rollback ALL migrations in Docker database
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[WARNING] This will rollback ALL migrations in the container" -ForegroundColor Yellow
	@$$response = Read-Host "Type 'yes' to confirm"; if ($$response -ne 'yes') { Write-Host "Aborted"; exit 0 }
	@$(SET_PGPASS) Set-Location $(SCHEMA_DIR); goose postgres "$(DB_URL_DOCKER)" reset
else
	@echo "[WARNING] This will rollback ALL migrations in the container"; \
	read -p "Type 'yes' to confirm: " response; \
	if [ "$$response" != "yes" ]; then echo "Aborted"; exit 0; fi; \
	cd $(SCHEMA_DIR) && goose postgres "$(DB_URL_DOCKER)" reset
endif

docker-migrate-redo: ## Rollback and reapply last migration in Docker database
ifeq ($(DETECTED_OS),Windows)
	@$(SET_PGPASS) Set-Location $(SCHEMA_DIR); goose postgres "$(DB_URL_DOCKER)" redo
else
	@cd $(SCHEMA_DIR) && goose postgres "$(DB_URL_DOCKER)" redo
endif

docker-migrate-version: ## Show current migration version in Docker database
ifeq ($(DETECTED_OS),Windows)
	@$(SET_PGPASS) Set-Location $(SCHEMA_DIR); goose postgres "$(DB_URL_DOCKER)" version
else
	@cd $(SCHEMA_DIR) && goose postgres "$(DB_URL_DOCKER)" version
endif

# ── Docker Seeds ──────────────────────────────────────────────────────────────

docker-seed: ## Apply seed data to Docker database
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SEEDS_DIR))) { Write-Host "[ERROR] Directory '$(SEEDS_DIR)' not found" -ForegroundColor Red; exit 1 }
	@Write-Host "[INFO] Applying seed data to container..." -ForegroundColor Cyan
	@$(SET_PGPASS) Set-Location $(SEEDS_DIR); goose -table goose_seed_version postgres "$(DB_URL_DOCKER)" up; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Seed data applied" -ForegroundColor Green } else { Write-Host "[ERROR] Seeding failed" -ForegroundColor Red; exit 1 }
else
	@if [ ! -d "$(SEEDS_DIR)" ]; then echo "[ERROR] Directory '$(SEEDS_DIR)' not found"; exit 1; fi
	@echo "[INFO] Applying seed data to container..."
	@cd $(SEEDS_DIR) && goose -table goose_seed_version postgres "$(DB_URL_DOCKER)" up && echo "[OK] Seed data applied" || (echo "[ERROR] Seeding failed"; exit 1)
endif

docker-seed-reset: ## Reset seed data in Docker database
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Resetting seed data in container..." -ForegroundColor Cyan
	@$(SET_PGPASS) Set-Location $(SEEDS_DIR); goose -table goose_seed_version postgres "$(DB_URL_DOCKER)" down-to 0; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Seeds rolled back" -ForegroundColor Green } else { Write-Host "[ERROR] Seed rollback failed" -ForegroundColor Red; exit 1 }
	@$(SET_PGPASS) Set-Location $(SEEDS_DIR); goose -table goose_seed_version postgres "$(DB_URL_DOCKER)" up; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Seed data reset" -ForegroundColor Green } else { Write-Host "[ERROR] Seeding failed" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Resetting seed data in container..."
	@cd $(SEEDS_DIR) && goose -table goose_seed_version postgres "$(DB_URL_DOCKER)" down-to 0 && echo "[OK] Seeds rolled back" || (echo "[ERROR] Seed rollback failed"; exit 1)
	@cd $(SEEDS_DIR) && goose -table goose_seed_version postgres "$(DB_URL_DOCKER)" up && echo "[OK] Seed data reset" || (echo "[ERROR] Seeding failed"; exit 1)
endif

docker-seed-status: ## Show seed status in Docker database
ifeq ($(DETECTED_OS),Windows)
	@$(SET_PGPASS) Set-Location $(SEEDS_DIR); goose -table goose_seed_version postgres "$(DB_URL_DOCKER)" status
else
	@cd $(SEEDS_DIR) && goose -table goose_seed_version postgres "$(DB_URL_DOCKER)" status
endif

# ── Redis Helpers ─────────────────────────────────────────────────────────────

docker-redis-cli: ## Open an interactive redis-cli session inside the Redis container
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Connecting to Redis container..." -ForegroundColor Cyan
	@docker exec -it $(REDIS_CONTAINER) redis-cli -a "$(REDIS_PASSWORD)"
else
	@echo "[INFO] Connecting to Redis container..."
	@docker exec -it $(REDIS_CONTAINER) redis-cli -a "$(REDIS_PASSWORD)"
endif

docker-redis-flush: ## Flush all keys from the Redis container (dev/test only)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[WARNING] Flushing all Redis keys in $(REDIS_CONTAINER)" -ForegroundColor Yellow
	@docker exec $(REDIS_CONTAINER) redis-cli -a "$(REDIS_PASSWORD)" FLUSHALL
	@Write-Host "[OK] Redis flushed" -ForegroundColor Green
else
	@echo "[WARNING] Flushing all Redis keys in $(REDIS_CONTAINER)"
	@docker exec $(REDIS_CONTAINER) redis-cli -a "$(REDIS_PASSWORD)" FLUSHALL
	@echo "[OK] Redis flushed"
endif

docker-redis-logs: ## Tail Redis container logs
	@docker-compose logs -f redis
