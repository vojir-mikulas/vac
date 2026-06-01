package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ErrPrivateAddress is returned by the dispatcher's dialer when a webhook URL
// resolves to a loopback, private, or link-local address. Exported so callers
// can match on it when surfacing the failure to the operator.
var ErrPrivateAddress = errors.New("notify: webhook host resolves to a private/loopback/link-local address")

// SettingsStore reads the stored notification settings.
type SettingsStore interface {
	GetNotificationSettings(ctx context.Context) (store.NotificationSettingsRow, error)
}

// Dispatcher renders events and POSTs them to the configured channels.
type Dispatcher struct {
	store        SettingsStore
	box          *crypto.Box
	http         *http.Client
	envDiscord   string
	envSlack     string
	baseURL      string // public base for deep links, e.g. "https://vac.example.com"
	backoff      time.Duration
	logger       *slog.Logger
	blockPrivate bool // SSRF guard: refuse to dial private/loopback/link-local addresses
}

// New wires a dispatcher. envDiscord/envSlack are the VAC_NOTIFY_* overrides
// (empty when unset); box decrypts stored URLs (nil disables stored URLs).
// The returned dispatcher refuses to dial private/loopback/link-local
// addresses (SSRF guard); tests that point at httptest servers must clear
// `blockPrivate`.
func New(s SettingsStore, box *crypto.Box, envDiscord, envSlack, baseURL string, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	d := &Dispatcher{
		store:        s,
		box:          box,
		envDiscord:   strings.TrimSpace(envDiscord),
		envSlack:     strings.TrimSpace(envSlack),
		baseURL:      strings.TrimRight(baseURL, "/"),
		backoff:      time.Second,
		logger:       logger,
		blockPrivate: true,
	}
	d.http = &http.Client{
		Timeout:   5 * time.Second,
		Transport: d.transport(),
	}
	return d
}

// transport returns an http.Transport whose DialContext rejects connections to
// private/loopback/link-local addresses when blockPrivate is set. The check
// runs after DNS resolution so a public hostname pointing at 127.0.0.1 (or
// 169.254.169.254) is still blocked — closing the obvious SSRF window.
func (d *Dispatcher) transport() *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if d.blockPrivate {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if isPrivateAddr(ip) {
					return nil, fmt.Errorf("%w: %s", ErrPrivateAddress, ip.String())
				}
			}
		}
		return dialer.DialContext(ctx, network, addr)
	}
	return base
}

// isPrivateAddr reports whether ip is loopback, private, link-local, multicast,
// or one of the special-purpose ranges (e.g. IPv4-mapped IPv6).
func isPrivateAddr(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() {
		return true
	}
	// 100.64.0.0/10 — RFC 6598 carrier-grade NAT (also used by cloud metadata
	// front-ends like Tailscale). Treat as private.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xC0 == 64 {
		return true
	}
	return false
}

// resolved is the effective config at dispatch time.
type resolved struct {
	discordURL string
	slackURL   string
	events     map[string]bool
}

// enabled reports whether an event should fire. A toggle absent from the map
// defaults to on.
func (r resolved) enabled(t EventType) bool {
	if v, ok := r.events[string(t)]; ok {
		return v
	}
	return true
}

func (d *Dispatcher) resolve(ctx context.Context) resolved {
	row, err := d.store.GetNotificationSettings(ctx)
	if err != nil {
		d.logger.Warn("notify: load settings", "err", err)
	}
	out := resolved{discordURL: d.envDiscord, slackURL: d.envSlack, events: map[string]bool{}}
	if out.discordURL == "" {
		out.discordURL = d.open(row.DiscordURLEnc)
	}
	if out.slackURL == "" {
		out.slackURL = d.open(row.SlackURLEnc)
	}
	if len(row.Events) > 0 {
		if err := json.Unmarshal(row.Events, &out.events); err != nil {
			// Falling through means every event is treated as enabled — log so
			// the operator knows the toggle row is corrupt and is being
			// ignored. (Recoverable; just degrades to defaults.)
			d.logger.Warn("notify: events toggle map is malformed; defaulting to all events on", "err", err)
		}
	}
	return out
}

func (d *Dispatcher) open(enc []byte) string {
	if len(enc) == 0 || d.box == nil {
		return ""
	}
	pt, err := d.box.Open(enc)
	if err != nil {
		d.logger.Warn("notify: decrypt webhook url", "err", err)
		return ""
	}
	return string(pt)
}

