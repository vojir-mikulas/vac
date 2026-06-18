package compose

import (
	"os"
	"path/filepath"
	"testing"
)

// writeCompose writes content to a temp compose file and returns its path.
func writeCompose(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	return path
}

// codeCounts tallies findings by code.
func codeCounts(fs []Finding) map[string]int {
	m := map[string]int{}
	for _, f := range fs {
		m[f.Code]++
	}
	return m
}

func hasCode(fs []Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

func TestServiceExposedPorts(t *testing.T) {
	cases := []struct {
		name    string
		compose string
		want    map[string]int
	}{
		{
			name: "expose-only service (Grafana add-on shape)",
			compose: `services:
  grafana:
    image: grafana/grafana-oss:11.3.0
    expose:
      - "3000"`,
			want: map[string]int{"grafana": 3000},
		},
		{
			name: "expose with protocol suffix",
			compose: `services:
  web:
    image: app
    expose:
      - "8080/tcp"`,
			want: map[string]int{"web": 8080},
		},
		{
			name: "expose preferred over ports target",
			compose: `services:
  web:
    image: app
    expose:
      - "9000"
    ports:
      - "8080:80"`,
			want: map[string]int{"web": 9000},
		},
		{
			name: "falls back to ports target when no expose",
			compose: `services:
  web:
    image: app
    ports:
      - "8080:80"`,
			want: map[string]int{"web": 80},
		},
		{
			name: "service with neither is omitted",
			compose: `services:
  worker:
    image: app`,
			want: map[string]int{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ServiceExposedPorts(writeCompose(t, tc.compose))
			if err != nil {
				t.Fatalf("ServiceExposedPorts: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for svc, port := range tc.want {
				if got[svc] != port {
					t.Errorf("service %q: got port %d, want %d", svc, got[svc], port)
				}
			}
		})
	}
}

func TestServicesWithVolumes(t *testing.T) {
	cases := []struct {
		name    string
		compose string
		want    map[string]bool
	}{
		{
			name: "named volume marks the service stateful",
			compose: `services:
  db:
    image: postgres:16
    volumes:
      - pgdata:/var/lib/postgresql/data`,
			want: map[string]bool{"db": true},
		},
		{
			name: "bind mount counts as persistent",
			compose: `services:
  app:
    image: app
    volumes:
      - ./uploads:/srv/uploads`,
			want: map[string]bool{"app": true},
		},
		{
			name: "long syntax volume",
			compose: `services:
  db:
    image: mariadb
    volumes:
      - type: volume
        source: dbdata
        target: /var/lib/mysql`,
			want: map[string]bool{"db": true},
		},
		{
			name: "docker socket alone is not persistent",
			compose: `services:
  proxy:
    image: app
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock`,
			want: map[string]bool{},
		},
		{
			name: "service with no volumes is omitted",
			compose: `services:
  web:
    image: nginx`,
			want: map[string]bool{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ServicesWithVolumes(writeCompose(t, tc.compose))
			if err != nil {
				t.Fatalf("ServicesWithVolumes: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for svc, persistent := range tc.want {
				if got[svc] != persistent {
					t.Errorf("service %q: got %v, want %v", svc, got[svc], persistent)
				}
			}
		})
	}
}

func TestPreflightRules(t *testing.T) {
	cases := []struct {
		name    string
		compose string
		code    string
		want    bool // whether code should be present
	}{
		{
			name: "edge port 80 conflicts",
			compose: `services:
  web:
    image: nginx:alpine
    ports:
      - "80:80"`,
			code: CodeEdgePortConflict, want: true,
		},
		{
			name: "edge port 443 conflicts",
			compose: `services:
  web:
    image: app
    ports:
      - "443:8443"`,
			code: CodeEdgePortConflict, want: true,
		},
		{
			name: "non-edge host port does not conflict",
			compose: `services:
  db:
    image: postgres:16
    ports:
      - "5432:5432"`,
			code: CodeEdgePortConflict, want: false,
		},
		{
			name: "traefik image is bundled proxy",
			compose: `services:
  proxy:
    image: traefik:v2.10`,
			code: CodeBundledReverseProxy, want: true,
		},
		{
			name: "caddy image is bundled proxy",
			compose: `services:
  proxy:
    image: caddy:2`,
			code: CodeBundledReverseProxy, want: true,
		},
		{
			name: "traefik label is bundled proxy",
			compose: `services:
  app:
    image: myapp
    labels:
      - "traefik.enable=true"`,
			code: CodeBundledReverseProxy, want: true,
		},
		{
			name: "certificatesresolvers command is bundled proxy",
			compose: `services:
  app:
    image: myapp
    command: ["--certificatesresolvers.le.acme.email=x@y.z"]`,
			code: CodeBundledReverseProxy, want: true,
		},
		{
			name: "plain nginx app is not a bundled proxy",
			compose: `services:
  web:
    image: nginx:alpine
    expose:
      - "8080"`,
			code: CodeBundledReverseProxy, want: false,
		},
		{
			name: "nginx binding edge port is a bundled proxy",
			compose: `services:
  web:
    image: nginx:alpine
    ports:
      - "80:80"`,
			code: CodeBundledReverseProxy, want: true,
		},
		{
			name: "docker socket mount detected",
			compose: `services:
  app:
    image: myapp
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro`,
			code: CodeDockerSocketMount, want: true,
		},
		{
			name: "ordinary volume is fine",
			compose: `services:
  app:
    image: myapp
    volumes:
      - ./data:/data`,
			code: CodeDockerSocketMount, want: false,
		},
		{
			name: "docker socket parent dir mount detected (/var/run)",
			compose: `services:
  app:
    image: myapp
    volumes:
      - /var/run:/host-run`,
			code: CodeDockerSocketMount, want: true,
		},
		{
			name: "docker socket parent dir mount detected (/run)",
			compose: `services:
  app:
    image: myapp
    volumes:
      - /run/:/host-run`,
			code: CodeDockerSocketMount, want: true,
		},
		{
			name: "root mount detected",
			compose: `services:
  app:
    image: myapp
    volumes:
      - /:/host`,
			code: CodeDockerSocketMount, want: true,
		},
		{
			name: "unrelated abs dir mount is fine",
			compose: `services:
  app:
    image: myapp
    volumes:
      - /var/lib/myapp:/data`,
			code: CodeDockerSocketMount, want: false,
		},
		{
			name: "privileged detected",
			compose: `services:
  app:
    image: myapp
    privileged: true`,
			code: CodePrivilegedOrHostNet, want: true,
		},
		{
			name: "host network detected",
			compose: `services:
  app:
    image: myapp
    network_mode: host`,
			code: CodePrivilegedOrHostNet, want: true,
		},
		{
			name: "cap_add SYS_ADMIN detected",
			compose: `services:
  app:
    image: myapp
    cap_add:
      - SYS_ADMIN`,
			code: CodePrivilegedOrHostNet, want: true,
		},
		{
			name: "benign cap_add ignored",
			compose: `services:
  app:
    image: myapp
    cap_add:
      - NET_ADMIN`,
			code: CodePrivilegedOrHostNet, want: false,
		},
		{
			name: "cap_add SYS_PTRACE detected",
			compose: `services:
  app:
    image: myapp
    cap_add:
      - SYS_PTRACE`,
			code: CodePrivilegedOrHostNet, want: true,
		},
		{
			name: "pid host detected",
			compose: `services:
  app:
    image: myapp
    pid: host`,
			code: CodePrivilegedOrHostNet, want: true,
		},
		{
			name: "userns_mode host detected",
			compose: `services:
  app:
    image: myapp
    userns_mode: host`,
			code: CodePrivilegedOrHostNet, want: true,
		},
		{
			name: "security_opt unconfined detected",
			compose: `services:
  app:
    image: myapp
    security_opt:
      - seccomp:unconfined`,
			code: CodePrivilegedOrHostNet, want: true,
		},
		{
			name: "host port publish warns",
			compose: `services:
  db:
    image: postgres:16
    ports:
      - "5432:5432"`,
			code: CodeHostPortPublish, want: true,
		},
		{
			name: "container_name warns",
			compose: `services:
  app:
    image: myapp
    container_name: my-app
    expose:
      - "8080"`,
			code: CodeFixedContainerName, want: true,
		},
		{
			name: "watchtower is a lifecycle daemon",
			compose: `services:
  watchtower:
    image: containrrr/watchtower`,
			code: CodeLifecycleDaemon, want: true,
		},
		{
			name: "watchtower label is a lifecycle daemon",
			compose: `services:
  app:
    image: myapp
    expose:
      - "8080"
    labels:
      - "com.centurylinklabs.watchtower.enable=true"`,
			code: CodeLifecycleDaemon, want: true,
		},
		{
			name: "no routable http warns",
			compose: `services:
  worker:
    image: myapp`,
			code: CodeNoRoutableHTTP, want: true,
		},
		{
			name: "expose makes it routable",
			compose: `services:
  web:
    image: myapp
    expose:
      - "3000"`,
			code: CodeNoRoutableHTTP, want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, err := Preflight(writeCompose(t, tc.compose))
			if err != nil {
				t.Fatalf("Preflight: %v", err)
			}
			if got := hasCode(fs, tc.code); got != tc.want {
				t.Fatalf("code %s present=%v, want %v; findings=%v", tc.code, got, tc.want, fs)
			}
		})
	}
}

// TestPreflightFixture asserts the exact finding set for the motivating
// GCP/Traefik/Watchtower stack from plan 16.
func TestPreflightFixture(t *testing.T) {
	fixture := `services:
  traefik:
    image: traefik:v2.10
    container_name: traefik
    command:
      - "--entrypoints.web.address=:80"
      - "--certificatesresolvers.le.acme.email=ops@example.com"
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    labels:
      - "traefik.enable=true"
  watchtower:
    image: containrrr/watchtower
    container_name: watchtower
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
  postgres:
    image: postgres:16
    container_name: db
    ports:
      - "5432:5432"
  redis:
    image: redis:7
    ports:
      - "6379:6379"
  gotenberg:
    image: gotenberg/gotenberg:8
    container_name: gotenberg
    ports:
      - "3000:3000"`

	fs, err := Preflight(writeCompose(t, fixture))
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	got := codeCounts(fs)
	want := map[string]int{
		CodeEdgePortConflict:    2, // 80 + 443 on traefik
		CodeBundledReverseProxy: 1, // traefik
		CodeDockerSocketMount:   2, // traefik + watchtower
		CodeHostPortPublish:     3, // postgres + redis + gotenberg
		CodeFixedContainerName:  4, // traefik, watchtower, db, gotenberg
		CodeLifecycleDaemon:     1, // watchtower
	}
	for code, n := range want {
		if got[code] != n {
			t.Errorf("code %s = %d, want %d", code, got[code], n)
		}
	}
	for code, n := range got {
		if want[code] == 0 {
			t.Errorf("unexpected finding code %s (×%d): %v", code, n, fs)
		}
	}
}

// TestPreflightNormalization checks list- vs map-form parsing of labels/ports.
func TestPreflightNormalization(t *testing.T) {
	mapForm := `services:
  app:
    image: myapp
    labels:
      traefik.enable: "true"
    ports:
      - target: 80
        published: 443
        protocol: tcp`
	fs, err := Preflight(writeCompose(t, mapForm))
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !hasCode(fs, CodeBundledReverseProxy) {
		t.Errorf("map-form traefik label not detected: %v", fs)
	}
	if !hasCode(fs, CodeEdgePortConflict) {
		t.Errorf("map-form published edge port not detected: %v", fs)
	}
}

func TestFindingIsHostEscape(t *testing.T) {
	if !(Finding{Code: CodeDockerSocketMount}).IsHostEscape() {
		t.Error("docker socket mount should be host escape")
	}
	if !(Finding{Code: CodePrivilegedOrHostNet}).IsHostEscape() {
		t.Error("privileged should be host escape")
	}
	if (Finding{Code: CodeEdgePortConflict}).IsHostEscape() {
		t.Error("edge port conflict should NOT be host escape")
	}
}
