package deploy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/deploy"
)

func TestCheckWithRetry_PassesOnFirstTry(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := deploy.CheckWithRetry(context.Background(), deploy.HTTPChecker{}, srv.URL+"/", 3, 5*time.Second); err != nil {
		t.Errorf("want pass, got %v", err)
	}
}

func TestCheckWithRetry_RetriesUntilSuccess(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			http.Error(w, "warmup", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := deploy.CheckWithRetry(context.Background(), deploy.HTTPChecker{}, srv.URL+"/", 5, 10*time.Second); err != nil {
		t.Errorf("want pass after retries, got %v", err)
	}
	if attempts.Load() < 3 {
		t.Errorf("retries did not run: attempts = %d", attempts.Load())
	}
}

func TestCheckWithRetry_FailsAfterExhaust(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	err := deploy.CheckWithRetry(context.Background(), deploy.HTTPChecker{}, srv.URL+"/", 2, 3*time.Second)
	if !errors.Is(err, deploy.ErrHealthCheckFailed) {
		t.Errorf("want ErrHealthCheckFailed, got %v", err)
	}
}

type fakeChecker struct{ statuses []int }

func (f *fakeChecker) Check(_ context.Context, _ string) (int, error) {
	if len(f.statuses) == 0 {
		return 500, nil
	}
	s := f.statuses[0]
	f.statuses = f.statuses[1:]
	return s, nil
}

func TestCheckWithRetry_FakeChecker(t *testing.T) {
	t.Parallel()
	c := &fakeChecker{statuses: []int{503, 503, 200}}
	if err := deploy.CheckWithRetry(context.Background(), c, "http://x.invalid/", 5, 10*time.Second); err != nil {
		t.Errorf("want pass on third attempt, got %v", err)
	}
}
