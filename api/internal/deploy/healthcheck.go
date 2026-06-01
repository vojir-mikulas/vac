package deploy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

// ErrHealthCheckFailed wraps the final attempt's error after retries are
// exhausted. The pipeline maps this to deployment status `error`.
var ErrHealthCheckFailed = errors.New("deploy: health check failed")

// HTTPChecker is the production HTTP probe. Tests can stand a fake by
// implementing the Checker interface.
type HTTPChecker struct{ Client *http.Client }

// Checker is the slice of HTTPChecker the pipeline uses. Decoupled for
// tests.
type Checker interface {
	Check(ctx context.Context, url string) (status int, err error)
}

// Check issues a single HTTP GET. Any 2xx or 3xx is treated as pass; the
// pipeline only cares whether the service is answering on its port.
func (h HTTPChecker) Check(ctx context.Context, url string) (int, error) {
	client := h.Client
	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
				ResponseHeaderTimeout: 5 * time.Second,
			},
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}

// CheckWithRetry probes `url` until a 2xx/3xx response arrives or the retry
// budget is exhausted. Backoff is fixed-step: 1s, 2s, 3s, ... `retries`s.
// `overall` caps the total time spent regardless of attempt count.
func CheckWithRetry(ctx context.Context, c Checker, url string, retries int, overall time.Duration) error {
	if c == nil {
		c = HTTPChecker{}
	}
	if retries <= 0 {
		retries = 1
	}
	if overall <= 0 {
		overall = 30 * time.Second
	}
	deadline := time.Now().Add(overall)
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		if time.Now().After(deadline) {
			break
		}
		attemptCtx, cancel := context.WithDeadline(ctx, deadline)
		status, err := c.Check(attemptCtx, url)
		cancel()
		if err == nil && status >= 200 && status < 400 {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("HTTP %d", status)
		}
		if attempt == retries {
			break
		}
		// Linear-ish backoff bounded by the overall deadline.
		wait := time.Duration(attempt) * time.Second
		if time.Now().Add(wait).After(deadline) {
			wait = time.Until(deadline)
		}
		if wait <= 0 {
			break
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("%w: %v", ErrHealthCheckFailed, lastErr)
}

// healthURLForPort builds the probe URL for a service. host=127.0.0.1 in
// Phase 2 (Caddy routing happens in Phase 3). Path is "/" — services that
// don't serve "/" can be configured via PATCH /services later.
func healthURLForPort(port int) string {
	return "http://127.0.0.1:" + strconv.Itoa(port) + "/"
}
