package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/gitcli"
)

type envExampleRequest struct {
	GitURL    string `json:"git_url"`
	GitBranch string `json:"git_branch,omitempty"`
}

// envExampleResponse mirrors testConnectionResponse's shape: HTTP is always 200
// and the outcome lives in the body, so the wizard can render structured
// guidance (found / not found / private-repo) without parsing a status code.
type envExampleResponse struct {
	Found        bool   `json:"found"`
	File         string `json:"file,omitempty"`
	Content      string `json:"content,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// ReadEnvExampleFunc lets tests stand in a fake for the git clone+read.
type ReadEnvExampleFunc func(ctx context.Context, gitURL, branch, sshKeyPath string) (string, []byte, error)

// EnvExample probes a repository for a conventional `.env.example` (or similar)
// file so the new-app wizard can pre-fill environment variables. It clones
// WITHOUT a deploy key — the wizard calls this before the app (and its key)
// exist, so public HTTPS repos resolve while private SSH repos come back as
// `auth_failed` and the UI falls back to manual entry.
func EnvExample(read ReadEnvExampleFunc) http.HandlerFunc {
	if read == nil {
		read = gitcli.ReadEnvExample
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var req envExampleRequest
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

		file, content, err := read(ctx, req.GitURL, branch, "")
		if err != nil {
			c := classifyConnectionErr(err)
			WriteJSON(w, http.StatusOK, envExampleResponse{
				ErrorCode:    c.ErrorCode,
				ErrorMessage: c.ErrorMessage,
			})
			return
		}
		if file == "" {
			WriteJSON(w, http.StatusOK, envExampleResponse{Found: false})
			return
		}
		WriteJSON(w, http.StatusOK, envExampleResponse{
			Found:   true,
			File:    file,
			Content: string(content),
		})
	}
}
