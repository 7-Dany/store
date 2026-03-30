# Store Project

`Store` is a full-stack commerce platform split into a Go backend and a Next.js frontend. The codebase is centered on secure account management, role-gated operations, operational visibility, and an evolving Bitcoin/payment and event pipeline.

## High-level architecture

- `backend/` contains the Go API, domain services, platform integrations, SQL schema, metrics, and operational tooling.
- `frontend/` contains the Next.js App Router UI, dashboard surfaces, auth flows, profile/settings pages, telemetry dashboards, and Bitcoin monitoring views.
- PostgreSQL is the primary system of record.
- Redis-backed capabilities are used where low-latency or ephemeral coordination is needed, with some development fallbacks available.
- Prometheus metrics power system-health and security telemetry.

## Core capabilities

### Authentication and account lifecycle

- Email/password registration and login
- Email verification with OTP flow
- Forgot/reset password flow
- Session refresh and logout
- Per-session management and revocation
- Account deletion scheduling and cancellation flows

### OAuth and linked identity support

- Google OAuth sign-in
- Telegram sign-in
- Link and unlink external identities from an existing account
- OAuth audit events and callback handling

### Authorization and RBAC

- RBAC enforcement in the backend
- Owner-role flows and guarded privilege transfer logic
- Approval and escalation-related backend primitives
- Permission-aware routing and admin protection

### Payments and Bitcoin domain

- Bitcoin RPC and ZMQ integration
- Address watch and transaction lifecycle monitoring
- SSE-backed event streaming for Bitcoin activity
- Invoice/payment lifecycle work is in progress across backend design and telemetry surfaces
- Reconciliation, sweep hold, payout-failure, and balance-drift monitoring paths are in progress

### Job queue and background work

- Database-backed job queue with a dedicated `JobStore`
- Atomic claiming with `FOR UPDATE SKIP LOCKED`
- Retry, dead-letter, pause/resume, and purge flows
- Worker heartbeats, stale-worker handling, and stalled-job requeueing
- Schedule management for recurring jobs
- Metrics-oriented stats queries for queue observability

### Operational and platform utilities

- SMTP mailer integration
- Redis/kvstore-backed coordination utilities
- Prometheus-backed telemetry and health summaries
- Audit event taxonomy across auth, OAuth, sessions, RBAC, and Bitcoin flows
- Config-driven feature wiring for optional Bitcoin services

## Backend feature areas

The backend includes these major areas:

- auth
- oauth
- profile and session management
- rbac and owner-role workflows
- mailer integration
- job queue platform layer
- telemetry and monitoring
- Bitcoin RPC, ZMQ, SSE, reconciliation, and related operational services

## Frontend feature areas

The frontend currently exposes these major product surfaces:

- auth pages for login, registration, verification, unlock, and password recovery
- dashboard overview
- profile page
- settings page for profile, password, sessions, and linked accounts
- system health / security dashboard
- transaction lifecycle / Bitcoin dashboard
- internal stage/planning surface

## Utilities and infrastructure the project supports

- PostgreSQL persistence and SQL migrations
- Redis-backed state and coordination
- SMTP email delivery
- Prometheus metric ingestion and health aggregation
- Google and Telegram identity providers
- Cookie-based authenticated frontend proxy routes to the backend API
- Build/test/lint workflows for both backend and frontend stacks

## Suggested reading order

If you are new to the codebase:

1. Start with the backend root README for environment and run instructions.
2. Read the frontend README for UI architecture and local development.
