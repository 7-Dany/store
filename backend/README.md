# Store Backend

[Go REST API for an e-commerce store](https://store-e032ba24.mintlify.app/guides/overview). Handles authentication, session management, and account lifecycle (register, verify email, reset password, unlock account) .

## Stack

- **Go** · chi router, pgx, JWT, bcrypt
- **PostgreSQL** · Goose migrations, SQLC-generated queries, pgTAP schema tests
- **Redis** · rate limiting, OTP/KV store
- **Docker** · Postgres + Redis via `docker-compose.yml`

## Quick Start

```bash
# 1. Copy env and fill in your values
make env-create

# 2. Start Docker services (Postgres + Redis)
make docker-up

# 3. Install tools, migrate, and generate code
make setup

# 4. Run the server
make run
```

> Server starts on `ADDR` from `.env` (default `:8080`).

## Common Commands

| Command | Description |
|---|---|
| `make run` | Build and run the API |
| `make test` | Unit tests (no DB) |
| `make test-integration` | Integration tests (requires DB) |
| `make test-all` | Unit + integration |
| `make test-coverage` | HTML coverage report |
| `make migrate-up` | Apply pending migrations |
| `make sqlc-generate` | Regenerate DB code from SQL |
| `make lint` | Run golangci-lint |
| `make e2e` | Run Newman e2e suites |

Run `make help` for the full list.

## Project Structure

```
cmd/api/          # Entrypoint (main.go)
internal/
  app/            # Dependency wiring
  config/         # Env-based config
  db/             # SQLC-generated queries
  domain/auth/    # Auth feature modules (login, register, password, session, …)
  platform/       # Shared utilities (token, mailer, ratelimit, crypto, kvstore)
  server/         # HTTP server + route registration
sql/
  schema/         # Goose migration files
  queries/        # SQLC SQL query files
make/             # Modular Makefile includes
e2e/              # Newman (Postman) collections
mint/             # API reference docs (Mintlify)
```

## Environment

Copy `.env.example` to `.env` and set at minimum:

- `DATABASE_URL` / `TEST_DATABASE_URL`
- `REDIS_URL` / `REDIS_PASSWORD`
- `JWT_ACCESS_SECRET` + `JWT_REFRESH_SECRET` (generate with `openssl rand -hex 32`)
- `SMTP_*` credentials for transactional email
- `ALLOWED_ORIGINS` for CORS
