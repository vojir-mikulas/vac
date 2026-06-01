package promexport

import (
	"strings"
	"testing"
)

func sampleSnapshot() Snapshot {
	return Snapshot{
		Host: HostVitals{
			CPUPercent: 12.5, MemUsedBytes: 200, MemTotalBytes: 2048,
			DiskUsedBytes: 10, DiskTotalBytes: 100, RequestRate: 3.5,
		},
		Apps: []AppSample{
			{App: "blog", Service: "web", CPUPercent: 4.2, MemBytes: 524288},
		},
		Deploys: []DeployCount{
			{App: "blog", Status: "success", TriggeredBy: "manual", Count: 7},
		},
		DeployDurs: []DeployDuration{{App: "blog", Seconds: 42.5}},
		Requests:   []RequestTotal{{App: "blog", Service: "web", Requests: 100, Errors: 3}},
		Build:      BuildInfo{Version: "v1.2.3", Commit: "abc123"},
	}
}

func TestWrite_HasTypedMetrics(t *testing.T) {
	var b strings.Builder
	Write(&b, sampleSnapshot())
	out := b.String()

	want := []string{
		"# TYPE vac_host_cpu_percent gauge",
		"vac_host_cpu_percent 12.5",
		"# TYPE vac_deploys_total counter",
		`vac_deploys_total{app="blog",status="success",triggered_by="manual"} 7`,
		`vac_app_cpu_percent{app="blog",service="web"} 4.2`,
		`vac_app_mem_bytes{app="blog",service="web"} 524288`,
		`vac_deploy_duration_seconds{app="blog"} 42.5`,
		`vac_requests_total{app="blog",service="web"} 100`,
		`vac_request_errors_total{app="blog",service="web"} 3`,
		`vac_build_info{version="v1.2.3",commit="abc123"} 1`,
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("exposition missing line:\n  %s\n--- full output ---\n%s", w, out)
		}
	}
}

func TestWrite_EveryMetricHasHelpAndType(t *testing.T) {
	var b strings.Builder
	Write(&b, sampleSnapshot())
	help, typ := 0, 0
	for _, line := range strings.Split(b.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "# HELP "):
			help++
		case strings.HasPrefix(line, "# TYPE "):
			typ++
		}
	}
	if help != typ || help == 0 {
		t.Fatalf("HELP=%d TYPE=%d, want equal and non-zero", help, typ)
	}
}

func TestWrite_EscapesLabelValues(t *testing.T) {
	var b strings.Builder
	Write(&b, Snapshot{Apps: []AppSample{
		{App: `we"ird\`, Service: "x", CPUPercent: 1},
	}})
	out := b.String()
	if !strings.Contains(out, `app="we\"ird\\"`) {
		t.Fatalf("label not escaped:\n%s", out)
	}
}

func TestWrite_NoAppsStillEmitsHostAndBuild(t *testing.T) {
	var b strings.Builder
	Write(&b, Snapshot{Build: BuildInfo{Version: "v0", Commit: "c0"}})
	out := b.String()
	if !strings.Contains(out, "vac_host_cpu_percent ") || !strings.Contains(out, `vac_build_info{version="v0",commit="c0"} 1`) {
		t.Fatalf("missing host/build metrics when no apps:\n%s", out)
	}
}
