package txstatus

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the two txstatus endpoints on r.
// Call from the bitcoin domain assembler:
//
//	txstatus.Routes(ctx, r, deps)
//
// Rate limit: 20 req/min per IP, burst 20.
// Auth: Bearer JWT (deps.JWTAuth). Rate limit runs BEFORE JWT auth.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	txstatusLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "btc:txstatus:ip:", 20.0/60, 20, 1*time.Minute)
	go txstatusLimiter.StartCleanup(ctx)

	svc := NewService(deps.BitcoinRPC, deps.Metrics)
	h := NewHandler(svc)

	r.With(txstatusLimiter.Limit, deps.JWTAuth).Get("/tx/{txid}/status", h.GetTxStatus)
	r.With(txstatusLimiter.Limit, deps.JWTAuth).Get("/tx/status", h.GetTxStatusBatch)
}
