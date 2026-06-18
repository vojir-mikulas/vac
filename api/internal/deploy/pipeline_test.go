package deploy

import (
	"context"
	"log/slog"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

func TestRollbackTargetSHA(t *testing.T) {
	sha := "abc123def456"
	tests := []struct {
		name string
		dep  store.Deployment
		want string
	}{
		{
			name: "rollback with pinned sha",
			dep:  store.Deployment{TriggeredBy: store.TriggeredRollback, CommitSHA: &sha},
			want: sha,
		},
		{
			name: "rollback without recorded sha falls back to HEAD",
			dep:  store.Deployment{TriggeredBy: store.TriggeredRollback, CommitSHA: nil},
			want: "",
		},
		{
			name: "manual deploy never pins",
			dep:  store.Deployment{TriggeredBy: store.TriggeredManual, CommitSHA: &sha},
			want: "",
		},
		{
			name: "push deploy never pins",
			dep:  store.Deployment{TriggeredBy: store.TriggeredPush, CommitSHA: &sha},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rollbackTargetSHA(tc.dep); got != tc.want {
				t.Errorf("rollbackTargetSHA() = %q, want %q", got, tc.want)
			}
		})
	}
}

// fakeMaintainer records MaintainOn/Off calls so the auto-maintenance seam can
// be tested without a full pipeline run.
type fakeMaintainer struct {
	on, off int
}

func (f *fakeMaintainer) MaintainOn(context.Context, string) error  { f.on++; return nil }
func (f *fakeMaintainer) MaintainOff(context.Context, string) error { f.off++; return nil }

// TestEnterAutoMaintenance covers the deploy pipeline's auto-maintenance seam:
// when the app opts in, MaintainOn fires immediately and the returned cleanup
// (deferred by Run on every exit path) calls MaintainOff — proving the page
// clears on both success and failure. When the app hasn't opted in, or no
// Maintainer is wired, it's a no-op.
func TestEnterAutoMaintenance(t *testing.T) {
	t.Run("opted in: on now, off on cleanup", func(t *testing.T) {
		fm := &fakeMaintainer{}
		p := &Pipeline{Maintainer: fm, Logger: slog.Default()}
		cleanup := p.enterAutoMaintenance(context.Background(), store.App{ID: "a1", MaintenanceAuto: true})
		if fm.on != 1 {
			t.Fatalf("MaintainOn calls = %d, want 1", fm.on)
		}
		if fm.off != 0 {
			t.Fatalf("MaintainOff should not fire before cleanup, got %d", fm.off)
		}
		cleanup()
		if fm.off != 1 {
			t.Fatalf("MaintainOff calls = %d, want 1", fm.off)
		}
	})

	t.Run("not opted in: no-op", func(t *testing.T) {
		fm := &fakeMaintainer{}
		p := &Pipeline{Maintainer: fm, Logger: slog.Default()}
		p.enterAutoMaintenance(context.Background(), store.App{ID: "a1", MaintenanceAuto: false})()
		if fm.on != 0 || fm.off != 0 {
			t.Fatalf("expected no maintenance calls, got on=%d off=%d", fm.on, fm.off)
		}
	})

	t.Run("no maintainer wired: no panic", func(t *testing.T) {
		p := &Pipeline{Logger: slog.Default()}
		p.enterAutoMaintenance(context.Background(), store.App{ID: "a1", MaintenanceAuto: true})()
	})
}
