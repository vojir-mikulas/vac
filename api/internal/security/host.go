package security

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// hostExecTimeout bounds each read-only host command so a hung binary can't
// block a dashboard request.
const hostExecTimeout = 3 * time.Second

// snapshotStale marks a host-agent snapshot as stale once it's older than this.
// The agent (scripts/vac-security-agent.sh) refreshes every ~60s, so a gap this
// wide means the timer stopped — surfaced as "host agent not reporting" rather
// than a false "not detected".
const snapshotStale = 5 * time.Minute

// Fail2banState is the read-only fail2ban view. Detected=false means the tool
// isn't present/readable on this host — the panel renders "not detected" rather
// than an error. Source records where the read came from ("agent" = host-side
// collector snapshot, "host" = direct exec when VAC runs on the host).
type Fail2banState struct {
	Detected    bool           `json:"detected"`
	Jails       []Fail2banJail `json:"jails"`
	Stale       bool           `json:"stale"`
	GeneratedAt *time.Time     `json:"generated_at,omitempty"`
	Source      string         `json:"source,omitempty"`
}

// Fail2banJail is one jail's banned-IP summary.
type Fail2banJail struct {
	Name            string   `json:"name"`
	CurrentlyBanned int      `json:"currently_banned"`
	TotalBanned     int      `json:"total_banned"`
	BannedIPs       []string `json:"banned_ips"`
}

// FirewallState is the read-only firewall view. Backend names the tool the rules
// came from ("ufw" | "nftables" | ""); Detected=false → "not detected".
type FirewallState struct {
	Detected    bool       `json:"detected"`
	Backend     string     `json:"backend"`
	Active      bool       `json:"active"`
	Rules       []string   `json:"rules"`
	Stale       bool       `json:"stale"`
	GeneratedAt *time.Time `json:"generated_at,omitempty"`
	Source      string     `json:"source,omitempty"`
}

// Host returns read-only fail2ban/firewall state. In production vac-api runs in
// a sandboxed container that can't see host binaries (no fail2ban-client / ufw /
// nft on PATH, no privilege), so it reads a snapshot written by a small host-side
// collector (scripts/vac-security-agent.sh, installed as a systemd timer) into a
// shared, read-only-mounted file. When no snapshot exists — e.g. VAC running
// directly on the host in dev — it falls back to direct read-only exec.
//
// It NEVER mutates host state: only `status`/list subcommands are ever invoked,
// and the snapshot path is read, never written.
type Host struct {
	// snapshotPath is the host-agent snapshot file. Empty disables the snapshot
	// path and forces direct exec (used by tests / pure host installs).
	snapshotPath string
	// cmdDir is the write-back queue the control plane drops ban requests into
	// for the host agent to drain (the control plane can't run fail2ban-client
	// itself). It sits beside the snapshot, mounted read-write while the snapshot
	// stays read-only so a compromised control plane still can't forge state.
	cmdDir string
	now    func() time.Time
	// run executes argv and returns combined stdout (and an error on non-zero
	// exit / missing binary). Defaults to a real, env-stripped exec.
	run func(ctx context.Context, name string, args ...string) (string, error)
	// look reports whether a binary is on PATH. Defaults to exec.LookPath.
	look func(name string) bool
}

// NewHost returns a Host that prefers the host-agent snapshot at snapshotPath and
// falls back to direct read-only exec when it's absent.
func NewHost(snapshotPath string) *Host {
	cmdDir := ""
	if snapshotPath != "" {
		cmdDir = filepath.Join(filepath.Dir(snapshotPath), "commands")
	}
	return &Host{
		snapshotPath: snapshotPath,
		cmdDir:       cmdDir,
		now:          time.Now,
		run:          runReadOnly,
		look:         binaryPresent,
	}
}

// runReadOnly executes a read-only command with a bounded timeout and a minimal
// env (never inheriting os.Environ, which would leak VAC_MASTER_KEY).
func runReadOnly(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, hostExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed read-only argv, no user input
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	out, err := cmd.Output()
	return string(out), err
}

