// Package certupload validates an operator-supplied TLS certificate + private
// key before VAC seals the key and hands the pair to Caddy (bring-your-own-cert,
// docs/plans/dns-automation-and-byo-cert.md Part B). It is a single-responsibility
// validator, like certprobe: it parses, checks the pair matches, checks the host
// is covered, and rejects an expired cert — returning parsed metadata for the UI.
//
// It never touches the network and never writes anything; the caller seals the
// key (crypto.Box) and persists the result.
package certupload

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Meta is the parsed certificate metadata returned to the caller (surfaced in
// the UI so the operator can confirm what they uploaded).
type Meta struct {
	// Subject is the leaf certificate's subject common name (may be empty for
	// SAN-only certs).
	Subject string
	// DNSNames are the leaf's subject-alternative DNS names (plus the CN when it
	// looks like a hostname), i.e. every host the cert is valid for.
	DNSNames []string
	// NotBefore / NotAfter bound the validity window.
	NotBefore time.Time
	NotAfter  time.Time
	// Issuer is the issuing CA's common name; empty for a self-signed cert.
	Issuer string
	// SelfSigned reports whether the leaf is self-signed (issuer == subject and
	// it is its own signer). Allowed (operator's choice for internal hosts) but
	// surfaced as a warning, not a hard reject.
	SelfSigned bool
}

// ErrExpired is returned when the uploaded leaf certificate is already expired.
var ErrExpired = errors.New("certupload: certificate has already expired")

// ErrHostNotCovered is returned when the certificate does not cover the domain's
// hostname (no matching SAN/CN, exact or wildcard).
var ErrHostNotCovered = errors.New("certupload: certificate does not cover this hostname")

// Validate parses certPEM + keyPEM, confirms they form a matching pair, that the
// leaf covers hostname (exact or wildcard), and that it is not already expired.
// It returns parsed metadata on success. A self-signed cert is accepted (the
// returned Meta.SelfSigned flags it) so an operator can serve an internal host.
//
// hostname must already be normalized (lower-case, no trailing dot) by the
// caller — it is compared case-insensitively against the cert's names.
func Validate(certPEM, keyPEM []byte, hostname string) (Meta, error) {
	// X509KeyPair both parses the chain and proves the key matches the leaf — a
	// mismatched key fails here with a clear error, so we don't re-implement the
	// signature check.
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return Meta{}, fmt.Errorf("certupload: cert and key do not form a valid pair: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return Meta{}, errors.New("certupload: no certificate found in PEM")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return Meta{}, fmt.Errorf("certupload: parse leaf certificate: %w", err)
	}

	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	if host == "" {
		return Meta{}, errors.New("certupload: hostname is required")
	}

	now := time.Now()
	if now.After(leaf.NotAfter) {
		return Meta{}, fmt.Errorf("%w (expired %s)", ErrExpired, leaf.NotAfter.Format("2006-01-02"))
	}

	if !covers(leaf, host) {
		return Meta{}, fmt.Errorf("%w %q (covers: %s)", ErrHostNotCovered, host, strings.Join(certNames(leaf), ", "))
	}

	meta := Meta{
		Subject:    leaf.Subject.CommonName,
		DNSNames:   certNames(leaf),
		NotBefore:  leaf.NotBefore,
		NotAfter:   leaf.NotAfter,
		Issuer:     leaf.Issuer.CommonName,
		SelfSigned: isSelfSigned(leaf),
	}
	return meta, nil
}

// certNames is every hostname the leaf is valid for: its SANs, plus the subject
// CN when the CN looks like a hostname and isn't already a SAN (legacy certs).
func certNames(leaf *x509.Certificate) []string {
	names := make([]string, 0, len(leaf.DNSNames)+1)
	seen := map[string]bool{}
	for _, n := range leaf.DNSNames {
		n = strings.ToLower(n)
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	if cn := strings.ToLower(leaf.Subject.CommonName); cn != "" && strings.Contains(cn, ".") && !seen[cn] {
		names = append(names, cn)
	}
	return names
}

// covers reports whether the leaf certificate is valid for host: an exact
// (case-insensitive) match against any name, or a single-label wildcard match
// (`*.example.com` covers `a.example.com` but not `example.com` or
// `a.b.example.com`), per RFC 6125.
func covers(leaf *x509.Certificate, host string) bool {
	for _, name := range certNames(leaf) {
		if name == host {
			return true
		}
		if strings.HasPrefix(name, "*.") {
			suffix := name[1:] // ".example.com"
			rest := strings.TrimSuffix(host, suffix)
			// host must end with the wildcard suffix and the remaining left label
			// must be exactly one non-empty, dot-free label.
			if rest != host && rest != "" && !strings.Contains(rest, ".") {
				return true
			}
		}
	}
	return false
}

// isSelfSigned reports whether the leaf's issuer DN equals its subject DN — the
// standard self-signed heuristic. (CheckSignatureFrom is too strict here: it
// also requires the signer to be a CA, which a self-signed leaf usually isn't.)
func isSelfSigned(leaf *x509.Certificate) bool {
	return bytes.Equal(leaf.RawIssuer, leaf.RawSubject)
}

// FirstCertBlock reports whether b contains at least one PEM CERTIFICATE block —
// a cheap shape check the handler can use before the fuller Validate, to map an
// obviously-non-PEM upload to a 400 with a clear message.
func FirstCertBlock(b []byte) bool {
	for {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			return false
		}
		if block.Type == "CERTIFICATE" {
			return true
		}
	}
}
