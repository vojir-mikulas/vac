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

func TestCollector_AutoHosts(t *testing.T) {
	// No custom-domain rows; the app is reached only via its derived
	// auto-subdomain, which has no domains row.
	s := &fakeStore{}
	c := New(s, "/nonexistent.log", time.Minute, nil)
	c.SetAutoHostSource(func(_ context.Context) ([]AutoHost, error) {
		return []AutoHost{{Hostname: "myapp.vac.example.com", AppID: "a1", ServiceName: "web"}}, nil
	})
	c.refreshHosts(context.Background())

	c.record("myapp.vac.example.com", 200, 100)
	c.record("myapp.vac.example.com:443", 200, 50) // :port stripped
	c.record("other.vac.example.com", 200, 999)    // unknown auto host, dropped

	c.flush(context.Background())

	if len(s.flushed) != 1 {
		t.Fatalf("expected 1 bucket, got %d (%+v)", len(s.flushed), s.flushed)
	}
	b := s.flushed[0]
	if b.AppID != "a1" || b.ServiceName != "web" || b.Requests != 2 || b.BytesOut != 150 {
		t.Errorf("bucket wrong: %+v", b)
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

func TestCollector_ObserverHook(t *testing.T) {
	s := &fakeStore{domains: []store.Domain{{AppID: "a1", ServiceName: "web", Hostname: "blog.example.com"}}}
	c := New(s, "/nonexistent.log", time.Minute, nil)
	c.refreshHosts(context.Background())

	var observed []AccessLine
	c.SetObserver(func(l AccessLine) { observed = append(observed, l) })

	c.handleLine([]byte(`{"request":{"host":"blog.example.com","client_ip":"1.2.3.4","uri":"/x","headers":{"User-Agent":["curl/8"]}},"status":200,"size":42}`))
	c.handleLine([]byte(`not json`))                // ignored, no observe
	c.handleLine([]byte(`{"request":{"host":""}}`)) // no host, no observe

	// The hook fires once for the valid line and sees the enriched fields.
	if len(observed) != 1 {
		t.Fatalf("observed = %d, want 1", len(observed))
	}
	if observed[0].IP() != "1.2.3.4" || observed[0].UserAgent() != "curl/8" || observed[0].Request.URI != "/x" {
		t.Errorf("observed line missing enriched fields: %+v", observed[0])
	}

	// Aggregation is unchanged by the hook.
	c.flush(context.Background())
	if len(s.flushed) != 1 || s.flushed[0].Requests != 1 || s.flushed[0].BytesOut != 42 {
		t.Errorf("aggregation altered by observer: %+v", s.flushed)
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
