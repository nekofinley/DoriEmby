package media

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// mp4box.go: just enough ISO-BMFF parsing to (a) read per-track timescales from
// an fMP4 init segment and (b) shift the baseMediaDecodeTime of media segments.
//
// Why it exists: ffmpeg rebases `tfdt` to zero whenever it starts a new process,
// even with -copyts / -output_ts_offset (measured; docs §11.3.1). A packaging
// session that starts at a resume or seek position therefore emits segments
// whose media timeline begins at zero, while the player's timeline is absolute.
// Shifting `tfdt` by the session's start offset puts the media back on the
// absolute timeline, which is what keeps the progress bar, seeking and
// (absolutely timed) sidecar subtitles aligned.
//
// Nothing here may ever be fatal to playback: callers fall back to serving the
// original bytes when parsing fails.

var errNotISOBMFF = errors.New("not an ISO base media file")

type box struct {
	typ   string
	start int // offset of the box header
	body  int // offset of the box payload
	end   int // offset just past the box
}

// walkBoxes yields the boxes directly contained in data[from:to]. Trailing bytes
// that cannot form a box mean the buffer is truncated or is not ISO-BMFF at all,
// which callers must hear about rather than silently patching nothing.
func walkBoxes(data []byte, from, to int) ([]box, error) {
	var out []box
	off := from
	for off+8 <= to {
		size := int(binary.BigEndian.Uint32(data[off : off+4]))
		typ := string(data[off+4 : off+8])
		body := off + 8
		switch size {
		case 0: // extends to the end of the enclosing box
			size = to - off
		case 1: // 64-bit size
			if off+16 > to {
				return nil, fmt.Errorf("%w: truncated largesize box %q", errNotISOBMFF, typ)
			}
			size = int(binary.BigEndian.Uint64(data[off+8 : off+16]))
			body = off + 16
		}
		if size < body-off || off+size > to {
			return nil, fmt.Errorf("%w: bad size %d for box %q", errNotISOBMFF, size, typ)
		}
		out = append(out, box{typ: typ, start: off, body: body, end: off + size})
		off += size
	}
	if off != to {
		return nil, fmt.Errorf("%w: %d trailing bytes", errNotISOBMFF, to-off)
	}
	return out, nil
}

func findBox(data []byte, from, to int, typ string) (box, bool, error) {
	boxes, err := walkBoxes(data, from, to)
	if err != nil {
		return box{}, false, err
	}
	for _, b := range boxes {
		if b.typ == typ {
			return b, true, nil
		}
	}
	return box{}, false, nil
}

// mp4Track is the part of a track we need to translate seconds into the track's
// own media timeline.
type mp4Track struct {
	ID        uint32
	Timescale uint32
}

// ParseInitTracks reads track IDs and timescales out of an init segment
// (ftyp + moov). The order matches the order the tracks appear in the file,
// which for ffmpeg output is video first, then audio.
func ParseInitTracks(init []byte) ([]mp4Track, error) {
	moov, ok, err := findBox(init, 0, len(init), "moov")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: no moov", errNotISOBMFF)
	}
	traks, err := walkBoxes(init, moov.body, moov.end)
	if err != nil {
		return nil, err
	}
	var out []mp4Track
	for _, trak := range traks {
		if trak.typ != "trak" {
			continue
		}
		id, err := readTrackID(init, trak)
		if err != nil {
			return nil, err
		}
		ts, err := readTrackTimescale(init, trak)
		if err != nil {
			return nil, err
		}
		out = append(out, mp4Track{ID: id, Timescale: ts})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no tracks", errNotISOBMFF)
	}
	return out, nil
}

func readTrackID(data []byte, trak box) (uint32, error) {
	tkhd, ok, err := findBox(data, trak.body, trak.end, "tkhd")
	if err != nil {
		return 0, err
	}
	if !ok || tkhd.body+4 > tkhd.end {
		return 0, fmt.Errorf("%w: no tkhd", errNotISOBMFF)
	}
	version := data[tkhd.body]
	// version+flags, then creation/modification times (4 or 8 bytes each).
	off := tkhd.body + 4
	if version == 1 {
		off += 16
	} else {
		off += 8
	}
	if off+4 > tkhd.end {
		return 0, fmt.Errorf("%w: truncated tkhd", errNotISOBMFF)
	}
	return binary.BigEndian.Uint32(data[off : off+4]), nil
}

