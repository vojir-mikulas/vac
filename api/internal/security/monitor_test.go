package security

import (
	"fmt"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/reqmetrics"
)

// fakeNotifier records TrafficAnomaly calls.
type fakeNotifier struct {
	calls []string // "kind|detail"
}

func (f *fakeNotifier) TrafficAnomaly(_, _, kind, detail string) {
	f.calls = append(f.calls, kind+"|"+detail)
}

func line(ip string, status int, ua string) reqmetrics.AccessLine {
	var l reqmetrics.AccessLine
	l.Request.Host = "blog.example.com"
	l.Request.ClientIP = ip
	l.Status = status
	if ua != "" {
		l.Request.Headers = map[string][]string{"User-Agent": {ua}}
	}
	return l
}

func TestMonitor_TopTalkerTracking(t *testing.T) {
	m := NewMonitor(Config{Window: time.Minute, RPSThreshold: 1000, ErrThreshold: 1000}, nil, nil)
	for i := 0; i < 5; i++ {
		m.Observe(line("1.1.1.1", 200, "curl"))
	}
	for i := 0; i < 2; i++ {
		m.Observe(line("2.2.2.2", 500, "bot"))
	}

	snap := m.Snapshot(10)
	if snap.TrackedIPs != 2 {
		t.Fatalf("tracked IPs = %d, want 2", snap.TrackedIPs)
	}
	if len(snap.TopTalkers) != 2 || snap.TopTalkers[0].IP != "1.1.1.1" {
		t.Fatalf("top talker = %+v, want 1.1.1.1 first", snap.TopTalkers)
	}
	if snap.TopTalkers[0].Requests != 5 {
		t.Errorf("top talker requests = %d, want 5", snap.TopTalkers[0].Requests)
	}
	if snap.TopTalkers[1].Errors != 2 {
		t.Errorf("second talker errors = %d, want 2", snap.TopTalkers[1].Errors)
	}
	if snap.TotalRequests != 7 || snap.TotalErrors != 2 {
		t.Errorf("totals = req %d err %d, want 7/2", snap.TotalRequests, snap.TotalErrors)
	}
}

func TestMonitor_WindowTrim(t *testing.T) {
	now := time.Now()
	m := NewMonitor(Config{Window: time.Minute, RPSThreshold: 1000, ErrThreshold: 1000}, nil, nil)
	m.now = func() time.Time { return now }

	m.Observe(line("1.1.1.1", 200, ""))
	if got := m.Snapshot(10).TotalRequests; got != 1 {
		t.Fatalf("before trim total = %d, want 1", got)
	}
	// Advance past the window; the entry should age out.
	now = now.Add(2 * time.Minute)
	snap := m.Snapshot(10)
	if snap.TotalRequests != 0 || snap.TrackedIPs != 0 {
		t.Errorf("after trim = req %d ips %d, want 0/0", snap.TotalRequests, snap.TrackedIPs)
	}
}

func TestMonitor_ErrorSurgeTripsOncePerCooldown(t *testing.T) {
	now := time.Now()
	notifier := &fakeNotifier{}
	m := NewMonitor(Config{Window: time.Minute, RPSThreshold: 1000, ErrThreshold: 5, Cooldown: 10 * time.Minute}, notifier, nil)
	m.now = func() time.Time { return now }

	// 5 errors → trips once.
	for i := 0; i < 5; i++ {
		m.Observe(line("9.9.9.9", 404, ""))
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("notifier calls = %d, want 1 (%v)", len(notifier.calls), notifier.calls)
	}
	if got := notifier.calls[0]; got[:11] != "error surge" {
		t.Errorf("kind = %q, want error surge", got)
	}

	// More errors within cooldown → no new alert.
	for i := 0; i < 5; i++ {
		m.Observe(line("9.9.9.9", 500, ""))
	}
	if len(notifier.calls) != 1 {
		t.Errorf("within cooldown calls = %d, want still 1", len(notifier.calls))
	}

	// After cooldown → trips again.
	now = now.Add(11 * time.Minute)
	for i := 0; i < 5; i++ {
		m.Observe(line("9.9.9.9", 500, ""))
	}
	if len(notifier.calls) != 2 {
		t.Errorf("after cooldown calls = %d, want 2", len(notifier.calls))
	}

	if got := len(m.Snapshot(10).RecentAnomalies); got != 2 {
		t.Errorf("recent anomalies = %d, want 2", got)
	}
}

