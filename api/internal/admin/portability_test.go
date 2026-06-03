package admin

import (
	"io"
	"strings"
	"testing"
)

// Export validates the slug and format before connecting to the database, so
// these guard rails are exercisable without a DB.
func TestExport_ArgValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "no slug", args: nil, wantErr: "usage"},
		{name: "blank slug", args: []string{"   "}, wantErr: "usage"},
		{name: "unsupported format", args: []string{"myapp", "--format=compose"}, wantErr: "unsupported format"},
		{name: "unsupported format k8s", args: []string{"myapp", "--format=k8s"}, wantErr: "unsupported format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Export(tt.args, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got err %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// Apply validates its path and parses the spec before connecting, so a missing
// path and a malformed spec are both reachable without a DB.
func TestApply_ArgValidation(t *testing.T) {
	t.Run("no path", func(t *testing.T) {
		err := Apply(nil, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "usage") {
			t.Fatalf("got err %v, want usage", err)
		}
	})

	t.Run("malformed spec from stdin", func(t *testing.T) {
		err := Apply([]string{"-f", "-"}, strings.NewReader("name: [unclosed"), io.Discard, io.Discard)
		if err == nil {
			t.Fatal("expected an error for malformed YAML, got nil")
		}
		// Must fail at the parse stage, not while trying to reach the database.
		if strings.Contains(err.Error(), "VAC_DATABASE_URL") || strings.Contains(err.Error(), "db open") {
			t.Fatalf("reached DB connect on malformed spec: %v", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		err := Apply([]string{"-f", "/no/such/spec-file.yaml"}, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "read spec") {
			t.Fatalf("got err %v, want read spec error", err)
		}
	})
}
