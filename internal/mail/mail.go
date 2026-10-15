// Package mail sends the few messages this server needs: proving an address at
// signup, and letting someone who forgot a password back in. It speaks SMTP
// directly, so a self-hosted install depends on nothing but the mail account its
// owner already has.
package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/proxy"
)

// Dial and conversation budgets. A mail server that accepts a connection and
// then stalls must not pin an HTTP request open behind it.
const (
	dialTimeout  = 15 * time.Second
	writeTimeout = 45 * time.Second
)

// Config is the SMTP account to send through.
type Config struct {
	Host       string
	Port       int
	Username   string
	Password   string
	From       string
	FromName   string
	Encryption string // "starttls" | "tls" | "none"
	Proxy      string // "", socks5://host:port, http://host:port
	SkipVerify bool
}

type Sender struct{ cfg Config }

func New(cfg Config) *Sender { return &Sender{cfg: cfg} }

// Enabled reports whether there is enough configuration to try sending at all.
func (s *Sender) Enabled() bool { return strings.TrimSpace(s.cfg.Host) != "" }

// Send delivers one message to one recipient. htmlBody is what people actually
// read; textBody is the fallback for clients that refuse HTML.
func (s *Sender) Send(to, subject, htmlBody, textBody string) error {
	if !s.Enabled() {
		return errors.New("还没有配置 SMTP 服务器")
	}
	from := strings.TrimSpace(s.cfg.From)
	if from == "" {
		from = strings.TrimSpace(s.cfg.Username)
	}
	if from == "" {
		return errors.New("还没有配置发件人地址(填 SMTP 用户名即可)")
	}
	if strings.TrimSpace(to) == "" {
		return errors.New("收件人为空")
	}

	port := s.cfg.Port
	if port <= 0 {
		if s.cfg.Encryption == "tls" {
			port = 465
		} else {
			port = 587
		}
	}
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(port))

	conn, err := s.dial(addr)
	if err != nil {
		return fmt.Errorf("连接 %s 失败: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(writeTimeout))

	tlsCfg := &tls.Config{ServerName: s.cfg.Host, InsecureSkipVerify: s.cfg.SkipVerify}
	if s.cfg.Encryption == "tls" {
		// Port 465 wraps the whole conversation in TLS, so the greeting is
		// already encrypted.
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.Handshake(); err != nil {
			return fmt.Errorf("TLS 握手失败: %w", err)
		}
		conn = tlsConn
	}

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("SMTP 握手失败: %w", err)
	}
	defer c.Close()

	if s.cfg.Encryption == "starttls" {
		ok, _ := c.Extension("STARTTLS")
		if !ok {
			return errors.New("服务器不支持 STARTTLS:若它用 465 端口,请把加密方式改成「隐式 TLS」")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("STARTTLS 失败: %w", err)
		}
	}

	if s.cfg.Username != "" {
		// PlainAuth refuses to hand the password over an unencrypted connection
		// to anything but localhost — a deliberate guard, kept here so a
		// misconfigured port cannot leak the mailbox password.
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("SMTP 认证失败(用户名/密码或授权码不正确): %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("发件人被拒绝(通常要填和账号一致的地址): %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("收件人被拒绝: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("开始发送失败: %w", err)
	}
	if _, err := w.Write(s.buildMessage(from, to, subject, htmlBody, textBody)); err != nil {
		return fmt.Errorf("发送正文失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("发送正文失败: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("结束会话失败: %w", err)
	}
	return nil
}

// dial opens the TCP connection, through the configured proxy when there is one
// (Gmail's SMTP is unreachable from mainland China without it).
func (s *Sender) dial(addr string) (net.Conn, error) {
	dialer, err := s.dialer()
	if err != nil {
		return nil, err
	}
	if cd, ok := dialer.(proxy.ContextDialer); ok {
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		defer cancel()
		return cd.DialContext(ctx, "tcp", addr)
	}
	return dialer.Dial("tcp", addr)
}

func (s *Sender) dialer() (proxy.Dialer, error) {
	forward := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	raw := strings.TrimSpace(s.cfg.Proxy)
	if raw == "" {
		return forward, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("代理地址无效: %q(示例 socks5://127.0.0.1:7890)", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h", "socks":
		var auth *proxy.Auth
		if u.User != nil {
			pw, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: pw}
		}
		return proxy.SOCKS5("tcp", u.Host, auth, forward)
	case "http", "https":
		return &httpProxyDialer{proxyURL: u, forward: forward}, nil
	default:
		return nil, fmt.Errorf("不支持的代理协议 %q(可用 socks5:// 或 http://)", u.Scheme)
	}
}

// httpProxyDialer carries a non-HTTP connection through a plain HTTP proxy with
// the CONNECT method, the same way an HTTPS request does.
type httpProxyDialer struct {
	proxyURL *url.URL
	forward  *net.Dialer
}

func (d *httpProxyDialer) Dial(network, addr string) (net.Conn, error) {
	conn, err := d.forward.Dial(network, d.proxyURL.Host)
	if err != nil {
		return nil, fmt.Errorf("连不上代理 %s: %w", d.proxyURL.Host, err)
	}
	req := &http.Request{
		Method: "CONNECT",
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: http.Header{},
	}
	if u := d.proxyURL.User; u != nil {
		pw, _ := u.Password()
		cred := base64.StdEncoding.EncodeToString([]byte(u.Username() + ":" + pw))
		req.Header.Set("Proxy-Authorization", "Basic "+cred)
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("代理应答异常: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		conn.Close()
		return nil, fmt.Errorf("代理拒绝 CONNECT: %s", resp.Status)
	}
	resp.Body.Close()
	// ReadResponse may have buffered bytes of the tunnel already.
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

// bufferedConn serves the bytes bufio read ahead before falling back to the
// socket.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// buildMessage renders an RFC 5322 message: an alternative part so the reader's
// client picks HTML, and text for the ones that will not.
func (s *Sender) buildMessage(from, to, subject, htmlBody, textBody string) []byte {
	var b bytes.Buffer
	writeHeader(&b, "From", formatAddress(s.cfg.FromName, from))
	writeHeader(&b, "To", to)
	writeHeader(&b, "Subject", encodeHeader(subject))
	writeHeader(&b, "Date", time.Now().Format(time.RFC1123Z))
	writeHeader(&b, "Message-ID", fmt.Sprintf("<%s@%s>", randomHex(12), messageIDDomain(from)))
	b.WriteString("MIME-Version: 1.0\r\n")

	boundary := "dm-" + randomHex(12)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundary)
	writePart(&b, boundary, "text/plain; charset=UTF-8", textBody)
	writePart(&b, boundary, "text/html; charset=UTF-8", htmlBody)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func writePart(b *bytes.Buffer, boundary, contentType, body string) {
	fmt.Fprintf(b, "--%s\r\nContent-Type: %s\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, contentType)
	qp := quotedprintable.NewWriter(b)
	// quotedprintable writes CRLF already, but a plain \n in the source would
	// survive as a bare LF, which some relays reject.
	_, _ = qp.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")))
	_ = qp.Close()
	b.WriteString("\r\n")
}

func writeHeader(b *bytes.Buffer, name, value string) {
	fmt.Fprintf(b, "%s: %s\r\n", name, value)
}

// formatAddress renders `Display Name <user@host>`, encoding the name when it is
// not plain ASCII.
func formatAddress(name, addr string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return addr
	}
	return fmt.Sprintf("%s <%s>", encodeHeader(name), addr)
}

func messageIDDomain(from string) string {
	if i := strings.LastIndex(from, "@"); i >= 0 && i+1 < len(from) {
		return from[i+1:]
	}
	return "doriyamedia.local"
}

// encodeHeader keeps a header line inside the 76-character limit SMTP expects.
// Chinese text cannot be sent raw, and one long encoded word gets folded by
// relays into something clients show as mojibake, so it is cut into words that
// each fit.
func encodeHeader(s string) string {
	if isASCII(s) {
		return s
	}
	// Base64 encoded words expand 3 bytes to 4 characters and carry a
	// "=?UTF-8?B?" + "?=" frame, so 45 bytes of text lands at 72 characters.
	const maxBytes = 45
	var words []string
	var buf []byte
	flush := func() {
		if len(buf) > 0 {
			words = append(words, mime.BEncoding.Encode("UTF-8", string(buf)))
			buf = nil
		}
	}
	for _, r := range s {
		enc := utf8.RuneLen(r)
		if len(buf)+enc > maxBytes {
			flush()
		}
		buf = append(buf, string(r)...)
	}
	flush()
	// Continuation lines start with a space, which is how a folded header is
	// stitched back together.
	return strings.Join(words, "\r\n ")
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}
