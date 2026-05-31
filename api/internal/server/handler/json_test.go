package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultCodeForStatus(t *testing.T) {
	t.Parallel()
	cases := map[int]string{
		http.StatusBadRequest:          CodeBadRequest,
		http.StatusUnauthorized:        CodeUnauthorized,
		http.StatusForbidden:           CodeForbidden,
		http.StatusNotFound:            CodeNotFound,
		http.StatusConflict:            CodeConflict,
		http.StatusTooManyRequests:     CodeRateLimited,
		http.StatusServiceUnavailable:  CodeServiceUnavailable,
		http.StatusInternalServerError: CodeInternal,
		http.StatusTeapot:              CodeInternal, // unmapped statuses fall to "internal"
	}
	for status, want := range cases {
		if got := defaultCodeForStatus(status); got != want {
			t.Errorf("defaultCodeForStatus(%d) = %q; want %q", status, got, want)
		}
	}
}

func TestWriteErrorShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	WriteError(rr, http.StatusNotFound, "missing")

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q; want application/json", ct)
	}

	var body errorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error != "missing" || body.Code != CodeNotFound {
		t.Errorf("body = %+v; want {Error: \"missing\", Code: %q}", body, CodeNotFound)
	}
}

func TestWriteErrorCodeOverridesDefault(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	WriteErrorCode(rr, http.StatusUnauthorized, CodeInvalidCredentials, "bad password")

	var body errorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != CodeInvalidCredentials {
		t.Errorf("code = %q; want %q (caller should be able to override the default)", body.Code, CodeInvalidCredentials)
	}
}

func TestWriteJSONEncodesBody(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	WriteJSON(rr, http.StatusCreated, map[string]int{"created": 1})

	if rr.Code != http.StatusCreated {
		t.Errorf("status = %d; want 201", rr.Code)
	}
	var got map[string]int
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["created"] != 1 {
		t.Errorf("body = %+v; want {created: 1}", got)
	}
}
