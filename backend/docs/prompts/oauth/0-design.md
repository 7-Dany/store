# Google OAuth — Stage 0: Design & Decisions

**Requirement source:** `docs/map/INCOMING.md §D-1`
**Target package:** `internal/domain/oauth/google/`
**Domain root:** `internal/domain/oauth/` — new first-class domain, mounted at `/api/v1/oauth`

**Scope of this document:** Google OAuth only.
Telegram (§D-2) and Linked Identities (§E-1) are separate Stage 0 documents
written after this feature ships.

---

## Read first (no modifications)

| File | What to extract |
|---|---|
| `docs/map/INCOMING.md §D-1` | Original requirement text |
| `docs/RULES.md` | Global conventions, error handling, comment style |
| `docs/rules/auth.md` | Reference implementation — guard ordering, FakeStorer, QuerierProxy patterns |
| `docs/rules/_template.md` | Domain rules template — fill in `docs/rules/oauth.md` after Stage 4 |
| `internal/domain/auth/shared/store.go` | BaseStore, BeginOrBind, IsNoRows helpers |
| `internal/domain/auth/shared/errors.go` | Sentinel errors to check for reuse |
| `internal/app/deps.go` | Current Deps shape — new fields needed |
| `internal/config/config.go` | Current Config shape — new fields needed |
| `internal/server/routes.go` | Where to mount `/api/v1/oauth` |
| `internal/audit/audit.go` | Existing event names — confirm no collision |
| `sql/schema/001_core.sql` | `user_identities` table, `auth_provider` enum, `users` columns |
| `sql/queries/auth.sql` | Existing query style to match |
| `internal/platform/crypto/` | Encryptor — for `access_token` encryption |
| `internal/platform/kvstore/store.go` | KV store interface — `Get`, `Set`, `Del` |
| `internal/platform/token/` | `MintTokens`, `UserIDFromContext` |

---

## 1. Feature summary

Google OAuth lets users sign in or register using their Google account (OIDC +
PKCE). The flow has three endpoints: an initiate step that builds the
authorization URL and stores PKCE state in the KV store, a callback that
exchanges the code, verifies the ID token, and issues a session, and an unlink
step that removes the Google identity from an authenticated account.

The link intent (connecting Google to an already-authenticated account) is
encoded in the same initiate/callback flow via an optional `link_user_id` field
stored in the KV state entry, avoiding the need for a separate link endpoint or
a separate redirect URI registered with Google.

OAuth lives in its own first-class domain (`internal/domain/oauth/`) separate
from the auth domain. This keeps the auth domain focused on credential
management and gives OAuth a dedicated shared layer (`oauthshared`) for
provider helpers (PKCE, OIDC token verification, access-token encryption).

---

## 2. HTTP contracts

### 2.1 GET /api/v1/oauth/google — Initiate

**Auth:** None required. Optional `Authorization: Bearer <access_token>`.
If present and valid, the callback will link instead of login.

**Request body:** None.

**Success response:** `302 Found` — redirect to Google authorization endpoint.

**Error responses:**

| Status | Code | Condition |
|---|---|---|
| 500 | `internal_error` | KV write failed; state could not be stored |

---

### 2.2 GET /api/v1/oauth/google/callback — Callback

**Auth:** None. The `state` query param is the CSRF credential.

**Query params:** `code` (string), `state` (string), optionally `error` (string
from Google on user-cancel or provider error).

**Response:** `302 Found` — always a redirect, never a JSON body. Success
redirects to `OAuthSuccessURL?provider=google`. Failure redirects to
`OAuthErrorURL?error=<code>`.

**Error redirect codes (opaque — never expose raw Google messages):**

| `?error=` value | Condition |
|---|---|
| `oauth_cancelled` | User cancelled or Google returned an `error` query param |
| `invalid_state` | `state` missing from KV (expired, CSRF attempt, or missing param) |
| `token_exchange_failed` | Code exchange with Google API failed |
| `invalid_id_token` | ID token signature, `aud`, or `exp` invalid |
| `provider_already_linked` | Google UID already linked to a different account (link mode only) |
| `account_locked` | Matched user is `is_locked` or `admin_locked` |
| `server_error` | Unexpected store, encryption, or infrastructure error |

---

### 2.3 DELETE /api/v1/oauth/google/unlink — Unlink

**Auth:** Valid JWT required.

**Request body:** None.

