//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// TestPruneDeployments_KeepsWindowRunningAndInFlight verifies the retention
// query: it keeps the keepN most recent deploys per app, always keeps the
// latest running row (even when it has fallen outside the window), never
// touches in-flight rows, and cascade-deletes the build logs of pruned rows.
func TestPruneDeployments_KeepsWindowRunningAndInFlight(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "prune-deploys")

	// Oldest: a successful running deploy — the live version's rollback target.
	running, err := s.CreateDeployment(ctx, a.ID, store.TriggeredManual, nil)
	if err != nil {
		t.Fatalf("create running: %v", err)
	}
	sha := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	if err := s.SetDeploymentCommit(ctx, running.ID, &sha, stringPtr("ship it")); err != nil {
		t.Fatalf("set commit: %v", err)
	}
	if err := s.MarkDeploymentFinished(ctx, running.ID, "running", nil); err != nil {
		t.Fatalf("finish running: %v", err)
	}

	// A never-finished (queued) deploy — non-terminal, must survive regardless.
	inflight, err := s.CreateDeployment(ctx, a.ID, store.TriggeredManual, nil)
	if err != nil {
		t.Fatalf("create inflight: %v", err)
	}

	// Four newer failed deploys; the two oldest of these should be pruned.
	var errored []store.Deployment
	for i := 0; i < 4; i++ {
		d, err := s.CreateDeployment(ctx, a.ID, store.TriggeredManual, nil)
		if err != nil {
			t.Fatalf("create error %d: %v", i, err)
		}
		if err := s.MarkDeploymentFinished(ctx, d.ID, "error", stringPtr("boom")); err != nil {
			t.Fatalf("finish error %d: %v", i, err)
		}
		errored = append(errored, d)
	}

	// Attach a build-log line to the oldest errored deploy to prove cascade.
	victim := errored[0]
	if _, err := s.AppendDeploymentLogs(ctx, victim.ID, []store.DeploymentLogRow{{Stream: "stdout", Message: "x"}}); err != nil {
		t.Fatalf("append log: %v", err)
	}

	n, err := s.PruneDeployments(ctx, 2)
	if err != nil {
		t.Fatalf("PruneDeployments: %v", err)
	}
	// errored has 4; keepN=2 keeps the 2 newest of them. running is kept by the
	// guard, inflight is non-terminal. So the 2 oldest errored rows are deleted.
	if n != 2 {
		t.Errorf("pruned = %d, want 2", n)
	}

	// running + inflight survive.
	if _, err := s.GetDeployment(ctx, running.ID); err != nil {
		t.Errorf("running deploy should survive: %v", err)
	}
	if _, err := s.GetDeployment(ctx, inflight.ID); err != nil {
		t.Errorf("in-flight deploy should survive: %v", err)
	}
	// Oldest errored deploy is gone, and its logs cascaded.
	if _, err := s.GetDeployment(ctx, victim.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("oldest errored deploy should be pruned, got err=%v", err)
	}
	if c, err := s.CountDeploymentLogs(ctx, victim.ID); err != nil {
		t.Fatalf("count logs: %v", err)
	} else if c != 0 {
		t.Errorf("pruned deploy logs = %d, want 0 (cascade)", c)
	}
}

func TestPruneDeployments_KeepNZeroIsNoOp(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "prune-noop")
	d, err := s.CreateDeployment(ctx, a.ID, store.TriggeredManual, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MarkDeploymentFinished(ctx, d.ID, "error", stringPtr("x")); err != nil {
		t.Fatalf("finish: %v", err)
	}
	n, err := s.PruneDeployments(ctx, 0)
	if err != nil {
		t.Fatalf("PruneDeployments(0): %v", err)
	}
	if n != 0 {
		t.Errorf("keepN=0 pruned %d rows, want 0 (disabled)", n)
	}
	if _, err := s.GetDeployment(ctx, d.ID); err != nil {
		t.Errorf("deploy should survive a no-op prune: %v", err)
	}
}

func TestListServiceProjects(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "svc-projects")
	cid := "c1"
	port := 8080
	if _, err := s.UpsertService(ctx, a.ID, "web", &cid, &port, &port, "running"); err != nil {
		t.Fatalf("upsert web: %v", err)
	}
	if _, err := s.UpsertService(ctx, a.ID, "worker", nil, nil, nil, "running"); err != nil {
		t.Fatalf("upsert worker: %v", err)
	}

	pairs, err := s.ListServiceProjects(ctx)
	if err != nil {
		t.Fatalf("ListServiceProjects: %v", err)
	}
	found := map[string]bool{}
	for _, p := range pairs {
		if p.Slug == a.Slug {
			found[p.ServiceName] = true
		}
	}
	if !found["web"] || !found["worker"] {
		t.Errorf("expected web+worker for slug %q, got %+v", a.Slug, pairs)
	}
}
