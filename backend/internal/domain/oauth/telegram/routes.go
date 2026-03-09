package telegram

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/7-Dany/store/backend/internal/app"
	"github.com/7-Dany/store/backend/internal/platform/ratelimit"
)

// Routes registers all Telegram OAuth endpoints on r.
// Call from the oauth root assembler:
//
//	telegram.Routes(ctx, r, deps)
//
// Rate limits:
//   - POST   /telegram/callback — 10 req / 1 min per IP
//   - POST   /telegram/link     — 5 req / 15 min per authenticated user
//   - DELETE /telegram/unlink   — 5 req / 15 min per authenticated user
func Routes(ctx context.Context, r chi.Router, deps *app.Deps) {
	// ── Rate limiters ─────────────────────────────────────────────────────────

	// 10 req / 1 min per IP — deters widget replay abuse.
	cbLimiter := ratelimit.NewIPRateLimiter(
		deps.KVStore, "tgcb:ip:",
		10.0/(1*60), 10, 1*time.Minute,
	)
	go cbLimiter.StartCleanup(ctx)

	// 5 req / 15 min per user — deters repeated link attempts.
	// rate=1/900: burst=5 tokens; Retry-After = ceil(1/rate) = 900 s (full window).
	linkLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "tglnk:usr:",
		1.0/900, 5, 15*time.Minute,
	)
	go linkLimiter.StartCleanup(ctx)

	// 5 req / 15 min per user — deters unlink cycling.
	// rate=1/900: burst=5 tokens; Retry-After = ceil(1/rate) = 900 s (full window).
	unlinkLimiter := ratelimit.NewUserRateLimiter(
		deps.KVStore, "tgunlk:usr:",
		1.0/900, 5, 15*time.Minute,
	)
	go unlinkLimiter.StartCleanup(ctx)

	// ── Dependency wiring ─────────────────────────────────────────────────────

	// D-15: Panic at startup if the bot token is absent — an empty token produces
	// a deterministic HMAC key that any attacker can replicate.
	if deps.OAuth.TelegramBotToken == "" {
		panic("telegram.Routes: TelegramBotToken must not be empty (set TELEGRAM_BOT_TOKEN)")
	}

	store := NewStore(deps.Pool)
	svc := NewService(store)
	h := NewHandler(svc, deps.OAuth.TelegramBotToken, deps.JWTConfig, deps.SecureCookies)

	// ── Route registration ────────────────────────────────────────────────────

	// POST /telegram/callback — Telegram Login Widget callback (IP-rate-limited; public)
	r.With(cbLimiter.Limit).Post("/telegram/callback", h.HandleCallback)

	// POST /telegram/link — link Telegram identity (JWT auth + user-rate-limited)
	r.With(deps.JWTAuth, linkLimiter.Limit).Post("/telegram/link", h.HandleLink)

	// DELETE /telegram/unlink — remove Telegram identity (JWT auth + user-rate-limited)
	r.With(deps.JWTAuth, unlinkLimiter.Limit).Delete("/telegram/unlink", h.HandleUnlink)
}
