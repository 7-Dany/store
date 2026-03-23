# Makefile for PostgreSQL + Goose + SQLC + Go workflow
# Works on: Linux, macOS, Windows (PowerShell)
#
# WINDOWS NOTE: Every Windows recipe line must be a self-contained @ command.
# PowerShell does not support \ line continuation — chain statements with ; instead.

# ─── OS Detection (must be first) ────────────────────────────────────────────
include make/os.mk

# ─── Environment ─────────────────────────────────────────────────────────────

ifneq (,$(wildcard .env))
include .env
export
endif

# ─── Database Configuration ──────────────────────────────────────────────────

DB_NAME        ?= store_dev
DB_USER        ?= postgres
DB_PASSWORD    ?=
DB_HOST        ?= localhost
DB_PORT        ?= 5432
DB_SSL_MODE    ?= disable
# Docker exposes postgres on a different port to avoid conflict with local postgres
DB_PORT_DOCKER ?= 5433

# Test database — defaults to <DB_NAME>_test on the same server.
# TEST_DATABASE_URL is read from .env and used verbatim by goose and Go tests;
# TEST_DB_NAME is used only for CREATE/DROP DATABASE psql commands.
TEST_DB_NAME      ?= $(DB_NAME)_test
TEST_DATABASE_URL ?= postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(TEST_DB_NAME)?sslmode=$(DB_SSL_MODE)

# Docker connection strings (point at the container port)
ifeq ($(DB_PASSWORD),)
DB_URL_DOCKER  := postgres://$(DB_USER)@$(DB_HOST):$(DB_PORT_DOCKER)/$(DB_NAME)?sslmode=$(DB_SSL_MODE)
else
DB_URL_DOCKER  := postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT_DOCKER)/$(DB_NAME)?sslmode=$(DB_SSL_MODE)
endif

# Build connection strings
# Passwords are passed via PGPASSWORD env var at runtime — never embedded in the URL
# printed to terminal or visible in `ps` process listings.
ifeq ($(DB_PASSWORD),)
	DB_URL       := postgres://$(DB_USER)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=$(DB_SSL_MODE)
	POSTGRES_URL := postgres://$(DB_USER)@$(DB_HOST):$(DB_PORT)/postgres?sslmode=$(DB_SSL_MODE)
else
	DB_URL       := postgres://$(DB_USER)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=$(DB_SSL_MODE)
	POSTGRES_URL := postgres://$(DB_USER)@$(DB_HOST):$(DB_PORT)/postgres?sslmode=$(DB_SSL_MODE)
endif

# Redacted URLs safe to print in logs and terminal output
ifeq ($(DB_PASSWORD),)
	DB_URL_DISPLAY      := $(DB_URL)
	TEST_DB_URL_DISPLAY := postgres://$(DB_USER)@$(DB_HOST):$(DB_PORT)/$(TEST_DB_NAME)?sslmode=$(DB_SSL_MODE)
else
	DB_URL_DISPLAY      := postgres://$(DB_USER):***@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=$(DB_SSL_MODE)
	TEST_DB_URL_DISPLAY := postgres://$(DB_USER):***@$(DB_HOST):$(DB_PORT)/$(TEST_DB_NAME)?sslmode=$(DB_SSL_MODE)
endif

# ─── Directories ─────────────────────────────────────────────────────────────

SCHEMA_DIR  := sql/schema
SEEDS_DIR   := sql/seeds
QUERIES_DIR := sql/queries

# Schema test configuration — override on the command line:
#   TEST_PATTERN='*functions_test.sql' make db-test-schema
#   TEST_FILE=sql/tests/003_test.sql make db-test-schema-file
TESTS_DIR    ?= sql/tests
TEST_PATTERN ?= *_test.sql
TEST_FILE    ?=

# Docker container name for pgTAP tests (matches docker-compose.yml)
COMPOSE_PROJECT_NAME ?= app
PGTAP_CONTAINER      ?= $(COMPOSE_PROJECT_NAME)_postgres

