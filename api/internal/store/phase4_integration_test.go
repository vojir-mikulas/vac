//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

func TestNotificationSettingsRoundTrip(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	// Seeded singleton row reads as empty.
	row, err := s.GetNotificationSettings(ctx)
	if err != nil {
		t.Fatalf("GetNotificationSettings: %v", err)
	}
	if len(row.DiscordURLEnc) != 0 || len(row.SlackURLEnc) != 0 {
		t.Errorf("fresh row should have no URLs: %+v", row)
	}

	discord := []byte("sealed-discord")
	events := []byte(`{"deploy_succeeded":false}`)
	if err := s.PutNotificationSettings(ctx, discord, nil, events); err != nil {
		t.Fatalf("PutNotificationSettings: %v", err)
	}

	row, err = s.GetNotificationSettings(ctx)
	if err != nil {
		t.Fatalf("GetNotificationSettings: %v", err)
	}
	if string(row.DiscordURLEnc) != "sealed-discord" {
		t.Errorf("discord enc = %q", row.DiscordURLEnc)
	}
	if len(row.SlackURLEnc) != 0 {
		t.Errorf("slack should be cleared: %q", row.SlackURLEnc)
	}
	if string(row.Events) != `{"deploy_succeeded":false}` {
		t.Errorf("events = %q", row.Events)
	}
}

func TestRuntimeLogsRingBufferTrim(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "ringbuf-app")

	// Append 10 lines for one service.
	for i := 0; i < 10; i++ {
		if _, err := s.AppendRuntimeLogs(ctx, a.ID, []store.RuntimeLogRow{
			{ServiceName: "web", Stream: store.RuntimeLogStreamStdout, Message: "line"},
		}); err != nil {
			t.Fatalf("AppendRuntimeLogs: %v", err)
		}
	}

	// Keep only the newest 3.
	deleted, err := s.TrimRuntimeLogsToRingBuffer(ctx, a.ID, "web", 3)
	if err != nil {
		t.Fatalf("TrimRuntimeLogsToRingBuffer: %v", err)
	}
	if deleted != 7 {
		t.Errorf("deleted = %d, want 7", deleted)
	}
	n, _ := s.CountRuntimeLogs(ctx, a.ID)
	if n != 3 {
		t.Errorf("remaining = %d, want 3", n)
	}

	// ListRuntimeLogServices reports the distinct pair.
	pairs, err := s.ListRuntimeLogServices(ctx)
	if err != nil {
		t.Fatalf("ListRuntimeLogServices: %v", err)
	}
	if len(pairs) != 1 || pairs[0].AppID != a.ID || pairs[0].ServiceName != "web" {
		t.Errorf("pairs = %+v", pairs)
	}
}

func TestAppendRuntimeLogsReturnsIDs(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "rtids-app")

	ids, err := s.AppendRuntimeLogs(ctx, a.ID, []store.RuntimeLogRow{
		{ServiceName: "web", Stream: store.RuntimeLogStreamStdout, Message: "a"},
		{ServiceName: "web", Stream: store.RuntimeLogStreamStderr, Message: "b"},
	})
	if err != nil {
		t.Fatalf("AppendRuntimeLogs: %v", err)
	}
	if len(ids) != 2 || ids[1] <= ids[0] {
		t.Errorf("ids = %v, want 2 ascending", ids)
	}
}
