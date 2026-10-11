package media

import (
	"fmt"
	"regexp"
	"strings"
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

var langPatterns = []struct {
	re  *regexp.Regexp
	lang string
}{
	{regexp.MustCompile(`(?i)\b(chs|sc|gb|zhhans|hans|简[体中]|简中|中字|中文)\b`), "chi"},
	{regexp.MustCompile(`(?i)\b(cht|tc|big5|zhhant|hant|繁[体中])\b`), "chi"},
	{regexp.MustCompile(`(?i)\b(eng|english|英[文语])\b`), "eng"},
	{regexp.MustCompile(`(?i)\b(jpn|japanese|日[文语])\b`), "jpn"},
	{regexp.MustCompile(`(?i)\b(kor|korean|韩[文语])\b`), "kor"},
	{regexp.MustCompile(`(?i)国语|普通话|mandarin`), "chi"},
	{regexp.MustCompile(`(?i)粤语|cantonese`), "yue"},
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
