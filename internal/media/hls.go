package media

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"doriyamedia/internal/config"
	"doriyamedia/internal/db"
	"doriyamedia/internal/models"
	"doriyamedia/internal/source"
)

// Engine bundles probe/ffmpeg tooling with the store for probe caching.
type Engine struct {
	Store  *db.Store
	Cfg    *config.Config
	prober *Prober
	ffmpeg *FFMpeg

	subMu    sync.Mutex
	subCache map[string][]byte
	subOrder []string

	resolveMu    sync.Mutex
	resolveCache map[string]resolveEntry
}

type resolveEntry struct {
	target  string
	headers map[string]string
	at      time.Time
}

func NewEngine(store *db.Store, cfg *config.Config) *Engine {
	return &Engine{
		Store:        store,
		Cfg:          cfg,
		prober:       NewProber(cfg.FFProbe),
		ffmpeg:       &FFMpeg{Path: cfg.FFmpeg},
		subCache:     map[string][]byte{},
		resolveCache: map[string]resolveEntry{},
	}
}

// SetBins updates tool paths after config changes (hot reload).
func (e *Engine) SetBins(ffmpeg, ffprobe string) {
	e.prober = NewProber(ffprobe)
	e.ffmpeg = &FFMpeg{Path: ffmpeg}
}

// ProbeTarget resolves a file into an ffprobe-able target (cached, TTL 10 min).
func (e *Engine) ProbeTarget(ctx context.Context, file *models.MediaFile, drv source.Driver) (string, map[string]string, error) {
	key := fmt.Sprintf("%d|%s", file.SourceID, file.Path)
	e.resolveMu.Lock()
	if ent, ok := e.resolveCache[key]; ok && time.Since(ent.at) < 10*time.Minute {
		e.resolveMu.Unlock()
		return ent.target, ent.headers, nil
	}
	e.resolveMu.Unlock()

	if drv == nil {
		var err error
		drv, err = e.DriverFor(file)
		if err != nil {
			return "", nil, err
		}
	}
	target, headers, err := drv.ProbeURL(ctx, file.Path)
	if err != nil {
		return "", nil, err
	}
	e.resolveMu.Lock()
	if len(e.resolveCache) > 512 {
		e.resolveCache = map[string]resolveEntry{}
	}
	e.resolveCache[key] = resolveEntry{target: target, headers: headers, at: time.Now()}
	e.resolveMu.Unlock()
	return target, headers, nil
}

// DriverFor resolves the right driver for a file (URL files use URLDriver).
func (e *Engine) DriverFor(file *models.MediaFile) (source.Driver, error) {
	if isHTTPPath(file.Path) {
		return &source.URLDriver{}, nil
	}
	src, err := e.Store.SourceByID(file.SourceID)
	if err != nil {
		return nil, err
	}
	return source.NewDriver(src)
}

func isHTTPPath(p string) bool {
	return strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://")
}

// EnsureProbe returns a cached probe or probes the file (and persists result).
func (e *Engine) EnsureProbe(ctx context.Context, file *models.MediaFile, drv source.Driver) (*Probe, error) {
	if file.Probe != "" {
		var p Probe
		if err := jsonUnmarshal(file.Probe, &p); err == nil && (p.Video() != nil || len(p.Streams) == 0) {
			return &p, nil
		}
	}
	target, headers, err := e.ProbeTarget(ctx, file, drv)
	if err != nil {
		return nil, err
	}
	probe, err := e.prober.ProbeFile(ctx, target, headers)
	if err != nil {
		return nil, err
	}
	raw, _ := jsonMarshal(probe)
	_ = e.Store.UpdateFileProbe(file.ID, string(raw))
	file.Probe = string(raw)
	e.RegisterEmbeddedSubs(file, probe)
	return probe, nil
}

func (e *Engine) RegisterEmbeddedSubs(file *models.MediaFile, probe *Probe) {
	for i, s := range probe.SubtitleStreams() {
		conv := false
		switch s.CodecName {
		case "subrip", "ass", "ssa", "mov_text", "webvtt", "text":
			conv = true
		}
		sub := &models.Subtitle{
			FileID:      file.ID,
			Lang:        s.Language(),
			Title:       s.Title(),
			Format:      s.CodecName,
			Embedded:    true,
			StreamIdx:   s.Index,
			Convertible: conv,
		}
		_ = e.Store.UpsertSubtitle(sub)
		_ = i
	}
}

// ---------- HLS segment generation ----------

