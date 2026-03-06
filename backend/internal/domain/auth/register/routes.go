package register

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/mailer"
	mailertemplates "github.com/7-Dany/store/backend/internal/platform/mailer/templates"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers the register endpoint on r.
// Call from the auth root assembler:
//
//	register.Routes(ctx, r, deps)
//
// Rate limits: burst=5, 5 req / 10 min per IP — deters mass account creation.
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 5 req / 10 min per IP, burst=5 — deters mass account creation.
	// rate = 5 / (10 * 60) = 0.00833 tokens/sec.
	limiter := ratelimit.NewIPRateLimiter(deps.KVStore, "reg:ip:", 5.0/(10*60), 5, 10*time.Minute)
	go limiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store, deps.OTPTokenTTL)
	h := NewHandler(svc, mailer.OTPHandlerBase{
		Send:    deps.Mailer.Send(mailertemplates.VerificationKey),
		Queue:   deps.MailQueue,
		Timeout: deps.MailDeliveryTimeout,
	})

	r.With(limiter.Limit).Post("/register", h.Register)
}
