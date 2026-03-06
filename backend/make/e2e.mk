# ─── E2E Tests (Newman) ──────────────────────────────────────────────────────
#
# One Newman collection per auth feature (each a flat JSON file under e2e/auth/):
#   e2e/health/health.json              → GET /health
#   e2e/auth/register.json              → POST /register
#   e2e/auth/verify-email.json          → POST /verify-email + POST /resend-verification
#   e2e/auth/login.json                 → POST /login
#   e2e/auth/session.json               → POST /refresh + POST /logout
#   e2e/auth/unlock.json                → POST /request-unlock + POST /confirm-unlock
#   e2e/auth/password-reset.json        → POST /forgot-password + POST /reset-password
#   e2e/auth/change-password.json       → POST /change-password (requires JWT)
#   e2e/auth/me.json                    → GET /me (requires JWT)
#   e2e/auth/sessions.json              → GET /sessions (requires JWT)
#   e2e/auth/revoke-session.json        → DELETE /sessions/{id} (requires JWT)
#
# e2e-password runs both password collections (forgot/reset + change) in order.
# e2e-profile runs the three profile collections (me + sessions + revoke-session) in order.
# e2e-auth runs all auth collections including e2e-password and e2e-profile.
#
# Each collection has a "rate-limiting" folder that is run in a SEPARATE newman
# invocation, after a Redis flush, so the IP bucket always starts empty.
# All other folders are run together in the first invocation.
#
# Test users use @e2e.test email addresses. The _e2e-db-clean step deletes them
# with a single DELETE query between collection runs.
# _e2e-kv-clean flushes the server's Redis DB 1 (test instance). Only call this
# on a dedicated dev/CI Redis — it wipes ALL keys in that DB.
#
# Prerequisites
#   make docker-up          — postgres + redis containers must be running
#   make e2e-install        — install Newman globally (npm install -g newman)
#   cp e2e/environment.template.json e2e/environment.json
#   Fill in base_url in e2e/environment.json (default: http://localhost:8080)
#   Server must be running: make run
# ─────────────────────────────────────────────────────────────────────────────

E2E_DIR      := e2e
E2E_ENV      := $(E2E_DIR)/environment.json
E2E_TEMPLATE := $(E2E_DIR)/environment.template.json
E2E_AUTH     := $(E2E_DIR)/auth
E2E_DELAY    ?= 150

.PHONY: e2e-install e2e e2e-health e2e-register e2e-unlock e2e-password e2e-profile e2e-auth _e2e-db-clean _e2e-kv-clean _e2e-clean _e2e-check-env

# ── Tooling ───────────────────────────────────────────────────────────────────

e2e-install: ## Install Newman globally (required before running e2e targets)
ifeq ($(DETECTED_OS),Windows)
	@if (Get-Command newman -ErrorAction SilentlyContinue) { Write-Host "[OK] newman already installed: $((Get-Command newman).Source)" -ForegroundColor Green; exit 0 }
	@Write-Host "[INFO] Installing newman..." -ForegroundColor Cyan
	@npm install -g newman; if ($$LASTEXITCODE -eq 0) { Write-Host "[OK] newman installed" -ForegroundColor Green } else { Write-Host "[ERROR] npm install failed" -ForegroundColor Red; exit 1 }
else
	@if command -v newman >/dev/null 2>&1; then echo "[OK] newman already installed: $$(command -v newman)"; exit 0; fi
	@echo "[INFO] Installing newman..."
	@npm install -g newman && echo "[OK] newman installed" || { echo "[ERROR] npm install failed"; exit 1; }
endif

# ── Internal helpers ──────────────────────────────────────────────────────────

