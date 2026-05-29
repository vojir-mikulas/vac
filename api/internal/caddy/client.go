package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrUnavailable is returned when the Caddy admin API cannot be reached. The
// proxy package treats this as eventual — a routing push is retried; it never
// fails a running deploy.
var ErrUnavailable = errors.New("caddy: admin API unavailable")

// Client talks to the Caddy Admin API over HTTP. All methods take a context
// with a deadline — a sick Caddy must not hang the caller.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client for the given admin base URL (e.g.
// "http://vac-proxy:2019"). A nil-safe zero timeout falls back to 10s.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Load replaces Caddy's entire config (POST /load). Used on boot to install
// the base config.
func (c *Client) Load(ctx context.Context, cfg *Config) error {
	return c.do(ctx, http.MethodPost, "/load", cfg, nil)
}

// PutRoute creates-or-replaces a single route addressed by its @id. We delete
// any existing route with the id first (best-effort) then append, which is
// idempotent and avoids duplicate routes — simpler than probing existence.
func (c *Client) PutRoute(ctx context.Context, id string, r Route) error {
	r.ID = id
	_ = c.DeleteRoute(ctx, id) // ignore "not found"
	path := fmt.Sprintf("/config/apps/http/servers/%s/routes", ServerName)
	return c.do(ctx, http.MethodPost, path, r, nil)
}

// DeleteRoute removes the route with the given @id. A missing id is not an
// error from the caller's perspective (reconcile relies on this).
func (c *Client) DeleteRoute(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/id/"+id, nil, nil)
}

// GetRoutes reads the current route set from the managed server. Used by
// reconcile to find and prune orphaned vac-route-* entries.
func (c *Client) GetRoutes(ctx context.Context) ([]Route, error) {
	var routes []Route
	path := fmt.Sprintf("/config/apps/http/servers/%s/routes", ServerName)
	if err := c.do(ctx, http.MethodGet, path, nil, &routes); err != nil {
		return nil, err
	}
	return routes, nil
}

// Upstreams returns Caddy's live reverse-proxy upstream pool. proxy.WaitHealthy
// matches a service's dial address here to gate a deploy on health.
func (c *Client) Upstreams(ctx context.Context) ([]UpstreamStatus, error) {
	var ups []UpstreamStatus
	if err := c.do(ctx, http.MethodGet, "/reverse_proxy/upstreams", nil, &ups); err != nil {
		return nil, err
	}
	return ups, nil
}

// Ping checks the admin API is reachable. Used by /health as a soft probe.
func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/config/apps/http/servers/"+ServerName+"/listen", nil, nil)
}

// do performs one admin request. body is JSON-encoded when non-nil; out is
// JSON-decoded when non-nil. Non-2xx responses become errors carrying Caddy's
// message; connection failures wrap ErrUnavailable.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("caddy: marshal body: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("caddy: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("caddy: %s %s: %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("caddy: decode response: %w", err)
		}
	}
	return nil
}
