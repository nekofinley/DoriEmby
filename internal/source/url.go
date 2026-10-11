package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// URLDriver serves a bare http(s) URL (the content of a .strm file).
// The "path" is the URL itself.
type URLDriver struct{}

func (d *URLDriver) List(ctx context.Context, dir string) ([]Entry, error) {
	return nil, fmt.Errorf("url source is not listable")
}

func (d *URLDriver) DirectURL(ctx context.Context, path string) (string, map[string]string, error) {
	return normalizeURL(path), nil, nil
}

func (d *URLDriver) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpDo(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("url: GET %s: %s", path, resp.Status)
	}
	return resp.Body, nil
}

func (d *URLDriver) ProbeURL(ctx context.Context, path string) (string, map[string]string, error) {
	return path, nil, nil
}

func (d *URLDriver) HasDirect() bool { return true }
