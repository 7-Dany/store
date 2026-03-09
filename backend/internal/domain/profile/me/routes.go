// Package me registers GET /me and PATCH /me for the profile domain.
// Call from the profile root assembler:
//
//	me.Routes(ctx, r, deps)
//
// Rate limits:
//   - GET   /me:             10 req / 1 min per IP (pme:ip:)
//   - PATCH /me:             10 req / 1 min per IP (prof:ip:)
//   - GET   /me/identities:  20 req / 1 min per IP (ident:ip:)
package me

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the GET /me, PATCH /me, and GET /me/identities endpoints on r.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 10 req / 1 min per IP — prevents bulk profile enumeration.
	// rate = 10 / (1 * 60) ≈ 0.167 tokens/sec.
	meLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "pme:ip:", 10.0/(1*60), 10, 1*time.Minute)
	go meLimiter.StartCleanup(ctx)

	// 10 req / 1 min per IP — rate-limits profile update requests.
	// rate = 10 / (1 * 60) ≈ 0.167 tokens/sec.
	updateProfileLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "prof:ip:", 10.0/(1*60), 10, 1*time.Minute)
	go updateProfileLimiter.StartCleanup(ctx)

	// 20 req / 1 min per IP — prevents bulk enumeration of identity data.
	// rate = 20 / (1 * 60) ≈ 0.333 tokens/sec.
	identitiesLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "ident:ip:", 20.0/(1*60), 20, 1*time.Minute)
	go identitiesLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc)

	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.With(meLimiter.Limit).Get("/me", h.Me)
		r.With(updateProfileLimiter.Limit).Patch("/me", h.UpdateProfile)
		r.With(identitiesLimiter.Limit).Get("/me/identities", h.Identities)
	})
}
