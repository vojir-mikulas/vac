package deploy

import (
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/compose"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

func intp(n int) *int { return &n }

func TestRollable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		internalPort *int
		def          *compose.Service
		want         bool
	}{
		{
			name:         "stateless http single replica is rollable",
			internalPort: intp(8080),
			def:          &compose.Service{Name: "web", Replicas: 1},
			want:         true,
		},
		{
			name:         "replicas zero (unset) treated as single",
			internalPort: intp(8080),
			def:          &compose.Service{Name: "web", Replicas: 0},
			want:         true,
		},
		{
			name:         "no internal port (worker) is not rollable",
			internalPort: nil,
			def:          &compose.Service{Name: "worker", Replicas: 1},
			want:         false,
		},
		{
			name:         "has volumes (stateful) is not rollable",
			internalPort: intp(5432),
			def:          &compose.Service{Name: "db", HasVolumes: true, Replicas: 1},
			want:         false,
		},
		{
			name:         "multi replica is not rollable in v1",
			internalPort: intp(8080),
			def:          &compose.Service{Name: "web", Replicas: 3},
			want:         false,
		},
		{
			name:         "no compose definition is not rollable",
			internalPort: intp(8080),
			def:          nil,
			want:         false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rollable(tc.internalPort, tc.def); got != tc.want {
				t.Errorf("rollable() = %v, want %v", got, tc.want)
			}
			// rollableService must agree with the primitive form.
			svc := store.Service{ServiceName: "x", InternalPort: tc.internalPort}
			if got := rollableService(svc, tc.def); got != tc.want {
				t.Errorf("rollableService() = %v, want %v", got, tc.want)
			}
		})
	}
}
