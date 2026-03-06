# ─── Seed Data ───────────────────────────────────────────────────────────────

.PHONY: seed seed-reset seed-status

seed: ## Apply all seed data
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path $(SEEDS_DIR))) { Write-Host "[ERROR] Directory '$(SEEDS_DIR)' not found" -ForegroundColor Red; exit 1 }
	@Write-Host "[INFO] Applying seed data..." -ForegroundColor Cyan
	@Set-Location $(SEEDS_DIR); $(GOOSE) -table goose_seed_version postgres "$(DB_URL)" up; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Seed data applied" -ForegroundColor Green } else { Write-Host "[ERROR] Seeding failed" -ForegroundColor Red; exit 1 }
else
	@if [ ! -d "$(SEEDS_DIR)" ]; then echo "[ERROR] Directory '$(SEEDS_DIR)' not found"; exit 1; fi
	@echo "[INFO] Applying seed data..."
	@cd $(SEEDS_DIR) && $(GOOSE) -table goose_seed_version postgres "$(DB_URL)" up && echo "[OK] Seed data applied" || (echo "[ERROR] Seeding failed"; exit 1)
endif

seed-reset: ## Reset seed data (rollback and reapply)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[INFO] Resetting seed data..." -ForegroundColor Cyan
	@Set-Location $(SEEDS_DIR); $(GOOSE) -table goose_seed_version postgres "$(DB_URL)" down-to 0; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Seeds rolled back" -ForegroundColor Green } else { Write-Host "[ERROR] Seed rollback failed" -ForegroundColor Red; exit 1 }
	@Set-Location $(SEEDS_DIR); $(GOOSE) -table goose_seed_version postgres "$(DB_URL)" up; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] Seed data reset" -ForegroundColor Green } else { Write-Host "[ERROR] Seeding failed" -ForegroundColor Red; exit 1 }
else
	@echo "[INFO] Resetting seed data..."
	@cd $(SEEDS_DIR) && $(GOOSE) -table goose_seed_version postgres "$(DB_URL)" down-to 0 && echo "[OK] Seeds rolled back" || (echo "[ERROR] Seed rollback failed"; exit 1)
	@cd $(SEEDS_DIR) && $(GOOSE) -table goose_seed_version postgres "$(DB_URL)" up && echo "[OK] Seed data reset" || (echo "[ERROR] Seeding failed"; exit 1)
endif

seed-status: ## Show which seed files have been applied
ifeq ($(DETECTED_OS),Windows)
	@Set-Location $(SEEDS_DIR); $(GOOSE) -table goose_seed_version postgres "$(DB_URL)" status
else
	@cd $(SEEDS_DIR) && $(GOOSE) -table goose_seed_version postgres "$(DB_URL)" status
endif
