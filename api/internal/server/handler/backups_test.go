package handler

import (
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/backup"
)

func TestBackupConfigReq_Validate(t *testing.T) {
	dow := func(n int) *int { return &n }

	cases := []struct {
		name    string
		req     backupConfigReq
		wantErr bool
	}{
		{
			name:    "valid daily local",
			req:     backupConfigReq{Command: "pg_dump", Frequency: "daily", HourOfDay: 3, Destination: "local"},
			wantErr: false,
		},
		{
			name:    "valid weekly s3",
			req:     backupConfigReq{Command: "pg_dump", Frequency: "weekly", HourOfDay: 2, DayOfWeek: dow(3), Destination: "s3", S3: &backup.S3Config{Endpoint: "e", Bucket: "b", AccessKey: "k", SecretKey: "s"}},
			wantErr: false,
		},
		{
			name:    "missing command",
			req:     backupConfigReq{Frequency: "daily", Destination: "local"},
			wantErr: true,
		},
		{
			name:    "bad frequency",
			req:     backupConfigReq{Command: "x", Frequency: "hourly", Destination: "local"},
			wantErr: true,
		},
		{
			name:    "weekly without day",
			req:     backupConfigReq{Command: "x", Frequency: "weekly", Destination: "local"},
			wantErr: true,
		},
		{
			name:    "weekly day out of range",
			req:     backupConfigReq{Command: "x", Frequency: "weekly", DayOfWeek: dow(9), Destination: "local"},
			wantErr: true,
		},
		{
			name:    "hour out of range",
			req:     backupConfigReq{Command: "x", Frequency: "daily", HourOfDay: 99, Destination: "local"},
			wantErr: true,
		},
		{
			name:    "s3 without creds",
			req:     backupConfigReq{Command: "x", Frequency: "daily", Destination: "s3"},
			wantErr: true,
		},
		{
			name:    "unknown destination",
			req:     backupConfigReq{Command: "x", Frequency: "daily", Destination: "ftp"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			msg := req.validate()
			if tc.wantErr && msg == "" {
				t.Errorf("expected validation error, got none")
			}
			if !tc.wantErr && msg != "" {
				t.Errorf("unexpected validation error: %s", msg)
			}
		})
	}
}

func TestBackupConfigReq_DefaultsKeepCount(t *testing.T) {
	req := backupConfigReq{Command: "x", Frequency: "daily", Destination: "local"}
	_ = req.validate()
	if req.KeepCount != 7 {
		t.Errorf("keep_count default = %d, want 7", req.KeepCount)
	}
}

func TestFilenameFromKey(t *testing.T) {
	if got := filenameFromKey("blog/db/20260601T030000Z.dump"); got != "20260601T030000Z.dump" {
		t.Errorf("filenameFromKey = %q", got)
	}
	if got := filenameFromKey("flat.dump"); got != "flat.dump" {
		t.Errorf("filenameFromKey flat = %q", got)
	}
}