# Delete all e2e test users from the test DB.
# Covers @e2e.test addresses and any punycode (xn--) domains used by tests.
# auth_audit_log.user_id is ON DELETE CASCADE so audit rows are cleaned automatically.
# Uses TEST_DATABASE_URL so e2e tests never touch the dev database.
_e2e-db-clean:
	@psql "$(TEST_DATABASE_URL)" -c "DELETE FROM users WHERE email LIKE '%@e2e.test' OR email ~ '@xn--' OR email = '$(E2E_GMAIL_EMAIL)';"
	@echo "[e2e] DB cleaned (e2e users removed from test DB)"

# Flush Redis DB 1 (the test server's rate-limiter and blocklist store).
# WARNING: this wipes ALL keys in DB 1 — only use on a dedicated dev/CI Redis.
_e2e-kv-clean:
ifeq ($(DETECTED_OS),Windows)
	@docker exec $(COMPOSE_PROJECT_NAME)_redis redis-cli -n 1 --no-auth-warning -a $(REDIS_PASSWORD) FLUSHDB 2>$(NULL); Write-Host "[e2e] Redis flushed (DB 1)" -ForegroundColor DarkGray
else
	@docker exec $(COMPOSE_PROJECT_NAME)_redis redis-cli -n 1 --no-auth-warning -a $(REDIS_PASSWORD) FLUSHDB 2>$(NULL); echo "[e2e] Redis flushed (DB 1)"
endif

# Full clean: DB test users + Redis.
_e2e-clean: _e2e-db-clean _e2e-kv-clean

# Guard: abort with a clear message if environment.json is missing.
_e2e-check-env:
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path "$(E2E_ENV)")) { Write-Host "[ERROR] Missing $(E2E_ENV)" -ForegroundColor Red; Write-Host "  Run: cp $(E2E_TEMPLATE) $(E2E_ENV)" -ForegroundColor Yellow; exit 1 }
else
	@if [ ! -f "$(E2E_ENV)" ]; then echo "[ERROR] Missing $(E2E_ENV)"; echo "  Run: cp $(E2E_TEMPLATE) $(E2E_ENV)"; exit 1; fi
endif

# ── Public targets ────────────────────────────────────────────────────────────

e2e: e2e-health e2e-auth ## Run ALL e2e suites (health + all auth collections)

e2e-health: _e2e-check-env ## Smoke-test GET /health (response shape, security headers, rate-limiting)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[e2e] --- GET /health ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-kv-clean
	@Write-Host "[e2e] Running: smoke" -ForegroundColor DarkGray
	@newman run "$(E2E_DIR)/health/health.json" --environment "$(E2E_ENV)" --folder "smoke" --delay-request $(E2E_DELAY) --reporters cli
	@Write-Host "[e2e] Flushing Redis before rate-limit folder..." -ForegroundColor DarkGray
	@$(MAKE) _e2e-kv-clean
	@Write-Host "[e2e] Running: rate-limiting" -ForegroundColor DarkGray
	@newman run "$(E2E_DIR)/health/health.json" --environment "$(E2E_ENV)" --folder "rate-limiting" --delay-request 1 --reporters cli
else
	@echo "[e2e] --- GET /health ---"
	@$(MAKE) _e2e-kv-clean
	@echo "[e2e] Running: smoke"
	@newman run "$(E2E_DIR)/health/health.json" --environment "$(E2E_ENV)" \
		--folder "smoke" \
		--delay-request $(E2E_DELAY) --reporters cli
	@echo "[e2e] Flushing Redis before rate-limit folder..."
	@$(MAKE) _e2e-kv-clean
	@echo "[e2e] Running: rate-limiting"
	@newman run "$(E2E_DIR)/health/health.json" --environment "$(E2E_ENV)" \
		--folder "rate-limiting" \
		--delay-request 1 --reporters cli
endif

