// Package handler holds HTTP handlers and small helpers they share.
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// errorBody is the canonical error response shape. `error` is the human
// message, `code` is a stable machine token clients can branch on without
// pattern-matching the message.
type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// Error code tokens. Keep these stable — UI / CLI consumers branch on them.
const (
	CodeBadRequest         = "bad_request"
	CodeUnauthorized       = "unauthorized"
	CodeInvalidCredentials = "invalid_credentials"
	CodeForbidden          = "forbidden"
	CodeCSRFMismatch       = "csrf_mismatch"
	CodeNotFound           = "not_found"
	CodeConflict           = "conflict"
	CodeRateLimited        = "rate_limited"
	CodeInternal           = "internal_error"
	CodeServiceUnavailable = "service_unavailable"
)

// WriteJSON writes body as JSON with the given status. Encode errors are
// logged but do not affect the response — by that point the status line is
// already on the wire.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("write json", "err", err)
	}
}

// WriteError writes {"error": msg, "code": <derived>} with the given status.
// The code is derived from the status — use WriteErrorCode when you need to
// override (e.g. invalid_credentials vs. plain unauthorized).
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteErrorCode(w, status, defaultCodeForStatus(status), msg)
}

// WriteErrorCode writes a fully-specified error response.
func WriteErrorCode(w http.ResponseWriter, status int, code, msg string) {
	WriteJSON(w, status, errorBody{Error: msg, Code: code})
}

func defaultCodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeBadRequest
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeConflict
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusServiceUnavailable:
		return CodeServiceUnavailable
	default:
		return CodeInternal
	}
}
