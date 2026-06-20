// Package backup runs per-service backup commands on a schedule, captures their
// stdout via `docker exec`, and ships the result to a pluggable destination
// (Track D / D1). It is the engine-agnostic dump primitive D2 reuses.
package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// Destination is where a captured dump is written. Implementations are selected
// per backup config; a third (rsync/SFTP) can drop in without touching the dump
// engine.
type Destination interface {
	// Put streams r to the object identified by key and returns the bytes
	// written. The reader is consumed fully on success.
	Put(ctx context.Context, key string, r io.Reader) (int64, error)
	// Open returns a reader for a previously-Put artifact (download path).
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// Prune removes all but the newest `keep` artifacts under prefix. Keys embed
	// a sortable timestamp, so lexical order is chronological.
	Prune(ctx context.Context, prefix string, keep int) error
}

// NewDestination builds the Destination for a config. `local` needs no creds;
// `s3` decrypts its sealed dest_config (bucket/endpoint/keys) with box. workDir
// is the VAC work directory — local writes under {workDir}/backups and S3 stages
// there before upload.
func NewDestination(cfg store.BackupConfig, box *crypto.Box, workDir string) (Destination, error) {
	base := filepath.Join(workDir, "backups")
	switch cfg.Destination {
	case "local", "":
		return &LocalDestination{Base: base}, nil
	case "s3":
		raw, err := openDestConfig(cfg.DestConfig, box)
		if err != nil {
			return nil, err
		}
		var sc S3Config
		if err := json.Unmarshal(raw, &sc); err != nil {
			return nil, fmt.Errorf("backup: parse s3 dest_config: %w", err)
		}
		if err := sc.validate(); err != nil {
			return nil, err
		}
		return newS3Destination(sc, base), nil
	default:
		return nil, fmt.Errorf("backup: unknown destination %q", cfg.Destination)
	}
}

// openDestConfig unseals the dest_config blob. An empty blob is an error for the
// destinations that require credentials (only S3 today).
func openDestConfig(sealed []byte, box *crypto.Box) ([]byte, error) {
	if len(sealed) == 0 {
		return nil, fmt.Errorf("backup: destination requires credentials but dest_config is empty")
	}
	if box == nil {
		return nil, fmt.Errorf("backup: encryption disabled (VAC_MASTER_KEY unset); cannot read destination credentials")
	}
	return box.Open(sealed)
}

// LocalDestination writes dumps to a VAC-managed host directory. Zero-dependency
// and the honest default for a single box; also the staging path for S3.
type LocalDestination struct {
	Base string // {workDir}/backups
}

func (l *LocalDestination) path(key string) string {
	return filepath.Join(l.Base, filepath.FromSlash(key))
}

func (l *LocalDestination) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	dst := l.path(key)
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // dst is a backup key resolved under the configured base dir, not external input
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dst) // don't leave a truncated artifact behind
		return 0, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return 0, closeErr
	}
	return n, nil
}

func (l *LocalDestination) Open(_ context.Context, key string) (io.ReadCloser, error) {
	return os.Open(l.path(key))
}

func (l *LocalDestination) Prune(_ context.Context, prefix string, keep int) error {
	if keep <= 0 {
		return nil
	}
	dir := filepath.Join(l.Base, filepath.FromSlash(prefix))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return nil
	}
	// Newest first — keys embed a sortable UTC timestamp, so a descending lexical
	// sort puts the most recent at the front.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var firstErr error
	for _, name := range names[keep:] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// keyJoin builds an object key from segments with forward slashes regardless of
// host OS — S3 keys are always slash-delimited and local paths convert on use.
func keyJoin(segments ...string) string {
	return strings.Join(segments, "/")
}

// LocalDiskUsage sums the bytes of every artifact under {workDir}/backups — the
// honest on-box footprint of local backups for the overview summary. S3-bound
// artifacts only stage here transiently, so this is the local destination total.
// A missing directory (no local backups yet) is zero, not an error.
func LocalDiskUsage(workDir string) (int64, error) {
	base := filepath.Join(workDir, "backups")
	var total int64
	err := filepath.WalkDir(base, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return total, err
}
