package google

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// oauthStateCookieName is the cookie set when Redis is unavailable.
// It carries the state payload signed with HMAC-SHA256 so CSRF protection
// is preserved even without a server-side KV store.
const oauthStateCookieName = "goauth_state_fb"

// signedStateCookie is the JSON structure stored inside the fallback cookie.
type signedStateCookie struct {
	State   string `json:"state"`   // the state UUID echoed back in the redirect
	Payload string `json:"payload"` // base64url-encoded OAuthState JSON
	Exp     int64  `json:"exp"`     // unix expiry
	Sig     string `json:"sig"`     // HMAC-SHA256(state+payload+exp, secret)
}

// setStateCookie writes the OAuth state as a signed HttpOnly cookie.
// Used as a fallback when the KV store is unavailable.
//
// Security properties:
//   - HttpOnly: JS cannot read the state, preventing exfiltration.
//   - SameSite=Lax: cookie is sent on cross-origin top-level GET navigations
//     (i.e. the Google redirect back), but not on cross-origin sub-requests.
//   - HMAC signature: prevents an attacker from forging or modifying the state.
//   - Short TTL (kvStateTTL = 10 min): limits replay window.
func setStateCookie(w http.ResponseWriter, state, rawPayload string, secure bool, secret string) error {
	exp := time.Now().Add(kvStateTTL).Unix()
	payloadB64 := base64.RawURLEncoding.EncodeToString([]byte(rawPayload))
	sig := computeStateSig(state, payloadB64, exp, secret)

	cookie := signedStateCookie{
		State:   state,
		Payload: payloadB64,
		Exp:     exp,
		Sig:     sig,
	}
	cookieJSON, err := json.Marshal(cookie)
	if err != nil {
		return err
	}
	encoded := base64.RawURLEncoding.EncodeToString(cookieJSON)

	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    encoded,
		Path:     "/",
		MaxAge:   int(kvStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// readStateCookie reads and verifies the fallback state cookie.
// Returns the raw OAuthState JSON and nil on success.
// Returns an error if the cookie is absent, expired, or the signature is invalid.
func readStateCookie(r *http.Request, state, secret string) (string, error) {
	c, err := r.Cookie(oauthStateCookieName)
	if err != nil {
		return "", errors.New("state cookie absent")
	}

	cookieJSON, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return "", errors.New("state cookie malformed")
	}

	var cookie signedStateCookie
	if err := json.Unmarshal(cookieJSON, &cookie); err != nil {
		return "", errors.New("state cookie unmarshal failed")
	}

	// State must match the query param — this is the CSRF check.
	if cookie.State != state {
		return "", errors.New("state mismatch")
	}

	// Expiry check.
	if time.Now().Unix() > cookie.Exp {
		return "", errors.New("state cookie expired")
	}

	// Signature check — constant-time comparison prevents timing attacks.
	expected := computeStateSig(cookie.State, cookie.Payload, cookie.Exp, secret)
	if !hmac.Equal([]byte(cookie.Sig), []byte(expected)) {
		return "", errors.New("state cookie signature invalid")
	}

	rawPayload, err := base64.RawURLEncoding.DecodeString(cookie.Payload)
	if err != nil {
		return "", errors.New("state cookie payload malformed")
	}
	return string(rawPayload), nil
}

// clearStateCookie deletes the fallback state cookie on callback, regardless
// of whether it was used, to keep the browser cookie jar clean.
func clearStateCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// computeStateSig returns HMAC-SHA256(state + "|" + payloadB64 + "|" + exp, secret)
// encoded as base64url.
func computeStateSig(state, payloadB64 string, exp int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(state))
	mac.Write([]byte("|"))
	mac.Write([]byte(payloadB64))
	mac.Write([]byte("|"))
	mac.Write([]byte{byte(exp >> 56), byte(exp >> 48), byte(exp >> 40), byte(exp >> 32),
		byte(exp >> 24), byte(exp >> 16), byte(exp >> 8), byte(exp)})
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
