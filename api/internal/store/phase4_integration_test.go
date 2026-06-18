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
	smtp := store.SMTPSettings{
		Host:        "smtp.example.com",
		Port:        587,
		Username:    "vac",
		PasswordEnc: []byte("sealed-pw"),
		From:        "vac@example.com",
		To:          "ops@example.com",
		TLSMode:     "starttls",
	}
	if err := s.PutNotificationSettings(ctx, discord, nil, events, smtp); err != nil {
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
	if row.SMTPHost != "smtp.example.com" || row.SMTPPort != 587 || row.SMTPFrom != "vac@example.com" ||
		row.SMTPTo != "ops@example.com" || row.SMTPUsername != "vac" || row.SMTPTLSMode != "starttls" {
		t.Errorf("smtp fields round-trip mismatch: %+v", row)
	}
	if string(row.SMTPPasswordEnc) != "sealed-pw" {
		t.Errorf("smtp password enc = %q", row.SMTPPasswordEnc)
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

func TestSearchRuntimeLogs(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "search-app")

	rows := []store.RuntimeLogRow{
		{ServiceName: "web", Stream: store.RuntimeLogStreamStdout, Message: "GET /health 200"},
		{ServiceName: "web", Stream: store.RuntimeLogStreamStderr, Message: "connection refused: error"},
		{ServiceName: "worker", Stream: store.RuntimeLogStreamStdout, Message: "processed 50% of queue"},
	}
	for _, r := range rows {
		if _, err := s.AppendRuntimeLogs(ctx, a.ID, []store.RuntimeLogRow{r}); err != nil {
			t.Fatalf("AppendRuntimeLogs: %v", err)
		}
	}

	// Free-text substring (case-insensitive).
	got, err := s.SearchRuntimeLogs(ctx, store.RuntimeLogQuery{Query: "REFUSED"})
	if err != nil {
		t.Fatalf("SearchRuntimeLogs: %v", err)
	}
	if len(got) != 1 || got[0].Message != "connection refused: error" {
		t.Errorf("query=refused got %+v", got)
	}

	// Service + stream filters compose.
	got, err = s.SearchRuntimeLogs(ctx, store.RuntimeLogQuery{
		AppID: a.ID, ServiceName: "web", Stream: store.RuntimeLogStreamStdout,
	})
	if err != nil {
		t.Fatalf("SearchRuntimeLogs: %v", err)
	}
	if len(got) != 1 || got[0].Message != "GET /health 200" {
		t.Errorf("web/stdout got %+v", got)
	}

	// A literal % must not act as a wildcard: it matches only the worker line,
	// not every row.
	got, err = s.SearchRuntimeLogs(ctx, store.RuntimeLogQuery{Query: "50%"})
	if err != nil {
		t.Fatalf("SearchRuntimeLogs: %v", err)
	}
	if len(got) != 1 || got[0].ServiceName != "worker" {
		t.Errorf("query=50%% got %+v", got)
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
