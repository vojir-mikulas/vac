package admin

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/appspec"
	"github.com/vojir-mikulas/vac/api/internal/config"
	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/db"
	"github.com/vojir-mikulas/vac/api/internal/portability"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// connect loads config and opens the database, returning the store, the crypto
// box (nil when VAC_MASTER_KEY is unset/malformed — callers surface the right
// error), and a cleanup func. Mirrors the wiring in ResetPassword so the CLI
// subcommands stay self-contained and never import the HTTP stack.
//
// Note: these are out-of-band operations (shell access ⇒ the master-key trust
// model, same as reset-password) and are not written to the audit_log; in-band
// import/export via the API is audited by the middleware.
func connect(ctx context.Context) (*store.Store, *crypto.Box, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("config load: %w", err)
	}
	if cfg.DatabaseURL == "" {
		return nil, nil, nil, errors.New("VAC_DATABASE_URL is not set — cannot connect to the database")
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("db open: %w", err)
	}
	var box *crypto.Box
	if len(cfg.MasterKey) > 0 {
		if b, err := crypto.New(cfg.MasterKey); err == nil {
			box = b
		}
	}
	return store.New(pool), box, pool.Close, nil
}

// Export is the entry point for `vac-api export <slug> [-o file] [--format=spec]`.
// It writes the app's portable vac.app.yaml to stdout (or -o file). Sensitive env
// values are omitted; non-sensitive values are decrypted, which needs the master
// key.
func Export(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "write the spec to this file instead of stdout")
	format := fs.String("format", "spec", "export format (spec)")
	// Accept the slug before or after the flags: Go's flag parser stops at the
	// first positional, so pull a leading bare slug out before parsing.
	var slug string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		slug, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if slug == "" {
		slug = fs.Arg(0)
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return errors.New("usage: vac-api export <slug> [-o file] [--format=spec]")
	}
	if *format != "spec" {
		return fmt.Errorf("unsupported format %q — only \"spec\" is available (compose/k8s land in later phases)", *format)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, box, closeFn, err := connect(ctx)
	if err != nil {
		return err
	}
	defer closeFn()

	app, err := st.GetAppBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("app %q not found", slug)
		}
		return fmt.Errorf("lookup app: %w", err)
	}
	spec, err := portability.Export(ctx, st, box, app.ID)
	if err != nil {
		if errors.Is(err, portability.ErrMasterKeyRequired) {
			return errors.New("VAC_MASTER_KEY is required to decrypt non-sensitive env values for export")
		}
		return fmt.Errorf("export: %w", err)
	}
	data, err := appspec.Marshal(spec)
	if err != nil {
		return fmt.Errorf("render spec: %w", err)
	}
	if *out != "" {
		if err := os.WriteFile(*out, data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", *out, err)
		}
		fmt.Fprintf(stderr, "wrote %s (%d bytes)\n", *out, len(data))
		return nil
	}
	_, err = stdout.Write(data)
	return err
}

// Apply is the entry point for `vac-api apply -f <vac.app.yaml>` (use -f - for
// stdin). It creates or updates an app from the spec, idempotent on slug. Env
// values are sealed under VAC_MASTER_KEY; sensitive keys with no value are
// created as placeholders the operator re-enters.
func Apply(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("f", "", `spec file to apply ("-" for stdin)`)
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Allow a bare path too: `apply foo.yaml` as well as `apply -f foo.yaml`.
	path := *file
	if path == "" {
		path = fs.Arg(0)
	}
	if path == "" {
		return errors.New("usage: vac-api apply -f <vac.app.yaml>  (use -f - for stdin)")
	}

	var (
		data []byte
		err  error
	)
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path) //nolint:gosec // operator-supplied spec path for an admin CLI import; not user-facing input
	}
	if err != nil {
		return fmt.Errorf("read spec: %w", err)
	}
	spec, err := appspec.Unmarshal(data)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	st, box, closeFn, err := connect(ctx)
	if err != nil {
		return err
	}
	defer closeFn()

	result, err := portability.Import(ctx, st, box, spec)
	if err != nil {
		var invalid portability.InvalidSpecError
		switch {
		case errors.As(err, &invalid):
			return fmt.Errorf("invalid spec: %w", invalid)
		case errors.Is(err, portability.ErrMasterKeyRequired):
			return errors.New("VAC_MASTER_KEY is required to seal env values on import")
		default:
			return fmt.Errorf("import: %w", err)
		}
	}

	verb := "Updated"
	if result.Created {
		verb = "Created"
	}
	fmt.Fprintf(stdout, "%s app %q (%s): %d service(s), %d domain(s), %d trigger(s), %d env var(s).\n",
		verb, result.Slug, result.AppID, result.Services, result.Domains, result.Triggers, result.EnvVars)
	if len(result.SecretsNeeded) > 0 {
		fmt.Fprintf(stdout, "Re-enter values for sensitive env keys: %s\n", strings.Join(result.SecretsNeeded, ", "))
	}
	return nil
}
