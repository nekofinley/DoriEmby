package source

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func jsonUnmarshal(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}

// LocalDriver serves a directory on the server's own filesystem.
type LocalDriver struct {
	Root string
}

func NewLocal(root string) *LocalDriver {
	return &LocalDriver{Root: filepath.Clean(root)}
}

func (d *LocalDriver) resolve(p string) string {
	p = strings.TrimPrefix(filepath.ToSlash(p), "/")
	return filepath.Join(d.Root, filepath.FromSlash(p))
}

func (d *LocalDriver) List(ctx context.Context, dir string) ([]Entry, error) {
	full := d.resolve(dir)
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Entry{
			Name:  e.Name(),
			Path:  VirtualJoin(dir, e.Name()),
			IsDir: e.IsDir(),
			Size:  info.Size(),
			MTime: info.ModTime().Unix(),
		})
	}
	return out, nil
}

func (d *LocalDriver) DirectURL(ctx context.Context, path string) (string, map[string]string, error) {
	return "", nil, ErrNoDirect
}

func (d *LocalDriver) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	return os.Open(d.resolve(path))
}

func (d *LocalDriver) ProbeURL(ctx context.Context, path string) (string, map[string]string, error) {
	return d.resolve(path), nil, nil
}

// Serve writes the file with full Range support (used for proxy playback).
func (d *LocalDriver) Serve(w http.ResponseWriter, r *http.Request, path string) {
	http.ServeFile(w, r, d.resolve(path))
}

func (d *LocalDriver) HasDirect() bool { return false }

// ErrNoDirect marks drivers that cannot produce a 302-able link.
var ErrNoDirect = errNoDirect{}

type errNoDirect struct{}

func (errNoDirect) Error() string { return "source does not support direct links" }
