package handler

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/gitcli"
	"github.com/vojir-mikulas/vac/api/internal/sshkey"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// LsRemoteFunc lets tests stand in a fake for the git binary call.
type LsRemoteFunc func(ctx context.Context, gitURL, branch, sshKeyPath string) error

type testConnectionResponse struct {
	OK           bool   `json:"ok"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// TestConnection wraps `git ls-remote` to verify the configured URL +
// branch + (for SSH) deploy key. Always returns HTTP 200 — the success or
// failure lives in the response body so the UI can render structured
// guidance without parsing a status code.
func TestConnection(s *store.Store, keys *sshkey.Manager, ls LsRemoteFunc) http.HandlerFunc {
	if ls == nil {
		ls = gitcli.LsRemote
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		app, err := s.GetApp(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				WriteError(w, http.StatusNotFound, "app not found")
				return
			}
			WriteError(w, http.StatusInternalServerError, "could not load app")
			return
		}

		var keyPath string
		if isSSHRepoURL(app.GitURL) {
			path, cleanup, err := materialiseKeyForApp(r.Context(), keys, app)
			if err != nil {
				WriteJSON(w, http.StatusOK, testConnectionResponse{
					OK:           false,
					ErrorCode:    "no_key",
					ErrorMessage: err.Error(),
				})
				return
			}
			defer cleanup()
			keyPath = path
		}

		// Use a fresh context for the git probe so a slow git can't pin the
		// HTTP request to its full timeout.
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()

		if err := ls(ctx, app.GitURL, app.GitBranch, keyPath); err != nil {
			WriteJSON(w, http.StatusOK, classifyConnectionErr(err))
			return
		}
		WriteJSON(w, http.StatusOK, testConnectionResponse{OK: true})
	}
}

func classifyConnectionErr(err error) testConnectionResponse {
	switch {
	case errors.Is(err, gitcli.ErrAuth):
		return testConnectionResponse{OK: false, ErrorCode: "auth_failed", ErrorMessage: err.Error()}
	case errors.Is(err, gitcli.ErrRepoNotFound):
		return testConnectionResponse{OK: false, ErrorCode: "repo_not_found", ErrorMessage: err.Error()}
	case errors.Is(err, gitcli.ErrBranchNotFound):
		return testConnectionResponse{OK: false, ErrorCode: "branch_not_found", ErrorMessage: err.Error()}
	case errors.Is(err, gitcli.ErrNetwork):
		return testConnectionResponse{OK: false, ErrorCode: "network", ErrorMessage: err.Error()}
	case errors.Is(err, gitcli.ErrGitMissing):
		return testConnectionResponse{OK: false, ErrorCode: "git_missing", ErrorMessage: err.Error()}
	default:
		return testConnectionResponse{OK: false, ErrorCode: "other", ErrorMessage: err.Error()}
	}
}

// materialiseKeyForApp writes the app's SSH private key to a 0600 temp file
// suitable for use as `ssh -i ...`. Returns the path and a cleanup that
// shreds the file. Mints a key on demand for first deploys.
func materialiseKeyForApp(ctx context.Context, keys *sshkey.Manager, app store.App) (path string, cleanup func(), err error) {
	if _, getErr := keys.Get(ctx, app.ID); errors.Is(getErr, store.ErrNotFound) {
		if _, mintErr := keys.Mint(ctx, app); mintErr != nil {
			return "", func() {}, mintErr
		}
	} else if getErr != nil {
		return "", func() {}, getErr
	}
	pem, err := keys.OpenPrivateKey(ctx, app.ID)
	if err != nil {
		return "", func() {}, err
	}
	f, err := os.CreateTemp("", "vac-ssh-*")
	if err != nil {
		return "", func() {}, err
	}
	// 0600 is required by ssh — it refuses the key file otherwise.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", func() {}, err
	}
	if _, err := f.Write(pem); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", func() {}, err
	}
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}