**Success response:** `200 OK`
```json
{ "message": "google account unlinked successfully" }
```

**Error responses:**

| Status | Code | Condition |
|---|---|---|
| 401 | `unauthorized` | Missing or invalid JWT |
| 404 | `not_found` | No Google identity linked to this user |
| 422 | `last_auth_method` | Removing Google would leave the user with no auth method |
| 500 | `internal_error` | Unexpected store or infrastructure error |

---

## 3. Decisions

| # | Question | Decision | Rationale |
|---|---|---|---|
| D-01 | Auth domain or own domain? | New `oauth` domain at `internal/domain/oauth/`, mounted at `/api/v1/oauth`. | Auth handles credentials. OAuth needs its own shared layer for PKCE helpers, OIDC verification, and access-token encryption. Mixing them couples auth to Google SDKs. |
| D-02 | One package for all three endpoints or separate packages? | Single `internal/domain/oauth/google/` package for all three endpoints. Permitted by the one-route-one-folder exception: all three operate on the same `user_identities` row for the `google` provider. | Splitting initiate/callback/unlink across three packages would require them to share types via the shared package, which adds indirection with no benefit. The exception exists for this exact pattern. |
| D-03 | How does link mode work without a separate `/link` endpoint? | `GET /oauth/google` reads an optional Bearer token best-effort. If valid, the extracted `userID` is stored as `link_user_id` in the KV state entry. The callback reads it from the KV state and switches to link mode. No additional endpoint or registered redirect URI required. | One redirect URI registered with Google. PKCE protection applies equally to login and link. |
| D-04 | How is Google's `access_token` stored? | Encrypted with `deps.Encryptor` (AES-256-GCM), stored with `enc:` prefix. The raw token is never written to the DB. | `chk_ui_access_token_encrypted` in `001_core.sql` enforces this at the schema level. `deps.Encryptor` is already wired from `TOKEN_ENCRYPTION_KEY`. |
| D-05 | What happens when the Google callback email matches an existing user with no Google identity? | Link the Google identity to the existing user and issue a session (same as a normal login). Guard: if the email-matched user is locked, redirect `account_locked`. | Users expect "Sign in with Google" to work with their existing account. Requiring an explicit link step creates unnecessary friction. |
| D-06 | How is the access token delivered to the frontend after the callback redirect? | Set a short-lived (MaxAge=30 seconds), non-HttpOnly cookie named `oauth_access_token` with `SameSite=Strict` and `Secure` matching `deps.SecureCookies`. The frontend reads it on the landing page and discards it. The refresh token uses the normal HttpOnly `refresh_token` cookie. | Redirects cannot carry a JSON body. URL fragments do not survive cross-origin navigation reliably. A 30-second MaxAge readable cookie is the safest cross-browser delivery mechanism. |
| D-07 | Where are Google credentials and redirect URLs stored? | New config fields `GoogleClientID`, `GoogleClientSecret`, `GoogleRedirectURI`, `OAuthSuccessURL`, `OAuthErrorURL`. Env vars: `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URI`, `OAUTH_SUCCESS_URL`, `OAUTH_ERROR_URL`. Required as a group when `GOOGLE_CLIENT_ID` is set. | Follows the pattern used for JWT secrets and SMTP. All URL fields validated as absolute URLs. |
| D-08 | How is the Google ID token verified? | Use `github.com/coreos/go-oidc/v3/oidc` provider. Do not hand-roll JWT verification. The library fetches and caches Google's JWKS, handles `kid` rotation, and enforces `aud`, `exp`, and `iss`. | Hand-rolling OIDC token verification is a high-risk area. The library handles RS256 pinning, key caching, and audience checks correctly. |
| D-09 | What KV schema is used for the PKCE state? | Key: `goauth:state:<state>` (UUID v4). TTL: 10 minutes. Value: JSON `{ "code_verifier": "...", "link_user_id": "" }`. `link_user_id` is empty string when not in link mode. | Short-lived and scoped. Single-use: the entry is deleted immediately after retrieval in the callback. |
| D-10 | Does `email_verified` need to be TRUE for new Google registrations? | Yes. `email_verified=TRUE` and `is_active=TRUE` are set in `OAuthRegisterTx`. | Google only returns verified email addresses in the ID token. The user has already completed verification with Google. |
| D-11 | What `password_hash` is set for new Google-only users? | NULL. `trg_require_auth_method` is satisfied by the `user_identities` row inserted in the same transaction. | OAuth-only users are already supported by the schema. |
| D-12 | New users from Google: what username is set? | NULL. Username is optional and can be set later via `PATCH /api/v1/profile/me`. | Not all Google accounts have a natural username. Forcing one at registration adds friction. |
| D-13 | Does the oauth domain apply `AllowContentType("application/json")` globally? | No. The root oauth assembler does NOT apply it globally. The google package registers no JSON-body endpoints, so it does not apply it at all. | Applying it globally would break the browser redirect flow for initiate and callback. |
| D-14 | Does the oauth domain share auth's `BaseStore`? | Yes — `oauthshared.BaseStore = authshared.BaseStore` alias (same pattern as profile domain). | Avoids premature duplication. Documented in `shared/store.go`. |
| D-15 | Is a separate SQL file used? | Yes — `sql/queries/oauth.sql`. | Keeps `auth.sql` focused on email/password flows. One SQL file per domain. |
| D-16 | Unlink: is the last-auth-method guard transactional? | No. Best-effort check (two store reads + delete). A simultaneous race from two devices in the same millisecond window is not a meaningful attack surface; recovery is available via forgot-password. | `FOR UPDATE` on the user row adds latency and deadlock risk for no security benefit. |
| D-17 | What audit events does this feature emit? | `oauth_login` (login or register), `oauth_linked` (link mode). `oauth_unlinked` is emitted by unlink. All three written with `context.WithoutCancel`. | OAuth events are security events; they belong in `auth_audit_log` alongside `login` and `logout`. |
| D-18 | Which `auth_provider` value is used? | `'google'` — already present in the `auth_provider` enum in `001_core.sql`. | No schema migration required. |

