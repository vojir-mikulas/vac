package compose

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Severity classifies a preflight finding. Errors block the deploy; warnings
// are logged and the deploy proceeds.
type Severity int

const (
	SeverityWarn Severity = iota
	SeverityError
)

func (s Severity) String() string {
	if s == SeverityError {
		return "error"
	}
	return "warning"
}

// Stable finding codes — kept as constants so the pipeline, tests, and any
// future UI can reference them without string-matching.
const (
	CodeEdgePortConflict    = "edge_port_conflict"
	CodeBundledReverseProxy = "bundled_reverse_proxy"
	CodeDockerSocketMount   = "docker_socket_mount"
	CodePrivilegedOrHostNet = "privileged_or_host_net"
	CodeHostPortPublish     = "host_port_publish"
	CodeFixedContainerName  = "fixed_container_name"
	CodeLifecycleDaemon     = "lifecycle_daemon"
	CodeNoRoutableHTTP      = "no_routable_http"
)

// hostEscapeCodes are the hard findings that hand app code host-level access on
// the control box. Unlike the "VAC owns the edge" errors, these are never
// downgraded by the per-app allow_unsafe_compose escape hatch.
var hostEscapeCodes = map[string]bool{
	CodeDockerSocketMount:   true,
	CodePrivilegedOrHostNet: true,
}

// Finding is one preflight result: a stable code, the offending service (empty
// for stack-level), and an operator-facing message explaining what, why, and
// the fix.
type Finding struct {
	Severity Severity
	Code     string
	Service  string // "" for stack-level
	Message  string
}

// IsHostEscape reports whether the finding is a host-escape hard error that the
// allow_unsafe_compose escape hatch must not downgrade.
func (f Finding) IsHostEscape() bool { return hostEscapeCodes[f.Code] }

// Format renders a finding as a single operator-facing deploy-log line.
func (f Finding) Format() string {
	return fmt.Sprintf("compose preflight %s [%s]: %s", f.Severity, f.Code, f.Message)
}

// JoinFindings renders a bulleted block of finding messages for a combined
// deployment error message.
func JoinFindings(fs []Finding) string {
	var b strings.Builder
	for i, f := range fs {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("  • ")
		b.WriteString(f.Message)
	}
	return b.String()
}

// Package-level matcher tables — exported as vars (not consts) so they stay easy
// to extend and unit-test.
var (
	proxyImageBases    = map[string]bool{"traefik": true, "caddy": true}
	daemonImageNeedles = []string{"watchtower", "ouroboros"}
	edgePorts          = map[int]bool{80: true, 443: true}

	// dangerousCaps are Linux capabilities that, added to a container, enable a
	// host escape or a bypass of VAC's isolation. cap_add of any of these blocks
	// the deploy (alongside privileged / host pid|net|userns).
	// Excludes capabilities that have common legitimate uses inside a bridged
	// netns and don't directly escape (e.g. NET_ADMIN, NET_RAW), to avoid false
	// positives that would block benign apps.
	dangerousCaps = map[string]bool{
		"ALL":             true,
		"SYS_ADMIN":       true, // mount, many escape primitives
		"SYS_MODULE":      true, // load kernel modules
		"SYS_PTRACE":      true, // inspect/escape other processes
		"SYS_RAWIO":       true, // raw I/O port / memory access
		"SYS_BOOT":        true, // reboot the host
		"DAC_READ_SEARCH": true, // bypass file read perms (open_by_handle_at escape)
		"DAC_OVERRIDE":    true, // bypass all file perm checks
		"BPF":             true, // load BPF programs
	}
)

// Preflight reads the compose file at path and lints it. Prefer PreflightBytes
// with the output of `docker compose config` when available, so the lint sees
// the fully merged document (include/extends/overrides) rather than the raw file.
func Preflight(path string) ([]Finding, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is VAC/operator-controlled
	if err != nil {
		return nil, fmt.Errorf("compose: read %s: %w", path, err)
	}
	return PreflightBytes(raw)
}

// PreflightBytes parses a compose document with a richer view than the lean
// Service struct and returns all findings (errors + warnings), sorted by
// service then code for deterministic output.
func PreflightBytes(data []byte) ([]Finding, error) {
	views, err := parsePreflightBytes(data)
	if err != nil {
		return nil, err
	}

	var findings []Finding
	routable := false
	for _, v := range views {
		findings = append(findings, v.findings()...)
		if len(v.expose) > 0 || v.hasPortTarget() {
			routable = true
		}
	}
	if len(views) > 0 && !routable {
		findings = append(findings, Finding{
			Severity: SeverityWarn,
			Code:     CodeNoRoutableHTTP,
			Message:  "no service exposes an HTTP port (no `expose`/`ports` target found) — VAC won't assign a domain to this app.",
		})
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Service != findings[j].Service {
			return findings[i].Service < findings[j].Service
		}
		return findings[i].Code < findings[j].Code
	})
	return findings, nil
}

