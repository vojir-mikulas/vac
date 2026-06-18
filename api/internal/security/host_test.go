package security

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFail2ban_NotDetectedWhenAbsent(t *testing.T) {
	h := &Host{
		look: func(string) bool { return false },
		run: func(context.Context, string, ...string) (string, error) {
			t.Fatal("run should not be called")
			return "", nil
		},
	}
	st := h.Fail2ban(context.Background())
	if st.Detected {
		t.Errorf("expected not detected, got %+v", st)
	}
}

func TestFail2ban_PresentButUnreadable(t *testing.T) {
	h := &Host{
		look: func(string) bool { return true },
		run:  func(context.Context, string, ...string) (string, error) { return "", errors.New("permission denied") },
	}
	if h.Fail2ban(context.Background()).Detected {
		t.Error("unreadable fail2ban should degrade to not-detected")
	}
}

func TestFail2ban_ParsesFixture(t *testing.T) {
	statusOut := `Status
|- Number of jail:      1
` + "`- Jail list:   sshd, caddy"
	jailOut := `Status for the jail: sshd
|- Filter
|  |- Currently failed: 0
|- Actions
   |- Currently banned: 2
   |- Total banned:     7
   ` + "`- Banned IP list:   1.2.3.4 5.6.7.8"

	h := &Host{
		look: func(string) bool { return true },
		run: func(_ context.Context, _ string, args ...string) (string, error) {
			if len(args) == 1 { // "status"
				return statusOut, nil
			}
			return jailOut, nil // "status <jail>"
		},
	}
	st := h.Fail2ban(context.Background())
	if !st.Detected {
		t.Fatal("expected detected")
	}
	if len(st.Jails) != 2 {
		t.Fatalf("jails = %d, want 2 (%+v)", len(st.Jails), st.Jails)
	}
	j := st.Jails[0]
	if j.Name != "sshd" || j.CurrentlyBanned != 2 || j.TotalBanned != 7 {
		t.Errorf("jail parse wrong: %+v", j)
	}
	if len(j.BannedIPs) != 2 || j.BannedIPs[0] != "1.2.3.4" {
		t.Errorf("banned IPs wrong: %v", j.BannedIPs)
	}
}

func TestBan_RejectsInvalidInput(t *testing.T) {
	h := &Host{
		run: func(context.Context, string, ...string) (string, error) {
			t.Fatal("run should not be called for invalid input")
			return "", nil
		},
	}
	for _, tc := range []struct{ jail, ip string }{
		{"sshd", "not-an-ip"},
		{"bad jail", "1.2.3.4"},
		{"sshd; rm -rf", "1.2.3.4"},
		{"sshd", "1.2.3.4; reboot"},
		{"", "1.2.3.4"},
	} {
		if _, err := h.Ban(context.Background(), tc.jail, tc.ip); !errors.Is(err, ErrInvalidBan) {
			t.Errorf("Ban(%q,%q) err = %v, want ErrInvalidBan", tc.jail, tc.ip, err)
		}
	}
}

func TestBan_DirectExecWhenNoSnapshot(t *testing.T) {
	var gotArgs []string
	h := &Host{
		look: func(string) bool { return true },
		run: func(_ context.Context, _ string, args ...string) (string, error) {
			gotArgs = args
			return "", nil
		},
	}
	queued, err := h.Ban(context.Background(), "sshd", "1.2.3.4")
	if err != nil {
		t.Fatalf("Ban: %v", err)
	}
	if queued {
		t.Error("queued should be false in direct-exec mode")
	}
	want := []string{"set", "sshd", "banip", "1.2.3.4"}
	if len(gotArgs) != 4 || gotArgs[1] != want[1] || gotArgs[3] != want[3] {
		t.Errorf("exec args = %v, want %v", gotArgs, want)
	}
}

