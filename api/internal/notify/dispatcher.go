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
	"strconv"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/netguard"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ErrPrivateAddress is returned by the dispatcher's dialer when a webhook URL
// resolves to a loopback, private, or link-local address. Aliased to the shared
// netguard error so existing callers (and post()'s retry-skip) keep matching.
var ErrPrivateAddress = netguard.ErrPrivateAddress

// SettingsStore reads the stored notification settings.
type SettingsStore interface {
	GetNotificationSettings(ctx context.Context) (store.NotificationSettingsRow, error)
}

// SMTPEnv carries the VAC_NOTIFY_SMTP_* overrides passed to New (empty fields
// when unset). It mirrors envDiscord/envSlack but spans the relay's several
// settings; Password is env-only, never read from the config file.
type SMTPEnv struct {
	Host, Port, Username, Password, From, To, TLSMode string
}

// Dispatcher renders events and POSTs them to the configured channels.
type Dispatcher struct {
	store        SettingsStore
	box          *crypto.Box
	http         *http.Client
	envDiscord   string
	envSlack     string
	envSMTP      smtpConfig
	baseURL      string // public base for deep links, e.g. "https://vac.example.com"
	backoff      time.Duration
	logger       *slog.Logger
	blockPrivate bool // SSRF guard: refuse to dial private/loopback/link-local addresses
	allowPrivate bool // SMTP-only opt-out (D10): permit a LAN/sidecar relay
}

// New wires a dispatcher. envDiscord/envSlack/envSMTP are the VAC_NOTIFY_*
// overrides (empty when unset); box decrypts stored secrets (nil disables them).
// The returned dispatcher refuses to dial private/loopback/link-local addresses
// for webhooks (SSRF guard); tests that point at httptest servers must clear
// `blockPrivate`. allowPrivate relaxes that guard for SMTP only (D10).
func New(s SettingsStore, box *crypto.Box, envDiscord, envSlack, baseURL string, env SMTPEnv, allowPrivate bool, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	d := &Dispatcher{
		store:      s,
		box:        box,
		envDiscord: strings.TrimSpace(envDiscord),
		envSlack:   strings.TrimSpace(envSlack),
		envSMTP: smtpConfig{
			host:     strings.TrimSpace(env.Host),
			port:     strings.TrimSpace(env.Port),
			username: strings.TrimSpace(env.Username),
			password: env.Password,
			from:     strings.TrimSpace(env.From),
			to:       env.To,
			tlsMode:  strings.TrimSpace(env.TLSMode),
		},
		baseURL:      strings.TrimRight(baseURL, "/"),
		backoff:      time.Second,
		logger:       logger,
		blockPrivate: true,
		allowPrivate: allowPrivate,
	}
	d.http = &http.Client{
		Timeout:   5 * time.Second,
		Transport: d.transport(),
	}
	return d
}

// transport returns an http.Transport whose DialContext rejects connections to
// private/loopback/link-local addresses when blockPrivate is set, dialing the
// validated literal IP so a low-TTL DNS rebind can't swap in an internal address
// after the check. A public hostname pointing at 127.0.0.1 (or 169.254.169.254)
// is blocked — closing the SSRF window. blockPrivate is cleared by tests that
// must reach httptest servers on loopback.
func (d *Dispatcher) transport() *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	guarded := netguard.DialContext(5*time.Second, 30*time.Second)
	plain := (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	// Evaluate the guard at dial time so tests can clear blockPrivate after
	// construction to reach httptest servers on loopback.
	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if d.blockPrivate {
			return guarded(ctx, network, addr)
		}
		return plain(ctx, network, addr)
	}
	return base
}

