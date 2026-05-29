package deploy

import (
	"context"
	"sync"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakeLogSink struct {
	mu   sync.Mutex
	rows []store.DeploymentLogRow
}

func (f *fakeLogSink) AppendDeploymentLogs(_ context.Context, _ string, rows []store.DeploymentLogRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, rows...)
	return nil
}

func (f *fakeLogSink) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func TestLogWriter_BuffersUntilFlush(t *testing.T) {
	sink := &fakeLogSink{}
	lw := NewLogWriter(context.Background(), sink, "d1", store.DeploymentLogStreamStdout, nil)
	// Force a large maxBatch so write alone doesn't auto-flush.
	lw.maxBatch = 1000

	if _, err := lw.Write([]byte("line one\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := lw.Write([]byte("line two\n")); err != nil {
		t.Fatal(err)
	}
	if sink.Len() != 0 {
		t.Errorf("rows landed before Flush: %d", sink.Len())
	}
	if err := lw.Flush(); err != nil {
		t.Fatal(err)
	}
	if sink.Len() != 2 {
		t.Errorf("after flush: %d rows, want 2", sink.Len())
	}
}

func TestLogWriter_AutoFlushAtMaxBatch(t *testing.T) {
	sink := &fakeLogSink{}
	lw := NewLogWriter(context.Background(), sink, "d1", store.DeploymentLogStreamStdout, nil)
	lw.maxBatch = 3

	for i := 0; i < 3; i++ {
		if _, err := lw.Write([]byte("x\n")); err != nil {
			t.Fatal(err)
		}
	}
	if sink.Len() != 3 {
		t.Errorf("expected auto-flush at 3 rows, got %d", sink.Len())
	}
}

func TestLogWriter_StripsCarriageReturnAndEmpties(t *testing.T) {
	sink := &fakeLogSink{}
	lw := NewLogWriter(context.Background(), sink, "d1", store.DeploymentLogStreamStdout, nil)
	if _, err := lw.Write([]byte("first\r\n\nsecond\n")); err != nil {
		t.Fatal(err)
	}
	_ = lw.Flush()
	if sink.Len() != 2 {
		t.Errorf("rows = %d, want 2 (empty line dropped)", sink.Len())
	}
	if sink.rows[0].Message != "first" || sink.rows[1].Message != "second" {
		t.Errorf("rows = %+v", sink.rows)
	}
}