e2e-register: _e2e-check-env ## Run POST /register E2E (all folders including rate-limit-reset)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[e2e] --- POST /register ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-clean
	@Write-Host "[e2e] Running: infrastructure + validation + happy-path + conflict" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/register.json" --environment "$(E2E_ENV)" --folder "infrastructure" --folder "validation" --folder "happy-path" --folder "conflict" --delay-request $(E2E_DELAY) --reporters cli
	@Write-Host "[e2e] Flushing Redis before rate-limit folder..." -ForegroundColor DarkGray
	@$(MAKE) _e2e-kv-clean
	@Write-Host "[e2e] Running: rate-limiting" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/register.json" --environment "$(E2E_ENV)" --folder "rate-limiting" --delay-request 1 --reporters cli
	@Write-Host "[e2e] Flushing Redis to simulate window expiry..." -ForegroundColor DarkGray
	@$(MAKE) _e2e-kv-clean
	@Write-Host "[e2e] Running: rate-limit-reset" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/register.json" --environment "$(E2E_ENV)" --folder "rate-limit-reset" --delay-request $(E2E_DELAY) --reporters cli
else
	@echo "[e2e] --- POST /register ---"
	@$(MAKE) _e2e-clean
	@echo "[e2e] Running: infrastructure + validation + happy-path + conflict"
	@newman run "$(E2E_AUTH)/register.json" --environment "$(E2E_ENV)" \
		--folder "infrastructure" --folder "validation" \
		--folder "happy-path" --folder "conflict" \
		--delay-request $(E2E_DELAY) --reporters cli
	@echo "[e2e] Flushing Redis before rate-limit folder..."
	@$(MAKE) _e2e-kv-clean
	@echo "[e2e] Running: rate-limiting"
	@newman run "$(E2E_AUTH)/register.json" --environment "$(E2E_ENV)" \
		--folder "rate-limiting" \
		--delay-request 1 --reporters cli
	@echo "[e2e] Flushing Redis to simulate window expiry..."
	@$(MAKE) _e2e-kv-clean
	@echo "[e2e] Running: rate-limit-reset"
	@newman run "$(E2E_AUTH)/register.json" --environment "$(E2E_ENV)" \
		--folder "rate-limit-reset" \
		--delay-request $(E2E_DELAY) --reporters cli
endif

e2e-password: _e2e-check-env ## Run POST /forgot-password + POST /reset-password + POST /change-password E2E
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[e2e] --- POST /forgot-password + POST /reset-password ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-clean
	@Write-Host "[e2e] Running: setup + happy-path + anti-enumeration + validation" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/password-reset.json" --environment "$(E2E_ENV)" --folder "setup" --folder "happy-path" --folder "anti-enumeration" --folder "validation" --delay-request $(E2E_DELAY) --reporters cli
	@Write-Host "[e2e] Flushing Redis before rate-limiting-fpw folder..." -ForegroundColor DarkGray
	@$(MAKE) _e2e-kv-clean
	@Write-Host "[e2e] Running: rate-limiting-fpw" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/password-reset.json" --environment "$(E2E_ENV)" --folder "rate-limiting-fpw" --delay-request 1 --reporters cli
	@Write-Host "[e2e] Flushing Redis before rate-limiting-rpw folder..." -ForegroundColor DarkGray
	@$(MAKE) _e2e-kv-clean
	@Write-Host "[e2e] Running: rate-limiting-rpw" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/password-reset.json" --environment "$(E2E_ENV)" --folder "rate-limiting-rpw" --delay-request 1 --reporters cli
	@Write-Host "[e2e] --- POST /change-password ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-clean
	@Write-Host "[e2e] Running: setup + happy-path + failures + auth-failures + validation" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/change-password.json" --environment "$(E2E_ENV)" --folder "setup" --folder "happy-path" --folder "failures" --folder "auth-failures" --folder "validation" --delay-request $(E2E_DELAY) --reporters cli
	@Write-Host "[e2e] Flushing Redis before rate-limiting-cpw folder..." -ForegroundColor DarkGray
	@$(MAKE) _e2e-kv-clean
	@Write-Host "[e2e] Running: rate-limiting-cpw" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/change-password.json" --environment "$(E2E_ENV)" --folder "rate-limiting-cpw" --delay-request 1 --reporters cli
	@Write-Host "[e2e] password suite passed" -ForegroundColor Green
