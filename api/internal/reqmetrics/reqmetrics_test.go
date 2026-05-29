package reqmetrics

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakeStore struct {
	domains []store.Domain
	flushed []store.RequestBucket
}

func (f *fakeStore) ListAllDomains(_ context.Context) ([]store.Domain, error) { return f.domains, nil }
func (f *fakeStore) UpsertRequestBuckets(_ context.Context, b []store.RequestBucket) error {
	f.flushed = append(f.flushed, b...)
	return nil
}

func TestCollector_RecordAndFlush(t *testing.T) {
	s := &fakeStore{domains: []store.Domain{
		{AppID: "a1", ServiceName: "web", Hostname: "blog.example.com"},
	}}
	c := New(s, "/nonexistent.log", time.Minute, nil)
	c.refreshHosts(context.Background())

	// Three requests to a known host, one a 5xx; one request to an unknown host.
	c.record("blog.example.com", 200, 100)
	c.record("blog.example.com:443", 502, 200) // :port stripped, counts as error
	c.record("blog.example.com", 204, 50)
	c.record("unknown.example.com", 200, 999) // dropped

	c.flush(context.Background())

	if len(s.flushed) != 1 {
		t.Fatalf("expected 1 bucket, got %d (%+v)", len(s.flushed), s.flushed)
	}
	b := s.flushed[0]
	if b.AppID != "a1" || b.ServiceName != "web" {
		t.Errorf("bucket service wrong: %+v", b)
	}
	if b.Requests != 3 {
		t.Errorf("requests = %d, want 3", b.Requests)
	}
	if b.Errors != 1 {
		t.Errorf("errors = %d, want 1", b.Errors)
	}
	if b.BytesOut != 350 {
		t.Errorf("bytes_out = %d, want 350", b.BytesOut)
	}
}

func TestCollector_HandleLine(t *testing.T) {
	s := &fakeStore{domains: []store.Domain{{AppID: "a1", ServiceName: "web", Hostname: "blog.example.com"}}}
	c := New(s, "/nonexistent.log", time.Minute, nil)
	c.refreshHosts(context.Background())

	c.handleLine([]byte(`{"request":{"host":"blog.example.com"},"status":200,"size":42}`))
	c.handleLine([]byte(`not json`))                // ignored
	c.handleLine([]byte(`{"request":{"host":""}}`)) // no host, ignored

	c.flush(context.Background())
	if len(s.flushed) != 1 || s.flushed[0].Requests != 1 || s.flushed[0].BytesOut != 42 {
		t.Errorf("flushed = %+v", s.flushed)
	}
}

func TestSumCounter(t *testing.T) {
	const body = `# HELP caddy_http_requests_total Counter
# TYPE caddy_http_requests_total counter
caddy_http_requests_total{server="vac",code="200"} 12
caddy_http_requests_total{server="vac",code="404"} 3
caddy_http_requests_in_flight 5
`
	got, err := SumCounter(strings.NewReader(body), "caddy_http_requests_total")
	if err != nil {
		t.Fatal(err)
	}
	if got != 15 {
		t.Errorf("SumCounter = %v, want 15", got)
	}
}
