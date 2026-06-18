package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/gitcli"
)

func postDetectBuild(t *testing.T, detect DetectBuildFunc, body string) (int, detectBuildResponse) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/apps/detect-build", strings.NewReader(body))
	DetectBuild(detect)(rr, req)
	var resp detectBuildResponse
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v — body %s", err, rr.Body.String())
		}
	}
	return rr.Code, resp
}

func TestDetectBuild_Framework(t *testing.T) {
	detect := func(context.Context, string, string) (DetectBuildResult, error) {
		return DetectBuildResult{Framework: "nextjs"}, nil
	}
	code, resp := postDetectBuild(t, detect, `{"git_url":"https://github.com/u/r.git"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp.Framework != "nextjs" || resp.ComposePath != "" || resp.HasDockerfile {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestDetectBuild_Compose(t *testing.T) {
	detect := func(context.Context, string, string) (DetectBuildResult, error) {
		return DetectBuildResult{ComposePath: "compose.yaml"}, nil
	}
	code, resp := postDetectBuild(t, detect, `{"git_url":"https://github.com/u/r.git"}`)
	if code != http.StatusOK || resp.ComposePath != "compose.yaml" {
		t.Fatalf("unexpected: %d %+v", code, resp)
	}
}

func TestDetectBuild_AuthFailureClassified(t *testing.T) {
	detect := func(context.Context, string, string) (DetectBuildResult, error) {
		return DetectBuildResult{}, gitcli.ErrAuth
	}
	code, resp := postDetectBuild(t, detect, `{"git_url":"git@github.com:u/private.git"}`)
	if code != http.StatusOK || resp.ErrorCode != "auth_failed" {
		t.Fatalf("expected auth_failed, got %d %+v", code, resp)
	}
}

func TestDetectBuild_RejectsBadURL(t *testing.T) {
	called := false
	detect := func(context.Context, string, string) (DetectBuildResult, error) {
		called = true
		return DetectBuildResult{}, nil
	}
	code, _ := postDetectBuild(t, detect, `{"git_url":"not a url"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if called {
		t.Fatal("detect should not be called for an invalid git_url")
	}
}
