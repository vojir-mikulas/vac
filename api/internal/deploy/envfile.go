package deploy

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// envSource is the slice of *store.Store this file needs.
type envSource interface {
	ListEnvVarsForApp(ctx context.Context, appID string) ([]store.EnvVar, error)
}

// RenderEnvFile reads sealed env vars from the store, decrypts them, and
// writes a docker-compose-compatible .env file at destPath (mode 0600).
// Values containing newlines or quotes are emitted in double-quoted form
// with backslash escaping.
func RenderEnvFile(ctx context.Context, src envSource, box *crypto.Box, appID, destPath string) error {
	vars, err := src.ListEnvVarsForApp(ctx, appID)
	if err != nil {
		return fmt.Errorf("envfile: list: %w", err)
	}
	sort.Slice(vars, func(i, j int) bool { return vars[i].Key < vars[j].Key })

	var b strings.Builder
	for _, v := range vars {
		val, err := box.Open(v.Value)
		if err != nil {
			return fmt.Errorf("envfile: open %q: %w", v.Key, err)
		}
		b.WriteString(v.Key)
		b.WriteByte('=')
		b.WriteString(escapeEnvValue(string(val)))
		b.WriteByte('\n')
	}
	return os.WriteFile(destPath, []byte(b.String()), 0o600)
}

// escapeEnvValue produces a docker-compose .env-compatible value. Plain
// strings (no whitespace, no quotes, no $ signs that look like compose
// interpolation) go raw; anything else is double-quoted with backslash
// escaping for `"`, `\`, and `\n`, and each `$` doubled to `$$` so compose
// interpolation passes it through literally.
func escapeEnvValue(s string) string {
	if s == "" {
		return ""
	}
	needsQuote := strings.ContainsAny(s, " \t\n\r\"'\\$#")
	if !needsQuote {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '$':
			// Docker Compose interpolates the .env file, so a literal `$`
			// must be doubled or it (and the following identifier) is
			// substituted away — which silently mangles bcrypt hashes,
			// JWT secrets, and DB URLs that contain `$`.
			b.WriteString(`$$`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
