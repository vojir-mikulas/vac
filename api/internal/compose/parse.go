package compose

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Service is the shallow view of a compose service VAC cares about. We do
// not implement the full compose schema — only the three things the
// pipeline needs: was a build context declared, what image (if any), and
// which host-side ports were published.
type Service struct {
	Name     string
	Image    string
	HasBuild bool
	Ports    []int
}

// Parse reads a compose file from disk and returns its services in
// alphabetical order. Anything that isn't a service entry is ignored.
func Parse(path string) ([]Service, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is operator-controlled
	if err != nil {
		return nil, fmt.Errorf("compose: read %s: %w", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("compose: parse %s: %w", path, err)
	}
	servicesAny, ok := doc["services"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("compose: %s has no services section", path)
	}

	out := make([]Service, 0, len(servicesAny))
	for name, body := range servicesAny {
		svc := Service{Name: name}
		bodyMap, ok := body.(map[string]any)
		if !ok {
			out = append(out, svc)
			continue
		}
		if image, ok := bodyMap["image"].(string); ok {
			svc.Image = image
		}
		if _, hasBuild := bodyMap["build"]; hasBuild {
			svc.HasBuild = true
		}
		if ports, ok := bodyMap["ports"].([]any); ok {
			svc.Ports = extractPorts(ports)
		}
		out = append(out, svc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// extractPorts pulls host-side port numbers out of the compose `ports:`
// list, which accepts several shapes:
//
//   - 8080:80
//   - 8080
//   - 127.0.0.1:8080:80
//   - { published: 8080, target: 80 }
//
// Only the host-side number matters here — VAC uses it for the health-check
// fallback in M9. Any unparsable entry is silently skipped.
func extractPorts(entries []any) []int {
	var out []int
	for _, e := range entries {
		switch v := e.(type) {
		case string:
			if p := hostPortFromString(v); p > 0 {
				out = append(out, p)
			}
		case int:
			out = append(out, v)
		case map[string]any:
			if p, ok := v["published"]; ok {
				switch pv := p.(type) {
				case int:
					out = append(out, pv)
				case string:
					if n, err := strconv.Atoi(pv); err == nil {
						out = append(out, n)
					}
				}
			}
		}
	}
	return out
}

func hostPortFromString(s string) int {
	// "127.0.0.1:8080:80" → trim leading IP if present.
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		n, _ := strconv.Atoi(parts[0])
		return n
	case 2:
		n, _ := strconv.Atoi(parts[0])
		return n
	case 3:
		n, _ := strconv.Atoi(parts[1])
		return n
	}
	return 0
}