---

## 4. Data model

### 4.1 New SQL file — `sql/queries/oauth.sql`

| Query name | Type | Purpose |
|---|---|---|
| `GetIdentityByProviderUID` | `:one` | Look up `user_identities` by `(provider, provider_uid)`; returns `id, user_id, provider_email, display_name, avatar_url, access_token` |
| `GetIdentityByUserAndProvider` | `:one` | Look up `user_identities` by `(user_id, provider)`; used for unlink guard and link-mode duplicate check |
| `UpsertUserIdentity` | `:one` | INSERT ON CONFLICT `uq_identity_user_provider` DO UPDATE; updates `provider_email`, `display_name`, `avatar_url`, `access_token`, `updated_at`; returns the row |
| `DeleteUserIdentity` | `:execrows` | DELETE WHERE `user_id = $1 AND provider = $2`; returns rows affected |
| `GetUserAuthMethods` | `:one` | Returns `(password_hash IS NOT NULL) AS has_password, COUNT(ui.id) AS identity_count` from `users LEFT JOIN user_identities`; for last-auth-method guard |
| `CreateOAuthUser` | `:one` | INSERT into `users` with `email_verified=TRUE, is_active=TRUE, password_hash=NULL, email=$1 (nullable), display_name=$2 (nullable)`; returns `id` |
| `GetUserByEmailForOAuth` | `:one` | Fetch `id, is_active, is_locked, admin_locked` by `email WHERE deleted_at IS NULL`; for email-match path in callback |
| `GetUserForOAuthCallback` | `:one` | Fetch `id, is_active, is_locked, admin_locked` by `id WHERE deleted_at IS NULL`; for lock guard in link mode |

### 4.2 Schema changes

**None.** `user_identities`, `auth_audit_log`, `auth_provider` enum (with
`'google'`), and `users.deleted_at` are all present in `sql/schema/001_core.sql`.

### 4.3 New audit events — `internal/audit/audit.go`

| Constant | Value string | When emitted | Metadata |
|---|---|---|---|
| `EventOAuthLogin` | `"oauth_login"` | Successful login or new user registration via Google | `{ "provider": "google", "new_user": true\|false }` |
| `EventOAuthLinked` | `"oauth_linked"` | Google identity linked to an existing account (link mode) | `{ "provider": "google" }` |
| `EventOAuthUnlinked` | `"oauth_unlinked"` | Google identity removed from an account | `{ "provider": "google" }` |

All three constants must also appear in `AllEvents()` (RULES.md §3.14 S-1).

---

## 5. New config and deps fields

### 5.1 config.Config additions

