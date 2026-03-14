# ─── E2E Tests (Newman) ──────────────────────────────────────────────────────
#
# One Newman collection per feature (each a flat JSON file):
#   e2e/health/health.json              → GET /health
#   e2e/auth/register.json              → POST /register
#   e2e/auth/verify-email.json          → POST /verify-email + POST /resend-verification
#   e2e/auth/login.json                 → POST /login
#   e2e/auth/session.json               → POST /refresh + POST /logout
#   e2e/auth/unlock.json                → POST /request-unlock + POST /confirm-unlock
#   e2e/auth/password-reset.json        → POST /forgot-password + POST /reset-password
#   e2e/auth/change-password.json       → POST /change-password (requires JWT)
#   e2e/profile/me.json                 → GET /me (requires JWT)
#   e2e/profile/sessions.json           → GET /sessions (requires JWT)
#   e2e/profile/revoke-session.json     → DELETE /sessions/{id} (requires JWT)
#   e2e/profile/update-profile.json     → PATCH /me (requires JWT)
#   e2e/profile/set-password.json       → POST /set-password (requires JWT)
#   e2e/profile/username.json           → GET /username/available + PATCH /me/username (requires JWT for PATCH)
#   e2e/oauth/google.json               → GET /oauth/google + GET /oauth/google/callback + DELETE /oauth/google/unlink
#   e2e/oauth/telegram.json             → POST /oauth/telegram/callback + POST /oauth/telegram/link + DELETE /oauth/telegram/unlink
#   e2e/profile/email.json              → POST /email/request-change + POST /email/verify-current + POST /email/confirm-change (requires JWT)
#   e2e/profile/delete-account.json     → DELETE /profile/me + POST /profile/me/cancel-deletion (requires JWT) + GET /profile/me/deletion-method (requires JWT)
#   e2e/profile/identities.json         → GET /me/identities (requires JWT)
#   e2e/rbac/bootstrap.json             → POST /owner/bootstrap
#   e2e/rbac/permissions.json           → GET /admin/permissions + GET /admin/permissions/groups (requires rbac:read)
#   e2e/rbac/roles.json                 → CRUD /admin/rbac/roles + /admin/rbac/roles/{id}/permissions (requires rbac:read + rbac:manage)
#   e2e/rbac/userroles.json              → GET/PUT/DELETE /admin/rbac/users/{user_id}/role (requires rbac:read + rbac:manage)
#   e2e/rbac/userpermissions.json       → GET/POST/DELETE /admin/rbac/users/{user_id}/permissions (requires rbac:read + rbac:grant_user_perm)
#
# Individual targets   — run a single collection:
#   e2e-health, e2e-register, e2e-verify-email, e2e-login, e2e-session,
#   e2e-unlock, e2e-password, e2e-set-password, e2e-me, e2e-sessions,
#   e2e-revoke-session, e2e-update-profile, e2e-username, e2e-email,
#   e2e-delete-account, e2e-identities, e2e-oauth-google, e2e-oauth-telegram,
#   e2e-rbac-bootstrap, e2e-rbac-permissions, e2e-rbac-roles, e2e-rbac-userroles,
#   e2e-rbac-userpermissions
#
# Group targets        — run a folder of collections in order:
#   e2e-auth           — register + verify-email + login + session + unlock + password
#   e2e-oauth          — oauth-google + oauth-telegram
#   e2e-profile        — me + sessions + revoke-session + update-profile +
#                        set-password + username + email + delete-account + identities
#   e2e-rbac           — rbac-bootstrap + rbac-permissions + rbac-roles + rbac-userroles +
#                        rbac-userpermissions
#
# Suite target         — run everything at once:
#   e2e                — e2e-health + e2e-auth + e2e-oauth + e2e-profile + e2e-rbac
#
# Rate-limiting notes:
#   Collections whose rate-limiters sit INSIDE the JWTAuth middleware group
#   (profile/* and oauth unlink) must run ALL folders in ONE newman invocation
#   so the JWT stored in collection variables is not lost between runs.
#   Collections whose rate-limiters are unauthenticated get a Redis flush +
#   a separate newman invocation, ensuring the IP bucket starts empty.
#
# Test users use @e2e.test email addresses. _e2e-db-clean deletes them between
# collection runs. _e2e-kv-clean flushes Redis DB 1 (test instance). Only call
# this on a dedicated dev/CI Redis — it wipes ALL keys in that DB.
#
# Prerequisites
#   make docker-up          — postgres + redis containers must be running
#   make e2e-install        — install Newman globally (npm install -g newman)
#   cp e2e/environment.template.json e2e/environment.json
#   Fill in e2e/environment.json
#   make run                — server must be running
# ─────────────────────────────────────────────────────────────────────────────

# ── Config ────────────────────────────────────────────────────────────────────
E2E_DIR      := e2e
E2E_ENV      := $(E2E_DIR)/environment.json
E2E_TEMPLATE := $(E2E_DIR)/environment.template.json
E2E_AUTH     := $(E2E_DIR)/auth
E2E_PROFILE  := $(E2E_DIR)/profile
E2E_OAUTH    := $(E2E_DIR)/oauth
E2E_RBAC     := $(E2E_DIR)/rbac
E2E_DELAY    ?= 150

