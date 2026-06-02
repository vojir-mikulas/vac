package security

import (
	"context"
	"errors"
	"testing"
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