```go
// ── OAuth — Google ────────────────────────────────────────────
// GoogleClientID is the OAuth 2.0 client ID from Google Cloud Console.
// Required when GOOGLE_CLIENT_ID env var is set; validated as a group with
// GoogleClientSecret and GoogleRedirectURI.
GoogleClientID string
// GoogleClientSecret is the OAuth 2.0 client secret. Required with GoogleClientID.
GoogleClientSecret string
// GoogleRedirectURI is the callback URL registered in Google Cloud Console.
// Must be an exact match. Required with GoogleClientID.
GoogleRedirectURI string

// ── OAuth — Frontend redirects ────────────────────────────────
// OAuthSuccessURL is the frontend base URL for successful OAuth flows.
// The callback appends ?provider=google (and ?action=linked for link mode).
// Required when any OAuth provider is enabled.
OAuthSuccessURL string
// OAuthErrorURL is the frontend base URL for failed OAuth flows.
// The callback appends ?error=<opaque_code>.
// Required when any OAuth provider is enabled.
OAuthErrorURL string
```

### 5.2 app.Deps additions

```go
// ── OAuth ─────────────────────────────────────────────────────
GoogleClientID     string
GoogleClientSecret string
GoogleRedirectURI  string
OAuthSuccessURL    string
OAuthErrorURL      string
```

### 5.3 Validation rules

- `GoogleClientID`, `GoogleClientSecret`, `GoogleRedirectURI` are required as a
  group when `GOOGLE_CLIENT_ID` is non-empty. Missing any one of the three
  while another is set is a startup error.
- `OAuthSuccessURL` and `OAuthErrorURL` are required when any OAuth provider is
  enabled.
- All URL fields must be absolute URLs. In production (`APP_ENV=production`),
  the scheme must be `https`.

---

## 6. Package layout

```
internal/domain/oauth/
├── routes.go                  # package oauth — root assembler; mounts /google
├── shared/                    # package oauthshared
│   ├── errors.go              # ErrIdentityNotFound, ErrProviderAlreadyLinked, ErrLastAuthMethod
│   ├── models.go              # LinkedIdentity, LoggedInSession (shared with future packages)
│   ├── store.go               # BaseStore = authshared.BaseStore alias
│   └── testutil/              # package oauthsharedtest
│       ├── fake_storer.go
│       ├── fake_servicer.go
│       ├── querier_proxy.go
│       └── builders.go
└── google/                    # package google
    ├── errors.go
    ├── models.go              # InitiateResult, CallbackResult, GoogleClaims
    ├── service.go             # Storer interface + Service struct
    ├── store.go               # Store struct + methods
    ├── handler.go             # Handler struct + Initiate, Callback, Unlink
    ├── routes.go              # rate limiters, route registrations
    ├── service_test.go
    ├── handler_test.go
    └── store_test.go
```

---

## 7. Guard ordering

### 7.1 GET /api/v1/oauth/google — Initiate

Handler-only logic (no service method needed — pure state generation + redirect):

```
1. Parse Authorization header — best-effort; error is silently ignored.
   If a valid access token is present: extract userID → link_user_id = userID.UUID string.
   If absent or invalid: link_user_id = "".
2. state = uuid.New().String().
3. code_verifier = base64url(32 random bytes), no padding.
4. code_challenge = base64url(sha256(code_verifier)), no padding.
5. KV set: key=goauth:state:<state>, TTL=10min,
   value=JSON{ "code_verifier": "...", "link_user_id": "..." }.
   Failure → 500 internal_error.
6. Build Google authorization URL:
   https://accounts.google.com/o/oauth2/v2/auth?
     client_id=<GoogleClientID>
     &redirect_uri=<GoogleRedirectURI>
     &response_type=code
     &scope=openid%20email%20profile
     &state=<state>
     &code_challenge=<code_challenge>
     &code_challenge_method=S256
7. http.Redirect(w, r, url, http.StatusFound).
```

### 7.2 GET /api/v1/oauth/google/callback — Callback

Handler + service. The handler does all redirects; the service returns a result
or a typed error that the handler converts to a redirect.

**Handler pre-service:**
```
1. If r.URL.Query().Get("error") != "" → redirect OAuthErrorURL?error=oauth_cancelled. Stop.
2. state = r.URL.Query().Get("state") — empty → redirect invalid_state. Stop.
3. KV get: key=goauth:state:<state>.
   Not found (expired or CSRF) → redirect invalid_state. Stop.
4. Unmarshal KV value → code_verifier string, link_user_id string.
5. KV del: key=goauth:state:<state>. Failure is non-fatal (log only; entry expires anyway).
6. code = r.URL.Query().Get("code") — empty → redirect invalid_state. Stop.
7. Call svc.HandleCallback(ctx, CallbackInput{
     Code: code, CodeVerifier: code_verifier,
     LinkUserID: link_user_id,
     IP: clientIP, UA: r.UserAgent(),
   }).
```

