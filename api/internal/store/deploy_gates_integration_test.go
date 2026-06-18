//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// TestScheduledDeployLifecycle covers the deploy-window park/release path
// (maintenance-mode-and-deploy-gates.md, Phase 3): a `scheduled` deploy is
// created, listed with its app's window, released to `queued`, and the per-app
// uniqueness guard counts it as active.
func TestScheduledDeployLifecycle(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	a := testApp(t, s, "sched")

	window := json.RawMessage(`[{"days":[1],"start":"09:00","end":"17:00","tz":"UTC"}]`)
	if err := s.SetAppDeployWindow(ctx, a.ID, window); err != nil {
		t.Fatalf("SetAppDeployWindow: %v", err)
	}

	d, err := s.CreateDeploymentWithStatus(ctx, a.ID, store.TriggeredPush, "scheduled", nil)
	if err != nil {
		t.Fatalf("CreateDeploymentWithStatus: %v", err)
	}
	if d.Status != "scheduled" {
		t.Fatalf("status = %q, want scheduled", d.Status)
	}

	// A second active deploy can't stack (uniqueness guard includes 'scheduled').
	if _, err := s.CreateDeployment(ctx, a.ID, store.TriggeredPush, nil); !errors.Is(err, store.ErrActiveDeploymentExists) {
		t.Fatalf("second deploy err = %v, want ErrActiveDeploymentExists", err)
	}

	// The sweeper sees it with the app's window attached.
	parked, err := s.ListScheduledDeployments(ctx)
	if err != nil {
		t.Fatalf("ListScheduledDeployments: %v", err)
	}
	var found *store.ScheduledDeploy
	for i := range parked {
		if parked[i].DeploymentID == d.ID {
			found = &parked[i]
		}
	}
	if found == nil {
		t.Fatal("scheduled deploy not listed")
	}
	if len(found.DeployWindow) == 0 {
		t.Fatal("scheduled deploy missing its app's deploy_window")
	}

	// Release flips it to queued.
	if err := s.ReleaseScheduledDeployment(ctx, d.ID); err != nil {
		t.Fatalf("ReleaseScheduledDeployment: %v", err)
	}
	got, _ := s.GetDeployment(ctx, d.ID)
	if got.Status != "queued" {
		t.Fatalf("after release, status = %q, want queued", got.Status)
	}
	// A second release is a no-op (status no longer scheduled).
	if err := s.ReleaseScheduledDeployment(ctx, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("double release err = %v, want ErrNotFound", err)
	}
}

// TestApprovalLifecycle covers the approval-gate path (Phase 4): a
// `pending-approval` deploy is listed, approved (→ queued) or rejected
// (→ canceled), and approving a non-pending deploy fails.
func TestApprovalLifecycle(t *testing.T) {
	ctx := context.Background()
	s := setup(t)

	t.Run("approve", func(t *testing.T) {
		a := testApp(t, s, "appr-ok")
		d, err := s.CreateDeploymentWithStatus(ctx, a.ID, store.TriggeredPush, "pending-approval", nil)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		pending, err := s.ListPendingApprovals(ctx, a.ID)
		if err != nil || len(pending) != 1 || pending[0].ID != d.ID {
			t.Fatalf("ListPendingApprovals = %+v, %v", pending, err)
		}
		approved, err := s.ApproveDeployment(ctx, a.ID, d.ID)
		if err != nil {
			t.Fatalf("ApproveDeployment: %v", err)
		}
		if approved.Status != "queued" {
			t.Fatalf("status = %q, want queued", approved.Status)
		}
		// Approving again (now queued, not pending) fails.
		if _, err := s.ApproveDeployment(ctx, a.ID, d.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("re-approve err = %v, want ErrNotFound", err)
		}
	})

	t.Run("reject", func(t *testing.T) {
		a := testApp(t, s, "appr-no")
		d, err := s.CreateDeploymentWithStatus(ctx, a.ID, store.TriggeredPush, "pending-approval", nil)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		rejected, err := s.RejectDeployment(ctx, a.ID, d.ID)
		if err != nil {
			t.Fatalf("RejectDeployment: %v", err)
		}
		if rejected.Status != "canceled" {
			t.Fatalf("status = %q, want canceled", rejected.Status)
		}
		if rejected.Error == nil {
			t.Fatal("rejected deploy should record a reason")
		}
		// Rejecting frees the app — a fresh deploy is allowed.
		if _, err := s.CreateDeployment(ctx, a.ID, store.TriggeredPush, nil); err != nil {
			t.Fatalf("deploy after reject: %v", err)
		}
	})
}

// TestRequireApprovalTrigger covers the deploy_triggers.require_approval column.
func TestRequireApprovalTrigger(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	a := testApp(t, s, "appr-trig")

	tr, err := s.CreateDeployTrigger(ctx, a.ID, store.TriggerEventPush, "release/*", true)
	if err != nil {
		t.Fatalf("CreateDeployTrigger: %v", err)
	}
	if !tr.RequireApproval {
		t.Fatal("RequireApproval should round-trip true")
	}
	rows, err := s.ListDeployTriggers(ctx, a.ID)
	if err != nil || len(rows) != 1 || !rows[0].RequireApproval {
		t.Fatalf("ListDeployTriggers = %+v, %v", rows, err)
	}
}
