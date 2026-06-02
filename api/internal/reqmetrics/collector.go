package reqmetrics

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// bucketWidth is the resolution of the rolling request-rate window.
const bucketWidth = 10 * time.Second

// Store is the slice of *store.Store the collector reads (host→service map) and
// writes (aggregated buckets).
type Store interface {
	ListAllDomains(ctx context.Context) ([]store.Domain, error)
	UpsertRequestBuckets(ctx context.Context, buckets []store.RequestBucket) error
}

// svcRef identifies the service a hostname maps to.
type svcRef struct {
	appID   string
	service string
}

type bucketKey struct {
	appID   string
	service string
	ts      time.Time
}

// Collector tails the access log, maps each line's host to a service, and
// aggregates request counts into 10s buckets flushed to the store.
type Collector struct {
	store     Store
	logPath   string
	flushIval time.Duration
	logger    *slog.Logger
	now       func() time.Time

	mu      sync.Mutex
	hosts   map[string]svcRef
	buckets map[bucketKey]*store.RequestBucket
}

// New wires a Collector. flushInterval also governs the host-map refresh.
func New(s Store, logPath string, flushInterval time.Duration, logger *slog.Logger) *Collector {
	if logger == nil {
		logger = slog.Default()
	}
	if flushInterval <= 0 {
		flushInterval = 10 * time.Second
	}
	return &Collector{
		store:     s,
		logPath:   logPath,
		flushIval: flushInterval,
		logger:    logger,
		now:       time.Now,
		hosts:     map[string]svcRef{},
		buckets:   map[bucketKey]*store.RequestBucket{},
	}
}

// accessLine is the subset of a Caddy JSON access-log line we read.
type accessLine struct {
	Request struct {
		Host string `json:"host"`
	} `json:"request"`
	Status int   `json:"status"`
	Size   int64 `json:"size"`
}

// Run refreshes the host map, starts the tailer, and flushes on a ticker until
// ctx is cancelled.
func (c *Collector) Run(ctx context.Context) {
	c.refreshHosts(ctx)
	go Tail(ctx, c.logPath, time.Second, c.handleLine)

	ticker := time.NewTicker(c.flushIval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Best-effort final flush — bounded so a stuck Postgres can't
			// block SIGTERM indefinitely.
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			c.flush(flushCtx)
			cancel()
			return
		case <-ticker.C:
			c.refreshHosts(ctx)
			c.flush(ctx)
		}
	}
}

func (c *Collector) handleLine(line []byte) {
	var l accessLine
	if err := json.Unmarshal(line, &l); err != nil {
		return // skip non-JSON / partial lines
	}
	if l.Request.Host == "" {
		return
	}
	c.record(l.Request.Host, l.Status, l.Size)
}

// record resolves the host to a service and increments its current bucket.
// Lines for unknown hosts are dropped.
func (c *Collector) record(host string, status int, size int64) {
	host = strings.ToLower(host)
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i] // strip any :port
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	ref, ok := c.hosts[host]
	if !ok {
		return
	}
	key := bucketKey{appID: ref.appID, service: ref.service, ts: c.now().UTC().Truncate(bucketWidth)}
	b := c.buckets[key]
	if b == nil {
		b = &store.RequestBucket{AppID: ref.appID, ServiceName: ref.service, BucketTS: key.ts}
		c.buckets[key] = b
	}
	b.Requests++
	if status >= 500 {
		b.Errors++
	}
	b.BytesOut += size
}

func (c *Collector) refreshHosts(ctx context.Context) {
	domains, err := c.store.ListAllDomains(ctx)
	if err != nil {
		c.logger.Warn("reqmetrics: refresh hosts", "err", err)
		return
	}
	next := make(map[string]svcRef, len(domains))
	for _, d := range domains {
		// Unassigned domains (added but not yet bound to a service) route
		// nowhere, so they carry no request metrics.
		if !d.Assigned() {
			continue
		}
		next[strings.ToLower(d.Hostname)] = svcRef{appID: d.AppID, service: d.ServiceName}
	}
	c.mu.Lock()
	c.hosts = next
	c.mu.Unlock()
}

func (c *Collector) flush(ctx context.Context) {
	c.mu.Lock()
	if len(c.buckets) == 0 {
		c.mu.Unlock()
		return
	}
	out := make([]store.RequestBucket, 0, len(c.buckets))
	for _, b := range c.buckets {
		out = append(out, *b)
	}
	c.buckets = map[bucketKey]*store.RequestBucket{}
	c.mu.Unlock()

	if err := c.store.UpsertRequestBuckets(ctx, out); err != nil {
		c.logger.Warn("reqmetrics: flush", "err", err, "buckets", len(out))
	}
}