**Error → redirect mapping in handler:**
```
ErrOAuthCancelled       → oauth_cancelled
ErrInvalidState         → invalid_state     (not reachable here — handler guards first)
ErrTokenExchangeFailed  → token_exchange_failed
ErrInvalidIDToken       → invalid_id_token
ErrProviderAlreadyLinked→ provider_already_linked
ErrAccountLocked        → account_locked
any other error         → server_error
```

**Service HandleCallback guard ordering:**
```
1. ExchangeCode(code, code_verifier) → googleTokens.
   Failure → ErrTokenExchangeFailed.
2. VerifyIDToken(googleTokens.IDToken) → GoogleClaims{Sub, Email, Name, Picture}.
   Failure → ErrInvalidIDToken.
3. Encrypt(googleTokens.AccessToken) → encryptedToken ("enc:" + base64(ciphertext)).
   Failure → return wrapped internal error.

── LINK MODE (input.LinkUserID != "") ──────────────────────────────────────────
4. GetUserForOAuthCallback(linkUserID) — not found or deleted_at IS NOT NULL → wrapped not-found error.
   Guard: is_locked || admin_locked → ErrAccountLocked.
5. GetIdentityByProviderUID("google", claims.Sub):
   FOUND and row.UserID != linkUserID → ErrProviderAlreadyLinked.
   (FOUND and row.UserID == linkUserID → fall through; upsert is idempotent.)
6. UpsertUserIdentity(linkUserID, "google", claims.Sub, claims.Email,
                      claims.Name, claims.Picture, encryptedToken).
7. InsertAuditLog(ctx.WithoutCancel, EventOAuthLinked, provider=google,
                  metadata={provider:google}).
8. Return CallbackResult{Linked: true}.

── LOGIN/REGISTER MODE (input.LinkUserID == "") ────────────────────────────────
4. GetIdentityByProviderUID("google", claims.Sub):
   FOUND:
     a. GetUserForOAuthCallback(row.UserID) —
        guard: is_locked || admin_locked → ErrAccountLocked.
     b. UpsertUserIdentity (refresh display_name, avatar_url, access_token).
     c. OAuthLoginTx(userID, "google", ip, ua) → LoggedInSession.
     d. newUser = false.
   NOT FOUND:
     GetUserByEmailForOAuth(claims.Email):
       FOUND existing user by email:
         a. Guard: is_locked || admin_locked → ErrAccountLocked.
         b. UpsertUserIdentity(existingUserID, "google", claims.Sub, ...).
         c. OAuthLoginTx(existingUserID, "google", ip, ua) → LoggedInSession.
         d. newUser = false.
       NOT FOUND (brand-new user):
         a. OAuthRegisterTx(claims.Email, claims.Name, "google", claims.Sub,
                            claims.Email, claims.Name, claims.Picture,
                            encryptedToken, ip, ua) → LoggedInSession.
         b. newUser = true.
5. InsertAuditLog(ctx.WithoutCancel, EventOAuthLogin, provider=google,
                  metadata={provider:google, new_user:newUser}).
6. Return CallbackResult{Session: loggedInSession, NewUser: newUser}.
```

**Handler post-service (login/register mode):**
```
7. MintTokens(loggedInSession.UserID, loggedInSession.SessionID,
              loggedInSession.RefreshJTI, loggedInSession.RefreshExpiresAt)
   → accessToken string, refreshToken string.
8. Set refresh_token HttpOnly cookie:
   Path=/api/v1/auth, SameSite=Strict, Secure=deps.SecureCookies, HttpOnly=true.
9. Set oauth_access_token readable cookie:
   MaxAge=30, SameSite=Strict, Secure=deps.SecureCookies, HttpOnly=false.
10. Redirect OAuthSuccessURL?provider=google.
```

**Handler post-service (link mode):**
```
7. Redirect OAuthSuccessURL?provider=google&action=linked.
   No session minted.
```

### 7.3 DELETE /api/v1/oauth/google/unlink — Unlink

