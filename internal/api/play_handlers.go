package api

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"doriya/internal/media"
	"doriya/internal/models"
	"doriya/internal/source"
)

// proxyClient streams media through the server (no global timeout).
var proxyClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	},
}

const sigTTL = 12 * time.Hour

// ---------- /api/play/{id} ----------

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request, user *models.User) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "bad id")
		return
	}
	file, err := s.Store.FileByID(id)
	if err != nil {
		fail(w, 404, "文件不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()

	drv, err := s.Engine.DriverFor(file)
	if err != nil {
		fail(w, 500, "存储源错误: "+err.Error())
		return
	}

	probe, err := s.Engine.EnsureProbe(ctx, file, drv)
	if err != nil {
		log.Printf("probe failed for file %d (%s): %v", file.ID, file.Path, err)
		fail(w, 500, "媒体探测失败: "+err.Error())
		return
	}

	client := s.clientKind(r)
	force := r.URL.Query().Get("force")
	if force == "" {
		force = s.Cfg.Playback.ForceMode
	}

	hasDirect := false
	var directHeaders map[string]string
	if drv.HasDirect() {
		if _, hdrs, err := drv.DirectURL(ctx, file.Path); err == nil {
			hasDirect = true
			directHeaders = hdrs
		}
	}

	dec := media.Decide(probe, file.Path, client, hasDirect, directHeaders, force)

	switch dec.Mode {
	case "direct":
		dec.URL = fmt.Sprintf("/api/stream/%d%s", file.ID, signedQuery(s.Cfg.Secret, file.ID, sigTTL))
		dec.MimeType = "video/mp4"
	case "proxy":
		dec.URL = fmt.Sprintf("/api/stream/%d?proxy=1", file.ID) +
			strings.Replace(signedQuery(s.Cfg.Secret, file.ID, sigTTL), "?", "&", 1)
		dec.MimeType = "video/mp4"
	default: // remux / transcode → HLS
		sq := signedQuery(s.Cfg.Secret, file.ID, sigTTL)
		dec.URL = fmt.Sprintf("/api/hls/%d/index.m3u8%s&m=%s", file.ID, sq, dec.Mode)
		dec.MimeType = "application/vnd.apple.mpegurl"
	}

	// audio tracks
	auds := probe.AudioStreams()
	selOrdinal := 0
	if a := r.URL.Query().Get("a"); a != "" {
		if n, err := strconv.Atoi(a); err == nil && n >= 0 && n < len(auds) {
			selOrdinal = n
		}
	}
	tracks := make([]audioInfo, 0, len(auds))
	for i, a := range auds {
		tracks = append(tracks, audioInfo{
			Ordinal: i, Index: a.Index, Codec: a.CodecName, Lang: a.Language(),
			Title: a.Title(), Channels: a.Channels, Selected: i == selOrdinal,
		})
	}

	// subtitles
	subs := s.discoverSubtitles(ctx, drv, file)
	type subDTO struct {
		ID       int64  `json:"id"`
		Label    string `json:"label"`
		Lang     string `json:"lang"`
		Embedded bool   `json:"embedded"`
		URL      string `json:"url,omitempty"`
	}
	subDTOs := make([]subDTO, 0, len(subs))
	for _, sub := range subs {
		dto := subDTO{ID: sub.ID, Label: sub.Title, Lang: sub.Lang, Embedded: sub.Embedded}
		if sub.Convertible {
			dto.URL = fmt.Sprintf("/api/subs/%d/%d.vtt%s", file.ID, sub.ID, signedQuery(s.Cfg.Secret, file.ID, sigTTL))
			subDTOs = append(subDTOs, dto)
		}
	}

	// item context
	item, _ := s.Store.ItemByID(file.ItemID)
	title := ""
	if item != nil {
		title = item.Name
		if item.Type == "episode" {
			if season, err := s.Store.ItemByID(deref(item.ParentID)); err == nil && season.ParentID != nil {
				if show, err := s.Store.ItemByID(*season.ParentID); err == nil {
					title = fmt.Sprintf("%s · S%02dE%02d · %s", show.Name, item.Season, item.Episode, item.Name)
				}
			}
		}
	}

	srcName := ""
	if src, err := s.Store.SourceByID(file.SourceID); err == nil {
		srcName = src.Name
	}

	writeJSON(w, 200, map[string]any{
		"file_id":      file.ID,
		"item_id":      file.ItemID,
		"title":        title,
		"source":       srcName,
		"client":       client,
		"mode":         dec.Mode,
		"kind":         dec.Kind,
		"url":          dec.URL,
		"mime_type":    dec.MimeType,
		"reason":       dec.Reason,
		"duration":     dec.Duration,
		"video_codec":  dec.VideoCodec,
		"audio_codec":  dec.AudioCodec,
		"resolution":   dec.Resolution,
		"media":        probeSummary(file.Probe),
		"audio_tracks": tracks,
		"subtitles":    subDTOs,
	})
}

func (s *Server) clientKind(r *http.Request) media.ClientKind {
	switch strings.ToLower(r.URL.Query().Get("client")) {
	case "ios", "safari":
		return media.ClientSafari
	case "chrome", "desktop", "android", "edge":
		return media.ClientChrome
	case "firefox":
		return media.ClientFirefox
	default:
		return media.DetectClient(r.UserAgent())
	}
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// ---------- subtitle discovery ----------

func (s *Server) discoverSubtitles(ctx context.Context, drv source.Driver, file *models.MediaFile) []models.Subtitle {
	if !strings.HasPrefix(file.Path, "http://") && !strings.HasPrefix(file.Path, "https://") {
		if dir, base := splitDirBase(file.Path); dir != "" && base != "" {
			if entries, err := drv.List(ctx, dir); err == nil {
				lowBase := strings.ToLower(stripExt(base))
				for _, e := range entries {
					if e.IsDir {
						continue
					}
					format, conv := media.DetectSubFormat(e.Name)
					if format == "" {
						continue
					}
					stem := strings.ToLower(stripExt(e.Name))
					if strings.HasPrefix(stem, lowBase) {
						_ = s.Store.UpsertSubtitle(&models.Subtitle{
							FileID: file.ID, Path: e.Path,
							Lang: media.GuessLang(e.Name), Title: media.SubLabel(e.Name),
							Format: format, Convertible: conv,
						})
					}
				}
			}
		}
	}
	// ensure embedded subs registered
	if file.Probe != "" {
		var p media.Probe
		if err := jsonUnmarshal(file.Probe, &p); err == nil {
			s.Engine.RegisterEmbeddedSubs(file, &p)
		}
	}
	subs, _ := s.Store.SubtitlesByFile(file.ID)
	return subs
}

func splitDirBase(p string) (string, string) {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}

func stripExt(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[:i]
	}
	return name
}

// ---------- /api/stream/{id} ----------

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "bad id")
		return
	}
	file, err := s.Store.FileByID(id)
	if err != nil {
		fail(w, 404, "文件不存在")
		return
	}
	drv, err := s.Engine.DriverFor(file)
	if err != nil {
		fail(w, 500, "存储源错误: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	proxy := r.URL.Query().Get("proxy") == "1"
	if !proxy && drv.HasDirect() {
		raw, hdrs, err := drv.DirectURL(ctx, file.Path)
		if err == nil && raw != "" && len(hdrs) == 0 {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, raw, http.StatusFound)
			return
		}
		if err != nil {
			log.Printf("direct url failed for file %d: %v, falling back to proxy", file.ID, err)
		}
	}

	// local files: let net/http serve with full Range support
	if ld, ok := drv.(*source.LocalDriver); ok {
		ld.Serve(w, r, file.Path)
		return
	}

	target, headers, err := drv.ProbeURL(ctx, file.Path)
	if err != nil {
		fail(w, 502, "获取源地址失败: "+err.Error())
		return
	}
	proxyRemote(w, r, target, headers)
}

