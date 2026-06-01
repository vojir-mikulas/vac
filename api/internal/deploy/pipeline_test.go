package deploy

import (
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
