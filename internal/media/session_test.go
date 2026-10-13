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

// fakeSession registers a session with a chosen start and production point, so
// the selection rules can be tested without running ffmpeg.
func fakeSession(t *testing.T, e *Engine, fileID int64, start, produced int) *Session {
	t.Helper()
	dir := t.TempDir()
	for i := start; i <= produced; i++ {
		name := filepath.Join(dir, fmt.Sprintf("seg%05d.m4s", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed segment: %v", err)
		}
	}
	s := &Session{key: SessionKey(fileID, "remux", 0, 0, 1080, start), dir: dir, lastUsed: time.Now()}
	e.sessMu.Lock()
	e.sessions[s.key] = s
	e.sessMu.Unlock()
	return s
}

// Which session serves a request is what decides whether a seek works. Inside
// the packaged window an existing session answers; far outside it the caller has
// to start a new one, because waiting would be slower than packaging afresh.
func TestSessionSelectionForSegment(t *testing.T) {
	e := newTestEngine(t, nil)
	e.Cfg.DataDir = t.TempDir()

	early := fakeSession(t, e, 7, 0, 20)    // started at the top, 20 segments in hand
	late := fakeSession(t, e, 7, 10, 30)    // started at segment 10
	other := fakeSession(t, e, 8, 0, 30)    // a different file
	_ = other

	// Inside a session's window: the latest session that had already begun wins.
	if got := e.SessionForSegment(7, "remux", 0, 0, 1080, 15, SessionSeekAhead); got != late {
		t.Errorf("segment 15 should be served by the session that started at 10")
	}
	if got := e.SessionForSegment(7, "remux", 0, 0, 1080, 5, SessionSeekAhead); got != early {
		t.Errorf("segment 5 predates the late session, so the early one must serve it")
	}
	// A seek far past what is packaged cannot be served by waiting.
	if got := e.SessionForSegment(7, "remux", 0, 0, 1080, 500, SessionSeekAhead); got != nil {
		t.Errorf("a request far ahead must not attach to a session that cannot reach it")
	}
	// Nor can a position before every session's start.
	e2 := newTestEngine(t, nil)
	e2.Cfg.DataDir = t.TempDir()
	fakeSession(t, e2, 7, 10, 20)
	if got := e2.SessionForSegment(7, "remux", 0, 0, 1080, 3, SessionSeekAhead); got != nil {
		t.Errorf("a request before a session's start must not attach to it")
	}
	// Buffering (just past production) must NOT look like a seek, or a slow
	// transcode would restart the packager on every request.
	if got := e.SessionForSegment(7, "remux", 0, 0, 1080, 25, SessionSeekAhead); got != late {
		t.Errorf("a request just past production must keep waiting on the live session")
	}
	// Sessions of another file or mode are never borrowed.
	if got := e.SessionForSegment(7, "transcode", 0, 0, 1080, 15, SessionSeekAhead); got != nil {
		t.Errorf("another mode's session must not be reused")
	}
	if got := e.SessionForSegment(9, "remux", 0, 0, 1080, 15, SessionSeekAhead); got != nil {
		t.Errorf("another file's session must not be reused")
	}
}

// The playlist follows whatever session is actually packaging, so a refreshed
// playlist after a seek does not resurrect the abandoned position.
func TestSessionForStreamFollowsLatestUse(t *testing.T) {
	e := newTestEngine(t, nil)
	e.Cfg.DataDir = t.TempDir()

	first := fakeSession(t, e, 7, 0, 5)
	second := fakeSession(t, e, 7, 40, 45)
	first.lastUsed = time.Now().Add(-time.Minute)
	if got := e.SessionForStream(7, "remux", 0, 0, 1080); got != second {
		t.Fatal("the most recently used session must answer the playlist")
	}
	if got := e.SessionForStream(7, "transcode", 0, 0, 1080); got != nil {
		t.Fatal("another mode has no session")
	}

	// Closing the abandoned ones leaves exactly the live session.
	e.CloseStreamSessions(7, "remux", 0, 0, 1080, second)
	if got := e.SessionForStream(7, "remux", 0, 0, 1080); got != second {
		t.Fatal("the kept session must survive")
	}
	e.sessMu.Lock()
	n := len(e.sessions)
	e.sessMu.Unlock()
	if n != 1 {
		t.Fatalf("expected only the kept session to remain, got %d", n)
	}
}
