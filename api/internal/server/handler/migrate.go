package handler

import (
	"archive/tar"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/audit"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
)

// controlDBContainer is the fixed container_name of the control-plane Postgres
// (compose.prod.yaml). pg_dump runs inside it so $POSTGRES_* creds resolve there.
const controlDBContainer = "vac-db"

// ExportInstanceBundle streams a portable instance bundle as an uncompressed tar
// containing `vac-db.sql.gz` (a logical dump of the control DB), `env` (the
// secrets needed to bring a destination up — including VAC_MASTER_KEY, so sealed
// rows decrypt without re-encryption), and `manifest.json`. The layout matches
// what `vac migrate import` consumes; extract it and point the importer at the
// directory (or the tarball directly).
//
// Deliberately scoped to the control plane: it does NOT carry app *data* volumes.
// Streaming gigabytes through the browser fights VAC's RAM budget, and bulk
// volume movement is the host CLI's job (`vac migrate export`). The hard-to-
// recreate part — the wiring + every encrypted secret — is exactly the DB + key.
//
// It is a POST (not a GET) so it flows through the CSRF, step-up (fresh 2FA), and
// audit middleware exactly like the other privileged instance operations.
func ExportInstanceBundle(docker *dockercli.Compose, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		audit.Describe(r.Context(), "exported the instance migration bundle (control DB + secrets)")
		audit.SetTarget(r.Context(), "instance", "bundle")

		// Dump the control DB to a temp file first: pg_dump's size is unknown up
		// front and tar needs it in the entry header, so we can't stream it
		// straight through. The dump lands on the host-backed work dir (not RAM)
		// and is removed on return.
		tmpDir := filepath.Join(cfg.WorkDir, ".migrate")
		if err := os.MkdirAll(tmpDir, 0o700); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not prepare a workspace for the bundle")
			return
		}
		dump, err := os.CreateTemp(tmpDir, "vac-db-*.sql.gz")
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not create a temp file for the dump")
			return
		}
		defer func() {
			_ = dump.Close()
			_ = os.Remove(dump.Name())
		}()

		// `pg_dump … | gzip` runs under `sh -c` inside vac-db (Exec joins on spaces).
		err = docker.Exec(r.Context(), controlDBContainer,
			[]string{"pg_dump", "-U", "vac", "-d", "vac", "|", "gzip"}, dump)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not dump the control database")
			return
		}
		info, err := dump.Stat()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "could not size the dump")
			return
		}
		if _, err := dump.Seek(0, 0); err != nil {
			WriteError(w, http.StatusInternalServerError, "could not rewind the dump")
			return
		}

		env := reconstructEnv(cfg)
		manifest := fmt.Sprintf(`{
  "schema": 1,
  "tool": "vac-migrate",
  "origin": "ui",
  "created_at": %q,
  "source_version": %q,
  "control_db": "vac-db.sql.gz",
  "env_file": "env",
  "volumes": [],
  "note": "control-plane bundle (no app data volumes); move volumes with 'vac migrate export' on the host"
}
`, time.Now().UTC().Format(time.RFC3339), cfg.Version)

		// Everything that can fail has failed by now, so it's safe to commit to a
		// 200 + streaming body (past this point we can't change the status code).
		w.Header().Set("Content-Type", "application/x-tar")
		w.Header().Set("Content-Disposition", `attachment; filename="vac-instance-bundle.tar"`)
		w.Header().Set("Cache-Control", "no-store")

		tw := tar.NewWriter(w)
		if err := tarBytes(tw, "manifest.json", 0o600, []byte(manifest)); err != nil {
			return
		}
		if err := tarBytes(tw, "env", 0o600, []byte(env)); err != nil {
			return
		}
		// Stream the dump entry from the temp file (no full read into RAM).
		hdr := &tar.Header{Name: "vac-db.sql.gz", Mode: 0o600, Size: info.Size(), ModTime: time.Now()}
		if err := tw.WriteHeader(hdr); err != nil {
			return
		}
		if _, err := io.Copy(tw, dump); err != nil {
			return
		}
		_ = tw.Close()

		audit.SetMetadata(r.Context(), map[string]any{"dump_bytes": info.Size()})
	}
}

// reconstructEnv rebuilds the subset of the host .env that a destination needs to
// boot with matching secrets. vac-api can't read the host file (it isn't mounted)
// — it only knows its own process config — so this is necessarily a subset: the
// master key, the DB password (parsed back out of the DSN), and the feature
// flags. DOCKER_GID is intentionally omitted (it's host-specific; `vac migrate
// import` re-derives it for the destination), as is VAC_REGISTRY (compose
// defaults it). The host CLI export copies the real .env verbatim instead.
func reconstructEnv(cfg config.Config) string {
	var b strings.Builder
	b.WriteString("# Reconstructed by VAC (UI export). DOCKER_GID is re-derived on import.\n")
	fmt.Fprintf(&b, "VAC_VERSION=%s\n", cfg.Version)
	fmt.Fprintf(&b, "VAC_MASTER_KEY=%x\n", cfg.MasterKey)
	if pw := dbPassword(cfg.DatabaseURL); pw != "" {
		fmt.Fprintf(&b, "VAC_DB_PASSWORD=%s\n", pw)
	}
	fmt.Fprintf(&b, "VAC_BASE_DOMAIN=%s\n", cfg.BaseDomain)
	fmt.Fprintf(&b, "VAC_MANAGED_SERVICES=%t\n", cfg.ManagedServices)
	fmt.Fprintf(&b, "VAC_ENABLE_SHELL=%t\n", cfg.EnableShell)
	fmt.Fprintf(&b, "VAC_IDLE_SUSPEND=%t\n", cfg.IdleSuspend)
	fmt.Fprintf(&b, "VAC_DNS_AUTOMATION=%t\n", cfg.DNSAutomation)
	return b.String()
}

// dbPassword pulls the password out of a postgres DSN, empty if absent/unparsable.
func dbPassword(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return ""
	}
	pw, _ := u.User.Password()
	return pw
}

func tarBytes(tw *tar.Writer, name string, mode int64, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: time.Now()}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}
