package api

import (
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"doriyamedia/internal/auth"
	"doriyamedia/internal/models"
)

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.UserCount()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"setup_needed": n == 0})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.UserCount()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if n > 0 {
		fail(w, 400, "已初始化,请直接登录")
		return
	}
	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := readBody(r, &req); err != nil || req.Name == "" || len(req.Password) < 4 {
		fail(w, 400, "需要用户名和至少 4 位密码")
		return
	}
	hash, salt := auth.HashPassword(req.Password)
	u := &models.User{Name: req.Name, PassHash: hash, Salt: salt, Role: "admin"}
	if err := s.Store.CreateUser(u); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.startSession(w, u)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := readBody(r, &req); err != nil {
		fail(w, 400, "参数错误")
		return
	}
	u, err := s.Store.UserByName(req.Name)
	if err != nil || !auth.Verify(req.Password, u.PassHash, u.Salt) {
		fail(w, 401, "用户名或密码错误")
		return
	}
	s.startSession(w, u)
}

func (s *Server) startSession(w http.ResponseWriter, u *models.User) {
	token := auth.NewToken()
	exp := time.Now().Add(30 * 24 * time.Hour)
	if err := s.Store.CreateSession(token, u.ID, exp); err != nil {
		fail(w, 500, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Expires: exp,
	})
	writeJSON(w, 200, map[string]any{"token": token, "user": u})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, user *models.User) {
	token := ""
	if c, err := r.Cookie(cookieName); err == nil {
		token = c.Value
	}
	if ah := r.Header.Get("Authorization"); token == "" && len(ah) > 7 {
		token = ah[7:]
	}
	_ = s.Store.DeleteSession(token)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, user *models.User) {
	writeJSON(w, 200, map[string]any{"user": user, "setup_needed": false})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request, user *models.User) {
	ffmpegOK := exec.Command(s.Cfg.FFmpeg, "-version").Run() == nil
	ffprobeOK := exec.Command(s.Cfg.FFProbe, "-version").Run() == nil
	writeJSON(w, 200, map[string]any{
		"version":    "0.1.0",
		"os":         runtime.GOOS,
		"ffmpeg_ok":  ffmpegOK,
		"ffprobe_ok": ffprobeOK,
		"tmdb_ok":    s.Cfg.TMDB.APIKey != "",
		"data_dir":   s.Cfg.Abs(),
		"force_mode": s.Cfg.Playback.ForceMode,
	})
}

