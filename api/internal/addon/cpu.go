package addon

import (
	"os"
	"runtime"
	"strings"
	"sync"
)

// CPUBaselineV2 is the manifest value for the x86-64-v2 microarchitecture level.
const CPUBaselineV2 = "x86-64-v2"

// x86-64-v2 is baseline + CMPXCHG16B, LAHF/SAHF, POPCNT, SSE3, SSSE3, SSE4.1,
// SSE4.2. We key detection on the /proc/cpuinfo flag names for the additions a
// generic virtual CPU typically omits; if any is missing the host can't run a
// v2 image (glibc aborts at startup). cx16/lahf_lm are present even on minimal
// QEMU models, so the discriminating flags are the SSE/POPCNT set.
var v2Flags = []string{"sse4_2", "sse4_1", "ssse3", "popcnt"}

var hostV2 = sync.OnceValue(detectHostV2)

// HostSupportsBaseline reports whether this host's CPU meets the named
// microarchitecture level. It FAILS OPEN: an unknown level, a non-Linux host, or
// an unreadable /proc/cpuinfo all return true, so we never hide an add-on that
// would actually run — we only flag the cases we can prove incompatible.
func HostSupportsBaseline(level string) bool {
	switch level {
	case "", CPUBaselineV2:
		// fall through — "" is "no requirement", v2 is the one we detect
	default:
		return true // unrecognised requirement: don't claim incompatible
	}
	if level == "" {
		return true
	}
	return hostV2()
}

func detectHostV2() bool {
	// The requirement is x86-specific; on any other arch (or non-Linux, where we
	// can't read the flags) assume compatible rather than mislabel.
	if runtime.GOARCH != "amd64" || runtime.GOOS != "linux" {
		return true
	}
	raw, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return true
	}
	// The "flags" line lists this CPU's features; one line is enough (all cores
	// share the same set on a single socket, and a missing flag on any core would
	// still abort the image).
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "flags") {
			if _, after, ok := strings.Cut(line, ":"); ok {
				return cpuFlagsMeetV2(after)
			}
		}
	}
	return true // couldn't parse a flags line — fail open
}

// cpuFlagsMeetV2 reports whether a /proc/cpuinfo "flags" value carries every
// discriminating x86-64-v2 flag. Split on whitespace so a substring like "sse4"
// can't masquerade as "sse4_2".
func cpuFlagsMeetV2(flagsLine string) bool {
	present := make(map[string]struct{})
	for _, f := range strings.Fields(flagsLine) {
		present[f] = struct{}{}
	}
	for _, f := range v2Flags {
		if _, ok := present[f]; !ok {
			return false
		}
	}
	return true
}