else
	@echo "[e2e] --- POST /forgot-password + POST /reset-password ---"
	@$(MAKE) _e2e-clean
	@echo "[e2e] Running: setup + happy-path + anti-enumeration + validation"
	@newman run "$(E2E_AUTH)/password-reset.json" --environment "$(E2E_ENV)" \
		--folder "setup" --folder "happy-path" \
		--folder "anti-enumeration" --folder "validation" \
		--delay-request $(E2E_DELAY) --reporters cli
	@echo "[e2e] Flushing Redis before rate-limiting-fpw folder..."
	@$(MAKE) _e2e-kv-clean
	@echo "[e2e] Running: rate-limiting-fpw"
	@newman run "$(E2E_AUTH)/password-reset.json" --environment "$(E2E_ENV)" \
		--folder "rate-limiting-fpw" \
		--delay-request 1 --reporters cli
	@echo "[e2e] Flushing Redis before rate-limiting-rpw folder..."
	@$(MAKE) _e2e-kv-clean
	@echo "[e2e] Running: rate-limiting-rpw"
	@newman run "$(E2E_AUTH)/password-reset.json" --environment "$(E2E_ENV)" \
		--folder "rate-limiting-rpw" \
		--delay-request 1 --reporters cli
	@echo "[e2e] --- POST /change-password ---"
	@$(MAKE) _e2e-clean
	@echo "[e2e] Running: setup + happy-path + failures + auth-failures + validation"
	@newman run "$(E2E_AUTH)/change-password.json" --environment "$(E2E_ENV)" \
		--folder "setup" --folder "happy-path" \
		--folder "failures" --folder "auth-failures" --folder "validation" \
		--delay-request $(E2E_DELAY) --reporters cli
	@echo "[e2e] Flushing Redis before rate-limiting-cpw folder..."
	@$(MAKE) _e2e-kv-clean
	@echo "[e2e] Running: rate-limiting-cpw"
	@newman run "$(E2E_AUTH)/change-password.json" --environment "$(E2E_ENV)" \
		--folder "rate-limiting-cpw" \
		--delay-request 1 --reporters cli
	@echo "[e2e] password suite passed"
endif

e2e-unlock: _e2e-check-env ## Run POST /request-unlock + POST /confirm-unlock E2E (all folders)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[e2e] --- POST /request-unlock + POST /confirm-unlock ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-clean
	@Write-Host "[e2e] Running: happy-path + validation + anti-enumeration" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/unlock.json" --environment "$(E2E_ENV)" --folder "happy-path" --folder "validation" --folder "anti-enumeration" --delay-request $(E2E_DELAY) --reporters cli
	@Write-Host "[e2e] Flushing Redis before rate-limit folder..." -ForegroundColor DarkGray
	@$(MAKE) _e2e-kv-clean
	@Write-Host "[e2e] Running: rate-limiting" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/unlock.json" --environment "$(E2E_ENV)" --folder "rate-limiting" --delay-request $(E2E_DELAY) --reporters cli
	@Write-Host "[e2e] unlock suite passed" -ForegroundColor Green
else
	@echo "[e2e] --- POST /request-unlock + POST /confirm-unlock ---"
	@$(MAKE) _e2e-clean
	@echo "[e2e] Running: happy-path + validation + anti-enumeration"
	@newman run "$(E2E_AUTH)/unlock.json" --environment "$(E2E_ENV)" \
		--folder "happy-path" --folder "validation" --folder "anti-enumeration" \
		--delay-request $(E2E_DELAY) --reporters cli
	@echo "[e2e] Flushing Redis before rate-limit folder..."
	@$(MAKE) _e2e-kv-clean
	@echo "[e2e] Running: rate-limiting"
	@newman run "$(E2E_AUTH)/unlock.json" --environment "$(E2E_ENV)" \
		--folder "rate-limiting" \
		--delay-request $(E2E_DELAY) --reporters cli
	@echo "[e2e] unlock suite passed"
