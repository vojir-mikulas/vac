package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/gitcli"
)

type detectComposeRequest struct {
	GitURL    string `json:"git_url"`
	GitBranch string `json:"git_branch,omitempty"`
}

// detectComposeResponse mirrors envExampleResponse's shape: HTTP is always 200
// and the outcome lives in the body, so the wizard can pre-fill the compose path
// (or fall back silently for a private repo) without parsing a status code.
type detectComposeResponse struct {
	Found        bool   `json:"found"`
	Path         string `json:"path,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// DetectComposeFunc lets tests stand in a fake for the git clone+probe.
type DetectComposeFunc func(ctx context.Context, gitURL, branch, sshKeyPath string) (string, error)

// DetectCompose probes a repository for a conventional compose file so the
// new-app wizard can pre-fill the compose path. It clones WITHOUT a deploy key —
// the wizard calls this before the app (and its key) exist, so public HTTPS
// repos resolve while private SSH repos come back as `auth_failed` and the UI
// falls back to the manual path input.
func DetectCompose(detect DetectComposeFunc) http.HandlerFunc {
	if detect == nil {
		detect = gitcli.DetectCompose
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var req detectComposeRequest
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

		path, err := detect(ctx, req.GitURL, branch, "")
		if err != nil {
			c := classifyConnectionErr(err)
			WriteJSON(w, http.StatusOK, detectComposeResponse{
				ErrorCode:    c.ErrorCode,
				ErrorMessage: c.ErrorMessage,
			})
			return
		}
		if path == "" {
			WriteJSON(w, http.StatusOK, detectComposeResponse{Found: false})
			return
		}
		WriteJSON(w, http.StatusOK, detectComposeResponse{Found: true, Path: path})
	}
}
