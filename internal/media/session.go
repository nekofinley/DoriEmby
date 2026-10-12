package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// session.go: per-playback packaging sessions (P2, phase 1).
//
// A session is one long-lived ffmpeg (see packager.go) packaging a file into
// <data>/cache/hls/<key>/. It is created on the first HLS request for a given
// file + parameters and torn down once idle, which also removes its directory —
// so disk use is bounded by the sessions actually in flight, not by the library.
//
// Only HEVC *copy* for Apple is routed here: that is the case that cannot work
// as MPEG-TS (Apple does not decode HEVC from TS) and needs fMP4 with a
// continuous timeline. Everything else keeps using the on-demand TS segmenter,
// which needs no temporary disk at all.

const (
	// sessionIdleTTL: a session with no requests for this long is closed and its
	// directory removed.
	sessionIdleTTL = 90 * time.Second
	// sessionFileWait bounds how long one HTTP request waits for a segment that
	// ffmpeg has not produced yet.
	sessionFileWait = 45 * time.Second
)

var reSessionSegment = regexp.MustCompile(`^seg\d+\.m4s$`)

// ParseSessionSegment extracts the index from a "segNNNNN.m4s" resource name
// (the naming ffmpeg's HLS muxer is given in packager.go).
func ParseSessionSegment(rest string) (int, bool) {
	if !reSessionSegment.MatchString(rest) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rest, "seg"), ".m4s"))
	if err != nil {
		return 0, false
	}
	return n, true
}

type sessionKey struct {
	fileID int64
	mode   string
	audio  int
	vindex int
	height int
}

// SessionKey identifies one packaging session. The type is deliberately
// unexported: callers obtain a value here and hand it straight back to
// EnsureSession.
func SessionKey(fileID int64, mode string, audio, vindex, height int) sessionKey {
	return sessionKey{fileID: fileID, mode: mode, audio: audio, vindex: vindex, height: height}
}

func (k sessionKey) dirName() string {
	return fmt.Sprintf("%d-%s-a%d-v%d-h%d", k.fileID, k.mode, k.audio, k.vindex, k.height)
}

// Session is one packaging session and its output directory.
type Session struct {
	key    sessionKey
	dir    string
	pack   *Packager
	cancel context.CancelFunc

	mu       sync.Mutex
	lastUsed time.Time
	closed   bool
}

// Touch records that the session was just used.
func (s *Session) Touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

func (s *Session) idleFor() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastUsed)
}

// Dir is the session's output directory.
func (s *Session) Dir() string { return s.dir }

// InitPath is the fMP4 initialization segment path.
func (s *Session) InitPath() string { return s.pack.InitPath() }

// PlaylistPath is ffmpeg's own media playlist path.
func (s *Session) PlaylistPath() string { return s.pack.PlaylistPath() }

// SegmentPath is the file path for segment index i.
func (s *Session) SegmentPath(i int) string { return s.pack.SegmentPath(i) }

// Failed reports a packaging failure that has already happened.
func (s *Session) Failed() error {
	select {
	case <-s.pack.Done():
		return s.pack.Err()
	default:
		return nil
	}
}

// WaitForFile blocks until path exists with non-zero size, the packaging process
// exits without producing it, or ctx/max elapses. Waiting (instead of answering
// 404) keeps the player from treating a not-yet-packaged segment as fatal.
func (s *Session) WaitForFile(ctx context.Context, path string, max time.Duration) error {
	deadline := time.Now().Add(max)
	for {
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			s.Touch()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.pack.Done():
			if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
				s.Touch()
				return nil
			}
			if err := s.pack.Err(); err != nil {
				return fmt.Errorf("packaging %s failed: %w (%s)", filepath.Base(path), err, s.pack.Stderr())
			}
			return fmt.Errorf("packaging ended before %s was written", filepath.Base(path))
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s", filepath.Base(path))
		}
	}
}

// close stops the packager and removes the session directory.
func (s *Session) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}
	// Give the child a moment to die before removing its output, otherwise it
	// may recreate a file inside a directory we are deleting.
	select {
	case <-s.pack.Done():
	case <-time.After(3 * time.Second):
	}
	_ = os.RemoveAll(s.dir)
}

// EnsureSession returns the live session for key, starting one if needed.
func (e *Engine) EnsureSession(ctx context.Context, key sessionKey, opts PackagerOptions) (*Session, error) {
	e.gcSessions()

	e.sessMu.Lock()
	if s, ok := e.sessions[key]; ok {
		e.sessMu.Unlock()
		s.Touch()
		return s, nil
	}
	e.sessMu.Unlock()

	// The packager needs an existing directory and runs with it as its working
	// directory (see packager.go for why).
	dir := filepath.Join(e.Cfg.Abs(), "cache", "hls", key.dirName())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	opts.Dir = dir

	// Detach from the request context: the session must outlive the HTTP request
	// that happened to start it.
	sessCtx, cancel := context.WithCancel(context.Background())
	p, err := StartPackager(sessCtx, opts)
	if err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return nil, err
	}

	s := &Session{key: key, dir: dir, pack: p, cancel: cancel, lastUsed: time.Now()}

	e.sessMu.Lock()
	// Another request may have won the race; keep only one session per key.
	if existing, ok := e.sessions[key]; ok {
		e.sessMu.Unlock()
		s.close()
		existing.Touch()
		return existing, nil
	}
	e.sessions[key] = s
	e.sessMu.Unlock()
	return s, nil
}

// gcSessions closes sessions that have been idle for a while.
func (e *Engine) gcSessions() {
	e.sessMu.Lock()
	var dead []*Session
	for k, s := range e.sessions {
		if s.idleFor() > sessionIdleTTL || s.Failed() != nil {
			dead = append(dead, s)
			delete(e.sessions, k)
		}
	}
	e.sessMu.Unlock()
	for _, s := range dead {
		s.close()
	}
}

// CloseSessions stops every session (used on shutdown).
func (e *Engine) CloseSessions() {
	e.sessMu.Lock()
	all := make([]*Session, 0, len(e.sessions))
	for k, s := range e.sessions {
		all = append(all, s)
		delete(e.sessions, k)
	}
	e.sessMu.Unlock()
	for _, s := range all {
		s.close()
	}
}

// RewriteSessionPlaylist points ffmpeg's own playlist at our signed HTTP
// endpoints. ffmpeg writes `init.mp4` and `segNNNNN.m4s` because the child runs
// with the session directory as its working directory (see packager.go); the
// player must instead fetch them through /api/hls.
func RewriteSessionPlaylist(pl, base, sig string) string {
	lines := strings.Split(pl, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if strings.HasPrefix(trimmed, "#EXT-X-MAP:") {
				lines[i] = `#EXT-X-MAP:URI="` + base + `/init.mp4` + sig + `"`
			}
			continue
		}
		if reSessionSegment.MatchString(trimmed) {
			lines[i] = base + "/" + trimmed + sig
		}
	}
	return strings.Join(lines, "\n")
}