func proxyRemote(w http.ResponseWriter, r *http.Request, target string, headers map[string]string) {
	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, r.Method, target, nil)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Doriya/0.1")
	}
	resp, err := proxyClient.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			fail(w, 502, "源请求失败: "+err.Error())
		}
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// ---------- /api/hls/{id}/... ----------

func (s *Server) handleHLS(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "bad id")
		return
	}
	file, err := s.Store.FileByID(id)
	if err != nil {
		fail(w, 404, "文件不存在")
		return
	}
	drv, err := s.Engine.DriverFor(file)
	if err != nil {
		fail(w, 500, "存储源错误: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()

	rest := r.PathValue("rest")
	q := r.URL.Query()
	segLen := s.Cfg.Playback.SegmentSeconds

	if rest == "" || strings.HasPrefix(rest, "index") || strings.HasPrefix(rest, "playlist") {
		probe, err := s.Engine.EnsureProbe(ctx, file, drv)
		if err != nil {
			fail(w, 500, "媒体探测失败: "+err.Error())
			return
		}
		duration := probe.Duration()
		if duration <= 0 {
			fail(w, 500, "无法确定媒体时长,无法生成 HLS")
			return
		}
		mode := q.Get("m")
		if mode != "transcode" {
			mode = "remux"
		}
		sig := signedQuery(s.Cfg.Secret, file.ID, sigTTL) + "&m=" + mode
		if a := q.Get("a"); a != "" {
			sig += "&a=" + a
		}
		playlist := s.Engine.Playlist(fmt.Sprintf("/api/hls/%d", file.ID), sig, duration, segLen)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(playlist))
		return
	}

	segIndex, err := parseSegID(strings.TrimSuffix(rest, ".ts"))
	if err != nil {
		fail(w, 404, "未知 HLS 资源")
		return
	}
	probe, err := s.Engine.EnsureProbe(ctx, file, drv)
	if err != nil {
		fail(w, 500, "媒体探测失败: "+err.Error())
		return
	}

	mode := q.Get("m")
	if mode != "transcode" {
		mode = "remux"
	}
	audioOrdinal := 0
	if a := q.Get("a"); a != "" {
		if n, e := strconv.Atoi(a); e == nil {
			audioOrdinal = n
		}
	}
	auds := probe.AudioStreams()
	if audioOrdinal >= len(auds) {
		audioOrdinal = -1
	}
	audioMode := "copy"
	if audioOrdinal >= 0 {
		ac := auds[audioOrdinal].CodecName
		if mode == "transcode" || (ac != "aac" && ac != "mp3") {
			audioMode = "aac"
		}
	}
	if mode == "transcode" {
		audioMode = "aac"
	}

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	start := float64(segIndex) * segLen
	if err := s.Engine.Segment(ctx, file, drv, start, segLen, mode, audioMode, audioOrdinal, s.Cfg.Playback.TranscodeHeight, w); err != nil {
		log.Printf("hls segment %d/%d failed: %v", file.ID, segIndex, err)
	}
}

