# Environment Setup Guide

Everything you need to configure before running the backend for the first time.
Work through each section in order — later sections depend on earlier ones.

---

## 1. Copy the env file

```bash
make env-create
# or manually:
cp .env.example .env
```

Open `.env` and fill in the values described in each section below.

---

## 2. Server

No external setup required — just choose values that match your environment.

```env
APP_ENV=development        # development | staging | production
ADDR=:8080
APP_NAME="Store"           # shown in emails and page titles, max 64 chars
DOCS_ENABLED=false         # set true to expose GET /docs in development only
HTTPS_ENABLED=false        # set true in prod behind a TLS proxy (enables HSTS)
ALLOWED_ORIGINS=http://localhost:3000
```

**`HTTPS_DISABLED`** — uncomment this only for local HTTP development when you
need the refresh-token cookie to send over plain HTTP. Never set it in
`APP_ENV=production` — the app rejects that combination at startup.

```env
# HTTPS_DISABLED=true   # uncomment for local dev only
```

**`TRUSTED_PROXIES`** — comma-separated CIDR list of reverse-proxy IPs trusted
for `X-Forwarded-For` resolution. Set to `127.0.0.1/32,::1/128` when running
behind a local proxy or running e2e tests with Newman. Leave empty when there
is no proxy in front of the backend.

```env
TRUSTED_PROXIES=127.0.0.1/32,::1/128
```

---

## 3. Database (PostgreSQL)

The app expects a running PostgreSQL instance. The easiest path is the included
Docker Compose service.

```bash
make docker-up   # starts Postgres + Redis
```

Set the individual fields used by Docker Compose (these must match `DATABASE_URL`):

```env
DB_NAME=store_dev
DB_USER=postgres
DB_PASSWORD=password
DB_HOST=localhost
DB_PORT=5432
DB_SSL_MODE=disable
```

Then set the full connection strings used by the application:

```env
DATABASE_URL=postgres://postgres:password@localhost:5432/store_dev?sslmode=disable
TEST_DATABASE_URL=postgres://postgres:password@localhost:5432/store_dev_test?sslmode=disable
```

Run migrations after the database is reachable:

```bash
make migrate-up
```

### Connection pool tuning (optional)

The defaults are reasonable for most development workloads. Adjust in production
to match your database server's `max_connections` setting.

```env
DB_MAX_CONNS=20         # maximum open connections. Default: 20
DB_MIN_CONNS=2          # minimum idle connections kept alive. Default: 2
DB_MAX_CONN_LIFETIME=30m  # max time a connection may be reused. Default: 30m
DB_MAX_CONN_IDLE=5m     # max time a connection may sit idle. Default: 5m
DB_HEALTH_CHECK=1m      # how often the pool pings idle connections. Default: 1m
```

---

## 4. Redis

Redis is used for rate limiting and KV storage (OTP state, OAuth state, etc.).
It is started alongside Postgres by `make docker-up`.

Generate a strong password and set it in both places:

```bash
openssl rand -hex 32
```

```env
REDIS_PASSWORD=<output from above>
REDIS_PORT_DOCKER=6380        # host port Docker maps to the container
                               # 6380 avoids a conflict with a local Redis on 6379
REDIS_URL=redis://:YOUR_PASSWORD@localhost:6380/0
TEST_REDIS_URL=redis://:YOUR_PASSWORD@localhost:6380/1
```

The password in `REDIS_URL` must match `REDIS_PASSWORD` exactly — Docker Compose
passes it to `redis-server --requirepass` at container startup.

`TEST_REDIS_URL` uses DB index 1 to keep test keys isolated from dev data. Flush
between e2e runs with:

```bash
redis-cli -a <password> -p 6380 -n 1 FLUSHDB
```

---

## 5. JWT Secrets

Two separate HMAC-SHA256 signing keys are required — one for access tokens, one for
refresh tokens. Using distinct secrets means a leaked access-token key cannot be used
to forge refresh tokens, and each can be rotated independently.

Generate them independently — never copy one into the other:

```bash
openssl rand -hex 32   # → JWT_ACCESS_SECRET
openssl rand -hex 32   # → JWT_REFRESH_SECRET
```

Rules enforced at startup:
- Each must be at least 32 bytes
- The two values must not be equal
- High entropy required (all-same-character strings and low-entropy patterns are rejected)

