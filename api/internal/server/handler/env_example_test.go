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

func postEnvExample(t *testing.T, read ReadEnvExampleFunc, body string) (int, envExampleResponse) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/apps/env-example", strings.NewReader(body))
	EnvExample(read)(rr, req)
	var resp envExampleResponse
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v — body %s", err, rr.Body.String())
		}
	}
	return rr.Code, resp
}

func TestEnvExample_Found(t *testing.T) {
	read := func(context.Context, string, string, string) (string, []byte, error) {
		return ".env.example", []byte("FOO=bar\n"), nil
	}
	code, resp := postEnvExample(t, read, `{"git_url":"https://github.com/u/r.git"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !resp.Found || resp.File != ".env.example" || resp.Content != "FOO=bar\n" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestEnvExample_NotFound(t *testing.T) {
	read := func(context.Context, string, string, string) (string, []byte, error) {
		return "", nil, nil
	}
	code, resp := postEnvExample(t, read, `{"git_url":"https://github.com/u/r.git","git_branch":"dev"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp.Found || resp.ErrorCode != "" {
		t.Fatalf("expected found=false with no error, got %+v", resp)
	}
}

func TestEnvExample_AuthFailureClassified(t *testing.T) {
	read := func(context.Context, string, string, string) (string, []byte, error) {
		return "", nil, gitcli.ErrAuth
	}
	code, resp := postEnvExample(t, read, `{"git_url":"git@github.com:u/private.git"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if resp.Found || resp.ErrorCode != "auth_failed" {
		t.Fatalf("expected found=false with auth_failed, got %+v", resp)
	}
}

func TestEnvExample_RejectsBadURL(t *testing.T) {
	called := false
	read := func(context.Context, string, string, string) (string, []byte, error) {
		called = true
		return "", nil, nil
	}
	code, _ := postEnvExample(t, read, `{"git_url":"not a url"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if called {
		t.Fatal("read should not be called for an invalid git_url")
	}
}

func TestEnvExample_RejectsBadBranch(t *testing.T) {
	code, _ := postEnvExample(t, nil, `{"git_url":"https://github.com/u/r.git","git_branch":"-evil"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}