// resolved is the effective config at dispatch time.
type resolved struct {
	discordURL string
	slackURL   string
	smtp       smtpConfig
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
	out.smtp = d.resolveSMTP(row)
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

// resolveSMTP merges the env override (env-first, per field) over the stored
// row. The password comes from the env override or the decrypted column;
// tlsMode defaults to starttls.
func (d *Dispatcher) resolveSMTP(row store.NotificationSettingsRow) smtpConfig {
	c := d.envSMTP
	if c.host == "" {
		c.host = row.SMTPHost
	}
	if c.port == "" && row.SMTPPort > 0 {
		c.port = strconv.Itoa(row.SMTPPort)
	}
	if c.username == "" {
		c.username = row.SMTPUsername
	}
	if c.password == "" {
		c.password = d.open(row.SMTPPasswordEnc)
	}
	if c.from == "" {
		c.from = row.SMTPFrom
	}
	if c.to == "" {
		c.to = row.SMTPTo
	}
	if c.tlsMode == "" {
		c.tlsMode = row.SMTPTLSMode
	}
	if c.tlsMode == "" {
		c.tlsMode = tlsModeStartTLS
	}
	return c
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
		if cfg.smtp.configured() {
			d.email(ctx, cfg.smtp, ev)
		}
	}()
}

