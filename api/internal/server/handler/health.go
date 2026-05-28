package handler

import "net/http"

func Health(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
