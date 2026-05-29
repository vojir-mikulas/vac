package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// NotificationSettingsRow is the raw settings row. URL columns hold AEAD
// ciphertext (sealed upstream by crypto.Box); Events is raw JSONB. The store
// never sees plaintext, mirroring env_vars.
type NotificationSettingsRow struct {
	DiscordURLEnc []byte
	SlackURLEnc   []byte
	Events        []byte // JSONB: {"deploy_succeeded":true,...}
}

// GetNotificationSettings reads the singleton row. The row is seeded by the
// migration, so a missing row is treated as empty rather than an error.
func (s *Store) GetNotificationSettings(ctx context.Context) (NotificationSettingsRow, error) {
	var r NotificationSettingsRow
	err := s.pool.QueryRow(ctx, `
		SELECT discord_url_enc, slack_url_enc, events
		FROM notification_settings WHERE id = 1
	`).Scan(&r.DiscordURLEnc, &r.SlackURLEnc, &r.Events)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationSettingsRow{}, nil
	}
	return r, err
}

// PutNotificationSettings replaces the singleton row. URL ciphertexts may be
// nil to clear a channel; events is raw JSONB.
func (s *Store) PutNotificationSettings(ctx context.Context, discordEnc, slackEnc, events []byte) error {
	if len(events) == 0 {
		events = []byte("{}")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_settings (id, discord_url_enc, slack_url_enc, events, updated_at)
		VALUES (1, $1, $2, $3, NOW())
		ON CONFLICT (id) DO UPDATE
			SET discord_url_enc = EXCLUDED.discord_url_enc,
			    slack_url_enc   = EXCLUDED.slack_url_enc,
			    events          = EXCLUDED.events,
			    updated_at      = NOW()
	`, discordEnc, slackEnc, events)
	return err
}
