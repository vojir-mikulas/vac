package maintenance

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		html string
		want error
	}{
		{"ok", "<h1>back soon</h1>", nil},
		{"empty", "", ErrEmpty},
		{"whitespace only", "  \n\t ", ErrEmpty},
		{"too large", strings.Repeat("a", MaxHTMLBytes+1), ErrTooLarge},
		{"at the cap", strings.Repeat("a", MaxHTMLBytes), nil},
		{"invalid utf8", "\xff\xfe valid-looking", ErrInvalidUTF8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Validate(tt.html); got != tt.want {
				t.Fatalf("Validate(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestRender(t *testing.T) {
	if got := Render(nil); got != DefaultHTML() {
		t.Fatalf("Render(nil) should return the default page")
	}
	blank := "   "
	if got := Render(&blank); got != DefaultHTML() {
		t.Fatalf("Render(blank) should fall back to the default page")
	}
	custom := "<h1>custom</h1>"
	if got := Render(&custom); got != custom {
		t.Fatalf("Render(custom) = %q, want the custom page", got)
	}
}

func TestDefaultHTMLNonEmpty(t *testing.T) {
	if strings.TrimSpace(DefaultHTML()) == "" {
		t.Fatal("embedded default page is empty")
	}
}
