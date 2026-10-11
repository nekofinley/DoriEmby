package scan

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Parsed is the filename identification result.
type Parsed struct {
	Kind      string // movie | episode
	Title     string // cleaned movie title, or show title for episodes
	Year      int
	Season    int
	Episode   int
	HasYear   bool
	IsEpisode bool
}

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".mov": true, ".ts": true, ".m2ts": true,
	".mts": true, ".avi": true, ".wmv": true, ".flv": true, ".webm": true, ".mpg": true,
	".mpeg": true, ".vob": true, ".rmvb": true, ".rm": true, ".strm": true, ".tp": true,
}

func IsVideo(name string) bool {
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return false
	}
	return videoExts[strings.ToLower(name[i:])]
}

var skipWords = []string{"sample", "trailer", "recycle", "$eaDir", "@eaDir", "lost+found", "#recycle", ".!qB", "_ths"}

func SkipEntry(name string) bool {
	low := strings.ToLower(name)
	for _, w := range skipWords {
		if strings.Contains(low, strings.ToLower(w)) {
			return true
		}
	}
	return false
}

var (
	reYear      = regexp.MustCompile(`(?:^|[\s\.\(_\-])((?:19|20)\d{2})(?:[\s\.\)\_\-]|$)`)
	reSE       = regexp.MustCompile(`(?i)\bs(\d{1,2})[\._ ]?e(\d{1,3})(?:[\._ -]?e(\d{1,3}))?`)
	reSeasonOnly = regexp.MustCompile(`(?i)^s(\d{1,2})$|season[\._ ]?(\d{1,2})$|第(\d{1,2})季$`)
	reCJKep     = regexp.MustCompile(`第\s*(\d{1,4})\s*[集话話期]`)
	reEP        = regexp.MustCompile(`(?:^|[\s\._\-\[])(?:EP|E)\.?\s*(\d{1,4})(?:[\s\.\_\-\]]|$)`)
	reBracketEp = regexp.MustCompile(`[\[【]\s*(\d{1,4}(?:\.\d)?(?:v\d)?)\s*[\]】]`)
	reDashEp    = regexp.MustCompile(`(?:^|[\s_])-[\s_]*(\d{1,4})(?:[\s_\-\[]|$)`)
	reBracketGroups = regexp.MustCompile(`[\[【]([^\]】]*)[\]】]`)
)

var qualityTokens = regexp.MustCompile(`(?i)\b(2160p|1080p|1080i|720p|576p|480p|4k|uhd|hdr10?|hdr10\+|dv|dovi|web[\.\-_ ]?(dl|rip)|brrip|blu[\.\-_ ]?ray|bdrip|remux|hdtv|dvdrip|hddvd|hevc|h[\.\-_ ]?26[45]|x26[45]|avc|10bit|8bit|60fps|30fps|23\.976|25fps|atmos|ddp?[\.\-_ ]?5\.1|eac3|ac3|dts[\.\-_ ]?(hd|ma|x)?|truehd|flac|aac|mp3|opus|chs|cht|chscj|big5|gb|mandarin|cantonese|chinese|english|japanese|dual|multi|sub|chs&eng|hsbs|sbs|3d|proper|repack|extended|uncut|remastered|unrated|internal|limited|imax|complete|hybrid|yify|rarbg|xvid|diy|mytvsuper|tvb|now|hbo|disney\+|netflix|nf|amzn|atvp|dsnp|hulu|max|pcm|truestem|web|dl|rip|bluray|uhdbluray)\b`)

var cjkTokens = regexp.MustCompile(`(国语|普通话|粤语|中字|简体|繁体|简繁|内封|外挂|内嵌|双语|双音轨|无字幕|杜比视界|杜比全景声|全景声|高清|超清|蓝光|原盘|修复版|完整版|抢先版|枪版|官方中字|中英双字|去水印|高码|高码率|低码|片源|压制|收录|合集|无水印|4K|2160P|1080P|720P)`)

