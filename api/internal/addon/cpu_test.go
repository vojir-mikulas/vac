package addon

import "testing"

func TestCPUFlagsMeetV2(t *testing.T) {
	tests := []struct {
		name  string
		flags string
		want  bool
	}{
		{
			name:  "modern cpu has all v2 flags",
			flags: "fpu vme de pse tsc msr pae cx16 lahf_lm ssse3 sse4_1 sse4_2 popcnt",
			want:  true,
		},
		{
			// The vm31811 case: a budget VPS that exposes cx16/lahf_lm but no
			// SSE4/POPCNT — MinIO's glibc aborts on it.
			name:  "budget vps missing sse4/popcnt",
			flags: "fpu vme de pse tsc msr pae cx16 lahf_lm",
			want:  false,
		},
		{
			name:  "missing only popcnt",
			flags: "cx16 lahf_lm ssse3 sse4_1 sse4_2",
			want:  false,
		},
		{
			// "sse4" must not satisfy the "sse4_2" requirement.
			name:  "substring does not masquerade",
			flags: "cx16 lahf_lm ssse3 sse4 popcnt",
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cpuFlagsMeetV2(tt.flags); got != tt.want {
				t.Errorf("cpuFlagsMeetV2(%q) = %v, want %v", tt.flags, got, tt.want)
			}
		})
	}
}

func TestHostSupportsBaseline_FailsOpen(t *testing.T) {
	// No requirement and unrecognised levels never claim incompatible.
	if !HostSupportsBaseline("") {
		t.Error(`HostSupportsBaseline("") = false, want true`)
	}
	if !HostSupportsBaseline("x86-64-v9000") {
		t.Error("unknown baseline should fail open (true)")
	}
}
