package handler

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIPString returns the originating client IP for the request as a bare
// host string (no port), or "" if it can't be determined.
//
// When proxy headers are trusted (TrustProxyHeaders, default true — the bundled
// vac-proxy is the single hop in front of vac-api), the RIGHTMOST entry of
// X-Forwarded-For is used: that's the peer address the trusted proxy actually
// observed the connection from and appended itself, so a client cannot forge it
// (anything a client puts in the header lands to the left of the proxy's
// append). This assumes exactly one trusted proxy hop, which is VAC's topology.
//
// When proxy headers are NOT trusted (a raw-HTTP box with no proxy in front),
// the header is attacker-spoofable, so only the real TCP peer in RemoteAddr is
// believed.
//
// Using RemoteAddr unconditionally — as the rate limiter previously did — is
// wrong behind the proxy: RemoteAddr is then vac-proxy's container IP for every
// request, collapsing all clients into a single bucket.
func ClientIPString(r *http.Request) string {
	if trustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := parseHostIP(strings.TrimSpace(parts[len(parts)-1])); ip != "" {
				return ip
			}
		}
	}
	return remoteHost(r)
}

// remoteHost strips the port off RemoteAddr.
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// parseHostIP normalizes a single XFF entry to a bare IP string, tolerating an
// accidental :port. Returns "" if it isn't a valid IP.
func parseHostIP(s string) string {
	if addr, err := netip.ParseAddr(s); err == nil {
		return addr.String()
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.String()
		}
	}
	return ""
}
