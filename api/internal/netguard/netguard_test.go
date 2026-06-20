package netguard

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestIsPrivate(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		// Must be rejected.
		{"nil", "", true},
		{"ipv4 loopback", "127.0.0.1", true},
		{"ipv4 loopback high", "127.255.255.254", true},
		{"ipv6 loopback", "::1", true},
		{"private 10/8", "10.0.0.1", true},
		{"private 172.16/12", "172.16.5.9", true},
		{"private 192.168/16", "192.168.1.1", true},
		{"link-local 169.254 (cloud metadata)", "169.254.169.254", true},
		{"ipv6 link-local", "fe80::1", true},
		{"unspecified v4", "0.0.0.0", true},
		{"unspecified v6", "::", true},
		{"multicast v4", "224.0.0.1", true},
		{"ula v6 (private)", "fc00::1", true},
		{"cgnat low (RFC6598)", "100.64.0.1", true},
		{"cgnat high (RFC6598)", "100.127.255.255", true},

		// Must be allowed — public addresses, including the boundaries that look
		// adjacent to CGNAT but aren't (100.0/100.128 are public).
		{"public dns", "1.1.1.1", false},
		{"public google", "8.8.8.8", false},
		{"public below cgnat", "100.63.255.255", false},
		{"public above cgnat", "100.128.0.1", false},
		{"public v6", "2606:4700:4700::1111", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ip net.IP
			if c.ip != "" {
				ip = net.ParseIP(c.ip)
				if ip == nil {
					t.Fatalf("bad test IP %q", c.ip)
				}
			}
			if got := IsPrivate(ip); got != c.want {
				t.Errorf("IsPrivate(%q) = %v, want %v", c.ip, got, c.want)
			}
		})
	}
}

// TestDialContext_RejectsPrivateLiteral verifies the dialer refuses a host that
// resolves (here, is) a private literal IP without ever attempting a connection,
// and that the failure matches ErrPrivateAddress so callers can detect it.
func TestDialContext_RejectsPrivateLiteral(t *testing.T) {
	dial := DialContext(2*time.Second, 0)
	for _, addr := range []string{
		"127.0.0.1:80",
		"169.254.169.254:80", // the cloud metadata service
		"10.0.0.5:5432",      // a control-plane DB on a private net
		"[::1]:443",
	} {
		conn, err := dial(context.Background(), "tcp", addr)
		if conn != nil {
			_ = conn.Close()
			t.Fatalf("dial(%q): got a connection, want refusal", addr)
		}
		if !errors.Is(err, ErrPrivateAddress) {
			t.Errorf("dial(%q): err = %v, want ErrPrivateAddress", addr, err)
		}
	}
}

// TestDialContext_AllowsPublic verifies a public literal IP is NOT rejected by
// the guard — it passes through to the stdlib dialer. We use TEST-NET-3
// (203.0.113.0/24, reserved-but-public-routable-shaped, no listener) so the dial
// fails with a connect/timeout error rather than ErrPrivateAddress, proving the
// guard let it through. We do not assert a successful connect (the guard's job is
// the rejection, and the connect path is the stdlib dialer).
func TestDialContext_AllowsPublic(t *testing.T) {
	dial := DialContext(500*time.Millisecond, 0)
	conn, err := dial(context.Background(), "tcp", "203.0.113.1:9") // discard port, no listener
	if conn != nil {
		_ = conn.Close()
	}
	if errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("public TEST-NET-3 address wrongly rejected as private: %v", err)
	}
}

// TestDialContext_BadAddr surfaces a malformed addr as a SplitHostPort error,
// not a panic or a silent allow.
func TestDialContext_BadAddr(t *testing.T) {
	dial := DialContext(time.Second, 0)
	_, err := dial(context.Background(), "tcp", "no-port-here")
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("malformed addr: err = %v, want a SplitHostPort error", err)
	}
}
