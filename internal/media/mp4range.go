package media

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"
)

// mp4range.go: serve an *already fragmented* MP4 as HLS by pointing the player at
// byte ranges of the original file, with no transcoding, no remuxing and nothing
// written to disk.
//
// What this deliberately does not do is fragment a progressive MP4 (moov at the
// front, one large mdat). That needs a real fragmenter — parsing stts/stsc/stco/
// stsz and generating moof/trun — and is out of scope here. Such a file is
// reported as unsupported rather than mis-served.
//
// Note on when this is worth choosing: after the fMP4 remux path (P2) and the
// range-hardened proxy (P5), both already deliver zero-transcode playback for
// every case this would serve — remuxing copies the streams, and the proxy streams
// the original file with ranges. The one thing byte-range serving saves is the
// packaged output on disk. See docs/ios-safari-playback-redesign.md §17.

// FragmentedSegment is one moof+mdat pair: a byte range of the file that a player
// can fetch and decode on its own.
type FragmentedSegment struct {
	Offset   int64
	Length   int64
	Duration float64
}

// FragmentedMP4 is the byte layout of a file that can be served by range.
type FragmentedMP4 struct {
	// InitOffset/InitLength cover ftyp+moov: the initialisation a player needs
	// before any fragment.
	InitOffset int64
	InitLength int64
	Segments   []FragmentedSegment
	Duration   float64
	// Size is the file size the index was built against.
	Size int64
}

// boxHeaderSize is the smallest possible top-level box.
const boxHeaderSize = 8

// ParseFragmentedMP4 indexes a fragmented MP4 through a ReaderAt, reading only box
// headers and moof bodies. It fails with a reason for anything it cannot serve,
// including a progressive MP4.
func ParseFragmentedMP4(f io.ReaderAt, size int64) (*FragmentedMP4, error) {
	if size < boxHeaderSize {
		return nil, fmt.Errorf("%w: file is too small", errNotISOBMFF)
	}
	out := &FragmentedMP4{Size: size}

	var (
		initStart = int64(-1)
		initEnd   = int64(-1)
		moofStart = int64(-1)
		moofDur   = 0.0
		tracks    map[uint32]uint32
		sawMoof   = false
	)

	for off := int64(0); off+boxHeaderSize <= size; {
		typ, boxSize, _, err := readBoxHeader(f, off, size)
		if err != nil {
			// A truncated tail after valid fragments is common enough (a partial
			// download) that it should stop indexing rather than fail the file.
			if sawMoof {
				break
			}
			return nil, err
		}
		switch typ {
		case "ftyp", "styp", "free", "skip", "wide":
			if initStart < 0 && typ == "ftyp" {
				initStart = off
			}
		case "moov":
			if sawMoof {
				return nil, fmt.Errorf("%w: moov follows the fragments, so the file cannot be streamed by range", errNotISOBMFF)
			}
			if initStart < 0 {
				initStart = off
			}
			initEnd = off + boxSize
			// Track timescales live in moov; without them fragment durations cannot
			// be turned into seconds.
			moov := make([]byte, boxSize)
			if _, err := f.ReadAt(moov, off); err != nil {
				return nil, fmt.Errorf("read moov: %w", err)
			}
			parsed, err := ParseInitTracks(moov)
			if err != nil {
				return nil, err
			}
			tracks = make(map[uint32]uint32, len(parsed))
			for _, t := range parsed {
				if t.Timescale > 0 {
					tracks[t.ID] = t.Timescale
				}
			}
		case "moof":
			if initEnd < 0 {
				return nil, fmt.Errorf("%w: a fragment appears before moov", errNotISOBMFF)
			}
			sawMoof = true
			moofStart = off
			box := make([]byte, boxSize)
			if _, err := f.ReadAt(box, off); err != nil {
				break // truncated fragment: stop, keeping what was indexed
			}
			d, err := fragmentSeconds(box, tracks)
			if err != nil {
				return nil, err
			}
			moofDur = d
		case "mdat":
			if moofStart < 0 {
				break
			}
			// The segment is everything from the moof through the end of its mdat,
			// so whatever lies between them is included and the range stays
			// contiguous.
			out.Segments = append(out.Segments, FragmentedSegment{
				Offset:   moofStart,
				Length:   off + boxSize - moofStart,
				Duration: moofDur,
			})
			out.Duration += moofDur
			moofStart, moofDur = -1, 0
		}
		if boxSize < boxHeaderSize {
			return nil, fmt.Errorf("%w: bad size %d for box %q", errNotISOBMFF, boxSize, typ)
		}
		off += boxSize
	}
	if initStart < 0 || initEnd < 0 {
		return nil, fmt.Errorf("%w: no moov box", errNotISOBMFF)
	}
	if len(out.Segments) == 0 {
		return nil, fmt.Errorf("%w: no fragments (a progressive MP4 would have to be fragmented first)", errNotISOBMFF)
	}
	out.InitOffset, out.InitLength = initStart, initEnd-initStart
	return out, nil
}

