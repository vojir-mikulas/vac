package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

	d := New(fakeSettings{}, nil, srv.URL, "", "", SMTPEnv{}, false, nil)
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
	d := New(fakeSettings{}, nil, "", "", "", SMTPEnv{}, false, nil)
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
	d := New(fakeSettings{row: store.NotificationSettingsRow{Events: events}}, nil, srv.URL, "", "", SMTPEnv{}, false, nil)
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
	d := New(fakeSettings{}, nil, srv.URL, "", "", SMTPEnv{}, false, nil)
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

func TestEmailMessageRendersFields(t *testing.T) {
	subject, body := emailMessage(Event{
		Type:     EventDeployFailed,
		Title:    "Deploy failed: blog",
		AppID:    "a1",
		Service:  "web",
		Commit:   "abc1234",
		Message:  "build error",
		Duration: 12 * time.Second,
	}, "https://vac.example.com")

	if subject != "Deploy failed: blog" {
		t.Errorf("subject = %q", subject)
	}
	for _, want := range []string{"build error", "Commit: abc1234", "Service: web", "Duration: 12s", "Open in VAC: https://vac.example.com/apps/a1"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestParseRecipients(t *testing.T) {
	got := parseRecipients(" a@x.com,b@x.com\n c@x.com ;; ")
	want := []string{"a@x.com", "b@x.com", "c@x.com"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("recipient %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSMTPConfigured(t *testing.T) {
	full := smtpConfig{host: "smtp.x.com", from: "vac@x.com", to: "ops@x.com"}
	if !full.configured() {
		t.Error("expected fully-specified config to be configured")
	}
	for _, c := range []smtpConfig{
		{from: "vac@x.com", to: "ops@x.com"},                 // no host
		{host: "smtp.x.com", to: "ops@x.com"},                // no from
		{host: "smtp.x.com", from: "vac@x.com"},              // no recipients
		{host: "smtp.x.com", from: "vac@x.com", to: "  ,, "}, // blank recipients
	} {
		if c.configured() {
			t.Errorf("expected %+v to be unconfigured", c)
		}
	}
}

func TestSendEmailRefusesPrivateAddress(t *testing.T) {
	cfg := smtpConfig{host: "localhost", port: "2525", from: "vac@x.com", to: "ops@x.com", tlsMode: tlsModeNone}
	err := sendEmail(context.Background(), cfg, false, "subj", "body")
	if err == nil {
		t.Fatal("expected refusal for loopback host")
	}
	if !errors.Is(err, ErrPrivateAddress) {
		t.Errorf("err = %v, want ErrPrivateAddress", err)
	}
}

func TestSendEmailAllowsPrivateWhenOptedIn(t *testing.T) {
	// With allowPrivate, the guard is skipped — the loopback host resolves and
	// the dial proceeds (and fails to connect, but NOT with ErrPrivateAddress).
	cfg := smtpConfig{host: "127.0.0.1", port: "1", from: "vac@x.com", to: "ops@x.com", tlsMode: tlsModeNone}
	err := sendEmail(context.Background(), cfg, true, "subj", "body")
	if errors.Is(err, ErrPrivateAddress) {
		t.Errorf("guard fired despite allowPrivate: %v", err)
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

	d := New(fakeSettings{}, nil, srv.URL, "", "", SMTPEnv{}, false, nil)
	d.blockPrivate = false
	d.backoff = time.Millisecond // keep the test fast
	if _, err := d.SendTest(context.Background()); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("hits = %d, want 2 (one 500, one 200)", hits.Load())
	}
}
