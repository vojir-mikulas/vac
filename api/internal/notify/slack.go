package notify

import "strings"

type slackBlock struct {
	Type string     `json:"type"`
	Text *slackText `json:"text,omitempty"`
}

type slackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type slackMessage struct {
	Text   string       `json:"text"` // fallback / notification text
	Blocks []slackBlock `json:"blocks"`
}

// slackPayload renders an Event as a Slack Block Kit message. The top-level
// `text` is the notification fallback; the blocks carry the formatted body.
func slackPayload(ev Event, baseURL string) slackMessage {
	emoji := ":white_check_mark:"
	if !ev.OK {
		emoji = ":x:"
		if ev.Type == EventCrashLoop {
			emoji = ":warning:"
		}
	}

	var b strings.Builder
	b.WriteString(emoji + " *" + ev.Title + "*")
	if ev.Message != "" {
		b.WriteString("\n" + ev.Message)
	}

	var fields []string
	if ev.Commit != "" {
		fields = append(fields, "*Commit:* "+ev.Commit)
	}
	if ev.Service != "" {
		fields = append(fields, "*Service:* "+ev.Service)
	}
	if ev.Duration > 0 {
		fields = append(fields, "*Duration:* "+fmtDuration(ev.Duration))
	}
	if link := deepLink(baseURL, ev.AppID); link != "" {
		fields = append(fields, "<"+link+"|Open in VAC>")
	}
	if len(fields) > 0 {
		b.WriteString("\n" + strings.Join(fields, "  •  "))
	}

	return slackMessage{
		Text: ev.Title,
		Blocks: []slackBlock{{
			Type: "section",
			Text: &slackText{Type: "mrkdwn", Text: b.String()},
		}},
	}
}