endif

# NOTE: profile rate-limiters (pme:ip:, psess:ip:, rsess:ip:) sit INSIDE the
# r.Use(deps.JWTAuth) middleware group, so JWT auth fires before the limiter.
# Running rate-limiting as a separate newman invocation loses the access token
# stored in collection variables (→ 401 missing_token instead of 429).
#
# Fix: run all folders (setup + happy-path + rate-limiting) in ONE invocation.
# The rate-limiting folder's prerequest already pins _xff to 127.0.0.1, so its
# requests use a separate IP bucket from happy-path (which uses unique 10.x.x.x
# addresses). Redis is flushed once before the run so the 127.0.0.1 bucket is
# empty when the rate-limiting folder starts — no mid-run flush is needed.
#
# --delay-request 1: profile rate limiters have small bursts (pme:ip:=20, psess:ip:=10,
# rsess:ip:=3). At 1 ms between requests the warmup completes in ~20/10/3 ms —
# negligible refill time — so the bucket is reliably exhausted before the final
# 429 request fires. The 8-second Gmail wait in the register test script is a
# synchronous busy-loop that Newman executes before advancing to the next request,
# so delay=1 is safe. (delay=0 is rejected by Newman as not a positive integer.)
e2e-profile: _e2e-check-env ## Run GET /me + GET /sessions + DELETE /sessions/{id} E2E (requires JWT)
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[e2e] --- GET /me ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-clean
	@Write-Host "[e2e] Running: setup + happy-path + rate-limiting (single invocation)" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/me.json" --environment "$(E2E_ENV)" --folder "setup" --folder "happy-path" --folder "rate-limiting" --delay-request 1 --reporters cli
	@Write-Host "[e2e] --- GET /sessions ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-clean
	@Write-Host "[e2e] Running: setup + happy-path + rate-limiting (single invocation)" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/sessions.json" --environment "$(E2E_ENV)" --folder "setup" --folder "happy-path" --folder "rate-limiting" --delay-request 1 --reporters cli
	@Write-Host "[e2e] --- DELETE /sessions/{id} ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-clean
	@Write-Host "[e2e] Running: setup + happy-path + rate-limiting (single invocation)" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/revoke-session.json" --environment "$(E2E_ENV)" --folder "setup" --folder "happy-path" --folder "rate-limiting" --delay-request 1 --reporters cli
	@Write-Host "[e2e] profile suite passed" -ForegroundColor Green
else
	@echo "[e2e] --- GET /me ---"
	@$(MAKE) _e2e-clean
	@echo "[e2e] Running: setup + happy-path + rate-limiting (single invocation)"
	@newman run "$(E2E_AUTH)/me.json" --environment "$(E2E_ENV)" \
		--folder "setup" --folder "happy-path" --folder "rate-limiting" \
		--delay-request 1 --reporters cli
	@echo "[e2e] --- GET /sessions ---"
	@$(MAKE) _e2e-clean
	@echo "[e2e] Running: setup + happy-path + rate-limiting (single invocation)"
	@newman run "$(E2E_AUTH)/sessions.json" --environment "$(E2E_ENV)" \
		--folder "setup" --folder "happy-path" --folder "rate-limiting" \
		--delay-request 1 --reporters cli
	@echo "[e2e] --- DELETE /sessions/{id} ---"
	@$(MAKE) _e2e-clean
	@echo "[e2e] Running: setup + happy-path + rate-limiting (single invocation)"
	@newman run "$(E2E_AUTH)/revoke-session.json" --environment "$(E2E_ENV)" \
		--folder "setup" --folder "happy-path" --folder "rate-limiting" \
		--delay-request 1 --reporters cli
	@echo "[e2e] profile suite passed"
