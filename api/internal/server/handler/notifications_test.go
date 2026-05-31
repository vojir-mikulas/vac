package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeSender struct {
	count int
	err   error
	calls int
}

func (f *fakeSender) SendTest(_ context.Context) (int, error) {
	f.calls++
	return f.count, f.err
}

func TestNotificationHandlerReportsSentCount(t *testing.T) {
	t.Parallel()
	sender := &fakeSender{count: 2}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/notifications/test", nil)

	TestNotification(sender)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rr.Code)
	}
	if sender.calls != 1 {
		t.Errorf("sender.SendTest called %d times; want 1", sender.calls)
	}
	var body map[string]int
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["sent"] != 2 {
		t.Errorf("body[sent] = %d; want 2", body["sent"])
	}
}

func TestNotificationHandler400WhenNoChannels(t *testing.T) {
	t.Parallel()
	sender := &fakeSender{err: errors.New("no channels")}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/notifications/test", nil)

	TestNotification(sender)(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 when sender returns error", rr.Code)
	}
}
