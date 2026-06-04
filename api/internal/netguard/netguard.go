// Package netguard provides an SSRF-hardened dialer for outbound HTTP to
// user-controlled URLs (notification webhooks, S3-compatible backup endpoints).
//
// The control plane lets operators point those features at arbitrary URLs, so a
// hostile (or merely mistyped) endpoint could otherwise be steered at the cloud
// metadata service (169.254.169.254), the control-plane database (vac-db), or
// loopback. The dialer here closes that window two ways:
//
//   - it resolves the hostname and refuses to connect if ANY resolved address is
//     loopback/private/link-local/CGNAT, and
//   - it dials the validated literal IP, so there is no second, unchecked DNS
//     resolution between the check and the connect (DNS-rebinding / TOCTOU).
//
// TLS SNI/verification still uses the hostname from the request URL, because
// net/http computes the TLS ServerName from the original address before calling
// DialContext — swapping the dial target to an IP does not affect it.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// ErrPrivateAddress is returned by the dialer when a host resolves to a
// loopback, private, link-local, or carrier-grade-NAT address. Callers can match
// on it (errors.Is) to surface a clear "refused private address" failure and to
// skip pointless retries.
var ErrPrivateAddress = errors.New("netguard: host resolves to a private/loopback/link-local address")

// IsPrivate reports whether ip is loopback, private, link-local, multicast,
// unspecified, or carrier-grade NAT (RFC 6598) — i.e. anything an outbound
// webhook/backup request to a public service should never reach.
func IsPrivate(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() {
		return true
	}
	// 100.64.0.0/10 — RFC 6598 carrier-grade NAT (also used by cloud metadata
	// front-ends like Tailscale). Treat as private.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xC0 == 64 {
		return true
	}
	return false
}

// DialContext returns a net/http DialContext that rejects private destinations
// and dials the validated literal IP. timeout/keepAlive tune the underlying
// dialer. The returned function is safe for concurrent use.
func DialContext(timeout, keepAlive time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: keepAlive}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("netguard: no addresses for %q", host)
		}
		// Reject if ANY resolved address is private — a multi-A record that mixes
		// a public and an internal address must not be allowed to fall through.
		for _, ip := range ips {
			if IsPrivate(ip) {
				return nil, fmt.Errorf("%w: %s", ErrPrivateAddress, ip.String())
			}
		}
		// Dial the validated literal IPs directly: no second resolution can swap
		// in a private address between the check above and the connect.
		var dialErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err != nil {
				dialErr = err
				continue
			}
			return conn, nil
		}
		return nil, dialErr
	}
}
