// Package block registers the GET /block/{hash} endpoint.
package block

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the block-details endpoint on r.
// Call from the bitcoin domain assembler:
//
//	block.Routes(ctx, r, deps)
//
// Rate limit: 20 req/min per IP, burst 20.
// Auth: Bearer JWT (deps.JWTAuth). Rate limit runs BEFORE JWT auth.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	blockLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "btc:block:ip:", 20.0/60, 20, 1*time.Minute)
	go blockLimiter.StartCleanup(ctx)

	svc := NewService(deps.BitcoinRPC)
	h := NewHandler(svc)

	r.With(blockLimiter.Limit, deps.JWTAuth).Get("/block/{hash}", h.GetBlock)
}
