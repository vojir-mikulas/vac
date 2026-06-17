package diskusage

import (
	"context"
	"io/fs"
	"path/filepath"
)

// dirSizeBytes sums the apparent size of every regular file under root, the
// Go-native equivalent of `du -sb` (the backup engine walks {workDir}/backups the
// same way). It checks ctx between entries so a bounded walk on a slow/large tree
// aborts at the deadline rather than wedging the poll. Per-entry stat errors are
// skipped (a vanishing temp file shouldn't fail the whole walk); ctx cancellation
// and a missing/unreachable root are returned so the caller records "not measured".
func dirSizeBytes(ctx context.Context, root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			// Skip an unreadable subtree entry, but surface a failure to open the
			// root itself (returned by WalkDir as an error on the root path).
			if d == nil {
				return err
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}