```
1. userID = token.UserIDFromContext(r.Context()).
2. svc.UnlinkGoogle(ctx, userID):
   a. GetUserAuthMethods(userID) → has_password bool, identity_count int.
   b. GetIdentityByUserAndProvider(userID, "google"):
      NOT FOUND → ErrIdentityNotFound.
   c. Guard: (has_password ? 1 : 0) + identity_count <= 1 → ErrLastAuthMethod.
   d. DeleteUserIdentity(userID, "google") → rowsAffected.
      rowsAffected == 0 → ErrIdentityNotFound (lost race).
   e. InsertAuditLog(context.WithoutCancel, EventOAuthUnlinked, provider=google,
                     metadata={provider:google}).
3. Handler error switch:
   ErrIdentityNotFound → 404 not_found
   ErrLastAuthMethod   → 422 last_auth_method
   other               → 500 internal_error
4. 200 OK { "message": "google account unlinked successfully" }.
```

---

## 8. Rate limiting

| Endpoint | Limit | KV prefix | Rationale |
|---|---|---|---|
| `GET /oauth/google` | 20 req / 5 min per IP | `goauth:init:ip:` | Prevents state KV flooding; each call writes a KV entry |
| `GET /oauth/google/callback` | 20 req / 5 min per IP | `goauth:cb:ip:` | Public endpoint; limits Google token-exchange API consumption |
| `DELETE /oauth/google/unlink` | 5 req / 15 min per user | `goauth:unl:usr:` | Mutation; user-scoped to allow unlinking from multiple devices |

No prefix in this table appears in `E2E_CHECKLIST.md`.

---

## 9. Test case inventory

**Legend:** S = service unit test, H = handler unit test, I = store integration test

### 9.1 GET /oauth/google — Initiate

| # | Case | Layer | Setup | Expected |
|---|---|---|---|---|
| T-01 | Happy path — no auth header | H | No Authorization header; `KVSetFn` returns nil | 302; Location contains `accounts.google.com`; `link_user_id=""` in stored value |
| T-02 | Happy path — valid Bearer stores link_user_id | H | Valid JWT; capture `KVSetFn` argument | 302; stored value's `link_user_id` == parsed userID |
| T-03 | Invalid/expired Bearer treated as unauthenticated | H | Malformed JWT in Authorization | 302; `link_user_id=""` in stored value |
| T-04 | KV set failure → 500 | H | `KVSetFn` returns error | 500 `internal_error` |
| T-05 | Two requests produce distinct state values | H | Two sequential calls; capture both `KVSetFn` keys | key1 != key2 |
| T-06 | Redirect URL contains PKCE params | H | Happy path; parse Location header | contains `code_challenge`, `code_challenge_method=S256`, `state` |

### 9.2 GET /oauth/google/callback — Callback (handler pre-service guards)

| # | Case | Layer | Setup | Expected |
|---|---|---|---|---|
| T-07 | Google `error` param present → redirect oauth_cancelled | H | `?error=access_denied` | Redirect `OAuthErrorURL?error=oauth_cancelled` |
| T-08 | Missing `state` param → redirect invalid_state | H | No state in URL | Redirect `invalid_state` |
| T-09 | State not found in KV → redirect invalid_state | H | `KVGetFn` returns not-found | Redirect `invalid_state` |
| T-10 | Missing `code` param (after valid state) → redirect invalid_state | H | No code; state found | Redirect `invalid_state` |
| T-11 | KV del failure is non-fatal (log, continue) | H | `KVDelFn` returns error; `ExchangeCodeFn` returns success | Flow continues; no redirect to error page |

### 9.3 GET /api/v1/oauth/google/callback — Callback (service)

