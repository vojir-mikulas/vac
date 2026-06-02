package handler

import (
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/addon"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// TestToAppDTO_TemplateResolution locks in the Stage 0 shared seam: a
// template-sourced app resolves its add-on name + icon via the catalog, while a
// git app exposes source="git" and leaves the template fields nil.
func TestToAppDTO_TemplateResolution(t *testing.T) {
	t.Parallel()
	cat := &fakeCatalog{templates: map[string]addon.Template{
		"grafana": {ID: "grafana", Name: "Grafana", Icon: "grafana"},
	}}

	tplID := "grafana"
	tpl := toAppDTO(store.App{ID: "a1", Source: store.AppSourceTemplate, TemplateID: &tplID}, cat)
	if tpl.Source != store.AppSourceTemplate {
		t.Errorf("source = %q; want %q", tpl.Source, store.AppSourceTemplate)
	}
	if tpl.TemplateID == nil || *tpl.TemplateID != "grafana" {
		t.Errorf("template_id = %v; want grafana", tpl.TemplateID)
	}
	if tpl.TemplateName == nil || *tpl.TemplateName != "Grafana" {
		t.Errorf("template_name = %v; want Grafana", tpl.TemplateName)
	}
	if tpl.TemplateIcon == nil || *tpl.TemplateIcon != "grafana" {
		t.Errorf("template_icon = %v; want grafana", tpl.TemplateIcon)
	}

	git := toAppDTO(store.App{ID: "a2", Source: store.AppSourceGit}, cat)
	if git.Source != store.AppSourceGit {
		t.Errorf("source = %q; want %q", git.Source, store.AppSourceGit)
	}
	if git.TemplateID != nil || git.TemplateName != nil || git.TemplateIcon != nil {
		t.Errorf("git app should have nil template fields, got id=%v name=%v icon=%v",
			git.TemplateID, git.TemplateName, git.TemplateIcon)
	}

	// Unknown template id (catalog miss) → name/icon stay nil, id still surfaced.
	missID := "nope"
	miss := toAppDTO(store.App{ID: "a3", Source: store.AppSourceTemplate, TemplateID: &missID}, cat)
	if miss.TemplateName != nil || miss.TemplateIcon != nil {
		t.Errorf("unknown template should leave name/icon nil, got name=%v icon=%v",
			miss.TemplateName, miss.TemplateIcon)
	}

	// Nil catalog must not panic (template apps created while managed-services off).
	_ = toAppDTO(store.App{ID: "a4", Source: store.AppSourceTemplate, TemplateID: &tplID}, nil)
}

func TestDeriveSlug(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"My App", "my-app"},
		{"  Foo--Bar  ", "foo-bar"},
		{"Hello World 123", "hello-world-123"},
		{"--leading-and-trailing--", "leading-and-trailing"},
		{"!!!", ""},
		{"", ""},
		{"alreadyslug", "alreadyslug"},
		{"UPPER", "upper"},
		{"name_with_underscores", "name-with-underscores"},
	}
	for _, c := range cases {
		if got := deriveSlug(c.in); got != c.want {
			t.Errorf("deriveSlug(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestGitURLRegex(t *testing.T) {
	t.Parallel()
	ok := []string{
		"https://github.com/user/repo.git",
		"http://gitea.local/u/r",
		"git@github.com:user/repo.git",
		"ssh://git@github.com/user/repo.git",
	}
	for _, u := range ok {
		if !gitURLRe.MatchString(u) {
			t.Errorf("gitURLRe should match %q", u)
		}
	}
	bad := []string{
		"",
		"not a url",
		"ftp://example.com/repo",
		"file:///tmp/repo",
		"github.com/user/repo",
		"git@github.com", // missing :path
		"https:// space.com/repo",
	}
	for _, u := range bad {
		if gitURLRe.MatchString(u) {
			t.Errorf("gitURLRe should NOT match %q", u)
		}
	}
}

func TestGitRefRegex(t *testing.T) {
	t.Parallel()
	ok := []string{
		"main",
		"v1.2.3",
		"feature/new-thing",
		"release-2026.01",
		"refs/heads/main",
	}
	for _, r := range ok {
		if !gitRefRe.MatchString(r) {
			t.Errorf("gitRefRe should match %q", r)
		}
	}
	bad := []string{
		"",
		"-rf", // leading dash → could be parsed as a flag by git
		"-flag",
		"branch with spaces",
		"branch;rm",
		"branch$VAR",
		"branch\nname",
		"--upload-pack=evil",
	}
	for _, r := range bad {
		if gitRefRe.MatchString(r) {
			t.Errorf("gitRefRe should NOT match %q", r)
		}
	}
}

func TestSlugRegex(t *testing.T) {
	t.Parallel()
	ok := []string{"a", "abc", "abc-def", "a1-b2-c3", "longslug"}
	for _, s := range ok {
		if !slugRe.MatchString(s) {
			t.Errorf("slugRe should match %q", s)
		}
	}
	bad := []string{"", "-abc", "abc-", "abc--def", "ABC", "abc_def", "abc def", "abc."}
	for _, s := range bad {
		if slugRe.MatchString(s) {
			t.Errorf("slugRe should NOT match %q", s)
		}
	}
}
