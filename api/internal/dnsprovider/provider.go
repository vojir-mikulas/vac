// Package dnsprovider creates/deletes DNS records at an operator-configured DNS
// provider so adding a custom domain can auto-create its A record instead of
// making the operator do it by hand (docs/plans/dns-automation-and-byo-cert.md
// Part A). Cloudflare ships first behind a Provider interface so others
// (Route53, RFC2136) can follow.
//
// All outbound HTTP uses an http.Client whose Transport.DialContext is
// netguard.DialContext — non-negotiable per the SSRF invariant: a hostile or
// mistyped API base-URL must not be steered at the cloud metadata service, the
// control-plane database, or loopback.
package dnsprovider

import (
	"context"

	"github.com/vojir-mikulas/vac/api/internal/netguard"
)

// ErrPrivateAddress is re-exported from netguard so callers can match
// (errors.Is) a refusal to reach a private/loopback endpoint without importing
// netguard directly.
var ErrPrivateAddress = netguard.ErrPrivateAddress

// Provider creates or deletes a single DNS record at a provider. EnsureRecord is
// an upsert (create-or-update); DeleteRecord removes the record if present and
// is a no-op when it is already gone.
type Provider interface {
	EnsureRecord(ctx context.Context, zone, name, recordType, value string, proxied bool) error
	DeleteRecord(ctx context.Context, zone, name, recordType string) error
}
