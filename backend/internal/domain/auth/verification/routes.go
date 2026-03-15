// Package verification registers the POST /verify-email and POST /resend-verification endpoints.
package verification

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

// Routes registers the verification endpoints on r.
// Call from auth.Routes in internal/domain/auth/routes.go:
//
//	verification.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST /verification:        5 req / 10 min per IP  ("vfy:ip:") + exponential backoff after OTP failures ("vfy:bo:")
//   - POST /verification/resend: 3 req / 10 min per IP  ("rsnd:ip:")
//
// Middleware ordering:
//
//	POST /verification, /verification/resend: IPRateLimiter → handler.{Method}
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 5 req / 10 min per IP, burst=5.
	// rate = 5 / (10 * 60) = 0.00833 tokens/sec.
	ipLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "vfy:ip:", 5.0/(10*60), 5, 10*time.Minute)
	go ipLimiter.StartCleanup(ctx)

	backoff := ratelimit.NewBackoffLimiterWithStore(
		deps.KVStore, "vfy:bo:",
		2*time.Second, 5*time.Minute, 30*time.Minute,
	)
	go backoff.StartCleanup(ctx)

	// 3 req / 10 min per IP, burst=3.
	// rate = 3 / (10 * 60) = 0.005 tokens/sec.
	resendLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "rsnd:ip:", 3.0/(10*60), 3, 10*time.Minute)
	go resendLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store, deps.OTPTokenTTL)
	h := NewHandler(svc, backoff, mailer.OTPHandlerBase{
		Send:    deps.Mailer.Send(mailertemplates.VerificationKey),
		Queue:   deps.MailQueue,
		Timeout: deps.MailDeliveryTimeout,
	})

	ratelimit.RouteWithIP(r, http.MethodPost, "/verification", h.VerifyEmail, ipLimiter)
	ratelimit.RouteWithIP(r, http.MethodPost, "/verification/resend", h.ResendVerification, resendLimiter)
}
