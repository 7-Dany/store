# ─── Build & Run ─────────────────────────────────────────────────────────────

.PHONY: build build-prod run vet tidy

# Output binary path (bin/api  or  bin/api.exe on Windows)
BIN_DIR  := bin
CMD_PATH := ./cmd/api

ifeq ($(DETECTED_OS),Windows)
	BINARY := $(BIN_DIR)/api.exe
else
	BINARY := $(BIN_DIR)/api
endif

# ── Build ─────────────────────────────────────────────────────────────────────

build: ## Compile the API binary to bin/api (development build)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Building $(BINARY)..." -ForegroundColor Cyan
	@if (-not (Test-Path $(BIN_DIR))) { New-Item -ItemType Directory -Path $(BIN_DIR) -Force | Out-Null }
	@go build -o $(BINARY) $(CMD_PATH); if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Built $(BINARY)" -ForegroundColor Green } else { Write-Host "[ERROR] Build failed" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Building $(BINARY)..."
	@mkdir -p $(BIN_DIR)
	@go build -o $(BINARY) $(CMD_PATH) && echo "[OK] Built $(BINARY)" || (echo "[ERROR] Build failed"; exit 1)
endif

build-prod: ## Compile a stripped, trimmed production binary to bin/api
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Building production binary $(BINARY)..." -ForegroundColor Cyan
	@if (-not (Test-Path $(BIN_DIR))) { New-Item -ItemType Directory -Path $(BIN_DIR) -Force | Out-Null }
	@go build -trimpath -ldflags="-s -w" -o $(BINARY) $(CMD_PATH); if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Production binary built: $(BINARY)" -ForegroundColor Green } else { Write-Host "[ERROR] Build failed" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Building production binary $(BINARY)..."
	@mkdir -p $(BIN_DIR)
	@go build -trimpath -ldflags="-s -w" -o $(BINARY) $(CMD_PATH) && echo "[OK] Production binary built: $(BINARY)" || (echo "[ERROR] Build failed"; exit 1)
endif

# ── Run ───────────────────────────────────────────────────────────────────────

run: ## Build and run the API server (reads .env automatically via Makefile)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Starting API server..." -ForegroundColor Cyan
	@go run $(CMD_PATH)
else
	@echo "[INFO] Starting API server..."
	@go run $(CMD_PATH)
endif

run-e2e: ## Build and run the API server against the TEST database and TEST Redis (for e2e tests)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Starting API server (test DB: $(TEST_DB_NAME), Redis DB 1)..." -ForegroundColor Cyan
	@$$env:DATABASE_URL='$(TEST_DATABASE_URL)'; $$env:REDIS_URL='$(TEST_REDIS_URL)'; go run $(CMD_PATH)
else
	@echo "[INFO] Starting API server (test DB: $(TEST_DB_NAME), Redis DB 1)..."
	DATABASE_URL='$(TEST_DATABASE_URL)' REDIS_URL='$(TEST_REDIS_URL)' go run $(CMD_PATH)
endif

# ── Quality ───────────────────────────────────────────────────────────────────

vet: ## Run go vet across all packages
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Running go vet..." -ForegroundColor Cyan
	@go vet ./...; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] go vet passed" -ForegroundColor Green } else { Write-Host "[ERROR] go vet found issues" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Running go vet..."
	@go vet ./... && echo "[OK] go vet passed" || (echo "[ERROR] go vet found issues"; exit 1)
endif

tidy: ## Run go mod tidy to sync dependencies
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Running go mod tidy..." -ForegroundColor Cyan
	@go mod tidy; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] go mod tidy complete" -ForegroundColor Green } else { Write-Host "[ERROR] go mod tidy failed" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Running go mod tidy..."
	@go mod tidy && echo "[OK] go mod tidy complete" || (echo "[ERROR] go mod tidy failed"; exit 1)
endif
