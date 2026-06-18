//go:build integration

package store_test

import (
	"context"
	"testing"
)

// TestMaintenanceFlags exercises the maintenance columns + their store methods
// (docs/plans/maintenance-mode-and-deploy-gates.md): the manual toggle, the
// pipeline's active raise/clear, the manual-survives-deploy invariant, and the
// custom-page set/clear path.
func TestMaintenanceFlags(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	a := testApp(t, s, "maint-flags")

	// Defaults: everything off, no custom page.
	if a.MaintenanceMode || a.MaintenanceAuto || a.MaintenanceActive || a.MaintenanceHTML != nil {
		t.Fatalf("fresh app should have maintenance off, got %+v", a)
	}

	// Manual toggle on → mode + active set, auto opted in.
	if err := s.SetAppMaintenance(ctx, a.ID, true, true); err != nil {
		t.Fatalf("SetAppMaintenance on: %v", err)
	}
	got, _ := s.GetApp(ctx, a.ID)
	if !got.MaintenanceMode || !got.MaintenanceAuto || !got.MaintenanceActive {
		t.Fatalf("after toggle on: %+v", got)
	}

	// Pipeline clear-on-exit must NOT clear active while manual mode is set
	// (manual maintenance survives a deploy).
	cleared, err := s.ClearAppMaintenanceActiveIfManualOff(ctx, a.ID)
	if err != nil {
		t.Fatalf("ClearAppMaintenanceActiveIfManualOff: %v", err)
	}
	if cleared {
		t.Fatal("active should NOT clear while manual maintenance is set")
	}
	got, _ = s.GetApp(ctx, a.ID)
	if !got.MaintenanceActive {
		t.Fatal("manual maintenance must survive a deploy's clear")
	}

	// Manual toggle off → mode + active cleared.
	if err := s.SetAppMaintenance(ctx, a.ID, false, false); err != nil {
		t.Fatalf("SetAppMaintenance off: %v", err)
	}
	got, _ = s.GetApp(ctx, a.ID)
	if got.MaintenanceMode || got.MaintenanceActive {
		t.Fatalf("after toggle off: %+v", got)
	}

	// Pipeline raise (auto-maintenance during deploy), then clear-on-exit clears
	// it because manual mode is off.
	if err := s.SetAppMaintenanceActive(ctx, a.ID, true); err != nil {
		t.Fatalf("SetAppMaintenanceActive: %v", err)
	}
	cleared, err = s.ClearAppMaintenanceActiveIfManualOff(ctx, a.ID)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !cleared {
		t.Fatal("auto-maintenance active should clear when manual mode is off")
	}
	got, _ = s.GetApp(ctx, a.ID)
	if got.MaintenanceActive {
		t.Fatal("active should be cleared after a deploy with manual mode off")
	}
}

// TestMaintenanceHTML covers storing, reading and clearing a custom page, and
// that the 64 KB DB CHECK constraint rejects an oversized page.
func TestMaintenanceHTML(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	a := testApp(t, s, "maint-html")

	html := "<h1>back soon</h1>"
	if err := s.SetAppMaintenanceHTML(ctx, a.ID, &html); err != nil {
		t.Fatalf("SetAppMaintenanceHTML: %v", err)
	}
	got, _ := s.GetApp(ctx, a.ID)
	if got.MaintenanceHTML == nil || *got.MaintenanceHTML != html {
		t.Fatalf("custom page = %v, want %q", got.MaintenanceHTML, html)
	}

	// Reset to default (nil).
	if err := s.SetAppMaintenanceHTML(ctx, a.ID, nil); err != nil {
		t.Fatalf("clear page: %v", err)
	}
	got, _ = s.GetApp(ctx, a.ID)
	if got.MaintenanceHTML != nil {
		t.Fatalf("page should be nil after reset, got %v", got.MaintenanceHTML)
	}

	// The DB CHECK constraint enforces the 64 KB cap (belt-and-braces with the
	// handler's maintenance.Validate).
	oversized := string(make([]byte, 65537))
	if err := s.SetAppMaintenanceHTML(ctx, a.ID, &oversized); err == nil {
		t.Fatal("expected the 64 KB CHECK constraint to reject an oversized page")
	}
}
