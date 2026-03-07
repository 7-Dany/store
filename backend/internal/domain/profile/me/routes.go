// Package me registers GET /me and PATCH /me for the profile domain.
// Call from the profile root assembler:
//
//	me.Routes(ctx, r, deps)
//
// Rate limits:
//   - GET   /me: 10 req / 1 min per IP (pme:ip:)
//   - PATCH /me: 10 req / 1 min per IP (prof:ip:)
package me

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the GET /me and PATCH /me endpoints on r.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 10 req / 1 min per IP — prevents bulk profile enumeration.
	// rate = 10 / (1 * 60) ≈ 0.167 tokens/sec.
	meLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "pme:ip:", 10.0/(1*60), 10, 1*time.Minute)
	go meLimiter.StartCleanup(ctx)

	// 10 req / 1 min per IP — rate-limits profile update requests.
	// rate = 10 / (1 * 60) ≈ 0.167 tokens/sec.
	updateProfileLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "prof:ip:", 10.0/(1*60), 10, 1*time.Minute)
	go updateProfileLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc)

	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.With(meLimiter.Limit).Get("/me", h.Me)
		r.With(updateProfileLimiter.Limit).Patch("/me", h.UpdateProfile)
	})
}