// readBoxHeader reads one top-level box header, returning its type, total size and
// header length (8, or 16 for a 64-bit size).
func readBoxHeader(f io.ReaderAt, off, size int64) (string, int64, int64, error) {
	var buf [16]byte
	n := int64(16)
	if off+n > size {
		n = size - off
	}
	if n < boxHeaderSize {
		return "", 0, 0, fmt.Errorf("%w: truncated box at %d", errNotISOBMFF, off)
	}
	if _, err := f.ReadAt(buf[:n], off); err != nil {
		return "", 0, 0, fmt.Errorf("read box header at %d: %w", off, err)
	}
	typ := string(buf[4:8])
	boxSize := int64(binary.BigEndian.Uint32(buf[0:4]))
	hdr := int64(boxHeaderSize)
	switch boxSize {
	case 0: // extends to the end of the file
		boxSize = size - off
	case 1: // 64-bit size
		if n < 16 {
			return "", 0, 0, fmt.Errorf("%w: truncated largesize box %q", errNotISOBMFF, typ)
		}
		boxSize = int64(binary.BigEndian.Uint64(buf[8:16]))
		hdr = 16
	}
	if boxSize < hdr || off+boxSize > size {
		return "", 0, 0, fmt.Errorf("%w: bad size %d for box %q at %d", errNotISOBMFF, boxSize, typ, off)
	}
	return typ, boxSize, hdr, nil
}

// fragmentSeconds returns a fragment's presentation duration: the longest track
// duration in its moof, since a fragment holds one piece of every track and the
// playlist advances by the piece it must wait for.
func fragmentSeconds(moof []byte, tracks map[uint32]uint32) (float64, error) {
	trafs, err := walkBoxes(moof, 0, len(moof))
	if err != nil {
		return 0, err
	}
	longest := 0.0
	for _, b := range trafs {
		if b.typ != "moof" {
			continue
		}
		inner, err := walkBoxes(moof, b.body, b.end)
		if err != nil {
			return 0, err
		}
		for _, traf := range inner {
			if traf.typ != "traf" {
				continue
			}
			secs, err := trafSeconds(moof, traf, tracks)
			if err != nil {
				return 0, err
			}
			if secs > longest {
				longest = secs
			}
		}
	}
	if longest == 0 {
		return 0, fmt.Errorf("%w: fragment declares no duration", errNotISOBMFF)
	}
	return longest, nil
}

