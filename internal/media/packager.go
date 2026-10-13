package media

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// packager.go: one long-lived ffmpeg per playback session, packaging fMP4 HLS
// into a directory.
//
// Why a long-lived process instead of the on-demand per-segment approach in
// hls.go (which stays the default until this is wired up and verified):
//
//   - Apple does not decode HEVC carried in MPEG-TS, so HEVC for Apple needs
//     fMP4. fMP4 needs a cumulative media timeline (`tfdt`), and ffmpeg rebases
//     `tfdt` to zero for every independently started process — only its own HLS
//     muxer writes a correct init segment plus a continuous timeline.
//   - One process reads the source sequentially once, instead of re-seeking and
//     re-reading a probe window for every 4-second segment.
//
// Verified recipe: docs/ios-safari-playback-redesign.md §11.3.1 (ffmpeg 8.0.1).
// It encodes two traps that cost real debugging time:
//
//   - `-hls_fmp4_init_filename` resolves against the *process* working
//     directory, not the playlist path. Rather than sprinkle absolute paths
//     (which then leak into the playlist URIs), the child runs with its working
//     directory set to the output directory and uses plain relative names.
//   - ffmpeg writes the `hev1` sample entry by default, which Apple rejects;
//     copying HEVC needs `-tag:v hvc1`.
type PackagerOptions struct {
	// FFmpeg is the ffmpeg binary (empty = "ffmpeg" from PATH).
	FFmpeg string
	// Target is what ffmpeg reads: an http(s) URL or a filesystem path.
	Target  string
	Headers map[string]string

	VideoIndex   int // absolute stream index; <0 falls back to 0:v:0
	AudioOrdinal int // ordinal among audio streams; <0 means no audio
	VideoMode    string
	AudioMode    string
	VideoCodec   string // source codec; decides whether the hvc1 tag is applied
	AudioCodec   string

	TranscodeHeight int
	SegmentSeconds  float64

	// StartSeconds makes packaging begin here instead of at the start of the
	// file, so resuming a playback does not have to package everything ahead of
	// the viewer's position first.
	//
	// ffmpeg rebases the output timeline to zero even when seeking, so segments
	// from a session that began mid-file carry timestamps relative to the
	// session; Session.ShiftSegment moves them back onto the absolute timeline.
	StartSeconds float64
	// StartSegment is this session's first segment number. ffmpeg writes it as
	// #EXT-X-MEDIA-SEQUENCE, which keeps segment N meaning "N*SegmentSeconds into
	// the file" wherever the session started.
	StartSegment int

	Dir string // output directory; must already exist
}

// Packager is a running (or finished) fMP4 HLS packaging session.
type Packager struct {
	dir      string
	playlist string
	initPath string
	args     []string

	cmd  *exec.Cmd
	done chan struct{}
	mu   sync.Mutex
	err  error
	tail []string // last stderr lines, for diagnostics
}

const packagerErrTail = 8

func (o PackagerOptions) ffmpegPath() string {
	if strings.TrimSpace(o.FFmpeg) == "" {
		return "ffmpeg"
	}
	return o.FFmpeg
}

// StartPackager launches ffmpeg to package the target into o.Dir.
// The caller owns ctx: cancelling it terminates the packaging process.
func StartPackager(ctx context.Context, o PackagerOptions) (*Packager, error) {
	if strings.TrimSpace(o.Dir) == "" {
		return nil, fmt.Errorf("packager: output directory is required")
	}
	if fi, err := os.Stat(o.Dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("packager: output directory %s is not usable: %v", o.Dir, err)
	}

	args, err := packagerArgs(o)
	if err != nil {
		return nil, err
	}

	p := &Packager{
		dir:      o.Dir,
		playlist: filepath.Join(o.Dir, "index.m3u8"),
		initPath: filepath.Join(o.Dir, "init.mp4"),
		args:     args,
		done:     make(chan struct{}),
	}
	cmd := exec.CommandContext(ctx, o.ffmpegPath(), args...)
	// The relative output names below are resolved against the child's working
	// directory — see the note about the init filename at the top of this file.
	cmd.Dir = o.Dir
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("packager: start ffmpeg: %w", err)
	}
	p.cmd = cmd

	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 32<<10), 1<<20)
		for sc.Scan() {
			p.mu.Lock()
			if len(p.tail) >= packagerErrTail {
				p.tail = p.tail[1:]
			}
			p.tail = append(p.tail, sc.Text())
			p.mu.Unlock()
		}
	}()

	go func() {
		defer close(p.done)
		werr := cmd.Wait()
		p.mu.Lock()
		p.err = werr
		p.mu.Unlock()
	}()
	return p, nil
}

