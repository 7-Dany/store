// Package block registers the block-detail lookup endpoints.
package block

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the block-details endpoints on r.
// Call from the bitcoin domain assembler:
//
//	block.Routes(ctx, r, deps)
//
// Rate limit: 20 req/min per IP, burst 20.
// Auth: Bearer JWT (deps.JWTAuth). Rate limit runs BEFORE JWT auth.
//
// Registered routes:
//
//	GET /block/latest  — chain-tip block details
//	GET /block/{hash}  — block details by hash
//
// NOTE: /block/latest must be registered before /block/{hash} so chi does not
// treat the literal "latest" as a dynamic hash parameter.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	blockLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "btc:block:ip:", 20.0/60, 20, 1*time.Minute)
	go blockLimiter.StartCleanup(ctx)

	svc := NewService(deps.BitcoinRPC)
	h := NewHandler(svc)

	// /block/latest MUST be registered before /block/{hash} — chi routes are
	// matched in registration order for the same path length, and "latest"
	// would otherwise be captured by the {hash} wildcard.
	r.With(blockLimiter.Limit, deps.JWTAuth).Get("/block/latest", h.GetLatestBlock)
	r.With(blockLimiter.Limit, deps.JWTAuth).Get("/block/{hash}", h.GetBlock)
}
