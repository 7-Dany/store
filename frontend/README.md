# Store Frontend

This is the Next.js frontend for the Store platform. It provides the customer/admin-facing auth flows, account management surfaces, operational dashboards, and Bitcoin transaction monitoring UI that sit in front of the Go backend.

## Stack

- Next.js 16 App Router
- React 19
- TypeScript
- Tailwind CSS 4
- shadcn/ui-style component primitives
- Sonner for toast notifications
- Recharts for telemetry visualizations

## What the frontend includes

### Authentication

- Login
- Registration
- Email verification
- Forgot password
- Password reset
- Unlock flow
- Google OAuth sign-in
- Telegram sign-in

### Dashboard surfaces

- Overview dashboard
- Profile page
- Settings page
- Sessions management
- Linked accounts management
- Security / system health dashboard
- Transaction lifecycle / Bitcoin dashboard

### Backend proxy routes

The frontend includes App Router API routes that proxy authenticated requests to the backend. These routes read the `session` cookie server-side and forward requests to backend services without exposing private backend configuration to the browser.

Examples include:

- `/api/auth/*`
- `/api/oauth/*`
- `/api/profile/*`
- `/api/bitcoin/*`
- `/api/telemetry`

## Project structure

```text
frontend/
  app/                App Router pages, layouts, and API proxy routes
  components/         Shared UI primitives
  features/           Feature-oriented UI modules
  lib/                API clients, utilities, telemetry helpers
  docs/               Frontend-specific documentation
  public/             Static assets
```

Feature folders are organized by domain, including:

- `features/auth`
- `features/dashboard`
- `features/profile`
- `features/settings`
- `features/security`
- `features/bitcoin`
- `features/shared`

## Local development

Install dependencies and start the app:

```bash
npm install
npm run dev
```

The app runs at [http://localhost:3000](http://localhost:3000) by default.

## Available scripts

```bash
npm run dev
npm run build
npm run start
npm run lint
```

## Environment notes

The frontend expects backend/API configuration through environment variables and local `.env` files. The codebase also references provider-specific public variables such as the Telegram bot username for the Telegram sign-in widget.

Keep secrets on the backend or server-only environment variables. Browser code should only use `NEXT_PUBLIC_*` values when they are safe to expose.

## Key implementation notes

- Authenticated server components and API routes rely on the `session` cookie.
- Telemetry data is fetched through the frontend's gated `/api/telemetry` route.
- Bitcoin monitoring UI consumes frontend API routes that proxy backend Bitcoin and event endpoints.
- The dashboard layout includes session-aware behavior and authenticated profile loading.

## Build verification

Production build:

```bash
npm run build
```

This was recently verified successfully against the current codebase.
