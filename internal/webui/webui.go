// Package webui serves the design system and JavaScript helpers shared by the
// Master and Agent web consoles. The assets are embedded; there is no
// frontend build step.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed assets
var assets embed.FS

// Register serves the shared assets below /ui/ (e.g. /ui/sg.css).
func Register(mux *http.ServeMux) {
	mux.Handle("GET /ui/{file}", Files(mustSub(assets, "assets")))
}

// Files serves the files of fsys by base name with an explicit content type
// and no-store caching, so a console upgrade never runs stale JavaScript.
func Files(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(r.PathValue("file"))
		if name == "" || name == "." || strings.HasPrefix(name, ".") {
			http.NotFound(w, r)
			return
		}
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ContentType(name))
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		_, _ = w.Write(b)
	})
}

// ContentType returns the MIME type for the asset types used by the consoles.
func ContentType(name string) string {
	switch path.Ext(name) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

func mustSub(f fs.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}
