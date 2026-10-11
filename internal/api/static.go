package api

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// serveStatic serves the embedded SPA with index.html fallback.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, 404, "not found")
		return
	}

	if s.DevWeb != "" {
		if st, err := os.Stat(s.DevWeb); err == nil && st.IsDir() {
			full := filepath.Join(s.DevWeb, filepath.Clean("/"+r.URL.Path))
			if st2, err := os.Stat(full); err == nil && !st2.IsDir() {
				http.ServeFile(w, r, full)
				return
			}
			http.ServeFile(w, r, filepath.Join(s.DevWeb, "index.html"))
			return
		}
	}

	if s.WebFS == nil {
		http.NotFound(w, r)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	if f, err := s.WebFS.Open(p); err == nil {
		f.Close()
		http.FileServerFS(s.WebFS).ServeHTTP(w, r)
		return
	}
	// SPA fallback
	index, err := fs.ReadFile(s.WebFS, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}
