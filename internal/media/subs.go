package media

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"doriyamedia/internal/models"
)

// DetectSubFormat classifies an external subtitle file.
func DetectSubFormat(name string) (format string, convertible bool) {
	low := strings.ToLower(name)
	switch {
	case strings.HasSuffix(low, ".srt"):
		return "subrip", true
	case strings.HasSuffix(low, ".ass"):
		return "ass", true
	case strings.HasSuffix(low, ".ssa"):
		return "ssa", true
	case strings.HasSuffix(low, ".vtt"):
		return "webvtt", true
	case strings.HasSuffix(low, ".sub"):
		return "sub", false
	default:
		return "", false
	}
}

// langPatterns map a file or track name to a language. The ASCII alternatives
// are word-anchored, but the CJK ones cannot be: Go's \b is an ASCII word
// boundary, so `\b简\b` never matches text such as ".简日." — which is exactly
// how Chinese subtitles are named in practice.
var langPatterns = []struct {
	re   *regexp.Regexp
	lang string
}{
	{regexp.MustCompile(`(?i)(\b(chs|sc|gb|zhhans|hans)\b|简[体中日英]|简中|中字|中文)`), "chi"},
	{regexp.MustCompile(`(?i)(\b(cht|tc|big5|zhhant|hant)\b|繁[体中日英]|繁中)`), "chi"},
	{regexp.MustCompile(`(?i)(\b(eng|english)\b|英[文语])`), "eng"},
	{regexp.MustCompile(`(?i)(\b(jpn|japanese)\b|日[文语])`), "jpn"},
	{regexp.MustCompile(`(?i)(\b(kor|korean)\b|韩[文语])`), "kor"},
	{regexp.MustCompile(`(?i)国语|普通话|mandarin`), "chi"},
	{regexp.MustCompile(`(?i)粤语|粵語|cantonese`), "yue"},
}

func GuessLang(name string) string {
	for _, lp := range langPatterns {
		if lp.re.MatchString(name) {
			return lp.lang
		}
	}
	return ""
}

func SubLabel(name string) string {
	base := name
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[:i]
	}
	return strings.TrimSpace(base)
}

// SRTToVTT converts SubRip bytes to WebVTT.
func SRTToVTT(data []byte) []byte {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.TrimPrefix(s, "\ufeff")
	if !strings.HasPrefix(s, "WEBVTT") {
		var b strings.Builder
		b.WriteString("WEBVTT\n\n")
		b.WriteString(s)
		s = b.String()
	}
	// cue timing: 00:00:01,000 --> 00:00:04,000  → comma to dot
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.Contains(line, "-->") {
			lines[i] = strings.ReplaceAll(line, ",", ".")
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

var assTime = regexp.MustCompile(`(\d+):(\d{1,2}):(\d{1,2})\.(\d{1,3})`)

func assTimestamp(t string) string {
	m := assTime.FindStringSubmatch(t)
	if m == nil {
		return "00:00:00.000"
	}
	h, mi, se, ms := m[1], m[2], m[3], m[4]
	for len(mi) < 2 {
		mi = "0" + mi
	}
	for len(se) < 2 {
		se = "0" + se
	}
	for len(ms) < 3 {
		ms += "0"
	}
	return fmt.Sprintf("%02s:%s:%s.%s", h, mi, se, ms)
}

var overrideTags = regexp.MustCompile(`\{[^}]*\}`)

// ASSToVTT converts SSA/ASS subtitle bytes to WebVTT (basic).
func ASSToVTT(data []byte) []byte {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	inEvents := false
	var fields []string
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			inEvents = strings.EqualFold(t, "[events]")
			continue
		}
		if !inEvents {
			continue
		}
		low := strings.ToLower(t)
		if strings.HasPrefix(low, "format:") {
			f := strings.Split(strings.TrimPrefix(t[len("format:"):], " "), ",")
			fields = f
			for i := range f {
				fields[i] = strings.TrimSpace(fields[i])
			}
			continue
		}
		if strings.HasPrefix(low, "dialogue:") {
			body := t[len("dialogue:"):]
			parts := strings.SplitN(body, ",", len(fields))
			if len(parts) != len(fields) {
				continue
			}
			var start, end, text string
			for i, name := range fields {
				switch strings.ToLower(name) {
				case "start":
					start = parts[i]
				case "end":
					end = parts[i]
				case "text":
					text = parts[i]
				}
			}
			if start == "" || end == "" {
				continue
			}
			text = overrideTags.ReplaceAllString(text, "")
			text = strings.ReplaceAll(text, "\\N", "\n")
			text = strings.ReplaceAll(text, "\\n", "\n")
			fmt.Fprintf(&b, "%s --> %s\n%s\n\n", assTimestamp(start), assTimestamp(end), text)
		}
	}
	return []byte(b.String())
}

