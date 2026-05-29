// Package dockercli wraps the `docker` and `docker compose` CLIs. All
// callers must pass a context with a timeout — these processes can hang
// indefinitely against a sick daemon.
//
// We use the CLI rather than the Docker Engine SDK for two reasons: the
// Compose v2 API is only stable at the CLI layer, and shell-out keeps the
// dependency surface small. Engine SDK calls would be a few hundred KB of
// extra Go binary for parity with `docker inspect` / `docker events`.
package dockercli

import "time"

// PsService is the structured view of one row from `docker compose ps`.
type PsService struct {
	ID         string        `json:"ID"`
	Name       string        `json:"Name"`
	Service    string        `json:"Service"`
	Image      string        `json:"Image"`
	State      string        `json:"State"`
	Status     string        `json:"Status"`
	Health     string        `json:"Health"`
	ExitCode   int           `json:"ExitCode"`
	Publishers []PsPublisher `json:"Publishers"`
}

// PsPublisher is one port-mapping entry within PsService.
type PsPublisher struct {
	URL           string `json:"URL"`
	TargetPort    int    `json:"TargetPort"`
	PublishedPort int    `json:"PublishedPort"`
	Protocol      string `json:"Protocol"`
}

// FirstPublishedPort returns the host-side port for the first publisher,
// or 0 if none. The pipeline's health check uses this when the service
// hasn't been hand-configured with an exposed_port.
func (p PsService) FirstPublishedPort() int {
	for _, pub := range p.Publishers {
		if pub.PublishedPort > 0 {
			return pub.PublishedPort
		}
	}
	return 0
}

// Event is the structured view of a line from `docker events --format json`.
// Only the fields the crash-loop monitor reads.
type Event struct {
	Status   string     `json:"status"`
	Action   string     `json:"Action"`
	Type     string     `json:"Type"`
	ID       string     `json:"id"`
	From     string     `json:"from"`
	Time     int64      `json:"time"`
	TimeNano int64      `json:"timeNano"`
	Actor    EventActor `json:"Actor"`
}

// EventActor.Attributes carries compose-project / compose-service / exitCode
// labels that the monitor maps back to VAC's app + service records.
type EventActor struct {
	ID         string            `json:"ID"`
	Attributes map[string]string `json:"Attributes"`
}

// ComposeProject returns the value of the com.docker.compose.project label
// (e.g. "vac-myapp"), or "" if the event is not from a compose-managed
// container.
func (e Event) ComposeProject() string { return e.Actor.Attributes["com.docker.compose.project"] }

// ComposeService returns the com.docker.compose.service label, identifying
// which service within the project the container belongs to.
func (e Event) ComposeService() string { return e.Actor.Attributes["com.docker.compose.service"] }

// EventTime returns the event's wall-clock time as a time.Time.
func (e Event) EventTime() time.Time {
	if e.TimeNano > 0 {
		return time.Unix(0, e.TimeNano)
	}
	return time.Unix(e.Time, 0)
}

// Image is the structured view of one row from `docker images --format json`.
type Image struct {
	ID         string `json:"ID"`
	Repository string `json:"Repository"`
	Tag        string `json:"Tag"`
	CreatedAt  string `json:"CreatedAt"`
}
