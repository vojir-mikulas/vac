// Package proxy translates VAC domains and services into Caddy routes and
// manages the vac-edge network that Caddy uses to reach app containers. It is
// the orchestration seam between the store, the caddy transport, and the
// docker CLI.
package proxy

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidHostname is returned by NormalizeHostname for anything that isn't a
// plain DNS hostname (wildcards, ports, paths, single labels, bad characters).
var ErrInvalidHostname = errors.New("proxy: invalid hostname")

// AutoSubdomain derives the automatic hostname for a service under the
// configured base domain. Single-HTTP-service apps get `{slug}.{base}`; apps
// that expose more than one HTTP service get `{service}.{slug}.{base}` so the
// hostnames don't collide. Returns "" when baseDomain is unset (automatic
// subdomains disabled).
func AutoSubdomain(appSlug, serviceName, baseDomain string, multiService bool) string {
	if baseDomain == "" {
		return ""
	}
	if multiService {
		return fmt.Sprintf("%s.%s.%s", serviceName, appSlug, baseDomain)
	}
	return fmt.Sprintf("%s.%s", appSlug, baseDomain)
}

// NormalizeHostname lower-cases, trims, and validates a custom hostname. It
// rejects wildcards, embedded ports, paths, single-label names, and any
// character outside the LDH (letters/digits/hyphen) set. ASCII only — IDNs
// must be supplied already punycode-encoded.
func NormalizeHostname(raw string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(raw))
	h = strings.TrimSuffix(h, ".")

	switch {
	case h == "":
		return "", fmt.Errorf("%w: empty", ErrInvalidHostname)
	case strings.ContainsAny(h, " \t\n\r"):
		return "", fmt.Errorf("%w: contains whitespace", ErrInvalidHostname)
	case strings.Contains(h, "*"):
		return "", fmt.Errorf("%w: wildcards not allowed", ErrInvalidHostname)
	case strings.ContainsAny(h, "/:?#@"):
		return "", fmt.Errorf("%w: must not contain a scheme, port, or path", ErrInvalidHostname)
	case !strings.Contains(h, "."):
		return "", fmt.Errorf("%w: must be a fully-qualified domain", ErrInvalidHostname)
	case len(h) > 253:
		return "", fmt.Errorf("%w: too long", ErrInvalidHostname)
	}

	for _, label := range strings.Split(h, ".") {
		if err := validateLabel(label); err != nil {
			return "", err
		}
	}
	return h, nil
}

func validateLabel(label string) error {
	if label == "" {
		return fmt.Errorf("%w: empty label", ErrInvalidHostname)
	}
	if len(label) > 63 {
		return fmt.Errorf("%w: label too long", ErrInvalidHostname)
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return fmt.Errorf("%w: label must not start or end with a hyphen", ErrInvalidHostname)
	}
	for _, c := range label {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return fmt.Errorf("%w: invalid character %q", ErrInvalidHostname, c)
		}
	}
	return nil
}
