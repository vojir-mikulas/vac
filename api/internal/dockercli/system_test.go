package dockercli

import "testing"

func TestParseDockerSize(t *testing.T) {
	cases := map[string]int64{
		"0B":          0,
		"1B":          1,
		"512kB":       512 * 1000,
		"1.6GB":       1_600_000_000,
		"1.2GB (75%)": 1_200_000_000,
		"1KiB":        1024,
		"2GiB":        2 * 1024 * 1024 * 1024,
		"  3 MB ":     3_000_000,
		"":            0,
		"garbage":     0,
	}
	for in, want := range cases {
		if got := parseDockerSize(in); got != want {
			t.Errorf("parseDockerSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseReclaimed(t *testing.T) {
	// `docker image prune` style footer.
	out := []byte("deleted: sha256:abc\ndeleted: sha256:def\n\nTotal reclaimed space: 1.2GB\n")
	if got := parseReclaimed(out); got != 1_200_000_000 {
		t.Errorf("parseReclaimed = %d, want 1200000000", got)
	}
	// `docker buildx prune` / `docker builder prune` style footer — a bare
	// "Total:" line after the cache table. Regression: this used to report 0,
	// so the reclaim toast undercounted by the entire build-cache amount.
	buildx := []byte("ID\tRECLAIMABLE\tSIZE\tLAST ACCESSED\nabc123\ttrue\t6.5GB\t2 days ago\nTotal:  6.5GB\n")
	if got := parseReclaimed(buildx); got != 6_500_000_000 {
		t.Errorf("parseReclaimed(buildx) = %d, want 6500000000", got)
	}
	if got := parseReclaimed([]byte("nothing here")); got != 0 {
		t.Errorf("parseReclaimed(no footer) = %d, want 0", got)
	}
}
