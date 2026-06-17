package selfupdate

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.6.0", "v0.5.0", true},
		{"v0.5.1", "v0.5.0", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.5.0", "v0.5.0", false},
		{"v0.5.0", "v0.6.0", false},
		{"v0.6.0", "dev", false},    // non-semver current → never nag
		{"v0.6.0", "", false},       // empty build version
		{"v0.6.0", "abc123", false}, // git sha
		{"0.6.0", "0.5.0", true},    // no leading v
		{"v0.6.0-rc1", "v0.5.0", true},
	}
	for _, c := range cases {
		if got := isNewer(c.latest, c.current); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	if compareSemver("1.2.3", "1.2.3") != 0 {
		t.Error("equal versions should compare 0")
	}
	if compareSemver("1.3.0", "1.2.9") <= 0 {
		t.Error("1.3.0 should be greater than 1.2.9")
	}
	if compareSemver("1.2", "1.2.0") != 0 {
		t.Error("1.2 should equal 1.2.0 (missing patch treated as 0)")
	}
}
