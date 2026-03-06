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
// Call from the auth root assembler:
//
//	unlock.Routes(ctx, r, deps)
//
// Rate limits: 3 req / 10 min per IP shared across request and confirm.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 3 req / 10 min per IP, burst=3.
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

	ratelimit.RouteWithIP(r, http.MethodPost, "/request-unlock", h.RequestUnlock, limiter)
	ratelimit.RouteWithIP(r, http.MethodPost, "/confirm-unlock", h.ConfirmUnlock, limiter)
}
