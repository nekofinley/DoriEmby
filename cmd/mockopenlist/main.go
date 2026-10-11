// Command mockopenlist is a minimal OpenList/AList v3 API mock for development
// and testing: /api/fs/list, /api/fs/get (raw_url → local http file) and range-
// capable static file serving.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	dir := flag.String("dir", ".", "media root directory")
	addr := flag.String("addr", "127.0.0.1:5699", "listen address")
	flag.Parse()
	root, _ := filepath.Abs(*dir)

	mux := http.NewServeMux()

	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		full := filepath.Join(root, filepath.FromSlash(path.Clean("/"+req.Path)))
		entries, err := os.ReadDir(full)
		resp := map[string]any{"code": 200, "message": "success"}
		if err != nil {
			resp["code"] = 500
			resp["message"] = err.Error()
		} else {
			type entry struct {
				Name     string `json:"name"`
				Size     int64  `json:"size"`
				IsDir    bool   `json:"is_dir"`
				Modified string `json:"modified"`
			}
			var content []entry
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					continue
				}
				content = append(content, entry{
					Name: e.Name(), Size: info.Size(), IsDir: e.IsDir(),
					Modified: info.ModTime().Format(time.RFC3339Nano),
				})
			}
			resp["data"] = map[string]any{"content": content, "total": len(content)}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/fs/get", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		clean := path.Clean("/" + req.Path)
		full := filepath.Join(root, filepath.FromSlash(clean))
		st, err := os.Stat(full)
		resp := map[string]any{"code": 200, "message": "success"}
		if err != nil {
			resp["code"] = 500
			resp["message"] = err.Error()
		} else {
			u := url.URL{Scheme: "http", Host: r.Host, Path: "/files" + strings.ReplaceAll(clean, "\\", "/")}
			resp["data"] = map[string]any{
				"name":    filepath.Base(clean),
				"size":    st.Size(),
				"is_dir":  st.IsDir(),
				"raw_url": u.String(),
				"header":  map[string]string{},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/files")
		http.ServeFile(w, r, filepath.Join(root, filepath.FromSlash(path.Clean(p))))
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_ = fs.WalkDir(os.DirFS(root), ".", func(p string, d fs.DirEntry, err error) error {
				if err == nil {
					fmt.Fprintln(w, p)
				}
				return nil
			})
			return
		}
		http.NotFound(w, r)
	})

	log.Printf("mock openlist serving %s at http://%s", root, *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
