// Package security powers the read-only Security dashboard (plan 15 / E2). It
// turns VAC's two best vantage points — Caddy's access log and VAC's own
// store/config — into a "your box at a glance" view: a posture checklist, a
// streaming traffic-anomaly detector, and read-only fail2ban/firewall state.
//
// Nothing in this package mutates host state. The control plane stays sandboxed:
// posture/host reads are pure reads (and read-only exec), the traffic monitor
// only holds bounded in-memory counters fed by the existing access-log tail.
package security

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/reqmetrics"
)

// maxTrackedIPs caps the per-IP table so a flood of distinct source addresses
// can't grow memory without bound. When full, the least-recently-seen IP is
// evicted (LRU) — an attacker churning IPs loses old entries, which is fine: the
// dashboard only cares about current top talkers.
const maxTrackedIPs = 1024

// maxRecentAnomalies bounds the recent-anomalies ring shown on the dashboard.
const maxRecentAnomalies = 50

// maxRecentRequests bounds the recent-requests ring. This is the live "what's
// hitting the box right now" feed: a small, always-populated tail so the traffic
// panel shows real requests even on a quiet box (where the per-IP rate windows
// are near-empty and the top-talker table looks blank).
const maxRecentRequests = 100

// Notifier is the slice of *notify.Dispatcher the monitor calls on a breach.
type Notifier interface {
	TrafficAnomaly(appName, appID, kind, detail string)
}

// Config tunes the anomaly detector. Zero values fall back to sane defaults.
type Config struct {
	Window       time.Duration // sliding window for per-IP counters
	RPSThreshold int           // requests from one IP in the window → spike
	ErrThreshold int           // 4xx/5xx from one IP in the window → error surge
	Cooldown     time.Duration // min gap between alerts for the same IP+kind
}

func (c Config) withDefaults() Config {
	if c.Window <= 0 {
		c.Window = time.Minute
	}
	if c.RPSThreshold <= 0 {
		c.RPSThreshold = 300
	}
	if c.ErrThreshold <= 0 {
		c.ErrThreshold = 100
	}
	if c.Cooldown <= 0 {
		c.Cooldown = 10 * time.Minute
	}
	return c
}

// window is a fixed-duration sliding window of timestamps (mirrors the
// crashloop monitor's pattern). trim drops entries older than maxAge.
type window struct {
	maxAge time.Duration
	events []time.Time
}

func (w *window) record(t time.Time) {
	w.events = append(w.events, t)
	w.trim(t)
}

func (w *window) size() int { return len(w.events) }

func (w *window) trim(now time.Time) {
	cutoff := now.Add(-w.maxAge)
	idx := 0
	for ; idx < len(w.events); idx++ {
		if w.events[idx].After(cutoff) {
			break
		}
	}
	if idx > 0 {
		w.events = w.events[idx:]
	}
}

// ipCounter tracks one source IP's recent activity.
type ipCounter struct {
	ip       string
	requests window
	errors   window
	lastSeen time.Time
	lastUA   string
	lastTrip time.Time // last alert for this IP, for cooldown debounce
}

// Anomaly is one recorded threshold breach, surfaced to the dashboard.
type Anomaly struct {
	At     time.Time `json:"at"`
	IP     string    `json:"ip"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
}

// RecentRequest is one line of the live recent-requests feed.
type RecentRequest struct {
	At        time.Time `json:"at"`
	IP        string    `json:"ip"`
	Host      string    `json:"host"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
	UserAgent string    `json:"user_agent"`
}

// TopTalker is one row of the busiest-source table.
type TopTalker struct {
	IP        string    `json:"ip"`
	Requests  int       `json:"requests"`
	Errors    int       `json:"errors"`
	UserAgent string    `json:"user_agent"`
	LastSeen  time.Time `json:"last_seen"`
}

// Snapshot is the read-only view the dashboard polls.
type Snapshot struct {
	WindowSeconds   int             `json:"window_seconds"`
	TrackedIPs      int             `json:"tracked_ips"`
	TotalRequests   int             `json:"total_requests"`  // across the window, all tracked IPs
	TotalErrors     int             `json:"total_errors"`    // 4xx/5xx across the window
	TopTalkers      []TopTalker     `json:"top_talkers"`     // busiest sources, capped
	RecentRequests  []RecentRequest `json:"recent_requests"` // live tail, newest first
	RecentAnomalies []Anomaly       `json:"recent_anomalies"`
}

// Monitor maintains bounded streaming per-IP counters fed by the access-log
// observer hook and trips a notification when a source crosses a threshold.
type Monitor struct {
	cfg      Config
	notifier Notifier
	logger   *slog.Logger
	now      func() time.Time

	mu        sync.Mutex
	ips       map[string]*ipCounter
	anomalies []Anomaly       // ring, newest last
	recent    []RecentRequest // ring, newest last
}

// NewMonitor wires a Monitor. notifier may be nil (alerts are then logged only).
func NewMonitor(cfg Config, notifier Notifier, logger *slog.Logger) *Monitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Monitor{
		cfg:      cfg.withDefaults(),
		notifier: notifier,
		logger:   logger,
		now:      time.Now,
		ips:      map[string]*ipCounter{},
	}
}

