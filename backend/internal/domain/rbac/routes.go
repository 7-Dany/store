// Package rbac assembles the rbac domain sub-router.
// Serves owner, roles, and permissions endpoints under /api/v1/rbac.
package rbac

import (
	"context"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/rbac/owner"
	"github.com/7-Dany/store/backend/internal/domain/rbac/permissions"
	"github.com/7-Dany/store/backend/internal/domain/rbac/roles"
)

// Mount registers the rbac sub-router at /rbac on r.
// Call from domain.Mount in internal/domain/routes.go:
//
//	rbac.Mount(ctx, r, deps)
func Mount(ctx context.Context, r chi.Router, deps *app.Deps) {
	r.Mount("/rbac", Routes(ctx, deps))
}

// Routes returns a self-contained chi sub-router for all /rbac endpoints.
// Called by Mount. Use directly in tests to exercise the rbac sub-domain.
//
//	r.Mount("/rbac", rbac.Routes(ctx, deps))
//
// Endpoints:
//
//	PUT    /owner/assign    — initial owner assignment (secret-gated)
//	POST   /owner/transfer — initiate ownership transfer
//	PUT    /owner/transfer — accept transfer (token is credential)
//	DELETE /owner/transfer — cancel pending transfer
//	GET    /roles                            — list roles
//	POST   /roles                            — create role
//	GET    /roles/{id}                       — get role
//	PATCH  /roles/{id}                       — update role
//	DELETE /roles/{id}                       — delete role
//	GET    /roles/{id}/permissions           — list role permissions
//	POST   /roles/{id}/permissions           — add role permission
//	DELETE /roles/{id}/permissions/{perm_id} — remove role permission
//	GET    /permissions                      — list permissions
//	GET    /permissions/groups               — list permission groups
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.AllowContentType("application/json"))

	owner.Routes(ctx, r, deps)       // PUT /owner/assign; POST /owner/transfer; PUT /owner/transfer; DELETE /owner/transfer
	permissions.Routes(ctx, r, deps) // GET /permissions, /permissions/groups
	roles.Routes(ctx, r, deps)       // GET/POST /roles; GET/PATCH/DELETE /roles/{id}; GET/POST/DELETE /roles/{id}/permissions/*

	return r
}
