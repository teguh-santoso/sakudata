package ui

import (
	"embed"
	"net/http"
	"regexp"
	"strings"
)

//go:embed assets
var content embed.FS

// Versioned asset paths (assets/v3/...) keep CDN/browser caches from serving
// stale modules after a deploy. The version segment is stripped before lookup.
var versionPrefix = regexp.MustCompile(`^/assets/v[0-9]+/`)

// Handler serves the admin SPA. It is mounted under "/_/" with the prefix
// stripped, so requests arrive as "/" and "/assets/...".
func Handler() http.Handler {
	files := http.FileServer(http.FS(content))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The admin UI is small and changes with the binary; never cache it.
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		w.Header().Set("CDN-Cache-Control", "no-store")
		w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")

		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			b, err := content.ReadFile("assets/index.html")
			if err != nil {
				http.Error(w, "admin UI unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			if versionPrefix.MatchString(r.URL.Path) {
				r = r.Clone(r.Context())
				r.URL.Path = versionPrefix.ReplaceAllString(r.URL.Path, "/assets/")
			}
			files.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}
