# ─── OS Detection & Shell Configuration ─────────────────────────────────────
# Included first by the root Makefile so every subsequent file can rely on
# DETECTED_OS, SHELL, SET_PGPASS, and the cross-platform tool aliases.

ifeq ($(OS),Windows_NT)
	SHELL          := pwsh.exe
	.SHELLFLAGS    := -NoProfile -Command
	DETECTED_OS    := Windows
	NULL           := $$null
else
	DETECTED_OS    := $(shell uname -s)
	SHELL          := /bin/bash
	NULL           := /dev/null
endif

# ─── Cross-Platform Tool Aliases ─────────────────────────────────────────────

ifeq ($(DETECTED_OS),Windows)
	GOOSE      := goose
	SQLC       := sqlc
	PSQL       := psql -U $(DB_USER) -h $(DB_HOST) -p $(DB_PORT)
	PG_DUMP    := pg_dump -U $(DB_USER) -h $(DB_HOST) -p $(DB_PORT)
	# SET_PGPASS sets PGPASSWORD for the current PowerShell session line.
	# $env:PGPASSWORD — the $ escapes Make's variable expansion; PowerShell sees $env:
	SET_PGPASS := $$env:PGPASSWORD='$(DB_PASSWORD)';
else
	GOOSE      := goose
	SQLC       := sqlc
	PSQL       := psql -U $(DB_USER) -h $(DB_HOST) -p $(DB_PORT)
	PG_DUMP    := pg_dump -U $(DB_USER) -h $(DB_HOST) -p $(DB_PORT)
	# SET_PGPASS injects the password as an environment variable so it never
	# appears in process listings, make output, or shell history.
	SET_PGPASS := PGPASSWORD='$(DB_PASSWORD)'
endif