| # | Case | Layer | Setup | Expected |
|---|---|---|---|---|
| T-12 | Code exchange failure → ErrTokenExchangeFailed | S | `ExchangeCodeFn` returns error | `errors.Is(err, ErrTokenExchangeFailed)` |
| T-13 | Handler maps ErrTokenExchangeFailed → redirect token_exchange_failed | H | Svc returns ErrTokenExchangeFailed | Redirect `token_exchange_failed` |
| T-14 | ID token verification failure → ErrInvalidIDToken | S | `VerifyIDTokenFn` returns error | `errors.Is(err, ErrInvalidIDToken)` |
| T-15 | Handler maps ErrInvalidIDToken → redirect invalid_id_token | H | Svc returns ErrInvalidIDToken | Redirect `invalid_id_token` |
| T-16 | Encryption failure → internal error | S | `EncryptFn` returns error | returned error is non-nil, not a typed sentinel |
| T-17 | Handler maps internal error → redirect server_error | H | Svc returns unexpected error | Redirect `server_error` |
| T-18 | Login mode — existing identity, active user → session issued | S | `GetIdentityByProviderUIDFn` returns hit; user not locked | `OAuthLoginTxFn` called; `CallbackResult.NewUser == false` |
| T-19 | Login mode — existing identity, user is_locked → ErrAccountLocked | S | Identity found; `GetUserForOAuthCallbackFn` returns is_locked=true | `errors.Is(err, ErrAccountLocked)` |
| T-20 | Login mode — existing identity, user admin_locked → ErrAccountLocked | S | admin_locked=true | same |
| T-21 | Login mode — no identity, email match, active user → links and issues session | S | No identity; `GetUserByEmailForOAuthFn` returns hit | `UpsertUserIdentityFn` called; `OAuthLoginTxFn` called on existing userID |
| T-22 | Login mode — no identity, email match, user locked → ErrAccountLocked | S | Email-matched user is_locked | `errors.Is(err, ErrAccountLocked)` |
| T-23 | Login mode — no identity, no email match → new user registered | S | No identity; no email match | `OAuthRegisterTxFn` called; `CallbackResult.NewUser == true` |
| T-24 | Handler maps ErrAccountLocked → redirect account_locked | H | Svc returns ErrAccountLocked | Redirect `account_locked` |
| T-25 | Link mode — happy path | S | `link_user_id` set; no conflicting identity | `UpsertUserIdentityFn` called; `InsertAuditLogFn` called with `EventOAuthLinked`; `CallbackResult.Linked == true` |
| T-26 | Link mode — provider already linked to different user → ErrProviderAlreadyLinked | S | `GetIdentityByProviderUIDFn` returns different userID | `errors.Is(err, ErrProviderAlreadyLinked)` |
| T-27 | Handler maps ErrProviderAlreadyLinked → redirect provider_already_linked | H | Svc returns ErrProviderAlreadyLinked | Redirect `provider_already_linked` |
| T-28 | Link mode — target user is_locked → ErrAccountLocked | S | `GetUserForOAuthCallbackFn` returns is_locked=true | `errors.Is(err, ErrAccountLocked)` |
| T-29 | access_token stored with `enc:` prefix | S | Capture `UpsertUserIdentityFn` arg | arg `access_token` field starts with `"enc:"` |
| T-30 | Handler sets oauth_access_token cookie (MaxAge=30, non-HttpOnly) | H | Login-mode happy path | Cookie present; MaxAge=30; HttpOnly=false; SameSite=Strict |
| T-31 | Handler sets refresh_token cookie (HttpOnly, Path=/api/v1/auth) | H | Login-mode happy path | Cookie present; HttpOnly=true; Path=/api/v1/auth; SameSite=Strict |
| T-32 | Login mode — no session cookie set in link mode | H | Link-mode happy path | No `refresh_token` cookie; no `oauth_access_token` cookie |
| T-33 | Audit written with context.WithoutCancel — login mode | S | Capture ctx in `InsertAuditLogFn` | `ctx.Done() == nil` |
| T-34 | Audit written with context.WithoutCancel — link mode | S | Same capture | `ctx.Done() == nil` |
| T-35 | OAuthRegisterTx error wraps correctly | S | `OAuthRegisterTxFn` returns raw error | `err.Error()` contains `"google.HandleCallback:"` |
| T-36 | Integration — new Google user has email_verified=TRUE, is_active=TRUE, password_hash=NULL | I | Full register path in DB | DB row matches |
| T-37 | Integration — existing identity login updates display_name/avatar_url | I | Seed identity + call with new name | DB `user_identities.display_name` updated |
| T-38 | Integration — email-match path links Google identity to existing user | I | Seed user with matching email; no identity | New `user_identities` row linked to existing user |

### 9.4 DELETE /api/v1/oauth/google/unlink — Unlink