// dispatch resolves config, checks the toggle, and POSTs to each configured
// channel on a detached goroutine so callers never block.
func (d *Dispatcher) dispatch(ev Event) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cfg := d.resolve(ctx)
		if !cfg.enabled(ev.Type) {
			return
		}
		if cfg.discordURL != "" {
			d.post(ctx, cfg.discordURL, discordPayload(ev, d.baseURL))
		}
		if cfg.slackURL != "" {
			d.post(ctx, cfg.slackURL, slackPayload(ev, d.baseURL))
		}
	}()
}

// post sends one JSON payload with bounded retry. 2xx is success; other codes
// and transport errors are retried up to 3 times with linear backoff.
func (d *Dispatcher) post(ctx context.Context, url string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		d.logger.Warn("notify: marshal payload", "err", err)
		return
	}
	const attempts = 3
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(i) * d.backoff):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := d.http.Do(req)
		if err != nil {
			// SSRF rejection is a permanent error — don't waste retries on it.
			if errors.Is(err, ErrPrivateAddress) {
				d.logger.Warn("notify: refused webhook to private address", "err", err)
				return
			}
			d.logger.Warn("notify: post failed", "attempt", i+1, "err", err)
			continue
		}
		code := resp.StatusCode
		_ = resp.Body.Close()
		if code >= 200 && code < 300 {
			return
		}
		d.logger.Warn("notify: webhook non-2xx", "attempt", i+1, "status", code)
	}
}

// SendTest posts a synthetic ping to every configured channel and returns the
// number of channels reached plus an error if none were configured. Synchronous
// so the settings UI can report success.
func (d *Dispatcher) SendTest(ctx context.Context) (int, error) {
	cfg := d.resolve(ctx)
	ev := Event{Type: EventVACRestarted, Title: "VAC test notification", Message: "If you can read this, your webhook is configured correctly.", OK: true}
	n := 0
	if cfg.discordURL != "" {
		d.post(ctx, cfg.discordURL, discordPayload(ev, d.baseURL))
		n++
	}
	if cfg.slackURL != "" {
		d.post(ctx, cfg.slackURL, slackPayload(ev, d.baseURL))
		n++
	}
	if n == 0 {
		return 0, fmt.Errorf("notify: no channels configured")
	}
	return n, nil
}

// --- Typed entry points the producers call ---

// DeploySucceeded fires the deploy-succeeded event.
func (d *Dispatcher) DeploySucceeded(appName, appID, sha, msg string, dur time.Duration) {
	d.dispatch(Event{
		Type: EventDeploySucceeded, OK: true,
		Title:   "Deploy succeeded: " + appName,
		AppName: appName, AppID: appID, Commit: sha, Message: msg, Duration: dur,
	})
}

// DeployFailed fires the deploy-failed event.
func (d *Dispatcher) DeployFailed(appName, appID, errMsg string, dur time.Duration) {
	d.dispatch(Event{
		Type: EventDeployFailed, OK: false,
		Title:   "Deploy failed: " + appName,
		AppName: appName, AppID: appID, Message: errMsg, Duration: dur,
	})
}

// CrashLoop fires the crash-loop event.
func (d *Dispatcher) CrashLoop(appName, appID, service string, restarts int, exitCode *int) {
	msg := fmt.Sprintf("%s restarted %d times and was stopped", service, restarts)
	if exitCode != nil {
		msg += fmt.Sprintf(" (last exit code %d)", *exitCode)
	}
	d.dispatch(Event{
		Type: EventCrashLoop, OK: false,
		Title:   "Crash loop: " + appName + "/" + service,
		AppName: appName, AppID: appID, Service: service, Message: msg,
	})
}

// OOMKilled fires the out-of-memory event. Distinct from CrashLoop: it means a
// container hit its memory limit and was killed by the kernel, which a RAM-limit
// bump usually fixes — so the message points at that rather than at the code.
func (d *Dispatcher) OOMKilled(appName, appID, service string, limitMB int) {
	msg := service + " was killed for exceeding its memory limit"
	if limitMB > 0 {
		msg = fmt.Sprintf("%s exceeded its %d MiB memory limit and was killed", service, limitMB)
	}
	d.dispatch(Event{
		Type: EventOOMKilled, OK: false,
		Title:   "Out of memory: " + appName + "/" + service,
		AppName: appName, AppID: appID, Service: service, Message: msg,
	})
}

// VACRestarted fires the control-plane-restarted event.
func (d *Dispatcher) VACRestarted() {
	d.dispatch(Event{
		Type: EventVACRestarted, OK: true,
		Title:   "VAC restarted",
		Message: "The VAC control plane is back up.",
	})
}