# ── OS-aware printing ─────────────────────────────────────────────────────────
# Usage: @$(call _e2e_info,message)
ifeq ($(DETECTED_OS),Windows)
_e2e_info = Write-Host "$(1)" -ForegroundColor Cyan
_e2e_gray = Write-Host "$(1)" -ForegroundColor DarkGray
_e2e_ok   = Write-Host "$(1)" -ForegroundColor Green
else
_e2e_info = echo "$(1)"
_e2e_gray = echo "$(1)"
_e2e_ok   = echo "$(1)"
endif

# ── Newman runner macro ────────────────────────────────────────────────────────
# Usage: $(call newman-run, collection-path, folder-flags, delay-ms)
define newman-run
	@newman run "$(1)" --environment "$(E2E_ENV)" $(2) --delay-request $(3) --reporters cli
endef

# ── Folder flag definitions ───────────────────────────────────────────────────
# Named sets of --folder flags, grouped by collection.
# Shared across individual and group targets so the two never drift apart.

# health
_F_HEALTH_SMOKE    := --folder "smoke"
_F_HEALTH_RL       := --folder "rate-limiting"

# auth/register
_F_REG_MAIN        := --folder "infrastructure" --folder "validation" --folder "happy-path" --folder "conflict"
_F_REG_RL          := --folder "rate-limiting"
_F_REG_RL_RESET    := --folder "rate-limit-reset"

# auth/verify-email
_F_VERIFY_MAIN     := --folder "setup" --folder "validation" --folder "happy-path" --folder "anti-enumeration" --folder "resend-validation" --folder "resend-happy-path"
_F_VERIFY_RL       := --folder "rate-limiting"

# auth/login
_F_LOGIN_MAIN      := --folder "setup" --folder "validation" --folder "happy-path" --folder "failures" --folder "time-lock"
_F_LOGIN_RL        := --folder "rate-limiting"

# auth/session (refresh + logout)
_F_SESSION_MAIN    := --folder "happy-path" --folder "failures" --folder "token-reuse"
_F_SESSION_RL      := --folder "rate-limiting"

# auth/unlock
_F_UNLOCK_MAIN     := --folder "happy-path" --folder "validation" --folder "anti-enumeration"
_F_UNLOCK_RL       := --folder "rate-limiting"

# auth/password-reset
_F_PW_RESET_MAIN   := --folder "setup" --folder "happy-path" --folder "anti-enumeration" --folder "validation"
_F_PW_RESET_RL_FPW := --folder "rate-limiting-fpw"
_F_PW_RESET_RL_RPW := --folder "rate-limiting-rpw"

# auth/change-password
_F_CHG_PW_MAIN     := --folder "setup" --folder "happy-path" --folder "failures" --folder "auth-failures" --folder "validation"
_F_CHG_PW_RL       := --folder "rate-limiting-cpw"

# profile/me, profile/sessions, profile/revoke-session — identical folder layout
# NOTE: rate-limiters are JWT-gated; all three folders run in one invocation.
_F_PROFILE_TRIO    := --folder "setup" --folder "happy-path" --folder "rate-limiting"

# profile/update-profile (JWT-gated rate-limiter — single invocation)
_F_UPD_PROF        := --folder "setup" --folder "happy-path" --folder "failures" --folder "auth-failures" --folder "validation" --folder "rate-limiting-prof"

# profile/set-password (JWT-gated rate-limiter — single invocation)
_F_SET_PW          := --folder "setup" --folder "failures" --folder "auth-failures" --folder "validation" --folder "rate-limiting-spw"

# profile/username  (rate-limiting-uchg is JWT-gated; rate-limiting-unav is not)
_F_USERNAME_MAIN   := --folder "setup" --folder "happy-path" --folder "failures" --folder "auth-failures" --folder "validation" --folder "rate-limiting-uchg"
_F_USERNAME_RL_NAV := --folder "rate-limiting-unav"

# profile/email (JWT-gated rate-limiters — single invocation)
_F_EMAIL           := --folder "setup" --folder "happy-path" --folder "failures" --folder "auth-failures" --folder "validation" --folder "rate-limiting-req" --folder "rate-limiting-vfy" --folder "rate-limiting-cnf"

# profile/delete-account (JWT-gated rate-limiters — single invocation)
_F_DEL_ACC         := --folder "setup" --folder "happy-path-password" --folder "happy-path-cancel" --folder "happy-path-email-otp" --folder "telegram-guards" --folder "failures" --folder "auth-failures" --folder "validation" --folder "rate-limiting-del" --folder "rate-limiting-delc"

# profile/identities (JWT-gated rate-limiter — single invocation)
_F_IDENTITIES      := --folder "setup" --folder "happy-path" --folder "auth-guard" --folder "rate-limiting"