endif

e2e-auth: _e2e-check-env ## Run all auth E2E collections in order with DB + Redis cleanup between each
ifeq ($(DETECTED_OS),Windows)
	@Write-Host "[e2e] --- POST /register ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-clean
	@Write-Host "[e2e] Running: infrastructure + validation + happy-path + conflict" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/register.json" --environment "$(E2E_ENV)" --folder "infrastructure" --folder "validation" --folder "happy-path" --folder "conflict" --delay-request $(E2E_DELAY) --reporters cli
	@Write-Host "[e2e] Flushing Redis before rate-limit folder..." -ForegroundColor DarkGray
	@$(MAKE) _e2e-kv-clean
	@Write-Host "[e2e] Running: rate-limiting" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/register.json" --environment "$(E2E_ENV)" --folder "rate-limiting" --delay-request 1 --reporters cli
	@Write-Host "[e2e] Flushing Redis to simulate window expiry..." -ForegroundColor DarkGray
	@$(MAKE) _e2e-kv-clean
	@Write-Host "[e2e] Running: rate-limit-reset" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/register.json" --environment "$(E2E_ENV)" --folder "rate-limit-reset" --delay-request $(E2E_DELAY) --reporters cli
	@Write-Host "[e2e] --- POST /verify-email + POST /resend-verification ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-clean
	@Write-Host "[e2e] Running: setup + validation + happy-path + anti-enumeration + resend-validation + resend-happy-path" -ForegroundColor DarkGray
	@newman run "$(E2E_AUTH)/verify-email.json" --environment "$(E2E_ENV)" --folder "setup" --folder "validation" --folder "happy-path" --folder "anti-enumeration" --folder "resend-validation" --folder "resend-happy-path" --delay-request $(E2E_DELAY) --reporters cli
	@$(MAKE) _e2e-kv-clean
	@newman run "$(E2E_AUTH)/verify-email.json" --environment "$(E2E_ENV)" --folder "rate-limiting" --delay-request $(E2E_DELAY) --reporters cli
	@Write-Host "[e2e] --- POST /login ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-clean
	@newman run "$(E2E_AUTH)/login.json" --environment "$(E2E_ENV)" --folder "setup" --folder "validation" --folder "happy-path" --folder "failures" --folder "time-lock" --delay-request $(E2E_DELAY) --reporters cli
	@$(MAKE) _e2e-kv-clean
	@newman run "$(E2E_AUTH)/login.json" --environment "$(E2E_ENV)" --folder "rate-limiting" --delay-request $(E2E_DELAY) --reporters cli
	@Write-Host "[e2e] --- POST /refresh + POST /logout ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-clean
	@newman run "$(E2E_AUTH)/session.json" --environment "$(E2E_ENV)" --folder "happy-path" --folder "failures" --folder "token-reuse" --delay-request $(E2E_DELAY) --reporters cli
	@$(MAKE) _e2e-kv-clean
	@newman run "$(E2E_AUTH)/session.json" --environment "$(E2E_ENV)" --folder "rate-limiting" --delay-request $(E2E_DELAY) --reporters cli
	@Write-Host "[e2e] --- POST /request-unlock + POST /confirm-unlock ---" -ForegroundColor Cyan
	@$(MAKE) _e2e-clean
	@newman run "$(E2E_AUTH)/unlock.json" --environment "$(E2E_ENV)" --folder "validation" --folder "happy-path" --folder "anti-enumeration" --delay-request $(E2E_DELAY) --reporters cli
	@$(MAKE) _e2e-kv-clean
	@newman run "$(E2E_AUTH)/unlock.json" --environment "$(E2E_ENV)" --folder "rate-limiting" --delay-request $(E2E_DELAY) --reporters cli
	@$(MAKE) e2e-password
	@$(MAKE) e2e-profile
	@Write-Host "[e2e] All auth suites passed" -ForegroundColor Green
