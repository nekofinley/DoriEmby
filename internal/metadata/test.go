package metadata

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"doriyamedia/internal/config"
)

// apiKeyRe masks credentials that stdlib errors embed via the request URL.
var apiKeyRe = regexp.MustCompile(`(api_key=)[^&"'\s]+`)

// TestTMDBResult is the outcome of a live TMDB connectivity check.
type TestTMDBResult struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	Proxy     string `json:"proxy,omitempty"`
	ViaProxy  bool   `json:"via_proxy"`
	LatencyMS int64  `json:"latency_ms"`
	Results   int    `json:"results"`
}

// TestTMDB runs one real TMDB search with the given settings, so the settings
// page can tell "key wrong" apart from "network/proxy not working".
// apiBase/imgBase/apiKey may be empty → the saved config values are used.
func TestTMDB(ctx context.Context, saved config.TMDB, apiKey, apiBase, imgBase, proxyAddr string, useProxy bool) TestTMDBResult {
	res := TestTMDBResult{}

	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		apiKey = saved.APIKey
	}
	if strings.TrimSpace(apiBase) == "" {
		apiBase = saved.APIBase
	}
	if strings.TrimSpace(imgBase) == "" {
		imgBase = saved.ImgBase
	}
	if strings.TrimSpace(proxyAddr) == "" {
		proxyAddr = saved.Proxy
	}
	if !useProxy {
		proxyAddr = ""
	}
	res.Proxy = proxyAddr
	res.ViaProxy = proxyAddr != ""

	if apiKey == "" {
		res.Message = "还没填 API Key(可填 v3 密钥或 v4 读访问令牌)"
		return res
	}

	// fail fast with a clear message when the local proxy isn't even up
	if err := CheckProxyReachable(proxyAddr, 4*time.Second); err != nil {
		res.Message = err.Error()
		return res
	}

	client, err := HTTPClientFor(proxyAddr, 25*time.Second)
	if err != nil {
		res.Message = err.Error()
		return res
	}
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}

	t := NewTMDBClient(apiKey, saved.Language, apiBase, imgBase, client)
	start := time.Now()
	items, err := t.SearchMovie(ctx, "test", 0)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Message = "请求失败: " + kindNetError(err)
		return res
	}
	res.OK = true
	res.Results = len(items)
	where := "直连"
	if res.ViaProxy {
		where = "经代理 " + proxyAddr
	}
	res.Message = fmt.Sprintf("连接成功(%s,耗时 %dms,返回 %d 条结果)", where, res.LatencyMS, len(items))
	return res
}

// TestBGMResult is the outcome of a live Bangumi connectivity check.
type TestBGMResult struct {
	OK        bool     `json:"ok"`
	Message   string   `json:"message"`
	Proxy     string   `json:"proxy,omitempty"`
	ViaProxy  bool     `json:"via_proxy"`
	LatencyMS int64    `json:"latency_ms"`
	Results   int      `json:"results"`
	Hits      []string `json:"hits,omitempty"`
}

// TestBGM runs one real Bangumi search so the settings page can tell a broken
// proxy/DNS apart from an empty result. Keyword defaults to a well-known anime.
func TestBGM(ctx context.Context, saved config.BGMTV, token, apiBase, keyword, proxyAddr string, useProxy bool) TestBGMResult {
	res := TestBGMResult{}

	token = strings.TrimSpace(token)
	if token == "" {
		token = saved.Token
	}
	if strings.TrimSpace(apiBase) == "" {
		apiBase = saved.APIBase
	}
	if strings.TrimSpace(proxyAddr) == "" {
		proxyAddr = saved.Proxy
	}
	if !useProxy {
		proxyAddr = ""
	}
	res.Proxy = proxyAddr
	res.ViaProxy = proxyAddr != ""

	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		keyword = "孤独摇滚"
	}
	if keyword == "test" {
		keyword = "孤独摇滚"
	}

	// fail fast with a clear message when the local proxy isn't even up
	if err := CheckProxyReachable(proxyAddr, 4*time.Second); err != nil {
		res.Message = err.Error()
		return res
	}

	client, err := HTTPClientFor(proxyAddr, 25*time.Second)
	if err != nil {
		res.Message = err.Error()
		return res
	}
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}

	b := NewBangumiClient(token, apiBase, client)
	start := time.Now()
	subs, err := b.Search(ctx, keyword, BgmTypeAnime)
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Message = "请求失败: " + kindBGMBError(err)
		return res
	}
	res.OK = true
	res.Results = len(subs)
	for _, s := range subs {
		if len(res.Hits) >= 5 {
			break
		}
		label := preferCN(s.NameCN, s.Name)
		if s.Year > 0 {
			label = fmt.Sprintf("%s (%d)", label, s.Year)
		}
		res.Hits = append(res.Hits, label)
	}
	where := "直连"
	if res.ViaProxy {
		where = "经代理 " + proxyAddr
	}
	if token != "" {
		where += ",已带令牌"
	}
	res.Message = fmt.Sprintf("连接成功(%s,耗时 %dms,搜索「%s」返回 %d 条)", where, res.LatencyMS, keyword, len(subs))
	if b.NoAuthFallback {
		res.Message += " — 注意:填的令牌被拒绝(401),已自动改用公开接口,建议清空令牌"
	}
	if len(subs) == 0 {
		res.Message += " — 接口通了但没结果,换个关键词试试"
	}
	return res
}

// kindBGMBError adds Bangumi-specific hints (its API rejects requests without a
// User-Agent, and api.bgm.tv is often DNS-poisoned in mainland China).
func kindBGMBError(err error) string {
	s := err.Error()
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "connection refused"):
		return "连接被拒绝(代理未启动或端口不对)— " + s
	case strings.Contains(low, "timeout"), strings.Contains(low, "deadline exceeded"):
		return "超时(api.bgm.tv 在国内常被墙/DNS 污染,请开启代理)— " + s
	case strings.Contains(low, "no such host"), strings.Contains(low, "dns"):
		return "域名解析失败(被污染,请开启代理)— " + s
	case strings.Contains(low, "403"):
		return "被拒绝(403,可能是缺少 User-Agent 或触发了限流)— " + s
	case strings.Contains(low, "certificate"):
		return "TLS 证书问题(可能被中间人劫持,请开代理)— " + s
	default:
		return s
	}
}

// kindNetError turns transport noise into a hint the user can act on.
func kindNetError(err error) string {
	s := apiKeyRe.ReplaceAllString(err.Error(), "api_key=***")
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "connection refused"):
		return "连接被拒绝(代理未启动或端口不对)— " + s
	case strings.Contains(low, "timeout"), strings.Contains(low, "deadline exceeded"):
		return "超时(国内直连 TMDB 基本不通,请开启代理)— " + s
	case strings.Contains(low, "no such host"), strings.Contains(low, "dns"):
		return "域名解析失败(可能被污染,请开启代理)— " + s
	case strings.Contains(low, "401"):
		return "API Key 无效(401)— " + s
	case strings.Contains(low, "certificate"):
		return "TLS 证书问题(可能被中间人劫持,请开代理)— " + s
	default:
		return s
	}
}
