package stats

import (
	"context"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

// HostSnapshot is the VPS-level view for the global dashboard.
type HostSnapshot struct {
	CPUPercent     float64 `json:"cpu_percent"`
	MemUsedBytes   uint64  `json:"mem_used_bytes"`
	MemTotalBytes  uint64  `json:"mem_total_bytes"`
	DiskUsedBytes  uint64  `json:"disk_used_bytes"`
	DiskTotalBytes uint64  `json:"disk_total_bytes"`
	RequestRate    float64 `json:"request_rate"` // requests/sec, from the Caddy scrape delta
}

// RequestScraper returns the cumulative Caddy request count. *reqmetrics.Scraper
// satisfies it. nil disables the request-rate field.
type RequestScraper interface {
	TotalRequests(ctx context.Context) (float64, error)
}

// HostCollector samples host vitals. It keeps the last request-count reading so
// successive snapshots can report a rate. Safe for concurrent use.
type HostCollector struct {
	scraper  RequestScraper
	diskPath string

	mu        sync.Mutex
	lastTotal float64
	lastAt    time.Time
	haveLast  bool
}

// NewHostCollector wires a collector. diskPath is the filesystem whose usage is
// reported (the VAC data volume); empty falls back to "/".
func NewHostCollector(scraper RequestScraper, diskPath string) *HostCollector {
	if diskPath == "" {
		diskPath = "/"
	}
	return &HostCollector{scraper: scraper, diskPath: diskPath}
}

// Snapshot reads current host vitals. The CPU reading samples briefly so a
// one-off call returns a real value rather than 0.
func (h *HostCollector) Snapshot(ctx context.Context) HostSnapshot {
	var snap HostSnapshot

	if pcts, err := cpu.PercentWithContext(ctx, 200*time.Millisecond, false); err == nil && len(pcts) > 0 {
		snap.CPUPercent = pcts[0]
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		snap.MemUsedBytes = vm.Used
		snap.MemTotalBytes = vm.Total
	}
	if du, err := disk.UsageWithContext(ctx, h.diskPath); err == nil {
		snap.DiskUsedBytes = du.Used
		snap.DiskTotalBytes = du.Total
	}
	snap.RequestRate = h.requestRate(ctx)
	return snap
}

// requestRate scrapes the cumulative count and derives a per-second rate from
// the delta since the previous call. The first call (no baseline) returns 0.
func (h *HostCollector) requestRate(ctx context.Context) float64 {
	if h.scraper == nil {
		return 0
	}
	total, err := h.scraper.TotalRequests(ctx)
	if err != nil {
		return 0
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	var rate float64
	if h.haveLast {
		elapsed := now.Sub(h.lastAt).Seconds()
		if elapsed > 0 && total >= h.lastTotal {
			rate = (total - h.lastTotal) / elapsed
		}
	}
	h.lastTotal = total
	h.lastAt = now
	h.haveLast = true
	return rate
}