# ─── Domain Includes ─────────────────────────────────────────────────────────

include make/build.mk
include make/db.mk
include make/migrate.mk
include make/seeds.mk
include make/test-db.mk
include make/test.mk
include make/docker.mk
include make/sqlc.mk
include make/schema.mk
include make/e2e.mk
include make/lint.mk
include make/tools.mk
include make/ci.mk

# ─── Default Goal ────────────────────────────────────────────────────────────

.DEFAULT_GOAL := help

# ─── Help ────────────────────────────────────────────────────────────────────

.PHONY: help
help: ## Show this help message
ifeq ($(DETECTED_OS),Windows)
	@& { Write-Host "=============================================================================="; Write-Host " PostgreSQL + Goose + SQLC + Go Makefile" -ForegroundColor Cyan; Write-Host "=============================================================================="; Write-Host "" }
	@& { Write-Host "QUICK START:" -ForegroundColor Cyan; Write-Host "  make setup              - Complete initial setup"; Write-Host "  make fresh              - Reset database and regenerate code"; Write-Host "  make rebuild            - Rollback all, migrate up, seed, generate"; Write-Host "" }
	@& { Write-Host "BUILD & RUN:" -ForegroundColor Cyan; Write-Host "  make build              - Compile API binary to bin/api (dev)"; Write-Host "  make build-prod         - Compile stripped production binary to bin/api"; Write-Host "  make run                - Build and run the API server"; Write-Host "  make run-e2e            - Run server against test DB + Redis DB 1 (for e2e)"; Write-Host "  make vet                - Run go vet across all packages"; Write-Host "  make tidy               - Run go mod tidy"; Write-Host "" }
	@& { Write-Host "DATABASE:" -ForegroundColor Cyan; Write-Host "  make db-create          - Create database"; Write-Host "  make db-drop            - Drop database"; Write-Host "  make db-reset           - Drop, create, migrate, seed"; Write-Host "  make db-connect         - Connect with psql"; Write-Host "  make db-dump            - Backup database"; Write-Host "  make db-restore         - Restore from backup (BACKUP_FILE=path)"; Write-Host "" }
	@& { Write-Host "MIGRATIONS:" -ForegroundColor Cyan; Write-Host "  make migrate-status     - Show migration status"; Write-Host "  make migrate-up         - Apply all pending migrations"; Write-Host "  make migrate-up-one     - Apply next single migration"; Write-Host "  make migrate-up-to      - Migrate up to version (VERSION=N)"; Write-Host "  make migrate-down       - Rollback last migration"; Write-Host "  make migrate-down-to    - Rollback to version (VERSION=N)"; Write-Host "  make migrate-reset      - Rollback all migrations (prompts for confirmation)"; Write-Host "  make migrate-refresh    - Rollback ALL then re-apply ALL (no confirmation)"; Write-Host "  make migrate-redo       - Rollback then re-apply last migration"; Write-Host "  make migrate-create     - Create migration (NAME=name)"; Write-Host "  make migrate-validate   - Validate migration files"; Write-Host "  make migrate-version    - Show current migration version"; Write-Host "" }
	@& { Write-Host "SEEDS:" -ForegroundColor Cyan; Write-Host "  make seed               - Apply seed data"; Write-Host "  make seed-reset         - Reset seed data"; Write-Host "  make seed-status        - Show which seed files have been applied"; Write-Host "" }
	@& { Write-Host "GO TESTS:" -ForegroundColor Cyan; Write-Host "  make test               - Run unit tests (fast, no DB)"; Write-Host "  make test-integration   - Run integration tests (requires TEST_DATABASE_URL)"; Write-Host "  make test-all           - Run unit + integration tests"; Write-Host "  make test-coverage      - Unit tests with HTML coverage report"; Write-Host "  make test-uncovered     - Unit tests - print only uncovered functions/blocks"; Write-Host "" }
	@& { Write-Host "CODE GENERATION (SQLC):" -ForegroundColor Cyan; Write-Host "  make sqlc-generate      - Generate Go code from SQL"; Write-Host "  make sqlc-vet           - Vet SQL queries with sqlc"; Write-Host "  make sqlc-compile       - Compile SQL without generating code"; Write-Host "" }
	@& { Write-Host "SCHEMA TESTING (pgTAP):" -ForegroundColor Cyan; Write-Host "  make db-test-schema              - Run all pgTAP schema tests"; Write-Host "  make db-test-schema-verbose      - Run tests with full TAP output + timing"; Write-Host "  make db-test-schema-file         - Run one file (TEST_FILE=path)"; Write-Host "  make db-test-schema-fresh        - migrate-up then run all tests"; Write-Host "  make db-test-schema-install-pgtap - Install pgTAP extension"; Write-Host "  make db-test-schema-check-deps   - Check pgTAP is ready"; Write-Host "" }
	@& { Write-Host "DOCKER:" -ForegroundColor Cyan; Write-Host "  make docker-up                - Start postgres + redis"; Write-Host "  make docker-up-full           - Start postgres + redis + prometheus (full dev stack)"; Write-Host "  make docker-down              - Stop all containers"; Write-Host "  make docker-logs              - Tail all container logs"; Write-Host "  make docker-setup             - Full setup inside Docker"; Write-Host "  make docker-db-tables         - List tables in Docker DB"; Write-Host "  make docker-db-schema         - Show schema in Docker DB"; Write-Host "  make docker-db-indexes        - List indexes in Docker DB"; Write-Host "  make docker-db-functions      - List functions in Docker DB"; Write-Host "  make docker-db-size           - Show Docker DB size"; Write-Host "  make docker-db-table-sizes    - Show per-table sizes in Docker DB"; Write-Host "  make docker-db-connect        - Connect to Docker DB with psql"; Write-Host "  make docker-db-dump           - Backup Docker database"; Write-Host "  make docker-db-restore        - Restore into Docker DB (BACKUP_FILE=path)"; Write-Host "  make docker-db-reset          - Drop, create, migrate, seed in Docker"; Write-Host "" }
	@& { Write-Host "  make docker-migrate-status    - Show migrations in Docker DB"; Write-Host "  make docker-migrate-up        - Apply all migrations in Docker DB"; Write-Host "  make docker-migrate-down      - Rollback last migration in Docker DB"; Write-Host "  make docker-migrate-reset     - Rollback all migrations in Docker DB"; Write-Host "  make docker-migrate-redo      - Redo last migration in Docker DB"; Write-Host "  make docker-migrate-version   - Show migration version in Docker DB"; Write-Host "  make docker-seed              - Apply seed data to Docker DB"; Write-Host "  make docker-seed-reset        - Reset seed data in Docker DB"; Write-Host "  make docker-seed-status       - Show seed status in Docker DB"; Write-Host "" }
	@& { Write-Host "PROMETHEUS:" -ForegroundColor Cyan; Write-Host "  make prometheus-up            - Start Prometheus container only"; Write-Host "  make prometheus-down          - Stop Prometheus (data preserved)"; Write-Host "  make prometheus-logs          - Tail Prometheus logs"; Write-Host "  make prometheus-reload        - Hot-reload config without restart"; Write-Host "  make prometheus-status        - Show scrape targets and their health"; Write-Host "  make prometheus-open          - Open Prometheus UI in browser"; Write-Host "  make prometheus-purge         - Stop + delete all stored metric data"; Write-Host "" }
	@& { Write-Host "TEST DATABASE:" -ForegroundColor Cyan; Write-Host "  make test-db-setup          - Create test DB + migrate (first-run)"; Write-Host "  make test-db-reset          - Drop, create, and migrate test DB"; Write-Host "  make test-db-create         - Create test database"; Write-Host "  make test-db-drop           - Drop test database"; Write-Host "  make test-db-connect        - Connect to test database with psql"; Write-Host "" }
	@& { Write-Host "TEST MIGRATIONS:" -ForegroundColor Cyan; Write-Host "  make test-migrate-status    - Show migration status of test DB"; Write-Host "  make test-migrate-up        - Apply all pending migrations to test DB"; Write-Host "  make test-migrate-down      - Rollback last migration on test DB"; Write-Host "  make test-migrate-reset     - Rollback ALL migrations on test DB"; Write-Host "  make test-migrate-refresh   - Rollback ALL then re-apply ALL on test DB"; Write-Host "  make test-migrate-version   - Show migration version of test DB"; Write-Host "" }
	@& { Write-Host "TEST SEEDS:" -ForegroundColor Cyan; Write-Host "  make test-seed              - Apply seed data to test DB"; Write-Host "  make test-seed-reset        - Reset seed data on test DB"; Write-Host "  make test-seed-status       - Show seed status of test DB"; Write-Host "" }
	@& { Write-Host "LINTING:" -ForegroundColor Cyan; Write-Host "  make lint              - Run all linters (local - TODOs are warnings)"; Write-Host "  make lint-ci           - Run linters in CI mode (TODOs/FIXMEs fail)"; Write-Host "  make lint-fix          - Auto-fix lint issues where possible"; Write-Host "  make install-lint      - Install golangci-lint"; Write-Host "" }
	@& { Write-Host "TOOLS:" -ForegroundColor Cyan; Write-Host "  make install-tools      - Install goose, sqlc, and golangci-lint"; Write-Host "  make install-goose      - Install goose only"; Write-Host "  make install-sqlc       - Install sqlc only"; Write-Host "" }
	@& { Write-Host "INSPECTION:" -ForegroundColor Cyan; Write-Host "  make db-tables          - List all tables"; Write-Host "  make db-schema          - Show complete schema"; Write-Host "  make db-indexes         - List all indexes"; Write-Host "  make db-functions       - List all functions/procedures"; Write-Host "  make db-size            - Show database size"; Write-Host "  make db-table-sizes     - Show per-table sizes"; Write-Host "" }
	@& { Write-Host "VALIDATION & CI:" -ForegroundColor Cyan; Write-Host "  make check              - Run all validations"; Write-Host "  make ci-test            - CI/CD pipeline: setup and validate"; Write-Host "  make ci-clean           - CI/CD cleanup: drop test database"; Write-Host "  make test-makefile      - Self-test: check dirs and tools exist"; Write-Host "" }
	@& { Write-Host "E2E TESTS (Newman):" -ForegroundColor Cyan; Write-Host "  make e2e-install        - Install Newman globally"; Write-Host "  make e2e                - Run ALL e2e suites (requires Gmail token)"; Write-Host "  make e2e-ci             - Run ALL e2e suites in CI mode (no Gmail token)"; Write-Host "  make e2e-auth           - Run all auth E2E collections in order (requires Gmail token)"; Write-Host "  make e2e-password       - Run POST /forgot-password + /reset-password + /change-password E2E"; Write-Host "  make e2e-profile        - Run GET /me + GET /sessions + DELETE /sessions/{id} E2E"; Write-Host "  make e2e-register       - Run POST /register E2E (all folders)"; Write-Host "  make e2e-unlock         - Run POST /request-unlock + /confirm-unlock E2E (all folders)"; Write-Host "  make e2e-health         - Smoke-test GET /health"; Write-Host "  make e2e-rbac           - Run all RBAC E2E collections in order (bootstrap)"; Write-Host "  make e2e-rbac-bootstrap - Run POST /owner/bootstrap E2E (all folders)"; Write-Host "  make e2e-btc            - Run POST /api/v1/bitcoin/watch E2E (all folders)"; Write-Host "" }
	@& { Write-Host "UTILITIES:" -ForegroundColor Cyan; Write-Host "  make env                - Show configuration"; Write-Host "  make env-create         - Create .env from .env.example"; Write-Host "  make db-debug           - Debug connection"; Write-Host "  make clean              - Clean generated files"; Write-Host ""; Write-Host "For detailed help: Select-String '##' Makefile"; Write-Host "==============================================================================" }