// Observe is the access-log hook. It records the line against its source IP's
// sliding windows and evaluates thresholds. Cheap and lock-bounded; safe to pass
// to reqmetrics.Collector.SetObserver.
func (m *Monitor) Observe(line reqmetrics.AccessLine) {
	ip := line.IP()
	if ip == "" {
		return
	}
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	c := m.ips[ip]
	if c == nil {
		m.evictIfFull()
		c = &ipCounter{
			ip:       ip,
			requests: window{maxAge: m.cfg.Window},
			errors:   window{maxAge: m.cfg.Window},
		}
		m.ips[ip] = c
	}
	c.requests.record(now)
	if line.Status >= 400 {
		c.errors.record(now)
	}
	c.lastSeen = now
	if ua := line.UserAgent(); ua != "" {
		c.lastUA = ua
	}

	m.recordRecent(RecentRequest{
		At:        now,
		IP:        ip,
		Host:      line.Request.Host,
		Method:    line.Request.Method,
		Path:      line.Request.URI,
		Status:    line.Status,
		UserAgent: line.UserAgent(),
	})
	m.evaluate(c, now)
}

// recordRecent appends to the bounded recent-requests ring. Caller holds m.mu.
func (m *Monitor) recordRecent(r RecentRequest) {
	m.recent = append(m.recent, r)
	if len(m.recent) > maxRecentRequests {
		m.recent = m.recent[len(m.recent)-maxRecentRequests:]
	}
}

// evaluate trips an alert when a counter crosses a threshold, debounced per IP by
// the cooldown. Caller holds m.mu.
func (m *Monitor) evaluate(c *ipCounter, now time.Time) {
	if now.Sub(c.lastTrip) < m.cfg.Cooldown {
		return
	}
	var kind, detail string
	switch {
	case c.errors.size() >= m.cfg.ErrThreshold:
		kind = "error surge"
		detail = fmt.Sprintf("%s produced %d 4xx/5xx responses in %s", c.ip, c.errors.size(), m.cfg.Window)
	case c.requests.size() >= m.cfg.RPSThreshold:
		kind = "request spike"
		detail = fmt.Sprintf("%s made %d requests in %s", c.ip, c.requests.size(), m.cfg.Window)
	default:
		return
	}
	c.lastTrip = now
	m.recordAnomaly(Anomaly{At: now, IP: c.ip, Kind: kind, Detail: detail})
	m.logger.Warn("security: traffic anomaly", "kind", kind, "ip", c.ip, "detail", detail)
	if m.notifier != nil {
		m.notifier.TrafficAnomaly("", "", kind, detail)
	}
}

// recordAnomaly appends to the bounded ring. Caller holds m.mu.
func (m *Monitor) recordAnomaly(a Anomaly) {
	m.anomalies = append(m.anomalies, a)
	if len(m.anomalies) > maxRecentAnomalies {
		m.anomalies = m.anomalies[len(m.anomalies)-maxRecentAnomalies:]
	}
}

// evictIfFull drops the least-recently-seen IP when the table is at capacity.
// Caller holds m.mu.
func (m *Monitor) evictIfFull() {
	if len(m.ips) < maxTrackedIPs {
		return
	}
	var oldest *ipCounter
	for _, c := range m.ips {
		if oldest == nil || c.lastSeen.Before(oldest.lastSeen) {
			oldest = c
		}
	}
	if oldest != nil {
		delete(m.ips, oldest.ip)
	}
}

// Run periodically trims stale IP entries so idle sources don't linger in the
// table. The windows themselves trim on record; this prunes IPs that stopped
// sending entirely. Returns when ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(m.cfg.Window)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.prune()
		}
	}
}

// prune drops IP counters whose windows are now empty (no activity in the last
// window), keeping the table small between bursts.
func (m *Monitor) prune() {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for ip, c := range m.ips {
		c.requests.trim(now)
		c.errors.trim(now)
		if c.requests.size() == 0 && c.errors.size() == 0 {
			delete(m.ips, ip)
		}
	}
}

// Snapshot returns the current top talkers, aggregate rates, and recent
// anomalies for the dashboard. topN caps the talker table (<=0 → 10).
func (m *Monitor) Snapshot(topN int) Snapshot {
	if topN <= 0 {
		topN = 10
	}
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	talkers := make([]TopTalker, 0, len(m.ips))
	totalReq, totalErr := 0, 0
	active := 0
	for _, c := range m.ips {
		c.requests.trim(now)
		c.errors.trim(now)
		req, errs := c.requests.size(), c.errors.size()
		if req == 0 && errs == 0 {
			continue
		}
		active++
		totalReq += req
		totalErr += errs
		talkers = append(talkers, TopTalker{
			IP: c.ip, Requests: req, Errors: errs, UserAgent: c.lastUA, LastSeen: c.lastSeen,
		})
	}
	sort.Slice(talkers, func(i, j int) bool {
		if talkers[i].Requests != talkers[j].Requests {
			return talkers[i].Requests > talkers[j].Requests
		}
		return talkers[i].Errors > talkers[j].Errors
	})
	if len(talkers) > topN {
		talkers = talkers[:topN]
	}

	recent := make([]Anomaly, len(m.anomalies))
	copy(recent, m.anomalies)
	// newest first
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}

	reqs := make([]RecentRequest, len(m.recent))
	copy(reqs, m.recent)
	// newest first
	for i, j := 0, len(reqs)-1; i < j; i, j = i+1, j-1 {
		reqs[i], reqs[j] = reqs[j], reqs[i]
	}

	return Snapshot{
		WindowSeconds:   int(m.cfg.Window.Seconds()),
		TrackedIPs:      active,
		TotalRequests:   totalReq,
		TotalErrors:     totalErr,
		TopTalkers:      talkers,
		RecentRequests:  reqs,
		RecentAnomalies: recent,
	}
}
