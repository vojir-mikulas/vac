package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/netguard"
)

// TLS modes for the SMTP relay. starttls is the default (submission port 587);
// implicit wraps the connection in TLS from the first byte (SMTPS, port 465);
// none sends in the clear (a localhost relay, opt-in only).
const (
	tlsModeStartTLS = "starttls"
	tlsModeImplicit = "implicit"
	tlsModeNone     = "none"
)

// smtpConfig is the effective email-channel config at dispatch time. host/port/
// from/to are operator config; password is the only secret. An email channel is
// "configured" only when host, from, and at least one recipient are present.
type smtpConfig struct {
	host     string
	port     string
	username string
	password string
	from     string
	to       string // comma/newline-separated recipient list
	tlsMode  string
}

func (c smtpConfig) configured() bool {
	return c.host != "" && c.from != "" && len(parseRecipients(c.to)) > 0
}

// parseRecipients splits a comma/newline-separated address list into trimmed,
// non-empty addresses.
func parseRecipients(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ';'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// emailMessage renders an Event into a subject line and a plain-text body. It
// mirrors slackPayload's body: the message, then Commit/Service/Duration and a
// deep link, one per line.
func emailMessage(ev Event, baseURL string) (subject, body string) {
	subject = ev.Title
	var b strings.Builder
	if ev.Message != "" {
		b.WriteString(ev.Message)
		b.WriteString("\n")
	}
	var lines []string
	if ev.Commit != "" {
		lines = append(lines, "Commit: "+ev.Commit)
	}
	if ev.Service != "" {
		lines = append(lines, "Service: "+ev.Service)
	}
	if ev.Duration > 0 {
		lines = append(lines, "Duration: "+fmtDuration(ev.Duration))
	}
	if link := deepLink(baseURL, ev.AppID); link != "" {
		lines = append(lines, "Open in VAC: "+link)
	}
	if len(lines) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(strings.Join(lines, "\n"))
	}
	return subject, b.String()
}

// sendEmail delivers one rendered message through the configured relay. It
// resolves the host and rejects private/loopback/link-local addresses (unless
// allowPrivate) using the same netguard.IsPrivate predicate as the webhook
// guard, then dials the validated literal IP — TOCTOU-safe, like the HTTP path.
// Unlike webhooks, the private-address block is opt-out here (deviation D10): a
// legitimate relay can be a LAN/sidecar MTA.
func sendEmail(ctx context.Context, cfg smtpConfig, allowPrivate bool, subject, body string) error {
	recipients := parseRecipients(cfg.to)
	if len(recipients) == 0 {
		return fmt.Errorf("notify: smtp has no recipients")
	}
	port := cfg.port
	if port == "" {
		port = "587"
	}

	ip, err := guardedResolve(ctx, cfg.host, allowPrivate)
	if err != nil {
		return err
	}
	dialAddr := net.JoinHostPort(ip, port)

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	if cfg.tlsMode == tlsModeImplicit {
		conn, err = tls.DialWithDialer(dialer, "tcp", dialAddr, &tls.Config{ServerName: cfg.host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", dialAddr)
	}
	if err != nil {
		return err
	}

	// smtp.NewClient keeps the hostname for HELO/SNI even though we dialed an IP.
	client, err := smtp.NewClient(conn, cfg.host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = client.Close() }()

	if cfg.tlsMode == tlsModeStartTLS {
		if err := client.StartTLS(&tls.Config{ServerName: cfg.host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if cfg.username != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.username, cfg.password, cfg.host)); err != nil {
			return err
		}
	}
	if err := client.Mail(cfg.from); err != nil {
		return err
	}
	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			return err
		}
	}
	wc, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write([]byte(buildMIME(cfg.from, recipients, subject, body))); err != nil {
		_ = wc.Close()
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// guardedResolve resolves host and returns the first address to dial, rejecting
// the host if ANY resolved address is private/loopback/link-local — unless
// allowPrivate is set (the SMTP-only opt-out, deviation D10).
func guardedResolve(ctx context.Context, host string, allowPrivate bool) (string, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("notify: no addresses for %q", host)
	}
	if !allowPrivate {
		for _, ip := range ips {
			if netguard.IsPrivate(ip) {
				return "", fmt.Errorf("%w: %s", ErrPrivateAddress, ip.String())
			}
		}
	}
	return ips[0].String(), nil
}

// buildMIME assembles a minimal RFC 5322 plain-text message. Body newlines are
// normalised to CRLF.
func buildMIME(from string, to []string, subject, body string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return b.String()
}
