package jobs

import (
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

func intp(n int) *int { return &n }

func daily(hour int) store.ScheduledJob {
	return store.ScheduledJob{Frequency: "daily", HourOfDay: hour}
}

func TestNextOccurrence_Daily(t *testing.T) {
	loc := time.UTC
	// Monday 2026-06-01 01:00 — before the 03:00 slot, so today.
	now := time.Date(2026, 6, 1, 1, 0, 0, 0, loc)
	got := nextOccurrence(now, daily(3))
	want := time.Date(2026, 6, 1, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("before slot: got %v, want %v", got, want)
	}

	// 04:00 — past the 03:00 slot, so tomorrow.
	now = time.Date(2026, 6, 1, 4, 0, 0, 0, loc)
	got = nextOccurrence(now, daily(3))
	want = time.Date(2026, 6, 2, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("after slot: got %v, want %v", got, want)
	}

	// Exactly at the slot counts as "not after now" → next day.
	now = time.Date(2026, 6, 1, 3, 0, 0, 0, loc)
	got = nextOccurrence(now, daily(3))
	want = time.Date(2026, 6, 2, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("at slot: got %v, want %v", got, want)
	}
}

func TestNextOccurrence_Weekly(t *testing.T) {
	loc := time.UTC
	weekly := func(hour, dow int) store.ScheduledJob {
		return store.ScheduledJob{Frequency: "weekly", HourOfDay: hour, DayOfWeek: intp(dow)}
	}
	// 2026-06-01 is a Monday (weekday 1). Target Wednesday (3) at 02:00.
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, loc)
	got := nextOccurrence(now, weekly(2, 3))
	want := time.Date(2026, 6, 3, 2, 0, 0, 0, loc) // Wed
	if !got.Equal(want) {
		t.Errorf("future weekday: got %v, want %v", got, want)
	}

	// Same weekday but the slot already passed today → next week.
	now = time.Date(2026, 6, 3, 5, 0, 0, 0, loc) // Wednesday 05:00
	got = nextOccurrence(now, weekly(2, 3))
	want = time.Date(2026, 6, 10, 2, 0, 0, 0, loc) // following Wed
	if !got.Equal(want) {
		t.Errorf("same weekday past slot: got %v, want %v", got, want)
	}

	// Same weekday, slot still ahead today → today.
	now = time.Date(2026, 6, 3, 1, 0, 0, 0, loc)
	got = nextOccurrence(now, weekly(2, 3))
	want = time.Date(2026, 6, 3, 2, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("same weekday before slot: got %v, want %v", got, want)
	}
}

func TestNextOccurrence_WeeklyNilDayFallsBackToDaily(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 1, 1, 0, 0, 0, loc)
	got := nextOccurrence(now, store.ScheduledJob{Frequency: "weekly", HourOfDay: 3})
	want := time.Date(2026, 6, 1, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNextOccurrence_Interval(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, loc)

	// Never-run job: anchored on now, so the first slot is now + interval.
	job := store.ScheduledJob{Frequency: "interval", IntervalMinutes: intp(10)}
	got := nextOccurrence(now, job)
	want := now.Add(10 * time.Minute)
	if !got.Equal(want) {
		t.Errorf("never-run: got %v, want %v", got, want)
	}

	// Anchored on a recent last_run, the next slot is last_run + interval.
	last := now.Add(-3 * time.Minute)
	job.LastRun = &last
	got = nextOccurrence(now, job)
	want = last.Add(10 * time.Minute)
	if !got.Equal(want) {
		t.Errorf("recent last_run: got %v, want %v", got, want)
	}

	// A slot that passed during downtime is not backfilled — it jumps to the
	// first future slot, preserving the cadence offset.
	last = now.Add(-25 * time.Minute) // 2.5 intervals ago
	job.LastRun = &last
	got = nextOccurrence(now, job)
	want = now.Add(5 * time.Minute) // last + 3*10m = now + 5m
	if !got.Equal(want) {
		t.Errorf("missed slots: got %v, want %v", got, want)
	}
}
