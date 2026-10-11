package api

import (
	"fmt"
	"regexp"
	"strings"
)

// versionDTO is one playable variant of the same episode/movie
// (e.g. the BDRip vs the WebRip release of a show).
type versionDTO struct {
	ID      int64  `json:"id"`
	Label   string `json:"label"`
	Size    int64  `json:"size"`
	Current bool   `json:"current"`
}

// versionsFor lists every file bound to the item as a human-labelled variant.
// Returns nil when the item has only one file (nothing to choose from).
func (s *Server) versionsFor(itemID, currentID int64) []versionDTO {
	files, err := s.Store.FilesByItem(itemID)
	if err != nil || len(files) < 2 {
		return nil
	}
	labels := make([]string, len(files))
	seen := map[string]int{}
	for i, f := range files {
		l := VersionLabel(f.Path)
		if l == "" {
			l = "默认版本"
		}
		labels[i] = l
		seen[l]++
	}
	out := make([]versionDTO, 0, len(files))
	for i, f := range files {
		label := labels[i]
		// identical labels (same source/codec/res): disambiguate with the
		// release group token from the file name, e.g. "[Sakurato]"
		if seen[label] > 1 {
			if g := releaseGroup(f.Path); g != "" {
				label += " · " + g
			}
		}
		out = append(out, versionDTO{ID: f.ID, Label: label, Size: f.Size, Current: f.ID == currentID})
	}
	// still identical (same group, different encodes)? append the size
	again := map[string]int{}
	for _, v := range out {
		again[v.Label]++
	}
	for i := range out {
		if again[out[i].Label] > 1 {
			out[i].Label = fmt.Sprintf("%s · %s", out[i].Label, humanSize(out[i].Size))
		}
	}
	return out
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%dMB", n>>20)
	default:
		return fmt.Sprintf("%dKB", n>>10)
	}
}

// releaseGroup extracts the first bracketed token of the file name that is
// not a technical tag (CHS/CHT/1080p/HEVC…), used to tell look-alike
// releases apart in the version picker.
func releaseGroup(path string) string {
	name := path
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	junk := map[string]bool{
		"chs": true, "cht": true, "gb": true, "big5": true, "zh": true,
		"hevc": true, "avc": true, "h265": true, "h264": true, "x265": true, "x264": true,
		"10bit": true, "8bit": true, "1080p": true, "720p": true, "2160p": true,
		"ma10p": true, "hi10p": true, "aac": true, "flac": true, "mp4": true, "mkv": true,
		"webrip": true, "bdrip": true, "web-dl": true, "remux": true, "vcb-studio": true,
	}
	for _, m := range bracketRe.FindAllStringSubmatch(name, -1) {
		tok := strings.ToLower(strings.TrimSpace(m[1]))
		if tok == "" || junk[tok] {
			continue
		}
		if strings.ContainsAny(tok, "0123456789") && len(tok) <= 6 {
			continue // likely episode/resolution fragments
		}
		return m[1]
	}
	return ""
}

var bracketRe = regexp.MustCompile(`\[([^\[\]]{2,40})\]`)

// VersionLabel distills a release into a readable tag like
// "BDRip · HEVC 10bit · 1080p", falling back to the nearest
// meaningful directory segment (e.g. "BDMV", a release-group dir).
func VersionLabel(path string) string {
	low := strings.ToLower(path)
	var parts []string

	switch {
	case strings.Contains(low, "bdrip"), strings.Contains(low, "blu-ray"),
		strings.Contains(low, "bluray"), strings.Contains(low, "blu_ray"),
		strings.Contains(low, "bdmv"):
		parts = append(parts, "BDRip")
	case strings.Contains(low, "web-dl"), strings.Contains(low, "webdl"),
		strings.Contains(low, "webrip"), strings.Contains(low, "web-rip"):
		parts = append(parts, "WebRip")
	case strings.Contains(low, "hdtv"):
		parts = append(parts, "HDTV")
	case strings.Contains(low, "dvdrip"), strings.Contains(low, "dvd"):
		parts = append(parts, "DVD")
	}

	codec := ""
	switch {
	case strings.Contains(low, "x265"), strings.Contains(low, "hevc"),
		strings.Contains(low, "h.265"), strings.Contains(low, "h265"):
		codec = "HEVC"
	case strings.Contains(low, "x264"), strings.Contains(low, "h.264"),
		strings.Contains(low, "avc"), strings.Contains(low, "h264"):
		codec = "AVC"
	}
	ten := strings.Contains(low, "10bit") || strings.Contains(low, "ma10p") ||
		strings.Contains(low, "hi10p") || strings.Contains(low, "10-bit")
	if codec != "" {
		if ten {
			codec += " 10bit"
		}
		parts = append(parts, codec)
	}

	for _, r := range []struct{ tok, name string }{
		{"4320p", "8K"}, {"2160p", "4K"}, {"1080p", "1080p"},
		{"1080i", "1080i"}, {"720p", "720p"}, {"480p", "480p"},
	} {
		if strings.Contains(low, r.tok) {
			parts = append(parts, r.name)
			break
		}
	}

	if len(parts) == 0 {
		// fall back to the deepest directory segment that looks like a source tag
		dir := path
		if i := strings.LastIndex(dir, "/"); i >= 0 {
			dir = dir[:i]
		}
		seg := strings.Split(dir, "/")
		for i := len(seg) - 1; i >= 0; i-- {
			n := strings.ToLower(strings.TrimSpace(seg[i]))
			if n == "" {
				continue
			}
			if strings.Contains(n, "rip") || strings.Contains(n, "bluray") ||
				strings.Contains(n, "blu-ray") || strings.Contains(n, "remux") {
				return seg[i]
			}
		}
		return ""
	}
	return strings.Join(parts, " · ")
}
