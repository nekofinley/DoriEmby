package media

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireFragmentedMP4 builds a fragmented MP4 (ftyp + empty moov + moof/mdat
// pairs). This is the only shape byte-range HLS can serve without a fragmenter.
func requireFragmentedMP4(t *testing.T, dir string, seconds int) string {
	t.Helper()
	out := filepath.Join(dir, "frag.mp4")
	cmd := exec.Command("ffmpeg", "-y", "-v", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=size=320x240:rate=25:duration=%d", seconds),
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "25", "-keyint_min", "25",
		"-sc_threshold", "0", "-pix_fmt", "yuv420p",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", out)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fragmented source: %v: %s", err, o)
	}
	return out
}

func openIndex(t *testing.T, path string) (*os.File, *FragmentedMP4, int64) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { f.Close() })
	st, err := f.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	idx, err := ParseFragmentedMP4(f, st.Size())
	if err != nil {
		t.Fatalf("ParseFragmentedMP4: %v", err)
	}
	return f, idx, st.Size()
}

// foo
func assertTailIsIndexBox(t *testing.T, f io.ReaderAt, at, size int64) {
	t.Helper()
	if at == size {
		return
	}
	if at > size {
		t.Fatalf("the fragments run past the end of the file (%d > %d)", at, size)
	}
	// A well-formed fragmented MP4 ends with a random-access index (mfra, holding
	// tfra+mfro) that carries no media, so the fragments tile the file only up to
	// it. Anything else there means the index missed media.
	var buf [8]byte
	if _, err := f.ReadAt(buf[:], at); err != nil {
		t.Fatalf("read the box after the last fragment: %v", err)
	}
	switch typ := string(buf[4:8]); typ {
	case "mfra", "sidx", "free", "skip":
	default:
		t.Errorf("unexpected %q box after the last fragment (at %d of %d)", typ, at, size)
	}
}

// The index has to describe the real file: the init covers ftyp+moov, the segments
// tile the file, and the durations add up to the media's own length.
func TestParseFragmentedMP4IndexesTheFile(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := requireFragmentedMP4(t, dir, 5)
	f, idx, size := openIndex(t, src)

	if idx.InitOffset != 0 {
		t.Errorf("init offset = %d, want 0 (ftyp leads the file)", idx.InitOffset)
	}
	if idx.InitLength <= 0 || idx.InitLength >= size {
		t.Errorf("init length = %d, which cannot be right for a %d-byte file", idx.InitLength, size)
	}
	if len(idx.Segments) < 2 {
		t.Fatalf("expected several fragments, got %d", len(idx.Segments))
	}

	// Segments must tile the file in order, with no gap the player would have to
	// guess at: byte ranges are absolute, so a hole means missing media.
	prev := idx.InitOffset + idx.InitLength
	for i, s := range idx.Segments {
		if s.Offset != prev {
			t.Errorf("segment %d starts at %d, want %d (contiguous)", i, s.Offset, prev)
		}
		if s.Length <= 0 {
			t.Errorf("segment %d has length %d", i, s.Length)
		}
		if s.Duration <= 0 {
			t.Errorf("segment %d has duration %v", i, s.Duration)
		}
		prev = s.Offset + s.Length
	}
	if prev > size {
		t.Errorf("segments end at %d, past the file size %d", prev, size)
	}
	assertTailIsIndexBox(t, f, prev, size)
	// ~1s fragments from a 5s source (a keyframe every 25 frames at 25fps).
	if idx.Duration < 4.5 || idx.Duration > 5.5 {
		t.Errorf("total duration = %.3f, want about 5s", idx.Duration)
	}
}

// The decisive check: cut the real bytes out by the parsed ranges and decode them.
// A well-formed but wrong index would fail here.
func TestByteRangesDecodeWithFFmpeg(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := requireFragmentedMP4(t, dir, 4)
	f, idx, _ := openIndex(t, src)

	read := func(off, n int64) []byte {
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, off); err != nil {
			t.Fatalf("read %d bytes at %d: %v", n, off, err)
		}
		return buf
	}

	// Init plus the first two fragments, exactly as a player would assemble them.
	joined := read(idx.InitOffset, idx.InitLength)
	for i := 0; i < 2 && i < len(idx.Segments); i++ {
		s := idx.Segments[i]
		joined = append(joined, read(s.Offset, s.Length)...)
	}
	combined := filepath.Join(dir, "cut.mp4")
	if err := os.WriteFile(combined, joined, 0o644); err != nil {
		t.Fatalf("write cut file: %v", err)
	}
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", combined,
		"-frames:v", "1", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("the byte ranges do not decode: %v: %s", err, out)
	}

	// And the last fragment must be reachable as its own range, ending at the last
	// media byte rather than somewhere arbitrary.
	last := idx.Segments[len(idx.Segments)-1]
	assertTailIsIndexBox(t, f, last.Offset+last.Length, idx.Size)
}

