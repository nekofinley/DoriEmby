package source

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// WebDAVDriver talks to a standard WebDAV server (Alist/OpenList dav, rclone serve webdav, ...).
// WebDAV links usually require auth headers, so direct 302 is disabled; playback proxies through
// the server. For 302 direct links use the "openlist" source type instead.
type WebDAVDriver struct {
	Base string
	User string
	Pass string
}

func NewWebDAV(base, user, pass string) *WebDAVDriver {
	return &WebDAVDriver{Base: strings.TrimSuffix(base, "/"), User: user, Pass: pass}
}

func (d *WebDAVDriver) auth() (string, bool) {
	if d.User == "" && d.Pass == "" {
		return "", false
	}
	return "Basic " + basicAuth(d.User, d.Pass), true
}

func (d *WebDAVDriver) newReq(ctx context.Context, method, u string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if a, ok := d.auth(); ok {
		req.Header.Set("Authorization", a)
	}
	return req, nil
}

type davMultistatus struct {
	Responses []struct {
		Href     string `xml:"href"`
		Propstat []struct {
			Prop struct {
				ContentLength int64  `xml:"getcontentlength"`
				LastModified  string `xml:"getlastmodified"`
				ResourceType  struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
			} `xml:"prop"`
			Status string `xml:"status"`
		} `xml:"propstat"`
	} `xml:"response"`
}

func (d *WebDAVDriver) basePath() string {
	u, err := url.Parse(d.Base)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(u.Path, "/")
}

// hrefToPath converts a response href (possibly url-encoded, absolute or relative)
// into a driver-space virtual path.
func (d *WebDAVDriver) hrefToPath(href string) (string, error) {
	if href == "" {
		return "", fmt.Errorf("empty href")
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		u, err := url.Parse(href)
		if err != nil {
			return "", err
		}
		href = u.Path
	}
	decoded, err := url.PathUnescape(href)
	if err != nil {
		decoded = href
	}
	if !strings.HasPrefix(decoded, "/") {
		decoded = "/" + decoded
	}
	decoded = strings.TrimSuffix(decoded, "/")
	if bp := d.basePath(); bp != "" && strings.HasPrefix(decoded, bp) {
		decoded = strings.TrimPrefix(decoded, bp)
	}
	if decoded == "" {
		decoded = "/"
	}
	return decoded, nil
}

func (d *WebDAVDriver) List(ctx context.Context, dir string) ([]Entry, error) {
	probeURL := joinURL(d.Base, dir)
	body := `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:"><d:prop><d:displayname/><d:getcontentlength/><d:getlastmodified/><d:resourcetype/></d:prop></d:propfind>`
	req, err := d.newReq(ctx, "PROPFIND", probeURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml")
	resp, err := httpDo(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("webdav: not found: %s", dir)
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("webdav: PROPFIND %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var ms davMultistatus
	if err := xml.Unmarshal(raw, &ms); err != nil {
		return nil, fmt.Errorf("webdav: bad PROPFIND response: %w", err)
	}
	self := strings.TrimSuffix(escapeURLPath(dir), "/")
	var out []Entry
	for _, r := range ms.Responses {
		p, err := d.hrefToPath(r.Href)
		if err != nil {
			continue
		}
		if strings.TrimSuffix(p, "/") == strings.TrimSuffix(dir, "/") {
			continue // self
		}
		name := p
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if name == "" {
			continue
		}
		var isDir bool
		var size int64
		var mtime int64
		for _, ps := range r.Propstat {
			if strings.Contains(ps.Status, "404") {
				continue
			}
			isDir = ps.Prop.ResourceType.Collection != nil
			size = ps.Prop.ContentLength
			if t, err := http.ParseTime(ps.Prop.LastModified); err == nil {
				mtime = t.Unix()
			}
			break
		}
		_ = self
		out = append(out, Entry{Name: name, Path: p, IsDir: isDir, Size: size, MTime: mtime})
	}
	return out, nil
}

func (d *WebDAVDriver) DirectURL(ctx context.Context, path string) (string, map[string]string, error) {
	return "", nil, ErrNoDirect
}

func (d *WebDAVDriver) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := d.newReq(ctx, "GET", joinURL(d.Base, path), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpDo(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("webdav: GET %s: %s", path, resp.Status)
	}
	return resp.Body, nil
}

func (d *WebDAVDriver) ProbeURL(ctx context.Context, path string) (string, map[string]string, error) {
	hdrs := map[string]string{}
	if a, ok := d.auth(); ok {
		hdrs["Authorization"] = a
	}
	return joinURL(d.Base, path), hdrs, nil
}

func (d *WebDAVDriver) HasDirect() bool { return false }
