package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// SettingsStore reads the stored notification settings.
type SettingsStore interface {
	GetNotificationSettings(ctx context.Context) (store.NotificationSettingsRow, error)
}

// Dispatcher renders events and POSTs them to the configured channels.
type Dispatcher struct {
	store      SettingsStore
	box        *crypto.Box
	http       *http.Client
	envDiscord string
	envSlack   string
	baseURL    string // public base for deep links, e.g. "https://vac.example.com"
	backoff    time.Duration
	logger     *slog.Logger
}

// New wires a dispatcher. envDiscord/envSlack are the VAC_NOTIFY_* overrides
// (empty when unset); box decrypts stored URLs (nil disables stored URLs).
func New(s SettingsStore, box *crypto.Box, envDiscord, envSlack, baseURL string, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{
		store:      s,
		box:        box,
		http:       &http.Client{Timeout: 5 * time.Second},
		envDiscord: strings.TrimSpace(envDiscord),
		envSlack:   strings.TrimSpace(envSlack),
		baseURL:    strings.TrimRight(baseURL, "/"),
		backoff:    time.Second,
		logger:     logger,
	}
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
		_ = json.Unmarshal(row.Events, &out.events)
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

// VACRestarted fires the control-plane-restarted event.
func (d *Dispatcher) VACRestarted() {
	d.dispatch(Event{
		Type: EventVACRestarted, OK: true,
		Title:   "VAC restarted",
		Message: "The VAC control plane is back up.",
	})
}
