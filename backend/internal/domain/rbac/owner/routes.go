// Package owner registers the PUT /owner/assign, POST /owner/transfer, PUT /owner/transfer, and DELETE /owner/transfer endpoints.
package owner

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers all owner endpoints on r.
// Call from rbac.Routes in internal/domain/rbac/routes.go:
//
//	owner.Routes(ctx, r, deps)
//
// Rate limits:
//   - PUT    /owner/assign:   3 req / 15 min per IP    ("asgn:ip:")
//   - POST   /owner/transfer: 3 req / 24 h   per user  ("xfr:usr:" — applied inside handler after validation)
//   - PUT    /owner/transfer: 10 req / 1 h   per IP    ("xfra:ip:")
//   - DELETE /owner/transfer: 10 req / 1 h   per user  ("xfrc:usr:")
//
// Middleware ordering:
//
//	PUT    /owner/assign:   JWTAuth → IPRateLimiter → handler.AssignOwner
//	POST   /owner/transfer: JWTAuth → handler.InitiateTransfer
//	PUT    /owner/transfer: IPRateLimiter → handler.AcceptTransfer
//	DELETE /owner/transfer: JWTAuth → UserRateLimiter → handler.CancelTransfer
//
// Note: all paths are prefixed with /owner/ so they resolve to /rbac/owner/...
// when mounted via rbac.Routes.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	secret := deps.BootstrapSecret

	// Assign-owner rate limiter: 3 req / 15 min per IP.
	assignLimiter := ratelimit.NewIPRateLimiter(
		deps.KVStore, "asgn:ip:",
		3.0/(15*60), 3,
		15*time.Minute,
	)
	go assignLimiter.StartCleanup(ctx)

	// Initiate transfer: 3 req / 24 h per user.
	initiateLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "xfr:usr:",
		3.0/(24*60*60), 3,
		24*time.Hour,
	)
	go initiateLimiter.StartCleanup(ctx)

	// Accept transfer: 10 req / 1 h per IP.
	acceptLimiter := ratelimit.NewIPRateLimiter(
		deps.KVStore, "xfra:ip:",
		10.0/3600, 10,
		time.Hour,
	)
	go acceptLimiter.StartCleanup(ctx)

	// Cancel transfer: 10 req / 1 h per user.
	cancelLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "xfrc:usr:",
		10.0/3600, 10,
		time.Hour,
	)
	go cancelLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc, secret, deps.RBAC, deps.Mailer, deps.MailQueue, initiateLimiter)

	// ── Assign owner (JWT required; IP rate-limited) ──────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.With(assignLimiter.Limit).Put("/owner/assign", h.AssignOwner)
	})

	// ── Transfer: initiate + cancel (JWT required; user rate-limited) ─────────
	// Note: initiateLimiter is applied inside the handler (after validation) so
	// malformed/invalid requests do not consume tokens from the per-user bucket.
	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.Post("/owner/transfer", h.InitiateTransfer)
		r.With(cancelLimiter.Limit).Delete("/owner/transfer", h.CancelTransfer)
	})

	// ── Transfer: accept (no JWT; IP rate-limited; token is credential) ───────
	r.With(acceptLimiter.Limit).Put("/owner/transfer", h.AcceptTransfer)
}