func TestBan_EnqueuesWhenSnapshotPresent(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	snap := filepath.Join(dir, "host.snapshot")
	if err := os.WriteFile(snap, []byte("generated_at: "+now.Format(time.RFC3339)+"\n@@@ fail2ban-status\nStatus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &Host{
		snapshotPath: snap,
		cmdDir:       filepath.Join(dir, "commands"),
		now:          func() time.Time { return now },
		run: func(context.Context, string, ...string) (string, error) {
			t.Fatal("run should not be called in queue mode")
			return "", nil
		},
	}
	queued, err := h.Ban(context.Background(), "sshd", "1.2.3.4")
	if err != nil {
		t.Fatalf("Ban: %v", err)
	}
	if !queued {
		t.Error("queued should be true when snapshot present")
	}
	entries, err := filepath.Glob(filepath.Join(dir, "commands", "*.cmd"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("want 1 queued .cmd file, got %v (err %v)", entries, err)
	}
	body, _ := os.ReadFile(entries[0])
	if string(body) != "ban sshd 1.2.3.4\n" {
		t.Errorf("queued body = %q", body)
	}
}

func TestSnapshot_ParsesAndFresh(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	content := "generated_at: " + now.Add(-30*time.Second).Format(time.RFC3339) + "\n" +
		"@@@ fail2ban-status\n" +
		"Status\n`- Jail list:\tsshd\n" +
		"@@@ fail2ban-jail sshd\n" +
		"Status for the jail: sshd\n   |- Currently banned: 3\n   |- Total banned: 9\n   `- Banned IP list: 9.9.9.9\n" +
		"@@@ firewall ufw\n" +
		"Status: active\n22/tcp                     ALLOW IN    Anywhere\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "host.snapshot")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &Host{snapshotPath: path, now: func() time.Time { return now }}

	f2b := h.Fail2ban(context.Background())
	if !f2b.Detected || f2b.Source != "agent" || f2b.Stale {
		t.Fatalf("fail2ban snapshot wrong: %+v", f2b)
	}
	if len(f2b.Jails) != 1 || f2b.Jails[0].CurrentlyBanned != 3 || f2b.Jails[0].TotalBanned != 9 {
		t.Errorf("fail2ban jail parse wrong: %+v", f2b.Jails)
	}

	fw := h.Firewall(context.Background())
	if !fw.Detected || fw.Backend != "ufw" || !fw.Active || fw.Stale {
		t.Errorf("firewall snapshot wrong: %+v", fw)
	}
}

func TestSnapshot_StaleFlagsOldGeneration(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	content := "generated_at: " + now.Add(-10*time.Minute).Format(time.RFC3339) + "\n" +
		"@@@ firewall ufw\nStatus: active\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "host.snapshot")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &Host{snapshotPath: path, now: func() time.Time { return now }}
	if fw := h.Firewall(context.Background()); !fw.Stale {
		t.Errorf("expected stale firewall snapshot, got %+v", fw)
	}
}

func TestSnapshot_MissingFileFallsBackToExec(t *testing.T) {
	// snapshotPath set but file absent → fall back to exec (which reports absent).
	h := &Host{
		snapshotPath: filepath.Join(t.TempDir(), "absent.snapshot"),
		now:          time.Now,
		look:         func(string) bool { return false },
	}
	if h.Firewall(context.Background()).Detected {
		t.Error("missing snapshot + no host tools should report not-detected")
	}
}

func TestFirewall_NotDetectedWhenAbsent(t *testing.T) {
	h := &Host{look: func(string) bool { return false }}
	if h.Firewall(context.Background()).Detected {
		t.Error("expected firewall not detected")
	}
}

func TestFirewall_ParsesUFW(t *testing.T) {
	out := `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere
80/tcp                     ALLOW IN    Anywhere`

	h := &Host{
		look: func(name string) bool { return name == "ufw" },
		run:  func(context.Context, string, ...string) (string, error) { return out, nil },
	}
	st := h.Firewall(context.Background())
	if !st.Detected || st.Backend != "ufw" || !st.Active {
		t.Fatalf("ufw parse header wrong: %+v", st)
	}
	if len(st.Rules) != 2 {
		t.Errorf("ufw rules = %d, want 2 (%v)", len(st.Rules), st.Rules)
	}
}

func TestFirewall_ParsesNFTWhenNoUFW(t *testing.T) {
	out := `table inet filter {
	chain input {
		type filter hook input priority 0;
	}
}`
	h := &Host{
		look: func(name string) bool { return name == "nft" },
		run:  func(context.Context, string, ...string) (string, error) { return out, nil },
	}
	st := h.Firewall(context.Background())
	if !st.Detected || st.Backend != "nftables" || !st.Active {
		t.Fatalf("nft parse wrong: %+v", st)
	}
	if len(st.Rules) == 0 {
		t.Error("expected nft rules")
	}
}
