package google

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	oauthshared "github.com/7-Dany/store/backend/internal/domain/oauth/shared"
	"github.com/7-Dany/store/backend/internal/platform/kvstore"
	"github.com/7-Dany/store/backend/internal/platform/respond"
	"github.com/7-Dany/store/backend/internal/platform/token"
)

const kvStateTTL = 10 * time.Minute

// Recorder is the narrow observability interface for the Google OAuth handler.
// *telemetry.Registry satisfies this interface structurally.
type Recorder interface {
	OnOAuthSuccess(provider string)
	OnOAuthFailed(provider string, reason string)
	OnOAuthLinked(provider string)
	OnOAuthUnlinked(provider string)
}

// Handler is the HTTP layer for Google OAuth: initiate, callback, and unlink.
type Handler struct {
	svc           Servicer
	recorder      Recorder
	kv            kvstore.Store
	cfg           token.JWTConfig
	clientID      string
	redirectURI   string
	successURL    string
	errorURL      string
	secureCookies bool
	stateSecret   string // HMAC key for the cookie fallback when KV is unavailable
}

// NewHandler constructs a Handler with the given dependencies.
func NewHandler(
	svc Servicer,
	kv kvstore.Store,
	cfg token.JWTConfig,
	clientID, redirectURI, successURL, errorURL string,
	secureCookies bool,
	recorder Recorder,
) *Handler {
	return &Handler{
		svc:           svc,
		recorder:      recorder,
		kv:            kv,
		cfg:           cfg,
		clientID:      clientID,
		redirectURI:   redirectURI,
		successURL:    successURL,
		errorURL:      errorURL,
		secureCookies: secureCookies,
		// Use the JWT access secret as the HMAC key for the cookie fallback.
		// It is already present, has high entropy, and is rotated alongside tokens.
		stateSecret: cfg.JWTAccessSecret,
	}
}

// redirectError redirects to h.errorURL with ?error=<code>.
func (h *Handler) redirectError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, h.errorURL+"?error="+code, http.StatusFound)
}

// HandleInitiate handles GET /oauth/google.
//
// Guard ordering (Stage 0 §7.1):
//  1. Best-effort JWT parse from Authorization header → link_user_id.
//  2. Generate state UUID.
//  3. Generate PKCE code_verifier (32 random bytes, base64url).
//  4. Derive code_challenge = base64url(sha256(code_verifier)).
//  5. KV set "goauth:state:<state>", TTL=10 min. Failure → 500.
//  6. Build Google authorization URL.
//  7. Redirect 302.
func (h *Handler) HandleInitiate(w http.ResponseWriter, r *http.Request) {
	// 1. Best-effort: extract link_user_id from Authorization header.
	// Any parse error is silently ignored and link_user_id is left empty.
	linkUserID := ""
	if raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && raw != "" {
		if claims, err := token.ParseAccessToken(raw, h.cfg.JWTAccessSecret); err == nil {
			linkUserID = claims.Subject
		}
	}

	// 2. Generate state UUID.
	state := uuid.New().String()

	// 3. Generate PKCE code_verifier: 32 random bytes, base64url-encoded.
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		log.Error(r.Context(), "HandleInitiate: rand.Read failed", "error", err)
		respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// 4. Derive code_challenge = base64url(sha256(code_verifier)).
	sum := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// 5. Persist state — primary path: KV store. Fallback: signed HttpOnly cookie.
	// The cookie fallback preserves CSRF protection via HMAC-SHA256 even when
	// Redis is unavailable, so OAuth keeps working during a Redis outage.
	statePayload, _ := json.Marshal(OAuthState{CodeVerifier: codeVerifier, LinkUserID: linkUserID})
	kvKey := "goauth:state:" + state
	if err := h.kv.Set(r.Context(), kvKey, string(statePayload), kvStateTTL); err != nil {
		log.Warn(r.Context(), "HandleInitiate: kv set failed, falling back to signed cookie", "error", err)
		if cookieErr := setStateCookie(w, state, string(statePayload), h.secureCookies, h.stateSecret); cookieErr != nil {
			log.Error(r.Context(), "HandleInitiate: cookie fallback also failed", "error", cookieErr)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
	} else {
		log.Debug(r.Context(), "HandleInitiate: state stored in KV",
			"state", state,
			"link_user_id", linkUserID,
			"ttl", kvStateTTL,
		)
	}

	// 6 + 7. Build URL and redirect.
	authURL := buildAuthURL(h.clientID, h.redirectURI, state, codeChallenge)
	log.Debug(r.Context(), "HandleInitiate: redirecting to Google")
	http.Redirect(w, r, authURL, http.StatusFound)
}

// buildAuthURL constructs the Google OAuth 2.0 authorization URL with all
// required PKCE and OIDC parameters.
func buildAuthURL(clientID, redirectURI, state, codeChallenge string) string {
	return fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth"+
			"?client_id=%s"+
			"&redirect_uri=%s"+
			"&response_type=code"+
			"&scope=openid+email+profile"+
			"&state=%s"+
			"&code_challenge=%s"+
			"&code_challenge_method=S256",
		clientID, redirectURI, state, codeChallenge,
	)
}

