package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"doriyamedia/internal/models"
)

// Entry is one row of a directory listing, in driver path space.
type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

// Driver abstracts a storage source.
type Driver interface {
	List(ctx context.Context, dir string) ([]Entry, error)
	// DirectURL returns a browsable URL for the file plus any headers that
	// MUST be sent when fetching it (nil if none). Only called when HasDirect().
	DirectURL(ctx context.Context, path string) (string, map[string]string, error)
	// Open returns a reader of the whole file (used for small files: .strm, subtitles).
	Open(ctx context.Context, path string) (io.ReadCloser, error)
	// ProbeURL returns a URL plus headers that ffprobe can consume, or "" when
	// probing must happen by another means (local files pass a filesystem path).
	ProbeURL(ctx context.Context, path string) (string, map[string]string, error)
	HasDirect() bool
}

// NewDriver builds a driver from a source row.
func NewDriver(src *models.Source) (Driver, error) {
	var cfg models.SourceConfig
	if src.Config != "" {
		if err := jsonUnmarshal(src.Config, &cfg); err != nil {
			return nil, fmt.Errorf("source %d config: %w", src.ID, err)
		}
	}
	switch src.Type {
	case "local":
		return NewLocal(cfg.Root), nil
	case "webdav":
		return NewWebDAV(cfg.WebDAVURL, cfg.WebDAVUser, cfg.WebDAVPass), nil
	case "openlist":
		return NewOpenList(cfg.BaseURL, cfg.Token)
	case "url":
		return &URLDriver{}, nil
	default:
		return nil, fmt.Errorf("unknown source type %q", src.Type)
	}
}

// ---------- shared helpers ----------

var httpClient = &http.Client{
	Timeout: 0, // streaming; per-call contexts apply
	Transport: &http.Transport{
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	},
}

// escapeURLPath escapes each path segment, preserving "/" separators.
func escapeURLPath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func joinURL(base, path string) string {
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(escapeURLPath(path), "/")
}

func httpDo(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)
	return httpClient.Do(req)
}

// VirtualJoin joins a virtual base dir and child name into a virtual path.
func VirtualJoin(dir, name string) string {
	return strings.TrimSuffix(dir, "/") + "/" + name
}
