package handler

import "testing"

func TestDescribeServicePatch(t *testing.T) {
	t.Parallel()
	ip := func(n int) *int { return &n }
	sp := func(s string) *string { return &s }
	cases := []struct {
		name string
		req  patchServiceRequest
		want string
	}{
		{"web", patchServiceRequest{InternalPort: ip(8080)}, "set web internal port to 8080"},
		{"api", patchServiceRequest{HealthPath: sp("/healthz")}, "set api health path to /healthz"},
		{"db", patchServiceRequest{ExposedPort: ip(5432)}, "set db exposed port to 5432"},
		// internal_port wins when several fields are set — it's the routing-relevant one.
		{"web", patchServiceRequest{InternalPort: ip(3001), HealthPath: sp("/up")}, "set web internal port to 3001"},
		{"worker", patchServiceRequest{}, "updated service worker"},
	}
	for _, c := range cases {
		if got := describeServicePatch(c.name, c.req); got != c.want {
			t.Errorf("describeServicePatch(%q, %+v) = %q; want %q", c.name, c.req, got, c.want)
		}
	}
}