// HandleCallback handles GET /oauth/google/callback.
//
// Pre-service guard ordering (Stage 0 §7.2, delete moved before unmarshal):
//  1. error param present → redirect oauth_cancelled.
//  2. state param absent → redirect invalid_state.
//  3. KV get state → not found → redirect invalid_state.
//  4. KV del — non-fatal; runs before unmarshal (single-use contract).
//  5. Unmarshal KV value.
//  6. code param absent → redirect invalid_state.
//  7. svc.HandleCallback → error switch → redirect; success → cookies + redirect.
func (h *Handler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// 1. Google signalled an error (e.g. user denied consent).
	if q.Get("error") != "" {
		h.redirectError(w, r, "oauth_cancelled")
		return
	}

	// 2. state param must be present.
	state := q.Get("state")
	if state == "" {
		h.redirectError(w, r, "invalid_state")
		return
	}

	// 3. Load state — primary path: KV store. Fallback: signed cookie.
	// Always clear the fallback cookie on callback so it doesn't linger.
	kvKey := "goauth:state:" + state
	raw, kvErr := h.kv.Get(r.Context(), kvKey)
	var usingCookieFallback bool
	if kvErr != nil {
		// KV unavailable or key not found — try the signed cookie fallback.
		cookieRaw, cookieErr := readStateCookie(r, state, h.stateSecret)
		if cookieErr != nil {
			log.Warn(r.Context(), "HandleCallback: state not found in KV or cookie",
				"kv_err", kvErr, "cookie_err", cookieErr)
			clearStateCookie(w, h.secureCookies)
			h.redirectError(w, r, "invalid_state")
			return
		}
		raw = cookieRaw
		usingCookieFallback = true
		log.Debug(r.Context(), "HandleCallback: resolved state from signed cookie fallback")
	}

	// 4. Delete state entry (single-use contract).
	// Always clear the fallback cookie; also delete from KV when it was used.
	clearStateCookie(w, h.secureCookies)
	if !usingCookieFallback {
		if err := h.kv.Delete(r.Context(), kvKey); err != nil {
			log.Warn(r.Context(), "HandleCallback: delete state key failed", "error", err)
		}
	}

	// 5. Unmarshal state payload.
	var oauthState OAuthState
	if err := json.Unmarshal([]byte(raw), &oauthState); err != nil {
		h.redirectError(w, r, "invalid_state")
		return
	}

	// 6. code param must be present.
	code := q.Get("code")
	if code == "" {
		h.redirectError(w, r, "invalid_state")
		return
	}

	// 7. Delegate to service.
	log.Debug(r.Context(), "HandleCallback: state resolved, delegating to service",
		"has_link_user_id", oauthState.LinkUserID != "",
		"ip", respond.ClientIP(r),
	)
	result, err := h.svc.HandleCallback(r.Context(), CallbackInput{
		Code:         code,
		CodeVerifier: oauthState.CodeVerifier,
		LinkUserID:   oauthState.LinkUserID,
		IPAddress:    respond.ClientIP(r),
		UserAgent:    r.UserAgent(),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrTokenExchangeFailed):
			h.recorder.OnOAuthFailed("google", "token_exchange_failed")
			h.redirectError(w, r, "token_exchange_failed")
		case errors.Is(err, ErrInvalidIDToken):
			h.recorder.OnOAuthFailed("google", "provider_error")
			h.redirectError(w, r, "invalid_id_token")
		case errors.Is(err, oauthshared.ErrProviderAlreadyLinked):
			h.redirectError(w, r, "provider_already_linked")
		case errors.Is(err, oauthshared.ErrAccountLocked):
			h.redirectError(w, r, "account_locked")
		default:
			h.recorder.OnOAuthFailed("google", "unknown")
			log.Error(r.Context(), "HandleCallback: service error", "error", err)
			h.redirectError(w, r, "server_error")
		}
		return
	}

	// ── Link mode: no session cookies, redirect with action=linked ────────────
	if result.Linked {
		h.recorder.OnOAuthLinked("google")
		log.Debug(r.Context(), "HandleCallback: link mode — redirecting",
			"success_url", h.successURL,
		)
		http.Redirect(w, r, h.successURL+"?provider=google&action=linked", http.StatusFound)
		return
	}

	// ── Login / Register mode: mint tokens, set cookies, redirect ─────────────
	log.Debug(r.Context(), "HandleCallback: minting tokens",
		"new_user", result.NewUser,
		"user_id", fmt.Sprintf("%x", result.Session.UserID),
		"session_id", fmt.Sprintf("%x", result.Session.SessionID),
	)
	mintResult, mintErr := token.MintTokens(w, token.MintTokensInput{
		UserID:        result.Session.UserID,
		SessionID:     result.Session.SessionID,
		RefreshJTI:    result.Session.RefreshJTI,
		FamilyID:      result.Session.FamilyID,
		RefreshExpiry: result.Session.RefreshExpiry,
	}, h.cfg)
	if mintErr != nil {
		h.recorder.OnOAuthFailed("google", "unknown")
		log.Error(r.Context(), "HandleCallback: mint tokens failed", "error", mintErr)
		h.redirectError(w, r, "server_error")
		return
	}

	// Short-lived, non-HttpOnly cookie so proxy.ts can read and promote it.
	// SameSite=Lax (not Strict) is required here: the browser follows a redirect
	// from localhost:8080 → localhost:3000, which is a cross-origin navigation.
	// SameSite=Strict suppresses the cookie on cross-origin redirects, so proxy.ts
	// never sees it. Lax allows the cookie on top-level GET navigations / redirects.
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_access_token",
		Value:    mintResult.AccessToken,
		Path:     "/",
		MaxAge:   30,
		HttpOnly: false,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	// token.MintTokens already set the refresh_token HttpOnly cookie via SetRefreshCookie.

	h.recorder.OnOAuthSuccess("google")
	log.Debug(r.Context(), "HandleCallback: cookies set, redirecting to frontend",
		"success_url", h.successURL,
		"new_user", result.NewUser,
	)
	http.Redirect(w, r, h.successURL+"?provider=google", http.StatusFound)
}

