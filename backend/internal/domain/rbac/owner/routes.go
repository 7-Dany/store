package owner

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers all owner-domain routes on r:
//
//	POST   /assign                 — initial owner role assignment (secret-gated)
//	POST   /transfer               — initiate ownership transfer
//	POST   /transfer/accept        — accept transfer (unauthenticated; token is credential)
//	DELETE /transfer               — cancel pending transfer
//
// Call from the owner sub-router assembler:
//
//	owner.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST /assign:          3 req / 15 min per IP   (tight; one-time path)
//   - POST /transfer:        3 req / 24 h per user   (high-stakes action)
//   - POST /transfer/accept: 10 req / 1 h per IP     (token provides auth)
//   - DELETE /transfer:      10 req / 1 h per user   (owner-only)
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
		r.With(assignLimiter.Limit).Post("/assign", h.AssignOwner)
	})

	// ── Transfer: initiate + cancel (JWT required; user rate-limited) ─────────
	// Note: initiateLimiter is applied inside the handler (after validation) so
	// malformed/invalid requests do not consume tokens from the per-user bucket.
	r.Group(func(r chi.Router) {
		r.Use(deps.JWTAuth)
		r.Post("/transfer", h.InitiateTransfer)
		r.With(cancelLimiter.Limit).Delete("/transfer", h.CancelTransfer)
	})

	// ── Transfer: accept (no JWT; IP rate-limited; token is credential) ───────
	r.With(acceptLimiter.Limit).Post("/transfer/accept", h.AcceptTransfer)
}