| # | Case | Layer | Setup | Expected |
|---|---|---|---|---|
| T-39 | Happy path — identity exists, multiple auth methods | S, H, I | identity_count=2; has_password=false | `DeleteUserIdentityFn` called; 200 OK |
| T-40 | Identity not found → ErrIdentityNotFound | S | `GetIdentityByUserAndProviderFn` returns not-found | `errors.Is(err, ErrIdentityNotFound)` |
| T-41 | Handler maps ErrIdentityNotFound → 404 | H | Svc returns ErrIdentityNotFound | 404 `not_found` |
| T-42 | Last auth method (no password, 1 identity) → ErrLastAuthMethod | S | has_password=false, identity_count=1 | `errors.Is(err, ErrLastAuthMethod)` |
| T-43 | Handler maps ErrLastAuthMethod → 422 | H | Svc returns ErrLastAuthMethod | 422 `last_auth_method` |
| T-44 | Has password + 1 identity → can unlink | S | has_password=true, identity_count=1 | `DeleteUserIdentityFn` called; no error |
| T-45 | No password + 2 identities → can unlink | S | has_password=false, identity_count=2 | `DeleteUserIdentityFn` called; no error |
| T-46 | Delete returns 0 rows (lost race) → ErrIdentityNotFound | S | `DeleteUserIdentityFn` returns rowsAffected=0 | `errors.Is(err, ErrIdentityNotFound)` |
| T-47 | Missing JWT → 401 | H | No Authorization header | 401 `unauthorized` |
| T-48 | Audit written with context.WithoutCancel | S | Capture ctx in `InsertAuditLogFn` | `ctx.Done() == nil` |
| T-49 | Store error wraps correctly | S | `DeleteUserIdentityFn` returns raw error | `err.Error()` contains `"google.UnlinkGoogle:"` |
| T-50 | Integration — GetUserAuthMethods returns correct counts | I | Seed user with password=true + 1 Google identity | has_password=true, identity_count=1 |
| T-51 | Integration — identity row deleted from DB | I | Seed identity; call unlink | `user_identities` row gone from DB |

---

## 10. Open questions

None. All questions resolved in §3.

---

## 11. Files to create / modify

### New files

```
internal/domain/oauth/routes.go
internal/domain/oauth/shared/errors.go
internal/domain/oauth/shared/models.go
internal/domain/oauth/shared/store.go
internal/domain/oauth/shared/testutil/fake_storer.go
internal/domain/oauth/shared/testutil/fake_servicer.go
internal/domain/oauth/shared/testutil/querier_proxy.go
internal/domain/oauth/shared/testutil/builders.go
internal/domain/oauth/google/errors.go
internal/domain/oauth/google/models.go
internal/domain/oauth/google/service.go
internal/domain/oauth/google/store.go
internal/domain/oauth/google/handler.go
internal/domain/oauth/google/routes.go
internal/domain/oauth/google/service_test.go
internal/domain/oauth/google/handler_test.go
internal/domain/oauth/google/store_test.go
sql/queries/oauth.sql
docs/rules/oauth.md                      — fill in after Stage 4 using _template.md
```

### Modified files

```
internal/config/config.go         — add §5.1 fields
internal/app/deps.go              — add §5.2 fields
internal/server/routes.go         — add r.Mount("/oauth", oauth.Routes(ctx, deps))
internal/audit/audit.go           — add EventOAuthLogin, EventOAuthLinked, EventOAuthUnlinked
docs/map/INCOMING.md              — mark §D-1 as [~] in progress
docs/skills/backend-route-impl/references/route-map.md
                                  — update §D-1 package path to internal/domain/oauth/google/
```

---

## 12. Approval checklist

- [x] HTTP contract (§2) matches the requirement — three endpoints, correct methods and paths
- [x] Every decision in §3 has a rationale
- [x] New config/deps fields documented in §5 with env var names and validation rules
- [x] Guard ordering (§7) is complete for all three endpoints — no guard missing
- [x] Test case inventory (§9) covers every path in §7 and every error in §2
- [x] No open questions in §10
- [x] Rate-limit prefixes in §8 do not collide with any existing prefix
- [x] Package layout follows the one-route-one-folder exception (all three endpoints share the same `user_identities` resource)
- [x] `AllowContentType` decision documented (D-13) — NOT applied globally or per-route (no JSON body endpoints)
- [x] `context.WithoutCancel` called out for all three audit writes (T-33, T-34, T-48)
- [x] `enc:` prefix enforced before any DB write (D-04, T-29)
- [x] No schema migration required (D-18)
- [x] New SQL file `sql/queries/oauth.sql` (D-15)
- [x] Audit events in `AllEvents()` called out (§4.3)
- [x] Telegram and Identities explicitly deferred — not in scope

**Stage 0 approved. Stage 1 may begin.**
