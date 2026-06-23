package deploy

import "testing"

func TestEscapeEnvValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"plain", "plain"},
		{"", ""},
		{"with space", `"with space"`},
		{`has"quote`, `"has\"quote"`},
		{"line1\nline2", `"line1\nline2"`},
		{`back\slash`, `"back\\slash"`},
		{"$INTERP", `"$$INTERP"`},
		{"127.0.0.1", "127.0.0.1"},
		{"hash#comment", `"hash#comment"`},
		{
			"$2a$10$Lg28VxdvEHteIIOBAH7pQuNQII6SrJ//md1Dy87cDQHWWbt5AXKUW",
			`"$$2a$$10$$Lg28VxdvEHteIIOBAH7pQuNQII6SrJ//md1Dy87cDQHWWbt5AXKUW"`,
		},
	}
	for _, tc := range tests {
		got := escapeEnvValue(tc.in)
		if got != tc.want {
			t.Errorf("escapeEnvValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
