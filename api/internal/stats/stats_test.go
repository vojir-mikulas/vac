package stats

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/deploy"
	"github.com/vojir-mikulas/vac/api/internal/dockercli"
	"github.com/vojir-mikulas/vac/api/internal/store"
	"github.com/vojir-mikulas/vac/api/internal/ws"
)

func TestParsePercent(t *testing.T) {
	cases := map[string]float64{"12.34%": 12.34, "0.00%": 0, "100%": 100, "bad": 0}
	for in, want := range cases {
		if got := parsePercent(in); got != want {
			t.Errorf("parsePercent(%q) = %v, want %v", in, got, want)
		}
	}
	// Surrounding whitespace is trimmed.
	if got := parsePercent("  7.5%  "); got != 7.5 {
		t.Errorf("parsePercent(padded) = %v, want 7.5", got)
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"0B":      0,
		"1024B":   1024,
		"1KiB":    1024,
		"1.5MiB":  int64(1.5 * 1024 * 1024),
		"2GB":     2 * 1024 * 1024 * 1024,
		"garbage": 0,
	}
	for in, want := range cases {
		if got := parseSize(in); got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParsePair(t *testing.T) {
	rx, tx := parsePair("1.2kB / 3.4kB")
	// 1.2*1024 = 1228.8 → 1228, 3.4*1024 = 3481.6 → 3481 (truncated).
	if rx != 1228 || tx != 3481 {
		t.Errorf("parsePair = %d/%d, want 1228/3481", rx, tx)
	}
}

func TestMatchServiceByPrefix(t *testing.T) {
	idToService := map[string]string{"abcdef1234567890": "web"}
	full, svc := matchService("abcdef123456", idToService) // short id from docker stats
	if svc != "web" || full != "abcdef1234567890" {
		t.Errorf("matchService = %q/%q, want abcdef1234567890/web", full, svc)
	}
}

type fakeStatSrc struct {
	calls atomic.Int64
}

func (f *fakeStatSrc) Stats(_ context.Context, _ []string) ([]dockercli.StatSample, error) {
	f.calls.Add(1)
	return []dockercli.StatSample{{
		ID: "c1", CPUPerc: "5.00%", MemUsage: "10MiB / 1GiB", MemPerc: "1.00%", NetIO: "1kB / 2kB",
	}}, nil
}

func (f *fakeStatSrc) ContainerStartedAt(_ context.Context, _ string) (time.Time, error) {
	return time.Now().Add(-time.Minute), nil
}

type fakeStatStore struct{}

func (fakeStatStore) ListServicesForApp(_ context.Context, _ string) ([]store.Service, error) {
	cid := "c1"
	return []store.Service{{ServiceName: "web", ContainerID: &cid, Status: deploy.ServiceStatusRunning}}, nil
}

func TestManagerGatesOnSubscribers(t *testing.T) {
	src := &fakeStatSrc{}
	hub := ws.NewHub()
	mgr := NewManager(src, fakeStatStore{}, hub, nil, 20*time.Millisecond, nil)
	hub.SetCallbacks(mgr.OnSubscribe, mgr.OnUnsubscribe)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)

	// No subscriber → no polling.
	time.Sleep(60 * time.Millisecond)
	if src.calls.Load() != 0 {
		t.Fatalf("polled %d times with no subscriber, want 0", src.calls.Load())
	}

	// Subscribe → collector starts, frames arrive.
	ch, unsub := hub.Subscribe(ws.StatsTopic("a1"))
	select {
	case frame := <-ch:
		f, err := ws.Decode(frame)
		if err != nil || f.Type != ws.TypeStats || f.Service != "web" {
			t.Fatalf("bad frame: type=%q service=%q err=%v", f.Type, f.Service, err)
		}
	case <-time.After(time.Second):
		t.Fatal("no stats frame after subscribe")
	}

	// Unsubscribe → polling stops.
	unsub()
	time.Sleep(40 * time.Millisecond) // let the collector observe cancellation
	stopped := src.calls.Load()
	time.Sleep(80 * time.Millisecond)
	if src.calls.Load() != stopped {
		t.Fatalf("kept polling after unsubscribe: %d → %d", stopped, src.calls.Load())
	}
}