// ParseName identifies a media filename.
func ParseName(filename string) Parsed {
	base := filename
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[:i]
	}
	original := base

	p := Parsed{}

	// anime style: [Group][Show][01][tags]
	if strings.HasPrefix(strings.TrimSpace(base), "[") || strings.HasPrefix(strings.TrimSpace(base), "【") {
		groups := reBracketGroups.FindAllStringSubmatch(base, -1)
		rest := reBracketGroups.ReplaceAllString(base, " ")
		epFound := false
		for _, g := range groups {
			val := strings.TrimSpace(g[1])
			if epRe := regexp.MustCompile(`^\d{1,4}(?:\.\d)?(?:v\d)?$`); epRe.MatchString(val) {
				if n, err := strconv.ParseFloat(strings.TrimSuffix(val, "v2"), 64); err == nil && n <= 9999 {
					p.Episode = int(n)
					p.IsEpisode = true
					p.Kind = "episode"
					epFound = true
				}
			}
		}
		if !epFound {
			if m := reSE.FindStringSubmatch(rest); m != nil {
				applySE(&p, m)
			} else if m := reCJKep.FindStringSubmatch(rest); m != nil {
				applyNum(&p, m[1])
			}
		}
		title := cleanTitle(rest)
		if p.IsEpisode {
			p.Title = title
		} else {
			title, year := extractYear(title)
			p.Title = title
			p.Year = year
			p.HasYear = year > 0
			p.Kind = "movie"
		}
		if p.Title == "" {
			p.Title = cleanTitle(original)
		}
		return p
	}

	// S01E02
	if m := reSE.FindStringSubmatch(base); m != nil {
		applySE(&p, m)
		loc := reSE.FindStringIndex(base)
		show := stripTitleYear(base[:loc[0]])
		if show == "" {
			show = stripTitleYear(base[loc[1]:])
		}
		p.Title = show
		return p
	}

	// 第xx集
	if m := reCJKep.FindStringSubmatch(base); m != nil {
		applyNum(&p, m[1])
		loc := reCJKep.FindStringIndex(base)
		left := stripTitleYear(base[:loc[0]])
		right := stripTitleYear(base[loc[1]:])
		p.Title = pickLonger(left, right)
		return p
	}

	// EP01 / E01
	if m := reEP.FindStringSubmatch(base); m != nil {
		applyNum(&p, m[1])
		loc := reEP.FindStringIndex(base)
		show := stripTitleYear(base[:loc[0]])
		if show == "" {
			show = stripTitleYear(base[loc[1]:])
		}
		p.Title = show
		return p
	}

	// trailing bracketed number [01]
	locs := reBracketEp.FindAllStringSubmatchIndex(base, -1)
	if len(locs) > 0 {
		m := locs[len(locs)-1]
		val := base[m[2]:m[3]]
		if n, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(val, "v2"), "v3"), 64); err == nil && n <= 9999 {
			p.Episode = int(n)
			p.IsEpisode = true
			p.Kind = "episode"
			p.Title = cleanTitle(base[:m[0]])
			if p.Title == "" {
				p.Title = cleanTitle(original)
			}
			return p
		}
	}

	// "Show - 01" pattern
	if m := reDashEp.FindStringSubmatch(base); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n <= 999 {
			p.Episode = n
			p.IsEpisode = true
			p.Kind = "episode"
			loc := reDashEp.FindStringIndex(base)
			p.Title = cleanTitle(base[:loc[0]])
			if p.Title == "" {
				p.Title = cleanTitle(original)
			}
			return p
		}
	}

	// movie
	title, year := extractYear(original)
	if year > 0 {
		p.HasYear = true
		p.Year = year
	}
	p.Title = title
	if p.Title == "" {
		p.Title = cleanTitle(original)
	}
	p.Kind = "movie"
	return p
}

func applySE(p *Parsed, m []string) {
	s, _ := strconv.Atoi(m[1])
	e, _ := strconv.Atoi(m[2])
	p.Season = s
	p.Episode = e
	p.IsEpisode = true
	p.Kind = "episode"
}

func applyNum(p *Parsed, s string) {
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(s, "v2"), "v3"), 64)
	if err == nil {
		p.Episode = int(f)
	}
	p.IsEpisode = true
	p.Kind = "episode"
	if p.Season == 0 {
		p.Season = 1
	}
}

func pickLonger(a, b string) string {
	if len([]rune(a)) >= len([]rune(b)) {
		return a
	}
	return b
}

// ParseSeasonDir detects "Season 01" / "S01" / "第一季" folder names.
func ParseSeasonDir(name string) (int, bool) {
	m := reSeasonOnly.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return 0, false
	}
	for _, g := range m[1:] {
		if g != "" {
			n, _ := strconv.Atoi(g)
			return n, true
		}
	}
	return 0, false
}

// extractYear finds the last standalone year in the string, returns cleaned title + year.
func extractYear(s string) (string, int) {
	locs := reYear.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return cleanTitle(s), 0
	}
	l := locs[len(locs)-1]
	year, _ := strconv.Atoi(s[l[2]:l[3]])
	return cleanTitle(s[:l[2]]), year
}

// stripTitleYear removes a trailing standalone year from a show title.
func stripTitleYear(s string) string {
	t, _ := extractYear(s)
	return t
}

var punctReplacer = strings.NewReplacer(".", " ", "_", " ", "-", " ")

func cleanTitle(s string) string {
	s = punctReplacer.Replace(s)
	s = qualityTokens.ReplaceAllString(s, " ")
	s = cjkTokens.ReplaceAllString(s, " ")
	s = reBracketGroups.ReplaceAllString(s, " ")
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '-' || r == '(' || r == ')' || r == '[' || r == ']' || r == '【' || r == '】' || r == '~' || r == '!' || r == ','
	}), " ")
	return strings.TrimSpace(s)
}

// NormalizeKey builds a match key for dedupe.
func NormalizeKey(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
