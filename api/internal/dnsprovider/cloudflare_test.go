package dnsprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// cfServer is a minimal fake Cloudflare API: one zone, and an in-memory record
// table keyed by type+name. It records the last create/update body.
func cfServer(t *testing.T) (*httptest.Server, *cfRecordBody) {
	t.Helper()
	var lastBody cfRecordBody
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	}
	mux.HandleFunc("/zones", func(w http.ResponseWriter, r *http.Request) {
		ok(w, []cfZone{{ID: "zone1"}})
	})
	mux.HandleFunc("/zones/zone1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			ok(w, []cfRecord{}) // no existing record ⇒ POST path
		case http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&lastBody)
			ok(w, cfRecord{ID: "rec1"})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &lastBody
}

func TestCloudflareEnsureRecordCreatesUnproxiedA(t *testing.T) {
	srv, lastBody := cfServer(t)
	c := NewCloudflare("token")
	c.baseURL = srv.URL
	c.blockPrivate = false // reach the httptest server on loopback

	if err := c.EnsureRecord(context.Background(), "example.com", "app.example.com", "A", "1.2.3.4", false); err != nil {
		t.Fatalf("EnsureRecord: %v", err)
	}
	if lastBody.Type != "A" {
		t.Errorf("type = %q, want A", lastBody.Type)
	}
	if lastBody.Name != "app.example.com" {
		t.Errorf("name = %q", lastBody.Name)
	}
	if lastBody.Content != "1.2.3.4" {
		t.Errorf("content = %q", lastBody.Content)
	}
	if lastBody.Proxied {
		t.Error("proxied must be false — the orange cloud breaks Caddy's ACME HTTP challenge")
	}
}

func TestCloudflareRefusesPrivateEndpoint(t *testing.T) {
	// With the SSRF guard on (the production default), a base URL resolving to a
	// loopback address must be refused with ErrPrivateAddress.
	c := NewCloudflare("token")
	c.baseURL = "http://127.0.0.1:0"
	err := c.EnsureRecord(context.Background(), "example.com", "app.example.com", "A", "1.2.3.4", false)
	if !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("expected ErrPrivateAddress, got %v", err)
	}
}

func TestCloudflareSurfacesAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/zones", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors":  []cfError{{Code: 9109, Message: "Invalid access token"}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewCloudflare("bad")
	c.baseURL = srv.URL
	c.blockPrivate = false
	err := c.EnsureRecord(context.Background(), "example.com", "app.example.com", "A", "1.2.3.4", false)
	if err == nil {
		t.Fatal("expected an API error")
	}
}
