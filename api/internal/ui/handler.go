package ui

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler serves the embedded UI with SPA fallback: any request for a path
// that doesn't exist in the embedded filesystem is served index.html so
// client-side routing works on refresh / deep links.
func Handler() http.Handler {
	fileServer := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if clean == "" || clean == "." {
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(files, clean); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic("ui: " + err.Error())
	}
	return sub
}
