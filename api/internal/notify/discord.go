package notify

import (
	"fmt"
	"time"
)

// Discord embed colours (decimal RGB).
const (
	colorGreen = 0x2ECC71
	colorRed   = 0xE74C3C
	colorAmber = 0xE67E22
)

type discordEmbed struct {
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Color       int                 `json:"color"`
	Fields      []discordEmbedField `json:"fields,omitempty"`
	URL         string              `json:"url,omitempty"`
}

type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type discordMessage struct {
	Embeds []discordEmbed `json:"embeds"`
}

// discordPayload renders an Event as a colour-coded Discord embed.
func discordPayload(ev Event, baseURL string) discordMessage {
	embed := discordEmbed{
		Title:       ev.Title,
		Description: ev.Message,
		Color:       colorFor(ev),
		URL:         deepLink(baseURL, ev.AppID),
	}
	if ev.Commit != "" {
		embed.Fields = append(embed.Fields, discordEmbedField{Name: "Commit", Value: ev.Commit, Inline: true})
	}
	if ev.Service != "" {
		embed.Fields = append(embed.Fields, discordEmbedField{Name: "Service", Value: ev.Service, Inline: true})
	}
	if ev.Duration > 0 {
		embed.Fields = append(embed.Fields, discordEmbedField{Name: "Duration", Value: fmtDuration(ev.Duration), Inline: true})
	}
	return discordMessage{Embeds: []discordEmbed{embed}}
}

func colorFor(ev Event) int {
	if ev.OK {
		return colorGreen
	}
	if ev.Type == EventCrashLoop {
		return colorAmber
	}
	return colorRed
}

func deepLink(baseURL, appID string) string {
	if baseURL == "" || appID == "" {
		return ""
	}
	return fmt.Sprintf("%s/apps/%s", baseURL, appID)
}

func fmtDuration(d time.Duration) string {
	return d.Round(time.Second).String()
}
