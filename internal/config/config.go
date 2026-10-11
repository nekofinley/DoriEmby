package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

type TMDB struct {
	APIKey  string `json:"api_key"`
	Language string `json:"language"`
	APIBase string `json:"api_base"`
	ImgBase string `json:"img_base"`
}

type Playback struct {
	ForceMode       string  `json:"force_mode"` // auto | direct | proxy | remux | transcode
	SegmentSeconds  float64 `json:"segment_seconds"`
	TranscodeHeight int     `json:"transcode_height"`
}

type Config struct {
	Port            int      `json:"port"`
	DataDir         string   `json:"data_dir"`
	Secret          string   `json:"secret"`
	FFmpeg          string   `json:"ffmpeg"`
	FFProbe         string   `json:"ffprobe"`
	TMDB            TMDB     `json:"tmdb"`
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
		},
		Playback: Playback{
			ForceMode:       "auto",
			SegmentSeconds:  4,
			TranscodeHeight: 1080,
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
	if c.Playback.ForceMode == "" {
		c.Playback.ForceMode = "auto"
	}
	if c.Playback.SegmentSeconds <= 0 {
		c.Playback.SegmentSeconds = 4
	}
	if c.Playback.TranscodeHeight <= 0 {
		c.Playback.TranscodeHeight = 1080
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

func (c *Config) DBPath() string     { return filepath.Join(c.Abs(), "doriya.db") }
func (c *Config) ImagesDir() string  { return filepath.Join(c.Abs(), "images") }
func (c *Config) LogsDir() string    { return filepath.Join(c.Abs(), "logs") }

func randomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
