package handler

import (
	"expvar"
	"net/http"
	"runtime"
	"runtime/debug"
)

// DebugVars serves the standard library's expvar handler, which publishes
// `runtime.ReadMemStats` under the "memstats" key (HeapAlloc / HeapSys / Sys).
// The RAM benchmark (plan 07) reads it to separate Go-heap allocations from
// total RSS. It's re-exported here so the router wraps it with the
// metrics-token guard instead of relying on expvar's implicit registration on
// http.DefaultServeMux.
func DebugVars() http.Handler {
	return expvar.Handler()
}

// ForceGC runs a full garbage collection and returns freed memory to the OS,
// then reports the post-GC heap. The benchmark hits this to reach a
// deterministic steady state before reading the cgroup memory figure, so a
// transient allocation spike doesn't get measured as resident set.
func ForceGC() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		runtime.GC()
		debug.FreeOSMemory()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		WriteJSON(w, http.StatusOK, map[string]uint64{
			"heap_alloc_bytes": m.HeapAlloc,
			"heap_sys_bytes":   m.HeapSys,
			"sys_bytes":        m.Sys,
		})
	}
}
