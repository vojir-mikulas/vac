package server

import (
	"encoding/json"
	"net/http"
	"time"
)

func New(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
