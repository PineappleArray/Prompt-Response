// Package web serves the embedded observability dashboard.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed assets/*
var assets embed.FS

// Handler serves the dashboard single-page app under the /dashboard/ prefix.
func Handler() http.Handler {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic("web: embedded assets missing: " + err.Error())
	}

	files := http.FileServer(http.FS(sub))
	return http.StripPrefix("/dashboard/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// MVP: keep the SPA shell and script uncached so refreshes pick up changes.
		if p := r.URL.Path; p == "" || p == "/" || strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".js") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	}))
}