else
	@printf "%s\n" \
		"==============================================================================" \
		" PostgreSQL + Goose + SQLC + Go Makefile" \
		"==============================================================================" \
		"" \
		"QUICK START:" \
		"  make setup              - Complete initial setup" \
		"  make fresh              - Reset database and regenerate code" \
		"  make rebuild            - Rollback all, migrate up, seed, generate" \
		"" \
		"BUILD & RUN:" \
		"  make build              - Compile API binary to bin/api (dev)" \
		"  make build-prod         - Compile stripped production binary to bin/api" \
		"  make run                - Build and run the API server" \
		"  make run-e2e            - Run server against test DB + Redis DB 1 (for e2e)" \
		"  make vet                - Run go vet across all packages" \
		"  make tidy               - Run go mod tidy" \
		"" \
		"DATABASE:" \
		"  make db-create          - Create database" \
		"  make db-drop            - Drop database" \
		"  make db-reset           - Drop, create, migrate, seed" \
		"  make db-connect         - Connect with psql" \
		"  make db-dump            - Backup database" \
		"  make db-restore         - Restore from backup (BACKUP_FILE=path)" \
		"" \
		"MIGRATIONS:" \
		"  make migrate-status     - Show migration status" \
		"  make migrate-up         - Apply all pending migrations" \
		"  make migrate-up-one     - Apply next single migration" \
		"  make migrate-up-to      - Migrate up to version (VERSION=N)" \
		"  make migrate-down       - Rollback last migration" \
		"  make migrate-down-to    - Rollback to version (VERSION=N)" \
		"  make migrate-reset      - Rollback all migrations (prompts for confirmation)" \
		"  make migrate-refresh    - Rollback ALL then re-apply ALL (no confirmation)" \
		"  make migrate-redo       - Rollback then re-apply last migration" \
		"  make migrate-create     - Create migration (NAME=name)" \
		"  make migrate-validate   - Validate migration files" \
		"  make migrate-version    - Show current migration version" \
		"" \
		"SEEDS:" \
		"  make seed               - Apply seed data" \
		"  make seed-reset         - Reset seed data" \
		"  make seed-status        - Show which seed files have been applied" \
		"" \
		"GO TESTS:" \
		"  make test               - Run unit tests (fast, no DB)" \
		"  make test-integration   - Run integration tests (requires TEST_DATABASE_URL)" \
		"  make test-all           - Run unit + integration tests" \
		"  make test-coverage      - Unit tests with HTML coverage report" \
		"  make test-uncovered     - Unit tests — print only uncovered functions/blocks" \
		"" \
		"CODE GENERATION (SQLC):" \
		"  make sqlc-generate      - Generate Go code from SQL" \
		"  make sqlc-vet           - Vet SQL queries with sqlc" \
		"  make sqlc-compile       - Compile SQL without generating code" \
		"" \
		"SCHEMA TESTING (pgTAP):" \
		"  make db-test-schema              - Run all pgTAP schema tests" \
		"  make db-test-schema-verbose      - Run tests with full TAP output + timing" \
		"  make db-test-schema-file         - Run one file (TEST_FILE=path)" \
		"  make db-test-schema-fresh        - migrate-up then run all tests" \
		"  make db-test-schema-install-pgtap - Install pgTAP extension" \
		"  make db-test-schema-check-deps   - Check pgTAP is ready" \
		"" \
		"DOCKER:" \
		"  make docker-up                - Start postgres + redis" \
		"  make docker-up-full           - Start postgres + redis + prometheus (full dev stack)" \
		"  make docker-down              - Stop all containers" \
		"  make docker-logs              - Tail all container logs" \
		"  make docker-setup             - Full setup inside Docker" \
		"  make docker-db-tables         - List tables in Docker DB" \
		"  make docker-db-schema         - Show schema in Docker DB" \
		"  make docker-db-indexes        - List indexes in Docker DB" \
		"  make docker-db-functions      - List functions in Docker DB" \
		"  make docker-db-size           - Show Docker DB size" \
		"  make docker-db-table-sizes    - Show per-table sizes in Docker DB" \
		"  make docker-db-connect        - Connect to Docker DB with psql" \
		"  make docker-db-dump           - Backup Docker database" \
		"  make docker-db-restore        - Restore into Docker DB (BACKUP_FILE=path)" \
		"  make docker-db-reset          - Drop, create, migrate, seed in Docker" \
		"  make docker-migrate-status    - Show migrations in Docker DB" \
		"  make docker-migrate-up        - Apply all migrations in Docker DB" \
		"  make docker-migrate-down      - Rollback last migration in Docker DB" \
		"  make docker-migrate-reset     - Rollback all migrations in Docker DB" \
		"  make docker-migrate-redo      - Redo last migration in Docker DB" \
		"  make docker-migrate-version   - Show migration version in Docker DB" \
		"  make docker-seed              - Apply seed data to Docker DB" \
		"  make docker-seed-reset        - Reset seed data in Docker DB" \
		"  make docker-seed-status       - Show seed status in Docker DB" \
		"" \
		"PROMETHEUS:" \
		"  make prometheus-up            - Start Prometheus container only" \
		"  make prometheus-down          - Stop Prometheus (data preserved)" \
		"  make prometheus-logs          - Tail Prometheus logs" \
		"  make prometheus-reload        - Hot-reload config without restart" \
		"  make prometheus-status        - Show scrape targets and their health" \
		"  make prometheus-open          - Open Prometheus UI in browser" \
		"  make prometheus-purge         - Stop + delete all stored metric data" \
		"" \
		"TEST DATABASE:" \
		"  make test-db-setup          - Create test DB + migrate (first-run)" \
		"  make test-db-reset          - Drop, create, and migrate test DB" \
		"  make test-db-create         - Create test database" \
		"  make test-db-drop           - Drop test database" \
		"  make test-db-connect        - Connect to test database with psql" \
		"" \
		"TEST MIGRATIONS:" \
		"  make test-migrate-status    - Show migration status of test DB" \
		"  make test-migrate-up        - Apply all pending migrations to test DB" \
		"  make test-migrate-down      - Rollback last migration on test DB" \
		"  make test-migrate-reset     - Rollback ALL migrations on test DB" \
		"  make test-migrate-refresh   - Rollback ALL then re-apply ALL on test DB" \
		"  make test-migrate-version   - Show migration version of test DB" \
		"" \
		"TEST SEEDS:" \
		"  make test-seed              - Apply seed data to test DB" \
		"  make test-seed-reset        - Reset seed data on test DB" \
		"  make test-seed-status       - Show seed status of test DB" \
		"" \
		"LINTING:" \
		"  make lint              - Run all linters (local — TODOs are warnings)" \
		"  make lint-ci           - Run linters in CI mode (TODOs/FIXMEs fail)" \
		"  make lint-fix          - Auto-fix lint issues where possible" \
		"  make install-lint      - Install golangci-lint" \
		"" \
		"TOOLS:" \
		"  make install-tools      - Install goose, sqlc, and golangci-lint" \
		"  make install-goose      - Install goose only" \
		"  make install-sqlc       - Install sqlc only" \
		"" \
		"INSPECTION:" \
		"  make db-tables          - List all tables" \
		"  make db-schema          - Show complete schema" \
		"  make db-indexes         - List all indexes" \
		"  make db-functions       - List all functions/procedures" \
		"  make db-size            - Show database size" \
		"  make db-table-sizes     - Show per-table sizes" \
		"" \
		"VALIDATION & CI:" \
		"  make check              - Run all validations" \
		"  make ci-test            - CI/CD pipeline: setup and validate" \
		"  make ci-clean           - CI/CD cleanup: drop test database" \
		"  make test-makefile      - Self-test: check dirs and tools exist" \
		"" \
		"E2E TESTS (Newman):" \
		"  make e2e-install        - Install Newman globally" \
		"  make e2e                - Run ALL e2e suites (requires Gmail token)" \
		"  make e2e-auth           - Run all auth E2E collections in order (requires Gmail token)" \
		"  make e2e-password       - Run POST /forgot-password + /reset-password + /change-password E2E" \
		"  make e2e-profile        - Run GET /me + GET /sessions + DELETE /sessions/{id} E2E" \
		"  make e2e-register       - Run POST /register E2E (all folders)" \
		"  make e2e-unlock         - Run POST /request-unlock + /confirm-unlock E2E (all folders)" \
		"  make e2e-health         - Smoke-test GET /health" \
		"  make e2e-rbac           - Run all RBAC E2E collections in order (bootstrap)" \
		"  make e2e-rbac-bootstrap - Run POST /owner/bootstrap E2E (all folders)" \
		"  make e2e-btc            - Run POST /api/v1/bitcoin/watch E2E (all folders)" \
		"" \
		"UTILITIES:" \
		"  make env                - Show configuration" \
		"  make env-create         - Create .env from .env.example" \
		"  make db-debug           - Debug connection" \
		"  make clean              - Clean generated files" \
		"" \
		"For detailed help: grep '##' Makefile" \
		"=============================================================================="