func binaryPresent(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// Fail2ban returns the current jail/ban state. It reads the host-agent snapshot
// when present (the production path), else falls back to direct exec. Never
// errors on a missing tool — Detected:false renders as "not detected".
func (h *Host) Fail2ban(ctx context.Context) Fail2banState {
	if snap, ok := h.readSnapshot(); ok {
		st := snap.fail2ban
		st.Source = "agent"
		if snap.generatedAt != nil {
			st.GeneratedAt = snap.generatedAt
			st.Stale = h.now().Sub(*snap.generatedAt) > snapshotStale
		}
		return st
	}
	return h.fail2banExec(ctx)
}

// fail2banExec is the direct-exec read used when no snapshot is available.
func (h *Host) fail2banExec(ctx context.Context) Fail2banState {
	if !h.look("fail2ban-client") {
		return Fail2banState{Detected: false}
	}
	statusOut, err := h.run(ctx, "fail2ban-client", "status")
	if err != nil {
		// Present but unreadable (e.g. needs root / socket down) — degrade
		// gracefully rather than surfacing an error to the dashboard.
		return Fail2banState{Detected: false}
	}
	jailNames := parseFail2banJailList(statusOut)
	state := Fail2banState{Detected: true, Source: "host"}
	for _, name := range jailNames {
		jailOut, err := h.run(ctx, "fail2ban-client", "status", name)
		if err != nil {
			continue
		}
		state.Jails = append(state.Jails, parseFail2banJail(name, jailOut))
	}
	return state
}

// jailNamePattern bounds a fail2ban jail name to safe characters before it ever
// reaches a shell — defence in depth alongside the host agent's own re-check.
var jailNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ErrInvalidBan is returned when the jail name or IP fails validation.
var ErrInvalidBan = fmt.Errorf("security: invalid jail or IP")

// Ban asks fail2ban to ban ip in jail. fail2ban already auto-bans; this is the
// operator's manual override. In production (host-agent mode, snapshot present)
// it enqueues a request the host agent drains on its next tick — so the ban
// applies within the agent interval, not instantly — and reports queued=true.
// When VAC runs directly on the host (no snapshot) it execs fail2ban-client and
// reports queued=false. Both paths validate jail/ip first.
func (h *Host) Ban(ctx context.Context, jail, ip string) (queued bool, err error) {
	if !jailNamePattern.MatchString(jail) || net.ParseIP(ip) == nil {
		return false, ErrInvalidBan
	}
	if _, ok := h.readSnapshot(); ok && h.cmdDir != "" {
		return true, h.enqueueBan(jail, ip)
	}
	return false, h.banExec(ctx, jail, ip)
}

// enqueueBan writes one ban request into the host-agent command queue. It writes
// to a temp file then renames so the agent never reads a half-written request;
// the line format is "ban <jail> <ip>".
func (h *Host) enqueueBan(jail, ip string) error {
	if err := os.MkdirAll(h.cmdDir, 0o755); err != nil { //nolint:gosec // shared IPC queue dir; the host-agent reads it as a separate (possibly different-uid) process
		return err
	}
	f, err := os.CreateTemp(h.cmdDir, "ban-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op once the rename succeeds
	if _, err := fmt.Fprintf(f, "ban %s %s\n", jail, ip); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, strings.TrimSuffix(tmp, ".tmp")+".cmd")
}

// banExec is the direct-exec ban used when VAC runs on the host (no agent).
func (h *Host) banExec(ctx context.Context, jail, ip string) error {
	if !h.look("fail2ban-client") {
		return fmt.Errorf("security: fail2ban-client not available")
	}
	_, err := h.run(ctx, "fail2ban-client", "set", jail, "banip", ip)
	return err
}

// parseFail2banJailList extracts jail names from `fail2ban-client status`, whose
// "Jail list:" line is a comma-separated list.
func parseFail2banJailList(out string) []string {
	for _, line := range strings.Split(out, "\n") {
		// The line is decorated, e.g. "`- Jail list:\tsshd, caddy" — match on the
		// label rather than a fixed prefix.
		i := strings.Index(line, "Jail list:")
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(line[i+len("Jail list:"):])
		if rest == "" {
			return nil
		}
		var names []string
		for _, n := range strings.Split(rest, ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		return names
	}
	return nil
}

// parseFail2banJail parses `fail2ban-client status <jail>` (currently/total
// banned counts + the banned IP list).
func parseFail2banJail(name, out string) Fail2banJail {
	jail := Fail2banJail{Name: name}
	// Lines are tree-decorated, e.g. "   |- Currently banned: 2" — match on the
	// label and read what follows.
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "Currently banned:"):
			jail.CurrentlyBanned = atoiSafe(after(line, "Currently banned:"))
		case strings.Contains(line, "Total banned:"):
			jail.TotalBanned = atoiSafe(after(line, "Total banned:"))
		case strings.Contains(line, "Banned IP list:"):
			jail.BannedIPs = strings.Fields(after(line, "Banned IP list:"))
		}
	}
	return jail
}

// Firewall returns the host firewall rules. It reads the host-agent snapshot when
// present (the production path), else falls back to direct exec preferring ufw
// then nftables. Never errors on a missing tool.
func (h *Host) Firewall(ctx context.Context) FirewallState {
	if snap, ok := h.readSnapshot(); ok {
		st := snap.firewall
		st.Source = "agent"
		if snap.generatedAt != nil {
			st.GeneratedAt = snap.generatedAt
			st.Stale = h.now().Sub(*snap.generatedAt) > snapshotStale
		}
		return st
	}
	return h.firewallExec(ctx)
}

