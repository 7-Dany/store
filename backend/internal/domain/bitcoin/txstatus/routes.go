// Package txstatus registers the Bitcoin tracked-transaction endpoints.
package txstatus

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the persisted tracked-transaction endpoints on r.
// Call from the bitcoin domain assembler:
//
//	txstatus.Routes(ctx, r, deps)
//
// Rate limit: 20 req/min per IP, burst 20.
//
// Endpoints:
//   - POST   /tx
//   - GET    /tx
//   - GET    /tx/{id}
//   - PUT    /tx/{id}
//   - DELETE /tx/{id}
//
// Auth: Bearer JWT (deps.JWTAuth). Rate limit runs BEFORE JWT auth.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 20 req / 1 min per IP. This protects the tracked-transaction surface from
	// easy scraping and burst retries without forcing auth middleware to absorb
	// the full request rate first.
	txstatusLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "btc:txstatus:ip:", 20.0/60, 20, 1*time.Minute)
	go txstatusLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(deps.BitcoinRPC, store, deps.Metrics, deps.BitcoinNetwork)
	h := NewHandler(svc, deps.BitcoinNetwork)

	r.With(txstatusLimiter.Limit, deps.JWTAuth).Post("/tx", h.CreateTrackedTxStatus)
	r.With(txstatusLimiter.Limit, deps.JWTAuth).Get("/tx", h.ListTrackedTxStatuses)
	r.With(txstatusLimiter.Limit, deps.JWTAuth).Get("/tx/{id}", h.GetTrackedTxStatus)
	r.With(txstatusLimiter.Limit, deps.JWTAuth).Put("/tx/{id}", h.UpdateTrackedTxStatus)
	r.With(txstatusLimiter.Limit, deps.JWTAuth).Delete("/tx/{id}", h.DeleteTrackedTxStatus)
}