# oauth/google (rate-limiting-unl is JWT-gated; init and cb are not)
_F_GOOGLE_MAIN     := --folder "setup" --folder "initiate" --folder "callback-guards" --folder "unlink-failures" --folder "rate-limiting-unl"
_F_GOOGLE_RL_INIT  := --folder "rate-limiting-init"
_F_GOOGLE_RL_CB    := --folder "rate-limiting-cb"

# oauth/telegram (lnk/unlk are JWT-gated; cb is not — single invocation for main)
_F_TELEGRAM_MAIN   := --folder "setup" --folder "callback-happy-path" --folder "callback-failures" --folder "validation" --folder "link-happy-path" --folder "link-failures" --folder "unlink-happy-path" --folder "unlink-failures" --folder "auth-failures" --folder "rate-limiting-lnk" --folder "rate-limiting-unlk"
_F_TELEGRAM_RL_CB  := --folder "rate-limiting-cb"

# rbac/bootstrap (JWT-gated rate-limiter — single invocation)
# ALL folders share ONE invocation so bstrp_access_token (captured in setup/login)
# persists across folders. The rate-limiting folder requires a valid JWT because
# the bstrp:ip: limiter fires AFTER the JWTAuth middleware.
# The main run assigns unique X-Forwarded-For IPs via the collection prerequest
# counter, so the 127.0.0.1 bucket used by rate-limiting is never consumed by
# earlier folders — no Redis flush is needed between them.
_F_BSTRP_MAIN      := --folder "setup" --folder "auth-guard" --folder "validation" --folder "secret-guard" --folder "happy-path" --folder "owner-already-exists" --folder "rate-limiting"

# rbac/permissions (no rate limiter — single invocation)
# owner-bootstrap promotes the setup user to owner mid-run; the RBAC middleware
# reads permissions from DB on every request so the same JWT goes from 403 → 200
# without re-issuing. No Redis flush needed.
_F_PERMS_MAIN      := --folder "setup" --folder "auth-guard" --folder "rbac-guard" --folder "owner-bootstrap" --folder "happy-path"

# rbac/roles (no rate limiter — single invocation)
# owner-bootstrap promotes the setup user to owner mid-run so the same JWT
# covers rbac-guard (403) and all subsequent folders (200/201/204).
# No Redis flush needed.
_F_ROLES_MAIN      := --folder "setup" --folder "auth-guard" --folder "rbac-guard" --folder "owner-bootstrap" --folder "happy-path" --folder "not-found" --folder "validation" --folder "immutable-guard"

# rbac/userroles (no rate limiter — single invocation)
# owner-bootstrap promotes the setup user to owner mid-run so the same JWT
# covers rbac-guard (403) and all subsequent folders (200/204).
# No Redis flush needed.
_F_USERROLES_MAIN  := --folder "setup" --folder "auth-guard" --folder "rbac-guard" --folder "owner-bootstrap" --folder "happy-path" --folder "conflict-guard" --folder "not-found" --folder "validation"

# rbac/userpermissions (no rate limiter — single invocation)
# owner-bootstrap promotes the setup user to owner mid-run so the same JWT
# covers rbac-guard (403) and all subsequent folders (200/201/204).
# No Redis flush needed.
_F_USERPERMS_MAIN  := --folder "setup" --folder "auth-guard" --folder "rbac-guard" --folder "owner-bootstrap" --folder "happy-path" --folder "not-found" --folder "validation" --folder "conflict"

