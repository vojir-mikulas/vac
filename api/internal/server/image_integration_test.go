//go:build integration

package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestImageAppCreateAndRegistryAuth covers the deploy-from-image create path and
// the sealed private-registry credential endpoints: an image app needs no
// git_url, records source="image", and its credentials are write-only (the
// password never appears in any API response).
func TestImageAppCreateAndRegistryAuth(t *testing.T) {
	h, cfg := setupServerWithKey(t)
	c := bootstrapAndLogin(t, h, cfg)

	// 1. Create an image app — no git_url required.
	rr := c.do(t, h, "POST", "/api/apps", map[string]any{
		"name":         "Pulled App",
		"build_kind":   "image",
		"build_config": map[string]any{"image": "ghcr.io/me/app:1.4.2", "port": 8080},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create image app: %d body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	mustJSON(t, rr.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("create response missing id")
	}
	if src, _ := created["source"].(string); src != "image" {
		t.Errorf("source = %q, want image", src)
	}
	if bk, _ := created["build_kind"].(string); bk != "image" {
		t.Errorf("build_kind = %q, want image", bk)
	}
	if gu, _ := created["git_url"].(string); gu != "" {
		t.Errorf("git_url = %q, want empty", gu)
	}

	// 2. An image app with no image ref is rejected.
	rr = c.do(t, h, "POST", "/api/apps", map[string]any{
		"name":         "No Ref",
		"build_kind":   "image",
		"build_config": map[string]any{"port": 8080},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("create image app without ref: %d, want 400", rr.Code)
	}

	// 3. No credentials yet.
	rr = c.do(t, h, "GET", "/api/apps/"+id+"/registry-auth", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get registry-auth: %d body=%s", rr.Code, rr.Body.String())
	}
	var cfg1 map[string]any
	mustJSON(t, rr.Body.Bytes(), &cfg1)
	if configured, _ := cfg1["configured"].(bool); configured {
		t.Error("registry-auth configured=true before any write")
	}

	// 4. Store credentials.
	rr = c.do(t, h, "PUT", "/api/apps/"+id+"/registry-auth", map[string]any{
		"registry": "ghcr.io",
		"username": "me",
		"password": "s3cr3t-token",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("put registry-auth: %d body=%s", rr.Code, rr.Body.String())
	}

	// 5. Now configured, with the (non-secret) host echoed back.
	rr = c.do(t, h, "GET", "/api/apps/"+id+"/registry-auth", nil)
	mustJSON(t, rr.Body.Bytes(), &cfg1)
	if configured, _ := cfg1["configured"].(bool); !configured {
		t.Error("registry-auth configured=false after write")
	}
	if reg, _ := cfg1["registry"].(string); reg != "ghcr.io" {
		t.Errorf("registry = %q, want ghcr.io", reg)
	}

	// 6. The password must never appear in any API response.
	if strings.Contains(rr.Body.String(), "s3cr3t-token") {
		t.Error("registry-auth GET leaked the password")
	}
	rr = c.do(t, h, "GET", "/api/apps/"+id, nil)
	body := rr.Body.String()
	if strings.Contains(body, "s3cr3t-token") || strings.Contains(body, "registry_auth_enc") {
		t.Errorf("GET app leaked registry credentials: %s", body)
	}

	// 7. Clearing the credentials reverts to public.
	rr = c.do(t, h, "DELETE", "/api/apps/"+id+"/registry-auth", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete registry-auth: %d body=%s", rr.Code, rr.Body.String())
	}
	rr = c.do(t, h, "GET", "/api/apps/"+id+"/registry-auth", nil)
	mustJSON(t, rr.Body.Bytes(), &cfg1)
	if configured, _ := cfg1["configured"].(bool); configured {
		t.Error("registry-auth still configured after delete")
	}
}
