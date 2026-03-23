// Package bitcoin assembles the Bitcoin domain sub-router.
package bitcoin

import (
	"context"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/domain/bitcoin/watch"
)

// Mount registers the bitcoin sub-router at /bitcoin on r.
// Called from domain.Mount in internal/domain/routes.go:
//
//	bitcoin.Mount(ctx, r, deps)
//
// No-op when deps.BitcoinEnabled is false.
func Mount(ctx context.Context, r chi.Router, deps *app.Deps) {
	if !deps.BitcoinEnabled {
		return
	}
	r.Mount("/bitcoin", Routes(ctx, deps))
}

// Routes returns a self-contained chi sub-router for all /bitcoin endpoints.
// Called by Mount. Use directly in tests to exercise the full domain routing.
//
//	r.Mount("/bitcoin", bitcoin.Routes(ctx, deps))
func Routes(ctx context.Context, deps *app.Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.AllowContentType("application/json"))

	watch.Routes(ctx, r, deps)

	return r
}