# ── PHONY ─────────────────────────────────────────────────────────────────────
.PHONY: e2e-install \
        e2e e2e-health \
        e2e-register e2e-verify-email e2e-login e2e-session e2e-unlock \
        e2e-password \
        e2e-me e2e-sessions e2e-revoke-session \
        e2e-update-profile e2e-set-password e2e-username e2e-email \
        e2e-delete-account e2e-identities \
        e2e-oauth-google e2e-oauth-telegram \
        e2e-profile e2e-auth e2e-oauth \
        e2e-rbac e2e-rbac-bootstrap e2e-rbac-permissions e2e-rbac-roles \
        e2e-rbac-userroles e2e-rbac-userpermissions \
        _e2e-db-clean _e2e-kv-clean _e2e-clean _e2e-check-env

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
#
# Why sequential statements instead of one big CTE:
# Postgres data-modifying CTEs execute concurrently against the same initial
# snapshot. This means the CTE that deletes users and the CTE that deletes
# user_roles both run "at the same time". When the CASCADE from DELETE users
# fires fn_audit_user_roles, the users row is simultaneously being removed, so
# the INSERT into user_roles_audit violates fk_ur_audit_user (RESTRICT).
# Sequential statements in one transaction avoid this: each statement sees the
# committed effects of all prior statements in the same transaction.
#
# Execution order (all inside one BEGIN…COMMIT block):
#   1. skip_orphan_check — suppresses trg_prevent_orphaned_owner mid-run
#   2. DELETE orphaned user_roles / user_permissions — rows whose parent user
#      was already removed by a previous failed cleanup. The trigger guard added
#      in migration 009 silently skips the audit INSERT for these rows.
#   3. DELETE user_roles / user_permissions for target users while they still
#      exist — triggers fire and write audit rows successfully (FK satisfied).
#   4. Sweep ALL *_audit rows that reference target users (including those just
#      written by step 3), plus auth_audit_log.
#   5. DELETE users — no CASCADE fires (child rows already gone).
#
# Uses TEST_DATABASE_URL so e2e tests never touch the dev database.
_e2e-db-clean:
	@psql "$(TEST_DATABASE_URL)" -c "BEGIN; SET LOCAL rbac.skip_orphan_check = '1'; DELETE FROM user_roles WHERE user_id NOT IN (SELECT id FROM users); DELETE FROM user_permissions WHERE user_id NOT IN (SELECT id FROM users); CREATE TEMP TABLE _e2e_target (id UUID) ON COMMIT DROP; INSERT INTO _e2e_target SELECT id FROM users WHERE email LIKE '%@e2e.test' OR email ~ '@xn--' OR email = '$(E2E_GMAIL_EMAIL)' OR email LIKE '%+spwrl@%' OR email LIKE '%+usrnrl@%' OR email LIKE '%+echgnew@%' OR email LIKE '%+echgfail@%' OR email LIKE '%+echgrlreq@%' OR email LIKE '%+echgrlvfy@%' OR email LIKE '%+echgrlcnf@%' OR email LIKE '%+goauthr@%' OR email LIKE '%+tgrl@%' OR email LIKE '%+delb@%' OR email LIKE '%+delvc@%' OR email LIKE '%+delvd@%' OR email LIKE '%+delrl@%' OR email LIKE '%+cncrl@%' OR email LIKE '%+uptarget@%' OR id IN (SELECT user_id FROM user_identities WHERE provider = 'telegram' AND provider_uid IN ('99887766', '99887700', '99887744')); DELETE FROM user_roles WHERE user_id IN (SELECT id FROM _e2e_target); DELETE FROM user_permissions WHERE user_id IN (SELECT id FROM _e2e_target); DELETE FROM auth_audit_log WHERE user_id IN (SELECT id FROM _e2e_target); DELETE FROM user_roles_audit WHERE user_id IN (SELECT id FROM _e2e_target) OR changed_by IN (SELECT id FROM _e2e_target); DELETE FROM user_permissions_audit WHERE user_id IN (SELECT id FROM _e2e_target) OR changed_by IN (SELECT id FROM _e2e_target); DELETE FROM role_permissions_audit WHERE changed_by IN (SELECT id FROM _e2e_target); DELETE FROM permission_request_approvers_audit WHERE changed_by IN (SELECT id FROM _e2e_target); DELETE FROM users WHERE id IN (SELECT id FROM _e2e_target); COMMIT;"
	@echo "[e2e] DB cleaned (e2e users removed from test DB)"

# Flush Redis DB 1 (the test server's rate-limiter and blocklist store).
# WARNING: this wipes ALL keys in DB 1 — only use on a dedicated dev/CI Redis.
_e2e-kv-clean:
	@docker exec $(COMPOSE_PROJECT_NAME)_redis redis-cli -n 1 --no-auth-warning -a $(REDIS_PASSWORD) FLUSHDB 2>$(NULL)
	@$(call _e2e_gray,[e2e] Redis flushed (DB 1))

# Full clean: DB test users + Redis.
_e2e-clean: _e2e-db-clean _e2e-kv-clean

# Guard: abort with a clear message if environment.json is missing.
_e2e-check-env:
ifeq ($(DETECTED_OS),Windows)
	@if (-not (Test-Path "$(E2E_ENV)")) { Write-Host "[ERROR] Missing $(E2E_ENV)" -ForegroundColor Red; Write-Host "  Run: cp $(E2E_TEMPLATE) $(E2E_ENV)" -ForegroundColor Yellow; exit 1 }
else
	@if [ ! -f "$(E2E_ENV)" ]; then echo "[ERROR] Missing $(E2E_ENV)"; echo "  Run: cp $(E2E_TEMPLATE) $(E2E_ENV)"; exit 1; fi
endif

# ── Suite targets ─────────────────────────────────────────────────────────────

e2e: e2e-health e2e-auth e2e-oauth e2e-profile e2e-rbac ## Run ALL e2e suites (health + auth + oauth + profile + rbac)

e2e-auth: _e2e-check-env ## Run all auth E2E collections in order (register → verify-email → login → session → unlock → password)
	@$(MAKE) e2e-register
	@$(MAKE) e2e-verify-email
	@$(MAKE) e2e-login
	@$(MAKE) e2e-session
	@$(MAKE) e2e-unlock
	@$(MAKE) e2e-password
	@$(call _e2e_ok,[e2e] auth suite passed)

e2e-oauth: _e2e-check-env ## Run all OAuth E2E collections in order (google + telegram)
	@$(MAKE) e2e-oauth-google
	@$(MAKE) e2e-oauth-telegram
	@$(call _e2e_ok,[e2e] oauth suite passed)