// trafSeconds sums one track fragment's sample durations, falling back to the
// per-fragment default when the run does not carry durations per sample.
func trafSeconds(moof []byte, traf box, tracks map[uint32]uint32) (float64, error) {
	timescale := uint32(0)
	id, err := readTrafTrackID(moof, traf)
	if err != nil {
		return 0, err
	}
	if ts, ok := tracks[id]; ok {
		timescale = ts
	}
	if timescale == 0 {
		return 0, fmt.Errorf("%w: no timescale for track %d", errNotISOBMFF, id)
	}

	tfhd, ok, err := findBox(moof, traf.body, traf.end, "tfhd")
	if err != nil {
		return 0, err
	}
	if !ok || tfhd.body+8 > tfhd.end {
		return 0, fmt.Errorf("%w: no tfhd in traf", errNotISOBMFF)
	}
	flags := uint32(moof[tfhd.body+1])<<16 | uint32(moof[tfhd.body+2])<<8 | uint32(moof[tfhd.body+3])
	off := tfhd.body + 8
	defaultDuration := uint32(0)
	if flags&0x000001 != 0 {
		off += 8 // base_data_offset
	}
	if flags&0x000002 != 0 {
		off += 4 // sample_description_index
	}
	if flags&0x000008 != 0 {
		if off+4 > tfhd.end {
			return 0, fmt.Errorf("%w: truncated tfhd", errNotISOBMFF)
		}
		defaultDuration = binary.BigEndian.Uint32(moof[off : off+4])
		off += 4
	}
	if flags&0x000010 != 0 {
		off += 4
	}
	if flags&0x000020 != 0 {
		off += 4
	}

	trun, ok, err := findBox(moof, traf.body, traf.end, "trun")
	if err != nil {
		return 0, err
	}
	if !ok || trun.body+8 > trun.end {
		return 0, fmt.Errorf("%w: no trun in traf", errNotISOBMFF)
	}
	tflags := uint32(moof[trun.body+1])<<16 | uint32(moof[trun.body+2])<<8 | uint32(moof[trun.body+3])
	count := int64(binary.BigEndian.Uint32(moof[trun.body+4 : trun.body+8]))
	at := trun.body + 8
	if tflags&0x000001 != 0 {
		at += 4 // data_offset
	}
	if tflags&0x000004 != 0 {
		at += 4 // first_sample_flags
	}
	perSample := 0
	if tflags&0x000100 != 0 {
		perSample += 4
	}
	if tflags&0x000200 != 0 {
		perSample += 4
	}
	if tflags&0x000400 != 0 {
		perSample += 4
	}
	if tflags&0x000800 != 0 {
		perSample += 4
	}

	total := uint64(0)
	if tflags&0x000100 != 0 {
		// Durations are per sample.
		for i := int64(0); i < count; i++ {
			if at+4 > trun.end {
				return 0, fmt.Errorf("%w: truncated trun samples", errNotISOBMFF)
			}
			total += uint64(binary.BigEndian.Uint32(moof[at : at+4]))
			at += perSample
		}
	} else {
		if defaultDuration == 0 {
			return 0, fmt.Errorf("%w: fragment has neither sample durations nor a default", errNotISOBMFF)
		}
		if perSample > 0 && at+perSample*int(count) > trun.end {
			return 0, fmt.Errorf("%w: truncated trun samples", errNotISOBMFF)
		}
		total = uint64(defaultDuration) * uint64(count)
	}
	if total == 0 {
		return 0, fmt.Errorf("%w: fragment has no samples", errNotISOBMFF)
	}
	return float64(total) / float64(timescale), nil
}

// ByteRangePlaylist renders a VOD playlist that fetches the file's own bytes. The
// init is declared with EXT-X-MAP and a byte range of its own, each fragment is an
// EXT-X-BYTERANGE segment, and every URI is the same endpoint — the player issues
// Range requests against it, which is exactly what the proxy already serves.
//
// The URI must be percent-safe: it is written inside an m3u8 attribute, so quotes
// and newlines are rejected rather than escaped into a broken playlist.
func (m *FragmentedMP4) ByteRangePlaylist(uri string) ([]byte, error) {
	if strings.ContainsAny(uri, "\"\n\r") {
		return nil, fmt.Errorf("byterange: uri contains a character an m3u8 attribute cannot carry")
	}
	if m.InitLength <= 0 || len(m.Segments) == 0 {
		return nil, fmt.Errorf("byterange: nothing indexed to serve")
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:4\n#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", targetDuration(m.Segments)))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString(fmt.Sprintf("#EXT-X-MAP:URI=\"%s\",BYTERANGE=\"%d@%d\"\n", uri, m.InitLength, m.InitOffset))
	for _, s := range m.Segments {
		// A non-zero start is mandatory on the first segment and least surprising
		// on the rest: the player then needs no continuity assumption.
		b.WriteString(fmt.Sprintf("#EXTINF:%.3f,\n", s.Duration))
		b.WriteString(fmt.Sprintf("#EXT-X-BYTERANGE:%d@%d\n", s.Length, s.Offset))
		b.WriteString(uri)
		b.WriteString("\n")
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String()), nil
}

// targetDuration is the rounded-up longest segment, as the spec requires.
func targetDuration(segs []FragmentedSegment) int {
	longest := 0.0
	for _, s := range segs {
		if s.Duration > longest {
			longest = s.Duration
		}
	}
	return int(math.Ceil(longest))
}
