package webhook

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Deploy windows (docs/plans/maintenance-mode-and-deploy-gates.md, Phase 3).
// An app may restrict push-to-deploy to one or more time windows; a push that
// arrives outside every window is parked as a `scheduled` deployment that a
// sweeper releases when a window opens. Empty / nil windows mean "always
// allowed" — the default.

// Window is one allowed deploy window. Days are weekday numbers (0=Sunday …
// 6=Saturday, matching time.Weekday); an empty Days means every day. Start/End
// are "HH:MM" 24-hour times in the window's TZ (an IANA name; empty = UTC). A
// window whose End is before its Start wraps past midnight (e.g. 22:00–06:00),
// with Days applying to the day the window opens.
type Window struct {
	Days  []int  `json:"days"`
	Start string `json:"start"`
	End   string `json:"end"`
	TZ    string `json:"tz"`
}

// ParseWindows decodes the JSONB deploy_window column. A nil/empty value yields
// no windows (always allowed).
func ParseWindows(raw []byte) ([]Window, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var ws []Window
	if err := json.Unmarshal(raw, &ws); err != nil {
		return nil, fmt.Errorf("deploy window: %w", err)
	}
	return ws, nil
}

// Allows reports whether a deploy is permitted at `now`. With no windows it is
// always allowed; otherwise the deploy is allowed if `now` falls inside any one
// window.
func Allows(now time.Time, windows []Window) bool {
	if len(windows) == 0 {
		return true
	}
	for _, w := range windows {
		if w.contains(now) {
			return true
		}
	}
	return false
}

// contains reports whether `now` is inside this single window.
func (w Window) contains(now time.Time) bool {
	loc := time.UTC
	if w.TZ != "" {
		if l, err := time.LoadLocation(w.TZ); err == nil {
			loc = l
		}
	}
	t := now.In(loc)
	start, okStart := parseHM(w.Start)
	end, okEnd := parseHM(w.End)
	if !okStart || !okEnd || start == end {
		// A malformed or zero-length window never matches (fail closed — the
		// handler validates on write, so this only guards corrupt data).
		return false
	}
	mins := t.Hour()*60 + t.Minute()
	today := int(t.Weekday())
	if start < end {
		// Same-day window.
		return dayMatches(w.Days, today) && mins >= start && mins < end
	}
	// Overnight window wraps midnight. The early-morning tail belongs to the
	// window that opened the previous day.
	if mins >= start {
		return dayMatches(w.Days, today)
	}
	if mins < end {
		prevDay := int(t.AddDate(0, 0, -1).Weekday())
		return dayMatches(w.Days, prevDay)
	}
	return false
}

// dayMatches reports whether `day` is allowed: an empty day list means every day.
func dayMatches(days []int, day int) bool {
	if len(days) == 0 {
		return true
	}
	for _, d := range days {
		if d == day {
			return true
		}
	}
	return false
}

// parseHM parses "HH:MM" into minutes-since-midnight. Returns ok=false for any
// malformed value or out-of-range component.
func parseHM(s string) (int, bool) {
	h, m, found := strings.Cut(s, ":")
	if !found {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// ValidateWindows checks a deploy-window schedule before it is stored: each
// window needs a parseable Start/End that aren't equal, weekday numbers in
// [0,6], and a loadable TZ. Returns a human-readable error for the first
// problem, or nil when the schedule is valid (including an empty schedule).
func ValidateWindows(windows []Window) error {
	for i, w := range windows {
		if _, ok := parseHM(w.Start); !ok {
			return fmt.Errorf("window %d: invalid start time %q (want HH:MM)", i+1, w.Start)
		}
		if _, ok := parseHM(w.End); !ok {
			return fmt.Errorf("window %d: invalid end time %q (want HH:MM)", i+1, w.End)
		}
		if w.Start == w.End {
			return fmt.Errorf("window %d: start and end are equal", i+1)
		}
		for _, d := range w.Days {
			if d < 0 || d > 6 {
				return fmt.Errorf("window %d: invalid weekday %d (want 0-6)", i+1, d)
			}
		}
		if w.TZ != "" {
			if _, err := time.LoadLocation(w.TZ); err != nil {
				return fmt.Errorf("window %d: unknown timezone %q", i+1, w.TZ)
			}
		}
	}
	return nil
}