# NOTE: profile rate-limiters (pme:ip:, psess:ip:, rsess:ip:) sit INSIDE the
# r.Use(deps.JWTAuth) middleware group, so JWT auth fires before the limiter.
# Each sub-target runs ALL folders (including rate-limiting) in ONE invocation
# so the JWT stored in collection variables is not lost between runs.
e2e-profile: _e2e-check-env ## Run all profile E2E collections in order (requires JWT)
	@$(MAKE) e2e-me
	@$(MAKE) e2e-sessions
	@$(MAKE) e2e-revoke-session
	@$(MAKE) e2e-update-profile
	@$(MAKE) e2e-set-password
	@$(MAKE) e2e-username
	@$(MAKE) e2e-email
	@$(MAKE) e2e-delete-account
	@$(MAKE) e2e-identities
	@$(call _e2e_ok,[e2e] profile suite passed)

e2e-rbac: _e2e-check-env ## Run all RBAC E2E collections in order (bootstrap + permissions + roles + userroles + userpermissions)
	@$(MAKE) e2e-rbac-bootstrap
	@$(MAKE) e2e-rbac-permissions
	@$(MAKE) e2e-rbac-roles
	@$(MAKE) e2e-rbac-userroles
	@$(MAKE) e2e-rbac-userpermissions
	@$(call _e2e_ok,[e2e] rbac suite passed)

# ── health ────────────────────────────────────────────────────────────────────

e2e-health: _e2e-check-env ## Smoke-test GET /health (response shape, security headers, rate-limiting)
	@$(call _e2e_info,[e2e] --- GET /health ---)
	@$(MAKE) _e2e-kv-clean
	@$(call _e2e_gray,[e2e] Running: smoke)
	$(call newman-run,$(E2E_DIR)/health/health.json,$(_F_HEALTH_SMOKE),$(E2E_DELAY))
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting...)
	@$(MAKE) _e2e-kv-clean
	@$(call _e2e_gray,[e2e] Running: rate-limiting)
	$(call newman-run,$(E2E_DIR)/health/health.json,$(_F_HEALTH_RL),1)

# ── auth/register ─────────────────────────────────────────────────────────────

e2e-register: _e2e-check-env ## Run POST /register E2E (all folders including rate-limit-reset)
	@$(call _e2e_info,[e2e] --- POST /register ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: infrastructure + validation + happy-path + conflict)
	$(call newman-run,$(E2E_AUTH)/register.json,$(_F_REG_MAIN),$(E2E_DELAY))
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting...)
	@$(MAKE) _e2e-kv-clean
	@$(call _e2e_gray,[e2e] Running: rate-limiting)
	$(call newman-run,$(E2E_AUTH)/register.json,$(_F_REG_RL),1)
	@$(call _e2e_gray,[e2e] Flushing Redis to simulate window expiry...)
	@$(MAKE) _e2e-kv-clean
	@$(call _e2e_gray,[e2e] Running: rate-limit-reset)
	$(call newman-run,$(E2E_AUTH)/register.json,$(_F_REG_RL_RESET),$(E2E_DELAY))

# ── auth/verify-email ─────────────────────────────────────────────────────────

e2e-verify-email: _e2e-check-env ## Run POST /verify-email + POST /resend-verification E2E
	@$(call _e2e_info,[e2e] --- POST /verify-email + POST /resend-verification ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + validation + happy-path + anti-enumeration + resend-validation + resend-happy-path)
	$(call newman-run,$(E2E_AUTH)/verify-email.json,$(_F_VERIFY_MAIN),$(E2E_DELAY))
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting...)
	@$(MAKE) _e2e-kv-clean
	@$(call _e2e_gray,[e2e] Running: rate-limiting)
	$(call newman-run,$(E2E_AUTH)/verify-email.json,$(_F_VERIFY_RL),$(E2E_DELAY))

# ── auth/login ────────────────────────────────────────────────────────────────

e2e-login: _e2e-check-env ## Run POST /login E2E
	@$(call _e2e_info,[e2e] --- POST /login ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + validation + happy-path + failures + time-lock)
	$(call newman-run,$(E2E_AUTH)/login.json,$(_F_LOGIN_MAIN),$(E2E_DELAY))
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting...)
	@$(MAKE) _e2e-kv-clean
	@$(call _e2e_gray,[e2e] Running: rate-limiting)
	$(call newman-run,$(E2E_AUTH)/login.json,$(_F_LOGIN_RL),$(E2E_DELAY))

# ── auth/session (refresh + logout) ──────────────────────────────────────────

e2e-session: _e2e-check-env ## Run POST /refresh + POST /logout E2E
	@$(call _e2e_info,[e2e] --- POST /refresh + POST /logout ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: happy-path + failures + token-reuse)
	$(call newman-run,$(E2E_AUTH)/session.json,$(_F_SESSION_MAIN),$(E2E_DELAY))
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting...)
	@$(MAKE) _e2e-kv-clean
	@$(call _e2e_gray,[e2e] Running: rate-limiting)
	$(call newman-run,$(E2E_AUTH)/session.json,$(_F_SESSION_RL),$(E2E_DELAY))

