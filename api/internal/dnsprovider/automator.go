package dnsprovider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ProviderCloudflare is the only provider value supported today.
const ProviderCloudflare = "cloudflare"

// SettingsStore reads the instance-wide DNS-provider settings.
type SettingsStore interface {
	GetDNSSettings(ctx context.Context) (store.DNSSettings, error)
}

// KeyOpener decrypts the sealed API token. *crypto.Box satisfies it.
type KeyOpener interface {
	Open(sealed []byte) ([]byte, error)
}

// Outcome describes what the automator did for a domain, for surfacing in the
// API response / a toast. Attempted is false when automation is off or
// unconfigured (the operator just adds the record by hand, as before).
type Outcome struct {
	Attempted bool   `json:"attempted"`
	Created   bool   `json:"created"`
	Detail    string `json:"detail,omitempty"`
}

// Automator ties the instance DNS settings, the SSRF-guarded provider client,
// and the VPS IP together. It is the seam the domain handler calls; a DNS
// failure here never fails domain creation (the manual-record fallback stays).
type Automator struct {
	enabled bool
	store   SettingsStore
	box     KeyOpener
	vpsIP   string
	logger  *slog.Logger
	// newProvider builds a Provider for a (provider-name, token). Injectable so
	// tests substitute a fake without real Cloudflare egress.
	newProvider func(provider, token string) (Provider, error)
}

// NewAutomator wires an Automator. enabled is the VAC_DNS_AUTOMATION flag; when
// false Enabled() reports false and the handler hides/404s the feature. vpsIP is
// the address records are pointed at (the same one domainstatus matches against).
func NewAutomator(enabled bool, s SettingsStore, box KeyOpener, vpsIP string, logger *slog.Logger) *Automator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Automator{
		enabled:     enabled,
		store:       s,
		box:         box,
		vpsIP:       vpsIP,
		logger:      logger,
		newProvider: defaultNewProvider,
	}
}

// Enabled reports whether DNS automation is switched on at the config layer.
func (a *Automator) Enabled() bool { return a.enabled }

// EnsureDomainRecord creates (or updates) the DNS record pointing hostname at the
// VPS. It always writes an A record to the VPS IP with proxied=false — a record
// to a literal IP can only be an A record, and Cloudflare's orange cloud would
// break Caddy's ACME HTTP challenge. Returns Outcome{Attempted:false} when
// automation is off or unconfigured.
func (a *Automator) EnsureDomainRecord(ctx context.Context, hostname string) (Outcome, error) {
	provider, settings, ok, err := a.provider(ctx)
	if err != nil || !ok {
		return Outcome{}, err
	}
	if err := provider.EnsureRecord(ctx, settings.Zone, hostname, "A", a.vpsIP, false); err != nil {
		return Outcome{Attempted: true}, err
	}
	detail := fmt.Sprintf("Created A record %s → %s at %s.", hostname, a.vpsIP, settings.Provider)
	a.logger.Info("dns: created record", "host", hostname, "ip", a.vpsIP, "provider", settings.Provider)
	return Outcome{Attempted: true, Created: true, Detail: detail}, nil
}

// DeleteDomainRecord removes the A record for hostname. Best-effort; an
// already-absent record is success.
func (a *Automator) DeleteDomainRecord(ctx context.Context, hostname string) (Outcome, error) {
	provider, settings, ok, err := a.provider(ctx)
	if err != nil || !ok {
		return Outcome{}, err
	}
	if err := provider.DeleteRecord(ctx, settings.Zone, hostname, "A"); err != nil {
		return Outcome{Attempted: true}, err
	}
	a.logger.Info("dns: deleted record", "host", hostname, "provider", settings.Provider)
	return Outcome{Attempted: true, Detail: fmt.Sprintf("Deleted A record %s.", hostname)}, nil
}

// provider builds a Provider from the stored settings, returning ok=false when
// automation is off or not configured (no error — that's the normal "do it by
// hand" path). It validates the VPS IP and token up front.
func (a *Automator) provider(ctx context.Context) (Provider, store.DNSSettings, bool, error) {
	if !a.enabled {
		return nil, store.DNSSettings{}, false, nil
	}
	settings, err := a.store.GetDNSSettings(ctx)
	if err != nil {
		return nil, store.DNSSettings{}, false, err
	}
	if !settings.Configured() {
		return nil, store.DNSSettings{}, false, nil
	}
	if strings.TrimSpace(a.vpsIP) == "" {
		return nil, settings, true, errors.New("no public IP is configured for VAC to point the record at (set VAC_PUBLIC_IP)")
	}
	if a.box == nil {
		return nil, settings, true, errors.New("encryption is not configured (VAC_MASTER_KEY); cannot decrypt the DNS token")
	}
	token, err := a.box.Open(settings.TokenEnc)
	if err != nil {
		return nil, settings, true, fmt.Errorf("could not decrypt the DNS provider token: %w", err)
	}
	p, err := a.newProvider(settings.Provider, string(token))
	if err != nil {
		return nil, settings, true, err
	}
	return p, settings, true, nil
}

func defaultNewProvider(provider, token string) (Provider, error) {
	switch provider {
	case ProviderCloudflare:
		return NewCloudflare(token), nil
	default:
		return nil, fmt.Errorf("dnsprovider: unsupported provider %q", provider)
	}
}
