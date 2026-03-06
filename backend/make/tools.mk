# ─── Tool Installation ────────────────────────────────────────────────────────

.PHONY: install-tools install-goose install-sqlc

install-tools: install-goose install-sqlc install-lint ## Install goose, sqlc, and golangci-lint

install-goose: ## Install goose migration tool (skips if already on PATH)
ifeq ($(DETECTED_OS),Windows)
	@& { if (Get-Command goose -ErrorAction SilentlyContinue) { Write-Host "[OK] goose already installed: $((Get-Command goose).Source)" -ForegroundColor Green; exit 0 }; Write-Host "[INFO] Installing goose..." -ForegroundColor Cyan; if (Get-Command go -ErrorAction SilentlyContinue) { go install github.com/pressly/goose/v3/cmd/goose@latest; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] goose installed via go install" -ForegroundColor Green } else { Write-Host "[ERROR] go install failed" -ForegroundColor Red; exit 1 } } elseif (Get-Command scoop -ErrorAction SilentlyContinue) { scoop install goose; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] goose installed via scoop" -ForegroundColor Green } else { Write-Host "[ERROR] scoop install failed" -ForegroundColor Red; exit 1 } } else { Write-Host "[ERROR] Cannot install goose: neither go nor scoop found. Install Go from https://go.dev or scoop from https://scoop.sh" -ForegroundColor Red; exit 1 } }
else
	@if command -v goose >/dev/null 2>&1; then echo "[OK] goose already installed: $$(command -v goose)"; exit 0; fi; \
	echo "[INFO] Installing goose..."; \
	if command -v go >/dev/null 2>&1; then \
		go install github.com/pressly/goose/v3/cmd/goose@latest && echo "[OK] goose installed via go install" || (echo "[ERROR] go install failed"; exit 1); \
	else \
		echo "[ERROR] Cannot install goose: go not found. Install Go from https://go.dev"; exit 1; \
	fi
endif

install-sqlc: ## Install sqlc code generation tool (skips if already on PATH)
ifeq ($(DETECTED_OS),Windows)
	@& { if (Get-Command sqlc -ErrorAction SilentlyContinue) { Write-Host "[OK] sqlc already installed: $((Get-Command sqlc).Source)" -ForegroundColor Green; exit 0 }; Write-Host "[INFO] Installing sqlc..." -ForegroundColor Cyan; if (Get-Command go -ErrorAction SilentlyContinue) { go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] sqlc installed via go install" -ForegroundColor Green } else { Write-Host "[ERROR] go install failed" -ForegroundColor Red; exit 1 } } elseif (Get-Command scoop -ErrorAction SilentlyContinue) { scoop install sqlc; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] sqlc installed via scoop" -ForegroundColor Green } else { Write-Host "[ERROR] scoop install failed" -ForegroundColor Red; exit 1 } } else { Write-Host "[ERROR] Cannot install sqlc: neither go nor scoop found. Install Go from https://go.dev or scoop from https://scoop.sh" -ForegroundColor Red; exit 1 } }
else
	@if command -v sqlc >/dev/null 2>&1; then echo "[OK] sqlc already installed: $$(command -v sqlc)"; exit 0; fi; \
	echo "[INFO] Installing sqlc..."; \
	if command -v go >/dev/null 2>&1; then \
		go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest && echo "[OK] sqlc installed via go install" || (echo "[ERROR] go install failed"; exit 1); \
	else \
		echo "[ERROR] Cannot install sqlc: go not found. Install Go from https://go.dev"; exit 1; \
	fi
endif
