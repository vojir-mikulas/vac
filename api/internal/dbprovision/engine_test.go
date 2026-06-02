package dbprovision

import (
	"regexp"
	"testing"
)

var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func TestGenerateNames_IdentifierSafe(t *testing.T) {
	for _, slug := range []string{"blog", "My-App", "123start", "a..b--c", "", "weird!!name"} {
		n, err := generateNames(slug)
		if err != nil {
			t.Fatalf("generateNames(%q): %v", slug, err)
		}
		if !identRe.MatchString(n.DBName) {
			t.Errorf("db name %q not identifier-safe (slug %q)", n.DBName, slug)
		}
		if !identRe.MatchString(n.RoleName) {
			t.Errorf("role name %q not identifier-safe (slug %q)", n.RoleName, slug)
		}
		if len(n.Password) != 28 {
			t.Errorf("password len = %d, want 28", len(n.Password))
		}
		if len(n.DBName) > 48 {
			t.Errorf("db name too long: %q", n.DBName)
		}
	}
}

func TestGenerateNames_Unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		n, err := generateNames("blog")
		if err != nil {
			t.Fatal(err)
		}
		if seen[n.DBName] {
			t.Fatalf("duplicate db name %q", n.DBName)
		}
		seen[n.DBName] = true
	}
}

func TestSanitizeIdent(t *testing.T) {
	cases := map[string]string{
		"blog":        "blog",
		"My-App":      "my_app",
		"a..b--c":     "a_b_c",
		"123":         "db_123",
		"--x--":       "x",
		"weird!!name": "weird_name",
	}
	for in, want := range cases {
		if got := sanitizeIdent(in); got != want {
			t.Errorf("sanitizeIdent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeriveAdminPassword_StableAndDistinct(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	a := deriveAdminPassword(key, "mariadb")
	b := deriveAdminPassword(key, "mariadb")
	if a != b {
		t.Errorf("admin password not stable: %q vs %q", a, b)
	}
	if len(a) != 24 {
		t.Errorf("admin password len = %d, want 24", len(a))
	}
	if c := deriveAdminPassword(key, "mongo"); c == a {
		t.Errorf("different engines share a password")
	}
}

func TestQuoteLiteral(t *testing.T) {
	if got := quoteLiteral("abc"); got != "'abc'" {
		t.Errorf("quoteLiteral(abc) = %q", got)
	}
	if got := quoteLiteral("a'b"); got != "'a''b'" {
		t.Errorf("quoteLiteral escaping wrong: %q", got)
	}
}
