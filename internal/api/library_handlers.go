package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"doriya/internal/models"
	"doriya/internal/source"
)

// ---------- sources ----------

type sourceDTO struct {
	models.Source
	Config models.SourceConfig `json:"config"`
}

func toSourceDTO(src models.Source) sourceDTO {
	var cfg models.SourceConfig
	_ = jsonUnmarshal(src.Config, &cfg)
	return sourceDTO{Source: src, Config: cfg}
}

func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request, user *models.User) {
	srcs, err := s.Store.Sources()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	out := make([]sourceDTO, 0, len(srcs))
	for _, src := range srcs {
		out = append(out, toSourceDTO(src))
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleCreateSource(w http.ResponseWriter, r *http.Request, user *models.User) {
	var req struct {
		Name   string              `json:"name"`
		Type   string              `json:"type"`
		Config models.SourceConfig `json:"config"`
	}
	if err := readBody(r, &req); err != nil || req.Name == "" {
		fail(w, 400, "名称不能为空")
		return
	}
	if req.Type != "local" && req.Type != "webdav" && req.Type != "openlist" {
		fail(w, 400, "类型必须是 local/webdav/openlist")
		return
	}
	raw, _ := jsonMarshal(req.Config)
	src := &models.Source{Name: req.Name, Type: req.Type, Config: string(raw)}
	if err := s.Store.CreateSource(src); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, toSourceDTO(*src))
}

func (s *Server) handleUpdateSource(w http.ResponseWriter, r *http.Request, user *models.User) {
	id, _ := pathID(r)
	src, err := s.Store.SourceByID(id)
	if err != nil {
		fail(w, 404, "源不存在")
		return
	}
	var req struct {
		Name   string              `json:"name"`
		Config models.SourceConfig `json:"config"`
	}
	if err := readBody(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.Name != "" {
		src.Name = req.Name
	}
	raw, _ := jsonMarshal(req.Config)
	src.Config = string(raw)
	if err := s.Store.UpdateSource(src); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, toSourceDTO(*src))
}

func (s *Server) handleDeleteSource(w http.ResponseWriter, r *http.Request, user *models.User) {
	id, _ := pathID(r)
	if err := s.Store.DeleteSource(id); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleTestSource validates source config by listing its root.
func (s *Server) handleTestSource(w http.ResponseWriter, r *http.Request, user *models.User) {
	var req struct {
		Type   string              `json:"type"`
		Config models.SourceConfig `json:"config"`
	}
	if err := readBody(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var drv source.Driver
	var err error
	switch req.Type {
	case "local":
		drv = source.NewLocal(req.Config.Root)
	case "webdav":
		drv = source.NewWebDAV(req.Config.WebDAVURL, req.Config.WebDAVUser, req.Config.WebDAVPass)
	case "openlist":
		drv, err = source.NewOpenList(req.Config.BaseURL, req.Config.Token)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
	default:
		fail(w, 400, "未知类型")
		return
	}
	ctx := r.Context()
	root := "/"
	if req.Type == "local" {
		root = "/"
	}
	entries, err := drv.List(ctx, root)
	if err != nil {
		fail(w, 400, "连接失败: "+err.Error())
		return
	}
	names := make([]string, 0, 10)
	for i, e := range entries {
		if i >= 10 {
			break
		}
		if e.IsDir {
			names = append(names, "[目录] "+e.Name)
		} else {
			names = append(names, e.Name)
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "count": len(entries), "entries": names})
}

// ---------- libraries ----------

func (s *Server) handleListLibraries(w http.ResponseWriter, r *http.Request, user *models.User) {
	libs, err := s.Store.Libraries()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if libs == nil {
		libs = []models.Library{}
	}
	writeJSON(w, 200, libs)
}

func (s *Server) handleCreateLibrary(w http.ResponseWriter, r *http.Request, user *models.User) {
	var req models.Library
	if err := readBody(r, &req); err != nil || req.Name == "" {
		fail(w, 400, "名称不能为空")
		return
	}
	if req.Type != "movie" && req.Type != "series" {
		req.Type = "movie"
	}
	if req.Path == "" {
		req.Path = "/"
	}
	if req.Providers == "" {
		req.Providers = `["nfo","tmdb"]`
	}
	if err := s.Store.CreateLibrary(&req); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, req)
}

func (s *Server) handleUpdateLibrary(w http.ResponseWriter, r *http.Request, user *models.User) {
	id, _ := pathID(r)
	lib, err := s.Store.LibraryByID(id)
	if err != nil {
		fail(w, 404, "媒体库不存在")
		return
	}
	var req models.Library
	if err := readBody(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.Name != "" {
		lib.Name = req.Name
	}
	if req.Type == "movie" || req.Type == "series" {
		lib.Type = req.Type
	}
	if req.Path != "" {
		lib.Path = req.Path
	}
	if req.Providers != "" {
		lib.Providers = req.Providers
	}
	if err := s.Store.UpdateLibrary(lib); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, lib)
}

func (s *Server) handleDeleteLibrary(w http.ResponseWriter, r *http.Request, user *models.User) {
	id, _ := pathID(r)
	if err := s.Store.DeleteLibrary(id); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleScanLibrary(w http.ResponseWriter, r *http.Request, user *models.User) {
	id, _ := pathID(r)
	lib, err := s.Store.LibraryByID(id)
	if err != nil {
		fail(w, 404, "媒体库不存在")
		return
	}
	go func() {
		_ = s.Scanner.ScanLibrary(lib)
	}()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleScanStatus(w http.ResponseWriter, r *http.Request, user *models.User) {
	writeJSON(w, 200, s.Scanner.AllStates())
}

// ---------- items ----------

func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request, user *models.User) {
	q := r.URL.Query()
	libID, _ := strconv.ParseInt(q.Get("library_id"), 10, 64)
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	items, total, err := s.Store.ItemsByLibrary(libID, q.Get("type"), q.Get("search"), q.Get("sort"), offset, limit)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if items == nil {
		items = []models.Item{}
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
}

func (s *Server) handleItemDetail(w http.ResponseWriter, r *http.Request, user *models.User) {
	id, _ := pathID(r)
	item, err := s.Store.ItemByID(id)
	if err != nil {
		fail(w, 404, "条目不存在")
		return
	}
	lib, _ := s.Store.LibraryByID(item.LibraryID)
	resp := map[string]any{"item": item, "library": lib}

	switch item.Type {
	case "show":
		seasons, _ := s.Store.ItemChildren(item.ID)
		type seasonOut struct {
			models.Item
			Episodes []models.Item `json:"episodes"`
		}
		out := make([]seasonOut, 0, len(seasons))
		for _, se := range seasons {
			eps, _ := s.Store.ItemChildren(se.ID)
			out = append(out, seasonOut{Item: se, Episodes: eps})
		}
		resp["seasons"] = out
	case "season":
		eps, _ := s.Store.ItemChildren(item.ID)
		resp["episodes"] = eps
	default:
		files, _ := s.Store.FilesByItem(item.ID)
		fdtos := make([]map[string]any, 0, len(files))
		for _, f := range files {
			dto := map[string]any{
				"id": f.ID, "size": f.Size, "mtime": f.MTime, "path": f.Path,
			}
			if f.Probe != "" {
				if sum := probeSummary(f.Probe); sum != nil {
					dto["media"] = sum
				}
			}
			subs, _ := s.Store.SubtitlesByFile(f.ID)
			dto["subtitles"] = subs
			fdtos = append(fdtos, dto)
		}
		resp["files"] = fdtos
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleItemRefresh(w http.ResponseWriter, r *http.Request, user *models.User) {
	id, _ := pathID(r)
	var req struct {
		TMDBID int `json:"tmdb_id"`
	}
	_ = readBody(r, &req)
	if err := s.Metadata.RefreshItem(id, req.TMDBID); err != nil {
		fail(w, 400, err.Error())
		return
	}
	it, err := s.Store.ItemByID(id)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"item": it})
}

// ---------- home ----------

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request, user *models.User) {
	resp := map[string]any{}

	if progs, err := s.Store.ProgressByUser(user.ID); err == nil {
		type cont struct {
			Item     models.Item `json:"item"`
			FileID   int64       `json:"file_id"`
			Position float64     `json:"position"`
			Duration float64     `json:"duration"`
			Percent  float64     `json:"percent"`
		}
		var list []cont
		for _, p := range progs {
			it, err := s.Store.ItemByID(p.ItemID)
			if err != nil {
				continue
			}
			if p.Duration > 0 && p.Position/p.Duration > 0.95 {
				continue
			}
			pct := 0.0
			if p.Duration > 0 {
				pct = p.Position / p.Duration * 100
			}
			list = append(list, cont{Item: *it, FileID: p.FileID, Position: p.Position, Duration: p.Duration, Percent: pct})
			if len(list) >= 12 {
				break
			}
		}
		resp["continue_watching"] = list
	}

	recent, _ := s.Store.RecentItems("movie", 18)
	if recent == nil {
		recent = []models.Item{}
	}
	resp["recent_movies"] = recent
	recentShows, _ := s.Store.RecentItems("show", 18)
	if recentShows == nil {
		recentShows = []models.Item{}
	}
	resp["recent_shows"] = recentShows
	libs, _ := s.Store.Libraries()
	resp["libraries"] = libs
	writeJSON(w, 200, resp)
}

// ---------- images ----------

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	kind := r.PathValue("kind")
	item, err := s.Store.ItemByID(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	name := ""
	switch kind {
	case "backdrop":
		name = item.BackdropPath
	case "still":
		name = item.StillPath
	default:
		name = item.PosterPath
	}
	if name == "" {
		s.servePlaceholder(w, r, item.Name)
		return
	}
	full := filepath.Join(s.Cfg.ImagesDir(), filepath.Base(name))
	if _, err := os.Stat(full); err != nil {
		s.servePlaceholder(w, r, item.Name)
		return
	}
	http.ServeFile(w, r, full)
}

func (s *Server) servePlaceholder(w http.ResponseWriter, r *http.Request, title string) {
	t := strings.ReplaceAll(title, "<", "")
	t = strings.ReplaceAll(t, ">", "")
	if rs := []rune(t); len(rs) > 12 {
		t = string(rs[:12])
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="300" height="450" viewBox="0 0 300 450">
<rect width="300" height="450" fill="#141516"/>
<rect width="300" height="450" fill="url(#g)"/>
<defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1">
<stop offset="0" stop-color="#1c1c1f"/><stop offset="1" stop-color="#08090a"/>
</linearGradient></defs>
<text x="150" y="220" fill="#62666d" font-size="16" text-anchor="middle" font-family="sans-serif">%s</text>
<text x="150" y="248" fill="#3e3e44" font-size="11" text-anchor="middle" font-family="sans-serif">暂无海报</text>
</svg>`, t)
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write([]byte(svg))
}

// ---------- progress ----------

func (s *Server) handleSaveProgress(w http.ResponseWriter, r *http.Request, user *models.User) {
	var req struct {
		FileID   int64   `json:"file_id"`
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
	}
	if err := readBody(r, &req); err != nil || req.FileID == 0 {
		fail(w, 400, "file_id 必填")
		return
	}
	f, err := s.Store.FileByID(req.FileID)
	if err != nil {
		fail(w, 404, "文件不存在")
		return
	}
	p := &models.Progress{UserID: user.ID, FileID: f.ID, ItemID: f.ItemID, Position: req.Position, Duration: req.Duration}
	if err := s.Store.SaveProgress(p); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleListProgress(w http.ResponseWriter, r *http.Request, user *models.User) {
	fileIDStr := r.URL.Query().Get("file_id")
	if fileIDStr != "" {
		fid, _ := strconv.ParseInt(fileIDStr, 10, 64)
		p, err := s.Store.ProgressForFile(user.ID, fid)
		if err != nil {
			writeJSON(w, 200, map[string]any{"position": 0, "duration": 0})
			return
		}
		writeJSON(w, 200, p)
		return
	}
	progs, err := s.Store.ProgressByUser(user.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if progs == nil {
		progs = []models.Progress{}
	}
	writeJSON(w, 200, progs)
}
