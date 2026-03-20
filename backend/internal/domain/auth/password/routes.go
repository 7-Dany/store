// Package password registers the POST /forgot-password, /verify-reset-code, /reset-password, and /change-password endpoints.
package password

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

// Routes registers the password endpoints on r.
// Call from auth.Routes in internal/domain/auth/routes.go:
//
//	password.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST  /password/reset:        3 req / 10 min per IP  ("fpw:ip:")
//   - POST  /password/reset/verify: 5 req / 10 min per IP  ("vpc:ip:")
//   - PUT   /password/reset:        5 req / 10 min per IP  ("rpw:ip:")
//   - PATCH /password:              5 req / 15 min per IP  ("cpw:ip:")
//
// Middleware ordering:
//
//	POST /password/reset, POST /password/reset/verify, PUT /password/reset: IPRateLimiter → handler.{Method}
//	PATCH /password:                                                          IPRateLimiter → JWTAuth → handler.ChangePassword
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// 3 req / 10 min per IP — limits password-reset OTP flooding per network origin.
	// rate = 3 / (10 * 60) = 0.005 tokens/sec.
	forgotLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "fpw:ip:", 3.0/(10*60), 3, 10*time.Minute)
	go forgotLimiter.StartCleanup(ctx)

	// 5 req / 10 min per IP — mirrors the reset-password limit to prevent
	// brute-force OTP guessing via the verify endpoint.
	// rate = 5 / (10 * 60) = 0.00833 tokens/sec.
	verifyLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "vpc:ip:", 5.0/(10*60), 5, 10*time.Minute)
	go verifyLimiter.StartCleanup(ctx)

	// 5 req / 10 min per IP — deters OTP brute-force at the network level.
	// rate = 5 / (10 * 60) = 0.00833 tokens/sec.
	resetLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "rpw:ip:", 5.0/(10*60), 5, 10*time.Minute)
	go resetLimiter.StartCleanup(ctx)

	// 5 req / 15 min per IP — deters credential stuffing at the network level.
	// rate = 5 / (15 * 60) = 0.00556 tokens/sec.
	changePasswordLimiter := ratelimit.NewIPRateLimiter(deps.KVStore, "cpw:ip:", 5.0/(15*60), 5, 15*time.Minute)
	go changePasswordLimiter.StartCleanup(ctx)

	store := NewStore(deps.Pool)
	svc := NewService(store, deps.OTPTokenTTL)

	ttl := deps.JWTConfig.AccessTTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	bl := &userBlocklist{store: deps.KVStore, ttl: ttl}

	h := NewHandler(
		svc,
		mailer.OTPHandlerBase{
			Send:    deps.Mailer.Send(mailertemplates.PasswordResetKey),
			Queue:   deps.MailQueue,
			Timeout: deps.MailDeliveryTimeout,
		},
		bl,              // per-user Blocklist for reset-password
		deps.Blocklist,  // JTIBlocklist for change-password
		deps.JWTConfig.AccessTTL,
		deps.SecureCookies,
		deps.KVStore,    // grantStore
		10*time.Minute,  // grantTTL
		deps.Metrics,
	)

	ratelimit.RouteWithIP(r, http.MethodPost, "/password/reset",        h.ForgotPassword, forgotLimiter)
	ratelimit.RouteWithIP(r, http.MethodPost, "/password/reset/verify", h.VerifyResetCode, verifyLimiter)
	ratelimit.RouteWithIP(r, http.MethodPut,  "/password/reset",        h.ResetPassword,  resetLimiter)

	r.Group(func(r chi.Router) {
		// Rate limiter fires before auth so unauthenticated warmup requests still
		// consume a slot — the 6th request from the same IP is 429 regardless of
		// whether the token is valid.
		r.Use(changePasswordLimiter.Limit)
		r.Use(deps.JWTAuth)
		r.Patch("/password", h.ChangePassword)
	})
}
