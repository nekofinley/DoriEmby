package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	key := SessionKey(42, "remux", 0, 0, 1080, 0)
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
	sess, err := e.EnsureSession(ctx, SessionKey(1, "remux", 0, 0, 1080, 0), PackagerOptions{
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

// requireSeekableSource builds a source with real keyframes, so seeking has
// something to land on. The committed fixtures carry a single keyframe, which
// makes every -ss snap back to zero and would hide the bug this guards.
func requireSeekableSource(t *testing.T, dir string, seconds int) string {
	t.Helper()
	src := filepath.Join(dir, "src.mp4")
	out, err := exec.Command("ffmpeg", "-y", "-v", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=size=320x240:rate=25:duration=%d", seconds),
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "50", "-keyint_min", "50",
		"-sc_threshold", "0", "-pix_fmt", "yuv420p", src).CombinedOutput()
	if err != nil {
		t.Fatalf("build test source: %v: %s", err, out)
	}
	return src
}

// A session that begins mid-file must still describe itself in absolute terms:
// segment N covers N*segLen, and the media it serves must be timestamped at that
// position — otherwise a player places a resumed stream at zero.
func TestSessionStartingMidFile(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := requireSeekableSource(t, dir, 40)

	e := newTestEngine(t, nil)
	e.Cfg.DataDir = t.TempDir()
	defer e.CloseSessions()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const startSeg = 2
	const segLen = 4.0
	opts := PackagerOptions{
		Target: src, VideoIndex: 0, AudioOrdinal: -1,
		VideoMode: "copy", VideoCodec: "h264",
		SegmentSeconds: segLen, StartSeconds: startSeg * segLen, StartSegment: startSeg,
	}
	sess, err := e.EnsureSession(ctx, SessionKey(9, "remux", 0, 0, 1080, startSeg), opts)
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := sess.WaitForFile(ctx, sess.PlaylistPath(), 30*time.Second); err != nil {
		t.Fatalf("waiting for playlist: %v", err)
	}
	pl, err := os.ReadFile(sess.PlaylistPath())
	if err != nil {
		t.Fatalf("read playlist: %v", err)
	}
	if !strings.Contains(string(pl), "#EXT-X-MEDIA-SEQUENCE:2") {
		t.Errorf("playlist must keep absolute numbering:\n%s", pl)
	}
	if !strings.Contains(string(pl), "seg00002.m4s") {
		t.Errorf("segments must keep absolute naming:\n%s", pl)
	}

	segPath := sess.SegmentPath(startSeg)
	if err := sess.WaitForFile(ctx, segPath, 30*time.Second); err != nil {
		t.Fatalf("waiting for the first segment: %v", err)
	}
	raw := readFileT(t, segPath)

	// The premise: ffmpeg rebased the timeline to zero.
	if v := tfdtValues(raw); len(v) == 0 || v[0] != 0 {
		t.Fatalf("expected the raw segment to start at 0, got %v", v)
	}

	initBytes := readFileT(t, sess.InitPath())
	comb := filepath.Join(dir, "shifted.mp4")
	if err := os.WriteFile(comb, append(append([]byte(nil), initBytes...), sess.ShiftSegment(raw)...), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := firstVideoPTS(t, comb); got < 7.8 || got > 8.2 {
		t.Errorf("served segment starts at %.3fs, want ~8s (its absolute position)", got)
	}
}

// A different start position is a different packaging job, not a cache hit.
func TestSessionKeyIncludesStart(t *testing.T) {
	if SessionKey(1, "remux", 0, 0, 1080, 0) == SessionKey(1, "remux", 0, 0, 1080, 2) {
		t.Fatal("sessions with different start positions must not share an identity")
	}
}
