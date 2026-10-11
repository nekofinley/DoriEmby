package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"doriyamedia/internal/auth"
	"doriyamedia/internal/config"
	"doriyamedia/internal/db"
	"doriyamedia/internal/media"
	"doriyamedia/internal/metadata"
	"doriyamedia/internal/models"
	"doriyamedia/internal/scan"
)

type Server struct {
	Store      *db.Store
	Cfg        *config.Config
	ConfigPath string
	Scanner    *scan.Scanner
	Metadata   *metadata.Manager
	Engine     *media.Engine
	WebFS      fs.FS // embedded dist root
	DevWeb     string
}

func New(store *db.Store, cfg *config.Config, scanner *scan.Scanner, meta *metadata.Manager, engine *media.Engine, webFS fs.FS) *Server {
	return &Server{Store: store, Cfg: cfg, Scanner: scanner, Metadata: meta, Engine: engine, WebFS: webFS, DevWeb: cfg.DevWebDir}
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// ---------- auth ----------

const cookieName = "doriyamedia_token"

func (s *Server) currentUser(r *http.Request) *models.User {
	token := ""
	if c, err := r.Cookie(cookieName); err == nil {
		token = c.Value
	}
	if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
		token = ah[len("Bearer "):]
	}
	if token == "" {
		return nil
	}
	sess, err := s.Store.SessionByToken(token)
	if err != nil || time.Now().After(sess.ExpiresAt) {
		return nil
	}
	u, err := s.Store.UserByID(sess.UserID)
	if err != nil {
		return nil
	}
	return u
}

type authedHandler func(w http.ResponseWriter, r *http.Request, user *models.User)

func (s *Server) requireAuth(h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			fail(w, 401, "未登录")
			return
		}
		h(w, r, u)
	}
}

func (s *Server) requireAdmin(h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			fail(w, 401, "未登录")
			return
		}
		if u.Role != "admin" {
			fail(w, 403, "需要管理员权限")
			return
		}
		h(w, r, u)
	}
}

// ---------- signed URLs (for players that cannot send headers) ----------

func signPayload(fileID int64, exp int64) string {
	return fmt.Sprintf("%d|%d", fileID, exp)
}

func signedQuery(secret string, fileID int64, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	sig := auth.Sign(secret, signPayload(fileID, exp))
	return fmt.Sprintf("?exp=%d&sig=%s", exp, sig)
}

// sigOK validates a signed URL for fileID.
func (s *Server) sigOK(r *http.Request, fileID int64) bool {
	q := r.URL.Query()
	sig := q.Get("sig")
	exp := q.Get("exp")
	if sig == "" || exp == "" {
		return false
	}
	var expN int64
	if _, err := fmt.Sscanf(exp, "%d", &expN); err != nil {
		return false
	}
	if time.Now().Unix() > expN {
		return false
	}
	return auth.VerifySig(s.Cfg.Secret, signPayload(fileID, expN), sig)
}

// allowPlayerAuth: session OR valid signature (players cannot send cookies/headers on 302 targets).
func (s *Server) allowPlayerAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.currentUser(r) != nil {
			h(w, r)
			return
		}
		// extract file id from path segment
		id, ok := pathID(r)
		if ok && s.sigOK(r, id) {
			h(w, r)
			return
		}
		fail(w, 401, "签名无效或已过期")
	}
}

func pathID(r *http.Request) (int64, bool) {
	v := r.PathValue("id")
	if v == "" {
		return 0, false
	}
	var id int64
	if _, err := fmt.Sscanf(v, "%d", &id); err != nil {
		return 0, false
	}
	return id, true
}

// ---------- router ----------

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// auth / setup
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("GET /api/bootstrap", s.handleBootstrap)

	// system
	mux.HandleFunc("GET /api/system/info", s.requireAuth(s.handleSystemInfo))
	mux.HandleFunc("GET /api/config", s.requireAdmin(s.handleGetConfig))
	mux.HandleFunc("PUT /api/config", s.requireAdmin(s.handlePutConfig))
	mux.HandleFunc("POST /api/metadata/test", s.requireAdmin(s.handleMetadataTest))

	// sources
	mux.HandleFunc("GET /api/sources", s.requireAuth(s.handleListSources))
	mux.HandleFunc("POST /api/sources", s.requireAdmin(s.handleCreateSource))
	mux.HandleFunc("PUT /api/sources/{id}", s.requireAdmin(s.handleUpdateSource))
	mux.HandleFunc("DELETE /api/sources/{id}", s.requireAdmin(s.handleDeleteSource))
	mux.HandleFunc("POST /api/sources/test", s.requireAdmin(s.handleTestSource))

	// libraries
	mux.HandleFunc("GET /api/libraries", s.requireAuth(s.handleListLibraries))
	mux.HandleFunc("POST /api/libraries", s.requireAdmin(s.handleCreateLibrary))
	mux.HandleFunc("PUT /api/libraries/{id}", s.requireAdmin(s.handleUpdateLibrary))
	mux.HandleFunc("DELETE /api/libraries/{id}", s.requireAdmin(s.handleDeleteLibrary))
	mux.HandleFunc("POST /api/libraries/{id}/scan", s.requireAdmin(s.handleScanLibrary))
	mux.HandleFunc("POST /api/libraries/{id}/scan/stop", s.requireAdmin(s.handleStopScan))
	mux.HandleFunc("GET /api/scan/status", s.requireAuth(s.handleScanStatus))

	// items
	mux.HandleFunc("GET /api/home", s.requireAuth(s.handleHome))
	mux.HandleFunc("GET /api/items", s.requireAuth(s.handleListItems))
	mux.HandleFunc("GET /api/items/{id}", s.requireAuth(s.handleItemDetail))
	mux.HandleFunc("POST /api/items/{id}/refresh", s.requireAuth(s.handleItemRefresh))
	mux.HandleFunc("GET /api/img/{id}/{kind}", s.handleImage)

	// playback
	mux.HandleFunc("GET /api/play/{id}", s.requireAuth(s.handlePlay))
	mux.HandleFunc("GET /api/browse/{id}", s.requireAuth(s.handleBrowse))
	mux.HandleFunc("GET /api/browseplay/{id}", s.requireAuth(s.handleBrowsePlay))
	mux.HandleFunc("GET /api/stream/{id}", s.allowPlayerAuth(s.handleStream))
	mux.HandleFunc("HEAD /api/stream/{id}", s.allowPlayerAuth(s.handleStream))
	mux.HandleFunc("GET /api/hls/{id}/{rest...}", s.allowPlayerAuth(s.handleHLS))
	mux.HandleFunc("GET /api/subs/{id}/{sub}", s.allowPlayerAuth(s.handleSubtitle))

	// progress
	mux.HandleFunc("POST /api/progress", s.requireAuth(s.handleSaveProgress))
	mux.HandleFunc("GET /api/progress", s.requireAuth(s.handleListProgress))

	// static SPA
	mux.HandleFunc("/", s.serveStatic)

	return logMiddleware(mux)
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}
