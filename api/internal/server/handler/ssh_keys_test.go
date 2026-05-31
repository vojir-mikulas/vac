package handler

import "testing"

func TestIsSSHRepoURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		url  string
		want bool
	}{
		{"git@github.com:user/repo.git", true},
		{"ssh://git@github.com/user/repo.git", true},
		{"  git@gitlab.local:foo/bar  ", true}, // surrounding whitespace tolerated
		{"https://github.com/user/repo.git", false},
		{"http://example.com/repo", false},
		{"git@noslashes", false}, // missing the host:path separator
		{"", false},
		{"file:///tmp/repo", false},
	}
	for _, c := range cases {
		if got := isSSHRepoURL(c.url); got != c.want {
			t.Errorf("isSSHRepoURL(%q) = %v; want %v", c.url, got, c.want)
		}
	}
}
