// Package web serves the embedded Ember SPA.
//
// The built Ember app is copied into web/dist by the Makefile (`make build`)
// and embedded into the binary here. Until the SPA has been built, dist holds
// only its .gitignore, so requests fall through to a "not built yet" notice.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded SPA. Real assets are
// served directly; any other path falls back to index.html so the Ember router
// can resolve client-side (history API) routes.
func Handler() (http.Handler, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	return &spaHandler{
		fsys:   sub,
		assets: http.FileServer(http.FS(sub)),
	}, nil
}

type spaHandler struct {
	fsys   fs.FS
	assets http.Handler
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}

	// Serve the file if it exists in the embedded build.
	if name != "index.html" {
		if _, err := fs.Stat(h.fsys, name); err == nil {
			h.assets.ServeHTTP(w, r)
			return
		}
	}

	// Otherwise fall back to index.html for the SPA router.
	if _, err := fs.Stat(h.fsys, "index.html"); err != nil {
		http.Error(w, "SPA not built — run `make build` to compile and embed the Ember app", http.StatusServiceUnavailable)
		return
	}
	http.ServeFileFS(w, r, h.fsys, "index.html")
}
