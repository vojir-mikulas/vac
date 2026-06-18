// Package maintenance owns the maintenance-page concept: the built-in default
// page, the size/validity rules for an operator-supplied custom page, and the
// choice between them. Keeping it here keeps the concept out of the proxy/caddy
// transport packages — the proxy package only asks for the HTML to serve.
//
// See docs/plans/maintenance-mode-and-deploy-gates.md. The custom HTML is pushed
// to Caddy as the inline `body` of a static_response route (no shared volume),
// so it lives in Caddy's in-memory config and must stay small (MaxHTMLBytes).
package maintenance

import (
	_ "embed"
	"errors"
	"strings"
	"unicode/utf8"
)

// MaxHTMLBytes caps a custom maintenance page. The HTML rides inside Caddy's
// in-memory config (and autosave.json), so it must be bounded; 64 KB is generous
// for a themed splash with inline CSS and no external assets. The apps table
// also enforces this with a CHECK constraint (belt-and-braces).
const MaxHTMLBytes = 64 * 1024

//go:embed default.html
var defaultHTML string

// DefaultHTML is VAC's built-in maintenance page, served when an app has no
// custom page (maintenance_html IS NULL).
func DefaultHTML() string { return defaultHTML }

// Render returns the HTML an app should serve while in maintenance: the
// operator's custom page when set, otherwise the built-in default.
func Render(customHTML *string) string {
	if customHTML != nil && strings.TrimSpace(*customHTML) != "" {
		return *customHTML
	}
	return defaultHTML
}

// ErrTooLarge is returned by Validate when the custom page exceeds MaxHTMLBytes.
var ErrTooLarge = errors.New("maintenance page exceeds 64 KB")

// ErrEmpty is returned by Validate for an all-whitespace page (use "reset to
// default" to clear instead).
var ErrEmpty = errors.New("maintenance page is empty")

// ErrInvalidUTF8 is returned by Validate for a non-UTF-8 body — Caddy serializes
// the page as a JSON string, which must be valid UTF-8.
var ErrInvalidUTF8 = errors.New("maintenance page is not valid UTF-8")

// Validate checks an operator-supplied custom page before it is stored and
// pushed to Caddy: non-empty, valid UTF-8, and within the size cap.
func Validate(html string) error {
	if strings.TrimSpace(html) == "" {
		return ErrEmpty
	}
	if len(html) > MaxHTMLBytes {
		return ErrTooLarge
	}
	if !utf8.ValidString(html) {
		return ErrInvalidUTF8
	}
	return nil
}
