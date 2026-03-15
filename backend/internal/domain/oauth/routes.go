// Package oauth assembles the oauth domain sub-router.
// Serves endpoints under /api/v1/oauth.
package oauth

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/oauth/google"
	"github.com/7-Dany/store/backend/internal/domain/oauth/telegram"
)

// Mount registers the oauth sub-router at /oauth on r.
// Call from domain.Mount in internal/domain/routes.go:
//
//	oauth.Mount(ctx, r, deps)
func Mount(ctx context.Context, r chi.Router, deps *app.Deps) {
	r.Mount("/oauth", Routes(ctx, deps))
}

// Routes returns a self-contained chi sub-router for all /oauth endpoints.
// Called by Mount. Use directly in tests to exercise the oauth sub-domain.
//
//	r.Mount("/oauth", oauth.Routes(ctx, deps))
//
// Endpoints:
//
//	GET    /google             — initiate Google OAuth flow
//	GET    /google/callback    — Google OAuth callback
//	DELETE /google             — remove linked Google identity (authenticated)
//	POST   /telegram/callback  — Telegram Login Widget callback
//	PUT    /telegram           — link Telegram identity (authenticated)
//	DELETE /telegram           — remove linked Telegram identity (authenticated)
//
// Note: AllowContentType("application/json") is intentionally omitted — all
// OAuth endpoints are browser redirects or JSON-free GETs/DELETEs (D-13).
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	google.Routes(ctx, r, deps)   // GET /google, /google/callback; DELETE /google/unlink
	telegram.Routes(ctx, r, deps) // POST /telegram/callback, /telegram/link; DELETE /telegram/unlink
	return r
}
