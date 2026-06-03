//go:build integration

package server_test

import (
	"net/http"
	"testing"
)

// createApp POSTs a minimal git app and returns its id. Env tests need a target
// app but don't care about its build config.
func createApp(t *testing.T, h http.Handler, c authedClient, slug string) string {
	t.Helper()
	rr := c.do(t, h, "POST", "/api/apps", map[string]string{
		"name":    slug,
		"git_url": "git@github.com:alice/" + slug + ".git",
		"slug":    slug,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create app: %d body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	mustJSON(t, rr.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("create app response missing id")
	}
	return id
}

// envList fetches the env list as a key→entry map for easy assertions.
func envList(t *testing.T, h http.Handler, c authedClient, appID string) map[string]map[string]any {
	t.Helper()
	rr := c.do(t, h, "GET", "/api/apps/"+appID+"/env", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list env: %d body=%s", rr.Code, rr.Body.String())
	}
	var rows []map[string]any
	mustJSON(t, rr.Body.Bytes(), &rows)
	out := map[string]map[string]any{}
	for _, r := range rows {
		out[r["key"].(string)] = r
	}
	return out
}

// TestEnvWriteOnlyLifecycle exercises P6.1 end to end through the real router,
// store, and crypto box: a write-only secret is sealed but never returned,
// reveal is refused, it survives an untouched full-replace via `keep`, and it
// cannot be downgraded.
func TestEnvWriteOnlyLifecycle(t *testing.T) {
	h, cfg := setupServerWithKey(t)
	c := bootstrapAndLogin(t, h, cfg)
	appID := createApp(t, h, c, "wo-app")

	// 1. Save one plaintext and one write-only var.
	rr := c.do(t, h, "PUT", "/api/apps/"+appID+"/env", map[string]any{
		"vars": []map[string]any{
			{"key": "LOG_LEVEL", "value": "info", "sensitive": false},
			{"key": "API_TOKEN", "value": "top-secret", "write_only": true},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("save env: %d body=%s", rr.Code, rr.Body.String())
	}

	// 2. List omits the write-only value and reports the flag (+ normalized sensitive).
	rows := envList(t, h, c, appID)
	tok := rows["API_TOKEN"]
	if tok == nil {
		t.Fatal("API_TOKEN missing from list")
	}
	if wo, _ := tok["write_only"].(bool); !wo {
		t.Errorf("API_TOKEN write_only = %v, want true", tok["write_only"])
	}
	if sens, _ := tok["sensitive"].(bool); !sens {
		t.Errorf("write-only API_TOKEN should be normalized sensitive, got %v", tok["sensitive"])
	}
	if _, hasVal := tok["value"]; hasVal {
		t.Errorf("write-only API_TOKEN should omit value, got %v", tok["value"])
	}
	// Plaintext value is still returned inline.
	if v, _ := rows["LOG_LEVEL"]["value"].(string); v != "info" {
		t.Errorf("LOG_LEVEL value = %q, want info", v)
	}

	// 3. Reveal on the write-only key → 403, and no value leaks.
	rr = c.do(t, h, "GET", "/api/apps/"+appID+"/env/API_TOKEN/reveal", nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("reveal write-only: %d body=%s, want 403", rr.Code, rr.Body.String())
	}

	// 4. Untouched full-replace via keep:true — the secret survives without plaintext.
	rr = c.do(t, h, "PUT", "/api/apps/"+appID+"/env", map[string]any{
		"vars": []map[string]any{
			{"key": "LOG_LEVEL", "value": "debug", "sensitive": false},
			{"key": "API_TOKEN", "write_only": true, "sensitive": true, "keep": true},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("keep replace: %d body=%s", rr.Code, rr.Body.String())
	}
	rows = envList(t, h, c, appID)
	if rows["API_TOKEN"] == nil {
		t.Fatal("API_TOKEN dropped after keep replace")
	}
	if wo, _ := rows["API_TOKEN"]["write_only"].(bool); !wo {
		t.Errorf("API_TOKEN should still be write-only after keep replace")
	}
	// Reveal still refused (the preserved value is intact and unrevealable).
	if rr = c.do(t, h, "GET", "/api/apps/"+appID+"/env/API_TOKEN/reveal", nil); rr.Code != http.StatusForbidden {
		t.Errorf("reveal after keep: %d, want 403", rr.Code)
	}

	// 5. Downgrade attempt → 400.
	rr = c.do(t, h, "PUT", "/api/apps/"+appID+"/env", map[string]any{
		"vars": []map[string]any{
			{"key": "API_TOKEN", "value": "now-readable", "write_only": false},
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("downgrade write-only: %d body=%s, want 400", rr.Code, rr.Body.String())
	}

	// 6. keep on an unknown key → 400.
	rr = c.do(t, h, "PUT", "/api/apps/"+appID+"/env", map[string]any{
		"vars": []map[string]any{
			{"key": "NOPE", "keep": true},
		},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("keep unknown key: %d body=%s, want 400", rr.Code, rr.Body.String())
	}
}

// TestEnvPlainReplaceRegression guards the new ReplaceAppEnv loop against
// breaking the ordinary (non-write-only) path: a plain sensitive + plaintext
// set saves, lists, and reveals as before.
func TestEnvPlainReplaceRegression(t *testing.T) {
	h, cfg := setupServerWithKey(t)
	c := bootstrapAndLogin(t, h, cfg)
	appID := createApp(t, h, c, "plain-app")

	rr := c.do(t, h, "PUT", "/api/apps/"+appID+"/env", map[string]any{
		"vars": []map[string]any{
			{"key": "GREETING", "value": "hello", "sensitive": false},
			{"key": "PASSWORD", "value": "hunter2", "sensitive": true},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("save env: %d body=%s", rr.Code, rr.Body.String())
	}

	rows := envList(t, h, c, appID)
	if v, _ := rows["GREETING"]["value"].(string); v != "hello" {
		t.Errorf("GREETING value = %q, want hello", v)
	}
	if _, hasVal := rows["PASSWORD"]["value"]; hasVal {
		t.Errorf("sensitive PASSWORD should omit value on list")
	}
	if wo, _ := rows["PASSWORD"]["write_only"].(bool); wo {
		t.Errorf("PASSWORD should not be write-only")
	}

	// Sensitive reveal still works (and is the audited path).
	rr = c.do(t, h, "GET", "/api/apps/"+appID+"/env/PASSWORD/reveal", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("reveal sensitive: %d body=%s", rr.Code, rr.Body.String())
	}
	var revealed map[string]any
	mustJSON(t, rr.Body.Bytes(), &revealed)
	if v, _ := revealed["value"].(string); v != "hunter2" {
		t.Errorf("revealed PASSWORD = %q, want hunter2", v)
	}
}
