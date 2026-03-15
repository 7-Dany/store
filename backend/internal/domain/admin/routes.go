// Package admin assembles the admin domain sub-router.
// Serves user-management endpoints under /api/v1/admin.
package admin

import (
	"context"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/admin/userlock"
	"github.com/7-Dany/store/backend/internal/domain/admin/userpermissions"
	"github.com/7-Dany/store/backend/internal/domain/admin/userroles"
)

// Mount registers the admin sub-router at /admin on r.
// Call from domain.Mount in internal/domain/routes.go:
//
//	admin.Mount(ctx, r, deps)
func Mount(ctx context.Context, r chi.Router, deps *app.Deps) {
	r.Mount("/admin", Routes(ctx, deps))
}

// Routes returns a self-contained chi sub-router for all /admin endpoints.
// Called by Mount. Use directly in tests to exercise the admin sub-domain.
//
//	r.Mount("/admin", admin.Routes(ctx, deps))
//
// Endpoints:
//
//	GET    /users/{user_id}/role                    — get the user's current role
//	PUT    /users/{user_id}/role                    — assign a role to a user
//	DELETE /users/{user_id}/role                    — remove a user's role
//	GET    /users/{user_id}/permissions             — list the user's direct permission grants
//	POST   /users/{user_id}/permissions             — grant a direct permission to a user
//	DELETE /users/{user_id}/permissions/{grant_id}  — revoke a direct permission grant
//	POST   /users/{user_id}/lock                    — lock a user account
//	DELETE /users/{user_id}/lock                    — unlock a user account
//	GET    /users/{user_id}/lock                    — get the user's lock status
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.AllowContentType("application/json"))

	userroles.Routes(ctx, r, deps)       // GET/PUT/DELETE /users/{id}/role
	userpermissions.Routes(ctx, r, deps) // GET/POST/DELETE /users/{id}/permissions/*
	userlock.Routes(ctx, r, deps)        // POST/DELETE/GET /users/{id}/lock

	return r
}
