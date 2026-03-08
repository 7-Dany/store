package token

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/google/uuid"
)

// Auth returns a chi-compatible middleware that validates the Bearer access
// token and injects userID + sessionID into the request context.
//
// Validation steps, in order:
//  1. Extract the raw token from "Authorization: Bearer <token>".
//     Missing or malformed header → 401 missing_token.
//  2. Parse and verify the JWT (signature, issuer, audience, expiry) via
//     ParseAccessToken. Any failure → 401 invalid_token.
//  2a. Validate sub is a parseable UUID. Invalid sub → 401 invalid_token.
//  3. Check the JTI against the blocklist (if non-nil).
//     Present in blocklist → 401 token_revoked.
//     Transient blocklist error → also 401 token_revoked (fail closed).
//  3b. Check the per-user block key ("pr_blocked_user:<uid>") in userStore.
//     Written by the reset-password handler after a successful reset so that
//     all outstanding access tokens for the user are immediately invalidated.
//     Present → 401 token_revoked. Transient error → fail closed.
//  4. Inject userID and sessionID into the request context and call next.
//
// Passing a nil blocklist skips step 3 — useful in tests or deployments
// before a Redis instance is available.
// Passing a nil userStore skips step 3b.
func Auth(secret string, blocklist kvstore.TokenBlocklist, userStore kvstore.Store) func(http.Handler) http.Handler {
	// Security: an empty secret would cause every authenticated request to return
	// 401 with no startup signal. Panic at construction so misconfiguration is
	// caught before the server accepts traffic.
	if secret == "" {
		panic("token.Auth: signing secret must not be empty")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Extract Bearer token.
			raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || raw == "" {
				respond.Error(w, http.StatusUnauthorized, "missing_token",
					"Authorization: Bearer <token> header is required")
				return
			}

			// 2. Parse and verify JWT.
			claims, err := ParseAccessToken(raw, secret)
			if err != nil {
				respond.Error(w, http.StatusUnauthorized, "invalid_token",
					"invalid or expired access token")
				return
			}

			// 2a. Validate sub claim is a well-formed UUID.
			// A token signed with sub:"" or a non-UUID subject would pass the library
			// checks above but corrupt the authenticated identity in the context.
			if _, err := uuid.Parse(claims.Subject); err != nil {
				respond.Error(w, http.StatusUnauthorized, "invalid_token",
					"invalid or expired access token")
				return
			}

			// 3. JTI blocklist check — fail closed on transient errors.
			if blocklist != nil {
				blocked, blErr := blocklist.IsTokenBlocked(r.Context(), claims.ID)
				if blErr != nil {
					slog.ErrorContext(r.Context(), "token.Auth: blocklist check error",
						"jti", claims.ID, "error", blErr)
					respond.Error(w, http.StatusUnauthorized, "token_revoked",
						"access token has been revoked")
					return
				}
				if blocked {
					respond.Error(w, http.StatusUnauthorized, "token_revoked",
						"access token has been revoked")
					return
				}
			}

			// 3b. Per-user block check (written by reset-password after a successful
			// reset). The value is a Unix timestamp recording when the block was
			// placed. Only tokens whose iat ≤ blockTime are rejected; tokens minted
			// after the reset (fresh logins) have iat > blockTime and pass through.
			// Fail closed on transient errors or unparseable values.
			if userStore != nil {
				blockVal, ubErr := userStore.Get(r.Context(), "pr_blocked_user:"+claims.Subject)
				if ubErr == nil {
					// Key exists — parse the stored timestamp.
					blockUnix, parseErr := strconv.ParseInt(blockVal, 10, 64)
					if parseErr != nil {
						// Unrecognised format (e.g. legacy "1" entry) — fail closed.
						slog.ErrorContext(r.Context(), "token.Auth: user block value unrecognised",
							"user_id", claims.Subject, "value", blockVal)
						respond.Error(w, http.StatusUnauthorized, "token_revoked",
							"access token has been revoked")
						return
					}
					// Block only tokens issued at or before the reset timestamp.
					if claims.IssuedAt == nil || !claims.IssuedAt.Time.After(time.Unix(blockUnix, 0)) {
						respond.Error(w, http.StatusUnauthorized, "token_revoked",
							"access token has been revoked")
						return
					}
					// Token was issued after the reset — not blocked, continue.
				} else if !errors.Is(ubErr, kvstore.ErrNotFound) {
					// Transient error — fail closed.
					slog.ErrorContext(r.Context(), "token.Auth: user block check error",
						"user_id", claims.Subject, "error", ubErr)
					respond.Error(w, http.StatusUnauthorized, "token_revoked",
						"access token has been revoked")
					return
				}
				// ErrNotFound → key absent → not blocked.
			}

			// 4. Inject claims into context and continue.
			ctx := context.WithValue(r.Context(), contextKeyUserID, claims.Subject)
			ctx = context.WithValue(ctx, contextKeySessionID, claims.SessionID)
			ctx = context.WithValue(ctx, contextKeyJTI, claims.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
