// Package certprobe reads the leaf TLS certificate a host is currently served,
// the same way a browser sees it: a TLS handshake to the proxy with the host's
// SNI, reading the served leaf certificate. The handshake never aborts on an
// untrusted chain (so we can still read a self-signed/staging cert's expiry),
// but the result reports separately whether the chain *would* be trusted by a
// browser — see Result.Trusted.
//
// It is the single TLS-observation implementation shared by two consumers
// (plan 09 §4): certcheck (daily expiry alerts — reads NotAfter only) and
// domainstatus (frequent "is a *trusted* cert being served right now" status).
// Keeping it in one place stops the two from drifting.
package certprobe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"time"
)

// Result is one observation of the leaf certificate a host is served.
type Result struct {
	// NotAfter is the served leaf certificate's expiry, read regardless of
	// whether the chain is trusted (so staging/self-signed certs still report an
	// expiry for the cert-expiry alerter).
	NotAfter time.Time
	// Trusted reports whether the served leaf chains to a system root AND matches
	// the requested host — i.e. whether a browser would accept it. A cert that is
	// served but untrusted (staging CA, self-signed fallback, wrong host) is
	// NotAfter-readable but Trusted == false.
	Trusted bool
}

// Func reads the leaf certificate observation for a host. A probe error (host
// unreachable, no cert yet) is non-fatal to the caller — it means "no cert
// served right now".
type Func func(ctx context.Context, host string) (Result, error)

// ErrNoPeerCert is returned when the handshake produced no peer certificate
// (should not happen against Caddy, but guarded).
var ErrNoPeerCert = errors.New("certprobe: no peer certificate presented")

// New returns a Func that TLS-dials proxyAddr (e.g. "vac-proxy:443") with the
// host as SNI and returns the served leaf certificate observation. timeout
// bounds each handshake (default 10s).
func New(proxyAddr string, timeout time.Duration) Func {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return func(ctx context.Context, host string) (Result, error) {
		dialer := &tls.Dialer{
			NetDialer: &net.Dialer{Timeout: timeout},
			Config: &tls.Config{
				ServerName: host,
				// Read the cert even when the chain doesn't verify — we need to read
				// a self-signed/staging cert's expiry, and we judge trust ourselves
				// below rather than letting the handshake abort.
				InsecureSkipVerify: true, //nolint:gosec // trust is judged explicitly via x509.Verify below
				MinVersion:         tls.VersionTLS12,
			},
		}
		conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = conn.Close() }()
		state := conn.(*tls.Conn).ConnectionState()
		if len(state.PeerCertificates) == 0 {
			return Result{}, ErrNoPeerCert
		}
		leaf := state.PeerCertificates[0]
		res := Result{NotAfter: leaf.NotAfter}

		// Trust check: verify the served chain against the system roots and the
		// requested host, exactly as a browser would. nil Roots ⇒ system roots;
		// zero CurrentTime ⇒ time.Now(). This is what stops the status engine from
		// reporting `active` off Caddy's self-signed fallback or a staging cert.
		intermediates := x509.NewCertPool()
		for _, c := range state.PeerCertificates[1:] {
			intermediates.AddCert(c)
		}
		if _, verr := leaf.Verify(x509.VerifyOptions{
			DNSName:       host,
			Intermediates: intermediates,
		}); verr == nil {
			res.Trusted = true
		}
		return res, nil
	}
}