endif

# ─── Quick Start ─────────────────────────────────────────────────────────────

.PHONY: setup fresh rebuild

setup: install-tools db-create migrate-up seed sqlc-generate ## Complete initial setup
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[OK] Setup complete! Your database is ready." -ForegroundColor Green
else
	@echo "[OK] Setup complete! Your database is ready."
endif

fresh: db-reset sqlc-generate ## Reset database and regenerate code
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[OK] Fresh database ready" -ForegroundColor Green
else
	@echo "[OK] Fresh database ready"
endif

rebuild: migrate-reset migrate-up seed sqlc-generate ## Rollback all, migrate up, seed, regenerate
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[OK] Rebuild complete" -ForegroundColor Green
else
	@echo "[OK] Rebuild complete"
endif

# ─── Utilities ────────────────────────────────────────────────────────────────

.PHONY: env env-create db-debug clean

env: ## Show current configuration
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "=== Configuration ===" -ForegroundColor Cyan
	@Write-Host "  DETECTED_OS   : $(DETECTED_OS)"
	@Write-Host "  DB_NAME       : $(DB_NAME)"
	@Write-Host "  DB_USER       : $(DB_USER)"
	@Write-Host "  DB_HOST       : $(DB_HOST)"
	@Write-Host "  DB_PORT       : $(DB_PORT)"
	@Write-Host "  DB_SSL_MODE   : $(DB_SSL_MODE)"
	@Write-Host "  DB_URL        : $(DB_URL_DISPLAY)"
	@Write-Host "  SCHEMA_DIR    : $(SCHEMA_DIR)"
	@Write-Host "  SEEDS_DIR     : $(SEEDS_DIR)"
	@Write-Host "  QUERIES_DIR   : $(QUERIES_DIR)"
	@Write-Host "  TESTS_DIR     : $(TESTS_DIR)"
	@Write-Host "  PGTAP_CONTAINER: $(PGTAP_CONTAINER)"
	@Write-Host "  PROMETHEUS_PORT: $(PROMETHEUS_PORT)"
	@Write-Host ""
	@Write-Host "=== Test Database ===" -ForegroundColor Cyan
	@Write-Host "  TEST_DB_NAME    : $(TEST_DB_NAME)"
	@Write-Host "  TEST_DB_URL     : $(TEST_DB_URL_DISPLAY)"
