# OAuth (Google) — Resolved Context

**Section:** INCOMING.md §D-1
**Package:** `internal/domain/oauth/google/` (+ `internal/domain/oauth/shared/`)
**Status:** Stage 0 approved

## Resolved paths
- SQL file: `sql/queries/oauth.sql` (new file; separate from auth.sql per D-15)
- Models (shared): `internal/domain/oauth/shared/models.go`
- Errors (shared): `internal/domain/oauth/shared/errors.go`
- Store (shared): `internal/domain/oauth/shared/store.go`
- Errors (google): `internal/domain/oauth/google/errors.go`
- Models (google): `internal/domain/oauth/google/models.go`
- Service: `internal/domain/oauth/google/service.go`
- Store: `internal/domain/oauth/google/store.go`
- Handler: `internal/domain/oauth/google/handler.go`
- Routes: `internal/domain/oauth/google/routes.go`
- Domain assembler: `internal/domain/oauth/routes.go`
- FakeStorer: `internal/domain/oauth/shared/testutil/fake_storer.go`
- FakeServicer: `internal/domain/oauth/shared/testutil/fake_servicer.go`
- QuerierProxy (oauth): `internal/domain/oauth/shared/testutil/querier_proxy.go`
- Builders: `internal/domain/oauth/shared/testutil/builders.go`

## Key decisions (from Stage 0 §3)
- D-01: New `oauth` first-class domain at `internal/domain/oauth/`; mounted at `/api/v1/oauth`
- D-02: Single `oauth/google/` package for initiate + callback + unlink (one-route-one-folder exception)
- D-03: Link mode encoded in KV state via `link_user_id`; no separate endpoint or redirect URI
- D-04: Google access_token encrypted with `deps.Encryptor` (AES-256-GCM); stored with `enc:` prefix
- D-05: Email-matched existing user → auto-link + session; locked → ErrAccountLocked
- D-06: Access token delivered via 30s MaxAge non-HttpOnly `oauth_access_token` cookie; refresh via normal HttpOnly cookie
- D-07: New config fields `GoogleClientID`, `GoogleClientSecret`, `GoogleRedirectURI`, `OAuthSuccessURL`, `OAuthErrorURL`
- D-08: Google ID token verified via `github.com/coreos/go-oidc/v3/oidc`; no hand-rolling
- D-09: KV state key `goauth:state:<uuid>`, TTL 10 min, JSON `{"code_verifier":"...","link_user_id":"..."}`
- D-10: New Google users: `email_verified=TRUE`, `is_active=TRUE`
- D-11: New Google-only users: `password_hash=NULL`
- D-12: New Google users: `username=NULL`
- D-13: No `AllowContentType("application/json")` anywhere in oauth domain
- D-14: `oauthshared.BaseStore = authshared.BaseStore` alias
- D-15: Separate `sql/queries/oauth.sql`
- D-16: Unlink last-auth-method guard is best-effort (no FOR UPDATE)
- D-17: Audit events `oauth_login`, `oauth_linked`, `oauth_unlinked` — all written with `context.WithoutCancel`
- D-18: `auth_provider` enum value `'google'` already present; no schema migration needed

## New SQL queries (in `sql/queries/oauth.sql`)
GetIdentityByProviderUID
GetIdentityByUserAndProvider
UpsertUserIdentity
DeleteUserIdentity
GetUserAuthMethods
CreateOAuthUser
GetUserByEmailForOAuth
GetUserForOAuthCallback

## New audit events
EventOAuthLogin   = "oauth_login"
EventOAuthLinked  = "oauth_linked"
EventOAuthUnlinked = "oauth_unlinked"

## New sentinel errors

oauthshared/errors.go:
  ErrIdentityNotFound
  ErrProviderAlreadyLinked
  ErrLastAuthMethod
  ErrAccountLocked

google/errors.go:
  ErrTokenExchangeFailed
  ErrInvalidIDToken

## Rate-limit prefixes
goauth:init:ip: → GET /oauth/google (20 req / 5 min per IP)
goauth:cb:ip:   → GET /oauth/google/callback (20 req / 5 min per IP)
goauth:unl:usr: → DELETE /oauth/google/unlink (5 req / 15 min per user)

## Test case IDs (from Stage 0 §9)
- H-layer (initiate): T-01 to T-06
- H-layer (callback pre-service): T-07 to T-11
- S-layer (callback service): T-12 to T-35
- I-layer (callback integration): T-36 to T-38
- S/H/I-layer (unlink): T-39 to T-51
