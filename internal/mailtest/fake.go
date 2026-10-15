// Package mailtest provides a mail server just real enough for tests: EHLO, an
// optional TLS upgrade, an optional AUTH, then the envelope and the data. It
// lets the send path be exercised — TLS, authentication, header encoding, the
// links inside a message — without a mailbox or a network.
//
// Only test binaries import it.
package mailtest

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type Mode int

const (
	// Plain sends everything in the clear, like a relay on localhost.
	Plain Mode = iota
	// StartTLS advertises the upgrade and authenticates afterwards.
	StartTLS
	// ImplicitTLS encrypts from the first byte, like port 465.
	ImplicitTLS
)

// Message is one delivered mail, already decoded the way a client would show
// it, so tests can assert on the link a person would actually click.
type Message struct {
	From    string
	To      string
	Subject string
	Text    string
	HTML    string
	Raw     string
}

type Server struct {
	ln        net.Listener
	mode      Mode
	tlsConfig *tls.Config
	requireA  bool

	mu       sync.Mutex
	messages []Message
	authUser string
	authPass string
	usedTLS  bool
}

// New starts a server that accepts anything.
func New(t *testing.T) *Server { return start(t, Plain, false) }

// NewStartTLS starts one that requires STARTTLS and the credentials
// robot@example.com / app-password.
func NewStartTLS(t *testing.T) *Server { return start(t, StartTLS, true) }

// NewImplicitTLS starts one that is encrypted from the first byte and requires
// the same credentials.
func NewImplicitTLS(t *testing.T) *Server { return start(t, ImplicitTLS, true) }

// Username and Password are the credentials the authenticating servers accept.
const (
	Username = "robot@example.com"
	Password = "app-password"
)

func start(t *testing.T, mode Mode, requireAuth bool) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mailtest: listen: %v", err)
	}
	s := &Server{ln: ln, mode: mode, requireA: requireAuth}
	if mode != Plain {
		s.tlsConfig = selfSignedCert(t)
	}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

// HostPort is what a mail.Sender config needs.
func (s *Server) HostPort(t *testing.T) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(s.ln.Addr().String())
	if err != nil {
		t.Fatalf("mailtest: split addr: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("mailtest: parse port %q: %v", portStr, err)
	}
	return host, port
}

// Messages returns everything delivered so far, oldest first.
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// Last returns the most recent message.
func (s *Server) Last(t *testing.T) Message {
	t.Helper()
	all := s.Messages()
	if len(all) == 0 {
		t.Fatal("mailtest: no message was delivered")
	}
	return all[len(all)-1]
}

// Sent reports how many messages arrived, which is how a test checks that a
// form did *not* turn into mail.
func (s *Server) Sent() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

// Credentials returns what was presented and whether TLS was in use, so a test
// can prove the password did not cross the wire in the clear.
func (s *Server) Credentials() (user, pass string, usedTLS bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authUser, s.authPass, s.usedTLS
}

// LinkWithPrefix finds the first URL in the last message that starts with
// prefix — the verification or reset link under test.
func (s *Server) LinkWithPrefix(t *testing.T, prefix string) string {
	t.Helper()
	msg := s.Last(t)
	for _, field := range []string{msg.Text, msg.HTML} {
		for _, word := range strings.FieldsFunc(field, func(r rune) bool {
			return r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == '<' || r == '>' || r == '"' || r == ')'
		}) {
			if strings.HasPrefix(word, prefix) {
				return strings.TrimRight(word, ".,;")
			}
		}
	}
	t.Fatalf("mailtest: no link starting with %q in:\n%s", prefix, msg.Text)
	return ""
}

