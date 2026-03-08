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
  domain/oauth/   # OAuth providers (Google, …)
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
- `GOOGLE_CLIENT_ID` + `GOOGLE_CLIENT_SECRET` + `GOOGLE_REDIRECT_URI` (see below)

## Google OAuth Setup

The API supports Google OAuth 2.0 (login, register, and account linking). Follow these steps to configure your credentials.

### 1. Create a Google Cloud project

Go to [console.cloud.google.com](https://console.cloud.google.com), create a new project (or select an existing one).

### 2. Configure the OAuth consent screen

Navigate to **APIs & Services → OAuth consent screen**:

- **User type** — choose **External** (allows any Google account, not just your org)
- Fill in **App name**, **User support email**, and **Developer contact email**
- Under **Scopes**, add: `openid`, `email`, `profile` — these are non-sensitive and require no Google review
- Leave everything else at defaults and save

> The app starts in **Testing** mode. You'll see a "Google hasn't verified this app" warning during the OAuth flow — this is normal in development. Publish the app when you're ready for production.

### 3. Create an OAuth 2.0 Client ID

Navigate to **APIs & Services → Credentials → Create Credentials → OAuth 2.0 Client ID**:

- **Application type** — Web application
- **Authorized JavaScript origins** — leave empty (the flow is server-side; the browser never calls Google directly)
- **Authorized redirect URIs** — add:
  ```
  http://localhost:8080/api/v1/oauth/google/callback
  ```
  For production, also add:
  ```
  https://api.yourdomain.com/api/v1/oauth/google/callback
  ```

Click **Create**. Copy the **Client ID** and **Client Secret** from the dialog that appears (the secret is shown once).

### 4. Add credentials to `.env`

```env
GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=GOCSPX-your-client-secret

# Must exactly match the redirect URI registered above
GOOGLE_REDIRECT_URI=http://localhost:8080/api/v1/oauth/google/callback

# Frontend URL to redirect to after a successful OAuth login/register/link
OAUTH_SUCCESS_URL=http://localhost:3000/dashboard

# Frontend URL to redirect to on failure (?error=<code> is appended)
OAUTH_ERROR_URL=http://localhost:3000/login
```

> The `GOOGLE_REDIRECT_URI` value must be **character-for-character identical** to what is registered in the Google Cloud Console. Any mismatch causes a `redirect_uri_mismatch` error from Google.
