package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OpenListDriver talks to OpenList / AList v3 HTTP API (/api/fs/list, /api/fs/get).
// raw_url from fs/get is the 302 direct link (cloud CDN), so playback can redirect
// clients straight to the cloud drive.
type OpenListDriver struct {
	Base  string
	Token string
}

func NewOpenList(base, token string) (*OpenListDriver, error) {
	if base == "" {
		return nil, fmt.Errorf("openlist: base url required")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	return &OpenListDriver{Base: strings.TrimSuffix(base, "/"), Token: token}, nil
}

type alistResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (d *OpenListDriver) call(ctx context.Context, api string, payload any) (json.RawMessage, error) {
	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", d.Base+api, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if d.Token != "" {
		req.Header.Set("Authorization", d.Token)
	}
	resp, err := httpDo(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	var r alistResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("openlist: bad response from %s: %w", api, err)
	}
	if r.Code != 200 {
		return nil, fmt.Errorf("openlist: %s", r.Message)
	}
	return r.Data, nil
}

type alistEntry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Modified string `json:"modified"`
}

func alistTime(s string) int64 {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05 -0700 MST"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix()
		}
	}
	return 0
}

func (d *OpenListDriver) List(ctx context.Context, dir string) ([]Entry, error) {
	if dir == "" {
		dir = "/"
	}
	var out []Entry
	for page := 1; ; page++ {
		data, err := d.call(ctx, "/api/fs/list", map[string]any{
			"path": dir, "password": "", "page": page, "per_page": 300, "refresh": false,
		})
		if err != nil {
			return nil, err
		}
		var res struct {
			Content []alistEntry `json:"content"`
			Total   int          `json:"total"`
		}
		if err := json.Unmarshal(data, &res); err != nil {
			return nil, err
		}
		for _, e := range res.Content {
			out = append(out, Entry{
				Name:  e.Name,
				Path:  VirtualJoin(dir, e.Name),
				IsDir: e.IsDir,
				Size:  e.Size,
				MTime: alistTime(e.Modified),
			})
		}
		if len(res.Content) == 0 || (res.Total > 0 && len(out) >= res.Total) {
			break
		}
	}
	return out, nil
}

type alistGet struct {
	Name     string          `json:"name"`
	Size     int64           `json:"size"`
	Sign     string          `json:"sign"`
	RawURL   string          `json:"raw_url"`
	Header   json.RawMessage `json:"header"`
	IsDir    bool            `json:"is_dir"`
	Provider string          `json:"provider"`
}

// parseAlistHeader accepts `header` as either a JSON object or a
// JSON-encoded string (some OpenList versions return the latter).
func parseAlistHeader(raw json.RawMessage) map[string]string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err == nil {
		return m
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		if err := json.Unmarshal([]byte(s), &m); err == nil {
			return m
		}
	}
	return nil
}

// resolve returns a fetchable URL for path plus required headers.
func (d *OpenListDriver) resolve(ctx context.Context, path string) (string, map[string]string, error) {
	data, err := d.call(ctx, "/api/fs/get", map[string]any{"path": path, "password": ""})
	if err != nil {
		return "", nil, err
	}
	var g alistGet
	if err := json.Unmarshal(data, &g); err != nil {
		return "", nil, err
	}
	if g.RawURL == "" {
		return "", nil, fmt.Errorf("openlist: empty raw_url for %s (网盘取直链失败)", path)
	}
	raw := normalizeURL(g.RawURL)
	if strings.HasPrefix(raw, "/") {
		raw = d.Base + raw // openlist proxy link (/p/...)
	}
	return raw, parseAlistHeader(g.Header), nil
}

// normalizeURL re-encodes a URL so spaces/CJK are percent-escaped (ffprobe/302 need this).
func normalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.String()
}

func (d *OpenListDriver) DirectURL(ctx context.Context, path string) (string, map[string]string, error) {
	return d.resolve(ctx, path)
}

func (d *OpenListDriver) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	raw, hdrs, err := d.resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	resp, err := httpDo(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("openlist: GET %s: %s", path, resp.Status)
	}
	return resp.Body, nil
}

func (d *OpenListDriver) ProbeURL(ctx context.Context, path string) (string, map[string]string, error) {
	return d.resolve(ctx, path)
}

func (d *OpenListDriver) HasDirect() bool { return true }
