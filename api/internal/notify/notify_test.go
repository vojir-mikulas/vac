package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

type fakeSettings struct {
	row store.NotificationSettingsRow
}

func (f fakeSettings) GetNotificationSettings(_ context.Context) (store.NotificationSettingsRow, error) {
	return f.row, nil
}

func TestDiscordPayloadColours(t *testing.T) {
	ok := discordPayload(Event{Type: EventDeploySucceeded, OK: true, Title: "x"}, "")
	if ok.Embeds[0].Color != colorGreen {
		t.Errorf("success colour = %d, want green", ok.Embeds[0].Color)
	}
	fail := discordPayload(Event{Type: EventDeployFailed, OK: false, Title: "x"}, "")
	if fail.Embeds[0].Color != colorRed {
		t.Errorf("fail colour = %d, want red", fail.Embeds[0].Color)
	}
	crash := discordPayload(Event{Type: EventCrashLoop, OK: false, Title: "x"}, "")
	if crash.Embeds[0].Color != colorAmber {
		t.Errorf("crash colour = %d, want amber", crash.Embeds[0].Color)
	}
}

func TestSlackPayloadHasBlocks(t *testing.T) {
	msg := slackPayload(Event{Type: EventDeploySucceeded, OK: true, Title: "Deploy succeeded: blog", AppID: "a1"}, "https://vac.example.com")
	if len(msg.Blocks) == 0 || msg.Blocks[0].Text == nil {
		t.Fatal("expected a section block with text")
	}
	if msg.Text == "" {
		t.Error("expected fallback text")
	}
}

func TestSendTestPostsToConfiguredChannel(t *testing.T) {
	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := New(fakeSettings{}, nil, srv.URL, "", "", nil)
	d.blockPrivate = false
	n, err := d.SendTest(context.Background())
	if err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if n != 1 {
		t.Errorf("sent = %d, want 1", n)
	}
	if got.Load() != 1 {
		t.Errorf("webhook hit %d times, want 1", got.Load())
	}
}

func TestSendTestErrorsWithNoChannels(t *testing.T) {
	d := New(fakeSettings{}, nil, "", "", "", nil)
	if _, err := d.SendTest(context.Background()); err == nil {
		t.Error("expected error when no channels configured")
	}
}

func TestDispatchHonoursToggleOff(t *testing.T) {
	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// deploy_succeeded explicitly disabled in the stored toggle map.
	events, _ := json.Marshal(map[string]bool{"deploy_succeeded": false})
	d := New(fakeSettings{row: store.NotificationSettingsRow{Events: events}}, nil, srv.URL, "", "", nil)
	d.DeploySucceeded("blog", "a1", "abc1234", "msg", time.Second)

	time.Sleep(150 * time.Millisecond) // dispatch is async
	if got.Load() != 0 {
		t.Errorf("webhook fired despite toggle off: %d", got.Load())
	}
}

func TestPostRefusesPrivateAddress(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// With the default blockPrivate=true, a loopback webhook must be refused
	// before any HTTP request is sent.
	d := New(fakeSettings{}, nil, srv.URL, "", "", nil)
	d.backoff = time.Millisecond
	if _, err := d.SendTest(context.Background()); err != nil {
		// SendTest currently reports "configured channels" — it does not
		// surface per-post errors. The important check is that nothing
		// reached the loopback server.
		t.Logf("SendTest returned: %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("SSRF guard failed: loopback hit %d times", hits.Load())
	}
}

func TestPostRetriesThenSucceeds(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := New(fakeSettings{}, nil, srv.URL, "", "", nil)
	d.blockPrivate = false
	d.backoff = time.Millisecond // keep the test fast
	if _, err := d.SendTest(context.Background()); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("hits = %d, want 2 (one 500, one 200)", hits.Load())
	}
}