// packagerArgs builds the ffmpeg command line for a packaging session.
func packagerArgs(o PackagerOptions) ([]string, error) {
	target := o.Target
	if strings.TrimSpace(target) == "" {
		return nil, fmt.Errorf("packager: empty target")
	}
	// The child's working directory is the output directory, so a local input
	// must be absolute to still resolve.
	if !isHTTPPath(target) {
		abs, err := filepath.Abs(target)
		if err != nil {
			return nil, fmt.Errorf("packager: resolve %s: %w", target, err)
		}
		target = abs
	}

	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	args = HeaderArgs(args, o.Headers)
	if isHTTPPath(target) {
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1")
	}
	// -ss belongs before -i (input seek): ffmpeg then starts reading near that
	// position instead of decoding from the beginning.
	if o.StartSeconds > 0 {
		args = append(args, "-ss", strconv.FormatFloat(o.StartSeconds, 'f', 3, 64))
	}
	args = append(args, "-i", target)

	if o.VideoIndex >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:%d", o.VideoIndex))
	} else {
		args = append(args, "-map", "0:v:0")
	}
	if o.AudioOrdinal >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", o.AudioOrdinal))
	} else {
		args = append(args, "-an")
	}

	if o.VideoMode == "transcode" {
		height := o.TranscodeHeight
		if height <= 0 {
			height = 1080
		}
		args = append(args,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "21",
			"-pix_fmt", "yuv420p",
			"-vf", fmt.Sprintf("scale=-2:min(ih\\,%d)", height),
		)
	} else {
		args = append(args, "-c:v", "copy")
		// Apple rejects the `hev1` sample entry ffmpeg writes by default.
		if o.VideoCodec == "hevc" {
			args = append(args, "-tag:v", "hvc1")
		}
	}

	if o.VideoMode == "transcode" || o.AudioMode == "aac" {
		args = append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
	} else {
		args = append(args, "-c:a", "copy")
	}

	seg := o.SegmentSeconds
	if seg <= 0 {
		seg = 4
	}
	// -start_number keeps the numbering on the absolute timeline, so a session
	// that began at segment K still calls its first segment K.
	if o.StartSegment > 0 {
		args = append(args, "-start_number", strconv.Itoa(o.StartSegment))
	}
	args = append(args,
		"-f", "hls",
		"-hls_segment_type", "fmp4",
		"-hls_time", strconv.FormatFloat(seg, 'f', -1, 64),
		// event: the playlist grows while packaging runs, so a player can start
		// on the first segments instead of waiting for the whole file.
		"-hls_playlist_type", "event",
		"-hls_list_size", "0",
		"-hls_fmp4_init_filename", packagerInitName,
		"-hls_segment_filename", packagerSegmentPattern,
		packagerPlaylistName,
	)
	return args, nil
}

// The names ffmpeg is told to write, and therefore the names a session — live or
// adopted from a previous run's cache — finds in its directory.
const (
	packagerPlaylistName    = "index.m3u8"
	packagerInitName        = "init.mp4"
	packagerSegmentPattern  = "seg%05d.m4s"
)

// Dir is the directory holding the packaged output.
func (p *Packager) Dir() string { return p.dir }

// InitPath is the fMP4 initialization segment.
func (p *Packager) InitPath() string { return p.initPath }

// PlaylistPath is ffmpeg's own media playlist.
func (p *Packager) PlaylistPath() string { return p.playlist }

// Args exposes the ffmpeg command line (used by tests and diagnostics).
func (p *Packager) Args() []string { return append([]string(nil), p.args...) }

// Done is closed once the packaging process has exited.
func (p *Packager) Done() <-chan struct{} { return p.done }

// Err is the process error, valid only after Done is closed.
func (p *Packager) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Stderr returns the last captured stderr lines, for diagnosing a failure.
func (p *Packager) Stderr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.tail, " | ")
}

// Wait blocks until packaging finishes and reports the failure with context.
func (p *Packager) Wait() error {
	<-p.done
	if err := p.Err(); err != nil {
		if tail := p.Stderr(); tail != "" {
			return fmt.Errorf("packager: %w: %s", err, tail)
		}
		return fmt.Errorf("packager: %w", err)
	}
	return nil
}

// Playlist returns the current media playlist bytes. While packaging runs,
// ffmpeg rewrites this file in place, so a caller may observe a partial file;
// retry on parse trouble rather than on read trouble.
func (p *Packager) Playlist() ([]byte, error) {
	return os.ReadFile(p.playlist)
}

// SegmentPath is the file for segment index i.
func (p *Packager) SegmentPath(i int) string {
	return filepath.Join(p.dir, fmt.Sprintf("seg%05d.m4s", i))
}
