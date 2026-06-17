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
	out := []byte("deleted: sha256:abc\ndeleted: sha256:def\n\nTotal reclaimed space: 1.2GB\n")
	if got := parseReclaimed(out); got != 1_200_000_000 {
		t.Errorf("parseReclaimed = %d, want 1200000000", got)
	}
	if got := parseReclaimed([]byte("nothing here")); got != 0 {
		t.Errorf("parseReclaimed(no footer) = %d, want 0", got)
	}
}
