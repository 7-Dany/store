package ratelimit

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// RouteWithIP registers method+pattern on r, applying limiter.Limit as
// middleware when limiter is non-nil. Use in Routes() functions to eliminate
// the repeated nil-guard if/else around optional IP rate limiters.
func RouteWithIP(r chi.Router, method, pattern string, h http.HandlerFunc, limiter *IPRateLimiter) {
	if limiter != nil {
		r.With(limiter.Limit).Method(method, pattern, h)
	} else {
		r.Method(method, pattern, h)
	}
}
