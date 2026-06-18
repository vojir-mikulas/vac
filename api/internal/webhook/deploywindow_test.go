package webhook

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

func TestAllows(t *testing.T) {
	// 2026-06-18 is a Thursday (weekday 4).
	tests := []struct {
		name    string
		now     string
		windows []Window
		want    bool
	}{
		{
			name:    "no windows always allowed",
			now:     "2026-06-18T03:00:00Z",
			windows: nil,
			want:    true,
		},
		{
			name:    "inside a same-day UTC window",
			now:     "2026-06-18T10:30:00Z",
			windows: []Window{{Start: "09:00", End: "17:00", TZ: "UTC"}},
			want:    true,
		},
		{
			name:    "before a same-day window",
			now:     "2026-06-18T08:59:00Z",
			windows: []Window{{Start: "09:00", End: "17:00"}},
			want:    false,
		},
		{
			name:    "end is exclusive",
			now:     "2026-06-18T17:00:00Z",
			windows: []Window{{Start: "09:00", End: "17:00"}},
			want:    false,
		},
		{
			name:    "matching weekday",
			now:     "2026-06-18T10:00:00Z", // Thursday
			windows: []Window{{Days: []int{4}, Start: "09:00", End: "17:00"}},
			want:    true,
		},
		{
			name:    "non-matching weekday",
			now:     "2026-06-18T10:00:00Z", // Thursday
			windows: []Window{{Days: []int{1, 2, 3}, Start: "09:00", End: "17:00"}},
			want:    false,
		},
		{
			name:    "overnight window after start",
			now:     "2026-06-18T23:00:00Z",
			windows: []Window{{Start: "22:00", End: "06:00"}},
			want:    true,
		},
		{
			name:    "overnight window early morning tail",
			now:     "2026-06-18T05:00:00Z",
			windows: []Window{{Start: "22:00", End: "06:00"}},
			want:    true,
		},
		{
			name:    "overnight window midday gap",
			now:     "2026-06-18T12:00:00Z",
			windows: []Window{{Start: "22:00", End: "06:00"}},
			want:    false,
		},
		{
			name: "timezone shifts the window",
			// June → EDT (UTC-4), so 14:00 UTC is 10:00 in America/New_York.
			now:     "2026-06-18T14:00:00Z",
			windows: []Window{{Start: "09:00", End: "11:00", TZ: "America/New_York"}},
			want:    true,
		},
		{
			name:    "timezone puts it outside the window",
			now:     "2026-06-18T14:00:00Z", // 10:00 EDT
			windows: []Window{{Start: "11:00", End: "12:00", TZ: "America/New_York"}},
			want:    false,
		},
		{
			name:    "any of several windows matches",
			now:     "2026-06-18T20:00:00Z",
			windows: []Window{{Start: "09:00", End: "12:00"}, {Start: "19:00", End: "21:00"}},
			want:    true,
		},
		{
			name:    "malformed window never matches",
			now:     "2026-06-18T10:00:00Z",
			windows: []Window{{Start: "nope", End: "17:00"}},
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Allows(mustTime(t, tt.now), tt.windows); got != tt.want {
				t.Errorf("Allows() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseWindows(t *testing.T) {
	ws, err := ParseWindows(nil)
	if err != nil || ws != nil {
		t.Fatalf("nil → %v, %v", ws, err)
	}
	ws, err = ParseWindows([]byte("null"))
	if err != nil || ws != nil {
		t.Fatalf("null → %v, %v", ws, err)
	}
	ws, err = ParseWindows([]byte(`[{"days":[1,2],"start":"09:00","end":"17:00","tz":"UTC"}]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ws) != 1 || ws[0].Start != "09:00" || len(ws[0].Days) != 2 {
		t.Fatalf("unexpected windows: %+v", ws)
	}
	if _, err := ParseWindows([]byte(`not json`)); err == nil {
		t.Fatal("expected parse error for invalid json")
	}
}

func TestValidateWindows(t *testing.T) {
	tests := []struct {
		name    string
		windows []Window
		wantErr bool
	}{
		{"valid", []Window{{Days: []int{0, 6}, Start: "09:00", End: "17:00", TZ: "UTC"}}, false},
		{"empty schedule", nil, false},
		{"bad start", []Window{{Start: "25:00", End: "17:00"}}, true},
		{"bad end", []Window{{Start: "09:00", End: "9"}}, true},
		{"equal start/end", []Window{{Start: "09:00", End: "09:00"}}, true},
		{"bad weekday", []Window{{Days: []int{7}, Start: "09:00", End: "10:00"}}, true},
		{"bad tz", []Window{{Start: "09:00", End: "10:00", TZ: "Mars/Phobos"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWindows(tt.windows)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateWindows() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
