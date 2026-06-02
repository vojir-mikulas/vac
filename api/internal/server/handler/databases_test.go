package handler

import (
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/dbprovision"
)

func TestFootprintWarning(t *testing.T) {
	// Postgres shares vac-db → no warning.
	if got := footprintWarning(dbprovision.EngineInfo{Name: "postgres", Shared: false}); got != "" {
		t.Errorf("postgres warning = %q, want empty", got)
	}
	// A shared engine warns with its footprint.
	got := footprintWarning(dbprovision.EngineInfo{Name: "mariadb", FootprintMB: 150, Shared: true})
	if !strings.Contains(got, "mariadb") || !strings.Contains(got, "150") {
		t.Errorf("mariadb warning = %q, want name + footprint", got)
	}
}
