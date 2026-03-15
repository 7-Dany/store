// Package unlock registers the POST /request-unlock and POST /confirm-unlock endpoints.
package unlock

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	mailertemplates "github.com/7-Dany/store/backend/internal/platform/mailer/templates"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the unlock endpoints on r.
// Call from auth.Routes in internal/domain/auth/routes.go:
//
//	unlock.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST /unlock: 3 req / 10 min per IP  ("unlk:ip:" — shared limiter)
//   - PUT  /unlock: 3 req / 10 min per IP  ("unlk:ip:" — shared limiter)
//
// Middleware ordering:
//
//	POST /unlock, PUT /unlock: IPRateLimiter → handler.{Method}
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 3 req / 10 min per IP — one shared bucket across both endpoints so an
	// attacker cannot bypass the limit by alternating between request and confirm.
	// rate = 3 / (10 * 60) = 0.005 tokens/sec.
	limiter := ratelimit.NewIPRateLimiter(deps.KVStore, "unlk:ip:", 3.0/(10*60), 3, 10*time.Minute)
	go limiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store, deps.OTPTokenTTL)
	h := NewHandler(svc, mailer.OTPHandlerBase{
		Send:    deps.Mailer.Send(mailertemplates.UnlockKey),
		Queue:   deps.MailQueue,
		Timeout: deps.MailDeliveryTimeout,
	})

	ratelimit.RouteWithIP(r, http.MethodPost, "/unlock", h.RequestUnlock, limiter)
	ratelimit.RouteWithIP(r, http.MethodPut, "/unlock", h.ConfirmUnlock, limiter)
}
