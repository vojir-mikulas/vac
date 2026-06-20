// Package selfupdate answers "is a newer VAC release available?" by checking the
// project's GitHub releases and comparing against the running build's version.
//
// This is the safe half of self-update (Tier 1): a read-only indicator the
// Instance settings card renders. Applying an upgrade still happens out-of-band
// via `vac upgrade` on the host — vac-api cannot cleanly recreate its own
// container mid-request (see docs/deviations.md on the self-restart mechanism).
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultRepo is the GitHub repo VAC ships from (matches the origin remote and
// the ghcr.io/vojir-mikulas image namespace).
const defaultRepo = "vojir-mikulas/vac"

// Result is one update check. Error is non-empty when the upstream check failed;
// callers treat it as a soft "couldn't check", not a server error.
type Result struct {
	Current         string    `json:"current"`
	Latest          string    `json:"latest"`
	UpdateAvailable bool      `json:"update_available"`
	ReleaseURL      string    `json:"release_url"`
	CheckedAt       time.Time `json:"checked_at"`
	Error           string    `json:"error,omitempty"`
}

// Checker queries GitHub for the latest release, caching the result so opening
// settings repeatedly doesn't hammer the (unauthenticated, 60/hr) API.
type Checker struct {
	current string
	repo    string
	client  *http.Client
	ttl     time.Duration

	mu        sync.Mutex
	cached    *Result
	fetchedAt time.Time
	now       func() time.Time
}

// New builds a Checker for the running build's version string (cfg.Version).
func New(current string) *Checker {
	return &Checker{
		current: strings.TrimSpace(current),
		repo:    defaultRepo,
		client:  &http.Client{Timeout: 8 * time.Second},
		ttl:     time.Hour,
		now:     time.Now,
	}
}

// Check returns the latest cached result, refreshing from GitHub when the cache
// is older than the TTL. A failed refresh re-serves the last good result (with
// the new error attached) and still resets the TTL window so failures back off
// instead of retrying on every request.
func (c *Checker) Check(ctx context.Context) Result {
	c.mu.Lock()
	if c.cached != nil && c.now().Sub(c.fetchedAt) < c.ttl {
		r := *c.cached
		c.mu.Unlock()
		return r
	}
	c.mu.Unlock()

	r := c.fetch(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case r.Error == "":
		c.cached = &r
	case c.cached != nil:
		prev := *c.cached
		prev.Error = r.Error
		r = prev
	}
	c.fetchedAt = c.now()
	return r
}

func (c *Checker) fetch(ctx context.Context) Result {
	res := Result{Current: c.current, CheckedAt: c.now()}
	url := "https://api.github.com/repos/" + c.repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		res.Error = "could not build update request"
		return res
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "vac-update-check")
	resp, err := c.client.Do(req)
	if err != nil {
		res.Error = "could not reach the update server"
		return res
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		res.Error = fmt.Sprintf("update server returned %d", resp.StatusCode)
		return res
	}
	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		res.Error = "could not parse the update response"
		return res
	}
	res.Latest = strings.TrimSpace(body.TagName)
	res.ReleaseURL = strings.TrimSpace(body.HTMLURL)
	res.UpdateAvailable = isNewer(res.Latest, c.current)
	return res
}

// isNewer reports whether latest is a strictly higher semver than current. A
// non-semver current ("dev", a bare git sha, empty) is uncomparable, so we never
// flag an update for it — better silent than a false "update available".
func isNewer(latest, current string) bool {
	if normalizeVer(current) == "" {
		return false
	}
	return compareSemver(latest, current) > 0
}

// normalizeVer strips a leading "v" and any pre-release/build suffix, returning
// the bare "N.N.N" core — or "" when the input isn't numeric-dotted at all.
func normalizeVer(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return ""
	}
	for _, p := range strings.Split(s, ".") {
		if _, err := strconv.Atoi(p); err != nil {
			return ""
		}
	}
	return s
}

// compareSemver returns -1/0/1 comparing the major.minor.patch cores of a and b.
// Uncomparable inputs compare equal (0).
func compareSemver(a, b string) int {
	an, bn := normalizeVer(a), normalizeVer(b)
	if an == "" || bn == "" {
		return 0
	}
	ap, bp := strings.Split(an, "."), strings.Split(bn, ".")
	for i := 0; i < 3; i++ {
		av, bv := 0, 0
		if i < len(ap) {
			av, _ = strconv.Atoi(ap[i])
		}
		if i < len(bp) {
			bv, _ = strconv.Atoi(bp[i])
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}
