package dbprovision

import (
	"context"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

func TestDatabaseInventory_PinsControlPlaneAndSizes(t *testing.T) {
	st := newFakeProvStore()
	st.dbs["db1"] = store.ManagedDatabase{ID: "db1", AppID: "app1", Engine: "postgres", DBName: "blog_abc", EnvVarName: "DATABASE_URL", Status: "ready"}

	p := newTestProvisioner(t, st, &fakeEngine{name: "postgres"})
	p.logger = discardLogger()
	p.ctrlDB = "vac"

	inv, err := p.DatabaseInventory(context.Background())
	if err != nil {
		t.Fatalf("DatabaseInventory: %v", err)
	}
	if len(inv.Engines) != 1 || inv.Engines[0].Engine != "postgres" {
		t.Fatalf("engines = %+v, want one postgres group", inv.Engines)
	}
	dbs := inv.Engines[0].Databases
	if len(dbs) != 2 {
		t.Fatalf("want control-plane + 1 user db, got %d: %+v", len(dbs), dbs)
	}
	// Control-plane entry is pinned first, flagged, and has no app.
	if !dbs[0].IsControlPlane || dbs[0].DBName != "vac" || dbs[0].AppID != "" {
		t.Errorf("first entry should be the pinned vac-db, got %+v", dbs[0])
	}
	// Every entry carries a size from the fake probe (non-nil, never zero-as-unknown).
	for _, d := range dbs {
		if d.SizeBytes == nil {
			t.Errorf("entry %q missing size", d.DBName)
		}
	}
	// User DB keeps its app linkage.
	if dbs[1].AppSlug != "blog" || dbs[1].DBName != "blog_abc" {
		t.Errorf("user db entry wrong: %+v", dbs[1])
	}
}

func TestDatabaseInventory_SizeCacheReused(t *testing.T) {
	st := newFakeProvStore()
	eng := &countingEngine{fakeEngine: fakeEngine{name: "postgres"}}
	p := newTestProvisioner(t, st, eng)
	p.logger = discardLogger()
	p.ctrlDB = "vac"

	if _, err := p.DatabaseInventory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DatabaseInventory(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second call within the TTL must serve cached sizes, not re-probe.
	if eng.probes != 1 {
		t.Errorf("size probes = %d, want 1 (second call cached)", eng.probes)
	}
}

type countingEngine struct {
	fakeEngine
	probes int
}

func (e *countingEngine) SizeBytes(ctx context.Context, dbNames []string) (map[string]int64, error) {
	e.probes++
	return e.fakeEngine.SizeBytes(ctx, dbNames)
}