// ServiceExposedPorts maps each service to the container port VAC should route
// to when the host publishes nothing: the first `expose:` entry, falling back
// to the first `ports:` target. Services declaring neither are omitted.
//
// The deploy pipeline consults this when `docker compose ps` reports no
// published port for a service. `ps` only surfaces host-published mappings, so
// an `expose`-only service (e.g. the Grafana add-on) yields TargetPort 0 — and
// would otherwise never be attached to vac-edge or routed by Caddy (503).
func ServiceExposedPorts(path string) (map[string]int, error) {
	views, err := parsePreflight(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(views))
	for _, v := range views {
		if p := v.routablePort(); p > 0 {
			out[v.name] = p
		}
	}
	return out, nil
}

// ServicesWithVolumes reports, per service, whether it declares a persistent
// volume — any volume mount other than the Docker socket (a control-plane bind,
// not data). The deploy pipeline persists this so the dashboard can nudge
// backups only on stateful services; a stateless web/API container is rebuilt
// from git and has nothing to back up. Services with no persistent volume are
// omitted (so the map doubles as a presence set).
func ServicesWithVolumes(path string) (map[string]bool, error) {
	views, err := parsePreflight(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(views))
	for _, v := range views {
		for _, vol := range v.volumes {
			if volumeIsDockerSocket(vol) {
				continue
			}
			out[v.name] = true
			break
		}
	}
	return out, nil
}

// volumeIsDockerSocket reports whether a raw compose volume entry
// (source[:target[:opts]]) mounts the Docker socket. Used to exclude the
// control-plane socket bind from the "is this service stateful" heuristic.
func volumeIsDockerSocket(vol string) bool {
	src := vol
	if i := strings.IndexByte(src, ':'); i >= 0 {
		src = src[:i]
	}
	return strings.HasSuffix(strings.TrimSpace(src), "docker.sock")
}

// volumeExposesDockerSocket reports whether a raw compose volume entry grants
// access to the Docker socket — either by binding the socket directly OR by
// binding a parent directory that contains it (e.g. /var/run, /run, /). The
// suffix check alone (volumeIsDockerSocket) is bypassable via the parent dir,
// so the security lint uses this stricter test.
func volumeExposesDockerSocket(vol string) bool {
	src := vol
	if i := strings.IndexByte(src, ':'); i >= 0 {
		src = src[:i]
	}
	src = strings.TrimRight(strings.TrimSpace(src), "/")
	if src == "" {
		src = "/" // a bare "/" bind, trimmed to empty above
	}
	if strings.HasSuffix(src, "docker.sock") {
		return true
	}
	// Any ancestor directory of the canonical socket paths exposes it.
	switch src {
	case "/", "/run", "/var", "/var/run":
		return true
	}
	return false
}

// ---- richer parse pass (kept private; Service stays lean) ----

type portMapping struct {
	host   int // host-side published port, 0 when none/ephemeral
	target int // container-side port
	proto  string
}

type preflightView struct {
	name          string
	image         string
	command       []string
	ports         []portMapping
	expose        []string
	volumes       []string // raw source[:target[:opts]]
	labels        map[string]string
	containerName string
	privileged    bool
	networkMode   string
	pidMode       string
	usernsMode    string
	capAdd        []string
	securityOpt   []string
}

func parsePreflight(path string) ([]preflightView, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is operator-controlled
	if err != nil {
		return nil, fmt.Errorf("compose: read %s: %w", path, err)
	}
	return parsePreflightBytes(raw)
}