func (s *Server) configFile() string {
	if s.ConfigPath != "" {
		return s.ConfigPath
	}
	return "config.json"
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request, user *models.User) {
	cfg := map[string]any{
		"port":             s.Cfg.Port,
		"ffmpeg":           s.Cfg.FFmpeg,
		"ffprobe":          s.Cfg.FFProbe,
		"scan_concurrency": s.Cfg.ScanConcurrency,
		"dev_web_dir":      s.Cfg.DevWebDir,
		"tmdb":             s.Cfg.TMDB,
		"bgm":              s.Cfg.BGM,
		"playback":         s.Cfg.Playback,
	}
	writeJSON(w, 200, cfg)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request, user *models.User) {
	var req struct {
		Port            *int            `json:"port"`
		FFmpeg          *string         `json:"ffmpeg"`
		FFProbe         *string         `json:"ffprobe"`
		ScanConcurrency *int            `json:"scan_concurrency"`
		DevWebDir       *string         `json:"dev_web_dir"`
		TMDB            *configTMDB     `json:"tmdb"`
		BGM             *configBGM      `json:"bgm"`
		Playback        *configPlayback `json:"playback"`
	}
	if err := readBody(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.Port != nil && *req.Port > 0 {
		s.Cfg.Port = *req.Port
	}
	if req.FFmpeg != nil && *req.FFmpeg != "" {
		s.Cfg.FFmpeg = *req.FFmpeg
	}
	if req.FFProbe != nil && *req.FFProbe != "" {
		s.Cfg.FFProbe = *req.FFProbe
	}
	if req.ScanConcurrency != nil && *req.ScanConcurrency > 0 {
		s.Cfg.ScanConcurrency = *req.ScanConcurrency
	}
	if req.DevWebDir != nil {
		s.Cfg.DevWebDir = *req.DevWebDir
	}
	if req.TMDB != nil {
		s.Cfg.TMDB.APIKey = req.TMDB.APIKey
		if req.TMDB.Language != "" {
			s.Cfg.TMDB.Language = req.TMDB.Language
		}
		if req.TMDB.APIBase != "" {
			s.Cfg.TMDB.APIBase = req.TMDB.APIBase
		}
		if req.TMDB.ImgBase != "" {
			s.Cfg.TMDB.ImgBase = req.TMDB.ImgBase
		}
		if req.TMDB.UseProxy != nil {
			s.Cfg.TMDB.UseProxy = *req.TMDB.UseProxy
		}
		if req.TMDB.Proxy != nil && strings.TrimSpace(*req.TMDB.Proxy) != "" {
			s.Cfg.TMDB.Proxy = strings.TrimSpace(*req.TMDB.Proxy)
		}
	}
	if req.BGM != nil {
		s.Cfg.BGM.Token = req.BGM.Token
		if req.BGM.APIBase != "" {
			s.Cfg.BGM.APIBase = strings.TrimSpace(req.BGM.APIBase)
		}
		if req.BGM.UseProxy != nil {
			s.Cfg.BGM.UseProxy = *req.BGM.UseProxy
		}
		if req.BGM.Proxy != nil && strings.TrimSpace(*req.BGM.Proxy) != "" {
			s.Cfg.BGM.Proxy = strings.TrimSpace(*req.BGM.Proxy)
		}
	}
	if req.Playback != nil {
		if req.Playback.ForceMode != "" {
			s.Cfg.Playback.ForceMode = req.Playback.ForceMode
		}
		if req.Playback.SegmentSeconds > 0 {
			s.Cfg.Playback.SegmentSeconds = req.Playback.SegmentSeconds
		}
		if req.Playback.TranscodeHeight > 0 {
			s.Cfg.Playback.TranscodeHeight = req.Playback.TranscodeHeight
		}
		if req.Playback.ProbeSizeMB != nil && *req.Playback.ProbeSizeMB > 0 {
			s.Cfg.Playback.ProbeSizeMB = *req.Playback.ProbeSizeMB
		}
		if req.Playback.ProbeAnalyzeSec != nil && *req.Playback.ProbeAnalyzeSec > 0 {
			s.Cfg.Playback.ProbeAnalyzeSec = *req.Playback.ProbeAnalyzeSec
		}
		if req.Playback.PreProbeAfterScan != nil {
			s.Cfg.Playback.PreProbeAfterScan = *req.Playback.PreProbeAfterScan
		}
		if req.Playback.PreProbeMaxFiles != nil && *req.Playback.PreProbeMaxFiles >= 0 {
			s.Cfg.Playback.PreProbeMaxFiles = *req.Playback.PreProbeMaxFiles
		}
		if req.Playback.PreProbeConcurrency != nil && *req.Playback.PreProbeConcurrency > 0 {
			s.Cfg.Playback.PreProbeConcurrency = *req.Playback.PreProbeConcurrency
		}
		if req.Playback.AppleHEVCCopy != nil {
			s.Cfg.Playback.AppleHEVCCopy = *req.Playback.AppleHEVCCopy
		}
		if req.Playback.HLSSessionPackager != nil {
			s.Cfg.Playback.HLSSessionPackager = *req.Playback.HLSSessionPackager
		}
	}
	if err := s.Cfg.Save(s.configFile()); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Engine.SetBins(s.Cfg.FFmpeg, s.Cfg.FFProbe)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

type configTMDB = struct {
	APIKey   string  `json:"api_key"`
	Language string  `json:"language"`
	APIBase  string  `json:"api_base"`
	ImgBase  string  `json:"img_base"`
	UseProxy *bool   `json:"use_proxy"`
	Proxy    *string `json:"proxy"`
}

type configBGM = struct {
	Token    string  `json:"token"`
	APIBase  string  `json:"api_base"`
	UseProxy *bool   `json:"use_proxy"`
	Proxy    *string `json:"proxy"`
}

type configPlayback = struct {
	ForceMode       string  `json:"force_mode"`
	SegmentSeconds  float64 `json:"segment_seconds"`
	TranscodeHeight int     `json:"transcode_height"`
	// pointers so "not sent" is distinguishable from an explicit false/0
	ProbeSizeMB         *int  `json:"probe_size_mb"`
	ProbeAnalyzeSec     *int  `json:"probe_analyze_sec"`
	PreProbeAfterScan   *bool `json:"preprobe_after_scan"`
	PreProbeMaxFiles    *int  `json:"preprobe_max_files"`
	PreProbeConcurrency *int  `json:"preprobe_concurrency"`
	AppleHEVCCopy       *bool `json:"apple_hevc_copy"`
	HLSSessionPackager  *bool `json:"hls_session_packager"`
}
