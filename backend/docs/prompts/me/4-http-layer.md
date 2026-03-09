# §E-1 — Linked Accounts — Stage 4: HTTP Layer

**Requirement source:** `docs/map/INCOMING.md §E-1`
**Target package:** `internal/domain/profile/me/`
**Depends on:** Stage 3 complete — `service.GetUserIdentities` implemented and S-layer tests green.

---

## Read first (no modifications)

| File | What to extract |
|---|---|
| `docs/prompts/me/context.md` | Rate-limit prefix (`ident:ip:`), test case IDs |
| `internal/domain/profile/me/handler.go` | Existing handler layout, `mustUserID` helper |
| `internal/domain/profile/me/routes.go` | Existing limiter/route pattern — follow exactly |
| `internal/domain/profile/me/requests.go` | `identityItem` + `identitiesResponse` types |
| `internal/domain/auth/shared/testutil/fake_servicer.go` | `MeFakeServicer.GetUserIdentitiesFn` |

---

## Changes by file

### 1. `internal/domain/profile/me/handler.go` — add Identities handler

Add after the `UpdateProfile` handler, before the private helpers section:

```go
// Identities handles GET /me/identities.
// Returns all linked OAuth identities for the authenticated user.
// access_token and refresh_token_provider are never present in the response —
// they are excluded at the SQL and service layers.
func (h *Handler) Identities(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.mustUserID(w, r)
	if !ok {
		return
	}

	identities, err := h.svc.GetUserIdentities(r.Context(), userID)
	if err != nil {
		slog.ErrorContext(r.Context(), "profile.Identities: service error", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	items := make([]identityItem, 0, len(identities))
	for _, id := range identities {
		items = append(items, identityItem{
			Provider:      id.Provider,
			ProviderEmail: id.ProviderEmail,
			DisplayName:   id.DisplayName,
			AvatarURL:     id.AvatarURL,
			CreatedAt:     id.CreatedAt,
		})
	}

	respond.JSON(w, http.StatusOK, identitiesResponse{Identities: items})
}
```

**Notes:**
- `make([]identityItem, 0, len(identities))` — ensures `Identities` in the JSON
  response is `[]` (not `null`) even when the service returns an empty slice, because
  `respond.JSON` serialises a non-nil empty slice as `[]`.
- No `errors.Is` switch — the only non-nil error from the service maps to 500
  (there are no domain sentinels for this read-only endpoint).
- No `http.MaxBytesReader` — GET endpoint, no request body.

---

### 2. `internal/domain/profile/me/routes.go` — register the route

Add a new rate limiter and route registration.

```go
// In Routes(), after the existing updateProfileLimiter setup:

// 20 req / 1 min per IP — prevents bulk enumeration of identity data.
// rate = 20 / (1 * 60) ≈ 0.333 tokens/sec.
identitiesLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "ident:ip:", 20.0/(1*60), 20, 1*time.Minute)
go identitiesLimiter.StartCleanup(ctx)
```

And inside the `r.Group` that applies `deps.JWTAuth`:

```go
r.With(identitiesLimiter.Limit).Get("/me/identities", h.Identities)
```

Full `Routes` function after changes:

```go
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	meLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "pme:ip:", 10.0/(1*60), 10, 1*time.Minute)
	go meLimiter.StartCleanup(ctx)

	updateProfileLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "prof:ip:", 10.0/(1*60), 10, 1*time.Minute)
	go updateProfileLimiter.StartCleanup(ctx)

	// 20 req / 1 min per IP — prevents bulk enumeration of identity data.
	identitiesLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "ident:ip:", 20.0/(1*60), 20, 1*time.Minute)
	go identitiesLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc)

	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.With(meLimiter.Limit).Get("/me", h.Me)
		r.With(updateProfileLimiter.Limit).Patch("/me", h.UpdateProfile)
		r.With(identitiesLimiter.Limit).Get("/me/identities", h.Identities)
	})
}
```

---

## Handler unit tests (H-layer)

Add to `handler_test.go` (or `identities_handler_test.go` in the same package):

### T-07 — Missing auth → 401

```go
func TestHandler_Identities_NoAuth(t *testing.T) {
    h := me.NewHandler(&authsharedtest.MeFakeServicer{})
    req := httptest.NewRequest(http.MethodGet, "/me/identities", nil)
    // No JWT context value — mustUserID returns false
    w := httptest.NewRecorder()
    h.Identities(w, req)
    assert.Equal(t, http.StatusUnauthorized, w.Code)
}
```