// firewallExec is the direct-exec read used when no snapshot is available.
func (h *Host) firewallExec(ctx context.Context) FirewallState {
	if h.look("ufw") {
		if out, err := h.run(ctx, "ufw", "status", "verbose"); err == nil {
			st := parseUFW(out)
			st.Source = "host"
			return st
		}
	}
	if h.look("nft") {
		if out, err := h.run(ctx, "nft", "list", "ruleset"); err == nil {
			st := parseNFT(out)
			st.Source = "host"
			return st
		}
	}
	return FirewallState{Detected: false}
}

// parseUFW reads `ufw status verbose` — the "Status: active" header plus the
// rule lines.
func parseUFW(out string) FirewallState {
	state := FirewallState{Detected: true, Backend: "ufw"}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Status:") {
			state.Active = strings.Contains(line, "active")
			continue
		}
		// Skip the column header / decoration lines; keep actual rules.
		if strings.HasPrefix(line, "To ") || strings.HasPrefix(line, "--") ||
			strings.HasPrefix(line, "Default:") || strings.HasPrefix(line, "Logging:") ||
			strings.HasPrefix(line, "New profiles:") {
			continue
		}
		state.Rules = append(state.Rules, line)
	}
	return state
}

// parseNFT keeps the non-empty lines of `nft list ruleset` verbatim — the raw
// ruleset is the read-only display.
func parseNFT(out string) FirewallState {
	state := FirewallState{Detected: true, Backend: "nftables", Active: strings.TrimSpace(out) != ""}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			state.Rules = append(state.Rules, line)
		}
	}
	return state
}

// hostSnapshot is the parsed host-agent snapshot file.
type hostSnapshot struct {
	generatedAt *time.Time
	fail2ban    Fail2banState
	firewall    FirewallState
}

// readSnapshot loads and parses the host-agent snapshot. ok=false when the path
// is unset or the file is absent/unreadable (then callers fall back to exec).
func (h *Host) readSnapshot() (hostSnapshot, bool) {
	if h.snapshotPath == "" {
		return hostSnapshot{}, false
	}
	data, err := os.ReadFile(h.snapshotPath) //nolint:gosec // operator-controlled config path
	if err != nil {
		return hostSnapshot{}, false
	}
	return parseSnapshot(string(data)), true
}

// parseSnapshot decodes the sectioned snapshot the host agent writes. The format
// is a leading "generated_at: <RFC3339>" line followed by "@@@ <section>" markers
// whose bodies are the verbatim command outputs, so the same parsers used for
// direct exec apply. Sections:
//
//	@@@ fail2ban-status        # presence marker (fail2ban-client status output)
//	@@@ fail2ban-jail <name>   # one per jail (fail2ban-client status <jail>)
//	@@@ firewall ufw|nftables|none
func parseSnapshot(content string) hostSnapshot {
	var snap hostSnapshot
	var header string
	var buf []string
	var f2bDetected bool
	f2b := Fail2banState{}
	fw := FirewallState{Detected: false}

	flush := func() {
		body := strings.Join(buf, "\n")
		switch {
		case header == "fail2ban-status":
			f2bDetected = true
		case strings.HasPrefix(header, "fail2ban-jail "):
			name := strings.TrimSpace(strings.TrimPrefix(header, "fail2ban-jail "))
			if name != "" {
				f2b.Jails = append(f2b.Jails, parseFail2banJail(name, body))
			}
		case strings.HasPrefix(header, "firewall "):
			switch strings.TrimSpace(strings.TrimPrefix(header, "firewall ")) {
			case "ufw":
				fw = parseUFW(body)
			case "nftables":
				fw = parseNFT(body)
			}
		}
		buf = nil
	}

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "@@@ ") {
			flush()
			header = strings.TrimSpace(strings.TrimPrefix(line, "@@@ "))
			continue
		}
		if header == "" {
			if ts, ok := parseGeneratedAt(line); ok {
				snap.generatedAt = &ts
			}
			continue
		}
		buf = append(buf, line)
	}
	flush()

	f2b.Detected = f2bDetected
	snap.fail2ban = f2b
	snap.firewall = fw
	return snap
}

// parseGeneratedAt reads the "generated_at: <RFC3339>" preamble line.
func parseGeneratedAt(line string) (time.Time, bool) {
	v := after(line, "generated_at:")
	if v == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// after returns the text following label in line (trimmed), or "" if absent.
func after(line, label string) string {
	i := strings.Index(line, label)
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(line[i+len(label):])
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