func parsePreflightBytes(raw []byte) ([]preflightView, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("compose: parse: %w", err)
	}
	servicesAny, ok := doc["services"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("compose: document has no services section")
	}

	out := make([]preflightView, 0, len(servicesAny))
	for name, body := range servicesAny {
		v := preflightView{name: name}
		bodyMap, ok := body.(map[string]any)
		if !ok {
			out = append(out, v)
			continue
		}
		if image, ok := bodyMap["image"].(string); ok {
			v.image = image
		}
		v.command = asStringSlice(bodyMap["command"])
		v.ports = normalizePorts(bodyMap["ports"])
		v.expose = asStringSlice(bodyMap["expose"])
		v.volumes = normalizeVolumes(bodyMap["volumes"])
		v.labels = normalizeLabels(bodyMap["labels"])
		if cn, ok := bodyMap["container_name"].(string); ok {
			v.containerName = cn
		}
		if priv, ok := bodyMap["privileged"].(bool); ok {
			v.privileged = priv
		}
		if nm, ok := bodyMap["network_mode"].(string); ok {
			v.networkMode = nm
		}
		if pm, ok := bodyMap["pid"].(string); ok {
			v.pidMode = pm
		}
		if um, ok := bodyMap["userns_mode"].(string); ok {
			v.usernsMode = um
		}
		v.capAdd = asStringSlice(bodyMap["cap_add"])
		v.securityOpt = asStringSlice(bodyMap["security_opt"])
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// routablePort returns the container port VAC should route to when nothing is
// published to the host: the first `expose:` entry (stripping any `/proto`),
// else the first `ports:` target. 0 when the service declares neither.
func (v preflightView) routablePort() int {
	for _, e := range v.expose {
		s := e
		if i := strings.IndexByte(s, '/'); i >= 0 {
			s = s[:i]
		}
		if n := atoiFirst(s); n > 0 {
			return n
		}
	}
	for _, p := range v.ports {
		if p.target > 0 {
			return p.target
		}
	}
	return 0
}

func (v preflightView) hasPortTarget() bool {
	for _, p := range v.ports {
		if p.target > 0 {
			return true
		}
	}
	return false
}

func (v preflightView) bindsEdgePort() bool {
	for _, p := range v.ports {
		if edgePorts[p.host] {
			return true
		}
	}
	return false
}

func (v preflightView) hasTraefikLabel() bool {
	for k := range v.labels {
		if strings.HasPrefix(k, "traefik.") {
			return true
		}
	}
	return false
}

// findings runs every rule over one service.
func (v preflightView) findings() []Finding {
	var fs []Finding
	add := func(sev Severity, code, msg string) {
		fs = append(fs, Finding{Severity: sev, Code: code, Service: v.name, Message: msg})
	}

	// Host port bindings: 80/443 are hard edge conflicts; anything else warns.
	for _, p := range v.ports {
		if p.host == 0 {
			continue
		}
		if edgePorts[p.host] {
			add(SeverityError, CodeEdgePortConflict, fmt.Sprintf(
				"service %q publishes host port %d — VAC's Caddy owns 80/443 and terminates TLS for you; remove the host port binding.", v.name, p.host))
		} else {
			add(SeverityWarn, CodeHostPortPublish, fmt.Sprintf(
				"service %q publishes host port %d — host ports bypass Caddy and expose the service directly; prefer `expose` plus a VAC domain.", v.name, p.host))
		}
	}

	// Bundled reverse proxy / edge.
	base := imageBaseName(v.image)
	cmd := strings.Join(v.command, " ")
	hasProxyCmd := strings.Contains(cmd, "--certificatesresolvers") || strings.Contains(cmd, "--entrypoints")
	nginxEdge := base == "nginx" && (v.bindsEdgePort() || v.hasTraefikLabel())
	if proxyImageBases[base] || v.hasTraefikLabel() || hasProxyCmd || nginxEdge {
		add(SeverityError, CodeBundledReverseProxy, fmt.Sprintf(
			"service %q looks like a bundled reverse proxy/edge — VAC is the edge; remove the bundled proxy/ACME and let VAC route and terminate TLS.", v.name))
	}

	// Docker socket mount (directly, or via a parent dir that contains it).
	for _, vol := range v.volumes {
		if volumeExposesDockerSocket(vol) {
			src := vol
			if i := strings.IndexByte(src, ':'); i >= 0 {
				src = src[:i]
			}
			add(SeverityError, CodeDockerSocketMount, fmt.Sprintf(
				"service %q mounts the Docker socket or a directory containing it (%s) — this grants host-root to app code on the control box; remove the mount.", v.name, strings.TrimSpace(src)))
			break
		}
	}

	// Privileged / host network / dangerous capabilities.
	var escalations []string
	if v.privileged {
		escalations = append(escalations, "privileged: true")
	}
	if v.networkMode == "host" {
		escalations = append(escalations, "network_mode: host")
	}
	if v.pidMode == "host" {
		escalations = append(escalations, "pid: host")
	}
	if v.usernsMode == "host" {
		escalations = append(escalations, "userns_mode: host")
	}
	for _, c := range v.capAdd {
		capName := strings.ToUpper(strings.TrimSpace(c))
		if dangerousCaps[capName] {
			escalations = append(escalations, "cap_add: "+capName)
		}
	}
	for _, so := range v.securityOpt {
		s := strings.ToLower(strings.TrimSpace(so))
		if strings.Contains(s, "unconfined") || strings.Contains(s, "seccomp=unconfined") ||
			strings.Contains(s, "apparmor=unconfined") || strings.Contains(s, "label:disable") {
			escalations = append(escalations, "security_opt: "+strings.TrimSpace(so))
		}
	}
	if len(escalations) > 0 {
		add(SeverityError, CodePrivilegedOrHostNet, fmt.Sprintf(
			"service %q requests host-level privileges (%s) — this can escape VAC's isolation; remove it.", v.name, strings.Join(escalations, ", ")))
	}

	// Fixed container name.
	if v.containerName != "" {
		add(SeverityWarn, CodeFixedContainerName, fmt.Sprintf(
			"service %q sets container_name %q — this breaks project-scoped naming, can collide, and won't be routed by VAC's alias; remove container_name.", v.name, v.containerName))
	}

	// Container-lifecycle daemon.
	repo := imageRepo(v.image)
	isDaemon := false
	for _, needle := range daemonImageNeedles {
		if strings.Contains(repo, needle) {
			isDaemon = true
			break
		}
	}
	if !isDaemon {
		for k := range v.labels {
			if strings.HasPrefix(k, "com.centurylinklabs.watchtower") {
				isDaemon = true
				break
			}
		}
	}
	if isDaemon {
		add(SeverityWarn, CodeLifecycleDaemon, fmt.Sprintf(
			"service %q is a container-lifecycle daemon — VAC owns the deploy lifecycle; this will desync the dashboard; remove it.", v.name))
	}

	return fs
}

// ---- normalizers (compose accepts list- and map-form for several fields) ----

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, fmt.Sprint(e))
		}
		return out
	case string:
		return []string{t}
	}
	return nil
}

