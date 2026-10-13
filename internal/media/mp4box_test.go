package media

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// firstVideoPTS asks ffprobe for the first video packet timestamp of a file.
func firstVideoPTS(t *testing.T, path string) float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time", "-of", "csv=p=0",
		"-read_intervals", "%+#1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", filepath.Base(path), err)
	}
	fields := strings.Fields(strings.TrimSpace(strings.Split(string(out), "\n")[0]))
	if len(fields) == 0 {
		t.Fatalf("ffprobe returned no packets for %s", path)
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(fields[0], ","), 64)
	if err != nil {
		t.Fatalf("parse pts %q: %v", fields[0], err)
	}
	return v
}

// The patch has to be real: a shifted segment must play at the shifted time, not
// merely carry different bytes.
func TestShiftTfdtMovesMediaTimeline(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, err := StartPackager(ctx, PackagerOptions{
		Target: testMKV, VideoIndex: 0, AudioOrdinal: 0,
		VideoMode: "copy", AudioMode: "aac", VideoCodec: "hevc", Dir: dir,
	})
	if err != nil {
		t.Fatalf("StartPackager: %v", err)
	}
	if err := p.Wait(); err != nil {
		t.Fatalf("packaging: %v", err)
	}
	init := readFileT(t, p.InitPath())
	seg := readFileT(t, p.SegmentPath(0))

	tracks, err := ParseInitTracks(init)
	if err != nil {
		t.Fatalf("ParseInitTracks: %v", err)
	}
	if len(tracks) < 2 {
		t.Fatalf("expected video+audio tracks, got %+v", tracks)
	}
	for _, tr := range tracks {
		if tr.Timescale == 0 || tr.ID == 0 {
			t.Fatalf("track with no id/timescale: %+v", tr)
		}
	}

	const offset = 8.0
	orig := append([]byte(nil), seg...)
	before := tfdtValues(seg)

	shifted, patched, err := ShiftTfdt(seg, tracks, offset)
	if err != nil {
		t.Fatalf("ShiftTfdt: %v", err)
	}
	if patched != len(tracks) {
		t.Errorf("patched %d tfdt boxes, want one per track (%d)", patched, len(tracks))
	}
	if !bytes.Equal(seg, orig) {
		t.Error("ShiftTfdt modified its input in place")
	}

	after := tfdtValues(shifted)
	if len(after) != len(before) {
		t.Fatalf("tfdt count changed: %d -> %d", len(before), len(after))
	}
	// ffmpeg writes one traf per track, in track order.
	for i := range after {
		want := uint64(offset*float64(tracks[i].Timescale) + 0.5)
		if got := after[i] - before[i]; got != want {
			t.Errorf("track %d (%d Hz) tfdt moved by %d, want %d", i, tracks[i].Timescale, got, want)
		}
	}

	// Unshifted, the segment starts at zero — this is what breaks a resumed
	// session, so assert the premise too.
	if len(before) == 0 || before[0] != 0 {
		t.Errorf("expected the fresh segment to start at 0, got %v", before)
	}

	// The decisive check: the media really is at 8s now.
	comb := filepath.Join(dir, "shifted.mp4")
	if err := os.WriteFile(comb, append(append([]byte(nil), init...), shifted...), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := firstVideoPTS(t, comb); got < offset-0.15 || got > offset+0.15 {
		t.Errorf("first video pts = %.3fs, want ~%.1fs", got, offset)
	}

	// And it still decodes.
	if out, err := exec.Command("ffmpeg", "-v", "error", "-i", comb, "-frames:v", "1", "-f", "null", "-").CombinedOutput(); err != nil {
		t.Fatalf("shifted segment does not decode: %v: %s", err, out)
	}

	// A zero offset must be a no-op.
	same, n, err := ShiftTfdt(seg, tracks, 0)
	if err != nil || n != 0 {
		t.Errorf("zero offset: n=%d err=%v", n, err)
	}
	if !bytes.Equal(same, seg) {
		t.Error("zero offset changed the segment")
	}
}

// Parsing failures must be errors, never panics: a malformed segment must not be
// able to take the server down (callers fall back to the original bytes).
func TestMp4BoxParsingRejectsGarbage(t *testing.T) {
	if _, err := ParseInitTracks([]byte("not an mp4 at all")); err == nil {
		t.Error("want an error for a non-MP4 init segment")
	}
	if _, err := ParseInitTracks(nil); err == nil {
		t.Error("want an error for an empty init segment")
	}
	// A valid-looking moov header with a bogus child size.
	bad := []byte{0, 0, 0, 16, 'm', 'o', 'o', 'v', 0, 0, 0, 5, 't', 'r', 'a', 'k'}
	if _, err := ParseInitTracks(bad); err == nil {
		t.Error("want an error for an out-of-range box size")
	}
	if _, _, err := ShiftTfdt([]byte("garbage"), []mp4Track{{ID: 1, Timescale: 1000}}, 4); err == nil {
		t.Error("want an error for a non-MP4 segment")
	}
	// A huge magic size must not cause a panic or an allocation.
	huge := []byte{1, 0, 0, 0, 0, 0, 0, 0, 'm', 'o', 'o', 'v', 0, 0, 0, 0}
	if _, _, err := ShiftTfdt(huge, []mp4Track{{ID: 1, Timescale: 1000}}, 4); err == nil {
		t.Error("want an error for an implausible largesize box")
	}
}
