// Package dbprovision provisions managed databases for apps (Track D / D2).
//
// Two shapes of engine live behind one interface:
//
//   - Postgres is the blessed, cheapest default — a new database + role inside
//     the shared control-plane vac-db, created with VAC's own pool (no new
//     process). vac-db is attached to vac-edge so the app can reach it by alias.
//   - Every other engine is a single shared daemon per engine (vac-mariadb, …),
//     lazily started the first time an app asks for it and provisioned by
//     `docker exec`ing its admin CLI. Cost = (distinct engines in use) processes,
//     never a container per app.
//
// Each engine is a small recipe (image, exec templates, connection-string
// template, footprint), never bespoke per-engine logic in the control plane —
// the same guardrail as build adapters and add-on templates.
package dbprovision

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Engine is one managed-database backend. Implementations are recipes, selected
// by name at provision time.
type Engine interface {
	// Name is the stable engine key (postgres | mariadb | …).
	Name() string
	// EnsureRunning makes the backing instance available (attach vac-db to
	// vac-edge for Postgres; lazily `compose up` the shared daemon otherwise).
	// Idempotent — a no-op once the instance is up.
	EnsureRunning(ctx context.Context) error
	// Provision creates the database + login role with the given password.
	Provision(ctx context.Context, dbName, roleName, password string) error
	// Deprovision drops the database + role. Best-effort / idempotent.
	Deprovision(ctx context.Context, dbName, roleName string) error
	// ConnString renders the connection string an app uses to reach the DB over
	// vac-edge (deterministic from the generated names + password).
	ConnString(dbName, roleName, password string) string
	// DefaultBackupCommand is the dump command D1 runs (inside BackupContainer)
	// to back this engine up with no manual config.
	DefaultBackupCommand(dbName string) string
	// BackupContainer is the container the D1 backup engine `docker exec`s into
	// to run DefaultBackupCommand (the shared instance, not an app service).
	BackupContainer() string
	// EnvVarName is the env var the connection string is injected as.
	EnvVarName() string
	// FootprintMB estimates the added RAM; 0 for Postgres (shares vac-db).
	FootprintMB() int
	// Shared reports whether using this engine starts a new shared daemon (so the
	// UI shows a footprint warning before confirm). False for Postgres.
	Shared() bool
}

// Config wires the engines to their dependencies.
type Config struct {
	WorkDir     string // VAC work dir; shared-engine compose projects live under {WorkDir}/managed
	EdgeNetwork string // vac-edge
	MasterKey   []byte // derives the stable per-engine admin password
	// PostgresAdminUser / Host are the shared control-plane Postgres coordinates
	// (default user "vac", host alias "vac-db").
	PostgresAdminUser string
	PostgresHost      string
}

// roleAlphabet / passwordAlphabet are deliberately quote-free so identifiers and
// passwords are safe to interpolate into DDL / shell without escaping surprises.
const (
	nameAlphabet     = "abcdefghijklmnopqrstuvwxyz0123456789"
	passwordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// GeneratedNames is the per-provision identity: a database name, a login role,
// and a password.
type GeneratedNames struct {
	DBName   string
	RoleName string
	Password string
}

// generateNames derives a unique, identifier-safe (db, role, password) triple
// from an app slug. db/role names are <=48 chars (well under the 63-char
// Postgres limit) and start with a letter.
func generateNames(slug string) (GeneratedNames, error) {
	base := sanitizeIdent(slug)
	if base == "" {
		base = "app"
	}
	if len(base) > 24 {
		base = base[:24]
	}
	suffix, err := randString(6, nameAlphabet)
	if err != nil {
		return GeneratedNames{}, err
	}
	pw, err := randString(28, passwordAlphabet)
	if err != nil {
		return GeneratedNames{}, err
	}
	db := base + "_" + suffix
	return GeneratedNames{DBName: db, RoleName: db + "_u", Password: pw}, nil
}

// sanitizeIdent lowercases and maps any non [a-z0-9_] run to a single '_',
// ensuring the result starts with a letter.
func sanitizeIdent(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastUnderscore = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return ""
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "db_" + out
	}
	return out
}

// randString returns n characters drawn uniformly from alphabet using crypto/rand.
func randString(n int, alphabet string) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("dbprovision: rand: %w", err)
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}

// deriveAdminPassword produces a stable admin password for a shared engine
// instance from the master key, so the password survives restarts without being
// stored separately. Rotating the master key rotates these (documented).
func deriveAdminPassword(masterKey []byte, engine string) string {
	h := hmac.New(sha256.New, masterKey)
	h.Write([]byte("vac-managed-db-admin:" + engine))
	return hex.EncodeToString(h.Sum(nil))[:24]
}
