package ratelimit

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// TrustedProxyRealIP returns middleware that rewrites r.RemoteAddr from the
// X-Forwarded-For or X-Real-IP header — but only when the direct TCP peer
// falls inside one of the provided trusted CIDRs.
//
// If the connecting peer is not a trusted proxy the headers are ignored and
// r.RemoteAddr is left unchanged, so forged headers from internet clients can
// never pollute the IP seen by rate-limiters or audit logs.
//
// trustedCIDRs must already be validated (use ParseTrustedProxies). Passing a
// nil or empty slice disables proxy-header rewriting entirely; every request
// will use its raw TCP peer address. This is the correct setting when the
// service is not behind any proxy.
func TrustedProxyRealIP(trustedCIDRs []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trustedCIDRs) > 0 {
				peer := peerIP(r.RemoteAddr)
				switch {
				case peer == nil:
					// RemoteAddr is unparseable — log and leave unchanged.
					slog.WarnContext(r.Context(), "realip: unparseable RemoteAddr — proxy rewrite skipped",
						"remote_addr", r.RemoteAddr)
				case !inCIDRList(peer, trustedCIDRs):
					// Peer is not a trusted proxy — ignore forwarding headers to
					// prevent internet clients from forging their IP.
					// This fires on every direct (non-proxied) connection and is
					// DEBUG level to avoid flooding logs in production.
					slog.DebugContext(r.Context(), "realip: peer not in trusted CIDR list — XFF ignored",
						"peer", peer.String(),
						"xff", r.Header.Get("X-Forwarded-For"))
				default:
					// Trusted proxy: rewrite RemoteAddr from the forwarding header.
					if clientIP := extractClientIP(r); clientIP != "" {
						// Preserve the port field chi/stdlib expect in RemoteAddr.
						// Use port 0 — the real port belongs to the proxy, not the
						// client, and nothing downstream relies on the client port.
						r.RemoteAddr = net.JoinHostPort(clientIP, "0")
						slog.DebugContext(r.Context(), "realip: rewrote RemoteAddr from XFF",
							"peer", peer.String(),
							"xff", r.Header.Get("X-Forwarded-For"),
							"client_ip", clientIP)
					} else {
						slog.DebugContext(r.Context(), "realip: trusted peer but no usable XFF/X-Real-IP — keeping RemoteAddr",
							"peer", peer.String())
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ParseTrustedProxies parses a comma-separated list of CIDR strings (e.g.
// "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16") and returns the parsed networks.
//
// An empty or whitespace-only string returns a nil slice (no trusted proxies).
// Returns an error if any CIDR is malformed so the caller can fail fast at
// startup rather than silently disabling the feature.
func ParseTrustedProxies(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	nets := make([]*net.IPNet, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(p)
		if err != nil {
			return nil, err
		}
		nets = append(nets, ipnet)
	}
	return nets, nil
}

// peerIP parses the host portion of a host:port RemoteAddr string.
// Returns nil when the address is unparseable.
func peerIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// No port — try treating the whole string as a bare IP (tests do this).
		return net.ParseIP(remoteAddr)
	}
	return net.ParseIP(host)
}

// inCIDRList reports whether ip is contained in any of the given networks.
func inCIDRList(ip net.IP, cidrs []*net.IPNet) bool {
	for _, cidr := range cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// extractClientIP returns the left-most (originating) IP from X-Forwarded-For,
// falling back to X-Real-IP, and finally returning "" if neither header is set.
//
// "Left-most" is the value appended by the first proxy that received the
// request from the actual client — subsequent proxies append their own IP to
// the right. This value is only used after verifying the direct TCP peer is a
// trusted proxy, so we accept the IP it claims the client had.
//
// Candidates are validated with net.ParseIP; any non-IP value (e.g. a string
// containing newlines or other control characters) is rejected and the next
// header is tried. This prevents injection of invalid strings into r.RemoteAddr
// and rate-limit store keys.
func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx != -1 {
			xff = xff[:idx]
		}
		xff = strings.TrimSpace(xff)
		if xff != "" && net.ParseIP(xff) != nil {
			return xff
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" && net.ParseIP(xri) != nil {
		return xri
	}
	return ""
}
