package media

import (
	"time"
)

// Segment generation progress.
//
// Preparing HLS for a cloud-drive file can take a minute or more: for every
// segment the server must reach the source, seek, decode and (for transcode)
// re-encode. The player used to show nothing but a spinner for all of it, which
// is indistinguishable from a hang. These counters let the player show how much
// timeline is actually ready, and — more usefully — whether the server can
// encode faster than realtime, i.e. whether playback will ever catch up.

// burstIdle: a request this long after the previous one starts a new burst
// (a fresh play or a seek), so the progress counters restart.
const burstIdle = 15 * time.Second

type streamProgress struct {
	burstStart time.Time
	lastAt     time.Time

	requested int
	ready     int
	failed    int

	generatedSeconds float64 // timeline seconds produced in this burst
	busySeconds      float64 // wall time spent producing them
	lastMs           int64
	lastBytes        int64
}

// StreamStatus is the observable segment-generation progress for one file.
type StreamStatus struct {
	SegmentsRequested int     `json:"segments_requested"`
	SegmentsReady     int     `json:"segments_ready"`
	SegmentsFailed    int     `json:"segments_failed"`
	GeneratedSeconds  float64 `json:"generated_seconds"`
	// RealtimeFactor is timeline seconds produced per second of segment work
	// (reaching the source, seeking, decoding, re-encoding). Below 1 a segment
	// takes longer to produce than it plays, so playback cannot keep up however
	// long it buffers — that is the number the user needs to see.
	//
	// Deliberately measured against busy time, not wall time: with on-demand
	// generation, wall time also includes idle stretches while the player is
	// comfortably buffered, which would understate throughput badly.
	RealtimeFactor   float64 `json:"realtime_factor"`
	LastSegmentMs    int64   `json:"last_segment_ms"`
	LastSegmentBytes int64   `json:"last_segment_bytes"`
	// IdleSeconds since the last segment request: the player uses it to hide the
	// progress UI once generation has stopped.
	IdleSeconds float64 `json:"idle_seconds"`
}

// noteSegmentStart records that a segment request is in flight.
func (e *Engine) noteSegmentStart(fileID int64) {
	now := time.Now()
	e.streamMu.Lock()
	defer e.streamMu.Unlock()
	if e.stream == nil {
		e.stream = map[int64]*streamProgress{}
	}
	p, ok := e.stream[fileID]
	if !ok || now.Sub(p.lastAt) > burstIdle {
		p = &streamProgress{burstStart: now}
		if len(e.stream) > 256 {
			e.stream = map[int64]*streamProgress{}
		}
		e.stream[fileID] = p
	}
	p.lastAt = now
	p.requested++
}

// noteSegmentDone records the outcome of one segment attempt.
func (e *Engine) noteSegmentDone(fileID int64, length float64, took time.Duration, n int64, err error) {
	e.streamMu.Lock()
	defer e.streamMu.Unlock()
	p := e.stream[fileID]
	if p == nil {
		return
	}
	p.lastMs = took.Milliseconds()
	p.lastBytes = n
	p.busySeconds += took.Seconds()
	if err != nil {
		p.failed++
		return
	}
	p.ready++
	p.generatedSeconds += length
}

// StreamStatus reports segment-generation progress, or nil when the file has no
// recent activity (so callers can omit it).
func (e *Engine) StreamStatus(fileID int64) *StreamStatus {
	e.streamMu.Lock()
	defer e.streamMu.Unlock()
	p, ok := e.stream[fileID]
	if !ok {
		return nil
	}
	idle := time.Since(p.lastAt).Seconds()
	if idle > 5*60 {
		return nil
	}
	st := &StreamStatus{
		SegmentsRequested: p.requested,
		SegmentsReady:     p.ready,
		SegmentsFailed:    p.failed,
		GeneratedSeconds:  p.generatedSeconds,
		LastSegmentMs:     p.lastMs,
		LastSegmentBytes:  p.lastBytes,
		IdleSeconds:       idle,
	}
	if p.busySeconds > 0 {
		st.RealtimeFactor = p.generatedSeconds / p.busySeconds
	}
	return st
}

// resetStreamProgress drops the counters for a file (used by tests).
func (e *Engine) resetStreamProgress() {
	e.streamMu.Lock()
	defer e.streamMu.Unlock()
	e.stream = map[int64]*streamProgress{}
}
