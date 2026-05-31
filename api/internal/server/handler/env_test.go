package handler

import "testing"

func TestValidEnvKey(t *testing.T) {
	t.Parallel()
	ok := []string{"FOO", "FOO_BAR", "_FOO", "foo", "a1", "_", "_X9"}
	for _, k := range ok {
		if !validEnvKey(k) {
			t.Errorf("validEnvKey should accept %q", k)
		}
	}
	bad := []string{
		"",
		"1FOO",         // leading digit
		"FOO-BAR",      // hyphen
		"FOO BAR",      // space
		"FOO.BAR",      // dot
		"LD_PRELOAD\n", // trailing newline
		"PATH=",        // equals
		"FOO$VAR",      // dollar
	}
	for _, k := range bad {
		if validEnvKey(k) {
			t.Errorf("validEnvKey should reject %q", k)
		}
	}
}

func TestValidEnvValue(t *testing.T) {
	t.Parallel()
	ok := []string{
		"",
		"plain",
		"with spaces",
		"with=equals",
		"with\"quote",
		`shell-special: $PATH ` + "`whoami`",
		"unicode-✓",
	}
	for _, v := range ok {
		if !validEnvValue(v) {
			t.Errorf("validEnvValue should accept %q", v)
		}
	}
	bad := []string{
		"contains\nnewline",
		"contains\rcr",
		"contains\x00nul",
		"smuggled\nOTHER_VAR=evil",
	}
	for _, v := range bad {
		if validEnvValue(v) {
			t.Errorf("validEnvValue should reject %q", v)
		}
	}
}
