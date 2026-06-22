package deploy

import (
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
)

func TestRoutableInternalPort(t *testing.T) {
	tests := []struct {
		name          string
		svc           dockercli.PsService
		composeExpose int
		want          int
	}{
		{
			// The bug: getmeili/meilisearch's image declares EXPOSE 7700, so
			// `docker compose ps` reports a publisher with TargetPort 7700 /
			// PublishedPort 0 even though the compose file has no `expose:`.
			// VAC must NOT route it (want 0).
			name: "image EXPOSE only, no compose expose, not host-published -> not routed",
			svc: dockercli.PsService{
				Service:    "meilisearch",
				Publishers: []dockercli.PsPublisher{{TargetPort: 7700, PublishedPort: 0, Protocol: "tcp"}},
			},
			composeExpose: 0,
			want:          0,
		},
		{
			// Grafana add-on shape: declared via compose `expose:`, not host-published.
			name: "compose expose, not host-published -> routed on compose port",
			svc: dockercli.PsService{
				Service:    "grafana",
				Publishers: []dockercli.PsPublisher{{TargetPort: 3000, PublishedPort: 0, Protocol: "tcp"}},
			},
			composeExpose: 3000,
			want:          3000,
		},
		{
			name: "no publishers, no compose expose -> not routed",
			svc: dockercli.PsService{
				Service:    "worker",
				Publishers: nil,
			},
			composeExpose: 0,
			want:          0,
		},
		{
			// Host-published via compose `ports:` — compose parse yields the
			// target, which we prefer.
			name: "compose ports target, host-published -> routed on compose target",
			svc: dockercli.PsService{
				Service:    "web",
				Publishers: []dockercli.PsPublisher{{TargetPort: 80, PublishedPort: 8080, Protocol: "tcp"}},
			},
			composeExpose: 80,
			want:          80,
		},
		{
			// Defensive fallback: host actually publishes a port but compose
			// parsing surfaced nothing (e.g. long-syntax ports VAC couldn't
			// read). Trust the published mapping's target.
			name: "host-published, compose parse empty -> fall back to ps target",
			svc: dockercli.PsService{
				Service:    "web",
				Publishers: []dockercli.PsPublisher{{TargetPort: 80, PublishedPort: 8080, Protocol: "tcp"}},
			},
			composeExpose: 0,
			want:          80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := routableInternalPort(tt.svc, tt.composeExpose); got != tt.want {
				t.Fatalf("routableInternalPort() = %d, want %d", got, tt.want)
			}
		})
	}
}
