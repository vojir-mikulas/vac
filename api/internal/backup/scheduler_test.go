package backup

import (
	"testing"
	"time"
)

func intp(n int) *int { return &n }

func TestNextOccurrence_Daily(t *testing.T) {
	loc := time.UTC
	// Monday 2026-06-01 01:00 — before the 03:00 slot, so today.
	now := time.Date(2026, 6, 1, 1, 0, 0, 0, loc)
	got := nextOccurrence(now, "daily", 3, nil)
	want := time.Date(2026, 6, 1, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("before slot: got %v, want %v", got, want)
	}

	// 04:00 — past the 03:00 slot, so tomorrow.
	now = time.Date(2026, 6, 1, 4, 0, 0, 0, loc)
	got = nextOccurrence(now, "daily", 3, nil)
	want = time.Date(2026, 6, 2, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("after slot: got %v, want %v", got, want)
	}

	// Exactly at the slot counts as "not after now" → next day.
	now = time.Date(2026, 6, 1, 3, 0, 0, 0, loc)
	got = nextOccurrence(now, "daily", 3, nil)
	want = time.Date(2026, 6, 2, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("at slot: got %v, want %v", got, want)
	}
}

func TestNextOccurrence_Weekly(t *testing.T) {
	loc := time.UTC
	// 2026-06-01 is a Monday (weekday 1). Target Wednesday (3) at 02:00.
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, loc)
	got := nextOccurrence(now, "weekly", 2, intp(3))
	want := time.Date(2026, 6, 3, 2, 0, 0, 0, loc) // Wed
	if !got.Equal(want) {
		t.Errorf("future weekday: got %v, want %v", got, want)
	}

	// Same weekday but the slot already passed today → next week.
	now = time.Date(2026, 6, 3, 5, 0, 0, 0, loc) // Wednesday 05:00
	got = nextOccurrence(now, "weekly", 2, intp(3))
	want = time.Date(2026, 6, 10, 2, 0, 0, 0, loc) // following Wed
	if !got.Equal(want) {
		t.Errorf("same weekday past slot: got %v, want %v", got, want)
	}

	// Same weekday, slot still ahead today → today.
	now = time.Date(2026, 6, 3, 1, 0, 0, 0, loc)
	got = nextOccurrence(now, "weekly", 2, intp(3))
	want = time.Date(2026, 6, 3, 2, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("same weekday before slot: got %v, want %v", got, want)
	}
}

func TestNextOccurrence_WeeklyNilDayFallsBackToDaily(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 1, 1, 0, 0, 0, loc)
	got := nextOccurrence(now, "weekly", 3, nil)
	want := time.Date(2026, 6, 1, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
