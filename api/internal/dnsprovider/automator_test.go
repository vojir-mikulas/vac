package dnsprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakeSettings struct {
	s   store.DNSSettings
	err error
}

func (f fakeSettings) GetDNSSettings(context.Context) (store.DNSSettings, error) {
	return f.s, f.err
}

// fakeOpener returns the sealed bytes verbatim (no real crypto) so the automator
// gets a deterministic "token".
type fakeOpener struct{}

func (fakeOpener) Open(b []byte) ([]byte, error) { return b, nil }

type fakeProvider struct {
	zone, name, recordType, value string
	proxied                       bool
	ensureErr                     error
	deleted                       bool
}

func (f *fakeProvider) EnsureRecord(_ context.Context, zone, name, recordType, value string, proxied bool) error {
	f.zone, f.name, f.recordType, f.value, f.proxied = zone, name, recordType, value, proxied
	return f.ensureErr
}
func (f *fakeProvider) DeleteRecord(_ context.Context, _, _, _ string) error {
	f.deleted = true
	return nil
}

func configured() store.DNSSettings {
	return store.DNSSettings{Provider: ProviderCloudflare, TokenEnc: []byte("tok"), Zone: "example.com"}
}

func TestAutomatorEnsureCreatesUnproxiedA(t *testing.T) {
	fp := &fakeProvider{}
	a := NewAutomator(true, fakeSettings{s: configured()}, fakeOpener{}, "1.2.3.4", nil)
	a.newProvider = func(string, string) (Provider, error) { return fp, nil }

	out, err := a.EnsureDomainRecord(context.Background(), "app.example.com")
	if err != nil {
		t.Fatalf("EnsureDomainRecord: %v", err)
	}
	if !out.Attempted || !out.Created {
		t.Fatalf("outcome = %+v", out)
	}
	if fp.recordType != "A" || fp.value != "1.2.3.4" || fp.proxied {
		t.Errorf("provider got type=%q value=%q proxied=%v; want A/1.2.3.4/false", fp.recordType, fp.value, fp.proxied)
	}
	if fp.name != "app.example.com" || fp.zone != "example.com" {
		t.Errorf("provider got name=%q zone=%q", fp.name, fp.zone)
	}
}

func TestAutomatorDisabledOrUnconfigured(t *testing.T) {
	// Disabled: never attempts.
	off := NewAutomator(false, fakeSettings{s: configured()}, fakeOpener{}, "1.2.3.4", nil)
	if out, err := off.EnsureDomainRecord(context.Background(), "app.example.com"); err != nil || out.Attempted {
		t.Errorf("disabled should be a no-op; out=%+v err=%v", out, err)
	}
	// Enabled but no provider configured: also a no-op (manual fallback).
	bare := NewAutomator(true, fakeSettings{s: store.DNSSettings{}}, fakeOpener{}, "1.2.3.4", nil)
	if out, err := bare.EnsureDomainRecord(context.Background(), "app.example.com"); err != nil || out.Attempted {
		t.Errorf("unconfigured should be a no-op; out=%+v err=%v", out, err)
	}
}

func TestAutomatorPropagatesPrivateAddress(t *testing.T) {
	fp := &fakeProvider{ensureErr: ErrPrivateAddress}
	a := NewAutomator(true, fakeSettings{s: configured()}, fakeOpener{}, "1.2.3.4", nil)
	a.newProvider = func(string, string) (Provider, error) { return fp, nil }
	out, err := a.EnsureDomainRecord(context.Background(), "app.example.com")
	if !out.Attempted {
		t.Error("attempt should be recorded even on failure")
	}
	if !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("expected ErrPrivateAddress, got %v", err)
	}
}

func TestAutomatorRequiresVPSIP(t *testing.T) {
	a := NewAutomator(true, fakeSettings{s: configured()}, fakeOpener{}, "", nil)
	a.newProvider = func(string, string) (Provider, error) { return &fakeProvider{}, nil }
	if _, err := a.EnsureDomainRecord(context.Background(), "app.example.com"); err == nil {
		t.Error("expected an error when no VPS IP is configured")
	}
}