func TestMonitor_RequestSpikeTrips(t *testing.T) {
	notifier := &fakeNotifier{}
	m := NewMonitor(Config{Window: time.Minute, RPSThreshold: 10, ErrThreshold: 1000, Cooldown: time.Hour}, notifier, nil)
	for i := 0; i < 10; i++ {
		m.Observe(line("5.5.5.5", 200, ""))
	}
	if len(notifier.calls) != 1 || notifier.calls[0][:13] != "request spike" {
		t.Fatalf("calls = %v, want one request spike", notifier.calls)
	}
}

func TestMonitor_AllowlistSuppressesNotification(t *testing.T) {
	notifier := &fakeNotifier{}
	// 5.5.5.5 is an exact match; 6.6.6.x is covered by a CIDR.
	m := NewMonitor(Config{
		Window: time.Minute, RPSThreshold: 10, ErrThreshold: 1000, Cooldown: time.Hour,
		Allowlist: []string{"5.5.5.5", "6.6.6.0/24"},
	}, notifier, nil)

	// Allowlisted exact IP: trips the threshold but no notification fires.
	for i := 0; i < 10; i++ {
		m.Observe(line("5.5.5.5", 200, ""))
	}
	// Allowlisted via CIDR.
	for i := 0; i < 10; i++ {
		m.Observe(line("6.6.6.42", 200, ""))
	}
	if len(notifier.calls) != 0 {
		t.Fatalf("notifier calls = %d, want 0 for allowlisted IPs (%v)", len(notifier.calls), notifier.calls)
	}

	// But the anomalies are still recorded and flagged as suppressed.
	anomalies := m.Snapshot(10).RecentAnomalies
	if len(anomalies) != 2 {
		t.Fatalf("recent anomalies = %d, want 2 (still recorded)", len(anomalies))
	}
	for _, a := range anomalies {
		if !a.Suppressed {
			t.Errorf("anomaly %s not flagged suppressed", a.IP)
		}
	}

	// A non-allowlisted IP still pages.
	for i := 0; i < 10; i++ {
		m.Observe(line("7.7.7.7", 200, ""))
	}
	if len(notifier.calls) != 1 {
		t.Errorf("notifier calls = %d, want 1 for non-allowlisted IP", len(notifier.calls))
	}
}

func TestMonitor_LRUCap(t *testing.T) {
	m := NewMonitor(Config{Window: time.Hour, RPSThreshold: 1e9, ErrThreshold: 1e9}, nil, nil)
	// Feed more distinct IPs than the cap; the table must stay bounded.
	for i := 0; i < maxTrackedIPs+200; i++ {
		m.Observe(line(fmt.Sprintf("10.0.%d.%d", i/256, i%256), 200, ""))
	}
	if got := m.Snapshot(maxTrackedIPs + 500).TrackedIPs; got > maxTrackedIPs {
		t.Errorf("tracked IPs = %d, want <= %d (LRU cap)", got, maxTrackedIPs)
	}
}

func TestMonitor_IgnoresEmptyIP(t *testing.T) {
	m := NewMonitor(Config{}, nil, nil)
	m.Observe(line("", 200, ""))
	if got := m.Snapshot(10).TrackedIPs; got != 0 {
		t.Errorf("tracked IPs = %d, want 0 for empty IP", got)
	}
}

func TestAccessLine_IPAndUA(t *testing.T) {
	var l reqmetrics.AccessLine
	l.Request.RemoteIP = "3.3.3.3"
	if l.IP() != "3.3.3.3" {
		t.Errorf("IP fallback to remote_ip failed: %q", l.IP())
	}
	l.Request.ClientIP = "4.4.4.4"
	if l.IP() != "4.4.4.4" {
		t.Errorf("IP should prefer client_ip: %q", l.IP())
	}
	l.Request.Headers = map[string][]string{"User-Agent": {"agent/1"}}
	if l.UserAgent() != "agent/1" {
		t.Errorf("UserAgent = %q, want agent/1", l.UserAgent())
	}
}
