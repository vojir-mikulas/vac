// Package handler holds HTTP handlers and small helpers they share.
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
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

// WriteError writes {"error": msg} with the given status.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}