// HandleUnlink handles DELETE /oauth/google/unlink.
//
// Guard ordering (Stage 0 §7.3):
//  1. userID from JWT context — zero value → 401 unauthorized.
//  2. svc.UnlinkGoogle.
//  3. Error switch → JSON error; success → 200 JSON message.
func (h *Handler) HandleUnlink(w http.ResponseWriter, r *http.Request) {
	// 1. Require authenticated user (injected by JWTAuth middleware).
	userIDStr, ok := token.UserIDFromContext(r.Context())
	if !ok || userIDStr == "" {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "missing authentication")
		return
	}

	parsed, err := uuid.Parse(userIDStr)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "invalid user identity")
		return
	}

	// 2. Delegate to service.
	if err := h.svc.UnlinkGoogle(r.Context(), [16]byte(parsed), respond.ClientIP(r), r.UserAgent()); err != nil {
		switch {
		case errors.Is(err, oauthshared.ErrIdentityNotFound):
			respond.Error(w, http.StatusNotFound, "not_found", "google account not linked")
		case errors.Is(err, oauthshared.ErrLastAuthMethod):
			respond.Error(w, http.StatusUnprocessableEntity, "last_auth_method", err.Error())
		default:
			log.Error(r.Context(), "HandleUnlink: service error", "error", err)
			respond.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}

	// 3. Success.
	h.recorder.OnOAuthUnlinked("google")
	respond.JSON(w, http.StatusOK, map[string]string{"message": "google account unlinked successfully"})
}
