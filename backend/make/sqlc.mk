# ─── Code Generation (SQLC) ──────────────────────────────────────────────────

.PHONY: sqlc-generate sqlc-vet sqlc-compile

TEST_SUPPORT_GO := internal/db/auth_test.sql.go

sqlc-generate: ## Generate Go code from SQL queries, then tag test_support.sql.go
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path sqlc.yaml)) { Write-Host "[ERROR] sqlc.yaml not found" -ForegroundColor Red; exit 1 }
	@$(SQLC) generate; if ($$LASTEXITCODE -ne 0) { Write-Host "[ERROR] Generation failed" -ForegroundColor Red; exit 1 }
	@$$content = Get-Content $(TEST_SUPPORT_GO) -Raw; if ($$content -notmatch '^//go:build integration_test') { Set-Content $(TEST_SUPPORT_GO) ("//go:build integration_test`n`n" + $$content) -NoNewline; Write-Host "[OK] Patched build tag onto $(TEST_SUPPORT_GO)" -ForegroundColor Green } else { Write-Host "[OK] Build tag already present" -ForegroundColor Yellow }
	@Write-Host "[OK] Code generated" -ForegroundColor Green
else
	@if [ ! -f "sqlc.yaml" ]; then echo "[ERROR] sqlc.yaml not found"; exit 1; fi
	@$(SQLC) generate && echo "[OK] Code generated" || (echo "[ERROR] Generation failed"; exit 1)
	@if ! grep -q '//go:build integration_test' $(TEST_SUPPORT_GO); then \
		{ printf '//go:build integration_test\n\n'; cat $(TEST_SUPPORT_GO); } > $(TEST_SUPPORT_GO).tmp && mv $(TEST_SUPPORT_GO).tmp $(TEST_SUPPORT_GO); \
		echo "[OK] Patched build tag onto $(TEST_SUPPORT_GO)"; \
	else \
		echo "[OK] Build tag already present"; \
	fi
endif

sqlc-vet: ## Run sqlc linter on queries
	@$(SQLC) vet

sqlc-compile: ## Compile SQL queries without generating
	@$(SQLC) compile
