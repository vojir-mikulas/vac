package backup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalDestination_PutOpenPrune(t *testing.T) {
	base := t.TempDir()
	dst := &LocalDestination{Base: base}
	ctx := context.Background()

	// Put writes the artifact and returns the byte count.
	key := keyJoin("blog", "db", "20260601T030000Z.dump")
	n, err := dst.Put(ctx, key, strings.NewReader("DUMPDATA"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n != 8 {
		t.Fatalf("size = %d, want 8", n)
	}
	if _, err := os.Stat(filepath.Join(base, "blog", "db", "20260601T030000Z.dump")); err != nil {
		t.Fatalf("artifact not on disk: %v", err)
	}

	// Open reads it back.
	rc, err := dst.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "DUMPDATA" {
		t.Fatalf("read back %q, want DUMPDATA", got)
	}
}

func TestLocalDestination_PruneKeepsNewest(t *testing.T) {
	base := t.TempDir()
	dst := &LocalDestination{Base: base}
	ctx := context.Background()

	// Five artifacts with sortable timestamps; keep the 2 newest.
	stamps := []string{
		"20260601T030000Z", "20260602T030000Z", "20260603T030000Z",
		"20260604T030000Z", "20260605T030000Z",
	}
	for _, s := range stamps {
		if _, err := dst.Put(ctx, keyJoin("blog", "db", s+".dump"), strings.NewReader("x")); err != nil {
			t.Fatalf("Put %s: %v", s, err)
		}
	}

	if err := dst.Prune(ctx, prunePrefix("blog", "db"), 2); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	entries, _ := os.ReadDir(filepath.Join(base, "blog", "db"))
	if len(entries) != 2 {
		t.Fatalf("after prune: %d files, want 2", len(entries))
	}
	names := []string{entries[0].Name(), entries[1].Name()}
	for _, want := range []string{"20260604T030000Z.dump", "20260605T030000Z.dump"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %s to survive prune; have %v", want, names)
		}
	}
}

func TestLocalDestination_PruneNoDirIsNoError(t *testing.T) {
	dst := &LocalDestination{Base: t.TempDir()}
	if err := dst.Prune(context.Background(), prunePrefix("nope", "db"), 3); err != nil {
		t.Fatalf("Prune on missing dir: %v", err)
	}
}