# ── auth/unlock ───────────────────────────────────────────────────────────────

e2e-unlock: _e2e-check-env ## Run POST /request-unlock + POST /confirm-unlock E2E
	@$(call _e2e_info,[e2e] --- POST /request-unlock + POST /confirm-unlock ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: happy-path + validation + anti-enumeration)
	$(call newman-run,$(E2E_AUTH)/unlock.json,$(_F_UNLOCK_MAIN),$(E2E_DELAY))
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting...)
	@$(MAKE) _e2e-kv-clean
	@$(call _e2e_gray,[e2e] Running: rate-limiting)
	$(call newman-run,$(E2E_AUTH)/unlock.json,$(_F_UNLOCK_RL),$(E2E_DELAY))
	@$(call _e2e_ok,[e2e] unlock suite passed)

# ── auth/password-reset + auth/change-password ────────────────────────────────

e2e-password: _e2e-check-env ## Run POST /forgot-password + /reset-password + /change-password E2E
	@$(call _e2e_info,[e2e] --- POST /forgot-password + POST /reset-password ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + happy-path + anti-enumeration + validation)
	$(call newman-run,$(E2E_AUTH)/password-reset.json,$(_F_PW_RESET_MAIN),$(E2E_DELAY))
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting-fpw...)
	@$(MAKE) _e2e-kv-clean
	$(call newman-run,$(E2E_AUTH)/password-reset.json,$(_F_PW_RESET_RL_FPW),1)
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting-rpw...)
	@$(MAKE) _e2e-kv-clean
	$(call newman-run,$(E2E_AUTH)/password-reset.json,$(_F_PW_RESET_RL_RPW),1)
	@$(call _e2e_info,[e2e] --- POST /change-password ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + happy-path + failures + auth-failures + validation)
	$(call newman-run,$(E2E_AUTH)/change-password.json,$(_F_CHG_PW_MAIN),$(E2E_DELAY))
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting-cpw...)
	@$(MAKE) _e2e-kv-clean
	$(call newman-run,$(E2E_AUTH)/change-password.json,$(_F_CHG_PW_RL),1)
	@$(call _e2e_ok,[e2e] password suite passed)

# ── profile/me ────────────────────────────────────────────────────────────────

# NOTE: pme:ip: rate-limiter is JWT-gated — all folders run in one invocation.
e2e-me: _e2e-check-env ## Run GET /me E2E (requires JWT — all folders in one invocation)
	@$(call _e2e_info,[e2e] --- GET /me ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + happy-path + rate-limiting (single invocation))
	$(call newman-run,$(E2E_PROFILE)/me.json,$(_F_PROFILE_TRIO),1)

# ── profile/sessions ──────────────────────────────────────────────────────────

# NOTE: psess:ip: rate-limiter is JWT-gated — all folders run in one invocation.
e2e-sessions: _e2e-check-env ## Run GET /sessions E2E (requires JWT — all folders in one invocation)
	@$(call _e2e_info,[e2e] --- GET /sessions ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + happy-path + rate-limiting (single invocation))
	$(call newman-run,$(E2E_PROFILE)/sessions.json,$(_F_PROFILE_TRIO),1)

# ── profile/revoke-session ────────────────────────────────────────────────────

# NOTE: rsess:ip: rate-limiter is JWT-gated — all folders run in one invocation.
e2e-revoke-session: _e2e-check-env ## Run DELETE /sessions/{id} E2E (requires JWT — all folders in one invocation)
	@$(call _e2e_info,[e2e] --- DELETE /sessions/{id} ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + happy-path + rate-limiting (single invocation))
	$(call newman-run,$(E2E_PROFILE)/revoke-session.json,$(_F_PROFILE_TRIO),1)

# ── profile/update-profile ────────────────────────────────────────────────────

# NOTE: rate-limiting-prof is JWT-gated — all folders run in one invocation.
e2e-update-profile: _e2e-check-env ## Run PATCH /me E2E (requires JWT — all folders in one invocation)
	@$(call _e2e_info,[e2e] --- PATCH /me ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + happy-path + failures + auth-failures + validation + rate-limiting-prof (single invocation))
	$(call newman-run,$(E2E_PROFILE)/update-profile.json,$(_F_UPD_PROF),1)
	@$(call _e2e_ok,[e2e] update-profile suite passed)

# ── profile/set-password ──────────────────────────────────────────────────────

# NOTE: rate-limiting-spw is JWT-gated — all folders run in one invocation.
e2e-set-password: _e2e-check-env ## Run POST /set-password E2E (requires JWT — all folders in one invocation)
	@$(call _e2e_info,[e2e] --- POST /set-password ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + failures + auth-failures + validation + rate-limiting-spw (single invocation))
	$(call newman-run,$(E2E_PROFILE)/set-password.json,$(_F_SET_PW),1)
	@$(call _e2e_ok,[e2e] set-password suite passed)

# ── profile/username ──────────────────────────────────────────────────────────

