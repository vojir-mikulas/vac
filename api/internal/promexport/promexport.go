// Package promexport renders VAC's own metrics as Prometheus text exposition
// (plan 13). It is a pure formatter: a handler gathers a Snapshot from the live
// sources (host vitals, per-service stats, deploy + request aggregates, build
// info) and calls Write. Keeping the format a pure function of Snapshot makes it
// unit-testable without a database or Docker, and keeps this package dependency-
// free (standard library only — no Prometheus client, matching the project's
// existing hand-rolled Caddy-metrics parsing and keeping the RAM budget small).
//
// Metric names and labels are a stable contract: external scrapers and the
// plan-12 dashboards depend on them. Do not rename without a migration note.
package promexport

import (
	"io"
	"strconv"
	"strings"
)

// Snapshot is the fully-gathered metric state for one scrape.
type Snapshot struct {
	Host       HostVitals
	Apps       []AppSample
	Deploys    []DeployCount
	DeployDurs []DeployDuration
	Requests   []RequestTotal
	Build      BuildInfo
}

// HostVitals mirrors the host snapshot (gopsutil + Caddy request-rate delta).
type HostVitals struct {
	CPUPercent     float64
	MemUsedBytes   uint64
	MemTotalBytes  uint64
	DiskUsedBytes  uint64
	DiskTotalBytes uint64
	RequestRate    float64
}

// AppSample is one service's point-in-time CPU/memory.
type AppSample struct {
	App        string
	Service    string
	CPUPercent float64
	MemBytes   int64
}

// DeployCount is a (app, status, trigger) tally.
type DeployCount struct {
	App         string
	Status      string
	TriggeredBy string
	Count       int64
}

// DeployDuration is an app's most recent completed deploy duration in seconds.
type DeployDuration struct {
	App     string
	Seconds float64
}

// RequestTotal is a service's summed request/error counts over the window.
type RequestTotal struct {
	App      string
	Service  string
	Requests int64
	Errors   int64
}

// BuildInfo labels the always-1 vac_build_info gauge.
type BuildInfo struct {
	Version string
	Commit  string
}

// Write renders the snapshot as Prometheus exposition to w. It never errors on
// content (only on the underlying writer, which it ignores — by the time we
// write, the 200 status line is already committed).
func Write(w io.Writer, s Snapshot) {
	b := &strings.Builder{}

	// --- host vitals ---
	gauge(b, "vac_host_cpu_percent", "Host CPU utilisation, percent.")
	sample(b, "vac_host_cpu_percent", nil, f(s.Host.CPUPercent))

	gauge(b, "vac_host_mem_used_bytes", "Host memory in use, bytes.")
	sample(b, "vac_host_mem_used_bytes", nil, u(s.Host.MemUsedBytes))
	gauge(b, "vac_host_mem_total_bytes", "Host total memory, bytes.")
	sample(b, "vac_host_mem_total_bytes", nil, u(s.Host.MemTotalBytes))

	gauge(b, "vac_host_disk_used_bytes", "Host disk in use, bytes.")
	sample(b, "vac_host_disk_used_bytes", nil, u(s.Host.DiskUsedBytes))
	gauge(b, "vac_host_disk_total_bytes", "Host total disk, bytes.")
	sample(b, "vac_host_disk_total_bytes", nil, u(s.Host.DiskTotalBytes))

	gauge(b, "vac_host_request_rate", "Host-wide HTTP requests per second (Caddy).")
	sample(b, "vac_host_request_rate", nil, f(s.Host.RequestRate))

	// --- per-service stats ---
	gauge(b, "vac_app_cpu_percent", "Per-service container CPU utilisation, percent.")
	for _, a := range s.Apps {
		sample(b, "vac_app_cpu_percent", labels("app", a.App, "service", a.Service), f(a.CPUPercent))
	}
	gauge(b, "vac_app_mem_bytes", "Per-service container memory usage, bytes.")
	for _, a := range s.Apps {
		sample(b, "vac_app_mem_bytes", labels("app", a.App, "service", a.Service), i(a.MemBytes))
	}

	// --- deployments ---
	counter(b, "vac_deploys_total", "Deployments by app, status and trigger reason (cumulative; deployment rows are never pruned).")
	for _, d := range s.Deploys {
		sample(b, "vac_deploys_total",
			labels("app", d.App, "status", d.Status, "triggered_by", d.TriggeredBy), i(d.Count))
	}
	gauge(b, "vac_deploy_duration_seconds", "Wall-clock duration of each app's most recent completed deployment, seconds.")
	for _, d := range s.DeployDurs {
		sample(b, "vac_deploy_duration_seconds", labels("app", d.App), f(d.Seconds))
	}

	// --- requests (rolling window; resets as old buckets are pruned) ---
	counter(b, "vac_requests_total", "HTTP requests per service over the retained window (resets as old buckets age out).")
	for _, rq := range s.Requests {
		sample(b, "vac_requests_total", labels("app", rq.App, "service", rq.Service), i(rq.Requests))
	}
	counter(b, "vac_request_errors_total", "HTTP 5xx responses per service over the retained window (resets as old buckets age out).")
	for _, rq := range s.Requests {
		sample(b, "vac_request_errors_total", labels("app", rq.App, "service", rq.Service), i(rq.Errors))
	}

	// --- build info ---
	gauge(b, "vac_build_info", "VAC build information; value is always 1.")
	sample(b, "vac_build_info", labels("version", s.Build.Version, "commit", s.Build.Commit), "1")

	_, _ = io.WriteString(w, b.String())
}

// ContentType is the Prometheus text exposition format media type.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

type label struct{ name, value string }

func labels(kv ...string) []label {
	out := make([]label, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, label{kv[i], kv[i+1]})
	}
	return out
}

func gauge(b *strings.Builder, name, help string)   { meta(b, name, help, "gauge") }
func counter(b *strings.Builder, name, help string) { meta(b, name, help, "counter") }

func meta(b *strings.Builder, name, help, typ string) {
	b.WriteString("# HELP ")
	b.WriteString(name)
	b.WriteByte(' ')
	b.WriteString(escapeHelp(help))
	b.WriteByte('\n')
	b.WriteString("# TYPE ")
	b.WriteString(name)
	b.WriteByte(' ')
	b.WriteString(typ)
	b.WriteByte('\n')
}

func sample(b *strings.Builder, name string, ls []label, value string) {
	b.WriteString(name)
	if len(ls) > 0 {
		b.WriteByte('{')
		for i, l := range ls {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(l.name)
			b.WriteString(`="`)
			b.WriteString(escapeLabel(l.value))
			b.WriteByte('"')
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(value)
	b.WriteByte('\n')
}

func f(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
func i(v int64) string   { return strconv.FormatInt(v, 10) }
func u(v uint64) string  { return strconv.FormatUint(v, 10) }

// escapeLabel escapes a label value per the exposition format: backslash,
// double-quote and newline.
func escapeLabel(s string) string {
	if !strings.ContainsAny(s, "\\\"\n") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return r.Replace(s)
}

// escapeHelp escapes a HELP string: backslash and newline (quotes are allowed).
func escapeHelp(s string) string {
	if !strings.ContainsAny(s, "\\\n") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	return r.Replace(s)
}
