//go:build integration

package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/auth"
)

// authedClient bundles a logged-in browser's cookies + CSRF token so the
// per-test plumbing stays out of the way.
type authedClient struct {
	cookies []*http.Cookie
	csrf    *http.Cookie
}

func bootstrapAndLogin(t *testing.T, h http.Handler) authedClient {
	t.Helper()
	rr, _ := do(t, h, "POST", "/api/setup/admin", map[string]string{
		"username": "alice",
		"password": "swordfish-pw",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("setup admin: %d", rr.Code)
	}
	cookies := loginAs(t, h, "alice", "swordfish-pw", "10.0.0.1")
	return authedClient{cookies: cookies, csrf: findCookie(auth.CSRFCookie, cookies)}
}

func (c authedClient) do(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := reqWithCookies(t, method, path, body, c.cookies)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set(auth.CSRFHeader, c.csrf.Value)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestAppCRUDFlow(t *testing.T) {
	h := setupServer(t)
	c := bootstrapAndLogin(t, h)

	// 1. List empty.
	rr := c.do(t, h, "GET", "/api/apps", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list empty: %d body=%s", rr.Code, rr.Body.String())
	}
	var list []map[string]any
	mustJSON(t, rr.Body.Bytes(), &list)
	if len(list) != 0 {
		t.Fatalf("initial list len = %d, want 0", len(list))
	}

	// 2. Create.
	rr = c.do(t, h, "POST", "/api/apps", map[string]string{
		"name":    "My Blog",
		"git_url": "git@github.com:alice/blog.git",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	mustJSON(t, rr.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("create response missing id")
	}
	if slug, _ := created["slug"].(string); slug != "my-blog" {
		t.Errorf("slug = %q, want my-blog", slug)
	}
	if br, _ := created["git_branch"].(string); br != "main" {
		t.Errorf("git_branch default = %q, want main", br)
	}
	if cf, _ := created["compose_file"].(string); cf != "compose.yaml" {
		t.Errorf("compose_file default = %q, want compose.yaml", cf)
	}
	if st, _ := created["status"].(string); st != "created" {
		t.Errorf("status default = %q, want created", st)
	}

	// 3. List shows it.
	rr = c.do(t, h, "GET", "/api/apps", nil)
	mustJSON(t, rr.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("list after create len = %d, want 1", len(list))
	}

	// 4. Get by id.
	rr = c.do(t, h, "GET", "/api/apps/"+id, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d", rr.Code)
	}
	var got map[string]any
	mustJSON(t, rr.Body.Bytes(), &got)
	if got["id"] != id {
		t.Errorf("get id = %v, want %s", got["id"], id)
	}

	// 5. Patch name + branch; slug is read-only.
	rr = c.do(t, h, "PATCH", "/api/apps/"+id, map[string]any{
		"name":       "Renamed Blog",
		"git_branch": "develop",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch: %d body=%s", rr.Code, rr.Body.String())
	}
	var patched map[string]any
	mustJSON(t, rr.Body.Bytes(), &patched)
	if patched["name"] != "Renamed Blog" {
		t.Errorf("patched name = %v", patched["name"])
	}
	if patched["git_branch"] != "develop" {
		t.Errorf("patched git_branch = %v", patched["git_branch"])
	}
	if patched["slug"] != "my-blog" {
		t.Errorf("slug should not change on patch; got %v", patched["slug"])
	}

	// 6. Delete.
	rr = c.do(t, h, "DELETE", "/api/apps/"+id, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: %d body=%s", rr.Code, rr.Body.String())
	}

	// 7. List empty again.
	rr = c.do(t, h, "GET", "/api/apps", nil)
	mustJSON(t, rr.Body.Bytes(), &list)
	if len(list) != 0 {
		t.Fatalf("list after delete len = %d, want 0", len(list))
	}

	// 8. Get on deleted id → 404.
	rr = c.do(t, h, "GET", "/api/apps/"+id, nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("get deleted: %d, want 404", rr.Code)
	}
}

func TestAppCreateValidation(t *testing.T) {
	h := setupServer(t)
	c := bootstrapAndLogin(t, h)

	cases := []struct {
		name string
		body any
		want int
	}{
		{"missing name", map[string]string{"git_url": "git@github.com:a/b.git"}, http.StatusBadRequest},
		{"missing git_url", map[string]string{"name": "x"}, http.StatusBadRequest},
		{"bad git_url", map[string]string{"name": "x", "git_url": "not-a-url"}, http.StatusBadRequest},
		{"ftp not allowed", map[string]string{"name": "x", "git_url": "ftp://example.com/repo"}, http.StatusBadRequest},
		{"https ok", map[string]string{"name": "x", "git_url": "https://github.com/a/b.git", "slug": "https-ok"}, http.StatusCreated},
		{"ssh:// ok", map[string]string{"name": "y", "git_url": "ssh://git@github.com/a/b.git", "slug": "ssh-ok"}, http.StatusCreated},
		{"bad slug", map[string]string{"name": "z", "git_url": "git@host:a/b.git", "slug": "Bad Slug!"}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := c.do(t, h, "POST", "/api/apps", tc.body)
			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestAppSlugCollisionReturns409(t *testing.T) {
	h := setupServer(t)
	c := bootstrapAndLogin(t, h)

	body := map[string]string{
		"name":    "collide",
		"git_url": "git@github.com:a/b.git",
		"slug":    "collide",
	}
	rr := c.do(t, h, "POST", "/api/apps", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("first create: %d body=%s", rr.Code, rr.Body.String())
	}
	rr = c.do(t, h, "POST", "/api/apps", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("second create with same slug: %d, want 409 body=%s", rr.Code, rr.Body.String())
	}
}

func TestAppRequiresAuth(t *testing.T) {
	h := setupServer(t)

	req := httptest.NewRequest("GET", "/api/apps", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("anon list: %d, want 401", rr.Code)
	}
}

func TestAppPatchUnknownIs404(t *testing.T) {
	h := setupServer(t)
	c := bootstrapAndLogin(t, h)

	rr := c.do(t, h, "PATCH", "/api/apps/00000000-0000-0000-0000-000000000000", map[string]string{
		"name": "x",
	})
	if rr.Code != http.StatusNotFound {
		t.Errorf("patch unknown: %d, want 404", rr.Code)
	}
}
