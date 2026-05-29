package proxy

import (
	"errors"
	"testing"
)

func TestAutoSubdomain(t *testing.T) {
	tests := []struct {
		name      string
		slug, svc string
		base      string
		multi     bool
		want      string
	}{
		{"single service", "blog", "web", "vac.example.com", false, "blog.vac.example.com"},
		{"multi service", "shop", "api", "vac.example.com", true, "api.shop.vac.example.com"},
		{"no base domain", "blog", "web", "", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AutoSubdomain(tt.slug, tt.svc, tt.base, tt.multi); got != tt.want {
				t.Errorf("AutoSubdomain = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeHostname_OK(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Example.COM", "example.com"},
		{"www.acme.com.", "www.acme.com"},
		{"  app.vac.example.com  ", "app.vac.example.com"},
		{"a-b.c-d.io", "a-b.c-d.io"},
	}
	for _, tt := range tests {
		got, err := NormalizeHostname(tt.in)
		if err != nil {
			t.Errorf("NormalizeHostname(%q) error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeHostname(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeHostname_Rejects(t *testing.T) {
	bad := []string{
		"",
		"localhost",          // single label
		"*.example.com",      // wildcard
		"example.com/path",   // path
		"example.com:8080",   // port
		"http://example.com", // scheme
		"exa mple.com",       // whitespace
		"-bad.example.com",   // leading hyphen
		"bad-.example.com",   // trailing hyphen
		"under_score.example.com",
	}
	for _, in := range bad {
		if _, err := NormalizeHostname(in); !errors.Is(err, ErrInvalidHostname) {
			t.Errorf("NormalizeHostname(%q) err = %v, want ErrInvalidHostname", in, err)
		}
	}
}
