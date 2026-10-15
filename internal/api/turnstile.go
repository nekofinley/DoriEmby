package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"doriyamedia/internal/metadata"
)

// turnstileSiteverify is the endpoint that checks a widget token. It is a
// variable so tests can point it at a local stub instead of the internet.
var turnstileSiteverify = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// verifyTurnstile checks the token the browser got from the Cloudflare widget.
// It is called before the password is even looked at, which is the point: a
// script has to solve a challenge for every guess.
//
// A network failure is reported as a failure, never as a pass. That is
// deliberate but worth knowing: if challenges.cloudflare.com cannot be reached
// (mainland China without a proxy, most often), nobody can sign in until the
// setting is turned off in config.json.
func (s *Server) verifyTurnstile(r *http.Request, token string) error {
	if !s.Cfg.TurnstileEnabled() {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("请先完成人机验证")
	}

	form := url.Values{}
	form.Set("secret", s.Cfg.Turnstile.SecretKey)
	form.Set("response", token)
	if ip := clientIP(r); ip != "" {
		form.Set("remoteip", ip)
	}

	proxyURL := ""
	if s.Cfg.Turnstile.UseProxy {
		proxyURL = s.Cfg.Turnstile.Proxy
	}
	client, err := metadata.HTTPClientFor(proxyURL, 10*time.Second)
	if err != nil {
		return fmt.Errorf("人机验证无法连接: %w", err)
	}
	resp, err := client.PostForm(turnstileSiteverify, form)
	if err != nil {
		return errors.New("连接 Cloudflare 人机验证服务失败,请检查服务器网络或代理设置")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("人机验证服务返回 HTTP %d", resp.StatusCode)
	}

	var out struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return errors.New("人机验证服务返回了无法解析的响应")
	}
	if out.Success {
		return nil
	}
	return fmt.Errorf("人机验证未通过: %s", turnstileErrorText(out.ErrorCodes))
}

// turnstileErrorText turns Cloudflare's codes into something an admin can act
// on, since the most common ones are configuration mistakes rather than a
// visitor failing the challenge.
func turnstileErrorText(codes []string) string {
	if len(codes) == 0 {
		return "请重新验证"
	}
	parts := make([]string, 0, len(codes))
	for _, c := range codes {
		switch c {
		case "missing-input-secret", "invalid-input-secret":
			parts = append(parts, "服务端密钥不正确(请检查 Secret Key)")
		case "missing-input-response", "invalid-input-response":
			parts = append(parts, "验证已失效,请重新点击验证")
		case "timeout-or-duplicate":
			parts = append(parts, "验证已使用或超时,请重新验证")
		case "bad-request":
			parts = append(parts, "请求格式有误")
		case "internal-error":
			parts = append(parts, "Cloudflare 服务暂时不可用")
		default:
			parts = append(parts, c)
		}
	}
	return strings.Join(parts, "; ")
}
