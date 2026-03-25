package events

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
)

// computeSID returns HMAC-SHA256(key, "{len}:{sessionID}:{jti}") as a hex string.
//
// The length prefix prevents second-preimage attacks in the rare case where
// sessionID contains a colon character. Both IssueToken and
// VerifyAndConsumeToken MUST use this function to guarantee the HMAC roundtrip.
func computeSID(key, sessionID, jti string) string {
	msg := fmt.Sprintf("%d:%s:%s", len(sessionID), sessionID, jti)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

// computeIPClaim returns "{ip}/24" for IPv4 when bindIP is true, and "" otherwise.
//
// IPv6 addresses and the disabled-binding case always return "". An unparseable
// clientIP also returns "" so that downstream validation is well-defined.
func computeIPClaim(clientIP string, bindIP bool) string {
	if !bindIP || clientIP == "" {
		return ""
	}
	addr, err := netip.ParseAddr(clientIP)
	if err != nil || !addr.Is4() {
		return ""
	}
	// Mask to /24 network address.
	prefix, err := addr.Prefix(24)
	if err != nil {
		return ""
	}
	return prefix.Masked().String()
}

// computeJTIHash returns HMAC-SHA256(serverSecret, jti) as a hex string.
// Written to sse_token_issuances.jti_hash for GDPR-IP audit trail.
func computeJTIHash(jti, serverSecret string) string {
	mac := hmac.New(sha256.New, []byte(serverSecret))
	mac.Write([]byte(jti))
	return hex.EncodeToString(mac.Sum(nil))
}

// computeIPHash returns SHA256(clientIP + "|" + dailyRotationKey) as a hex pointer.
// Returns nil when clientIP is empty (IP unavailable or IPv6 not tracked).
//
// The daily rotation key limits IP-hash validity to a 24-hour window, reducing
// re-identification risk while still allowing intra-day GDPR erasure queries.
func computeIPHash(clientIP, dailyRotationKey string) *string {
	if clientIP == "" {
		return nil
	}
	h := sha256.New()
	h.Write([]byte(clientIP))
	h.Write([]byte("|"))
	h.Write([]byte(dailyRotationKey))
	s := hex.EncodeToString(h.Sum(nil))
	return &s
}