// ---------- /api/subs/{id}/{sub}.vtt ----------

func (s *Server) handleSubtitle(w http.ResponseWriter, r *http.Request) {
	fileID, ok := pathID(r)
	if !ok {
		fail(w, 400, "bad id")
		return
	}
	subID, err := strconv.ParseInt(stripVTT(r.PathValue("sub")), 10, 64)
	if err != nil {
		fail(w, 400, "bad subtitle id")
		return
	}
	sub, err := s.Store.SubtitleByID(subID)
	if err != nil || sub.FileID != fileID {
		fail(w, 404, "字幕不存在")
		return
	}
	file, err := s.Store.FileByID(fileID)
	if err != nil {
		fail(w, 404, "文件不存在")
		return
	}
	drv, err := s.Engine.DriverFor(file)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	var data []byte
	if sub.Embedded {
		data, err = s.Engine.ExtractEmbeddedSubtitle(ctx, file, drv, sub.StreamIdx)
		if err != nil {
			fail(w, 500, "字幕提取失败: "+err.Error())
			return
		}
	} else {
		rc, err := drv.Open(ctx, sub.Path)
		if err != nil {
			fail(w, 502, "字幕读取失败: "+err.Error())
			return
		}
		defer rc.Close()
		raw, err := io.ReadAll(io.LimitReader(rc, 8<<20))
		if err != nil {
			fail(w, 502, "字幕读取失败: "+err.Error())
			return
		}
		data = media.ToVTT(sub.Format, raw)
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}
