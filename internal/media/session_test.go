package media

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// The player must never see ffmpeg's own relative URIs: it fetches everything
// through /api/hls, with the same signature as the playlist.
func TestRewriteSessionPlaylist(t *testing.T) {
	in := "#EXTM3U\n" +
		"#EXT-X-VERSION:7\n" +
		"#EXT-X-MAP:URI=\"init.mp4\"\n" +
		"#EXTINF:4.000000,\n" +
		"seg00000.m4s\n" +
		"#EXTINF:4.000000,\n" +
		"seg00001.m4s\n" +
		"#EXT-X-ENDLIST\n"
	got := RewriteSessionPlaylist(in, "/api/hls/7", "?exp=1&sig=abc")

	for _, want := range []string{
		`#EXT-X-MAP:URI="/api/hls/7/init.mp4?exp=1&sig=abc"`,
		"/api/hls/7/seg00000.m4s?exp=1&sig=abc",
		"/api/hls/7/seg00001.m4s?exp=1&sig=abc",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rewritten playlist missing %q:\n%s", want, got)
		}
	}
	// ffmpeg's own relative names must be gone.
	for _, unwanted := range []string{"\nseg00000.m4s", `URI="init.mp4"`} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("rewritten playlist still contains %q:\n%s", unwanted, got)
		}
	}
	// Tags other than EXT-X-MAP must be left alone.
	if !strings.Contains(got, "#EXTINF:4.000000,") {
		t.Fatalf("EXTINF was altered:\n%s", got)
	}
}

func TestParseSessionSegment(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"seg00000.m4s", 0, true},
		{"seg00042.m4s", 42, true},
		{"seg9.m4s", 9, true},
		{"init.mp4", 0, false},
		{"seg.m4s", 0, false},
		{"segabc.m4s", 0, false},
		{"index.m3u8", 0, false},
		{"seg00001", 0, false},
	}
	for _, c := range cases {
		got, ok := ParseSessionSegment(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("ParseSessionSegment(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// End to end through the session layer: a real packager is started, the
// playlist is waited for, the init segment is fetched and must be hvc1, and
// closing the session must remove its directory (that is what bounds disk use).
func TestSessionLifecycle(t *testing.T) {
	requireFFmpeg(t)
	e := newTestEngine(t, nil)
	e.Cfg.DataDir = t.TempDir() // never write into the package directory

	key := SessionKey(42, "remux", 0, 0, 1080)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sess, err := e.EnsureSession(ctx, key, PackagerOptions{
		Target:       testMKV,
		VideoIndex:   0,
		AudioOrdinal: 0,
		VideoMode:    "copy",
		AudioMode:    "aac",
		VideoCodec:   "hevc",
	})
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if sess.Dir() == "" || !strings.Contains(sess.Dir(), "cache") {
		t.Fatalf("unexpected session dir %q", sess.Dir())
	}

	// A second call must reuse the same session, not spawn another packager.
	again, err := e.EnsureSession(ctx, key, PackagerOptions{Target: testMKV})
	if err != nil {
		t.Fatalf("EnsureSession (again): %v", err)
	}
	if again != sess {
		t.Fatal("EnsureSession started a second session for the same key")
	}

	if err := sess.WaitForFile(ctx, sess.PlaylistPath(), 30*time.Second); err != nil {
		t.Fatalf("waiting for playlist: %v", err)
	}
	pl, err := os.ReadFile(sess.PlaylistPath())
	if err != nil {
		t.Fatalf("read playlist: %v", err)
	}
	if !strings.Contains(string(pl), "#EXT-X-MAP") {
		t.Fatalf("playlist has no EXT-X-MAP:\n%s", pl)
	}
	if !strings.Contains(RewriteSessionPlaylist(string(pl), "/api/hls/42", "?s=1"), "/api/hls/42/seg") {
		t.Fatalf("rewritten playlist has no signed segment URIs:\n%s", pl)
	}

	if err := sess.WaitForFile(ctx, sess.InitPath(), 30*time.Second); err != nil {
		t.Fatalf("waiting for init segment: %v", err)
	}
	initBytes, err := os.ReadFile(sess.InitPath())
	if err != nil {
		t.Fatalf("read init: %v", err)
	}
	if !strings.Contains(string(initBytes), "hvc1") {
		t.Error("session init segment is not hvc1")
	}

	if err := sess.WaitForFile(ctx, sess.SegmentPath(0), 30*time.Second); err != nil {
		t.Fatalf("waiting for first segment: %v", err)
	}

	dir := sess.Dir()
	e.CloseSessions()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("closing the session must remove its directory %s (stat err=%v)", dir, err)
	}
}

// Waiting for a resource that will never appear must not hang forever.
func TestSessionWaitForFileTimesOut(t *testing.T) {
	requireFFmpeg(t)
	e := newTestEngine(t, nil)
	e.Cfg.DataDir = t.TempDir()
	defer e.CloseSessions()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sess, err := e.EnsureSession(ctx, SessionKey(1, "remux", 0, 0, 1080), PackagerOptions{
		Target: testMKV, VideoIndex: 0, AudioOrdinal: 0, VideoMode: "copy", AudioMode: "aac", VideoCodec: "hevc",
	})
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	start := time.Now()
	err = sess.WaitForFile(ctx, sess.SegmentPath(999999), 700*time.Millisecond)
	if err == nil {
		t.Fatal("want a timeout error for a segment that is never produced")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout was not honoured (took %v)", elapsed)
	}
}
