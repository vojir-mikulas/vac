package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/notify"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// TestSender posts a synthetic notification to the configured channels.
// *notify.Dispatcher satisfies it.
type TestSender interface {
	SendTest(ctx context.Context) (int, error)
}

type notificationSettingsDTO struct {
	DiscordConfigured bool            `json:"discord_configured"`
	DiscordHint       string          `json:"discord_hint,omitempty"`
	SlackConfigured   bool            `json:"slack_configured"`
	SlackHint         string          `json:"slack_hint,omitempty"`
	Events            map[string]bool `json:"events"`
}

// GetNotificationSettings returns the channel config with URLs redacted to a
// "configured" flag plus the last 4 characters (never the full secret URL).
func GetNotificationSettings(s *store.Store, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		row, err := s.GetNotificationSettings(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load settings")
			return
		}
		dto := notificationSettingsDTO{Events: defaultedEvents(row.Events)}
		if url := openURL(box, row.DiscordURLEnc); url != "" {
			dto.DiscordConfigured = true
			dto.DiscordHint = last4(url)
		}
		if url := openURL(box, row.SlackURLEnc); url != "" {
			dto.SlackConfigured = true
			dto.SlackHint = last4(url)
		}
		WriteJSON(w, http.StatusOK, dto)
	}
}

type putNotificationsRequest struct {
	DiscordURL *string         `json:"discord_url"` // nil = leave; "" = clear
	SlackURL   *string         `json:"slack_url"`
	Events     map[string]bool `json:"events"`
}

// PutNotificationSettings replaces the channel config. URLs are sealed with
// VAC_MASTER_KEY before storage; a missing key returns 503 when a URL is set.
func PutNotificationSettings(s *store.Store, box *crypto.Box) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req putNotificationsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		row, err := s.GetNotificationSettings(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not load settings")
			return
		}

		discordEnc, ok := sealURLUpdate(w, box, req.DiscordURL, row.DiscordURLEnc)
		if !ok {
			return
		}
		slackEnc, ok := sealURLUpdate(w, box, req.SlackURL, row.SlackURLEnc)
		if !ok {
			return
		}

		events := row.Events
		if req.Events != nil {
			b, err := json.Marshal(req.Events)
			if err != nil {
				WriteError(w, http.StatusBadRequest, "invalid events")
				return
			}
			events = b
		}

		if err := s.PutNotificationSettings(r.Context(), discordEnc, slackEnc, events); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not save settings")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "saved"})
	}
}

// TestNotification fires a synthetic ping to every configured channel.
func TestNotification(sender TestSender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, err := sender.SendTest(r.Context())
		if err != nil {
			WriteError(w, http.StatusBadRequest, "no notification channels configured")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]int{"sent": n})
	}
}

// sealURLUpdate computes the new ciphertext for a URL field given the patch
// semantics: nil pointer leaves the existing value; "" clears it; a non-empty
// value is sealed (requires the box). Writes the error response and returns
// ok=false on failure.
func sealURLUpdate(w http.ResponseWriter, box *crypto.Box, patch *string, existing []byte) ([]byte, bool) {
	if patch == nil {
		return existing, true
	}
	if *patch == "" {
		return nil, true // clear
	}
	if box == nil {
		WriteErrorCode(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "VAC_MASTER_KEY not configured; webhook URLs cannot be encrypted")
		return nil, false
	}
	sealed, err := box.Seal([]byte(*patch))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "could not encrypt webhook url")
		return nil, false
	}
	return sealed, true
}

func openURL(box *crypto.Box, enc []byte) string {
	if box == nil || len(enc) == 0 {
		return ""
	}
	pt, err := box.Open(enc)
	if err != nil {
		return ""
	}
	return string(pt)
}

func last4(s string) string {
	if len(s) <= 4 {
		return s
	}
	return "…" + s[len(s)-4:]
}

// defaultedEvents parses the stored toggle map and fills missing implemented
// events as enabled.
func defaultedEvents(raw []byte) map[string]bool {
	out := map[string]bool{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	for _, e := range notify.AllEvents {
		if _, ok := out[string(e)]; !ok {
			out[string(e)] = true
		}
	}
	return out
}