func readTrackTimescale(data []byte, trak box) (uint32, error) {
	mdia, ok, err := findBox(data, trak.body, trak.end, "mdia")
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("%w: no mdia", errNotISOBMFF)
	}
	mdhd, ok, err := findBox(data, mdia.body, mdia.end, "mdhd")
	if err != nil {
		return 0, err
	}
	if !ok || mdhd.body+4 > mdhd.end {
		return 0, fmt.Errorf("%w: no mdhd", errNotISOBMFF)
	}
	version := data[mdhd.body]
	off := mdhd.body + 4
	if version == 1 {
		off += 16
	} else {
		off += 8
	}
	if off+4 > mdhd.end {
		return 0, fmt.Errorf("%w: truncated mdhd", errNotISOBMFF)
	}
	return binary.BigEndian.Uint32(data[off : off+4]), nil
}

// ShiftTfdt returns a copy of an fMP4 media segment whose decode timestamps are
// moved forward by offsetSeconds, each track using its own timescale. It reports
// how many tfdt boxes it rewrote. The input is never modified.
func ShiftTfdt(seg []byte, tracks []mp4Track, offsetSeconds float64) ([]byte, int, error) {
	if offsetSeconds == 0 {
		return seg, 0, nil
	}
	byID := make(map[uint32]uint32, len(tracks))
	for _, t := range tracks {
		if t.Timescale > 0 {
			byID[t.ID] = t.Timescale
		}
	}
	moofs, err := walkBoxes(seg, 0, len(seg)) // top-level: styp/sidx/moof/mdat
	if err != nil {
		return nil, 0, err
	}
	out := seg
	copied := false
	patched := 0
	for _, moof := range moofs {
		if moof.typ != "moof" {
			continue
		}
		trafs, err := walkBoxes(seg, moof.body, moof.end)
		if err != nil {
			return nil, 0, err
		}
		for _, traf := range trafs {
			if traf.typ != "traf" {
				continue
			}
			id, err := readTrafTrackID(seg, traf)
			if err != nil {
				return nil, 0, err
			}
			timescale, ok := byID[id]
			if !ok {
				continue
			}
			delta := int64(offsetSeconds*float64(timescale) + 0.5)
			if delta <= 0 {
				continue
			}
			at, width, err := findTfdt(seg, traf)
			if err != nil {
				return nil, 0, err
			}
			if at < 0 {
				continue
			}
			if !copied { // copy once, lazily
				out = append([]byte(nil), seg...)
				copied = true
			}
			var cur uint64
			if width == 8 {
				cur = binary.BigEndian.Uint64(out[at : at+8])
			} else {
				cur = uint64(binary.BigEndian.Uint32(out[at : at+4]))
			}
			next := cur + uint64(delta)
			if width == 8 {
				binary.BigEndian.PutUint64(out[at:at+8], next)
			} else {
				if next > 0xFFFFFFFF {
					return nil, 0, fmt.Errorf("tfdt would overflow 32 bits (%d)", next)
				}
				binary.BigEndian.PutUint32(out[at:at+4], uint32(next))
			}
			patched++
		}
	}
	return out, patched, nil
}

func readTrafTrackID(data []byte, traf box) (uint32, error) {
	tfhd, ok, err := findBox(data, traf.body, traf.end, "tfhd")
	if err != nil {
		return 0, err
	}
	if !ok || tfhd.body+8 > tfhd.end {
		return 0, fmt.Errorf("%w: no tfhd", errNotISOBMFF)
	}
	return binary.BigEndian.Uint32(data[tfhd.body+4 : tfhd.body+8]), nil
}

// findTfdt locates the baseMediaDecodeTime value inside a traf. It returns -1
// when the box is absent, which is legal.
func findTfdt(data []byte, traf box) (at, width int, err error) {
	tfdt, ok, err := findBox(data, traf.body, traf.end, "tfdt")
	if err != nil {
		return -1, 0, err
	}
	if !ok {
		return -1, 0, nil
	}
	if tfdt.body+4 > tfdt.end {
		return -1, 0, fmt.Errorf("%w: truncated tfdt", errNotISOBMFF)
	}
	if data[tfdt.body] == 1 {
		if tfdt.body+12 > tfdt.end {
			return -1, 0, fmt.Errorf("%w: truncated tfdt v1", errNotISOBMFF)
		}
		return tfdt.body + 4, 8, nil
	}
	if tfdt.body+8 > tfdt.end {
		return -1, 0, fmt.Errorf("%w: truncated tfdt v0", errNotISOBMFF)
	}
	return tfdt.body + 4, 4, nil
}
