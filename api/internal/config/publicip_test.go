package config

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsPublicIP(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"203.0.113.10", true},
		{"192.168.1.5", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"127.0.0.1", false},
		{"100.64.0.1", false},  // CGNAT
		{"169.254.0.1", false}, // link-local
		{"0.0.0.0", false},     // unspecified
		{"", false},
		{"not-an-ip", false},
		{"2606:4700:4700::1111", true}, // public IPv6
		{"fc00::1", false},             // unique-local IPv6
		{"::1", false},                 // IPv6 loopback
	}
	for _, c := range cases {
		if got := isPublicIP(c.in); got != c.want {
			t.Errorf("isPublicIP(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPublicIPFrom_ReturnsPublicBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("203.0.113.7\n"))
	}))
	defer srv.Close()

	if got := publicIPFrom(srv.Client(), []string{srv.URL}); got != "203.0.113.7" {
		t.Fatalf("publicIPFrom = %q, want 203.0.113.7", got)
	}
}

func TestPublicIPFrom_RejectsPrivateBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("192.168.1.10\n"))
	}))
	defer srv.Close()

	if got := publicIPFrom(srv.Client(), []string{srv.URL}); got != "" {
		t.Fatalf("publicIPFrom = %q, want empty (private rejected)", got)
	}
}

func TestPublicIPFrom_FallsThroughToSecondURL(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("garbage"))
	}))
	defer bad.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("203.0.113.42"))
	}))
	defer good.Close()

	// good.Client() trusts good's TLS (plain HTTP test servers share defaults).
	if got := publicIPFrom(good.Client(), []string{bad.URL, good.URL}); got != "203.0.113.42" {
		t.Fatalf("publicIPFrom = %q, want 203.0.113.42 (second URL)", got)
	}
}

func TestPublicIPFrom_AllFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if got := publicIPFrom(srv.Client(), []string{srv.URL}); got != "" {
		t.Fatalf("publicIPFrom = %q, want empty (all failed)", got)
	}
}
