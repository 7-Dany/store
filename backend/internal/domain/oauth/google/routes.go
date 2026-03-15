// Package google registers the GET /google, GET /google/callback, and DELETE /google/unlink endpoints.
package google

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers all Google OAuth endpoints on r.
// Call from oauth.Routes in internal/domain/oauth/routes.go:
//
//	google.Routes(ctx, r, deps)
//
// Rate limits:
//   - GET    /google:          20 req / 5 min per IP    ("goauth:init:ip:")
//   - GET    /google/callback: 20 req / 5 min per IP    ("goauth:cb:ip:")
//   - DELETE /google:            5 req / 15 min per user  ("goauth:unl:usr:")
//
// Middleware ordering:
//
//	GET /google, /google/callback: IPRateLimiter → handler.{Method}
//	DELETE /google:                JWTAuth → UserRateLimiter → handler.HandleUnlink
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// ── Rate limiters ─────────────────────────────────────────────────────────

	// 20 req / 5 min per IP — permits rapid browser redirects and page refreshes
	// during the OAuth flow without enabling state-store flooding from a single IP.
	// rate = 20 / (5 * 60) ≈ 0.0667 tokens/sec.
	initLimiter := ratelimit.NewIPRateLimiter(
		deps.KVStore, "goauth:init:ip:",
		20.0/(5*60), 20, 5*time.Minute,
	)
	go initLimiter.StartCleanup(ctx)

	// 20 req / 5 min per IP — prevents callback-URL replay enumeration; matches
	// the initiate limit so the two endpoints cannot be driven out of sync.
	// rate = 20 / (5 * 60) ≈ 0.0667 tokens/sec.
	cbLimiter := ratelimit.NewIPRateLimiter(
		deps.KVStore, "goauth:cb:ip:",
		20.0/(5*60), 20, 5*time.Minute,
	)
	go cbLimiter.StartCleanup(ctx)

	// 5 req / 15 min per user — deters repeated unlink/relink cycling that could
	// be used to probe account state or exhaust provider token quotas.
	// rate = 5 / (15 * 60) ≈ 0.00556 tokens/sec.
	unlinkLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "goauth:unl:usr:",
		5.0/(15*60), 5, 15*time.Minute,
	)
	go unlinkLimiter.StartCleanup(ctx)

	// ── Dependency wiring ─────────────────────────────────────────────────────
	provider, err := NewGoogleProvider(
		ctx,
		deps.OAuth.GoogleClientID,
		deps.OAuth.GoogleClientSecret,
		deps.OAuth.GoogleRedirectURI,
	)
	if err != nil {
		// NewGoogleProvider fails only when the OIDC discovery endpoint is
		// unreachable at startup. Panic here so the misconfiguration is
		// caught immediately rather than silently serving 500s.
		panic("google.Routes: OIDC provider init: " + err.Error())
	}

	store := NewStore(deps.Pool)
	svc := NewService(store, provider, deps.Encryptor)
	h := NewHandler(
		svc,
		deps.KVStore,
		deps.JWTConfig,
		deps.OAuth.GoogleClientID,
		deps.OAuth.GoogleRedirectURI,
		deps.OAuth.SuccessURL,
		deps.OAuth.ErrorURL,
		deps.SecureCookies,
	)

	// ── Route registration ────────────────────────────────────────────────────

	// GET /google — initiate OAuth flow (IP-rate-limited; no auth required)
	r.With(initLimiter.Limit).Get("/google", h.HandleInitiate)

	// GET /google/callback — OAuth callback (IP-rate-limited; no auth required)
	r.With(cbLimiter.Limit).Get("/google/callback", h.HandleCallback)

	// DELETE /google — remove Google identity (JWT auth + user-rate-limited)
	r.With(deps.JWTAuth, unlinkLimiter.Limit).Delete("/google", h.HandleUnlink)
}
