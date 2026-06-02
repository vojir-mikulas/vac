// Package certprobe reads the leaf TLS certificate a host is currently served,
// the same way a browser sees it: a TLS handshake to the proxy with the host's
// SNI, reading the served leaf certificate's NotAfter. Trust is irrelevant — we
// only need the metadata — so verification is skipped.
//
// It is the single TLS-observation implementation shared by two consumers
// (plan 09 §4): certcheck (daily expiry alerts) and domainstatus (frequent
// "is a cert being served right now" status). Keeping it in one place stops the
// two from drifting.
package certprobe

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"
)

// Func reads the leaf certificate's NotAfter for a host. A probe error (host
// unreachable, no cert yet) is non-fatal to the caller — it means "no cert
// served right now".
type Func func(ctx context.Context, host string) (time.Time, error)

// ErrNoPeerCert is returned when the handshake produced no peer certificate
// (should not happen against Caddy, but guarded).
var ErrNoPeerCert = errors.New("certprobe: no peer certificate presented")

// New returns a Func that TLS-dials proxyAddr (e.g. "vac-proxy:443") with the
// host as SNI and returns the served leaf certificate's NotAfter. timeout bounds
// each handshake (default 10s).
func New(proxyAddr string, timeout time.Duration) Func {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return func(ctx context.Context, host string) (time.Time, error) {
		dialer := &tls.Dialer{
			NetDialer: &net.Dialer{Timeout: timeout},
			Config: &tls.Config{
				ServerName: host,
				// We read NotAfter only; whether the cert chains to a trusted root
				// is irrelevant to "when does it expire" / "is one served". Skipping
				// verification also lets us read a self-signed/staging cert.
				InsecureSkipVerify: true, //nolint:gosec // metadata read, not a trust decision
				MinVersion:         tls.VersionTLS12,
			},
		}
		conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
		if err != nil {
			return time.Time{}, err
		}
		defer conn.Close()
		state := conn.(*tls.Conn).ConnectionState()
		if len(state.PeerCertificates) == 0 {
			return time.Time{}, ErrNoPeerCert
		}
		return state.PeerCertificates[0].NotAfter, nil
	}
}
