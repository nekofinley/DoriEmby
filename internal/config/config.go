package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// DefaultTMDBProxy is the usual local SOCKS5 port (Clash/v2rayN default).
const DefaultTMDBProxy = "socks5://127.0.0.1:7890"

type TMDB struct {
	APIKey   string `json:"api_key"`
	Language string `json:"language"`
	APIBase  string `json:"api_base"`
	ImgBase  string `json:"img_base"`
	// UseProxy routes TMDB API calls and poster/backdrop downloads through a
	// local proxy — required for api.themoviedb.org / image.tmdb.org from
	// mainland China.
	UseProxy bool   `json:"use_proxy"`
	Proxy    string `json:"proxy"`
}

// BGMTV is the Bangumi (bgm.tv) metadata source — the best Chinese-language
// source for anime titles, episode titles and air dates.
type BGMTV struct {
	// Token is an optional personal access token (next.bgm.tv/demo/access-token).
	// Public search/detail/episode endpoints work without it.
	Token   string `json:"token"`
	APIBase string `json:"api_base"`
	// UseProxy defaults to true: api.bgm.tv is frequently DNS-poisoned from
	// mainland China, so a local proxy is usually the only way to reach it.
	UseProxy bool   `json:"use_proxy"`
	Proxy    string `json:"proxy"`
}

type Playback struct {
	ForceMode       string  `json:"force_mode"` // auto | direct | proxy | remux | transcode
	SegmentSeconds  float64 `json:"segment_seconds"`
	TranscodeHeight int     `json:"transcode_height"`

	// ProbeSizeMB / ProbeAnalyzeSec bound how much of the source ffprobe pulls
	// before playback can be decided. The historical fixed 50MB/20s values cost
	// roughly 60MB per file, which on a cloud drive meant minutes of waiting
	// before the decision even came back (and every first play paid it again).
	// A small window resolves container/codecs for virtually all real files;
	// the deep values are still used as an automatic fallback when it doesn't.
	ProbeSizeMB     int `json:"probe_size_mb"`
	ProbeAnalyzeSec int `json:"probe_analyze_sec"`

	// PreProbeAfterScan warms the probe cache in the background after a library
	// scan, so opening a file is already decided. PreProbeMaxFiles caps how many
	// files one scan warms (0 = unlimited); PreProbeConcurrency bounds parallel
	// probes so the cloud drive is not hammered.
	PreProbeAfterScan   bool `json:"preprobe_after_scan"`
	PreProbeMaxFiles    int  `json:"preprobe_max_files"`
	PreProbeConcurrency int  `json:"preprobe_concurrency"`

	// AppleHEVCCopy lets Apple clients copy HEVC into MPEG-TS instead of
	// transcoding. It is OFF by default because Apple's HLS stack does not decode
	// HEVC carried in MPEG-TS: such a copy plays audio with no picture. Enable it
	// only on a device that has been verified to play HEVC-in-TS.
	AppleHEVCCopy bool `json:"apple_hevc_copy"`

	// HLSSessionPackager routes HEVC copy for Apple through one long-lived ffmpeg
	// that packages fMP4 HLS — the only form of copied HEVC Apple will play — so
	// copied HEVC needs no transcoding at all. It writes the packaged segments
	// under <data>/cache/hls for the life of a playback session, trading
	// temporary disk space for CPU. Off by default.
	HLSSessionPackager bool `json:"hls_session_packager"`

	// HLSCacheMB bounds the packaged-segment cache under <data>/cache/hls. A
	// session that finished the whole file is kept, so replaying that file needs
	// no packaging at all; the least recently used are evicted to stay inside this
	// budget, and nothing partial survives a restart. 0 turns the cache off, and
	// then a session's output is deleted as soon as it ends.
	HLSCacheMB int `json:"hls_cache_mb"`

	// TranscodeFallback offers a real re-encode as a *fallback* when the strategy
	// a file was decided on fails. Off by default: the decision already picks a
	// transcode when nothing else can play the file, so a fallback transcode is
	// usually spent re-encoding something the client could not play anyway — and
	// on a small server it cannot keep up regardless, so it burns the CPU and
	// still shows a spinner. With it off, a failed playback says so and lets the
	// viewer choose (强制转码 is still one click away).
	TranscodeFallback bool `json:"transcode_fallback"`
}

type Config struct {
	Port            int      `json:"port"`
	DataDir         string   `json:"data_dir"`
	Secret          string   `json:"secret"`
	FFmpeg          string   `json:"ffmpeg"`
	FFProbe         string   `json:"ffprobe"`
	TMDB            TMDB     `json:"tmdb"`
	BGM             BGMTV    `json:"bgm"`
	Playback        Playback `json:"playback"`
	ScanConcurrency int      `json:"scan_concurrency"`
	DevWebDir       string   `json:"dev_web_dir"`
}

