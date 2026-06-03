//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// TestPerAppDeployGuard verifies the one_active_deploy_per_app partial unique
// index (migration 00062): an app may have at most one non-terminal deployment.
// A second create while one is active returns ErrActiveDeploymentExists; once the
// first settles (terminal), a new deploy is allowed again. This is what lets the
// worker pool run >1 deploy concurrently without two workers racing one app.
func TestPerAppDeployGuard(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	app := testApp(t, s, "guarded")

	first, err := s.CreateDeployment(ctx, app.ID, store.TriggeredManual, nil)
	if err != nil {
		t.Fatalf("first CreateDeployment: %v", err)
	}

	// Second create while the first is still queued (non-terminal) is rejected.
	if _, err := s.CreateDeployment(ctx, app.ID, store.TriggeredManual, nil); !errors.Is(err, store.ErrActiveDeploymentExists) {
		t.Fatalf("second CreateDeployment err = %v, want ErrActiveDeploymentExists", err)
	}

	// A different app is unaffected — the guard is per-app, not global.
	other := testApp(t, s, "other")
	if _, err := s.CreateDeployment(ctx, other.ID, store.TriggeredManual, nil); err != nil {
		t.Fatalf("CreateDeployment for a different app: %v", err)
	}

	// Settle the first deploy terminally; the app is then free to deploy again.
	if err := s.MarkDeploymentFinished(ctx, first.ID, "canceled", nil); err != nil {
		t.Fatalf("MarkDeploymentFinished: %v", err)
	}
	if _, err := s.CreateDeployment(ctx, app.ID, store.TriggeredManual, nil); err != nil {
		t.Fatalf("CreateDeployment after first settled: %v", err)
	}
}

// TestListActiveDeployments confirms the queue snapshot returns only
// non-terminal rows, in FIFO order, joined to the app name/slug.
func TestListActiveDeployments(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	a := testApp(t, s, "queue-a")
	b := testApp(t, s, "queue-b")

	da, err := s.CreateDeployment(ctx, a.ID, store.TriggeredManual, nil)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := s.CreateDeployment(ctx, b.ID, store.TriggeredManual, nil); err != nil {
		t.Fatalf("create b: %v", err)
	}

	active, err := s.ListActiveDeployments(ctx)
	if err != nil {
		t.Fatalf("ListActiveDeployments: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active len = %d, want 2", len(active))
	}
	// FIFO: a was created first.
	if active[0].AppSlug != "queue-a" || active[0].AppName == "" {
		t.Errorf("first active = %+v, want app queue-a with a name", active[0])
	}

	// Settling one drops it from the snapshot.
	if err := s.MarkDeploymentFinished(ctx, da.ID, "running", nil); err != nil {
		t.Fatalf("finish a: %v", err)
	}
	active, err = s.ListActiveDeployments(ctx)
	if err != nil {
		t.Fatalf("ListActiveDeployments after settle: %v", err)
	}
	if len(active) != 1 || active[0].AppSlug != "queue-b" {
		t.Fatalf("after settle, active = %+v, want only queue-b", active)
	}
}
