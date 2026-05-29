package dockercli_test

import (
	"encoding/json"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/dockercli"
)

func TestParsePsOutput_JSONArrayShape(t *testing.T) {
	in := []byte(`[
		{"ID":"abc","Name":"vac-myapp-web-1","Service":"web","State":"running","Status":"Up 2 minutes","Image":"myapp-web","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]},
		{"ID":"def","Name":"vac-myapp-db-1","Service":"db","State":"running","Status":"Up 2 minutes","Image":"postgres:16","Publishers":null}
	]`)
	out, err := dockercli.ParsePsOutput(in)
	if err != nil {
		t.Fatalf("ParsePsOutput: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].Service != "web" || out[0].FirstPublishedPort() != 8080 {
		t.Errorf("web row wrong: %+v", out[0])
	}
	if out[1].Service != "db" || out[1].FirstPublishedPort() != 0 {
		t.Errorf("db row wrong: %+v", out[1])
	}
}

func TestParsePsOutput_LineDelimitedShape(t *testing.T) {
	in := []byte(`{"ID":"abc","Name":"vac-myapp-web-1","Service":"web","State":"running"}
{"ID":"def","Name":"vac-myapp-worker-1","Service":"worker","State":"exited","ExitCode":137}`)
	out, err := dockercli.ParsePsOutput(in)
	if err != nil {
		t.Fatalf("ParsePsOutput: %v", err)
	}
	if len(out) != 2 || out[1].ExitCode != 137 {
		t.Errorf("unexpected: %+v", out)
	}
}

func TestParsePsOutput_Empty(t *testing.T) {
	out, err := dockercli.ParsePsOutput(nil)
	if err != nil || out != nil {
		t.Errorf("empty input: out=%v err=%v", out, err)
	}
}

func TestEvent_ComposeLabels(t *testing.T) {
	raw := []byte(`{
		"Action": "die",
		"Type": "container",
		"id": "abc123",
		"time": 1717000000,
		"timeNano": 1717000000000000000,
		"Actor": {
			"ID": "abc123",
			"Attributes": {
				"com.docker.compose.project": "vac-myapp",
				"com.docker.compose.service": "web",
				"exitCode": "137"
			}
		}
	}`)
	var ev dockercli.Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.ComposeProject() != "vac-myapp" {
		t.Errorf("project = %q", ev.ComposeProject())
	}
	if ev.ComposeService() != "web" {
		t.Errorf("service = %q", ev.ComposeService())
	}
	if ev.Action != "die" {
		t.Errorf("action = %q", ev.Action)
	}
	if ev.EventTime().IsZero() {
		t.Error("EventTime should resolve from timeNano")
	}
}
