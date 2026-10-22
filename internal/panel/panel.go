// Package panel embeds the admin web panel's static assets directly into the Go binary —
// no separate build step, no Node/npm, no files to ship alongside the server.
package panel

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/index.html
var staticFiles embed.FS

// Handler serves the single-page admin panel. The page itself handles routing (login vs
// enrollment vs dashboard) client-side based on /admin/whoami, so every path just serves
// the same index.html.
func Handler() http.Handler {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic("panel: embedded static assets missing: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Single-file SPA: always serve index.html regardless of the requested path.
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}