```env
JWT_ACCESS_SECRET=<first output>
JWT_REFRESH_SECRET=<second output>
ACCESS_TOKEN_TTL=15m    # keep short — access tokens are not server-side revokable
```

---

## 6. Token Encryption Key

OAuth provider access tokens are stored encrypted at rest in `user_identities`
using AES-256-GCM. This key must be exactly 32 bytes (64 hex characters).

```bash
openssl rand -hex 32
```

```env
TOKEN_ENCRYPTION_KEY=<output from above>
```

The app validates the key at startup — a key that is not exactly 64 valid hex
characters is rejected immediately. Keep this key in a secret manager in
production. Losing it makes all stored OAuth access tokens unreadable.

---

## 7. SMTP (Transactional Email)

The app sends emails for email verification, OTP codes, password resets, and
account unlock flows. Any SMTP provider works.

### Gmail setup (recommended for development)

1. Go to your Google Account → **Security → 2-Step Verification** and make sure
   it is enabled (App Passwords require 2FA).
2. Go to **Security → App passwords** (search for it if not visible directly).
3. Choose **Mail** and your device, then click **Generate**.
4. Copy the 16-character password shown. Gmail displays it with spaces — paste
   the 16 characters **without spaces and without quotes**.

```env
SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_USERNAME=yourname@gmail.com
SMTP_PASSWORD=abcdefghijklmnop    # 16 chars, no spaces
SMTP_FROM=no-reply@yourdomain.com
OTP_VALID_MINUTES=15
```

### Other providers

| Provider | SMTP_HOST | SMTP_PORT |
|---|---|---|
| Gmail | smtp.gmail.com | 587 |
| Outlook / Hotmail | smtp-mail.outlook.com | 587 |
| Yahoo | smtp.mail.yahoo.com | 587 |
| Mailgun | smtp.mailgun.org | 587 |
| Postmark | smtp.postmarkapp.com | 587 |
| AWS SES | email-smtp.us-east-1.amazonaws.com | 587 |

For Mailgun, Postmark, and SES — use the SMTP credentials from their dashboard
instead of a personal account.

### Development alternative — Mailpit (zero-config)

If you do not want to configure a real mail provider locally, run
[Mailpit](https://github.com/axllent/mailpit) (a lightweight SMTP catcher with a
web UI) and point the app at it:

```env
SMTP_HOST=localhost
SMTP_PORT=1025
SMTP_USERNAME=test
SMTP_PASSWORD=test
SMTP_FROM=no-reply@store.local
```

All outbound emails are captured and viewable at `http://localhost:8025`. No
account or configuration needed.

### Mail queue tuning (optional)

```env
MAIL_WORKERS=4              # concurrent async mail-delivery goroutines. Default: 4
MAIL_DELIVERY_TIMEOUT=30s   # max time for a single delivery attempt. Default: 30s
```

Increase `MAIL_WORKERS` if you are sending a high volume of transactional emails
and see queue latency. Increase `MAIL_DELIVERY_TIMEOUT` if your SMTP provider is
slow to respond.

---

## 8. Google OAuth

Required for `POST /api/v1/auth/oauth/google/*` endpoints.

### 8.1 Create a Google Cloud project

