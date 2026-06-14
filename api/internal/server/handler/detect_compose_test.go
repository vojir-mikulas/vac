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

func postDetectCompose(t *testing.T, detect DetectComposeFunc, body string) (int, detectComposeResponse) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/apps/detect-compose", strings.NewReader(body))
	DetectCompose(detect)(rr, req)
	var resp detectComposeResponse
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v — body %s", err, rr.Body.String())
		}
	}
	return rr.Code, resp
}

func TestDetectCompose_Found(t *testing.T) {
	detect := func(context.Context, string, string, string) (string, error) {
		return "docker-compose.yml", nil
	}
	code, resp := postDetectCompose(t, detect, `{"git_url":"https://github.com/u/r.git"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !resp.Found || resp.Path != "docker-compose.yml" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestDetectCompose_NotFound(t *testing.T) {
	detect := func(context.Context, string, string, string) (string, error) {
		return "", nil
	}
	code, resp := postDetectCompose(t, detect, `{"git_url":"https://github.com/u/r.git","git_branch":"dev"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp.Found || resp.ErrorCode != "" {
		t.Fatalf("expected found=false with no error, got %+v", resp)
	}
}

func TestDetectCompose_AuthFailureClassified(t *testing.T) {
	detect := func(context.Context, string, string, string) (string, error) {
		return "", gitcli.ErrAuth
	}
	code, resp := postDetectCompose(t, detect, `{"git_url":"git@github.com:u/private.git"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp.Found || resp.ErrorCode != "auth_failed" {
		t.Fatalf("expected found=false with auth_failed, got %+v", resp)
	}
}

func TestDetectCompose_RejectsBadURL(t *testing.T) {
	called := false
	detect := func(context.Context, string, string, string) (string, error) {
		called = true
		return "", nil
	}
	code, _ := postDetectCompose(t, detect, `{"git_url":"not a url"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if called {
		t.Fatal("detect should not be called for an invalid git_url")
	}
}

func TestDetectCompose_RejectsBadBranch(t *testing.T) {
	code, _ := postDetectCompose(t, nil, `{"git_url":"https://github.com/u/r.git","git_branch":"-evil"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}
