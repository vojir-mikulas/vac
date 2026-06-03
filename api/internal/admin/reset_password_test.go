package admin

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestReadNewPassword(t *testing.T) {
	long := strings.Repeat("a", 73) // one byte over bcrypt's 72-byte ceiling

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "happy path returns the entered password",
			input: "correcthorse1\ncorrecthorse1\n",
			want:  "correcthorse1",
		},
		{
			name:  "trims a trailing CR from CRLF input",
			input: "correcthorse1\r\ncorrecthorse1\r\n",
			want:  "correcthorse1",
		},
		{
			name:    "too short is rejected before confirm",
			input:   "short\n",
			wantErr: "at least",
		},
		{
			name:    "over 72 bytes is rejected (bcrypt ceiling)",
			input:   long + "\n",
			wantErr: "at most",
		},
		{
			name:    "mismatch is rejected",
			input:   "correcthorse1\ncorrecthorse2\n",
			wantErr: "do not match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readNewPassword(strings.NewReader(tt.input), io.Discard)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got err %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got password %q, want %q", got, tt.want)
			}
		})
	}
}

// ResetPassword validates its arguments before touching config or the database,
// so these paths are exercisable without a DB.
func TestResetPassword_ArgValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "no username", args: nil, wantErr: "usage"},
		{name: "blank username", args: []string{"   "}, wantErr: "username is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ResetPassword(tt.args, strings.NewReader(""), io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got err %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// Guard: an *os.File-less reader must take the non-interactive line-read path
// (not the TTY path), so scripted recovery keeps working.
func TestReadNewPassword_NonInteractiveStdout(t *testing.T) {
	var out bytes.Buffer
	got, err := readNewPassword(strings.NewReader("correcthorse1\ncorrecthorse1\n"), &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "correcthorse1" {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(out.String(), "New password:") {
		t.Fatalf("prompt label not written to stdout: %q", out.String())
	}
}
