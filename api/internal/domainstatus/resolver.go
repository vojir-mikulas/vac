package domainstatus

import (
	"context"
	"net"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Resolver is the subset of *net.Resolver the engine uses. Abstracted so tests
// inject a deterministic fake instead of dialling real DNS.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
}

// PublicResolver dials a public recursive resolver (e.g. "1.1.1.1:53") directly
// rather than going through the box's local stub/systemd-resolved cache (plan 09
// F3 §2). The local cache respects TTL and won't see a freshly-changed record
// until the old one expires, so a domain the operator just pointed here would
// read awaiting_dns for minutes. A public recursive resolver still honours
// authoritative TTL but bypasses the local cache, seeing changes as soon as the
// operator's DNS provider serves them. addr empty falls back to the system
// resolver (the documented escape hatch when egress to public DNS is blocked).
func PublicResolver(addr string) *net.Resolver {
	if addr == "" {
		return net.DefaultResolver
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 4 * time.Second}
			return d.DialContext(ctx, network, addr)
		},
	}
}

// isApex reports whether host is its own registrable domain (eTLD+1) — an apex,
// which must use an A record (CNAME-at-apex is invalid). Uses the public-suffix
// list so example.co.uk is treated as apex, not co.uk. Falls back to a
// label-count heuristic only if the host can't be parsed.
func isApex(host string) bool {
	etld1, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return strings.Count(host, ".") <= 1
	}
	return strings.EqualFold(etld1, host)
}
