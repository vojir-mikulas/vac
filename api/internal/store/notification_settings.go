package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// NotificationSettingsRow is the raw settings row. URL columns and the SMTP
// password hold AEAD ciphertext (sealed upstream by crypto.Box); the rest of
// the SMTP fields are operator-set config stored plaintext. Events is raw
// JSONB. The store never sees secret plaintext, mirroring env_vars.
type NotificationSettingsRow struct {
	DiscordURLEnc []byte
	SlackURLEnc   []byte
	Events        []byte // JSONB: {"deploy_succeeded":true,...}

	// SMTP email channel (migration 00070). An empty SMTPHost means the email
	// channel is off; SMTPPasswordEnc is the only sealed SMTP field.
	SMTPHost        string
	SMTPPort        int
	SMTPUsername    string
	SMTPPasswordEnc []byte
	SMTPFrom        string
	SMTPTo          string
	SMTPTLSMode     string
}

// GetNotificationSettings reads the singleton row. The row is seeded by the
// migration, so a missing row is treated as empty rather than an error.
func (s *Store) GetNotificationSettings(ctx context.Context) (NotificationSettingsRow, error) {
	var r NotificationSettingsRow
	var host, username, from, to, tlsMode *string
	var port *int
	err := s.pool.QueryRow(ctx, `
		SELECT discord_url_enc, slack_url_enc, events,
		       smtp_host, smtp_port, smtp_username, smtp_password_enc,
		       smtp_from, smtp_to, smtp_tls_mode
		FROM notification_settings WHERE id = 1
	`).Scan(&r.DiscordURLEnc, &r.SlackURLEnc, &r.Events,
		&host, &port, &username, &r.SMTPPasswordEnc,
		&from, &to, &tlsMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationSettingsRow{}, nil
	}
	if err != nil {
		return NotificationSettingsRow{}, err
	}
	// Nullable text/int columns scan into pointers; flatten to the zero value.
	r.SMTPHost = deref(host)
	r.SMTPPort = derefInt(port)
	r.SMTPUsername = deref(username)
	r.SMTPFrom = deref(from)
	r.SMTPTo = deref(to)
	r.SMTPTLSMode = deref(tlsMode)
	return r, nil
}

// PutNotificationSettings replaces the singleton row. URL/password ciphertexts
// may be nil to clear a channel; events is raw JSONB. SMTP config fields are
// stored plaintext.
func (s *Store) PutNotificationSettings(ctx context.Context, discordEnc, slackEnc, events []byte, smtp SMTPSettings) error {
	if len(events) == 0 {
		events = []byte("{}")
	}
	tlsMode := smtp.TLSMode
	if tlsMode == "" {
		tlsMode = "starttls"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_settings
			(id, discord_url_enc, slack_url_enc, events,
			 smtp_host, smtp_port, smtp_username, smtp_password_enc,
			 smtp_from, smtp_to, smtp_tls_mode, updated_at)
		VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		ON CONFLICT (id) DO UPDATE
			SET discord_url_enc   = EXCLUDED.discord_url_enc,
			    slack_url_enc     = EXCLUDED.slack_url_enc,
			    events            = EXCLUDED.events,
			    smtp_host         = EXCLUDED.smtp_host,
			    smtp_port         = EXCLUDED.smtp_port,
			    smtp_username     = EXCLUDED.smtp_username,
			    smtp_password_enc = EXCLUDED.smtp_password_enc,
			    smtp_from         = EXCLUDED.smtp_from,
			    smtp_to           = EXCLUDED.smtp_to,
			    smtp_tls_mode     = EXCLUDED.smtp_tls_mode,
			    updated_at        = NOW()
	`, discordEnc, slackEnc, events,
		nilIfEmpty(smtp.Host), nilIfZero(smtp.Port), nilIfEmpty(smtp.Username), smtp.PasswordEnc,
		nilIfEmpty(smtp.From), nilIfEmpty(smtp.To), tlsMode)
	return err
}

// SMTPSettings carries the SMTP fields written by PutNotificationSettings.
// PasswordEnc is ciphertext (nil clears the stored password).
type SMTPSettings struct {
	Host        string
	Port        int
	Username    string
	PasswordEnc []byte
	From        string
	To          string
	TLSMode     string
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nilIfZero(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}
