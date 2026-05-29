package reqmetrics

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Scraper reads Caddy's Prometheus /metrics for a host-level aggregate request
// count. This is a Phase 4 seam — per-service attribution comes from the access
// log (Caddy's Prometheus metrics carry no host/upstream label). Kept minimal:
// a hand parse of one counter, no prometheus client dependency.
type Scraper struct {
	url  string
	http *http.Client
}

func NewScraper(metricsURL string, client *http.Client) *Scraper {
	if client == nil {
		client = http.DefaultClient
	}
	return &Scraper{url: metricsURL, http: client}
}

// TotalRequests fetches and sums caddy_http_requests_total across all label
// sets. Returns the aggregate count.
func (s *Scraper) TotalRequests(ctx context.Context) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("reqmetrics: scrape %s: %d", s.url, resp.StatusCode)
	}
	return SumCounter(resp.Body, "caddy_http_requests_total")
}

// SumCounter sums every sample of the named Prometheus counter in the exposition
// text, ignoring HELP/TYPE lines and label sets.
func SumCounter(r io.Reader, metric string) (float64, error) {
	var total float64
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, metric) {
			continue
		}
		// Skip if the next char after the metric name is part of a longer name.
		rest := line[len(metric):]
		if rest != "" && rest[0] != ' ' && rest[0] != '{' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		total += v
	}
	return total, scanner.Err()
}
