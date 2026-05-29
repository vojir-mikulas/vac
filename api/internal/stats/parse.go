// Package stats produces real-time per-service container metrics and host
// vitals for the dashboard. Per-service samples come from `docker stats`
// (subscriber-gated so no work happens when nobody is watching); host stats
// come from gopsutil plus the Caddy request-rate scrape.
package stats

import (
	"strconv"
	"strings"
)

// Sample is one service's metrics at a point in time — the Data of a "stats"
// frame.
type Sample struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemBytes      int64   `json:"mem_bytes"`
	MemPercent    float64 `json:"mem_percent"`
	NetRxBytes    int64   `json:"net_rx_bytes"`
	NetTxBytes    int64   `json:"net_tx_bytes"`
	UptimeSeconds int64   `json:"uptime_seconds"`
}

// parsePercent turns "12.34%" into 12.34. A malformed value yields 0.
func parsePercent(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseSize turns a docker size string ("12.3MiB", "1.9GB", "0B") into bytes.
// docker uses binary units with an "i" (MiB) for memory and decimal (kB, MB)
// for net/block IO; we accept both. A malformed value yields 0.
func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// Split the numeric prefix from the unit suffix.
	i := 0
	for i < len(s) && (s[i] == '.' || s[i] == '-' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	num, err := strconv.ParseFloat(strings.TrimSpace(s[:i]), 64)
	if err != nil {
		return 0
	}
	unit := strings.ToLower(strings.TrimSpace(s[i:]))
	mult := 1.0
	switch unit {
	case "b", "":
		mult = 1
	case "kb", "kib":
		mult = 1024
	case "mb", "mib":
		mult = 1024 * 1024
	case "gb", "gib":
		mult = 1024 * 1024 * 1024
	case "tb", "tib":
		mult = 1024 * 1024 * 1024 * 1024
	}
	return int64(num * mult)
}

// parsePair splits a "a / b" docker field (NetIO, MemUsage) into its two sizes.
func parsePair(s string) (int64, int64) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return parseSize(s), 0
	}
	return parseSize(parts[0]), parseSize(parts[1])
}