Go to [console.cloud.google.com](https://console.cloud.google.com), create a new
project (or select an existing one).

### 8.2 Configure the OAuth consent screen

**APIs & Services → OAuth consent screen**:

- **User type** — External (allows any Google account)
- Fill in **App name**, **User support email**, and **Developer contact email**
- Under **Scopes** → Add: `openid`, `email`, `profile`
  (these are non-sensitive and do not require a Google review)
- Save and continue through the remaining steps

The app starts in **Testing** mode. During the OAuth flow you will see a
"Google hasn't verified this app" warning — this is expected in development.
Add test users under **Test users** if you hit a limit, or publish the app when
you are ready for production.

### 8.3 Create an OAuth 2.0 Client ID

**APIs & Services → Credentials → Create Credentials → OAuth 2.0 Client ID**:

- **Application type** — Web application
- **Authorized JavaScript origins** — leave empty (the flow is fully server-side)
- **Authorized redirect URIs** — add:
  ```
  http://localhost:8080/api/v1/oauth/google/callback
  ```
  For production, also add:
  ```
  https://api.yourdomain.com/api/v1/oauth/google/callback
  ```

Click **Create** and copy the **Client ID** and **Client Secret** from the dialog.

### 8.4 Set environment variables

```env
GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your-client-secret
GOOGLE_REDIRECT_URI=http://localhost:8080/api/v1/oauth/google/callback

OAUTH_SUCCESS_URL=http://localhost:3000/dashboard
OAUTH_ERROR_URL=http://localhost:3000/login
```

`GOOGLE_REDIRECT_URI` must be **character-for-character identical** to the URI
registered in Google Cloud Console — any mismatch causes a `redirect_uri_mismatch`
error before the backend is ever reached.

---

## 9. Telegram OAuth

Required for `POST /api/v1/auth/oauth/telegram/*` endpoints.

Telegram's Login Widget does not use redirect URLs or PKCE. The browser receives
a signed data bundle directly from Telegram's widget JS and posts it to your
backend for HMAC-SHA256 verification. The backend never calls a Telegram API —
it only verifies the signature.

However, the widget itself will only render on a domain that is registered with
your bot. This means **the frontend must be accessible at a public or tunnelled
HTTPS URL during development**.

### 9.1 Create a bot and get the token

1. Open Telegram and start a chat with [@BotFather](https://t.me/BotFather).
2. Send `/newbot` and follow the prompts (choose a name and a `@username` for the bot).
3. BotFather replies with your **bot token**:
   ```
   1234567890:AAF_your_bot_token_here
   ```
   Copy it — this is `TELEGRAM_BOT_TOKEN`.

```env
TELEGRAM_BOT_TOKEN=1234567890:AAF_your_bot_token_here
```

### 9.2 Register your domain with the bot

The Login Widget only fires from domains registered with your bot. Run this once
(substitute your bot token and domain):

```bash
curl -X POST "https://api.telegram.org/bot<YOUR_BOT_TOKEN>/setDomain" \
  -d "domain=yourdomain.com"
```

For local development you need a tunnel URL — see §9.3.

### 9.3 Local development — expose the frontend via a tunnel

Telegram checks the domain of the page that hosts the widget. Your **backend** does
not need to be public — only the **frontend** URL that renders the login button.

**Option A — [ngrok](https://ngrok.com)** (requires a free account):

```bash
ngrok http 3000
# prints: https://a1b2c3d4.ngrok-free.app
```

**Option B — [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)** (free, no account needed):

```bash
cloudflared tunnel --url http://localhost:3000
# prints: https://random-name.trycloudflare.com
```

**Option C — [localtunnel](https://github.com/localtunnel/localtunnel)** (npm):

```bash
npx localtunnel --port 3000
```

Once you have the tunnel URL, register the **hostname only** (no `https://`, no
path):

```bash
curl -X POST "https://api.telegram.org/bot<YOUR_BOT_TOKEN>/setDomain" \
  -d "domain=a1b2c3d4.ngrok-free.app"
```

Then open the frontend via the tunnel URL (e.g.
`https://a1b2c3d4.ngrok-free.app/login`). The widget only renders correctly when
accessed through the registered domain — opening it via `localhost:3000` will show
nothing.

> **Gotcha — tunnel URLs change on restart.** The ngrok free tier assigns a new
> random URL every time you restart the `ngrok` process. Each new URL requires a
> new `setDomain` call. To avoid this, pin a static subdomain on a paid ngrok plan,
> or use Cloudflare Tunnel with a named persistent tunnel.

### 9.4 Embed the Login Widget in the frontend

The widget script reads `data-*` attributes from the page and renders the Telegram
button. It must run client-side. Minimal Next.js component:

```tsx
// app/login/TelegramLoginButton.tsx
"use client";

import { useEffect } from "react";

const BOT_USERNAME = process.env.NEXT_PUBLIC_TELEGRAM_BOT_USERNAME!;
const CALLBACK_URL = `${process.env.NEXT_PUBLIC_API_URL}/auth/oauth/telegram/callback`;

export function TelegramLoginButton() {
  useEffect(() => {
    const script = document.createElement("script");
    script.src = "https://telegram.org/js/telegram-widget.js?22";
    script.setAttribute("data-telegram-login", BOT_USERNAME);
    script.setAttribute("data-size", "large");
    script.setAttribute("data-auth-url", CALLBACK_URL);
    script.setAttribute("data-request-access", "write");
    script.async = true;
    document.getElementById("tg-login")?.appendChild(script);
  }, []);

  return <div id="tg-login" />;
}
```

Add to your frontend `.env.local`:

```env
NEXT_PUBLIC_TELEGRAM_BOT_USERNAME=your_bot_username
```

### 9.5 Verify the HMAC setup is working

After starting the backend with `TELEGRAM_BOT_TOKEN` set, the callback endpoint
rejects any payload with an invalid signature. Smoke-test it with:

```bash
curl -X POST http://localhost:8080/api/v1/auth/oauth/telegram/callback \
  -H "Content-Type: application/json" \
  -d '{"id":123,"auth_date":0,"hash":"invalid"}'
# Expected: 401 {"code":"invalid_signature"}
```

---

## 10. Docker — project name

Docker Compose uses `COMPOSE_PROJECT_NAME` as a prefix for container and volume
names. Set it to something that identifies the project:

```env
COMPOSE_PROJECT_NAME=store
```

The Postgres container will be named `<COMPOSE_PROJECT_NAME>_postgres`. If you run
multiple projects with the same name on the same machine, containers will conflict.

---

## 11. E2E Tests

The e2e suite uses a real Gmail inbox to receive OTP emails during the
`verify-email` test flow.

```env
E2E_GMAIL_EMAIL=yourname@gmail.com
```

This must match `SMTP_USERNAME` (the account the backend sends from) and
`gmail_test_email` in `e2e/environment.json`. The `_e2e-db-clean` make target
deletes this user before each run so the `register` step never returns 409.

---

## 12. Quick reference — key generation

| Variable | Command |
|---|---|
| `JWT_ACCESS_SECRET` | `openssl rand -hex 32` |
| `JWT_REFRESH_SECRET` | `openssl rand -hex 32` |
| `TOKEN_ENCRYPTION_KEY` | `openssl rand -hex 32` |
| `REDIS_PASSWORD` | `openssl rand -hex 32` |

Run each command separately and paste the raw output directly into `.env`.
Never reuse the same value across two different variables.

---

## 13. Minimal `.env` for first run

The absolute minimum to boot the server locally with email auth only
(no OAuth providers, using Mailpit for email):

```env
APP_ENV=development
ADDR=:8080
APP_NAME="Store"
ALLOWED_ORIGINS=http://localhost:3000
HTTPS_DISABLED=true

DB_NAME=store_dev
DB_USER=postgres
DB_PASSWORD=password
DB_HOST=localhost
DB_PORT=5432
DB_SSL_MODE=disable
DATABASE_URL=postgres://postgres:password@localhost:5432/store_dev?sslmode=disable
TEST_DATABASE_URL=postgres://postgres:password@localhost:5432/store_dev_test?sslmode=disable

REDIS_PASSWORD=localdevpassword
REDIS_PORT_DOCKER=6380
REDIS_URL=redis://:localdevpassword@localhost:6380/0
TEST_REDIS_URL=redis://:localdevpassword@localhost:6380/1

JWT_ACCESS_SECRET=<openssl rand -hex 32>
JWT_REFRESH_SECRET=<openssl rand -hex 32>
TOKEN_ENCRYPTION_KEY=<openssl rand -hex 32>

SMTP_HOST=localhost
SMTP_PORT=1025
SMTP_USERNAME=test
SMTP_PASSWORD=test
SMTP_FROM=no-reply@store.local

OAUTH_SUCCESS_URL=http://localhost:3000/dashboard
OAUTH_ERROR_URL=http://localhost:3000/login

# Google OAuth is required by config validation — use placeholder values
# if you are not testing OAuth yet. The routes will return errors until
# real credentials are supplied.
GOOGLE_CLIENT_ID=placeholder
GOOGLE_CLIENT_SECRET=placeholder
GOOGLE_REDIRECT_URI=http://localhost:8080/api/v1/oauth/google/callback

COMPOSE_PROJECT_NAME=store
```

Add `TELEGRAM_BOT_TOKEN` when you are ready to test Telegram OAuth.
