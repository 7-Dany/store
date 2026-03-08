// Package oauth assembles the oauth domain sub-router.
// Serves endpoints under /api/v1/oauth.
package oauth

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/oauth/google"
)

// Routes returns a self-contained chi sub-router for all /oauth endpoints.
// Mount at /api/v1/oauth in the server router:
//
//	r.Mount("/oauth", oauth.Routes(ctx, deps))
//
// Note: AllowContentType("application/json") is intentionally omitted — all
// OAuth endpoints are browser redirects or JSON-free GETs/DELETEs (D-13).
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	google.Routes(ctx, r, deps)
	return r
}
