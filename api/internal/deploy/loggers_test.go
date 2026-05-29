package deploy

import (
	"context"
	"sync"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ws"
)

type fakePublisher struct {
	mu     sync.Mutex
	topics []string
	frames [][]byte
}

func (f *fakePublisher) Publish(topic string, msg []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.topics = append(f.topics, topic)
	f.frames = append(f.frames, msg)
}

func (f *fakePublisher) len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.frames)
}

type fakeLogSink struct {
	mu   sync.Mutex
	rows []store.DeploymentLogRow
}

func (f *fakeLogSink) AppendDeploymentLogs(_ context.Context, _ string, rows []store.DeploymentLogRow) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	base := int64(len(f.rows))
	f.rows = append(f.rows, rows...)
	ids := make([]int64, len(rows))
	for i := range rows {
		ids[i] = base + int64(i) + 1
	}
	return ids, nil
}

func (f *fakeLogSink) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func TestLogWriter_BuffersUntilFlush(t *testing.T) {
	sink := &fakeLogSink{}
	lw := NewLogWriter(context.Background(), sink, nil, "d1", store.DeploymentLogStreamStdout, nil)
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
	lw := NewLogWriter(context.Background(), sink, nil, "d1", store.DeploymentLogStreamStdout, nil)
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
	lw := NewLogWriter(context.Background(), sink, nil, "d1", store.DeploymentLogStreamStdout, nil)
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

func TestLogWriter_PublishesLiveFrames(t *testing.T) {
	sink := &fakeLogSink{}
	pub := &fakePublisher{}
	lw := NewLogWriter(context.Background(), sink, pub, "d1", store.DeploymentLogStreamStdout, nil)

	if _, err := lw.Write([]byte("alpha\nbravo\n")); err != nil {
		t.Fatal(err)
	}
	if err := lw.Flush(); err != nil {
		t.Fatal(err)
	}

	if pub.len() != 2 {
		t.Fatalf("published %d frames, want 2", pub.len())
	}
	if pub.topics[0] != ws.BuildTopic("d1") {
		t.Errorf("topic = %q, want %q", pub.topics[0], ws.BuildTopic("d1"))
	}
	// Frames carry the sink-assigned ids (1, 2) for client-side dedup.
	f0, err := ws.Decode(pub.frames[0])
	if err != nil {
		t.Fatal(err)
	}
	if f0.Type != ws.TypeBuild || f0.ID != 1 {
		t.Errorf("frame0 type=%q id=%d, want build/1", f0.Type, f0.ID)
	}
	f1, _ := ws.Decode(pub.frames[1])
	if f1.ID != 2 {
		t.Errorf("frame1 id=%d, want 2", f1.ID)
	}
}

func TestLogSystem_PublishesAndPersists(t *testing.T) {
	sink := &fakeLogSink{}
	pub := &fakePublisher{}
	if err := LogSystem(context.Background(), sink, pub, "d1", "hello"); err != nil {
		t.Fatal(err)
	}
	if sink.Len() != 1 {
		t.Errorf("persisted %d rows, want 1", sink.Len())
	}
	if pub.len() != 1 {
		t.Errorf("published %d frames, want 1", pub.len())
	}
}

func TestPublishBuildEnd_EmitsTerminator(t *testing.T) {
	pub := &fakePublisher{}
	PublishBuildEnd(pub, "d1")
	if pub.len() != 1 {
		t.Fatalf("published %d frames, want 1", pub.len())
	}
	f, _ := ws.Decode(pub.frames[0])
	if f.Type != ws.TypeBuildEnd {
		t.Errorf("type = %q, want %q", f.Type, ws.TypeBuildEnd)
	}
}

func TestLogWriter_NilPublisherIsSafe(t *testing.T) {
	sink := &fakeLogSink{}
	lw := NewLogWriter(context.Background(), sink, nil, "d1", store.DeploymentLogStreamStdout, nil)
	if _, err := lw.Write([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if err := lw.Flush(); err != nil {
		t.Fatal(err)
	}
	if sink.Len() != 1 {
		t.Errorf("persisted %d rows, want 1", sink.Len())
	}
}
