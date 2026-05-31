package ui

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler serves the embedded UI with SPA fallback: any request for a path
// that doesn't exist in the embedded filesystem is served index.html so
// client-side routing works on refresh / deep links.
//
// Returns an error if the embedded filesystem can't be sub-rooted at the
// build-time directory ("dist" with -tags embedui, "placeholder" otherwise).
// In practice this only fails if the embed wiring is broken — but we surface
// it to main.go so the operator sees a clean log line, not a panic stack.
func Handler() (http.Handler, error) {
	sub, err := fs.Sub(rawFS, subDir)
	if err != nil {
		return nil, fmt.Errorf("ui: sub %q: %w", subDir, err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if clean == "" || clean == "." {
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(sub, clean); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	}), nil
}