func Defaults() *Config {
	return &Config{
		Port:    5666,
		DataDir: "data",
		Secret:  randomSecret(),
		FFmpeg:  "ffmpeg",
		FFProbe: "ffprobe",
		TMDB: TMDB{
			Language: "zh-CN",
			APIBase:  "https://api.themoviedb.org/3",
			ImgBase:  "https://image.tmdb.org/t/p",
			Proxy:    DefaultTMDBProxy,
		},
		BGM: BGMTV{
			APIBase:  "https://api.bgm.tv",
			UseProxy: true,
			Proxy:    DefaultTMDBProxy,
		},
		Playback: Playback{
			ForceMode:           "auto",
			SegmentSeconds:      4,
			TranscodeHeight:     1080,
			ProbeSizeMB:         8,
			ProbeAnalyzeSec:     4,
			PreProbeAfterScan:   true,
			PreProbeMaxFiles:    100,
			PreProbeConcurrency: 1,
			HLSCacheMB:          2048,
		},
		ScanConcurrency: 4,
	}
}

func Load(path string) (*Config, error) {
	cfg := Defaults()
	raw, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	cfg.normalize()
	if err := cfg.Save(path); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) normalize() {
	if c.Port <= 0 {
		c.Port = 5666
	}
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	if c.Secret == "" {
		c.Secret = randomSecret()
	}
	if c.TMDB.Language == "" {
		c.TMDB.Language = "zh-CN"
	}
	if c.TMDB.APIBase == "" {
		c.TMDB.APIBase = "https://api.themoviedb.org/3"
	}
	if c.TMDB.ImgBase == "" {
		c.TMDB.ImgBase = "https://image.tmdb.org/t/p"
	}
	if c.TMDB.Proxy == "" {
		c.TMDB.Proxy = DefaultTMDBProxy
	}
	if c.BGM.APIBase == "" {
		c.BGM.APIBase = "https://api.bgm.tv"
	}
	if c.BGM.Proxy == "" {
		c.BGM.Proxy = DefaultTMDBProxy
	}
	if c.Playback.ForceMode == "" {
		c.Playback.ForceMode = "auto"
	}
	if c.Playback.SegmentSeconds <= 0 {
		c.Playback.SegmentSeconds = 4
	}
	if c.Playback.TranscodeHeight <= 0 {
		c.Playback.TranscodeHeight = 1080
	}
	if c.Playback.ProbeSizeMB <= 0 {
		c.Playback.ProbeSizeMB = 8
	}
	if c.Playback.ProbeSizeMB > 512 {
		c.Playback.ProbeSizeMB = 512
	}
	if c.Playback.ProbeAnalyzeSec <= 0 {
		c.Playback.ProbeAnalyzeSec = 4
	}
	if c.Playback.ProbeAnalyzeSec > 120 {
		c.Playback.ProbeAnalyzeSec = 120
	}
	if c.Playback.PreProbeMaxFiles < 0 {
		c.Playback.PreProbeMaxFiles = 0
	}
	if c.Playback.PreProbeConcurrency <= 0 {
		c.Playback.PreProbeConcurrency = 1
	}
	if c.Playback.PreProbeConcurrency > 8 {
		c.Playback.PreProbeConcurrency = 8
	}
	// The cache budget is in MB; an unset value gets the default rather than
	// silently disabling the cache, and a negative one means "off".
	if c.Playback.HLSCacheMB < 0 {
		c.Playback.HLSCacheMB = 0
	}
	if c.Playback.HLSCacheMB > 102400 {
		c.Playback.HLSCacheMB = 102400
	}
	if c.ScanConcurrency <= 0 {
		c.ScanConcurrency = 4
	}
	if c.FFmpeg == "" {
		c.FFmpeg = "ffmpeg"
	}
	if c.FFProbe == "" {
		c.FFProbe = "ffprobe"
	}
}

func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func (c *Config) Abs() string {
	if filepath.IsAbs(c.DataDir) {
		return c.DataDir
	}
	wd, _ := os.Getwd()
	return filepath.Join(wd, c.DataDir)
}

func (c *Config) DBPath() string    { return filepath.Join(c.Abs(), "doriyamedia.db") }
func (c *Config) ImagesDir() string { return filepath.Join(c.Abs(), "images") }
func (c *Config) LogsDir() string   { return filepath.Join(c.Abs(), "logs") }

func randomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