# NOTE: rate-limiting-uchg is JWT-gated (single invocation with main folders).
#       rate-limiting-unav is unauthenticated — separate invocation after Redis flush.
e2e-username: _e2e-check-env ## Run GET /username/available + PATCH /me/username E2E
	@$(call _e2e_info,[e2e] --- GET /username/available + PATCH /me/username ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + happy-path + failures + auth-failures + validation + rate-limiting-uchg (single invocation))
	$(call newman-run,$(E2E_PROFILE)/username.json,$(_F_USERNAME_MAIN),1)
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting-unav...)
	@$(MAKE) _e2e-kv-clean
	@$(call _e2e_gray,[e2e] Running: rate-limiting-unav)
	$(call newman-run,$(E2E_PROFILE)/username.json,$(_F_USERNAME_RL_NAV),1)
	@$(call _e2e_ok,[e2e] username suite passed)

# ── profile/email ─────────────────────────────────────────────────────────────

# NOTE: rate-limiting-req/vfy/cnf are JWT-gated — all folders run in one invocation.
e2e-email: _e2e-check-env ## Run POST /email/request-change + verify-current + confirm-change E2E (requires JWT)
	@$(call _e2e_info,[e2e] --- POST /email/request-change + verify-current + confirm-change ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + happy-path + failures + auth-failures + validation + rate-limiting-req + rate-limiting-vfy + rate-limiting-cnf (single invocation))
	$(call newman-run,$(E2E_PROFILE)/email.json,$(_F_EMAIL),1)
	@$(call _e2e_ok,[e2e] email-change suite passed)

# ── profile/delete-account ────────────────────────────────────────────────────

# NOTE: del:usr: (3 req/1hr) and delc:usr: (5 req/10min) are JWT-gated.
#       All folders run in one invocation; Redis flushed once before the run.
e2e-delete-account: _e2e-check-env ## Run DELETE /me + POST /me/cancel-deletion E2E (requires JWT — all folders in one invocation)
	@$(call _e2e_info,[e2e] --- DELETE /profile/me + POST /profile/me/cancel-deletion ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + happy-path-password + happy-path-cancel + happy-path-email-otp + telegram-guards + failures + auth-failures + validation + rate-limiting-del + rate-limiting-delc (single invocation))
	$(call newman-run,$(E2E_PROFILE)/delete-account.json,$(_F_DEL_ACC),1)
	@$(call _e2e_ok,[e2e] delete-account suite passed)

# ── profile/identities ────────────────────────────────────────────────────────

# NOTE: rate-limiter is JWT-gated — all folders run in one invocation.
e2e-identities: _e2e-check-env ## Run GET /me/identities E2E (requires JWT — all folders in one invocation)
	@$(call _e2e_info,[e2e] --- GET /me/identities ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + happy-path + auth-guard + rate-limiting (single invocation))
	$(call newman-run,$(E2E_PROFILE)/identities.json,$(_F_IDENTITIES),1)
	@$(call _e2e_ok,[e2e] identities suite passed)

# ── oauth/google ──────────────────────────────────────────────────────────────

# NOTE: rate-limiting-unl is JWT-gated (single invocation with main folders).
#       rate-limiting-init and rate-limiting-cb are unauthenticated — separate
#       invocations after Redis flush.
e2e-oauth-google: _e2e-check-env ## Run Google OAuth E2E (initiate + callback guards + unlink)
	@$(call _e2e_info,[e2e] --- GET /oauth/google + GET /oauth/google/callback + DELETE /oauth/google/unlink ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + initiate + callback-guards + unlink-failures + rate-limiting-unl (single invocation))
	$(call newman-run,$(E2E_OAUTH)/google.json,$(_F_GOOGLE_MAIN),1)
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting-init...)
	@$(MAKE) _e2e-kv-clean
	$(call newman-run,$(E2E_OAUTH)/google.json,$(_F_GOOGLE_RL_INIT),1)
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting-cb...)
	@$(MAKE) _e2e-kv-clean
	$(call newman-run,$(E2E_OAUTH)/google.json,$(_F_GOOGLE_RL_CB),1)
	@$(call _e2e_ok,[e2e] oauth-google suite passed)

# ── oauth/telegram ────────────────────────────────────────────────────────────

# NOTE: rate-limiting-lnk/unlk are JWT-gated (single invocation with main folders).
#       rate-limiting-cb is unauthenticated — separate invocation after Redis flush.
e2e-oauth-telegram: _e2e-check-env ## Run Telegram OAuth E2E (callback + link + unlink)
	@$(call _e2e_info,[e2e] --- POST /oauth/telegram/callback + POST /oauth/telegram/link + DELETE /oauth/telegram/unlink ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + callback-happy-path + callback-failures + validation + link-happy-path + link-failures + unlink-happy-path + unlink-failures + auth-failures + rate-limiting-lnk + rate-limiting-unlk (single invocation))
	$(call newman-run,$(E2E_OAUTH)/telegram.json,$(_F_TELEGRAM_MAIN),1)
	@$(call _e2e_gray,[e2e] Flushing Redis before rate-limiting-cb...)
	@$(MAKE) _e2e-kv-clean
	$(call newman-run,$(E2E_OAUTH)/telegram.json,$(_F_TELEGRAM_RL_CB),1)
	@$(call _e2e_ok,[e2e] oauth-telegram suite passed)

