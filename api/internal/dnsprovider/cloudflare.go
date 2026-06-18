package dnsprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/netguard"
)

// cloudflareAPI is the default Cloudflare API v4 base. Overridable for tests.
const cloudflareAPI = "https://api.cloudflare.com/client/v4"

// Cloudflare implements Provider against the Cloudflare API v4 using a
// token-scoped API token (Zone:DNS:Edit). All requests go through the
// SSRF-guarded transport.
type Cloudflare struct {
	token   string
	baseURL string
	http    *http.Client
	// blockPrivate gates the SSRF guard. Always true in production; tests pointing
	// at an httptest server on loopback clear it after construction (mirrors
	// notify.Dispatcher). The private-address-refusal test keeps it true.
	blockPrivate bool
}

// NewCloudflare returns a Cloudflare provider for the given API token.
func NewCloudflare(token string) *Cloudflare {
	c := &Cloudflare{
		token:        strings.TrimSpace(token),
		baseURL:      cloudflareAPI,
		blockPrivate: true,
	}
	c.http = &http.Client{Timeout: 10 * time.Second, Transport: c.transport()}
	return c
}

// transport rejects connections to private/loopback/link-local addresses when
// blockPrivate is set, dialing the validated literal IP so a low-TTL DNS rebind
// can't swap in an internal address after the check (mirrors notify.Dispatcher).
func (c *Cloudflare) transport() *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	guarded := netguard.DialContext(5*time.Second, 30*time.Second)
	plain := (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if c.blockPrivate {
			return guarded(ctx, network, addr)
		}
		return plain(ctx, network, addr)
	}
	return base
}

// cfEnvelope is the common Cloudflare response wrapper.
type cfEnvelope struct {
	Success bool            `json:"success"`
	Errors  []cfError       `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e cfError) String() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

type cfZone struct {
	ID string `json:"id"`
}

type cfRecord struct {
	ID string `json:"id"`
}

type cfRecordBody struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

// EnsureRecord upserts an A/CNAME record. It looks up the zone id by name, then
// the existing record by name+type, PUTting it when present and POSTing it
// otherwise. proxied=false is required for VAC's hosts — Cloudflare's orange
// cloud terminates TLS itself and breaks Caddy's ACME HTTP challenge.
func (c *Cloudflare) EnsureRecord(ctx context.Context, zone, name, recordType, value string, proxied bool) error {
	zoneID, err := c.zoneID(ctx, zone)
	if err != nil {
		return err
	}
	recordID, err := c.findRecord(ctx, zoneID, name, recordType)
	if err != nil {
		return err
	}
	body := cfRecordBody{Type: recordType, Name: name, Content: value, TTL: 1, Proxied: proxied}
	if recordID == "" {
		return c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", body, nil)
	}
	return c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+recordID, body, nil)
}

// DeleteRecord removes the record with the given name+type. A record that is
// already absent is not an error.
func (c *Cloudflare) DeleteRecord(ctx context.Context, zone, name, recordType string) error {
	zoneID, err := c.zoneID(ctx, zone)
	if err != nil {
		return err
	}
	recordID, err := c.findRecord(ctx, zoneID, name, recordType)
	if err != nil {
		return err
	}
	if recordID == "" {
		return nil
	}
	return c.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+recordID, nil, nil)
}

// zoneID resolves a zone name to its Cloudflare id.
func (c *Cloudflare) zoneID(ctx context.Context, zone string) (string, error) {
	var zones []cfZone
	if err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(zone), nil, &zones); err != nil {
		return "", err
	}
	if len(zones) == 0 {
		return "", fmt.Errorf("dnsprovider: cloudflare zone %q not found (check the zone name and token scope)", zone)
	}
	return zones[0].ID, nil
}

// findRecord returns the id of the record matching name+type, or "" if none.
func (c *Cloudflare) findRecord(ctx context.Context, zoneID, name, recordType string) (string, error) {
	var recs []cfRecord
	path := fmt.Sprintf("/zones/%s/dns_records?type=%s&name=%s", zoneID, url.QueryEscape(recordType), url.QueryEscape(name))
	if err := c.do(ctx, http.MethodGet, path, nil, &recs); err != nil {
		return "", err
	}
	if len(recs) == 0 {
		return "", nil
	}
	return recs[0].ID, nil
}

// do performs one Cloudflare API request, decoding the envelope and surfacing
// API-level failures (success:false) as errors. out, when non-nil, receives the
// decoded `result`.
func (c *Cloudflare) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("dnsprovider: marshal body: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("dnsprovider: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Surface a netguard refusal unwrapped-matchable so callers (errors.Is)
		// can report "refused private address".
		return fmt.Errorf("dnsprovider: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env cfEnvelope
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("dnsprovider: decode response (%d): %w", resp.StatusCode, err)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !env.Success {
		return fmt.Errorf("dnsprovider: cloudflare %s %s: %d: %s", method, redactPath(path), resp.StatusCode, joinErrors(env.Errors))
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("dnsprovider: decode result: %w", err)
		}
	}
	return nil
}

func joinErrors(errs []cfError) string {
	if len(errs) == 0 {
		return "unknown error"
	}
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, e.String())
	}
	return strings.Join(parts, "; ")
}

// redactPath strips query strings from a path for error messages (the zone-name
// query is harmless, but record-id paths needn't be echoed verbatim).
func redactPath(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}
