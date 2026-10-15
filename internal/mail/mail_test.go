package mail

import (
	"mime"
	"strings"
	"testing"

	"doriyamedia/internal/mailtest"
)

// A plain message reaches the envelope and the body it was built from, and a
// Chinese subject survives the trip.
func TestSendDeliversAMessage(t *testing.T) {
	fake := mailtest.New(t)
	host, port := fake.HostPort(t)
	s := New(Config{
		Host: host, Port: port, Encryption: "none",
		From: "robot@example.com", FromName: "DoriyaMedia 影音",
	})

	subject := "验证你的邮箱地址"
	if err := s.Send("someone@example.net", subject, "<p>你好</p>", "你好"); err != nil {
		t.Fatalf("send: %v", err)
	}

	msg := fake.Last(t)
	if msg.From != "robot@example.com" {
		t.Errorf("envelope from = %q", msg.From)
	}
	if msg.To != "someone@example.net" {
		t.Errorf("envelope to = %q", msg.To)
	}
	if msg.Subject != subject {
		t.Errorf("subject = %q, want %q", msg.Subject, subject)
	}
	// Both alternatives must decode back to exactly what was handed in.
	if msg.HTML != "<p>你好</p>" {
		t.Errorf("html part = %q", msg.HTML)
	}
	if msg.Text != "你好" {
		t.Errorf("text part = %q", msg.Text)
	}
	for _, want := range []string{
		"multipart/alternative",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Type: text/html; charset=UTF-8",
		"MIME-Version: 1.0",
	} {
		if !strings.Contains(msg.Raw, want) {
			t.Errorf("message missing %q", want)
		}
	}
	// The subject must have travelled as an encoded word, not raw bytes.
	if rawSubject := headerValue(msg.Raw, "Subject:"); !strings.Contains(rawSubject, "=?") {
		t.Errorf("Chinese subject was sent raw: %q", rawSubject)
	}
	if name, err := new(mime.WordDecoder).DecodeHeader(headerValue(msg.Raw, "From:")); err != nil ||
		!strings.Contains(name, "DoriyaMedia 影音") {
		t.Errorf("From display name did not survive: %q (err %v)", name, err)
	}
}

func headerValue(raw, name string) string {
	lines := strings.Split(raw, "\r\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, name) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, name))
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

// Port 587 upgrades with STARTTLS and only then authenticates.
func TestSendUsesStartTLSThenAuthenticates(t *testing.T) {
	fake := mailtest.NewStartTLS(t)
	host, port := fake.HostPort(t)
	s := New(Config{
		Host: host, Port: port, Encryption: "starttls",
		Username: mailtest.Username, Password: mailtest.Password,
		From:       mailtest.Username,
		SkipVerify: true, // the test server's certificate is self-signed
	})
	if err := s.Send("someone@example.net", "hi", "<p>hi</p>", "hi"); err != nil {
		t.Fatalf("send over STARTTLS: %v", err)
	}
	user, pass, usedTLS := fake.Credentials()
	if !usedTLS {
		t.Error("the message was not sent over TLS")
	}
	if user != mailtest.Username || pass != mailtest.Password {
		t.Errorf("credentials = %q / %q", user, pass)
	}
	if msg := fake.Last(t); !strings.Contains(msg.HTML, "hi") {
		t.Error("body did not arrive")
	}
}

// Port 465 is encrypted from the first byte, greeting included.
func TestSendUsesImplicitTLS(t *testing.T) {
	fake := mailtest.NewImplicitTLS(t)
	host, port := fake.HostPort(t)
	s := New(Config{
		Host: host, Port: port, Encryption: "tls",
		Username: mailtest.Username, Password: mailtest.Password,
		From: mailtest.Username, SkipVerify: true,
	})
	if err := s.Send("someone@example.net", "hi", "<p>hi</p>", "hi"); err != nil {
		t.Fatalf("send over implicit TLS: %v", err)
	}
	if _, _, usedTLS := fake.Credentials(); !usedTLS {
		t.Error("the connection was not encrypted")
	}
	if msg := fake.Last(t); !strings.Contains(msg.HTML, "hi") {
		t.Error("body did not arrive")
	}
}

// Choosing STARTTLS against a server that does not offer it is a configuration
// mistake worth naming, because sending anyway would expose the password.
func TestStartTLSRefusedWhenServerDoesNotOfferIt(t *testing.T) {
	fake := mailtest.New(t)
	host, port := fake.HostPort(t)
	s := New(Config{Host: host, Port: port, Encryption: "starttls", From: "robot@example.com"})
	err := s.Send("someone@example.net", "hi", "<p>hi</p>", "hi")
	if err == nil {
		t.Fatal("STARTTLS was skipped silently")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("error does not name the real problem: %v", err)
	}
	if fake.Sent() != 0 {
		t.Error("the message was sent anyway")
	}
}

// A wrong password is reported as an authentication problem, not as a generic
// failure the owner cannot act on.
func TestSendReportsBadCredentials(t *testing.T) {
	fake := mailtest.NewStartTLS(t)
	host, port := fake.HostPort(t)
	s := New(Config{
		Host: host, Port: port, Encryption: "starttls",
		Username: mailtest.Username, Password: "wrong", From: mailtest.Username,
		SkipVerify: true,
	})
	err := s.Send("someone@example.net", "hi", "<p>hi</p>", "hi")
	if err == nil {
		t.Fatal("a wrong password was accepted")
	}
	if !strings.Contains(err.Error(), "认证") {
		t.Errorf("error does not name the real problem: %v", err)
	}
}

func TestSendWithoutConfigurationExplainsItself(t *testing.T) {
	s := New(Config{})
	if s.Enabled() {
		t.Error("an empty config reports itself as enabled")
	}
	if err := s.Send("someone@example.net", "hi", "hi", "hi"); err == nil {
		t.Error("sending without a host succeeded")
	}
	if err := New(Config{Host: "127.0.0.1", Port: 1}).Send("a@b.c", "hi", "hi", "hi"); err == nil {
		t.Error("sending to a closed port succeeded")
	}
}

// A long Chinese subject has to be cut into encoded words that each fit the
// 76-character line limit, and must still decode back to exactly the original.
func TestEncodeHeaderKeepsLinesShort(t *testing.T) {
	long := strings.Repeat("这是一封很长的中文主题,", 12)
	encoded := encodeHeader(long)
	for _, line := range strings.Split(encoded, "\r\n ") {
		if len(line) > 76 {
			t.Errorf("encoded word is %d characters, over the 76 limit: %q", len(line), line)
		}
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded != long {
		t.Errorf("long subject round-trip failed:\n got %q\nwant %q", decoded, long)
	}
	// Plain ASCII passes through untouched so ordinary headers stay readable.
	if got := encodeHeader("DoriyaMedia"); got != "DoriyaMedia" {
		t.Errorf("ASCII header was re-encoded: %q", got)
	}
}
