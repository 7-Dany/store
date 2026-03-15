// Package domain wires all domain sub-routers onto a versioned API group.
// It is the single place that maps every domain to its canonical URL prefix.
package domain

import (
	"context"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/admin"
	"github.com/7-Dany/store/backend/internal/domain/auth"
	"github.com/7-Dany/store/backend/internal/domain/oauth"
	"github.com/7-Dany/store/backend/internal/domain/profile"
	"github.com/7-Dany/store/backend/internal/domain/rbac"
)

// Mount registers all domain sub-routers on r.
// Call from server/routes.go inside the /api/v1 route group:
//
//	r.Route("/api/v1", func(r chi.Router) {
//		domain.Mount(ctx, r, deps)
//	})
func Mount(ctx context.Context, r chi.Router, deps *app.Deps) {
	auth.Mount(ctx, r, deps)
	oauth.Mount(ctx, r, deps)
	profile.Mount(ctx, r, deps)
	rbac.Mount(ctx, r, deps)
	admin.Mount(ctx, r, deps)
}
