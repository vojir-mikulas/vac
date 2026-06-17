package handler

import (
	"context"
	"net/http"

	"github.com/vojir-mikulas/vac/api/internal/selfupdate"
)

// UpdateChecker resolves the latest released VAC version. *selfupdate.Checker
// satisfies it.
type UpdateChecker interface {
	Check(ctx context.Context) selfupdate.Result
}

// UpdateCheck reports whether a newer VAC release is available, for the Instance
// settings Version card. Best-effort and cached server-side: a failed upstream
// check returns 200 with an `error` field (and update_available=false) rather
// than failing, so the card degrades to "couldn't check" instead of breaking.
//
// GET /api/instance/update-check
func UpdateCheck(c UpdateChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, c.Check(r.Context()))
	}
}
