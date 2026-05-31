package handler

import (
	"strings"
	"testing"
)

func TestValidateStruct(t *testing.T) {
	t.Parallel()

	type sample struct {
		Name  string `json:"name"  validate:"required,min=2,max=10"`
		Email string `json:"email" validate:"required,email"`
	}

	cases := []struct {
		name     string
		in       sample
		ok       bool
		contains string
	}{
		{"valid", sample{Name: "Alice", Email: "a@b.co"}, true, ""},
		{"missing required", sample{Email: "a@b.co"}, false, "required"},
		{"too short", sample{Name: "a", Email: "a@b.co"}, false, "at least"},
		{"too long", sample{Name: strings.Repeat("x", 100), Email: "a@b.co"}, false, "at most"},
		{"bad email", sample{Name: "Alice", Email: "not-an-email"}, false, "invalid"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			msg, ok := validateStruct(c.in)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (msg=%q)", ok, c.ok, msg)
			}
			if !ok && !strings.Contains(strings.ToLower(msg), c.contains) {
				t.Errorf("msg = %q; want substring %q", msg, c.contains)
			}
		})
	}
}