else
	@echo "[e2e] --- POST /register ---"
	@$(MAKE) _e2e-clean
	@echo "[e2e] Running: infrastructure + validation + happy-path + conflict"
	@newman run "$(E2E_AUTH)/register.json" --environment "$(E2E_ENV)" \
		--folder "infrastructure" --folder "validation" \
		--folder "happy-path" --folder "conflict" \
		--delay-request $(E2E_DELAY) --reporters cli
	@echo "[e2e] Flushing Redis before rate-limit folder..."
	@$(MAKE) _e2e-kv-clean
	@echo "[e2e] Running: rate-limiting"
	@newman run "$(E2E_AUTH)/register.json" --environment "$(E2E_ENV)" \
		--folder "rate-limiting" \
		--delay-request 1 --reporters cli
	@echo "[e2e] Flushing Redis to simulate window expiry..."
	@$(MAKE) _e2e-kv-clean
	@echo "[e2e] Running: rate-limit-reset"
	@newman run "$(E2E_AUTH)/register.json" --environment "$(E2E_ENV)" \
		--folder "rate-limit-reset" \
		--delay-request $(E2E_DELAY) --reporters cli
	@echo "[e2e] --- POST /verify-email + POST /resend-verification ---"
	@$(MAKE) _e2e-clean
	@newman run "$(E2E_AUTH)/verify-email.json" --environment "$(E2E_ENV)" \
		--folder "setup" --folder "validation" \
		--folder "happy-path" --folder "anti-enumeration" \
		--folder "resend-validation" --folder "resend-happy-path" \
		--delay-request $(E2E_DELAY) --reporters cli
	@$(MAKE) _e2e-kv-clean
	@newman run "$(E2E_AUTH)/verify-email.json" --environment "$(E2E_ENV)" \
		--folder "rate-limiting" \
		--delay-request $(E2E_DELAY) --reporters cli
	@echo "[e2e] --- POST /login ---"
	@$(MAKE) _e2e-clean
	@newman run "$(E2E_AUTH)/login.json" --environment "$(E2E_ENV)" \
		--folder "setup" --folder "validation" \
		--folder "happy-path" --folder "failures" --folder "time-lock" \
		--delay-request $(E2E_DELAY) --reporters cli
	@$(MAKE) _e2e-kv-clean
	@newman run "$(E2E_AUTH)/login.json" --environment "$(E2E_ENV)" \
		--folder "rate-limiting" \
		--delay-request $(E2E_DELAY) --reporters cli
	@echo "[e2e] --- POST /refresh + POST /logout ---"
	@$(MAKE) _e2e-clean
	@newman run "$(E2E_AUTH)/session.json" --environment "$(E2E_ENV)" \
		--folder "happy-path" --folder "failures" --folder "token-reuse" \
		--delay-request $(E2E_DELAY) --reporters cli
	@$(MAKE) _e2e-kv-clean
	@newman run "$(E2E_AUTH)/session.json" --environment "$(E2E_ENV)" \
		--folder "rate-limiting" \
		--delay-request $(E2E_DELAY) --reporters cli
	@echo "[e2e] --- POST /request-unlock + POST /confirm-unlock ---"
	@$(MAKE) _e2e-clean
	@newman run "$(E2E_AUTH)/unlock.json" --environment "$(E2E_ENV)" \
		--folder "validation" --folder "happy-path" --folder "anti-enumeration" \
		--delay-request $(E2E_DELAY) --reporters cli
	@$(MAKE) _e2e-kv-clean
	@newman run "$(E2E_AUTH)/unlock.json" --environment "$(E2E_ENV)" \
		--folder "rate-limiting" \
		--delay-request $(E2E_DELAY) --reporters cli
	@$(MAKE) e2e-password
	@$(MAKE) e2e-profile
	@echo "[e2e] All auth suites passed"
endif
