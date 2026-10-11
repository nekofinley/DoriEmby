package metadata

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

var (
	clientMu    sync.Mutex
	clientCache = map[string]*http.Client{}
)

// HTTPClientFor builds (and caches) an HTTP client that dials through
// proxyURL when non-empty:
//   - socks5://host:port  → SOCKS5 dialer (hostnames are sent to the proxy,
//     so DNS is resolved proxy-side, like socks5h)
//   - http://host:port    → standard HTTP CONNECT proxy
//
// An empty proxyURL yields a plain client (still honouring HTTP(S)_PROXY).
func HTTPClientFor(proxyURL string, timeout time.Duration) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	key := fmt.Sprintf("%s|%s", proxyURL, timeout)
	clientMu.Lock()
	defer clientMu.Unlock()
	if c, ok := clientCache[key]; ok {
		return c, nil
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
	}

	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("代理地址无效: %q(示例 socks5://127.0.0.1:7890)", proxyURL)
		}
		switch strings.ToLower(u.Scheme) {
		case "socks5", "socks5h", "socks":
			var auth *proxy.Auth
			if u.User != nil {
				pw, _ := u.User.Password()
				auth = &proxy.Auth{User: u.User.Username(), Password: pw}
			}
			dialer, err := proxy.SOCKS5("tcp", u.Host, auth, &net.Dialer{
				Timeout:   15 * time.Second,
				KeepAlive: 30 * time.Second,
			})
			if err != nil {
				return nil, fmt.Errorf("SOCKS 代理初始化失败: %w", err)
			}
			if cd, ok := dialer.(proxy.ContextDialer); ok {
				transport.DialContext = cd.DialContext
			} else {
				transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					type res struct {
						c net.Conn
						e error
					}
					ch := make(chan res, 1)
					go func() {
						c, e := dialer.Dial(network, addr)
						ch <- res{c, e}
					}()
					select {
					case r := <-ch:
						return r.c, r.e
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
			}
			transport.Proxy = nil // SOCKS 已在 dialer 层处理
		case "http", "https":
			transport.Proxy = http.ProxyURL(u)
		default:
			return nil, fmt.Errorf("不支持的代理协议 %q(可用 socks5:// 或 http://)", u.Scheme)
		}
	}

	c := &http.Client{Timeout: timeout, Transport: transport}
	clientCache[key] = c
	return c, nil
}

// CheckProxyReachable dials the proxy endpoint itself so a missing proxy
// gives "代理没启动" instead of a cryptic TLS/timeout error.
func CheckProxyReachable(proxyURL string, timeout time.Duration) error {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("代理地址无效: %q(示例 socks5://127.0.0.1:7890)", proxyURL)
	}
	host := u.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "1080")
	}
	conn, err := net.DialTimeout("tcp", host, timeout)
	if err != nil {
		return fmt.Errorf("连不上本地代理 %s(请确认代理软件已启动)", host)
	}
	_ = conn.Close()
	return nil
}
