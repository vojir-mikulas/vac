package jobs

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// fakeStore is an in-memory EngineStore for the engine tests.
type fakeStore struct {
	app        store.App
	service    store.Service
	serviceErr error

	finishedStatus string
	finishedExit   *int
	finishedOutput *string
	finishedErr    *string
	scheduleRolled bool
}

func (f *fakeStore) GetApp(context.Context, string) (store.App, error) { return f.app, nil }
func (f *fakeStore) GetService(context.Context, string, string) (store.Service, error) {
	return f.service, f.serviceErr
}

func (f *fakeStore) CreateJobRun(context.Context, string) (store.JobRun, error) {
	return store.JobRun{ID: "run-1"}, nil
}

func (f *fakeStore) FinishJobRun(_ context.Context, _ string, status string, exitCode *int, output, errMsg *string) error {
	f.finishedStatus = status
	f.finishedExit = exitCode
	f.finishedOutput = output
	f.finishedErr = errMsg
	return nil
}

func (f *fakeStore) UpdateJobSchedule(context.Context, string, time.Time, time.Time) error {
	f.scheduleRolled = true
	return nil
}

// fakeExec is a programmable ExecRunner.
type fakeExec struct {
	output string
	err    error
	block  bool // when true, block until ctx is cancelled, then return ctx.Err()
}

func (f *fakeExec) Exec(ctx context.Context, _ string, _ []string, out io.Writer) error {
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if f.output != "" {
		_, _ = io.WriteString(out, f.output)
	}
	return f.err
}

type fakeNotifier struct{ calls int }

func (f *fakeNotifier) JobFailed(string, string, string, string) { f.calls++ }

func cid(s string) *string { return &s }

func baseJob() store.ScheduledJob {
	return store.ScheduledJob{
		ID: "job-1", AppID: "app-1", Name: "cleanup", ServiceName: "web",
		Command: "echo hi", Frequency: "daily", HourOfDay: 3, TimeoutSeconds: 60,
	}
}

func newEngine(st *fakeStore, ex *fakeExec, n Notifier) *Engine {
	st.app = store.App{ID: "app-1", Name: "blog", Slug: "blog"}
	st.service = store.Service{ContainerID: cid("container-1")}
	return NewEngine(st, ex, n, nil)
}

func TestRunOnce_Success(t *testing.T) {
	st := &fakeStore{}
	n := &fakeNotifier{}
	e := newEngine(st, &fakeExec{output: "done\n"}, n)

	if err := e.RunOnce(context.Background(), baseJob()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if st.finishedStatus != "success" {
		t.Errorf("status = %q, want success", st.finishedStatus)
	}
	if st.finishedExit == nil || *st.finishedExit != 0 {
		t.Errorf("exit = %v, want 0", st.finishedExit)
	}
	if st.finishedOutput == nil || *st.finishedOutput != "done\n" {
		t.Errorf("output = %v, want %q", st.finishedOutput, "done\n")
	}
	if n.calls != 0 {
		t.Errorf("notifier fired %d times on success", n.calls)
	}
	if !st.scheduleRolled {
		t.Error("schedule not rolled forward")
	}
}

func TestRunOnce_NonZeroExitRecordsFailed(t *testing.T) {
	st := &fakeStore{}
	n := &fakeNotifier{}
	// A real non-zero exit gives us a genuine *exec.ExitError to unwrap the code.
	exitErr := exec.Command("sh", "-c", "exit 3").Run()
	e := newEngine(st, &fakeExec{output: "boom\n", err: exitErr}, n)

	if err := e.RunOnce(context.Background(), baseJob()); err == nil {
		t.Fatal("RunOnce: expected error")
	}
	if st.finishedStatus != "failed" {
		t.Errorf("status = %q, want failed", st.finishedStatus)
	}
	if st.finishedExit == nil || *st.finishedExit != 3 {
		t.Errorf("exit = %v, want 3", st.finishedExit)
	}
	if st.finishedOutput == nil || *st.finishedOutput != "boom\n" {
		t.Errorf("output = %v, want stderr tail", st.finishedOutput)
	}
	if n.calls != 1 {
		t.Errorf("notifier fired %d times, want 1", n.calls)
	}
}

func TestRunOnce_NoRunningContainer(t *testing.T) {
	st := &fakeStore{}
	n := &fakeNotifier{}
	e := newEngine(st, &fakeExec{}, n)
	st.service = store.Service{ContainerID: nil} // stopped

	err := e.RunOnce(context.Background(), baseJob())
	if err == nil || !strings.Contains(err.Error(), "no running container") {
		t.Fatalf("err = %v, want no-running-container", err)
	}
	if st.finishedStatus != "failed" {
		t.Errorf("status = %q, want failed", st.finishedStatus)
	}
	if st.finishedExit != nil {
		t.Errorf("exit = %v, want nil", st.finishedExit)
	}
	if n.calls != 1 {
		t.Errorf("notifier fired %d times, want 1", n.calls)
	}
}

func TestRunOnce_TimeoutRecordsTimeout(t *testing.T) {
	st := &fakeStore{}
	n := &fakeNotifier{}
	job := baseJob()
	job.TimeoutSeconds = 1 // the engine caps the run at 1s; the fake exec blocks past it
	e := newEngine(st, &fakeExec{block: true}, n)

	done := make(chan error, 1)
	go func() { done <- e.RunOnce(context.Background(), job) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("RunOnce: expected timeout error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunOnce did not return within 5s")
	}
	if st.finishedStatus != "timeout" {
		t.Errorf("status = %q, want timeout", st.finishedStatus)
	}
	if n.calls != 1 {
		t.Errorf("notifier fired %d times, want 1", n.calls)
	}
}

func TestCappedBuffer_KeepsTail(t *testing.T) {
	b := &cappedBuffer{cap: 8}
	_, _ = io.WriteString(b, "abcdefghij") // 10 bytes, cap 8
	if got := b.String(); got != "cdefghij" {
		t.Errorf("single overflow write: got %q, want %q", got, "cdefghij")
	}

	b = &cappedBuffer{cap: 8}
	_, _ = io.WriteString(b, "abcde")
	_, _ = io.WriteString(b, "fghij") // total 10, keep last 8
	if got := b.String(); got != "cdefghij" {
		t.Errorf("incremental overflow: got %q, want %q", got, "cdefghij")
	}
}