else
	@printf "%-22s %s\n" "DETECTED_OS:"      "$(DETECTED_OS)"
	@printf "%-22s %s\n" "DB_NAME:"          "$(DB_NAME)"
	@printf "%-22s %s\n" "DB_USER:"          "$(DB_USER)"
	@printf "%-22s %s\n" "DB_HOST:"          "$(DB_HOST)"
	@printf "%-22s %s\n" "DB_PORT:"          "$(DB_PORT)"
	@printf "%-22s %s\n" "DB_SSL_MODE:"      "$(DB_SSL_MODE)"
	@printf "%-22s %s\n" "DB_URL:"           "$(DB_URL_DISPLAY)"
	@printf "%-22s %s\n" "SCHEMA_DIR:"       "$(SCHEMA_DIR)"
	@printf "%-22s %s\n" "SEEDS_DIR:"        "$(SEEDS_DIR)"
	@printf "%-22s %s\n" "QUERIES_DIR:"      "$(QUERIES_DIR)"
	@printf "%-22s %s\n" "TESTS_DIR:"        "$(TESTS_DIR)"
	@printf "%-22s %s\n" "PGTAP_CONTAINER:"  "$(PGTAP_CONTAINER)"
	@printf "%-22s %s\n" "PROMETHEUS_PORT:"  "$(PROMETHEUS_PORT)"
	@printf "\n"
	@printf "%-22s %s\n" "TEST_DB_NAME:"     "$(TEST_DB_NAME)"
	@printf "%-22s %s\n" "TEST_DB_URL:"      "$(TEST_DB_URL_DISPLAY)"
