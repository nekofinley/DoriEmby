package api

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"doriyamedia/internal/media"
	"doriyamedia/internal/models"
	"doriyamedia/internal/source"
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
	s.playFile(w, r, file)
}

// playFile runs the decision pipeline for a media file and writes the response.
// Shared by the library player (/api/play) and the file browser (/api/browseplay).
func (s *Server) playFile(w http.ResponseWriter, r *http.Request, file *models.MediaFile) {
	drv, err := s.Engine.DriverFor(file)
	if err != nil {
		fail(w, 500, "存储源错误: "+err.Error())
		return
	}

	// The decision needs a probe, and probing a cloud-drive file is slow (ffprobe
	// must pull a window of it). Instead of holding the response for minutes, a
	// missing probe starts a background job and the request answers immediately
	// with state=probing; the player polls /api/play/{id}/status and re-requests
	// once the probe is ready.
	probe := media.ProbeFromCache(file)
	if probe == nil {
		s.Engine.StartProbe(file, drv)
		writeJSON(w, 200, s.playPreparingPayload(r, file))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()

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

	dec := media.Decide(probe, file.Path, client, media.DecideOptions{
		Force:         force,
		HasDirect:     hasDirect,
		DirectHeaders: directHeaders,
		// Copying HEVC is only safe for Apple when the result is fMP4, which the
		// session packager produces; without it, Apple gets MPEG-TS it cannot
		// decode (audio, no picture) — so the packager implies the copy is allowed.
		AppleHEVCCopy: s.Cfg.Playback.AppleHEVCCopy || s.Cfg.Playback.HLSSessionPackager,
	})

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

	// Fallback strategies for the player to auto-try when the primary mode
	// fails on this client (e.g. HEVC MKV native → cloud transcode).
	sq := signedQuery(s.Cfg.Secret, file.ID, sigTTL)
	proxyURL := fmt.Sprintf("/api/stream/%d?proxy=1", file.ID) + strings.Replace(sq, "?", "&", 1)
	directURL := fmt.Sprintf("/api/stream/%d%s", file.ID, sq)
	hlsURL := func(m string) string {
		return fmt.Sprintf("/api/hls/%d/index.m3u8%s&m=%s", file.ID, sq, m)
	}
	addAlt := func(mode, url, mime string) {
		if url == dec.URL {
			return
		}
		for _, a := range dec.Alternates {
			if a.URL == url {
				return
			}
		}
		dec.Alternates = append(dec.Alternates, media.DecisionAlt{Mode: mode, URL: url, MimeType: mime})
	}
	if hasDirect && len(directHeaders) == 0 && dec.Mode != "direct" {
		addAlt("direct", directURL, "video/mp4")
	}
	if dec.Mode != "proxy" {
		addAlt("proxy", proxyURL, "video/mp4")
	}
	if dec.Duration > 0 {
		if dec.Mode != "transcode" {
			addAlt("transcode", hlsURL("transcode"), "application/vnd.apple.mpegurl")
		}
		if dec.Mode != "remux" {
			addAlt("remux", hlsURL("remux"), "application/vnd.apple.mpegurl")
		}
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

	// subtitles: only convertible (text) tracks can be served as WebVTT, and the
	// one worth turning on automatically is picked here so every client agrees.
	subs := s.discoverSubtitles(ctx, drv, file)
	servable := make([]models.Subtitle, 0, len(subs))
	for _, sub := range subs {
		if sub.Convertible {
			servable = append(servable, sub)
		}
	}
	defSubID := media.PickDefaultSubtitle(servable)

	type subDTO struct {
		ID       int64  `json:"id"`
		Label    string `json:"label"`
		Lang     string `json:"lang"`
		Embedded bool   `json:"embedded"`
		Default  bool   `json:"default"`
		URL      string `json:"url,omitempty"`
	}
	subDTOs := make([]subDTO, 0, len(servable))
	for _, sub := range servable {
		subDTOs = append(subDTOs, subDTO{
			ID:       sub.ID,
			Label:    sub.Title,
			Lang:     sub.Lang,
			Embedded: sub.Embedded,
			Default:  sub.ID == defSubID,
			URL:      fmt.Sprintf("/api/subs/%d/%d.vtt%s", file.ID, sub.ID, signedQuery(s.Cfg.Secret, file.ID, sigTTL)),
		})
	}

	// item context
	title := s.playTitle(file)

	srcName := ""
	if src, err := s.Store.SourceByID(file.SourceID); err == nil {
		srcName = src.Name
	}

	writeJSON(w, 200, map[string]any{
		"state":        "ready",
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
		"alternates":   dec.Alternates,
		"media":        probeSummary(file.Probe),
		"audio_tracks": tracks,
		"versions":     s.versionsFor(file.ItemID, file.ID),
		"subtitles":    subDTOs,
		// The subtitle a player should enable by itself (0 = none). Sent both as
		// a flag per track and once here, so a client can pick either way.
		"default_subtitle_id": defSubID,
	})
}

// playTitle resolves the display title for a file ("剧名 · S01E02 · 标题").
func (s *Server) playTitle(file *models.MediaFile) string {
	item, _ := s.Store.ItemByID(file.ItemID)
	if item == nil {
		return ""
	}
	title := item.Name
	if item.Type == "episode" {
		if season, err := s.Store.ItemByID(deref(item.ParentID)); err == nil && season.ParentID != nil {
			if show, err := s.Store.ItemByID(*season.ParentID); err == nil {
				title = fmt.Sprintf("%s · S%02dE%02d · %s", show.Name, item.Season, item.Episode, item.Name)
			}
		}
	}
	return title
}

// playPreparingPayload is the answer while the first probe of a file runs.
// It carries only what the player needs to render its "preparing" state —
// no URL, because the mode cannot be decided yet.
func (s *Server) playPreparingPayload(r *http.Request, file *models.MediaFile) map[string]any {
	srcName := ""
	if src, err := s.Store.SourceByID(file.SourceID); err == nil {
		srcName = src.Name
	}
	return map[string]any{
		"state":        "probing",
		"file_id":      file.ID,
		"item_id":      file.ItemID,
		"title":        s.playTitle(file),
		"source":       srcName,
		"client":       s.clientKind(r),
		"mode":         "",
		"kind":         "",
		"url":          "",
		"mime_type":    "",
		"reason":       "正在探测媒体信息(容器 / 编码 / 音轨)…",
		"duration":     0,
		"alternates":   []any{},
		"audio_tracks": []any{},
		"subtitles":    []any{},
		"versions":     s.versionsFor(file.ItemID, file.ID),
	}
}

// ---------- /api/play/{id}/status ----------

// handlePlayStatus reports the probe state for a file so the player can show
// real progress instead of an indistinguishable spinner.
func (s *Server) handlePlayStatus(w http.ResponseWriter, r *http.Request, user *models.User) {
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
	st := s.Engine.ProbeStatus(id)
	if media.ProbeFromCache(file) != nil {
		st = media.ProbeStatus{State: "ready"}
	}
	// stream carries segment-generation progress once playback starts, so the
	// player can show how much is ready and whether the server keeps up.
	writeJSON(w, 200, map[string]any{
		"file_id": id,
		"state":   st.State, // idle | probing | ready | failed
		"elapsed": st.Elapsed,
		"error":   st.Error,
		"stream":  s.Engine.StreamStatus(id),
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
		req.Header.Set("User-Agent", "DoriyaMedia/0.1")
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

	// HEVC copy for Apple cannot be MPEG-TS: Apple does not decode HEVC from TS,
	// and fMP4 needs a continuous timeline that only a long-lived packager
	// produces. Everything else stays on the on-demand TS path, which needs no
	// temporary disk. See media/session.go.
	if s.Cfg.Playback.HLSSessionPackager && q.Get("m") != "transcode" {
		if probe, err := s.Engine.EnsureProbe(ctx, file, drv); err == nil {
			if v := probe.Video(); v != nil && v.CodecName == "hevc" {
				s.serveSessionHLS(w, r, ctx, file, drv, probe, rest, q, segLen)
				return
			}
		}
	}

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

	videoIndex := -1
	if v := probe.Video(); v != nil {
		videoIndex = v.Index
	}
	start := float64(segIndex) * segLen
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	written, err := s.Engine.Segment(ctx, file, drv, start, segLen, mode, audioMode, audioOrdinal, videoIndex, s.Cfg.Playback.TranscodeHeight, w)
	if err != nil {
		log.Printf("hls segment %d/%d @%.0fs failed: %v", file.ID, segIndex, start, err)
		// Nothing reached the client yet, so the status is still ours to choose.
		// A real error makes the player retry the segment; answering 200 with an
		// empty body looks like a corrupt segment and can stop playback for good.
		if written == 0 {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "分片生成失败,请重试", http.StatusBadGateway)
		}
	}
}

// ---------- /api/hls/{id}/... served from a packaging session ----------

// serveSessionHLS answers HLS requests from a long-lived packager session: the
// media playlist (rewritten to point at our signed endpoints), the fMP4 init
// segment, and the media segments. A resource the packager has not produced yet
// is waited for rather than 404'd — the player would otherwise treat a segment
// that is merely early as fatal.
func (s *Server) serveSessionHLS(w http.ResponseWriter, r *http.Request, ctx context.Context,
	file *models.MediaFile, drv source.Driver, probe *media.Probe, rest string, q url.Values, segLen float64) {

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
	videoIndex := -1
	videoCodec := ""
	if v := probe.Video(); v != nil {
		videoIndex = v.Index
		videoCodec = v.CodecName
	}

	// Re-resolve so a link the cloud refused is replaced (see resolveTarget).
	target, headers, err := s.Engine.ProbeTarget(ctx, file, drv)
	if err != nil {
		fail(w, 502, "获取源地址失败: "+err.Error())
		return
	}

	audioMode := "copy"
	if audioOrdinal >= 0 {
		if ac := auds[audioOrdinal].CodecName; ac != "aac" && ac != "mp3" {
			audioMode = "aac"
		}
	}

	opts := media.PackagerOptions{
		FFmpeg:          s.Cfg.FFmpeg,
		Target:          target,
		Headers:         headers,
		VideoIndex:      videoIndex,
		AudioOrdinal:    audioOrdinal,
		VideoMode:       "remux",
		AudioMode:       audioMode,
		VideoCodec:      videoCodec,
		TranscodeHeight: s.Cfg.Playback.TranscodeHeight,
		SegmentSeconds:  segLen,
	}
	key := media.SessionKey(file.ID, "remux", audioOrdinal, videoIndex, s.Cfg.Playback.TranscodeHeight)
	sess, err := s.Engine.EnsureSession(ctx, key, opts)
	if err != nil {
		log.Printf("hls session for file %d failed to start: %v", file.ID, err)
		fail(w, 502, "打包会话启动失败: "+err.Error())
		return
	}

	sig := signedQuery(s.Cfg.Secret, file.ID, sigTTL) + "&m=remux"
	if a := q.Get("a"); a != "" {
		sig += "&a=" + a
	}
	base := fmt.Sprintf("/api/hls/%d", file.ID)

	switch {
	case rest == "" || strings.HasPrefix(rest, "index") || strings.HasPrefix(rest, "playlist"):
		if err := sess.WaitForFile(ctx, sess.PlaylistPath(), 45*time.Second); err != nil {
			fail(w, 502, "打包播放列表未就绪: "+err.Error())
			return
		}
		pl, err := os.ReadFile(sess.PlaylistPath())
		if err != nil {
			fail(w, 502, "读取播放列表失败: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(media.RewriteSessionPlaylist(string(pl), base, sig)))

	case rest == "init.mp4":
		s.serveSessionFile(w, r, ctx, sess, sess.InitPath())

	default:
		idx, ok := media.ParseSessionSegment(rest)
		if !ok {
			fail(w, 404, "未知 HLS 资源")
			return
		}
		s.serveSessionFile(w, r, ctx, sess, sess.SegmentPath(idx))
	}
}

// serveSessionFile waits for a packaged file and serves it with Range support.
func (s *Server) serveSessionFile(w http.ResponseWriter, r *http.Request, ctx context.Context, sess *media.Session, path string) {
	if err := sess.WaitForFile(ctx, path, 45*time.Second); err != nil {
		log.Printf("hls session %s: %v", filepath.Base(path), err)
		w.Header().Set("Retry-After", "2")
		http.Error(w, "分片尚未就绪,请重试", http.StatusBadGateway)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		fail(w, 502, "读取分片失败: "+err.Error())
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		fail(w, 502, "读取分片失败: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	// Segments are transient: the session directory goes away when the session
	// closes, so nothing here may be cached by the client.
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
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