func TestByteRangePlaylist(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := requireFragmentedMP4(t, dir, 4)
	f, idx, size := openIndex(t, src)

	const uri = "/api/stream/7?proxy=1&exp=1&sig=abc"
	pl, err := idx.ByteRangePlaylist(uri)
	if err != nil {
		t.Fatalf("ByteRangePlaylist: %v", err)
	}
	text := string(pl)

	if !strings.HasPrefix(text, "#EXTM3U") || !strings.Contains(text, "#EXT-X-VERSION:4") {
		t.Errorf("byterange playlists need version 4:\n%s", text)
	}
	if !strings.Contains(text, "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.HasSuffix(text, "#EXT-X-ENDLIST\n") {
		t.Errorf("a byte-range stream of a file is a VOD playlist:\n%s", text)
	}
	wantMap := fmt.Sprintf("#EXT-X-MAP:URI=\"%s\",BYTERANGE=\"%d@%d\"", uri, idx.InitLength, idx.InitOffset)
	if !strings.Contains(text, wantMap) {
		t.Errorf("missing or wrong EXT-X-MAP:\n got %s\nwant %s", text, wantMap)
	}
	// One EXTINF and one BYTERANGE per fragment, in order, each naming the same URI.
	lines := strings.Split(strings.TrimSpace(text), "\n")
	var infs, ranges, uris int
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "#EXTINF:"):
			infs++
			if i+2 >= len(lines) || !strings.HasPrefix(lines[i+1], "#EXT-X-BYTERANGE:") {
				t.Errorf("EXTINF at line %d is not followed by a byte range", i)
			}
		case strings.HasPrefix(l, "#EXT-X-BYTERANGE:"):
			ranges++
		case l == uri:
			uris++
		}
	}
	if infs != len(idx.Segments) || ranges != len(idx.Segments) || uris != len(idx.Segments) {
		t.Errorf("infs=%d ranges=%d uris=%d, want %d each", infs, ranges, uris, len(idx.Segments))
	}
	// The last range must reach the end of the file.
	lastRange := fmt.Sprintf("#EXT-X-BYTERANGE:%d@%d", idx.Segments[len(idx.Segments)-1].Length, idx.Segments[len(idx.Segments)-1].Offset)
	if !strings.Contains(text, lastRange) {
		t.Errorf("missing the final byte range %s:\n%s", lastRange, text)
	}
	if idx.Segments[len(idx.Segments)-1].Offset+idx.Segments[len(idx.Segments)-1].Length > size {
		t.Error("the playlist claims bytes beyond the end of the file")
	}
	assertTailIsIndexBox(t, f, idx.Segments[len(idx.Segments)-1].Offset+idx.Segments[len(idx.Segments)-1].Length, size)

	// A URI that would break the attribute is refused rather than written out.
	if _, err := idx.ByteRangePlaylist("/x\"\n"); err == nil {
		t.Error("a URI with a quote or newline must be refused")
	}
}

// A progressive MP4 cannot be served this way: its samples would have to be
// fragmented first, which is deliberately out of scope.
func TestParseFragmentedMP4RejectsProgressive(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := requireSeekableSource(t, dir, 3) // progressive: moov + one mdat, no moof
	f, err := os.Open(src)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	st, _ := f.Stat()
	_, err = ParseFragmentedMP4(f, st.Size())
	if err == nil {
		t.Fatal("a progressive MP4 must not be indexed for byte-range serving")
	}
	if !strings.Contains(err.Error(), "progressive") {
		t.Errorf("the error should say what is missing, got: %v", err)
	}
}

func TestParseFragmentedMP4RejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.mp4")
	if err := os.WriteFile(junk, bytes.Repeat([]byte{0xAB}, 4096), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := os.Open(junk)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	if _, err := ParseFragmentedMP4(f, 4096); err == nil {
		t.Error("garbage must not parse as a fragmented MP4")
	}
	// Too small to hold even a box header.
	if _, err := ParseFragmentedMP4(bytes.NewReader([]byte{0, 0}), 2); err == nil {
		t.Error("a 2-byte file must not parse")
	}
}

// A file whose fragments are followed by a truncated tail still yields the
// fragments that are complete, which is what a partially downloaded file looks
// like.
func TestParseFragmentedMP4ToleratesTruncatedTail(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := requireFragmentedMP4(t, dir, 4)
	full, idx, size := openIndex(t, src)
	if len(idx.Segments) < 2 {
		t.Skip("need at least two fragments for this test")
	}
	// Cut inside the last fragment.
	cut := idx.Segments[len(idx.Segments)-1].Offset + 4
	if _, err := full.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	head := make([]byte, cut)
	if _, err := io.ReadFull(full, head); err != nil {
		t.Fatalf("read: %v", err)
	}
	trimmed := filepath.Join(dir, "trimmed.mp4")
	if err := os.WriteFile(trimmed, head, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tf, err := os.Open(trimmed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer tf.Close()
	part, err := ParseFragmentedMP4(tf, cut)
	if err != nil {
		t.Fatalf("a truncated tail should still yield the complete fragments: %v", err)
	}
	if len(part.Segments) != len(idx.Segments)-1 {
		t.Errorf("got %d segments, want %d", len(part.Segments), len(idx.Segments)-1)
	}
	if part.Duration >= idx.Duration {
		t.Errorf("the partial index should be shorter: %v vs %v", part.Duration, idx.Duration)
	}
	if size == cut {
		t.Error("test did not actually truncate anything")
	}
}