func normalizeLabels(v any) map[string]string {
	out := map[string]string{}
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			out[k] = fmt.Sprint(val)
		}
	case []any:
		for _, e := range t {
			s := fmt.Sprint(e)
			if i := strings.IndexByte(s, '='); i >= 0 {
				out[s[:i]] = s[i+1:]
			} else {
				out[s] = ""
			}
		}
	}
	return out
}

func normalizeVolumes(v any) []string {
	t, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(t))
	for _, e := range t {
		switch ev := e.(type) {
		case string:
			out = append(out, ev)
		case map[string]any:
			if src, ok := ev["source"].(string); ok {
				out = append(out, src)
			}
		}
	}
	return out
}

func normalizePorts(v any) []portMapping {
	t, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []portMapping
	for _, e := range t {
		switch ev := e.(type) {
		case string:
			out = append(out, parsePortString(ev))
		case int:
			out = append(out, portMapping{target: ev})
		case map[string]any:
			pm := portMapping{host: toInt(ev["published"]), target: toInt(ev["target"])}
			if pr, ok := ev["protocol"].(string); ok {
				pm.proto = pr
			}
			out = append(out, pm)
		}
	}
	return out
}

// parsePortString handles the short syntaxes: "8080", "8080:80",
// "127.0.0.1:8080:80", "80:80/tcp", "8080-8090:80-90". A bare single number is
// treated as a container (target) port — only explicit host bindings carry a
// host port.
func parsePortString(s string) portMapping {
	pm := portMapping{}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		pm.proto = s[i+1:]
		s = s[:i]
	}
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		pm.target = atoiFirst(parts[0])
	case 2:
		pm.host = atoiFirst(parts[0])
		pm.target = atoiFirst(parts[1])
	case 3:
		pm.host = atoiFirst(parts[1])
		pm.target = atoiFirst(parts[2])
	}
	return pm
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	case string:
		return atoiFirst(t)
	}
	return 0
}

func atoiFirst(s string) int {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i]
	}
	n, _ := strconv.Atoi(s)
	return n
}

// imageRepo strips the tag and digest from an image reference, leaving the
// repository path (registry + namespace + name).
func imageRepo(image string) string {
	s := image
	if i := strings.IndexByte(s, '@'); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 && !strings.Contains(s[i+1:], "/") {
		s = s[:i]
	}
	return s
}

// imageBaseName returns the final path component of an image's repository
// (e.g. "traefik" from "traefik:v2.10", "watchtower" from "containrrr/watchtower").
func imageBaseName(image string) string {
	repo := imageRepo(image)
	if i := strings.LastIndexByte(repo, '/'); i >= 0 {
		return repo[i+1:]
	}
	return repo
}
