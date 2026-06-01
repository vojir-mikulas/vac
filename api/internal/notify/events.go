// Package notify dispatches outbound webhook notifications (Discord, Slack) for
// key VAC events. Webhook URLs are stored encrypted; env vars override the
// stored values. Dispatch is fire-and-forget — a failed webhook is logged,
// never fatal, and never blocks the triggering path.
package notify

import "time"

// EventType is the stable key used both as the per-event toggle name and in
// rendered payloads.
type EventType string

const (
	EventDeploySucceeded EventType = "deploy_succeeded"
	EventDeployFailed    EventType = "deploy_failed"
	EventCrashLoop       EventType = "crash_loop"
	EventOOMKilled       EventType = "oom_killed"
	EventVACRestarted    EventType = "vac_restarted"
	EventCertExpiring    EventType = "cert_expiring"
)

// AllEvents is the set of implemented events, used to default a missing toggle
// to "on".
var AllEvents = []EventType{EventDeploySucceeded, EventDeployFailed, EventCrashLoop, EventOOMKilled, EventVACRestarted, EventCertExpiring}

// Event is a render-neutral notification. Channels turn it into their own
// payload shape.
type Event struct {
	Type     EventType
	Title    string        // headline, e.g. "Deploy succeeded: blog"
	AppName  string        // empty for host-level events
	AppID    string        // for deep links
	Service  string        // for service-scoped events (crash-loop)
	Commit   string        // short SHA, optional
	Message  string        // commit message or detail line
	Duration time.Duration // deploy duration, optional
	OK       bool          // colour: green when true, red/amber otherwise
}
