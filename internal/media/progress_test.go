package media

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// Segment generation progress is what the player renders as a progress bar, so
// the counters must reflect reality: successes add timeline seconds, failures do
// not, and the realtime factor is generated seconds per wall second.
func TestStreamStatusTracksGeneration(t *testing.T) {
	drv := &stubDriver{}
	e := newTestEngine(t, nil)
	e.resetStreamProgress()
	// Spend a measurable amount of work per segment so the throughput figure has
	// real inputs; an instantaneous fake would divide by ~0.
	e.runStream = func(_ context.Context, _ []string, out io.Writer) error {
		time.Sleep(20 * time.Millisecond)
		_, err := out.Write([]byte("segment"))
		return err
	}

	file := testFile()
	if st := e.StreamStatus(file.ID); st != nil {
		t.Fatalf("no activity must report nil, got %+v", st)
	}

	if _, err := e.Segment(context.Background(), file, drv, 0, 4, "transcode", "aac", 0, 0, 1080, io.Discard); err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if _, err := e.Segment(context.Background(), file, drv, 4, 4, "transcode", "aac", 0, 0, 1080, io.Discard); err != nil {
		t.Fatalf("Segment: %v", err)
	}

	st := e.StreamStatus(file.ID)
	if st == nil {
		t.Fatal("progress must be reported after a segment")
	}
	if st.SegmentsRequested != 2 || st.SegmentsReady != 2 {
		t.Fatalf("requested=%d ready=%d, want 2/2", st.SegmentsRequested, st.SegmentsReady)
	}
	if st.GeneratedSeconds != 8 {
		t.Fatalf("generated=%.1fs, want 8s", st.GeneratedSeconds)
	}
	if st.LastSegmentBytes != int64(len("segment")) {
		t.Fatalf("last bytes=%d, want %d", st.LastSegmentBytes, len("segment"))
	}
	// 8s of timeline from ~40ms of work: far above realtime, and finite.
	if st.RealtimeFactor <= 1 {
		t.Fatalf("realtime factor=%.2f, want > 1 (work is much faster than playback)", st.RealtimeFactor)
	}
	if st.LastSegmentMs < 15 {
		t.Fatalf("last segment took %dms, expected the fake's ~20ms to be recorded", st.LastSegmentMs)
	}
}

func TestStreamStatusCountsFailuresWithoutGenerating(t *testing.T) {
	drv := &stubDriver{}
	e := newTestEngine(t, nil)
	e.resetStreamProgress()
	e.runStream = func(context.Context, []string, io.Writer) error {
		return errors.New("403 Forbidden")
	}

	file := testFile()
	if _, err := e.Segment(context.Background(), file, drv, 0, 4, "transcode", "aac", 0, 0, 1080, io.Discard); err == nil {
		t.Fatal("want an error")
	}

	st := e.StreamStatus(file.ID)
	if st == nil {
		t.Fatal("a failed attempt must still be reported")
	}
	if st.SegmentsReady != 0 {
		t.Fatalf("ready=%d, want 0", st.SegmentsReady)
	}
	if st.GeneratedSeconds != 0 {
		t.Fatalf("generated=%.1fs, want 0 (a failed segment produces no timeline)", st.GeneratedSeconds)
	}
	if st.SegmentsFailed == 0 {
		t.Fatal("the failure must be counted")
	}
	if st.LastSegmentBytes != 0 {
		t.Fatalf("last bytes=%d, want 0", st.LastSegmentBytes)
	}
}

// A fresh play (or a seek) after a long gap starts a new burst, so the progress
// bar does not start out already full.
func TestStreamStatusResetsAfterIdleGap(t *testing.T) {
	drv := &stubDriver{}
	e := newTestEngine(t, nil)
	e.resetStreamProgress()
	e.runStream = func(_ context.Context, _ []string, out io.Writer) error {
		_, err := out.Write([]byte("x"))
		return err
	}

	file := testFile()
	if _, err := e.Segment(context.Background(), file, drv, 0, 4, "transcode", "aac", 0, 0, 1080, io.Discard); err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if got := e.StreamStatus(file.ID).GeneratedSeconds; got != 4 {
		t.Fatalf("generated=%.1f, want 4", got)
	}

	// Age the burst past the idle threshold, as a new play would.
	e.streamMu.Lock()
	e.stream[file.ID].lastAt = time.Now().Add(-2 * burstIdle)
	e.streamMu.Unlock()

	if _, err := e.Segment(context.Background(), file, drv, 600, 4, "transcode", "aac", 0, 0, 1080, io.Discard); err != nil {
		t.Fatalf("Segment: %v", err)
	}
	st := e.StreamStatus(file.ID)
	if st.GeneratedSeconds != 4 {
		t.Fatalf("generated=%.1f after a new burst, want 4", st.GeneratedSeconds)
	}
	if st.SegmentsRequested != 1 {
		t.Fatalf("requested=%d after a new burst, want 1", st.SegmentsRequested)
	}
}
