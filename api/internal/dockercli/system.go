package dockercli

import (
	"bufio"
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// DiskUsageEntry is one category of `docker system df` (Images, Containers,
// Local Volumes, Build Cache): how much it occupies on disk and how much of that
// is reclaimable — i.e. not referenced by a running container or in-use cache.
type DiskUsageEntry struct {
	Type             string `json:"type"`
	TotalCount       int    `json:"total_count"`
	Active           int    `json:"active"`
	SizeBytes        int64  `json:"size_bytes"`
	ReclaimableBytes int64  `json:"reclaimable_bytes"`
}

// DiskUsage is the parsed `docker system df` summary.
type DiskUsage struct {
	Images     DiskUsageEntry `json:"images"`
	Containers DiskUsageEntry `json:"containers"`
	Volumes    DiskUsageEntry `json:"volumes"`
	BuildCache DiskUsageEntry `json:"build_cache"`
}

// dfEntry is one line of `docker system df --format '{{json .}}'`. Docker emits
// the size fields as human strings ("1.6GB", "1.2GB (75%)"), not bytes, so we
// parse them back below.
type dfEntry struct {
	Type        string `json:"Type"`
	TotalCount  string `json:"TotalCount"`
	Active      string `json:"Active"`
	Size        string `json:"Size"`
	Reclaimable string `json:"Reclaimable"`
}

// SystemDF returns the host's docker disk-usage summary. Sizes come back from
// docker as human strings and are parsed to bytes best-effort: an unparseable
// size degrades to 0 rather than failing the whole call.
func (c *Compose) SystemDF(ctx context.Context) (DiskUsage, error) {
	cmd := c.command(ctx, "", "system", "df", "--format", "{{json .}}")
	out, err := cmd.Output()
	if err != nil {
		return DiskUsage{}, mapCmdError(err, out)
	}
	var du DiskUsage
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e dfEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		entry := DiskUsageEntry{
			Type:             e.Type,
			TotalCount:       atoiSafe(e.TotalCount),
			Active:           atoiSafe(e.Active),
			SizeBytes:        parseDockerSize(e.Size),
			ReclaimableBytes: parseDockerSize(e.Reclaimable),
		}
		switch strings.ToLower(strings.TrimSpace(e.Type)) {
		case "images":
			du.Images = entry
		case "containers":
			du.Containers = entry
		case "local volumes":
			du.Volumes = entry
		case "build cache":
			du.BuildCache = entry
		}
	}
	return du, scanner.Err()
}

// PruneDanglingImages removes dangling (untagged, unreferenced) images via
// `docker image prune -f` and returns the bytes reclaimed, parsed from docker's
// "Total reclaimed space" line (0 if absent). Compose-built images are tagged,
// so this only sweeps the orphaned layers left behind by rebuilds — it never
// removes a service's current or rollback image.
func (c *Compose) PruneDanglingImages(ctx context.Context) (int64, error) {
	cmd := c.command(ctx, "", "image", "prune", "-f")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, mapCmdError(err, out)
	}
	return parseReclaimed(out), nil
}

// PruneBuildCacheAll removes all reclaimable BuildKit cache via
// `docker buildx prune -f`, falling back to `docker builder prune -f` on older
// CLIs, and returns the bytes reclaimed. Cache currently in use is untouched;
// the next deploy simply rebuilds the swept layers. This is the manual,
// reclaim-everything counterpart to the nightly BuildCachePrune cap.
func (c *Compose) PruneBuildCacheAll(ctx context.Context) (int64, error) {
	cmd := c.command(ctx, "", "buildx", "prune", "-f")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return parseReclaimed(out), nil
	}
	fb := c.command(ctx, "", "builder", "prune", "-f")
	if fout, ferr := fb.CombinedOutput(); ferr == nil {
		return parseReclaimed(fout), nil
	}
	return 0, mapCmdError(err, out)
}

// sizeRe matches a docker human size like "1.6GB", "512kB", "0B", "1.2GiB".
// reclaimedRe pulls the size off docker's "Total reclaimed space: …" footer.
var (
	sizeRe      = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*([KMGTP]?I?B)`)
	reclaimedRe = regexp.MustCompile(`(?i)Total reclaimed space:\s*(.+)`)
)

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// parseDockerSize converts a docker human size ("1.6GB", "1.2GB (75%)", "0B")
// to bytes. It strips a trailing "(NN%)" first and accepts both SI (GB, base
// 1000) and binary (GiB, base 1024) suffixes. Unrecognised input yields 0 so a
// format change degrades gracefully rather than erroring.
func parseDockerSize(s string) int64 {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToUpper(m[2])
	base := 1000.0
	if strings.Contains(unit, "I") { // KiB / MiB / … → binary
		base = 1024.0
	}
	exp := 0
	switch unit[0] {
	case 'K':
		exp = 1
	case 'M':
		exp = 2
	case 'G':
		exp = 3
	case 'T':
		exp = 4
	case 'P':
		exp = 5
	default: // plain "B"
		exp = 0
	}
	mult := 1.0
	for i := 0; i < exp; i++ {
		mult *= base
	}
	return int64(val * mult)
}

// parseReclaimed extracts the byte count from a prune command's
// "Total reclaimed space: 1.2GB" footer; 0 when the line is absent.
func parseReclaimed(out []byte) int64 {
	m := reclaimedRe.FindSubmatch(out)
	if m == nil {
		return 0
	}
	return parseDockerSize(string(m[1]))
}