// Segment writes one MPEG-TS HLS segment for the file.
// start: seconds into the media; length: nominal segment length.
// videoMode: remux (copy) | transcode (h264). audioMode: copy | aac.
// audioOrdinal: which audio stream (ordinal among audio streams, -1 = none).
// videoIndex: absolute ffmpeg stream index of the real video stream
// (cover-art attachments excluded by the prober); -1 falls back to 0:v:0.
func (e *Engine) Segment(ctx context.Context, file *models.MediaFile, drv source.Driver,
	start, length float64, videoMode, audioMode string, audioOrdinal, videoIndex, transcodeHeight int, out io.Writer) error {

	target, headers, err := e.ProbeTarget(ctx, file, drv)
	if err != nil {
		return err
	}
	args := []string{"-hide_banner", "-loglevel", "error"}
	args = HeaderArgs(args, headers)
	if isHTTPPath(target) {
		// reconnect helps mid-segment CDN hiccups for http sources
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1")
	}
	args = append(args, "-ss", fmt.Sprintf("%.3f", start), "-i", target)

	// Map by absolute index: 0:v:0 would pick an attached cover image when
	// present, producing garbage/error segments.
	if videoIndex >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:%d", videoIndex))
	} else {
		args = append(args, "-map", "0:v:0")
	}
	if audioOrdinal >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", audioOrdinal))
	} else {
		args = append(args, "-an")
	}

	if videoMode == "transcode" {
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "21")
		// 10-bit sources (HEVC Main10 / Hi10P) must be converted to 8-bit
		// yuv420p — MSE hardware paths cannot handle anything else.
		args = append(args, "-pix_fmt", "yuv420p")
		args = append(args, "-vf", fmt.Sprintf("scale=-2:min(ih\\,%d)", transcodeHeight))
		args = append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
	} else {
		args = append(args, "-c:v", "copy")
		if audioMode == "aac" {
			args = append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
		} else {
			args = append(args, "-c:a", "copy")
		}
	}
	args = append(args,
		"-avoid_negative_ts", "make_zero",
		"-f", "mpegts",
		"-t", fmt.Sprintf("%.3f", length),
		"pipe:1",
	)
	return e.ffmpeg.RunStream(ctx, args, out)
}

// Playlist renders the VOD HLS playlist for a file.
func (e *Engine) Playlist(basePath string, sigQuery string, duration, segLen float64) string {
	if segLen <= 0 {
		segLen = 4
	}
	if duration <= 0 {
		duration = segLen
	}
	n := int(duration/segLen + 0.999999)
	if n < 1 {
		n = 1
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:3\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", int(segLen+0.999))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	for i := 0; i < n; i++ {
		d := segLen
		if i == n-1 {
			d = duration - float64(i)*segLen
			if d <= 0 {
				d = segLen
			}
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n", d)
		fmt.Fprintf(&b, "%s/%d%s\n", basePath, i, sigQuery)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// ---------- subtitles ----------

// ExtractEmbeddedSubtitle pulls one embedded text subtitle as WebVTT.
func (e *Engine) ExtractEmbeddedSubtitle(ctx context.Context, file *models.MediaFile, drv source.Driver, streamIndex int) ([]byte, error) {
	key := fmt.Sprintf("%d|%d", file.ID, streamIndex)
	e.subMu.Lock()
	if b, ok := e.subCache[key]; ok {
		e.subMu.Unlock()
		return b, nil
	}
	e.subMu.Unlock()

	target, headers, err := e.ProbeTarget(ctx, file, drv)
	if err != nil {
		return nil, err
	}
	// ordinal of the stream among subtitle streams
	probe, err := e.EnsureProbe(ctx, file, drv)
	if err != nil {
		return nil, err
	}
	ordinal := -1
	for i, s := range probe.SubtitleStreams() {
		if s.Index == streamIndex {
			ordinal = i
			break
		}
	}
	if ordinal < 0 {
		return nil, fmt.Errorf("subtitle stream not found")
	}

	var buf strings.Builder
	args := []string{"-hide_banner", "-loglevel", "error"}
	args = HeaderArgs(args, headers)
	args = append(args, "-i", target, "-map", fmt.Sprintf("0:s:%d", ordinal), "-f", "webvtt", "pipe:1")
	if err := e.ffmpeg.RunStream(ctx, args, writerFunc(buf.WriteString)); err != nil {
		return nil, err
	}
	data := []byte(buf.String())
	e.subMu.Lock()
	if len(e.subOrder) >= 64 {
		delete(e.subCache, e.subOrder[0])
		e.subOrder = e.subOrder[1:]
	}
	e.subCache[key] = data
	e.subOrder = append(e.subOrder, key)
	e.subMu.Unlock()
	return data, nil
}

func writerFunc(fn func(string) (int, error)) io.Writer {
	return writerFn{fn}
}

type writerFn struct {
	fn func(string) (int, error)
}

func (w writerFn) Write(p []byte) (int, error) {
	return w.fn(string(p))
}