endif

env-create: ## Create .env from .env.example
ifeq ($(DETECTED_OS),Windows)
	@if (Test-Path .env) { Write-Host "[WARNING] .env already exists — skipping" -ForegroundColor Yellow; exit 0 }
	@if (-not (Test-Path .env.example)) { Write-Host "[ERROR] .env.example not found" -ForegroundColor Red; exit 1 }
	@Copy-Item .env.example .env; Write-Host "[OK] Created .env from .env.example — edit it with your settings" -ForegroundColor Green
else
	@if [ -f .env ]; then echo "[WARNING] .env already exists — skipping"; exit 0; fi
	@if [ ! -f .env.example ]; then echo "[ERROR] .env.example not found"; exit 1; fi
	@cp .env.example .env && echo "[OK] Created .env from .env.example — edit it with your settings"
endif

db-debug: ## Debug database connection
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "=== Connection Debug ===" -ForegroundColor Cyan
	@Write-Host "  DB_URL: $(DB_URL_DISPLAY)" -ForegroundColor Cyan
	@Write-Host "[INFO] Testing connection..." -ForegroundColor Cyan
	@& { psql "$(DB_URL)" -c "SELECT current_database(), current_user, version();"; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Connection successful" -ForegroundColor Green } else { Write-Host "[ERROR] Connection failed - check DB_HOST, DB_PORT, DB_USER, DB_PASSWORD" -ForegroundColor Red; exit 1 } }
else
	@echo "=== Connection Debug ==="
	@echo "  DB_URL: $(DB_URL_DISPLAY)"
	@echo "[INFO] Testing connection..."
	@psql "$(DB_URL)" -c "SELECT current_database(), current_user, version();" && echo "[OK] Connection successful" || { echo "[ERROR] Connection failed - check DB_HOST, DB_PORT, DB_USER, DB_PASSWORD"; exit 1; }
endif

clean: ## Remove generated files (sqlc output)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Cleaning generated files..." -ForegroundColor Cyan
	@if (Test-Path internal/db) { Remove-Item -Recurse -Force internal/db; Write-Host "[OK] Removed internal/db" -ForegroundColor Green }
	@if (Test-Path db/sqlc) { Remove-Item -Recurse -Force db/sqlc; Write-Host "[OK] Removed db/sqlc" -ForegroundColor Green }
	@Write-Host "[OK] Clean complete" -ForegroundColor Green
else
	@echo "[INFO] Cleaning generated files..."
	@rm -rf internal/db db/sqlc
	@echo "[OK] Clean complete"
endif
