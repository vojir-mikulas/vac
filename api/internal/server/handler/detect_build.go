package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/adapter"
	"github.com/vojir-mikulas/vac/api/internal/compose"
	"github.com/vojir-mikulas/vac/api/internal/gitcli"
)

type detectBuildRequest struct {
	GitURL    string `json:"git_url"`
	GitBranch string `json:"git_branch,omitempty"`
}

// detectBuildResponse reports everything the wizard needs to pick (and badge) a
// build source from one keyless clone: whether a compose file or Dockerfile is
// present, and the auto-detected framework. Like detectComposeResponse it's
// always HTTP 200 with the outcome in the body, so a private repo degrades to a
// silent `auth_failed` rather than a hard error.
type detectBuildResponse struct {
	ComposePath   string `json:"compose_path,omitempty"`
	HasDockerfile bool   `json:"has_dockerfile"`
	Framework     string `json:"framework,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
}

// DetectBuildResult is what the clone+probe produces; broken out so tests can
// stand in a fake without a real git clone.
type DetectBuildResult struct {
	ComposePath   string
	HasDockerfile bool
	Framework     string
}

// DetectBuildFunc clones gitURL (keyless) and inspects the checkout.
type DetectBuildFunc func(ctx context.Context, gitURL, branch string) (DetectBuildResult, error)

// DetectBuild probes a repository for its build source — compose file,
// Dockerfile, or a known framework — so the new-app wizard can pre-select a
// build kind and tell the operator "we detected a Next.js app". Clones WITHOUT a
// deploy key (the app doesn't exist yet), so public HTTPS repos resolve and
// private SSH repos come back as `auth_failed`.
func DetectBuild(detect DetectBuildFunc) http.HandlerFunc {
	if detect == nil {
		detect = defaultDetectBuild
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var req detectBuildRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json")
			return
		}
		req.GitURL = strings.TrimSpace(req.GitURL)
		req.GitBranch = strings.TrimSpace(req.GitBranch)
		if !gitURLRe.MatchString(req.GitURL) {
			WriteError(w, http.StatusBadRequest, "git_url must be an https:// or git@ SSH URL")
			return
		}
		branch := req.GitBranch
		if branch == "" {
			branch = defaultBranch
		}
		if !gitRefRe.MatchString(branch) {
			WriteError(w, http.StatusBadRequest, "git_branch must match "+gitRefRe.String())
			return
		}

		// Fresh context so a slow git can't pin the request to the parent timeout.
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		res, err := detect(ctx, req.GitURL, branch)
		if err != nil {
			c := classifyConnectionErr(err)
			WriteJSON(w, http.StatusOK, detectBuildResponse{
				ErrorCode:    c.ErrorCode,
				ErrorMessage: c.ErrorMessage,
			})
			return
		}
		WriteJSON(w, http.StatusOK, detectBuildResponse{
			ComposePath:   res.ComposePath,
			HasDockerfile: res.HasDockerfile,
			Framework:     res.Framework,
		})
	}
}

// defaultDetectBuild clones the repo once and runs the same detection the deploy
// pipeline uses: compose.Detect (compose file → Dockerfile-only) plus the
// framework probe. A compose file shadows the Dockerfile flag — matching the
// auto-detect precedence (compose → dockerfile → framework).
func defaultDetectBuild(ctx context.Context, gitURL, branch string) (DetectBuildResult, error) {
	dir, cleanup, err := gitcli.CloneTemp(ctx, gitURL, branch, "")
	if err != nil {
		return DetectBuildResult{}, err
	}
	defer cleanup()

	var out DetectBuildResult
	if res, derr := compose.Detect(dir); derr == nil {
		if res.Source == compose.SourceGenerated {
			out.HasDockerfile = true
		} else {
			out.ComposePath = string(res.Source)
		}
	}
	out.Framework = adapter.DetectFramework(dir)
	return out, nil
}