# ── rbac/roles ───────────────────────────────────────────────────────────────

# No rate limiter on admin routes — single invocation, no Redis flush needed.
# owner-bootstrap runs mid-collection (promotes the test user to owner so both
# RBAC guards pass); the same JWT is reused for all subsequent folders.
e2e-rbac-roles: _e2e-check-env ## Run CRUD /admin/rbac/roles E2E (requires rbac:read + rbac:manage)
	@$(call _e2e_info,[e2e] --- CRUD /admin/rbac/roles ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + auth-guard + rbac-guard + owner-bootstrap + happy-path + not-found + validation + immutable-guard (single invocation))
	$(call newman-run,$(E2E_RBAC)/roles.json,$(_F_ROLES_MAIN),$(E2E_DELAY))
	@$(call _e2e_ok,[e2e] rbac-roles suite passed)

# ── rbac/userroles ───────────────────────────────────────────────────────────

# No rate limiter on admin routes — single invocation, no Redis flush needed.
# owner-bootstrap runs mid-collection (promotes the test user to owner so both
# RBAC guards pass); the same JWT is reused for all subsequent folders.
e2e-rbac-userroles: _e2e-check-env ## Run GET/PUT/DELETE /admin/rbac/users/{user_id}/role E2E (requires rbac:read + rbac:manage)
	@$(call _e2e_info,[e2e] --- GET/PUT/DELETE /admin/rbac/users/{user_id}/role ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + auth-guard + rbac-guard + owner-bootstrap + happy-path + conflict-guard + not-found + validation (single invocation))
	$(call newman-run,$(E2E_RBAC)/userroles.json,$(_F_USERROLES_MAIN),$(E2E_DELAY))
	@$(call _e2e_ok,[e2e] rbac-userroles suite passed)

# ── rbac/bootstrap ────────────────────────────────────────────────────────────

# Rate limit: burst=3, rate=3/(15*60) tok/s → Retry-After = ceil(900/3) = 300 s.
# All folders share ONE invocation so bstrp_access_token (captured in setup/login)
# persists in collection variables. Redis is flushed before rate-limiting.
e2e-rbac-bootstrap: _e2e-check-env ## Run POST /owner/bootstrap E2E (all folders)
	@$(call _e2e_info,[e2e] --- POST /owner/bootstrap ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + auth-guard + validation + secret-guard + happy-path + owner-already-exists + rate-limiting (single invocation))
	$(call newman-run,$(E2E_RBAC)/bootstrap.json,$(_F_BSTRP_MAIN),$(E2E_DELAY))
	@$(call _e2e_ok,[e2e] rbac-bootstrap suite passed)

# ── rbac/permissions ──────────────────────────────────────────────────────────

# No rate limiter on admin routes — single invocation, no Redis flush needed.
# owner-bootstrap runs mid-collection (promotes the test user to owner so the
# RBAC guard passes); the same JWT is reused for happy-path without re-logging in.
e2e-rbac-permissions: _e2e-check-env ## Run GET /admin/permissions + GET /admin/permissions/groups E2E (requires rbac:read)
	@$(call _e2e_info,[e2e] --- GET /admin/permissions + GET /admin/permissions/groups ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + auth-guard + rbac-guard + owner-bootstrap + happy-path (single invocation))
	$(call newman-run,$(E2E_RBAC)/permissions.json,$(_F_PERMS_MAIN),$(E2E_DELAY))
	@$(call _e2e_ok,[e2e] rbac-permissions suite passed)

# ── rbac/userpermissions ──────────────────────────────────────────────────────

# No rate limiter on admin routes — single invocation, no Redis flush needed.
# owner-bootstrap runs mid-collection (promotes the acting user to owner so both
# RBAC guards pass); the same JWT covers rbac-guard (403) and all subsequent
# folders (200/201/204) without re-issuing.
# The target user (+uptarget alias) is cleaned up by _e2e-db-clean via the
# '%+uptarget@%' pattern added to the cleanup query.
e2e-rbac-userpermissions: _e2e-check-env ## Run GET/POST/DELETE /admin/rbac/users/{user_id}/permissions E2E (requires rbac:read + rbac:grant_user_perm)
	@$(call _e2e_info,[e2e] --- GET/POST/DELETE /admin/rbac/users/{user_id}/permissions ---)
	@$(MAKE) _e2e-clean
	@$(call _e2e_gray,[e2e] Running: setup + auth-guard + rbac-guard + owner-bootstrap + happy-path + not-found + validation + conflict (single invocation))
	$(call newman-run,$(E2E_RBAC)/userpermissions.json,$(_F_USERPERMS_MAIN),$(E2E_DELAY))
	@$(call _e2e_ok,[e2e] rbac-userpermissions suite passed)
