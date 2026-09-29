package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// handleStatic serves the embedded Svelte app. Unknown paths get index.html so the
// client-side router can handle them.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "" {
		if info, err := fs.Stat(s.static, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				// Vite puts a content hash in these names.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.static, name)
			return
		}
	}
	index, err := fs.ReadFile(s.static, "index.html")
	if err != nil {
		http.Error(w, "the web UI isn't built into this binary (run npm run build in web/)", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(index)
}