func (s *Server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	if s.mode == ImplicitTLS {
		tlsConn := tls.Server(conn, s.tlsConfig)
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		conn = tlsConn
		s.mu.Lock()
		s.usedTLS = true
		s.mu.Unlock()
	}

	br := bufio.NewReader(conn)
	send := func(line string) { fmt.Fprintf(conn, "%s\r\n", line) }
	send("220 mailtest ESMTP")

	authed := false
	var from, to string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			send("250-mailtest")
			if s.mode == StartTLS && !s.usedTLS {
				send("250-STARTTLS")
			}
			send("250 AUTH PLAIN")

		case strings.HasPrefix(upper, "STARTTLS"):
			if s.mode != StartTLS {
				send("502 not supported")
				continue
			}
			send("220 ready")
			tlsConn := tls.Server(conn, s.tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			s.mu.Lock()
			s.usedTLS = true
			s.mu.Unlock()
			conn = tlsConn
			br = bufio.NewReader(conn)
			send = func(l string) { fmt.Fprintf(conn, "%s\r\n", l) }

		case strings.HasPrefix(upper, "AUTH PLAIN"):
			fields := strings.Fields(cmd)
			if len(fields) < 3 {
				send("501 bad auth")
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(fields[2])
			if err != nil {
				send("501 bad auth")
				continue
			}
			parts := strings.Split(string(raw), "\x00")
			if len(parts) != 3 {
				send("501 bad auth")
				continue
			}
			s.mu.Lock()
			s.authUser, s.authPass = parts[1], parts[2]
			s.mu.Unlock()
			if s.requireA && (parts[1] != Username || parts[2] != Password) {
				send("535 authentication failed")
				continue
			}
			authed = true
			send("235 authenticated")

		case strings.HasPrefix(upper, "MAIL FROM:"):
			if s.requireA && !authed {
				send("530 authentication required")
				continue
			}
			from = strings.Trim(strings.TrimSpace(cmd[len("MAIL FROM:"):]), "<>")
			send("250 ok")

		case strings.HasPrefix(upper, "RCPT TO:"):
			to = strings.Trim(strings.TrimSpace(cmd[len("RCPT TO:"):]), "<>")
			send("250 ok")

		case strings.HasPrefix(upper, "DATA"):
			send("354 end with <CRLF>.<CRLF>")
			var body strings.Builder
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					return
				}
				if line == ".\r\n" || line == ".\n" {
					break
				}
				// Undo dot-stuffing, as any real receiver would.
				if strings.HasPrefix(line, "..") {
					line = line[1:]
				}
				body.WriteString(line)
			}
			s.mu.Lock()
			s.messages = append(s.messages, decode(from, to, body.String()))
			s.mu.Unlock()
			send("250 queued")

		case strings.HasPrefix(upper, "QUIT"):
			send("221 bye")
			return

		default:
			send("250 ok")
		}
	}
}

// decode turns the wire format back into what a reader would see: headers
// unfolded and decoded, parts transfer-decoded.
func decode(from, to, raw string) Message {
	m := Message{From: from, To: to, Raw: raw}
	m.Subject = headerValue(raw, "Subject")
	if decoded, err := new(mime.WordDecoder).DecodeHeader(m.Subject); err == nil {
		m.Subject = decoded
	}
	m.Text = partBody(raw, "text/plain")
	m.HTML = partBody(raw, "text/html")
	return m
}

func headerValue(raw, name string) string {
	lines := strings.Split(raw, "\r\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, name+":") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, name+":"))
		for j := i + 1; j < len(lines); j++ {
			if !strings.HasPrefix(lines[j], " ") && !strings.HasPrefix(lines[j], "\t") {
				break
			}
			value += strings.TrimSpace(lines[j])
		}
		return value
	}
	return ""
}

// partBody decodes one MIME part. Bodies are quoted-printable, which is what
// keeps Chinese text intact through relays that predate 8BITMIME.
func partBody(raw, contentType string) string {
	for _, section := range strings.Split(raw, "\r\n--") {
		idx := strings.Index(section, "\r\n\r\n")
		if idx < 0 {
			continue
		}
		head, body := section[:idx], section[idx+4:]
		if !strings.Contains(head, contentType) {
			continue
		}
		body = strings.TrimSuffix(body, "\r\n")
		decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
		if err != nil {
			return body
		}
		return string(decoded)
	}
	return ""
}

// selfSignedCert makes the certificate the test server presents for 127.0.0.1.
func selfSignedCert(t *testing.T) *tls.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("mailtest: key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("mailtest: cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("mailtest: keypair: %v", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}
}