// ToVTT converts known subtitle bytes to WebVTT by format name.
func ToVTT(format string, data []byte) []byte {
	switch format {
	case "subrip", "srt":
		return SRTToVTT(data)
	case "ass", "ssa":
		return ASSToVTT(data)
	case "webvtt", "vtt":
		return data
	default:
		return SRTToVTT(data)
	}
}

// ---------- automatic subtitle selection ----------

var (
	reSimplified  = regexp.MustCompile(`(?i)(简|chs|hans|\bsc\b|\bgb\b|zh[-_]cn)`)
	reTraditional = regexp.MustCompile(`(?i)(繁|cht|hant|\btc\b|big5|zh[-_]tw)`)
)

// subtitleRank orders subtitle tracks for automatic selection; the
// lexicographically smallest rank wins. Order of preference:
//
//  1. Chinese, then Cantonese, then an identified other language, then an
//     untagged track. Chinese first because this library (and its viewers) are
//     Chinese; an untagged track last because we cannot tell what it is.
//  2. Simplified before Traditional — the two share an ISO code, and a viewer
//     who reads one usually cannot comfortably read the other.
//  3. Tracks whose language is known before untagged ones.
//  4. Registration order, which follows the file's own track order.
func subtitleRank(s models.Subtitle) [4]int {
	lang := strings.ToLower(strings.TrimSpace(s.Lang))
	// "und" is the container's way of saying "no idea", so treat it as untagged.
	if lang == "und" {
		lang = ""
	}
	// When the container has no usable tag, the title usually still says it
	// ("简日", "繁體", "English").
	if lang == "" {
		lang = GuessLang(s.Title)
	}
	// Both the tag and the human title carry hints ("简日", "SC", "繁體").
	text := strings.ToLower(s.Title + " " + s.Path)

	langRank := 3
	switch {
	case lang == "chi" || lang == "zho" || lang == "zh" || strings.HasPrefix(lang, "zh"):
		langRank = 0
	case lang == "yue":
		langRank = 1
	case lang != "":
		langRank = 2
	}

	scriptRank := 1
	switch {
	case reSimplified.MatchString(text):
		scriptRank = 0
	case reTraditional.MatchString(text):
		scriptRank = 2
	}

	untagged := 0
	if lang == "" {
		untagged = 1
	}
	return [4]int{langRank, scriptRank, untagged, int(s.ID)}
}

func lessRank(a, b [4]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// PickDefaultSubtitle returns the id of the subtitle to enable automatically
// when a file starts playing, or 0 when there is nothing to pick. Callers must
// pass only subtitles they can actually serve (see Subtitle.Convertible).
func PickDefaultSubtitle(subs []models.Subtitle) int64 {
	var best int64
	var bestRank [4]int
	for _, s := range subs {
		r := subtitleRank(s)
		if best == 0 || lessRank(r, bestRank) {
			best, bestRank = s.ID, r
		}
	}
	return best
}

// ---------- timing shifts ----------

func parseVTTTime(s string) (float64, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	return float64(h)*3600 + float64(m)*60 + sec, true
}

// formatVTTTime renders seconds as a WebVTT timestamp, rounded to milliseconds
// so a shifted cue can never come out as "…:60.000".
func formatVTTTime(t float64) string {
	ms := int64(math.Round(t * 1000))
	if ms < 0 {
		ms = 0
	}
	h := ms / 3600000
	m := (ms % 3600000) / 60000
	s := (ms % 60000) / 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms%1000)
}

// ShiftVTT moves a WebVTT document's cues back by offset seconds, dropping cues
// that end before the new zero point and clamping ones that straddle it.
//
// HLS subtitle renditions are timed against the stream the player is on, and a
// packaging session's stream starts wherever the session started, while a sidecar
// file's cues are in file time. Blocks without a cue timing line (the WEBVTT
// header, STYLE, NOTE) are passed through untouched, and so is a whole cue's
// payload when it survives.
func ShiftVTT(data []byte, offset float64) []byte {
	if offset <= 0 {
		return data
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	blocks := strings.Split(text, "\n\n")
	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		lines := strings.Split(block, "\n")
		idx := -1
		for i, line := range lines {
			if strings.Contains(line, "-->") {
				idx = i
				break
			}
		}
		if idx < 0 {
			out = append(out, block)
			continue
		}
		fields := strings.Fields(lines[idx])
		arrow := -1
		for i, f := range fields {
			if f == "-->" {
				arrow = i
				break
			}
		}
		if arrow < 1 || arrow+1 >= len(fields) {
			out = append(out, block)
			continue
		}
		start, ok1 := parseVTTTime(fields[arrow-1])
		end, ok2 := parseVTTTime(fields[arrow+1])
		if !ok1 || !ok2 {
			out = append(out, block)
			continue
		}
		if end <= offset {
			continue // wholly before the stream starts
		}
		fields[arrow-1] = formatVTTTime(start - offset)
		fields[arrow+1] = formatVTTTime(end - offset)
		lines[idx] = strings.Join(fields, " ")
		out = append(out, strings.Join(lines, "\n"))
	}
	return []byte(strings.Join(out, "\n\n"))
}