### T-04 / T-05 — access_token and refresh_token_provider never in response

```go
func TestHandler_Identities_SensitiveFieldsAbsent(t *testing.T) {
    email := "alice@gmail.com"
    fake := &authsharedtest.MeFakeServicer{
        GetUserIdentitiesFn: func(_ context.Context, _ string) ([]me.LinkedIdentity, error) {
            return []me.LinkedIdentity{
                {Provider: "google", ProviderEmail: &email, CreatedAt: time.Now()},
            }, nil
        },
    }
    h := me.NewHandler(fake)
    req := httptest.NewRequest(http.MethodGet, "/me/identities", nil)
    req = req.WithContext(token.WithUserID(req.Context(), "00000000-0000-0000-0000-000000000001"))
    w := httptest.NewRecorder()
    h.Identities(w, req)
    assert.Equal(t, http.StatusOK, w.Code)
    body := w.Body.String()
    assert.NotContains(t, body, "access_token")
    assert.NotContains(t, body, "refresh_token_provider")
}
```

### T-06 — Nullable fields omitted when nil

```go
func TestHandler_Identities_NullableFieldsOmitted(t *testing.T) {
    fake := &authsharedtest.MeFakeServicer{
        GetUserIdentitiesFn: func(_ context.Context, _ string) ([]me.LinkedIdentity, error) {
            return []me.LinkedIdentity{
                {Provider: "telegram", CreatedAt: time.Now()},
                // ProviderEmail, DisplayName, AvatarURL all nil
            }, nil
        },
    }
    h := me.NewHandler(fake)
    req := httptest.NewRequest(http.MethodGet, "/me/identities", nil)
    req = req.WithContext(token.WithUserID(req.Context(), "00000000-0000-0000-0000-000000000001"))
    w := httptest.NewRecorder()
    h.Identities(w, req)
    assert.Equal(t, http.StatusOK, w.Code)
    body := w.Body.String()
    assert.NotContains(t, body, "provider_email")
    assert.NotContains(t, body, "display_name")
    assert.NotContains(t, body, "avatar_url")
}
```

### T-02 — Empty identities → `"identities":[]` (never null)

```go
func TestHandler_Identities_Empty(t *testing.T) {
    fake := &authsharedtest.MeFakeServicer{} // default returns empty slice
    h := me.NewHandler(fake)
    req := httptest.NewRequest(http.MethodGet, "/me/identities", nil)
    req = req.WithContext(token.WithUserID(req.Context(), "00000000-0000-0000-0000-000000000001"))
    w := httptest.NewRecorder()
    h.Identities(w, req)
    assert.Equal(t, http.StatusOK, w.Code)
    assert.Contains(t, w.Body.String(), `"identities":[]`)
}
```

### T-08 — Service error → 500

```go
func TestHandler_Identities_ServiceError(t *testing.T) {
    fake := &authsharedtest.MeFakeServicer{
        GetUserIdentitiesFn: func(_ context.Context, _ string) ([]me.LinkedIdentity, error) {
            return nil, errors.New("db failure")
        },
    }
    h := me.NewHandler(fake)
    req := httptest.NewRequest(http.MethodGet, "/me/identities", nil)
    req = req.WithContext(token.WithUserID(req.Context(), "00000000-0000-0000-0000-000000000001"))
    w := httptest.NewRecorder()
    h.Identities(w, req)
    assert.Equal(t, http.StatusInternalServerError, w.Code)
}
```

---

## Checklist before closing Stage 4

- [ ] `Handler.Identities` implemented in `handler.go`
- [ ] `identitiesLimiter` wired in `routes.go` with prefix `ident:ip:`, rate 20/min, burst 20
- [ ] `go identitiesLimiter.StartCleanup(ctx)` called
- [ ] Route registered: `r.With(identitiesLimiter.Limit).Get("/me/identities", h.Identities)`
- [ ] `make([]identityItem, 0, len(identities))` — non-nil slice → never `null` in JSON
- [ ] No `http.MaxBytesReader` — GET endpoint with no body
- [ ] H-layer tests T-02, T-04, T-05, T-06, T-07, T-08 written and green
- [ ] `go build ./...` passes
- [ ] `go vet ./...` clean

Once all items are checked → proceed to Stage 5 (audit).