// email renders an Event and sends it through the configured relay, logging
// failures (fire-and-forget, same posture as post).
func (d *Dispatcher) email(ctx context.Context, cfg smtpConfig, ev Event) {
	subject, body := emailMessage(ev, d.baseURL)
	if err := sendEmail(ctx, cfg, d.allowPrivate, subject, body); err != nil {
		if errors.Is(err, ErrPrivateAddress) {
			d.logger.Warn("notify: refused smtp to private address", "err", err)
			return
		}
		d.logger.Warn("notify: smtp send failed", "err", err)
	}
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
	if cfg.smtp.configured() {
		d.email(ctx, cfg.smtp, ev)
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

// TrafficAnomaly fires the traffic-anomaly event (plan 15 / E2). kind names the
// breach class (e.g. "error surge", "request spike") and detail carries the
// specific figures (top talker IP, rate). appName/appID are empty for box-level
// anomalies that aren't attributable to one app.
func (d *Dispatcher) TrafficAnomaly(appName, appID, kind, detail string) {
	title := "Traffic anomaly: " + kind
	if appName != "" {
		title = "Traffic anomaly on " + appName + ": " + kind
	}
	d.dispatch(Event{
		Type: EventTrafficAnomaly, OK: false,
		Title:   title,
		AppName: appName, AppID: appID, Message: detail,
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

// BackupFailed fires the backup-failed event (Track D / D1). Backup success is
// surfaced in-UI only — a failed scheduled backup is the event that warrants a
// push, since stateful data is the thing the operator most wants to hear about.
func (d *Dispatcher) BackupFailed(appName, appID, service, errMsg string) {
	msg := fmt.Sprintf("Backup of %s failed", service)
	if errMsg != "" {
		msg += ": " + errMsg
	}
	d.dispatch(Event{
		Type: EventBackupFailed, OK: false,
		Title:   "Backup failed: " + appName + "/" + service,
		AppName: appName, AppID: appID, Service: service, Message: msg,
	})
}

// JobFailed fires the scheduled-job-failed event (plan: scheduled-jobs.md). Like
// a backup, only failure warrants a push — a successful job run is surfaced
// in-UI only. jobName is the operator's label for the job (e.g. "cleanup").
func (d *Dispatcher) JobFailed(appName, appID, jobName, errMsg string) {
	msg := fmt.Sprintf("Scheduled job %q failed", jobName)
	if errMsg != "" {
		msg += ": " + errMsg
	}
	d.dispatch(Event{
		Type: EventJobFailed, OK: false,
		Title:   "Job failed: " + appName + "/" + jobName,
		AppName: appName, AppID: appID, Message: msg,
	})
}

// RestoreFinished fires the backup-restore-finished event. Unlike a backup
// (where only failure warrants a push), a restore is an operator-initiated,
// destructive action, so both outcomes are surfaced — success confirms the
// overwrite landed; failure means the database may be partially overwritten.
func (d *Dispatcher) RestoreFinished(appName, appID, service string, ok bool) {
	title := "Restore complete: " + appName + "/" + service
	msg := "Backup restored into " + service + "."
	if !ok {
		title = "Restore failed: " + appName + "/" + service
		msg = "Restore of " + service + " failed; its data may be partially overwritten."
	}
	d.dispatch(Event{
		Type: EventRestoreFinished, OK: ok,
		Title:   title,
		AppName: appName, AppID: appID, Service: service, Message: msg,
	})
}

// CertExpiring fires the TLS-certificate-expiring event (plan 03). daysLeft is
// the whole days until notAfter; a non-positive value means already expired.
// uploaded selects the copy: a bring-your-own cert can't auto-renew, so it tells
// the operator to upload a replacement rather than waiting for ACME.
func (d *Dispatcher) CertExpiring(host string, daysLeft int, notAfter time.Time, uploaded bool) {
	var msg string
	when := notAfter.Format("2006-01-02")
	switch {
	case uploaded && daysLeft <= 0:
		msg = fmt.Sprintf("The uploaded TLS certificate for %s has expired (%s). Upload a new certificate to keep HTTPS working — it will not auto-renew.", host, when)
	case uploaded && daysLeft == 1:
		msg = fmt.Sprintf("The uploaded TLS certificate for %s expires tomorrow (%s). Upload a new certificate — it will not auto-renew.", host, when)
	case uploaded:
		msg = fmt.Sprintf("The uploaded TLS certificate for %s expires in %d days (%s). Upload a new certificate — it will not auto-renew.", host, daysLeft, when)
	case daysLeft <= 0:
		msg = fmt.Sprintf("The TLS certificate for %s has expired (%s). Auto-renewal has not recovered it.", host, when)
	case daysLeft == 1:
		msg = fmt.Sprintf("The TLS certificate for %s expires tomorrow (%s) and has not auto-renewed.", host, when)
	default:
		msg = fmt.Sprintf("The TLS certificate for %s expires in %d days (%s) and has not auto-renewed.", host, daysLeft, when)
	}
	d.dispatch(Event{
		Type: EventCertExpiring, OK: false,
		Title:   "Certificate expiring: " + host,
		Message: msg,
	})
}

// DiskUsageHigh fires the storage-high event (volume usage / disk alerts). scope
// names what's full (an app name, or "host disk") and detail carries the figures
// (e.g. "82% of the 1.0 GiB disk budget (824 MiB used)"). appName/appID are empty
// for the host-level alert that isn't attributable to one app.
func (d *Dispatcher) DiskUsageHigh(appName, appID, scope, detail string) {
	d.dispatch(Event{
		Type: EventDiskUsageHigh, OK: false,
		Title:   "Storage high: " + scope,
		AppName: appName, AppID: appID, Message: detail,
	})
}

// MemOverCommitted fires when apps have reserved (via per-app RAM caps) more
// memory than the box physically has. Host-scoped (not attributable to one app)
// and a soft signal — VAC never blocks a deploy on it; the operator decides
// whether to lower a cap, add RAM, or accept the overcommit.
func (d *Dispatcher) MemOverCommitted(detail string) {
	d.dispatch(Event{
		Type: EventMemOverCommitted, OK: false,
		Title:   "RAM over-committed",
		Message: detail,
	})
}

// PreviewCapReached fires when a push that would create a new preview is refused
// because the instance is at VAC_MAX_PREVIEWS (preview-deployments.md decision
// #5). It is an app-attributable event — the parent whose branch was refused —
// so the operator knows to tear down an idle preview or raise the cap rather
// than discovering the box silently dropped a preview.
func (d *Dispatcher) PreviewCapReached(appName, appID, branch string, max int) {
	d.dispatch(Event{
		Type: EventPreviewCapReached, OK: false,
		Title:   "Preview limit reached: " + appName,
		AppName: appName, AppID: appID,
		Message: fmt.Sprintf("A push to branch %q was not deployed as a preview because the instance is at its limit of %d previews. Tear down an idle preview or raise VAC_MAX_PREVIEWS.", branch, max),
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
